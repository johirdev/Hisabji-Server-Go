package query

import (
	"strings"
)

// Apply writes everything the client asked for onto the builder: the search
// OR-group, each validated filter, the date-range shortcut, ORDER BY and
// LIMIT/OFFSET. Call it after your own mandatory conditions (owner scoping).
func (o *Options) Apply(b *Builder) *Builder {
	o.ApplyFilters(b)
	o.ApplySort(b)
	o.ApplyPagination(b)
	return b
}

// ApplyFilters writes only the WHERE parts. Use this for the COUNT query and
// for aggregate queries, where sorting and pagination make no sense.
func (o *Options) ApplyFilters(b *Builder) *Builder {
	if o == nil {
		return b
	}

	// Soft delete is enforced here, not left to each repository to remember.
	if col := o.schema.SoftDeleteColumn; col != "" {
		b.Where(o.schema.Qualify(col) + " IS NULL")
	}

	// ?search=coffee  ->  (note ILIKE ? OR merchant ILIKE ? OR ...)
	if o.Search != "" && len(o.SearchFields) > 0 {
		pattern := likePattern(OpContains, o.Search)
		var parts []string
		args := make([]any, 0, len(o.SearchFields))
		for _, expr := range o.SearchFields {
			parts = append(parts, expr+"::text ILIKE ?")
			args = append(args, pattern)
		}
		b.Where(strings.Join(parts, " OR "), args...)
	}

	// ?date_from / ?date_to
	if o.schema.DateField != "" {
		if o.DateFrom != nil {
			b.Where(o.schema.DateField+" >= ?", *o.DateFrom)
		}
		if o.DateTo != nil {
			b.Where(o.schema.DateField+" <= ?", *o.DateTo)
		}
	}

	for _, f := range o.Filters {
		cond, args := f.SQL()
		b.Where(cond, args...)
	}
	return b
}

// ApplySort writes ORDER BY, falling back to the schema default.
func (o *Options) ApplySort(b *Builder) *Builder {
	sorts := o.Sorts
	if len(sorts) == 0 {
		sorts = o.schema.DefaultSort
	}
	for _, s := range sorts {
		col, ok := o.schema.Columns[s.Field]
		if !ok {
			continue // schema default referencing an undeclared column: ignore
		}
		term := col.DB
		if s.Desc {
			term += " DESC"
		} else {
			term += " ASC"
		}
		if s.NullsLast {
			term += " NULLS LAST"
		}
		b.OrderBy(term)
	}
	return b
}

// ApplyPagination writes LIMIT/OFFSET.
func (o *Options) ApplyPagination(b *Builder) *Builder {
	limit := o.Limit
	if limit <= 0 {
		limit = o.schema.defaultLimit()
	}
	b.Limit(limit, o.Offset)
	return b
}

// SQL renders one filter into a condition plus its arguments. The column
// expression comes from the Schema (trusted); every value is a placeholder.
func (f Filter) SQL() (string, []any) {
	col := f.Column.DB

	switch f.Op {
	case OpEq:
		return col + " = ?", f.Values
	case OpNe:
		// "IS DISTINCT FROM" so that ?status[ne]=paid also returns NULL rows,
		// which is what a user filtering "not paid" actually expects.
		return col + " IS DISTINCT FROM ?", f.Values
	case OpGt:
		return col + " > ?", f.Values
	case OpGte:
		return col + " >= ?", f.Values
	case OpLt:
		return col + " < ?", f.Values
	case OpLte:
		return col + " <= ?", f.Values
	case OpBetween:
		return col + " BETWEEN ? AND ?", f.Values
	case OpIn:
		return col + " IN (" + placeholders(len(f.Values)) + ")", f.Values
	case OpNotIn:
		return "(" + col + " IS NULL OR " + col + " NOT IN (" + placeholders(len(f.Values)) + "))", f.Values
	case OpLike:
		return col + "::text LIKE ?", f.Values
	case OpContains, OpStarts, OpEnds:
		return col + "::text ILIKE ?", f.Values
	case OpHas:
		return "? = ANY(" + col + ")", f.Values
	case OpIsNull:
		if b, ok := f.Values[0].(bool); ok && b {
			return col + " IS NULL", nil
		}
		return col + " IS NOT NULL", nil
	default:
		return col + " = ?", f.Values
	}
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
