// Package insights finds what is worth a look in the ledger: recurring payments (subscriptions,
// bills, rent, the salary) with their cadence, next date, yearly cost and price changes; payments
// that look charged twice; and amounts far from what a merchant usually costs.
package insights

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/merchant"
)

// Tx is one bank-account movement (from the transactions view).
type Tx struct {
	ID       int64     `json:"id"`
	Date     time.Time `json:"date"`
	Account  string    `json:"account"`
	Currency string    `json:"currency"`
	Amount   float64   `json:"amount"` // signed, own currency
	RON      float64   `json:"amount_ron"`
	Kind     string    `json:"kind"`
	Category string    `json:"category"`
	Merchant string    `json:"merchant"`
	Desc     string    `json:"description"`
	key      string
}

// Load reads the movements that can recur or be anomalous (not transfers and exchanges).
func Load(ctx context.Context, db *sql.DB) ([]Tx, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, date, account, currency, amount::DOUBLE, amount_ron::DOUBLE, kind, category,
		coalesce(merchant, ''), coalesce(description, '') FROM transactions WHERE kind IN ('expense', 'income') ORDER BY date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tx
	for rows.Next() {
		var t Tx
		if err := rows.Scan(&t.ID, &t.Date, &t.Account, &t.Currency, &t.Amount, &t.RON, &t.Kind, &t.Category, &t.Merchant, &t.Desc); err != nil {
			return nil, err
		}
		t.key = merchant.Key(t.Merchant)
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- recurring payments -------------------------------------------------------------------------------

// Recurring is a payment (or income) that comes back on a schedule.
type Recurring struct {
	Merchant  string    `json:"merchant"`
	Category  string    `json:"category"`
	Currency  string    `json:"currency"`
	Cadence   string    `json:"cadence"` // weekly, monthly, yearly
	Count     int       `json:"count"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
	Next      time.Time `json:"next"`
	Amount    float64   `json:"amount"` // the latest, signed
	Typical   float64   `json:"typical"`
	YearlyRON float64   `json:"yearly_ron"` // what a year of it costs (negative) or brings
	// One occurrence in RON (the average of the last three), for the forecast.
	TypicalRON float64 `json:"typical_ron"`
	Active     bool    `json:"active"`
	Income     bool    `json:"income"`
	// A price change: the amount before it and when it happened.
	PreviousAmount float64    `json:"previous_amount,omitempty"`
	ChangedOn      *time.Time `json:"changed_on,omitempty"`
	// Started recently (within its last two periods).
	New bool `json:"new"`
}

var cadences = []struct {
	name     string
	lo, hi   float64 // days between charges
	period   float64
	minCount int
}{
	{"weekly", 5.5, 8.5, 7, 4},
	{"monthly", 25, 35, 30.44, 3},
	{"yearly", 340, 390, 365.25, 2},
}

// FindRecurring finds the schedules. `today` is the end of the data (to tell active from ended).
func FindRecurring(txs []Tx, today time.Time) []Recurring {
	groups := map[string][]Tx{}
	for _, t := range txs {
		k := t.key + "|" + t.Currency + "|" + map[bool]string{true: "in", false: "out"}[t.Amount > 0]
		groups[k] = append(groups[k], t)
	}
	var out []Recurring
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		// Intervals between consecutive charges; several on one day count once.
		var days []float64
		for i := 1; i < len(g); i++ {
			if d := g[i].Date.Sub(g[i-1].Date).Hours() / 24; d >= 1 {
				days = append(days, d)
			}
		}
		if len(days) == 0 {
			continue
		}
		med := median(days)
		for _, c := range cadences {
			if med < c.lo || med > c.hi || len(g) < c.minCount {
				continue
			}
			// Most intervals must fit the cadence: groceries every few days never do.
			fit := 0
			for _, d := range days {
				if d >= c.lo && d <= c.hi {
					fit++
				}
			}
			if float64(fit) < 0.75*float64(len(days)) {
				continue
			}
			amounts := make([]float64, len(g))
			for i, t := range g {
				amounts[i] = math.Abs(t.Amount)
			}
			typical := median(amounts)
			// The amounts must be steady (bills vary a little, a shop's receipts a lot).
			steady := 0
			for _, a := range amounts {
				if math.Abs(a-typical) <= 0.35*typical {
					steady++
				}
			}
			// ...unless the schedule itself is unmistakable: a seasonal bill (gas in winter costs five
			// times the summer's) still comes every month.
			regular := float64(fit) >= 0.9*float64(len(days)) && len(g) >= 6
			if float64(steady) < 0.8*float64(len(amounts)) && !regular {
				continue
			}
			last := g[len(g)-1]
			r := Recurring{
				Merchant: last.Merchant, Category: last.Category, Currency: last.Currency, Cadence: c.name, Count: len(g),
				First: g[0].Date, Last: last.Date, Next: last.Date.AddDate(0, 0, int(math.Round(c.period))), Amount: last.Amount,
				Typical: typical * sign(last.Amount), Income: last.Amount > 0,
				Active: today.Sub(last.Date).Hours()/24 <= 1.5*c.period,
				New:    today.Sub(g[0].Date).Hours()/24 <= 2.2*c.period,
			}
			if c.name == "monthly" {
				r.Next = last.Date.AddDate(0, 1, 0)
			}
			ron := 0.0
			for _, t := range g[max(0, len(g)-3):] {
				ron += t.RON
			}
			ron /= float64(min(3, len(g)))
			r.TypicalRON = ron
			r.YearlyRON = ron * 365.25 / c.period
			r.PreviousAmount, r.ChangedOn = priceChange(g)
			out = append(out, r)
			break
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].YearlyRON < out[b].YearlyRON })
	return out
}

// priceChange finds a step in an otherwise fixed price (a subscription getting dearer): the last
// change, if the amounts before and after it were each constant.
func priceChange(g []Tx) (float64, *time.Time) {
	if len(g) < 3 {
		return 0, nil
	}
	last := g[len(g)-1].Amount
	i := len(g) - 1
	for i > 0 && g[i-1].Amount == last {
		i--
	}
	if i == 0 {
		return 0, nil
	}
	before := g[i-1].Amount
	// Constant before the change too (at least two charges), and a real step (over 2%).
	if i >= 2 && g[i-2].Amount == before && math.Abs(last-before) > 0.02*math.Abs(before) {
		d := g[i].Date
		return before, &d
	}
	return 0, nil
}

// --- duplicates and unusual amounts ------------------------------------------------------------------

// Alert is something unusual about one payment.
type Alert struct {
	Kind    string `json:"kind"` // duplicate, unusual_amount, new_fee
	Tx      Tx     `json:"transaction"`
	Message string `json:"message"`
	// For a duplicate, the other charge.
	Other *Tx `json:"other,omitempty"`
}

// FindAlerts looks for payments charged twice, amounts far above a merchant's usual ones, and bank
// fees never seen before.
func FindAlerts(txs []Tx) []Alert {
	var out []Alert
	// The same merchant, the same amount, the same account, the same day: likely charged twice.
	seen := map[string]Tx{}
	for _, t := range txs {
		if t.Amount >= 0 || math.Abs(t.RON) < 30 {
			continue
		}
		k := t.Date.Format("2006-01-02") + "|" + t.Account + "|" + t.key + "|" + money2(t.Amount)
		if prev, ok := seen[k]; ok {
			p := prev
			out = append(out, Alert{Kind: "duplicate", Tx: t, Other: &p, Message: "charged twice on the same day"})
			continue
		}
		seen[k] = t
	}
	// Far above what this merchant usually costs. Receipts vary multiplicatively (a big shop is
	// several times a small one), so the comparison is on a log scale, with robust statistics (median
	// and MAD) so that one odd amount does not hide the next. Utility bills are left out: they are
	// seasonal by nature, and winter's are expected to be several times summer's.
	history := map[string][]float64{}
	for _, t := range txs {
		if t.Amount >= 0 || t.Category == "Utilities" {
			continue
		}
		h := history[t.key]
		a := math.Log(math.Abs(t.RON) + 1)
		if len(h) >= 15 {
			med := median(h)
			spread := 1.4826 * median(absDev(h, med))
			if spread < 0.1 {
				spread = 0.1
			}
			if a-med > math.Log(5) && (a-med)/spread > 4.5 {
				out = append(out, Alert{Kind: "unusual_amount", Tx: t,
					Message: "about " + ratio(math.Exp(a-med)) + " times what " + t.Merchant + " usually costs"})
			}
		}
		history[t.key] = append(h, a)
	}
	// A bank fee with a description never seen before.
	fees := map[string]bool{}
	first := true
	for _, t := range txs {
		if t.Category != "Fees" {
			continue
		}
		k := strings.ToUpper(strings.TrimSpace(t.Desc))
		if !fees[k] && !first {
			out = append(out, Alert{Kind: "new_fee", Tx: t, Message: "a fee you were not charged before"})
		}
		fees[k], first = true, false
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Tx.Date.After(out[b].Tx.Date) })
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func absDev(xs []float64, m float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = math.Abs(x - m)
	}
	return out
}

func sign(x float64) float64 {
	if x < 0 {
		return -1
	}
	return 1
}

// money2 is an amount as a map key (two decimals).
func money2(x float64) string { return strconv.FormatFloat(x, 'f', 2, 64) }

func ratio(x float64) string { return strconv.FormatFloat(math.Round(x*10)/10, 'f', 1, 64) }
