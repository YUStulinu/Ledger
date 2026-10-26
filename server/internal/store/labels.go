package store

import (
	"context"
	"fmt"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/category"
)

// Label sources, from the most to the least trusted. A label is only ever replaced by one from a
// source at least as trusted: the model never overrides what you set by hand.
const (
	SourceUser  = "user"
	SourceRule  = "rule"
	SourceModel = "model"
	SourceLLM   = "llm"
)

var trust = map[string]int{SourceUser: 4, SourceRule: 3, SourceModel: 2, SourceLLM: 1}

// Label is a category assigned to a bank line.
type Label struct {
	Key        string
	Category   string
	Source     string
	Confidence float64
}

// SetLabels stores categories, keeping more trusted ones in place.
func (s *Store) SetLabels(ctx context.Context, labels []Label) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows := make([][]any, 0, len(labels))
	seen := map[string]bool{}
	for _, l := range labels {
		if _, ok := category.Lookup(l.Category); !ok {
			return 0, fmt.Errorf("unknown category %q", l.Category)
		}
		if _, ok := trust[l.Source]; !ok {
			return 0, fmt.Errorf("unknown label source %q", l.Source)
		}
		if seen[l.Key] {
			continue
		}
		seen[l.Key] = true
		rows = append(rows, []any{l.Key, l.Category, l.Source, l.Confidence})
	}
	const rank = "CASE %s WHEN 'user' THEN 4 WHEN 'rule' THEN 3 WHEN 'model' THEN 2 ELSE 1 END"
	n, err := bulkInsert(ctx, tx, "labels", []string{"key", "category", "source", "confidence"}, rows,
		"ON CONFLICT (key) DO UPDATE SET category = excluded.category, source = excluded.source, confidence = excluded.confidence WHERE "+
			fmt.Sprintf(rank, "excluded.source")+" >= "+fmt.Sprintf(rank, "labels.source"))
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// Correct records your category for a merchant and applies it to every line of that merchant. The
// corrections are what the classifier is trained on.
func (s *Store) Correct(ctx context.Context, lineKey, cat string) (int, error) {
	if _, ok := category.Lookup(cat); !ok {
		return 0, fmt.Errorf("unknown category %q", cat)
	}
	var mkey, desc string
	var amount int64
	if err := s.DB.QueryRowContext(ctx, "SELECT merchant_key, description, amount FROM bank_lines WHERE key = ?", lineKey).
		Scan(&mkey, &desc, &amount); err != nil {
		return 0, fmt.Errorf("no such transaction: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO corrections VALUES (?, ?, ?, ?, ?)", mkey, desc, amount, cat, time.Now()); err != nil {
		return 0, err
	}
	keys, err := s.keysOfMerchant(ctx, mkey)
	if err != nil {
		return 0, err
	}
	labels := make([]Label, len(keys))
	for i, k := range keys {
		labels[i] = Label{k, cat, SourceUser, 1}
	}
	return s.SetLabels(ctx, labels)
}

func (s *Store) keysOfMerchant(ctx context.Context, mkey string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT key FROM bank_lines WHERE merchant_key = ?", mkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Unlabeled is a bank line without a category yet (or with one only a weaker source gave).
type Unlabeled struct {
	Key, Bank, Description, Merchant, MerchantKey, Currency, Type string
	Amount                                                        int64
	Date                                                          time.Time
}

// LinesToLabel lists lines with no label, or with labels from sources less trusted than `above`.
func (s *Store) LinesToLabel(ctx context.Context, above string) ([]Unlabeled, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.key, b.bank, coalesce(b.description, ''), coalesce(b.merchant, ''), coalesce(b.merchant_key, ''),
		b.currency, coalesce(b.type, ''), b.amount, b.date
		FROM bank_lines b LEFT JOIN labels l ON l.key = b.key
		WHERE l.key IS NULL OR (CASE l.source WHEN 'user' THEN 4 WHEN 'rule' THEN 3 WHEN 'model' THEN 2 ELSE 1 END) < ?
		ORDER BY b.date, b.seq`, trust[above])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unlabeled
	for rows.Next() {
		var u Unlabeled
		if err := rows.Scan(&u.Key, &u.Bank, &u.Description, &u.Merchant, &u.MerchantKey, &u.Currency, &u.Type, &u.Amount, &u.Date); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Correction is one category you set by hand.
type Correction struct {
	MerchantKey string `json:"merchant_key"`
	Description string `json:"description"`
	Amount      int64  `json:"amount"`
	Category    string `json:"category"`
}

// Corrections lists your corrections, latest per merchant.
func (s *Store) Corrections(ctx context.Context) ([]Correction, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT merchant_key, description, amount, category FROM corrections
		QUALIFY row_number() OVER (PARTITION BY merchant_key ORDER BY created_at DESC) = 1 ORDER BY merchant_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Correction
	for rows.Next() {
		var c Correction
		if err := rows.Scan(&c.MerchantKey, &c.Description, &c.Amount, &c.Category); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
