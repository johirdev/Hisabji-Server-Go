package money_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/johirdev/Hisabji-Server/internal/core/money"
)

type row struct {
	Amount money.Amount `db:"amount"`
	Note   *string      `db:"note"`
}

// TestAmountScansFromBigint proves that money.Amount round-trips through a
// BIGINT column via pgx's struct scanner. Everything in the app depends on it.
func TestAmountScansFromBigint(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run the pgx integration check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx,
		`SELECT $1::bigint AS amount, NULL::text AS note`, money.MustParse("1500.50").Minor())
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[row])
	if err != nil {
		t.Fatalf("scan into money.Amount failed: %v", err)
	}
	if got.Amount.String() != "1500.50" {
		t.Errorf("got %s, want 1500.50", got.Amount)
	}

	// And as a query argument.
	var back int64
	if err := conn.QueryRow(ctx, `SELECT $1::bigint`, money.MustParse("99.99")).Scan(&back); err != nil {
		t.Fatalf("money.Amount as a query argument failed: %v", err)
	}
	if back != 9999 {
		t.Errorf("got %d, want 9999", back)
	}
}
