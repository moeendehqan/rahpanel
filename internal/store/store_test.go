package store

import (
	"context"
	"database/sql"
	"github.com/rahpanel/rahpanel/internal/core"
	"os"
	"sync"
	"testing"
	"time"
)

func TestUsageExactlyOnceAcrossNodesAndProcessRuns(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; requires an isolated PostgreSQL database")
	}
	ctx := context.Background()
	s, e := New(ctx, dsn, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if _, e = s.DB.Exec("TRUNCATE users,nodes,jobs,reports,credentials CASCADE"); e != nil {
		t.Fatal(e)
	}
	u := core.User{ID: core.ID(), Name: "usage-test", Enabled: true, ExpiresAt: time.Now().Add(time.Hour), GroupID: "default"}
	if e = s.SaveUser(ctx, u, true); e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for i := 0; i < 2; i++ {
		n := core.Node{ID: core.ID(), Name: "test", Host: core.ID(), Domain: core.ID() + ".example.com", GroupID: "default", RealityTarget: "example.com:443", RealitySNI: "example.com"}
		if e = core.NewNodeKeys(&n); e != nil {
			t.Fatal(e)
		}
		if e = s.AddNode(ctx, n); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Credentials(ctx, n.ID, []core.User{u}); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, n.ID)
	}
	r := core.Report{ID: "report-1", RunID: "process-1", Sequence: 1, Usage: []core.Usage{{UserID: u.ID, Upload: 10, Download: 15}}}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.ApplyReport(ctx, ids[0], r); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if e = s.ApplyReport(ctx, ids[1], r); e != nil {
		t.Fatal(e)
	}
	r.RunID = "process-2"
	r.ID = "report-2"
	if e = s.ApplyReport(ctx, ids[0], r); e != nil {
		t.Fatal(e)
	}
	var used int64
	if e = s.DB.QueryRow("SELECT used_bytes FROM users WHERE id=$1", u.ID).Scan(&used); e != nil {
		t.Fatal(e)
	}
	if used != 75 {
		t.Fatalf("want 75, got %d", used)
	}
	r.ID = "bad"
	r.Sequence = 2
	r.Usage[0].Upload = -1
	if e = s.ApplyReport(ctx, ids[0], r); e == nil {
		t.Fatal("negative counter accepted")
	}
	var count int
	if e = s.DB.QueryRow("SELECT count(*) FROM reports WHERE id='bad'").Scan(&count); e != nil || count != 0 {
		t.Fatal("invalid report was not rolled back")
	}
}
func TestDatabaseUnavailableDoesNotCreateAnInMemoryFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, e := New(ctx, "postgres://bad:bad@127.0.0.1:1/missing?sslmode=disable", make([]byte, 32))
	if e == nil {
		t.Fatal("expected database error")
	}
}

var _ = sql.ErrNoRows
