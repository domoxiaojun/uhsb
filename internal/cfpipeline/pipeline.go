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
	HighQualityHops    int
	ScanKeywords       string
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
		c.DataMode = cfcommon.DefaultDataMode
	}
	if c.IPType == "" {
		c.IPType = cfcommon.DefaultIPType
	}
	if c.ScanThreads <= 0 {
		c.ScanThreads = cfcommon.DefaultScanThreads
	}
	if c.TestThreads <= 0 {
		c.TestThreads = cfcommon.DefaultTestThreads
	}
	if c.Port <= 0 {
		c.Port = cfcommon.DefaultPort
	}
	if c.Delay <= 0 {
		c.Delay = cfcommon.DefaultDelay
	}
	if c.SpeedMode == "" {
		c.SpeedMode = cfcommon.DefaultSpeedMode
	}
	if c.SpeedProtocol == "" {
		c.SpeedProtocol = cfcommon.DefaultSpeedProtocol
	}
	if c.SpeedSize <= 0 {
		c.SpeedSize = cfcommon.DefaultSpeedSize
	}
	if c.SpeedWorkers <= 0 {
		c.SpeedWorkers = cfcommon.DefaultWorkers
	}
	if c.SpeedRounds <= 0 {
		c.SpeedRounds = cfcommon.DefaultTestRounds
	}
	if c.SpeedTimeout <= 0 {
		c.SpeedTimeout = cfcommon.DefaultConnectTimeout
	}
	// 负数为非法值；0 是调用方显式传入的「关闭该门槛」，必须原样透传给引擎。
	if c.SpeedLoss < 0 {
		c.SpeedLoss = cfcommon.DefaultMaxPacketLoss
	}
	if c.SpeedMinMbps < 0 {
		c.SpeedMinMbps = cfcommon.DefaultMinSpeedMbps
	}
	if c.SpeedLatency <= 0 {
		c.SpeedLatency = cfcommon.DefaultMaxLatency
	}
	if c.HighQualityHops == 0 {
		// 0 = 用默认阈值；负数 = 关闭"高跳数视为优质"的启发式
		c.HighQualityHops = cfcommon.DefaultHighQualityHops
	}
	if c.ScanKeywords == "" {
		c.ScanKeywords = cfcommon.DefaultKeywords
	}
	if c.ConnWorkers <= 0 {
		c.ConnWorkers = cfcommon.DefaultConnWorkers
	}
	if c.ConnRounds <= 0 {
		c.ConnRounds = cfcommon.DefaultConnRounds
	}
	if c.ConnTimeout <= 0 {
		c.ConnTimeout = cfcommon.DefaultConnTimeout
	}
	if c.ConnLoss < 0 {
		c.ConnLoss = cfcommon.DefaultConnLoss
	}
	if c.ConnSpeed < 0 {
		c.ConnSpeed = cfcommon.DefaultConnSpeed
	}
	if c.ConnLatency <= 0 {
		c.ConnLatency = cfcommon.DefaultConnLatency
	}
	if c.ConnSize <= 0 {
		c.ConnSize = cfcommon.DefaultConnSize
	}
	if c.HostHeader == "" {
		c.HostHeader = cfcommon.DefaultHost
	}
	if c.RootDir == "" {
		c.RootDir, _ = os.Getwd()
	}
	if c.RunsDir == "" {
		c.RunsDir = filepath.Join(c.RootDir, "runs")
	}
	return c
}

// qualityFromScan 把 cfscan 结果转成测速输入。
// 命中 0 时必须报错停止，绝不能悄悄退回 cfdata 的全量 IP 列表去测速。
func qualityFromScan(res *cfscan.Result) (string, error) {
	if res == nil || res.QualityFile == "" || len(res.Hits) == 0 {
		return "", fmt.Errorf("cfscan 未产出质量 IP（命中 0），已停止测速，避免改测 cfdata 全量")
	}
	return res.QualityFile, nil
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
	// 时间戳只精确到秒，同一秒内的两次运行（或手工塞了同名目录）会互相覆盖产物。
	// 用 os.Mkdir 的原子性挑一个不存在的目录名。
	base := time.Now().Format("20060102_150405")
	runDir := filepath.Join(cfg.RunsDir, base)
	for i := 2; ; i++ {
		err := os.Mkdir(runDir, 0755)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return nil, err
		}
		runDir = filepath.Join(cfg.RunsDir, fmt.Sprintf("%s-%d", base, i))
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
		CacheDir:    cfg.RootDir,
	}, reporter)
	if err != nil {
		return out, err
	}
	out.Data = dataRes
	ipsFile := firstNonEmpty(dataRes.ZeroLossIPFile, dataRes.IPFile)
	if ipsFile == "" {
		// 用户点「停止」时 cfdata 可能只返回空结果而不带 error，
		// 此时优先报「已取消」，不要误报成「没有产出 IP 文件」。
		if ctx.Err() != nil {
			return out, fmt.Errorf("任务已取消: %w", ctx.Err())
		}
		return out, fmt.Errorf("cfdata 没有产出 IP 文件")
	}
	out.IPsFile = ipsFile
	if ctx.Err() != nil {
		return out, ctx.Err()
	}

	// 只探测一次，并把结果同时用于「能否扫」和日志说明，避免重复 fork mtr。
	_, mtrErr := cfscan.MTRStatusCtx(ctx)
	skipScan := cfg.SkipScan || mtrErr != nil
	speedInput := ipsFile
	if skipScan {
		out.SkippedScan = true
		if mtrErr != nil {
			reporter.Warn("pipeline", "mtr 不可用（%v），已跳过 cfscan，直接测速", mtrErr)
		} else {
			reporter.Log("pipeline", "已按设置跳过 cfscan")
		}
	} else {
		prefix := filepath.Join(runDir, tag)
		scanRes, err := cfscan.Run(ctx, cfscan.Config{
			InputFile:       ipsFile,
			OutputPfx:       prefix,
			HostHeader:      cfg.HostHeader,
			Workers:         cfcommon.PipelineScanWorkers,
			HighQualityHops: cfg.HighQualityHops,
			Keywords:        cfg.ScanKeywords,
		}, reporter)
		if err != nil {
			return out, err
		}
		out.Scan = scanRes
		qualityFile, err := qualityFromScan(scanRes)
		if err != nil {
			return out, err
		}
		speedInput = qualityFile
		out.QualityFile = qualityFile
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
		Port:               cfg.Port,
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
			if len(names) >= cfcommon.MaxRunList {
				break // /api/runs 不该把几百次历史一次全吐给浏览器
			}
		}
	}
	return names, nil
}
