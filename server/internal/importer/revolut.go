package importer

import (
	"fmt"
	"strings"

	"github.com/YUStulinu/Ledger/server/internal/money"
)

// revolutCSV reads Revolut's account statement CSV:
//
//	Type, Product, Started Date, Completed Date, Description, Amount, Fee, Currency, State, Balance
//
// One file holds every currency pocket; each row has its own currency and the pocket's balance after
// it. Rows that never went through (DECLINED, REVERTED, PENDING) are skipped.
type revolutCSV struct{}

func (revolutCSV) Name() string { return "revolut-csv" }

func (revolutCSV) Detect(name string, data []byte) bool {
	s := strings.ToLower(text(head(data)))
	return strings.Contains(s, "started date") && strings.Contains(s, "completed date") && strings.Contains(s, "currency")
}

func (revolutCSV) Parse(data []byte) (*Statement, error) {
	recs, lines, err := records(text(data), "Started Date")
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("empty file")
	}
	h := recs[0]
	cType, cStarted, cDesc := column(h, "Type"), column(h, "Started Date"), column(h, "Description")
	cAmount, cFee, cCur, cState, cBal := column(h, "Amount"), column(h, "Fee"), column(h, "Currency"), column(h, "State"), column(h, "Balance")
	if cStarted < 0 || cAmount < 0 || cCur < 0 {
		return nil, fmt.Errorf("missing Started Date, Amount or Currency")
	}
	st := &Statement{Bank: "Revolut", Format: "revolut-csv", Account: "Revolut"}
	skipped := 0
	for i, rec := range recs[1:] {
		line := lines[i+1]
		if len(rec) <= cAmount {
			continue
		}
		if state := strings.ToUpper(cell(rec, cState)); state != "" && state != "COMPLETED" {
			skipped++
			continue
		}
		started := cell(rec, cStarted)
		date, err := parseDate(started, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "02/01/2006 15:04")
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		amount, err := money.Parse(cell(rec, cAmount))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if f := cell(rec, cFee); f != "" {
			fee, err := money.Parse(f)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			amount -= abs(fee)
		}
		row := Row{Line: line, Date: time0(date), Description: cell(rec, cDesc), Amount: amount, Currency: strings.ToUpper(cell(rec, cCur)),
			Type: strings.ToUpper(cell(rec, cType))}
		if len(started) >= 19 {
			row.Time = started[11:19]
		}
		if b := cell(rec, cBal); b != "" {
			if v, err := money.Parse(b); err == nil {
				row.Balance = amountPtr(v)
			}
		}
		st.Rows = append(st.Rows, row)
	}
	if skipped > 0 {
		st.Warnings = append(st.Warnings, fmt.Sprintf("skipped %d rows that did not complete (declined, reverted or pending)", skipped))
	}
	return st, nil
}
