// Package query turns an HTTP query string into safe, parameterised SQL.
//
// It exists so that adding a new list endpoint is a *declaration*, not code:
// you describe the columns once in a Schema, and search, filtering, sorting,
// date ranges and pagination all work for free — with validation errors that
// name the exact offending query parameter.
//
//	var ExpenseSchema = query.Schema{
//	    Table: "expenses", Alias: "e", OwnerColumn: "user_id",
//	    Columns: query.Columns{
//	        "amount":   {DB: "e.amount",   Type: query.TypeMoney,  Filter: true, Sort: true},
//	        "note":     {DB: "e.note",     Type: query.TypeString, Search: true},
//	        "spent_at": {DB: "e.spent_at", Type: query.TypeDate,   Filter: true, Sort: true},
//	    },
//	    DefaultSort: []query.Sort{{Field: "spent_at", Desc: true}},
//	}
//
// SECURITY: no value from the URL is ever concatenated into SQL. Column names
// come only from the Schema map (a whitelist) and every value becomes a $N
// placeholder. That makes SQL injection structurally impossible here.
package query

import "time"

// Type tells the parser how to coerce and validate a raw query-string value.
type Type int

const (
	TypeString Type = iota // varchar/text
	TypeInt                // integer/bigint
	TypeMoney              // BIGINT minor units; accepts "1500.50" from clients
	TypeFloat              // double precision / numeric
	TypeBool               // boolean; accepts true/false/1/0/yes/no
	TypeUUID               // uuid; validated before it reaches the driver
	TypeDate               // date (YYYY-MM-DD)
	TypeTime               // timestamptz (RFC3339 or YYYY-MM-DD)
	TypeEnum               // varchar constrained to Column.Enum
	TypeArray              // text[] column — supports the "has" operator
)

// Operator is a comparison the client may ask for via field[op]=value.
type Operator string

const (
	OpEq       Operator = "eq"       // field=value            or field[eq]=value
	OpNe       Operator = "ne"       // field[ne]=value
	OpGt       Operator = "gt"       // field[gt]=100
	OpGte      Operator = "gte"      // field[gte]=100
	OpLt       Operator = "lt"       // field[lt]=100
	OpLte      Operator = "lte"      // field[lte]=100
	OpIn       Operator = "in"       // field[in]=a,b,c
	OpNotIn    Operator = "nin"      // field[nin]=a,b
	OpLike     Operator = "like"     // case-sensitive contains
	OpContains Operator = "contains" // case-insensitive contains (default text op)
	OpStarts   Operator = "starts"   // case-insensitive prefix
	OpEnds     Operator = "ends"     // case-insensitive suffix
	OpBetween  Operator = "between"  // field[between]=10,50 (inclusive)
	OpIsNull   Operator = "null"     // field[null]=true / false
	OpHas      Operator = "has"      // array column contains the value
)

// Column declares one filterable/sortable/searchable field.
type Column struct {
	// DB is the real SQL expression. It is NEVER derived from user input, so
	// it may safely be a qualified column ("e.amount") or an expression
	// ("(e.amount * e.qty)"). Keep it free of user data.
	DB string

	Type Type

	Filter bool // may appear as ?field=... / ?field[op]=...
	Sort   bool // may appear in ?sort=
	Search bool // included in the OR-group built from ?search=

	// Enum lists the only accepted values when Type == TypeEnum. A value
	// outside the list produces a 422 naming the field and the allowed set.
	Enum []string

	// Ops optionally narrows which operators are allowed on this column.
	// Empty means "every operator valid for the Type".
	Ops []Operator

	// Nullable allows ?field[null]=true.
	Nullable bool
}

// Columns maps the public (JSON/query-string) field name to its declaration.
type Columns map[string]Column

// Sort is one ORDER BY term.
type Sort struct {
	Field string // public field name; must exist in Columns with Sort:true
	Desc  bool
	// NullsLast places NULLs at the end regardless of direction. Useful for
	// optional columns such as note or target_date.
	NullsLast bool
}

// Schema declares everything a list endpoint allows.
type Schema struct {
	Table string // "expenses"
	Alias string // "e" — used to qualify OwnerColumn and the soft-delete column

	Columns Columns

	// DefaultSort applies when the client sends no ?sort=. Always include a
	// unique tiebreaker (usually id) so pagination is stable across pages.
	DefaultSort []Sort

	DefaultLimit int // used when ?limit= is absent (falls back to 20)
	MaxLimit     int // hard ceiling so nobody can ask for a million rows (falls back to 100)

	// OwnerColumn scopes every query to the caller. When set, the repository
	// forces alias.owner_column = $1 into the WHERE clause, so a client can
	// never read another user rows even by crafting filters.
	OwnerColumn string

	// SoftDeleteColumn, when set, appends "alias.col IS NULL" automatically.
	SoftDeleteColumn string

	// DateField is the column the ?date_from / ?date_to shortcut applies to.
	DateField string

	// SearchMinLength rejects a ?search= shorter than this (default 1).
	SearchMinLength int

	// MaxFilters caps how many filter conditions one request may send, so a
	// hostile client cannot build a pathological query. Defaults to 25.
	MaxFilters int
}

// Qualify prefixes a bare column name with the schema alias.
func (s Schema) Qualify(col string) string {
	if s.Alias != "" {
		return s.Alias + "." + col
	}
	return col
}

func (s Schema) defaultLimit() int {
	if s.DefaultLimit > 0 {
		return s.DefaultLimit
	}
	return 20
}

func (s Schema) maxLimit() int {
	if s.MaxLimit > 0 {
		return s.MaxLimit
	}
	return 100
}

func (s Schema) maxFilters() int {
	if s.MaxFilters > 0 {
		return s.MaxFilters
	}
	return 25
}

// Filter is one parsed, validated and type-coerced condition.
type Filter struct {
	Field  string   // public name (for echoing back in meta)
	Column Column   // resolved declaration
	Op     Operator // validated against the column type
	Values []any    // already coerced to the Go type the driver expects
	Raw    string   // original text, echoed in meta.filters
}

// Options is the fully parsed query string.
type Options struct {
	Page   int
	Limit  int
	Offset int

	Search       string
	SearchFields []string // resolved DB expressions of columns with Search:true

	Sorts   []Sort
	Filters []Filter

	// DateFrom/DateTo come from the ?date_from / ?date_to shortcut.
	DateFrom *time.Time
	DateTo   *time.Time

	// Fields is the ?fields= sparse-fieldset request. The repository may use
	// it to trim the SELECT list; handlers may use it to trim the JSON.
	Fields []string

	// Include is the ?include= relation-expansion request (e.g. "category").
	Include []string

	// WithTotal reports whether the client wants the (more expensive) COUNT.
	// Defaults to true; ?with_total=false skips it for infinite-scroll UIs.
	WithTotal bool

	schema Schema
}

// Schema returns the schema this Options was parsed against.
func (o *Options) Schema() Schema { return o.schema }

// SortString rebuilds the canonical ?sort= value, for echoing in meta.
func (o *Options) SortString() string {
	out := ""
	for i, s := range o.Sorts {
		if i > 0 {
			out += ","
		}
		if s.Desc {
			out += "-"
		}
		out += s.Field
	}
	return out
}

// FilterMap echoes the applied filters so the frontend can render active chips.
func (o *Options) FilterMap() map[string]any {
	if len(o.Filters) == 0 && o.DateFrom == nil && o.DateTo == nil {
		return nil
	}
	m := make(map[string]any, len(o.Filters)+2)
	for _, f := range o.Filters {
		key := f.Field
		if f.Op != OpEq {
			key = f.Field + "[" + string(f.Op) + "]"
		}
		m[key] = f.Raw
	}
	if o.DateFrom != nil {
		m["date_from"] = o.DateFrom.Format("2006-01-02")
	}
	if o.DateTo != nil {
		m["date_to"] = o.DateTo.Format("2006-01-02")
	}
	return m
}

// HasInclude reports whether the client asked to expand a relation.
func (o *Options) HasInclude(name string) bool {
	for _, v := range o.Include {
		if v == name {
			return true
		}
	}
	return false
}
