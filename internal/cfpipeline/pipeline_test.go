package cfpipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"cftest/internal/cfcommon"
	"cftest/internal/cfscan"
)

// 直接验证编排层「命中 0 就停」的决策函数，而不是构造一个空结构体自证。
func TestQualityFromScanRejectsMiss(t *testing.T) {
	cases := []struct {
		name string
		res  *cfscan.Result
	}{
		{"nil", nil},
		{"empty", &cfscan.Result{}},
		{"file but no hits", &cfscan.Result{QualityFile: "q.txt"}},
		{"hits but no file", &cfscan.Result{Hits: []cfscan.CategorizedResult{{IP: "1.1.1.1"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := qualityFromScan(tc.res)
			if err == nil {
				t.Fatalf("qualityFromScan() = %q, want error (must not fall back to all IPs)", file)
			}
			if file != "" {
				t.Fatalf("qualityFromScan() returned file %q together with error", file)
			}
		})
	}
	file, err := qualityFromScan(&cfscan.Result{
		QualityFile: "quality.txt",
		Hits:        []cfscan.CategorizedResult{{IP: "1.1.1.1"}},
	})
	if err != nil || file != "quality.txt" {
		t.Fatalf("qualityFromScan(valid) = %q, %v", file, err)
	}
}

func TestSkipScanKeepsDataIPs(t *testing.T) {
	cfg := Config{SkipScan: true}.withDefaults()
	if !cfg.SkipScan {
		t.Fatal("SkipScan should stay true")
	}
}

func TestDefaultsComeFromCommon(t *testing.T) {
	cfg := Config{}.withDefaults()
	checks := map[string]struct{ got, want int }{
		"Port":            {cfg.Port, cfcommon.DefaultPort},
		"Delay":           {cfg.Delay, cfcommon.DefaultDelay},
		"ScanThreads":     {cfg.ScanThreads, cfcommon.DefaultScanThreads},
		"TestThreads":     {cfg.TestThreads, cfcommon.DefaultTestThreads},
		"SpeedSize":       {cfg.SpeedSize, cfcommon.DefaultSpeedSize},
		"SpeedWorkers":    {cfg.SpeedWorkers, cfcommon.DefaultWorkers},
		"SpeedRounds":     {cfg.SpeedRounds, cfcommon.DefaultTestRounds},
		"SpeedTimeout":    {cfg.SpeedTimeout, cfcommon.DefaultConnectTimeout},
		"SpeedLatency":    {cfg.SpeedLatency, cfcommon.DefaultMaxLatency},
		"HighQualityHops": {cfg.HighQualityHops, cfcommon.DefaultHighQualityHops},
		"ConnWorkers":     {cfg.ConnWorkers, cfcommon.DefaultConnWorkers},
		"ConnRounds":      {cfg.ConnRounds, cfcommon.DefaultConnRounds},
		"ConnTimeout":     {cfg.ConnTimeout, cfcommon.DefaultConnTimeout},
		"ConnLatency":     {cfg.ConnLatency, cfcommon.DefaultConnLatency},
		"ConnSize":        {cfg.ConnSize, cfcommon.DefaultConnSize},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want cfcommon default %d", name, c.got, c.want)
		}
	}
	if cfg.DataMode != cfcommon.DefaultDataMode || cfg.IPType != cfcommon.DefaultIPType ||
		cfg.SpeedMode != cfcommon.DefaultSpeedMode || cfg.SpeedProtocol != cfcommon.DefaultSpeedProtocol ||
		cfg.ScanKeywords != cfcommon.DefaultKeywords || cfg.HostHeader != cfcommon.DefaultHost {
		t.Fatalf("string defaults drifted: %+v", cfg)
	}
}

func TestDefaultsHighQualityHopsAndPort(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.HighQualityHops != cfcommon.DefaultHighQualityHops {
		t.Fatalf("HighQualityHops = %d, want %d", cfg.HighQualityHops, cfcommon.DefaultHighQualityHops)
	}
	if cfg.Port != cfcommon.DefaultPort {
		t.Fatalf("Port = %d, want %d", cfg.Port, cfcommon.DefaultPort)
	}
	neg := Config{HighQualityHops: -1}.withDefaults()
	if neg.HighQualityHops != -1 {
		t.Fatalf("HighQualityHops = %d, want -1 to disable", neg.HighQualityHops)
	}
}

func TestZeroThresholdsSurviveWithDefaults(t *testing.T) {
	// WebUI 显式传 0 表示「关闭该门槛」，编排层不能把它重置成默认值。
	cfg := Config{
		SpeedMinMbps: 0,
		SpeedLoss:    0,
		ConnSpeed:    0,
		ConnLoss:     0,
	}.withDefaults()
	if cfg.SpeedMinMbps != 0 || cfg.SpeedLoss != 0 || cfg.ConnSpeed != 0 || cfg.ConnLoss != 0 {
		t.Fatalf("zero thresholds were overwritten: %+v", cfg)
	}
}

func TestListRunsCapsResults(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < cfcommon.MaxRunList+5; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("20260101_0000%02d", i)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	names, err := ListRuns(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != cfcommon.MaxRunList {
		t.Fatalf("ListRuns = %d entries, want cap %d", len(names), cfcommon.MaxRunList)
	}
}
