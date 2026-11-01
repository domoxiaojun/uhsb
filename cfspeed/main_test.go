package main

import (
	"testing"

	"cftest/internal/cfspeed"
)

func TestBuildDownloadURL(t *testing.T) {
	tests := []struct {
		protocol string
		host     string
		sizeMB   int
		want     string
	}{
		{"http", cfspeed.DefaultSpeedHost, 50, "http://cftest.domob.org/50m"},
		{"https", cfspeed.TraceHost, 10, "https://speed.cloudflare.com/__down?bytes=10485760"},
	}
	for _, tt := range tests {
		if got := cfspeed.BuildDownloadURL(tt.protocol, tt.host, tt.sizeMB); got != tt.want {
			t.Fatalf("BuildDownloadURL(%q, %q, %d) = %q, want %q",
				tt.protocol, tt.host, tt.sizeMB, got, tt.want)
		}
	}
}

func TestNormalizeModeAndProtocol(t *testing.T) {
	if got := cfspeed.NormalizeMode("history"); got != "history" {
		t.Fatalf("NormalizeMode(history) = %q", got)
	}
	if got := cfspeed.NormalizeMode("unknown"); got != "batch" {
		t.Fatalf("NormalizeMode(unknown) = %q, want batch", got)
	}
	if got := cfspeed.NormalizeProtocol("https"); got != "https" {
		t.Fatalf("NormalizeProtocol(https) = %q", got)
	}
	if got := cfspeed.NormalizeProtocol("ftp"); got != "http" {
		t.Fatalf("NormalizeProtocol(ftp) = %q, want http", got)
	}
}

func TestTrimRecordBOM(t *testing.T) {
	record := cfspeed.TrimRecordBOM([]string{"\ufeffIP", "rest"})
	if record[0] != "IP" {
		t.Fatalf("TrimRecordBOM first field = %q, want IP", record[0])
	}
}
