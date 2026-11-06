package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"
)

type Authority struct {
	Certificate []byte `json:"certificate"`
	PrivateKey  []byte `json:"private_key"`
}

func serial() *big.Int {
	s, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		panic(e)
	}
	return s
}
func NewAuthority() (*Authority, error) {
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	t := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Rah Panel Node CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, e := x509.CreateCertificate(rand.Reader, t, t, &k.PublicKey, k)
	if e != nil {
		return nil, e
	}
	key, e := x509.MarshalECPrivateKey(k)
	return &Authority{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key})}, e
}
func (a *Authority) Issue(name string, server bool, hosts []string) ([]byte, []byte, string, error) {
	cb, _ := pem.Decode(a.Certificate)
	kb, _ := pem.Decode(a.PrivateKey)
	ca, e := x509.ParseCertificate(cb.Bytes)
	if e != nil {
		return nil, nil, "", e
	}
	cak, e := x509.ParseECPrivateKey(kb.Bytes)
	if e != nil {
		return nil, nil, "", e
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, "", e
	}
	s := serial()
	t := &x509.Certificate{SerialNumber: s, Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if server {
		t.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		for _, h := range hosts {
			if ip := net.ParseIP(h); ip != nil {
				t.IPAddresses = append(t.IPAddresses, ip)
			} else {
				t.DNSNames = append(t.DNSNames, h)
			}
		}
	}
	der, e := x509.CreateCertificate(rand.Reader, t, ca, &k.PublicKey, cak)
	if e != nil {
		return nil, nil, "", e
	}
	key, e := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key}), s.String(), e
}
