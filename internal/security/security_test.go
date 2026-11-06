package security

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestNodeIdentityIsClientOnlyAndSigned(t *testing.T) {
	ca, e := NewAuthority()
	if e != nil {
		t.Fatal(e)
	}
	cert, key, serial, e := ca.Issue("node-123", false, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tls.X509KeyPair(cert, key); e != nil {
		t.Fatal(e)
	}
	b, _ := pem.Decode(cert)
	c, e := x509.ParseCertificate(b.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.Certificate)
	if _, e = c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e != nil {
		t.Fatal(e)
	}
	if c.Subject.CommonName != "node-123" || serial == "" {
		t.Fatal("identity missing")
	}
	if _, e = c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e == nil {
		t.Fatal("client identity usable as server")
	}
}
func TestTOTPRejectsWrongCodes(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	now := time.Unix(1700000000, 0)
	if _, ok := CheckTOTP(secret, "000000", now); ok {
		t.Fatal("wrong OTP accepted")
	}
}
