package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTripPrivateAndAtomic(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	s := State{Version: 1, Subscriptions: []Subscription{{ID: "one", Name: "Home", URL: "https://example.com/secret", IntervalSeconds: 43200}}}
	if err := store.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got.Subscriptions[0].URL != s.Subscriptions[0].URL {
		t.Fatal("roundtrip", err)
	}
	stat, _ := os.Stat(filepath.Join(dir, "manager.json"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("state is not private")
	}
	// A blocked rename must leave the previous accepted document intact.
	if err = os.Mkdir(filepath.Join(dir, "manager.json.block"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(dir, "manager.json"), filepath.Join(dir, "saved")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "manager.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = store.Save(State{Version: 1}); err == nil {
		t.Fatal("save over directory succeeded")
	}
	old, err := os.ReadFile(filepath.Join(dir, "saved"))
	if err != nil || len(old) == 0 {
		t.Fatal("old document lost")
	}
}
func TestEnvironmentAuthoritativeOnEveryBoot(t *testing.T) {
	store := NewStore(t.TempDir())
	env := map[string]string{"SUBSCRIPTION_URL": "https://example.com/a", "SERVER_NAME": "Germany 🇩🇪"}
	s, err := store.LoadOrImport(func(k string) string { return env[k] })
	if err != nil || len(s.Subscriptions) != 1 || s.Subscriptions[0].BootstrapName != "Germany 🇩🇪" {
		t.Fatal("bootstrap failed", err)
	}
	s.Subscriptions[0].URL = "https://example.com/edited"
	if err = store.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadOrImport(func(k string) string { return env[k] })
	if err != nil || got.Subscriptions[0].URL != "https://example.com/a" {
		t.Fatal("saved sources overrode environment", err)
	}
}
func TestEnvironmentSourceReplacementAndRemoval(t *testing.T) {
	store := NewStore(t.TempDir())
	env := map[string]string{"SUBSCRIPTION_URL": "https://example.com/one", "SUBSCRIPTION_NAME": "First"}
	lookup := func(k string) string { return env[k] }
	state, err := store.LoadOrImport(lookup)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _, _ := ParseSubscription([]byte(sampleURI))
	state.Subscriptions[0].Nodes = nodes
	state.Active = &Selection{SubscriptionID: state.Subscriptions[0].ID, SourceHash: sourceHash(env["SUBSCRIPTION_URL"]), Node: nodes[0], Enabled: false}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	env["SUBSCRIPTION_NAME"] = "Renamed"
	same, err := store.LoadOrImport(lookup)
	if err != nil || same.Active == nil || same.Active.Enabled || len(same.Subscriptions[0].Nodes) != 1 {
		t.Fatal("same-source choice/stop not preserved", err)
	}
	env["SUBSCRIPTION_URL"] = "https://example.com/two"
	changed, err := store.LoadOrImport(lookup)
	if err != nil || changed.Active != nil || len(changed.Subscriptions[0].Nodes) != 0 {
		t.Fatal("new source inherited old nodes/selection", err)
	}
	delete(env, "SUBSCRIPTION_URL")
	empty, err := store.LoadOrImport(lookup)
	if err != nil || len(empty.Subscriptions) != 0 || empty.Active != nil {
		t.Fatal("removed source returned from disk", err)
	}
}
func TestMultipleEnvironmentSources(t *testing.T) {
	env := map[string]string{"SUBSCRIPTION_URL": "https://one.example/sub", "SUBSCRIPTION_URL_2": "https://two.example/sub", "SUBSCRIPTION_NAME_2": "Second", "SUBSCRIPTION_REFRESH_SECONDS": "0"}
	state, err := NewStore(t.TempDir()).LoadOrImport(func(k string) string { return env[k] })
	if err != nil || len(state.Subscriptions) != 2 || state.Subscriptions[1].Name != "Second" || state.Subscriptions[1].IntervalSeconds != 0 {
		t.Fatal("multiple env sources failed", err)
	}
}
func TestVerifiedLegacySymlinkMigration(t *testing.T) {
	dir := t.TempDir()
	nodes, _, _ := ParseSubscription([]byte(sampleURI))
	snapshot := filepath.Join(dir, ".snapshot.old")
	if err := os.Mkdir(snapshot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(snapshot, "last-good.json"), map[string]any{"source": sourceHash("https://example.com/sub"), "selector": map[string]string{"name": nodes[0].Name, "id": ""}, "node": nodes[0]}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := RenderConfig(nodes[0], "192.0.2.1", "eth0")
	_ = os.WriteFile(filepath.Join(snapshot, "config.json"), cfg, 0600)
	for _, file := range []string{"last-good.json", "config.json"} {
		if err := os.Symlink(filepath.Join(snapshot, file), filepath.Join(dir, file)); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"SUBSCRIPTION_URL": "https://example.com/sub", "SERVER_NAME": nodes[0].Name}
	s, err := NewStore(dir).LoadOrImport(func(k string) string { return env[k] })
	if err != nil || s.Active == nil || s.Active.Node.ID != nodes[0].ID || len(s.Subscriptions[0].Nodes) != 1 {
		t.Fatal("matching legacy acceptance was lost", err)
	}
}

func TestDefaultSubscriptionNamesAreNumbered(t *testing.T) {
	store := NewStore(t.TempDir())
	env := map[string]string{"SUBSCRIPTION_URL": "https://example.com/one", "SUBSCRIPTION_URL_2": "https://example.com/two"}
	state, err := store.LoadOrImport(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if state.Subscriptions[0].Name != "Subscription 1" || state.Subscriptions[1].Name != "Subscription 2" {
		t.Fatalf("unexpected source names: %q, %q", state.Subscriptions[0].Name, state.Subscriptions[1].Name)
	}
}
