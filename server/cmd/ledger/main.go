// ledger: your personal accountant, from your bank statements, on your own machine.
//
//	ledger import <statement files...>     read, check and store statements: any bank's CSV, camt.053 XML or
//	                                       OFX/QFX; BT, ING and Revolut exports and BT PDFs. For a CSV it does not
//	                                       recognize: --map date=Col,amount=Col,... and --date-format, --currency,
//	                                       --account, --flip-sign
//	ledger base [CUR] [--reset-rates]      the currency totals are in (set by the first import; EUR, USD, GBP...)
//	ledger rates <file> | --fetch          exchange rates: --fetch downloads the European Central Bank's; or a file
//	                                       (the ECB's or the BNR's, or date,currency,value CSV in the base currency)
//	ledger categorize [--relabel]          sort what is not sorted yet
//	ledger correct <key> <category>        set a category by hand (for every transaction of that merchant)
//	ledger correct --file list.json        many corrections at once ([{"key", "category"}]), one rebuild
//	ledger merchants [--uncategorized]     each merchant once, with one line's key and description
//	ledger transactions [--category C] [--month YYYY-MM] [--uncategorized]
//	ledger recurring                       subscriptions, bills and income on a schedule
//	ledger alerts                          double charges, unusual amounts, new fees
//	ledger forecast [--days 90] [--from YYYY-MM-DD]
//	ledger ask "question"                  a question in Romanian or English, answered with checked SQL
//	ledger schema                          the views, categories and rules a query may use
//	ledger query "SELECT ..."              run your own SQL (an AI agent's), with the same checks as ask
//	ledger serve [--addr 127.0.0.1:8080]   the web app
//
// Common flags: --db (default data/ledger.duckdb), --no-llm, --json.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/api"
	"github.com/YUStulinu/Ledger/server/internal/app"
	"github.com/YUStulinu/Ledger/server/internal/ask"
	"github.com/YUStulinu/Ledger/server/internal/category"
	"github.com/YUStulinu/Ledger/server/internal/forecast"
	"github.com/YUStulinu/Ledger/server/internal/importer"
	"github.com/YUStulinu/Ledger/server/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		usage()
		return
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	db := fs.String("db", "", "the ledger file (default data/ledger.duckdb in the repository)")
	noLLM := fs.Bool("no-llm", false, "do not use the language model")
	asJSON := fs.Bool("json", false, "print JSON")
	addr := fs.String("addr", "127.0.0.1:8080", "serve: the address to listen on")
	days := fs.Int("days", 90, "forecast: days ahead")
	from := fs.String("from", "", "forecast: backtest from this date (YYYY-MM-DD)")
	threshold := fs.Float64("threshold", 0, "forecast: the balance to stay above")
	relabel := fs.Bool("relabel", false, "categorize: ask again about lines the models already labeled")
	cat := fs.String("category", "", "transactions: only this category")
	month := fs.String("month", "", "transactions: only this month (YYYY-MM)")
	uncategorized := fs.Bool("uncategorized", false, "transactions: only uncategorized ones")
	labels := fs.String("labels", "", "import: also apply the categories of a synthetic truth.json (for evaluation)")
	file := fs.String("file", "", `correct: a JSON list of {"key": ..., "category": ...}, applied with one rebuild`)
	mapCols := fs.String("map", "", "import: name a CSV's columns, e.g. date=Posting Date,amount=Amount,description=Payee")
	dateFormat := fs.String("date-format", "", "import: DMY, MDY, YMD or a Go layout, when a CSV's dates are ambiguous")
	currency := fs.String("currency", "", "import: the account's currency when the file does not say it")
	account := fs.String("account", "", "import: a name for the account when the file does not give one")
	flipSign := fs.Bool("flip-sign", false, "import: positive amounts are money out (credit-card exports)")
	fetch := fs.Bool("fetch", false, "rates: download the European Central Bank's rates")
	resetRates := fs.Bool("reset-rates", false, "base: drop the stored rates (they are in the old base)")
	fs.Parse(reorder(args))

	a, err := app.Open(app.Config{DB: *db, NoLLM: *noLLM})
	if err != nil {
		fatal(err)
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out := func(v any, text func()) {
		if *asJSON || text == nil {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(v)
		} else {
			text()
		}
	}

	switch cmd {
	case "import":
		if fs.NArg() == 0 {
			fatal(fmt.Errorf("import: name the statement files"))
		}
		var results []*store.ImportResult
		for _, f := range fs.Args() {
			data, err := os.ReadFile(f)
			if err != nil {
				fatal(err)
			}
			name := filepath.Base(f)
			opt := importer.Options{DateFormat: *dateFormat, Currency: *currency, Account: *account, FlipSign: *flipSign}
			if *mapCols != "" {
				opt.Map = map[string]string{}
				for _, kv := range strings.Split(*mapCols, ",") {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						fatal(fmt.Errorf("--map wants field=Column pairs separated by commas, not %q", kv))
					}
					opt.Map[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
				}
			}
			r, err := a.Store.ImportWith(ctx, name, data, opt)
			if err != nil {
				fatal(err)
			}
			results = append(results, r)
			if r.AlreadyImported {
				fmt.Fprintf(os.Stderr, "%s: imported before\n", name)
			} else {
				fmt.Fprintf(os.Stderr, "%s: %s %s (%s), %d rows: %d new, %d already known\n", name, r.Bank, r.Account, r.Format, r.Rows, r.New, r.Duplicates)
			}
			for _, w := range r.Warnings {
				fmt.Fprintf(os.Stderr, "  %s\n", w)
			}
		}
		if *labels != "" {
			n, err := applyTruthLabels(ctx, a, *labels)
			if err != nil {
				fatal(err)
			}
			fmt.Fprintf(os.Stderr, "applied %d true categories from %s\n", n, *labels)
		}
		rep, err := a.Categorize(ctx, progress)
		if err != nil {
			fatal(err)
		}
		out(map[string]any{"imported": results, "categorized": rep}, func() {
			fmt.Printf("categorized: %d by rules, %d by your corrections, %d by the classifier, %d by the language model (%d merchants asked), %d left\n",
				rep.Rules, rep.Corrections, rep.Model, rep.LLM, rep.Merchants, rep.Left)
		})
		if missing, _ := a.Store.MissingRates(ctx); len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "note: no exchange rates for %s yet: `ledger rates --fetch` downloads the European Central Bank's\n", strings.Join(missing, ", "))
		}

	case "base":
		if fs.NArg() == 1 {
			if err := a.Store.SetBase(ctx, fs.Arg(0), *resetRates); err != nil {
				fatal(err)
			}
			if _, err := a.Store.Rebuild(ctx); err != nil {
				fatal(err)
			}
		}
		base, err := a.Store.Base(ctx)
		if err != nil {
			fatal(err)
		}
		out(map[string]string{"base": base}, func() { fmt.Println(base) })

	case "rates":
		var data []byte
		var err error
		switch {
		case *fetch:
			data, err = store.FetchECB(ctx)
		case fs.NArg() == 1:
			data, err = os.ReadFile(fs.Arg(0))
		default:
			fatal(fmt.Errorf("rates: --fetch, or name the file"))
		}
		if err != nil {
			fatal(err)
		}
		base, err := a.Store.Base(ctx)
		if err != nil {
			fatal(err)
		}
		if base == "" {
			fatal(fmt.Errorf("rates: import a statement first, or set the base currency (ledger base EUR)"))
		}
		rates, err := store.ParseRates(data, base)
		if err != nil {
			fatal(err)
		}
		n, err := a.Store.AddRates(ctx, rates)
		if err != nil {
			fatal(err)
		}
		if _, err := a.Store.Rebuild(ctx); err != nil {
			fatal(err)
		}
		fmt.Printf("stored %d exchange rates\n", n)

	case "categorize":
		categorizeFn := a.Categorize
		if *relabel {
			categorizeFn = a.CategorizeRelabel
		}
		rep, err := categorizeFn(ctx, progress)
		if err != nil {
			fatal(err)
		}
		out(rep, func() {
			fmt.Printf("categorized: %d by rules, %d by your corrections, %d by the classifier, %d by the language model, %d left\n",
				rep.Rules, rep.Corrections, rep.Model, rep.LLM, rep.Left)
		})

	case "correct":
		if *file != "" {
			correctMany(ctx, a, *file, *asJSON)
			return
		}
		if fs.NArg() != 2 {
			fatal(fmt.Errorf("correct <key> <category>, or correct --file list.json; categories: %s", strings.Join(category.Assignable(), ", ")))
		}
		n, err := a.Correct(ctx, fs.Arg(0), fs.Arg(1))
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%d transactions of that merchant are now %s\n", n, fs.Arg(1))

	case "transactions":
		listTransactions(ctx, a, *cat, *month, *uncategorized, *asJSON)

	case "merchants":
		listMerchants(ctx, a, *uncategorized, *asJSON)

	case "recurring":
		rec, _, err := a.Insights(ctx)
		if err != nil {
			fatal(err)
		}
		out(rec, func() {
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "MERCHANT\tCATEGORY\tCADENCE\tAMOUNT\tNEXT\tPER YEAR (RON)\tNOTE")
			for _, r := range rec {
				note := ""
				switch {
				case !r.Active:
					note = "ended"
				case r.ChangedOn != nil:
					note = fmt.Sprintf("was %.2f until %s", r.PreviousAmount, r.ChangedOn.Format("2006-01"))
				case r.New:
					note = "new"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%.2f %s\t%s\t%.0f\t%s\n", r.Merchant, r.Category, r.Cadence, r.Amount, r.Currency,
					r.Next.Format("2006-01-02"), r.YearlyRON, note)
			}
			tw.Flush()
		})

	case "alerts":
		_, alerts, err := a.Insights(ctx)
		if err != nil {
			fatal(err)
		}
		out(alerts, func() {
			for _, al := range alerts {
				fmt.Printf("%s  %-15s %10.2f %s  %s: %s\n", al.Tx.Date.Format("2006-01-02"), al.Kind, al.Tx.Amount, al.Tx.Currency, al.Tx.Merchant, al.Message)
			}
		})

	case "forecast":
		opt := forecast.Options{Days: *days, Threshold: *threshold}
		if *from != "" {
			t, err := time.Parse("2006-01-02", *from)
			if err != nil {
				fatal(err)
			}
			opt.From = t
		}
		r, err := a.Forecast(ctx, opt)
		if err != nil {
			fatal(err)
		}
		out(r, func() {
			fmt.Printf("balance now: %.2f RON\n", r.StartBalance)
			fmt.Printf("in %d days:  %.2f RON (80%% likely between %.2f and %.2f)\n", len(r.Days), r.End.P50, r.End.P10, r.End.P90)
			fmt.Printf("lowest:      %.2f RON around %s\n", r.Lowest.P50, r.Lowest.Date)
			fmt.Printf("chance of going below %.0f RON: %.0f%%\n", r.Threshold, 100*r.ChanceBelowThreshold)
			if len(r.Actual) > 0 {
				fmt.Printf("backtest: actual end %.2f RON, error %.2f; the real balance was inside the band on %.0f%% of days\n",
					r.Actual[len(r.Actual)-1], r.EndError, 100*r.Coverage1090)
			}
		})

	case "ask":
		q := strings.Join(fs.Args(), " ")
		if q == "" {
			fatal(fmt.Errorf(`ask "your question"`))
		}
		ans, err := a.Ask(ctx, q)
		if err != nil {
			if *asJSON && ans != nil {
				out(map[string]any{"error": err.Error(), "answer": ans}, nil)
				os.Exit(1)
			}
			fatal(err)
		}
		out(ans, func() {
			fmt.Printf("-- %s\n", ans.SQL)
			printTable(ans.Columns, ans.Rows)
		})

	case "schema":
		base, _ := a.Store.Base(ctx)
		fmt.Print(ask.Prompt(a.DataEnd(ctx), base))

	case "query":
		// The same checks and read-only run as ask, for SQL written elsewhere (by an AI agent): one
		// SELECT over the four views, no table functions, rolled back, 10 s and 500 rows at most.
		q := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.Join(fs.Args(), " ")), ";"))
		if q == "" {
			fatal(fmt.Errorf(`query "SELECT ..." (ledger schema lists the views)`))
		}
		cols, rows, err := ask.Run(ctx, a.Store.DB, q)
		if err != nil {
			fatal(err)
		}
		out(map[string]any{"sql": q, "columns": cols, "rows": rows}, func() { printTable(cols, rows) })

	case "serve":
		srv := &http.Server{Addr: *addr, Handler: api.New(a), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			srv.Shutdown(sctx)
		}()
		fmt.Printf("Ledger on http://%s/ (ledger: %s)\n", *addr, a.Cfg.DB)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal(err)
		}

	default:
		usage()
		os.Exit(2)
	}
}

// reorder puts flags before positional arguments, so `ledger import a.csv --db x` works too.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && takesValue(args[i]) && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			pos = append(pos, args[i])
		}
	}
	return append(flags, pos...)
}

func takesValue(f string) bool {
	switch strings.TrimLeft(f, "-") {
	case "no-llm", "json", "relabel", "uncategorized", "flip-sign", "fetch", "reset-rates":
		return false
	}
	return true
}

func progress(done, total int) {
	fmt.Fprintf(os.Stderr, "\rlanguage model: %d/%d merchants", done, total)
	if done == total {
		fmt.Fprintln(os.Stderr)
	}
}

func printTable(cols []string, rows [][]any) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.ToUpper(strings.Join(cols, "\t")))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = cellString(v)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
}

func cellString(v any) string {
	switch x := v.(type) {
	case float64:
		return fmt.Sprintf("%.2f", x)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func listTransactions(ctx context.Context, a *app.App, cat, month string, uncategorized, asJSON bool) {
	where := []string{"a.type = 'asset'", "t.kind <> 'opening'"}
	var args []any
	if cat != "" {
		where, args = append(where, "t.category = ?"), append(args, cat)
	}
	if month != "" {
		where, args = append(where, "strftime(t.date, '%Y-%m') = ?"), append(args, month)
	}
	if uncategorized {
		where = append(where, "t.category = 'Uncategorized'")
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT t.date, a.bank, (p.amount / 100.0)::DOUBLE, p.currency, t.category, coalesce(t.merchant, ''),
		coalesce(p.line_key, '') FROM ledger_postings p JOIN ledger_transactions t ON t.id = p.tx_id JOIN ledger_accounts a ON a.name = p.account
		WHERE `+strings.Join(where, " AND ")+" ORDER BY t.date, t.id", args...)
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	type row struct {
		Date                               string
		Bank, Currency, Category, Merchant string
		Amount                             float64
		Key                                string
	}
	var list []row
	for rows.Next() {
		var r row
		var d time.Time
		if err := rows.Scan(&d, &r.Bank, &r.Amount, &r.Currency, &r.Category, &r.Merchant, &r.Key); err != nil {
			fatal(err)
		}
		r.Date = d.Format("2006-01-02")
		list = append(list, r)
	}
	if asJSON {
		json.NewEncoder(os.Stdout).Encode(list)
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "DATE\tBANK\tAMOUNT\tCATEGORY\tMERCHANT\tKEY")
	for _, r := range list {
		fmt.Fprintf(tw, "%s\t%s\t%.2f %s\t%s\t%s\t%s\n", r.Date, r.Bank, r.Amount, r.Currency, r.Category, r.Merchant, r.Key)
	}
	tw.Flush()
}

// listMerchants lists each merchant once: its category, how many lines, and one line's key and
// description, which is what deciding a category (and `correct`) needs.
func listMerchants(ctx context.Context, a *app.App, uncategorized, asJSON bool) {
	where := "a.type = 'asset' AND t.kind <> 'opening'"
	if uncategorized {
		where += " AND t.category = 'Uncategorized'"
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT coalesce(t.merchant, ''), t.category, count(*), min(p.line_key), arg_min(b.description, p.line_key)
		FROM ledger_postings p JOIN ledger_transactions t ON t.id = p.tx_id JOIN ledger_accounts a ON a.name = p.account
		LEFT JOIN bank_lines b ON b.key = p.line_key
		WHERE `+where+" GROUP BY 1, 2 ORDER BY 3 DESC, 1")
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	type merchant struct {
		Merchant    string `json:"merchant"`
		Category    string `json:"category"`
		Lines       int    `json:"lines"`
		Key         string `json:"key"`
		Description string `json:"description"`
	}
	var list []merchant
	for rows.Next() {
		var m merchant
		var key, desc *string
		if err := rows.Scan(&m.Merchant, &m.Category, &m.Lines, &key, &desc); err != nil {
			fatal(err)
		}
		if key != nil {
			m.Key = *key
		}
		if desc != nil {
			m.Description = *desc
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		fatal(err)
	}
	if asJSON {
		json.NewEncoder(os.Stdout).Encode(list)
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MERCHANT\tCATEGORY\tLINES\tKEY")
	for _, m := range list {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", m.Merchant, m.Category, m.Lines, m.Key)
	}
	tw.Flush()
}

// correctMany applies a list of corrections (an AI agent's, say) and rebuilds the ledger once. Every
// category is checked before anything is stored; a key that is not found is reported and skipped.
func correctMany(ctx context.Context, a *app.App, path string, asJSON bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	var list []struct {
		Key      string `json:"key"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		fatal(fmt.Errorf("%s: want a JSON list of {\"key\": ..., \"category\": ...}: %w", path, err))
	}
	assignable := map[string]bool{}
	for _, c := range category.Assignable() {
		assignable[c] = true
	}
	for _, c := range list {
		if !assignable[c.Category] {
			fatal(fmt.Errorf("category %q (for %s) cannot be set; categories: %s", c.Category, c.Key, strings.Join(category.Assignable(), ", ")))
		}
	}
	type result struct {
		Key      string `json:"key"`
		Category string `json:"category"`
		Lines    int    `json:"lines"`
		Error    string `json:"error,omitempty"`
	}
	var out []result
	failed := 0
	for _, c := range list {
		n, err := a.Store.Correct(ctx, c.Key, c.Category)
		r := result{Key: c.Key, Category: c.Category, Lines: n}
		if err != nil {
			r.Error, failed = err.Error(), failed+1
		}
		out = append(out, r)
	}
	if _, err := a.Store.Rebuild(ctx); err != nil {
		fatal(err)
	}
	if asJSON {
		json.NewEncoder(os.Stdout).Encode(out)
	} else {
		lines := 0
		for _, r := range out {
			lines += r.Lines
			if r.Error != "" {
				fmt.Fprintf(os.Stderr, "%s: %s\n", r.Key, r.Error)
			}
		}
		fmt.Printf("%d corrections, %d transactions relabeled, %d failed\n", len(out)-failed, lines, failed)
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// applyTruthLabels labels the lines of the synthetic statements with their true categories (the
// evaluation uses it to measure questions apart from categorization).
func applyTruthLabels(ctx context.Context, a *app.App, path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var tr struct {
		Accounts     map[string]string `json:"accounts"`
		Transactions []struct {
			Date, Account, Currency, Category, Kind string
			Amount                                  int64
		} `json:"transactions"`
	}
	if err := json.Unmarshal(data, &tr); err != nil {
		return 0, err
	}
	id := map[string]string{"bt": tr.Accounts["bt"], "ing": "ING", "revolut": "Revolut"}
	seen := map[string]int{}
	var labels []store.Label
	for _, t := range tr.Transactions {
		base := fmt.Sprintf("%s|%s|%s|%d", id[t.Account], t.Currency, t.Date, t.Amount)
		key := fmt.Sprintf("%s|%d", base, seen[base])
		seen[base]++
		if t.Kind != "transfer" && t.Kind != "exchange" {
			labels = append(labels, store.Label{Key: key, Category: t.Category, Source: store.SourceUser, Confidence: 1})
		}
	}
	n, err := a.Store.SetLabels(ctx, labels)
	if err != nil {
		return 0, err
	}
	_, err = a.Store.Rebuild(ctx)
	return n, err
}

func usage() {
	fmt.Fprint(os.Stderr, `ledger: your personal accountant, from your bank statements

  ledger import <files...>         any bank's CSV, camt.053 or OFX/QFX; BT, ING, Revolut exports and BT PDFs
                                   (--map date=Col,amount=Col,... --date-format DMY|MDY --currency EUR --flip-sign)
  ledger base [CUR]                the currency totals are in
  ledger rates --fetch | <file>    exchange rates (the ECB's, downloaded; or an ECB/BNR file)
  ledger categorize [--relabel]
  ledger correct <key> <category>  or  ledger correct --file list.json
  ledger merchants [--uncategorized]
  ledger transactions [--category C] [--month YYYY-MM] [--uncategorized]
  ledger recurring | alerts
  ledger forecast [--days 90] [--from YYYY-MM-DD] [--threshold RON]
  ledger ask "question"
  ledger schema                    what a query may read
  ledger query "SELECT ..."        your own SQL, checked and run read-only
  ledger serve [--addr 127.0.0.1:8080]

flags: --db FILE, --no-llm, --json
`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
