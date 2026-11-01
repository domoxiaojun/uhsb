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
