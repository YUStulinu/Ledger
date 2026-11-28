package store

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var international = filepath.Join("..", "..", "..", "testdata", "international")

// A ledger outside Romania: the base currency comes from the first statement, the ECB's euro rates
// convert the other accounts, and every total is in the base.
func TestBaseCurrencyAndECBRates(t *testing.T) {
	ctx := context.Background()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, f := range []string{"camt053.xml", "bank.ofx"} {
		data, err := os.ReadFile(filepath.Join(international, f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Import(ctx, f, data); err != nil {
			t.Fatal(err)
		}
	}
	if base, _ := s.Base(ctx); base != "EUR" {
		t.Fatalf("base %q, want EUR (the first statement's currency)", base)
	}
	if missing, _ := s.MissingRates(ctx); len(missing) != 1 || missing[0] != "USD" {
		t.Fatalf("missing rates %v, want [USD]", missing)
	}
	ecb, _ := os.ReadFile(filepath.Join(international, "ecb-sample.csv"))
	rates, err := ParseRates(ecb, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRates(ctx, rates); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	// The USD rent of 20 November at the 2 November rate (1 EUR = 1.08 USD).
	var base float64
	if err := s.DB.QueryRow("SELECT amount_base FROM expenses WHERE currency = 'USD' AND amount = 1450").Scan(&base); err != nil {
		t.Fatal(err)
	}
	if math.Abs(base-1450/1.08) > 0.01 {
		t.Fatalf("amount_base %.2f, want %.2f", base, 1450/1.08)
	}
	// Changing the base drops the rates only when asked to.
	if err := s.SetBase(ctx, "USD", false); err == nil || !strings.Contains(err.Error(), "--reset-rates") {
		t.Fatalf("changing the base with rates stored must ask for --reset-rates: %v", err)
	}
	if err := s.SetBase(ctx, "usd", true); err != nil {
		t.Fatal(err)
	}
	if base, _ := s.Base(ctx); base != "USD" {
		t.Fatalf("base %q", base)
	}
}

// The ECB quotes against the euro; for a ledger in another currency its rates are crossed.
func TestECBRatesCrossedToAnotherBase(t *testing.T) {
	ecb, _ := os.ReadFile(filepath.Join(international, "ecb-sample.csv"))
	rates, err := ParseRates(ecb, "USD")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rates {
		if r.Date.Format("2006-01-02") == "2026-11-27" {
			got[r.Currency] = r.Value
		}
	}
	// 1 GBP = 1.05 / 0.84 USD; 1 EUR = 1.05 USD; no rate for USD itself.
	if math.Abs(got["GBP"]-1.25) > 1e-9 || math.Abs(got["EUR"]-1.05) > 1e-9 || got["USD"] != 0 {
		t.Fatalf("%v", got)
	}
}
