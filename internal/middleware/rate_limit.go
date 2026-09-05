package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RedisRateLimit(client *redis.Client, prefix string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := "rl:" + prefix + ":" + c.ClientIP()
		ctx := c.Request.Context()
		count, err := client.Incr(ctx, key).Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
			return
		}
		if count == 1 {
			_ = client.Expire(ctx, key, window).Err()
		}
		if count > int64(limit) {
			ttl, _ := client.TTL(ctx, key).Result()
			c.Header("Retry-After", ttl.Round(time.Second).String())
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded", "retry_after_seconds": int(ttl.Seconds())})
			return
		}
		c.Next()
	}
}
