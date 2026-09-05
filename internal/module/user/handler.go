package user

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	Repo *Repository
}

// Update handles user profile updates.
func (h *Handler) Update(c *gin.Context) {
	var q UpdateRequest

	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	user, err := h.Repo.Update(
		c.Request.Context(),
		c.GetString("user_id"),
		q,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": user,
	})
}

// Username handles username updates.
func (h *Handler) Username(c *gin.Context) {
	var q UsernameRequest

	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	user, err := h.Repo.SetUsername(
		c.Request.Context(),
		c.GetString("user_id"),
		q.Username,
	)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error": "username is unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": user,
	})
}

// Delete handles user account deletion.
func (h *Handler) Delete(c *gin.Context) {
	err := h.Repo.Delete(
		c.Request.Context(),
		c.GetString("user_id"),
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.Status(http.StatusNoContent)
}
