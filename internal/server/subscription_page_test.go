package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

func TestSubscriptionPageNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, accept, mode, dest string
		want                                   bool
	}{
		{"navigation", "GET", "/sub/test", "TEXT/HTML; q=0.8, */*;q=0.5", "navigate", "document", true},
		{"head", "HEAD", "/sub/test", "text/html", "navigate", "document", false},
		{"yaml", "GET", "/sub/test/clash", "text/html", "navigate", "document", false},
		{"no accept", "GET", "/sub/test", "", "navigate", "document", false},
		{"wildcard", "GET", "/sub/test", "*/*", "navigate", "document", false},
		{"client", "GET", "/sub/test", "text/html", "", "", false},
		{"iframe", "GET", "/sub/test", "text/html", "navigate", "iframe", false},
		{"rejected", "GET", "/sub/test", "text/html;q=0, */*", "navigate", "document", false},
		{"invalid quality", "GET", "/sub/test", "text/html;q=NaN", "navigate", "document", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Header.Set("Accept", tc.accept)
			r.Header.Set("Sec-Fetch-Mode", tc.mode)
			r.Header.Set("Sec-Fetch-Dest", tc.dest)
			mux := http.NewServeMux()
			check := func(_ http.ResponseWriter, r *http.Request) {
				if got := wantsSubscriptionPage(r); got != tc.want {
					t.Errorf("wantsSubscriptionPage = %t, want %t", got, tc.want)
				}
			}
			mux.HandleFunc(subscriptionPageRoute, check)
			mux.HandleFunc("GET /sub/{token}/clash", check)
			mux.ServeHTTP(httptest.NewRecorder(), r)
		})
	}
}

func TestRenderSubscriptionPage(t *testing.T) {
	s := Server{PublicURL: "https://panel.example.com/"}
	user := core.User{
		Name: "<example>", Token: "test-only-token",
		UsedBytes: 1 << 30,
		ExpiresAt: time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC),
	}
	w := httptest.NewRecorder()
	s.renderSubscriptionPage(w, user)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("unexpected response: %d %v", w.Code, w.Header())
	}
	body := w.Body.String()
	for _, want := range []string{
		`lang="fa" dir="rtl"`, "&lt;example&gt;", "1.00 گیگابایت", "نامحدود", "2026/11/18 · 14:30",
		`value="https://panel.example.com/sub/test-only-token"`,
		`value="https://panel.example.com/sub/test-only-token/clash"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	_, nonce, ok := strings.Cut(w.Header().Get("Content-Security-Policy"), "'nonce-")
	nonce, _, _ = strings.Cut(nonce, "'")
	if !ok || nonce == "" || strings.Count(body, `nonce="`+nonce+`"`) != 2 {
		t.Error("CSP nonce must match both inline resources")
	}
}
