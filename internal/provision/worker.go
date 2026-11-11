package provision

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/agent"
	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/security"
	"github.com/rahpanel/rahpanel/internal/store"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Request struct {
	JobID, NodeID string
	SSH           SSHCredentials
}
type Worker struct {
	Store   *store.Store
	CA      *security.Authority
	Queue   chan Request
	certMu  sync.Mutex
	routeMu sync.RWMutex
	routes  map[routeKey]map[string]routeSample
}

func NewWorker(s *store.Store, ca *security.Authority) *Worker {
	return &Worker{Store: s, CA: ca, Queue: make(chan Request, 20)}
}
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-w.Queue:
			w.run(ctx, r)
		}
	}
}
func (w *Worker) run(parent context.Context, r Request) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	j := core.Job{ID: r.JobID, NodeID: r.NodeID, Status: "running", Step: "preflight"}
	update := func(step string) { j.Step = step; _ = w.Store.Job(ctx, j) }
	fail := func(message string) { j.Status = "failed"; j.Error = message; _ = w.Store.Job(context.Background(), j) }
	if e := w.Store.Job(ctx, j); e != nil {
		return
	}
	n, e := w.Store.Node(ctx, r.NodeID)
	if e != nil {
		fail("Node not found")
		return
	}
	if n.Host != r.SSH.Host {
		fail("Node IP changed; start installation again using the new address")
		return
	}
	var cfg Settings
	if e = w.Store.GetSetting(ctx, "provider", &cfg); e != nil || cfg.Validate() != nil {
		fail("Configure domain, Cloudflare and panel URLs first")
		return
	}
	client, e := connect(ctx, r.SSH)
	r.SSH.Password = ""
	r.SSH.PrivateKey = ""
	if e != nil {
		fail(e.Error())
		return
	}
	defer client.Close()
	script, e := preflightScript(n)
	if e != nil {
		fail(e.Error())
		return
	}
	pre, e := remote(client, script)
	if e == nil && !n.RealityBehindNginx && strings.Contains(string(pre), "NGINX_REALITY_443\n") {
		n.RealityBehindNginx = true
		script, e = preflightScript(n)
		if e != nil {
			fail(e.Error())
			return
		}
		pre, e = remote(client, script)
	}
	if e != nil {
		for _, line := range strings.Split(string(pre), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PORT_CONFLICT:") {
				port, err := strconv.Atoi(strings.TrimPrefix(line, "PORT_CONFLICT:"))
				if err == nil {
					fail(fmt.Sprintf("Port %d is used by another service. Select a free port in installation settings.", port))
					return
				}
			}
			if line == "MANAGED_NODE_MISMATCH" || line == "UNMANAGED_RAH_INSTALLATION" {
				fail("Existing Rah installation belongs to another node or is not safely identified; no services were changed")
				return
			}
		}
		fail("Preflight failed: Ubuntu 24.04, sudo access, free ports and disk are required")
		return
	}
	configureNginx := func(mode string) error {
		script, err := nginxRealityScript(n, mode)
		if err != nil {
			return err
		}
		out, err := remote(client, script)
		if err == nil {
			return nil
		}
		if message := strings.TrimSpace(string(out)); strings.HasPrefix(message, "Nginx: ") {
			return fmt.Errorf("%s", message)
		}
		return fmt.Errorf("could not configure Nginx to share TCP 443 with REALITY")
	}
	if n.RealityBehindNginx {
		if err := configureNginx("check"); err != nil {
			fail(err.Error())
			return
		}
	}
	arch := "amd64"
	if strings.Contains(string(pre), "aarch64") {
		arch = "arm64"
	} else if !strings.Contains(string(pre), "x86_64") {
		fail("Only amd64 and arm64 are supported")
		return
	}
	update("dns")
	dns := DNS{Token: cfg.CloudflareToken, Zone: cfg.ZoneID}
	if e = dns.EnsureAddress(ctx, n.Domain, n.Host, n.ID); e != nil {
		fail(e.Error())
		return
	}
	update("certificate")
	cert, e := w.Certificate(ctx, n.ID, cfg, n.Domain)
	if e != nil {
		fail("Certificate issuance failed; verify DNS permissions and ACME reachability")
		return
	}
	update("identity")
	crt, key, serial, e := w.CA.Issue(n.ID, false, nil)
	if e != nil {
		fail("Node identity issuance failed")
		return
	}
	update("artifacts")
	binary, e := agentBinary(arch)
	if e != nil {
		fail("Agent artifact missing; build Linux release artifacts first")
		return
	}
	xray, e := loadXray(arch)
	if e != nil {
		fail(e.Error())
		return
	}
	users, e := w.Store.Users(ctx)
	if e != nil {
		fail("Cannot read users")
		return
	}
	allowed := []core.User{}
	for _, u := range users {
		if u.GroupID == n.GroupID {
			allowed = append(allowed, u)
		}
	}
	creds, e := w.Store.Credentials(ctx, n.ID, allowed)
	if e != nil {
		fail("Cannot issue node credentials")
		return
	}
	config, e := core.BuildConfig(n, allowed, creds, time.Now())
	if e != nil {
		fail("Configuration generation failed")
		return
	}
	files := units()
	agentConfig, _ := json.Marshal(agent.Config{NodeID: n.ID, URL: cfg.AgentURL, Directory: "/etc/rahpanel", StatsPort: n.EffectivePorts().Stats})
	for name, b := range map[string][]byte{"agent": binary, "xray": xray, "agent.json": agentConfig, "client.crt": crt, "client.key": key, "ca.crt": w.CA.Certificate, "tls.crt": cert.Certificate, "tls.key": cert.Key, "xray.json": config} {
		files[name] = b
	}
	update("install")
	stage := "/tmp/rah-" + j.ID
	if e = upload(client, stage, files); e != nil {
		fail("SSH artifact upload failed")
		return
	}
	// Activate the new serial before starting the reconciler; old identities are revoked.
	sealedClientKey, se := core.Seal(w.Store.Key, string(key))
	if se != nil {
		fail("Cannot protect node identity")
		return
	}
	if _, e = w.Store.DB.ExecContext(ctx, "UPDATE nodes SET client_serial=$2,client_cert=$3,client_key=$4,reality_behind_nginx=$5,previous_serial='',previous_until=NULL,ready=false,verified='[]' WHERE id=$1", n.ID, serial, crt, sealedClientKey, n.RealityBehindNginx); e != nil {
		fail("Cannot activate node identity")
		return
	}
	script, e = installNodeScript(stage, n)
	if e != nil {
		fail(e.Error())
		return
	}
	if _, e = remote(client, script); e != nil {
		fail("Remote installation failed; retry with SSH credentials after correcting host prerequisites")
		return
	}
	if n.RealityBehindNginx {
		update("nginx")
		if err := configureNginx("apply"); err != nil {
			fail(err.Error())
			return
		}
	}
	update("verify")
	verified := []string{}
	for attempt := 0; attempt < 12; attempt++ {
		verified = []string{}
		for _, kind := range []string{"reality", "websocket", "trojan"} {
			if verifyTransport(ctx, n, kind, cfg.PublicURL) == nil {
				verified = append(verified, kind)
			}
		}
		if len(verified) == 3 {
			break
		}
		select {
		case <-ctx.Done():
			fail("Verification timed out")
			return
		case <-time.After(5 * time.Second):
		}
	}
	if len(verified) == 0 {
		fail("No transport reachable from panel; check provider firewall, DNS and node agent")
		return
	}
	encoded, _ := json.Marshal(verified)
	if _, e = w.Store.DB.ExecContext(ctx, "UPDATE nodes SET ready=true,verified=$2 WHERE id=$1", n.ID, encoded); e != nil {
		fail("Cannot publish verified transports")
		return
	}
	j.Status = "completed"
	j.Step = "ready"
	if len(verified) < 3 {
		j.Error = "Some transports failed verification and were not published"
	}
	_ = w.Store.Job(ctx, j)
}
func (w *Worker) Certificate(ctx context.Context, nodeID string, cfg Settings, domain string) (Certificate, error) {
	w.certMu.Lock()
	defer w.certMu.Unlock()
	var crt, key []byte
	var expires time.Time
	e := w.Store.DB.QueryRowContext(ctx, "SELECT tls_cert,tls_key,coalesce(tls_expiry,'epoch') FROM nodes WHERE id=$1", nodeID).Scan(&crt, &key, &expires)
	if e != nil {
		return Certificate{}, e
	}
	if time.Until(expires) > 30*24*time.Hour {
		plain, e := core.Open(w.Store.Key, key)
		return Certificate{Certificate: crt, Key: []byte(plain), Expires: expires}, e
	}
	cert, e := IssueCertificate(ctx, w.Store, cfg, domain)
	if e != nil {
		return cert, e
	}
	sealed, e := core.Seal(w.Store.Key, string(cert.Key))
	if e != nil {
		return cert, e
	}
	_, e = w.Store.DB.ExecContext(ctx, "UPDATE nodes SET tls_cert=$2,tls_key=$3,tls_expiry=$4 WHERE id=$1", nodeID, cert.Certificate, sealed, cert.Expires)
	return cert, e
}

type ArtifactLock struct {
	Version string            `json:"version"`
	SHA256  map[string]string `json:"sha256"`
}

func loadXray(arch string) ([]byte, error) {
	var lock ArtifactLock
	b, e := os.ReadFile(filepath.Join("artifacts", "xray-lock.json"))
	if e != nil {
		return nil, fmt.Errorf("Xray artifact lock missing")
	}
	if e = json.Unmarshal(b, &lock); e != nil {
		return nil, e
	}
	expected := lock.SHA256[arch]
	if len(expected) != 64 {
		return nil, fmt.Errorf("Xray checksum is not pinned")
	}
	b, e = os.ReadFile(filepath.Join("artifacts", "xray-linux-"+arch+".zip"))
	if e != nil {
		return nil, fmt.Errorf("Xray archive missing")
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != expected {
		return nil, fmt.Errorf("Xray archive checksum mismatch")
	}
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		return nil, e
	}
	for _, f := range z.File {
		if f.Name == "xray" {
			if f.UncompressedSize64 > 100<<20 {
				return nil, fmt.Errorf("oversized Xray binary")
			}
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			defer r.Close()
			return io.ReadAll(io.LimitReader(r, 100<<20))
		}
	}
	return nil, fmt.Errorf("Xray archive lacks binary")
}
