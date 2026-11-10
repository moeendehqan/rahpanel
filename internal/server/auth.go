package server

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/store"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"sync"
	"time"
)

type adminKey struct{}
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{entries: map[string][]time.Time{}} }
func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.entries) > 10000 {
		for k, v := range l.entries {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > 10*time.Minute {
				delete(l.entries, k)
			}
		}
		if len(l.entries) > 10000 {
			return false
		}
	}
	keep := []time.Time{}
	for _, at := range l.entries[key] {
		if now.Sub(at) < time.Minute {
			keep = append(keep, at)
		}
	}
	if len(keep) >= 5 {
		l.entries[key] = keep
		return false
	}
	l.entries[key] = append(keep, now)
	return true
}
func BootstrapAdmin(ctx context.Context, s *store.Store, username, password string) error {
	var count int
	if e := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM admins").Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return nil
	}
	if username == "" || len(password) < 14 {
		return fmt.Errorf("initial admin username and password (14+ characters) required")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO admins(id,username,password) VALUES($1,$2,$3)", core.ID(), username, hash)
	return e
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.limiter.allow(host) {
		problem(w, 429, "Too many login attempts; retry in one minute")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Password) > 72 || len(body.Username) > 100 {
		problem(w, 401, "Invalid credentials")
		return
	}
	var id string
	var hash []byte
	e := s.Store.DB.QueryRowContext(r.Context(), "SELECT id,password FROM admins WHERE username=$1", body.Username).Scan(&id, &hash)
	if e != nil {
		hash = []byte("$2a$12$94Q9kgCfBMZo72vhG9pY5eMzGhZRw75aHHJ97TJkHU5zhhG3Us1xq")
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(body.Password))
	if e != nil || passwordErr != nil {
		problem(w, 401, "Invalid credentials")
		return
	}
	tx, e := s.Store.DB.BeginTx(r.Context(), nil)
	if e != nil {
		problem(w, 503, "Login unavailable")
		return
	}
	defer tx.Rollback()
	token := core.RandomToken()
	expires := time.Now().Add(8 * time.Hour)
	// Lock the credential row so a concurrent password change can revoke this session.
	result, e := tx.ExecContext(r.Context(), `INSERT INTO sessions(hash,admin_id,expires_at)
		SELECT $1,id,$2 FROM admins WHERE id=$3 AND password=$4 FOR UPDATE`, store.Hash(token), expires, id, hash)
	if e != nil {
		problem(w, 503, "Login unavailable")
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		problem(w, 401, "Invalid credentials")
		return
	}
	if e = tx.Commit(); e != nil {
		problem(w, 503, "Login unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rah_session", Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: expires})
	_ = s.Store.Audit(r.Context(), id, "admin.login", id)
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, e := r.Cookie("rah_session")
		if e != nil {
			problem(w, 401, "Login required")
			return
		}
		var id, role string
		e = s.Store.DB.QueryRowContext(r.Context(), "SELECT a.id,a.role FROM sessions s JOIN admins a ON a.id=s.admin_id WHERE s.hash=$1 AND s.expires_at>now()", store.Hash(cookie.Value)).Scan(&id, &role)
		if e != nil {
			if e != sql.ErrNoRows {
				problem(w, 503, "Authentication unavailable")
			} else {
				problem(w, 401, "Session expired")
			}
			return
		}
		if r.Method != "GET" && role != "admin" {
			problem(w, 403, "Read-only account")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), adminKey{}, id)))
	}
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(adminKey{}).(string)
	var name, role string
	if e := s.Store.DB.QueryRowContext(r.Context(), "SELECT username,role FROM admins WHERE id=$1", id).Scan(&name, &role); e != nil {
		problem(w, 500, "Cannot load account")
		return
	}
	write(w, 200, map[string]string{"username": name, "role": role})
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.New) < 14 || len(body.New) > 72 || body.New == body.Current {
		problem(w, 400, "Choose a different password between 14 and 72 bytes")
		return
	}

	ctx := r.Context()
	id := ctx.Value(adminKey{}).(string)
	if !s.limiter.allow("password:" + id) {
		problem(w, 429, "Too many password change attempts; retry in one minute")
		return
	}
	var oldHash []byte
	if err := s.Store.DB.QueryRowContext(ctx, "SELECT password FROM admins WHERE id=$1", id).Scan(&oldHash); err != nil {
		problem(w, 503, "Password change unavailable")
		return
	}
	if len(body.Current) > 72 || bcrypt.CompareHashAndPassword(oldHash, []byte(body.Current)) != nil {
		problem(w, 400, "Current password is incorrect")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.New), 12)
	if err != nil {
		problem(w, 503, "Password change unavailable")
		return
	}

	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		problem(w, 503, "Password change unavailable")
		return
	}
	defer tx.Rollback()
	cookie, _ := r.Cookie("rah_session")
	sessionHash := store.Hash(cookie.Value)
	result, err := tx.ExecContext(ctx, `UPDATE admins SET password=$1 WHERE id=$2 AND password=$3
		AND EXISTS (SELECT 1 FROM sessions WHERE hash=$4 AND admin_id=$2 AND expires_at>now())`, newHash, id, oldHash, sessionHash)
	if err != nil {
		problem(w, 503, "Password change unavailable")
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		problem(w, 409, "Password or session changed; sign in again and retry")
		return
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE admin_id=$1 AND hash<>$2", id, sessionHash); err != nil {
		problem(w, 503, "Password change unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Password change unavailable")
		return
	}
	s.auditAction(r, "admin.password_change", id)
	write(w, 200, map[string]bool{"ok": true})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("rah_session")
	if cookie != nil {
		_, _ = s.Store.DB.ExecContext(r.Context(), "DELETE FROM sessions WHERE hash=$1", store.Hash(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "rah_session", Value: "", Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	write(w, 200, map[string]bool{"ok": true})
}
