package store

import (
	"context"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/core"
	"os"
	"sync"
	"testing"
	"time"
)

func TestControlPlaneCapacity(t *testing.T) {
	if os.Getenv("RUN_CAPACITY_TEST") != "1" || os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("opt-in control-plane capacity exercise")
	}
	ctx := context.Background()
	s, e := New(ctx, os.Getenv("TEST_DATABASE_URL"), make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if _, e = s.DB.Exec("TRUNCATE users,nodes,jobs,reports,credentials CASCADE"); e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	users := []core.User{}
	for i := 0; i < 1000; i++ {
		u := core.User{ID: core.ID(), Name: fmt.Sprintf("capacity-%04d", i), Enabled: true, ExpiresAt: time.Now().Add(time.Hour), QuotaBytes: 1 << 30, GroupID: "default"}
		if e = s.SaveUser(ctx, u, true); e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
	}
	nodes := []core.Node{}
	for i := 0; i < 20; i++ {
		n := core.Node{ID: core.ID(), Name: fmt.Sprintf("node-%d", i), Host: fmt.Sprintf("192.0.2.%d", i+1), Domain: fmt.Sprintf("n%d.example.com", i), GroupID: "default", RealityTarget: "example.com:443", RealitySNI: "example.com"}
		if e = core.NewNodeKeys(&n); e != nil {
			t.Fatal(e)
		}
		if e = s.AddNode(ctx, n); e != nil {
			t.Fatal(e)
		}
		nodes = append(nodes, n)
	}
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n core.Node) {
			defer wg.Done()
			cs, e := s.Credentials(ctx, n.ID, users)
			if e != nil {
				t.Error(e)
				return
			}
			if len(cs) != 1000 {
				t.Errorf("node has %d credentials", len(cs))
				return
			}
			if _, e = core.BuildConfig(n, users, cs, time.Now()); e != nil {
				t.Error(e)
			}
		}(n)
	}
	wg.Wait()
	t.Logf("Provisioned 1,000 users × 20 nodes and generated configs in %s (not a throughput benchmark)", time.Since(started))
	started = time.Now()
	for _, n := range nodes {
		cs, e := s.Credentials(ctx, n.ID, users)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = core.BuildConfig(n, users, cs, time.Now()); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("Steady-state reconciliation across 20 nodes: %s", time.Since(started))
}
