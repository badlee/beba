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
		hashed = "{BCRYPT}" + hashed
	}
	if strings.HasPrefix(hashed, "{") {
		end := strings.IndexByte(hashed, '}')
		if end > 1 {
			alg, val := strings.ToUpper(hashed[1:end]), hashed[end+1:]
			candidate := []byte(val)
			plainBytes := []byte(plain)
			if b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(val)); err == nil {
				candidate = b
			} else if b, err := base64.StdEncoding.DecodeString(val); err == nil {
				candidate = b
			} else if b, err := hex.DecodeString(val); err == nil {
				candidate = b
			}
			var dig []byte
			switch alg {
			case "SHA512":
				h := sha512.Sum512(plainBytes)
				dig = h[:]
			case "SHA256":
				h := sha256.Sum256(plainBytes)
				dig = h[:]
			case "SHA1":
				h := sha1.Sum(plainBytes)
				dig = h[:]
			case "MD5":
				h := md5.Sum(plainBytes)
				dig = h[:]
			case "BCRYPT":
				if bcrypt.CompareHashAndPassword(candidate, plainBytes) == nil {
					return true
				}
				return false
			default:
				return false
			}
			return bytes.Equal(candidate, dig)
		}
	}
	return hashed == plain
}
