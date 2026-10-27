package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Prober checks the complete VPN path, not just the server's TCP port.
type Prober interface {
	Probe(context.Context, Node) (time.Duration, error)
}
type ProbeResult struct {
	Milliseconds int64     `json:"milliseconds"`
	Reachable    bool      `json:"reachable"`
	CheckedAt    time.Time `json:"checkedAt"`
}
type XrayProber struct {
	Binary, Dir, URL string
	Network          Runtime
	mu               sync.Mutex
}

func RenderProbeConfig(n Node, ip, iface string, port int) ([]byte, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("invalid probe port")
	}
	data, err := RenderConfig(n, ip, iface)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg["log"] = map[string]any{"loglevel": "none"}
	cfg["inbounds"] = []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}
	return json.Marshal(cfg)
}
func (p *XrayProber) Probe(parent context.Context, n Node) (time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	target := p.URL
	if target == "" {
		target = "https://www.gstatic.com/generate_204"
	}
	if ValidateURL(target) != nil {
		return 0, errors.New("PROBE_URL must be an HTTPS URL")
	}
	ip, err := p.Network.Resolve(ctx, n.Server)
	if err != nil {
		return 0, errors.New("server unavailable")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, errors.New("could not prepare server probe")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	data, err := RenderProbeConfig(n, ip, p.Network.Interface(), port)
	if err != nil {
		listener.Close()
		return 0, err
	}
	dir, err := os.MkdirTemp(p.Dir, ".probe-")
	if err != nil {
		listener.Close()
		return 0, errors.New("could not prepare server probe")
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		listener.Close()
		return 0, errors.New("could not prepare server probe")
	}
	// Release the reserved ephemeral port immediately before launching Xray.
	listener.Close()
	cmd := exec.CommandContext(ctx, p.Binary, "run", "-config", path)
	if cmd.Start() != nil {
		return 0, errors.New("could not start server probe")
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill(); <-done }()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-done:
			return 0, errors.New("probe process exited")
		case <-ticker.C:
			connection, dialErr := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(ctx, "tcp4", address)
			if dialErr == nil {
				connection.Close()
				return proxyDelay(ctx, address, target)
			}
		}
	}
}
func proxyDelay(ctx context.Context, address, target string) (time.Duration, error) {
	proxy := &url.URL{Scheme: "socks5", Host: address}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, errors.New("invalid probe URL")
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0, errors.New("server unavailable or probe timed out")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, errors.New("test site returned an unsuccessful response")
	}
	// Read only a bounded small response; never run a bandwidth download.
	if _, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return 0, errors.New("could not receive response")
	}
	return time.Since(started), nil
}
