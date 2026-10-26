// Package testenv builds a ledger from the synthetic statements (testdata/synthetic) for tests, with
// every line labeled with its true category, and exposes the truth it was generated from.
package testenv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/YUStulinu/Ledger/server/internal/store"
)

// Dir is testdata/synthetic.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "synthetic")
}

// Files are the synthetic statements, overlaps included.
var Files = []string{"bt_2025-04_2026-09.csv", "bt_2026-09.pdf", "ing_2025-04_2025-12.csv", "ing_2025-12_2026-09.csv", "revolut_2025-04_2026-09.csv"}

type Tx struct {
	ID        int     `json:"id"`
	Date      string  `json:"date"`
	Account   string  `json:"account"`
	Currency  string  `json:"currency"`
	Amount    int64   `json:"amount"`
	Merchant  string  `json:"merchant"`
	Category  string  `json:"category"`
	Kind      string  `json:"kind"`
	Channel   string  `json:"channel"`
	Recurring *string `json:"recurring"`
	Anomaly   *string `json:"anomaly"`
	Pair      *int    `json:"pair"`
	Desc      string  `json:"description"`
}

type Truth struct {
	Transactions []Tx               `json:"transactions"`
	Accounts     map[string]string  `json:"accounts"`
	Rates        map[string]float64 `json:"rates"`
	Period       []string           `json:"period"`
}

func LoadTruth(t testing.TB) *Truth {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(Dir(), "truth.json"))
	if err != nil {
		t.Skipf("synthetic data missing (cd ml && python -m ledger_ml.synth): %v", err)
	}
	var tr Truth
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatal(err)
	}
	return &tr
}

// Keys maps every truth transaction to the key its bank line gets in the store.
func (tr *Truth) Keys() map[string]Tx {
	// BT statements carry the IBAN; the ING export and Revolut's do not.
	id := map[string]string{"bt": tr.Accounts["bt"], "ing": "ING", "revolut": "Revolut"}
	seen := map[string]int{}
	out := map[string]Tx{}
	for _, tx := range tr.Transactions {
		base := fmt.Sprintf("%s|%s|%s|%d", id[tx.Account], tx.Currency, tx.Date, tx.Amount)
		out[fmt.Sprintf("%s|%d", base, seen[base])] = tx
		seen[base]++
	}
	return out
}

// Ledger imports every synthetic statement and the rates into a fresh in-memory store. With
// trueLabels, every line gets its true category (as if you had sorted everything by hand).
func Ledger(t testing.TB, trueLabels bool) (*store.Store, *Truth) {
	t.Helper()
	tr := LoadTruth(t)
	s, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	for _, f := range Files {
		data, err := os.ReadFile(filepath.Join(Dir(), f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Import(ctx, f, data); err != nil {
			t.Fatal(err)
		}
	}
	rates, err := os.ReadFile(filepath.Join(Dir(), "rates.csv"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := store.ParseRates(rates)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRates(ctx, parsed); err != nil {
		t.Fatal(err)
	}
	if trueLabels {
		var labels []store.Label
		for k, tx := range tr.Keys() {
			if tx.Kind != "transfer" && tx.Kind != "exchange" {
				labels = append(labels, store.Label{Key: k, Category: tx.Category, Source: store.SourceUser, Confidence: 1})
			}
		}
		if _, err := s.SetLabels(ctx, labels); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	return s, tr
}

// Root is the repository's root directory.
func Root() string { return filepath.Join(Dir(), "..", "..") }
