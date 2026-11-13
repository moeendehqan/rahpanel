package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

func TestBotOrderLifecycle(t *testing.T) {
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
	telegramID := time.Now().UnixNano() / 1000
	user := core.User{ID: core.ID(), Name: core.ID(), Enabled: true, ExpiresAt: time.Now().Add(-time.Hour), QuotaBytes: 100, GroupID: "default"}
	must(s.SaveUser(ctx, user, true))
	defer s.DB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
	plan := BotPlan{ID: core.ID(), Name: "Test", Days: 1, QuotaBytes: 200, GroupID: "default", Enabled: true}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO bot_plans VALUES($1,$2,$3,$4,$5,$6,$7)", plan.ID, plan.Name, plan.Days, plan.QuotaBytes, plan.GroupID, plan.Stars, plan.Enabled)
	must(err)
	defer s.DB.ExecContext(ctx, "DELETE FROM bot_plans WHERE id=$1", plan.ID)
	defer s.DB.ExecContext(ctx, "DELETE FROM bot_orders WHERE plan_id=$1", plan.ID)
	var original string
	must(s.DB.QueryRowContext(ctx, "SELECT token FROM users WHERE id=$1", user.ID).Scan(&original))
	stale, err := s.IssueBotCode(ctx, user.ID)
	must(err)
	code, err := s.IssueBotCode(ctx, user.ID)
	must(err)
	if err = s.RedeemBotCode(ctx, telegramID, stale); !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("stale code: %v", err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []int64{telegramID, telegramID + 1} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.RedeemBotCode(ctx, id, code) }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result == nil {
			successes++
		} else if !errors.Is(result, ErrBotUnavailable) {
			t.Fatal(result)
		}
	}
	if successes != 1 {
		t.Fatalf("successful redemptions: %d", successes)
	}
	linked, err := s.BotUser(ctx, telegramID)
	must(err)
	if linked.ID == "" {
		telegramID++
		linked, err = s.BotUser(ctx, telegramID)
		must(err)
	}
	clear, err := s.Reveal(original)
	must(err)
	if linked.Token != clear || linked.QuotaBytes != 100 || !linked.ExpiresAt.Before(time.Now()) {
		t.Fatal("linking modified subscription")
	}
	first, err := s.PrepareBotOrder(ctx, telegramID, plan.ID)
	must(err)
	second, err := s.PrepareBotOrder(ctx, telegramID, plan.ID)
	must(err)
	results = make(chan error, 2)
	for _, o := range []BotOrder{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ProcessBotOrder(ctx, o.ID, telegramID, "free", "", 0, "")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes = 0
	for result := range results {
		if result == nil {
			successes++
		} else if !errors.Is(result, ErrBotUnavailable) {
			t.Fatal(result)
		}
	}
	if successes != 1 {
		t.Fatalf("successful free renewals: %d", successes)
	}
	renewed, err := s.BotUser(ctx, telegramID)
	must(err)
	if renewed.QuotaBytes != 300 || renewed.Token != clear {
		t.Fatalf("incorrect free renewal: %+v", renewed)
	}
	plan.Stars = 10
	must(s.SaveBotPlan(ctx, plan))
	paid, err := s.PrepareBotOrder(ctx, telegramID, plan.ID)
	must(err)
	_, err = s.ProcessBotOrder(ctx, paid.ID, telegramID, "invoice", "", 0, "")
	must(err)
	if _, err = s.ProcessBotOrder(ctx, paid.ID, telegramID, "checkout", "XTR", 9, "wrong"); !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("wrong amount: %v", err)
	}
	if _, err = s.ProcessBotOrder(ctx, paid.ID, telegramID+1, "checkout", "XTR", 10, "wrong"); !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("wrong buyer: %v", err)
	}
	_, err = s.ProcessBotOrder(ctx, paid.ID, telegramID, "checkout", "XTR", 10, "checkout")
	must(err)
	if _, err = s.ProcessBotOrder(ctx, paid.ID, telegramID, "checkout", "XTR", 10, "second-checkout"); !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("duplicate checkout: %v", err)
	}
	for range 2 {
		status, err := s.ProcessBotOrder(ctx, paid.ID, telegramID, "receipt", "XTR", 10, paid.ID)
		if err != nil || status != "fulfilled" {
			t.Fatalf("receipt: %q %v", status, err)
		}
	}
	final, err := s.BotUser(ctx, telegramID)
	must(err)
	if final.QuotaBytes != 500 || !final.ExpiresAt.Equal(renewed.ExpiresAt.AddDate(0, 0, 1)) {
		t.Fatal("receipt applied incorrectly")
	}
	retry, err := s.PrepareBotOrder(ctx, telegramID, plan.ID)
	must(err)
	_, err = s.ProcessBotOrder(ctx, retry.ID, telegramID, "invoice", "", 0, "")
	must(err)
	_, err = s.ProcessBotOrder(ctx, retry.ID, telegramID, "checkout", "XTR", 10, "another-checkout")
	must(err)
	if _, err = s.ProcessBotOrder(ctx, retry.ID, telegramID, "receipt", "XTR", 10, paid.ID); !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("charge reused on another order: %v", err)
	}
	rolledBack, err := s.BotUser(ctx, telegramID)
	must(err)
	if rolledBack.QuotaBytes != final.QuotaBytes || !rolledBack.ExpiresAt.Equal(final.ExpiresAt) {
		t.Fatal("failed order modified subscription")
	}
	plan.Enabled = false
	must(s.SaveBotPlan(ctx, plan))
	if _, err = s.PrepareBotOrder(ctx, telegramID, plan.ID); !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("disabled plan: %v", err)
	}
	plan.Enabled = true
	must(s.SaveBotPlan(ctx, plan))
	_, err = s.DB.ExecContext(ctx, "UPDATE users SET enabled=false WHERE id=$1", user.ID)
	must(err)
	if _, err = s.PrepareBotOrder(ctx, telegramID, plan.ID); !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("disabled user: %v", err)
	}
	plan.Stars = 0
	must(s.SaveBotPlan(ctx, plan))
	newBuyer := telegramID + 2
	purchase, err := s.PrepareBotOrder(ctx, newBuyer, plan.ID)
	must(err)
	_, err = s.ProcessBotOrder(ctx, purchase.ID, newBuyer, "free", "", 0, "")
	must(err)
	created, err := s.BotUser(ctx, newBuyer)
	must(err)
	defer s.DB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", created.ID)
	if created.ID == "" || created.Token == "" || created.QuotaBytes != plan.QuotaBytes || !created.Active(time.Now()) {
		t.Fatal("free purchase did not create an active subscription")
	}
}
