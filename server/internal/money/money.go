// Package money holds amounts as whole minor units (bani, cents), never as floats, and parses the
// number formats bank statements use.
package money

import (
	"fmt"
	"strconv"
	"strings"
)

// Amount is a signed number of minor units: 12345 is 123.45.
type Amount int64

// FromFloat rounds to the nearest minor unit (for values computed elsewhere, such as conversions).
func FromFloat(f float64) Amount {
	if f < 0 {
		return -Amount(-f*100 + 0.5)
	}
	return Amount(f*100 + 0.5)
}

func (a Amount) Float() float64 { return float64(a) / 100 }

// String formats with a decimal point and no grouping: "-1234.50".
func (a Amount) String() string {
	sign := ""
	v := int64(a)
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// RO formats as Romanian statements do: "-1.234,50".
func (a Amount) RO() string {
	sign := ""
	v := int64(a)
	if v < 0 {
		sign, v = "-", -v
	}
	whole := strconv.FormatInt(v/100, 10)
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	return fmt.Sprintf("%s%s,%02d", sign, b.String(), v%100)
}

// Parse reads an amount in either convention: "1.234,56" (Romanian), "1,234.56", "1234.56",
// "-12,5", "+300". The decimal separator is the last '.' or ',' followed by one or two digits;
// any other separators group thousands. Currency codes and spaces around it are ignored.
func Parse(s string) (Amount, error) {
	orig := s
	s = strings.TrimSpace(s)
	for _, cur := range []string{"RON", "LEI", "EUR", "USD", "GBP"} {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, cur), cur))
	}
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	whole, frac := s, ""
	if i := strings.LastIndexAny(s, ".,"); i >= 0 {
		tail := s[i+1:]
		if len(tail) == 1 || len(tail) == 2 {
			whole, frac = s[:i], tail
		}
	}
	whole = strings.NewReplacer(".", "", ",", "", "'", "").Replace(whole)
	if whole == "" {
		whole = "0"
	}
	for _, part := range []string{whole, frac} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("not an amount: %q", orig)
			}
		}
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not an amount: %q", orig)
	}
	f := int64(0)
	if frac != "" {
		f, _ = strconv.ParseInt(frac, 10, 64)
		if len(frac) == 1 {
			f *= 10
		}
	}
	v := Amount(w*100 + f)
	if neg {
		v = -v
	}
	return v, nil
}

// MustParse is Parse for constants in tests and tables.
func MustParse(s string) Amount {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return a
}
