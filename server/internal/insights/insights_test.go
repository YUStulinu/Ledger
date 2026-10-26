package insights

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/testenv"
)

func TestRecurringMatchesTheTruth(t *testing.T) {
	s, tr := testenv.Ledger(t, true)
	txs, err := Load(context.Background(), s.DB)
	if err != nil {
		t.Fatal(err)
	}
	end, _ := time.Parse("2006-01-02", tr.Period[1])
	found := map[string]Recurring{}
	for _, r := range FindRecurring(txs, end) {
		found[r.Merchant] = r
	}
	// Every schedule of the truth (transfers between own accounts are not payments) is found.
	want := map[string]string{}
	for _, tx := range tr.Transactions {
		if tx.Recurring != nil && tx.Kind != "transfer" {
			want[*tx.Recurring] = tx.Merchant
		}
	}
	names := map[string]bool{}
	for _, r := range found {
		names[strings.ToUpper(r.Merchant)] = true
	}
	for id := range want {
		ok := false
		for _, r := range found {
			for _, tx := range tr.Transactions {
				if tx.Recurring != nil && *tx.Recurring == id && strings.Contains(strings.ToUpper(tx.Desc), strings.ToUpper(r.Merchant)) {
					ok = true
				}
			}
		}
		if !ok {
			t.Errorf("recurring %q not found", id)
		}
	}
	// And nothing irregular is taken for a schedule (no supermarket, no taxi).
	for _, r := range found {
		switch r.Category {
		case "Groceries", "Restaurants", "Transport", "Food Delivery", "Shopping", "Fuel":
			if r.Category != "Transport" || !strings.Contains(strings.ToUpper(r.Merchant), "STB") {
				t.Errorf("%s (%s) is not a recurring payment: %+v", r.Merchant, r.Category, r)
			}
		}
	}
	// Netflix went from 49.99 to 59.99 in March 2026.
	var netflix *Recurring
	for _, r := range found {
		if strings.Contains(strings.ToUpper(r.Merchant), "NETFLIX") {
			r := r
			netflix = &r
		}
	}
	if netflix == nil || netflix.PreviousAmount != -49.99 || netflix.Amount != -59.99 || netflix.ChangedOn == nil ||
		netflix.ChangedOn.Format("2006-01") != "2026-03" || netflix.Cadence != "monthly" || !netflix.Active {
		t.Errorf("Netflix: %+v", netflix)
	}
}

func TestAlertsMatchTheTruth(t *testing.T) {
	s, tr := testenv.Ledger(t, true)
	txs, err := Load(context.Background(), s.DB)
	if err != nil {
		t.Fatal(err)
	}
	alerts := FindAlerts(txs)
	want := map[string]string{} // kind -> date
	for _, tx := range tr.Transactions {
		if tx.Anomaly != nil && *tx.Anomaly != "price_increase" {
			want[*tx.Anomaly] = tx.Date
		}
	}
	kinds := map[string]string{"duplicate": "duplicate", "unusual_amount": "unusual_amount", "unexpected_fee": "new_fee"}
	got := map[string][]string{}
	for _, a := range alerts {
		got[a.Kind] = append(got[a.Kind], a.Tx.Date.Format("2006-01-02"))
	}
	for anomaly, date := range want {
		dates := got[kinds[anomaly]]
		if len(dates) != 1 || dates[0] != date {
			t.Errorf("%s: alerts on %v, want exactly %s", anomaly, dates, date)
		}
	}
	if len(alerts) != len(want) {
		for _, a := range alerts {
			t.Logf("alert: %s %s %s %.2f %s", a.Kind, a.Tx.Date.Format("2006-01-02"), a.Tx.Merchant, a.Tx.Amount, a.Message)
		}
		t.Errorf("%d alerts, want %d (no false alarms)", len(alerts), len(want))
	}
}
