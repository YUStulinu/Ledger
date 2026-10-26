// Package importer reads bank statements (BT, ING and Revolut CSV exports, BT PDF statements) into
// a common shape, and checks them.
//
// Every statement that carries balances is verified: the running balance after each row must equal
// the previous one plus the row's amount, and the opening balance plus all rows must give the
// closing one. A row read wrongly (a debit taken as a credit, a thousands separator taken as a
// decimal point, a line lost) breaks the chain at that row, so mistakes are caught on import, with
// the row number, instead of surfacing months later as a wrong total.
package importer

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// Row is one transaction as the bank lists it.
type Row struct {
	Line        int // where it is in the file (for messages)
	Date        time.Time
	Description string
	// Amount is signed: negative leaves the account.
	Amount   money.Amount
	Currency string
	// Balance after this row, when the statement shows one.
	Balance   *money.Amount
	Reference string
	// The bank's own transaction type, when it has one (Revolut: CARD_PAYMENT, TOPUP, EXCHANGE...).
	Type string
	// Time of day, when known (Revolut), to order and pair same-day rows.
	Time string
}

// Statement is one imported file.
type Statement struct {
	Bank    string // "BT", "ING", "Revolut"
	Format  string // "bt-csv", "ing-csv", "revolut-csv", "bt-pdf", "mapped-csv"
	Account string // the IBAN, or the bank's name when the file does not say
	Owner   string
	// Currency of the account (Revolut statements mix currencies: each row has its own).
	Currency string
	Opening  map[string]money.Amount
	Closing  map[string]money.Amount
	Rows     []Row
	Warnings []string
}

// Parser reads one bank format.
type Parser interface {
	Name() string
	// Detect says whether the file looks like this format (from its name and first bytes).
	Detect(name string, data []byte) bool
	Parse(data []byte) (*Statement, error)
}

// Parsers lists the known formats, most specific first.
var Parsers = []Parser{revolutCSV{}, btCSV{}, ingCSV{}, btPDF{}}

// ErrUnknownFormat means no parser recognized the file (the caller may try mapping its columns).
var ErrUnknownFormat = fmt.Errorf("unknown statement format")

// Read detects the format of a file and parses and verifies it.
func Read(name string, data []byte) (*Statement, error) {
	for _, p := range Parsers {
		if p.Detect(name, data) {
			st, err := p.Parse(data)
			if err != nil {
				return nil, fmt.Errorf("%s (%s): %w", name, p.Name(), err)
			}
			if err := Verify(st); err != nil {
				return nil, fmt.Errorf("%s (%s): %w", name, p.Name(), err)
			}
			return st, nil
		}
	}
	return nil, fmt.Errorf("%s: %w", name, ErrUnknownFormat)
}

// Verify checks the running balances and the opening and closing balances, per currency.
func Verify(st *Statement) error {
	if len(st.Rows) == 0 {
		return fmt.Errorf("no transactions found")
	}
	prev := map[string]money.Amount{}
	known := map[string]bool{}
	for cur, v := range st.Opening {
		prev[cur], known[cur] = v, true
	}
	sum := map[string]money.Amount{}
	var problems []string
	for _, r := range st.Rows {
		sum[r.Currency] += r.Amount
		if r.Balance == nil {
			if known[r.Currency] {
				prev[r.Currency] += r.Amount
			}
			continue
		}
		if known[r.Currency] {
			if want := prev[r.Currency] + r.Amount; want != *r.Balance {
				problems = append(problems, fmt.Sprintf("line %d: the balance after %s %s should be %s, the statement says %s",
					r.Line, r.Amount.RO(), r.Currency, want.RO(), r.Balance.RO()))
				if len(problems) >= 5 {
					break
				}
			}
		}
		prev[r.Currency], known[r.Currency] = *r.Balance, true
	}
	for cur, closing := range st.Closing {
		if open, ok := st.Opening[cur]; ok {
			if open+sum[cur] != closing {
				problems = append(problems, fmt.Sprintf("%s: opening %s plus the transactions (%s) is %s, but the closing balance is %s",
					cur, open.RO(), sum[cur].RO(), (open+sum[cur]).RO(), closing.RO()))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("the balances do not add up:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// --- helpers shared by the parsers ------------------------------------------------------------------

// text decodes a file's bytes: UTF-8 (with or without a BOM), otherwise Windows-1250, which older
// Romanian exports use.
func text(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	for _, c := range data {
		if c < 0x80 {
			b.WriteByte(c)
		} else if r, ok := cp1250[c]; ok {
			b.WriteRune(r)
		} else {
			b.WriteRune('?')
		}
	}
	return b.String()
}

// The Romanian letters of Windows-1250 (the rest of its upper half does not occur in statements).
var cp1250 = map[byte]rune{
	0xC3: 'Ă', 0xE3: 'ă', 0xC2: 'Â', 0xE2: 'â', 0xCE: 'Î', 0xEE: 'î', 0xAA: 'Ş', 0xBA: 'ş', 0xDE: 'Ţ', 0xFE: 'ţ',
	0x8A: 'Š', 0x9A: 'š', 0xA0: ' ', 0x96: '–', 0x84: '„', 0x94: '”',
}

func parseDate(s string, layouts ...string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not a date: %q", s)
}

var monthsRO = map[string]time.Month{
	"ianuarie": 1, "februarie": 2, "martie": 3, "aprilie": 4, "mai": 5, "iunie": 6, "iulie": 7, "august": 8,
	"septembrie": 9, "octombrie": 10, "noiembrie": 11, "decembrie": 12,
	"ian": 1, "feb": 2, "mar": 3, "apr": 4, "iun": 6, "iul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
}

// parseRODate reads "05 martie 2026" (and the abbreviated "05 mar. 2026").
func parseRODate(s string) (time.Time, bool) {
	f := strings.Fields(strings.ToLower(strings.ReplaceAll(s, ".", " ")))
	if len(f) != 3 {
		return time.Time{}, false
	}
	m, ok := monthsRO[strings.TrimSpace(f[1])]
	if !ok {
		return time.Time{}, false
	}
	var d, y int
	if _, err := fmt.Sscanf(f[0]+" "+f[2], "%d %d", &d, &y); err != nil || d < 1 || d > 31 || y < 1990 {
		return time.Time{}, false
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), true
}

func amountPtr(a money.Amount) *money.Amount { return &a }

// sortStable orders rows by date, keeping the file's order within a day (balances follow it).
func sortStable(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Date.Before(rows[j].Date) })
}

// time0 drops the time of day.
func time0(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
