package cfcommon

import (
	"net"
	"testing"
)

func TestAccessControlPrivateAllowedPublicRejected(t *testing.T) {
	ac, err := NewAccessControl("private")
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	cases := []struct {
		addr string
		want bool
	}{
		{"192.168.1.10:5555", true},
		{"10.0.0.5:1", true},
		{"172.16.3.4:1", true},
		{"127.0.0.1:1", true},
		{"[::1]:1", true},
		{"8.8.8.8:53", false},
		{"1.1.1.1:80", false},
	}
	for _, c := range cases {
		if got := ac.Allowed(c.addr); got != c.want {
			t.Errorf("Allowed(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestAccessControlExplicitCIDR(t *testing.T) {
	ac, err := NewAccessControl("192.168.50.0/24")
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	if !ac.Allowed("192.168.50.20:1") {
		t.Fatal("expected in-subnet allowed")
	}
	if ac.Allowed("192.168.51.20:1") {
		t.Fatal("expected out-of-subnet rejected")
	}
}

func TestAccessControlAny(t *testing.T) {
	ac, err := NewAccessControl("any")
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	if !ac.Allowed("8.8.8.8:1") {
		t.Fatal("any should allow public")
	}
}

func TestAccessControlInvalid(t *testing.T) {
	if _, err := NewAccessControl("not-a-cidr"); err == nil {
		t.Fatal("expected error for invalid spec")
	}
}

func TestIsLANIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		{"::1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"8.8.8.8", false},
		{"203.0.113.5", false},
		{"2606:4700::1111", false},
	}
	for _, c := range cases {
		if got := isLANIP(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("isLANIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestLocalInterfaceCIDRsNeverIncludePublicSubnet(t *testing.T) {
	// 在 VPS 上，公网网卡网段不能混进 lan 白名单，否则同网段公网主机
	// 也能访问无认证的 WebUI。
	for _, n := range localInterfaceCIDRs() {
		if !isLANIP(n.IP) {
			t.Fatalf("public interface CIDR leaked into lan allowlist: %s", n)
		}
	}
}
