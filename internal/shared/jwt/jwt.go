// Package jwt issues the access tokens this API authenticates with, and derives
// the stored form of the opaque refresh tokens that back them.
//
// ACCESS token  — a short-lived (30 min) signed JWT, sent on every request. It
//
//	carries the claims the auth middleware needs (user, role,
//	plan), so a normal request costs zero database reads to
//	authenticate.
//
// REFRESH token — 256 bits of crypto/rand, NOT a JWT. It is sent only to
//
//	/auth/refresh and is checked against the sessions table on
//	every use, which is what makes revocation and reuse detection
//	possible. Only its HMAC (keyed with REFRESH_SECRET) is
//	stored, so a database dump contains no usable tokens.
//
// The plan claim is a deliberate 30-minute-stale cache. A user who upgrades
// sees premium unlock on their next token refresh; the billing service always
// re-checks the database before actually spending credits, so a stale claim
// can never grant something that was not paid for.
package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
)

// TokenType distinguishes the two secrets and prevents a refresh token from
// being replayed as an access token.
type TokenType string

const (
	TypeAccess  TokenType = "access"
	TypeRefresh TokenType = "refresh"
)

// Claims is the payload of both token types.
type Claims struct {
	UserID    string    `json:"sub"`
	Role      string    `json:"role,omitempty"`
	Plan      string    `json:"plan,omitempty"`
	SessionID string    `json:"sid,omitempty"`
	Type      TokenType `json:"typ"`
	jwtv5.RegisteredClaims
}

// Issuer signs and verifies tokens. One instance is built at boot and shared.
type Issuer struct {
	accessSecret  []byte
	refreshSecret []byte
	issuer        string
	accessTTL     time.Duration
	refreshTTL    time.Duration
}

// Config configures an Issuer.
type Config struct {
	AccessSecret  string
	RefreshSecret string
	Issuer        string
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
}

// New builds an Issuer, rejecting a configuration that cannot be secure.
func New(cfg Config) (*Issuer, error) {
	if len(cfg.AccessSecret) < 16 {
		return nil, errors.New("jwt: access secret is too short")
	}
	if len(cfg.RefreshSecret) < 16 {
		return nil, errors.New("jwt: refresh secret is too short")
	}
	if cfg.AccessSecret == cfg.RefreshSecret {
		return nil, errors.New("jwt: access and refresh secrets must differ")
	}
	return &Issuer{
		accessSecret:  []byte(cfg.AccessSecret),
		refreshSecret: []byte(cfg.RefreshSecret),
		issuer:        cfg.Issuer,
		accessTTL:     cfg.AccessTTL,
		refreshTTL:    cfg.RefreshTTL,
	}, nil
}

// TokenPair is what a successful login or refresh returns.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"` // seconds until the access token expires
	ExpiresAt    time.Time `json:"expires_at"`
	RefreshAt    time.Time `json:"refresh_expires_at"`
}

// Identity is the caller a token represents.
type Identity struct {
	UserID    string
	Role      string
	Plan      string
	SessionID string
}

// Issue packages an access token with the caller's opaque refresh token.
//
// # WHY THE REFRESH TOKEN IS NOT A JWT
//
// A JWT is worth its size when the receiver must validate it WITHOUT a lookup.
// That is exactly true of the access token — and exactly false of the refresh
// token, which is checked against the sessions table on every use anyway (that
// is what makes revocation and reuse detection possible). Signing it would add
// length and a second secret while changing nothing about the security, and it
// invites the real mistake: trusting the claims instead of the row.
//
// So a refresh token is 256 bits of crypto/rand, stored as a peppered hash.
// It cannot be forged, and it is worthless the moment its row is revoked.
func (i *Issuer) Issue(id Identity, refreshToken string) (TokenPair, error) {
	now := time.Now()

	access, err := i.sign(id, TypeAccess, i.accessSecret, now, i.accessTTL)
	if err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  access,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(i.accessTTL.Seconds()),
		ExpiresAt:    now.Add(i.accessTTL),
		RefreshAt:    now.Add(i.refreshTTL),
	}, nil
}

// IssueAccess mints only a new access token, for the refresh flow when the
// refresh token itself is being rotated separately.
func (i *Issuer) IssueAccess(id Identity) (string, time.Time, error) {
	now := time.Now()
	tok, err := i.sign(id, TypeAccess, i.accessSecret, now, i.accessTTL)
	return tok, now.Add(i.accessTTL), err
}

func (i *Issuer) sign(id Identity, typ TokenType, secret []byte, now time.Time, ttl time.Duration) (string, error) {
	claims := Claims{
		UserID:    id.UserID,
		Role:      id.Role,
		Plan:      id.Plan,
		SessionID: id.SessionID,
		Type:      typ,
		RegisteredClaims: jwtv5.RegisteredClaims{
			Subject:   id.UserID,
			Issuer:    i.issuer,
			IssuedAt:  jwtv5.NewNumericDate(now),
			NotBefore: jwtv5.NewNumericDate(now.Add(-30 * time.Second)), // clock skew
			ExpiresAt: jwtv5.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims).SignedString(secret)
}

// ParseAccess verifies an access token and returns its claims.
func (i *Issuer) ParseAccess(token string) (*Claims, error) {
	return i.parse(token, TypeAccess, i.accessSecret)
}

// PepperRefresh returns the value stored in sessions.token_hash: an HMAC of the
// opaque refresh token, keyed with REFRESH_SECRET.
//
// A plain SHA-256 would already be safe, because the token is 256 bits of
// randomness with nothing to guess. The HMAC adds one property on top: an
// attacker who obtains a database dump but NOT the application secret cannot
// mint a row that matches a token of their choosing. It costs one hash and
// gives the refresh secret a real job.
func (i *Issuer) PepperRefresh(token string) string {
	mac := hmac.New(sha256.New, i.refreshSecret)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (i *Issuer) parse(token string, want TokenType, secret []byte) (*Claims, error) {
	if token == "" {
		return nil, apperr.New(401, apperr.CodeUnauthorized, "An access token is required.").
			WithHint("Send the header: Authorization: Bearer <access_token>")
	}

	claims := &Claims{}
	parsed, err := jwtv5.ParseWithClaims(token, claims,
		func(t *jwtv5.Token) (any, error) {
			// Pinning the algorithm is what stops the classic "alg: none" and
			// HMAC/RSA confusion attacks. Never trust the header's alg.
			if _, ok := t.Method.(*jwtv5.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
			}
			return secret, nil
		},
		jwtv5.WithValidMethods([]string{jwtv5.SigningMethodHS256.Alg()}),
		jwtv5.WithIssuer(i.issuer),
		jwtv5.WithLeeway(30*time.Second),
	)

	if err != nil {
		switch {
		case errors.Is(err, jwtv5.ErrTokenExpired):
			return nil, apperr.New(401, apperr.CodeTokenExpired, "Your session has expired.").
				WithHint("Call POST /api/v1/auth/refresh with your refresh token to get a new one.")
		case errors.Is(err, jwtv5.ErrTokenNotValidYet):
			return nil, apperr.New(401, apperr.CodeTokenInvalid,
				"This token is not valid yet. Check that your device clock is correct.")
		default:
			return nil, apperr.New(401, apperr.CodeTokenInvalid,
				"Your session token is not valid. Please sign in again.").WithCause(err)
		}
	}
	if !parsed.Valid {
		return nil, apperr.New(401, apperr.CodeTokenInvalid, "Your session token is not valid.")
	}

	// A refresh token presented as an access token must be rejected: it is
	// long-lived, so accepting it would defeat the short access-token lifetime.
	if claims.Type != want {
		return nil, apperr.New(401, apperr.CodeTokenInvalid,
			fmt.Sprintf("This is a %s token; a %s token is required here.", claims.Type, want))
	}
	if claims.UserID == "" {
		return nil, apperr.New(401, apperr.CodeTokenInvalid, "Your session token is incomplete.")
	}

	return claims, nil
}

// AccessTTL exposes the configured access token lifetime.
func (i *Issuer) AccessTTL() time.Duration { return i.accessTTL }

// RefreshTTL exposes the configured refresh token lifetime.
func (i *Issuer) RefreshTTL() time.Duration { return i.refreshTTL }
