package cfcommon

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchTraceMapRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	host, _, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	if _, err := FetchTraceMap(context.Background(), host, srv.URL, 0); err == nil {
		t.Fatal("FetchTraceMap() error = nil, want non-nil")
	}
}

func TestFetchTraceMapRejectsInvalidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not trace format"))
	}))
	defer srv.Close()

	host, _, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	if _, err := FetchTraceMap(context.Background(), host, srv.URL, 0); err == nil {
		t.Fatal("FetchTraceMap() error = nil, want non-nil")
	}
}

func TestNewForcedIPHTTPClientConfiguresTLSVerification(t *testing.T) {
	client, err := NewForcedIPHTTPClient("127.0.0.1", "https://speed.cloudflare.com/cdn-cgi/trace", 0)
	if err != nil {
		t.Fatalf("NewForcedIPHTTPClient() error = %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig = nil, want non-nil")
	}
	if transport.TLSClientConfig.ServerName != "speed.cloudflare.com" {
		t.Fatalf("ServerName = %q, want speed.cloudflare.com", transport.TLSClientConfig.ServerName)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = true, want false")
	}
}

func TestLoadLocationsRejectsNon200(t *testing.T) {
	oldCache := locCache
	oldURL := locationURL
	oldPath := locationPath
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tempDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer srv.Close()

	t.Cleanup(func() {
		locCache = oldCache
		locationURL = oldURL
		locationPath = oldPath
		_ = os.Chdir(oldWD)
	})

	locCache = nil
	locationURL = srv.URL
	locationPath = filepath.Join(tempDir, "locations.json")
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if _, err := LoadLocations(); err == nil {
		t.Fatal("LoadLocations() error = nil, want non-nil")
	}
}
