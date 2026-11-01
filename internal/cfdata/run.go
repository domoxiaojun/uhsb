package cfdata

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"cftest/internal/cfcommon"
)

func Run(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	cfg = cfg.withDefaults()

	switch cfg.Mode {
	case "1":
		return runModeScanColo(ctx, cfg, reporter)
	case "2":
		return runModeFilterColo(ctx, cfg, reporter)
	case "3":
		return runModeFilterCountry(ctx, cfg, reporter)
	default:
		return nil, fmt.Errorf("无效 mode: %s (应为 1/2/3)", cfg.Mode)
	}
}

func runModeScanColo(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	if strings.TrimSpace(cfg.Colo) == "" {
		return nil, fmt.Errorf("输入不能为空")
	}
	targetColos := ParseColoList(cfg.Colo)
	if len(targetColos) > 5 {
		targetColos = targetColos[:5]
	}
	reporter.Log("cfdata", "目标Colo: %v", targetColos)

	subnets, locMap, err := loadResources(cfg, reporter)
	if err != nil {
		return nil, err
	}

	targetMap := make(map[string]bool)
	for _, c := range targetColos {
		targetMap[c] = true
	}
	coloCount := make(map[string]int)
	var coloCountMu sync.Mutex
	matchFn := func(colo, _ string) bool {
		upper := strings.ToUpper(colo)
		if !targetMap[upper] {
			return false
		}
		coloCountMu.Lock()
		defer coloCountMu.Unlock()
		if cfg.NumPerColo > 0 && coloCount[upper] >= cfg.NumPerColo {
			return false
		}
		coloCount[upper]++
		return true
	}
	stopCheck := func(colo, _ string) (bool, string) {
		if cfg.NumPerColo <= 0 {
			return false, ""
		}
		coloCountMu.Lock()
		defer coloCountMu.Unlock()
		if ShouldStopForTargetColos(coloCount, targetColos, cfg.NumPerColo) {
			return true, "已为所有目标 colo 找到足够数量的 IP，停止扫描..."
		}
		return false, ""
	}

	coloStr := strings.Join(targetColos, "_")
	return scanAndMaybeDetail(ctx, cfg, subnets, locMap, matchFn, stopCheck, coloStr, false, cfg.Detail, reporter)
}

func runModeFilterColo(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	coloInput := cfg.Colo
	if coloInput == "" {
		coloInput = "hkg,nrt,sin,tpa"
	}
	targetColos := ParseColoList(coloInput)
	subnets, locMap, err := loadResources(cfg, reporter)
	if err != nil {
		return nil, err
	}

	targetMap := make(map[string]bool)
	for _, c := range targetColos {
		targetMap[c] = true
	}
	coloCount := make(map[string]int)
	var coloCountMu sync.Mutex
	matchFn := func(colo, _ string) bool {
		upper := strings.ToUpper(colo)
		if !targetMap[upper] {
			return false
		}
		if cfg.NumPerColo > 0 {
			coloCountMu.Lock()
			defer coloCountMu.Unlock()
			if coloCount[upper] >= cfg.NumPerColo {
				return false
			}
			coloCount[upper]++
		}
		return true
	}
	stopCheck := func(colo, _ string) (bool, string) {
		if cfg.NumPerColo <= 0 {
			return false, ""
		}
		coloCountMu.Lock()
		defer coloCountMu.Unlock()
		if ShouldStopForTargetColos(coloCount, targetColos, cfg.NumPerColo) {
			return true, "已为所有目标 colo 找到足够数量的 IP，停止扫描..."
		}
		return false, ""
	}
	coloStr := strings.Join(targetColos, "_")
	return scanAndMaybeDetail(ctx, cfg, subnets, locMap, matchFn, stopCheck, coloStr, true, false, reporter)
}

func runModeFilterCountry(ctx context.Context, cfg Config, reporter cfcommon.Reporter) (*Result, error) {
	if strings.TrimSpace(cfg.Country) == "" {
		return nil, fmt.Errorf("输入不能为空")
	}
	targetCountries := ParseColoList(cfg.Country)
	subnets, locMap, err := loadResources(cfg, reporter)
	if err != nil {
		return nil, err
	}
	targetMap := make(map[string]bool)
	for _, c := range targetCountries {
		targetMap[c] = true
	}
	matchFn := func(_, country string) bool {
		return targetMap[strings.ToUpper(country)]
	}
	countryStr := "country_" + strings.Join(targetCountries, "_")
	return scanAndMaybeDetail(ctx, cfg, subnets, locMap, matchFn, nil, countryStr, true, cfg.Detail, reporter)
}

func scanAndMaybeDetail(ctx context.Context, cfg Config, subnets []string, locMap map[string]cfcommon.Location,
	matchFn func(string, string) bool, stopCheck func(string, string) (bool, string),
	tag string, includeCSV, doDetail bool, reporter cfcommon.Reporter) (*Result, error) {

	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	liveSaver, err := newScanLiveSaver(cfg, tag, includeCSV)
	if err != nil {
		reporter.Warn("cfdata", "无法启用实时保存: %v", err)
	}
	results := scanIPs(scanCtx, cancel, cfg, subnets, locMap, matchFn, stopCheck, writeScanResult(liveSaver), reporter)
	if liveSaver != nil {
		if err := liveSaver.Close(); err != nil {
			reporter.Warn("cfdata", "实时保存关闭失败: %v", err)
		}
	}

	out := &Result{ScanResults: results, Tag: tag}
	if len(results) == 0 {
		reporter.Log("cfdata", "未找到匹配的IP")
		return out, nil
	}

	if includeCSV {
		out.CSVFile, out.IPFile = saveFilteredResults(cfg, results, tag, reporter)
	} else {
		out.IPFile = pathIn(cfg, SanitizeOutputTag(tag)+"_ips.txt")
		var ipList []string
		for _, r := range results {
			ipList = append(ipList, r.IP)
		}
		if err := cfcommon.WriteLinesAtomic(out.IPFile, ipList); err != nil {
			return out, fmt.Errorf("保存 %s 失败: %w", out.IPFile, err)
		}
		reporter.File("cfdata", out.IPFile, "[保存] %d 个IP → %s", len(ipList), out.IPFile)
		if liveSaver != nil {
			out.CSVFile = liveSaver.csvFile
		}
	}

	if doDetail {
		details, zero, resultCSV, zeroCSV, zeroTxt := runDetailedTest(ctx, cfg, results, tag, reporter)
		out.DetailResults = details
		out.ZeroLoss = zero
		out.ResultCSV = resultCSV
		out.ZeroLossCSV = zeroCSV
		out.ZeroLossIPFile = zeroTxt
	}
	return out, nil
}
