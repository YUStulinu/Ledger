// Package forecast prepares the balance forecast (run in Python, ml/ledger_ml/forecast.py): the
// history, the scheduled payments and the starting balance, and, for a backtest, what really
// happened after a past date.
package forecast

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/insights"
	"github.com/YUStulinu/Ledger/server/internal/ml"
)

// Result is the Python side's answer (see forecast.py).
type Result struct {
	StartBalance float64   `json:"start_balance"`
	Days         []string  `json:"days"`
	P10          []float64 `json:"p10"`
	P50          []float64 `json:"p50"`
	P90          []float64 `json:"p90"`
	End          struct {
		P10 float64 `json:"p10"`
		P50 float64 `json:"p50"`
		P90 float64 `json:"p90"`
	} `json:"end"`
	Lowest struct {
		Date string  `json:"date"`
		P50  float64 `json:"p50"`
		P10  float64 `json:"p10"`
	} `json:"lowest"`
	Threshold            float64 `json:"threshold"`
	ChanceBelowThreshold float64 `json:"chance_below_threshold"`
	EverydayPerMonth     float64 `json:"everyday_spending_per_month"`
	Scheduled            int     `json:"scheduled"`
	// The balance on the days before the forecast starts (for the chart).
	History []Point `json:"history"`
	// Backtests only.
	Actual       []float64 `json:"actual,omitempty"`
	Coverage1090 float64   `json:"coverage_10_90,omitempty"`
	MeanAbsError float64   `json:"mean_abs_error,omitempty"`
	EndError     float64   `json:"end_error,omitempty"`
}

// Point is the balance on a day.
type Point struct {
	Date string  `json:"date"`
	RON  float64 `json:"ron"`
}

// historyDays of balances come with a forecast.
const historyDays = 60

type historyItem struct {
	Date     string  `json:"date"`
	RON      float64 `json:"amount_ron"`
	Kind     string  `json:"kind"`
	Merchant string  `json:"merchant"`
}

// Options of a forecast.
type Options struct {
	Days      int
	Threshold float64
	Paths     int
	// Backtest from this date (forecast as if the data ended there, compare with what followed).
	From time.Time
}

// Run forecasts the total balance (all accounts, in RON) from the end of the data, or backtests
// from opt.From.
func Run(ctx context.Context, db *sql.DB, py *ml.Bridge, opt Options) (*Result, error) {
	if opt.Days == 0 {
		opt.Days = 90
	}
	if opt.Paths == 0 {
		opt.Paths = 2000
	}
	txs, err := insights.Load(ctx, db)
	if err != nil {
		return nil, err
	}
	daily, err := DailyBalances(ctx, db)
	if err != nil {
		return nil, err
	}
	if len(daily) == 0 {
		return nil, errNoData
	}
	end := daily[len(daily)-1].Date
	if !opt.From.IsZero() {
		end = opt.From
		var cut []insights.Tx
		for _, t := range txs {
			if !t.Date.After(end) {
				cut = append(cut, t)
			}
		}
		txs = cut
	}
	var startBalance float64
	var actual []float64
	var past []Point
	for _, d := range daily {
		if !d.Date.After(end) {
			startBalance = d.RON
			if d.Date.After(end.AddDate(0, 0, -historyDays)) {
				past = append(past, Point{d.Date.Format("2006-01-02"), d.RON})
			}
		} else if !opt.From.IsZero() && len(actual) < opt.Days {
			actual = append(actual, d.RON)
		}
	}
	var scheduled []map[string]any
	for _, r := range insights.FindRecurring(txs, end) {
		if !r.Active {
			continue
		}
		scheduled = append(scheduled, map[string]any{"merchant": r.Merchant, "cadence": r.Cadence, "next": r.Next.Format("2006-01-02"),
			"typical_ron": r.TypicalRON, "active": true})
	}
	history := make([]historyItem, len(txs))
	for i, t := range txs {
		history[i] = historyItem{t.Date.Format("2006-01-02"), t.RON, t.Kind, t.Merchant}
	}
	req := map[string]any{
		"history": history, "recurring": scheduled, "balance": startBalance, "start": end.AddDate(0, 0, 1).Format("2006-01-02"),
		"days": opt.Days, "paths": opt.Paths, "threshold": opt.Threshold, "seed": 1,
	}
	if !opt.From.IsZero() {
		req["actual"] = actual
	}
	var res Result
	if err := py.Call(ctx, "forecast", nil, req, &res); err != nil {
		return nil, err
	}
	res.History = past
	return &res, nil
}

type errString string

func (e errString) Error() string { return string(e) }

const errNoData = errString("no transactions yet: import a statement first")

// Day is the total balance of all accounts at the end of a day.
type Day struct {
	Date time.Time `json:"date"`
	RON  float64   `json:"ron"`
}

// DailyBalances is the total balance (in RON, at each day's rate) for every day from the first
// opening to the last transaction.
func DailyBalances(ctx context.Context, db *sql.DB) ([]Day, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.date, p.currency, sum(p.amount) FROM ledger_postings p
		JOIN ledger_transactions t ON t.id = p.tx_id JOIN ledger_accounts a ON a.name = p.account
		WHERE a.type = 'asset' GROUP BY ALL ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	type move struct {
		date time.Time
		cur  string
		amt  int64
	}
	var moves []move
	for rows.Next() {
		var m move
		if err := rows.Scan(&m.date, &m.cur, &m.amt); err != nil {
			rows.Close()
			return nil, err
		}
		moves = append(moves, m)
	}
	rows.Close()
	if len(moves) == 0 {
		return nil, nil
	}
	rates := map[string][]struct {
		d time.Time
		v float64
	}{}
	rr, err := db.QueryContext(ctx, "SELECT date, currency, ron FROM rates ORDER BY date")
	if err != nil {
		return nil, err
	}
	for rr.Next() {
		var d time.Time
		var c string
		var v float64
		if err := rr.Scan(&d, &c, &v); err != nil {
			rr.Close()
			return nil, err
		}
		rates[c] = append(rates[c], struct {
			d time.Time
			v float64
		}{d, v})
	}
	rr.Close()
	rateOn := func(cur string, d time.Time) float64 {
		if cur == "RON" {
			return 1
		}
		rs := rates[cur]
		i := sort.Search(len(rs), func(i int) bool { return rs[i].d.After(d) })
		if i == 0 {
			if len(rs) > 0 {
				return rs[0].v
			}
			return 1
		}
		return rs[i-1].v
	}
	held := map[string]int64{}
	var out []Day
	j := 0
	for d := moves[0].date; !d.After(moves[len(moves)-1].date); d = d.AddDate(0, 0, 1) {
		for j < len(moves) && !moves[j].date.After(d) {
			held[moves[j].cur] += moves[j].amt
			j++
		}
		total := 0.0
		for cur, amt := range held {
			total += float64(amt) / 100 * rateOn(cur, d)
		}
		out = append(out, Day{d, total})
	}
	return out, nil
}
