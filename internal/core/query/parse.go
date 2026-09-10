package query

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/money"
)

// reserved lists query parameters that are never treated as column filters.
var reserved = map[string]bool{
	"page": true, "limit": true, "per_page": true, "size": true, "offset": true,
	"search": true, "q": true,
	"sort": true, "order": true, "order_by": true, "sort_by": true, "direction": true,
	"fields": true, "select": true, "include": true, "expand": true,
	"date_from": true, "date_to": true, "from": true, "to": true,
	"with_total": true, "count": true,
	// harmless extras browsers/analytics append
	"_": true, "t": true, "timestamp": true,
}

// Parse reads the full query string of the request and validates it against
// the schema. Every failure is a 422 that names the exact parameter, so the
// frontend can show the message beside the offending filter control.
func Parse(c *gin.Context, s Schema) (*Options, error) {
	return ParseValues(c.Request.URL.Query(), s)
}

// ParseValues is Parse without gin, so jobs and tests can use it too.
func ParseValues(values url.Values, s Schema) (*Options, error) {
	o := &Options{schema: s, WithTotal: true}
	var fieldErrs []apperr.FieldError

	// ---- pagination ------------------------------------------------------
	page, err := positiveInt(values, 1, "page")
	if err != nil {
		fieldErrs = append(fieldErrs, *err)
	}
	limit, err := positiveInt(values, s.defaultLimit(), "limit", "per_page", "size")
	if err != nil {
		fieldErrs = append(fieldErrs, *err)
	}
	if limit > s.maxLimit() {
		fieldErrs = append(fieldErrs, apperr.FieldError{
			Field: "limit", Rule: "max", Value: limit,
			Message:   fmt.Sprintf("limit cannot be greater than %d.", s.maxLimit()),
			MessageBN: fmt.Sprintf("limit সর্বোচ্চ %d হতে পারে।", s.maxLimit()),
		})
		limit = s.maxLimit()
	}
	o.Page, o.Limit = page, limit
	o.Offset = (page - 1) * limit

	// ---- search ----------------------------------------------------------
	o.Search = strings.TrimSpace(firstOf(values, "search", "q"))
	if o.Search != "" {
		minLen := s.SearchMinLength
		if minLen <= 0 {
			minLen = 1
		}
		if len([]rune(o.Search)) < minLen {
			fieldErrs = append(fieldErrs, apperr.FieldError{
				Field: "search", Rule: "min", Value: o.Search,
				Message:   fmt.Sprintf("search must be at least %d characters.", minLen),
				MessageBN: fmt.Sprintf("search কমপক্ষে %d অক্ষরের হতে হবে।", minLen),
			})
		}
		if len(o.Search) > 200 {
			o.Search = o.Search[:200]
		}
		for _, name := range sortedKeys(s.Columns) {
			if col := s.Columns[name]; col.Search {
				o.SearchFields = append(o.SearchFields, col.DB)
			}
		}
		if len(o.SearchFields) == 0 {
			fieldErrs = append(fieldErrs, apperr.FieldError{
				Field: "search", Rule: "unsupported",
				Message:   "Search is not available on this resource.",
				MessageBN: "এই resource-এ search সুবিধা নেই।",
			})
		}
	}

	// ---- sort ------------------------------------------------------------
	sortRaw := firstOf(values, "sort", "order_by", "sort_by", "order")
	if sortRaw != "" {
		sorts, errs := parseSort(sortRaw, s)
		fieldErrs = append(fieldErrs, errs...)
		o.Sorts = sorts
	}
	if len(o.Sorts) == 0 {
		o.Sorts = s.DefaultSort
	}

	// ---- date range shortcut --------------------------------------------
	if s.DateField != "" {
		if raw := firstOf(values, "date_from", "from"); raw != "" {
			t, e := parseDate(raw, false)
			if e != nil {
				fieldErrs = append(fieldErrs, apperr.FieldError{
					Field: "date_from", Rule: "date", Value: raw,
					Message:   "date_from must be a date such as 2026-01-31.",
					MessageBN: "date_from অবশ্যই 2026-01-31 এই format-এ দিতে হবে।",
				})
			} else {
				o.DateFrom = &t
			}
		}
		if raw := firstOf(values, "date_to", "to"); raw != "" {
			t, e := parseDate(raw, true)
			if e != nil {
				fieldErrs = append(fieldErrs, apperr.FieldError{
					Field: "date_to", Rule: "date", Value: raw,
					Message:   "date_to must be a date such as 2026-01-31.",
					MessageBN: "date_to অবশ্যই 2026-01-31 এই format-এ দিতে হবে।",
				})
			} else {
				o.DateTo = &t
			}
		}
		if o.DateFrom != nil && o.DateTo != nil && o.DateTo.Before(*o.DateFrom) {
			fieldErrs = append(fieldErrs, apperr.FieldError{
				Field: "date_to", Rule: "after",
				Message:   "date_to must be the same as or after date_from.",
				MessageBN: "date_to অবশ্যই date_from-এর সমান বা পরে হতে হবে।",
			})
		}
	}

	// ---- sparse fieldsets / relation expansion ---------------------------
	o.Fields = splitList(firstOf(values, "fields", "select"))
	o.Include = splitList(firstOf(values, "include", "expand"))

	// ---- with_total ------------------------------------------------------
	if raw := firstOf(values, "with_total", "count"); raw != "" {
		if b, e := parseBool(raw); e == nil {
			o.WithTotal = b
		}
	}

	// ---- column filters --------------------------------------------------
	for key := range values {
		name, op, ok := splitFilterKey(key)
		if !ok || reserved[name] {
			continue
		}
		col, exists := s.Columns[name]
		if !exists {
			fieldErrs = append(fieldErrs, apperr.FieldError{
				Field: key, Rule: "unknown_filter", Value: values.Get(key),
				Message:   fmt.Sprintf("%q is not a filterable field. Allowed: %s.", name, strings.Join(filterableNames(s), ", ")),
				MessageBN: fmt.Sprintf("%q নামে কোনো filter নেই।", name),
			})
			continue
		}
		if !col.Filter {
			fieldErrs = append(fieldErrs, apperr.FieldError{
				Field: key, Rule: "not_filterable",
				Message:   fmt.Sprintf("Filtering by %q is not allowed.", name),
				MessageBN: fmt.Sprintf("%q দিয়ে filter করা যাবে না।", name),
			})
			continue
		}
		if op == "" {
			op = defaultOp(col.Type)
		}
		if !opAllowed(col, op) {
			fieldErrs = append(fieldErrs, apperr.FieldError{
				Field: key, Rule: "operator",
				Message: fmt.Sprintf("Operator %q is not supported on %q. Supported: %s.",
					op, name, strings.Join(opNames(allowedOps(col)), ", ")),
				MessageBN: fmt.Sprintf("%q field-এ %q operator চলবে না।", name, op),
			})
			continue
		}

		raw := values.Get(key)
		f, fe := buildFilter(name, key, col, op, raw)
		if fe != nil {
			fieldErrs = append(fieldErrs, *fe)
			continue
		}
		o.Filters = append(o.Filters, f)
	}

	if len(o.Filters) > s.maxFilters() {
		fieldErrs = append(fieldErrs, apperr.FieldError{
			Field: "filters", Rule: "max",
			Message:   fmt.Sprintf("At most %d filters may be applied at once.", s.maxFilters()),
			MessageBN: fmt.Sprintf("একসাথে সর্বোচ্চ %d টি filter দেওয়া যাবে।", s.maxFilters()),
		})
	}

	// Deterministic order so generated SQL (and its plan cache entry) is stable.
	sort.Slice(o.Filters, func(i, j int) bool {
		if o.Filters[i].Field == o.Filters[j].Field {
			return o.Filters[i].Op < o.Filters[j].Op
		}
		return o.Filters[i].Field < o.Filters[j].Field
	})

	if len(fieldErrs) > 0 {
		return nil, apperr.Validation("Some query parameters are invalid.", fieldErrs...).
			WithHint("Fix the listed query parameters and retry.")
	}
	return o, nil
}

// splitFilterKey turns "amount[gte]" into ("amount", "gte", true) and a plain
// "amount" into ("amount", "", true).
func splitFilterKey(key string) (name string, op Operator, ok bool) {
	open := strings.IndexByte(key, '[')
	if open < 0 {
		return key, "", key != ""
	}
	if !strings.HasSuffix(key, "]") || open == 0 {
		return "", "", false
	}
	return key[:open], Operator(strings.ToLower(key[open+1 : len(key)-1])), true
}

func defaultOp(t Type) Operator {
	if t == TypeString {
		return OpContains
	}
	return OpEq
}

// allowedOps returns the operator set valid for a column.
func allowedOps(c Column) []Operator {
	if len(c.Ops) > 0 {
		return c.Ops
	}
	base := []Operator{OpEq, OpNe, OpIn, OpNotIn}
	switch c.Type {
	case TypeString:
		base = append(base, OpContains, OpStarts, OpEnds, OpLike)
	case TypeInt, TypeMoney, TypeFloat, TypeDate, TypeTime:
		base = append(base, OpGt, OpGte, OpLt, OpLte, OpBetween)
	case TypeBool:
		base = []Operator{OpEq, OpNe}
	case TypeUUID, TypeEnum:
		// eq/ne/in/nin only
	case TypeArray:
		base = []Operator{OpHas, OpIn}
	}
	if c.Nullable {
		base = append(base, OpIsNull)
	}
	return base
}

func opAllowed(c Column, op Operator) bool {
	for _, v := range allowedOps(c) {
		if v == op {
			return true
		}
	}
	return false
}

func opNames(ops []Operator) []string {
	out := make([]string, len(ops))
	for i, v := range ops {
		out[i] = string(v)
	}
	return out
}

func filterableNames(s Schema) []string {
	var out []string
	for _, name := range sortedKeys(s.Columns) {
		if s.Columns[name].Filter {
			out = append(out, name)
		}
	}
	return out
}

func sortableNames(s Schema) []string {
	var out []string
	for _, name := range sortedKeys(s.Columns) {
		if s.Columns[name].Sort {
			out = append(out, name)
		}
	}
	return out
}

func sortedKeys(m Columns) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildFilter coerces the raw text into driver-ready values.
func buildFilter(name, key string, col Column, op Operator, raw string) (Filter, *apperr.FieldError) {
	f := Filter{Field: name, Column: col, Op: op, Raw: raw}

	switch op {
	case OpIsNull:
		b, err := parseBool(raw)
		if err != nil {
			return f, &apperr.FieldError{Field: key, Rule: "boolean", Value: raw,
				Message: "Value must be true or false.", MessageBN: "মান true অথবা false হতে হবে।"}
		}
		f.Values = []any{b}
		return f, nil

	case OpIn, OpNotIn:
		parts := splitList(raw)
		if len(parts) == 0 {
			return f, &apperr.FieldError{Field: key, Rule: "required", Value: raw,
				Message: "Provide at least one comma-separated value.", MessageBN: "কমপক্ষে একটি মান দিন।"}
		}
		if len(parts) > 100 {
			return f, &apperr.FieldError{Field: key, Rule: "max", Value: len(parts),
				Message: "At most 100 values may be listed.", MessageBN: "সর্বোচ্চ ১০০টি মান দেওয়া যাবে।"}
		}
		for _, p := range parts {
			v, err := coerce(col, p)
			if err != nil {
				return f, fieldErr(key, col, p, err)
			}
			f.Values = append(f.Values, v)
		}
		return f, nil

	case OpBetween:
		parts := splitList(raw)
		if len(parts) != 2 {
			return f, &apperr.FieldError{Field: key, Rule: "between", Value: raw,
				Message:   "between expects exactly two comma-separated values, e.g. 100,500.",
				MessageBN: "between-এ ঠিক দুটি মান দিতে হবে, যেমন 100,500।"}
		}
		lo, err := coerce(col, parts[0])
		if err != nil {
			return f, fieldErr(key, col, parts[0], err)
		}
		// For date columns the upper bound should cover the whole day.
		hi, err := coerceBound(col, parts[1], true)
		if err != nil {
			return f, fieldErr(key, col, parts[1], err)
		}
		f.Values = []any{lo, hi}
		return f, nil

	case OpContains, OpStarts, OpEnds, OpLike:
		if raw == "" {
			return f, &apperr.FieldError{Field: key, Rule: "required",
				Message: "Value cannot be empty.", MessageBN: "মান খালি রাখা যাবে না।"}
		}
		f.Values = []any{likePattern(op, raw)}
		return f, nil

	case OpHas:
		f.Values = []any{raw}
		return f, nil

	default: // eq, ne, gt, gte, lt, lte
		upper := op == OpLte || op == OpLt
		v, err := coerceBound(col, raw, upper && col.Type == TypeTime)
		if err != nil {
			return f, fieldErr(key, col, raw, err)
		}
		f.Values = []any{v}
		return f, nil
	}
}

func fieldErr(key string, col Column, raw string, err error) *apperr.FieldError {
	fe := &apperr.FieldError{Field: key, Value: raw, Message: err.Error()}
	switch col.Type {
	case TypeInt:
		fe.Rule, fe.MessageBN = "integer", "মানটি একটি পূর্ণসংখ্যা হতে হবে।"
	case TypeMoney, TypeFloat:
		fe.Rule, fe.MessageBN = "numeric", "মানটি একটি সংখ্যা হতে হবে।"
	case TypeBool:
		fe.Rule, fe.MessageBN = "boolean", "মান true অথবা false হতে হবে।"
	case TypeUUID:
		fe.Rule, fe.MessageBN = "uuid", "মানটি সঠিক id নয়।"
	case TypeDate, TypeTime:
		fe.Rule, fe.MessageBN = "date", "তারিখ 2026-01-31 format-এ দিতে হবে।"
	case TypeEnum:
		fe.Rule, fe.MessageBN = "enum", "অনুমোদিত মানের বাইরে।"
	default:
		fe.Rule = "invalid"
	}
	return fe
}

// coerce converts one raw string into the Go value the driver expects.
func coerce(col Column, raw string) (any, error) { return coerceBound(col, raw, false) }

func coerceBound(col Column, raw string, endOfDay bool) (any, error) {
	raw = strings.TrimSpace(raw)
	switch col.Type {
	case TypeInt:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q must be a whole number.", raw)
		}
		return v, nil

	case TypeMoney:
		v, err := money.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%q must be an amount such as 1500 or 1500.50.", raw)
		}
		return v.Minor(), nil

	case TypeFloat:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q must be a number.", raw)
		}
		return v, nil

	case TypeBool:
		v, err := parseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q must be true or false.", raw)
		}
		return v, nil

	case TypeUUID:
		if !IsUUID(raw) {
			return nil, fmt.Errorf("%q is not a valid id.", raw)
		}
		return raw, nil

	case TypeDate, TypeTime:
		t, err := parseDate(raw, endOfDay)
		if err != nil {
			return nil, fmt.Errorf("%q must be a date such as 2026-01-31.", raw)
		}
		if col.Type == TypeDate {
			return t.Format("2006-01-02"), nil
		}
		return t, nil

	case TypeEnum:
		for _, allowed := range col.Enum {
			if strings.EqualFold(allowed, raw) {
				return allowed, nil
			}
		}
		return nil, fmt.Errorf("%q is not allowed. Allowed values: %s.", raw, strings.Join(col.Enum, ", "))

	default:
		if len(raw) > 500 {
			return nil, fmt.Errorf("value is too long (max 500 characters).")
		}
		return raw, nil
	}
}

// likePattern escapes LIKE wildcards in user input so a client cannot turn a
// prefix search into a full table scan with "%%%%%".
func likePattern(op Operator, raw string) string {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(raw)
	switch op {
	case OpStarts:
		return esc + "%"
	case OpEnds:
		return "%" + esc
	default:
		return "%" + esc + "%"
	}
}

func parseSort(raw string, s Schema) ([]Sort, []apperr.FieldError) {
	var out []Sort
	var errs []apperr.FieldError
	seen := map[string]bool{}

	for _, part := range splitList(raw) {
		desc := false
		field := part
		switch {
		case strings.HasPrefix(part, "-"):
			desc, field = true, part[1:]
		case strings.HasPrefix(part, "+"):
			field = part[1:]
		default:
			// also accept "field:desc" / "field asc"
			if f, dir, ok := strings.Cut(part, ":"); ok {
				field = strings.TrimSpace(f)
				desc = strings.EqualFold(strings.TrimSpace(dir), "desc")
			} else if f, dir, ok := strings.Cut(part, " "); ok {
				field = strings.TrimSpace(f)
				desc = strings.EqualFold(strings.TrimSpace(dir), "desc")
			}
		}
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		col, ok := s.Columns[field]
		if !ok || !col.Sort {
			errs = append(errs, apperr.FieldError{
				Field: "sort", Rule: "not_sortable", Value: field,
				Message:   fmt.Sprintf("Cannot sort by %q. Sortable fields: %s.", field, strings.Join(sortableNames(s), ", ")),
				MessageBN: fmt.Sprintf("%q দিয়ে sort করা যাবে না।", field),
			})
			continue
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, Sort{Field: field, Desc: desc, NullsLast: col.Nullable})
		if len(out) >= 4 {
			break // more than 4 sort keys is always an index-less scan
		}
	}
	return out, errs
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func firstOf(values url.Values, keys ...string) string {
	for _, k := range keys {
		if v := values.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func positiveInt(values url.Values, fallback int, keys ...string) (int, *apperr.FieldError) {
	raw := firstOf(values, keys...)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return fallback, &apperr.FieldError{
			Field: keys[0], Rule: "positive_integer", Value: raw,
			Message:   fmt.Sprintf("%s must be a whole number of 1 or more.", keys[0]),
			MessageBN: fmt.Sprintf("%s অবশ্যই ১ বা তার বেশি সংখ্যা হতে হবে।", keys[0]),
		}
	}
	return v, nil
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean")
}

var dateLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"02-01-2006",
	"02/01/2006",
}

// parseDate accepts several common shapes. When endOfDay is true and the input
// carried no time component, the result is 23:59:59.999999999 so that a
// ?date_to=2026-01-31 filter includes everything spent on the 31st.
func parseDate(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range dateLayouts {
		t, err := time.Parse(layout, raw)
		if err != nil {
			continue
		}
		dateOnly := layout == "2006-01-02" || layout == "02-01-2006" || layout == "02/01/2006"
		if dateOnly && endOfDay {
			return t.Add(24*time.Hour - time.Nanosecond), nil
		}
		return t, nil
	}
	// unix seconds
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 100000000 {
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised date format")
}

func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsUUID validates the canonical 8-4-4-4-12 hex form without pulling in a
// dependency. Rejecting a malformed id here produces a clean 422 instead of a
// confusing "invalid input syntax for type uuid" 500 from Postgres.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}
