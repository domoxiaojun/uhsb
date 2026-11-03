package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"cftest/internal/cfcommon"
	"cftest/internal/cfpipeline"
	"cftest/internal/cfscan"
	"cftest/internal/singbox"
)

var (
	addrFlag    = flag.String("addr", envOr("CFWEB_ADDR", "127.0.0.1:8787"), "监听地址")
	openFlag    = flag.Bool("open", envBool("CFWEB_OPEN", true), "启动后打开浏览器")
	rootFlag    = flag.String("root", envOr("CFWEB_ROOT", ""), "工作目录 (默认仓库根)")
	versionFlag = flag.Bool("version", false, "显示版本号")
)

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// runRequest 是 /api/run 允许客户端设置的字段白名单。
// 刻意不包含 SingBoxBinary / RootDir / RunsDir 等会带来任意执行或路径逃逸的字段。
type runRequest struct {
	DataMode           string
	Colo               string
	Country            string
	IPType             string
	NumPerColo         int
	Detail             bool
	SkipScan           bool
	RunConnect         bool
	SpeedSize          int
	SpeedMinMbps       *float64
	SpeedLoss          *float64
	HostHeader         string
	UseSingBox         bool
	VMessUUID          string
	VMessHost          string
	VMessPath          string
	VMessSNI           string
	VMessPort          int
	VMessTLS           *bool
	VMessAllowInsecure bool
}

type server struct {
	root   string
	hub    *cfcommon.Hub
	access *cfcommon.AccessControl

	mu     sync.Mutex
	cancel context.CancelFunc
	busy   bool
	runDir string
	err    string
}

func main() {
	flag.Parse()
	if *versionFlag {
		cfcommon.PrintVersionAndExit("cfweb")
	}

	root := *rootFlag
	if root == "" {
		root, _ = os.Getwd()
		if filepath.Base(root) == "cfweb" {
			root = filepath.Dir(root)
		}
	}
	access, err := cfcommon.NewAccessControl(envOr("CFWEB_ALLOW", "lan"))
	if err != nil {
		fmt.Printf("[错误] CFWEB_ALLOW 配置无效: %v\n", err)
		os.Exit(1)
	}
	s := &server{root: root, hub: cfcommon.NewHub(), access: access}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.Handle("/", http.FileServer(http.FS(webFS())))

	ln, err := net.Listen("tcp", *addrFlag)
	if err != nil {
		fmt.Printf("[错误] 监听失败: %v\n", err)
		os.Exit(1)
	}
	url := "http://" + ln.Addr().String()
	fmt.Printf("═══ CFWeb %s ═══\n", cfcommon.Version)
	fmt.Printf("[信息] 工作目录: %s\n", root)
	fmt.Printf("[信息] 访问范围: %s (CFWEB_ALLOW)\n", access)
	fmt.Printf("[信息] 打开: %s\n", url)
	if *openFlag {
		go openBrowser(url)
	}
	if err := http.Serve(ln, s.wrap(mux)); err != nil {
		fmt.Printf("[错误] %v\n", err)
		os.Exit(1)
	}
}

// wrap 统一加上安全响应头，并做局域网访问控制。
func (s *server) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		if s.access != nil && !s.access.Allowed(r.RemoteAddr) {
			http.Error(w, "禁止访问：WebUI 仅对局域网开放", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// sameOrigin 拒绝跨站写请求（CSRF 防护）。没有 Origin 头的请求（curl 等）放行，
// 由局域网访问控制兜底。
func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func webFS() fs.FS {
	sub, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic(err)
	}
	return sub
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// 这两个探测可能耗时（mtr 探测最高约 8s），放在锁外，避免阻塞 /api/run。
	_, hasBox := singbox.Available("")
	mtrPath, mtrErr := cfscan.MTRStatus()
	mtrMsg := ""
	if mtrErr != nil {
		mtrMsg = mtrErr.Error()
	}
	port := 443
	if v := envOr("VMESS_PORT", ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	s.mu.Lock()
	busy, runDir, lastErr := s.busy, s.runDir, s.err
	s.mu.Unlock()
	// 注意：绝不返回 VMESS_UUID 明文，只告诉前端"服务端已配置"，避免凭据泄露。
	writeJSON(w, map[string]any{
		"busy":    busy,
		"runDir":  runDir,
		"error":   lastErr,
		"mtr":     mtrErr == nil,
		"mtrPath": mtrPath,
		"mtrErr":  mtrMsg,
		"singbox": hasBox,
		"version": cfcommon.Version,
		"root":    s.root,
		"allow":   s.access.String(),
		"goos":    runtime.GOOS,
		"defaults": map[string]any{
			"uuidSet": envOr("VMESS_UUID", "") != "",
			"host":    envOr("VMESS_HOST", ""),
			"path":    envOr("VMESS_PATH", "/"),
			"sni":     envOr("VMESS_SNI", envOr("VMESS_HOST", "")),
			"port":    port,
			"tls":     envBool("VMESS_TLS", true),
		},
	})
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "跨站请求被拒绝", http.StatusForbidden)
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		http.Error(w, "需要 application/json", http.StatusUnsupportedMediaType)
		return
	}

	var req runRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.applyEnvDefaults(&req)
	cfg := buildPipelineConfig(req)
	if err := validateRunConfig(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, "已有任务在跑", http.StatusConflict)
		return
	}
	s.busy = true
	s.err = ""
	s.runDir = ""
	s.hub.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	cfg.RootDir = s.root
	cfg.RunsDir = filepath.Join(s.root, "runs")
	go s.runPipeline(ctx, cfg)
	writeJSON(w, map[string]any{"ok": true})
}

// applyEnvDefaults 用服务端环境变量补齐前端没填的 VMess 参数。
// 显式传入的值优先，其次环境变量，最后回落到派生默认值。
func (s *server) applyEnvDefaults(req *runRequest) {
	if req.VMessUUID == "" {
		req.VMessUUID = envOr("VMESS_UUID", "")
	}
	if req.VMessHost == "" {
		req.VMessHost = envOr("VMESS_HOST", "")
	}
	if req.VMessPath == "" {
		req.VMessPath = envOr("VMESS_PATH", "/")
	}
	if req.VMessSNI == "" {
		req.VMessSNI = envOr("VMESS_SNI", req.VMessHost)
	}
	if req.VMessPort == 0 {
		req.VMessPort = 443
		if v := envOr("VMESS_PORT", ""); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				req.VMessPort = n
			}
		}
	}
	if req.VMessTLS == nil {
		b := envBool("VMESS_TLS", true)
		req.VMessTLS = &b
	}
	if !req.VMessAllowInsecure {
		req.VMessAllowInsecure = envBool("VMESS_ALLOW_INSECURE", false)
	}
}

func buildPipelineConfig(req runRequest) cfpipeline.Config {
	cfg := cfpipeline.Config{
		DataMode:           req.DataMode,
		Colo:               req.Colo,
		Country:            req.Country,
		IPType:             req.IPType,
		NumPerColo:         req.NumPerColo,
		Detail:             req.Detail,
		SkipScan:           req.SkipScan,
		RunConnect:         req.RunConnect,
		SpeedSize:          req.SpeedSize,
		HostHeader:         req.HostHeader,
		UseSingBox:         req.UseSingBox,
		VMessUUID:          strings.TrimSpace(req.VMessUUID),
		VMessHost:          strings.TrimSpace(req.VMessHost),
		VMessPath:          req.VMessPath,
		VMessSNI:           req.VMessSNI,
		VMessPort:          req.VMessPort,
		VMessAllowInsecure: req.VMessAllowInsecure,
	}
	if req.VMessTLS != nil {
		cfg.VMessTLS = *req.VMessTLS
	}
	if req.SpeedMinMbps != nil {
		cfg.SpeedMinMbps = *req.SpeedMinMbps
	}
	if req.SpeedLoss != nil {
		cfg.SpeedLoss = *req.SpeedLoss
	}
	return cfg
}

var (
	hostHeaderRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]{1,5})?$`)
	coloRe       = regexp.MustCompile(`^[A-Za-z0-9,_\-\s]*$`)
)

func validateRunConfig(cfg *cfpipeline.Config) error {
	switch cfg.DataMode {
	case "", "1", "2", "3":
	default:
		return fmt.Errorf("无效的数据模式: %q", cfg.DataMode)
	}
	if len(cfg.Colo) > 200 || len(cfg.Country) > 200 {
		return fmt.Errorf("目标 colo/国家 过长")
	}
	if !coloRe.MatchString(cfg.Colo) || !coloRe.MatchString(cfg.Country) {
		return fmt.Errorf("目标 colo/国家 含有非法字符")
	}
	if cfg.HostHeader == "" {
		cfg.HostHeader = "www.visa.cn"
	}
	if !hostHeaderRe.MatchString(cfg.HostHeader) {
		return fmt.Errorf("Host 头格式非法")
	}
	cfg.NumPerColo = clampInt(cfg.NumPerColo, 0, 100000, 0)
	cfg.SpeedSize = clampInt(cfg.SpeedSize, 1, 1000, 50)
	cfg.Port = clampInt(cfg.Port, 1, 65535, 443)
	cfg.ScanThreads = clampInt(cfg.ScanThreads, 1, 200, 50)
	cfg.TestThreads = clampInt(cfg.TestThreads, 1, 100, 25)
	cfg.SpeedWorkers = clampInt(cfg.SpeedWorkers, 1, 50, 20)
	cfg.ConnWorkers = clampInt(cfg.ConnWorkers, 1, 50, 20)
	cfg.ConnSize = clampInt(cfg.ConnSize, 1, 1000, 200)
	if cfg.SpeedMinMbps < 0 || cfg.SpeedLoss < 0 || cfg.ConnLoss < 0 || cfg.ConnSpeed < 0 {
		return fmt.Errorf("阈值不能为负")
	}
	if cfg.VMessPort < 0 || cfg.VMessPort > 65535 {
		return fmt.Errorf("VMess 端口非法")
	}
	if cfg.UseSingBox {
		if cfg.VMessUUID == "" || cfg.VMessHost == "" {
			return fmt.Errorf("启用 sing-box 时必须填写 UUID 和 WS Host")
		}
		if _, ok := singbox.Available(""); !ok {
			return fmt.Errorf("未检测到 sing-box 内核")
		}
	}
	return nil
}

func clampInt(v, lo, hi, def int) int {
	if v <= 0 {
		if lo <= 0 {
			return 0
		}
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (s *server) runPipeline(ctx context.Context, cfg cfpipeline.Config) {
	reporter := s.hub.Reporter()
	res, err := cfpipeline.Run(ctx, cfg, reporter)
	s.mu.Lock()
	s.busy = false
	s.cancel = nil
	if res != nil {
		s.runDir = res.RunDir
	}
	if err != nil {
		s.err = err.Error()
		reporter.Error("pipeline", "%v", err)
	}
	runDir := s.runDir
	s.mu.Unlock()
	reporter.Emit(cfcommon.Event{Stage: "pipeline", Kind: cfcommon.KindDone, Message: "任务结束", Path: runDir})
}

func (s *server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "跨站请求被拒绝", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, snapshot, unsub := s.hub.Subscribe()
	defer unsub()
	enc := json.NewEncoder(w)
	writeEvent := func(e cfcommon.Event) bool {
		fmt.Fprint(w, "data: ")
		if err := enc.Encode(e); err != nil {
			return false
		}
		fmt.Fprint(w, "\n")
		flusher.Flush()
		return true
	}
	// 先让页面清空，再回放完整快照，避免重连后内容重复或残留旧任务。
	if !writeEvent(cfcommon.Event{Stage: "pipeline", Kind: cfcommon.KindReset, Message: "同步"}) {
		return
	}
	for _, e := range snapshot {
		if !writeEvent(e) {
			return
		}
	}

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !writeEvent(e) {
				return
			}
		}
	}
}

func (s *server) handleRuns(w http.ResponseWriter, r *http.Request) {
	names, err := cfpipeline.ListRuns(filepath.Join(s.root, "runs"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, names)
}

func (s *server) handleFiles(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Query().Get("run"))
	if name == "" || name == "." || name == ".." {
		http.Error(w, "bad run", 400)
		return
	}
	dir := filepath.Join(s.root, "runs", name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	file := r.URL.Query().Get("file")
	if file != "" {
		file = filepath.Base(file)
		if file == "." || file == ".." {
			http.Error(w, "bad file", 400)
			return
		}
		full := filepath.Join(dir, file)
		st, err := os.Stat(full)
		if err != nil || st.IsDir() {
			http.Error(w, "file not found", 404)
			return
		}
		http.ServeFile(w, r, full)
		return
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	writeJSON(w, files)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func openBrowser(url string) {
	time.Sleep(300 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
