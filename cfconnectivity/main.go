package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"cftest/internal/cfcommon"
	"cftest/internal/cfconnectivity"
)

// exitCode 让批处理调用方（CI / 脚本）能通过退出码判断失败。
var exitCode int

var (
	inputFile      = flag.String("input", "ip.txt", "输入IP文件路径(支持IPv4/IPv6)")
	workers        = flag.Int("workers", cfcommon.DefaultConnWorkers, "并发测试数量")
	testRounds     = flag.Int("rounds", cfcommon.DefaultConnRounds, "每个IP的测试轮数")
	connectTimeout = flag.Int("timeout", cfcommon.DefaultConnTimeout, "连接超时时间(秒)")
	maxPacketLoss  = flag.Float64("loss", cfcommon.DefaultConnLoss, "最大允许丢包率(%)")
	minSpeed       = flag.Float64("speed", cfcommon.DefaultConnSpeed, "最低速度要求(Mbps)")
	maxLatency     = flag.Int("latency", cfcommon.DefaultConnLatency, "最大延迟要求(ms)")
	outputPrefix   = flag.String("output", "cfconnectivity", "输出文件前缀")
	verbose        = flag.Bool("verbose", false, "详细输出")
	preferredColos = flag.String("colos", cfcommon.DefaultPreferredColos, "优先的Colo代码(逗号分隔)")
	coloFilter     = flag.String("colo-filter", "", "只测试指定Colo的IP(逗号分隔),留空测试全部")
	downloadSize   = flag.Int("size", cfcommon.DefaultConnSize, "下载测试文件大小(MB)")
	nonInteractive = flag.Bool("noninteractive", false, "非交互模式")
	fetchRetries   = flag.Int("colo-retries", cfcommon.DefaultColoRetries, "trace 获取失败时的重试次数")

	inputFileShort      = flag.String("f", "", "输入IP文件路径(-input 别名)")
	workersShort        = flag.Int("w", -1, "并发数(-workers 别名)")
	outputPrefixShort   = flag.String("o", "", "输出前缀(-output 别名)")
	verboseShort        = flag.Bool("v", false, "详细输出(-verbose 别名)")
	preferredColosShort = flag.String("preferred-colo", "", "优先Colo(-colos 别名)")
	coloFilterShort     = flag.String("colo", "", "仅测试指定Colo(-colo-filter 别名)")
	batchShort          = flag.Bool("batch", false, "非交互模式(-noninteractive 别名)")
	showVersion         = flag.Bool("version", false, "显示版本号")
)

func main() {
	defer func() {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()
	flag.Parse()
	if *showVersion {
		cfcommon.PrintVersionAndExit("cfconnectivity")
	}
	normalizeCompatFlags()

	if !*nonInteractive {
		fmt.Println("═══ CFConnectivity — Cloudflare IP 连通性深度测试 ═══")
		if v := cfcommon.PromptString(fmt.Sprintf("IP文件 (默认 %s): ", *inputFile), *inputFile); v != "" {
			*inputFile = v
		}
		if v := cfcommon.PromptInt(fmt.Sprintf("并发数 (默认 %d): ", *workers), *workers); v > 0 {
			*workers = v
		}
		if v := cfcommon.PromptInt(fmt.Sprintf("下载文件大小MB (默认 %d): ", *downloadSize), *downloadSize); v > 0 {
			*downloadSize = v
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopSig := cfcommon.HandleInterrupt(cancel, "[中断] 正在保存结果...")
	defer stopSig()

	fmt.Println("\n═══ CFConnectivity — Cloudflare IP 连通性深度测试 ═══")
	_, err := cfconnectivity.Run(ctx, cfconnectivity.Config{
		InputFile:      *inputFile,
		OutputPrefix:   *outputPrefix,
		PreferredColos: *preferredColos,
		ColoFilter:     *coloFilter,
		Workers:        *workers,
		TestRounds:     *testRounds,
		ConnectTimeout: *connectTimeout,
		DownloadSize:   *downloadSize,
		FetchRetries:   *fetchRetries,
		MaxLatency:     *maxLatency,
		MaxPacketLoss:  *maxPacketLoss,
		MinSpeed:       *minSpeed,
		Verbose:        *verbose,
	}, cfcommon.StdoutReporter())
	if err != nil {
		fmt.Printf("[错误] %v\n", err)
		exitCode = 1
	}
}

func normalizeCompatFlags() {
	if *inputFileShort != "" {
		*inputFile = *inputFileShort
	}
	if *workersShort >= 0 {
		*workers = *workersShort
	}
	if *outputPrefixShort != "" {
		*outputPrefix = *outputPrefixShort
	}
	if *verboseShort {
		*verbose = true
	}
	if *preferredColosShort != "" {
		*preferredColos = *preferredColosShort
	}
	if *coloFilterShort != "" {
		*coloFilter = *coloFilterShort
	}
	if *batchShort {
		*nonInteractive = true
	}
}
