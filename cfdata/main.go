package main

import (
	"context"
	"flag"
	"fmt"

	"cftest/internal/cfcommon"
	"cftest/internal/cfdata"
)

var (
	hostHeader        = flag.String("host", cfdata.DefaultHost, "HTTP请求Host头(CF已禁止裸IP直连)")
	flagMode          = flag.String("mode", "", "运行模式 1|2|3 (1=扫Colo+详测,2=筛Colo,3=按国家筛)")
	flagColo          = flag.String("colo", "", "目标Colo 逗号分隔,如 HKG,NRT")
	flagCountry       = flag.String("country", "", "目标国家代码 逗号分隔,如 HK,JP")
	flagIPType        = flag.String("iptype", "4", "4 或 6")
	flagPort          = flag.Int("port", 443, "详细测试端口")
	flagDelay         = flag.Int("delay", 500, "详细测试延迟阈值 ms")
	flagScanThreads   = flag.Int("scan-threads", 50, "扫描并发数")
	flagTestThreads   = flag.Int("test-threads", 25, "测试并发数")
	flagScanAlias     = flag.Int("scan", -1, "兼容旧版: 扫描阶段最大并发数")
	flagTestAlias     = flag.Int("test", -1, "兼容旧版: 详细测试阶段最大并发数")
	flagNumPerColo    = flag.Int("num-per-colo", 0, "每个 Colo 查找 IP 数 (0=不限)")
	flagDetail        = flag.Bool("detail", true, "模式1/3 是否跑详细测试")
	flagBatch         = flag.Bool("batch", false, "非交互模式,缺失参数用默认值")
	flagIPv6PerSubnet = flag.Int("ipv6-per-subnet", 100, "IPv6 每个 /48 子网生成的 IP 数")
	flagVersion       = flag.Bool("version", false, "显示版本号")
)

func main() {
	flag.Parse()
	if *flagVersion {
		cfcommon.PrintVersionAndExit("cfdata")
	}
	normalizeCompatFlags()
	fmt.Println("═══ CFData — Cloudflare IP 段扫描筛选工具 ═══")

	if *flagMode != "" {
		runFromFlags()
		return
	}

	for {
		fmt.Println()
		fmt.Println("═════════════════════════════════════════════")
		fmt.Println("           CFData 主菜单")
		fmt.Println("═════════════════════════════════════════════")
		fmt.Println(" 1. 扫描指定Colo并详细测试")
		fmt.Println(" 2. 筛选特定Colo的IP")
		fmt.Println(" 3. 按国家代码筛选IP")
		fmt.Println(" 0. 退出")
		fmt.Println("─────────────────────────────────────────────")
		mode := cfcommon.PromptChoice("选择 (0/1/2/3, 默认1): ",
			[]string{"0", "1", "2", "3"}, "1")
		if mode == "0" {
			fmt.Println("已退出。")
			return
		}

		scanThreads := cfcommon.PromptInt("扫描并发数 (默认50): ", 50)
		testThreads := cfcommon.PromptInt("测试并发数 (默认25): ", 25)
		port := cfcommon.PromptInt("测试端口 (默认443): ", 443)
		delay := cfcommon.PromptInt("延迟阈值ms (默认500): ", 500)

		cfg := cfdata.Config{
			Mode:        mode,
			ScanThreads: scanThreads,
			TestThreads: testThreads,
			Port:        port,
			Delay:       delay,
			HostHeader:  *hostHeader,
			Detail:      true,
		}
		switch mode {
		case "1":
			cfg.Colo = cfcommon.PromptString("输入目标Colo (逗号分隔, 最多5个, 如 HKG,NRT): ", "")
			cfg.IPType = cfcommon.PromptChoice("IPv4还是IPv6? (4/6, 默认4): ", []string{"4", "6"}, "4")
			cfg.NumPerColo = cfcommon.PromptInt("每个Colo查找IP数 (0=不限): ", 0)
			answer := cfcommon.PromptChoice("是否进行详细延迟测试? (y/n, 默认y): ", []string{"y", "n"}, "y")
			cfg.Detail = answer == "y"
		case "2":
			cfg.Colo = cfcommon.PromptString("输入目标Colo (默认 hkg,nrt,sin,tpa): ", "hkg,nrt,sin,tpa")
			cfg.IPType = cfcommon.PromptChoice("IPv4还是IPv6? (4/6, 默认4): ", []string{"4", "6"}, "4")
			cfg.NumPerColo = cfcommon.PromptInt("每个Colo查找IP数 (0=不限, 默认0): ", 0)
			cfg.Detail = false
		case "3":
			cfg.Country = cfcommon.PromptString("输入国家代码 (逗号分隔, 如 HK,JP,US): ", "")
			cfg.IPType = cfcommon.PromptChoice("IPv4还是IPv6? (4/6, 默认4): ", []string{"4", "6"}, "4")
			answer := cfcommon.PromptChoice("是否进行详细测试? (y/n, 默认y): ", []string{"y", "n"}, "y")
			cfg.Detail = answer == "y"
		}
		runEngine(cfg)
	}
}

func normalizeCompatFlags() {
	if *flagScanAlias >= 0 {
		*flagScanThreads = *flagScanAlias
	}
	if *flagTestAlias >= 0 {
		*flagTestThreads = *flagTestAlias
	}
}

func runFromFlags() {
	runEngine(cfdata.Config{
		Mode:          *flagMode,
		Colo:          *flagColo,
		Country:       *flagCountry,
		IPType:        *flagIPType,
		Port:          *flagPort,
		Delay:         *flagDelay,
		ScanThreads:   *flagScanThreads,
		TestThreads:   *flagTestThreads,
		NumPerColo:    *flagNumPerColo,
		Detail:        *flagDetail,
		IPv6PerSubnet: *flagIPv6PerSubnet,
		HostHeader:    *hostHeader,
	})
}

func runEngine(cfg cfdata.Config) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopSig := cfcommon.HandleInterrupt(cancel, "[中断] 正在保存已有结果...")
	defer stopSig()
	if _, err := cfdata.Run(ctx, cfg, cfcommon.StdoutReporter()); err != nil {
		fmt.Printf("[错误] %v\n", err)
	}
}
