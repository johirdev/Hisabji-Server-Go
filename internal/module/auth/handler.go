package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	Service *Service
}

// Register handles user registration.
func (h *Handler) Register(c *gin.Context) {
	var q RegisterRequest

	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	u, otp, err := h.Service.Register(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error": err.Error(),
		})
		return
	}

	out := gin.H{
		"user":    u,
		"message": "verification OTP sent",
	}

	if otp != "" {
		out["otp"] = otp
	}

	c.JSON(http.StatusCreated, out)
}

// Login handles user login.
func (h *Handler) Login(c *gin.Context) {
	var q LoginRequest

	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	u, token, err := h.Service.Login(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user":  u,
		"token": token,
	})
}

// Verify handles OTP verification.
func (h *Handler) Verify(c *gin.Context) {
	var q VerifyOTPRequest

	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	if err := h.Service.Verify(c.Request.Context(), q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "phone verified",
	})
}
