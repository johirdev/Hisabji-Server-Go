package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/migrations"
)

// advisoryLockKey is an arbitrary but fixed number. Every replica that starts
// up tries to take this Postgres advisory lock before migrating, so a rolling
// deploy of five containers runs the migrations exactly once instead of five
// times concurrently (which is how you get duplicate-index errors and a failed
// deploy at the worst possible moment).
const advisoryLockKey int64 = 8_15_20_09_02

// Migrate applies every pending migration in filename order, inside a single
// transaction per file, and records what it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	log := logger.Named("migrate")

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("could not acquire a connection for migrations: %w", err)
	}
	defer conn.Release()

	// Serialise across replicas. The lock is released automatically when the
	// session ends, so a crashed migrator cannot deadlock the next deploy.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("could not take the migration lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     text PRIMARY KEY,
			checksum    text        NOT NULL,
			applied_at  timestamptz NOT NULL DEFAULT now(),
			duration_ms integer     NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("could not create schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, conn.Conn())
	if err != nil {
		return err
	}

	if err := checkLegacySchema(ctx, conn.Conn(), applied); err != nil {
		return err
	}

	files, err := listMigrations()
	if err != nil {
		return err
	}

	pending := 0
	for _, name := range files {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("could not read migration %s: %w", name, err)
		}
		sum := checksum(body)

		if prev, ok := applied[name]; ok {
			// A changed file that is already applied means somebody edited
			// history. Refuse to guess: the database and the repository now
			// disagree, and silently continuing corrupts the schema.
			if prev != sum {
				return fmt.Errorf(
					"migration %s has changed since it was applied (recorded %s, file %s).\n"+
						"Never edit an applied migration — add a new one instead.\n"+
						"If this change is intentional and the database already matches, update its checksum in schema_migrations",
					name, short(prev), short(sum))
			}
			continue
		}

		started := time.Now()
		if err := applyOne(ctx, conn.Conn(), name, string(body), sum); err != nil {
			return err
		}
		took := time.Since(started)
		pending++
		log.Info("migration applied", "version", name, "duration_ms", took.Milliseconds())
	}

	if pending == 0 {
		log.Info("database schema is up to date", "migrations", len(files))
	} else {
		log.Info("database schema updated", "applied", pending, "total", len(files))
	}
	return nil
}

// applyOne runs a single migration file inside its own transaction, so a
// failure leaves the database on the last good version rather than half-way
// through a new one.
func applyOne(ctx context.Context, conn *pgx.Conn, name, body, sum string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("could not begin migration %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	started := time.Now()
	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("migration %s failed: %w", name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, checksum, duration_ms) VALUES ($1, $2, $3)`,
		name, sum, time.Since(started).Milliseconds()); err != nil {
		return fmt.Errorf("could not record migration %s: %w", name, err)
	}
	return tx.Commit(ctx)
}

// checkLegacySchema refuses to run against a database created by the old
// EnsureSchema() bootstrap rather than by these migrations.
//
// That database has a `users` table with a different, incompatible shape. Every
// CREATE TABLE here is IF NOT EXISTS, so migrating on top of it would appear to
// succeed while leaving the old columns in place — and the first login would
// then fail with a baffling "column role does not exist". Failing loudly with
// instructions is far kinder than that.
func checkLegacySchema(ctx context.Context, conn *pgx.Conn, applied map[string]string) error {
	if len(applied) > 0 {
		return nil // already managed by this migrator
	}

	var usersExists, hasRoleColumn bool
	if err := conn.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM information_schema.tables
			        WHERE table_schema = 'public' AND table_name = 'users'),
			EXISTS (SELECT 1 FROM information_schema.columns
			        WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'role')
	`).Scan(&usersExists, &hasRoleColumn); err != nil {
		return fmt.Errorf("could not inspect the existing schema: %w", err)
	}

	if usersExists && !hasRoleColumn {
		return fmt.Errorf(
			"this database was created by the old bootstrap schema and is not compatible " +
				"with the migration set.\n\n" +
				"The development data here is throwaway, so the fix is to recreate the database:\n\n" +
				"    make db-reset\n\n" +
				"or manually:\n\n" +
				"    docker compose exec postgres psql -U postgres -c \"DROP DATABASE hisabji\"\n" +
				"    docker compose exec postgres psql -U postgres -c \"CREATE DATABASE hisabji\"\n\n" +
				"If this is a database with real data, do NOT drop it — write a migration that " +
				"reshapes the existing tables instead")
	}
	return nil
}

func loadApplied(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("could not read schema_migrations: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var version, sum string
		if err := rows.Scan(&version, &sum); err != nil {
			return nil, err
		}
		out[version] = sum
	}
	return out, rows.Err()
}

func listMigrations() ([]string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("could not list migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	// Filenames are zero-padded (001_, 002_, ...), so lexical order is
	// numeric order.
	sort.Strings(names)
	return names, nil
}

// Applied returns the migration history, for the admin health endpoint.
func Applied(ctx context.Context, pool *pgxpool.Pool) ([]map[string]any, error) {
	rows, err := pool.Query(ctx,
		`SELECT version, applied_at, duration_ms FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var version string
		var appliedAt time.Time
		var durationMS int
		if err := rows.Scan(&version, &appliedAt, &durationMS); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"version": version, "applied_at": appliedAt, "duration_ms": durationMS,
		})
	}
	return out, rows.Err()
}

func checksum(b []byte) string {
	// Normalise line endings so a Windows checkout and a Linux CI runner agree.
	normalised := strings.ReplaceAll(string(b), "\r\n", "\n")
	h := sha256.Sum256([]byte(normalised))
	return hex.EncodeToString(h[:])
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
