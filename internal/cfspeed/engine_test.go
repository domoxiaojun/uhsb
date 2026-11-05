package cfspeed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cftest/internal/cfcommon"
)

func TestNewSessionSingBoxRequiresUUID(t *testing.T) {
	_, err := newSession(Config{
		UseSingBox: true,
		Workers:    20,
	}, cfcommon.NewReporter(nil))
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("err = %v, want UUID required", err)
	}
}

func TestRunSingBoxMissingUUID(t *testing.T) {
	dir := t.TempDir()
	ipFile := filepath.Join(dir, "ips.txt")
	if err := os.WriteFile(ipFile, []byte("1.1.1.1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Config{
		Mode:       "batch",
		InputFile:  ipFile,
		UseSingBox: true,
	}, cfcommon.NewReporter(nil))
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("Run err = %v, want UUID required", err)
	}
}

// 真正走一遍 newSessionCtx，验证「启用 sing-box 时并发会被压到上限」确实生效，
// 而不是在测试里手动把 20 改成 6 再断言自己是 6。
func TestNewSessionCapsSingBoxWorkers(t *testing.T) {
	dir := t.TempDir()
	fakeBinary := filepath.Join(dir, "sing-box-fake")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := newSessionCtx(context.Background(), Config{
		Mode:           "batch",
		Workers:        20,
		UseSingBox:     true,
		VMessUUID:      "11111111-2222-3333-4444-555555555555",
		VMessHost:      "example.com",
		SingBoxBinary:  fakeBinary,
		SingBoxWorkDir: filepath.Join(dir, "sb"),
		HistoryFile:    filepath.Join(dir, "history.csv"),
	}, cfcommon.NewReporter(nil))
	if err != nil {
		t.Fatalf("newSessionCtx: %v", err)
	}
	defer s.close()
	if s.box == nil {
		t.Fatal("sing-box manager was not created")
	}
	if s.cfg.Workers != singBoxMaxWorkers {
		t.Fatalf("Workers = %d, want capped %d (runBatch/runOptimize 用的是 s.cfg.Workers)", s.cfg.Workers, singBoxMaxWorkers)
	}
}

func TestNewSessionKeepsWorkersWithoutSingBox(t *testing.T) {
	s, err := newSessionCtx(context.Background(), Config{
		Mode:        "batch",
		Workers:     20,
		HistoryFile: filepath.Join(t.TempDir(), "history.csv"),
	}, cfcommon.NewReporter(nil))
	if err != nil {
		t.Fatalf("newSessionCtx: %v", err)
	}
	defer s.close()
	if s.cfg.Workers != 20 {
		t.Fatalf("Workers = %d, want 20 unchanged", s.cfg.Workers)
	}
	if s.box != nil {
		t.Fatal("sing-box manager must not be created in direct mode")
	}
}

// 负向安全测试：含明文 UUID 的 VMessURL 绝不能进入任何 JSON/SSE/日志路径。
func TestResultEventNeverLeaksVMessURL(t *testing.T) {
	const secret = "vmess://SECRET-UUID-BLOB"
	r := TestResult{IP: "1.1.1.1", DataCenter: "HKG", DownloadMbps: 42, VMessURL: secret}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "vmess://") || strings.Contains(string(raw), "SECRET-UUID-BLOB") {
		t.Fatalf("TestResult 序列化泄露凭据: %s", raw)
	}
	ev := cfcommon.Event{Stage: "cfspeed", Kind: cfcommon.KindResult, Extra: resultEvent{
		IP: r.IP, DataCenter: r.DataCenter, DownloadMbps: r.DownloadMbps, Score: r.Score,
	}}
	frame, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(frame), "vmess://") || strings.Contains(string(frame), "UUID") {
		t.Fatalf("SSE 事件泄露凭据: %s", frame)
	}
	var msgs []string
	rep := cfcommon.NewReporter(func(e cfcommon.Event) { msgs = append(msgs, e.Message) })
	showDetailedResult(r, rep)
	for _, m := range msgs {
		if strings.Contains(m, "vmess://") || strings.Contains(m, "SECRET-UUID-BLOB") {
			t.Fatalf("日志泄露节点链接: %q", m)
		}
	}
}
