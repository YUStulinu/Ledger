// Package app ties Ledger together for the command line and the web server: the store, the local
// language model (started on first use), the Python side, and the operations both offer.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/ask"
	"github.com/YUStulinu/Ledger/server/internal/categorize"
	"github.com/YUStulinu/Ledger/server/internal/forecast"
	"github.com/YUStulinu/Ledger/server/internal/insights"
	"github.com/YUStulinu/Ledger/server/internal/llm"
	"github.com/YUStulinu/Ledger/server/internal/ml"
	"github.com/YUStulinu/Ledger/server/internal/store"
)

// Config says where everything is.
type Config struct {
	Root      string // the repository (ml/, models/, tools/)
	DB        string // the ledger file
	Model     string // the .gguf model
	LlamaBin  string // llama-server
	LlamaURL  string // an already running server to use instead of starting one
	LlamaPort int
	NoLLM     bool // never use the language model (rules and corrections only)
}

// Defaults fills in what is not set, relative to the repository root.
func (c Config) Defaults() Config {
	if c.Root == "" {
		c.Root = FindRoot()
	}
	if c.DB == "" {
		c.DB = filepath.Join(c.Root, "data", "ledger.duckdb")
	}
	if c.Model == "" {
		c.Model = env("LEDGER_MODEL", filepath.Join(c.Root, "models", "Qwen2.5-7B-Instruct-Q4_K_M.gguf"))
	}
	if c.LlamaBin == "" {
		c.LlamaBin = env("LEDGER_LLAMA_SERVER", llm.DefaultBinary(c.Root))
	}
	if c.LlamaURL == "" {
		c.LlamaURL = os.Getenv("LEDGER_LLM_URL")
	}
	if c.LlamaPort == 0 {
		c.LlamaPort, _ = strconv.Atoi(env("LEDGER_LLAMA_PORT", "8091"))
	}
	return c
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// FindRoot walks up from the working directory and from the executable to the directory that
// holds ml/ledger_ml (the repository).
func FindRoot() string {
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, s := range starts {
		for d := s; ; d = filepath.Dir(d) {
			if st, err := os.Stat(filepath.Join(d, "ml", "ledger_ml")); err == nil && st.IsDir() {
				return d
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	if len(starts) > 0 {
		return starts[0]
	}
	return "."
}

type App struct {
	Cfg   Config
	Store *store.Store
	ML    *ml.Bridge

	mu     sync.Mutex
	server *llm.Server
	model  llm.Model
}

func Open(cfg Config) (*App, error) {
	cfg = cfg.Defaults()
	s, err := store.Open(cfg.DB)
	if err != nil {
		return nil, err
	}
	return &App{Cfg: cfg, Store: s, ML: ml.Find(cfg.Root)}, nil
}

// Close stops the language model if Ledger started it, and closes the store.
func (a *App) Close() {
	a.mu.Lock()
	if a.server != nil {
		a.server.Stop()
	}
	a.mu.Unlock()
	a.Store.Close()
}

// UseModel replaces the language model (tests, the evaluation).
func (a *App) UseModel(m llm.Model) { a.model = m }

// Model returns the language model, starting llama-server on first use.
func (a *App) Model(ctx context.Context) (llm.Model, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.model != nil {
		return a.model, nil
	}
	if a.Cfg.NoLLM {
		return nil, fmt.Errorf("the language model is turned off (--no-llm)")
	}
	if a.Cfg.LlamaURL != "" {
		a.model = llm.NewClient(a.Cfg.LlamaURL)
		return a.model, nil
	}
	srv, err := llm.Start(ctx, llm.ServerConfig{Binary: a.Cfg.LlamaBin, Model: a.Cfg.Model, Port: a.Cfg.LlamaPort,
		Log: filepath.Join(filepath.Dir(a.Cfg.DB), "llama-server.log")})
	if err != nil {
		return nil, err
	}
	a.server, a.model = srv, srv
	return a.model, nil
}

// ModelAvailable says whether a language model can be used (without starting it).
func (a *App) ModelAvailable() bool {
	if a.model != nil || a.Cfg.LlamaURL != "" {
		return true
	}
	if a.Cfg.NoLLM {
		return false
	}
	_, e1 := os.Stat(a.Cfg.Model)
	_, e2 := os.Stat(a.Cfg.LlamaBin)
	return e1 == nil && e2 == nil
}

// --- operations -------------------------------------------------------------------------------------

// Import stores statement files, then categorizes and rebuilds the ledger.
func (a *App) Import(ctx context.Context, files map[string][]byte, progress func(done, total int)) ([]*store.ImportResult, *categorize.Report, error) {
	var results []*store.ImportResult
	for name, data := range files {
		r, err := a.Store.Import(ctx, name, data)
		if err != nil {
			return results, nil, err
		}
		results = append(results, r)
	}
	rep, err := a.Categorize(ctx, progress)
	return results, rep, err
}

// Categorize labels what has no category yet, and rebuilds the ledger.
func (a *App) Categorize(ctx context.Context, progress func(done, total int)) (*categorize.Report, error) {
	return a.categorize(ctx, false, progress)
}

// CategorizeRelabel also asks the classifier and the language model again about lines they
// labeled before (your own corrections stay).
func (a *App) CategorizeRelabel(ctx context.Context, progress func(done, total int)) (*categorize.Report, error) {
	return a.categorize(ctx, true, progress)
}

func (a *App) categorize(ctx context.Context, relabel bool, progress func(done, total int)) (*categorize.Report, error) {
	opt := categorize.Options{ML: a.ML, ModelPath: filepath.Join(filepath.Dir(a.Cfg.DB), "classifier.npz"), Progress: progress, Relabel: relabel}
	if a.ModelAvailable() {
		m, err := a.Model(ctx)
		if err != nil {
			return nil, err
		}
		opt.LLM = m
	}
	// Matching transfers first: lines that turn out to be transfers need no category.
	if _, err := a.Store.Rebuild(ctx); err != nil {
		return nil, err
	}
	rep, err := categorize.Run(ctx, a.Store, opt)
	if err != nil {
		return nil, err
	}
	_, err = a.Store.Rebuild(ctx)
	return rep, err
}

// Correct sets a category by hand (for every transaction of that merchant) and rebuilds.
func (a *App) Correct(ctx context.Context, lineKey, cat string) (int, error) {
	n, err := a.Store.Correct(ctx, lineKey, cat)
	if err != nil {
		return 0, err
	}
	_, err = a.Store.Rebuild(ctx)
	return n, err
}

// DataEnd is the date of the last transaction ("today" for questions and the forecast).
func (a *App) DataEnd(ctx context.Context) time.Time {
	var d *time.Time
	if err := a.Store.DB.QueryRowContext(ctx, "SELECT max(date) FROM bank_lines").Scan(&d); err != nil || d == nil {
		return time.Now()
	}
	return *d
}

// Ask answers a question in plain language.
func (a *App) Ask(ctx context.Context, question string) (*ask.Answer, error) {
	m, err := a.Model(ctx)
	if err != nil {
		return nil, err
	}
	return ask.Ask(ctx, a.Store.DB, m, question, a.DataEnd(ctx))
}

// Insights finds recurring payments and alerts.
func (a *App) Insights(ctx context.Context) ([]insights.Recurring, []insights.Alert, error) {
	txs, err := insights.Load(ctx, a.Store.DB)
	if err != nil {
		return nil, nil, err
	}
	return insights.FindRecurring(txs, a.DataEnd(ctx)), insights.FindAlerts(txs), nil
}

// Forecast runs the balance forecast.
func (a *App) Forecast(ctx context.Context, opt forecast.Options) (*forecast.Result, error) {
	return forecast.Run(ctx, a.Store.DB, a.ML, opt)
}
