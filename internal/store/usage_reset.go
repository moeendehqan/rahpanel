package store

import (
	"context"
	"database/sql"
	"log"
	"time"
)

const resetReady = `EXISTS(SELECT 1 FROM nodes WHERE enabled AND group_id=users.group_id)
 AND NOT EXISTS(SELECT 1 FROM nodes WHERE enabled AND group_id=users.group_id
 AND (accounting_sample_at IS NULL OR accounting_sample_at < now()-interval '2 minutes'))`

const resetDue = `UPDATE users SET used_bytes=CASE WHEN last_reset_at >= next_reset_at+floor(extract(epoch FROM (now()-next_reset_at))/(86400.0*reset_days))*reset_days*interval '24 hours' THEN used_bytes ELSE 0 END,
 last_reset_at=greatest(last_reset_at,next_reset_at+floor(extract(epoch FROM (now()-next_reset_at))/(86400.0*reset_days))*reset_days*interval '24 hours'),
 next_reset_at=next_reset_at+(1+floor(extract(epoch FROM (now()-next_reset_at))/(86400.0*reset_days)))*reset_days*interval '24 hours'
 WHERE reset_days>0 AND next_reset_at<=now() AND ` + resetReady

func (s *Store) MaintainUsage(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.DB.ExecContext(ctx, resetDue+" AND id IN (SELECT id FROM users WHERE reset_days>0 AND next_reset_at<=now() AND "+resetReady+" ORDER BY next_reset_at LIMIT 100 FOR UPDATE SKIP LOCKED)"); err != nil && ctx.Err() == nil {
			log.Print("Cannot apply scheduled quota resets: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) ResetPolicy(ctx context.Context, id string) (int, *time.Time, bool, error) {
	var days int
	var next *time.Time
	var ready bool
	err := s.DB.QueryRowContext(ctx, "SELECT reset_days,next_reset_at,("+resetReady+") FROM users WHERE id=$1", id).Scan(&days, &next, &ready)
	return days, next, ready, err
}

func (s *Store) SetResetPolicy(ctx context.Context, id string, days int) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE users SET reset_days=$2,
 next_reset_at=CASE WHEN $2=0 THEN NULL ELSE now()+$2*interval '24 hours' END
 WHERE id=$1 AND ($2=0 OR (`+resetReady+`))`, id, days)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func applyDueReset(ctx context.Context, tx *sql.Tx, id string) error {
	var locked string
	err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", id).Scan(&locked)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, resetDue+" AND id=$1", id)
	return err
}
