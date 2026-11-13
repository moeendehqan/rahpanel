package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return ip.Is4() || netip.MustParsePrefix("2000::/3").Contains(ip)
}

func webhookURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("HTTPS webhook URL required")
	}
	return u, nil
}

func webhookSend(ctx context.Context, destination, secret string, body []byte) error {
	u, err := webhookURL(destination)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return errors.New("webhook DNS unavailable")
	}
	for _, ip := range ips {
		if !publicAddress(ip) {
			return errors.New("webhook address is not public")
		}
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), "443"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Rah-Timestamp", stamp)
	request.Header.Set("X-Rah-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("webhook HTTP delivery failed")
	}
	return nil
}

func (s *Server) webhookSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == "PUT" {
		var input struct {
			URL    string `json:"url"`
			Secret string `json:"secret"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.URL != "" {
			if _, err := webhookURL(input.URL); err != nil || len(input.Secret) < 32 {
				problem(w, 400, "Public HTTPS URL and signing secret of at least 32 characters required")
				return
			}
		}
		tx, err := s.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			problem(w, 503, "Cannot save webhook")
			return
		}
		defer tx.Rollback()
		save := func() error {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(503812)"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE webhook_events SET delivery='cancelled: settings changed' WHERE delivery='pending'"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM webhook_state"); err != nil {
				return err
			}
			if input.URL == "" {
				_, err := tx.ExecContext(ctx, "DELETE FROM webhook_config")
				return err
			}
			secret, err := s.Store.Protect(input.Secret)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO webhook_config VALUES(true,$1,$2) ON CONFLICT(singleton) DO UPDATE SET url=excluded.url,secret=excluded.secret", input.URL, []byte(secret))
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO webhook_state(kind,target,status)
                SELECT 'user.enabled',id,CASE WHEN enabled THEN 'enabled' ELSE 'suspended' END FROM users
                UNION ALL SELECT 'user.quota',id,CASE WHEN quota_bytes>0 AND used_bytes>=quota_bytes THEN 'exhausted' ELSE 'available' END FROM users
                UNION ALL SELECT 'user.expiry',id,CASE WHEN expires_at<=now() THEN 'expired' ELSE 'valid' END FROM users
                UNION ALL SELECT 'node.connection',id,CASE WHEN last_seen>=now()-interval '5 minutes' THEN 'connected' ELSE 'disconnected' END FROM nodes WHERE enabled AND ready`)
			return err
		}
		if save() != nil || tx.Commit() != nil {
			problem(w, 503, "Cannot save webhook")
			return
		}
		s.auditAction(r, "webhook.settings", "webhook")
	}
	var destination string
	err := s.Store.DB.QueryRowContext(ctx, "SELECT url FROM webhook_config").Scan(&destination)
	if err != nil && err != sql.ErrNoRows {
		problem(w, 503, "Cannot read webhook")
		return
	}
	rows, err := s.Store.DB.QueryContext(ctx, "SELECT id,kind,target,status,occurred_at,attempts,delivery FROM webhook_events ORDER BY occurred_at DESC,id LIMIT 50")
	if err != nil {
		problem(w, 503, "Cannot read webhook history")
		return
	}
	defer rows.Close()
	history := []webhookEvent{}
	for rows.Next() {
		var item webhookEvent
		if err = rows.Scan(&item.ID, &item.Kind, &item.Target, &item.Status, &item.OccurredAt, &item.Attempts, &item.Delivery); err != nil {
			problem(w, 503, "Cannot read webhook history")
			return
		}
		history = append(history, item)
	}
	if rows.Err() != nil {
		problem(w, 503, "Cannot read webhook history")
		return
	}
	write(w, 200, map[string]any{"url": destination, "history": history})
}

type webhookEvent struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Target     string    `json:"target"`
	Status     string    `json:"status"`
	OccurredAt time.Time `json:"occurred_at"`
	Attempts   int       `json:"attempts,omitempty"`
	Delivery   string    `json:"delivery,omitempty"`
}

func (s *Server) RunWebhooks(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.webhookTick(ctx); err != nil && ctx.Err() == nil {
			log.Print("Webhook worker failed: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) webhookTick(ctx context.Context) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(503812)"); err != nil {
		return err
	}
	var destination, encrypted string
	err = tx.QueryRowContext(ctx, "SELECT url,secret FROM webhook_config").Scan(&destination, &encrypted)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	const observe = `WITH current_status(kind,target,status) AS (
  SELECT 'user.expiry',id,CASE WHEN expires_at<=now() THEN 'expired' ELSE 'valid' END FROM users
  UNION ALL SELECT 'node.connection',id,CASE WHEN last_seen>=now()-interval '5 minutes' THEN 'connected' ELSE 'disconnected' END FROM nodes WHERE enabled AND ready
 ), events AS (
  INSERT INTO webhook_events(id,kind,target,status)
   SELECT gen_random_uuid()::text,c.kind,c.target,c.status FROM current_status c JOIN webhook_state p USING(kind,target)
   WHERE p.status<>c.status AND c.status NOT IN ('valid','available')
 ), prune AS (
  DELETE FROM webhook_state p WHERE p.kind IN ('user.expiry','node.connection') AND NOT EXISTS(SELECT 1 FROM current_status c WHERE c.kind=p.kind AND c.target=p.target)
 )
 INSERT INTO webhook_state SELECT * FROM current_status ON CONFLICT(kind,target) DO UPDATE SET status=excluded.status WHERE webhook_state.status<>excluded.status`
	if _, err = tx.ExecContext(ctx, observe); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.deliverWebhook(ctx, destination, encrypted)
}

func (s *Server) deliverWebhook(ctx context.Context, destination, encrypted string) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var item webhookEvent
	err = tx.QueryRowContext(ctx, `SELECT id,kind,target,status,occurred_at,attempts FROM webhook_events
 WHERE delivery='pending' AND next_attempt<=now() AND EXISTS(SELECT 1 FROM webhook_config WHERE url=$1 AND secret=$2)
 ORDER BY next_attempt,id LIMIT 1 FOR UPDATE SKIP LOCKED`, destination, []byte(encrypted)).Scan(&item.ID, &item.Kind, &item.Target, &item.Status, &item.OccurredAt, &item.Attempts)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	secret, err := s.Store.Reveal(encrypted)
	if err != nil {
		return err
	}
	attempts := item.Attempts + 1
	item.Attempts = 0
	body, err := json.Marshal(item)
	if err != nil {
		return err
	}
	delivery := "delivered"
	if webhookSend(ctx, destination, secret, body) != nil {
		delivery = "pending"
		if attempts >= 5 {
			delivery = "failed"
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE webhook_events SET attempts=$2,delivery=$3,next_attempt=now()+$4*interval '1 second' WHERE id=$1`, item.ID, attempts, delivery, 30*(1<<(attempts-1))); err != nil {
		return err
	}
	return tx.Commit()
}
