package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	startupTimeout = 8 * time.Second
	startAttempts  = 3
)

type Config struct {
	Binary     string
	WorkDir    string
	ListenHost string
}

func (c Config) withDefaults() Config {
	if c.ListenHost == "" {
		c.ListenHost = "127.0.0.1"
	}
	return c
}

type Instance struct {
	Node       Node
	ListenAddr string
	ProxyURL   string
	ConfigPath string

	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	exitErr error
	stderr  *tailBuffer
}

func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	var errs []error
	if i.cancel != nil {
		i.cancel()
	}
	if i.cmd != nil && i.cmd.Process != nil {
		select {
		case <-i.done:
		default:
			if err := i.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				errs = append(errs, fmt.Errorf("结束 sing-box 进程失败: %w", err))
			}
			// Kill 失败或 cmd.Wait 卡住时不能无限等，否则 worker 的 wg.Wait()
			// 会把整个 pipeline 永久挂死，且没有任何 ctx 能解开。
			select {
			case <-i.done:
			case <-time.After(3 * time.Second):
				errs = append(errs, fmt.Errorf("等待 sing-box 退出超时（3s）"))
			}
		}
	}
	if i.ConfigPath != "" {
		if err := os.Remove(i.ConfigPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("清理含凭据的临时配置失败: %w", err))
		}
		i.ConfigPath = ""
	}
	return errors.Join(errs...)
}

// Stderr 返回 sing-box 最近输出的诊断信息（启动失败时用于报错）。
func (i *Instance) Stderr() string {
	if i == nil || i.stderr == nil {
		return ""
	}
	return i.stderr.String()
}

type Manager struct {
	cfg    Config
	binary string

	// procCtx 是「本次测速任务」级别的 ctx，Cancel 后所有 sing-box 子进程一起退出。
	// 子进程不直接绑定单次测速的 ctx（那会因测速超时误杀内核），
	// 但必须有任务级兜底，避免异常退出留下孤儿进程。
	procCtx    context.Context
	procCancel context.CancelFunc
}

// Close 取消本次任务拉起的所有 sing-box 进程，由 session.close() 调用。
func (m *Manager) Close() {
	if m != nil && m.procCancel != nil {
		m.procCancel()
	}
}

func Available(explicit string) (string, bool) {
	if explicit != "" {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			return explicit, true
		}
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p, true
	}
	return "", false
}

func NewManager(cfg Config) (*Manager, error) {
	cfg = cfg.withDefaults()
	bin, ok := Available(cfg.Binary)
	if !ok {
		return nil, fmt.Errorf("未找到 sing-box。Linux 安装: curl -fsSL https://sing-box.app/install.sh | sh  或把二进制放到 PATH")
	}
	if cfg.WorkDir != "" {
		if err := os.MkdirAll(cfg.WorkDir, 0755); err != nil {
			return nil, err
		}
		cleanStaleConfigs(cfg.WorkDir)
	}
	procCtx, procCancel := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, binary: bin, procCtx: procCtx, procCancel: procCancel}, nil
}

// cleanStaleConfigs 清掉上次被 SIGKILL/OOM 留下的临时配置（内含明文 UUID）。
func cleanStaleConfigs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := strings.ToLower(e.Name())
		if strings.HasPrefix(n, "singbox-") && strings.HasSuffix(n, ".json") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func (m *Manager) Binary() string { return m.binary }

// Start 为单个节点拉起一个临时 sing-box 实例。
// ctx 只约束"启动"阶段；进程生命周期由内部 handle 管理，测速超时不会误杀内核。
func (m *Manager) Start(ctx context.Context, node Node) (*Instance, error) {
	var lastErr error
	for attempt := 0; attempt < startAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		inst, err := m.startOnce(ctx, node)
		if err == nil {
			return inst, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("sing-box 启动失败")
	}
	return nil, lastErr
}

func (m *Manager) startOnce(ctx context.Context, node Node) (*Instance, error) {
	port, err := freePort(m.cfg.ListenHost)
	if err != nil {
		return nil, fmt.Errorf("分配本地端口失败: %w", err)
	}

	raw, err := json.MarshalIndent(runtimeConfig(node, m.cfg.ListenHost, port), "", "  ")
	if err != nil {
		return nil, err
	}
	dir := m.cfg.WorkDir
	if dir == "" {
		dir = os.TempDir()
	}
	// CreateTemp 生成唯一文件名（0600）：freePort 存在 TOCTOU 窗口，
	// 固定 pid+port 命名会让并发 Start 互相覆盖配置、串用节点凭据。
	f, err := os.CreateTemp(dir, "singbox-*.json")
	if err != nil {
		return nil, fmt.Errorf("创建临时配置失败: %w", err)
	}
	path := f.Name()
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("写入临时配置失败: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("关闭临时配置失败: %w", err)
	}

	runCtx, cancel := context.WithCancel(m.procCtx)
	cmd := exec.CommandContext(runCtx, m.binary, "run", "-c", path)
	stderr := &tailBuffer{max: 4096}
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.Remove(path)
		return nil, fmt.Errorf("启动 sing-box 失败: %w", err)
	}

	inst := &Instance{
		Node:       node,
		ListenAddr: net.JoinHostPort(m.cfg.ListenHost, fmt.Sprintf("%d", port)),
		ConfigPath: path,
		cmd:        cmd,
		cancel:     cancel,
		done:       make(chan struct{}),
		stderr:     stderr,
	}
	inst.ProxyURL = "http://" + inst.ListenAddr

	go func() {
		inst.exitErr = cmd.Wait()
		close(inst.done)
	}()

	if err := waitReady(ctx, inst.ListenAddr, inst.done, startupTimeout); err != nil {
		diag := strings.TrimSpace(stderr.String())
		_ = inst.Close()
		if diag != "" {
			return nil, fmt.Errorf("sing-box 未就绪 (%s): %w；内核输出: %s", inst.ListenAddr, err, firstLine(diag))
		}
		return nil, fmt.Errorf("sing-box 未就绪 (%s): %w", inst.ListenAddr, err)
	}
	return inst, nil
}

func waitReady(ctx context.Context, addr string, done <-chan struct{}, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-done:
			return fmt.Errorf("sing-box 启动后立即退出")
		default:
		}
		d := net.Dialer{Timeout: 400 * time.Millisecond}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			// 端口能连上还不够：确认不是别家进程占着，也不是内核刚崩
			select {
			case <-done:
				return fmt.Errorf("sing-box 启动后立即退出")
			default:
			}
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	if last == nil {
		last = fmt.Errorf("timeout")
	}
	return last
}

// freePort 向系统要一个当前空闲的本地端口，避免固定端口段被占用时
// 测速流量打到别的进程上。
func freePort(host string) (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("无法确定监听端口")
	}
	return addr.Port, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if t.max > 0 && len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
