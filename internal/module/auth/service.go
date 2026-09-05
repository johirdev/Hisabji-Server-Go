package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/domain"
	"github.com/johirdev/Hisabji-Server/internal/shared/hash"
	"github.com/johirdev/Hisabji-Server/internal/shared/jwt"
)

type Service struct {
	Repo   *Repository
	Config config.Config
}

func (s *Service) Register(ctx context.Context, q RegisterRequest) (domain.User, string, error) {
	p, e := hash.Password(q.Password)
	if e != nil {
		return domain.User{}, "", e
	}
	u, e := s.Repo.Create(ctx, q, p)
	if e != nil {
		return u, "", e
	}
	code, e := s.sendOTP(ctx, q.Phone)
	return u, code, e
}
func (s *Service) Login(ctx context.Context, q LoginRequest) (domain.User, string, error) {
	u, p, verified, e := s.Repo.Find(ctx, q.Identifier)
	if e != nil {
		return u, "", e
	}
	if s.Repo.LoginFailureCount(ctx, q.Identifier) >= 5 {
		return u, "", errors.New("too many failed attempts; try again after 6 hours")
	}
	if p == "" || !hash.Compare(p, q.Password) {
		s.Repo.RecordLoginFailure(ctx, q.Identifier)
		return u, "", errors.New("invalid credentials")
	}
	s.Repo.ClearLoginFailures(ctx, q.Identifier)
	if !verified {
		return u, "", errors.New("phone verification required")
	}
	token, e := jwt.Sign(s.Config.JWTSecret, u.ID)
	return u, token, e
}
func (s *Service) Verify(ctx context.Context, q VerifyOTPRequest) error {
	return s.Repo.VerifyOTP(ctx, q.Phone, digest(q.OTP))
}
func (s *Service) sendOTP(ctx context.Context, phone string) (string, error) {
	if s.Repo.OTPCount(ctx, phone) >= 3 {
		return "", errors.New("OTP limit reached; try again after 6 hours")
	}
	n, e := rand.Int(rand.Reader, big.NewInt(900000))
	if e != nil {
		return "", e
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if e = s.Repo.SaveOTP(ctx, phone, digest(code), time.Now().Add(10*time.Minute)); e != nil {
		return "", e
	}
	if s.Config.Env == "development" {
		return code, nil
	}
	return "", nil
}
func digest(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
