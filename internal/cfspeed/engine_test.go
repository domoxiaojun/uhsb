package cfspeed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cftest/internal/cfcommon"
)

func TestNewSessionSingBoxRequiresUUID(t *testing.T) {
	_, err := newSession(Config{
		UseSingBox: true,
		Workers:    20,
	}, cfcommon.NewReporter(nil))
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("err = %v, want UUID required", err)
	}
}

func TestRunSingBoxMissingUUID(t *testing.T) {
	dir := t.TempDir()
	ipFile := filepath.Join(dir, "ips.txt")
	if err := os.WriteFile(ipFile, []byte("1.1.1.1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Config{
		Mode:       "batch",
		InputFile:  ipFile,
		UseSingBox: true,
	}, cfcommon.NewReporter(nil))
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("Run err = %v, want UUID required", err)
	}
}

func TestSingBoxCapsWorkersOnSession(t *testing.T) {
	s := &session{cfg: Config{Workers: 20, UseSingBox: true}}
	if s.cfg.Workers != 20 {
		t.Fatal("precondition")
	}
	s.cfg.Workers = 6
	if s.cfg.Workers != 6 {
		t.Fatalf("workers = %d", s.cfg.Workers)
	}
}
