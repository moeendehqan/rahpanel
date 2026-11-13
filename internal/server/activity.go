package server

import (
	"net/http"
	"time"
)

func (s *Server) userActivity(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT n.name,a.sampled_at FROM credentials c
 JOIN nodes n ON n.id=c.node_id LEFT JOIN user_node_activity a ON a.node_id=c.node_id AND a.user_id=c.user_id
 WHERE c.user_id=$1 ORDER BY n.name`, r.PathValue("id"))
	if err != nil {
		problem(w, 503, "Cannot read user activity")
		return
	}
	defer rows.Close()
	type activity struct {
		Node      string     `json:"node"`
		SampledAt *time.Time `json:"sampled_at"`
	}
	result := []activity{}
	for rows.Next() {
		var current activity
		if err = rows.Scan(&current.Node, &current.SampledAt); err != nil {
			problem(w, 503, "Cannot read user activity")
			return
		}
		result = append(result, current)
	}
	if rows.Err() != nil {
		problem(w, 503, "Cannot read user activity")
		return
	}
	write(w, 200, result)
}
