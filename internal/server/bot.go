package server

import (
	"net/http"
	"time"

	"github.com/rahpanel/rahpanel/internal/store"
)

func (s *Server) botPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.Store.BotPlans(r.Context())
	if err != nil {
		problem(w, 500, "Cannot list bot plans")
		return
	}
	write(w, 200, plans)
}

func (s *Server) saveBotPlan(w http.ResponseWriter, r *http.Request) {
	var p store.BotPlan
	if !decode(w, r, &p) {
		return
	}
	p.ID = r.PathValue("id")
	if err := s.Store.SaveBotPlan(r.Context(), p); err != nil {
		problem(w, 400, "Invalid plan or VPN group")
		return
	}
	s.auditAction(r, "bot.plan.save", p.Name)
	write(w, 200, map[string]bool{"ok": true})
}

func (s *Server) botOrders(w http.ResponseWriter, r *http.Request) {
	before := time.Now()
	if raw := r.URL.Query().Get("before"); raw != "" {
		var err error
		before, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			problem(w, 400, "Invalid order cursor")
			return
		}
	}
	orders, err := s.Store.BotOrders(r.Context(), before)
	if err != nil {
		problem(w, 500, "Cannot list bot orders")
		return
	}
	write(w, 200, orders)
}

func (s *Server) botLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("id")
	switch r.Method {
	case "GET":
		linked, err := s.Store.BotLinkStatus(r.Context(), id)
		if err != nil {
			problem(w, 404, "User not found")
			return
		}
		write(w, 200, map[string]string{"telegram_id": linked})
	case "POST":
		code, err := s.Store.IssueBotCode(r.Context(), id)
		if err != nil {
			problem(w, 400, "Cannot issue code")
			return
		}
		s.auditAction(r, "bot.link.issue", id)
		write(w, 200, map[string]string{"code": code})
	case "DELETE":
		if err := s.Store.UnlinkBot(r.Context(), id); err != nil {
			problem(w, 500, "Cannot disconnect Telegram")
			return
		}
		s.auditAction(r, "bot.link.remove", id)
		write(w, 200, map[string]bool{"ok": true})
	}
}
