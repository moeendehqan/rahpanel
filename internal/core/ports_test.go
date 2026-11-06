package core

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSharedHostPortsStayConsistentAcrossCoreAndSubscription(t *testing.T) {
	n := Node{ID: "shared", Name: "Panel host", Host: "203.0.113.1", Domain: "node.example.com", SharedHost: true, Ports: NodePorts{Reality: 2087, WebSocket: 18443, Trojan: 12053, Stats: 10086}, Ready: true, Verified: []string{"reality", "websocket", "trojan"}}
	raw, err := BuildConfig(n, nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag    string `json:"tag"`
			Port   int    `json:"port"`
			Listen string `json:"listen"`
		} `json:"inbounds"`
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	expected := map[string]int{"reality": 2087, "websocket": 18443, "trojan": 12053, "api": 10086}
	for _, in := range cfg.Inbounds {
		if in.Port != expected[in.Tag] {
			t.Fatalf("%s listens on %d", in.Tag, in.Port)
		}
		if in.Tag == "api" && in.Listen != "127.0.0.1" {
			t.Fatal("stats exposed publicly")
		}
	}
	subscription, err := base64.StdEncoding.DecodeString(Subscription([]Node{n}, []Credential{{NodeID: n.ID, UUID: "uuid", Password: "secret"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(subscription), "\n") {
		u, err := url.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		kind := u.Query().Get("security")
		want := "2087"
		if kind == "tls" {
			want = "18443"
		}
		if u.Scheme == "trojan" {
			want = "12053"
		}
		if u.Port() != want {
			t.Fatalf("subscription port %s, want %s", u.Port(), want)
		}
	}
}

func TestPortsRejectConflictsAndProtectSharedPanelListeners(t *testing.T) {
	defaults := Node{SharedHost: true}.EffectivePorts()
	if defaults.Reality == 443 || defaults.Stats == 10085 {
		t.Fatal("shared defaults collide with existing panel/common stats")
	}
	for _, ports := range []NodePorts{{Reality: -1}, {Reality: 65536}, {Reality: 8443}, {Stats: 2053}} {
		if ports.WithDefaults(false).Validate(false) == nil {
			t.Fatalf("invalid ports accepted: %+v", ports)
		}
	}
	for _, port := range []int{80, 443, 9443} {
		ports := defaults
		ports.Reality = port
		if ports.Validate(true) == nil {
			t.Fatalf("shared panel port %d accepted", port)
		}
	}
	if err := (Node{}).EffectivePorts().Validate(false); err != nil {
		t.Fatal(err)
	}
}
