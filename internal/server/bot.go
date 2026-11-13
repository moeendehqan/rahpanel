package server

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rahpanel/rahpanel/internal/store"
	"github.com/rahpanel/rahpanel/internal/telegram"
)

func (s *Server) referralSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "PUT" {
		var cfg store.ReferralSettings
		if !decode(w, r, &cfg) {
			return
		}
		if cfg.Threshold < 1 || cfg.Threshold > 1000000 || cfg.Days < 0 || cfg.Days > 3650 || cfg.GiB < 0 || cfg.GiB > 1000000 ||
			(cfg.Enabled && cfg.Days == 0 && cfg.GiB == 0) {
			problem(w, 400, "تعداد دعوت و مقدار پاداش را بررسی کنید.")
			return
		}
		if err := s.Store.SetSetting(r.Context(), store.ReferralSettingsKey, cfg); err != nil {
			problem(w, 500, "Cannot save referral settings")
			return
		}
		s.auditAction(r, "bot.referrals.save", store.ReferralSettingsKey)
		write(w, 200, cfg)
		return
	}
	cfg, err := s.Store.ReferralSettings(r.Context())
	if err != nil {
		problem(w, 500, "Cannot load referral settings")
		return
	}
	write(w, 200, cfg)
}

func (s *Server) botSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	current, err := telegram.LoadSettings(r.Context(), s.Store, telegram.Settings{
		Token: os.Getenv("TELEGRAM_BOT_TOKEN"), Terms: os.Getenv("TELEGRAM_BOT_TERMS"), Support: os.Getenv("TELEGRAM_BOT_SUPPORT"),
	})
	if err != nil {
		problem(w, 500, "Cannot load bot settings")
		return
	}
	if r.Method == "PUT" {
		var input struct {
			telegram.Settings
			RemoveToken bool `json:"remove_token"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Token = strings.TrimSpace(input.Token)
		input.Terms = strings.TrimSpace(input.Terms)
		input.Support = strings.TrimSpace(input.Support)
		if input.RemoveToken {
			input.Token = ""
		} else if input.Token == "" {
			input.Token = current.Token
		}
		current = input.Settings
		if err := current.Validate(); err != nil {
			problem(w, 400, err.Error())
			return
		}
		if err := s.Store.SetSetting(r.Context(), telegram.SettingsKey, current); err != nil {
			problem(w, 500, "Cannot save bot settings")
			return
		}
		s.auditAction(r, "bot.settings.save", telegram.SettingsKey)
	}
	write(w, 200, map[string]any{"has_token": current.Token != "", "terms": current.Terms, "support": current.Support})
}

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
