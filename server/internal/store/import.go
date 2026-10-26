package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/importer"
	"github.com/YUStulinu/Ledger/server/internal/merchant"
	"github.com/YUStulinu/Ledger/server/internal/money"
)

// ImportResult says what one file contributed.
type ImportResult struct {
	File       string   `json:"file"`
	Bank       string   `json:"bank"`
	Format     string   `json:"format"`
	Account    string   `json:"account"`
	Rows       int      `json:"rows"`
	New        int      `json:"new"`
	Duplicates int      `json:"duplicates"`
	Warnings   []string `json:"warnings,omitempty"`
	// The very same file was imported before.
	AlreadyImported bool `json:"already_imported,omitempty"`
}

// Import reads, verifies and stores a statement file. Importing the same file twice, or files whose
// periods overlap, adds nothing twice.
func (s *Store) Import(ctx context.Context, name string, data []byte) (*ImportResult, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	var prev string
	err := s.DB.QueryRowContext(ctx, "SELECT file_name FROM imports WHERE file_sha = ?", sha).Scan(&prev)
	if err == nil {
		return &ImportResult{File: name, AlreadyImported: true}, nil
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	st, err := importer.Read(name, data)
	if err != nil {
		return nil, err
	}
	return s.ImportStatement(ctx, st, name, sha)
}

// lineKey identifies a bank line across files: the same account, currency, day and amount, and its
// occurrence among identical lines of the file. Two identical coffees on one day stay two, while
// the overlap of two exports (or a PDF of a period also exported as CSV) is recognized.
func lineKey(account, currency string, date time.Time, amount money.Amount, occurrence int) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d", account, currency, date.Format("2006-01-02"), amount, occurrence)
}

// ImportStatement stores a parsed statement.
func (s *Store) ImportStatement(ctx context.Context, st *importer.Statement, name, sha string) (*ImportResult, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(seq), 0) FROM bank_lines").Scan(&seq); err != nil {
		return nil, err
	}
	res := &ImportResult{File: name, Bank: st.Bank, Format: st.Format, Account: st.Account, Rows: len(st.Rows), Warnings: st.Warnings}
	seen := map[string]int{}
	first := map[string]importer.Row{}
	var rows [][]any
	for _, r := range st.Rows {
		cur := r.Currency
		if cur == "" {
			cur = st.Currency
		}
		base := fmt.Sprintf("%s|%s|%s|%d", st.Account, cur, r.Date.Format("2006-01-02"), r.Amount)
		key := lineKey(st.Account, cur, r.Date, r.Amount, seen[base])
		seen[base]++
		if _, ok := first[cur]; !ok {
			first[cur] = r
		}
		seq++
		m := merchant.Extract(st.Bank, r.Description)
		var balance any
		if r.Balance != nil {
			balance = int64(*r.Balance)
		}
		rows = append(rows, []any{key, seq, st.Bank, st.Account, cur, r.Date, r.Time, int64(r.Amount), balance,
			r.Description, r.Reference, r.Type, m, merchant.Key(m), sha, r.Line})
	}
	n, err := bulkInsert(ctx, tx, "bank_lines", []string{"key", "seq", "bank", "account", "currency", "date", "time", "amount", "balance",
		"description", "reference", "type", "merchant", "merchant_key", "file_sha", "line"}, rows, "ON CONFLICT DO NOTHING")
	if err != nil {
		return nil, err
	}
	res.New, res.Duplicates = int(n), len(rows)-int(n)
	// Opening balances: the statement's, or, without one, the first row's balance before it. The
	// earliest known one per account and currency is what the ledger starts from.
	for cur, r := range first {
		opening, ok := st.Opening[cur]
		if !ok {
			if r.Balance == nil {
				continue
			}
			opening = *r.Balance - r.Amount
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO openings VALUES (?, ?, ?, ?)
			ON CONFLICT (account, currency) DO UPDATE SET date = excluded.date, amount = excluded.amount WHERE excluded.date < openings.date`,
			st.Account, cur, r.Date, int64(opening)); err != nil {
			return nil, err
		}
	}
	var firstDate, lastDate time.Time
	for i, r := range st.Rows {
		if i == 0 || r.Date.Before(firstDate) {
			firstDate = r.Date
		}
		if r.Date.After(lastDate) {
			lastDate = r.Date
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO imports VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now())`,
		sha, name, st.Bank, st.Format, st.Account, st.Owner, firstDate, lastDate, len(st.Rows), res.New); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}
