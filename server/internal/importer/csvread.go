package importer

import (
	"encoding/csv"
	"io"
	"strings"
)

// records splits CSV text into rows, guessing the delimiter (',' or ';') from the line that names
// the columns. Rows may have different lengths (statements put account details above the table).
// It also returns each row's line number in the file (blank lines are skipped, so they differ from
// the row index), for error messages.
func records(s string, headerHint string) ([][]string, []int, error) {
	// Windows line endings confuse the reader's lenient quote handling (a closing quote followed by
	// a carriage return reads as a literal quote), so normalize them first.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	delim := ','
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(headerHint)) {
			if strings.Count(line, ";") > strings.Count(line, ",") {
				delim = ';'
			}
			break
		}
	}
	r := csv.NewReader(strings.NewReader(s))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	var recs [][]string
	var lines []int
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		line, _ := r.FieldPos(0)
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		recs = append(recs, rec)
		lines = append(lines, line)
	}
	return recs, lines, nil
}

// column finds a column by any of its names (case-insensitive), or -1.
func column(header []string, names ...string) int {
	for i, h := range header {
		for _, n := range names {
			if strings.EqualFold(strings.TrimSpace(h), n) {
				return i
			}
		}
	}
	return -1
}

func cell(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}
