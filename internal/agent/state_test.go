package agent

import (
	"encoding/json"
	"github.com/rahpanel/rahpanel/internal/core"
	"testing"
	"time"
)

func TestPruneKeepsHysteriaProbeAndRemovesRevokedUser(t *testing.T) {
	now := time.Now()
	n := core.Node{ID: "node", Hysteria2: true, RealityPrivate: "test-only"}
	user := core.User{ID: "user", Enabled: true, ExpiresAt: now.Add(time.Hour)}
	raw, err := core.BuildConfig(n, []core.User{user}, []core.Credential{{NodeID: n.ID, UserID: user.ID, Password: "user-secret"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	pruned, err := Prune(raw, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string
			Settings struct {
				Clients []struct{ Auth, Email string }
			}
		}
	}
	if err := json.Unmarshal(pruned, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, inbound := range cfg.Inbounds {
		if inbound.Tag == "hysteria2" {
			clients := inbound.Settings.Clients
			if len(clients) != 1 || clients[0].Auth != core.ProbeCredential(n).Password || clients[0].Email != "" {
				t.Fatalf("unexpected Hysteria clients after revocation: %+v", clients)
			}
			return
		}
	}
	t.Fatal("Hysteria inbound missing")
}

func TestCumulativeCountersBecomeDurableDeltas(t *testing.T) {
	s := State{RunID: "run1", Baseline: map[string]int64{}, Outbox: []core.Report{}}
	s.Sample("run1", map[string]int64{"user>>>u>>>traffic>>>uplink": 10}, time.Now())
	s.Sample("run1", map[string]int64{"user>>>u>>>traffic>>>uplink": 15}, time.Now())
	if len(s.Outbox) != 2 || s.Outbox[0].Usage[0].Upload != 10 || s.Outbox[1].Usage[0].Upload != 5 {
		t.Fatalf("bad deltas: %+v", s.Outbox)
	}
	s.Sample("run2", map[string]int64{"user>>>u>>>traffic>>>uplink": 3}, time.Now())
	if s.Outbox[2].Usage[0].Upload != 3 {
		t.Fatal("restart was treated as old baseline")
	}
}
func TestPruneRemovesUserAtExpiry(t *testing.T) {
	now := time.Now()
	raw := []byte(`{"inbounds":[{"settings":{"clients":[{"email":"a","id":"secret"},{"email":"b","id":"keep"}]}}]}`)
	b, e := Prune(raw, []core.User{{ID: "a", Enabled: true, ExpiresAt: now}, {ID: "b", Enabled: true, ExpiresAt: now.Add(time.Hour)}}, now)
	if e != nil {
		t.Fatal(e)
	}
	if string(b) == string(raw) {
		t.Fatal("expired user retained")
	}
}
func TestWatchdogRejectsRebootAndDeadline(t *testing.T) {
	l := Lease{BootID: "boot", Deadline: 300, ConfigDeadline: 200}
	if !l.Valid("boot", 199) {
		t.Fatal("valid lease rejected")
	}
	if l.Valid("boot", 200) || l.Valid("other", 100) {
		t.Fatal("unsafe lease accepted")
	}
}

func TestLeaseCannotGrowWhenWallClockMoves(t *testing.T) {
	l := NewLease("boot", 100)
	if l.Deadline != 395 {
		t.Fatal("lease exceeds maximum duration")
	}
	// The watchdog's decision has no wall-clock argument, including a ten-minute skew.
	if l.Valid("boot", 395) {
		t.Fatal("expired uptime lease accepted")
	}
	server := time.Unix(1700000000, 0)
	safe := ConfigDeadline(l, server, 100, []core.User{{Enabled: true, ExpiresAt: server.Add(time.Minute)}})
	if safe != 160 {
		t.Fatalf("expiry not bounded by server time: %v", safe)
	}
}
