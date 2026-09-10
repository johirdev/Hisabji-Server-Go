package query

import (
	"net/url"
	"strings"
	"testing"

	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
)

var testSchema = Schema{
	Table: "expenses", Alias: "e", OwnerColumn: "user_id",
	SoftDeleteColumn: "deleted_at",
	DateField:        "e.spent_at",
	Columns: Columns{
		"id":          {DB: "e.id", Type: TypeUUID, Filter: true, Sort: true},
		"amount":      {DB: "e.amount", Type: TypeMoney, Filter: true, Sort: true},
		"note":        {DB: "e.note", Type: TypeString, Filter: true, Search: true, Nullable: true},
		"category_id": {DB: "e.category_id", Type: TypeUUID, Filter: true},
		"method":      {DB: "e.payment_method", Type: TypeEnum, Filter: true, Enum: []string{"cash", "card", "bkash"}},
		"spent_at":    {DB: "e.spent_at", Type: TypeDate, Filter: true, Sort: true},
		"secret":      {DB: "e.secret", Type: TypeString}, // neither filterable nor sortable
	},
	DefaultSort: []Sort{{Field: "spent_at", Desc: true}, {Field: "id", Desc: true}},
}

func parse(t *testing.T, raw string) *Options {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("bad test query: %v", err)
	}
	opts, err := ParseValues(v, testSchema)
	if err != nil {
		t.Fatalf("ParseValues(%q) returned error: %v", raw, err)
	}
	return opts
}

func parseErr(t *testing.T, raw string) *apperr.Error {
	t.Helper()
	v, _ := url.ParseQuery(raw)
	_, err := ParseValues(v, testSchema)
	if err == nil {
		t.Fatalf("ParseValues(%q) should have failed", raw)
	}
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("error is not an *apperr.Error: %v", err)
	}
	return e
}

func TestPlaceholdersAreNumberedInOrder(t *testing.T) {
	b := NewBuilder().
		Select("e.id").
		From("expenses e").
		Where("e.user_id = ?", "u1").
		Where("e.amount >= ?", 100).
		Limit(20, 40)

	sql, args := b.Build()

	want := "SELECT e.id FROM expenses e WHERE (e.user_id = $1) AND (e.amount >= $2) LIMIT $3 OFFSET $4"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 4 || args[0] != "u1" || args[2] != 20 || args[3] != 40 {
		t.Errorf("args mismatch: %#v", args)
	}
}

func TestBuildCountDropsOrderAndLimit(t *testing.T) {
	b := NewBuilder().Select("e.id").From("expenses e").
		Where("e.user_id = ?", "u1").
		OrderBy("e.spent_at DESC").
		Limit(20, 0)

	sql, args := b.BuildCount()

	if strings.Contains(sql, "ORDER BY") || strings.Contains(sql, "LIMIT") {
		t.Errorf("count query must not paginate or sort: %s", sql)
	}
	if !strings.HasPrefix(sql, "SELECT count(*)") {
		t.Errorf("expected a count projection, got: %s", sql)
	}
	if len(args) != 1 {
		t.Errorf("count should carry only the where args, got %#v", args)
	}
}

func TestPlaceholderIsIgnoredInsideStringLiteral(t *testing.T) {
	sql, _ := NewBuilder().
		Select("'is it? yes' AS label").
		From("t").
		Where("a = ?", 1).
		Build()

	if !strings.Contains(sql, "'is it? yes'") {
		t.Errorf("question mark inside a literal must be left alone: %s", sql)
	}
	if !strings.Contains(sql, "a = $1") {
		t.Errorf("real placeholder should still be numbered: %s", sql)
	}
}

func TestOwnerScopeAndSoftDeleteAlwaysApplied(t *testing.T) {
	opts := parse(t, "amount[gte]=100")
	b := NewBuilder().From("expenses e").Where("e.user_id = ?", "u1")
	opts.ApplyFilters(b)
	sql, args := b.Build()

	if !strings.Contains(sql, "e.deleted_at IS NULL") {
		t.Errorf("soft delete predicate missing: %s", sql)
	}
	if args[0] != "u1" {
		t.Errorf("owner id must stay the first argument, got %#v", args)
	}
}

func TestMoneyFilterIsCoercedToMinorUnits(t *testing.T) {
	opts := parse(t, "amount[gte]=1500.50")
	if len(opts.Filters) != 1 {
		t.Fatalf("expected one filter, got %d", len(opts.Filters))
	}
	if got := opts.Filters[0].Values[0]; got != int64(150050) {
		t.Errorf("1500.50 should become 150050 paisa, got %#v", got)
	}
}

func TestBetweenAndIn(t *testing.T) {
	opts := parse(t, "amount[between]=100,500&method[in]=cash,bkash")

	var between, in Filter
	for _, f := range opts.Filters {
		switch f.Op {
		case OpBetween:
			between = f
		case OpIn:
			in = f
		}
	}

	cond, args := between.SQL()
	if cond != "e.amount BETWEEN ? AND ?" || len(args) != 2 {
		t.Errorf("between rendered wrong: %s %#v", cond, args)
	}
	cond, args = in.SQL()
	if cond != "e.payment_method IN (?, ?)" || len(args) != 2 {
		t.Errorf("in rendered wrong: %s %#v", cond, args)
	}
}

func TestSearchBuildsOrGroupOverSearchableColumnsOnly(t *testing.T) {
	opts := parse(t, "search=coffee")
	b := NewBuilder().From("expenses e")
	opts.ApplyFilters(b)
	sql, args := b.Build()

	if !strings.Contains(sql, "e.note::text ILIKE") {
		t.Errorf("searchable column missing from search group: %s", sql)
	}
	if strings.Contains(sql, "e.amount::text ILIKE") {
		t.Errorf("non-searchable column must not be searched: %s", sql)
	}
	if args[0] != "%coffee%" {
		t.Errorf("expected a contains pattern, got %#v", args[0])
	}
}

func TestSearchWildcardsAreEscaped(t *testing.T) {
	opts := parse(t, "search=100%25_off")
	b := NewBuilder().From("expenses e")
	opts.ApplyFilters(b)
	_, args := b.Build()

	pattern, _ := args[0].(string)
	if !strings.Contains(pattern, `\%`) || !strings.Contains(pattern, `\_`) {
		t.Errorf("LIKE wildcards must be escaped, got %q", pattern)
	}
}

func TestUnknownFilterFieldIsRejectedWithFieldDetail(t *testing.T) {
	e := parseErr(t, "nonsense=1")
	if e.Code != apperr.CodeValidation || e.Status != 422 {
		t.Errorf("expected a 422 VALIDATION_ERROR, got %d %s", e.Status, e.Code)
	}
	if len(e.Fields) == 0 || e.Fields[0].Field != "nonsense" {
		t.Errorf("error should name the offending parameter: %#v", e.Fields)
	}
}

func TestNonFilterableColumnIsRejected(t *testing.T) {
	e := parseErr(t, "secret=x")
	if e.Fields[0].Rule != "not_filterable" {
		t.Errorf("expected not_filterable, got %#v", e.Fields[0])
	}
}

func TestNonSortableColumnIsRejected(t *testing.T) {
	e := parseErr(t, "sort=secret")
	if len(e.Fields) == 0 || e.Fields[0].Field != "sort" {
		t.Errorf("expected a sort error, got %#v", e.Fields)
	}
}

func TestEnumValueOutsideAllowedSetIsRejected(t *testing.T) {
	e := parseErr(t, "method=crypto")
	if !strings.Contains(e.Fields[0].Message, "cash") {
		t.Errorf("error should list the allowed values, got %q", e.Fields[0].Message)
	}
}

func TestBadUUIDIsRejectedBeforeReachingPostgres(t *testing.T) {
	e := parseErr(t, "category_id=not-a-uuid")
	if e.Fields[0].Rule != "uuid" {
		t.Errorf("expected a uuid rule failure, got %#v", e.Fields[0])
	}
}

func TestLimitIsClampedToSchemaMaximum(t *testing.T) {
	v, _ := url.ParseQuery("limit=100000")
	_, err := ParseValues(v, testSchema)
	if err == nil {
		t.Fatal("an over-large limit should be reported to the caller")
	}
	e, _ := apperr.As(err)
	if e.Fields[0].Field != "limit" {
		t.Errorf("expected the limit field to be named, got %#v", e.Fields)
	}
}

func TestDefaultSortAppliesWhenClientSendsNone(t *testing.T) {
	opts := parse(t, "")
	b := NewBuilder().From("expenses e")
	opts.ApplySort(b)
	sql, _ := b.Build()

	if !strings.Contains(sql, "ORDER BY e.spent_at DESC, e.id DESC") {
		t.Errorf("default sort not applied: %s", sql)
	}
}

func TestDateToCoversTheWholeDay(t *testing.T) {
	opts := parse(t, "date_from=2026-01-01&date_to=2026-01-31")
	if opts.DateTo.Hour() != 23 || opts.DateTo.Minute() != 59 {
		t.Errorf("date_to should extend to end of day, got %s", opts.DateTo)
	}
}

func TestDateRangeOrderIsValidated(t *testing.T) {
	e := parseErr(t, "date_from=2026-02-01&date_to=2026-01-01")
	if e.Fields[0].Field != "date_to" {
		t.Errorf("expected date_to to be flagged, got %#v", e.Fields)
	}
}

func TestPaginationOffsetMath(t *testing.T) {
	opts := parse(t, "page=3&limit=25")
	if opts.Offset != 50 {
		t.Errorf("page 3 at 25 per page should offset 50, got %d", opts.Offset)
	}
}

func TestFilterOrderIsDeterministic(t *testing.T) {
	// Same filters, different query-string order, must produce identical SQL,
	// so Postgres can reuse one plan instead of one per permutation.
	a := parse(t, "amount[gte]=1&note=x&method=cash")
	b := parse(t, "method=cash&note=x&amount[gte]=1")

	ba := NewBuilder().From("expenses e")
	bb := NewBuilder().From("expenses e")
	a.ApplyFilters(ba)
	b.ApplyFilters(bb)

	sqlA, _ := ba.Build()
	sqlB, _ := bb.Build()
	if sqlA != sqlB {
		t.Errorf("filter order must be stable:\n%s\n%s", sqlA, sqlB)
	}
}

func TestIsUUID(t *testing.T) {
	cases := map[string]bool{
		"3f2504e0-4f89-11d3-9a0c-0305e82c3301": true,
		"3F2504E0-4F89-11D3-9A0C-0305E82C3301": true,
		"3f2504e0-4f89-11d3-9a0c-0305e82c330":  false,
		"3f2504e04f8911d39a0c0305e82c3301":     false,
		"":                                     false,
		"'; DROP TABLE users; --":              false,
	}
	for in, want := range cases {
		if got := IsUUID(in); got != want {
			t.Errorf("IsUUID(%q) = %v, want %v", in, got, want)
		}
	}
}
