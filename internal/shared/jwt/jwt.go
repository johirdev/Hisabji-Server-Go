package jwt

import (
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

func Sign(secret, userID string) (string, error) {
	return jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, jwtv5.MapClaims{"sub": userID, "exp": time.Now().Add(24 * time.Hour).Unix()}).SignedString([]byte(secret))
}
func Parse(secret, token string) (string, error) {
	t, e := jwtv5.Parse(token, func(t *jwtv5.Token) (interface{}, error) { return []byte(secret), nil })
	if e != nil {
		return "", e
	}
	return t.Claims.GetSubject()
}
