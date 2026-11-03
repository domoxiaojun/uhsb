package cfconnectivity

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cftest/internal/cfcommon"
)

const (
	DefaultTestURL = "https://speed.cloudflare.com"
	TestPort       = "443"
	TraceEndpoint  = "/cdn-cgi/trace"
)

type Config struct {
	InputFile      string
	OutputPrefix   string
	PreferredColos string
	ColoFilter     string
	Workers        int
	TestRounds     int
	ConnectTimeout int
	DownloadSize   int
	FetchRetries   int
	MaxLatency     int
	MaxPacketLoss  float64
	MinSpeed       float64
	Verbose        bool
}

func (c Config) withDefaults() Config {
	if c.InputFile == "" {
		c.InputFile = "ip.txt"
	}
	if c.OutputPrefix == "" {
		c.OutputPrefix = "cfconnectivity"
	}
	if c.PreferredColos == "" {
		c.PreferredColos = "hkg,nrt,sin,tpe,icn"
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
	if c.DownloadSize <= 0 {
		c.DownloadSize = 200
	}
	if c.FetchRetries < 0 {
		c.FetchRetries = 1
	}
	if c.MaxLatency <= 0 {
		c.MaxLatency = 200
	}
	if c.MaxPacketLoss < 0 {
		c.MaxPacketLoss = 10
	}
	if c.MinSpeed < 0 {
		c.MinSpeed = 100
	}
	return c
}

type ConnectivityResult struct {
	IP             string
	AvgLatency     time.Duration
	MinLatency     time.Duration
	MaxLatency     time.Duration
	StdDevLatency  float64
	PacketLoss     float64
	SuccessRate    float64
	DownloadSpeed  float64
	DataCenter     string
	Location       string
	Country        string
	Score          float64
	TestTime       time.Time
	FailureReasons []string
}

type Result struct {
	Results     []ConnectivityResult
	Premium     []ConnectivityResult
	ResultsCSV  string
	PremiumFile string
	ReportFile  string
}

func Run(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	cfg = cfg.withDefaults()
	var coloFilterList []string
	for _, p := range strings.Split(cfg.ColoFilter, ",") {
		if c := strings.TrimSpace(p); c != "" {
			coloFilterList = append(coloFilterList, strings.ToUpper(c))
		}
	}

	locMap, err := cfcommon.LoadLocations()
	if err != nil {
		reporter.Warn("cfconnectivity", "加载位置信息失败: %v", err)
	}

	ips, err := cfcommon.ReadIPs(cfg.InputFile)
	if err != nil {
		return nil, fmt.Errorf("读取IP文件失败: %w", err)
	}
	reporter.Log("cfconnectivity", "[信息] 已加载 %d 个IP", len(ips))

	progress := cfcommon.NewProgressWithHandler(len(ips), 500*time.Millisecond, reporter.ProgressHandler("cfconnectivity"))
	sem := make(chan struct{}, cfg.Workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var testResults []ConnectivityResult
	var skipped atomic.Int64

	for _, ip := range ips {
		if ctx.Err() != nil {
			break
		}
		if !cfcommon.AcquireSemaphore(ctx, sem) {
			break
		}
		wg.Add(1)
		go func(ip string) {
			defer func() { <-sem; wg.Done(); progress.Inc() }()
			result := performFullConnectivityTest(ctx, cfg, locMap, coloFilterList, &skipped, ip)
			mu.Lock()
			testResults = append(testResults, result)
			mu.Unlock()
			if result.Score >= 80 && result.PacketLoss < cfg.MaxPacketLoss {
				progress.Hit(fmt.Sprintf("优质: %s [%s] 延迟%dms 速度%.0fMbps 评分%.0f",
					result.IP, result.DataCenter, result.AvgLatency.Milliseconds(), result.DownloadSpeed, result.Score))
				reporter.Emit(cfcommon.Event{Stage: "cfconnectivity", Kind: cfcommon.KindResult, IP: result.IP, Extra: result,
					Message: fmt.Sprintf("%s [%s] %.0fMbps", result.IP, result.DataCenter, result.DownloadSpeed)})
			}
		}(ip)
	}
	wg.Wait()
	progress.Stop()

	sort.Slice(testResults, func(i, j int) bool { return testResults[i].Score > testResults[j].Score })
	premium := filterPremium(cfg, testResults)
	out := &Result{Results: testResults, Premium: premium}
	out.ResultsCSV = cfg.OutputPrefix + "_full_results.csv"
	saveResultsCSV(testResults, out.ResultsCSV, reporter)
	if len(premium) > 0 {
		out.PremiumFile = cfg.OutputPrefix + "_premium_ips.txt"
		savePremiumTxt(cfg, premium, out.PremiumFile, reporter)
	}
	out.ReportFile = cfg.OutputPrefix + "_report.txt"
	generateReport(cfg, testResults, premium, out.ReportFile, reporter)
	reporter.Log("cfconnectivity", "IP总数: %d | 因Colo跳过: %d | 优质: %d", len(testResults), skipped.Load(), len(premium))
	return out, nil
}

func performFullConnectivityTest(ctx context.Context, cfg Config, locMap map[string]cfcommon.Location, coloFilterList []string, skipped *atomic.Int64, ip string) ConnectivityResult {
	result := ConnectivityResult{IP: ip, TestTime: time.Now()}
	if len(coloFilterList) > 0 {
		colo, _ := fetchCDNInfo(ctx, cfg, ip)
		if colo == "" || !coloMatches(coloFilterList, colo) {
			skipped.Add(1)
			result.DataCenter = colo
			return result
		}
		applyColo(&result, locMap, colo)
	}

	maxTestTime := 60 * time.Second
	testCtx, cancel := context.WithTimeout(ctx, maxTestTime)
	defer cancel()

	latencies := runLatencyRounds(testCtx, cfg, ip)
	applyLatencyStats(&result, latencies, cfg.TestRounds)
	if result.SuccessRate >= 50 && result.AvgLatency <= time.Duration(cfg.MaxLatency+50)*time.Millisecond {
		if speed, err := performSpeedTest(testCtx, cfg, ip); err == nil {
			result.DownloadSpeed = speed
		}
	}
	calculateScore(cfg, &result)
	if result.DataCenter == "" && result.SuccessRate >= 50 {
		if colo, country := fetchCDNInfo(ctx, cfg, ip); colo != "" {
			applyColo(&result, locMap, colo)
			if result.Country == "" {
				result.Country = country
			}
			calculateScore(cfg, &result)
		}
	}
	return result
}

type latencyProbe struct {
	lat time.Duration
	ok  bool
}

func runLatencyRounds(ctx context.Context, cfg Config, ip string) []latencyProbe {
	parsedURL, err := url.Parse(DefaultTestURL)
	if err != nil {
		return nil
	}
	hostname := parsedURL.Hostname()
	timeout := time.Duration(cfg.ConnectTimeout) * time.Second
	probes := make([]latencyProbe, cfg.TestRounds)
	for i := 0; i < cfg.TestRounds; i++ {
		if ctx.Err() != nil {
			break
		}
		roundCtx, cancel := context.WithTimeout(ctx, timeout)
		dialer := &net.Dialer{Timeout: timeout}
		start := time.Now()
		conn, err := dialer.DialContext(roundCtx, "tcp", net.JoinHostPort(ip, TestPort))
		tcpLat := time.Since(start)
		if err != nil {
			cancel()
			continue
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: hostname, InsecureSkipVerify: true})
		if err := tlsConn.HandshakeContext(roundCtx); err != nil {
			tlsConn.Close()
			cancel()
			continue
		}
		tlsConn.Close()
		cancel()
		probes[i] = latencyProbe{lat: tcpLat, ok: true}
	}
	return probes
}

func applyLatencyStats(result *ConnectivityResult, probes []latencyProbe, rounds int) {
	var lats []time.Duration
	for _, p := range probes {
		if p.ok {
			lats = append(lats, p.lat)
		}
	}
	success := len(lats)
	if rounds > 0 {
		result.SuccessRate = float64(success) / float64(rounds) * 100
		result.PacketLoss = 100 - result.SuccessRate
	}
	if success == 0 {
		return
	}
	result.MinLatency = lats[0]
	result.MaxLatency = lats[0]
	var sum, sumSq float64
	for _, l := range lats {
		ms := float64(l.Nanoseconds()) / 1e6
		sum += ms
		sumSq += ms * ms
		if l < result.MinLatency {
			result.MinLatency = l
		}
		if l > result.MaxLatency {
			result.MaxLatency = l
		}
	}
	avgMs := sum / float64(success)
	result.AvgLatency = time.Duration(avgMs * 1e6)
	variance := (sumSq / float64(success)) - (avgMs * avgMs)
	if variance > 0 {
		result.StdDevLatency = math.Sqrt(variance)
	}
}

func performSpeedTest(ctx context.Context, cfg Config, ip string) (float64, error) {
	timeout := 30 * time.Second
	speedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	downloadURL := fmt.Sprintf("%s/__down?bytes=%d", DefaultTestURL, cfg.DownloadSize*1024*1024)
	speed, _, err := cfcommon.MeasureDownloadSpeed(speedCtx, ip, downloadURL, timeout, 0, 5*time.Second, cfg.MinSpeed, 256*1024)
	return speed, err
}

func fetchCDNInfo(ctx context.Context, cfg Config, ip string) (colo, country string) {
	traceURL := DefaultTestURL + TraceEndpoint
	attempts := 1 + cfg.FetchRetries
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return "", ""
		}
		info, err := cfcommon.FetchTraceMap(ctx, ip, traceURL, 10*time.Second)
		if err == nil {
			if c := strings.TrimSpace(info["colo"]); c != "" {
				colo = strings.ToUpper(c)
			}
			country = strings.TrimSpace(info["loc"])
			if colo != "" {
				return colo, country
			}
		}
	}
	return "", ""
}

func applyColo(result *ConnectivityResult, locMap map[string]cfcommon.Location, colo string) {
	result.DataCenter = colo
	if locMap == nil {
		return
	}
	if loc, ok := locMap[strings.ToUpper(colo)]; ok {
		result.Location = loc.City
		result.Country = loc.CCA2
	}
	if result.Location == "" {
		result.Location = colo
	}
}

func coloMatches(list []string, colo string) bool {
	if len(list) == 0 {
		return true
	}
	for _, c := range list {
		if strings.EqualFold(c, colo) {
			return true
		}
	}
	return false
}

func calculateScore(cfg Config, result *ConnectivityResult) {
	score := 0.0
	if result.AvgLatency > 0 {
		ms := float64(result.AvgLatency.Milliseconds())
		switch {
		case ms <= 50:
			score += 40
		case ms <= 100:
			score += 35
		case ms <= 150:
			score += 25
		case ms <= 200:
			score += 15
		case ms <= 300:
			score += 5
		}
		if result.StdDevLatency < 10 {
			score += 5
		}
	}
	switch {
	case result.DownloadSpeed >= 500:
		score += 30
	case result.DownloadSpeed >= 300:
		score += 25
	case result.DownloadSpeed >= 200:
		score += 20
	case result.DownloadSpeed >= 100:
		score += 15
	case result.DownloadSpeed >= 50:
		score += 10
	case result.DownloadSpeed >= 20:
		score += 5
	}
	switch {
	case result.SuccessRate >= 95:
		score += 20
	case result.SuccessRate >= 90:
		score += 15
	case result.SuccessRate >= 80:
		score += 10
	case result.SuccessRate >= 70:
		score += 5
	}
	for _, p := range strings.Split(cfg.PreferredColos, ",") {
		if strings.EqualFold(strings.TrimSpace(p), result.DataCenter) {
			score += 10
			break
		}
	}
	if result.PacketLoss > 0 {
		score -= result.PacketLoss * 2
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	result.Score = score
}

func filterPremium(cfg Config, results []ConnectivityResult) []ConnectivityResult {
	var premium []ConnectivityResult
	maxLatMs := float64(cfg.MaxLatency)
	for _, r := range results {
		latMs := float64(r.AvgLatency.Nanoseconds()) / 1e6
		if r.PacketLoss <= cfg.MaxPacketLoss && r.DownloadSpeed >= cfg.MinSpeed && latMs <= maxLatMs && r.SuccessRate >= 90 {
			premium = append(premium, r)
		}
	}
	return premium
}

func saveResultsCSV(results []ConnectivityResult, filename string, reporter cfcommon.Reporter) {
	tempFile := cfcommon.TempWritePath(filename)
	w, _, closeFn, err := cfcommon.NewCSVWriter(tempFile)
	if err != nil {
		return
	}
	writeErr := w.Write([]string{"IP地址", "平均延迟ms", "最小延迟ms", "最大延迟ms", "抖动ms", "丢包率%", "成功率%", "下载速度Mbps", "数据中心", "位置", "国家", "评分", "测试时间"})
	for _, r := range results {
		if writeErr != nil {
			break
		}
		writeErr = w.Write([]string{
			r.IP,
			fmt.Sprintf("%.2f", float64(r.AvgLatency.Nanoseconds())/1e6),
			fmt.Sprintf("%.2f", float64(r.MinLatency.Nanoseconds())/1e6),
			fmt.Sprintf("%.2f", float64(r.MaxLatency.Nanoseconds())/1e6),
			fmt.Sprintf("%.2f", r.StdDevLatency),
			fmt.Sprintf("%.2f", r.PacketLoss),
			fmt.Sprintf("%.2f", r.SuccessRate),
			fmt.Sprintf("%.2f", r.DownloadSpeed),
			r.DataCenter, r.Location, r.Country,
			fmt.Sprintf("%.1f", r.Score),
			r.TestTime.Format("2006-01-02 15:04:05"),
		})
	}
	if writeErr != nil || closeFn() != nil || cfcommon.ReplaceFileFromTemp(tempFile, filename) != nil {
		return
	}
	reporter.File("cfconnectivity", filename, "[保存] 详细结果 → %s", filename)
}

func savePremiumTxt(cfg Config, premium []ConnectivityResult, filename string, reporter cfcommon.Reporter) {
	var lines []string
	lines = append(lines, "# Cloudflare 优质IP列表",
		fmt.Sprintf("# 筛选: 丢包<%.1f%% 延迟<%dms 速度>%.0fMbps 成功率>=90%%", cfg.MaxPacketLoss, cfg.MaxLatency, cfg.MinSpeed))
	for _, r := range premium {
		lines = append(lines, fmt.Sprintf("%s # %s %.0fms %.1fMbps %.1f%% 评分%.1f",
			r.IP, r.DataCenter, float64(r.AvgLatency.Milliseconds()), r.DownloadSpeed, r.PacketLoss, r.Score))
	}
	if err := cfcommon.WriteLinesAtomic(filename, lines); err == nil {
		reporter.File("cfconnectivity", filename, "[保存] 优质IP → %s (%d个)", filename, len(premium))
	}
}

func generateReport(cfg Config, results, premium []ConnectivityResult, filename string, reporter cfcommon.Reporter) {
	lines := []string{
		"CFConnectivity 测试报告",
		fmt.Sprintf("IP 总数: %d | 优质: %d", len(results), len(premium)),
	}
	if err := cfcommon.WriteLinesAtomic(filename, lines); err == nil {
		reporter.File("cfconnectivity", filename, "[保存] 报告 → %s", filename)
	}
}
