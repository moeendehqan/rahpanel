package server

import (
	"github.com/rahpanel/rahpanel/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrossOriginMutationIsRejectedBeforeAuthentication(t *testing.T) {
	s := New(&store.Store{}, nil, "https://panel.example.com", "missing", true)
	req := httptest.NewRequest("POST", "https://panel.example.com/api/v1/users", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example.com")
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatalf("want 403, got %d", res.Code)
	}
}
func TestAgentEndpointRejectsMissingClientCertificate(t *testing.T) {
	s := New(&store.Store{}, nil, "https://panel.example.com", "missing", true)
	res := httptest.NewRecorder()
	s.AgentHandler().ServeHTTP(res, httptest.NewRequest("GET", "/agent/v1/snapshot", nil))
	if res.Code != 401 {
		t.Fatalf("want 401, got %d", res.Code)
	}
}
func TestUserEndpointsNeverPermitAnonymousAccess(t *testing.T) {
	s := New(&store.Store{}, nil, "https://panel.example.com", "missing", true)
	for _, path := range []string{"/api/v1/users", "/api/v1/nodes", "/api/v1/settings", "/api/v1/audit"} {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, httptest.NewRequest("GET", path, nil))
		if res.Code != 401 {
			t.Fatalf("anonymous access to %s: %d", path, res.Code)
		}
	}
}
