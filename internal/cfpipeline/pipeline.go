package cfpipeline

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cftest/internal/cfcommon"
	"cftest/internal/cfconnectivity"
	"cftest/internal/cfdata"
	"cftest/internal/cfscan"
	"cftest/internal/cfspeed"
)

type Config struct {
	DataMode           string
	Colo               string
	Country            string
	IPType             string
	ScanThreads        int
	TestThreads        int
	Port               int
	Delay              int
	NumPerColo         int
	Detail             bool
	SkipScan           bool
	RunConnect         bool
	SpeedMode          string
	SpeedProtocol      string
	SpeedSize          int
	SpeedWorkers       int
	SpeedRounds        int
	SpeedTimeout       int
	SpeedLoss          float64
	SpeedMinMbps       float64
	SpeedLatency       int
	ConnWorkers        int
	ConnRounds         int
	ConnTimeout        int
	ConnLoss           float64
	ConnSpeed          float64
	ConnLatency        int
	ConnSize           int
	HostHeader         string
	RootDir            string
	RunsDir            string
	UseSingBox         bool
	SingBoxBinary      string
	VMessUUID          string
	VMessHost          string
	VMessPath          string
	VMessSNI           string
	VMessPort          int
	VMessTLS           bool
	VMessAllowInsecure bool
}

func (c Config) withDefaults() Config {
	if c.DataMode == "" {
		c.DataMode = "1"
	}
	if c.IPType == "" {
		c.IPType = "4"
	}
	if c.ScanThreads <= 0 {
		c.ScanThreads = 50
	}
	if c.TestThreads <= 0 {
		c.TestThreads = 25
	}
	if c.Port <= 0 {
		c.Port = 443
	}
	if c.Delay <= 0 {
		c.Delay = 500
	}
	if c.SpeedMode == "" {
		c.SpeedMode = "batch"
	}
	if c.SpeedProtocol == "" {
		c.SpeedProtocol = "http"
	}
	if c.SpeedSize <= 0 {
		c.SpeedSize = 50
	}
	if c.SpeedWorkers <= 0 {
		c.SpeedWorkers = 20
	}
	if c.SpeedRounds <= 0 {
		c.SpeedRounds = 3
	}
	if c.SpeedTimeout <= 0 {
		c.SpeedTimeout = 3
	}
	if c.SpeedLoss == 0 {
		c.SpeedLoss = 10
	}
	if c.SpeedMinMbps == 0 {
		c.SpeedMinMbps = 50
	}
	if c.SpeedLatency <= 0 {
		c.SpeedLatency = 200
	}
	if c.ConnWorkers <= 0 {
		c.ConnWorkers = 20
	}
	if c.ConnRounds <= 0 {
		c.ConnRounds = 3
	}
	if c.ConnTimeout <= 0 {
		c.ConnTimeout = 3
	}
	if c.ConnLoss == 0 {
		c.ConnLoss = 10
	}
	if c.ConnSpeed == 0 {
		c.ConnSpeed = 100
	}
	if c.ConnLatency <= 0 {
		c.ConnLatency = 200
	}
	if c.ConnSize <= 0 {
		c.ConnSize = 200
	}
	if c.HostHeader == "" {
		c.HostHeader = "www.visa.cn"
	}
	if c.RootDir == "" {
		c.RootDir, _ = os.Getwd()
	}
	if c.RunsDir == "" {
		c.RunsDir = filepath.Join(c.RootDir, "runs")
	}
	return c
}

type Result struct {
	RunDir       string
	Tag          string
	IPsFile      string
	QualityFile  string
	SpeedPrefix  string
	SkippedScan  bool
	Data         *cfdata.Result
	Scan         *cfscan.Result
	Speed        *cfspeed.Result
	Connectivity *cfconnectivity.Result
}

func Run(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	cfg = cfg.withDefaults()
	if err := os.MkdirAll(cfg.RunsDir, 0755); err != nil {
		return nil, err
	}
	runDir := filepath.Join(cfg.RunsDir, time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, err
	}
	tag := buildTag(cfg)
	out := &Result{RunDir: runDir, Tag: tag}
	reporter.Stage("pipeline", "[信息] 本次运行目录: %s", runDir)

	dataRes, err := cfdata.Run(ctx, cfdata.Config{
		Mode:        cfg.DataMode,
		Colo:        cfg.Colo,
		Country:     cfg.Country,
		IPType:      cfg.IPType,
		Port:        cfg.Port,
		Delay:       cfg.Delay,
		ScanThreads: cfg.ScanThreads,
		TestThreads: cfg.TestThreads,
		NumPerColo:  cfg.NumPerColo,
		Detail:      cfg.Detail,
		HostHeader:  cfg.HostHeader,
		WorkDir:     runDir,
	}, reporter)
	if err != nil {
		return out, err
	}
	out.Data = dataRes
	ipsFile := firstNonEmpty(dataRes.ZeroLossIPFile, dataRes.IPFile)
	if ipsFile == "" {
		return out, fmt.Errorf("cfdata 没有产出 IP 文件")
	}
	out.IPsFile = ipsFile
	if ctx.Err() != nil {
		return out, ctx.Err()
	}

	skipScan := cfg.SkipScan || !cfscan.MTRAvailable()
	speedInput := ipsFile
	if skipScan {
		out.SkippedScan = true
		if !cfscan.MTRAvailable() {
			reporter.Warn("pipeline", "本机没有 mtr，已跳过 cfscan，直接测速")
		} else {
			reporter.Log("pipeline", "已按设置跳过 cfscan")
		}
	} else {
		prefix := filepath.Join(runDir, tag)
		scanWorkers := 16
		scanRes, err := cfscan.Run(ctx, cfscan.Config{
			InputFile:  ipsFile,
			OutputPfx:  prefix,
			HostHeader: cfg.HostHeader,
			Workers:    scanWorkers,
		}, reporter)
		if err != nil {
			return out, err
		}
		out.Scan = scanRes
		if scanRes == nil || scanRes.QualityFile == "" || len(scanRes.Hits) == 0 {
			return out, fmt.Errorf("cfscan 未产出质量 IP（命中 0），已停止测速，避免改测 cfdata 全量")
		}
		speedInput = scanRes.QualityFile
		out.QualityFile = scanRes.QualityFile
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}

	speedPrefix := filepath.Join(runDir, tag+"_speed")
	out.SpeedPrefix = speedPrefix
	speedRes, err := cfspeed.Run(ctx, cfspeed.Config{
		Mode:               cfg.SpeedMode,
		Protocol:           cfg.SpeedProtocol,
		InputFile:          speedInput,
		OutputPrefix:       speedPrefix,
		HistoryFile:        filepath.Join(runDir, "speed_test_results.csv"),
		HostHeader:         cfg.HostHeader,
		Workers:            cfg.SpeedWorkers,
		TestRounds:         cfg.SpeedRounds,
		ConnectTimeout:     cfg.SpeedTimeout,
		MaxLatency:         cfg.SpeedLatency,
		DownloadSizeMB:     cfg.SpeedSize,
		MaxPacketLoss:      cfg.SpeedLoss,
		MinSpeedMbps:       cfg.SpeedMinMbps,
		UseSingBox:         cfg.UseSingBox,
		SingBoxBinary:      cfg.SingBoxBinary,
		SingBoxWorkDir:     filepath.Join(runDir, "singbox"),
		VMessUUID:          cfg.VMessUUID,
		VMessHost:          cfg.VMessHost,
		VMessPath:          cfg.VMessPath,
		VMessSNI:           cfg.VMessSNI,
		VMessPort:          cfg.VMessPort,
		VMessTLS:           cfg.VMessTLS,
		VMessAllowInsecure: cfg.VMessAllowInsecure,
	}, reporter)
	if err != nil {
		return out, err
	}
	out.Speed = speedRes

	if cfg.RunConnect && ctx.Err() == nil {
		connInput := speedInput
		if speedRes != nil && speedRes.PremiumFile != "" {
			if cleaned := writePremiumIPs(speedRes.PremiumFile, filepath.Join(runDir, tag+"_premium_ips.txt")); cleaned != "" {
				connInput = cleaned
			}
		}
		connRes, err := cfconnectivity.Run(ctx, cfconnectivity.Config{
			InputFile:      connInput,
			OutputPrefix:   filepath.Join(runDir, tag+"_connectivity"),
			Workers:        cfg.ConnWorkers,
			TestRounds:     cfg.ConnRounds,
			ConnectTimeout: cfg.ConnTimeout,
			DownloadSize:   cfg.ConnSize,
			MaxLatency:     cfg.ConnLatency,
			MaxPacketLoss:  cfg.ConnLoss,
			MinSpeed:       cfg.ConnSpeed,
		}, reporter)
		if err != nil {
			return out, err
		}
		out.Connectivity = connRes
	}

	reporter.Stage("pipeline", "[完成] 运行目录 %s", runDir)
	return out, nil
}

func buildTag(cfg Config) string {
	raw := cfg.Colo
	if cfg.DataMode == "3" {
		raw = cfg.Country
	}
	parts := cfdata.ParseColoList(raw)
	if len(parts) == 0 {
		return "run"
	}
	joined := strings.Join(parts, "_")
	if cfg.DataMode == "3" {
		return "country_" + joined
	}
	return joined
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			if st, err := os.Stat(v); err == nil && st.Size() > 0 {
				return v
			}
		}
	}
	return ""
}

func writePremiumIPs(src, dest string) string {
	f, err := os.Open(src)
	if err != nil {
		return ""
	}
	defer f.Close()
	var ips []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			ips = append(ips, fields[0])
		}
	}
	if len(ips) == 0 {
		return ""
	}
	if err := cfcommon.WriteLinesAtomic(dest, ips); err != nil {
		return ""
	}
	return dest
}

func ListRuns(runsDir string) ([]string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			names = append(names, entries[i].Name())
		}
	}
	return names, nil
}
