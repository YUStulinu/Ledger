package importer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// Options tune how a statement is read, for files the built-in formats do not know.
type Options struct {
	// Map names the columns of a CSV: field -> column header. Fields: date, amount (signed), debit,
	// credit (unsigned money out / in), description, payee, balance, currency.
	Map map[string]string
	// DateFormat settles ambiguous dates: "DMY", "MDY", "YMD", or a Go layout ("02/01/2006").
	DateFormat string
	// Currency of the account when the file does not say.
	Currency string
	// Account name (an IBAN or "Chase Checking") when the file does not say.
	Account string
	// FlipSign reads positive amounts as money out (credit-card exports list charges as positive).
	FlipSign bool
}

// The column names banks and apps use, lower case. A trailing "(EUR)" is stripped first and taken
// as the currency.
var genericNames = map[string][]string{
	"date": {"date", "transaction date", "posting date", "posted date", "booking date", "booked date", "value date", "trans. date",
		"trans date", "date posted", "completed date", "date completed", "datum", "buchungstag", "fecha", "data"},
	"amount": {"amount", "transaction amount", "amount (signed)", "betrag", "importe", "montant", "value", "net amount"},
	"debit":  {"debit", "debit amount", "withdrawal", "withdrawals", "money out", "paid out", "out", "outflow", "spent", "charges"},
	"credit": {"credit", "credit amount", "deposit", "deposits", "money in", "paid in", "in", "inflow", "received"},
	"description": {"description", "details", "transaction description", "narrative", "memo", "reference", "payment reference",
		"particulars", "transaction details", "verwendungszweck", "concepto", "libellé", "libelle", "notes"},
	"payee":    {"payee", "name", "merchant", "counterparty", "counter party", "beneficiary", "recipient", "payee name", "merchant name"},
	"balance":  {"balance", "running balance", "balance after", "saldo", "solde"},
	"currency": {"currency", "ccy", "währung", "moneda", "devise"},
}

var parenCurrency = regexp.MustCompile(`\s*\(([A-Za-z]{3})\)\s*$`)

// genericCSV reads any CSV with a date and an amount (or debit and credit) column, recognized by
// the usual names in English (and a few other languages), or named with Options.Map.
type genericCSV struct{ opt Options }

func (genericCSV) Name() string { return "generic-csv" }

func (g genericCSV) Detect(name string, data []byte) bool {
	if len(g.opt.Map) > 0 {
		return true
	}
	_, _, ok := g.header(text(head(data)))
	return ok
}

// header finds the line that names the columns (statements may put account details above it) and
// which field each column holds.
func (g genericCSV) header(s string) (int, map[string]int, bool) {
	recs, _, err := records(s, "date")
	if err != nil {
		return 0, nil, false
	}
	for i, rec := range recs {
		if i > 30 {
			break
		}
		cols := g.match(rec)
		if _, ok := cols["date"]; !ok {
			continue
		}
		_, a := cols["amount"]
		_, d := cols["debit"]
		_, c := cols["credit"]
		if a || d || c {
			return i, cols, true
		}
	}
	return 0, nil, false
}

func (g genericCSV) match(rec []string) map[string]int {
	cols := map[string]int{}
	norm := func(h string) string {
		return strings.ToLower(strings.TrimSpace(parenCurrency.ReplaceAllString(h, "")))
	}
	if len(g.opt.Map) > 0 {
		for field, name := range g.opt.Map {
			for i, h := range rec {
				if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(name)) || norm(h) == strings.ToLower(strings.TrimSpace(name)) {
					cols[field] = i
				}
			}
		}
		return cols
	}
	for field, names := range genericNames {
		for _, n := range names {
			for i, h := range rec {
				if _, taken := cols[field]; !taken && norm(h) == n {
					cols[field] = i
				}
			}
		}
	}
	return cols
}

// Date layouts by order of the parts.
var dateLayouts = map[string][]string{
	"YMD": {"2006-01-02", "2006/01/02", "2006.01.02", "20060102", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02T15:04:05Z07:00"},
	"DMY": {"02/01/2006", "2/1/2006", "02.01.2006", "2.1.2006", "02-01-2006", "02/01/06", "02.01.06", "2 Jan 2006", "02 Jan 2006", "2-Jan-2006",
		"02-Jan-2006", "2 January 2006", "02/01/2006 15:04", "02.01.2006 15:04"},
	"MDY": {"01/02/2006", "1/2/2006", "01-02-2006", "01/02/06", "1/2/06", "Jan 2, 2006", "January 2, 2006", "01/02/2006 15:04"},
}

// parseDates reads every row's date with one layout: the only one that reads them all, or the
// one DateFormat names. Dates such as 03/04/2026 read both as 3 April and as 4 March; when every
// date of a file is like that, the file cannot say which, and DateFormat must.
func parseDates(values []string, format string) ([]time.Time, error) {
	var candidates []string
	switch strings.ToUpper(format) {
	case "":
		for _, k := range []string{"YMD", "DMY", "MDY"} {
			candidates = append(candidates, dateLayouts[k]...)
		}
	case "YMD", "DMY", "MDY":
		candidates = dateLayouts[strings.ToUpper(format)]
	default:
		candidates = []string{format}
	}
	var fits []string
	for _, l := range candidates {
		ok := true
		for _, v := range values {
			if _, err := time.Parse(l, strings.TrimSpace(v)); err != nil {
				ok = false
				break
			}
		}
		if ok {
			fits = append(fits, l)
		}
	}
	if len(fits) == 0 {
		return nil, fmt.Errorf("the dates (e.g. %q) are in no known format: pass --date-format with a Go layout such as 02/01/2006", values[0])
	}
	order := func(l string) string {
		for k, ls := range dateLayouts {
			for _, x := range ls {
				if x == l {
					return k
				}
			}
		}
		return l
	}
	first := order(fits[0])
	for _, l := range fits[1:] {
		if o := order(l); o != first && format == "" {
			return nil, fmt.Errorf("the dates (e.g. %q) read both as day/month and month/day: pass --date-format DMY or MDY", values[0])
		}
	}
	out := make([]time.Time, len(values))
	for i, v := range values {
		t, _ := time.Parse(fits[0], strings.TrimSpace(v))
		out[i] = time0(t)
	}
	return out, nil
}

func (g genericCSV) Parse(data []byte) (*Statement, error) {
	s := text(data)
	at, cols, ok := g.header(s)
	if !ok {
		if len(g.opt.Map) > 0 {
			return nil, fmt.Errorf("the columns named with --map were not found, or they lack a date and an amount (or debit/credit)")
		}
		return nil, ErrUnknownFormat
	}
	recs, lines, err := records(s, "date")
	if err != nil {
		return nil, err
	}
	header := recs[at]
	currency := strings.ToUpper(g.opt.Currency)
	for _, f := range []string{"amount", "debit", "credit", "balance"} {
		if i, ok := cols[f]; ok {
			if m := parenCurrency.FindStringSubmatch(header[i]); m != nil && currency == "" {
				currency = strings.ToUpper(m[1])
			}
		}
	}
	var body [][]string
	var bodyLines []int
	var dates []string
	for i, rec := range recs[at+1:] {
		if strings.TrimSpace(cell(rec, cols["date"])) == "" {
			continue // a blank or summary line
		}
		body = append(body, rec)
		bodyLines = append(bodyLines, lines[at+1+i])
		dates = append(dates, cell(rec, cols["date"]))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("no transactions found")
	}
	parsed, err := parseDates(dates, g.opt.DateFormat)
	if err != nil {
		return nil, err
	}
	get := func(rec []string, f string) string {
		i, ok := cols[f]
		if !ok {
			return ""
		}
		return cell(rec, i)
	}
	st := &Statement{Bank: knownBank(header), Format: "generic-csv", Account: g.opt.Account, Currency: currency}
	if st.Account == "" {
		st.Account = st.Bank
		if st.Bank == "Bank" {
			st.Warnings = append(st.Warnings, "the file does not name its bank or account: pass --account \"My Bank Checking\" so that every export of this account is kept together (and apart from others)")
		}
	}
	if len(g.opt.Map) > 0 {
		st.Format = "mapped-csv"
	}
	for i, rec := range body {
		line := bodyLines[i]
		var amount money.Amount
		if v := get(rec, "amount"); v != "" {
			a, err := money.Parse(v)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			amount = a
		} else {
			d, c := get(rec, "debit"), get(rec, "credit")
			if d == "" && c == "" {
				return nil, fmt.Errorf("line %d: no amount", line)
			}
			if d != "" {
				a, err := money.Parse(d)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", line, err)
				}
				amount -= abs(a)
			}
			if c != "" {
				a, err := money.Parse(c)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", line, err)
				}
				amount += abs(a)
			}
		}
		if g.opt.FlipSign {
			amount = -amount
		}
		desc := strings.TrimSpace(get(rec, "description"))
		if p := strings.TrimSpace(get(rec, "payee")); p != "" && !strings.Contains(strings.ToLower(desc), strings.ToLower(p)) {
			desc = strings.TrimSpace(p + " " + desc)
		}
		cur := strings.ToUpper(get(rec, "currency"))
		if cur == "" {
			cur = currency
		}
		row := Row{Line: line, Date: parsed[i], Description: desc, Amount: amount, Currency: cur}
		if b := get(rec, "balance"); b != "" {
			if v, err := money.Parse(b); err == nil {
				row.Balance = amountPtr(v)
			}
		}
		st.Rows = append(st.Rows, row)
	}
	for _, r := range st.Rows {
		if r.Currency == "" {
			return nil, fmt.Errorf("the file does not say its currency: pass --currency (e.g. EUR, USD, GBP)")
		}
	}
	// A currency column in the file outranks --currency, which is only for files that say none.
	if _, ok := cols["currency"]; ok || st.Currency == "" {
		st.Currency = st.Rows[0].Currency
	}
	orderRows(st)
	return st, nil
}

// knownBank names the bank from the columns of its export; "Bank" when it is not one of these.
func knownBank(header []string) string {
	h := strings.ToLower(strings.Join(header, "|"))
	switch {
	case strings.Contains(h, "transferwise id"):
		return "Wise"
	case strings.Contains(h, "emoji") && strings.Contains(h, "transaction id"):
		return "Monzo"
	case strings.Contains(h, "counter party") && strings.Contains(h, "spending category"):
		return "Starling"
	case strings.Contains(h, "payment reference") && strings.Contains(h, "amount (eur)"):
		return "N26"
	case strings.Contains(h, "posting date") && strings.Contains(h, "check or slip"):
		return "Chase"
	case strings.Contains(h, "card member"):
		return "American Express"
	}
	return "Bank"
}

// orderRows puts the rows in the order their balances follow: banks export newest first as often
// as oldest first, and rows of one day keep the file's order. Without balances, rows are sorted by
// date (oldest first), and the file cannot be checked: a warning says so.
func orderRows(st *Statement) {
	hasBalance := false
	for _, r := range st.Rows {
		hasBalance = hasBalance || r.Balance != nil
	}
	if !hasBalance {
		sortStable(st.Rows)
		st.Warnings = append(st.Warnings, "this file has no running balance, so its amounts could not be checked: compare one month's total with your bank")
		return
	}
	chains := func(rows []Row) bool {
		var prev *money.Amount
		for _, r := range rows {
			if r.Balance == nil {
				continue
			}
			if prev != nil && *prev+r.Amount != *r.Balance {
				return false
			}
			prev = r.Balance
		}
		return true
	}
	if chains(st.Rows) && !st.Rows[0].Date.After(st.Rows[len(st.Rows)-1].Date) {
		return
	}
	rev := make([]Row, len(st.Rows))
	for i, r := range st.Rows {
		rev[len(rev)-1-i] = r
	}
	if chains(rev) {
		st.Rows = rev
		return
	}
	// Charges written as positive numbers (credit-card exports): the balances add up only with the
	// signs turned round.
	for _, rows := range [][]Row{st.Rows, rev} {
		flipped := make([]Row, len(rows))
		for i, r := range rows {
			r.Amount = -r.Amount
			flipped[i] = r
		}
		if chains(flipped) {
			st.Rows = flipped
			st.Warnings = append(st.Warnings, "the amounts were read with the opposite sign: only that way do they add up to the balances in the file")
			return
		}
	}
	// Nothing adds up: keep the dates in order and let Verify name the first bad row.
	sort.SliceStable(st.Rows, func(i, j int) bool { return st.Rows[i].Date.Before(st.Rows[j].Date) })
}
