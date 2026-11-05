package singbox

import (
	"encoding/base64"
	"encoding/json"
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
		"transport": map[string]any{
			"type": "ws",
			"path": n.Path,
			"headers": map[string]any{
				"Host": n.Host,
			},
		},
	}
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
