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
	var ipURL, name string
	if cfg.IPType == "6" {
		ipURL, name = cfg.IPv6URL, "ips-v6.txt"
	} else {
		ipURL, name = cfg.IPv4URL, "ips-v4.txt"
	}

	cachePath := cfcommon.WorkPath(cfg.CacheDir, name)
	subnets, source, err := loadSubnetList(cachePath, ipURL, cfg.MaxAge)
	if err != nil {
		return nil, nil, fmt.Errorf("加载IP段失败: %v", err)
	}
	reporter.Log("cfdata", "[信息] 已加载 %d 个子网 (来源: %s)", len(subnets), source)

	locMap, err := cfcommon.LoadLocations()
	if err != nil {
		return nil, nil, fmt.Errorf("加载位置数据失败: %v", err)
	}
	reporter.Log("cfdata", "[信息] 已加载 %d 个数据中心", len(locMap))
	return subnets, locMap, nil
}

// loadSubnetList 读取网段列表，优先用未过期的本地缓存；缓存过期或缺失时
// 重新下载并刷新缓存；下载失败则退回旧缓存或镜像预置文件。
func loadSubnetList(cachePath, url string, maxAge time.Duration) ([]string, string, error) {
	cached, cacheErr := os.ReadFile(cachePath)
	if cacheErr == nil {
		if lines := parseLines(string(cached)); len(lines) > 0 && validateSubnetLines(lines) == nil {
			if maxAge <= 0 {
				return lines, cachePath + " (缓存)", nil
			}
			if st, err := os.Stat(cachePath); err == nil && time.Since(st.ModTime()) < maxAge {
				return lines, cachePath + " (缓存)", nil
			}
		} else {
			cached = nil
		}
	}

	if lines, err := loadOrDownload(cachePath, url); err == nil {
		return lines, url, nil
	} else if len(cached) > 0 {
		if lines := parseLines(string(cached)); len(lines) > 0 {
			return lines, cachePath + " (过期缓存)", nil
		}
		return nil, "", err
	}

	if seed := findSeed(filepath.Base(cachePath)); seed != "" {
		if data, err := os.ReadFile(seed); err == nil {
			if lines := parseLines(string(data)); len(lines) > 0 && validateSubnetLines(lines) == nil {
				return lines, seed + " (预置)", nil
			}
		}
	}
	return nil, "", fmt.Errorf("下载失败且本地无可用缓存: %s", url)
}

// findSeed 查找镜像/仓库自带的网段文件。
func findSeed(name string) string {
	candidates := []string{
		filepath.Join("/app", name),
		name,
		filepath.Join("..", name),
		filepath.Join("cfdata", name),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append([]string{filepath.Join(filepath.Dir(exe), name)}, candidates...)
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

// loadOrDownload 下载 IP 段列表，校验通过后写入 localFile 作为缓存。
func loadOrDownload(localFile, url string) ([]string, error) {
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
	if err := cfcommon.WriteLinesAtomic(localFile, lines); err != nil {
		// 能下载到就够了，缓存写不进去不致命
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
