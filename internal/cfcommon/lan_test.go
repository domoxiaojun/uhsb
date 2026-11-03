package cfcommon

import "testing"

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
