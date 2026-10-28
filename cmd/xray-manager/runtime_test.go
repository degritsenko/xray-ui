package main

import (
	"os"
	"testing"
)

func TestRuntimeStartsFreshAndIgnoresPersistentDirectory(t *testing.T) {
	oldDir := t.TempDir()
	env := func(key string) string {
		switch key {
		case "STATE_DIR":
			return oldDir
		case "SUBSCRIPTION_URL":
			return "https://provider.example/sub/test"
		}
		return ""
	}
	first, err := newRuntime(env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(first.dir)
	state := first.state
	state.Subscriptions[0].Name = "Changed during previous run"
	if err := first.store.Save(state); err != nil {
		t.Fatal(err)
	}
	second, err := newRuntime(env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(second.dir)
	if first.dir == second.dir || first.dir == oldDir || second.dir == oldDir {
		t.Fatal("runtime reused persistent storage")
	}
	if first.generated == "" || second.generated == "" || first.generated == second.generated {
		t.Fatal("generated password reused across starts")
	}
	if second.state.Subscriptions[0].Name != "Subscription 1" || second.state.Active != nil {
		t.Fatal("previous state restored")
	}
	entries, err := os.ReadDir(oldDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("STATE_DIR was used")
	}
}

func TestRuntimeUsesConfiguredPasswordWithoutGeneratingOne(t *testing.T) {
	runtime, err := newRuntime(func(key string) string {
		if key == "PANEL_PASSWORD" {
			return "test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime.dir)
	if runtime.generated != "" {
		t.Fatal("configured password replaced")
	}
}

func TestFailedStartupRemovesTemporaryRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	if _, err := newRuntime(func(key string) string {
		if key == "PANEL_PASSWORD" {
			return "abc"
		}
		return ""
	}); err == nil {
		t.Fatal("invalid password accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed startup left runtime files behind")
	}
}
