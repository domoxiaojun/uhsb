package cfdata

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSubnetListUsesFreshCache(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "ips-v4.txt")
	if err := os.WriteFile(cache, []byte("192.0.2.0/24\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	lines, src, err := loadSubnetList(cache, srv.URL, time.Hour)
	if err != nil {
		t.Fatalf("loadSubnetList: %v", err)
	}
	if len(lines) != 1 || lines[0] != "192.0.2.0/24" {
		t.Fatalf("lines = %v", lines)
	}
	if src == srv.URL {
		t.Fatalf("expected cache source, got %q", src)
	}
}

func TestLoadSubnetListRefreshesStaleCache(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "ips-v4.txt")
	if err := os.WriteFile(cache, []byte("192.0.2.0/24\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(cache, old, old); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "198.51.100.0/24\n")
	}))
	defer srv.Close()

	lines, src, err := loadSubnetList(cache, srv.URL, time.Hour)
	if err != nil {
		t.Fatalf("loadSubnetList: %v", err)
	}
	if len(lines) != 1 || lines[0] != "198.51.100.0/24" {
		t.Fatalf("want refreshed data, got %v", lines)
	}
	if src != srv.URL {
		t.Fatalf("src = %q, want remote url", src)
	}
	data, err := os.ReadFile(cache)
	if err != nil || !strings.Contains(string(data), "198.51.100.0/24") {
		t.Fatalf("cache not refreshed: %q err=%v", data, err)
	}
}

func TestLoadSubnetListFallsBackToStaleOnDownloadFail(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "ips-v4.txt")
	if err := os.WriteFile(cache, []byte("192.0.2.0/24\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(cache, old, old); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()

	lines, src, err := loadSubnetList(cache, srv.URL, time.Hour)
	if err != nil {
		t.Fatalf("loadSubnetList: %v", err)
	}
	if len(lines) != 1 || lines[0] != "192.0.2.0/24" {
		t.Fatalf("want stale fallback, got %v", lines)
	}
	if !strings.Contains(src, "过期") {
		t.Fatalf("src = %q, want stale marker", src)
	}
}
