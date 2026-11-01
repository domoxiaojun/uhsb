package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"
)

type Config struct {
	Binary     string
	WorkDir    string
	ListenHost string
	BasePort   int
}

func (c Config) withDefaults() Config {
	if c.ListenHost == "" {
		c.ListenHost = "127.0.0.1"
	}
	if c.BasePort <= 0 {
		c.BasePort = 18080
	}
	return c
}

type Instance struct {
	Node       Node
	ListenAddr string
	ProxyURL   string
	ConfigPath string
	cmd        *exec.Cmd
}

func (i *Instance) Close() error {
	if i == nil || i.cmd == nil || i.cmd.Process == nil {
		return nil
	}
	_ = i.cmd.Process.Kill()
	_, _ = i.cmd.Process.Wait()
	if i.ConfigPath != "" {
		_ = os.Remove(i.ConfigPath)
	}
	return nil
}

type Manager struct {
	cfg    Config
	binary string
	seq    atomic.Uint32
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
	}
	return &Manager{cfg: cfg, binary: bin}, nil
}

func (m *Manager) Binary() string { return m.binary }

func (m *Manager) Start(ctx context.Context, node Node) (*Instance, error) {
	port := m.cfg.BasePort + int(m.seq.Add(1)%1000)
	listen := m.cfg.ListenHost
	cfgMap := runtimeConfig(node, listen, port)
	raw, err := json.MarshalIndent(cfgMap, "", "  ")
	if err != nil {
		return nil, err
	}
	dir := m.cfg.WorkDir
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("singbox-%d-%d.json", os.Getpid(), port))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, m.binary, "run", "-c", path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("启动 sing-box 失败: %w", err)
	}

	addr := net.JoinHostPort(listen, fmt.Sprintf("%d", port))
	if err := waitTCP(ctx, addr, 8*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sing-box 未在 %s 就绪: %w", addr, err)
	}

	return &Instance{
		Node:       node,
		ListenAddr: addr,
		ProxyURL:   "http://" + addr,
		ConfigPath: path,
		cmd:        cmd,
	}, nil
}

func waitTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d := net.Dialer{Timeout: 400 * time.Millisecond}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
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
