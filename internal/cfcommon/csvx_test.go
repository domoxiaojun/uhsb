package cfcommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCSVAppendWriterWritesHeaderOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.csv")

	w, _, closeFn, created, err := NewCSVAppendWriter(path, []string{"A", "B"})
	if err != nil {
		t.Fatalf("first append writer: %v", err)
	}
	if !created {
		t.Fatalf("expected first writer to create file")
	}
	if err := w.Write([]string{"1", "2"}); err != nil {
		t.Fatalf("write first row: %v", err)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("close first writer: %v", err)
	}

	w, _, closeFn, created, err = NewCSVAppendWriter(path, []string{"A", "B"})
	if err != nil {
		t.Fatalf("second append writer: %v", err)
	}
	if created {
		t.Fatalf("expected second writer to append existing file")
	}
	if err := w.Write([]string{"3", "4"}); err != nil {
		t.Fatalf("write second row: %v", err)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("close second writer: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "\ufeffA,B") {
		t.Fatalf("expected BOM + header, got %q", content)
	}
	if strings.Count(content, "A,B") != 1 {
		t.Fatalf("expected header once, got %q", content)
	}
	if !strings.Contains(content, "1,2") || !strings.Contains(content, "3,4") {
		t.Fatalf("expected both rows in %q", content)
	}
}

func TestNewCSVWriterCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "results.csv")

	w, _, closeFn, err := NewCSVWriter(path)
	if err != nil {
		t.Fatalf("new csv writer: %v", err)
	}
	if err := w.Write([]string{"A", "B"}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := w.Write([]string{"1", "2"}); err != nil {
		t.Fatalf("write row: %v", err)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "\ufeffA,B") {
		t.Fatalf("expected BOM + header, got %q", content)
	}
	if !strings.Contains(content, "1,2") {
		t.Fatalf("expected data row in %q", content)
	}
}
