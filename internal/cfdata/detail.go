package cfdata

import (
	"context"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"cftest/internal/cfcommon"
)

func runDetailedTest(ctx context.Context, cfg Config, scanResults []ScanResult, tag string, reporter cfcommon.Reporter) ([]DetailedTestResult, []DetailedTestResult, string, string, string) {
	reporter.Log("cfdata", "开始详细测试: %d 个IP, 端口=%d, 延迟阈值=%dms", len(scanResults), cfg.Port, cfg.Delay)

	liveSaver, err := newDetailedLiveSaver(cfg, tag)
	if err != nil {
		reporter.Warn("cfdata", "无法启用实时保存: %v", err)
	}

	var wg sync.WaitGroup
	resultCh := make(chan DetailedTestResult, len(scanResults))
	sem := make(chan struct{}, cfg.TestThreads)
	var count atomic.Int64
	total := len(scanResults)
	progress := cfcommon.NewProgressWithHandler(total, 500*time.Millisecond, reporter.ProgressHandler("cfdata"))

launch:
	for _, sr := range scanResults {
		select {
		case <-ctx.Done():
			break launch
		default:
		}
		if !cfcommon.AcquireSemaphore(ctx, sem) {
			break launch
		}
		wg.Add(1)
		go func(sr ScanResult) {
			defer func() { <-sem; wg.Done(); progress.Inc() }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			dialer := &net.Dialer{Timeout: time.Duration(cfg.Delay) * time.Millisecond}
			var success int
			var totalLat time.Duration
			minLat, maxLat := time.Duration(math.MaxInt64), time.Duration(0)

			consecutiveFail := 0
			for i := 0; i < 10; i++ {
				select {
				case <-ctx.Done():
					return
				default:
				}
				start := time.Now()
				conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(sr.IP, strconv.Itoa(cfg.Port)))
				if err != nil {
					consecutiveFail++
					if consecutiveFail >= 5 {
						break
					}
					continue
				}
				lat := time.Since(start)
				conn.Close()
				if lat > time.Duration(cfg.Delay)*time.Millisecond {
					consecutiveFail++
					if consecutiveFail >= 5 {
						break
					}
					continue
				}
				consecutiveFail = 0
				success++
				totalLat += lat
				if lat < minLat {
					minLat = lat
				}
				if lat > maxLat {
					maxLat = lat
				}
			}

			c := count.Add(1)
			progress.Hit(fmt.Sprintf("详细测试: %d/%d", c, total))

			if success > 0 {
				result := DetailedTestResult{
					IP: sr.IP, DataCenter: sr.DataCenter,
					Region: sr.Region, City: sr.City, CountryCode: sr.CountryCode,
					MinLatency: minLat, MaxLatency: maxLat,
					AvgLatency: totalLat / time.Duration(success),
					LossRate:   float64(10-success) / 10.0,
				}
				if liveSaver != nil {
					liveSaver.Write(result)
				}
				resultCh <- result
			}
		}(sr)
	}

	wg.Wait()
	close(resultCh)
	progress.Stop()

	if liveSaver != nil {
		if err := liveSaver.Close(); err != nil {
			reporter.Warn("cfdata", "实时保存关闭失败: %v", err)
		}
	}

	var results []DetailedTestResult
	for r := range resultCh {
		results = append(results, r)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].LossRate == results[j].LossRate {
			return results[i].AvgLatency < results[j].AvgLatency
		}
		return results[i].LossRate < results[j].LossRate
	})

	if len(results) == 0 {
		reporter.Log("cfdata", "详细测试: 无有效IP")
		return nil, nil, "", "", ""
	}

	safeTag := SanitizeOutputTag(tag)
	resultCSV := pathIn(cfg, safeTag+"_result.csv")
	saveDetailedCSV(results, resultCSV, reporter)

	zeroLoss := sortZeroLoss(results)
	zeroCSV, zeroTxt := "", ""
	if len(zeroLoss) > 0 {
		zeroCSV = pathIn(cfg, safeTag+"_0loss.csv")
		saveDetailedCSV(zeroLoss, zeroCSV, reporter)
		var zeroIPs []string
		for _, r := range zeroLoss {
			zeroIPs = append(zeroIPs, r.IP)
		}
		zeroTxt = pathIn(cfg, safeTag+"_0loss_ips.txt")
		if err := cfcommon.WriteLinesAtomic(zeroTxt, zeroIPs); err != nil {
			reporter.Error("cfdata", "保存 %s 失败: %v", zeroTxt, err)
			zeroTxt = ""
		} else {
			reporter.File("cfdata", zeroTxt, "[保存] %d 个0丢包IP → %s", len(zeroIPs), zeroTxt)
		}

		showCount := 5
		if showCount > len(zeroLoss) {
			showCount = len(zeroLoss)
		}
		reporter.Log("cfdata", "延迟最低的0丢包IP:")
		for i := 0; i < showCount; i++ {
			r := zeroLoss[i]
			reporter.Log("cfdata", "  %d. %s (%s, %s, %dms)",
				i+1, r.IP, r.DataCenter, r.City, r.AvgLatency.Milliseconds())
		}
	}

	analyzeResults(results, len(scanResults), reporter)
	return results, zeroLoss, resultCSV, zeroCSV, zeroTxt
}
