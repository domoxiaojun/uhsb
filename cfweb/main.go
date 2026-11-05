package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"cftest/internal/cfcommon"
	"cftest/internal/cfpipeline"
	"cftest/internal/cfscan"
	"cftest/internal/singbox"
)

var (
	addrFlag    = flag.String("addr", envOr("CFWEB_ADDR", cfcommon.DefaultCFWebAddr), "监听地址")
	openFlag    = flag.Bool("open", envBool("CFWEB_OPEN", cfcommon.DefaultCFWebOpen), "启动后打开浏览器")
	rootFlag    = flag.String("root", envOr("CFWEB_ROOT", ""), "工作目录 (默认仓库根)")
	versionFlag = flag.Bool("version", false, "显示版本号")
)

var (
	requestSeq uint64
	sseSeq     uint64
)

type ctxKey string

const requestIDKey ctxKey = "request-id"

func newRequestID() string {
	n := atomic.AddUint64(&requestSeq, 1)
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), n)
}

func requestID(r *http.Request) string {
	if v, ok := r.Context().Value(requestIDKey).(string); ok && v != "" {
		return v
	}
	return ""
}

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
	DataMode        string
	Colo            string
	Country         string
	IPType          string
	NumPerColo      int
	Detail          bool
	SkipScan        bool
	RunConnect      bool
	SpeedSize       int
	SpeedMinMbps    *float64
	SpeedLoss       *float64
	HighQualityHops *int
	ScanKeywords    string
	HostHeader      string
	UseSingBox      bool
	VMessUUID       string
	VMessHost       string
	VMessPath       string
	VMessSNI        string
	VMessPort       int
	VMessTLS        *bool
	// 指针：前端恒传布尔，显式 false 必须能覆盖环境变量里的 true，
	// 否则用户取消勾选「允许无效证书」会被静默翻转回跳过 TLS 校验。
	VMessAllowInsecure *bool
}

type server struct {
	root   string
	hub    *cfcommon.Hub
	access *cfcommon.AccessControl

	// shutdownCh 在进程收到终止信号时关闭，让 SSE 长连接主动退出，
	// 否则 http.Server.Shutdown 会被长连接一直拖到超时。
	shutdownCh chan struct{}

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
	access, err := cfcommon.NewAccessControl(envOr("CFWEB_ALLOW", cfcommon.DefaultCFWebAllow))
	if err != nil {
		fmt.Printf("[错误] CFWEB_ALLOW 配置无效: %v\n", err)
		os.Exit(1)
	}
	s := &server{
		root:       root,
		hub:        cfcommon.NewHub(),
		access:     access,
		shutdownCh: make(chan struct{}),
	}
	if access.Insecure() {
		fmt.Printf("[警告] CFWEB_ALLOW=any 已放行所有来源，且本服务没有账号体系；请勿直接暴露到公网！\n")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	// 轻量探活：不做 mtr / sing-box 探测，避免健康检查被慢探测拖超时。
	mux.HandleFunc("/api/health", s.handleHealth)
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
	// 不设 WriteTimeout：/api/events 是有心跳的长连接，写超时会把 SSE 掐断。
	srv := &http.Server{
		Handler:           s.wrap(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       5 * time.Minute,
	}
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-sigCtx.Done()
		fmt.Printf("\n[停机] 收到终止信号，正在取消当前任务并关闭服务…\n")
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		close(s.shutdownCh) // 让 SSE 长连接退出，Shutdown 才能及时返回
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Printf("[警告] 优雅停机超时: %v\n", err)
		}
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Printf("[错误] %v\n", err)
		os.Exit(1)
	}
}

// wrap 统一加上安全响应头、请求 ID，并做局域网访问控制。
func (s *server) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := newRequestID()
		w.Header().Set("X-Request-Id", rid)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, rid))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		if s.access != nil && !s.access.Allowed(r.RemoteAddr) {
			writeError(w, r, http.StatusForbidden, "forbidden", "禁止访问：WebUI 仅对局域网开放")
			return
		}
		// 所有端点（含 GET /api/files、SSE）统一校验来源，避免 DNS rebinding
		// 用自有域名解析到 127.0.0.1 后绕过 LAN-only 读到凭据产物。
		if !requestFromAllowedOrigin(r) {
			writeError(w, r, http.StatusForbidden, "forbidden", "请求来源不被允许：Host/Origin 校验失败")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// hostAllowed 只放行 IP 字面量、localhost 与 CFWEB_HOSTS 显式列出的域名。
// DNS rebinding 必须依赖攻击者控制的域名；只要拒绝未知 Host 就能挡住，
// 同时不影响用 IP 直连（含局域网 IP）访问 WebUI。
func hostAllowed(host string) bool {
	h := host
	if u, err := url.Parse("http://" + host); err == nil {
		h = u.Hostname()
	}
	if net.ParseIP(h) != nil || strings.EqualFold(h, "localhost") {
		return true
	}
	for _, allowed := range strings.Split(envOr("CFWEB_HOSTS", ""), ",") {
		if allowed = strings.TrimSpace(allowed); allowed != "" && strings.EqualFold(h, allowed) {
			return true
		}
	}
	return false
}

func requestFromAllowedOrigin(r *http.Request) bool {
	if !hostAllowed(r.Host) {
		return false
	}
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	return sameOrigin(r)
}

// writeError 统一返回结构化错误，前端可据 code 判断是否需要重试。
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSONStatus(w, status, map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

// internalError 不把文件系统路径、环境变量等内部细节回显给客户端。
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("[%s] 内部错误: %v", requestID(r), err)
	writeError(w, r, http.StatusInternalServerError, "internal_error", "服务内部错误，请查看服务端日志")
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 "+method)
	return false
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

// handleHealth 只回报进程存活，不触发任何外部探测，供容器健康检查使用。
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	// 这两个探测可能耗时（mtr 探测最高约 8s），放在锁外，避免阻塞 /api/run。
	_, hasBox := singbox.Available("")
	mtrPath, mtrErr := cfscan.MTRStatusCtx(r.Context())
	mtrMsg := ""
	if mtrErr != nil {
		mtrMsg = mtrErr.Error()
	}
	mtrPathBase := ""
	if mtrPath != "" {
		mtrPathBase = filepath.Base(mtrPath)
	}
	port := cfcommon.DefaultVMessPort
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
		"busy":     busy,
		"runDir":   runDir,
		"error":    lastErr,
		"mtr":      mtrErr == nil,
		"mtrPath":  mtrPathBase, // 只回文件名，不回显服务端绝对路径
		"insecure": s.access.Insecure(),
		"mtrErr":   mtrMsg,
		"singbox":  hasBox,
		"version":  cfcommon.Version,
		"root":     s.root,
		"allow":    s.access.String(),
		"goos":     runtime.GOOS,
		"defaults": map[string]any{
			// 表单默认值全部来自 cfcommon，WebUI 不再自带一份可能漂移的字面量。
			"dataMode":      cfcommon.DefaultDataMode,
			"ipType":        cfcommon.DefaultIPType,
			"target":        cfcommon.DefaultTargetColo,
			"num":           cfcommon.DefaultWebNumPerColo,
			"size":          cfcommon.DefaultSpeedSize,
			"speed":         cfcommon.DefaultMinSpeedMbps,
			"loss":          cfcommon.DefaultMaxPacketLoss,
			"hops":          cfcommon.DefaultHighQualityHops,
			"keywords":      envOr("CFSCAN_KEYWORDS", cfcommon.DefaultKeywords),
			"uuidSet":       envOr("VMESS_UUID", "") != "",
			"host":          envOr("VMESS_HOST", ""),
			"path":          envOr("VMESS_PATH", cfcommon.DefaultVMessPath),
			"sni":           envOr("VMESS_SNI", envOr("VMESS_HOST", "")),
			"port":          port,
			"tls":           envBool("VMESS_TLS", cfcommon.DefaultVMessTLS),
			"allowInsecure": envBool("VMESS_ALLOW_INSECURE", cfcommon.DefaultVMessAllowInsecure),
		},
	})
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !sameOrigin(r) {
		writeError(w, r, http.StatusForbidden, "forbidden", "跨站请求被拒绝")
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "需要 application/json")
		return
	}

	var req runRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	// 未知字段直接拒绝（例如 SingBoxBinary / RootDir），避免"看起来成功了但其实被忽略"。
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		// 解析细节（字符位置、Go 类型名）只进日志，不回显给客户端。
		log.Printf("[%s] 请求体解析失败: %v", requestID(r), err)
		writeError(w, r, http.StatusBadRequest, "invalid_json", "请求体解析失败，请检查 JSON 格式与字段名")
		return
	}
	// 拒绝一次请求塞两个 JSON 对象。
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "请求体只能包含一个 JSON 对象")
		return
	}
	s.applyEnvDefaults(&req)
	cfg := buildPipelineConfig(req)
	if err := validateRunConfig(&cfg); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		writeError(w, r, http.StatusConflict, "busy", "已有任务在跑")
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
		req.VMessPath = envOr("VMESS_PATH", cfcommon.DefaultVMessPath)
	}
	if req.VMessSNI == "" {
		req.VMessSNI = envOr("VMESS_SNI", req.VMessHost)
	}
	if req.VMessPort == 0 {
		req.VMessPort = cfcommon.DefaultVMessPort
		if v := envOr("VMESS_PORT", ""); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				req.VMessPort = n
			}
		}
	}
	if req.VMessTLS == nil {
		b := envBool("VMESS_TLS", cfcommon.DefaultVMessTLS)
		req.VMessTLS = &b
	}
	if req.VMessAllowInsecure == nil {
		b := envBool("VMESS_ALLOW_INSECURE", cfcommon.DefaultVMessAllowInsecure)
		req.VMessAllowInsecure = &b
	}
	if strings.TrimSpace(req.ScanKeywords) == "" {
		req.ScanKeywords = envOr("CFSCAN_KEYWORDS", cfcommon.DefaultKeywords)
	}
}

func buildPipelineConfig(req runRequest) cfpipeline.Config {
	// 未提供的阈值先填默认，再由指针覆盖。这样前端显式传 0 才能表达
	// 「关闭该门槛」，而页面没带上的字段仍保持原有默认行为。
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
		SpeedMinMbps:       cfcommon.DefaultMinSpeedMbps,
		SpeedLoss:          cfcommon.DefaultMaxPacketLoss,
		HighQualityHops:    cfcommon.DefaultHighQualityHops,
		ScanKeywords:       strings.TrimSpace(req.ScanKeywords),
		ConnSpeed:          cfcommon.DefaultConnSpeed,
		ConnLoss:           cfcommon.DefaultConnLoss,
		HostHeader:         req.HostHeader,
		UseSingBox:         req.UseSingBox,
		VMessUUID:          strings.TrimSpace(req.VMessUUID),
		VMessHost:          strings.TrimSpace(req.VMessHost),
		VMessPath:          req.VMessPath,
		VMessSNI:           req.VMessSNI,
		VMessPort:          req.VMessPort,
		VMessAllowInsecure: boolOr(req.VMessAllowInsecure, cfcommon.DefaultVMessAllowInsecure),
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
	if req.HighQualityHops != nil {
		cfg.HighQualityHops = *req.HighQualityHops
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
		cfg.HostHeader = cfcommon.DefaultHost
	}
	if !hostHeaderRe.MatchString(cfg.HostHeader) {
		return fmt.Errorf("Host 头格式非法")
	}
	cfg.NumPerColo = clampInt(cfg.NumPerColo, 0, cfcommon.MaxNumPerColo, 0)
	cfg.SpeedSize = clampInt(cfg.SpeedSize, 1, cfcommon.MaxSpeedSize, cfcommon.DefaultSpeedSize)
	cfg.Port = clampInt(cfg.Port, 1, 65535, cfcommon.DefaultPort)
	cfg.ScanThreads = clampInt(cfg.ScanThreads, 1, cfcommon.MaxScanThreads, cfcommon.DefaultScanThreads)
	cfg.TestThreads = clampInt(cfg.TestThreads, 1, cfcommon.MaxTestThreads, cfcommon.DefaultTestThreads)
	cfg.SpeedWorkers = clampInt(cfg.SpeedWorkers, 1, cfcommon.MaxSpeedWorkers, cfcommon.DefaultWorkers)
	cfg.ConnWorkers = clampInt(cfg.ConnWorkers, 1, cfcommon.MaxConnWorkers, cfcommon.DefaultConnWorkers)
	cfg.ConnSize = clampInt(cfg.ConnSize, 1, cfcommon.MaxConnSize, cfcommon.DefaultConnSize)
	if cfg.SpeedMinMbps < 0 || cfg.SpeedLoss < 0 || cfg.ConnLoss < 0 || cfg.ConnSpeed < 0 {
		return fmt.Errorf("阈值不能为负")
	}
	// -1 = 关闭「高跳数视为优质」启发式；0 = 用 cfcommon.DefaultHighQualityHops。
	if cfg.HighQualityHops < -1 || cfg.HighQualityHops > cfcommon.MaxHighQualityHops {
		return fmt.Errorf("高跳数阈值超出范围 (-1..1000)")
	}
	// 关键字会在 cfscan 里被编译成正则，必须在入口就挡掉非法字符。
	if _, err := cfscan.CompileKeywords(cfg.ScanKeywords); err != nil {
		return err
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

func boolOr(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
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
	// 后台 goroutine 里的 panic 会终止整个进程，并把 busy 永久卡在 true。
	// 这里兜住它，重置状态、上报错误，并让前端收到 done 结束等待。
	defer func() {
		if v := recover(); v != nil {
			log.Printf("[panic] pipeline goroutine: %v\n%s", v, debug.Stack())
			s.mu.Lock()
			s.busy = false
			s.cancel = nil
			s.err = fmt.Sprintf("任务异常终止: %v", v)
			s.mu.Unlock()
			reporter.Error("pipeline", "任务异常终止: %v（详见服务端日志）", v)
			reporter.Emit(cfcommon.Event{Stage: "pipeline", Kind: cfcommon.KindDone, Message: "任务结束"})
		}
	}()
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !sameOrigin(r) {
		writeError(w, r, http.StatusForbidden, "forbidden", "跨站请求被拒绝")
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
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "stream_unsupported", "当前服务器不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// 断线后 3s 重连；带上单调递增的 id，方便客户端去重与排查。
	fmt.Fprint(w, "retry: 3000\n\n")
	// 本服务是「快照式」SSE：每次连接（含重连）都先发 KindReset 再回放
	// 完整历史，因此不使用 Last-Event-ID 做增量补发；id 仅用于客户端去重。
	// 这也意味着超过 hubMaxHistory 的历史会被裁剪（Hub 会插入裁剪提示事件）。
	_ = r.Header.Get("Last-Event-ID")

	ch, snapshot, unsub := s.hub.Subscribe()
	defer unsub()
	enc := json.NewEncoder(w)
	writeEvent := func(e cfcommon.Event) bool {
		fmt.Fprintf(w, "id: %d\n", atomic.AddUint64(&sseSeq, 1))
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
		case <-s.shutdownCh:
			return // 进程停机，主动断开 SSE 让 Shutdown 立即完成
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
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	names, err := cfpipeline.ListRuns(filepath.Join(s.root, "runs"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, names)
}

// isCredentialArtifact 判断产物是否含明文节点凭据。这类文件默认不允许经
// Web 下载：/api/status 刻意不返回 VMESS_UUID，不能让 /api/files 成为旁路。
// 确实需要从 WebUI 取节点时，显式设 CFWEB_EXPOSE_CREDENTIALS=true。
func isCredentialArtifact(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, "_vmess.txt") ||
		strings.HasSuffix(lower, "_singbox.json") ||
		(strings.HasPrefix(lower, "singbox-") && strings.HasSuffix(lower, ".json"))
}

func (s *server) handleFiles(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	exposeCredentials := envBool("CFWEB_EXPOSE_CREDENTIALS", false)
	name := filepath.Base(r.URL.Query().Get("run"))
	if name == "" || name == "." || name == ".." {
		writeError(w, r, http.StatusBadRequest, "bad_run", "运行目录名非法")
		return
	}
	dir := filepath.Join(s.root, "runs", name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "run_not_found", "运行目录不存在")
		return
	}
	file := r.URL.Query().Get("file")
	if file != "" {
		file = filepath.Base(file)
		if file == "." || file == ".." {
			writeError(w, r, http.StatusBadRequest, "bad_file", "文件名非法")
			return
		}
		if !exposeCredentials && isCredentialArtifact(file) {
			writeError(w, r, http.StatusForbidden, "credential_forbidden",
				"含凭据产物默认不提供 Web 下载；请从宿主机 data/runs/ 目录取用，或显式设置 CFWEB_EXPOSE_CREDENTIALS=true")
			return
		}
		full := filepath.Join(dir, file)
		st, err := os.Stat(full)
		if err != nil || st.IsDir() {
			writeError(w, r, http.StatusNotFound, "file_not_found", "文件不存在")
			return
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file}))
		http.ServeFile(w, r, full)
		return
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && (exposeCredentials || !isCredentialArtifact(e.Name())) {
			files = append(files, e.Name())
		}
	}
	writeJSON(w, files)
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
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
