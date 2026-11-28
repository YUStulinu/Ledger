package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Rate is the value of one unit of a currency in the ledger's base currency on a day.
type Rate struct {
	Date     time.Time
	Currency string
	Value    float64
}

// ECBURL is the European Central Bank's history of its daily reference rates (every currency it
// quotes, against the euro, since 1999), as a zipped CSV.
const ECBURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.zip"

// ParseRates reads exchange rates and returns them as values in `base`, from:
//   - the National Bank of Romania's XML (nbrfxrates.xml, or a yearly archive): values in RON;
//   - the European Central Bank's XML or its history CSV (eurofxref-hist.csv): units per euro;
//   - a CSV date,currency,value whose values are already in the base currency.
//
// The central banks' rates are converted to the base through the base's own rate that day, so the
// ECB's euro rates serve a ledger in USD or GBP as well.
func ParseRates(data []byte, base string) ([]Rate, error) {
	base = strings.ToUpper(base)
	start := string(data[:min(len(data), 512)])
	var ref string
	var days map[time.Time]map[string]float64
	var err error
	switch {
	case strings.Contains(start, "<DataSet"):
		ref, days, err = parseBNR(data)
	case strings.Contains(start, "gesmes") || strings.Contains(start, "eurofxref"):
		ref, days, err = parseECBXML(data)
	case strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(start, "\uFEFF")), "Date,"):
		ref, days, err = parseECBCSV(data)
	default:
		return parsePlain(data)
	}
	if err != nil {
		return nil, err
	}
	var out []Rate
	for d, vals := range days {
		vals[ref] = 1
		vb, ok := vals[base]
		if !ok || vb <= 0 {
			continue // the base currency is not quoted that day
		}
		for c, v := range vals {
			if c != base && v > 0 {
				out = append(out, Rate{d, c, v / vb})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no rates for %s found in this file (it quotes against %s)", base, ref)
	}
	return out, nil
}

// parsePlain reads date,currency,value lines (a header line is optional), values in the base.
func parsePlain(data []byte) ([]Rate, error) {
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
			return nil, fmt.Errorf("rates line %d: want date,currency,value", i+1)
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
func parseBNR(data []byte) (string, map[time.Time]map[string]float64, error) {
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
		return "", nil, fmt.Errorf("BNR rates: %w", err)
	}
	days := map[time.Time]map[string]float64{}
	for _, c := range doc.Body.Cubes {
		d, err := time.Parse("2006-01-02", c.Date)
		if err != nil {
			continue
		}
		days[d] = map[string]float64{}
		for _, r := range c.Rates {
			m := r.Multiplier
			if m == 0 {
				m = 1
			}
			days[d][strings.ToUpper(r.Currency)] = r.Value / m
		}
	}
	if len(days) == 0 {
		return "", nil, fmt.Errorf("BNR rates: no rates found")
	}
	return "RON", days, nil
}

// The ECB's XML: <Cube time="2026-11-27"><Cube currency="USD" rate="1.0532"/>... (units per euro).
func parseECBXML(data []byte) (string, map[time.Time]map[string]float64, error) {
	var doc struct {
		Days []struct {
			Time  string `xml:"time,attr"`
			Rates []struct {
				Currency string  `xml:"currency,attr"`
				Rate     float64 `xml:"rate,attr"`
			} `xml:"Cube"`
		} `xml:"Cube>Cube"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return "", nil, fmt.Errorf("ECB rates: %w", err)
	}
	days := map[time.Time]map[string]float64{}
	for _, c := range doc.Days {
		d, err := time.Parse("2006-01-02", c.Time)
		if err != nil {
			continue
		}
		days[d] = map[string]float64{}
		for _, r := range c.Rates {
			if r.Rate > 0 {
				days[d][strings.ToUpper(r.Currency)] = 1 / r.Rate
			}
		}
	}
	if len(days) == 0 {
		return "", nil, fmt.Errorf("ECB rates: no rates found")
	}
	return "EUR", days, nil
}

// The ECB's history CSV: Date,USD,JPY,... with one line per day (units per euro, "N/A" when none).
func parseECBCSV(data []byte) (string, map[time.Time]map[string]float64, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\uFEFF"))))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return "", nil, fmt.Errorf("ECB rates: %w", err)
	}
	if len(recs) < 2 {
		return "", nil, fmt.Errorf("ECB rates: no rates found")
	}
	header := recs[0]
	days := map[time.Time]map[string]float64{}
	for _, rec := range recs[1:] {
		d, err := time.Parse("2006-01-02", strings.TrimSpace(rec[0]))
		if err != nil {
			continue
		}
		days[d] = map[string]float64{}
		for i := 1; i < len(rec) && i < len(header); i++ {
			if v, err := strconv.ParseFloat(strings.TrimSpace(rec[i]), 64); err == nil && v > 0 {
				days[d][strings.ToUpper(strings.TrimSpace(header[i]))] = 1 / v
			}
		}
	}
	return "EUR", days, nil
}

// FetchECB downloads the ECB's rate history (about 600 KB) and returns its CSV.
func FetchECB(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", ECBURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading the ECB rates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("downloading the ECB rates: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("the ECB rates: %w", err)
	}
	for _, f := range z.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 50<<20))
		}
	}
	return nil, fmt.Errorf("the ECB rates: no CSV in the download")
}

// AddRates stores rates (replacing those of the same day and currency). Rates for the base
// currency itself are left out: it is worth 1.
func (s *Store) AddRates(ctx context.Context, rates []Rate) (int, error) {
	base, err := s.Base(ctx)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rows [][]any
	seen := map[string]bool{}
	for _, r := range rates {
		k := r.Date.Format("2006-01-02") + r.Currency
		if r.Currency == base || seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, []any{r.Date, r.Currency, r.Value})
	}
	if _, err := bulkInsert(ctx, tx, "rates", []string{"date", "currency", "ron"}, rows,
		"ON CONFLICT (date, currency) DO UPDATE SET ron = excluded.ron"); err != nil {
		return 0, err
	}
	return len(rows), tx.Commit()
}

// MissingRates lists the currencies used on days before the first known rate (amounts there
// cannot be converted to the base currency).
func (s *Store) MissingRates(ctx context.Context) ([]string, error) {
	base, err := s.Base(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT b.currency FROM bank_lines b
		WHERE b.currency <> ? AND NOT EXISTS (SELECT 1 FROM rates r WHERE r.currency = b.currency AND r.date <= b.date)`, base)
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
