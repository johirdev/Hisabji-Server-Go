package auth

type RegisterRequest struct {
	Name          string  `json:"name" binding:"required,min=2,max=100"`
	Phone         string  `json:"phone" binding:"required"`
	Email         *string `json:"email"`
	Username      *string `json:"username"`
	Password      string  `json:"password" binding:"required,min=8"`
	UserImagePath *string `json:"user_image_path"`
}
type LoginRequest struct {
	Identifier string `json:"identifier" binding:"required"`
	Password   string `json:"password" binding:"required"`
}
type VerifyOTPRequest struct {
	Phone string `json:"phone" binding:"required"`
	OTP   string `json:"otp" binding:"required,len=6"`
}
type UpdateRequest struct {
	Name          *string `json:"name"`
	Email         *string `json:"email"`
	UserImagePath *string `json:"user_image_path"`
}
type UsernameRequest struct {
	Username string `json:"username" binding:"required,min=3,max=30"`
}
