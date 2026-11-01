package cfcommon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var coloRegex = regexp.MustCompile(`colo=([A-Z]+)`)

// GetColo 通过已建立的 TCP conn 发送一次 HTTP /cdn-cgi/trace,解析 colo=XXX。
// 不含重试:失败返 error,由调用方决定是否重试。
// hostHeader 用于绕过 CF 对裸 IP 的拦截(如 "www.visa.cn")。
// timeout 控制整个 HTTP 请求(含读响应)。
func GetColo(conn net.Conn, ip, hostHeader string, timeout time.Duration) (string, error) {
	client := http.Client{
		Transport: &http.Transport{
			Dial: func(_, _ string) (net.Conn, error) { return conn, nil },
		},
		Timeout: timeout,
	}
	req, _ := http.NewRequest("GET", "http://"+net.JoinHostPort(ip, "80")+"/cdn-cgi/trace", nil)
	req.Host = hostHeader
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Close = true

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	s := string(body)
	// 先尝试 colo=XXX 行前缀
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "colo=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "colo=")), nil
		}
	}
	// fallback 正则
	if m := coloRegex.FindStringSubmatch(s); len(m) > 1 {
		return m[1], nil
	}
	return "", fmt.Errorf("未找到 colo 字段")
}

// DialAndGetColo 一次性 Dial + GetColo,timeout 拆分给 dial/http。
func DialAndGetColo(ctx context.Context, ip, hostHeader string, dialTimeout, httpTimeout time.Duration) (string, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, "80"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return GetColo(conn, ip, hostHeader, httpTimeout)
}
