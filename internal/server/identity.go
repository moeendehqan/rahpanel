package server

import (
	"fmt"
	"github.com/rahpanel/rahpanel/internal/core"
	"net/http"
	"time"
)

// Two-phase node identity rotation: the old certificate remains usable for a
// bounded recovery window, so losing a response does not strand the node.
func (s *Server) refreshNodeIdentity(r *http.Request, nodeID string) ([]byte, []byte, error) {
	peer := r.TLS.PeerCertificates[0]
	tx, e := s.Store.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, nil, e
	}
	defer tx.Rollback()
	var current string
	var cert, encrypted []byte
	if e = tx.QueryRowContext(r.Context(), "SELECT client_serial,client_cert,client_key FROM nodes WHERE id=$1 FOR UPDATE", nodeID).Scan(&current, &cert, &encrypted); e != nil {
		return nil, nil, e
	}
	if peer.SerialNumber.String() == current && time.Until(peer.NotAfter) < 30*24*time.Hour {
		var serial string
		var key []byte
		cert, key, serial, e = s.Worker.CA.Issue(nodeID, false, nil)
		if e != nil {
			return nil, nil, e
		}
		encrypted, e = core.Seal(s.Store.Key, string(key))
		if e != nil {
			return nil, nil, e
		}
		_, e = tx.ExecContext(r.Context(), "UPDATE nodes SET previous_serial=client_serial,previous_until=now()+interval '24 hours',client_serial=$2,client_cert=$3,client_key=$4 WHERE id=$1", nodeID, serial, cert, encrypted)
		if e != nil {
			return nil, nil, e
		}
		current = serial
	}
	if e = tx.Commit(); e != nil {
		return nil, nil, e
	}
	if peer.SerialNumber.String() == current {
		return nil, nil, nil
	}
	if len(cert) == 0 || len(encrypted) == 0 {
		return nil, nil, fmt.Errorf("pending client identity unavailable")
	}
	key, e := core.Open(s.Store.Key, encrypted)
	if e != nil {
		return nil, nil, e
	}
	return cert, []byte(key), nil
}
