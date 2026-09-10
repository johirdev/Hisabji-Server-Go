package user

import (
	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/handler"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/johirdev/Hisabji-Server/internal/core/validate"
)

// Handler is the user module's HTTP layer.
type Handler struct {
	Service *Service
}

// NewHandler builds the handler.
func NewHandler(s *Service) *Handler { return &Handler{Service: s} }

// Me handles GET /users/me.
//
// ?stats=true adds the lifetime counters. They are opt-in because they cost
// several aggregate scans, and a token refresh that only needs the plan should
// not pay for them.
func (h *Handler) Me(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	withStats := c.Query("stats") == "true" || c.Query("stats") == "1"

	out, err := h.Service.Profile(c.Request.Context(), userID, withStats)
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Profile fetched successfully."), nil
}

// UpdateMe handles PATCH /users/me.
func (h *Handler) UpdateMe(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in UpdateProfileRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.UpdateProfile(c.Request.Context(), userID, in)
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Profile updated successfully."), nil
}

// Preferences handles PATCH /users/me/preferences.
func (h *Handler) Preferences(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in UpdatePreferencesRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.UpdatePreferences(c.Request.Context(), userID, in)
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Preferences updated successfully."), nil
}

// SetUsername handles PUT /users/me/username.
func (h *Handler) SetUsername(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in UsernameRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.SetUsername(c.Request.Context(), userID, in)
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Username updated successfully."), nil
}

// CheckUsername handles GET /users/username-available?username=x.
func (h *Handler) CheckUsername(c *gin.Context) (*response.Result, error) {
	username := c.Query("username")
	if username == "" {
		return nil, validateMissing("username")
	}
	// Works signed out (during registration) and signed in (when changing it,
	// where the caller's own current username must not count as taken).
	out, err := h.Service.CheckUsername(c.Request.Context(), handler.OptionalUserID(c), username)
	if err != nil {
		return nil, err
	}
	if out.Available {
		return response.OK(out, "That username is available."), nil
	}
	return response.OK(out, out.Reason), nil
}

// Onboarding handles POST /users/me/onboarding.
func (h *Handler) Onboarding(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in OnboardingRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.Onboarding(c.Request.Context(), userID, in)
	if err != nil {
		return nil, err
	}
	if in.Step == "done" {
		return response.OK(out, "Setup complete. Welcome to Hisabji."), nil
	}
	return response.OK(out, "Setup progress saved."), nil
}

// DeleteMe handles DELETE /users/me.
func (h *Handler) DeleteMe(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in DeleteAccountRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.DeleteAccount(c.Request.Context(), userID, in)
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Your account has been closed."), nil
}

func validateMissing(field string) error {
	return validate.NewErrors().
		Add(field, "required",
			field+" is required as a query parameter.",
			field+" query parameter হিসেবে দিতে হবে।").
		Err()
}
