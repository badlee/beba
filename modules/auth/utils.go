package auth

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func CheckPassword(hashed, plain string) bool {
	if hashed == "" {
		return plain == ""
	}
	if strings.HasPrefix(hashed, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
	}
	if strings.HasPrefix(hashed, "{") {
		end := strings.IndexByte(hashed, '}')
		if end > 1 {
			alg, val := strings.ToUpper(hashed[1:end]), hashed[end+1:]
			var candidates [][]byte
			if b, err := hex.DecodeString(val); err == nil {
				candidates = append(candidates, b)
			}
			if b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(val)); err == nil {
				candidates = append(candidates, b)
			}
			if b, err := base64.StdEncoding.DecodeString(val); err == nil {
				candidates = append(candidates, b)
			}
			if len(candidates) == 0 {
				candidates = append(candidates, []byte(val))
			}
			var dig []byte
			switch alg {
			case "SHA512":
				h := sha512.Sum512([]byte(plain))
				dig = h[:]
			case "SHA256":
				h := sha256.Sum256([]byte(plain))
				dig = h[:]
			case "SHA1":
				h := sha1.Sum([]byte(plain))
				dig = h[:]
			case "MD5":
				h := md5.Sum([]byte(plain))
				dig = h[:]
			default:
				return false
			}
			for _, c := range candidates {
				if bytes.Equal(c, dig) {
					return true
				}
			}
			return false
		}
	}
	return hashed == plain
}
