package user

type UpdateRequest struct {
	Name          *string `json:"name"`
	Email         *string `json:"email"`
	UserImagePath *string `json:"user_image_path"`
}
type UsernameRequest struct {
	Username string `json:"username" binding:"required,min=3,max=30"`
}
