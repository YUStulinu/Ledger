package importer

import (
	"fmt"
	"strings"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// btCSV reads Banca Transilvania's "Extras de cont" CSV (BT24 / BT Pay): a few lines of account
// details ("Numar cont:", "Sold initial:"), then a table
//
//	Data tranzactie, Data valuta, Descriere, Referinta tranzactiei, Debit, Credit, Sold contabil
//
// with amounts in the Romanian format, and a closing "Sold final:" line.
type btCSV struct{}

func (btCSV) Name() string { return "bt-csv" }

func (btCSV) Detect(name string, data []byte) bool {
	s := strings.ToLower(text(head(data)))
	return strings.Contains(s, "data tranzactie") && strings.Contains(s, "descriere") &&
		(strings.Contains(s, "banca transilvania") || strings.Contains(s, "btrl") || strings.Contains(s, "sold contabil"))
}

func (btCSV) Parse(data []byte) (*Statement, error) {
	recs, lines, err := records(text(data), "Data tranzactie")
	if err != nil {
		return nil, err
	}
	st := &Statement{Bank: "BT", Format: "bt-csv", Account: "BT", Currency: "RON", Opening: map[string]money.Amount{}, Closing: map[string]money.Amount{}}
	var header []string
	var cDate, cDesc, cRef, cDebit, cCredit, cBal int
	for i, rec := range recs {
		first := strings.ToLower(cell(rec, 0))
		if header == nil {
			switch {
			case strings.HasPrefix(first, "numar cont"), strings.HasPrefix(first, "cont:"), strings.HasPrefix(first, "iban"):
				st.Account = strings.ReplaceAll(cell(rec, 1), " ", "")
			case strings.HasPrefix(first, "titular"):
				st.Owner = cell(rec, 1)
			case strings.HasPrefix(first, "moneda"):
				st.Currency = strings.ToUpper(cell(rec, 1))
			case strings.HasPrefix(first, "sold initial"):
				if v, err := money.Parse(cell(rec, 1)); err == nil {
					st.Opening[st.Currency] = v
				}
			case strings.HasPrefix(first, "data tranzactie"):
				header = rec
				cDate = column(header, "Data tranzactie")
				cDesc = column(header, "Descriere")
				cRef = column(header, "Referinta tranzactiei", "Referinta")
				cDebit = column(header, "Debit")
				cCredit = column(header, "Credit")
				cBal = column(header, "Sold contabil", "Sold")
				if cDesc < 0 || cDebit < 0 || cCredit < 0 {
					return nil, fmt.Errorf("the table lacks Descriere, Debit or Credit")
				}
			}
			continue
		}
		if strings.HasPrefix(first, "sold final") {
			if v, err := money.Parse(cell(rec, 1)); err == nil {
				st.Closing[st.Currency] = v
			}
			continue
		}
		date, err := parseDate(cell(rec, cDate), "2006-01-02", "02.01.2006", "02/01/2006")
		if err != nil {
			continue // a blank line or a page footer
		}
		row := Row{Line: lines[i], Date: date, Description: cell(rec, cDesc), Reference: cell(rec, cRef), Currency: st.Currency}
		debit, credit := cell(rec, cDebit), cell(rec, cCredit)
		var amount money.Amount
		if debit != "" {
			v, err := money.Parse(debit)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lines[i], err)
			}
			// BT lists debits as positive numbers (some exports with a minus sign: either way out).
			amount -= abs(v)
		}
		if credit != "" {
			v, err := money.Parse(credit)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lines[i], err)
			}
			amount += abs(v)
		}
		row.Amount = amount
		if b := cell(rec, cBal); b != "" {
			if v, err := money.Parse(b); err == nil {
				row.Balance = amountPtr(v)
			}
		}
		st.Rows = append(st.Rows, row)
	}
	if header == nil {
		return nil, fmt.Errorf("no transaction table (no 'Data tranzactie' header)")
	}
	return st, nil
}

func abs(a money.Amount) money.Amount {
	if a < 0 {
		return -a
	}
	return a
}

// head is the beginning of a file, enough to recognize its format.
func head(data []byte) []byte {
	if len(data) > 4096 {
		return data[:4096]
	}
	return data
}
