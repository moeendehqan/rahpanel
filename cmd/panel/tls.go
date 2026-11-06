package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"github.com/rahpanel/rahpanel/internal/security"
	"github.com/rahpanel/rahpanel/internal/store"
	"time"
)

func endpointCertificate(ctx context.Context, s *store.Store, ca *security.Authority, host string) (*tls.Certificate, error) {
	var identity struct{ Cert, Key []byte }
	e := s.GetSetting(ctx, "agent-server-tls", &identity)
	renew := errors.Is(e, sql.ErrNoRows)
	if e != nil && !renew {
		return nil, e
	}
	if !renew {
		pair, e := tls.X509KeyPair(identity.Cert, identity.Key)
		if e != nil {
			return nil, e
		}
		leaf, e := x509.ParseCertificate(pair.Certificate[0])
		if e != nil {
			return nil, e
		}
		renew = time.Until(leaf.NotAfter) < 30*24*time.Hour || leaf.VerifyHostname(host) != nil
		if !renew {
			return &pair, nil
		}
	}
	identity.Cert, identity.Key, _, e = ca.Issue("rah-agent-endpoint", true, []string{host})
	if e != nil {
		return nil, e
	}
	if e = s.SetSetting(ctx, "agent-server-tls", identity); e != nil {
		return nil, e
	}
	pair, e := tls.X509KeyPair(identity.Cert, identity.Key)
	return &pair, e
}
