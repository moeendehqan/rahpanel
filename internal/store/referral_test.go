package store

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

func TestReferralLifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated TEST_DATABASE_URL")
	}
	ctx := context.Background()
	s, err := New(ctx, dsn, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UnixNano() / 1000
	defer func() {
		s.DB.Exec("DELETE FROM bot_referral_rewards WHERE inviter=$1", base)
		s.DB.Exec("DELETE FROM bot_referral_users WHERE telegram_id>$1 AND telegram_id<$1+20", base)
		s.DB.Exec("DELETE FROM bot_referral_users WHERE telegram_id=$1", base)
		s.DB.Exec("DELETE FROM settings WHERE id=$1", ReferralSettingsKey)
	}()
	cfg := ReferralSettings{Enabled: true, Threshold: 3, Days: 2, GiB: 5}
	must(s.SetSetting(ctx, ReferralSettingsKey, cfg))
	p, err := s.ReferralProgress(ctx, base)
	must(err)
	must(s.RecordBotStart(ctx, base, p.Code))
	must(s.RecordBotStart(ctx, base+1, ""))
	must(s.RecordBotStart(ctx, base+1, p.Code))
	must(s.RecordBotStart(ctx, base+2, "bad-code"))
	must(s.RecordBotStart(ctx, base+2, p.Code))
	var wg sync.WaitGroup
	for i := int64(3); i < 9; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RecordBotStart(ctx, base+i, p.Code); err != nil {
				t.Error(err)
			}
			if err := s.RecordBotStart(ctx, base+i, p.Code); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	p, err = s.ReferralProgress(ctx, base)
	must(err)
	if p.Total != 6 || p.Unused != 0 || p.PendingDays != 4 || p.PendingGiB != 10 {
		t.Fatalf("unexpected progress: %+v", p)
	}
	var rewards int
	must(s.DB.QueryRow("SELECT count(*) FROM bot_referral_rewards WHERE inviter=$1", base).Scan(&rewards))
	if rewards != 2 {
		t.Fatalf("reward count: %d", rewards)
	}
	u := core.User{ID: core.ID(), Name: core.ID(), GroupID: "default", ExpiresAt: time.Now().Add(-time.Hour), QuotaBytes: 1}
	must(s.SaveUser(ctx, u, true))
	defer s.DB.Exec("DELETE FROM users WHERE id=$1", u.ID)
	code, err := s.IssueBotCode(ctx, u.ID)
	must(err)
	must(s.RedeemBotCode(ctx, base, code))
	must(s.RecordBotStart(ctx, base+9, p.Code))
	must(s.ApplyPendingReferrals(ctx))
	p, err = s.ReferralProgress(ctx, base)
	must(err)
	if p.PendingDays != 4 || p.Unused != 1 {
		t.Fatalf("disabled account: %+v", p)
	}
	u.Enabled = true
	must(s.SaveUser(ctx, u, false))
	must(s.ApplyPendingReferrals(ctx))
	must(s.ApplyPendingReferrals(ctx))
	current, err := s.BotUser(ctx, base)
	must(err)
	if current.QuotaBytes != 1+(10<<30) || time.Until(current.ExpiresAt) < 95*time.Hour || time.Until(current.ExpiresAt) > 96*time.Hour {
		t.Fatalf("unexpected subscription reward: %+v", current)
	}
	cfg.Threshold = 2
	cfg.Days = 0
	must(s.SetSetting(ctx, ReferralSettingsKey, cfg))
	u.QuotaBytes = 0
	must(s.SaveUser(ctx, u, false))
	must(s.RecordBotStart(ctx, base+10, p.Code))
	p, err = s.ReferralProgress(ctx, base)
	must(err)
	current, err = s.BotUser(ctx, base)
	must(err)
	if p.Total != 8 || p.Unused != 0 || p.PendingGiB != 0 || current.QuotaBytes != 0 {
		t.Fatalf("new settings: %+v, quota %d", p, current.QuotaBytes)
	}
}
