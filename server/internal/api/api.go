// Package api is Ledger's HTTP interface: a JSON API for the web UI, and the UI itself (built into
// the binary from web/, see embed.go).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/app"
	"github.com/YUStulinu/Ledger/server/internal/category"
	"github.com/YUStulinu/Ledger/server/internal/forecast"
)

type Server struct {
	App *app.App
	mux *http.ServeMux

	jobMu sync.Mutex
	job   *Job
}

// Job is a long-running import or categorization (the language model takes a while).
type Job struct {
	Kind     string `json:"kind"`
	Running  bool   `json:"running"`
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Error    string `json:"error,omitempty"`
	Result   any    `json:"result,omitempty"`
	Started  string `json:"started"`
	Finished string `json:"finished,omitempty"`
}

func New(a *app.App) *Server {
	s := &Server{App: a, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/status", s.status)
	s.mux.HandleFunc("GET /api/overview", s.overview)
	s.mux.HandleFunc("GET /api/categories", s.categories)
	s.mux.HandleFunc("GET /api/transactions", s.transactions)
	s.mux.HandleFunc("POST /api/transactions/category", s.correct)
	s.mux.HandleFunc("POST /api/import", s.importFiles)
	s.mux.HandleFunc("POST /api/categorize", s.categorize)
	s.mux.HandleFunc("GET /api/job", s.jobStatus)
	s.mux.HandleFunc("GET /api/recurring", s.recurring)
	s.mux.HandleFunc("GET /api/alerts", s.alerts)
	s.mux.HandleFunc("GET /api/forecast", s.forecast)
	s.mux.HandleFunc("POST /api/ask", s.ask)
	s.mux.Handle("/", uiHandler())
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The UI is served from the same origin; nothing else may call the API from a browser.
	if r.Method != "GET" && r.Header.Get("Origin") != "" && !sameOrigin(r) {
		http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "http://"+r.Host || o == "https://"+r.Host
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: %v", err)
	}
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var lines, uncategorized int
	s.App.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM bank_lines").Scan(&lines)
	s.App.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM ledger_transactions WHERE category = 'Uncategorized'").Scan(&uncategorized)
	writeJSON(w, map[string]any{
		"lines": lines, "uncategorized": uncategorized, "llm": s.App.ModelAvailable(), "data_end": s.App.DataEnd(ctx).Format("2006-01-02"),
		"model": shortName(s.App.Cfg.Model),
	})
}

func shortName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	type cat struct {
		Name, Kind, Hint string
		Assignable       bool
	}
	assign := map[string]bool{}
	for _, c := range category.Assignable() {
		assign[c] = true
	}
	var out []cat
	for _, c := range category.All {
		out = append(out, cat{c.Name, string(c.Kind), c.Hint, assign[c.Name]})
	}
	writeJSON(w, out)
}

// overview is the dashboard: balances, this month against the last, and the monthly series.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := s.App.Store.DB
	query := func(q string, args ...any) ([]map[string]any, error) {
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		var out []map[string]any
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			m := map[string]any{}
			for i, c := range cols {
				m[c] = jsonValue(vals[i])
			}
			out = append(out, m)
		}
		return out, rows.Err()
	}
	res := map[string]any{}
	parts := []struct {
		name, q string
	}{
		{"balances", "SELECT bank, account, currency, balance::DOUBLE AS balance FROM balances ORDER BY bank, currency"},
		{"monthly", `SELECT strftime(month_start, '%Y-%m') AS month, category, round(sum(amount_ron), 2)::DOUBLE AS amount
			FROM expenses GROUP BY ALL ORDER BY month, amount DESC`},
		{"income", `SELECT strftime(month_start, '%Y-%m') AS month, round(sum(amount_ron), 2)::DOUBLE AS amount FROM income GROUP BY ALL ORDER BY month`},
		{"top_merchants", `SELECT merchant, category, round(sum(amount_ron), 2)::DOUBLE AS amount, count(*) AS count FROM expenses
			WHERE date > (SELECT max(date) FROM transactions) - INTERVAL 90 DAY GROUP BY ALL ORDER BY amount DESC LIMIT 10`},
	}
	for _, p := range parts {
		v, err := query(p.q)
		if err != nil {
			fail(w, 500, fmt.Errorf("%s: %w", p.name, err))
			return
		}
		res[p.name] = v
	}
	days, err := forecast.DailyBalances(ctx, db)
	if err != nil {
		fail(w, 500, err)
		return
	}
	var series []map[string]any
	for i, d := range days {
		if i%3 == 0 || i == len(days)-1 { // every third day is enough for the chart
			series = append(series, map[string]any{"date": d.Date.Format("2006-01-02"), "ron": d.RON})
		}
	}
	res["balance_history"] = series
	writeJSON(w, res)
}

func jsonValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.Format("2006-01-02")
	case fmt.Stringer:
		if f, err := strconv.ParseFloat(x.String(), 64); err == nil {
			return f
		}
		return x.String()
	}
	return v
}

// transactions lists bank-account movements, newest first, with filters.
func (s *Server) transactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	where := []string{"a.type = 'asset'", "t.kind <> 'opening'"}
	var args []any
	if c := q.Get("category"); c != "" {
		where, args = append(where, "t.category = ?"), append(args, c)
	}
	if m := q.Get("month"); m != "" {
		where, args = append(where, "strftime(t.date, '%Y-%m') = ?"), append(args, m)
	}
	if text := q.Get("q"); text != "" {
		where, args = append(where, "(t.merchant ILIKE ? OR t.description ILIKE ?)"), append(args, "%"+text+"%", "%"+text+"%")
	}
	if q.Get("uncategorized") == "1" {
		where = append(where, "t.category = 'Uncategorized'")
	}
	sql := `SELECT t.id, t.date, a.bank, p.currency, (p.amount / 100.0)::DOUBLE, t.kind, t.category, coalesce(t.merchant, ''),
		coalesce(t.description, ''), coalesce(p.line_key, ''), coalesce(l.source, ''), count(*) OVER ()
		FROM ledger_postings p JOIN ledger_transactions t ON t.id = p.tx_id JOIN ledger_accounts a ON a.name = p.account
		LEFT JOIN labels l ON l.key = p.line_key WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY t.date DESC, t.id DESC LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.App.Store.DB.QueryContext(r.Context(), sql, args...)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	type tx struct {
		ID          int64   `json:"id"`
		Date        string  `json:"date"`
		Bank        string  `json:"bank"`
		Currency    string  `json:"currency"`
		Amount      float64 `json:"amount"`
		Kind        string  `json:"kind"`
		Category    string  `json:"category"`
		Merchant    string  `json:"merchant"`
		Description string  `json:"description"`
		Key         string  `json:"key"`
		Source      string  `json:"source"`
	}
	out := struct {
		Total int  `json:"total"`
		Items []tx `json:"items"`
	}{Items: []tx{}}
	for rows.Next() {
		var t tx
		var d time.Time
		if err := rows.Scan(&t.ID, &d, &t.Bank, &t.Currency, &t.Amount, &t.Kind, &t.Category, &t.Merchant, &t.Description, &t.Key, &t.Source, &out.Total); err != nil {
			fail(w, 500, err)
			return
		}
		t.Date = d.Format("2006-01-02")
		out.Items = append(out.Items, t)
	}
	writeJSON(w, out)
}

func (s *Server) correct(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      string `json:"key"`
		Category string `json:"category"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, 400, err)
		return
	}
	n, err := s.App.Correct(r.Context(), req.Key, req.Category)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, map[string]int{"updated": n})
}

// startJob runs work in the background; only one at a time.
func (s *Server) startJob(kind string, work func(ctx context.Context, progress func(done, total int)) (any, error)) (*Job, error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.job != nil && s.job.Running {
		return nil, fmt.Errorf("a %s is still running", s.job.Kind)
	}
	job := &Job{Kind: kind, Running: true, Started: time.Now().Format(time.RFC3339)}
	s.job = job
	go func() {
		res, err := work(context.Background(), func(done, total int) {
			s.jobMu.Lock()
			job.Done, job.Total = done, total
			s.jobMu.Unlock()
		})
		s.jobMu.Lock()
		defer s.jobMu.Unlock()
		job.Running, job.Result, job.Finished = false, res, time.Now().Format(time.RFC3339)
		if err != nil {
			job.Error = err.Error()
		}
	}()
	return job, nil
}

func (s *Server) jobStatus(w http.ResponseWriter, r *http.Request) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.job == nil {
		writeJSON(w, map[string]any{"running": false})
		return
	}
	writeJSON(w, s.job)
}

func (s *Server) importFiles(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		fail(w, 400, err)
		return
	}
	files := map[string][]byte{}
	for _, fhs := range r.MultipartForm.File {
		for _, fh := range fhs {
			f, err := fh.Open()
			if err != nil {
				fail(w, 400, err)
				return
			}
			data, err := io.ReadAll(io.LimitReader(f, 32<<20))
			f.Close()
			if err != nil {
				fail(w, 400, err)
				return
			}
			files[fh.Filename] = data
		}
	}
	if len(files) == 0 {
		fail(w, 400, fmt.Errorf("no files"))
		return
	}
	// The files are read and checked right away (so a wrong file fails now); categorizing, which
	// may take the language model a minute, runs in the background.
	var results []any
	for name, data := range files {
		res, err := s.App.Store.Import(r.Context(), name, data)
		if err != nil {
			fail(w, 400, err)
			return
		}
		results = append(results, res)
	}
	job, err := s.startJob("categorization", func(ctx context.Context, progress func(int, int)) (any, error) {
		return s.App.Categorize(ctx, progress)
	})
	if err != nil {
		writeJSON(w, map[string]any{"imported": results, "note": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"imported": results, "job": job})
}

func (s *Server) categorize(w http.ResponseWriter, r *http.Request) {
	job, err := s.startJob("categorization", func(ctx context.Context, progress func(int, int)) (any, error) {
		return s.App.Categorize(ctx, progress)
	})
	if err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, job)
}

func (s *Server) recurring(w http.ResponseWriter, r *http.Request) {
	rec, _, err := s.App.Insights(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, rec)
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	_, alerts, err := s.App.Insights(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, alerts)
}

func (s *Server) forecast(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	threshold, _ := strconv.ParseFloat(q.Get("threshold"), 64)
	opt := forecast.Options{Days: days, Threshold: threshold}
	if from := q.Get("from"); from != "" {
		t, err := time.Parse("2006-01-02", from)
		if err != nil {
			fail(w, 400, err)
			return
		}
		opt.From = t
	}
	res, err := s.App.Forecast(r.Context(), opt)
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Question) == "" {
		fail(w, 400, fmt.Errorf("ask a question"))
		return
	}
	a, err := s.App.Ask(r.Context(), req.Question)
	if err != nil {
		if a != nil { // the rejected queries, for the UI to show
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(422)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "answer": a})
			return
		}
		fail(w, 422, err)
		return
	}
	writeJSON(w, a)
}
