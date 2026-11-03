package cfcommon

import (
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
	locCacheMux.Lock()
	defer locCacheMux.Unlock()
	if locCache != nil {
		return locCache, nil
	}

	var data []byte
	downloaded := false
	for _, path := range locationSearchPaths() {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			data = b
			break
		}
	}
	if data == nil {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(locationURL)
		if err != nil {
			return nil, fmt.Errorf("locations.json 未找到且下载失败: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("下载 locations.json 失败: HTTP %d", resp.StatusCode)
		}
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		downloaded = true
	}

	var locs []Location
	if err := json.Unmarshal(data, &locs); err != nil {
		return nil, fmt.Errorf("解析 locations.json 失败: %w", err)
	}
	if downloaded && len(locs) > 0 {
		if writeErr := os.WriteFile(locationPath, data, 0644); writeErr != nil {
			fmt.Printf("[警告] locations.json 缓存失败: %v\n", writeErr)
		}
	}

	m := make(map[string]Location, len(locs))
	for _, loc := range locs {
		m[strings.ToUpper(loc.IATA)] = loc
	}
	locCache = m
	return m, nil
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
