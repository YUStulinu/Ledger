package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// Statements from banks Ledger has no reader of its own for, in testdata/international.
func readIntl(t *testing.T, name string, o Options) *Statement {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "international", name))
	if err != nil {
		t.Fatal(err)
	}
	st, err := ReadWith(name, data, o)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func amounts(st *Statement) []money.Amount {
	var out []money.Amount
	for _, r := range st.Rows {
		out = append(out, r.Amount)
	}
	return out
}

func TestWiseNewestFirstWithBalances(t *testing.T) {
	st := readIntl(t, "wise-eur.csv", Options{})
	if st.Format != "generic-csv" || st.Currency != "EUR" || len(st.Rows) != 3 {
		t.Fatalf("%s %s %d rows", st.Format, st.Currency, len(st.Rows))
	}
	// Put in the order the balances follow (oldest first), dates read day-first (28-11-2026).
	if got := amounts(st); got[0] != -1299 || got[2] != -4210 {
		t.Fatalf("order: %v", got)
	}
	if st.Rows[0].Date.Format("2006-01-02") != "2026-11-20" || !strings.Contains(st.Rows[2].Description, "Albert Heijn") {
		t.Fatalf("%v %q", st.Rows[0].Date, st.Rows[2].Description)
	}
}

func TestChaseUSDates(t *testing.T) {
	st := readIntl(t, "chase-checking.csv", Options{Currency: "USD", Account: "Chase Checking"})
	if st.Account != "Chase Checking" || st.Rows[0].Date.Format("2006-01-02") != "2026-11-14" || st.Rows[2].Amount != -645 {
		t.Fatalf("%s %v %v", st.Account, st.Rows[0].Date, amounts(st))
	}
}

func TestCardExportNeedsFlipSign(t *testing.T) {
	// Charges are positive and there is no balance to tell: the caller says so.
	st := readIntl(t, "amex-card.csv", Options{Currency: "USD", FlipSign: true})
	if got := amounts(st); got[0] != -2340 || got[2] != 11052 {
		t.Fatalf("%v", got)
	}
	if !strings.Contains(strings.Join(st.Warnings, " "), "no running balance") {
		t.Fatalf("warn that nothing could be checked: %v", st.Warnings)
	}
}

func TestSignFoundFromBalances(t *testing.T) {
	// The same Chase file with charges written positive: only flipped do the balances add up.
	data, _ := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "international", "chase-checking.csv"))
	s := strings.NewReplacer(",-6.45,", ",6.45,", ",2500.00,", ",-2500.00,", ",-95.00,", ",95.00,").Replace(string(data))
	st, err := ReadWith("chase.csv", []byte(s), Options{Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if got := amounts(st); got[0] != -9500 || got[1] != 250000 {
		t.Fatalf("%v", got)
	}
	if !strings.Contains(strings.Join(st.Warnings, " "), "opposite sign") {
		t.Fatalf("say the sign was turned: %v", st.Warnings)
	}
}

func TestMonzoCurrencyColumn(t *testing.T) {
	st := readIntl(t, "monzo-gbp.csv", Options{})
	if st.Currency != "GBP" || st.Rows[2].Amount != 190000 || !strings.Contains(st.Rows[1].Description, "TESCO") {
		t.Fatalf("%s %v %q", st.Currency, amounts(st), st.Rows[1].Description)
	}
}

func TestAmbiguousDatesNeedAFormat(t *testing.T) {
	csv := "Date,Description,Amount\n03/04/2026,A,-1.00\n05/06/2026,B,-2.00\n"
	_, err := ReadWith("x.csv", []byte(csv), Options{Currency: "EUR"})
	if err == nil || !strings.Contains(err.Error(), "--date-format") {
		t.Fatalf("ambiguous dates must ask for --date-format: %v", err)
	}
	st, err := ReadWith("x.csv", []byte(csv), Options{Currency: "EUR", DateFormat: "MDY"})
	if err != nil || st.Rows[0].Date.Format("2006-01-02") != "2026-03-04" {
		t.Fatalf("%v %v", err, st)
	}
}

func TestMappedColumns(t *testing.T) {
	csv := "Buchung;Text;Soll;Haben\n01.11.2026;Miete;850,00;\n02.11.2026;Lohn;;2.800,00\n"
	st, err := ReadWith("bank.csv", []byte(csv), Options{Currency: "EUR",
		Map: map[string]string{"date": "Buchung", "description": "Text", "debit": "Soll", "credit": "Haben"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Format != "mapped-csv" || amounts(st)[0] != -85000 || amounts(st)[1] != 280000 {
		t.Fatalf("%s %v", st.Format, amounts(st))
	}
}

func TestOFX(t *testing.T) {
	st := readIntl(t, "bank.ofx", Options{})
	if st.Format != "ofx" || st.Currency != "USD" || st.Account != "9876543210" || st.Bank != "Sample Bank" || len(st.Rows) != 3 {
		t.Fatalf("%+v", st)
	}
	if st.Opening["USD"] != 300000 || st.Closing["USD"] != 459580 || st.Rows[0].Description != "SHELL OIL 5741 FUEL" {
		t.Fatalf("opening %v closing %v %q", st.Opening, st.Closing, st.Rows[0].Description)
	}
}

func TestCAMT053(t *testing.T) {
	st := readIntl(t, "camt053.xml", Options{})
	if st.Account != "DE89370400440532013000" || st.Bank != "Commerzbank" || st.Currency != "EUR" || len(st.Rows) != 3 {
		t.Fatalf("%s %s %s %d (the pending entry is left out)", st.Account, st.Bank, st.Currency, len(st.Rows))
	}
	if st.Rows[1].Description != "Hausverwaltung Schmidt Miete November" || st.Rows[1].Amount != -85000 {
		t.Fatalf("%q %v", st.Rows[1].Description, st.Rows[1].Amount)
	}
	// A wrong amount breaks opening + entries = closing.
	data, _ := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "international", "camt053.xml"))
	bad := strings.Replace(string(data), "<Amt Ccy=\"EUR\">16.60</Amt>", "<Amt Ccy=\"EUR\">61.60</Amt>", 1)
	if _, err := Read("bad.xml", []byte(bad)); err == nil || !strings.Contains(err.Error(), "do not add up") {
		t.Fatalf("a wrong amount must fail the balance check: %v", err)
	}
}
