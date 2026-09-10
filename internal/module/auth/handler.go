package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/handler"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/johirdev/Hisabji-Server/internal/core/validate"
)

// Handler is the auth module's HTTP layer.
//
// Every method has the same shape: bind, call the service, wrap the result.
// There is no error formatting, no status-code selection and no JSON writing
// here — handler.H does all three, so a controller stays readable and no
// endpoint can accidentally invent its own error format.
type Handler struct {
	Service *Service
}

// NewHandler builds the handler.
func NewHandler(s *Service) *Handler { return &Handler{Service: s} }

// meta collects the ambient request information the service audits.
func meta(c *gin.Context) RequestMeta {
	return RequestMeta{
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Locale:    contextx.Locale(c),
	}
}

// Register handles POST /auth/register.
func (h *Handler) Register(c *gin.Context) (*response.Result, error) {
	var in RegisterRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	in.DeviceName = deviceName(c, in.DeviceName)

	m := meta(c)
	m.DeviceName = in.DeviceName

	out, err := h.Service.Register(c.Request.Context(), in, m)
	if err != nil {
		return nil, err
	}
	return response.Created(out,
		"Account created. Enter the verification code we sent to your phone."), nil
}

// VerifyOTP handles POST /auth/verify-otp.
func (h *Handler) VerifyOTP(c *gin.Context) (*response.Result, error) {
	var in VerifyOTPRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.VerifyPhone(c.Request.Context(), in, meta(c))
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Phone number verified. You are now signed in."), nil
}

// ResendOTP handles POST /auth/resend-otp.
func (h *Handler) ResendOTP(c *gin.Context) (*response.Result, error) {
	var in ResendOTPRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.ResendOTP(c.Request.Context(), in, meta(c))
	if err != nil {
		return nil, err
	}
	return response.OK(out, "A new verification code has been sent."), nil
}

// Login handles POST /auth/login.
func (h *Handler) Login(c *gin.Context) (*response.Result, error) {
	var in LoginRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	in.DeviceName = deviceName(c, in.DeviceName)

	m := meta(c)
	m.DeviceName = in.DeviceName

	out, err := h.Service.Login(c.Request.Context(), in, m)
	if err != nil {
		return nil, err
	}
	if out.RequiresVerification {
		return response.OK(out,
			"Please verify your phone number to continue."), nil
	}
	return response.OK(out, "Signed in successfully."), nil
}

// Refresh handles POST /auth/refresh.
func (h *Handler) Refresh(c *gin.Context) (*response.Result, error) {
	var in RefreshRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	m := meta(c)
	m.DeviceName = deviceName(c, nil)

	out, err := h.Service.Refresh(c.Request.Context(), in, m)
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Session refreshed."), nil
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	// An empty body is a valid logout of the current session, so binding
	// failures on an absent body are ignored here on purpose.
	var in LogoutRequest
	_ = c.ShouldBindJSON(&in)

	out, err := h.Service.Logout(c.Request.Context(), userID, contextx.SessionID(c), in)
	if err != nil {
		return nil, err
	}
	if in.AllDevices {
		return response.OK(out, "Signed out of all devices."), nil
	}
	return response.OK(out, "Signed out successfully."), nil
}

// Sessions handles GET /auth/sessions.
func (h *Handler) Sessions(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	out, err := h.Service.ListSessions(c.Request.Context(), userID, contextx.SessionID(c))
	if err != nil {
		return nil, err
	}
	return response.OK(out, "Signed-in devices fetched successfully."), nil
}

// RevokeSession handles DELETE /auth/sessions/:id.
func (h *Handler) RevokeSession(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	id, err := validate.UUIDParam(c, "id")
	if err != nil {
		return nil, err
	}
	if err := h.Service.RevokeSession(c.Request.Context(), userID, id); err != nil {
		return nil, err
	}
	return response.OK(map[string]any{"id": id}, "That device has been signed out."), nil
}

// ChangePassword handles POST /auth/change-password.
func (h *Handler) ChangePassword(c *gin.Context) (*response.Result, error) {
	userID, err := handler.UserID(c)
	if err != nil {
		return nil, err
	}
	var in ChangePasswordRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.ChangePassword(c.Request.Context(), userID, contextx.SessionID(c), in)
	if err != nil {
		return nil, err
	}
	return response.OK(out,
		"Password changed. Your other devices have been signed out."), nil
}

// ForgotPassword handles POST /auth/forgot-password.
func (h *Handler) ForgotPassword(c *gin.Context) (*response.Result, error) {
	var in ForgotPasswordRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.ForgotPassword(c.Request.Context(), in, meta(c))
	if err != nil {
		return nil, err
	}
	// Deliberately the same message whether or not the number is registered.
	return response.OK(out,
		"If that phone number is registered, a reset code has been sent to it."), nil
}

// ResetPassword handles POST /auth/reset-password.
func (h *Handler) ResetPassword(c *gin.Context) (*response.Result, error) {
	var in ResetPasswordRequest
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := h.Service.ResetPassword(c.Request.Context(), in, meta(c))
	if err != nil {
		return nil, err
	}
	return response.OK(out,
		"Password reset. Please sign in with your new password."), nil
}

// deviceName falls back to the User-Agent so the sessions list is never a
// column of "Unknown device".
func deviceName(c *gin.Context, provided *string) *string {
	if provided != nil && *provided != "" {
		return provided
	}
	ua := c.Request.UserAgent()
	if ua == "" {
		return nil
	}
	if len(ua) > 120 {
		ua = ua[:120]
	}
	return &ua
}
