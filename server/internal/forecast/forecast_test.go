package forecast

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/ml"
	"github.com/YUStulinu/Ledger/server/internal/testenv"
)

// From the end of June 2026, forecast the next 90 days and compare with what happened.
func TestBacktest(t *testing.T) {
	s, _ := testenv.Ledger(t, true)
	py := ml.Find(testenv.Root())
	from := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	r, err := Run(context.Background(), s.DB, py, Options{Days: 90, From: from})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("start %.0f, predicted end %.0f [%.0f, %.0f], actual end %.0f; %.0f%% of days inside the 10-90 band, mean error %.0f RON, %d scheduled flows",
		r.StartBalance, r.End.P50, r.End.P10, r.End.P90, r.Actual[len(r.Actual)-1], 100*r.Coverage1090, r.MeanAbsError, r.Scheduled)
	if len(r.Actual) != 90 || len(r.P50) != 90 {
		t.Fatalf("lengths: %d %d", len(r.Actual), len(r.P50))
	}
	if r.Scheduled < 10 {
		t.Errorf("only %d scheduled flows", r.Scheduled)
	}
	// The band holds the real balance most of the time, and the median ends close to it.
	if r.Coverage1090 < 0.6 {
		t.Errorf("the 10-90%% band held the real balance on only %.0f%% of days", 100*r.Coverage1090)
	}
	if math.Abs(r.EndError) > 0.15*math.Abs(r.Actual[len(r.Actual)-1]) {
		t.Errorf("end error %.0f RON", r.EndError)
	}
}

func TestDailyBalancesEndAtTheStatements(t *testing.T) {
	s, _ := testenv.Ledger(t, true)
	days, err := DailyBalances(context.Background(), s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if days[0].Date.Format("2006-01-02") != "2025-04-01" || days[len(days)-1].Date.Format("2006-01-02") != "2026-09-30" {
		t.Errorf("range %s - %s", days[0].Date, days[len(days)-1].Date)
	}
	var total float64
	if err := s.DB.QueryRow(`SELECT sum(balance * CASE currency WHEN 'RON' THEN 1 ELSE (SELECT ron FROM rates WHERE currency = 'EUR' ORDER BY date DESC LIMIT 1) END) FROM balances`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if math.Abs(days[len(days)-1].RON-total) > 0.01 {
		t.Errorf("last day %.2f, balances %.2f", days[len(days)-1].RON, total)
	}
}
