package auth

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/middleware"
	"github.com/redis/go-redis/v9"
)

func Routes(r *gin.RouterGroup, h *Handler, redisClient *redis.Client) {
	r.POST("/register", middleware.RedisRateLimit(redisClient, "auth:register", 10, time.Minute), h.Register)
	r.POST("/login", middleware.RedisRateLimit(redisClient, "auth:login", 30, time.Minute), h.Login)
	r.POST("/verify-otp", middleware.RedisRateLimit(redisClient, "auth:verify-otp", 10, time.Minute), h.Verify)
}
