package security

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

func CheckTOTP(secret, code string, now time.Time) (int64, bool) {
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if e != nil || len(key) < 10 || len(code) != 6 {
		return 0, false
	}
	counter := now.Unix() / 30
	for d := int64(-1); d <= 1; d++ {
		c := counter + d
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(c))
		m := hmac.New(sha1.New, key)
		m.Write(b)
		sum := m.Sum(nil)
		o := sum[len(sum)-1] & 15
		n := (binary.BigEndian.Uint32(sum[o:o+4]) & 0x7fffffff) % 1000000
		if subtle.ConstantTimeCompare([]byte(fmt.Sprintf("%06d", n)), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}
