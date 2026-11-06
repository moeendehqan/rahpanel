package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatePersistsDeltasAndBaselineTogether(t *testing.T) {
	s := State{}
	s.Sample("run", map[string]int64{"user>>>u>>>traffic>>>downlink": 42}, time.Now())
	path := filepath.Join(t.TempDir(), "state.json")
	if e := Save(path, s); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var recovered State
	if e = json.Unmarshal(b, &recovered); e != nil {
		t.Fatal(e)
	}
	recovered.Sample("run", map[string]int64{"user>>>u>>>traffic>>>downlink": 45}, time.Now())
	if len(recovered.Outbox) != 2 || recovered.Outbox[1].Usage[0].Download != 3 {
		t.Fatal("restart duplicated cumulative bytes")
	}
	if e = Save(path, recovered); e != nil {
		t.Fatal(e)
	}
}
