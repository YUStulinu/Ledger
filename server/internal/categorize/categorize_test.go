package categorize

import (
	"testing"

	"github.com/YUStulinu/Ledger/server/internal/store"
)

func TestRules(t *testing.T) {
	cases := []struct {
		desc   string
		amount int64
		want   string
	}{
		{"Comision administrare cont", -500, "Fees"},
		{"Comision retragere numerar alte banci", -2500, "Fees"},
		{"Plata la POS non-BT cu card VISA;POS 01/04/2025 TID:1 PROFI 2643 BUCURESTI RO;valoare tranzactie: 159,81 RON RRN:1 comision tranzactie 0,00 RON;", -15981, ""},
		{"Retragere numerar ATM;ATM BT 4821 BUCURESTI RO;valoare tranzactie: 300,00 RON;", -30000, "Cash"},
		{"Dobanda; Dobanda cont economii", 350, "Other Income"},
		{"Incasare OP - canal electronic;NORTHWIND SOFTWARE SRL;RO02BACX0000001234567890;Salariu martie 2026;REF: 1", 1085000, "Salary"},
		{"Salariu", -100, ""}, // paying a salary is not earning one
		{"Top-up by *1234", 50000, "Transfer"},
		{"Plata online cu card VISA;NETFLIX.COM AMSTERDAM RO;valoare tranzactie: 49,99 RON;", -4999, ""},
	}
	for _, c := range cases {
		if got := Rule(store.Unlabeled{Description: c.desc, Amount: c.amount}); got != c.want {
			t.Errorf("%q: %q, want %q", c.desc, got, c.want)
		}
	}
}
