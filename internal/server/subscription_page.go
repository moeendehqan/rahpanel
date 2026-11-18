package server

import (
	_ "embed"
	"html/template"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/rahpanel/rahpanel/internal/core"
)

const subscriptionPageRoute = "GET /sub/{token}"

//go:embed subscription_page.html
var subscriptionPageHTML string

var subscriptionPage = template.Must(template.New("subscription").Parse(subscriptionPageHTML))

func wantsSubscriptionPage(r *http.Request) bool {
	if r.Pattern != subscriptionPageRoute || r.Method != http.MethodGet ||
		r.Header.Get("Sec-Fetch-Mode") != "navigate" || r.Header.Get("Sec-Fetch-Dest") != "document" {
		return false
	}
	for _, accept := range r.Header.Values("Accept") {
		for _, entry := range strings.Split(accept, ",") {
			mediaType, params, err := mime.ParseMediaType(entry)
			if err != nil || mediaType != "text/html" {
				continue
			}
			if quality, ok := params["q"]; ok {
				q, err := strconv.ParseFloat(quality, 64)
				if err != nil || !(q > 0 && q <= 1) {
					continue
				}
			}
			return true
		}
	}
	return false
}

func (s *Server) renderSubscriptionPage(w http.ResponseWriter, user core.User) {
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		problem(w, 503, "Unavailable")
		return
	}
	expiry := user.ExpiresAt.In(tehran)
	data := struct {
		Name, Used, Quota, Expiry, DateTime, Link, Nonce string
	}{
		Name: user.Name, Used: formatGB(user.UsedBytes) + " گیگابایت",
		Quota: "نامحدود", Expiry: expiry.Format("2006/01/02 · 15:04"),
		DateTime: expiry.Format(time.RFC3339),
		Link:     strings.TrimRight(s.PublicURL, "/") + "/sub/" + user.Token,
		Nonce:    core.RandomToken(),
	}
	if user.QuotaBytes > 0 {
		data.Quota = formatGB(user.QuotaBytes) + " گیگابایت"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+data.Nonce+"'; script-src 'nonce-"+data.Nonce+"'; frame-ancestors 'none'; base-uri 'none'")
	_ = subscriptionPage.Execute(w, data)
}
