package cfcommon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLinesCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "ips.txt")

	if err := WriteLines(path, []string{"1.1.1.1", "1.0.0.1"}); err != nil {
		t.Fatalf("write lines: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "1.1.1.1\n1.0.0.1\n" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestWriteLinesAtomicReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ips.txt")

	if err := os.WriteFile(path, []byte("old\n"), 0644); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	if err := WriteLinesAtomic(path, []string{"1.1.1.1", "1.0.0.1"}); err != nil {
		t.Fatalf("write lines atomic: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "1.1.1.1\n1.0.0.1\n" {
		t.Fatalf("unexpected content %q", got)
	}
}
