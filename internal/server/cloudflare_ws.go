package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"
)

func (s *Server) cloudflareWS(w http.ResponseWriter, r *http.Request) {
	if s.Edge == nil {
		problem(w, 503, "مدیریت Caddy در این استقرار فعال نیست")
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		problem(w, 400, "وضعیت فعال‌سازی را مشخص کنید")
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		problem(w, 503, "ثبت تغییر ناموفق بود")
		return
	}
	defer tx.Rollback()
	var enabled, ready bool
	var seen sql.NullTime
	var verified string
	var requested sql.NullBool
	err = tx.QueryRowContext(ctx, "SELECT enabled,ready,last_seen,verified::text,cloudflare_ws FROM nodes WHERE id=$1 FOR UPDATE", id).
		Scan(&enabled, &ready, &seen, &verified, &requested)
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "نود پیدا نشد")
		return
	}
	if err != nil {
		problem(w, 503, "خواندن وضعیت نود ناموفق بود")
		return
	}
	var routes []string
	if err := json.Unmarshal([]byte(verified), &routes); err != nil {
		problem(w, 503, "خواندن مسیرهای نود ناموفق بود")
		return
	}
	if *body.Enabled && (!enabled || !ready || !seen.Valid || time.Since(seen.Time) > 5*time.Minute || !slices.Contains(routes, "websocket")) {
		problem(w, 409, "مسیر WebSocket مستقیم نود باید فعال و تأیید شده باشد")
		return
	}
	if *body.Enabled || requested.Valid {
		if _, err := tx.ExecContext(ctx, "UPDATE nodes SET cloudflare_ws=$2 WHERE id=$1", id, *body.Enabled); err != nil {
			problem(w, 503, "ثبت تغییر ناموفق بود")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		problem(w, 503, "ثبت تغییر ناموفق بود")
		return
	}
	s.Edge.Wake(id)
	s.auditAction(r, "node.cloudflare_ws", id)
	write(w, 202, map[string]bool{"enabled": *body.Enabled})
}
