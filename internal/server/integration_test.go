package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/provision"
	"github.com/rahpanel/rahpanel/internal/security"
	"github.com/rahpanel/rahpanel/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type cloudflareTestTransport func(*http.Request) (*http.Response, error)

func (f cloudflareTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestAdminLoginUserLifecycleAndSecretRedaction(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated TEST_DATABASE_URL")
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("CREATE SCHEMA IF NOT EXISTS api_tests"); e != nil {
		t.Fatal(e)
	}
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", "api_tests")
	parsed.RawQuery = q.Encode()
	s, e := store.New(context.Background(), parsed.String(), make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if _, e = s.DB.Exec("TRUNCATE users,nodes,jobs,reports,credentials,sessions,admins,settings,audit CASCADE"); e != nil {
		t.Fatal(e)
	}
	if e = BootstrapAdmin(context.Background(), s, "admin", "a-long-test-password"); e != nil {
		t.Fatal(e)
	}
	// Upgrade an existing administrator without resetting its password or retaining the retired seed.
	if _, e = s.DB.Exec("UPDATE admins SET totp=$1,last_totp=42", []byte("legacy-encrypted-seed")); e != nil {
		t.Fatal(e)
	}
	upgraded, e := store.New(context.Background(), parsed.String(), make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	var seedLength, lastCounter int
	e = upgraded.DB.QueryRow("SELECT octet_length(totp),last_totp FROM admins WHERE username='admin'").Scan(&seedLength, &lastCounter)
	upgraded.DB.Close()
	if e != nil || seedLength != 0 || lastCounter != 0 {
		t.Fatal("retired second-factor material survived migration", e)
	}
	ca, e := security.NewAuthority()
	if e != nil {
		t.Fatal(e)
	}
	srv := New(s, provision.NewWorker(s, ca), "https://panel.example.com", "missing", true)
	t.Run("identity rotation survives lost response", func(t *testing.T) {
		certPEM, _, serial, err := ca.Issue("rotation-node", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(certPEM)
		peer, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		peer.NotAfter = time.Now().Add(time.Hour)
		encryptedReality, err := core.Seal(s.Key, "unused")
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.DB.Exec(`INSERT INTO nodes(id,name,host,domain,group_id,reality_target,reality_sni,reality_private,reality_public,short_id,ws_path,client_serial) VALUES('rotation-node','rotation','192.0.2.5','rotation.example.com','default','example.com:443','example.com',$1,'public','abcd','/ws',$2)`, encryptedReality, serial)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("GET", "/agent/v1/snapshot", nil)
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}}
		cert, key, err := srv.refreshNodeIdentity(req, "rotation-node")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tls.X509KeyPair(cert, key); err != nil {
			t.Fatal(err)
		}
		retryCert, retryKey, err := srv.refreshNodeIdentity(req, "rotation-node")
		if err != nil || string(retryCert) != string(cert) || string(retryKey) != string(key) {
			t.Fatal("lost response did not recover same identity", err)
		}
		block, _ = pem.Decode(cert)
		next, _ := x509.ParseCertificate(block.Bytes)
		req.TLS.PeerCertificates = []*x509.Certificate{next}
		cert, key, err = srv.refreshNodeIdentity(req, "rotation-node")
		if err != nil || len(cert) != 0 || len(key) != 0 {
			t.Fatal("current identity unexpectedly rotated", err)
		}
	})
	handler := srv.Handler()
	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", "https://panel.example.com")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if request("POST", "/api/v1/login", `{"username":"admin","password":"wrong-password"}`, nil).Code != 401 {
		t.Fatal("wrong password accepted")
	}
	if request("POST", "/api/v1/login", `{"username":"missing","password":"a-long-test-password"}`, nil).Code != 401 {
		t.Fatal("unknown account accepted")
	}
	login := `{"username":"admin","password":"a-long-test-password"}`
	response := request("POST", "/api/v1/login", login, nil)
	if response.Code != 200 {
		t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	cookie := response.Result().Cookies()[0]
	setup := request("GET", "/api/v1/settings", "", cookie)
	var initial struct {
		Settings   provision.Settings `json:"settings"`
		Configured bool               `json:"configured"`
	}
	if e = json.Unmarshal(setup.Body.Bytes(), &initial); e != nil {
		t.Fatal(e)
	}
	if initial.Configured || initial.Settings.PublicURL != "https://panel.example.com" || initial.Settings.AgentURL != "https://panel.example.com:9443" {
		t.Fatalf("fresh setup must expose installed endpoints without manual configuration: %+v", initial)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie lacks protections")
	}
	again := request("POST", "/api/v1/login", login, nil)
	if again.Code != 200 {
		t.Fatal("password login unexpectedly requires a second factor")
	}
	user := core.User{Name: "alice", Enabled: true, ExpiresAt: time.Now().Add(time.Hour), QuotaBytes: 10000, GroupID: "default"}
	raw, _ := json.Marshal(user)
	res := request("POST", "/api/v1/users", string(raw), cookie)
	if res.Code != 201 {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var created map[string]string
	json.Unmarshal(res.Body.Bytes(), &created)
	user.ID = created["id"]
	user.Enabled = false
	raw, _ = json.Marshal(user)
	if res = request("PUT", "/api/v1/users/"+user.ID, string(raw), cookie); res.Code != 200 {
		t.Fatalf("suspend: %s", res.Body)
	}
	all, e := s.Users(context.Background())
	if e != nil || len(all) != 1 || all[0].Enabled {
		t.Fatal("suspension not persisted")
	}
	settings := provision.Settings{Domain: "example.com", ZoneID: "zone", CloudflareToken: "cf-private-secret", Email: "admin@example.com", PublicURL: "https://panel.example.com", AgentURL: "https://panel.example.com:9443", RealityTarget: "example.com:443", RealitySNI: "example.com"}
	raw, _ = json.Marshal(settings)
	if res = request("PUT", "/api/v1/settings", string(raw), cookie); res.Code != 200 {
		t.Fatalf("settings save: %s", res.Body)
	}
	res = request("GET", "/api/v1/settings", "", cookie)
	if res.Code != 200 || strings.Contains(res.Body.String(), "cf-private-secret") {
		t.Fatal("provider secret exposed")
	}
	if !strings.Contains(res.Body.String(), `"configured":true`) {
		t.Fatal("completed setup not reported")
	}
	t.Run("Cloudflare test uses form inputs without saving", func(t *testing.T) {
		const path = "/api/v1/settings/cloudflare/test"
		const body = `{"domain":"draft.example.com","zone_id":"draft-zone","cloudflare_token":"new-token"}`
		if got := request("POST", path, body, nil); got.Code != 401 {
			t.Fatalf("guest got %d: %s", got.Code, got.Body)
		}
		if _, err := s.DB.Exec("UPDATE admins SET role='viewer' WHERE username='admin'"); err != nil {
			t.Fatal(err)
		}
		viewerResponse := request("POST", path, body, cookie)
		if _, err := s.DB.Exec("UPDATE admins SET role='admin' WHERE username='admin'"); err != nil {
			t.Fatal(err)
		}
		if viewerResponse.Code != 403 {
			t.Fatalf("viewer got %d: %s", viewerResponse.Code, viewerResponse.Body)
		}
		if got := request("POST", path, `{"domain":"bad domain","zone_id":"draft-zone"}`, cookie); got.Code != 400 {
			t.Fatalf("invalid domain got %d: %s", got.Code, got.Body)
		}

		transport := http.DefaultTransport
		t.Cleanup(func() { http.DefaultTransport = transport })
		for _, token := range []string{"new-token", settings.CloudflareToken} {
			var calls int
			http.DefaultTransport = cloudflareTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("unexpected Cloudflare credential")
				}
				if !strings.HasPrefix(r.URL.Path, "/client/v4/zones/draft-zone") {
					t.Errorf("unexpected zone: %s", r.URL.Path)
				}
				payload := `{"success":true,"result":{"name":"example.com","id":"probe-id"}}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})
			testBody := body
			if token == settings.CloudflareToken {
				testBody = strings.Replace(body, "new-token", "", 1)
			}
			got := request("POST", path, testBody, cookie)
			if got.Code != 200 || strings.TrimSpace(got.Body.String()) != `{"ok":true}` || calls != 3 {
				t.Fatalf("Cloudflare test: %d %s (%d calls)", got.Code, got.Body, calls)
			}
		}
		var saved provision.Settings
		if err := s.GetSetting(context.Background(), "provider", &saved); err != nil || saved != settings {
			t.Fatal("Cloudflare test changed stored settings", err)
		}
	})
	t.Run("shared host creation and installation port locking", func(t *testing.T) {
		res := request("POST", "/api/v1/nodes", `{"name":"panel node","host":"192.0.2.10","group_id":"default","shared_host":true}`, cookie)
		if res.Code != 201 {
			t.Fatalf("create shared node: %d %s", res.Code, res.Body)
		}
		var n core.Node
		if err := json.Unmarshal(res.Body.Bytes(), &n); err != nil {
			t.Fatal(err)
		}
		if !n.SharedHost || n.Ports.Reality != 2087 || n.Ports.Stats != 10086 {
			t.Fatalf("shared defaults missing: %+v", n.Ports)
		}
		if request("POST", "/api/v1/nodes", `{"name":"bad shared node","host":"192.0.2.11","group_id":"default","shared_host":true,"ports":{"reality":443}}`, cookie).Code != 400 {
			t.Fatal("reserved panel listener accepted")
		}
		p := n.Ports
		p.Reality = 12087
		job := core.Job{ID: core.ID(), NodeID: n.ID, Status: "queued", Step: "queued"}
		if err := s.QueueInstall(context.Background(), job, &p); err != nil {
			t.Fatal(err)
		}
		other := p
		other.Reality = 22087
		if err := s.QueueInstall(context.Background(), core.Job{ID: core.ID(), NodeID: n.ID, Status: "queued", Step: "queued"}, &other); err == nil {
			t.Fatal("concurrent active installation accepted")
		}
		got, err := s.Node(context.Background(), n.ID)
		if err != nil || got.Ports != p {
			t.Fatal("conflicting job changed persisted ports", err)
		}
		job.Status = "completed"
		if err = s.Job(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec("UPDATE nodes SET client_serial='active-identity' WHERE id=$1", n.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.QueueInstall(context.Background(), core.Job{ID: core.ID(), NodeID: n.ID, Status: "queued", Step: "queued"}, &other); err != store.ErrNodePortsLocked {
			t.Fatal("failed verification allowed deployed agent port edits", err)
		}
		got, err = s.Node(context.Background(), n.ID)
		if err != nil || !got.PortsLocked || got.Ports != p {
			t.Fatal("deployed port lock not exposed", err)
		}
		if _, err = s.DB.Exec("UPDATE nodes SET ready=true WHERE id=$1", n.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.QueueInstall(context.Background(), core.Job{ID: core.ID(), NodeID: n.ID, Status: "queued", Step: "queued"}, &other); err != store.ErrNodePortsLocked {
			t.Fatal("ready node ports changed", err)
		}
	})
	t.Run("change admin password", func(t *testing.T) {
		const path = "/api/v1/me/password"
		const newPassword = "123456"
		const change = `{"current_password":"a-long-test-password","new_password":"` + newPassword + `"}`
		if w := request("PUT", path, change, nil); w.Code != 401 {
			t.Fatalf("unauthenticated password change: %d", w.Code)
		}
		short := strings.Replace(change, newPassword, "12345", 1)
		if w := request("PUT", path, short, cookie); w.Code != 400 {
			t.Fatalf("five-digit password: %d", w.Code)
		}
		wrong := strings.Replace(change, "a-long-test-password", "incorrect", 1)
		if w := request("PUT", path, wrong, cookie); w.Code != 400 {
			t.Fatalf("wrong current password: %d", w.Code)
		}
		if w := request("PUT", path, change, cookie); w.Code != 200 {
			t.Fatalf("password change: %d %s", w.Code, w.Body)
		}
		if w := request("GET", "/api/v1/me", "", again.Result().Cookies()[0]); w.Code != 401 {
			t.Fatalf("previous session still valid: %d", w.Code)
		}
		if w := request("GET", "/api/v1/me", "", cookie); w.Code != 200 {
			t.Fatalf("current session lost: %d", w.Code)
		}
		srv.limiter = newLoginLimiter()
		if w := request("POST", "/api/v1/login", login, nil); w.Code != 401 {
			t.Fatalf("old password login: %d", w.Code)
		}
		newLogin := strings.Replace(login, "a-long-test-password", newPassword, 1)
		if w := request("POST", "/api/v1/login", newLogin, nil); w.Code != 200 {
			t.Fatalf("new password login: %d %s", w.Code, w.Body)
		}
	})
	response = request("POST", "/api/v1/logout", "", cookie)
	if response.Code != 200 {
		t.Fatal("logout failed")
	}
	if request("GET", "/api/v1/users", "", cookie).Code != 401 {
		t.Fatal("logged out session still authorized")
	}
}
