package cfcommon

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MeasureTCPLatency 通过 TCP 连接测量延迟。
func MeasureTCPLatency(ctx context.Context, ip, port string, timeout time.Duration) (time.Duration, error) {
	dialer := &net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

// NewForcedIPHTTPClient 创建一个强制拨到指定 IP 的 HTTP 客户端。
// targetURLStr 的 host/scheme 用于决定 Host 头、端口和 TLS SNI。
func NewForcedIPHTTPClient(ip, targetURLStr string, timeout time.Duration) (*http.Client, error) {
	targetURL, err := url.Parse(targetURLStr)
	if err != nil {
		return nil, fmt.Errorf("解析 URL 失败: %w", err)
	}

	port := targetURL.Port()
	if port == "" {
		if strings.EqualFold(targetURL.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}

	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
		},
		DisableKeepAlives: true,
	}
	if strings.EqualFold(targetURL.Scheme, "https") {
		transport.TLSClientConfig = &tls.Config{
			ServerName: targetURL.Hostname(),
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}

// MeasureDownloadSpeed 从指定 URL 下载数据并计算 Mbps。
// limitBytes 为 0 时不限制下载量。
func MeasureDownloadSpeed(ctx context.Context, ip, targetURL string, timeout time.Duration,
	limitBytes int64, earlyCheck time.Duration, minMbps float64, bufferSize int) (float64, int64, error) {
	client, err := NewForcedIPHTTPClient(ip, targetURL, timeout)
	if err != nil {
		return 0, 0, err
	}
	return measureDownloadWithClient(ctx, client, targetURL, limitBytes, earlyCheck, minMbps, bufferSize)
}

// MeasureDownloadSpeedViaProxy 经本地 HTTP/SOCKS 代理下载测速。
func MeasureDownloadSpeedViaProxy(ctx context.Context, proxyURL, targetURL string, timeout time.Duration,
	limitBytes int64, earlyCheck time.Duration, minMbps float64, bufferSize int) (float64, int64, error) {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return 0, 0, fmt.Errorf("解析代理 URL 失败: %w", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(parsed),
			DisableKeepAlives: true,
		},
		Timeout: timeout,
	}
	return measureDownloadWithClient(ctx, client, targetURL, limitBytes, earlyCheck, minMbps, bufferSize)
}

func measureDownloadWithClient(ctx context.Context, client *http.Client, targetURL string,
	limitBytes int64, earlyCheck time.Duration, minMbps float64, bufferSize int) (float64, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, 0, err
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, 0, ctxErr
		}
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("HTTP 状态码异常: %d", resp.StatusCode)
	}

	reader := io.Reader(resp.Body)
	if limitBytes > 0 {
		reader = io.LimitReader(resp.Body, limitBytes)
	}
	if bufferSize <= 0 {
		bufferSize = 32 * 1024
	}
	buf := make([]byte, bufferSize)

	var downloaded int64
	earlyChecked := false

	speed := func() float64 {
		elapsed := time.Since(start)
		if elapsed <= 0 {
			return 0
		}
		return float64(downloaded) * 8 / elapsed.Seconds() / 1e6
	}

	for {
		// 每轮先看 ctx：若恰好在两次读取之间到期，用已下载数据计速，
		// 而不是把这份本来有效的测速结果丢掉。
		if err := ctx.Err(); err != nil {
			if downloaded > 0 && !errors.Is(err, context.Canceled) {
				return speed(), downloaded, nil
			}
			if downloaded == 0 {
				return 0, downloaded, err
			}
			return speed(), downloaded, err
		}

		n, readErr := reader.Read(buf)
		downloaded += int64(n)

		if !earlyChecked && earlyCheck > 0 && time.Since(start) >= earlyCheck {
			earlyChecked = true
			if s := speed(); s < minMbps {
				return s, downloaded, fmt.Errorf("速度过慢 (%.2f Mbps < %.2f Mbps)", s, minMbps)
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if downloaded > 0 {
				// 用户主动取消仍算失败；超时/连接重置则用已下载数据计速
				if errors.Is(ctx.Err(), context.Canceled) {
					return 0, downloaded, ctx.Err()
				}
				return speed(), downloaded, nil
			}
			return 0, downloaded, readErr
		}
	}

	if downloaded <= 0 {
		return 0, downloaded, fmt.Errorf("下载数据量过少")
	}
	return speed(), downloaded, nil
}

// FetchTraceMap 读取 trace 响应并解析 key=value。
func FetchTraceMap(ctx context.Context, ip, traceURL string, timeout time.Duration) (map[string]string, error) {
	client, err := NewForcedIPHTTPClient(ip, traceURL, timeout)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trace HTTP 状态码异常: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("trace 响应为空")
	}
	info := ParseTraceMap(trimmed)
	if len(info) == 0 {
		return nil, fmt.Errorf("trace 响应无法解析")
	}
	return info, nil
}

// ParseTraceMap 解析 Cloudflare trace 的 key=value 文本。
func ParseTraceMap(body string) map[string]string {
	info := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		info[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return info
}
