package server

import "net/http"

func (s *Server) resetPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == "PUT" {
		var input struct {
			Days int `json:"days"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Days < 0 || input.Days > 3650 {
			problem(w, 400, "Invalid reset period")
			return
		}
		ok, err := s.Store.SetResetPolicy(r.Context(), id, input.Days)
		if err != nil {
			problem(w, 503, "Cannot save reset policy")
			return
		}
		if !ok {
			problem(w, 409, "User or accounting group unavailable")
			return
		}
		s.auditAction(r, "usage.reset-policy", id)
	}
	days, next, ready, err := s.Store.ResetPolicy(r.Context(), id)
	if err != nil {
		problem(w, 404, "Cannot read reset policy")
		return
	}
	write(w, 200, map[string]any{"days": days, "next": next, "ready": ready})
}
