package store

import (
	"context"
	"database/sql"
	"strings"
)

// bulkInsert inserts rows in batches of multi-row VALUES statements: DuckDB is a columnar engine and
// is slow at one-row inserts, fast at large ones. `suffix` (such as an ON CONFLICT clause) is added
// to every statement. Rows within one call must not conflict with each other. Returns the number of
// rows affected.
func bulkInsert(ctx context.Context, tx *sql.Tx, table string, cols []string, rows [][]any, suffix string) (int64, error) {
	const batch = 500
	var total int64
	placeholder := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ")"
	for start := 0; start < len(rows); start += batch {
		end := min(start+batch, len(rows))
		var b strings.Builder
		b.WriteString("INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ")
		args := make([]any, 0, (end-start)*len(cols))
		for i := start; i < end; i++ {
			if i > start {
				b.WriteString(", ")
			}
			b.WriteString(placeholder)
			args = append(args, rows[i]...)
		}
		b.WriteString(" " + suffix)
		res, err := tx.ExecContext(ctx, b.String(), args...)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
