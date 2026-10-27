package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

type testRuntime struct {
	body string
	err  error
}

func (r *testRuntime) Interface() string                               { return "eth0" }
func (r *testRuntime) Resolve(context.Context, string) (string, error) { return "192.0.2.1", nil }
func (r *testRuntime) Download(context.Context, string, string) ([]byte, error) {
	return []byte(r.body), r.err
}
func (r *testRuntime) Listen(context.Context, string) (net.Listener, error) {
	return nil, errors.New("unused")
}

type testCore struct {
	running bool
	applies int
	failure bool
}

func (c *testCore) Apply(_ context.Context, _ []byte, commit func() error) error {
	if c.failure {
		return errors.New("launch failed")
	}
	if err := commit(); err != nil {
		return err
	}
	c.running = true
	c.applies++
	return nil
}
func (c *testCore) Stop()         { c.running = false }
func (c *testCore) Running() bool { return c.running }
func testManager(t *testing.T) (*Manager, *testRuntime, *testCore, string) {
	t.Helper()
	store := NewStore(t.TempDir())
	state, err := store.LoadOrImport(func(k string) string {
		if k == "SUBSCRIPTION_URL" {
			return "https://example.com/sub"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	network := &testRuntime{body: sampleURI}
	core := &testCore{}
	m := NewManager(store, state, network, core, NewLogBuffer(nil))
	return m, network, core, state.Subscriptions[0].ID
}
func TestManagerExplicitSelectionAndRecovery(t *testing.T) {
	m, r, c, id := testManager(t)
	ctx := context.Background()
	if err := m.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	if c.Running() {
		t.Fatal("refresh implicitly activated first node")
	}
	node := m.stateCopy().Subscriptions[0].Nodes[0]
	if err := m.Activate(ctx, id, node.ID); err != nil {
		t.Fatal(err)
	}
	r.body = "invalid response"
	if err := m.Refresh(ctx, id); err == nil {
		t.Fatal("bad response accepted")
	}
	if !c.Running() || m.stateCopy().Active.Node.ID != node.ID {
		t.Fatal("bad refresh lost active node")
	}
	r.body = strings.Split(sampleURI, "#")[0] + "#Other"
	if err := m.Refresh(ctx, id); err == nil {
		t.Fatal("disappeared selected name not reported")
	}
	if !c.Running() {
		t.Fatal("missing node stopped VPN")
	}
}
func TestStoppedCoreStaysStoppedOnRefreshAndBoot(t *testing.T) {
	m, _, c, id := testManager(t)
	ctx := context.Background()
	_ = m.Refresh(ctx, id)
	n := m.stateCopy().Subscriptions[0].Nodes[0]
	_ = m.Activate(ctx, id, n.ID)
	if err := m.Control(ctx, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	if c.Running() || m.stateCopy().Active.Enabled {
		t.Fatal("refresh restarted explicitly stopped VPN")
	}
	if err := m.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Running() {
		t.Fatal("boot ignored stopped flag")
	}
}
func (c *testCore) Restart(ctx context.Context, data []byte, commit func() error) error {
	return c.Apply(ctx, data, commit)
}
func TestBootstrapLoadsSourcesWithoutSelectingFirstNode(t *testing.T) {
	m, _, core, _ := testManager(t)
	if err := m.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(m.stateCopy().Subscriptions[0].Nodes) != 1 {
		t.Fatal("startup did not load environment source")
	}
	if core.Running() {
		t.Fatal("startup selected first server implicitly")
	}
}

func (r *testRuntime) EnsureTunnel(context.Context) error { return nil }
