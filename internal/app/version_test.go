package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectXrayVersionFromInstalledBinary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = version ] || exit 1\necho 'Xray 26.9.9 (Xray, Penetrates Everything.) build'\necho 'A unified platform for anti-censorship.'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	version, err := DetectXrayVersion(context.Background(), binary)
	if err != nil || version != "26.9.9" {
		t.Fatalf("version=%q error=%v", version, err)
	}
	m, _, _, _ := testManager(t)
	m.SetCoreVersion(version)
	if m.Snapshot().CoreVersion != "26.9.9" {
		t.Fatal("core version missing from panel API")
	}
}
func TestDetectXrayVersionRejectsUnexpectedOutput(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "xray")
	_ = os.WriteFile(binary, []byte("#!/bin/sh\necho 'untrusted output'\n"), 0700)
	if _, err := DetectXrayVersion(context.Background(), binary); err == nil {
		t.Fatal("invalid version accepted")
	}
}
