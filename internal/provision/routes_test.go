package provision

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

func TestRouteRecoveryAndFallbackOrder(t *testing.T) {
	n := core.Node{ID: "n", Name: "Node", Enabled: true, Ready: true, Verified: []string{"reality"}, Host: "192.0.2.1", Domain: "node.example"}
	var reality, trojan routeSample
	for i := 0; i < 3; i++ {
		reality.record(false, 0)
	}
	trojan.record(true, 100*time.Millisecond)
	w := Worker{routes: map[routeKey]map[string]routeSample{keyFor(n): {"reality": reality, "trojan": trojan}}}
	initial := n
	w.ApplyRouteQuality(&initial)
	if len(initial.Verified) != 1 {
		t.Fatal("published after one success")
	}
	trojan.record(true, 100*time.Millisecond)
	w.routes[keyFor(n)]["trojan"] = trojan
	w.ApplyRouteQuality(&n)
	encoded := core.Subscription([]core.Node{n}, []core.Credential{{NodeID: n.ID, UUID: "uuid", Password: "password"}})
	raw, err := base64.StdEncoding.DecodeString(encoded)
	lines := strings.Split(string(raw), "\n")
	if err != nil || len(lines) != 2 || !strings.HasPrefix(lines[0], "trojan://") || !strings.HasPrefix(lines[1], "vless://") {
		t.Fatalf("expected recovered Trojan followed by weak REALITY, got %q (%v)", raw, err)
	}
}

func TestRouteFlapping(t *testing.T) {
	n := core.Node{ID: "n", Enabled: true, Ready: true, Verified: []string{"reality"}}
	samples := make(map[string]routeSample)
	w := Worker{routes: map[routeKey]map[string]routeSample{keyFor(n): samples}}
	var sample routeSample
	for i, step := range []struct {
		ok, weak bool
	}{
		{false, false}, {false, false}, {true, false},
		{false, false}, {false, false}, {false, true},
		{true, true}, {false, true}, {true, true}, {true, false},
	} {
		sample.record(step.ok, time.Millisecond)
		samples["reality"] = sample
		current := n
		w.ApplyRouteQuality(&current)
		quality, ok := current.RouteQuality["reality"]
		if !ok || quality.Weak != step.weak {
			t.Fatalf("step %d: quality = %+v, present = %t, want weak = %t", i+1, quality, ok, step.weak)
		}
	}
}

func TestAllWeakRoutesStayInSubscription(t *testing.T) {
	n := core.Node{ID: "n", Enabled: true, Ready: true, Verified: []string{"reality", "websocket", "trojan"}, Host: "192.0.2.1", Domain: "node.example"}
	samples := make(map[string]routeSample)
	for _, kind := range n.Verified {
		var sample routeSample
		for i := 0; i < 3; i++ {
			sample.record(false, 0)
		}
		samples[kind] = sample
	}
	w := Worker{routes: map[routeKey]map[string]routeSample{keyFor(n): samples}}
	w.ApplyRouteQuality(&n)
	for kind := range samples {
		if !n.RouteQuality[kind].Weak {
			t.Errorf("%s should be weak", kind)
		}
	}
	checkSubscriptionRoutes(t, n, "reality", "websocket", "trojan")
}

func TestSubscriptionAfterWorkerRestart(t *testing.T) {
	n := core.Node{ID: "n", Enabled: true, Ready: true, Verified: []string{"reality"}, Host: "192.0.2.1", Domain: "node.example"}
	w := Worker{}
	current := n
	w.ApplyRouteQuality(&current)
	checkSubscriptionRoutes(t, current, "reality")

	var trojan routeSample
	trojan.record(true, time.Millisecond)
	w.routes = map[routeKey]map[string]routeSample{keyFor(n): {"trojan": trojan}}
	current = n
	w.ApplyRouteQuality(&current)
	checkSubscriptionRoutes(t, current, "reality")

	trojan.record(true, time.Millisecond)
	w.routes[keyFor(n)]["trojan"] = trojan
	current = n
	w.ApplyRouteQuality(&current)
	checkSubscriptionRoutes(t, current, "reality", "trojan")
}

func checkSubscriptionRoutes(t *testing.T, n core.Node, want ...string) {
	t.Helper()
	encoded := core.Subscription([]core.Node{n}, []core.Credential{{NodeID: n.ID, UUID: "uuid", Password: "password"}})
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d subscription links, want %d: %q", len(lines), len(want), raw)
	}
	for _, kind := range want {
		count := 0
		for _, line := range lines {
			link, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(link.Fragment, " · "+kind) {
				count++
			}
		}
		if count != 1 {
			t.Errorf("got %d links for %s, want one", count, kind)
		}
	}
}
