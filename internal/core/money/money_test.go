package money

import (
	"encoding/json"
	"testing"
)

func TestParseKeepsExactPaisa(t *testing.T) {
	cases := map[string]int64{
		"0":        0,
		"1500":     150000,
		"1500.5":   150050,
		"1500.50":  150050,
		"0.07":     7,
		"0.01":     1,
		"-250.25":  -25025,
		"+99.99":   9999,
		".5":       50,
		"1500.505": 150051, // rounds half up on the third decimal
		"1500.504": 150050,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) failed: %v", in, err)
		}
		if got.Minor() != want {
			t.Errorf("Parse(%q) = %d, want %d", in, got.Minor(), want)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "1,500", "1.2.3", "12a", "--5", "1e5"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should have failed", in)
		}
	}
}

func TestFloatArithmeticWouldLoseMoneyButAmountDoesNot(t *testing.T) {
	// The exact case that makes float money a bug: 0.1 + 0.2 != 0.3.
	a, b := MustParse("0.10"), MustParse("0.20")
	if got := a.Add(b); got != MustParse("0.30") {
		t.Errorf("0.10 + 0.20 = %s, want 0.30", got)
	}

	// A month of daily expenses must total exactly.
	var total Amount
	for i := 0; i < 30; i++ {
		total = total.Add(MustParse("33.33"))
	}
	if total.String() != "999.90" {
		t.Errorf("30 x 33.33 = %s, want 999.90", total)
	}
}

func TestStringAlwaysHasTwoDecimals(t *testing.T) {
	cases := map[Amount]string{
		0:      "0.00",
		5:      "0.05",
		50:     "0.50",
		150000: "1500.00",
		150050: "1500.50",
		-25025: "-250.25",
		-5:     "-0.05",
	}
	for in, want := range cases {
		if got := in.String(); got != want {
			t.Errorf("Amount(%d).String() = %q, want %q", int64(in), got, want)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	type payload struct {
		Amount Amount `json:"amount"`
	}

	// Numbers, strings and integers are all accepted from clients.
	for _, body := range []string{
		`{"amount":1500.50}`,
		`{"amount":"1500.50"}`,
		`{"amount":1500.5}`,
	} {
		var p payload
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if p.Amount.Minor() != 150050 {
			t.Errorf("unmarshal %s gave %d paisa", body, p.Amount.Minor())
		}
	}

	// And we always emit a plain JSON number with two decimals.
	out, err := json.Marshal(payload{Amount: 150050})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"amount":1500.50}` {
		t.Errorf("marshal gave %s", out)
	}
}

func TestNullAndEmptyBecomeZero(t *testing.T) {
	var a Amount
	for _, body := range []string{"null", `""`} {
		if err := a.UnmarshalJSON([]byte(body)); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if a != 0 {
			t.Errorf("%s should decode to zero, got %s", body, a)
		}
	}
}

func TestDivIntRoundsHalfAwayFromZero(t *testing.T) {
	// "Safe to spend per day": ৳1000 over 3 days.
	if got := MustParse("1000").DivInt(3); got.String() != "333.33" {
		t.Errorf("1000/3 = %s, want 333.33", got)
	}
	if got := MustParse("10.00").DivInt(4); got.String() != "2.50" {
		t.Errorf("10/4 = %s, want 2.50", got)
	}
	if got := MustParse("100").DivInt(0); got != 0 {
		t.Errorf("division by zero must be safe, got %s", got)
	}
}

func TestPercentAndRatio(t *testing.T) {
	budget := MustParse("30000")
	if got := budget.Percent(80); got.String() != "24000.00" {
		t.Errorf("80%% of 30000 = %s", got)
	}

	spent := MustParse("22500")
	if got := spent.RatioPercent(budget); got != 75 {
		t.Errorf("22500 of 30000 = %v%%, want 75", got)
	}
	if got := spent.RatioPercent(0); got != 0 {
		t.Errorf("ratio against a zero budget must be 0, got %v", got)
	}
}

func TestSumAndHelpers(t *testing.T) {
	total := Sum(MustParse("3000"), MustParse("4000"), MustParse("10500"))
	if total.String() != "17500.00" {
		t.Errorf("sum = %s", total)
	}
	if !MustParse("-1").IsNegative() || !MustParse("0").IsZero() || !MustParse("1").IsPositive() {
		t.Error("sign helpers are wrong")
	}
	if MustParse("-250.25").Abs().String() != "250.25" {
		t.Error("Abs is wrong")
	}
}
