package provision

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

func TestRouteRecoveryAndFallbackOrder(t *testing.T) {
	n := core.Node{ID: "n", Name: "Node", Enabled: true, Ready: true, Verified: []string{"reality"}, Host: "192.0.2.1", Domain: "node.example"}
	var reality, trojan routeSample
	for i := 0; i < 3; i++ {
		reality.record(errProbeConnection, 0)
	}
	trojan.record(nil, 100*time.Millisecond)
	w := Worker{routes: map[routeKey]map[string]routeSample{keyFor(n): {"reality": reality, "trojan": trojan}}}
	initial := n
	w.ApplyRouteQuality(&initial)
	if len(initial.Verified) != 1 {
		t.Fatal("published after one success")
	}
	trojan.record(nil, 100*time.Millisecond)
	w.routes[keyFor(n)]["trojan"] = trojan
	w.ApplyRouteQuality(&n)
	reality.record(nil, 120*time.Millisecond)
	if reality.LastError != "connection" || reality.LastErrorAt == nil || reality.LastErrorAt.After(reality.CheckedAt) {
		t.Fatalf("expected last failure to survive a successful check: %+v", reality)
	}
	encoded := core.Subscription([]core.Node{n}, []core.Credential{{NodeID: n.ID, UUID: "uuid", Password: "password"}})
	raw, err := base64.StdEncoding.DecodeString(encoded)
	lines := strings.Split(string(raw), "\n")
	if err != nil || len(lines) != 2 || !strings.HasPrefix(lines[0], "trojan://") || !strings.HasPrefix(lines[1], "vless://") {
		t.Fatalf("expected recovered Trojan followed by weak REALITY, got %q (%v)", raw, err)
	}
}
