// Package hash holds the one-way functions used for credentials and tokens.
//
// Two different primitives, for two different threat models:
//
//	Password(...)  bcrypt  — deliberately SLOW, so an attacker who steals the
//	                         users table cannot brute-force passwords. Cost is
//	                         configurable because hardware keeps getting faster.
//	Token(...)     SHA-256 — deliberately FAST, and that is correct here: a
//	                         refresh token or OTP is 128+ bits of our own
//	                         randomness, not a human-chosen word, so there is
//	                         nothing to brute-force and we verify them on every
//	                         request.
package hash

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/bcrypt"
)

// DefaultCost is used when a caller does not specify one.
const DefaultCost = 12

// ErrPasswordTooLong is returned for input bcrypt cannot handle. bcrypt
// silently truncates at 72 bytes, so a 200-character passphrase would be
// verified on its first 72 bytes only — surprising, and worth an explicit error.
var ErrPasswordTooLong = errors.New("password must be 72 bytes or fewer")

// Password hashes a plaintext password with bcrypt at the given cost.
// Pass 0 to use DefaultCost.
func Password(plain string, cost int) (string, error) {
	if len(plain) > 72 {
		return "", ErrPasswordTooLong
	}
	if cost <= 0 {
		cost = DefaultCost
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", fmt.Errorf("hash: could not hash password: %w", err)
	}
	return string(b), nil
}

// Compare reports whether the plaintext matches the stored hash. It is
// constant-time with respect to the hash contents, which bcrypt guarantees.
func Compare(encoded, plain string) bool {
	if encoded == "" || plain == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(plain)) == nil
}

// NeedsRehash reports whether a stored hash was made with a weaker cost than we
// now require. Call it after a successful login and transparently upgrade the
// stored hash — that is how a password database keeps up with hardware without
// forcing anybody to reset a password.
func NeedsRehash(encoded string, wantCost int) bool {
	if wantCost <= 0 {
		wantCost = DefaultCost
	}
	cost, err := bcrypt.Cost([]byte(encoded))
	if err != nil {
		return true // unparseable: replace it
	}
	return cost < wantCost
}

// Token returns the hex SHA-256 of a value. Used for refresh tokens and OTP
// codes, which are stored hashed so a database dump is not a set of live keys.
func Token(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// EqualToken compares two token digests in constant time, so an attacker
// cannot learn a prefix from response-timing differences.
func EqualToken(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RandomToken returns a URL-safe random string with n bytes of entropy.
// 32 bytes (256 bits) is the right size for a refresh token.
func RandomToken(n int) (string, error) {
	if n <= 0 {
		n = 32
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("hash: could not read secure randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NumericCode returns a cryptographically random decimal code of the given
// length, zero-padded — "042917" is as valid an OTP as "942917".
//
// crypto/rand, not math/rand: a predictable OTP is the same as no OTP.
func NumericCode(digits int) (string, error) {
	if digits < 4 {
		digits = 4
	}
	if digits > 9 {
		digits = 9
	}
	max := big.NewInt(1)
	for i := 0; i < digits; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("hash: could not generate a verification code: %w", err)
	}
	return fmt.Sprintf("%0*d", digits, n.Int64()), nil
}
