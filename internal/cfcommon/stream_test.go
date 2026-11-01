package cfcommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCSVRecordSinkWritesHeaderAndRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "records.csv")

	sink, err := NewCSVRecordSink(path, []string{"A", "B"})
	if err != nil {
		t.Fatalf("new csv record sink: %v", err)
	}
	if err := sink.Write([]string{"1", "2"}); err != nil {
		t.Fatalf("write first row: %v", err)
	}
	if err := sink.Write([]string{"3", "4"}); err != nil {
		t.Fatalf("write second row: %v", err)
	}
	if got := sink.Rows(); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close sink: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "\ufeffA,B") {
		t.Fatalf("expected BOM + header, got %q", content)
	}
	if !strings.Contains(content, "1,2") || !strings.Contains(content, "3,4") {
		t.Fatalf("expected both rows in %q", content)
	}
}

func TestLineSinkWritesLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "lines.txt")

	sink, err := NewLineSink(path)
	if err != nil {
		t.Fatalf("new line sink: %v", err)
	}
	if err := sink.WriteLine("1.1.1.1"); err != nil {
		t.Fatalf("write first line: %v", err)
	}
	if err := sink.WriteLine("1.0.0.1"); err != nil {
		t.Fatalf("write second line: %v", err)
	}
	if got := sink.Lines(); got != 2 {
		t.Fatalf("lines = %d, want 2", got)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close sink: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "1.1.1.1\n1.0.0.1\n" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestCSVRecordSinkCloseAllowsNilReceiver(t *testing.T) {
	var sink *CSVRecordSink
	if err := sink.Close(); err != nil {
		t.Fatalf("close nil csv sink: %v", err)
	}
}

func TestLineSinkCloseAllowsNilReceiver(t *testing.T) {
	var sink *LineSink
	if err := sink.Close(); err != nil {
		t.Fatalf("close nil line sink: %v", err)
	}
}
