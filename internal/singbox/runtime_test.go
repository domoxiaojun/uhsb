package singbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreePort(t *testing.T) {
	port, err := freePort("127.0.0.1")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port = %d", port)
	}
}

func TestStartReportsStderrOnFailure(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakesb")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'config invalid: boom' >&2\nexit 2\n"), 0755); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(Config{Binary: bin, WorkDir: dir})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	_, err = mgr.Start(context.Background(), Node{Server: "1.1.1.1", UUID: "u", Host: "h"})
	if err == nil {
		t.Fatal("expected start error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should surface sing-box stderr, got %v", err)
	}
}
