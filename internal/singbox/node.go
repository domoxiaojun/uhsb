package singbox

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"cftest/internal/cfcommon"
)

type Node struct {
	Server     string
	UUID       string
	Host       string
	Path       string
	SNI        string
	Port       int
	TLS        bool
	AllowInsec bool
	Tag        string
}

func (n Node) withDefaults() Node {
	if n.Port <= 0 {
		n.Port = cfcommon.DefaultVMessPort
	}
	if n.Path == "" {
		n.Path = cfcommon.DefaultVMessPath
	}
	if !strings.HasPrefix(n.Path, "/") {
		n.Path = "/" + n.Path
	}
	if n.SNI == "" {
		n.SNI = n.Host
	}
	if n.Tag == "" {
		n.Tag = "cf-vmess"
	}
	return n
}

func (n Node) ForIP(ip, tag string) Node {
	out := n.withDefaults()
	out.Server = ip
	if tag != "" {
		out.Tag = tag
	}
	return out
}

func (n Node) VMessURL() string {
	n = n.withDefaults()
	tls := ""
	if n.TLS {
		tls = "tls"
	}
	payload := map[string]any{
		"v":    "2",
		"ps":   n.Tag,
		"add":  n.Server,
		"port": strconv.Itoa(n.Port),
		"id":   n.UUID,
		"aid":  "0",
		"scy":  "auto",
		"net":  "ws",
		"type": "none",
		"host": n.Host,
		"path": n.Path,
		"tls":  tls,
		"sni":  n.SNI,
		"alpn": "",
		"fp":   "",
	}
	raw, _ := json.Marshal(payload)
	return "vmess://" + base64.StdEncoding.EncodeToString(raw)
}

func (n Node) ClientConfigJSON() ([]byte, error) {
	n = n.withDefaults()
	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []any{
			map[string]any{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "127.0.0.1",
				"listen_port": 1080,
			},
		},
		"outbounds": []any{
			n.outbound(),
			map[string]any{"type": "direct", "tag": "direct"},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func (n Node) Outbound() map[string]any {
	return n.outbound()
}

func (n Node) outbound() map[string]any {
	n = n.withDefaults()
	tls := map[string]any{"enabled": n.TLS, "server_name": n.SNI, "insecure": n.AllowInsec}
	if !n.TLS {
		tls = map[string]any{"enabled": false}
	}
	return map[string]any{
		"type":        "vmess",
		"tag":         n.Tag,
		"server":      n.Server,
		"server_port": n.Port,
		"uuid":        n.UUID,
		"security":    "auto",
		"tls":         tls,
		"transport": wsTransport(n.Path, n.Host),
	}
}

// wsTransport 把 V2Ray/Xray 分享链接里的 `path?ed=2048` 翻译成 sing-box 字段。
// sing-box 1.14 不会解析 path 中的 ed 查询；若原样写入，问号会被当成路径的
// 一部分，WebSocket 握手返回 404。ed 是路径上的早期数据长度，对应
// max_early_data；不设置 early_data_header_name，让 sing-box 按 Xray 的
// 默认方式把早期数据追加到路径末尾。
func wsTransport(path, host string) map[string]any {
	cleanPath, earlyData := splitEarlyData(path)
	transport := map[string]any{
		"type": "ws",
		"path": cleanPath,
		"headers": map[string]any{
			"Host": host,
		},
	}
	if earlyData > 0 {
		transport["max_early_data"] = earlyData
	}
	return transport
}

func splitEarlyData(path string) (string, int) {
	u, err := url.Parse(path)
	if err != nil || u.RawQuery == "" {
		return path, 0
	}
	query := u.Query()
	raw := query.Get("ed")
	if raw == "" {
		return path, 0
	}
	earlyData, err := strconv.Atoi(raw)
	if err != nil || earlyData < 0 {
		return path, 0
	}
	query.Del("ed")
	u.RawQuery = query.Encode()
	cleaned := u.Path
	if cleaned == "" {
		cleaned = "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	if u.RawQuery != "" {
		cleaned += "?" + u.RawQuery
	}
	return cleaned, earlyData
}

func runtimeConfig(n Node, listen string, listenPort int) map[string]any {
	n = n.withDefaults()
	return map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{
			map[string]any{
				"type":        "http",
				"tag":         "http-in",
				"listen":      listen,
				"listen_port": listenPort,
			},
		},
		"outbounds": []any{n.outbound()},
	}
}
