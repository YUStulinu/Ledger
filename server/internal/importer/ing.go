package importer

import (
	"fmt"
	"strings"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// ingCSV reads ING Home'Bank's CSV export. Its quirk: each transaction spans several rows. The first
// has the date ("05 martie 2026"), the kind of transaction and the amount; the rows after it, with
// no date, add the details (card, terminal, beneficiary, reference). The file ends with "Sold
// initial" and "Sold final" rows.
type ingCSV struct{}

func (ingCSV) Name() string { return "ing-csv" }

func (ingCSV) Detect(name string, data []byte) bool {
	s := strings.ToLower(text(head(data)))
	return strings.Contains(s, "detalii tranzactie") && strings.Contains(s, "debit") && strings.Contains(s, "credit")
}

func (ingCSV) Parse(data []byte) (*Statement, error) {
	recs, lines, err := records(text(data), "Detalii tranzactie")
	if err != nil {
		return nil, err
	}
	st := &Statement{Bank: "ING", Format: "ing-csv", Account: "ING", Currency: "RON", Opening: map[string]money.Amount{}, Closing: map[string]money.Amount{}}
	var header []string
	var cDate, cDetails, cDebit, cCredit int
	var cur *Row
	flush := func() {
		if cur != nil {
			st.Rows = append(st.Rows, *cur)
			cur = nil
		}
	}
	amountOf := func(rec []string, line int) (money.Amount, error) {
		var a money.Amount
		if d := cell(rec, cDebit); d != "" {
			v, err := money.Parse(d)
			if err != nil {
				return 0, fmt.Errorf("line %d: %w", line, err)
			}
			a -= abs(v)
		}
		if c := cell(rec, cCredit); c != "" {
			v, err := money.Parse(c)
			if err != nil {
				return 0, fmt.Errorf("line %d: %w", line, err)
			}
			a += abs(v)
		}
		return a, nil
	}
	for i, rec := range recs {
		if header == nil {
			if column(rec, "Detalii tranzactie") >= 0 {
				header = rec
				cDate = column(header, "Data")
				cDetails = column(header, "Detalii tranzactie")
				cDebit = column(header, "Debit")
				cCredit = column(header, "Credit")
			} else if iban := findIBAN(strings.Join(rec, " ")); iban != "" {
				st.Account = iban
			}
			continue
		}
		details := cell(rec, cDetails)
		low := strings.ToLower(details)
		if strings.HasPrefix(low, "sold initial") || strings.HasPrefix(low, "sold final") {
			flush()
			// The balance sits in the Debit column; a negative balance would carry a minus sign.
			raw := cell(rec, cDebit)
			if raw == "" {
				raw = cell(rec, cCredit)
			}
			if v, err := money.Parse(raw); err == nil {
				if strings.HasPrefix(low, "sold initial") {
					st.Opening[st.Currency] = v
				} else {
					st.Closing[st.Currency] = v
				}
			}
			continue
		}
		if d := cell(rec, cDate); d != "" {
			date, ok := parseRODate(d)
			if !ok {
				t, err := parseDate(d, "02.01.2006", "2006-01-02", "02/01/2006", "02-01-2006")
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", lines[i], err)
				}
				date = t
			}
			flush()
			a, err := amountOf(rec, lines[i])
			if err != nil {
				return nil, err
			}
			cur = &Row{Line: lines[i], Date: date, Description: details, Amount: a, Currency: st.Currency, Type: details}
			continue
		}
		if cur != nil && details != "" {
			cur.Description += "; " + details
			if iban := findIBAN(details); iban != "" && strings.HasPrefix(strings.ToLower(details), "din contul") {
				cur.Reference = iban
			}
		}
	}
	flush()
	if header == nil {
		return nil, fmt.Errorf("no transaction table (no 'Detalii tranzactie' header)")
	}
	// ING lists only the range's opening balance; without per-row balances the check is opening plus
	// transactions against the closing balance.
	return st, nil
}

// findIBAN returns the first Romanian IBAN in s.
func findIBAN(s string) string {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ':' || r == ';' || r == ',' }) {
		f = strings.ToUpper(f)
		if len(f) == 24 && strings.HasPrefix(f, "RO") && f[2] >= '0' && f[2] <= '9' {
			return f
		}
	}
	return ""
}
