// Package database owns the Postgres pool, the Redis client and the migration
// runner. Nothing else in the app constructs a connection.
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
)

// Open creates and verifies the connection pool.
//
// The pool settings matter more than they look. MaxConns must stay below
// Postgres's own max_connections divided by the number of API replicas, or a
// rolling deploy takes the database down. MaxConnLifetime recycles connections
// so a failed-over primary does not leave the pool holding dead sockets.
func Open(ctx context.Context, cfg config.Database, appCfg config.App) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL could not be parsed: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = time.Minute
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	// Identify ourselves in pg_stat_activity — invaluable when you need to see
	// which service is holding a lock at 2am.
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolCfg.ConnConfig.RuntimeParams["application_name"] = appCfg.Name

	// A server-side statement timeout is the backstop that keeps one pathological
	// analytics query from pinning a connection forever. The HTTP layer's own
	// context timeout cancels the client side; this cancels the server side too.
	if cfg.StatementTimeout > 0 {
		poolCfg.ConnConfig.RuntimeParams["statement_timeout"] =
			fmt.Sprintf("%d", cfg.StatementTimeout.Milliseconds())
		poolCfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] =
			fmt.Sprintf("%d", (cfg.StatementTimeout * 2).Milliseconds())
	}

	// All analytics group by the user's local day, so every session must agree
	// on what "today" means.
	poolCfg.ConnConfig.RuntimeParams["timezone"] = appCfg.Timezone

	if appCfg.IsDevelopment() {
		poolCfg.ConnConfig.Tracer = &tracelog.TraceLog{
			Logger:   pgxSlogAdapter{},
			LogLevel: tracelog.LogLevelWarn,
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("could not create the database pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database is unreachable at the configured DATABASE_URL: %w", err)
	}

	logger.Named("database").Info("postgres connected",
		"max_conns", cfg.MaxConns,
		"min_conns", cfg.MinConns,
		"statement_timeout", cfg.StatementTimeout.String())

	return pool, nil
}

// Stats returns pool telemetry for the /health/detailed endpoint.
func Stats(pool *pgxpool.Pool) map[string]any {
	if pool == nil {
		return map[string]any{"status": "not_initialised"}
	}
	s := pool.Stat()
	return map[string]any{
		"total_conns":        s.TotalConns(),
		"idle_conns":         s.IdleConns(),
		"acquired_conns":     s.AcquiredConns(),
		"max_conns":          s.MaxConns(),
		"acquire_count":      s.AcquireCount(),
		"acquire_duration_ms": s.AcquireDuration().Milliseconds(),
		"canceled_acquires":  s.CanceledAcquireCount(),
		"empty_acquires":     s.EmptyAcquireCount(),
	}
}

// Healthy reports whether the pool can still serve a trivial query.
func Healthy(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var one int
	return pool.QueryRow(ctx, "SELECT 1").Scan(&one)
}

// pgxSlogAdapter routes pgx's internal logging through our structured logger.
type pgxSlogAdapter struct{}

func (pgxSlogAdapter) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	attrs := make([]any, 0, len(data)*2)
	for k, v := range data {
		// Never let query arguments (which may contain a password hash or an
		// OTP) reach the log.
		if k == "args" {
			continue
		}
		attrs = append(attrs, k, v)
	}
	l := logger.FromContext(ctx).With("component", "pgx")
	switch level {
	case tracelog.LogLevelError:
		l.Error(msg, attrs...)
	case tracelog.LogLevelWarn:
		l.Warn(msg, attrs...)
	case tracelog.LogLevelInfo:
		l.Info(msg, attrs...)
	default:
		l.Debug(msg, attrs...)
	}
}

// Acquire runs fn on a single dedicated connection. Use it for work that must
// share session state across statements (advisory locks, SET LOCAL).
func Acquire(ctx context.Context, pool *pgxpool.Pool, fn func(conn *pgx.Conn) error) error {
	c, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer c.Release()
	return fn(c.Conn())
}
