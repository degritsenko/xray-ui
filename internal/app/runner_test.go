package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerTransactionalRollback(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "core")
	text := "#!/bin/sh\nif [ \"$2\" = -test ]; then exit 0; fi\nif grep -q fail-run \"$3\"; then exit 2; fi\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(script, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(script, dir, io.Discard, 60*time.Millisecond)
	defer r.Stop()
	ctx := context.Background()
	if err := r.Apply(ctx, []byte(`{"ok":1}`), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	pid := r.pid()
	if err := r.Apply(ctx, []byte(`{"ok":1}`), func() error { return nil }); err != nil || r.pid() != pid {
		t.Fatal("unchanged config restarted core", err)
	}
	if err := r.Apply(ctx, []byte(`{"fail-run":1}`), func() error { return nil }); err == nil || !r.Running() {
		t.Fatal("failed launch did not roll back")
	}
	if err := r.Apply(ctx, []byte(`{"ok":2}`), func() error { return errors.New("disk full") }); err == nil || !r.Running() {
		t.Fatal("failed persistence lost active process")
	}
	r.Stop()
	if r.Running() {
		t.Fatal("stop left running process")
	}
}
func TestRunnerColdFailureNeverUsesOtherCache(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "core")
	_ = os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$2\" = -test ]; then exit 0; fi\nexit 2\n"), 0700)
	r := NewRunner(script, dir, io.Discard, 30*time.Millisecond)
	defer r.Stop()
	if err := r.Apply(context.Background(), []byte(`{}`), func() error { return nil }); err == nil || r.Running() {
		t.Fatal("cold failed launch accepted")
	}
}
func TestRunnerStatusRemainsResponsiveDuringValidation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "core")
	text := "#!/bin/sh\nif [ \"$2\" = -test ]; then if grep -q slow \"$4\"; then sleep 1; fi; exit 0; fi\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"
	_ = os.WriteFile(script, []byte(text), 0700)
	r := NewRunner(script, dir, io.Discard, 30*time.Millisecond)
	defer r.Stop()
	if err := r.Apply(context.Background(), []byte(`{"ok":1}`), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Apply(context.Background(), []byte(`{"slow":1}`), func() error { return nil }) }()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	_ = r.Running()
	if time.Since(start) > 100*time.Millisecond {
		t.Error("status blocked behind configuration validation")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRestartValidatesBeforeStoppingActiveCore(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "core")
	_ = os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$2\" = -test ]; then if grep -q invalid \"$4\"; then exit 2; fi; exit 0; fi\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0700)
	r := NewRunner(script, dir, io.Discard, 30*time.Millisecond)
	defer r.Stop()
	ctx := context.Background()
	if err := r.Apply(ctx, []byte(`{"ok":1}`), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	pid := r.pid()
	if err := r.Restart(ctx, []byte(`{"invalid":1}`), func() error { return nil }); err == nil || r.pid() != pid {
		t.Fatal("invalid restart interrupted active core")
	}
}
