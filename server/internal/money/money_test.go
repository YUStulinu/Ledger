package money

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Amount{
		"1.234,56":     123456,
		"1,234.56":     123456,
		"1234.56":      123456,
		"1234,56":      123456,
		"-12,5":        -1250,
		"+300":         30000,
		"12.345.678,9": 1234567890,
		"0,05":         5,
		"1.000":        100000, // three digits after the separator: thousands
		"2,500":        250000,
		"  45 RON ":    4500,
		"EUR -3.20":    -320,
		"(15,00)":      -1500,
		"1 234,00":     123400,
		"-0.99":        -99,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "12a,00", "--5", "1,2,3x"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestFormat(t *testing.T) {
	if s := Amount(-123456).RO(); s != "-1.234,56" {
		t.Errorf("RO: %s", s)
	}
	if s := Amount(5).String(); s != "0.05" {
		t.Errorf("String: %s", s)
	}
	if s := Amount(100000000).RO(); s != "1.000.000,00" {
		t.Errorf("RO: %s", s)
	}
	if a := FromFloat(-0.125); a != -13 {
		t.Errorf("FromFloat: %d", a)
	}
}
