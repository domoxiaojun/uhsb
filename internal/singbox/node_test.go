package singbox

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestVMessURLRoundTrip(t *testing.T) {
	n := Node{
		Server: "1.1.1.1",
		UUID:   "11111111-1111-1111-1111-111111111111",
		Host:   "example.com",
		Path:   "/ws",
		SNI:    "example.com",
		Port:   443,
		TLS:    true,
		Tag:    "hkg-test",
	}
	link := n.VMessURL()
	if !strings.HasPrefix(link, "vmess://") {
		t.Fatalf("link = %q", link)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["add"] != "1.1.1.1" || payload["host"] != "example.com" || payload["net"] != "ws" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestClientConfigJSON(t *testing.T) {
	n := Node{Server: "1.0.0.1", UUID: "u", Host: "a.com", Path: "ws", TLS: true}
	raw, err := n.ClientConfigJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"type": "vmess"`) {
		t.Fatalf("missing vmess outbound: %s", raw)
	}
}

func TestEarlyDataPathBecomesSingBoxField(t *testing.T) {
	n := Node{Server: "1.1.1.1", UUID: "u", Host: "sg.example.com", Path: "/domovws?ed=2048", TLS: true}
	link := n.VMessURL()
	rawLink, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawLink), `/domovws?ed=2048`) {
		t.Fatalf("share link should keep the v2ray early-data path, got %s", rawLink)
	}

	outbound := n.Outbound()
	transport, ok := outbound["transport"].(map[string]any)
	if !ok {
		t.Fatalf("transport = %#v", outbound["transport"])
	}
	if transport["path"] != "/domovws" {
		t.Fatalf("path = %#v, want /domovws", transport["path"])
	}
	if transport["max_early_data"] != 2048 {
		t.Fatalf("max_early_data = %#v, want 2048", transport["max_early_data"])
	}
	if _, ok := transport["early_data_header_name"]; ok {
		t.Fatal("empty early_data_header_name selects Xray path mode and must be omitted")
	}
	encoded, err := json.Marshal(transport)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ed=2048") {
		t.Fatalf("sing-box transport still contains the ed query: %s", encoded)
	}
}

func TestSplitEarlyData(t *testing.T) {
	cases := []struct {
		in    string
		path  string
		early int
	}{
		{"/ws", "/ws", 0},
		{"/domovws?ed=0", "/domovws", 0},
		{"/domovws?token=abc&ed=256", "/domovws?token=abc", 256},
		{"/domovws?ed=nope", "/domovws?ed=nope", 0},
		{"/?ed=2048", "/", 2048},
	}
	for _, tc := range cases {
		path, early := splitEarlyData(tc.in)
		if path != tc.path || early != tc.early {
			t.Errorf("splitEarlyData(%q) = %q, %d; want %q, %d", tc.in, path, early, tc.path, tc.early)
		}
	}
}
