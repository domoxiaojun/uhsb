package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cftest/internal/cfcommon"
	"cftest/internal/cfpipeline"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	ac, err := cfcommon.NewAccessControl("lan")
	if err != nil {
		t.Fatal(err)
	}
	return &server{root: t.TempDir(), hub: cfcommon.NewHub(), access: ac}
}

func TestStatusDoesNotLeakUUID(t *testing.T) {
	t.Setenv("VMESS_UUID", "super-secret-uuid")
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	body := rec.Body.String()
	if strings.Contains(body, "super-secret-uuid") {
		t.Fatalf("status leaked UUID: %s", body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	defaults, _ := payload["defaults"].(map[string]any)
	if defaults["uuidSet"] != true {
		t.Fatalf("uuidSet = %v, want true", defaults["uuidSet"])
	}
}

func TestWrapRejectsPublicClient(t *testing.T) {
	s := newTestServer(t)
	h := s.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("public client code = %d, want 403", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "192.168.1.20:1234"
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("lan client code = %d, want 200", rec2.Code)
	}
}

func TestSameOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://192.168.1.10:8787/api/run", nil)
	req.Host = "192.168.1.10:8787"
	req.Header.Set("Origin", "http://192.168.1.10:8787")
	if !sameOrigin(req) {
		t.Fatal("same origin should pass")
	}
	req.Header.Set("Origin", "http://evil.example")
	if sameOrigin(req) {
		t.Fatal("cross origin should be rejected")
	}
}

func TestHandleRunRejectsNonJSON(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader("x"))
	req.Header.Set("Content-Type", "text/plain")
	s.handleRun(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("code = %d, want 415", rec.Code)
	}
}

func TestBuildPipelineConfigIgnoresDangerousFields(t *testing.T) {
	var req runRequest
	if err := json.Unmarshal([]byte(`{"DataMode":"1","SingBoxBinary":"/bin/sh","RootDir":"/etc","RunsDir":"/tmp"}`), &req); err != nil {
		t.Fatal(err)
	}
	cfg := buildPipelineConfig(req)
	if cfg.SingBoxBinary != "" || cfg.RootDir != "" || cfg.RunsDir != "" {
		t.Fatalf("dangerous fields leaked into config: %#v", cfg)
	}
}

func TestValidateRunConfigClamps(t *testing.T) {
	cfg := cfpipeline.Config{DataMode: "1", SpeedSize: 999999, ScanThreads: 100000}
	if err := validateRunConfig(&cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.SpeedSize != 1000 {
		t.Fatalf("SpeedSize = %d, want clamp 1000", cfg.SpeedSize)
	}
	if cfg.ScanThreads != 200 {
		t.Fatalf("ScanThreads = %d, want clamp 200", cfg.ScanThreads)
	}
}

func TestValidateRunConfigSingboxNeedsCredentials(t *testing.T) {
	cfg := cfpipeline.Config{DataMode: "1", UseSingBox: true}
	if err := validateRunConfig(&cfg); err == nil {
		t.Fatal("expected error when sing-box enabled without credentials")
	}
}

func TestHandleFilesGuardsTraversal(t *testing.T) {
	s := newTestServer(t)
	runDir := filepath.Join(s.root, "runs", "run1")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "ok.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{".", ".."} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/files?run=run1&file="+bad, nil)
		s.handleFiles(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("file=%q code = %d, want 400", bad, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/files?run=run1&file=ok.txt", nil)
	s.handleFiles(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("ok file code=%d body=%q", rec.Code, rec.Body.String())
	}
}
