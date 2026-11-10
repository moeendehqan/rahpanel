package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/rahpanel/rahpanel/internal/core"
	"sort"
	"strings"
	"time"
)

//go:embed schema.sql
var schema string

type Store struct {
	DB  *sql.DB
	Key []byte
}

func New(ctx context.Context, dsn string, key []byte) (*Store, error) {
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	if _, e = db.ExecContext(ctx, schema); e != nil {
		db.Close()
		return nil, e
	}
	_, e = db.ExecContext(ctx, "UPDATE jobs SET status='interrupted',error='Process restarted. Supply SSH credentials to resume.',updated_at=now() WHERE status IN ('queued','running')")
	return &Store{db, key}, e
}
func Hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func (s *Store) Protect(value string) (string, error) {
	b, e := core.Seal(s.Key, value)
	if e != nil {
		return "", e
	}
	return "enc:v1:" + base64.RawStdEncoding.EncodeToString(b), nil
}
func (s *Store) Reveal(value string) (string, error) {
	if !strings.HasPrefix(value, "enc:v1:") {
		return value, nil
	}
	b, e := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, "enc:v1:"))
	if e != nil {
		return "", e
	}
	return core.Open(s.Key, b)
}
func (s *Store) Audit(ctx context.Context, admin, action, target string) error {
	_, e := s.DB.ExecContext(ctx, "INSERT INTO audit(admin_id,action,target) VALUES($1,$2,$3)", admin, action, target)
	return e
}
func (s *Store) Users(ctx context.Context) ([]core.User, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,name,enabled,expires_at,quota_bytes,used_bytes,group_id,token FROM users ORDER BY name")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.User{}
	for rows.Next() {
		var u core.User
		if e = rows.Scan(&u.ID, &u.Name, &u.Enabled, &u.ExpiresAt, &u.QuotaBytes, &u.UsedBytes, &u.GroupID, &u.Token); e != nil {
			return nil, e
		}
		u.Token, e = s.Reveal(u.Token)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) SaveUser(ctx context.Context, u core.User, create bool) error {
	if strings.TrimSpace(u.Name) == "" || len(u.Name) > 100 || u.QuotaBytes < 0 || u.ExpiresAt.IsZero() {
		return fmt.Errorf("invalid user")
	}
	if create {
		token, e := s.Protect(core.RandomToken())
		if e != nil {
			return e
		}
		_, e = s.DB.ExecContext(ctx, "INSERT INTO users(id,name,enabled,expires_at,quota_bytes,group_id,token) VALUES($1,$2,$3,$4,$5,$6,$7)", u.ID, u.Name, u.Enabled, u.ExpiresAt, u.QuotaBytes, u.GroupID, token)
		return e
	}
	r, e := s.DB.ExecContext(ctx, "UPDATE users SET name=$2,enabled=$3,expires_at=$4,quota_bytes=$5,group_id=$6 WHERE id=$1", u.ID, u.Name, u.Enabled, u.ExpiresAt, u.QuotaBytes, u.GroupID)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) Nodes(ctx context.Context) ([]core.Node, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,name,host,domain,group_id,enabled,ready,verified,coalesce(last_seen,'epoch'),applied_version,last_error,reality_target,reality_sni,reality_private,reality_public,short_id,ws_path,shared_host,reality_port,websocket_port,trojan_port,stats_port,reality_behind_nginx,(client_serial<>'' OR ready) FROM nodes ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.Node{}
	for rows.Next() {
		var n core.Node
		var secret, verified []byte
		if e = rows.Scan(&n.ID, &n.Name, &n.Host, &n.Domain, &n.GroupID, &n.Enabled, &n.Ready, &verified, &n.LastSeen, &n.AppliedVersion, &n.LastError, &n.RealityTarget, &n.RealitySNI, &secret, &n.RealityPublic, &n.ShortID, &n.WSPath, &n.SharedHost, &n.Ports.Reality, &n.Ports.WebSocket, &n.Ports.Trojan, &n.Ports.Stats, &n.RealityBehindNginx, &n.PortsLocked); e != nil {
			return nil, e
		}
		n.RealityPrivate, e = core.Open(s.Key, secret)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(verified, &n.Verified); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) Node(ctx context.Context, id string) (core.Node, error) {
	all, e := s.Nodes(ctx)
	if e != nil {
		return core.Node{}, e
	}
	for _, n := range all {
		if n.ID == id {
			return n, nil
		}
	}
	return core.Node{}, sql.ErrNoRows
}
func (s *Store) AddNode(ctx context.Context, n core.Node) error {
	n.Ports = n.EffectivePorts()
	if e := n.Ports.Validate(n.SharedHost); e != nil {
		return e
	}
	secret, e := core.Seal(s.Key, n.RealityPrivate)
	if e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, `INSERT INTO nodes(id,name,host,domain,group_id,reality_target,reality_sni,reality_private,reality_public,short_id,ws_path,shared_host,reality_port,websocket_port,trojan_port,stats_port) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, n.ID, n.Name, n.Host, n.Domain, n.GroupID, n.RealityTarget, n.RealitySNI, secret, n.RealityPublic, n.ShortID, n.WSPath, n.SharedHost, n.Ports.Reality, n.Ports.WebSocket, n.Ports.Trojan, n.Ports.Stats)
	return e
}

var ErrNodePortsLocked = errors.New("ports cannot change after node identity activation; reinstall using its existing ports")

// Lock the node while updating pending ports and queuing the job. A conflicting
// concurrent install rolls back its port changes with the failed unique insert.
func (s *Store) QueueInstall(ctx context.Context, j core.Job, requested *core.NodePorts) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n core.Node
	if e = tx.QueryRowContext(ctx, `SELECT ready,shared_host,reality_port,websocket_port,trojan_port,stats_port,(client_serial<>'') FROM nodes WHERE id=$1 FOR UPDATE`, j.NodeID).Scan(&n.Ready, &n.SharedHost, &n.Ports.Reality, &n.Ports.WebSocket, &n.Ports.Trojan, &n.Ports.Stats, &n.PortsLocked); e != nil {
		return e
	}
	if requested != nil {
		ports := requested.WithDefaults(n.SharedHost)
		if e = ports.Validate(n.SharedHost); e != nil {
			return e
		}
		if (n.Ready || n.PortsLocked) && ports != n.Ports {
			return ErrNodePortsLocked
		}
		if _, e = tx.ExecContext(ctx, `UPDATE nodes SET reality_port=$2,websocket_port=$3,trojan_port=$4,stats_port=$5 WHERE id=$1`, j.NodeID, ports.Reality, ports.WebSocket, ports.Trojan, ports.Stats); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,node_id,status,step,error) VALUES($1,$2,$3,$4,$5)`, j.ID, j.NodeID, j.Status, j.Step, j.Error); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Credentials(ctx context.Context, nodeID string, users []core.User) ([]core.Credential, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, "SELECT user_id,node_id,uuid,password FROM credentials WHERE node_id=$1", nodeID)
	if e != nil {
		return nil, e
	}
	out := []core.Credential{}
	existing := map[string]bool{}
	for rows.Next() {
		var c core.Credential
		var encrypted []byte
		if e = rows.Scan(&c.UserID, &c.NodeID, &c.UUID, &encrypted); e != nil {
			rows.Close()
			return nil, e
		}
		c.UUID, e = s.Reveal(c.UUID)
		if e != nil {
			rows.Close()
			return nil, e
		}
		c.Password, e = core.Open(s.Key, encrypted)
		if e != nil {
			rows.Close()
			return nil, e
		}
		existing[c.UserID] = true
		out = append(out, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, u := range users {
		if existing[u.ID] {
			continue
		}
		c := core.Credential{UserID: u.ID, NodeID: nodeID, UUID: core.ID(), Password: core.RandomToken()}
		protectedUUID, e := s.Protect(c.UUID)
		if e != nil {
			return nil, e
		}
		encrypted, e := core.Seal(s.Key, c.Password)
		if e != nil {
			return nil, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO credentials(user_id,node_id,uuid,password) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", c.UserID, c.NodeID, protectedUUID, encrypted)
		if e != nil {
			return nil, e
		}
		existing[c.UserID] = true
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	// A concurrent subscription request may have won the credential insertion.
	// Re-read canonical values rather than returning a locally generated loser.
	if len(out) < len(existing) {
		return s.Credentials(ctx, nodeID, nil)
	}
	return out, nil
}
func (s *Store) ApplyReport(ctx context.Context, nodeID string, r core.Report) error {
	if r.ID == "" || r.RunID == "" || r.Sequence < 0 || len(r.Usage) > 5000 {
		return errors.New("invalid report")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, "INSERT INTO reports(node_id,id,run_id,sequence) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", nodeID, r.ID, r.RunID, r.Sequence)
	if e != nil {
		return e
	}
	inserted, _ := result.RowsAffected()
	if inserted > 0 {
		sort.Slice(r.Usage, func(i, j int) bool { return r.Usage[i].UserID < r.Usage[j].UserID })
		for _, u := range r.Usage {
			if u.Upload < 0 || u.Download < 0 || u.Upload > 1<<60 || u.Download > 1<<60 {
				return errors.New("invalid usage")
			}
			res, e := tx.ExecContext(ctx, `UPDATE users SET used_bytes=used_bytes+$1 WHERE id=$2 AND EXISTS(SELECT 1 FROM credentials WHERE user_id=$2 AND node_id=$3)`, u.Upload+u.Download, u.UserID, nodeID)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				var exists bool
				if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)", u.UserID).Scan(&exists); e != nil {
					return e
				}
				if exists {
					return errors.New("credential not assigned to node")
				}
			}
		}
	}
	// Agent cannot overwrite readiness or external verification results.
	_, e = tx.ExecContext(ctx, "UPDATE nodes SET last_seen=now(),applied_version=$2,last_error=$3 WHERE id=$1", nodeID, r.AppliedVersion, SafeAgentError(r.Error))
	if e != nil {
		return e
	}
	return tx.Commit()
}
func SafeAgentError(s string) string {
	if s == "" {
		return ""
	}
	return "Agent reconciliation failed; check local service health."
}
func (s *Store) Job(ctx context.Context, j core.Job) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO jobs(id,node_id,status,step,error) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET status=$3,step=$4,error=$5,updated_at=now()`, j.ID, j.NodeID, j.Status, j.Step, j.Error)
	return e
}
func (s *Store) GetSetting(ctx context.Context, id string, out any) error {
	var b []byte
	if e := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE id=$1", id).Scan(&b); e != nil {
		return e
	}
	plain, e := core.Open(s.Key, b)
	if e != nil {
		return e
	}
	return json.Unmarshal([]byte(plain), out)
}
func (s *Store) SetSetting(ctx context.Context, id string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	sealed, e := core.Seal(s.Key, string(b))
	if e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO settings(id,value) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET value=$2", id, sealed)
	return e
}
