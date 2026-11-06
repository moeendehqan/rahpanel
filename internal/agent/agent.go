package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/core"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func command(ctx context.Context, name string, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exec.CommandContext(c, name, args...).Output()
}
func stop(ctx context.Context) error {
	_, e := command(ctx, "systemctl", "stop", "rah-xray.service")
	return e
}
func Watchdog(ctx context.Context, dir string) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var l Lease
		b, e := os.ReadFile(filepath.Join(dir, "lease.json"))
		if e == nil {
			e = json.Unmarshal(b, &l)
		}
		boot, up, be := Boot()
		if e != nil || be != nil || !l.Valid(boot, up) {
			_ = stop(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func Run(ctx context.Context, cfg Config) error {
	if cfg.StatsPort == 0 {
		cfg.StatsPort = 10085
	}
	if cfg.StatsPort < 1 || cfg.StatsPort > 65535 {
		return fmt.Errorf("invalid statistics port")
	}
	if cfg.Directory == "" {
		cfg.Directory = "/etc/rahpanel"
	}
	dir := cfg.Directory
	defer stop(context.Background())
	cert, e := tls.LoadX509KeyPair(filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key"))
	if e != nil {
		return e
	}
	var rotated struct{ Certificate, Key []byte }
	if raw, err := os.ReadFile(filepath.Join(dir, "identity.json")); err == nil {
		if err = json.Unmarshal(raw, &rotated); err != nil {
			return err
		}
		cert, e = tls.X509KeyPair(rotated.Certificate, rotated.Key)
		if e != nil {
			return e
		}
	}
	var currentIdentity atomic.Pointer[tls.Certificate]
	currentIdentity.Store(&cert)
	ca, e := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if e != nil {
		return e
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return fmt.Errorf("invalid node CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return currentIdentity.Load(), nil }}}
	client := &http.Client{Timeout: 20 * time.Second, Transport: transport}
	state := State{Baseline: map[string]int64{}, Outbox: []core.Report{}}
	statePath := filepath.Join(dir, "state.json")
	if b, e := os.ReadFile(statePath); e == nil {
		if e = json.Unmarshal(b, &state); e != nil {
			return fmt.Errorf("corrupt usage state; refusing to lose accounting")
		}
	}
	if state.AccountingBlocked {
		return fmt.Errorf("accounting is quarantined; repair local state before resuming")
	}
	var snapshot core.Snapshot
	have := false
	lastReport := time.Time{}
	lastPoll := time.Time{}
	lastApplied := ""
	lease := Lease{}
	requestUptime := float64(0)
	serverReceived := time.Time{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	sample := func() error {
		if len(state.Outbox) > 10000 {
			return fmt.Errorf("usage queue capacity exceeded")
		}
		samplingCtx, samplingCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer samplingCancel()
		run, values, e := readStats(samplingCtx, cfg.StatsPort)
		if errors.Is(e, ErrCoreStopped) {
			lastApplied = ""
			return nil
		}
		if e != nil {
			return e
		}
		state.Sample(run, values, time.Now())
		return Save(statePath, state)
	}
	quarantine := func(cause error) error {
		state.AccountingBlocked = true
		state.LastError = "accounting"
		_ = Save(statePath, state)
		_ = Atomic(filepath.Join(dir, "accounting-blocked"), []byte("Manual accounting recovery required\n"))
		_ = stop(context.Background())
		return fmt.Errorf("accounting quarantined: %w", cause)
	}
	if _, err := os.Stat(filepath.Join(dir, "accounting-blocked")); err == nil {
		return fmt.Errorf("accounting quarantine marker exists")
	}
	// Recover the still-running core's final cumulative sample before controlled stop.
	if running(ctx) {
		if e = sample(); e != nil {
			return quarantine(e)
		}
	}
	_ = stop(ctx)
	for {
		now := time.Now()
		if now.Sub(lastReport) >= 30*time.Second {
			lastReport = now
			if lastApplied != "" {
				if e = sample(); e != nil {
					return quarantine(e)
				}
			}
			for len(state.Outbox) > 0 {
				r := state.Outbox[0]
				body, _ := json.Marshal(r)
				req, _ := http.NewRequestWithContext(ctx, "POST", cfg.URL+"/agent/v1/report", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				res, e := client.Do(req)
				if e != nil {
					break
				}
				io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
				res.Body.Close()
				if res.StatusCode != 200 {
					break
				}
				state.Outbox = state.Outbox[1:]
				if e = Save(statePath, state); e != nil {
					_ = stop(ctx)
					return e
				}
			}
		}
		if now.Sub(lastPoll) >= 30*time.Second {
			lastPoll = now
			boot, up, bootErr := Boot()
			started := time.Now()
			req, _ := http.NewRequestWithContext(ctx, "GET", cfg.URL+"/agent/v1/snapshot", nil)
			res, re := client.Do(req)
			if re == nil {
				var next core.Snapshot
				if res.StatusCode == 200 {
					re = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&next)
				} else {
					re = fmt.Errorf("snapshot rejected")
				}
				res.Body.Close()
				if re == nil && bootErr == nil && !next.ServerTime.IsZero() && next.LeaseUntil.Sub(next.ServerTime) <= 5*time.Minute && next.LeaseUntil.After(next.ServerTime) && len(next.Config) > 0 {
					if len(next.ClientCertificate) > 0 {
						pair, err := tls.X509KeyPair(next.ClientCertificate, next.ClientPrivateKey)
						if err != nil {
							return err
						}
						leaf, err := x509.ParseCertificate(pair.Certificate[0])
						if err != nil || leaf.Subject.CommonName != cfg.NodeID {
							return fmt.Errorf("invalid renewed node identity")
						}
						if _, err = leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
							return err
						}
						if err = Save(filepath.Join(dir, "identity.json"), struct{ Certificate, Key []byte }{next.ClientCertificate, next.ClientPrivateKey}); err != nil {
							return err
						}
						currentIdentity.Store(&pair)
						transport.CloseIdleConnections()
					}
					snapshot = next
					lease = NewLease(boot, up)
					requestUptime = up
					serverReceived = started
					have = true
				}
			}
		}
		boot, up, bootErr := Boot()
		if !have || bootErr != nil || !lease.Valid(boot, up) {
			if running(ctx) {
				if e = sample(); e != nil {
					return quarantine(e)
				}
			}
			_ = stop(ctx)
			lastApplied = ""
		} else {
			effectiveNow := snapshot.ServerTime.Add(time.Since(serverReceived))
			// Apply expiry slightly early so the watchdog never observes an old
			// configuration after its earliest user's deadline.
			desired, re := Prune(snapshot.Config, snapshot.Users, effectiveNow.Add(2*time.Second))
			if re != nil {
				_ = stop(ctx)
				return re
			}
			h := sha256.Sum256(append(append(append([]byte{}, desired...), snapshot.Certificate...), snapshot.PrivateKey...))
			version := hex.EncodeToString(h[:])
			if version != lastApplied {
				if lastApplied != "" {
					if re = sample(); re != nil {
						return quarantine(re)
					}
				}
				remaining := []core.User{}
				for _, u := range snapshot.Users {
					if u.Active(effectiveNow.Add(2 * time.Second)) {
						remaining = append(remaining, u)
					}
				}
				lease.ConfigDeadline = ConfigDeadline(lease, snapshot.ServerTime, requestUptime, remaining)
				re = apply(ctx, dir, desired, snapshot.Certificate, snapshot.PrivateKey, lease)
				if re != nil {
					state.LastError = "apply"
					_ = stop(ctx)
					lastApplied = ""
				} else {
					lastApplied = version
					state.AppliedVersion = snapshot.Version
					state.LastError = ""
				}
			}
			if lastApplied != "" {
				_, _, re := Boot()
				if re != nil {
					return re
				}
				// Exclude already-pruned users when advancing the configuration safety deadline.
				remaining := []core.User{}
				for _, u := range snapshot.Users {
					if u.Active(effectiveNow.Add(2 * time.Second)) {
						remaining = append(remaining, u)
					}
				}
				lease.ConfigDeadline = ConfigDeadline(lease, snapshot.ServerTime, requestUptime, remaining)
				if re = Save(filepath.Join(dir, "lease.json"), lease); re != nil {
					_ = stop(ctx)
					return re
				}
				boot, up, e = Boot()
				if e != nil || !lease.Valid(boot, up) {
					_ = stop(ctx)
					lastApplied = ""
					continue
				}
				if _, re = command(ctx, "systemctl", "start", "rah-xray.service"); re != nil {
					state.LastError = "start"
				}
			}
		}
		select {
		case <-ctx.Done():
			if running(context.Background()) {
				_ = sample()
			}
			_ = stop(context.Background())
			return nil
		case <-tick.C:
		}
	}
}
func running(ctx context.Context) bool {
	b, e := command(ctx, "systemctl", "show", "rah-xray.service", "--property=MainPID", "--value")
	return e == nil && strings.TrimSpace(string(b)) != "0" && strings.TrimSpace(string(b)) != ""
}
func readStats(ctx context.Context, statsPort int) (string, map[string]int64, error) {
	pid, e := command(ctx, "systemctl", "show", "rah-xray.service", "--property=MainPID", "--value")
	if e != nil {
		return "", nil, e
	}
	p := strings.TrimSpace(string(pid))
	if p == "0" || p == "" {
		return "", nil, ErrCoreStopped
	}
	proc, e := os.ReadFile("/proc/" + p + "/stat")
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return "", nil, ErrCoreStopped
		}
		return "", nil, e
	}
	boot, _, e := Boot()
	if e != nil {
		return "", nil, e
	}
	start := strings.LastIndex(string(proc), ")")
	fields := strings.Fields(string(proc)[start+1:])
	if len(fields) < 20 {
		return "", nil, fmt.Errorf("invalid process stat")
	}
	run := boot + ":" + p + ":" + fields[19]
	raw, e := command(ctx, "/usr/local/bin/rah-xray", "api", "statsquery", fmt.Sprintf("--server=127.0.0.1:%d", statsPort), "-pattern", "user>>>")
	if e != nil {
		if !running(ctx) {
			return "", nil, ErrCoreStopped
		}
		return "", nil, fmt.Errorf("Xray statistics unavailable")
	}
	var data struct {
		Stat []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"stat"`
	}
	if e = json.Unmarshal(raw, &data); e != nil {
		return "", nil, e
	}
	values := map[string]int64{}
	for _, s := range data.Stat {
		v := strings.Trim(string(s.Value), "\"")
		if v == "" {
			v = "0"
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil {
			return "", nil, e
		}
		values[s.Name] = n
	}
	return run, values, nil
}
func apply(ctx context.Context, dir string, cfg, cert, key []byte, lease Lease) error {
	// Preserve previous material on disk. Validation never replaces a running configuration.
	previous := map[string][]byte{}
	rollback := func() {
		for p, b := range previous {
			if b != nil {
				_ = Atomic(p, b)
			} else {
				_ = os.Remove(p)
			}
		}
	}
	for name, data := range map[string][]byte{"tls.crt": cert, "tls.key": key} {
		path := filepath.Join(dir, name)
		previous[path], _ = os.ReadFile(path)
		if e := Atomic(path, data); e != nil {
			rollback()
			return e
		}
	}
	candidate := filepath.Join(dir, "candidate.json")
	if e := Atomic(candidate, cfg); e != nil {
		rollback()
		return e
	}
	if _, e := command(ctx, "/usr/local/bin/rah-xray", "run", "-test", "-config", candidate); e != nil {
		rollback()
		return fmt.Errorf("Xray rejected configuration")
	}
	path := filepath.Join(dir, "xray.json")
	old, _ := os.ReadFile(path)
	previous[path] = old
	if e := Atomic(path, cfg); e != nil {
		rollback()
		return e
	}
	boot, up, e := Boot()
	if e != nil || !lease.Valid(boot, up) {
		rollback()
		if len(old) > 0 {
			_ = Atomic(path, old)
		}
		return fmt.Errorf("lease expired while validating configuration")
	}
	if e = Save(filepath.Join(dir, "lease.json"), lease); e != nil {
		rollback()
		return e
	}
	if _, e := command(ctx, "systemctl", "restart", "rah-xray.service"); e != nil {
		if len(old) > 0 {
			_ = Atomic(path, old)
		}
		rollback()
		return fmt.Errorf("Xray restart failed")
	}
	return nil
}
