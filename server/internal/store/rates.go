package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Rate is the value of one unit of a currency in RON on a day.
type Rate struct {
	Date     time.Time
	Currency string
	RON      float64
}

// ParseRates reads exchange rates from the National Bank of Romania's XML (nbrfxrates.xml, or a
// yearly archive) or from a CSV with the columns date, currency, ron.
func ParseRates(data []byte) ([]Rate, error) {
	if bytes.Contains(data[:min(len(data), 512)], []byte("<DataSet")) {
		return parseBNR(data)
	}
	r := csv.NewReader(bytes.NewReader(data))
	recs, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	var out []Rate
	for i, rec := range recs {
		if i == 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "date") {
			continue
		}
		if len(rec) < 3 {
			return nil, fmt.Errorf("rates line %d: want date,currency,ron", i+1)
		}
		d, err := time.Parse("2006-01-02", strings.TrimSpace(rec[0]))
		if err != nil {
			return nil, fmt.Errorf("rates line %d: %w", i+1, err)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rec[2]), 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("rates line %d: bad rate %q", i+1, rec[2])
		}
		out = append(out, Rate{d, strings.ToUpper(strings.TrimSpace(rec[1])), v})
	}
	return out, nil
}

// The BNR format: <Cube date="2026-03-05"><Rate currency="EUR">4.9772</Rate><Rate currency="HUF" multiplier="100">1.2876</Rate>...
func parseBNR(data []byte) ([]Rate, error) {
	var doc struct {
		Body struct {
			Cubes []struct {
				Date  string `xml:"date,attr"`
				Rates []struct {
					Currency   string  `xml:"currency,attr"`
					Multiplier float64 `xml:"multiplier,attr"`
					Value      float64 `xml:",chardata"`
				} `xml:"Rate"`
			} `xml:"Cube"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("BNR rates: %w", err)
	}
	var out []Rate
	for _, c := range doc.Body.Cubes {
		d, err := time.Parse("2006-01-02", c.Date)
		if err != nil {
			continue
		}
		for _, r := range c.Rates {
			m := r.Multiplier
			if m == 0 {
				m = 1
			}
			out = append(out, Rate{d, strings.ToUpper(r.Currency), r.Value / m})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("BNR rates: no rates found")
	}
	return out, nil
}

// AddRates stores rates (replacing those of the same day and currency).
func (s *Store) AddRates(ctx context.Context, rates []Rate) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rows [][]any
	seen := map[string]bool{}
	for _, r := range rates {
		k := r.Date.Format("2006-01-02") + r.Currency
		if r.Currency == "RON" || seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, []any{r.Date, r.Currency, r.RON})
	}
	if _, err := bulkInsert(ctx, tx, "rates", []string{"date", "currency", "ron"}, rows,
		"ON CONFLICT (date, currency) DO UPDATE SET ron = excluded.ron"); err != nil {
		return 0, err
	}
	return len(rows), tx.Commit()
}

// MissingRates lists the currencies used on days before the first known rate (amounts there
// cannot be converted to RON).
func (s *Store) MissingRates(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT b.currency FROM bank_lines b
		WHERE b.currency <> 'RON' AND NOT EXISTS (SELECT 1 FROM rates r WHERE r.currency = b.currency AND r.date <= b.date)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
