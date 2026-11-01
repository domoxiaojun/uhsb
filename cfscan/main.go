package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"cftest/internal/cfcommon"
	"cftest/internal/cfscan"
)

var (
	workers     = flag.Int("w", 100, "并发worker数量")
	retries     = flag.Int("r", 1, "失败后的重试次数")
	mtrCycles   = flag.Int("c", 2, "MTR每节点探测包数量")
	mtrTimeout  = flag.Int("t", 10, "MTR超时(秒)")
	verbose     = flag.Bool("v", false, "详细输出")
	inputFile   = flag.String("f", "", "IP文件路径")
	outputPfx   = flag.String("o", "", "输出文件前缀(默认用输入文件名)")
	hostHeader  = flag.String("host", cfscan.DefaultHost, "HTTP请求Host头(CF已禁止裸IP直连)")
	showVersion = flag.Bool("version", false, "显示版本号")
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "CFScan — Cloudflare IP 路由质量分析 (Linux/macOS, MTR)\n\n")
		fmt.Fprintf(os.Stderr, "通过 MTR 分析路由质量，匹配关键字(hk/tw/tpe)、CMI线路、高跳数路线。\n")
		fmt.Fprintf(os.Stderr, "管道上游: cfdata 输出的 *_ips.txt\n")
		fmt.Fprintf(os.Stderr, "管道下游: cfspeed -f *_quality.txt\n\n")
		flag.PrintDefaults()
	}
}

func main() {
	flag.Parse()
	if *showVersion {
		cfcommon.PrintVersionAndExit("cfscan")
	}

	fmt.Println("═══ CFScan — 路由质量分析 (MTR) ═══")

	selectedFile := *inputFile
	if selectedFile == "" {
		selectedFile = cfcommon.PromptString("IP文件路径 (默认 testip.txt): ", "testip.txt")
		if _, err := os.Stat(selectedFile); os.IsNotExist(err) {
			fmt.Printf("[错误] 文件不存在: %s\n", selectedFile)
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopSig := cfcommon.HandleInterrupt(cancel, "[中断] 正在停止...")
	defer stopSig()

	_, err := cfscan.Run(ctx, cfscan.Config{
		InputFile:  selectedFile,
		OutputPfx:  *outputPfx,
		Workers:    *workers,
		Retries:    *retries,
		MTRCycles:  *mtrCycles,
		MTRTimeout: *mtrTimeout,
		Verbose:    *verbose,
		HostHeader: *hostHeader,
	}, cfcommon.StdoutReporter())
	if err != nil {
		fmt.Printf("[错误] %v\n", err)
	}
}
