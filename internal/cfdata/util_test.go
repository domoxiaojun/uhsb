package cfdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseColoList(t *testing.T) {
	got := ParseColoList("hkg, nrt sin")
	want := []string{"HKG", "NRT", "SIN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseColoList() = %#v, want %#v", got, want)
	}
}

func TestShouldStopForTargetColos(t *testing.T) {
	tests := []struct {
		name        string
		counts      map[string]int
		targets     []string
		numPerColo  int
		wantStopped bool
	}{
		{
			name:        "limit disabled",
			counts:      map[string]int{"HKG": 2},
			targets:     []string{"HKG"},
			numPerColo:  0,
			wantStopped: false,
		},
		{
			name:        "not all targets satisfied",
			counts:      map[string]int{"HKG": 2, "NRT": 1},
			targets:     []string{"HKG", "NRT"},
			numPerColo:  2,
			wantStopped: false,
		},
		{
			name:        "all targets satisfied",
			counts:      map[string]int{"HKG": 2, "NRT": 2},
			targets:     []string{"hkg", "nrt"},
			numPerColo:  2,
			wantStopped: true,
		},
	}

	for _, tt := range tests {
		if got := ShouldStopForTargetColos(tt.counts, tt.targets, tt.numPerColo); got != tt.wantStopped {
			t.Fatalf("%s: ShouldStopForTargetColos() = %v, want %v", tt.name, got, tt.wantStopped)
		}
	}
}

func TestCountGeneratedIPsIPv4(t *testing.T) {
	got := countGeneratedIPs([]string{"192.0.2.0/30", "198.51.100.0/31", "invalid"}, false, 100)
	if got != 2 {
		t.Fatalf("countGeneratedIPs() = %d, want 2", got)
	}
}

func TestForEachGeneratedIPProducesExpectedCount(t *testing.T) {
	var ips []string
	forEachGeneratedIP(context.Background(), []string{"192.0.2.0/30"}, false, 100, func(ip string) bool {
		ips = append(ips, ip)
		return true
	})
	if len(ips) != 2 {
		t.Fatalf("generated %d IPs, want 2", len(ips))
	}
}

func TestForEachGeneratedIPDoesNotRepeatIPv4Hosts(t *testing.T) {
	seen := make(map[string]bool)
	forEachGeneratedIP(context.Background(), []string{"198.51.100.0/29"}, false, 100, func(ip string) bool {
		if seen[ip] {
			t.Fatalf("duplicate ip generated: %s", ip)
		}
		seen[ip] = true
		return true
	})
	if len(seen) != 6 {
		t.Fatalf("generated %d IPs, want 6", len(seen))
	}
}

func TestLoadOrDownloadRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusBadGateway)
	}))
	defer srv.Close()

	localFile := filepath.Join(t.TempDir(), "ips-v4.txt")
	if _, err := loadOrDownload(localFile, srv.URL); err == nil {
		t.Fatal("loadOrDownload() error = nil, want non-nil")
	}
}

func TestLoadOrDownloadRejectsInvalidSubnetBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-a-cidr\n"))
	}))
	defer srv.Close()

	localFile := filepath.Join(t.TempDir(), "ips-v4.txt")
	if _, err := loadOrDownload(localFile, srv.URL); err == nil {
		t.Fatal("loadOrDownload() error = nil, want non-nil")
	}
}

func TestScanLiveSaverCloseNilSafe(t *testing.T) {
	var sink *scanLiveSaver
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() on nil sink error = %v, want nil", err)
	}
}
