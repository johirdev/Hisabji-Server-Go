package query

import (
	"strconv"
	"strings"
)

// Builder assembles a parameterised SELECT. Conditions are written with "?"
// placeholders and converted to Postgres $1..$N at Build time, so you never
// have to count argument positions by hand when a filter is conditional.
//
//	b := query.NewBuilder().
//	    Select("e.id", "e.amount", "c.name AS category_name").
//	    From("expenses e").
//	    LeftJoin("categories c ON c.id = e.category_id").
//	    Where("e.user_id = ?", userID)
//
//	opts.Apply(b)                      // search + filters + sort + pagination
//	sql, args := b.Build()
//
// Only the *values* ever come from user input. Every SQL fragment passed to
// Builder is written by us, and column names inside Apply come from the
// Schema whitelist.
type Builder struct {
	ctes     []frag
	distinct bool
	selects  []string
	from     frag
	joins    []frag
	wheres   []frag
	groups   []string
	havings  []frag
	orders   []string
	limit    int
	offset   int
	hasLimit bool
	forShare string
}

type frag struct {
	sql  string
	args []any
}

// NewBuilder returns an empty builder.
func NewBuilder() *Builder { return &Builder{} }

// With prepends a common table expression.
func (b *Builder) With(name, body string, args ...any) *Builder {
	b.ctes = append(b.ctes, frag{name + " AS (" + body + ")", args})
	return b
}

// Select sets (or extends) the projection.
func (b *Builder) Select(cols ...string) *Builder {
	b.selects = append(b.selects, cols...)
	return b
}

// Distinct adds SELECT DISTINCT.
func (b *Builder) Distinct() *Builder { b.distinct = true; return b }

// From sets the driving table, optionally with an alias: From("expenses e").
func (b *Builder) From(table string, args ...any) *Builder {
	b.from = frag{table, args}
	return b
}

// Join / LeftJoin add joins. Pass the full clause after the keyword.
func (b *Builder) Join(clause string, args ...any) *Builder {
	b.joins = append(b.joins, frag{"JOIN " + clause, args})
	return b
}

// LeftJoin adds a LEFT JOIN.
func (b *Builder) LeftJoin(clause string, args ...any) *Builder {
	b.joins = append(b.joins, frag{"LEFT JOIN " + clause, args})
	return b
}

// Where ANDs one condition onto the query.
func (b *Builder) Where(cond string, args ...any) *Builder {
	if cond == "" {
		return b
	}
	b.wheres = append(b.wheres, frag{cond, args})
	return b
}

// WhereIf applies the condition only when ok is true. This is what keeps
// repositories free of "if x != nil { ... }" placeholder bookkeeping.
func (b *Builder) WhereIf(ok bool, cond string, args ...any) *Builder {
	if ok {
		b.Where(cond, args...)
	}
	return b
}

// GroupBy adds grouping columns.
func (b *Builder) GroupBy(cols ...string) *Builder {
	b.groups = append(b.groups, cols...)
	return b
}

// Having ANDs a HAVING condition.
func (b *Builder) Having(cond string, args ...any) *Builder {
	b.havings = append(b.havings, frag{cond, args})
	return b
}

// OrderBy appends raw ORDER BY terms. Callers must not pass user input here;
// use Options.Apply, which resolves terms through the Schema whitelist.
func (b *Builder) OrderBy(terms ...string) *Builder {
	b.orders = append(b.orders, terms...)
	return b
}

// Limit sets LIMIT/OFFSET.
func (b *Builder) Limit(limit, offset int) *Builder {
	b.limit, b.offset, b.hasLimit = limit, offset, true
	return b
}

// ForUpdate adds row locking, for read-modify-write inside a transaction.
func (b *Builder) ForUpdate() *Builder { b.forShare = " FOR UPDATE"; return b }

// ForUpdateSkipLocked is the safe locking mode for queue-style workers.
func (b *Builder) ForUpdateSkipLocked() *Builder {
	b.forShare = " FOR UPDATE SKIP LOCKED"
	return b
}

// HasWhere reports whether any condition was added.
func (b *Builder) HasWhere() bool { return len(b.wheres) > 0 }

// Build renders the SELECT and its ordered argument list.
func (b *Builder) Build() (string, []any) {
	var sb strings.Builder
	args := make([]any, 0, 16)

	b.writeCTEs(&sb, &args)

	sb.WriteString("SELECT ")
	if b.distinct {
		sb.WriteString("DISTINCT ")
	}
	if len(b.selects) == 0 {
		sb.WriteString("*")
	} else {
		sb.WriteString(strings.Join(b.selects, ", "))
	}

	b.writeFromJoinsWhere(&sb, &args)

	if len(b.groups) > 0 {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(b.groups, ", "))
	}
	if len(b.havings) > 0 {
		sb.WriteString(" HAVING ")
		writeAnd(&sb, &args, b.havings)
	}
	if len(b.orders) > 0 {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(strings.Join(b.orders, ", "))
	}
	if b.hasLimit {
		sb.WriteString(" LIMIT ?")
		args = append(args, b.limit)
		if b.offset > 0 {
			sb.WriteString(" OFFSET ?")
			args = append(args, b.offset)
		}
	}
	sb.WriteString(b.forShare)

	return numbered(sb.String()), args
}

// BuildCount renders the matching COUNT(*) query: same FROM/JOIN/WHERE, but no
// ORDER BY, LIMIT or OFFSET, so the total is correct for the whole result set.
func (b *Builder) BuildCount() (string, []any) {
	var sb strings.Builder
	args := make([]any, 0, 16)

	b.writeCTEs(&sb, &args)

	if len(b.groups) > 0 {
		// Grouped queries need an outer count over the grouped rows.
		sb.WriteString("SELECT count(*) FROM (SELECT 1")
		b.writeFromJoinsWhere(&sb, &args)
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(b.groups, ", "))
		if len(b.havings) > 0 {
			sb.WriteString(" HAVING ")
			writeAnd(&sb, &args, b.havings)
		}
		sb.WriteString(") grouped_rows")
		return numbered(sb.String()), args
	}

	sb.WriteString("SELECT count(*)")
	b.writeFromJoinsWhere(&sb, &args)
	return numbered(sb.String()), args
}

func (b *Builder) writeCTEs(sb *strings.Builder, args *[]any) {
	if len(b.ctes) == 0 {
		return
	}
	sb.WriteString("WITH ")
	for i, c := range b.ctes {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.sql)
		*args = append(*args, c.args...)
	}
	sb.WriteString(" ")
}

func (b *Builder) writeFromJoinsWhere(sb *strings.Builder, args *[]any) {
	if b.from.sql != "" {
		sb.WriteString(" FROM ")
		sb.WriteString(b.from.sql)
		*args = append(*args, b.from.args...)
	}
	for _, j := range b.joins {
		sb.WriteString(" ")
		sb.WriteString(j.sql)
		*args = append(*args, j.args...)
	}
	if len(b.wheres) > 0 {
		sb.WriteString(" WHERE ")
		writeAnd(sb, args, b.wheres)
	}
}

func writeAnd(sb *strings.Builder, args *[]any, frags []frag) {
	for i, f := range frags {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString("(")
		sb.WriteString(f.sql)
		sb.WriteString(")")
		*args = append(*args, f.args...)
	}
}

// numbered rewrites "?" placeholders as $1, $2, ... It skips anything inside a
// single-quoted SQL literal and treats "??" as an escaped literal question mark
// (needed for Postgres JSON operators such as ?| and ?&).
func numbered(sql string) string {
	var sb strings.Builder
	sb.Grow(len(sql) + 16)
	n := 0
	inLiteral := false

	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		switch {
		case ch == '\'':
			inLiteral = !inLiteral
			sb.WriteByte(ch)
		case ch == '?' && !inLiteral:
			if i+1 < len(sql) && sql[i+1] == '?' {
				sb.WriteByte('?')
				i++
				continue
			}
			n++
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(n))
		default:
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}
