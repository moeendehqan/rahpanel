package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

func TestReportSenderKeepsSnapshotRequestsIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan string, 4)
	reply := make(chan int)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent/v1/snapshot":
			w.WriteHeader(http.StatusOK)
		case "/agent/v1/report":
			var report core.Report
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			started <- report.ID
			select {
			case status := <-reply:
				w.WriteHeader(status)
			case <-ctx.Done():
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() { cancel(); panel.Close() }()

	now := time.Now()
	state := State{Outbox: []core.Report{{ID: "first"}, {ID: "second"}}}
	path := filepath.Join(t.TempDir(), "state.json")
	var sender reportSender
	step := func(at time.Time) {
		t.Helper()
		if err := sender.step(ctx, at, panel.Client(), panel.URL, path, &state); err != nil {
			t.Fatal(err)
		}
	}
	expectReport := func(want string) {
		t.Helper()
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("report = %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	finish := func(status int) {
		t.Helper()
		reply <- status
		select {
		case result := <-sender.pending:
			completed := make(chan reportResult, 1)
			completed <- result
			sender.pending = completed
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	step(now)
	expectReport("first")
	inFlight := sender.pending
	step(now.Add(30 * time.Second))
	if sender.pending != inFlight || len(state.Outbox) != 2 {
		t.Fatal("pending report was replaced or removed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, panel.URL+"/agent/v1/snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := panel.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("snapshot returned %d", res.StatusCode)
	}

	finish(http.StatusServiceUnavailable)
	step(now)
	step(now.Add(29 * time.Second))
	if sender.pending != nil || len(state.Outbox) != 2 {
		t.Fatal("failed report was removed or retried early")
	}
	step(now.Add(30 * time.Second))
	expectReport("first")
	finish(http.StatusOK)
	step(now.Add(31 * time.Second))
	expectReport("second")
	if len(state.Outbox) != 1 || state.Outbox[0].ID != "second" {
		t.Fatalf("unexpected remaining reports: %+v", state.Outbox)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var recovered State
	if err := json.Unmarshal(raw, &recovered); err != nil {
		t.Fatal(err)
	}
	if len(recovered.Outbox) != 1 || recovered.Outbox[0].ID != "second" {
		t.Fatalf("unexpected persisted reports: %+v", recovered.Outbox)
	}
}
