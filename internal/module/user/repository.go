package user

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

type Repository struct {
	DB *pgxpool.Pool
}

// Update updates user profile information.
func (r *Repository) Update(
	ctx context.Context,
	id string,
	q UpdateRequest,
) (domain.User, error) {
	var user domain.User

	err := r.DB.QueryRow(
		ctx,
		`
		UPDATE users
		SET
			name = COALESCE(NULLIF($2, ''), name),
			email = COALESCE(NULLIF($3, ''), email),
			user_image_path = COALESCE(NULLIF($4, ''), user_image_path),
			updated_at = NOW()
		WHERE id = $1
		RETURNING
			id,
			name,
			username,
			email,
			phone,
			user_image_path,
			user_type,
			plan,
			ai_predictions_used,
			ai_predictions_limit,
			created_at,
			updated_at
		`,
		id,
		val(q.Name),
		val(q.Email),
		val(q.UserImagePath),
	).Scan(
		&user.ID,
		&user.Name,
		&user.Username,
		&user.Email,
		&user.Phone,
		&user.UserImagePath,
		&user.UserType,
		&user.Plan,
		&user.AIPredictionsUsed,
		&user.AIPredictionsLimit,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	return user, err
}

// SetUsername updates user's username.
func (r *Repository) SetUsername(
	ctx context.Context,
	id string,
	username string,
) (domain.User, error) {
	var user domain.User

	err := r.DB.QueryRow(
		ctx,
		`
		UPDATE users
		SET
			username = $2,
			updated_at = NOW()
		WHERE id = $1
		RETURNING
			id,
			name,
			username,
			email,
			phone,
			user_image_path,
			user_type,
			plan,
			ai_predictions_used,
			ai_predictions_limit,
			created_at,
			updated_at
		`,
		id,
		username,
	).Scan(
		&user.ID,
		&user.Name,
		&user.Username,
		&user.Email,
		&user.Phone,
		&user.UserImagePath,
		&user.UserType,
		&user.Plan,
		&user.AIPredictionsUsed,
		&user.AIPredictionsLimit,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	return user, err
}

// Delete deletes a user account.
func (r *Repository) Delete(ctx context.Context, id string) error {
	_, err := r.DB.Exec(
		ctx,
		`DELETE FROM users WHERE id = $1`,
		id,
	)

	return err
}

// val returns the value of a string pointer.
func val(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}
