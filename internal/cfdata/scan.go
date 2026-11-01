package cfdata

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cftest/internal/cfcommon"
)

func loadResources(cfg Config, reporter cfcommon.Reporter) ([]string, map[string]cfcommon.Location, error) {
	var ipURL, localFile string
	if cfg.IPType == "6" {
		ipURL, localFile = cfg.IPv6URL, pathIn(cfg, "ips-v6.txt")
	} else {
		ipURL, localFile = cfg.IPv4URL, pathIn(cfg, "ips-v4.txt")
	}

	subnets, err := loadOrDownload(localFile, ipURL)
	if err != nil {
		return nil, nil, fmt.Errorf("加载IP段失败: %v", err)
	}
	reporter.Log("cfdata", "[信息] 已加载 %d 个子网", len(subnets))

	locMap, err := cfcommon.LoadLocations()
	if err != nil {
		return nil, nil, fmt.Errorf("加载位置数据失败: %v", err)
	}
	reporter.Log("cfdata", "[信息] 已加载 %d 个数据中心", len(locMap))
	return subnets, locMap, nil
}

func loadOrDownload(localFile, url string) ([]string, error) {
	name := filepath.Base(localFile)
	candidates := []string{
		localFile,
		name,
		filepath.Join("..", name),
		filepath.Join("cfdata", name),
		filepath.Join("/app", name),
		filepath.Join("/data", name),
	}
	for _, candidate := range candidates {
		if data, err := os.ReadFile(candidate); err == nil {
			return parseLines(string(data)), nil
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	lines := parseLines(string(body))
	if len(lines) == 0 {
		return nil, fmt.Errorf("下载结果为空")
	}
	if err := validateSubnetLines(lines); err != nil {
		return nil, fmt.Errorf("下载结果包含无效网段: %w", err)
	}
	if err := os.WriteFile(localFile, body, 0644); err != nil {
		return lines, nil
	}
	return lines, nil
}

func scanIPs(ctx context.Context, cancel context.CancelFunc, cfg Config, subnets []string, locMap map[string]cfcommon.Location,
	matchFn func(colo string, country string) bool,
	stopCheck func(colo string, country string) (bool, string),
	onMatch func(ScanResult), reporter cfcommon.Reporter) []ScanResult {

	totalIPs := countGeneratedIPs(subnets, cfg.IPType == "6", cfg.IPv6PerSubnet)
	reporter.Log("cfdata", "[信息] 准备扫描 %d 个IP", totalIPs)

	progress := cfcommon.NewProgressWithHandler(totalIPs, 500*time.Millisecond, reporter.ProgressHandler("cfdata"))

	var results []ScanResult
	var mu sync.Mutex
	var stopOnce sync.Once

	sem := make(chan struct{}, cfg.ScanThreads)
	var wg sync.WaitGroup

	forEachGeneratedIP(ctx, subnets, cfg.IPType == "6", cfg.IPv6PerSubnet, func(ip string) bool {
		select {
		case <-ctx.Done():
			return false
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(ip string) {
			defer func() {
				<-sem
				wg.Done()
				progress.Inc()
			}()
			select {
			case <-ctx.Done():
				return
			default:
			}

			dialer := &net.Dialer{Timeout: 2 * time.Second}
			start := time.Now()
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, "80"))
			if err != nil {
				return
			}
			tcpDur := time.Since(start)
			defer conn.Close()

			colo, err := cfcommon.GetColo(conn, ip, cfg.HostHeader, 3*time.Second)
			if err != nil || colo == "" {
				return
			}

			loc, hasLoc := locMap[strings.ToUpper(colo)]
			country := ""
			if hasLoc {
				country = loc.CCA2
			}

			if !matchFn(colo, country) {
				return
			}

			r := ScanResult{
				IP:          ip,
				DataCenter:  colo,
				TCPDuration: tcpDur,
				LatencyStr:  fmt.Sprintf("%dms", tcpDur.Milliseconds()),
			}
			if hasLoc {
				r.Region = loc.Region
				r.City = loc.City
				r.CountryCode = loc.CCA2
			}

			mu.Lock()
			results = append(results, r)
			mu.Unlock()
			if onMatch != nil {
				onMatch(r)
			}
			reporter.Emit(cfcommon.Event{
				Stage:   "cfdata",
				Kind:    cfcommon.KindHit,
				Message: fmt.Sprintf("最新: %s [%s %s] %dms", ip, colo, r.City, tcpDur.Milliseconds()),
				IP:      ip,
				Extra:   r,
			})
			progress.Hit(fmt.Sprintf("最新: %s [%s %s] %dms", ip, colo, r.City, tcpDur.Milliseconds()))

			if stopCheck != nil {
				if stop, msg := stopCheck(colo, country); stop {
					stopOnce.Do(func() {
						if msg != "" {
							reporter.Log("cfdata", "%s", msg)
						}
						if cancel != nil {
							cancel()
						}
					})
				}
			}
		}(ip)
		return true
	})

	wg.Wait()
	progress.Stop()
	reporter.Log("cfdata", "[扫描完成] 命中 %d 个IP", len(results))

	sort.Slice(results, func(i, j int) bool {
		return results[i].TCPDuration < results[j].TCPDuration
	})
	return results
}
