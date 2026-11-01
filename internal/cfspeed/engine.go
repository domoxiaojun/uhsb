package cfspeed

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cftest/internal/cfcommon"
	"cftest/internal/singbox"
)

const (
	DefaultSpeedHost = "cftest.domob.org"
	TraceHost        = "speed.cloudflare.com"
	DefaultHost      = "www.visa.cn"
	HistoryHeaderLen = 11
)

type Config struct {
	Mode                  string
	Protocol              string
	InputFile             string
	SingleIP              string
	SpeedHost             string
	HistoryFile           string
	OutputPrefix          string
	HostHeader            string
	ColoFilter            string
	Workers               int
	TestRounds            int
	ConnectTimeout        int
	MaxLatency            int
	DownloadSizeMB        int
	ColoRetries           int
	SingleSpeedTimeoutSec int
	MaxPacketLoss         float64
	MinSpeedMbps          float64
	JitterBaseline        float64
	EarlyMinSpeed         float64
	BandwidthGoal         float64
	EarlyCheckDur         time.Duration
	Verbose               bool
	UseSingBox            bool
	SingBoxBinary         string
	SingBoxWorkDir        string
	VMessUUID             string
	VMessHost             string
	VMessPath             string
	VMessSNI              string
	VMessPort             int
	VMessTLS              bool
	VMessAllowInsecure    bool
}

func (c Config) withDefaults() Config {
	if c.Mode == "" {
		c.Mode = "batch"
	}
	c.Mode = NormalizeMode(c.Mode)
	c.Protocol = NormalizeProtocol(c.Protocol)
	if c.InputFile == "" {
		c.InputFile = "speed.txt"
	}
	if c.SpeedHost == "" {
		c.SpeedHost = DefaultSpeedHost
	}
	if c.HistoryFile == "" {
		c.HistoryFile = "speed_test_results.csv"
	}
	if c.OutputPrefix == "" {
		c.OutputPrefix = "cfspeed"
	}
	if c.HostHeader == "" {
		c.HostHeader = DefaultHost
	}
	if c.Workers <= 0 {
		c.Workers = 20
	}
	if c.TestRounds <= 0 {
		c.TestRounds = 3
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = 3
	}
	if c.MaxLatency <= 0 {
		c.MaxLatency = 200
	}
	if c.DownloadSizeMB <= 0 {
		c.DownloadSizeMB = 200
	}
	if c.ColoRetries < 0 {
		c.ColoRetries = 1
	}
	if c.SingleSpeedTimeoutSec <= 0 {
		c.SingleSpeedTimeoutSec = 30
	}
	if c.MaxPacketLoss == 0 {
		c.MaxPacketLoss = 10
	}
	if c.MinSpeedMbps == 0 {
		c.MinSpeedMbps = 50
	}
	if c.JitterBaseline == 0 {
		c.JitterBaseline = 100
	}
	if c.EarlyMinSpeed == 0 {
		c.EarlyMinSpeed = 10
	}
	if c.EarlyCheckDur == 0 {
		c.EarlyCheckDur = 3 * time.Second
	}
	if c.VMessPort <= 0 {
		c.VMessPort = 443
	}
	if c.VMessPath == "" {
		c.VMessPath = "/"
	}
	if c.VMessSNI == "" {
		c.VMessSNI = c.VMessHost
	}
	return c
}

type TestResult struct {
	IP            string
	DataCenter    string
	Location      string
	PublicIP      string
	Protocol      string
	Country       string
	Region        string
	City          string
	Lat           float64
	Lon           float64
	RawLoc        string
	BandwidthGoal float64
	AvgLatency    time.Duration
	MinLatency    time.Duration
	MaxLatency    time.Duration
	PacketLoss    float64
	DownloadMbps  float64
	Score         float64
	TestTime      time.Time
	VMessURL      string
}

type Result struct {
	Results     []TestResult
	Premium     []TestResult
	ResultsCSV  string
	PremiumFile string
	ReportFile  string
	HistoryFile string
	VMessFile   string
	ClientJSON  string
}

func NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "optimize", "single", "history":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "batch"
	}
}

func NormalizeProtocol(protocol string) string {
	if strings.EqualFold(strings.TrimSpace(protocol), "https") {
		return "https"
	}
	return "http"
}

func BuildDownloadURL(protocol, host string, sizeMB int) string {
	if strings.EqualFold(host, TraceHost) {
		return fmt.Sprintf("%s://%s/__down?bytes=%d", protocol, host, int64(sizeMB)*1024*1024)
	}
	return fmt.Sprintf("%s://%s/%dm", protocol, host, sizeMB)
}

func TrimRecordBOM(record []string) []string {
	if len(record) > 0 {
		record[0] = strings.TrimPrefix(record[0], "\ufeff")
	}
	return record
}

func Run(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	cfg = cfg.withDefaults()
	switch cfg.Mode {
	case "history":
		return runHistory(cfg, reporter)
	case "single":
		return runSingle(ctx, cfg, reporter)
	case "optimize":
		ips, err := cfcommon.ReadIPs(cfg.InputFile)
		if err != nil {
			return nil, err
		}
		return runOptimize(ctx, cfg, ips, reporter)
	default:
		ips, err := cfcommon.ReadIPs(cfg.InputFile)
		if err != nil {
			return nil, err
		}
		return runBatch(ctx, cfg, ips, reporter)
	}
}

type session struct {
	cfg      Config
	reporter cfcommon.Reporter
	locMap   map[string]cfcommon.Location
	coloList []string
	history  *historyRecorder
	box      *singbox.Manager
}

func newSession(cfg Config, reporter cfcommon.Reporter) (*session, error) {
	s := &session{cfg: cfg, reporter: reporter}
	if m, err := cfcommon.LoadLocations(); err != nil {
		reporter.Warn("cfspeed", "%v", err)
	} else {
		s.locMap = m
		reporter.Log("cfspeed", "[信息] 已加载 %d 个位置", len(m))
	}
	for _, c := range strings.Split(cfg.ColoFilter, ",") {
		if c = strings.TrimSpace(c); c != "" {
			s.coloList = append(s.coloList, strings.ToUpper(c))
		}
	}
	if cfg.Mode != "history" {
		if rec, err := newHistoryRecorder(cfg.HistoryFile); err != nil {
			reporter.Warn("cfspeed", "无法启用历史结果实时保存: %v", err)
		} else {
			s.history = rec
		}
	}
	if cfg.UseSingBox {
		if strings.TrimSpace(cfg.VMessUUID) == "" || strings.TrimSpace(cfg.VMessHost) == "" {
			return nil, fmt.Errorf("已启用 sing-box，必须提供 UUID 和 WS Host，拒绝回退直连")
		}
		mgr, err := singbox.NewManager(singbox.Config{
			Binary:  cfg.SingBoxBinary,
			WorkDir: cfg.SingBoxWorkDir,
		})
		if err != nil {
			return nil, fmt.Errorf("sing-box 不可用: %w", err)
		}
		s.box = mgr
		reporter.Log("cfspeed", "[信息] 测速走 sing-box VMess+WS (%s) host=%s path=%s tls=%v",
			mgr.Binary(), cfg.VMessHost, cfg.VMessPath, cfg.VMessTLS)
		if s.cfg.Workers > 6 {
			reporter.Warn("cfspeed", "sing-box 测速并发从 %d 降到 6，避免本机拉起过多内核", s.cfg.Workers)
			s.cfg.Workers = 6
		}
	}
	return s, nil
}

func (s *session) close() {
	if s.history != nil {
		_ = s.history.Close()
	}
}

func (s *session) fetchColo(ctx context.Context, ip string) string {
	attempts := 1 + s.cfg.ColoRetries
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		colo, err := cfcommon.DialAndGetColo(ctx, ip, s.cfg.HostHeader, 2*time.Second, 3*time.Second)
		if err == nil && colo != "" {
			return strings.ToUpper(colo)
		}
	}
	return ""
}

func (s *session) matchesColo(colo string) bool {
	if len(s.coloList) == 0 {
		return true
	}
	for _, c := range s.coloList {
		if strings.EqualFold(c, colo) {
			return true
		}
	}
	return false
}

func (s *session) applyLocation(result *TestResult, colo string) {
	result.DataCenter = strings.ToUpper(colo)
	if s.locMap == nil {
		return
	}
	if loc, ok := s.locMap[result.DataCenter]; ok {
		result.City = loc.City
		result.Region = loc.Region
		result.Country = loc.CCA2
		result.Lat = loc.Lat
		result.Lon = loc.Lon
		result.Location = strings.Trim(strings.Join([]string{loc.City, loc.Region}, ", "), ", ")
	}
	if result.Location == "" {
		result.Location = result.DataCenter
	}
}

func (s *session) applyTrace(ctx context.Context, result *TestResult) {
	info, err := cfcommon.FetchTraceMap(ctx, result.IP, fmt.Sprintf("%s://%s/cdn-cgi/trace", s.cfg.Protocol, TraceHost), 5*time.Second)
	if err != nil || info == nil {
		return
	}
	if v := info["ip"]; v != "" {
		result.PublicIP = v
	}
	if v := info["loc"]; v != "" {
		result.RawLoc = v
	}
	if colo := strings.ToUpper(info["colo"]); colo != "" {
		s.applyLocation(result, colo)
	}
}

func (s *session) latencyRounds(ctx context.Context, ip string) ([]time.Duration, float64) {
	var latencies []time.Duration
	timeout := time.Duration(s.cfg.ConnectTimeout) * time.Second
	for i := 0; i < s.cfg.TestRounds; i++ {
		if ctx.Err() != nil {
			return latencies, 100
		}
		roundCtx, cancel := context.WithTimeout(ctx, timeout)
		lat, err := cfcommon.MeasureTCPLatency(roundCtx, ip, "443", timeout)
		cancel()
		if err == nil {
			latencies = append(latencies, lat)
			continue
		}
		if i == 0 {
			break
		}
	}
	loss := 100.0
	if s.cfg.TestRounds > 0 {
		loss = float64(s.cfg.TestRounds-len(latencies)) / float64(s.cfg.TestRounds) * 100
	}
	return latencies, loss
}

func summarizeLatencies(result *TestResult, latencies []time.Duration) {
	if len(latencies) == 0 {
		return
	}
	result.MinLatency = latencies[0]
	result.MaxLatency = latencies[0]
	var sum time.Duration
	for _, lat := range latencies {
		sum += lat
		if lat < result.MinLatency {
			result.MinLatency = lat
		}
		if lat > result.MaxLatency {
			result.MaxLatency = lat
		}
	}
	result.AvgLatency = sum / time.Duration(len(latencies))
}

func (s *session) calculateScore(r TestResult) float64 {
	latMs := float64(r.AvgLatency.Milliseconds())
	latScore := math.Max(0, 40*(1-latMs/float64(s.cfg.MaxLatency)))
	speedScore := math.Min(35, r.DownloadMbps/(s.cfg.MinSpeedMbps)*35)
	jitter := float64(r.MaxLatency-r.MinLatency) / float64(time.Millisecond)
	stabilityScore := math.Max(0, 15*(1-jitter/(s.cfg.JitterBaseline)))
	lossBase := math.Max(s.cfg.MaxPacketLoss, 1)
	lossScore := math.Max(0, 10*(1-r.PacketLoss/lossBase))
	return latScore + speedScore + stabilityScore + lossScore
}

func (s *session) isPremium(r TestResult) bool {
	return r.DownloadMbps >= s.cfg.MinSpeedMbps &&
		r.AvgLatency.Milliseconds() <= int64(s.cfg.MaxLatency) &&
		r.PacketLoss <= s.cfg.MaxPacketLoss
}

func (s *session) vmessNode(ip, colo string) singbox.Node {
	tag := ip
	if colo != "" {
		tag = colo + "-" + ip
	}
	return singbox.Node{
		Server:     ip,
		UUID:       s.cfg.VMessUUID,
		Host:       s.cfg.VMessHost,
		Path:       s.cfg.VMessPath,
		SNI:        s.cfg.VMessSNI,
		Port:       s.cfg.VMessPort,
		TLS:        s.cfg.VMessTLS,
		AllowInsec: s.cfg.VMessAllowInsecure,
		Tag:        tag,
	}.ForIP(ip, tag)
}

func (s *session) attachNode(result *TestResult) {
	if s.box == nil || s.cfg.VMessUUID == "" || s.cfg.VMessHost == "" {
		return
	}
	result.VMessURL = s.vmessNode(result.IP, result.DataCenter).VMessURL()
}

func (s *session) measureSpeed(ctx context.Context, ip string, timeout time.Duration) (float64, error) {
	speedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := BuildDownloadURL(s.cfg.Protocol, s.cfg.SpeedHost, s.cfg.DownloadSizeMB)
	if s.box != nil && s.cfg.VMessUUID != "" && s.cfg.VMessHost != "" {
		inst, err := s.box.Start(speedCtx, s.vmessNode(ip, ""))
		if err != nil {
			return 0, err
		}
		defer inst.Close()
		speed, _, err := cfcommon.MeasureDownloadSpeedViaProxy(speedCtx, inst.ProxyURL, target,
			timeout, 0, s.cfg.EarlyCheckDur, s.cfg.EarlyMinSpeed, 256*1024)
		return speed, err
	}
	speed, _, err := cfcommon.MeasureDownloadSpeed(speedCtx, ip, target,
		timeout, 0, s.cfg.EarlyCheckDur, s.cfg.EarlyMinSpeed, 256*1024)
	return speed, err
}

func (s *session) testBatchCandidate(ctx context.Context, ip string) (TestResult, bool) {
	result := TestResult{IP: ip, Protocol: strings.ToUpper(s.cfg.Protocol), TestTime: time.Now()}
	colo := s.fetchColo(ctx, ip)
	if colo == "" || !s.matchesColo(colo) {
		return result, false
	}
	s.applyLocation(&result, colo)
	latencies, loss := s.latencyRounds(ctx, ip)
	if len(latencies) == 0 {
		return result, false
	}
	result.PacketLoss = loss
	if result.PacketLoss > s.cfg.MaxPacketLoss {
		return result, false
	}
	summarizeLatencies(&result, latencies)
	if result.AvgLatency.Milliseconds() > int64(s.cfg.MaxLatency) {
		return result, false
	}
	speed, err := s.measureSpeed(ctx, ip, 30*time.Second)
	if err != nil {
		return result, false
	}
	result.DownloadMbps = speed
	result.Score = s.calculateScore(result)
	s.attachNode(&result)
	return result, true
}

func runBatch(ctx context.Context, cfg Config, ips []string, reporter cfcommon.Reporter) (*Result, error) {
	s, err := newSession(cfg, reporter)
	if err != nil {
		return nil, err
	}
	defer s.close()

	saver, err := newBatchOutputSaver(s.cfg)
	if err != nil {
		reporter.Warn("cfspeed", "无法启用批量结果实时保存: %v", err)
	}

	var results []TestResult
	var mu sync.Mutex
	progress := cfcommon.NewProgressWithHandler(len(ips), 500*time.Millisecond, reporter.ProgressHandler("cfspeed"))
	sem := make(chan struct{}, s.cfg.Workers)
	var wg sync.WaitGroup

launch:
	for _, ip := range ips {
		if ctx.Err() != nil {
			break
		}
		if !cfcommon.AcquireSemaphore(ctx, sem) {
			break launch
		}
		wg.Add(1)
		go func(ip string) {
			defer func() { <-sem; wg.Done(); progress.Inc() }()
			result, ok := s.testBatchCandidate(ctx, ip)
			if !ok {
				return
			}
			if s.history != nil {
				_ = s.history.Write(result)
			}
			if saver != nil {
				saver.Write(result, s.isPremium(result))
			}
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
			reporter.Emit(cfcommon.Event{
				Stage:   "cfspeed",
				Kind:    cfcommon.KindResult,
				Message: fmt.Sprintf("%s → %.0fms %.1fMbps %s", ip, float64(result.AvgLatency.Milliseconds()), result.DownloadMbps, result.DataCenter),
				IP:      ip,
				Extra:   result,
			})
			progress.Hit(fmt.Sprintf("%s → %.0fms %.1fMbps %s", ip, float64(result.AvgLatency.Milliseconds()), result.DownloadMbps, result.DataCenter))
		}(ip)
	}
	wg.Wait()
	progress.Stop()
	if saver != nil {
		_ = saver.Close()
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	out := &Result{Results: results, HistoryFile: cfg.HistoryFile}
	if len(results) == 0 {
		reporter.Log("cfspeed", "[完成] 无有效结果")
		return out, nil
	}
	var premium []TestResult
	for _, r := range results {
		if s.isPremium(r) {
			premium = append(premium, r)
		}
	}
	out.Premium = premium
	out.ResultsCSV = cfg.OutputPrefix + "_results.csv"
	saveAllCSV(results, out.ResultsCSV, reporter)
	if len(premium) > 0 {
		out.PremiumFile = cfg.OutputPrefix + "_premium.txt"
		savePremiumTxt(premium, out.PremiumFile, reporter)
	}
	saveVMessExports(cfg, premium, out, reporter)
	out.ReportFile = cfg.OutputPrefix + "_report.txt"
	generateReport(cfg, results, premium, out.ReportFile, reporter)
	showSummary(results, premium, reporter)
	return out, nil
}

type latencyProbe struct {
	IP      string
	Latency time.Duration
}

func runOptimize(ctx context.Context, cfg Config, ips []string, reporter cfcommon.Reporter) (*Result, error) {
	s, err := newSession(cfg, reporter)
	if err != nil {
		return nil, err
	}
	defer s.close()
	if len(ips) == 0 {
		return nil, fmt.Errorf("IP列表为空")
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(ips), func(i, j int) { ips[i], ips[j] = ips[j], ips[i] })
	if len(ips) > 200 {
		ips = ips[:200]
	}

	var candidates []latencyProbe
	var mu sync.Mutex
	var completed atomic.Int64
	sem := make(chan struct{}, s.cfg.Workers)
	var wg sync.WaitGroup
	progress := cfcommon.NewProgressWithHandler(len(ips), 500*time.Millisecond, reporter.ProgressHandler("cfspeed"))

launch:
	for _, ip := range ips {
		if !cfcommon.AcquireSemaphore(ctx, sem) {
			break launch
		}
		wg.Add(1)
		go func(ip string) {
			defer func() { <-sem; wg.Done(); progress.Inc() }()
			timeout := time.Duration(cfg.ConnectTimeout) * time.Second
			roundCtx, cancel := context.WithTimeout(ctx, timeout)
			lat, err := cfcommon.MeasureTCPLatency(roundCtx, ip, "443", timeout)
			cancel()
			completed.Add(1)
			if err == nil {
				mu.Lock()
				candidates = append(candidates, latencyProbe{IP: ip, Latency: lat})
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	progress.Stop()
	if len(candidates) == 0 {
		return nil, fmt.Errorf("所有IP延迟测试失败")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Latency < candidates[j].Latency })

	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("测试已中断")
		}
		colo := s.fetchColo(ctx, candidate.IP)
		if colo == "" || !s.matchesColo(colo) {
			continue
		}
		speed, err := s.measureSpeed(ctx, candidate.IP, 30*time.Second)
		if err != nil || speed < cfg.MinSpeedMbps {
			continue
		}
		result := TestResult{
			IP: candidate.IP, Protocol: strings.ToUpper(cfg.Protocol),
			AvgLatency: candidate.Latency, MinLatency: candidate.Latency, MaxLatency: candidate.Latency,
			DownloadMbps: speed, BandwidthGoal: cfg.BandwidthGoal, TestTime: time.Now(),
		}
		s.applyLocation(&result, colo)
		s.applyTrace(ctx, &result)
		s.attachNode(&result)
		if s.history != nil {
			_ = s.history.Write(result)
		}
		showDetailedResult(result, reporter)
		out := &Result{Results: []TestResult{result}, Premium: []TestResult{result}, HistoryFile: cfg.HistoryFile}
		saveVMessExports(cfg, []TestResult{result}, out, reporter)
		return out, nil
	}
	return nil, fmt.Errorf("未找到满足条件的IP")
}

func runSingle(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	s, err := newSession(cfg, reporter)
	if err != nil {
		return nil, err
	}
	defer s.close()
	ip := strings.TrimSpace(cfg.SingleIP)
	if ip == "" {
		return nil, fmt.Errorf("未提供目标IP")
	}
	timeout := time.Duration(cfg.ConnectTimeout) * time.Second
	roundCtx, cancel := context.WithTimeout(ctx, timeout)
	lat, err := cfcommon.MeasureTCPLatency(roundCtx, ip, "443", timeout)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("延迟测试失败: %w", err)
	}
	speedTimeout := time.Duration(cfg.SingleSpeedTimeoutSec) * time.Second
	speed, err := s.measureSpeed(ctx, ip, speedTimeout)
	if err != nil {
		return nil, fmt.Errorf("速度测试失败: %w", err)
	}
	result := TestResult{
		IP: ip, Protocol: strings.ToUpper(cfg.Protocol),
		AvgLatency: lat, MinLatency: lat, MaxLatency: lat,
		DownloadMbps: speed, TestTime: time.Now(),
	}
	if colo := s.fetchColo(ctx, ip); colo != "" {
		s.applyLocation(&result, colo)
	}
	s.applyTrace(ctx, &result)
	s.attachNode(&result)
	if s.history != nil {
		_ = s.history.Write(result)
	}
	showDetailedResult(result, reporter)
	out := &Result{Results: []TestResult{result}, HistoryFile: cfg.HistoryFile}
	saveVMessExports(cfg, []TestResult{result}, out, reporter)
	return out, nil
}

func runHistory(cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	file, err := os.Open(cfg.HistoryFile)
	if err != nil {
		return nil, fmt.Errorf("无法读取结果文件: %w", err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("读取CSV文件失败: %w", err)
	}
	if len(records) <= 1 {
		reporter.Log("cfspeed", "[信息] 暂无测试结果")
		return &Result{HistoryFile: cfg.HistoryFile}, nil
	}
	reporter.Log("cfspeed", "历史测试结果 (最近最多 10 条, 共 %d 条)", len(records)-1)
	start := len(records) - 10
	if start < 1 {
		start = 1
	}
	for i := start; i < len(records); i++ {
		record := TrimRecordBOM(records[i])
		if len(record) < HistoryHeaderLen {
			continue
		}
		city := record[8]
		if city == "" {
			city = "N/A"
		}
		displayTime := record[6]
		if len(displayTime) > 16 {
			displayTime = displayTime[:16]
		}
		reporter.Log("cfspeed", "%-15s %-8s %-10s %-8s %-10s %-15s %-8s",
			record[0], record[1], record[2], record[3], city, displayTime, record[7])
	}
	return &Result{HistoryFile: cfg.HistoryFile}, nil
}

type historyRecorder struct {
	sink *cfcommon.CSVRecordSink
}

func newHistoryRecorder(path string) (*historyRecorder, error) {
	sink, err := cfcommon.NewCSVAppendSink(path, []string{
		"IP地址", "延迟(ms)", "速度(Mbps)", "数据中心", "位置", "公网IP", "测试时间", "协议", "城市", "地区", "国家",
	})
	if err != nil {
		return nil, err
	}
	return &historyRecorder{sink: sink}, nil
}

func (r *historyRecorder) Write(result TestResult) error {
	return r.sink.Write([]string{
		result.IP,
		fmt.Sprintf("%.0f", float64(result.AvgLatency.Nanoseconds())/1e6),
		fmt.Sprintf("%.2f", result.DownloadMbps),
		result.DataCenter, result.Location, result.PublicIP,
		result.TestTime.Format("2006-01-02 15:04:05"),
		result.Protocol, result.City, result.Region, result.Country,
	})
}

func (r *historyRecorder) Close() error {
	if r == nil {
		return nil
	}
	return r.sink.Close()
}

type batchOutputSaver struct {
	results     *cfcommon.CSVRecordSink
	premium     *cfcommon.LineSink
	report      *cfcommon.LineSink
	resultsFile string
	premiumFile string
	reportFile  string
	mu          sync.Mutex
}

func newBatchOutputSaver(cfg Config) (*batchOutputSaver, error) {
	saver := &batchOutputSaver{
		resultsFile: cfg.OutputPrefix + "_results.csv",
		premiumFile: cfg.OutputPrefix + "_premium.txt",
		reportFile:  cfg.OutputPrefix + "_report.txt",
	}
	var initErr error
	if sink, err := cfcommon.NewCSVRecordSink(saver.resultsFile,
		[]string{"IP", "Colo", "位置", "国家", "平均延迟ms", "最小延迟ms", "最大延迟ms", "丢包率%", "下载速度Mbps", "评分"}); err != nil {
		initErr = errors.Join(initErr, err)
	} else {
		saver.results = sink
	}
	if sink, err := cfcommon.NewLineSink(saver.premiumFile); err != nil {
		initErr = errors.Join(initErr, err)
	} else {
		saver.premium = sink
	}
	if sink, err := cfcommon.NewLineSink(saver.reportFile); err != nil {
		initErr = errors.Join(initErr, err)
	} else {
		saver.report = sink
		_ = sink.WriteLine("═══ CFSpeed 测试进行中 ═══")
		_ = sink.WriteLine("已完成结果:")
	}
	if saver.results == nil && saver.premium == nil && saver.report == nil {
		return nil, initErr
	}
	return saver, initErr
}

func (s *batchOutputSaver) Write(result TestResult, premium bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.results != nil {
		_ = s.results.Write([]string{
			result.IP, result.DataCenter, result.Location, result.Country,
			fmt.Sprintf("%d", result.AvgLatency.Milliseconds()),
			fmt.Sprintf("%d", result.MinLatency.Milliseconds()),
			fmt.Sprintf("%d", result.MaxLatency.Milliseconds()),
			fmt.Sprintf("%.1f", result.PacketLoss),
			fmt.Sprintf("%.1f", result.DownloadMbps),
			fmt.Sprintf("%.1f", result.Score),
		})
	}
	if premium && s.premium != nil {
		_ = s.premium.WriteLine(fmt.Sprintf("%s # %s %s %.0fms %.1fMbps score=%.1f",
			result.IP, result.DataCenter, result.Location,
			float64(result.AvgLatency.Milliseconds()), result.DownloadMbps, result.Score))
	}
}

func (s *batchOutputSaver) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.results.Close(), s.premium.Close(), s.report.Close())
}

func saveAllCSV(results []TestResult, filename string, reporter cfcommon.Reporter) {
	tempFile := cfcommon.TempWritePath(filename)
	w, _, closeFn, err := cfcommon.NewCSVWriter(tempFile)
	if err != nil {
		return
	}
	writeErr := w.Write([]string{"IP", "Colo", "位置", "国家", "平均延迟ms", "最小延迟ms", "最大延迟ms", "丢包率%", "下载速度Mbps", "评分"})
	for _, r := range results {
		if writeErr == nil {
			writeErr = w.Write([]string{
				r.IP, r.DataCenter, r.Location, r.Country,
				fmt.Sprintf("%d", r.AvgLatency.Milliseconds()),
				fmt.Sprintf("%d", r.MinLatency.Milliseconds()),
				fmt.Sprintf("%d", r.MaxLatency.Milliseconds()),
				fmt.Sprintf("%.1f", r.PacketLoss),
				fmt.Sprintf("%.1f", r.DownloadMbps),
				fmt.Sprintf("%.1f", r.Score),
			})
		}
	}
	if writeErr != nil || closeFn() != nil || cfcommon.ReplaceFileFromTemp(tempFile, filename) != nil {
		return
	}
	reporter.File("cfspeed", filename, "[保存] 全部结果 → %s (%d条)", filename, len(results))
}

func saveVMessExports(cfg Config, nodes []TestResult, out *Result, reporter cfcommon.Reporter) {
	if cfg.VMessUUID == "" || cfg.VMessHost == "" {
		return
	}
	var links []string
	var clientOutbounds []any
	for _, r := range nodes {
		if r.VMessURL == "" {
			continue
		}
		links = append(links, r.VMessURL+"  # "+r.DataCenter+" "+r.Location)
		n := singbox.Node{
			Server:     r.IP,
			UUID:       cfg.VMessUUID,
			Host:       cfg.VMessHost,
			Path:       cfg.VMessPath,
			SNI:        cfg.VMessSNI,
			Port:       cfg.VMessPort,
			TLS:        cfg.VMessTLS,
			AllowInsec: cfg.VMessAllowInsecure,
			Tag:        strings.TrimSpace(r.DataCenter + "-" + r.IP),
		}
		clientOutbounds = append(clientOutbounds, n.Outbound())
	}
	if len(links) == 0 {
		return
	}
	out.VMessFile = cfg.OutputPrefix + "_vmess.txt"
	if err := cfcommon.WriteLinesAtomic(out.VMessFile, links); err == nil {
		reporter.File("cfspeed", out.VMessFile, "[保存] VMess 节点 → %s (%d条)", out.VMessFile, len(links))
	}
	if len(clientOutbounds) > 0 {
		client := map[string]any{
			"log": map[string]any{"level": "warn"},
			"inbounds": []any{
				map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 1080},
			},
			"outbounds": append(clientOutbounds, map[string]any{"type": "direct", "tag": "direct"}),
		}
		raw, err := json.MarshalIndent(client, "", "  ")
		if err == nil {
			out.ClientJSON = cfg.OutputPrefix + "_singbox.json"
			if writeErr := os.WriteFile(out.ClientJSON, raw, 0644); writeErr == nil {
				reporter.File("cfspeed", out.ClientJSON, "[保存] sing-box 客户端配置 → %s", out.ClientJSON)
			}
		}
	}
}

func savePremiumTxt(premium []TestResult, filename string, reporter cfcommon.Reporter) {
	var lines []string
	for _, r := range premium {
		lines = append(lines, fmt.Sprintf("%s # %s %s %.0fms %.1fMbps score=%.1f",
			r.IP, r.DataCenter, r.Location,
			float64(r.AvgLatency.Milliseconds()), r.DownloadMbps, r.Score))
	}
	if err := cfcommon.WriteLinesAtomic(filename, lines); err == nil {
		reporter.File("cfspeed", filename, "[保存] 优质IP → %s (%d个)", filename, len(premium))
	}
}

func generateReport(cfg Config, results, premium []TestResult, filename string, reporter cfcommon.Reporter) {
	var lines []string
	lines = append(lines,
		"═══ CFSpeed 测试报告 ═══",
		fmt.Sprintf("时间: %s", time.Now().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("测速URL: %s", BuildDownloadURL(cfg.Protocol, cfg.SpeedHost, cfg.DownloadSizeMB)),
		fmt.Sprintf("测试IP总数: %d", len(results)),
		fmt.Sprintf("优质IP数量: %d", len(premium)),
	)
	if err := cfcommon.WriteLinesAtomic(filename, lines); err == nil {
		reporter.File("cfspeed", filename, "[保存] 报告 → %s", filename)
	}
}

func showSummary(results, premium []TestResult, reporter cfcommon.Reporter) {
	reporter.Log("cfspeed", "═══ 测试摘要 ═══")
	reporter.Log("cfspeed", "测试IP: %d | 有效: %d | 优质: %d", len(results), len(results), len(premium))
	showN := 10
	if showN > len(premium) {
		showN = len(premium)
	}
	for i := 0; i < showN; i++ {
		r := premium[i]
		reporter.Log("cfspeed", "%s %s %dms %.1fMbps %.1f %s",
			r.IP, r.DataCenter, r.AvgLatency.Milliseconds(), r.DownloadMbps, r.Score, r.Location)
	}
}

func showDetailedResult(result TestResult, reporter cfcommon.Reporter) {
	reporter.Log("cfspeed", "测试 IP: %s | 延迟: %s | 带宽: %.2f Mbps | Colo: %s | 城市: %s",
		result.IP, result.AvgLatency.Round(time.Millisecond), result.DownloadMbps, result.DataCenter, result.City)
	if result.VMessURL != "" {
		reporter.Log("cfspeed", "节点: %s", result.VMessURL)
	}
}
