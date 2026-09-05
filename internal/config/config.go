package config

import (
	"errors"
	"os"

	"github.com/joho/godotenv"
)

type Config struct{ Env, Port, DatabaseURL, RedisURL, JWTSecret, OTPSecret string }

func Load() (Config, error) {
	_ = godotenv.Load()
	c := Config{Env: getenv("APP_ENV", "development"), Port: getenv("PORT", "8080"), DatabaseURL: os.Getenv("DATABASE_URL"), RedisURL: getenv("REDIS_URL", "redis://localhost:6379/0"), JWTSecret: os.Getenv("JWT_SECRET"), OTPSecret: os.Getenv("OTP_SECRET")}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required; create .env from .env.example")
	}
	if c.JWTSecret == "" && c.Env == "production" {
		return c, errors.New("JWT_SECRET is required in production")
	}
	if c.JWTSecret == "" {
		c.JWTSecret = "development-only-change-me"
	}
	if c.OTPSecret == "" {
		c.OTPSecret = c.JWTSecret
	}
	return c, nil
}
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
