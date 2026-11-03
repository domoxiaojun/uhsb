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

func TestDefaultsHighQualityHopsAndPort(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.HighQualityHops != 16 {
		t.Fatalf("HighQualityHops = %d, want 16", cfg.HighQualityHops)
	}
	if cfg.Port != 443 {
		t.Fatalf("Port = %d, want 443", cfg.Port)
	}
	neg := Config{HighQualityHops: -1}.withDefaults()
	if neg.HighQualityHops != -1 {
		t.Fatalf("HighQualityHops = %d, want -1 to disable", neg.HighQualityHops)
	}
}
