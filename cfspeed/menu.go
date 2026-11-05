package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"

	"cftest/internal/cfcommon"
	"cftest/internal/cfspeed"
)

var (
	menuMu       sync.Mutex
	activeCancel context.CancelFunc
	interrupted  atomic.Bool
	menuSignalCh chan os.Signal
)

// installMenuSignals 首次 Ctrl+C 取消当前测速（让引擎有机会落盘已有结果），
// 空闲时则退出菜单；第二次 Ctrl+C 才强制 os.Exit。
func installMenuSignals() {
	menuSignalCh = make(chan os.Signal, 1)
	signal.Notify(menuSignalCh, os.Interrupt)
	go func() {
		count := 0
		for range menuSignalCh {
			count++
			if count >= 2 {
				fmt.Println("\n[强制退出]")
				os.Exit(130)
			}
			interrupted.Store(true)
			menuMu.Lock()
			cancel := activeCancel
			menuMu.Unlock()
			if cancel != nil {
				fmt.Println("\n[中断] 已请求停止，正在保存已有结果…（再按一次 Ctrl+C 强制退出）")
				cancel()
			} else {
				fmt.Println("\n[中断] 退出菜单（再按一次 Ctrl+C 可强制退出）")
			}
		}
	}()
}

func stopMenuSignals() {
	signal.Stop(menuSignalCh)
	close(menuSignalCh)
}

func runMenu() {
	fmt.Println("═══ CFSpeed — Cloudflare IP 速度优选 (交互菜单) ═══")
	installMenuSignals()
	defer stopMenuSignals()

	for {
		if interrupted.Load() {
			fmt.Println("已退出。")
			return
		}
		fmt.Println()
		fmt.Println("═════════════════════════════════════════════")
		fmt.Println("           CFSpeed 主菜单")
		fmt.Println("═════════════════════════════════════════════")
		fmt.Println(" 1. IPv4 优选 (HTTPS)")
		fmt.Println(" 2. IPv4 优选 (HTTP)")
		fmt.Println(" 3. IPv6 优选 (HTTPS)")
		fmt.Println(" 4. IPv6 优选 (HTTP)")
		fmt.Println(" 5. 单 IP 测速 (HTTPS)")
		fmt.Println(" 6. 单 IP 测速 (HTTP)")
		fmt.Println(" 7. 批量速度测试")
		fmt.Println(" 8. 查看历史结果")
		fmt.Println(" 0. 退出")
		fmt.Println("─────────────────────────────────────────────")
		choice := cfcommon.PromptString("请选择 (0-8): ", "")
		switch strings.TrimSpace(choice) {
		case "1":
			menuOptimize("speed.txt", "https")
		case "2":
			menuOptimize("speed.txt", "http")
		case "3":
			menuOptimize("ipv6.txt", "https")
		case "4":
			menuOptimize("ipv6.txt", "http")
		case "5":
			menuSingle("https")
		case "6":
			menuSingle("http")
		case "7":
			menuBatch()
		case "8":
			runEngine(cfspeed.Config{Mode: "history", HistoryFile: *historyFileFlag})
		case "0", "q", "Q":
			fmt.Println("已退出。")
			return
		}
	}
}

func menuOptimize(defaultFile, protocol string) {
	cfg := currentConfig()
	cfg.Mode = "optimize"
	cfg.Protocol = protocol
	cfg.InputFile = cfcommon.PromptString(fmt.Sprintf("IP文件 (默认 %s): ", defaultFile), defaultFile)
	if v := cfcommon.PromptInt(fmt.Sprintf("期望带宽门槛 Mbps (默认 %.0f): ", cfg.MinSpeedMbps), int(cfg.MinSpeedMbps)); v > 0 {
		cfg.MinSpeedMbps = float64(v)
		cfg.BandwidthGoal = float64(v)
	}
	if v := cfcommon.PromptInt(fmt.Sprintf("并发数 (默认 %d): ", cfg.Workers), cfg.Workers); v > 0 {
		cfg.Workers = v
	}
	promptTuning(&cfg)
	runEngine(cfg)
}

func menuBatch() {
	cfg := currentConfig()
	cfg.Mode = "batch"
	cfg.InputFile = cfcommon.PromptString("IP文件 (默认 speed.txt): ", "speed.txt")
	proto := cfcommon.PromptChoice("使用 HTTPS? (y/n, 默认 y): ", []string{"y", "n", "yes", "no"}, "y")
	if strings.HasPrefix(strings.ToLower(proto), "n") {
		cfg.Protocol = "http"
	} else {
		cfg.Protocol = "https"
	}
	if v := cfcommon.PromptInt(fmt.Sprintf("最低速度 Mbps (默认 %.0f): ", cfg.MinSpeedMbps), int(cfg.MinSpeedMbps)); v > 0 {
		cfg.MinSpeedMbps = float64(v)
	}
	if v := cfcommon.PromptInt(fmt.Sprintf("并发数 (默认 %d): ", cfg.Workers), cfg.Workers); v > 0 {
		cfg.Workers = v
	}
	if v := cfcommon.PromptInt(fmt.Sprintf("最大延迟 ms (默认 %d): ", cfg.MaxLatency), cfg.MaxLatency); v > 0 {
		cfg.MaxLatency = v
	}
	promptTuning(&cfg)
	runEngine(cfg)
}

func menuSingle(protocol string) {
	cfg := currentConfig()
	cfg.Mode = "single"
	cfg.Protocol = protocol
	cfg.SingleIP = cfcommon.PromptString("请输入要测速的 IP: ", "")
	if cfg.SingleIP == "" {
		fmt.Println("[错误] IP 不能为空")
		return
	}
	if v := cfcommon.PromptInt("超时时间 秒 (默认 8): ", 8); v > 0 {
		cfg.SingleSpeedTimeoutSec = v
	}
	promptTuning(&cfg)
	runEngine(cfg)
}

func runEngine(cfg cfspeed.Config) {
	ctx, cancel := context.WithCancel(context.Background())
	menuMu.Lock()
	activeCancel = cancel
	menuMu.Unlock()
	defer func() {
		cancel()
		menuMu.Lock()
		activeCancel = nil
		menuMu.Unlock()
	}()
	if _, err := cfspeed.Run(ctx, cfg, cfcommon.StdoutReporter()); err != nil {
		if ctx.Err() != nil {
			fmt.Printf("[中断] 任务已取消，已有结果已保存: %v\n", err)
		} else {
			fmt.Printf("[错误] %v\n", err)
		}
	}
}
