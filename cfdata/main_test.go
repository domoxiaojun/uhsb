package main

import (
	"testing"
)

func TestNormalizeCompatFlags(t *testing.T) {
	oldScanThreads := *flagScanThreads
	oldTestThreads := *flagTestThreads
	oldScanAlias := *flagScanAlias
	oldTestAlias := *flagTestAlias
	t.Cleanup(func() {
		*flagScanThreads = oldScanThreads
		*flagTestThreads = oldTestThreads
		*flagScanAlias = oldScanAlias
		*flagTestAlias = oldTestAlias
	})

	*flagScanThreads = 50
	*flagTestThreads = 25
	*flagScanAlias = 88
	*flagTestAlias = 44

	normalizeCompatFlags()

	if *flagScanThreads != 88 {
		t.Fatalf("scan threads = %d, want 88", *flagScanThreads)
	}
	if *flagTestThreads != 44 {
		t.Fatalf("test threads = %d, want 44", *flagTestThreads)
	}
}
