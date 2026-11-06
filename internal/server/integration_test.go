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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

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
		_, err = s.DB.Exec(`INSERT INTO nodes(id,name,host,domain,group_id,reality_target,reality_sni,reality_private,reality_public,short_id,ws_path,client_serial) VALUES('rotation-node','rotation','192.0.2.5','rotation.example.com','default','example.com:443','example.com',$1,'public','abcd','/ws',$2)`, []byte("unused"), serial)
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
	if again := request("POST", "/api/v1/login", login, nil); again.Code != 200 {
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
	response = request("POST", "/api/v1/logout", "", cookie)
	if response.Code != 200 {
		t.Fatal("logout failed")
	}
	if request("GET", "/api/v1/users", "", cookie).Code != 401 {
		t.Fatal("logged out session still authorized")
	}
}
