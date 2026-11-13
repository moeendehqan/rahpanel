package server

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/store"
	"github.com/skip2/go-qrcode"
)

func (s *Server) accountLink(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if !decode(w, r, &input) {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	var token string
	var result sql.Result
	var err error
	switch input.Action {
	case "issue":
		token = core.RandomToken()
		result, err = s.Store.DB.ExecContext(ctx, "UPDATE users SET account_token_hash=$2 WHERE id=$1", id, store.Hash(token))
	case "revoke":
		result, err = s.Store.DB.ExecContext(ctx, "UPDATE users SET account_token_hash=NULL WHERE id=$1", id)
	case "revoke-both":
		protected, e := s.Store.Protect(core.RandomToken())
		if e != nil {
			problem(w, 500, "Cannot protect subscription")
			return
		}
		result, err = s.Store.DB.ExecContext(ctx, "UPDATE users SET account_token_hash=NULL,token=$2 WHERE id=$1", id, protected)
	default:
		problem(w, 400, "Invalid action")
		return
	}
	if err != nil {
		problem(w, 503, "Cannot change account link")
		return
	}
	if count, _ := result.RowsAffected(); count != 1 {
		problem(w, 404, "Unknown user")
		return
	}
	s.auditAction(r, "account."+input.Action, id)
	w.Header().Set("Cache-Control", "no-store")
	link := ""
	if token != "" {
		link = "/account/" + token
	}
	write(w, 200, map[string]string{"link": link})
}

var accountTemplate = template.Must(template.New("account").Parse(`<!doctype html>
<html lang="fa" dir="rtl"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>اشتراک راه</title>
<main><h1>اشتراک {{.User.Name}}</h1>
<dl><dt>وضعیت</dt><dd>{{.Status}}</dd><dt>مصرف</dt><dd>{{.Used}} گیگابایت</dd>
<dt>سهمیه</dt><dd>{{.Quota}}</dd><dt>انقضا</dt><dd>{{.Expiry}}</dd></dl>
{{if .Link}}{{if .QR}}<img width="192" height="192" alt="کد QR اشتراک" src="{{.QR}}">{{end}}<p>لینک را کپی کنید و در بخش اشتراک کلاینت وارد کنید.</p>
<input dir="ltr" aria-label="لینک اشتراک" size="40" readonly value="{{.Link}}"><label>اشتراک Mihomo <input dir="ltr" readonly value="{{.Link}}/clash"></label>{{end}}</main></html>`))

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; frame-ancestors 'none'; base-uri 'none'")
	var u core.User
	var protected string
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT name,enabled,expires_at,quota_bytes,used_bytes,token FROM users WHERE account_token_hash=$1`, store.Hash(r.PathValue("token"))).Scan(&u.Name, &u.Enabled, &u.ExpiresAt, &u.QuotaBytes, &u.UsedBytes, &protected)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		problem(w, 503, "Unavailable")
		return
	}
	data := struct {
		User                              core.User
		Status, Link, Used, Quota, Expiry string
		QR                                template.URL
	}{User: u, Status: "سهمیه تمام شده", Used: formatGB(u.UsedBytes), Quota: "نامحدود", Expiry: u.ExpiresAt.UTC().Format(time.RFC3339)}
	if u.QuotaBytes > 0 {
		data.Quota = formatGB(u.QuotaBytes) + " گیگابایت"
	}
	now := time.Now()
	switch {
	case !u.Enabled:
		data.Status = "تعلیق‌شده"
	case !now.Before(u.ExpiresAt):
		data.Status = "منقضی"
	case u.Active(now):
		token, e := s.Store.Reveal(protected)
		if e != nil {
			problem(w, 503, "Unavailable")
			return
		}
		data.Status = "فعال"
		data.Link = strings.TrimRight(s.PublicURL, "/") + "/sub/" + token
		if png, err := qrcode.Encode(data.Link, qrcode.Medium, 192); err == nil {
			data.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = accountTemplate.Execute(w, data)
}

func formatGB(bytes int64) string { return fmt.Sprintf("%.2f", float64(bytes)/(1<<30)) }
