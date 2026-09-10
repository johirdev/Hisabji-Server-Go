// Package money holds every currency amount in the system.
//
// Design decision: money is stored and computed as an int64 count of MINOR
// UNITS (paisa for BDT, cents for USD) — never float64. Floats silently lose
// precision (0.1 + 0.2 != 0.3), which is unacceptable for a finance app where
// a user compares our totals against their own arithmetic.
//
//	Database column : BIGINT           (e.g. 150050)
//	JSON on the wire: number, 2 dp     (e.g. 1500.50)
//	Go type         : money.Amount     (int64 paisa)
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Amount is a monetary value in minor units (paisa). Amount(150050) == ৳1500.50
type Amount int64

// Scale is how many minor units make one major unit.
const Scale = 100

// Zero is the additive identity.
const Zero Amount = 0

// FromMajor builds an Amount from whole currency units: FromMajor(1500) == ৳1500.00
func FromMajor(v int64) Amount { return Amount(v * Scale) }

// FromMinor builds an Amount from minor units directly.
func FromMinor(v int64) Amount { return Amount(v) }

// Minor returns the raw minor-unit count (what goes into the BIGINT column).
func (a Amount) Minor() int64 { return int64(a) }

// Major returns the whole-unit part, truncated toward zero.
func (a Amount) Major() int64 { return int64(a) / Scale }

// Float returns a float64 for display/statistics ONLY. Never accumulate with it.
func (a Amount) Float() float64 { return float64(a) / float64(Scale) }

// String renders "1500.50" — no currency symbol, no thousands separators.
func (a Amount) String() string {
	neg := a < 0
	v := int64(a)
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d.%02d", v/Scale, v%Scale)
	if neg {
		return "-" + s
	}
	return s
}

// IsZero / IsPositive / IsNegative read better than comparing to 0 inline.
func (a Amount) IsZero() bool     { return a == 0 }
func (a Amount) IsPositive() bool { return a > 0 }
func (a Amount) IsNegative() bool { return a < 0 }

// Add, Sub and Neg are exact.
func (a Amount) Add(b Amount) Amount { return a + b }
func (a Amount) Sub(b Amount) Amount { return a - b }
func (a Amount) Neg() Amount         { return -a }

// Abs returns the magnitude.
func (a Amount) Abs() Amount {
	if a < 0 {
		return -a
	}
	return a
}

// MulInt scales by a whole number (e.g. 30 days of a daily limit).
func (a Amount) MulInt(n int64) Amount { return Amount(int64(a) * n) }

// DivInt splits by a whole number, rounding half away from zero. Use for
// "safe to spend per day" style calculations.
func (a Amount) DivInt(n int64) Amount {
	if n == 0 {
		return 0
	}
	v := int64(a)
	q := v / n
	r := v % n
	if r != 0 && (r*2 >= n || -r*2 >= n) {
		if (v < 0) != (n < 0) {
			q--
		} else {
			q++
		}
	}
	return Amount(q)
}

// Percent returns p percent of a, rounded half away from zero.
// Example: budget.Percent(80) == the 80% warning threshold.
func (a Amount) Percent(p float64) Amount {
	return Amount(roundHalf(float64(a) * p / 100))
}

// RatioPercent returns what percentage a is of total, as a float rounded to
// 2 decimals. Returns 0 when total is zero, so callers never divide by zero.
func (a Amount) RatioPercent(total Amount) float64 {
	if total == 0 {
		return 0
	}
	v := float64(a) * 100 / float64(total)
	return float64(int64(v*100+copySign(0.5, v))) / 100
}

// Sum adds any number of amounts exactly.
func Sum(items ...Amount) Amount {
	var t Amount
	for _, v := range items {
		t += v
	}
	return t
}

// Max / Min pick between two amounts.
func Max(a, b Amount) Amount {
	if a > b {
		return a
	}
	return b
}
func Min(a, b Amount) Amount {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// JSON — accepts 1500.5, 1500.50, "1500.50", 1500 and null.
// ---------------------------------------------------------------------------

// MarshalJSON writes the amount as a JSON number with exactly two decimals.
func (a Amount) MarshalJSON() ([]byte, error) { return []byte(a.String()), nil }

// UnmarshalJSON parses decimal text EXACTLY — it never round-trips through
// float64, so "0.07" can never arrive as 6.999999 paisa.
func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` || s == "" {
		*a = 0
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
		if s == "" {
			*a = 0
			return nil
		}
	}
	v, err := Parse(s)
	if err != nil {
		return err
	}
	*a = v
	return nil
}

// ErrInvalidAmount is returned for text that is not a decimal number.
var ErrInvalidAmount = errors.New("money: value must be a number such as 1500 or 1500.50")

// Parse converts decimal text into an Amount, rounding half away from zero at
// the second decimal place. Accepts leading +/-, underscores and commas are not.
func Parse(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrInvalidAmount
	}
	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return 0, ErrInvalidAmount
	}

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		intPart = "0"
	}
	// Only digits may remain: ParseInt would happily accept a second sign, so
	// "--5" must be rejected here rather than becoming +5.
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return 0, ErrInvalidAmount
		}
	}
	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, ErrInvalidAmount
	}

	var frac int64
	if hasFrac {
		for _, r := range fracPart {
			if r < '0' || r > '9' {
				return 0, ErrInvalidAmount
			}
		}
		switch {
		case len(fracPart) == 0:
			frac = 0
		case len(fracPart) == 1:
			frac, _ = strconv.ParseInt(fracPart, 10, 64)
			frac *= 10
		default:
			frac, _ = strconv.ParseInt(fracPart[:2], 10, 64)
			// round half up on the third decimal
			if len(fracPart) > 2 && fracPart[2] >= '5' {
				frac++
			}
		}
	}

	total := whole*Scale + frac
	if neg {
		total = -total
	}
	return Amount(total), nil
}

// MustParse is Parse for constants and tests; it panics on bad input.
func MustParse(s string) Amount {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

func roundHalf(f float64) int64 { return int64(f + copySign(0.5, f)) }

func copySign(mag, sign float64) float64 {
	if sign < 0 {
		return -mag
	}
	return mag
}
