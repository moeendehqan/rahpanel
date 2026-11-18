package core

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCloudflareWSSubscriptionPreservesDirectRoute(t *testing.T) {
	n := Node{ID: "node", Name: "Test", Domain: "node.example.com", Host: "192.0.2.1", Ready: true, WSPath: "/ws", Verified: []string{"websocket"}}
	credentials := []Credential{{NodeID: n.ID, UUID: "test-user"}}
	direct := Subscription([]Node{n}, credentials)
	n.CloudflareWS = &CloudflareWSStatus{Enabled: true, Hostname: "cf-node.example.com"}
	if got := Subscription([]Node{n}, credentials); got != direct {
		t.Fatal("pending route was published")
	}
	n.CloudflareWS.Ready = true
	encoded := Subscription([]Node{n}, credentials)
	raw, _ := base64.StdEncoding.DecodeString(encoded)
	previous, _ := base64.StdEncoding.DecodeString(direct)
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 2 || lines[0] != string(previous) || !strings.Contains(lines[1], "@cf-node.example.com:443?") ||
		!strings.Contains(lines[1], "host=cf-node.example.com") || !strings.Contains(lines[1], "sni=cf-node.example.com") {
		t.Fatalf("unexpected routes: %s", raw)
	}
	mihomo := MihomoSubscription([]Node{n}, credentials)
	if !strings.Contains(mihomo, "WS Cloudflare") || !strings.Contains(mihomo, "cf-node.example.com") {
		t.Fatal(mihomo)
	}
	n.CloudflareWS.Enabled = false
	if got := Subscription([]Node{n}, credentials); got != direct {
		t.Fatal("disabling changed direct route")
	}
}
