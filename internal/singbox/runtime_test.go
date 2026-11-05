package singbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFreePort(t *testing.T) {
	port, err := freePort("127.0.0.1")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port = %d", port)
	}
}

// fakeSingBox 生成一个启动后立即失败的最小可执行程序，跨平台兼容。
// Windows 用 .bat（Go 的 os/exec 会自动交给 cmd.exe），Unix 用 shebang。
func fakeSingBox(t *testing.T, stderr string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	var path, body string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "fakesb.bat")
		body = "@echo " + stderr + " 1>&2\r\n" + fmt.Sprintf("@exit /b %d\r\n", exitCode)
	} else {
		path = filepath.Join(dir, "fakesb")
		body = "#!/bin/sh\necho '" + stderr + "' >&2\nexit " + fmt.Sprintf("%d", exitCode) + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStartReportsStderrOnFailure(t *testing.T) {
	bin := fakeSingBox(t, "config invalid: boom", 2)
	mgr, err := NewManager(Config{Binary: bin, WorkDir: filepath.Dir(bin)})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	_, err = mgr.Start(context.Background(), Node{Server: "1.1.1.1", UUID: "u", Host: "h"})
	if err == nil {
		t.Fatal("expected start error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should surface sing-box stderr, got %v", err)
	}
}

func TestInstanceCloseRemovesCredentialConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "singbox-test.json")
	if err := os.WriteFile(path, []byte(`{"uuid":"SECRET"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&Instance{ConfigPath: path}).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Close 未清理含凭据的临时配置")
	}
}

func TestManagerCloseCancelsProcCtx(t *testing.T) {
	bin := fakeSingBox(t, "x", 1)
	m, err := NewManager(Config{Binary: bin})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.Close()
	if m.procCtx.Err() == nil {
		t.Fatal("Manager.Close 未取消任务级 ctx，异常退出会留孤儿内核")
	}
}

func TestNewManagerCleansStaleConfigs(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "singbox-old.json")
	if err := os.WriteFile(stale, []byte(`{"uuid":"SECRET"}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := fakeSingBox(t, "x", 1)
	if _, err := NewManager(Config{Binary: bin, WorkDir: dir}); err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("上次异常退出留下的含凭据配置未被清理")
	}
}
