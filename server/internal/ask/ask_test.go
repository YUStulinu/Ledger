package ask

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/llm"
	"github.com/YUStulinu/Ledger/server/internal/testenv"
)

func TestCheckRefusesWhatIsNotASelectOverTheViews(t *testing.T) {
	s, _ := testenv.Ledger(t, true)
	ctx := context.Background()
	refused := map[string]string{
		"DELETE FROM expenses":                                "only SELECT",
		"DROP TABLE bank_lines":                               "only SELECT",
		"SELECT 1; DELETE FROM labels":                        "only SELECT",
		"SELECT 1; SELECT 2":                                  "one query",
		"SELECT * FROM bank_lines":                            "unknown table",
		"SELECT * FROM labels":                                "unknown table",
		"SELECT * FROM main.ledger_postings":                  "unknown table",
		"SELECT * FROM read_csv('C:/secret.csv')":             "not allowed",
		"SELECT * FROM duckdb_settings()":                     "not allowed",
		"SELECT read_text('C:/Windows/win.ini')":              "read_text",
		"SELECT getenv('PATH') FROM expenses":                 "getenv",
		"SELECT current_setting('threads')":                   "current_setting",
		"SELECT * FROM expenses e JOIN corrections c ON true": "unknown table",
		"SELECT * FROM (SELECT * FROM imports)":               "unknown table",
		"INSERT INTO labels VALUES ('x','y','user',1)":        "only SELECT",
		"ATTACH 'x.duckdb'":                                   "only SELECT",
		"":                                                    "empty",
	}
	for q, want := range refused {
		if err := Check(ctx, s.DB, q); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error about %q", q, err, want)
		}
	}
	allowed := []string{
		"SELECT round(sum(amount_ron), 2) FROM expenses WHERE category = 'Groceries'",
		"WITH m AS (SELECT month_start, sum(amount_ron) s FROM expenses GROUP BY 1) SELECT * FROM m ORDER BY 1",
		"SELECT bank, sum(balance) FROM balances GROUP BY bank",
		"SELECT category FROM expenses UNION SELECT category FROM income",
		"SELECT * FROM transactions t WHERE t.kind = 'transfer' AND t.id IN (SELECT id FROM transactions)",
	}
	for _, q := range allowed {
		if err := Check(ctx, s.DB, q); err != nil {
			t.Errorf("%q refused: %v", q, err)
		}
		if _, _, err := Run(ctx, s.DB, q); err != nil {
			t.Errorf("%q failed: %v", q, err)
		}
	}
}

// fake answers with prepared queries, in order.
type fake struct {
	answers []string
	seen    []llm.Request
}

func (f *fake) Complete(ctx context.Context, req llm.Request) (string, error) {
	f.seen = append(f.seen, req)
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func TestAskRetriesOnceWithTheError(t *testing.T) {
	s, tr := testenv.Ledger(t, true)
	m := &fake{answers: []string{
		`{"sql": "SELECT sum(amount_ron) FROM bank_lines"}`,
		`{"sql": "SELECT round(sum(amount_ron), 2) AS groceries FROM expenses WHERE category = 'Groceries' AND year = 2026 AND month = 3"}`,
	}}
	a, err := Ask(context.Background(), s.DB, m, "Cât am dat pe mâncare de la supermarket în martie 2026?", time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Rejected) != 1 || !strings.Contains(a.Rejected[0].Error, "unknown table") {
		t.Errorf("rejected: %+v", a.Rejected)
	}
	// The second request carried the error back to the model.
	last := m.seen[1].Messages[len(m.seen[1].Messages)-1].Content
	if !strings.Contains(last, "unknown table") {
		t.Errorf("the retry did not include the error: %q", last)
	}
	var want int64
	for _, tx := range tr.Transactions {
		if tx.Category == "Groceries" && strings.HasPrefix(tx.Date, "2026-03") {
			want -= tx.Amount
		}
	}
	got, ok := a.Rows[0][0].(float64)
	if !ok || int64(got*100+0.5) != want {
		t.Errorf("answer %v (%T), want %.2f", a.Rows[0][0], a.Rows[0][0], float64(want)/100)
	}
}
