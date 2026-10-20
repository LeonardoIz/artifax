package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// Basic checks the single admin's email and password from the environment.
type Basic struct {
	emailHash    [32]byte
	passwordHash [32]byte
}

// NewBasic returns a checker for the given credentials.
func NewBasic(email, password string) *Basic {
	return &Basic{
		emailHash:    sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email)))),
		passwordHash: sha256.Sum256([]byte(password)),
	}
}

// Check reports whether the credentials match, in constant time.
func (b *Basic) Check(email, password string) bool {
	e := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	p := sha256.Sum256([]byte(password))
	okEmail := subtle.ConstantTimeCompare(e[:], b.emailHash[:])
	okPass := subtle.ConstantTimeCompare(p[:], b.passwordHash[:])
	return okEmail&okPass == 1
}
