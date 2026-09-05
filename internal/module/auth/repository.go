package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

type Repository struct{ DB *pgxpool.Pool }

func (r *Repository) Create(ctx context.Context, q RegisterRequest, password string) (domain.User, error) {
	var u domain.User
	e := r.DB.QueryRow(ctx, `INSERT INTO users(name,phone,email,username,password_hash,user_image_path) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5,NULLIF($6,'')) RETURNING id,name,username,email,phone,user_image_path,user_type,plan,ai_predictions_used,ai_predictions_limit,created_at,updated_at`, q.Name, q.Phone, val(q.Email), val(q.Username), password, val(q.UserImagePath)).Scan(&u.ID, &u.Name, &u.Username, &u.Email, &u.Phone, &u.UserImagePath, &u.UserType, &u.Plan, &u.AIPredictionsUsed, &u.AIPredictionsLimit, &u.CreatedAt, &u.UpdatedAt)
	return u, e
}
func (r *Repository) Find(ctx context.Context, identifier string) (domain.User, string, bool, error) {
	var u domain.User
	var p string
	var verified bool
	e := r.DB.QueryRow(ctx, `SELECT id,name,username,email,phone,user_image_path,user_type,plan,ai_predictions_used,ai_predictions_limit,created_at,updated_at,password_hash,phone_verified FROM users WHERE phone=$1 OR email=$1 OR username=$1`, identifier).Scan(&u.ID, &u.Name, &u.Username, &u.Email, &u.Phone, &u.UserImagePath, &u.UserType, &u.Plan, &u.AIPredictionsUsed, &u.AIPredictionsLimit, &u.CreatedAt, &u.UpdatedAt, &p, &verified)
	if errors.Is(e, pgx.ErrNoRows) {
		return u, "", false, nil
	}
	return u, p, verified, e
}
func (r *Repository) SaveOTP(ctx context.Context, phone, digest string, expires interface{}) error {
	_, e := r.DB.Exec(ctx, `INSERT INTO phone_otps(phone,code_hash,expires_at) VALUES($1,$2,$3)`, phone, digest, expires)
	return e
}
func (r *Repository) OTPCount(ctx context.Context, phone string) int {
	var n int
	_ = r.DB.QueryRow(ctx, `SELECT count(*) FROM phone_otps WHERE phone=$1 AND created_at>now()-interval '6 hours'`, phone).Scan(&n)
	return n
}
func (r *Repository) RecordLoginFailure(ctx context.Context, identifier string) {
	_, _ = r.DB.Exec(ctx, `INSERT INTO login_attempts(identifier) VALUES($1)`, identifier)
}
func (r *Repository) LoginFailureCount(ctx context.Context, identifier string) int {
	var n int
	_ = r.DB.QueryRow(ctx, `SELECT count(*) FROM login_attempts WHERE identifier=$1 AND created_at>now()-interval '6 hours'`, identifier).Scan(&n)
	return n
}
func (r *Repository) ClearLoginFailures(ctx context.Context, identifier string) {
	_, _ = r.DB.Exec(ctx, `DELETE FROM login_attempts WHERE identifier=$1`, identifier)
}
func (r *Repository) VerifyOTP(ctx context.Context, phone, digest string) error {
	tx, e := r.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	e = tx.QueryRow(ctx, `SELECT id FROM phone_otps WHERE phone=$1 AND code_hash=$2 AND expires_at>now() AND consumed_at IS NULL ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, phone, digest).Scan(&id)
	if e != nil {
		return errors.New("invalid or expired OTP")
	}
	if _, e = tx.Exec(ctx, `UPDATE phone_otps SET consumed_at=now() WHERE id=$1`, id); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET phone_verified=true,updated_at=now() WHERE phone=$1`, phone); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func val(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
