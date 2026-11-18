package provision_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/agent"
	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/security"
	"github.com/rahpanel/rahpanel/internal/store"
)

// This opt-in test uses isolated listeners and a disposable PostgreSQL database.
func TestHysteriaAcceptance(t *testing.T) {
	xray, mihomo, dsn := os.Getenv("TEST_XRAY_BIN"), os.Getenv("TEST_MIHOMO_BIN"), os.Getenv("TEST_HYSTERIA_DATABASE_URL")
	if xray == "" || mihomo == "" || dsn == "" {
		t.Skip("set TEST_XRAY_BIN, TEST_MIHOMO_BIN and TEST_HYSTERIA_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	dir := t.TempDir()
	ca, err := security.NewAuthority()
	if err != nil {
		t.Fatal(err)
	}
	cert, key, _, err := ca.Issue("node", true, []string{"node.test"})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"tls.crt": cert, "tls.key": key, "ca.crt": ca.Certificate} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	n := core.Node{ID: core.ID(), Name: "acceptance", Host: "127.0.0.1", Domain: "node.test", GroupID: "default",
		Ready: true, Enabled: true, Hysteria2: true, Verified: []string{"reality", "hysteria2", "websocket", "trojan"},
		RealityTarget: "www.apple.com:443", RealitySNI: "www.apple.com", WSPath: "/acceptance",
		Ports: core.NodePorts{Reality: 14444, WebSocket: 18443, Trojan: 12053, Stats: 15085}}
	if err := core.NewNodeKeys(&n); err != nil {
		t.Fatal(err)
	}
	u := core.User{ID: core.ID(), Name: core.ID(), Enabled: true, GroupID: "default", Token: core.RandomToken(), ExpiresAt: now.Add(time.Hour), QuotaBytes: 1 << 30}
	c := core.Credential{UserID: u.ID, NodeID: n.ID, UUID: core.ID(), Password: core.RandomToken()}
	raw, err := core.BuildConfig(n, []core.User{u}, []core.Credential{c}, now)
	if err != nil {
		t.Fatal(err)
	}
	configs := map[string]json.RawMessage{"enabled": raw}
	for _, kind := range []string{"suspended", "expired", "quota", "removed"} {
		user := u
		switch kind {
		case "suspended":
			user.Enabled = false
		case "expired":
			user.ExpiresAt = now.Add(-time.Second)
		case "quota":
			user.UsedBytes = user.QuotaBytes
		}
		users := []core.User{user}
		if kind == "removed" {
			users = nil
		}
		configs[kind], err = agent.Prune(raw, users, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := json.Marshal(map[string]any{"configs": configs, "subscription": core.MihomoSubscription([]core.Node{n}, []core.Credential{c}), "user": u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "python3", "../../scripts/test-hysteria.py", dir, xray, mihomo)
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 10 * time.Second
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		t.Fatalf("real-core acceptance failed: %v", err)
	}
	var results struct {
		Samples []struct {
			Run    string           `json:"run"`
			Values map[string]int64 `json:"values"`
		} `json:"samples"`
	}
	resultBytes, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resultBytes, &results); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(ctx, dsn, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err := s.AddNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DELETE FROM nodes WHERE id=$1", n.ID)
	if err := s.SaveUser(ctx, u, true); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DELETE FROM users WHERE id=$1", u.ID)
	if _, err := s.Credentials(ctx, n.ID, []core.User{u}); err != nil {
		t.Fatal(err)
	}
	var state agent.State
	var expected int64
	for _, sample := range results.Samples {
		state.Sample(sample.Run, sample.Values, time.Now())
		report := state.Outbox[len(state.Outbox)-1]
		for _, usage := range report.Usage {
			if usage.UserID == u.ID {
				expected += usage.Upload + usage.Download
			}
		}
		// Retries must never charge traffic twice.
		for i := 0; i < 2; i++ {
			if err := s.ApplyReport(ctx, n.ID, report); err != nil {
				t.Fatal(err)
			}
		}
	}
	var used int64
	if err := s.DB.QueryRowContext(ctx, "SELECT used_bytes FROM users WHERE id=$1", u.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if expected == 0 || used != expected {
		t.Fatalf("shared quota mismatch: persisted=%d sampled=%d", used, expected)
	}
	t.Logf("PASS shared PostgreSQL quota and idempotent report replay: %d bytes", used)
}
