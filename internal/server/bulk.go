package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/store"
)

func (s *Server) bulkUsers(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Names     []string `json:"names"`
		Group     string   `json:"group_id"`
		Days      int64    `json:"days"`
		Quota     int64    `json:"quota_bytes"`
		Key       string   `json:"key"`
		Action    string   `json:"action"`
		IDs       []string `json:"ids"`
		Amount    int64    `json:"amount"`
		Confirmed int      `json:"confirmed"`
	}
	if !decode(w, r, &input) {
		return
	}
	count := len(input.IDs)
	if input.Action == "create" {
		if count != 0 || input.Days < 1 || input.Days > 36500 || input.Quota < 0 || input.Group == "" {
			problem(w, 400, "Invalid creation settings")
			return
		}
		count = len(input.Names)
	}
	if len(input.Key) < 16 || len(input.Key) > 128 || count == 0 || count > 100 {
		problem(w, 400, "Invalid operation key or user count")
		return
	}
	statements := map[string]string{
		"create":  "INSERT INTO users(id,name,enabled,expires_at,quota_bytes,group_id,token) VALUES($1,$2,true,now()+$3*interval '1 day',$4,$5,$6)",
		"extend":  "UPDATE users SET expires_at=greatest(expires_at,now())+$2*interval '1 day' WHERE id=$1",
		"quota":   "UPDATE users SET quota_bytes=CASE WHEN quota_bytes=0 THEN 0 ELSE quota_bytes+$2 END WHERE id=$1",
		"enable":  "UPDATE users SET enabled=true WHERE id=$1",
		"suspend": "UPDATE users SET enabled=false WHERE id=$1",
		"reset":   "UPDATE users SET used_bytes=0,last_reset_at=now() WHERE id=$1",
		"delete":  "DELETE FROM users WHERE id=$1",
	}
	query, ok := statements[input.Action]
	withAmount := input.Action == "extend" || input.Action == "quota"
	if !ok || (withAmount && input.Amount <= 0) || (input.Action == "extend" && input.Amount > 36500) ||
		((input.Action == "delete" || input.Action == "reset") && input.Confirmed != len(input.IDs)) {
		problem(w, 400, "Invalid bulk operation")
		return
	}
	sort.Strings(input.IDs)
	for i, id := range input.IDs {
		if id == "" || (i > 0 && input.IDs[i-1] == id) {
			problem(w, 400, "Invalid or duplicate user: "+id)
			return
		}
	}
	body, _ := json.Marshal(input)
	digest := store.Hash(string(body))
	admin, _ := r.Context().Value(adminKey{}).(string)
	ctx := r.Context()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		problem(w, 503, "Cannot start bulk operation")
		return
	}
	defer tx.Rollback()
	var inserted string
	err = tx.QueryRowContext(ctx, "INSERT INTO bulk_requests(admin_id,key,digest) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING key", admin, input.Key, digest).Scan(&inserted)
	if err == sql.ErrNoRows {
		var previous string
		if err = tx.QueryRowContext(ctx, "SELECT digest FROM bulk_requests WHERE admin_id=$1 AND key=$2", admin, input.Key).Scan(&previous); err != nil {
			problem(w, 503, "Cannot read bulk operation")
			return
		}
		if previous != digest {
			problem(w, 409, "Operation key already used")
			return
		}
	} else if err != nil {
		problem(w, 503, "Cannot reserve operation key")
		return
	} else {
		if input.Action == "create" {
			for _, name := range input.Names {
				if strings.TrimSpace(name) == "" || len(name) > 256 {
					problem(w, 400, "Invalid user name: "+name)
					return
				}
				id := core.ID()
				protected, e := s.Store.Protect(core.RandomToken())
				if e != nil {
					problem(w, 503, "Cannot protect subscription")
					return
				}
				if _, e = tx.ExecContext(ctx, query, id, strings.TrimSpace(name), input.Days, input.Quota, input.Group, protected); e != nil {
					problem(w, 400, "Invalid or duplicate user: "+name)
					return
				}
				if _, e = tx.ExecContext(ctx, "INSERT INTO audit(admin_id,action,target) VALUES($1,'user.bulk.create',$2)", admin, id); e != nil {
					problem(w, 503, "Cannot audit bulk operation")
					return
				}
			}
		}
		for _, id := range input.IDs {
			args := []any{id}
			if withAmount {
				args = append(args, input.Amount)
			}
			result, e := tx.ExecContext(ctx, query, args...)
			if e != nil {
				problem(w, 400, "Cannot update user: "+id)
				return
			}
			if count, _ := result.RowsAffected(); count != 1 {
				problem(w, 400, "Unknown user: "+id)
				return
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO audit(admin_id,action,target) VALUES($1,$2,$3)", admin, "user.bulk."+input.Action, id); e != nil {
				problem(w, 503, "Cannot audit bulk operation")
				return
			}
		}
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Cannot commit bulk operation")
		return
	}
	write(w, 200, map[string]int{"count": count})
}
