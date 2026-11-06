package provision

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"net"
	"sync/atomic"
	"testing"
)

func TestChangedHostKeyPreventsSendingPassword(t *testing.T) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(key)
	if e != nil {
		t.Fatal(e)
	}
	var attempts atomic.Int32
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) { attempts.Add(1); return nil, nil }}
	config.AddHostKey(signer)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				s, ch, req, e := ssh.NewServerConn(c, config)
				if e != nil {
					c.Close()
					return
				}
				defer s.Close()
				go ssh.DiscardRequests(req)
				for channel := range ch {
					channel.Reject(ssh.Prohibited, "unused")
				}
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	fp, e := Fingerprint(context.Background(), "127.0.0.1", port)
	if e != nil {
		t.Fatal(e)
	}
	if fp != ssh.FingerprintSHA256(signer.PublicKey()) || attempts.Load() != 0 {
		t.Fatal("probe authenticated or returned wrong host key")
	}
	c := SSHCredentials{Host: "127.0.0.1", Port: port, Username: "root", Password: "must-not-send", Fingerprint: "SHA256:wrong"}
	if client, e := connect(context.Background(), c); e == nil {
		client.Close()
		t.Fatal("mismatched host key accepted")
	}
	if attempts.Load() != 0 {
		t.Fatal("password sent before host key verification")
	}
	c.Fingerprint = fp
	client, e := connect(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	client.Close()
	if attempts.Load() != 1 {
		t.Fatal("correctly pinned host did not authenticate")
	}
}
