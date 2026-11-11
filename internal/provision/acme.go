package provision

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/store"
	"golang.org/x/crypto/acme"
	"time"
)

type Certificate struct {
	Certificate []byte
	Key         []byte
	Expires     time.Time
}

func IssueCertificate(ctx context.Context, s *store.Store, cfg Settings, domain string) (Certificate, error) {
	var saved []byte
	e := s.GetSetting(ctx, "acme-account", &saved)
	var account *ecdsa.PrivateKey
	if errors.Is(e, sql.ErrNoRows) {
		account, e = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return Certificate{}, e
		}
		saved, e = x509.MarshalECPrivateKey(account)
		if e != nil {
			return Certificate{}, e
		}
		if e = s.SetSetting(ctx, "acme-account", saved); e != nil {
			return Certificate{}, e
		}
	} else if e != nil {
		return Certificate{}, e
	} else {
		account, e = x509.ParseECPrivateKey(saved)
		if e != nil {
			return Certificate{}, e
		}
	}
	directory := cfg.ACMEURL
	if directory == "" {
		directory = acme.LetsEncryptURL
	}
	client := &acme.Client{Key: account, DirectoryURL: directory}
	if _, e = client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + cfg.Email}}, acme.AcceptTOS); e != nil && !errors.Is(e, acme.ErrAccountAlreadyExists) {
		return Certificate{}, fmt.Errorf("ACME account registration failed")
	}
	order, e := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if e != nil {
		return Certificate{}, fmt.Errorf("ACME order failed")
	}
	dns := DNS{Token: cfg.CloudflareToken, Zone: cfg.ZoneID}
	for _, authURL := range order.AuthzURLs {
		auth, e := client.GetAuthorization(ctx, authURL)
		if e != nil {
			return Certificate{}, e
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		var challenge *acme.Challenge
		for _, ch := range auth.Challenges {
			if ch.Type == "dns-01" {
				challenge = ch
				break
			}
		}
		if challenge == nil {
			return Certificate{}, fmt.Errorf("DNS-01 challenge not offered")
		}
		value, e := client.DNS01ChallengeRecord(challenge.Token)
		if e != nil {
			return Certificate{}, e
		}
		name := "_acme-challenge." + domain
		id, e := dns.TXT(ctx, name, value)
		if e != nil {
			return Certificate{}, e
		}
		// Delete only the exact TXT record created by this issuance, including on failure.
		cleanup := func() {
			c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = dns.Delete(c, id)
		}
		prop, cancel := context.WithTimeout(ctx, 3*time.Minute)
		e = waitTXT(prop, name, value)
		cancel()
		if e == nil {
			_, e = client.Accept(ctx, challenge)
		}
		if e == nil {
			_, e = client.WaitAuthorization(ctx, authURL)
		}
		cleanup()
		if e != nil {
			return Certificate{}, fmt.Errorf("ACME DNS validation failed")
		}
	}
	order, e = client.WaitOrder(ctx, order.URI)
	if e != nil {
		return Certificate{}, e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return Certificate{}, e
	}
	csr, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain}}, key)
	if e != nil {
		return Certificate{}, e
	}
	chain, _, e := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if e != nil {
		return Certificate{}, fmt.Errorf("ACME certificate issuance failed")
	}
	out := Certificate{}
	for _, der := range chain {
		out.Certificate = append(out.Certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	leaf, e := x509.ParseCertificate(chain[0])
	if e != nil {
		return out, e
	}
	out.Expires = leaf.NotAfter
	raw, e := x509.MarshalECPrivateKey(key)
	out.Key = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})
	return out, e
}
