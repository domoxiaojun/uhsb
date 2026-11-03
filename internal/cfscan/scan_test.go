package cfscan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildMTROutput(hops int) string {
	var b strings.Builder
	b.WriteString("HOST: example                       Loss%   Snt   Last   Avg  Best  Wrst StDev\n")
	for i := 1; i <= hops; i++ {
		fmt.Fprintf(&b, " %2d.|-- 10.0.%d.1                     0.0%%     2    1.0   1.0   1.0   1.0   0.0\n", i, i)
	}
	return b.String()
}

func TestClassifyMTRKeywordHit(t *testing.T) {
	output := buildMTROutput(3) + "  4.|-- hkg-core.example.net          0.0%     2    1.0   1.0   1.0   1.0   0.0\n"
	res, ok := classifyMTR(output, "192.0.2.1", DefaultHighQualityHops)
	if !ok {
		t.Fatal("expected keyword hit")
	}
	if !strings.Contains(res.Keyword, "hkg") {
		t.Fatalf("keyword = %q, want hkg", res.Keyword)
	}
	if res.HopCount != 4 {
		t.Fatalf("hopCount = %d, want 4", res.HopCount)
	}
}

func TestClassifyMTRHighQualityByHops(t *testing.T) {
	res, ok := classifyMTR(buildMTROutput(20), "192.0.2.1", DefaultHighQualityHops)
	if !ok {
		t.Fatal("expected high-quality hit")
	}
	if !strings.Contains(res.Keyword, "high-quality") {
		t.Fatalf("keyword = %q, want high-quality", res.Keyword)
	}
	if res.HopCount != 20 {
		t.Fatalf("hopCount = %d", res.HopCount)
	}
}

func TestClassifyMTRHighQualityDisabled(t *testing.T) {
	if _, ok := classifyMTR(buildMTROutput(20), "192.0.2.1", -1); ok {
		t.Fatal("high-quality heuristic should be disabled")
	}
}

func TestClassifyMTRCMIDirect(t *testing.T) {
	output := buildMTROutput(2) + fmt.Sprintf("  3.|-- %s                       0.0%%     2    1.0   1.0   1.0   1.0   0.0\n", CMIHK)
	res, ok := classifyMTR(output, "192.0.2.1", DefaultHighQualityHops)
	if !ok {
		t.Fatal("expected CMI hit")
	}
	if !strings.Contains(res.Keyword, "cmi-hk") {
		t.Fatalf("keyword = %q, want cmi-hk", res.Keyword)
	}
}

func TestClassifyMTRNoMatch(t *testing.T) {
	if _, ok := classifyMTR(buildMTROutput(3), "192.0.2.1", DefaultHighQualityHops); ok {
		t.Fatal("expected no match for plain short route")
	}
}

func TestExtractLossAvg(t *testing.T) {
	loss, avg := ExtractLossAvg("  3.|-- 1.1.1.1  0.0%  2  1.0  1.2  1.0  1.4  0.1")
	if loss != "0.0" {
		t.Fatalf("loss = %q", loss)
	}
	if avg == "" {
		t.Fatal("avg should not be empty")
	}
}

func TestProbeMTRRejectsFailingBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakemtr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'mtr: permission denied' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := probeMTR(bin); err == nil {
		t.Fatal("expected probe error for failing mtr")
	}
}

func TestProbeMTRAcceptsWorkingBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakemtr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'HOST: example'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := probeMTR(bin); err != nil {
		t.Fatalf("probeMTR: %v", err)
	}
}
