package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rahpanel/rahpanel/internal/core"
)

var ErrBotUnavailable = errors.New("telegram subscription operation unavailable")

type BotPlan struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Days       int    `json:"days"`
	QuotaBytes int64  `json:"quota_bytes"`
	GroupID    string `json:"group_id"`
	Stars      int    `json:"stars"`
	Enabled    bool   `json:"enabled"`
}

type BotOrder struct {
	ID         string    `json:"id"`
	TelegramID int64     `json:"telegram_id"`
	UserID     string    `json:"user_id"`
	UserName   string    `json:"user_name"`
	Plan       BotPlan   `json:"plan"`
	Status     string    `json:"status"`
	ChargeID   string    `json:"charge_id"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) BotPlans(ctx context.Context) ([]BotPlan, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,days,quota_bytes,group_id,stars,enabled FROM bot_plans ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := []BotPlan{}
	for rows.Next() {
		var p BotPlan
		if err := rows.Scan(&p.ID, &p.Name, &p.Days, &p.QuotaBytes, &p.GroupID, &p.Stars, &p.Enabled); err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

func (s *Store) SaveBotPlan(ctx context.Context, p BotPlan) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 100 || p.Days < 1 || p.Days > 3650 || p.Stars < 0 || p.Stars > math.MaxInt32 || p.QuotaBytes < 0 {
		return ErrBotUnavailable
	}
	if p.ID == "" {
		_, err := s.DB.ExecContext(ctx, "INSERT INTO bot_plans VALUES($1,$2,$3,$4,$5,$6,$7)", core.ID(), p.Name, p.Days, p.QuotaBytes, p.GroupID, p.Stars, p.Enabled)
		return err
	}
	var id string
	return s.DB.QueryRowContext(ctx, "UPDATE bot_plans SET name=$2,days=$3,quota_bytes=$4,group_id=$5,stars=$6,enabled=$7 WHERE id=$1 RETURNING id", p.ID, p.Name, p.Days, p.QuotaBytes, p.GroupID, p.Stars, p.Enabled).Scan(&id)
}

func (s *Store) BotOrders(ctx context.Context, before time.Time) ([]BotOrder, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT o.id,o.telegram_id,coalesce(o.user_id,''),coalesce(u.name,''),o.plan_name,o.stars,o.status,coalesce(o.charge_id,''),o.created_at
 FROM bot_orders o LEFT JOIN users u ON u.id=o.user_id WHERE o.created_at<$1 ORDER BY o.created_at DESC LIMIT 100`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := []BotOrder{}
	for rows.Next() {
		var o BotOrder
		if err := rows.Scan(&o.ID, &o.TelegramID, &o.UserID, &o.UserName, &o.Plan.Name, &o.Plan.Stars, &o.Status, &o.ChargeID, &o.CreatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (s *Store) BotLinkStatus(ctx context.Context, id string) (string, error) {
	var linked string
	err := s.DB.QueryRowContext(ctx, `SELECT coalesce(l.telegram_id::text,'') FROM users u LEFT JOIN bot_links l ON u.id=l.user_id WHERE u.id=$1`, id).Scan(&linked)
	return linked, err
}

func (s *Store) IssueBotCode(ctx context.Context, id string) (string, error) {
	code := core.RandomToken()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO bot_codes VALUES($1,$2,now()+interval '15 minutes')
 ON CONFLICT(user_id) DO UPDATE SET hash=excluded.hash,expires_at=excluded.expires_at`, id, Hash(code))
	return code, err
}

func (s *Store) UnlinkBot(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM bot_codes WHERE user_id=$1", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM bot_links WHERE user_id=$1", id); err != nil {
		return err
	}
	return tx.Commit()
}

func botTx(ctx context.Context, db *sql.DB, telegramID int64) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", telegramID); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Store) RedeemBotCode(ctx context.Context, telegramID int64, code string) error {
	tx, err := botTx(ctx, s.DB, telegramID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, "DELETE FROM bot_codes WHERE hash=$1 AND expires_at>now() RETURNING user_id", Hash(code)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBotUnavailable
	}
	if err != nil {
		return err
	}
	var linked string
	err = tx.QueryRowContext(ctx, "INSERT INTO bot_links VALUES($1,$2) ON CONFLICT DO NOTHING RETURNING user_id", telegramID, id).Scan(&linked)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBotUnavailable
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

type botQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func botUser(ctx context.Context, q botQueryer, telegramID int64, lock string) (core.User, error) {
	var u core.User
	err := q.QueryRowContext(ctx, `SELECT u.id,u.name,u.enabled,u.expires_at,u.quota_bytes,u.used_bytes,u.group_id,u.token
 FROM bot_links l JOIN users u ON u.id=l.user_id WHERE l.telegram_id=$1`+lock, telegramID).
		Scan(&u.ID, &u.Name, &u.Enabled, &u.ExpiresAt, &u.QuotaBytes, &u.UsedBytes, &u.GroupID, &u.Token)
	if errors.Is(err, sql.ErrNoRows) {
		return u, nil
	}
	return u, err
}

func (s *Store) BotUser(ctx context.Context, telegramID int64) (core.User, error) {
	u, err := botUser(ctx, s.DB, telegramID, "")
	if err == nil && u.ID != "" {
		u.Token, err = s.Reveal(u.Token)
	}
	return u, err
}

func botEligible(u core.User, o BotOrder, now time.Time) bool {
	return u.ID == o.UserID && (u.ID == "" ||
		(u.Enabled && u.GroupID == o.Plan.GroupID &&
			(o.Plan.Stars > 0 || !u.Active(now)) &&
			(u.QuotaBytes == 0 || o.Plan.QuotaBytes <= math.MaxInt64-u.QuotaBytes)))
}

// PrepareBotOrder snapshots the offer shown on the confirmation screen.
func (s *Store) PrepareBotOrder(ctx context.Context, telegramID int64, planID string) (BotOrder, error) {
	o := BotOrder{ID: core.ID(), TelegramID: telegramID}
	u, err := botUser(ctx, s.DB, telegramID, "")
	if err != nil {
		return o, err
	}
	o.UserID = u.ID
	p := &o.Plan
	p.ID = planID
	err = s.DB.QueryRowContext(ctx, "SELECT name,days,quota_bytes,group_id,stars FROM bot_plans WHERE id=$1 AND enabled", planID).
		Scan(&p.Name, &p.Days, &p.QuotaBytes, &p.GroupID, &p.Stars)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrBotUnavailable
	}
	if err != nil {
		return o, err
	}
	if !botEligible(u, o, time.Now()) {
		return o, ErrBotUnavailable
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO bot_orders(id,telegram_id,user_id,plan_id,plan_name,group_id,days,quota_bytes,stars,status)
 VALUES($1,$2,nullif($3,''),$4,$5,$6,$7,$8,$9,'pending')`, o.ID, telegramID, o.UserID, p.ID, p.Name, p.GroupID, p.Days, p.QuotaBytes, p.Stars)
	return o, err
}

// ProcessBotOrder claims a free order, authorizes checkout, or applies a receipt.
// A valid receipt for an account that changed after checkout is kept for support.
func (s *Store) ProcessBotOrder(ctx context.Context, id string, telegramID int64, stage, currency string, stars int, charge string) (string, error) {
	tx, err := botTx(ctx, s.DB, telegramID)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var o BotOrder
	var available bool
	var checkoutID string
	p := &o.Plan
	err = tx.QueryRowContext(ctx, `SELECT coalesce(o.user_id,''),o.group_id,o.days,o.quota_bytes,o.stars,o.status,coalesce(o.charge_id,''),
 p.enabled AND p.group_id=o.group_id AND p.days=o.days AND p.quota_bytes=o.quota_bytes AND p.stars=o.stars,
 o.created_at,coalesce(o.checkout_id,'') FROM bot_orders o JOIN bot_plans p ON p.id=o.plan_id
 WHERE o.id=$1 AND o.telegram_id=$2 FOR UPDATE OF o`, id, telegramID).
		Scan(&o.UserID, &p.GroupID, &p.Days, &p.QuotaBytes, &p.Stars, &o.Status, &o.ChargeID, &available, &o.CreatedAt, &checkoutID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrBotUnavailable
	}
	if err != nil {
		return "", err
	}
	if stage == "receipt" {
		if charge == "" || currency != "XTR" || stars != p.Stars || p.Stars == 0 {
			return "", ErrBotUnavailable
		}
		if o.ChargeID == charge {
			return o.Status, nil
		}
		if o.Status != "approved" {
			return "", ErrBotUnavailable
		}
	} else {
		if stage == "free" && o.Status == "fulfilled" && p.Stars == 0 {
			return o.Status, nil
		}
		if !available || time.Since(o.CreatedAt) > 30*time.Minute {
			return "", ErrBotUnavailable
		}
		switch stage {
		case "invoice":
			if p.Stars == 0 || (o.Status != "pending" && o.Status != "invoiced") {
				return "", ErrBotUnavailable
			}
		case "checkout":
			if p.Stars == 0 || currency != "XTR" || stars != p.Stars || charge == "" ||
				(o.Status != "invoiced" && !(o.Status == "approved" && checkoutID == charge)) {
				return "", ErrBotUnavailable
			}
		case "free":
			if p.Stars != 0 || o.Status != "pending" {
				return "", ErrBotUnavailable
			}
		default:
			return "", ErrBotUnavailable
		}
	}
	u, err := botUser(ctx, tx, telegramID, " FOR UPDATE OF l,u")
	if err != nil {
		return "", err
	}
	now := time.Now()
	status := "fulfilled"
	if !botEligible(u, o, now) {
		if stage != "receipt" {
			return "", ErrBotUnavailable
		}
		status = "review"
	} else {
		switch stage {
		case "invoice":
			_, err = tx.ExecContext(ctx, "UPDATE bot_orders SET status='invoiced' WHERE id=$1", id)
			if err != nil {
				return "", err
			}
			return strconv.Itoa(p.Stars), tx.Commit()
		case "checkout":
			status = "approved"
			checkoutID, charge = charge, ""
		default:
			if u.ID == "" {
				u.ID = core.ID()
				token, err := s.Protect(core.RandomToken())
				if err != nil {
					return "", err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO users(id,name,enabled,expires_at,quota_bytes,group_id,token)
    VALUES($1,$2,true,$3,$4,$5,$6)`, u.ID, "Telegram "+u.ID, now.AddDate(0, 0, p.Days), p.QuotaBytes, p.GroupID, token)
				if err != nil {
					return "", err
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO bot_links VALUES($1,$2)", telegramID, u.ID); err != nil {
					return "", err
				}
			} else {
				expiry := now
				if u.ExpiresAt.After(now) {
					expiry = u.ExpiresAt
				}
				quota := int64(0)
				if u.QuotaBytes != 0 && p.QuotaBytes != 0 {
					quota = u.QuotaBytes + p.QuotaBytes
				}
				if _, err = tx.ExecContext(ctx, "UPDATE users SET expires_at=$2,quota_bytes=$3 WHERE id=$1", u.ID, expiry.AddDate(0, 0, p.Days), quota); err != nil {
					return "", err
				}
			}
			o.UserID = u.ID
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE bot_orders SET status=$2,user_id=nullif($3,''),charge_id=nullif($4,''),checkout_id=nullif($5,'') WHERE id=$1`, id, status, o.UserID, charge, checkoutID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "bot_orders_charge_id_key" {
			return "", ErrBotUnavailable
		}
		return "", err
	}
	return status, tx.Commit()
}
