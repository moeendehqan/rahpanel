package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/core"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var ErrCoreStopped = errors.New("Xray process stopped")

type Config struct {
	NodeID    string `json:"node_id"`
	URL       string `json:"url"`
	Directory string `json:"directory"`
}
type State struct {
	AccountingBlocked bool             `json:"accounting_blocked"`
	RunID             string           `json:"run_id"`
	Sequence          int64            `json:"sequence"`
	Baseline          map[string]int64 `json:"baseline"`
	Outbox            []core.Report    `json:"outbox"`
	AppliedVersion    string           `json:"applied_version"`
	LastError         string           `json:"last_error"`
}

// NewLease uses boot uptime, never wall time, and leaves shutdown latency margin.
func NewLease(boot string, requestUptime float64) Lease {
	return Lease{BootID: boot, Deadline: requestUptime + 295, ConfigDeadline: requestUptime + 295}
}
func ConfigDeadline(l Lease, serverTime time.Time, requestUptime float64, users []core.User) float64 {
	safe := l.Deadline
	for _, u := range users {
		if u.Active(serverTime) {
			d := requestUptime + u.ExpiresAt.Sub(serverTime).Seconds()
			if d < safe {
				safe = d
			}
		}
	}
	return safe
}

type Lease struct {
	BootID         string  `json:"boot_id"`
	Deadline       float64 `json:"deadline"`
	ConfigDeadline float64 `json:"config_deadline"`
}

func (l Lease) Valid(boot string, uptime float64) bool {
	return l.BootID == boot && uptime >= 0 && uptime < l.Deadline && uptime < l.ConfigDeadline
}
func Boot() (string, float64, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return "", 0, e
	}
	u, e := os.ReadFile("/proc/uptime")
	if e != nil {
		return "", 0, e
	}
	f := strings.Fields(string(u))
	if len(f) == 0 {
		return "", 0, fmt.Errorf("missing uptime")
	}
	n, e := strconv.ParseFloat(f[0], 64)
	return strings.TrimSpace(string(b)), n, e
}
func Atomic(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".rah-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
func Save(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return Atomic(path, b)
}
func (s *State) Sample(run string, values map[string]int64, now time.Time) {
	if s.RunID != run {
		s.RunID = run
		s.Sequence = 0
		s.Baseline = map[string]int64{}
	}
	if s.Baseline == nil {
		s.Baseline = map[string]int64{}
	}
	usage := map[string]*core.Usage{}
	for name, value := range values {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" || value < 0 {
			continue
		}
		old := s.Baseline[name]
		delta := value - old
		if delta < 0 {
			delta = value
		}
		s.Baseline[name] = value
		if delta == 0 {
			continue
		}
		u := usage[parts[1]]
		if u == nil {
			u = &core.Usage{UserID: parts[1]}
			usage[parts[1]] = u
		}
		switch parts[3] {
		case "uplink":
			u.Upload += delta
		case "downlink":
			u.Download += delta
		}
	}
	list := []core.Usage{}
	for _, u := range usage {
		list = append(list, *u)
	}
	s.Sequence++
	s.Outbox = append(s.Outbox, core.Report{ID: core.ID(), RunID: run, Sequence: s.Sequence, Usage: list, AppliedVersion: s.AppliedVersion, Error: s.LastError})
}
func Prune(raw []byte, users []core.User, now time.Time) ([]byte, error) {
	var cfg map[string]any
	if e := json.Unmarshal(raw, &cfg); e != nil {
		return nil, e
	}
	active := map[string]bool{}
	for _, u := range users {
		active[u.ID] = u.Active(now)
	}
	inbounds, ok := cfg["inbounds"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing inbounds")
	}
	for _, in := range inbounds {
		m, ok := in.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid inbound")
		}
		settings, ok := m["settings"].(map[string]any)
		if !ok {
			continue
		}
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		keep := []any{}
		for _, c := range clients {
			v, ok := c.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid client")
			}
			email, _ := v["email"].(string)
			if email == "" || active[email] {
				keep = append(keep, c)
			}
		}
		settings["clients"] = keep
	}
	return json.MarshalIndent(cfg, "", "  ")
}
