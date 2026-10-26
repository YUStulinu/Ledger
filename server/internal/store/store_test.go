package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var synthetic = filepath.Join("..", "..", "..", "testdata", "synthetic")

var files = []string{
	"bt_2025-04_2026-09.csv",
	"bt_2026-09.pdf", // overlaps the CSV's last month
	"ing_2025-04_2025-12.csv",
	"ing_2025-12_2026-09.csv", // overlaps the first ING export by two weeks
	"revolut_2025-04_2026-09.csv",
}

type truthTx struct {
	ID       int    `json:"id"`
	Date     string `json:"date"`
	Account  string `json:"account"`
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Channel  string `json:"channel"`
	Pair     *int   `json:"pair"`
}

type truth struct {
	Transactions []truthTx          `json:"transactions"`
	Accounts     map[string]string  `json:"accounts"`
	Rates        map[string]float64 `json:"rates"`
	Openings     map[string]any     `json:"openings"`
}

func loadTruth(t *testing.T) truth {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(synthetic, "truth.json"))
	if err != nil {
		t.Skipf("synthetic data missing: %v", err)
	}
	var tr truth
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

// The key each truth transaction gets in the store (the account as the statement names it).
func (tr truth) keys() map[string]truthTx {
	// BT statements carry the IBAN; the ING export and Revolut's do not, so those accounts go by the bank's name.
	accountID := map[string]string{"bt": tr.Accounts["bt"], "ing": "ING", "revolut": "Revolut"}
	seen := map[string]int{}
	out := map[string]truthTx{}
	for _, tx := range tr.Transactions {
		base := fmt.Sprintf("%s|%s|%s|%d", accountID[tx.Account], tx.Currency, tx.Date, tx.Amount)
		out[fmt.Sprintf("%s|%d", base, seen[base])] = tx
		seen[base]++
	}
	return out
}

func importAll(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(synthetic, f))
		if err != nil {
			t.Skip(err)
		}
		if _, err := s.Import(ctx, f, data); err != nil {
			t.Fatal(err)
		}
	}
	rates, err := os.ReadFile(filepath.Join(synthetic, "rates.csv"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRates(rates)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRates(ctx, parsed); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestImportDeduplicates(t *testing.T) {
	tr := loadTruth(t)
	s := importAll(t)
	ctx := context.Background()
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM bank_lines").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(tr.Transactions) {
		t.Fatalf("%d bank lines, want %d (overlapping files must not add duplicates)", n, len(tr.Transactions))
	}
	// Every stored line is a true transaction.
	keys := tr.keys()
	rows, err := s.DB.Query("SELECT key FROM bank_lines")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		rows.Scan(&k)
		if _, ok := keys[k]; !ok {
			t.Errorf("line %s is not in the truth", k)
		}
	}
	// The same file again is recognized as such.
	data, _ := os.ReadFile(filepath.Join(synthetic, files[0]))
	res, err := s.Import(ctx, files[0], data)
	if err != nil || !res.AlreadyImported {
		t.Fatalf("re-import: %+v %v", res, err)
	}
}

func TestLedgerBalancesAndPairs(t *testing.T) {
	tr := loadTruth(t)
	s := importAll(t)
	ctx := context.Background()
	stats, err := s.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transfers, exchanges := 0, 0
	for _, tx := range tr.Transactions {
		if tx.Pair != nil && tx.Amount < 0 {
			if tx.Kind == "transfer" {
				transfers++
			} else if tx.Kind == "exchange" {
				exchanges++
			}
		}
	}
	if stats.Transfers != transfers || stats.Exchanges != exchanges {
		t.Errorf("matched %d transfers and %d exchanges, want %d and %d", stats.Transfers, stats.Exchanges, transfers, exchanges)
	}
	if stats.UnmatchedTransfers != 0 {
		t.Errorf("%d unmatched transfers", stats.UnmatchedTransfers)
	}
	// Double entry: every transaction balances in every currency.
	var unbalanced int
	if err := s.DB.QueryRow(`SELECT count(*) FROM (SELECT tx_id, currency FROM ledger_postings GROUP BY ALL HAVING sum(amount) <> 0)`).Scan(&unbalanced); err != nil {
		t.Fatal(err)
	}
	if unbalanced != 0 {
		t.Errorf("%d transactions do not balance", unbalanced)
	}
	// The accounts end where the statements end.
	final := map[string]int64{}
	op := tr.Openings
	final["bt|RON"] = int64(op["bt"].(float64))
	final["ing|RON"] = int64(op["ing"].(float64))
	rev := op["revolut"].(map[string]any)
	final["revolut|RON"], final["revolut|EUR"] = int64(rev["RON"].(float64)), int64(rev["EUR"].(float64))
	for _, tx := range tr.Transactions {
		final[tx.Account+"|"+tx.Currency] += tx.Amount
	}
	bank := map[string]string{"bt": "BT", "ing": "ING", "revolut": "Revolut"}
	for k, want := range final {
		var acct, cur string
		fmt.Sscanf(k, "%s", &acct)
		parts := splitKey(k)
		acct, cur = parts[0], parts[1]
		var got float64
		if err := s.DB.QueryRow("SELECT balance FROM balances WHERE bank = ? AND currency = ?", bank[acct], cur).Scan(&got); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if int64(got*100+0.5*sign(got)) != want {
			t.Errorf("%s: balance %.2f, want %.2f", k, got, float64(want)/100)
		}
	}
}

func splitKey(k string) []string {
	for i := range k {
		if k[i] == '|' {
			return []string{k[:i], k[i+1:]}
		}
	}
	return []string{k, ""}
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

func TestViewsAnswerWithTrueCategories(t *testing.T) {
	tr := loadTruth(t)
	s := importAll(t)
	ctx := context.Background()
	// Label every line with its true category, then compare a question's answer with the truth.
	var labels []Label
	for k, tx := range tr.keys() {
		if tx.Kind != "transfer" && tx.Kind != "exchange" {
			labels = append(labels, Label{k, tx.Category, SourceUser, 1})
		}
	}
	if _, err := s.SetLabels(ctx, labels); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	// Groceries in March 2026 (all RON).
	var want int64
	for _, tx := range tr.Transactions {
		if tx.Category == "Groceries" && tx.Date >= "2026-03-01" && tx.Date <= "2026-03-31" {
			want -= tx.Amount
		}
	}
	var got float64
	if err := s.DB.QueryRow("SELECT sum(amount_ron) FROM expenses WHERE category = 'Groceries' AND year = 2026 AND month = 3").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if int64(got*100+0.5) != want {
		t.Errorf("groceries in March 2026: %.2f, want %.2f", got, float64(want)/100)
	}
	// Restaurants in August 2025 include the euro meals in Barcelona, converted at each day's rate.
	var wantRON float64
	for _, tx := range tr.Transactions {
		if tx.Category == "Restaurants" && tx.Date >= "2025-08-01" && tx.Date <= "2025-08-31" {
			rate := 1.0
			if tx.Currency == "EUR" {
				rate = rateOn(tr.Rates, tx.Date)
			}
			wantRON += -float64(tx.Amount) / 100 * rate
		}
	}
	if err := s.DB.QueryRow("SELECT sum(amount_ron) FROM expenses WHERE category = 'Restaurants' AND month_start = DATE '2025-08-01'").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if d := got - wantRON; d > 0.05 || d < -0.05 {
		t.Errorf("restaurants in August 2025: %.2f RON, want %.2f", got, wantRON)
	}
}

// rateOn is the last rate published on or before the day.
func rateOn(rates map[string]float64, day string) float64 {
	best, rate := "", 0.0
	for d, r := range rates {
		if d <= day && d > best {
			best, rate = d, r
		}
	}
	return rate
}

func TestExternalAccessIsOff(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{
		"SELECT * FROM read_csv('../../go.mod')",
		"COPY (SELECT 1) TO 'x.csv'",
		"SET enable_external_access = true",
		"ATTACH 'other.duckdb'",
	} {
		if _, err := s.DB.Exec(q); err == nil {
			t.Errorf("%q should be refused", q)
		}
	}
}
