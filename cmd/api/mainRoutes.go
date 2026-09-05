package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/middleware"
	"github.com/johirdev/Hisabji-Server/internal/module/auth"
	"github.com/johirdev/Hisabji-Server/internal/module/user"
	"github.com/redis/go-redis/v9"
)

func setupRoutes(db *pgxpool.Pool, redisClient *redis.Client, cfg config.Config) *gin.Engine {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.RedisRateLimit(redisClient, "global", 300, time.Minute))

	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Hisabji server is running",
			"service": "Hisabji API",
			"env":     cfg.Env,
		})
	})

	r.GET("/health", func(c *gin.Context) {
		if err := db.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	authHandler := &auth.Handler{
		Service: &auth.Service{
			Repo:   &auth.Repository{DB: db},
			Config: cfg,
		},
	}
	auth.Routes(api.Group("/auth"), authHandler, redisClient)

	userHandler := &user.Handler{Repo: &user.Repository{DB: db}}
	user.Routes(api.Group("/users"), userHandler, cfg.JWTSecret, redisClient)

	return r
}
