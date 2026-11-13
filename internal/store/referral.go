package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

const ReferralSettingsKey = "telegram-referrals"

type ReferralSettings struct {
	Enabled   bool `json:"enabled"`
	Threshold int  `json:"threshold"`
	Days      int  `json:"days"`
	GiB       int  `json:"gib"`
}

func (s *Store) ReferralSettings(ctx context.Context) (ReferralSettings, error) {
	cfg := ReferralSettings{Threshold: 3}
	err := s.GetSetting(ctx, ReferralSettingsKey, &cfg)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return cfg, err
}

type ReferralProgress struct {
	Code                                   string
	Total, Unused, PendingDays, PendingGiB int64
}

func (s *Store) RecordBotStart(ctx context.Context, id int64, code string) error {
	var cfg ReferralSettings
	if code != "" {
		var err error
		cfg, err = s.ReferralSettings(ctx)
		if err != nil {
			return err
		}
	}
	tx, err := botTx(ctx, s.DB, id)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var inserted int64
	err = tx.QueryRowContext(ctx, `INSERT INTO bot_referral_users(telegram_id,code,started) VALUES($1,$2,true)
 ON CONFLICT(telegram_id) DO UPDATE SET started=true WHERE NOT bot_referral_users.started
 RETURNING telegram_id`, id, core.RandomToken()).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return tx.Commit()
	}
	var inviter int64
	var unused int64
	err = tx.QueryRowContext(ctx, `SELECT telegram_id,unused FROM bot_referral_users
 WHERE code=$2 AND telegram_id<>$1
 AND NOT EXISTS(SELECT 1 FROM bot_links WHERE telegram_id=$1)
 AND NOT EXISTS(SELECT 1 FROM bot_orders WHERE telegram_id=$1)
 FOR UPDATE`, id, code).Scan(&inviter, &unused)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE bot_referral_users SET inviter=$2 WHERE telegram_id=$1", id, inviter); err != nil {
		return err
	}
	unused++
	count := unused / int64(cfg.Threshold)
	if _, err = tx.ExecContext(ctx, `UPDATE bot_referral_users SET total=total+1,unused=$2 WHERE telegram_id=$1`, inviter, unused%int64(cfg.Threshold)); err != nil {
		return err
	}
	if count > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO bot_referral_rewards(invitee,inviter,days,gib)
  VALUES($1,$2,$3,$4)`, id, inviter, count*int64(cfg.Days), count*int64(cfg.GiB)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if count > 0 {
		return s.ApplyReferralRewards(ctx, inviter)
	}
	return nil
}

func (s *Store) ReferralProgress(ctx context.Context, id int64) (ReferralProgress, error) {
	var p ReferralProgress
	_, err := s.DB.ExecContext(ctx, `INSERT INTO bot_referral_users(telegram_id,code) VALUES($1,$2) ON CONFLICT(telegram_id) DO NOTHING`, id, core.RandomToken())
	if err != nil {
		return p, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT u.code,u.total,u.unused,coalesce(sum(r.days),0),coalesce(sum(r.gib),0)
 FROM bot_referral_users u LEFT JOIN bot_referral_rewards r ON r.inviter=u.telegram_id AND r.applied_at IS NULL
 WHERE u.telegram_id=$1 GROUP BY u.telegram_id`, id).Scan(&p.Code, &p.Total, &p.Unused, &p.PendingDays, &p.PendingGiB)
	return p, err
}

func (s *Store) ApplyReferralRewards(ctx context.Context, id int64) error {
	tx, err := botTx(ctx, s.DB, id)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	u, err := botUser(ctx, tx, id, " FOR UPDATE OF l,u")
	if err != nil || !u.Enabled {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH claimed AS (
 UPDATE bot_referral_rewards SET applied_at=now() WHERE inviter=$1 AND applied_at IS NULL RETURNING days,gib
 ), totals AS (SELECT sum(days) days,sum(gib) gib FROM claimed)
 UPDATE users SET
 expires_at=CASE WHEN totals.days>0 THEN greatest(expires_at,now())+totals.days::double precision*interval '24 hours' ELSE expires_at END,
 quota_bytes=CASE WHEN quota_bytes=0 THEN 0 ELSE least(9223372036854775807,quota_bytes::numeric+totals.gib*1073741824)::bigint END
 FROM totals WHERE id=$2 AND totals.days IS NOT NULL`, id, u.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ApplyPendingReferrals(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT r.inviter FROM bot_referral_rewards r
 JOIN bot_links l ON l.telegram_id=r.inviter JOIN users u ON u.id=l.user_id
 WHERE r.applied_at IS NULL AND u.enabled`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var result error
	for _, id := range ids {
		if err = s.ApplyReferralRewards(ctx, id); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (s *Store) WatchReferralRewards(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.ApplyPendingReferrals(ctx); err != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
