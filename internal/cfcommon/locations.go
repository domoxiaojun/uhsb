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

// LoadLocations 依次尝试 ./locations.json、../locations.json,最后从 CF 下载并缓存到 ./locations.json。
// 返回 IATA(大写) → Location 的映射。进程级缓存,多次调用只读一次。
func LoadLocations() (map[string]Location, error) {
	locCacheMux.Lock()
	defer locCacheMux.Unlock()
	if locCache != nil {
		return locCache, nil
	}

	var data []byte
	var err error
	for _, path := range locationSearchPaths() {
		if data, err = os.ReadFile(path); err == nil {
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
	}

	var locs []Location
	if err := json.Unmarshal(data, &locs); err != nil {
		return nil, fmt.Errorf("解析 locations.json 失败: %w", err)
	}
	if _, err := os.Stat(locationPath); err != nil && os.IsNotExist(err) {
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
		"locations.json",
		filepath.Join("..", "locations.json"),
		"/app/locations.json",
		"/data/locations.json",
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(dir, "locations.json"),
			filepath.Join(dir, "..", "locations.json"),
		)
	}
	return paths
}

func WorkPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}
