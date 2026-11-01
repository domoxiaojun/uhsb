package cfdata

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"cftest/internal/cfcommon"
)

type scanLiveSaver struct {
	csv     *cfcommon.CSVRecordSink
	csvErr  error
	csvFile string

	txt     *cfcommon.LineSink
	txtErr  error
	txtFile string

	writeMu sync.Mutex
}

func newScanLiveSaver(cfg Config, tag string, includeCSV bool) (*scanLiveSaver, error) {
	safeTag := SanitizeOutputTag(tag)
	saver := &scanLiveSaver{
		csvFile: pathIn(cfg, safeTag+"_ips.csv"),
		txtFile: pathIn(cfg, safeTag+"_ips.txt"),
	}
	var initErr error
	if txt, err := cfcommon.NewLineSink(saver.txtFile); err != nil {
		saver.txtErr = err
		initErr = errors.Join(initErr, err)
	} else {
		saver.txt = txt
	}
	if includeCSV {
		if csvSink, err := cfcommon.NewCSVRecordSink(saver.csvFile,
			[]string{"IP", "Colo", "地区", "城市", "国家", "延迟"}); err != nil {
			saver.csvErr = err
			initErr = errors.Join(initErr, err)
		} else {
			saver.csv = csvSink
		}
	}
	if saver.csv == nil && saver.txt == nil {
		return nil, initErr
	}
	return saver, initErr
}

func writeScanResult(saver *scanLiveSaver) func(ScanResult) {
	if saver == nil {
		return nil
	}
	return saver.Write
}

func (s *scanLiveSaver) Write(r ScanResult) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.csv != nil && s.csvErr == nil {
		if err := s.csv.Write([]string{r.IP, r.DataCenter, r.Region, r.City, r.CountryCode, r.LatencyStr}); err != nil {
			s.csvErr = err
		}
	}
	if s.txt != nil && s.txtErr == nil {
		if err := s.txt.WriteLine(r.IP); err != nil {
			s.txtErr = err
		}
	}
}

func (s *scanLiveSaver) Close() error {
	if s == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return errors.Join(s.csvErr, s.txtErr, s.csv.Close(), s.txt.Close())
}

type detailedLiveSaver struct {
	result     *cfcommon.CSVRecordSink
	resultErr  error
	resultFile string

	mu          sync.Mutex
	zeroCSV     *cfcommon.CSVRecordSink
	zeroCSVErr  error
	zeroCSVFile string

	zeroTxt     *cfcommon.LineSink
	zeroTxtErr  error
	zeroTxtFile string
}

func newDetailedLiveSaver(cfg Config, tag string) (*detailedLiveSaver, error) {
	safeTag := SanitizeOutputTag(tag)
	saver := &detailedLiveSaver{
		resultFile:  pathIn(cfg, safeTag+"_result.csv"),
		zeroCSVFile: pathIn(cfg, safeTag+"_0loss.csv"),
		zeroTxtFile: pathIn(cfg, safeTag+"_0loss_ips.txt"),
	}
	if resultSink, err := cfcommon.NewCSVRecordSink(saver.resultFile,
		[]string{"IP", "Colo", "地区", "城市", "国家", "最小延迟ms", "最大延迟ms", "平均延迟ms", "丢包率%"}); err != nil {
		saver.resultErr = err
		return saver, err
	} else {
		saver.result = resultSink
	}
	return saver, nil
}

func (s *detailedLiveSaver) Write(r DetailedTestResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result != nil && s.resultErr == nil {
		if err := s.result.Write(detailedResultRow(r)); err != nil {
			s.resultErr = err
		}
	}
	if r.LossRate != 0 {
		return
	}
	s.ensureZeroLossCSVLocked()
	s.ensureZeroLossTxtLocked()
	if s.zeroCSV != nil && s.zeroCSVErr == nil {
		if err := s.zeroCSV.Write(detailedResultRow(r)); err != nil {
			s.zeroCSVErr = err
		}
	}
	if s.zeroTxt != nil && s.zeroTxtErr == nil {
		if err := s.zeroTxt.WriteLine(r.IP); err != nil {
			s.zeroTxtErr = err
		}
	}
}

func (s *detailedLiveSaver) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.resultErr, s.zeroCSVErr, s.zeroTxtErr,
		s.result.Close(), s.zeroCSV.Close(), s.zeroTxt.Close())
}

func (s *detailedLiveSaver) ensureZeroLossCSVLocked() {
	if s.zeroCSV != nil || s.zeroCSVErr != nil {
		return
	}
	csvSink, err := cfcommon.NewCSVRecordSink(s.zeroCSVFile,
		[]string{"IP", "Colo", "地区", "城市", "国家", "最小延迟ms", "最大延迟ms", "平均延迟ms", "丢包率%"})
	if err != nil {
		s.zeroCSVErr = err
		return
	}
	s.zeroCSV = csvSink
}

func (s *detailedLiveSaver) ensureZeroLossTxtLocked() {
	if s.zeroTxt != nil || s.zeroTxtErr != nil {
		return
	}
	txtSink, err := cfcommon.NewLineSink(s.zeroTxtFile)
	if err != nil {
		s.zeroTxtErr = err
		return
	}
	s.zeroTxt = txtSink
}

func detailedResultRow(r DetailedTestResult) []string {
	return []string{
		r.IP, r.DataCenter, r.Region, r.City, r.CountryCode,
		fmt.Sprintf("%d", r.MinLatency.Milliseconds()),
		fmt.Sprintf("%d", r.MaxLatency.Milliseconds()),
		fmt.Sprintf("%d", r.AvgLatency.Milliseconds()),
		fmt.Sprintf("%d", int(r.LossRate*100)),
	}
}

func saveFilteredResults(cfg Config, results []ScanResult, tag string, reporter cfcommon.Reporter) (csvFile, txtFile string) {
	if len(results) == 0 {
		reporter.Log("cfdata", "未找到匹配IP")
		return "", ""
	}

	safeTag := SanitizeOutputTag(tag)
	csvFile = pathIn(cfg, safeTag+"_ips.csv")
	txtFile = pathIn(cfg, safeTag+"_ips.txt")

	tempCSV := cfcommon.TempWritePath(csvFile)
	w, _, closeFn, err := cfcommon.NewCSVWriter(tempCSV)
	csvSaved := false
	if err == nil {
		writeErr := w.Write([]string{"IP", "Colo", "地区", "城市", "国家", "延迟"})
		for _, r := range results {
			if writeErr == nil {
				writeErr = w.Write([]string{r.IP, r.DataCenter, r.Region, r.City, r.CountryCode, r.LatencyStr})
			}
		}
		closeErr := closeFn()
		if writeErr == nil && closeErr == nil && cfcommon.ReplaceFileFromTemp(tempCSV, csvFile) == nil {
			csvSaved = true
		}
	}

	var ips []string
	for _, r := range results {
		ips = append(ips, r.IP)
	}
	txtSaved := cfcommon.WriteLinesAtomic(txtFile, ips) == nil
	if csvSaved && txtSaved {
		reporter.File("cfdata", txtFile, "[保存] %d 个IP → %s 和 %s", len(results), csvFile, txtFile)
	} else if csvSaved {
		reporter.File("cfdata", csvFile, "[保存] %d 个IP → %s", len(results), csvFile)
	} else if txtSaved {
		reporter.File("cfdata", txtFile, "[保存] %d 个IP → %s", len(results), txtFile)
	}
	return csvFile, txtFile
}

func saveDetailedCSV(results []DetailedTestResult, filename string, reporter cfcommon.Reporter) bool {
	tempFile := cfcommon.TempWritePath(filename)
	w, _, closeFn, err := cfcommon.NewCSVWriter(tempFile)
	if err != nil {
		reporter.Error("cfdata", "保存 %s 失败: %v", filename, err)
		return false
	}
	writeErr := w.Write([]string{"IP", "Colo", "地区", "城市", "国家", "最小延迟ms", "最大延迟ms", "平均延迟ms", "丢包率%"})
	for _, r := range results {
		if writeErr == nil {
			writeErr = w.Write(detailedResultRow(r))
		}
	}
	closeErr := closeFn()
	if writeErr != nil || closeErr != nil {
		return false
	}
	if err := cfcommon.ReplaceFileFromTemp(tempFile, filename); err != nil {
		reporter.Error("cfdata", "替换 %s 失败: %v", filename, err)
		return false
	}
	reporter.File("cfdata", filename, "[保存] %s (%d条)", filename, len(results))
	return true
}

func analyzeResults(results []DetailedTestResult, totalIPs int, reporter cfcommon.Reporter) {
	groups := make(map[int][]DetailedTestResult)
	for _, r := range results {
		bucket := (int(r.LossRate*100) / 10) * 10
		groups[bucket] = append(groups[bucket], r)
	}
	unsuccess := totalIPs - len(results)

	reporter.Log("cfdata", "─── 丢包率分布 ───")
	maxBar := 50
	for bucket := 0; bucket <= 100; bucket += 10 {
		count := len(groups[bucket])
		if bucket == 100 {
			count += unsuccess
		}
		prop := 0.0
		if totalIPs > 0 {
			prop = float64(count) / float64(totalIPs) * 100
		}
		barLen := int(prop / 100 * float64(maxBar))
		if barLen < 0 {
			barLen = 0
		}
		if barLen > maxBar {
			barLen = maxBar
		}
		bar := strings.Repeat("█", barLen) + strings.Repeat("░", maxBar-barLen)
		reporter.Log("cfdata", "丢包%3d%% |%s| %.1f%% (%d个)", bucket, bar, prop, count)
	}
}

func sortZeroLoss(results []DetailedTestResult) []DetailedTestResult {
	var zeroLoss []DetailedTestResult
	for _, r := range results {
		if r.LossRate == 0 {
			zeroLoss = append(zeroLoss, r)
		}
	}
	sort.Slice(zeroLoss, func(i, j int) bool {
		return zeroLoss[i].AvgLatency < zeroLoss[j].AvgLatency
	})
	return zeroLoss
}
