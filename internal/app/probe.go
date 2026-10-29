package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Prober checks the complete VPN path, not just the server's TCP port.
type Prober interface {
	Probe(context.Context, Node) (time.Duration, error)
}
type ProbeSuccess struct {
	Milliseconds int64     `json:"milliseconds"`
	CheckedAt    time.Time `json:"checkedAt"`
}
type ProbeResult struct {
	History      []bool        `json:"history,omitempty"`
	Error        string        `json:"error,omitempty"`
	LastSuccess  *ProbeSuccess `json:"lastSuccess,omitempty"`
	Milliseconds int64         `json:"milliseconds"`
	Reachable    bool          `json:"reachable"`
	CheckedAt    time.Time     `json:"checkedAt"`
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
	return retryProbe(parent, func(ctx context.Context) (time.Duration, error) { return p.probeOnce(ctx, n) })
}
func (p *XrayProber) probeOnce(ctx context.Context, n Node) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	target := p.URL
	if target == "" {
		target = "https://www.gstatic.com/generate_204"
	}
	if ValidateURL(target) != nil {
		return 0, &probeFailure{message: "PROBE_URL must be an HTTPS URL"}
	}
	resolveCtx, cancelResolve := context.WithTimeout(ctx, probeDNSTimeout)
	ip, err := p.Network.Resolve(resolveCtx, n.Server)
	resolveTimeout := errors.Is(resolveCtx.Err(), context.DeadlineExceeded)
	cancelResolve()
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, dnsProbeFailure(err, resolveTimeout)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, &probeFailure{message: "Could not prepare check process"}
	}
	port := listener.Addr().(*net.TCPAddr).Port
	data, err := RenderProbeConfig(n, ip, p.Network.Interface(), port)
	if err != nil {
		listener.Close()
		return 0, &probeFailure{message: "Invalid server configuration"}
	}
	dir, err := os.MkdirTemp(p.Dir, ".probe-")
	if err != nil {
		listener.Close()
		return 0, &probeFailure{message: "Could not prepare check process"}
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		listener.Close()
		return 0, &probeFailure{message: "Could not prepare check process"}
	}
	// Release the reserved ephemeral port immediately before launching Xray.
	listener.Close()
	cmd := exec.CommandContext(ctx, p.Binary, "run", "-config", path)
	if err := cmd.Start(); err != nil {
		return 0, &probeFailure{message: "Could not start check process", retryable: !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission)}
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill(); <-done }()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	startupCtx, cancelStartup := context.WithTimeout(ctx, probeStartupTimeout)
	defer cancelStartup()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-startupCtx.Done():
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, &probeFailure{message: "Check process startup timed out", retryable: true}
		case <-done:
			return 0, &probeFailure{message: "Check process exited", retryable: true}
		case <-ticker.C:
			connection, dialErr := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(ctx, "tcp4", address)
			if dialErr == nil {
				connection.Close()
				cancelStartup()
				requestCtx, cancelRequest := context.WithTimeout(ctx, probeRequestTimeout)
				defer cancelRequest()
				return proxyDelay(requestCtx, address, target)
			}
		}
	}
}
func proxyDelay(ctx context.Context, address, target string) (time.Duration, error) {
	proxy := &url.URL{Scheme: "socks5", Host: address}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, TLSHandshakeTimeout: probeTLSTimeout}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, &probeFailure{message: "Invalid probe URL"}
	}
	var phase atomic.Int32
	trace := &httptrace.ClientTrace{TLSHandshakeStart: func() { phase.Store(1) }, TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
		if err == nil {
			phase.Store(2)
		}
	}, WroteRequest: func(httptrace.WroteRequestInfo) { phase.Store(2) }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0, proxyFailure(err, phase.Load())
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, httpProbeFailure(response.StatusCode)
	}
	// Read only a bounded small response; never run a bandwidth download.
	if _, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return 0, proxyFailure(err, 2)
	}
	return time.Since(started), nil
}
