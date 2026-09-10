package domain_test

import (
	"context"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/johirdev/Hisabji-Server/internal/domain"
)

// TestStructsCoverEveryColumn catches schema drift.
//
// pgx's RowToStructByNameLax tolerates a struct FIELD with no matching column,
// but it fails hard on a COLUMN with no matching struct field:
//
//	struct doesn't have corresponding row field provider_payload
//
// So every `SELECT *` becomes a runtime 500 the moment a migration adds a
// column and nobody updates the struct. That is exactly the kind of break that
// ships green and fails in production, so it is checked here instead: add a
// column, forget the field, and this test tells you at build time.
//
// Set TEST_DATABASE_URL to run it. It is skipped otherwise, so `go test ./...`
// still works with no database.
func TestStructsCoverEveryColumn(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to check the structs against the live schema")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Every struct that is ever populated from a `SELECT *` on its table.
	cases := []struct {
		table string
		model any
	}{
		{"users", domain.User{}},
		{"sessions", domain.Session{}},
		{"categories", domain.Category{}},
		{"expenses", domain.Expense{}},
		{"incomes", domain.Income{}},
		{"recurring_rules", domain.RecurringRule{}},
		{"budgets", domain.Budget{}},
		{"budget_limits", domain.BudgetLimit{}},
		{"goals", domain.Goal{}},
		{"goal_contributions", domain.GoalContribution{}},
		{"features", domain.Feature{}},
		{"subscription_plans", domain.Plan{}},
		{"credit_packs", domain.CreditPack{}},
		{"subscriptions", domain.Subscription{}},
		{"credit_wallets", domain.Wallet{}},
		{"credit_ledger", domain.LedgerEntry{}},
		{"payments", domain.Payment{}},
		{"ai_insights", domain.Insight{}},
		{"forecasts", domain.Forecast{}},
		{"notifications", domain.Notification{}},
		{"user_devices", domain.Device{}},
		{"feedback", domain.Feedback{}},
	}

	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			columns, err := tableColumns(ctx, conn, tc.table)
			if err != nil {
				t.Fatalf("could not read the columns of %s: %v", tc.table, err)
			}
			if len(columns) == 0 {
				t.Fatalf("table %s does not exist — did a migration get renamed?", tc.table)
			}

			fields := structDBTags(tc.model)

			var missing []string
			for _, col := range columns {
				if !fields[col] {
					missing = append(missing, col)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Errorf(
					"%s has %d column(s) with no field on %T: %s\n"+
						"Every SELECT * into this struct will fail at runtime with\n"+
						"  \"struct doesn't have corresponding row field <name>\"\n"+
						"Add the field with a db tag (use json:\"-\" if it must not be exposed).",
					tc.table, len(missing), tc.model, strings.Join(missing, ", "))
			}
		})
	}
}

func tableColumns(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// structDBTags returns the set of column names a struct can receive. It mirrors
// pgx's matching rules: the `db` tag when present, otherwise the field name
// compared case-insensitively.
func structDBTags(model any) map[string]bool {
	t := reflect.TypeOf(model)
	out := map[string]bool{}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		tag := strings.SplitN(f.Tag.Get("db"), ",", 2)[0]
		switch tag {
		case "-":
			continue // explicitly not a column
		case "":
			out[strings.ToLower(f.Name)] = true
		default:
			out[tag] = true
		}
	}
	return out
}
