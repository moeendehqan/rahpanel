package core

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExpiredSuspendedAndExhaustedUsersNeverEnterConfig(t *testing.T) {
	now := time.Now()
	n := Node{ID: "n1", Host: "203.0.113.10", Domain: "n1.example.com", RealityTarget: "www.microsoft.com:443", RealitySNI: "www.microsoft.com", RealityPrivate: "key", RealityPublic: "pub", ShortID: "abcdef12", WSPath: "/private"}
	users := []User{{ID: "active", Enabled: true, ExpiresAt: now.Add(time.Hour)}, {ID: "expired", Enabled: true, ExpiresAt: now.Add(-time.Second)}, {ID: "suspended", Enabled: false, ExpiresAt: now.Add(time.Hour)}, {ID: "exhausted", Enabled: true, ExpiresAt: now.Add(time.Hour), QuotaBytes: 100, UsedBytes: 100}}
	creds := []Credential{}
	for _, u := range users {
		creds = append(creds, Credential{UserID: u.ID, NodeID: n.ID, UUID: "id-" + u.ID, Password: "pw-" + u.ID})
	}
	b, err := BuildConfig(n, users, creds, now)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err = json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "id-active") {
		t.Fatal("active user missing")
	}
	for _, s := range []string{"id-expired", "id-suspended", "id-exhausted", "pw-expired"} {
		if strings.Contains(string(b), s) {
			t.Fatalf("unauthorized credential %s published", s)
		}
	}
	if strings.Contains(string(b), "access.log") {
		t.Fatal("destination logging enabled")
	}
}

func TestSubscriptionPublishesOnlyVerifiedTransports(t *testing.T) {
	n := Node{ID: "n", Name: "Tehran / 1", Domain: "n.example.com", Host: "203.0.113.1", RealityPublic: "public", RealitySNI: "example.org", ShortID: "abcdef", WSPath: "/hello", Ready: true, Verified: []string{"reality", "trojan"}}
	b := Subscription([]Node{n}, []Credential{{NodeID: "n", UUID: "123", Password: "secret"}})
	raw, e := base64.StdEncoding.DecodeString(b)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "vless://123@203.0.113.1:443") || !strings.Contains(string(raw), "trojan://secret@n.example.com:2053") {
		t.Fatalf("missing expected links: %s", raw)
	}
	if strings.Contains(string(raw), "8443") {
		t.Fatal("unverified WS transport leaked")
	}
	n.Ready = false
	if Subscription([]Node{n}, nil) != "" {
		t.Fatal("offline node published")
	}
}

func TestLeaseFailsClosedAtBoundary(t *testing.T) {
	now := time.Now()
	if LeaseValid(now, now) {
		t.Fatal("expired lease accepted")
	}
	if LeaseValid(time.Time{}, now) {
		t.Fatal("missing lease accepted")
	}
	if !LeaseValid(now.Add(time.Second), now) {
		t.Fatal("valid lease rejected")
	}
}

func TestSecretsRoundTripAndTampering(t *testing.T) {
	key := make([]byte, 32)
	sealed, e := Seal(key, "secret")
	if e != nil {
		t.Fatal(e)
	}
	plain, e := Open(key, sealed)
	if e != nil || plain != "secret" {
		t.Fatal("cannot decrypt")
	}
	sealed[len(sealed)-1] ^= 1
	if _, e = Open(key, sealed); e == nil {
		t.Fatal("tampering accepted")
	}
}
