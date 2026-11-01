package main

import (
	"testing"

	"cftest/internal/cfscan"
)

func TestExtractLossAvg(t *testing.T) {
	tests := []struct {
		line     string
		wantLoss string
		wantAvg  string
	}{
		{
			line:     " 7.|-- example-hk 0.0% 1 10.1 10.2 10.3 10.4 0.0",
			wantLoss: "0.0",
			wantAvg:  "10.2",
		},
		{
			line:     "12.|-- 223.119.6.101 30.0% 10 12.1 13.2 14.3 15.4 0.0",
			wantLoss: "30.0",
			wantAvg:  "13.2",
		},
	}

	for _, tt := range tests {
		loss, avg := cfscan.ExtractLossAvg(tt.line)
		if loss != tt.wantLoss || avg != tt.wantAvg {
			t.Fatalf("ExtractLossAvg(%q) = (%q, %q), want (%q, %q)",
				tt.line, loss, avg, tt.wantLoss, tt.wantAvg)
		}
	}
}
