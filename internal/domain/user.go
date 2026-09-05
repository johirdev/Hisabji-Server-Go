package domain

import "time"

type User struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Username           *string   `json:"username,omitempty"`
	Email              *string   `json:"email,omitempty"`
	Phone              string    `json:"phone"`
	UserImagePath      *string   `json:"user_image_path,omitempty"`
	UserType           string    `json:"user_type"`
	Plan               string    `json:"plan"`
	AIPredictionsUsed  int       `json:"ai_predictions_used"`
	AIPredictionsLimit int       `json:"ai_predictions_limit"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
