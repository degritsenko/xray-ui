package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Runtime interface {
	Interface() string
	EnsureTunnel(context.Context) error
	Resolve(context.Context, string) (string, error)
	Download(context.Context, string, string) ([]byte, error)
	Listen(context.Context, string) (net.Listener, error)
}
type LinuxRuntime struct {
	iface   string
	gateway string
	client  *http.Client
}
type route struct {
	Dev     string `json:"dev"`
	Gateway string `json:"gateway"`
}

func (n *LinuxRuntime) Interface() string { return n.iface }
func runIP(ctx context.Context, args ...string) error {
	if exec.CommandContext(ctx, "ip", args...).Run() != nil {
		return errors.New("could not configure container routing")
	}
	return nil
}
func physicalRoute(ctx context.Context, args ...string) (route, error) {
	data, err := exec.CommandContext(ctx, "ip", args...).Output()
	if err != nil {
		return route{}, err
	}
	var routes []route
	if err = json.Unmarshal(data, &routes); err != nil {
		return route{}, err
	}
	for _, r := range routes {
		if r.Dev != "" && r.Dev != "tun0" {
			return r, nil
		}
	}
	return route{}, errors.New("no physical gateway")
}
func (n *LinuxRuntime) Setup(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("TUN setup is supported only on Linux")
	}
	r, err := physicalRoute(ctx, "-4", "-j", "route", "show", "default")
	if err != nil {
		r, err = physicalRoute(ctx, "-4", "-j", "route", "show", "table", "10000", "default")
	}
	if err != nil {
		return errors.New("physical container gateway not found")
	}
	n.iface, n.gateway = r.Dev, r.Gateway
	routeArgs := func(prefix []string) []string {
		a := append([]string(nil), prefix...)
		if n.gateway != "" {
			a = append(a, "via", n.gateway)
		}
		return append(a, "dev", n.iface)
	}
	if err = runIP(ctx, routeArgs([]string{"-4", "route", "replace", "table", "10000", "default"})...); err != nil {
		return err
	}
	_ = runIP(ctx, "-4", "rule", "del", "pref", "10000", "oif", n.iface, "lookup", "10000")
	if err = runIP(ctx, "-4", "rule", "add", "pref", "10000", "oif", n.iface, "lookup", "10000"); err != nil {
		return err
	}
	resolv, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return errors.New("could not read container DNS configuration")
	}
	for _, line := range strings.Split(string(resolv), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(f[1])
		if ip != nil && ip.To4() != nil {
			if err = runIP(ctx, routeArgs([]string{"-4", "route", "replace", ip.String() + "/32"})...); err != nil {
				return err
			}
		}
	}
	if exec.CommandContext(ctx, "ip", "link", "show", "dev", "tun0").Run() == nil {
		if err = runIP(ctx, "tuntap", "del", "dev", "tun0", "mode", "tun"); err != nil {
			return err
		}
	}
	// Keep a rejecting fallback even if Xray lowers TUN and Linux removes its route.
	if err = runIP(ctx, "-4", "route", "replace", "blackhole", "default", "metric", "32767"); err != nil {
		return err
	}
	return n.EnsureTunnel(ctx)
}
func (n *LinuxRuntime) EnsureTunnel(ctx context.Context) error {
	if exec.CommandContext(ctx, "ip", "link", "show", "dev", "tun0").Run() != nil {
		if err := runIP(ctx, "tuntap", "add", "dev", "tun0", "mode", "tun"); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"addr", "replace", "172.31.200.10/30", "dev", "tun0"}, {"link", "set", "dev", "tun0", "up"}, {"-4", "route", "replace", "default", "dev", "tun0", "metric", "0"}} {
		if err := runIP(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func (n *LinuxRuntime) Resolve(ctx context.Context, host string) (string, error) {
	if n.iface == "" {
		return "", errors.New("physical interface is not configured")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return "", &serverResolveError{cause: err}
	}
	if len(ips) == 0 {
		return "", errors.New("could not resolve server IPv4 address")
	}
	return ips[0].String(), nil
}
func (n *LinuxRuntime) Download(ctx context.Context, source, ua string) ([]byte, error) {
	data, _, err := n.DownloadSubscription(ctx, source, ua)
	return data, err
}
func (n *LinuxRuntime) DownloadSubscription(ctx context.Context, source, ua string) ([]byte, string, error) {
	data, metadata, err := n.DownloadSubscriptionMetadata(ctx, source, ua)
	return data, metadata.Title, err
}
func (n *LinuxRuntime) DownloadSubscriptionMetadata(ctx context.Context, source, ua string) ([]byte, SubscriptionMetadata, error) {
	if err := ValidateURL(source); err != nil {
		return nil, SubscriptionMetadata{}, err
	}
	if n.iface == "" {
		return nil, SubscriptionMetadata{}, errors.New("physical interface is not configured")
	}
	if n.client == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: bindDevice(n.iface)}
		n.client = &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", address)
		}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) > 3 {
				return errors.New("invalid subscription redirect")
			}
			return nil
		}}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, SubscriptionMetadata{}, errors.New("invalid subscription URL")
	}
	req.Header.Set("User-Agent", ua)
	response, err := n.client.Do(req)
	if err != nil {
		return nil, SubscriptionMetadata{}, errors.New("could not download subscription: check network and HTTPS URL")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, SubscriptionMetadata{}, errors.New("subscription server returned an HTTP error")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxSubscriptionBytes+1))
	if err != nil {
		return nil, SubscriptionMetadata{}, errors.New("could not read subscription")
	}
	if len(data) > MaxSubscriptionBytes {
		return nil, SubscriptionMetadata{}, errors.New("subscription exceeds 2 MiB")
	}
	return data, SubscriptionMetadata{Title: subscriptionTitle(response.Header.Get("Profile-Title"), data), Usage: subscriptionUsage(response.Header.Get("Subscription-Userinfo"), data)}, nil
}
func (n *LinuxRuntime) Listen(ctx context.Context, address string) (net.Listener, error) {
	config := net.ListenConfig{Control: bindDevice(n.iface)}
	return config.Listen(ctx, "tcp4", address)
}

// Keep a safe public message while preserving DNS error classification internally.
type serverResolveError struct{ cause error }

func (e *serverResolveError) Error() string { return "could not resolve server IPv4 address" }
func (e *serverResolveError) Unwrap() error { return e.cause }
