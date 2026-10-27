package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProbeConfigHasOnlyLoopbackSOCKS(t *testing.T) {
	nodes, _, err := ParseSubscription([]byte(sampleURI))
	if err != nil {
		t.Fatal(err)
	}
	data, err := RenderProbeConfig(nodes[0], "192.0.2.1", "eth0", 12345)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds []struct {
			Listen   string
			Port     int
			Protocol string
		}
		Outbounds []json.RawMessage
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inbounds) != 1 || cfg.Inbounds[0].Listen != "127.0.0.1" || cfg.Inbounds[0].Port != 12345 || cfg.Inbounds[0].Protocol != "socks" {
		t.Fatalf("unsafe probe inbound: %s", data)
	}
	if len(cfg.Outbounds) != 1 {
		t.Fatal("probe must use VPN outbound only")
	}
}

type fakeProber struct {
	failed map[string]bool
	calls  []string
}

func (p *fakeProber) Probe(ctx context.Context, n Node) (time.Duration, error) {
	p.calls = append(p.calls, n.ID)
	if p.failed[n.ID] {
		return 0, context.DeadlineExceeded
	}
	return 42 * time.Millisecond, nil
}
func TestStartupChoosesFirstWorkingServer(t *testing.T) {
	m, r, c, _ := testManager(t)
	r.body = sampleURI + "\n" + stringsReplaceServer(sampleURI)
	nodes, _, _ := ParseSubscription([]byte(r.body))
	p := &fakeProber{failed: map[string]bool{nodes[0].ID: true}}
	m.SetProber(p)
	if err := m.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.Running() || m.stateCopy().Active.Node.ID != nodes[1].ID {
		t.Fatal("did not choose first reachable VPN")
	}
	if len(p.calls) != 2 {
		t.Fatalf("repeated probes: %v", p.calls)
	}
}
func stringsReplaceServer(s string) string {
	return strings.ReplaceAll(s, "example.com", "second.example.com")
}
func TestStartupDoesNotConnectUnavailableServers(t *testing.T) {
	m, _, c, _ := testManager(t)
	nodes, _, _ := ParseSubscription([]byte(sampleURI))
	m.SetProber(&fakeProber{failed: map[string]bool{nodes[0].ID: true}})
	if err := m.Bootstrap(context.Background()); err == nil {
		t.Fatal("unavailable servers reported success")
	}
	if c.Running() || m.stateCopy().Active != nil {
		t.Fatal("unreachable VPN activated")
	}
}

func TestStartupPrefersSavedServerAndChecksRemaining(t *testing.T) {
	m, r, _, id := testManager(t)
	ctx := context.Background()
	r.body = sampleURI + "\n" + stringsReplaceServer(sampleURI)
	if err := m.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	nodes := m.stateCopy().Subscriptions[0].Nodes
	if err := m.Activate(ctx, id, nodes[1].ID); err != nil {
		t.Fatal(err)
	}
	p := &fakeProber{}
	m.SetProber(p)
	if err := m.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 2 || p.calls[0] != nodes[1].ID || m.stateCopy().Active.Node.ID != nodes[1].ID {
		t.Fatal("saved preference lost or background check switched node")
	}
	if m.Snapshot().Subscriptions[0].Nodes[0].Probe.Milliseconds != 42 {
		t.Fatal("latency missing from safe snapshot")
	}
}
func TestProbeDoesNotRestartStoppedVPN(t *testing.T) {
	m, _, c, id := testManager(t)
	ctx := context.Background()
	_ = m.Refresh(ctx, id)
	_ = m.Activate(ctx, id, m.stateCopy().Subscriptions[0].Nodes[0].ID)
	_ = m.Control(ctx, "stop")
	m.SetProber(&fakeProber{})
	if err := m.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Running() {
		t.Fatal("probe restarted stopped VPN")
	}
}

func TestProxyDelayUsesSOCKSAndCancels(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	used := make(chan struct{}, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		header := make([]byte, 3)
		if _, e = io.ReadFull(c, header); e != nil {
			return
		}
		c.Write([]byte{5, 0})
		request := make([]byte, 4)
		if _, e = io.ReadFull(c, request); e != nil {
			return
		}
		var host string
		switch request[3] {
		case 1:
			b := make([]byte, 4)
			io.ReadFull(c, b)
			host = net.IP(b).String()
		case 3:
			b := make([]byte, 1)
			io.ReadFull(c, b)
			name := make([]byte, int(b[0]))
			io.ReadFull(c, name)
			host = string(name)
		default:
			return
		}
		port := make([]byte, 2)
		io.ReadFull(c, port)
		remote, e := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port[0])*256+int(port[1]))))
		if e != nil {
			return
		}
		defer remote.Close()
		c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		used <- struct{}{}
		go io.Copy(remote, c)
		io.Copy(c, remote)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = proxyDelay(ctx, listener.Addr().String(), target.URL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-used:
	default:
		t.Fatal("test bypassed SOCKS")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err = proxyDelay(canceled, listener.Addr().String(), target.URL); err == nil {
		t.Fatal("canceled probe succeeded")
	}
}
func TestProbeEndpointRequiresSessionAndCSRF(t *testing.T) {
	server, m := setupHTTP(t)
	m.SetProber(&fakeProber{})
	r := request(t, server.Client(), "POST", server.URL+"/api/probe", "{}", nil, "", server.URL)
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("probe unauthenticated")
	}
	cookie, csrf := login(t, server)
	r = request(t, server.Client(), "POST", server.URL+"/api/probe", "{}", cookie, "", server.URL)
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("probe missing CSRF")
	}
	r = request(t, server.Client(), "POST", server.URL+"/api/probe", "{}", cookie, csrf, server.URL)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("probe failed", r.StatusCode)
	}
}

type blockingProber struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingProber) Probe(ctx context.Context, n Node) (time.Duration, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-p.release:
		return time.Millisecond, nil
	}
}
func TestManualSelectionWinsDuringStartupProbe(t *testing.T) {
	m, r, _, id := testManager(t)
	r.body = sampleURI + "\n" + stringsReplaceServer(sampleURI)
	p := &blockingProber{entered: make(chan struct{}), release: make(chan struct{})}
	m.SetProber(p)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Bootstrap(ctx) }()
	<-p.entered
	nodes := m.stateCopy().Subscriptions[0].Nodes
	selected := make(chan error, 1)
	go func() { selected <- m.Activate(ctx, id, nodes[1].ID) }()
	select {
	case err := <-selected:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(p.release)
		t.Fatal("startup probe blocked manual activation")
	}
	close(p.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m.stateCopy().Active.Node.ID != nodes[1].ID {
		t.Fatal("startup overrode manual selection")
	}
}

type restoreOrderRuntime struct {
	*testRuntime
	core     *testCore
	restored bool
}

func (r *restoreOrderRuntime) Download(ctx context.Context, u, ua string) ([]byte, error) {
	r.restored = r.core.Running()
	return r.testRuntime.Download(ctx, u, ua)
}
func TestSavedVPNRestoredBeforeSubscriptionDownload(t *testing.T) {
	m, r, c, id := testManager(t)
	ctx := context.Background()
	_ = m.Refresh(ctx, id)
	_ = m.Activate(ctx, id, m.stateCopy().Subscriptions[0].Nodes[0].ID)
	c.Stop()
	tracked := &restoreOrderRuntime{testRuntime: r, core: c}
	m.network = tracked
	m.SetProber(&fakeProber{})
	if err := m.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if !tracked.restored {
		t.Fatal("subscription download delayed restoration of saved VPN")
	}
}

func TestStartupDoesNotActivateNodeRemovedDuringProbe(t *testing.T) {
	m, r, c, id := testManager(t)
	p := &blockingProber{entered: make(chan struct{}), release: make(chan struct{})}
	m.SetProber(p)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Bootstrap(ctx) }()
	<-p.entered
	r.body = stringsReplaceServer(sampleURI)
	if err := m.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	close(p.release)
	if err := <-done; err == nil {
		t.Fatal("removed candidate reported connected")
	}
	if c.Running() || m.stateCopy().Active != nil {
		t.Fatal("removed candidate activated")
	}
}
func TestManualActionDuringOnlyProbeDoesNotReportFailure(t *testing.T) {
	m, _, _, id := testManager(t)
	p := &blockingProber{entered: make(chan struct{}), release: make(chan struct{})}
	m.SetProber(p)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Bootstrap(ctx) }()
	<-p.entered
	if err := m.Activate(ctx, id, m.stateCopy().Subscriptions[0].Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	close(p.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().LastError != "" {
		t.Fatal("manual success reported as startup failure")
	}
}
