package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

func applyVMessEnv(req *cfpipeline.Config) {
	if req.VMessUUID == "" {
		req.VMessUUID = envOr("VMESS_UUID", "")
	}
	if req.VMessHost == "" {
		req.VMessHost = envOr("VMESS_HOST", "")
	}
	if req.VMessPath == "" || req.VMessPath == "/" {
		if v := envOr("VMESS_PATH", ""); v != "" {
			req.VMessPath = v
		}
	}
	if req.VMessSNI == "" {
		req.VMessSNI = envOr("VMESS_SNI", req.VMessHost)
	}
	if req.VMessPort == 0 {
		if v := envOr("VMESS_PORT", ""); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				req.VMessPort = n
			}
		}
	}
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

type server struct {
	root string
	hub  *cfcommon.Hub

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
	s := &server{root: root, hub: cfcommon.NewHub()}

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
	fmt.Printf("[信息] 打开: %s\n", url)
	if *openFlag {
		go openBrowser(url)
	}
	if err := http.Serve(ln, mux); err != nil {
		fmt.Printf("[错误] %v\n", err)
		os.Exit(1)
	}
}

func webFS() fs.FS {
	sub, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic(err)
	}
	return sub
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, hasBox := singbox.Available("")
	port := 443
	if v := envOr("VMESS_PORT", ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	writeJSON(w, map[string]any{
		"busy":    s.busy,
		"runDir":  s.runDir,
		"error":   s.err,
		"mtr":     cfscan.MTRAvailable(),
		"singbox": hasBox,
		"version": cfcommon.Version,
		"root":    s.root,
		"goos":    runtime.GOOS,
		"defaults": map[string]any{
			"uuid": envOr("VMESS_UUID", ""),
			"host": envOr("VMESS_HOST", ""),
			"path": envOr("VMESS_PATH", "/"),
			"sni":  envOr("VMESS_SNI", envOr("VMESS_HOST", "")),
			"port": port,
			"tls":  envBool("VMESS_TLS", true),
		},
	})
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cfpipeline.Config
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	applyVMessEnv(&req)

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, "已有任务在跑", http.StatusConflict)
		return
	}
	s.busy = true
	s.err = ""
	s.runDir = ""
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	req.RootDir = s.root
	req.RunsDir = filepath.Join(s.root, "runs")
	go s.runPipeline(ctx, req)
	writeJSON(w, map[string]any{"ok": true})
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
	s.mu.Unlock()
	reporter.Emit(cfcommon.Event{Stage: "pipeline", Kind: cfcommon.KindDone, Message: "任务结束", Path: s.runDir})
}

func (s *server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	ch, snapshot, unsub := s.hub.Subscribe()
	defer unsub()
	enc := json.NewEncoder(w)
	for _, e := range snapshot {
		fmt.Fprint(w, "data: ")
		_ = enc.Encode(e)
		fmt.Fprint(w, "\n")
	}
	flusher.Flush()

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
			fmt.Fprint(w, "data: ")
			_ = enc.Encode(e)
			fmt.Fprint(w, "\n")
			flusher.Flush()
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
		http.ServeFile(w, r, filepath.Join(dir, file))
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
