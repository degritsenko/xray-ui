package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoppedRefreshKeepsAcceptedConfiguration(t *testing.T) {
	m, network, _, id := testManager(t)
	ctx := context.Background()
	_ = m.Refresh(ctx, id)
	n := m.stateCopy().Subscriptions[0].Nodes[0]
	if err := m.Activate(ctx, id, n.ID); err != nil {
		t.Fatal(err)
	}
	_ = m.Control(ctx, "stop")
	old := m.stateCopy().Active
	network.body = strings.Replace(sampleURI, "fp=chrome", "fp=invalid-browser", 1)
	_ = m.Refresh(ctx, id)
	accepted := m.stateCopy().Active
	if accepted.Node.ID != old.Node.ID || string(accepted.Config) != string(old.Config) {
		t.Fatal("stopped refresh overwrote accepted config without validation")
	}
}
func TestBootstrapSelectorSurvivesActivationFailure(t *testing.T) {
	store := NewStore(t.TempDir())
	env := map[string]string{"SUBSCRIPTION_URL": "https://example.com/sub", "SERVER_NAME": "Germany 🇩🇪"}
	state, err := store.LoadOrImport(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	core := &testCore{failure: true}
	m := NewManager(store, state, &testRuntime{body: sampleURI}, core, NewLogBuffer(nil))
	id := state.Subscriptions[0].ID
	if m.Bootstrap(context.Background()) == nil {
		t.Fatal("expected first activation failure")
	}
	if m.stateCopy().Subscriptions[0].BootstrapName == "" {
		t.Fatal("bootstrap selector discarded on failure")
	}
	core.failure = false
	if err = m.Refresh(context.Background(), id); err != nil || !core.Running() {
		t.Fatal("selection did not recover", err)
	}
	if m.stateCopy().Subscriptions[0].BootstrapName != "" {
		t.Fatal("accepted bootstrap was not cleared")
	}
}
func TestLegacyMigrationRejectsCatalogFromAnotherSource(t *testing.T) {
	dir := t.TempDir()
	nodes, _, _ := ParseSubscription([]byte(sampleURI))
	data, _ := json.Marshal(nodes)
	_ = os.WriteFile(filepath.Join(dir, "servers.json"), data, 0600)
	_ = atomicJSON(filepath.Join(dir, "last-good.json"), map[string]any{"source": sourceHash("https://old.example/sub"), "selector": map[string]string{"name": nodes[0].Name, "id": ""}, "node": nodes[0]})
	config, _ := RenderConfig(nodes[0], "192.0.2.1", "eth0")
	_ = os.WriteFile(filepath.Join(dir, "config.json"), config, 0600)
	state, err := NewStore(dir).LoadOrImport(func(k string) string {
		if k == "SUBSCRIPTION_URL" {
			return "https://new.example/sub"
		}
		if k == "SERVER_NAME" {
			return nodes[0].Name
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Subscriptions[0].Nodes) != 0 || state.Active != nil {
		t.Fatal("old source catalog imported under new URL")
	}
}
func TestStoreRejectsStateLargerThanLoadLimit(t *testing.T) {
	store := NewStore(t.TempDir())
	original := State{Version: 1, Subscriptions: []Subscription{{ID: "accepted", Name: "Original"}}}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	large := State{Version: 1, Subscriptions: []Subscription{{ID: "large", Nodes: []Node{{Name: strings.Repeat("x", 16*1024*1024)}}}}}
	if err := store.Save(large); err == nil {
		t.Fatal("state beyond read limit saved successfully")
	}
	got, err := store.Load()
	if err != nil || got.Subscriptions[0].ID != "accepted" {
		t.Fatal("oversized state replaced accepted document", err)
	}
}
func TestRollbackReusesAcceptedFileWhenStorageUnavailable(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "core")
	_ = os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$2\" = -test ]; then exit 0; fi\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0700)
	r := NewRunner(script, dir, io.Discard, 30*time.Millisecond)
	defer r.Stop()
	ctx := context.Background()
	if err := r.Apply(ctx, []byte(`{"ok":1}`), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "unavailable")
	_ = os.WriteFile(blocked, []byte("not a directory"), 0600)
	err := r.Apply(ctx, []byte(`{"ok":2}`), func() error { r.dir = blocked; return errors.New("storage unavailable") })
	if err == nil || !r.Running() {
		t.Fatal("rollback required fresh writes and lost accepted process")
	}
}
