package user

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/middleware"
	"github.com/redis/go-redis/v9"
)

func Routes(r *gin.RouterGroup, h *Handler, secret string, redisClient *redis.Client) {
	r.Use(middleware.Auth(secret))
	r.PUT("/me", middleware.RedisRateLimit(redisClient, "user:update", 30, time.Hour), h.Update)
	r.PUT("/me/username", middleware.RedisRateLimit(redisClient, "user:username", 10, time.Hour), h.Username)
	r.DELETE("/me", middleware.RedisRateLimit(redisClient, "user:delete", 5, time.Hour), h.Delete)
}
