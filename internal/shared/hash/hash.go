package hash

import "golang.org/x/crypto/bcrypt"

func Password(value string) (string, error) {
	b, e := bcrypt.GenerateFromPassword([]byte(value), bcrypt.DefaultCost)
	return string(b), e
}
func Compare(encoded, value string) bool {
	return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(value)) == nil
}
