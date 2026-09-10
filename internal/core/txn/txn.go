// Package txn runs work inside a database transaction with correct rollback
// and bounded retries.
//
// Getting this wrong is the classic Go/pgx bug: a `defer tx.Rollback(ctx)` that
// hides the real error, or a missing rollback on an early return that leaks a
// connection until the pool drains. Run handles both, so service code reads as
// plain business logic:
//
//	err := txn.Run(ctx, pool, func(tx pgx.Tx) error {
//	    if err := wallet.Debit(ctx, tx, userID, cost); err != nil { return err }
//	    return ledger.Record(ctx, tx, entry)
//	})
package txn

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
)

// Pool is the subset of pgxpool.Pool this package needs.
type Pool interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// maxAttempts bounds retries of serialization failures and deadlocks. Three is
// enough in practice; more usually means a genuine hot-row design problem.
const maxAttempts = 3

// Run executes fn inside a transaction, committing on success and rolling back
// on any error or panic. Serialization failures and deadlocks are retried.
func Run(ctx context.Context, pool Pool, fn func(tx pgx.Tx) error) error {
	return RunWith(ctx, pool, pgx.TxOptions{}, fn)
}

// RunSerializable runs fn at SERIALIZABLE isolation. Use it wherever money or
// credits are read and then written — a subscription purchase, a credit debit —
// so two concurrent requests cannot both see the same balance and both spend it.
func RunSerializable(ctx context.Context, pool Pool, fn func(tx pgx.Tx) error) error {
	return RunWith(ctx, pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, fn)
}

// RunWith is Run with explicit transaction options.
func RunWith(ctx context.Context, pool Pool, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := attemptOnce(ctx, pool, opts, fn)
		if err == nil {
			return nil
		}
		lastErr = err

		if !isRetryable(err) || attempt == maxAttempts {
			return err
		}
		logger.FromContext(ctx).Warn("retrying transaction",
			"attempt", attempt, "error", err.Error())

		// Short randomised-ish backoff; enough to let the winner commit.
		select {
		case <-ctx.Done():
			return apperr.Timeout("").WithCause(ctx.Err())
		case <-time.After(time.Duration(attempt*attempt) * 15 * time.Millisecond):
		}
	}
	return lastErr
}

// attemptOnce is one transaction try. It is a separate function so the
// rollback defer runs before the retry loop continues.
func attemptOnce(ctx context.Context, pool Pool, opts pgx.TxOptions, fn func(tx pgx.Tx) error) (err error) {
	tx, beginErr := pool.BeginTx(ctx, opts)
	if beginErr != nil {
		return apperr.FromPostgres(beginErr, "")
	}

	committed := false
	defer func() {
		if r := recover(); r != nil {
			// Roll back with a fresh context: the request context may already
			// be cancelled, which would make the rollback itself fail.
			rollback(tx)
			panic(r) // let the recovery middleware log and translate it
		}
		if !committed {
			rollback(tx)
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxClosed) {
			return nil // fn already committed or rolled back deliberately
		}
		return apperr.FromPostgres(err, "")
	}
	committed = true
	return nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		logger.Named("txn").Error("rollback failed", "error", err.Error())
	}
}

// isRetryable reports whether Postgres told us to simply try again:
// 40001 serialization_failure, 40P01 deadlock_detected.
func isRetryable(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "40001" || pg.Code == "40P01"
	}
	// Our own translator turns those into a 409 CONFLICT.
	return apperr.IsCode(err, apperr.CodeConflict) && errors.As(err, &pg)
}

// Runner adapts a *pgxpool.Pool to the Pool interface. It exists so services
// can hold the narrow interface (and be tested with a fake) while main passes
// the real pool.
func Runner(pool *pgxpool.Pool) Pool { return pool }
