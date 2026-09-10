package database

import (
	"context"
	"fmt"
	"time"

	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/redis/go-redis/v9"
)

// OpenRedis creates and verifies the Redis client used for rate limiting,
// caching and short-lived locks.
//
// Redis holds nothing that cannot be rebuilt: counters, cached analytics,
// idempotency guards. Losing it degrades performance, never correctness — all
// durable state lives in Postgres.
func OpenRedis(ctx context.Context, cfg config.Redis) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL could not be parsed: %w", err)
	}

	opts.PoolSize = cfg.PoolSize
	opts.MinIdleConns = cfg.MinIdleConns
	opts.DialTimeout = cfg.DialTimeout
	opts.ReadTimeout = cfg.ReadTimeout
	opts.WriteTimeout = cfg.WriteTimeout
	opts.MaxRetries = 2
	opts.PoolTimeout = cfg.ReadTimeout + time.Second

	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis is unreachable at the configured REDIS_URL: %w", err)
	}

	logger.Named("database").Info("redis connected",
		"pool_size", cfg.PoolSize, "key_prefix", cfg.KeyPrefix)

	return client, nil
}

// RedisStats returns client telemetry for the detailed health endpoint.
func RedisStats(client *redis.Client) map[string]any {
	if client == nil {
		return map[string]any{"status": "not_initialised"}
	}
	s := client.PoolStats()
	return map[string]any{
		"total_conns": s.TotalConns,
		"idle_conns":  s.IdleConns,
		"stale_conns": s.StaleConns,
		"hits":        s.Hits,
		"misses":      s.Misses,
		"timeouts":    s.Timeouts,
	}
}

// RedisHealthy reports whether Redis still answers.
func RedisHealthy(ctx context.Context, client *redis.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return client.Ping(ctx).Err()
}
