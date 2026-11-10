package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/provision"
	"github.com/rahpanel/rahpanel/internal/store"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Server struct {
	Store         *store.Store
	Worker        *provision.Worker
	PublicURL     string
	AgentURL      string
	SecureCookies bool
	WebDir        string
	limiter       *loginLimiter
}

func New(s *store.Store, w *provision.Worker, publicURL, webDir string, secure bool) *Server {
	return &Server{Store: s, Worker: w, PublicURL: publicURL, WebDir: webDir, SecureCookies: secure, limiter: newLoginLimiter()}
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, message string) {
	write(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		problem(w, 400, "Invalid request")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		problem(w, 400, "Unexpected request content")
		return false
	}
	return true
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if e := s.Store.DB.PingContext(r.Context()); e != nil {
			problem(w, 503, "Database unavailable")
			return
		}
		write(w, 200, map[string]string{"service": "rahpanel", "status": "ok"})
	})
	m.HandleFunc("POST /api/v1/login", s.login)
	m.HandleFunc("POST /api/v1/logout", s.authorize(s.logout))
	m.HandleFunc("GET /api/v1/me", s.authorize(s.me))
	m.HandleFunc("PUT /api/v1/me/password", s.authorize(s.changePassword))
	m.HandleFunc("GET /api/v1/users", s.authorize(s.users))
	m.HandleFunc("POST /api/v1/users", s.authorize(s.createUser))
	m.HandleFunc("PUT /api/v1/users/{id}", s.authorize(s.updateUser))
	m.HandleFunc("DELETE /api/v1/users/{id}", s.authorize(s.deleteUser))
	m.HandleFunc("POST /api/v1/users/{id}/rotate", s.authorize(s.rotate))
	m.HandleFunc("POST /api/v1/users/{id}/reset-usage", s.authorize(s.resetUsage))
	m.HandleFunc("GET /api/v1/groups", s.authorize(s.groups))
	m.HandleFunc("POST /api/v1/groups", s.authorize(s.addGroup))
	m.HandleFunc("GET /api/v1/nodes", s.authorize(s.nodes))
	m.HandleFunc("POST /api/v1/nodes", s.authorize(s.addNode))
	m.HandleFunc("PUT /api/v1/nodes/{id}", s.authorize(s.updateNode))
	m.HandleFunc("POST /api/v1/ssh/fingerprint", s.authorize(s.fingerprint))
	m.HandleFunc("POST /api/v1/nodes/{id}/install", s.authorize(s.install))
	m.HandleFunc("GET /api/v1/jobs", s.authorize(s.jobs))
	m.HandleFunc("GET /api/v1/settings", s.authorize(s.settings))
	m.HandleFunc("PUT /api/v1/settings", s.authorize(s.saveSettings))
	m.HandleFunc("GET /api/v1/audit", s.authorize(s.audit))
	m.HandleFunc("GET /sub/{token}", s.subscription)
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/agent/") {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(s.WebDir, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		rel, e := filepath.Rel(s.WebDir, path)
		if e != nil || strings.HasPrefix(rel, "..") {
			http.NotFound(w, r)
			return
		}
		info, e := os.Stat(path)
		if e == nil && !info.IsDir() {
			http.ServeFile(w, r, path)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.WebDir, "index.html"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'self'")
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != s.PublicURL {
				problem(w, 403, "Origin rejected")
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Users(r.Context())
	if e != nil {
		problem(w, 500, "Cannot list users")
		return
	}
	write(w, 200, v)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var u core.User
	if !decode(w, r, &u) {
		return
	}
	u.ID = core.ID()
	if e := s.Store.SaveUser(r.Context(), u, true); e != nil {
		problem(w, 400, "Invalid or duplicate user/group")
		return
	}
	s.auditAction(r, "user.create", u.ID)
	write(w, 201, map[string]string{"id": u.ID})
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var u core.User
	if !decode(w, r, &u) {
		return
	}
	u.ID = r.PathValue("id")
	if e := s.Store.SaveUser(r.Context(), u, false); e != nil {
		problem(w, 400, "Invalid user or group")
		return
	}
	s.auditAction(r, "user.update", u.ID)
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, e := s.Store.DB.ExecContext(r.Context(), "DELETE FROM users WHERE id=$1", id)
	if e != nil {
		problem(w, 500, "Cannot delete user")
		return
	}
	s.auditAction(r, "user.delete", id)
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) rotate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := core.RandomToken()
	protected, e := s.Store.Protect(token)
	if e != nil {
		problem(w, 500, "Cannot protect subscription")
		return
	}
	_, e = s.Store.DB.ExecContext(r.Context(), "UPDATE users SET token=$2 WHERE id=$1", id, protected)
	if e != nil {
		problem(w, 500, "Cannot rotate subscription")
		return
	}
	s.auditAction(r, "subscription.rotate", id)
	write(w, 200, map[string]string{"token": token})
}
func (s *Server) resetUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, e := s.Store.DB.ExecContext(r.Context(), "UPDATE users SET used_bytes=0 WHERE id=$1", id); e != nil {
		problem(w, 500, "Cannot reset usage")
		return
	}
	s.auditAction(r, "usage.reset", id)
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	rows, e := s.Store.DB.QueryContext(r.Context(), "SELECT id,name FROM groups ORDER BY name")
	if e != nil {
		problem(w, 500, "Cannot read groups")
		return
	}
	defer rows.Close()
	out := []core.Group{}
	for rows.Next() {
		var g core.Group
		if e = rows.Scan(&g.ID, &g.Name); e != nil {
			problem(w, 500, "Cannot read groups")
			return
		}
		out = append(out, g)
	}
	write(w, 200, out)
}
func (s *Server) addGroup(w http.ResponseWriter, r *http.Request) {
	var g core.Group
	if !decode(w, r, &g) {
		return
	}
	if strings.TrimSpace(g.Name) == "" || len(g.Name) > 100 {
		problem(w, 400, "Invalid group name")
		return
	}
	g.ID = core.ID()
	if _, e := s.Store.DB.ExecContext(r.Context(), "INSERT INTO groups(id,name) VALUES($1,$2)", g.ID, g.Name); e != nil {
		problem(w, 400, "Duplicate group")
		return
	}
	s.auditAction(r, "group.create", g.ID)
	write(w, 201, g)
}
func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Nodes(r.Context())
	if e != nil {
		problem(w, 500, "Cannot list nodes")
		return
	}
	write(w, 200, v)
}
func (s *Server) addNode(w http.ResponseWriter, r *http.Request) {
	var n core.Node
	if !decode(w, r, &n) {
		return
	}
	var cfg provision.Settings
	if e := s.Store.GetSetting(r.Context(), "provider", &cfg); e != nil || cfg.Validate() != nil {
		problem(w, 400, "Configure provider settings first")
		return
	}
	n.ID = core.ID()
	n.Domain = "n-" + strings.ReplaceAll(n.ID, "-", "")[:12] + "." + cfg.Domain
	n.RealityTarget = cfg.RealityTarget
	n.RealitySNI = cfg.RealitySNI
	n.Enabled = true
	n.Ready = false
	n.PortsLocked = false
	n.Verified = []string{}
	n.Ports = n.EffectivePorts()
	if e := n.Ports.Validate(n.SharedHost); e != nil {
		problem(w, 400, e.Error())
		return
	}
	if n.Name == "" {
		problem(w, 400, "Name required")
		return
	}
	ssh := provision.SSHCredentials{Host: n.Host, Port: 22}
	if _, e := ssh.Address(); e != nil {
		problem(w, 400, "Invalid IP address")
		return
	}
	if e := core.NewNodeKeys(&n); e != nil {
		problem(w, 500, "Cannot generate keys")
		return
	}
	if e := s.Store.AddNode(r.Context(), n); e != nil {
		problem(w, 400, "Duplicate host or invalid group")
		return
	}
	s.auditAction(r, "node.create", n.ID)
	write(w, 201, n)
}
func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	_, e := s.Store.DB.ExecContext(r.Context(), "UPDATE nodes SET enabled=$2 WHERE id=$1", r.PathValue("id"), body.Enabled)
	if e != nil {
		problem(w, 500, "Cannot update node")
		return
	}
	s.auditAction(r, "node.update", r.PathValue("id"))
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) fingerprint(w http.ResponseWriter, r *http.Request) {
	var c struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if !decode(w, r, &c) {
		return
	}
	fp, e := provision.Fingerprint(r.Context(), c.Host, c.Port)
	if e != nil {
		problem(w, 400, e.Error())
		return
	}
	write(w, 200, map[string]string{"fingerprint": fp})
}
func (s *Server) install(w http.ResponseWriter, r *http.Request) {
	n, e := s.Store.Node(r.Context(), r.PathValue("id"))
	if e != nil {
		problem(w, 404, "Node not found")
		return
	}
	var c provision.SSHCredentials
	if !decode(w, r, &c) {
		return
	}
	if c.Host != n.Host || c.Username == "" || c.Fingerprint == "" || (c.Password == "" && c.PrivateKey == "") {
		problem(w, 400, "SSH credentials and confirmed fingerprint required")
		return
	}
	if _, e = c.Address(); e != nil {
		problem(w, 400, e.Error())
		return
	}
	j := core.Job{ID: core.ID(), NodeID: n.ID, Status: "queued", Step: "queued"}
	if c.Ports != nil {
		ports := c.Ports.WithDefaults(n.SharedHost)
		if e = ports.Validate(n.SharedHost); e != nil {
			problem(w, 400, e.Error())
			return
		}
	}
	if e = s.Store.QueueInstall(r.Context(), j, c.Ports); e != nil {
		if errors.Is(e, store.ErrNodePortsLocked) {
			problem(w, 409, e.Error())
			return
		}
		problem(w, 409, "A job is already active for this node")
		return
	}
	select {
	case s.Worker.Queue <- provision.Request{JobID: j.ID, NodeID: n.ID, SSH: c}:
		s.auditAction(r, "node.install", n.ID)
		write(w, 202, j)
	default:
		j.Status = "failed"
		j.Error = "Installation queue full"
		_ = s.Store.Job(r.Context(), j)
		problem(w, 503, "Installation queue full")
	}
}
func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	rows, e := s.Store.DB.QueryContext(r.Context(), "SELECT id,node_id,status,step,error,updated_at FROM jobs ORDER BY updated_at DESC LIMIT 100")
	if e != nil {
		problem(w, 500, "Cannot read jobs")
		return
	}
	defer rows.Close()
	v := []core.Job{}
	for rows.Next() {
		var j core.Job
		if e = rows.Scan(&j.ID, &j.NodeID, &j.Status, &j.Step, &j.Error, &j.UpdatedAt); e != nil {
			problem(w, 500, "Cannot read jobs")
			return
		}
		v = append(v, j)
	}
	write(w, 200, v)
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var v provision.Settings
	e := s.Store.GetSetting(r.Context(), "provider", &v)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		problem(w, 500, "Cannot read settings")
		return
	}
	has := v.CloudflareToken != ""
	// Endpoints come from the installed listeners, not manual provider configuration.
	v.PublicURL = s.PublicURL
	v.AgentURL = s.agentEndpoint()
	if v.RealityTarget == "" {
		v.RealityTarget = "www.microsoft.com:443"
	}
	if v.RealitySNI == "" {
		v.RealitySNI = "www.microsoft.com"
	}
	configured := v.Validate() == nil
	v.CloudflareToken = ""
	write(w, 200, map[string]any{"settings": v, "has_token": has, "configured": configured})
}
func (s *Server) agentEndpoint() string {
	if s.AgentURL != "" {
		return s.AgentURL
	}
	u, e := url.Parse(s.PublicURL)
	if e != nil || u.Hostname() == "" {
		return ""
	}
	return "https://" + net.JoinHostPort(u.Hostname(), "9443")
}
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var v provision.Settings
	if !decode(w, r, &v) {
		return
	}
	v.PublicURL = s.PublicURL
	v.AgentURL = s.agentEndpoint()
	if v.CloudflareToken == "" {
		var old provision.Settings
		if s.Store.GetSetting(r.Context(), "provider", &old) == nil {
			v.CloudflareToken = old.CloudflareToken
		}
	}
	if e := v.Validate(); e != nil {
		problem(w, 400, e.Error())
		return
	}
	if e := s.Store.SetSetting(r.Context(), "provider", v); e != nil {
		problem(w, 500, "Cannot save settings")
		return
	}
	s.auditAction(r, "settings.update", "provider")
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	rows, e := s.Store.DB.QueryContext(r.Context(), "SELECT admin_id,action,target,created_at FROM audit ORDER BY id DESC LIMIT 200")
	if e != nil {
		problem(w, 500, "Cannot read audit log")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var a, action, target string
		var at time.Time
		if e = rows.Scan(&a, &action, &target, &at); e != nil {
			problem(w, 500, "Cannot read audit log")
			return
		}
		out = append(out, map[string]any{"admin_id": a, "action": action, "target": target, "created_at": at})
	}
	write(w, 200, out)
}
func (s *Server) auditAction(r *http.Request, action, target string) {
	id, _ := r.Context().Value(adminKey{}).(string)
	_ = s.Store.Audit(r.Context(), id, action, target)
}
func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	users, e := s.Store.Users(r.Context())
	if e != nil {
		problem(w, 503, "Unavailable")
		return
	}
	var user *core.User
	for i := range users {
		if users[i].Token == r.PathValue("token") {
			user = &users[i]
			break
		}
	}
	if user == nil || !user.Active(time.Now()) {
		problem(w, 404, "Subscription unavailable")
		return
	}
	nodes, e := s.Store.Nodes(r.Context())
	if e != nil {
		problem(w, 503, "Unavailable")
		return
	}
	allowed := []core.Node{}
	creds := []core.Credential{}
	for _, n := range nodes {
		if !n.Enabled || !n.Ready || n.GroupID != user.GroupID || time.Since(n.LastSeen) > 5*time.Minute {
			continue
		}
		cs, e := s.Store.Credentials(r.Context(), n.ID, []core.User{*user})
		if e != nil {
			problem(w, 503, "Unavailable")
			return
		}
		for _, c := range cs {
			if c.UserID == user.ID {
				creds = append(creds, c)
			}
		}
		allowed = append(allowed, n)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Subscription-Userinfo", usageHeader(*user))
	w.Header().Set("Profile-Update-Interval", "1")
	io.WriteString(w, core.Subscription(allowed, creds))
}
func usageHeader(u core.User) string {
	return "upload=0; download=" + itoa(u.UsedBytes) + "; total=" + itoa(u.QuotaBytes) + "; expire=" + itoa(u.ExpiresAt.Unix())
}
func itoa(n int64) string   { return json.Number(strings.TrimSpace(string(mustJSON(n)))).String() }
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func (s *Server) AgentHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /agent/v1/report", s.agentReport)
	m.HandleFunc("GET /agent/v1/snapshot", s.snapshot)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			problem(w, 401, "Client certificate required")
			return
		}
		c := r.TLS.PeerCertificates[0]
		n, e := s.Store.Node(r.Context(), c.Subject.CommonName)
		if e != nil {
			problem(w, 401, "Unknown node")
			return
		}
		var authorized bool
		if e = s.Store.DB.QueryRowContext(r.Context(), "SELECT client_serial=$2 OR (previous_serial=$2 AND previous_until>now()) FROM nodes WHERE id=$1", n.ID, c.SerialNumber.String()).Scan(&authorized); e != nil || !authorized {
			problem(w, 401, "Revoked node identity")
			return
		}
		if !n.Enabled {
			problem(w, 403, "Node disabled")
			return
		}
		m.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nodeKey{}, n)))
	})
}

type nodeKey struct{}

func (s *Server) agentReport(w http.ResponseWriter, r *http.Request) {
	n := r.Context().Value(nodeKey{}).(core.Node)
	var report core.Report
	if !decode(w, r, &report) {
		return
	}
	if e := s.Store.ApplyReport(r.Context(), n.ID, report); e != nil {
		problem(w, 400, "Invalid usage report")
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	n := r.Context().Value(nodeKey{}).(core.Node)
	users, e := s.Store.Users(r.Context())
	if e != nil {
		problem(w, 503, "Cannot build snapshot")
		return
	}
	allowed := []core.User{}
	for _, u := range users {
		if u.GroupID == n.GroupID {
			u.Token = ""
			allowed = append(allowed, u)
		}
	}
	creds, e := s.Store.Credentials(r.Context(), n.ID, allowed)
	if e != nil {
		problem(w, 503, "Cannot issue credentials")
		return
	}
	raw, e := core.BuildConfig(n, allowed, creds, time.Now())
	if e != nil {
		problem(w, 503, "Cannot build configuration")
		return
	}
	var cert, key []byte
	if e = s.Store.DB.QueryRowContext(r.Context(), "SELECT tls_cert,tls_key FROM nodes WHERE id=$1", n.ID).Scan(&cert, &key); e != nil {
		problem(w, 503, "Certificate unavailable")
		return
	}
	plain, e := core.Open(s.Store.Key, key)
	if e != nil {
		problem(w, 503, "Certificate unavailable")
		return
	}
	sum := sha256.Sum256(append(append([]byte{}, raw...), cert...))
	clientCert, clientKey, e := s.refreshNodeIdentity(r, n.ID)
	if e != nil {
		problem(w, 503, "Cannot renew node identity")
		return
	}
	now := time.Now()
	write(w, 200, core.Snapshot{ClientCertificate: clientCert, ClientPrivateKey: clientKey, ServerTime: now, Version: hex.EncodeToString(sum[:]), LeaseUntil: now.Add(5 * time.Minute), Config: raw, Users: allowed, Certificate: cert, PrivateKey: []byte(plain)})
}
func ValidateOrigin(v string) bool {
	u, e := url.Parse(v)
	return e == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && u.Path == ""
}
