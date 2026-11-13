package telegram

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"regexp"
	"time"

	"github.com/rahpanel/rahpanel/internal/store"
)

const SettingsKey = "telegram-bot"

type Settings struct {
	Token   string `json:"token"`
	Terms   string `json:"terms"`
	Support string `json:"support"`
}

var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,128}$`)

func (s Settings) Validate() error {
	if s.Token != "" && (!tokenPattern.MatchString(s.Token) || s.Terms == "" || s.Support == "") {
		return errors.New("توکن BotFather، شرایط خرید و راه تماس پشتیبانی را بررسی کنید.")
	}
	if len([]rune(s.Terms)) > 3500 || len([]rune(s.Support)) > 3500 {
		return errors.New("شرایط خرید و راه تماس پشتیبانی هرکدام حداکثر ۳۵۰۰ نویسه باشند.")
	}
	return nil
}

func LoadSettings(ctx context.Context, s *store.Store, fallback Settings) (Settings, error) {
	var settings Settings
	err := s.GetSetting(ctx, SettingsKey, &settings)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	return settings, err
}

func WatchSettings(ctx context.Context, s *store.Store, public string, test bool, fallback Settings) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	var active Settings
	stop := func() {}
	defer func() { stop() }()
	for ctx.Err() == nil {
		next, err := LoadSettings(ctx, s, fallback)
		if err != nil {
			log.Print("Cannot read Telegram bot settings")
		} else if next != active {
			stop()
			stop = func() {}
			active = next
			if next.Token != "" && next.Validate() == nil {
				botCtx, cancel := context.WithCancel(ctx)
				done := make(chan struct{})
				stop = func() {
					cancel()
					<-done
				}
				bot := Bot{Store: s, Token: next.Token, PublicURL: public, Terms: next.Terms, Support: next.Support, Test: test}
				go func(finished chan struct{}) {
					defer close(finished)
					bot.Run(botCtx)
				}(done)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
