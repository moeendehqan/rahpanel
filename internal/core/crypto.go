package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

func RandomToken() string {
	b := make([]byte, 32)
	if _, e := io.ReadFull(rand.Reader, b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func NewNodeKeys(n *Node) error {
	k, e := ecdh.X25519().GenerateKey(rand.Reader)
	if e != nil {
		return e
	}
	n.RealityPrivate = base64.RawURLEncoding.EncodeToString(k.Bytes())
	n.RealityPublic = base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
	b := make([]byte, 8)
	if _, e = rand.Read(b); e != nil {
		return e
	}
	n.ShortID = hex.EncodeToString(b)
	n.WSPath = "/" + RandomToken()
	return nil
}
func Seal(key []byte, plain string) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, []byte(plain), nil), nil
}
func Open(key, data []byte) (string, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	if len(data) < g.NonceSize() {
		return "", fmt.Errorf("invalid ciphertext")
	}
	p, e := g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], nil)
	return string(p), e
}
