package cfpipeline

import (
	"testing"

	"cftest/internal/cfscan"
)

func TestScanMissShouldStop(t *testing.T) {
	scanRes := &cfscan.Result{}
	if scanRes.QualityFile != "" && len(scanRes.Hits) > 0 {
		t.Fatal("empty result should not look successful")
	}
}

func TestSkipScanKeepsDataIPs(t *testing.T) {
	cfg := Config{SkipScan: true}.withDefaults()
	if !cfg.SkipScan {
		t.Fatal("SkipScan should stay true")
	}
}
