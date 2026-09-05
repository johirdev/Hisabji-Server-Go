package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/shared/jwt"
)

func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		id, e := jwt.Parse(secret, h)
		if e != nil || id == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Set("user_id", id)
		c.Next()
	}
}
