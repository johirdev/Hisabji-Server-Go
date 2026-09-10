// Package repository provides a generic, owner-scoped data-access layer.
//
// Every finance table in Hisabji has the same shape — a uuid primary key, a
// user_id owner column, created_at/updated_at — so the CRUD SQL is identical
// apart from the table and column names. Base[T] writes that SQL once.
//
//	type Repository struct{ repository.Base[domain.Expense] }
//
//	func New(db repository.DB) *Repository {
//	    return &Repository{repository.Base[domain.Expense]{
//	        DB: db, Schema: ExpenseSchema, Resource: "Expense",
//	        SelectColumns: []string{"e.*"},
//	    }}
//	}
//
// A module then only writes the queries that are genuinely special (dashboard
// aggregates, analytics roll-ups), and inherits list/get/insert/update/delete.
//
// OWNERSHIP: every method takes an ownerID and adds `user_id = $n` to the
// WHERE clause. There is no code path that reads a row without that predicate,
// so one user can never reach another user's data through this layer.
package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/query"
)

// DB is the subset of pgxpool.Pool / pgx.Tx this package needs. Accepting the
// interface means every repository method works unchanged inside a
// transaction — see the txn package.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// identifier guards the dynamic column names used by Insert/Update. Those come
// from our own code, never from a request, but validating them keeps a future
// refactor from turning a map key into an injection point.
var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Base implements the shared data-access operations for one table.
type Base[T any] struct {
	DB     DB
	Schema query.Schema

	// Resource is the singular name used in "X was not found." messages.
	Resource string

	// SelectColumns is the projection for List/Get. Defaults to "alias.*".
	SelectColumns []string

	// Joins are extra clauses appended to every List/Get query, e.g.
	// []string{"LEFT JOIN categories c ON c.id = e.category_id"}. Use them to
	// populate denormalised display fields such as category_name.
	Joins []string

	// IDColumn is the primary key column name. Defaults to "id".
	IDColumn string
}

func (b *Base[T]) idColumn() string {
	if b.IDColumn == "" {
		return "id"
	}
	return b.IDColumn
}

func (b *Base[T]) qualifiedID() string { return b.Schema.Qualify(b.idColumn()) }

func (b *Base[T]) resource() string {
	if b.Resource == "" {
		return "Record"
	}
	return b.Resource
}

func (b *Base[T]) fromClause() string {
	if b.Schema.Alias != "" {
		return b.Schema.Table + " " + b.Schema.Alias
	}
	return b.Schema.Table
}

func (b *Base[T]) projection() []string {
	if len(b.SelectColumns) > 0 {
		return b.SelectColumns
	}
	if b.Schema.Alias != "" {
		return []string{b.Schema.Alias + ".*"}
	}
	return []string{"*"}
}

// newBuilder starts a query already scoped to the owner and to non-deleted
// rows. Nothing in this package builds a query any other way.
func (b *Base[T]) newBuilder(ownerID string) *query.Builder {
	qb := query.NewBuilder().From(b.fromClause())
	for _, j := range b.Joins {
		qb.LeftJoin(strings.TrimPrefix(strings.TrimPrefix(j, "LEFT JOIN "), "JOIN "))
	}
	if col := b.Schema.OwnerColumn; col != "" && ownerID != "" {
		qb.Where(b.Schema.Qualify(col)+" = ?", ownerID)
	}
	return qb
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// List returns one page of rows plus the total number of matches. The total is
// skipped (and returned as -1) when the client sent ?with_total=false, which
// halves the cost of an infinite-scroll feed.
func (b *Base[T]) List(ctx context.Context, ownerID string, opts *query.Options) ([]T, int64, error) {
	qb := b.newBuilder(ownerID).Select(b.projection()...)
	opts.Apply(qb)

	sql, args := qb.Build()
	rows, err := b.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, apperr.FromPostgres(err, b.resource())
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return nil, 0, apperr.FromPostgres(err, b.resource())
	}

	if !opts.WithTotal {
		return items, -1, nil
	}

	// Cheap short-circuit: a first page that is not full is the whole result.
	if opts.Page == 1 && len(items) < opts.Limit {
		return items, int64(len(items)), nil
	}

	total, err := b.Count(ctx, ownerID, opts)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Count returns how many rows match the filters, ignoring pagination.
func (b *Base[T]) Count(ctx context.Context, ownerID string, opts *query.Options) (int64, error) {
	qb := b.newBuilder(ownerID)
	if opts != nil {
		opts.ApplyFilters(qb)
	}
	sql, args := qb.BuildCount()

	var total int64
	if err := b.DB.QueryRow(ctx, sql, args...).Scan(&total); err != nil {
		return 0, apperr.FromPostgres(err, b.resource())
	}
	return total, nil
}

// Get loads one row by id, scoped to the owner. A row that belongs to somebody
// else is reported as NOT_FOUND, never as FORBIDDEN, so the API does not leak
// whether an id exists.
func (b *Base[T]) Get(ctx context.Context, ownerID, id string) (T, error) {
	var zero T
	qb := b.newBuilder(ownerID).
		Select(b.projection()...).
		Where(b.qualifiedID()+" = ?", id).
		Limit(1, 0)
	if col := b.Schema.SoftDeleteColumn; col != "" {
		qb.Where(b.Schema.Qualify(col) + " IS NULL")
	}

	sql, args := qb.Build()
	rows, err := b.DB.Query(ctx, sql, args...)
	if err != nil {
		return zero, apperr.FromPostgres(err, b.resource())
	}
	out, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, apperr.NotFound(b.resource())
		}
		return zero, apperr.FromPostgres(err, b.resource())
	}
	return out, nil
}

// Exists reports whether the owner has a row with this id.
func (b *Base[T]) Exists(ctx context.Context, ownerID, id string) (bool, error) {
	qb := b.newBuilder(ownerID).Select("1").Where(b.qualifiedID()+" = ?", id).Limit(1, 0)
	if col := b.Schema.SoftDeleteColumn; col != "" {
		qb.Where(b.Schema.Qualify(col) + " IS NULL")
	}
	sql, args := qb.Build()

	var one int
	err := b.DB.QueryRow(ctx, sql, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, apperr.FromPostgres(err, b.resource())
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// Insert writes one row from a column map and returns it fully populated
// (including database defaults such as id and created_at).
func (b *Base[T]) Insert(ctx context.Context, values map[string]any) (T, error) {
	var zero T
	if len(values) == 0 {
		return zero, apperr.Internal("Nothing to insert.")
	}

	cols, args, err := orderedColumns(values)
	if err != nil {
		return zero, err
	}

	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		b.Schema.Table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	rows, err := b.DB.Query(ctx, sql, args...)
	if err != nil {
		return zero, apperr.FromPostgres(err, b.resource())
	}
	out, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return zero, apperr.FromPostgres(err, b.resource())
	}
	return out, nil
}

// Update applies a partial change to one owned row and returns the new state.
// An empty map is a no-op error rather than an UPDATE with no SET clause.
func (b *Base[T]) Update(ctx context.Context, ownerID, id string, values map[string]any) (T, error) {
	var zero T
	if len(values) == 0 {
		return zero, apperr.BadRequest("No changes were provided.").
			WithHint("Send at least one field to update.")
	}

	cols, args, err := orderedColumns(values)
	if err != nil {
		return zero, err
	}

	sets := make([]string, 0, len(cols)+1)
	for i, c := range cols {
		sets = append(sets, fmt.Sprintf("%s = $%d", c, i+1))
	}
	sets = append(sets, "updated_at = now()")

	n := len(args)
	where := []string{fmt.Sprintf("%s = $%d", b.idColumn(), n+1)}
	args = append(args, id)
	if col := b.Schema.OwnerColumn; col != "" {
		where = append(where, fmt.Sprintf("%s = $%d", col, len(args)+1))
		args = append(args, ownerID)
	}
	if col := b.Schema.SoftDeleteColumn; col != "" {
		where = append(where, col+" IS NULL")
	}

	sql := fmt.Sprintf("UPDATE %s SET %s WHERE %s RETURNING *",
		b.Schema.Table, strings.Join(sets, ", "), strings.Join(where, " AND "))

	rows, err := b.DB.Query(ctx, sql, args...)
	if err != nil {
		return zero, apperr.FromPostgres(err, b.resource())
	}
	out, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, apperr.NotFound(b.resource())
		}
		return zero, apperr.FromPostgres(err, b.resource())
	}
	return out, nil
}

// Delete removes one owned row. When the schema declares a soft-delete column
// the row is marked instead, so historical analytics stay correct.
func (b *Base[T]) Delete(ctx context.Context, ownerID, id string) error {
	var sql string
	args := []any{id}

	where := b.idColumn() + " = $1"
	if col := b.Schema.OwnerColumn; col != "" {
		where += " AND " + col + " = $2"
		args = append(args, ownerID)
	}

	if col := b.Schema.SoftDeleteColumn; col != "" {
		sql = fmt.Sprintf("UPDATE %s SET %s = now(), updated_at = now() WHERE %s AND %s IS NULL",
			b.Schema.Table, col, where, col)
	} else {
		sql = fmt.Sprintf("DELETE FROM %s WHERE %s", b.Schema.Table, where)
	}

	tag, err := b.DB.Exec(ctx, sql, args...)
	if err != nil {
		return apperr.FromPostgres(err, b.resource())
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound(b.resource())
	}
	return nil
}

// DeleteMany removes several owned rows in one round trip and reports how many
// actually belonged to the caller.
func (b *Base[T]) DeleteMany(ctx context.Context, ownerID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	args := []any{ids}
	where := b.idColumn() + " = ANY($1)"
	if col := b.Schema.OwnerColumn; col != "" {
		where += " AND " + col + " = $2"
		args = append(args, ownerID)
	}

	var sql string
	if col := b.Schema.SoftDeleteColumn; col != "" {
		sql = fmt.Sprintf("UPDATE %s SET %s = now(), updated_at = now() WHERE %s AND %s IS NULL",
			b.Schema.Table, col, where, col)
	} else {
		sql = fmt.Sprintf("DELETE FROM %s WHERE %s", b.Schema.Table, where)
	}

	tag, err := b.DB.Exec(ctx, sql, args...)
	if err != nil {
		return 0, apperr.FromPostgres(err, b.resource())
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------
// Aggregates — shared by the dashboard and analytics modules
// ---------------------------------------------------------------------------

// SumWhere returns the sum of one numeric column over the rows matching opts.
// The result is cast to bigint so money columns (minor units) scan cleanly.
func (b *Base[T]) SumWhere(ctx context.Context, ownerID, column string, opts *query.Options) (int64, error) {
	if !identifier.MatchString(strings.TrimPrefix(column, b.Schema.Alias+".")) {
		return 0, apperr.Internal("Invalid aggregate column.")
	}
	qb := b.newBuilder(ownerID).Select("COALESCE(SUM(" + column + "), 0)::bigint")
	if opts != nil {
		opts.ApplyFilters(qb)
	}
	sql, args := qb.Build()

	var total int64
	if err := b.DB.QueryRow(ctx, sql, args...).Scan(&total); err != nil {
		return 0, apperr.FromPostgres(err, b.resource())
	}
	return total, nil
}

// Query runs an arbitrary statement and collects rows into R. Modules use it
// for the analytics queries that Base cannot express, while still getting the
// shared error translation.
func Query[R any](ctx context.Context, db DB, resource, sql string, args ...any) ([]R, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, apperr.FromPostgres(err, resource)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[R])
	if err != nil {
		return nil, apperr.FromPostgres(err, resource)
	}
	return out, nil
}

// QueryOne runs a statement expected to return exactly one row.
func QueryOne[R any](ctx context.Context, db DB, resource, sql string, args ...any) (R, error) {
	var zero R
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return zero, apperr.FromPostgres(err, resource)
	}
	out, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[R])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, apperr.NotFound(resource)
		}
		return zero, apperr.FromPostgres(err, resource)
	}
	return out, nil
}

// orderedColumns turns a column map into a deterministic (cols, args) pair.
// Deterministic ordering matters: it keeps the generated SQL text stable, so
// Postgres can reuse one prepared-statement plan instead of one per map order.
func orderedColumns(values map[string]any) ([]string, []any, error) {
	cols := make([]string, 0, len(values))
	for c := range values {
		if !identifier.MatchString(c) {
			return nil, nil, apperr.Internal(fmt.Sprintf("Invalid column name %q.", c))
		}
		cols = append(cols, c)
	}
	sort.Strings(cols)

	args := make([]any, len(cols))
	for i, c := range cols {
		args[i] = values[c]
	}
	return cols, args, nil
}
