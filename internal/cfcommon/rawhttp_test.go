package cfcommon

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestMeasureDownloadSpeedPartialOnDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 32*1024)
		for i := 0; i < 16; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // 客户端超时前一直挂着
	}))
	defer srv.Close()

	host, _, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	speed, n, err := MeasureDownloadSpeed(ctx, host, srv.URL, 400*time.Millisecond, 0, 0, 0, 32*1024)
	if err != nil {
		t.Fatalf("deadline should yield partial success, got err=%v", err)
	}
	if speed <= 0 || n == 0 {
		t.Fatalf("speed=%v bytes=%d, want positive", speed, n)
	}
}

func TestMeasureDownloadSpeedCanceledReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	host, _, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()
	_, _, err = MeasureDownloadSpeed(ctx, host, srv.URL, 2*time.Second, 0, 0, 0, 32*1024)
	if err == nil {
		t.Fatal("user cancel should return error")
	}
}
