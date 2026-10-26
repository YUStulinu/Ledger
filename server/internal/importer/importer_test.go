package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// The synthetic statements (ml/ledger_ml/synth.py) and the truth they were generated from.
var synthetic = filepath.Join("..", "..", "..", "testdata", "synthetic")

type truthTx struct {
	Date     string `json:"date"`
	Account  string `json:"account"`
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
	Merchant string `json:"merchant"`
	Category string `json:"category"`
}

func loadTruth(t *testing.T) []truthTx {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(synthetic, "truth.json"))
	if err != nil {
		t.Skipf("synthetic data missing (python -m ledger_ml.synth): %v", err)
	}
	var truth struct {
		Transactions []truthTx `json:"transactions"`
	}
	if err := json.Unmarshal(data, &truth); err != nil {
		t.Fatal(err)
	}
	return truth.Transactions
}

func readFile(t *testing.T, name string) *Statement {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(synthetic, name))
	if err != nil {
		t.Fatal(err)
	}
	st, err := Read(name, data)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// matches compares a statement's rows with the truth's rows of one account (and period), in order.
func matches(t *testing.T, st *Statement, truth []truthTx, account string, from, to string) {
	t.Helper()
	var want []truthTx
	for _, tx := range truth {
		if tx.Account == account && tx.Date >= from && tx.Date <= to {
			want = append(want, tx)
		}
	}
	if len(st.Rows) != len(want) {
		t.Fatalf("%s: %d rows, want %d", st.Format, len(st.Rows), len(want))
	}
	for i, r := range st.Rows {
		w := want[i]
		if r.Date.Format("2006-01-02") != w.Date || int64(r.Amount) != w.Amount || r.Currency != w.Currency {
			t.Fatalf("%s row %d (line %d): %s %s %s, want %s %s %d", st.Format, i, r.Line, r.Date.Format("2006-01-02"), r.Amount, r.Currency,
				w.Date, w.Currency, w.Amount)
		}
	}
}

func TestBTCSV(t *testing.T) {
	truth := loadTruth(t)
	st := readFile(t, "bt_2025-04_2026-09.csv")
	if st.Bank != "BT" || st.Account != "RO49BTRLRONCRT0123456701" || st.Owner != "IONESCU MARIA" {
		t.Errorf("header: %+v", st)
	}
	matches(t, st, truth, "bt", "0000", "9999")
	if !strings.Contains(st.Rows[0].Description, "POPESCU ANDREI") {
		t.Errorf("description: %q", st.Rows[0].Description)
	}
}

func TestBTPDF(t *testing.T) {
	truth := loadTruth(t)
	st := readFile(t, "bt_2026-09.pdf")
	if st.Format != "bt-pdf" || st.Account != "RO49BTRLRONCRT0123456701" {
		t.Errorf("header: %+v", st)
	}
	matches(t, st, truth, "bt", "2026-09-01", "2026-09-30")
	if len(st.Opening) != 1 || len(st.Closing) != 1 {
		t.Errorf("balances: %v %v", st.Opening, st.Closing)
	}
}

func TestINGCSVOverlappingExports(t *testing.T) {
	truth := loadTruth(t)
	first := readFile(t, "ing_2025-04_2025-12.csv")
	second := readFile(t, "ing_2025-12_2026-09.csv")
	matches(t, first, truth, "ing", "2025-04-01", "2025-12-31")
	matches(t, second, truth, "ing", "2025-12-15", "2026-09-30")
	// Details from the continuation rows end up in the description.
	found := false
	for _, r := range first.Rows {
		if strings.Contains(r.Description, "Terminal:") && strings.Contains(r.Description, "Nr. card") {
			found = true
		}
	}
	if !found {
		t.Error("continuation rows were not joined into the description")
	}
}

func TestRevolutCSV(t *testing.T) {
	truth := loadTruth(t)
	st := readFile(t, "revolut_2025-04_2026-09.csv")
	matches(t, st, truth, "revolut", "0000", "9999")
	currencies := map[string]int{}
	for _, r := range st.Rows {
		currencies[r.Currency]++
	}
	if currencies["RON"] == 0 || currencies["EUR"] == 0 {
		t.Errorf("currencies: %v", currencies)
	}
	if len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], "skipped 1") {
		t.Errorf("the declined row should be skipped with a warning: %v", st.Warnings)
	}
}

func TestBalancesCatchAMisreadRow(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(synthetic, "bt_2025-04_2026-09.csv"))
	if err != nil {
		t.Skip(err)
	}
	lines := strings.Split(string(data), "\r\n")
	// Line 12 is a payment: misread the first digit of its debit, as a bad conversion would.
	debit := regexp.MustCompile(`,"(\d)[\d.]*,\d\d",,`)
	m := debit.FindStringSubmatchIndex(lines[11])
	if m == nil {
		t.Fatalf("line 12 is not a debit: %s", lines[11])
	}
	other := "7"
	if lines[11][m[2]:m[3]] == "7" {
		other = "8"
	}
	lines[11] = lines[11][:m[2]] + other + lines[11][m[3]:]
	_, err = Read("bt.csv", []byte(strings.Join(lines, "\r\n")))
	if err == nil || !strings.Contains(err.Error(), "line 12") {
		t.Fatalf("want a balance error at line 12, got %v", err)
	}
}

func TestUnknownFormat(t *testing.T) {
	_, err := Read("x.csv", []byte("a,b,c\n1,2,3\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown statement format") {
		t.Fatalf("got %v", err)
	}
}

func TestSemicolonsAndWindows1250(t *testing.T) {
	// An older BT export: semicolons, Windows-1250 text, signed debits.
	csv := "Banca Transilvania S.A.\r\nNumar cont:;RO49BTRLRONCRT0123456701\r\nSold initial:;\"100,00\"\r\n\r\n" +
		"Data tranzactie;Data valuta;Descriere;Referinta tranzactiei;Debit;Credit;Sold contabil\r\n" +
		"03.02.2026;03.02.2026;Plat\xe3 online;1;\"-25,50\";;\"74,50\"\r\n" +
		"04.02.2026;04.02.2026;Incasare;2;;\"1.000,00\";\"1.074,50\"\r\nSold final:;\"1.074,50\"\r\n"
	st, err := Read("old.csv", []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rows) != 2 || st.Rows[0].Amount != money.MustParse("-25,50") || st.Rows[1].Amount != 100000 {
		t.Fatalf("rows: %+v", st.Rows)
	}
	if st.Rows[0].Description != "Plată online" {
		t.Errorf("Windows-1250: %q", st.Rows[0].Description)
	}
	if st.Rows[0].Date != time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC) {
		t.Errorf("date: %v", st.Rows[0].Date)
	}
}

func TestRomanianDates(t *testing.T) {
	for in, want := range map[string]string{"05 martie 2026": "2026-03-05", "1 ian. 2025": "2025-01-01", "31 decembrie 2025": "2025-12-31"} {
		d, ok := parseRODate(in)
		if !ok || d.Format("2006-01-02") != want {
			t.Errorf("%q: %v %v", in, d, ok)
		}
	}
	if _, ok := parseRODate("05 marchie 2026"); ok {
		t.Error("misspelled month accepted")
	}
}

func TestPDFJoinsWrappedDescriptions(t *testing.T) {
	st := readFile(t, "bt_2026-09.pdf")
	for _, r := range st.Rows {
		if strings.HasPrefix(r.Description, "Plata la POS") && !strings.Contains(r.Description, "RRN:") {
			t.Fatalf("the wrapped part of the description is missing: %q", r.Description)
		}
	}
}
