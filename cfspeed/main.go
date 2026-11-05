package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"cftest/internal/cfcommon"
	"cftest/internal/cfspeed"
)

// exitCode 让批处理调用方（CI / 脚本）能通过退出码判断失败。
var exitCode int

var (
	modeFlag           = flag.String("mode", cfcommon.DefaultSpeedMode, "运行模式: batch|optimize|single|history")
	protocolFlag       = flag.String("protocol", cfcommon.DefaultSpeedProtocol, "测速协议: http|https")
	singleIPFlag       = flag.String("ip", "", "单IP模式的目标IP")
	speedHostFlag      = flag.String("speed-host", cfspeed.DefaultSpeedHost, "测速域名")
	historyFileFlag    = flag.String("history-file", "speed_test_results.csv", "历史结果CSV路径")
	inputFile          = flag.String("f", "speed.txt", "输入IP文件路径")
	workers            = flag.Int("w", cfcommon.DefaultWorkers, "并发测试数量")
	testRounds         = flag.Int("rounds", cfcommon.DefaultTestRounds, "每个IP的延迟测试轮数")
	connectTimeout     = flag.Int("timeout", cfcommon.DefaultConnectTimeout, "连接超时(秒)")
	maxPacketLoss      = flag.Float64("loss", cfcommon.DefaultMaxPacketLoss, "最大允许丢包率(%)")
	minSpeedMbps       = flag.Float64("speed", cfcommon.DefaultMinSpeedMbps, "最低速度要求(Mbps)")
	maxLatency         = flag.Int("latency", cfcommon.DefaultMaxLatency, "最大延迟(ms)")
	downloadSizeMB     = flag.Int("size", cfcommon.DefaultSpeedSize, "测速文件大小(MB), 如: 10, 50, 200")
	outputPrefix       = flag.String("o", "cfspeed", "输出文件前缀")
	coloFilter         = flag.String("colo", "", "只测指定Colo(逗号分隔,如:HKG,SIN)")
	verbose            = flag.Bool("v", false, "详细输出")
	nonInteractive     = flag.Bool("batch", false, "非交互模式")
	hostHeader         = flag.String("host", cfspeed.DefaultHost, "HTTP请求Host头(CF已禁止裸IP直连)")
	jitterBaseline     = flag.Float64("jitter-baseline", cfcommon.DefaultJitterBaseline, "稳定性评分归零的抖动ms阈值")
	coloRetries        = flag.Int("colo-retries", cfcommon.DefaultColoRetries, "trace 获取 colo 失败的重试次数 (0 = 不重试)")
	showVersion        = flag.Bool("version", false, "显示版本号")
	useSingBox         = flag.Bool("singbox", false, "经 sing-box 用 VMess+WS 测速")
	singBoxBinary      = flag.String("singbox-bin", "", "sing-box 二进制路径,空则 PATH 查找")
	vmessUUID          = flag.String("uuid", "", "VMess UUID")
	vmessHost          = flag.String("ws-host", "", "VMess WS Host / SNI")
	vmessPath          = flag.String("ws-path", cfcommon.DefaultVMessPath, "VMess WS path")
	vmessSNI           = flag.String("sni", "", "TLS SNI,默认跟 ws-host")
	vmessPort          = flag.Int("vmess-port", cfcommon.DefaultVMessPort, "VMess 端口")
	vmessTLS           = flag.Bool("vmess-tls", cfcommon.DefaultVMessTLS, "VMess 是否 TLS")
	vmessAllowInsecure = flag.Bool("vmess-insecure", cfcommon.DefaultVMessAllowInsecure, "跳过 TLS 校验")
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "CFSpeed — Cloudflare IP 速度优选工具\n\n")
		fmt.Fprintf(os.Stderr, "支持批量测速、优选、单IP测速和历史结果查看。\n")
		fmt.Fprintf(os.Stderr, "默认下载URL: %s\n\n", cfspeed.BuildDownloadURL("http", cfspeed.DefaultSpeedHost, cfcommon.DefaultSpeedSize))
		flag.PrintDefaults()
	}
}

func main() {
	defer func() {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()
	flag.Parse()
	if *showVersion {
		cfcommon.PrintVersionAndExit("cfspeed")
	}
	if len(os.Args) <= 1 {
		runMenu()
		return
	}
	runOnce()
}

func runOnce() {
	cfg := currentConfig()
	fmt.Println("═══ CFSpeed — Cloudflare IP 速度优选 ═══")
	fmt.Printf("模式: %s | 协议: %s | 测速: %s\n",
		cfg.Mode, strings.ToUpper(cfg.Protocol), cfspeed.BuildDownloadURL(cfg.Protocol, cfg.SpeedHost, cfg.DownloadSizeMB))

	if cfg.Mode == "single" && !*nonInteractive && cfg.SingleIP == "" {
		cfg.SingleIP = cfcommon.PromptString("请输入目标IP: ", "")
	}
	if (cfg.Mode == "batch" || cfg.Mode == "optimize") && !*nonInteractive {
		promptTuning(&cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopSig := cfcommon.HandleInterrupt(cancel, "[中断] 正在保存已有结果...")
	defer stopSig()

	if _, err := cfspeed.Run(ctx, cfg, cfcommon.StdoutReporter()); err != nil {
		fmt.Printf("[错误] %v\n", err)
		exitCode = 1
	}
}

func currentConfig() cfspeed.Config {
	return cfspeed.Config{
		Mode:               *modeFlag,
		Protocol:           *protocolFlag,
		InputFile:          *inputFile,
		SingleIP:           *singleIPFlag,
		SpeedHost:          *speedHostFlag,
		HistoryFile:        *historyFileFlag,
		OutputPrefix:       *outputPrefix,
		HostHeader:         *hostHeader,
		ColoFilter:         *coloFilter,
		Workers:            *workers,
		TestRounds:         *testRounds,
		ConnectTimeout:     *connectTimeout,
		MaxLatency:         *maxLatency,
		DownloadSizeMB:     *downloadSizeMB,
		ColoRetries:        *coloRetries,
		MaxPacketLoss:      *maxPacketLoss,
		MinSpeedMbps:       *minSpeedMbps,
		JitterBaseline:     *jitterBaseline,
		Verbose:            *verbose,
		UseSingBox:         *useSingBox,
		SingBoxBinary:      *singBoxBinary,
		VMessUUID:          *vmessUUID,
		VMessHost:          *vmessHost,
		VMessPath:          *vmessPath,
		VMessSNI:           *vmessSNI,
		VMessPort:          *vmessPort,
		VMessTLS:           *vmessTLS,
		VMessAllowInsecure: *vmessAllowInsecure,
	}
}

func promptTuning(cfg *cfspeed.Config) {
	if v := cfcommon.PromptInt(fmt.Sprintf("测速文件大小MB (当前 %d): ", cfg.DownloadSizeMB), cfg.DownloadSizeMB); v > 0 {
		cfg.DownloadSizeMB = v
	}
}
