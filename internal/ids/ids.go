// Package ids generates random identifiers and secrets.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

const (
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	// DocIDLength is the length of document identifiers.
	DocIDLength = 12
)

// New returns a nanoid of the given size using an alphanumeric alphabet.
func New(size int) string {
	out := make([]byte, 0, size)
	buf := make([]byte, size+size/2)
	for len(out) < size {
		rand.Read(buf) // never fails since Go 1.24
		for _, b := range buf {
			// 248 = 4*62: rejecting higher bytes keeps the distribution uniform.
			if b >= 248 {
				continue
			}
			out = append(out, alphabet[b%62])
			if len(out) == size {
				break
			}
		}
	}
	return string(out)
}

// DocID returns a new document identifier.
func DocID() string { return New(DocIDLength) }

// ValidDocID reports whether s has the shape of a document identifier.
func ValidDocID(s string) bool {
	if len(s) != DocIDLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z') {
			return false
		}
	}
	return true
}

// Secret returns n random bytes encoded as unpadded base64url.
func Secret(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hash returns the hex SHA-256 of s. Used for high-entropy secrets only.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
