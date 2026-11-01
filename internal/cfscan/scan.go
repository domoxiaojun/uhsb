package cfscan

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cftest/internal/cfcommon"
)

const (
	CMIHK = "223.119.6.101"
	CMIJP = "223.118.4.105"
	CMIUS = "221.183.137.177"
	DefaultHost = "www.visa.cn"
)

var keywordRegex = regexp.MustCompile(`\b(hkg|tpe|tw|hk)\b`)

type Config struct {
	InputFile  string
	OutputPfx  string
	Workers    int
	Retries    int
	MTRCycles  int
	MTRTimeout int
	Verbose    bool
	HostHeader string
	WorkDir    string
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 100
	}
	if c.Retries < 0 {
		c.Retries = 1
	}
	if c.MTRCycles <= 0 {
		c.MTRCycles = 2
	}
	if c.MTRTimeout <= 0 {
		c.MTRTimeout = 10
	}
	if c.HostHeader == "" {
		c.HostHeader = DefaultHost
	}
	return c
}

type CategorizedResult struct {
	IP, Keyword, MatchingHop, LossPct, AvgMs, Location, Colo string
	HopCount                                                 int
}

type Result struct {
	Hits        []CategorizedResult
	Missed      []string
	ScanCSV     string
	QualityFile string
	MissedFile  string
}

func MTRAvailable() bool {
	_, err := exec.LookPath("mtr")
	return err == nil
}

func Run(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	cfg = cfg.withDefaults()
	if !MTRAvailable() {
		return nil, fmt.Errorf("mtr 未安装。请: sudo apt install mtr / brew install mtr")
	}

	locMap, err := cfcommon.LoadLocations()
	if err != nil {
		reporter.Warn("cfscan", "%v", err)
	} else {
		reporter.Log("cfscan", "[信息] %d 个数据中心", len(locMap))
	}

	input := cfg.InputFile
	if input == "" {
		input = "testip.txt"
	}
	ips, err := cfcommon.ReadIPs(input)
	if err != nil {
		return nil, err
	}

	prefix := cfg.OutputPfx
	if prefix == "" {
		prefix = strings.TrimSuffix(input, ".txt")
		prefix = strings.TrimSuffix(prefix, "_ips")
	}

	liveSaver, err := newLiveOutputSaver(prefix)
	if err != nil {
		reporter.Warn("cfscan", "无法启用实时保存: %v", err)
	}

	reporter.Log("cfscan", "[信息] %d 个IP | 并发=%d", len(ips), cfg.Workers)

	jobs := make(chan string, cfg.Workers)
	results := make(chan CategorizedResult, len(ips))
	var wg sync.WaitGroup
	var completed atomic.Int64
	var missed struct {
		sync.Mutex
		list []string
	}
	total := int64(len(ips))
	progress := cfcommon.NewProgressWithHandler(int(total), 500*time.Millisecond, reporter.ProgressHandler("cfscan"))

	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if ctx.Err() != nil {
					return
				}
				var found bool
				for attempt := 0; attempt <= cfg.Retries; attempt++ {
					if ctx.Err() != nil {
						return
					}
					if attempt > 0 {
						select {
						case <-ctx.Done():
							return
						case <-time.After(time.Second):
						}
					}
					if r, ok := analyzeIP(ctx, cfg, locMap, ip); ok {
						if liveSaver != nil {
							liveSaver.WriteResult(r)
						}
						results <- r
						found = true
						progress.Hit(fmt.Sprintf("%s → %s", r.IP, r.Keyword))
						break
					}
				}
				if !found {
					missed.Lock()
					missed.list = append(missed.list, ip)
					missed.Unlock()
					if liveSaver != nil {
						liveSaver.WriteMissed(ip)
					}
				}
				completed.Add(1)
				progress.Inc()
			}
		}()
	}

feed:
	for _, ip := range ips {
		select {
		case <-ctx.Done():
			break feed
		case jobs <- ip:
		}
	}
	close(jobs)
	wg.Wait()
	progress.Stop()
	close(results)

	var finalResults []CategorizedResult
	for r := range results {
		finalResults = append(finalResults, r)
	}
	if liveSaver != nil {
		_ = liveSaver.Close()
	}
	sort.Slice(finalResults, func(i, j int) bool {
		return finalResults[i].HopCount > finalResults[j].HopCount
	})

	out := &Result{Hits: finalResults, Missed: missed.list}
	reporter.Log("cfscan", "[完成] 命中 %d / %d", len(finalResults), len(ips))

	if len(finalResults) > 0 {
		out.ScanCSV = prefix + "_scan.csv"
		saveResultsToCSV(finalResults, out.ScanCSV, reporter)
		out.QualityFile = prefix + "_quality.txt"
		var qualityIPs []string
		for _, r := range finalResults {
			qualityIPs = append(qualityIPs, r.IP)
		}
		if err := cfcommon.WriteLinesAtomic(out.QualityFile, qualityIPs); err != nil {
			reporter.Error("cfscan", "保存 %s 失败: %v", out.QualityFile, err)
		} else {
			reporter.File("cfscan", out.QualityFile, "[保存] 质量IP列表 → %s (%d个)", out.QualityFile, len(qualityIPs))
		}
		showTop(finalResults, 15, reporter)
	}

	if len(missed.list) > 0 {
		out.MissedFile = prefix + "_missed.txt"
		if err := cfcommon.WriteLinesAtomic(out.MissedFile, missed.list); err != nil {
			reporter.Error("cfscan", "保存 %s 失败: %v", out.MissedFile, err)
		} else {
			reporter.File("cfscan", out.MissedFile, "[注意] %d 个未命中 → %s", len(missed.list), out.MissedFile)
		}
	}
	return out, nil
}

func analyzeIP(ctx context.Context, cfg Config, locMap map[string]cfcommon.Location, ip string) (CategorizedResult, bool) {
	result := CategorizedResult{IP: ip}

	var directCMI string
	switch ip {
	case CMIHK:
		directCMI = "cmi-hk"
	case CMIJP:
		directCMI = "cmi-jp"
	case CMIUS:
		directCMI = "cmi-us"
	}

	cmd := exec.CommandContext(ctx, "mtr", "-r",
		"-c", strconv.Itoa(cfg.MTRCycles),
		"--timeout", strconv.Itoa(cfg.MTRTimeout), ip)

	output, err := cmd.Output()
	if err != nil {
		if directCMI != "" {
			result.Keyword = directCMI
			result.MatchingHop = "CMI Direct"
			enrichColo(ctx, cfg, locMap, &result)
			return result, true
		}
		return result, false
	}

	lines := strings.Split(string(output), "\n")
	type match struct{ keyword, hop, loss, avg string }
	var matches []match
	var hopCount int

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Contains(line, "HOST") || strings.Contains(line, "Start:") {
			continue
		}
		if strings.Contains(line, "|--") || strings.Contains(line, "|??") {
			hopCount++
		}
		lineLower := strings.ToLower(line)

		for _, cmi := range []struct{ ip, key string }{
			{CMIHK, "cmi-hk"}, {CMIJP, "cmi-jp"}, {CMIUS, "cmi-us"},
		} {
			if strings.Contains(line, cmi.ip) {
				l, a := ExtractLossAvg(line)
				matches = append(matches, match{cmi.key, trimmed, l, a})
			}
		}

		if m := keywordRegex.FindString(lineLower); m != "" {
			l, a := ExtractLossAvg(line)
			matches = append(matches, match{m, trimmed, l, a})
		}
	}

	result.HopCount = hopCount
	isHighQuality := hopCount > 16

	var keywords []string
	if directCMI != "" {
		keywords = append(keywords, directCMI)
	}
	if isHighQuality {
		keywords = append(keywords, "high-quality")
	}
	for _, m := range matches {
		keywords = append(keywords, m.keyword)
	}
	if len(keywords) == 0 {
		return result, false
	}

	result.Keyword = strings.Join(keywords, "+")
	if len(matches) > 0 {
		result.MatchingHop = matches[0].hop
		result.LossPct = matches[0].loss
		result.AvgMs = matches[0].avg
	} else if isHighQuality {
		result.MatchingHop = fmt.Sprintf("High Quality (%d跳)", hopCount)
	} else {
		result.MatchingHop = "CMI Direct"
	}

	enrichColo(ctx, cfg, locMap, &result)
	return result, true
}

func ExtractLossAvg(line string) (loss, avg string) {
	parts := strings.Fields(line)
	for i, p := range parts {
		if strings.HasSuffix(p, "%") {
			loss = strings.TrimSuffix(p, "%")
			if i+3 < len(parts) {
				avg = strings.Trim(parts[i+3], ",%|")
			} else if i+2 < len(parts) {
				avg = strings.Trim(parts[i+2], ",%|")
			}
			break
		}
	}
	return
}

func enrichColo(ctx context.Context, cfg Config, locMap map[string]cfcommon.Location, result *CategorizedResult) {
	for attempt := 1; attempt <= 3; attempt++ {
		colo, err := cfcommon.DialAndGetColo(ctx, result.IP, cfg.HostHeader, 2*time.Second, 3*time.Second)
		if err == nil && colo != "" {
			result.Colo = colo
			result.Location = getLocation(locMap, colo)
			return
		}
		if attempt < 3 {
			select {
			case <-ctx.Done():
				result.Colo = "?"
				result.Location = "?"
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	result.Colo = "?"
	result.Location = "?"
}

func getLocation(locMap map[string]cfcommon.Location, iata string) string {
	if locMap == nil {
		return iata
	}
	if loc, ok := locMap[strings.ToUpper(iata)]; ok {
		return fmt.Sprintf("%s, %s", loc.City, loc.Region)
	}
	return iata
}

type liveOutputSaver struct {
	results     *cfcommon.CSVRecordSink
	resultsErr  error
	resultsFile string
	quality     *cfcommon.LineSink
	qualityErr  error
	qualityFile string
	missed      *cfcommon.LineSink
	missedErr   error
	missedFile  string
	mu          sync.Mutex
}

func newLiveOutputSaver(prefix string) (*liveOutputSaver, error) {
	saver := &liveOutputSaver{
		resultsFile: prefix + "_scan.csv",
		qualityFile: prefix + "_quality.txt",
		missedFile:  prefix + "_missed.txt",
	}
	var initErr error
	if sink, err := cfcommon.NewCSVRecordSink(saver.resultsFile,
		[]string{"IP", "Colo", "位置", "跳数", "关键字", "丢包%", "延迟ms", "匹配路由"}); err != nil {
		saver.resultsErr = err
		initErr = errors.Join(initErr, err)
	} else {
		saver.results = sink
	}
	if sink, err := cfcommon.NewLineSink(saver.qualityFile); err != nil {
		saver.qualityErr = err
		initErr = errors.Join(initErr, err)
	} else {
		saver.quality = sink
	}
	if sink, err := cfcommon.NewLineSink(saver.missedFile); err != nil {
		saver.missedErr = err
		initErr = errors.Join(initErr, err)
	} else {
		saver.missed = sink
	}
	if saver.results == nil && saver.quality == nil && saver.missed == nil {
		return nil, initErr
	}
	return saver, initErr
}

func (s *liveOutputSaver) WriteResult(r CategorizedResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hop := strings.ReplaceAll(r.MatchingHop, "\n", " ")
	hop = strings.ReplaceAll(hop, "\r", "")
	if s.results != nil && s.resultsErr == nil {
		_ = s.results.Write([]string{
			r.IP, r.Colo, r.Location,
			strconv.Itoa(r.HopCount), r.Keyword,
			r.LossPct, r.AvgMs, hop,
		})
	}
	if s.quality != nil && s.qualityErr == nil {
		_ = s.quality.WriteLine(r.IP)
	}
}

func (s *liveOutputSaver) WriteMissed(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.missed != nil && s.missedErr == nil {
		_ = s.missed.WriteLine(ip)
	}
}

func (s *liveOutputSaver) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.resultsErr, s.qualityErr, s.missedErr, s.results.Close(), s.quality.Close(), s.missed.Close())
}

func saveResultsToCSV(results []CategorizedResult, filename string, reporter cfcommon.Reporter) {
	tempFile := cfcommon.TempWritePath(filename)
	w, _, closeFn, err := cfcommon.NewCSVWriter(tempFile)
	if err != nil {
		reporter.Error("cfscan", "保存 %s 失败: %v", filename, err)
		return
	}
	writeErr := w.Write([]string{"IP", "Colo", "位置", "跳数", "关键字", "丢包%", "延迟ms", "匹配路由"})
	for _, r := range results {
		hop := strings.ReplaceAll(r.MatchingHop, "\n", " ")
		hop = strings.ReplaceAll(hop, "\r", "")
		if writeErr == nil {
			writeErr = w.Write([]string{r.IP, r.Colo, r.Location,
				strconv.Itoa(r.HopCount), r.Keyword,
				r.LossPct, r.AvgMs, hop})
		}
	}
	if writeErr != nil || closeFn() != nil || cfcommon.ReplaceFileFromTemp(tempFile, filename) != nil {
		return
	}
	reporter.File("cfscan", filename, "[保存] 扫描结果 → %s", filename)
}

func showTop(results []CategorizedResult, n int, reporter cfcommon.Reporter) {
	if len(results) < n {
		n = len(results)
	}
	reporter.Log("cfscan", "%-16s %-5s %-6s %-20s %s", "IP", "Colo", "跳数", "关键字", "位置")
	reporter.Log("cfscan", "%s", strings.Repeat("─", 70))
	for i := 0; i < n; i++ {
		r := results[i]
		reporter.Log("cfscan", "%-16s %-5s %-6d %-20s %s",
			r.IP, r.Colo, r.HopCount, r.Keyword, r.Location)
	}
}
