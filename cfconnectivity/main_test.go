package main

import "testing"

func TestNormalizeCompatFlags(t *testing.T) {
	oldInput := *inputFile
	oldWorkers := *workers
	oldOutput := *outputPrefix
	oldVerbose := *verbose
	oldPreferred := *preferredColos
	oldColo := *coloFilter
	oldBatch := *nonInteractive
	oldInputShort := *inputFileShort
	oldWorkersShort := *workersShort
	oldOutputShort := *outputPrefixShort
	oldVerboseShort := *verboseShort
	oldPreferredShort := *preferredColosShort
	oldColoShort := *coloFilterShort
	oldBatchShort := *batchShort
	t.Cleanup(func() {
		*inputFile = oldInput
		*workers = oldWorkers
		*outputPrefix = oldOutput
		*verbose = oldVerbose
		*preferredColos = oldPreferred
		*coloFilter = oldColo
		*nonInteractive = oldBatch
		*inputFileShort = oldInputShort
		*workersShort = oldWorkersShort
		*outputPrefixShort = oldOutputShort
		*verboseShort = oldVerboseShort
		*preferredColosShort = oldPreferredShort
		*coloFilterShort = oldColoShort
		*batchShort = oldBatchShort
	})

	*inputFileShort = "ips.txt"
	*workersShort = 33
	*outputPrefixShort = "conn"
	*verboseShort = true
	*preferredColosShort = "HKG,NRT"
	*coloFilterShort = "SIN"
	*batchShort = true

	normalizeCompatFlags()

	if *inputFile != "ips.txt" {
		t.Fatalf("inputFile = %q", *inputFile)
	}
	if *workers != 33 {
		t.Fatalf("workers = %d", *workers)
	}
	if *outputPrefix != "conn" {
		t.Fatalf("outputPrefix = %q", *outputPrefix)
	}
	if !*verbose {
		t.Fatalf("verbose not enabled")
	}
	if *preferredColos != "HKG,NRT" {
		t.Fatalf("preferredColos = %q", *preferredColos)
	}
	if *coloFilter != "SIN" {
		t.Fatalf("coloFilter = %q", *coloFilter)
	}
	if !*nonInteractive {
		t.Fatalf("nonInteractive not enabled")
	}
}
