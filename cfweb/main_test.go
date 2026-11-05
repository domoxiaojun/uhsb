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
	req2.Host = "127.0.0.1:8787" // Host 白名单：用 IP 直连
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

func TestBuildPipelineConfigZeroVsUnset(t *testing.T) {
	min0, loss0, hopsOff := 0.0, 0.0, -1
	zero := buildPipelineConfig(runRequest{
		DataMode: "1", SpeedMinMbps: &min0, SpeedLoss: &loss0, HighQualityHops: &hopsOff,
	})
	if zero.SpeedMinMbps != 0 || zero.SpeedLoss != 0 || zero.HighQualityHops != -1 {
		t.Fatalf("explicit zero/disable lost: %+v", zero)
	}
	unset := buildPipelineConfig(runRequest{DataMode: "1"})
	if unset.SpeedMinMbps != 50 || unset.SpeedLoss != 10 ||
		unset.HighQualityHops != 16 || unset.ConnSpeed != 100 || unset.ConnLoss != 10 {
		t.Fatalf("unset thresholds should keep defaults: %+v", unset)
	}
}

func TestHealthEndpointDoesNotProbe(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("health code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestValidateRunConfigRejectsBadHops(t *testing.T) {
	cfg := cfpipeline.Config{DataMode: "1", HighQualityHops: 5000}
	if err := validateRunConfig(&cfg); err == nil {
		t.Fatal("expected error for out-of-range high-quality hops")
	}
	cfg.HighQualityHops = -1
	if err := validateRunConfig(&cfg); err != nil {
		t.Fatalf("-1 should be allowed to disable heuristic: %v", err)
	}
}

func TestKeywordsFlowThroughRunConfig(t *testing.T) {
	cfg := buildPipelineConfig(runRequest{DataMode: "1", ScanKeywords: "nrt,sin"})
	if cfg.ScanKeywords != "nrt,sin" {
		t.Fatalf("ScanKeywords = %q, want nrt,sin", cfg.ScanKeywords)
	}
	if err := validateRunConfig(&cfg); err != nil {
		t.Fatalf("valid custom keywords rejected: %v", err)
	}
	bad := buildPipelineConfig(runRequest{DataMode: "1", ScanKeywords: "nrt|sin"})
	if err := validateRunConfig(&bad); err == nil {
		t.Fatal("regex-like keywords should be rejected at the API boundary")
	}
}

// 只读端点不允许 POST，写端点不允许 GET；否则 GET 就能触发副作用。
func TestMethodContract(t *testing.T) {
	s := newTestServer(t)
	readOnly := map[string]http.HandlerFunc{
		"/api/status": s.handleStatus,
		"/api/health": s.handleHealth,
		"/api/runs":   s.handleRuns,
		"/api/files":  s.handleFiles,
		"/api/events": s.handleEvents,
	}
	for path, h := range readOnly {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, rec.Code)
		}
		if rec.Header().Get("Allow") != http.MethodGet {
			t.Errorf("POST %s Allow = %q, want GET", path, rec.Header().Get("Allow"))
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("POST %s returned non-JSON error: %q", path, rec.Body.String())
		}
	}
	for path, h := range map[string]http.HandlerFunc{
		"/api/run":  s.handleRun,
		"/api/stop": s.handleStop,
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", path, rec.Code)
		}
	}
}

func decodeError(t *testing.T, body string) map[string]any {
	t.Helper()
	var payload struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("error response is not JSON: %q (%v)", body, err)
	}
	if payload.Error == nil || payload.Error["code"] == "" {
		t.Fatalf("missing error.code in %q", body)
	}
	return payload.Error
}

func TestErrorEnvelopeHasCodeAndRequestID(t *testing.T) {
	s := newTestServer(t)
	h := s.wrap(http.HandlerFunc(s.handleRun))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader("not-json"))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "127.0.0.1:8787"
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	info := decodeError(t, rec.Body.String())
	if info["code"] != "invalid_json" {
		t.Fatalf("code = %v, want invalid_json", info["code"])
	}
	if rid := rec.Header().Get("X-Request-Id"); rid == "" || info["request_id"] != rid {
		t.Fatalf("request id mismatch: header=%q body=%v", rid, info["request_id"])
	}
}

// 未知字段（尤其是 SingBoxBinary）必须报错，不能静默忽略还返回 200。
func TestHandleRunRejectsUnknownFields(t *testing.T) {
	s := newTestServer(t)
	for name, body := range map[string]string{
		"dangerous field": `{"DataMode":"1","SingBoxBinary":"/bin/sh"}`,
		"two objects":     `{"DataMode":"1"}{"DataMode":"2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			s.handleRun(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (body must not be silently ignored)", rec.Code)
			}
			decodeError(t, rec.Body.String())
		})
	}
}

func TestStatusDefaultsComeFromCommon(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var payload struct {
		Defaults map[string]any `json:"defaults"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	d := payload.Defaults
	wantInt := map[string]int{
		"num":   cfcommon.DefaultWebNumPerColo,
		"size":  cfcommon.DefaultSpeedSize,
		"speed": cfcommon.DefaultMinSpeedMbps,
		"loss":  cfcommon.DefaultMaxPacketLoss,
		"hops":  cfcommon.DefaultHighQualityHops,
		"port":  cfcommon.DefaultVMessPort,
	}
	for k, want := range wantInt {
		if got, _ := d[k].(float64); int(got) != want {
			t.Errorf("defaults.%s = %v, want %d", k, d[k], want)
		}
	}
	if d["keywords"] != cfcommon.DefaultKeywords || d["path"] != cfcommon.DefaultVMessPath ||
		d["target"] != cfcommon.DefaultTargetColo || d["dataMode"] != cfcommon.DefaultDataMode {
		t.Fatalf("string defaults drifted: %+v", d)
	}
}

// 内部错误不能把服务端文件系统路径回显给客户端。
func TestHandleFilesHidesInternalPaths(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/files?run=does-not-exist", nil)
	s.handleFiles(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), s.root) {
		t.Fatalf("response leaked internal path: %q", rec.Body.String())
	}
	decodeError(t, rec.Body.String())
}

func seedRunWithCredentials(t *testing.T, s *server) string {
	t.Helper()
	dir := filepath.Join(s.root, "runs", "demo")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"demo_speed_vmess.txt":    "vmess://SECRET-UUID-BLOB",
		"demo_speed_singbox.json": `{"outbounds":[{"uuid":"SECRET-UUID-BLOB"}]}`,
		"demo_ips.txt":            "1.1.1.1\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// 回归：审查实测匿名 GET /api/files 能直接下走含明文 UUID 的产物。
func TestHandleFilesBlocksCredentialArtifacts(t *testing.T) {
	s := newTestServer(t)
	seedRunWithCredentials(t, s)

	rec := httptest.NewRecorder()
	s.handleFiles(rec, httptest.NewRequest(http.MethodGet, "/api/files?run=demo", nil))
	var files []string
	if err := json.Unmarshal(rec.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "demo_ips.txt" {
		t.Fatalf("凭据文件仍出现在列表里: %v", files)
	}
	for _, name := range []string{"demo_speed_vmess.txt", "demo_speed_singbox.json"} {
		dl := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/files?run=demo&file="+name, nil)
		s.handleFiles(dl, req)
		if dl.Code != http.StatusForbidden {
			t.Fatalf("下载 %s = %d, want 403", name, dl.Code)
		}
		if strings.Contains(dl.Body.String(), "SECRET-UUID-BLOB") {
			t.Fatalf("错误响应泄露了凭据内容: %s", dl.Body.String())
		}
	}
	dl := httptest.NewRecorder()
	s.handleFiles(dl, httptest.NewRequest(http.MethodGet, "/api/files?run=demo&file=demo_ips.txt", nil))
	if dl.Code != http.StatusOK || !strings.Contains(dl.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("普通产物下载 code=%d disposition=%q", dl.Code, dl.Header().Get("Content-Disposition"))
	}
}

func TestCredentialArtifactsExposableByExplicitOptIn(t *testing.T) {
	t.Setenv("CFWEB_EXPOSE_CREDENTIALS", "true")
	s := newTestServer(t)
	seedRunWithCredentials(t, s)
	rec := httptest.NewRecorder()
	s.handleFiles(rec, httptest.NewRequest(http.MethodGet, "/api/files?run=demo", nil))
	var files []string
	if err := json.Unmarshal(rec.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("显式开启后应列出全部产物, got %v", files)
	}
	dl := httptest.NewRecorder()
	s.handleFiles(dl, httptest.NewRequest(http.MethodGet, "/api/files?run=demo&file=demo_speed_vmess.txt", nil))
	if dl.Code != http.StatusOK {
		t.Fatalf("显式开启后下载 = %d, want 200", dl.Code)
	}
}

func TestWrapRejectsUnknownHost(t *testing.T) {
	s := newTestServer(t)
	h := s.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Host = "evil.rebind.example.com"
		return r
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未知 Host = %d, want 403 (DNS rebinding 防护)", rec.Code)
	}
	t.Setenv("CFWEB_HOSTS", "evil.rebind.example.com")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, newReq())
	if rec2.Code != http.StatusOK {
		t.Fatalf("CFWEB_HOSTS 放行后 = %d, want 200", rec2.Code)
	}
}

// 回归：UI 取消勾选「允许无效证书」必须能覆盖 .env 里的 true。
func TestVMessAllowInsecureFalseOverridesEnv(t *testing.T) {
	t.Setenv("VMESS_ALLOW_INSECURE", "true")
	s := newTestServer(t)

	unset := runRequest{}
	s.applyEnvDefaults(&unset)
	if !buildPipelineConfig(unset).VMessAllowInsecure {
		t.Fatal("未显式设置时应回落环境变量 true")
	}
	f := false
	explicit := runRequest{VMessAllowInsecure: &f}
	s.applyEnvDefaults(&explicit)
	if buildPipelineConfig(explicit).VMessAllowInsecure {
		t.Fatal("显式 false 被环境变量翻转回 true")
	}
}
