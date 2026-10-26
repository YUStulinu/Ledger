// Package ask answers questions about your money in plain language ("how much did I spend on food
// in March compared to February?") by having the local language model write SQL, which is checked
// before it runs:
//
//  1. DuckDB's own parser reads the query (json_serialize_sql): anything but a single SELECT is
//     refused, since only SELECTs can be serialized at all;
//  2. the syntax tree is walked: the only tables it may read are the four documented views (and its
//     own WITH clauses), and table functions (read_csv, glob...) are refused;
//  3. it runs inside a transaction that is rolled back, with a time limit and a row limit, on a
//     database whose file and network access is switched off (store.Open);
//  4. if it fails, the model is shown the error once and may correct its query.
package ask

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/category"
	"github.com/YUStulinu/Ledger/server/internal/llm"
	"github.com/YUStulinu/Ledger/server/internal/store"
)

// Answer is a question's result.
type Answer struct {
	Question string   `json:"question"`
	SQL      string   `json:"sql"`
	Columns  []string `json:"columns"`
	Rows     [][]any  `json:"rows"`
	// The model's attempts that were refused or failed, with why (shown for transparency).
	Rejected []Attempt `json:"rejected,omitempty"`
	Seconds  float64   `json:"seconds"`
}

type Attempt struct {
	SQL   string `json:"sql"`
	Error string `json:"error"`
}

const maxRows = 500

// Ask turns a question into SQL, checks it, runs it, and returns the result.
func Ask(ctx context.Context, db *sql.DB, model llm.Model, question string, today time.Time) (*Answer, error) {
	t0 := time.Now()
	a := &Answer{Question: question}
	msgs := []llm.Message{{Role: "system", Content: Prompt(today)}, {Role: "user", Content: question}}
	for attempt := 0; attempt < 2; attempt++ {
		var out struct {
			SQL string `json:"sql"`
		}
		err := llm.JSON(ctx, model, llm.Request{
			Messages: msgs,
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"sql": map[string]any{"type": "string"}},
				"required":   []string{"sql"},
			},
			MaxTokens: 600,
		}, &out)
		if err != nil {
			return nil, err
		}
		q := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(out.SQL), ";"))
		cols, rows, err := Run(ctx, db, q)
		if err == nil {
			a.SQL, a.Columns, a.Rows = q, cols, rows
			a.Seconds = time.Since(t0).Seconds()
			return a, nil
		}
		a.Rejected = append(a.Rejected, Attempt{SQL: q, Error: err.Error()})
		msgs = append(msgs, llm.Message{Role: "assistant", Content: fmt.Sprintf(`{"sql": %q}`, q)},
			llm.Message{Role: "user", Content: "That query failed: " + err.Error() + "\nWrite a corrected query for the same question."})
	}
	a.Seconds = time.Since(t0).Seconds()
	return a, fmt.Errorf("could not answer: %s", a.Rejected[len(a.Rejected)-1].Error)
}

// Run checks a query and runs it (read-only, limited in time and rows).
func Run(ctx context.Context, db *sql.DB, q string) ([]string, [][]any, error) {
	if err := Check(ctx, db, q); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT * FROM (%s) LIMIT %d", q, maxRows))
	if err != nil {
		return nil, nil, cleanError(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		for i, v := range vals {
			vals[i] = plain(v)
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, cleanError(err)
	}
	return cols, out, nil
}

// plain turns driver values into JSON-friendly ones.
func plain(v any) any {
	switch x := v.(type) {
	case time.Time:
		if x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 {
			return x.Format("2006-01-02")
		}
		return x.Format(time.RFC3339)
	case []byte:
		return string(x)
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f
	case fmt.Stringer:
		// DuckDB decimals and other driver types.
		var f float64
		if _, err := fmt.Sscan(x.String(), &f); err == nil {
			return f
		}
		return x.String()
	}
	return v
}

func cleanError(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "\n\nLINE"); i > 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s", strings.TrimSpace(msg))
}

// Check refuses anything but a single SELECT over the allowed views.
func Check(ctx context.Context, db *sql.DB, q string) error {
	if strings.TrimSpace(q) == "" {
		return fmt.Errorf("empty query")
	}
	var tree string
	if err := db.QueryRowContext(ctx, "SELECT CAST(json_serialize_sql(?) AS VARCHAR)", q).Scan(&tree); err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(tree), &root); err != nil {
		return err
	}
	if e, _ := root["error"].(bool); e {
		msg, _ := root["error_message"].(string)
		if strings.Contains(msg, "Only SELECT") {
			return fmt.Errorf("only SELECT queries are allowed")
		}
		return fmt.Errorf("the query does not parse: %s", msg)
	}
	stmts, _ := root["statements"].([]any)
	if len(stmts) != 1 {
		return fmt.Errorf("one query at a time (got %d statements)", len(stmts))
	}
	ctes := map[string]bool{}
	var tables []string
	var funcs []string
	walk(stmts[0], func(m map[string]any) {
		if cm, ok := m["cte_map"].(map[string]any); ok {
			if entries, ok := cm["map"].([]any); ok {
				for _, e := range entries {
					if em, ok := e.(map[string]any); ok {
						if k, ok := em["key"].(string); ok {
							ctes[strings.ToLower(k)] = true
						}
					}
				}
			}
		}
		switch m["type"] {
		case "BASE_TABLE":
			name, _ := m["table_name"].(string)
			schema, _ := m["schema_name"].(string)
			catalog, _ := m["catalog_name"].(string)
			if schema != "" || catalog != "" {
				name = catalog + "." + schema + "." + name
			}
			tables = append(tables, strings.ToLower(name))
		case "TABLE_FUNCTION":
			funcs = append(funcs, "table function")
		}
		if m["class"] == "FUNCTION" {
			if name, _ := m["function_name"].(string); forbidden.MatchString(name) {
				funcs = append(funcs, name)
			}
		}
	})
	if len(funcs) > 0 {
		return fmt.Errorf("functions that read outside the views (%s) are not allowed; read from the views %s",
			strings.Join(funcs, ", "), strings.Join(viewNames(), ", "))
	}
	for _, t := range tables {
		if _, ok := store.ViewColumns[t]; !ok && !ctes[t] {
			return fmt.Errorf("unknown table %q; the tables are %s", t, strings.Join(viewNames(), ", "))
		}
	}
	return nil
}

// forbidden are functions that reach outside the views: files, settings, the environment, other
// queries. DuckDB refuses most of them anyway (external access is off); this says why, early.
var forbidden = regexp.MustCompile(`(?i)^(read_|glob$|getenv$|current_setting$|sniff_|parquet_|query$|query_table$|duckdb_|pragma_|which_secret$|load$|install$)`)

func walk(v any, f func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		f(x)
		for _, c := range x {
			walk(c, f)
		}
	case []any:
		for _, c := range x {
			walk(c, f)
		}
	}
}

func viewNames() []string { return []string{"transactions", "expenses", "income", "balances"} }

// Prompt describes the views to the model, with the conventions and a few examples.
func Prompt(today time.Time) string {
	var cats []string
	for _, c := range category.All {
		cats = append(cats, fmt.Sprintf("'%s' (%s)", c.Name, c.Kind))
	}
	return fmt.Sprintf(`You write DuckDB SQL that answers questions about a person's personal finances in Romania.
Answer with one SELECT query. Today is %s; "this month", "last year" and the like are relative to today.
Questions may be in Romanian or English. Romanian months: ianuarie=1, februarie=2, martie=3, aprilie=4, mai=5, iunie=6,
iulie=7, august=8, septembrie=9, octombrie=10, noiembrie=11, decembrie=12.

Tables (views):
- expenses(id, date DATE, year INT, month INT, month_start DATE, bank, account, currency, amount DECIMAL, amount_ron DECIMAL,
  category, merchant, description): money spent. amount and amount_ron are POSITIVE for spending (a refund is negative).
- income(id, date, year, month, month_start, bank, account, currency, amount, amount_ron, category, merchant, description):
  money earned (salary, interest), positive.
- transactions(id, date, year, month, month_start, bank, account, currency, amount, amount_ron, kind, category, merchant,
  description): every movement on a bank account; amount is signed (negative leaves the account); kind is
  'expense', 'income', 'transfer' (between own accounts) or 'exchange' (between currencies).
- balances(bank, account, currency, balance): current balance of each account, in its own currency.

Categories: %s.
Words people use for them, and the condition to write (never search these words in merchant or description).
Write a category condition only when the question uses one of these words; a question that names a merchant
(Kaufland, Netflix, Bolt) filters on merchant alone, and 'Uncategorized', 'Expense' and 'Income' are never conditions:
- supermarket, alimente, cumpărături de la supermarket, groceries -> category = 'Groceries'
- mâncare, food (without more detail) -> category IN ('Groceries', 'Restaurants', 'Food Delivery')
- restaurant, cafenea, ieșit în oraș -> category = 'Restaurants'; livrare, delivery -> category = 'Food Delivery'
- benzină, motorină, combustibil -> category = 'Fuel'; taxi, transport -> category = 'Transport'
- chirie -> category = 'Rent'; utilități, curent, gaz, facturi -> category = 'Utilities'; telefon, internet -> category = 'Phone & Internet'
- abonamente -> category = 'Subscriptions'; sănătate, farmacie, doctor -> category = 'Health'
- haine, electronice, cumpărături (not food) -> category = 'Shopping'
- comisioane -> category = 'Fees'; numerar, cash, retrageri -> category = 'Cash'; salariu -> category = 'Salary' (in income)
- dobândă, interest -> category = 'Other Income' (in income)
Statements write Romanian without diacritics (DOBANDA, not dobândă): never put diacritics in a text pattern.

Rules:
- Spending comes from expenses, earnings from income; use transactions only for transfers or signed movements.
- Totals are in RON: sum amount_ron. Only for "in euros" / "în euro" filter currency = 'EUR' and sum amount (in EUR).
- For a range of dates use the date column: date >= DATE '2025-12-01' AND date < DATE '2026-03-01'.
  Never compare month numbers across a year boundary.
- To group by month use month_start (so that different years stay apart).
- Do not limit the period unless the question names one.
- An average per month: total each month (GROUP BY month_start), then average those totals.
- A comparison between periods returns one row per period with its total.
- "Which ..." / "each" / "fiecare" / "ce ... am plătit" asks for one row per merchant (GROUP BY merchant).
- Totals from expenses and income are computed in separate subqueries, never by joining the two views.
- Put parentheses around OR conditions.
- merchant is the merchant's name in capitals (KAUFLAND, NETFLIX.COM, BOLT.EU/R): match it with ILIKE '%%name%%'.
- Round money to 2 decimals. Give columns clear names. Order results meaningfully.

Examples:
Q: Cât am cheltuit pe benzină în 2025?
{"sql": "SELECT round(sum(amount_ron), 2) AS fuel_ron FROM expenses WHERE category = 'Fuel' AND year = 2025"}
Q: How much did I spend per category last month?
{"sql": "SELECT category, round(sum(amount_ron), 2) AS spent_ron FROM expenses WHERE month_start = date_trunc('month', DATE '%s' - INTERVAL 1 MONTH) GROUP BY category ORDER BY spent_ron DESC"}
Q: Cât am dat pe transport în mai 2026 față de iunie 2026?
{"sql": "SELECT month_start, round(sum(amount_ron), 2) AS transport_ron FROM expenses WHERE category = 'Transport' AND month_start IN (DATE '2026-05-01', DATE '2026-06-01') GROUP BY month_start ORDER BY month_start"}
Q: How much do I spend on restaurants per month on average?
{"sql": "SELECT round(avg(total), 2) AS avg_monthly_ron FROM (SELECT month_start, sum(amount_ron) AS total FROM expenses WHERE category = 'Restaurants' GROUP BY month_start)"}
Q: Cât am câștigat net în 2026 (venituri minus cheltuieli)?
{"sql": "SELECT round((SELECT sum(amount_ron) FROM income WHERE year = 2026) - (SELECT sum(amount_ron) FROM expenses WHERE year = 2026), 2) AS net_ron"}
Q: How often did I pay at Lidl or Profi between November 2025 and January 2026?
{"sql": "SELECT count(*) AS payments FROM expenses WHERE (merchant ILIKE '%%LIDL%%' OR merchant ILIKE '%%PROFI%%') AND date >= DATE '2025-11-01' AND date < DATE '2026-02-01'"}
`, today.Format("2006-01-02"), strings.Join(cats, ", "), today.Format("2006-01-02"))
}
