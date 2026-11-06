package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckRequiresSuccessfulReadiness(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer srv.Close()
			err := probeHealth(context.Background(), srv.URL)
			if (err == nil) != (status == http.StatusOK) {
				t.Fatalf("status %d: %v", status, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if probeHealth(ctx, "http://127.0.0.1:1") == nil {
		t.Fatal("unreachable service reported ready")
	}
}
