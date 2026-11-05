package cfcommon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Location 数据中心位置信息。
type Location struct {
	IATA   string  `json:"iata"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	CCA2   string  `json:"cca2"`
	Region string  `json:"region"`
	City   string  `json:"city"`
}

const defaultLocationURL = "https://speed.cloudflare.com/locations"

var (
	locCache     map[string]Location
	locCacheMux  sync.Mutex
	locationURL  = defaultLocationURL
	locationPath = "locations.json"
)

// LoadLocations 读取 locations.json（colo→城市对照的唯一数据源）。
//
// 查找顺序优先 /app/locations.json（Docker 镜像内位置），再依次尝试可执行
// 文件所在目录、当前目录及 /data。只有在"本地都没找到、走网络下载"时才会把
// 结果缓存到 locationPath，避免镜像更新后 /data 里的旧副本继续遮蔽 /app 的新数据。
// 返回 IATA(大写) → Location 的映射。进程级缓存，多次调用只读一次。
func LoadLocations() (map[string]Location, error) {
	return LoadLocationsCtx(context.Background())
}

// LoadLocationsCtx 与 LoadLocations 相同，但下载可被取消（例如用户点了「停止」）。
// 读文件与网络 IO 都在锁外完成，只在读写进程级缓存时持锁，避免一次慢下载
// 把其它阶段的 LoadLocations 全部堵住。
func LoadLocationsCtx(ctx context.Context) (map[string]Location, error) {
	locCacheMux.Lock()
	cached := locCache
	locCacheMux.Unlock()
	if cached != nil {
		return cached, nil
	}

	data, downloaded, err := loadLocationData(ctx)
	if err != nil {
		return nil, err
	}
	var locs []Location
	if err := json.Unmarshal(data, &locs); err != nil {
		return nil, fmt.Errorf("解析 locations.json 失败: %w", err)
	}
	if downloaded && len(locs) > 0 {
		if writeErr := WriteLinesAtomic(locationPath, []string{string(data)}); writeErr != nil {
			fmt.Printf("[警告] locations.json 缓存失败: %v\n", writeErr)
		}
	}

	m := make(map[string]Location, len(locs))
	for _, loc := range locs {
		m[strings.ToUpper(loc.IATA)] = loc
	}
	locCacheMux.Lock()
	if locCache != nil {
		m = locCache // 并发加载时以先完成者为准
	} else {
		locCache = m
	}
	locCacheMux.Unlock()
	return m, nil
}

func loadLocationData(ctx context.Context) ([]byte, bool, error) {
	for _, path := range locationSearchPaths() {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b, false, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, locationURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("构造 locations.json 下载请求失败: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("locations.json 未找到且下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("下载 locations.json 失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func locationSearchPaths() []string {
	paths := []string{
		"/app/locations.json",
		"locations.json",
		filepath.Join("..", "locations.json"),
		"/data/locations.json",
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append([]string{
			filepath.Join(dir, "locations.json"),
			filepath.Join(dir, "..", "locations.json"),
		}, paths...)
	}
	return paths
}

func WorkPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}
