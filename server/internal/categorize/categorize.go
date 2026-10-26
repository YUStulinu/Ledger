// Package categorize gives every bank line a category, in steps from the most to the least certain:
//
//  1. rules: what the statement says outright (a bank fee, a cash withdrawal, interest, a salary,
//     a top-up between your own accounts);
//  2. your corrections: a category you set for a merchant applies to all its transactions;
//  3. the classifier trained on your corrections (Python), when it is confident;
//  4. the local language model, for what is left, one call per batch of merchants (not per line),
//     shown your corrections as examples and constrained to answer with an existing category.
//
// A step never overrides an earlier, more trusted one (see store.SetLabels).
package categorize

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/YUStulinu/Ledger/server/internal/category"
	"github.com/YUStulinu/Ledger/server/internal/llm"
	"github.com/YUStulinu/Ledger/server/internal/ml"
	"github.com/YUStulinu/Ledger/server/internal/money"
	"github.com/YUStulinu/Ledger/server/internal/store"
)

// Options says which steps are available.
type Options struct {
	LLM llm.Model  // nil: no language model
	ML  *ml.Bridge // nil: no classifier
	// Where the classifier's model lives.
	ModelPath string
	// The classifier's answers below this probability are left to the language model.
	MinConfidence float64
	// Merchants per language model call.
	Batch int
	// Progress is told how many merchants the language model has done (for the UI).
	Progress func(done, total int)
	// Relabel asks the classifier and the language model again about lines they already labeled
	// (after many corrections, say). Otherwise only lines without a category are looked at.
	Relabel bool
}

// Report counts the lines each step categorized.
type Report struct {
	Rules       int `json:"rules"`
	Corrections int `json:"corrections"`
	Model       int `json:"model"`
	LLM         int `json:"llm"`
	Left        int `json:"left"`
	Merchants   int `json:"merchants_asked"`
}

var rules = []struct {
	re       *regexp.Regexp
	category string
	incoming *bool // nil: either direction
}{
	// A fee is a line that starts with it: card payments also mention "comision tranzactie 0,00 RON".
	{regexp.MustCompile(`(?i)^\s*(comision|taxa|commission|fee)\b`), "Fees", ptr(false)},
	{regexp.MustCompile(`(?i)retragere numerar|\bATM\b|cash withdrawal`), "Cash", ptr(false)},
	{regexp.MustCompile(`(?i)\bdobanda\b|\binterest\b`), "Other Income", ptr(true)},
	{regexp.MustCompile(`(?i)\bsalariu\b|\bsalary\b|\bsalar\b`), "Salary", ptr(true)},
	{regexp.MustCompile(`(?i)^top-up by|\btopup\b`), "Transfer", ptr(true)},
}

func ptr(b bool) *bool { return &b }

// Rule returns the category a rule gives a line, or "".
func Rule(u store.Unlabeled) string {
	text := u.Description + " " + u.Type
	for _, r := range rules {
		if r.incoming != nil && *r.incoming != (u.Amount > 0) {
			continue
		}
		if r.re.MatchString(text) {
			return r.category
		}
	}
	return ""
}

// Run categorizes every line that has no category yet (or only a weaker one).
func Run(ctx context.Context, s *store.Store, opt Options) (*Report, error) {
	if opt.MinConfidence == 0 {
		opt.MinConfidence = 0.6
	}
	if opt.Batch == 0 {
		opt.Batch = 8
	}
	rep := &Report{}
	above := store.SourceLLM // only lines with no label at all
	if opt.Relabel {
		above = store.SourceRule
	}
	lines, err := s.LinesToLabel(ctx, above)
	if err != nil {
		return nil, err
	}
	corrections, err := s.Corrections(ctx)
	if err != nil {
		return nil, err
	}
	byMerchant := map[string]string{}
	for _, c := range corrections {
		byMerchant[c.MerchantKey] = c.Category
	}
	var labels []store.Label
	var rest []store.Unlabeled
	for _, u := range lines {
		if c, ok := byMerchant[u.MerchantKey]; ok {
			labels = append(labels, store.Label{Key: u.Key, Category: c, Source: store.SourceUser, Confidence: 1})
			rep.Corrections++
		} else if c := Rule(u); c != "" {
			labels = append(labels, store.Label{Key: u.Key, Category: c, Source: store.SourceRule, Confidence: 1})
			rep.Rules++
		} else {
			rest = append(rest, u)
		}
	}
	if _, err := s.SetLabels(ctx, labels); err != nil {
		return nil, err
	}

	// The classifier, retrained on everything you corrected.
	if opt.ML != nil && len(corrections) > 0 && len(rest) > 0 {
		examples, err := userExamples(ctx, s)
		if err != nil {
			return nil, err
		}
		if ok, err := opt.ML.Train(ctx, opt.ModelPath, examples); err != nil {
			return nil, err
		} else if ok {
			items := make([]ml.Example, len(rest))
			for i, u := range rest {
				items[i] = ml.Example{Merchant: u.Merchant, Description: u.Description, Amount: money.Amount(u.Amount).Float()}
			}
			preds, err := opt.ML.Predict(ctx, opt.ModelPath, items)
			if err != nil {
				return nil, err
			}
			var mlLabels []store.Label
			var still []store.Unlabeled
			for i, u := range rest {
				if i < len(preds) && preds[i].Confidence >= opt.MinConfidence {
					mlLabels = append(mlLabels, store.Label{Key: u.Key, Category: preds[i].Category, Source: store.SourceModel, Confidence: preds[i].Confidence})
				} else {
					still = append(still, u)
				}
			}
			if _, err := s.SetLabels(ctx, mlLabels); err != nil {
				return nil, err
			}
			rep.Model = len(mlLabels)
			rest = still
		}
	}

	// The language model, for each merchant still without a category.
	if opt.LLM != nil && len(rest) > 0 {
		groups := groupByMerchant(rest)
		rep.Merchants = len(groups)
		var llmLabels []store.Label
		for start := 0; start < len(groups); start += opt.Batch {
			batch := groups[start:min(start+opt.Batch, len(groups))]
			cats, err := Ask(ctx, opt.LLM, batch, corrections)
			if err != nil {
				return nil, err
			}
			for i, g := range batch {
				if cats[i] == "" {
					continue
				}
				for _, u := range g.Lines {
					llmLabels = append(llmLabels, store.Label{Key: u.Key, Category: cats[i], Source: store.SourceLLM, Confidence: 0.5})
				}
			}
			if opt.Progress != nil {
				opt.Progress(min(start+opt.Batch, len(groups)), len(groups))
			}
		}
		if _, err := s.SetLabels(ctx, llmLabels); err != nil {
			return nil, err
		}
		rep.LLM = len(llmLabels)
	}
	rep.Left = len(lines) - rep.Rules - rep.Corrections - rep.Model - rep.LLM
	return rep, nil
}

// userExamples are the lines you categorized by hand, for training the classifier.
func userExamples(ctx context.Context, s *store.Store) ([]ml.Example, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT coalesce(b.merchant, ''), coalesce(b.description, ''), b.amount, l.category
		FROM bank_lines b JOIN labels l ON l.key = b.key WHERE l.source = 'user'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ml.Example
	for rows.Next() {
		var e ml.Example
		var amount int64
		if err := rows.Scan(&e.Merchant, &e.Description, &amount, &e.Category); err != nil {
			return nil, err
		}
		e.Amount, e.Weight = money.Amount(amount).Float(), 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// Group is one merchant's uncategorized lines.
type Group struct {
	Key   string
	Lines []store.Unlabeled
}

func groupByMerchant(lines []store.Unlabeled) []Group {
	idx := map[string]int{}
	var out []Group
	for _, u := range lines {
		k := u.MerchantKey + "|" + fmt.Sprint(u.Amount > 0)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, Group{Key: u.MerchantKey})
		}
		out[i].Lines = append(out[i].Lines, u)
	}
	// The merchants you meet most first: if the run stops early, the most lines are done.
	sort.SliceStable(out, func(a, b int) bool { return len(out[a].Lines) > len(out[b].Lines) })
	return out
}

// Ask has the language model categorize a batch of merchants; it returns one category per group
// ("" where it could not say).
func Ask(ctx context.Context, model llm.Model, groups []Group, corrections []store.Correction) ([]string, error) {
	var sys strings.Builder
	sys.WriteString("You sort the bank transactions of a person living in Romania into categories.\n" +
		"Statements are in Romanian: 'Plata la POS' is a card payment in a shop, 'Plata online' an online card payment, " +
		"'Plata OP' a bank transfer sent, 'Incasare' money received, 'Retur' a refund.\n" +
		"Use what you know about Romanian and international merchants. A refund keeps the category of what was bought. " +
		"Money received from a company with 'salariu' is Salary.\n\nCategories:\n")
	for _, name := range category.Assignable() {
		c, _ := category.Lookup(name)
		fmt.Fprintf(&sys, "- %s: %s\n", c.Name, c.Hint)
	}
	if len(corrections) > 0 {
		sys.WriteString("\nThis person has already sorted these merchants (follow their choices for similar ones):\n")
		for i, c := range corrections {
			if i == 30 {
				break
			}
			fmt.Fprintf(&sys, "- %s -> %s\n", c.MerchantKey, c.Category)
		}
	}
	var user strings.Builder
	user.WriteString("Give the category of each merchant below. Name each merchant exactly as listed.\n\n")
	// Each answer names its merchant (constrained to this batch's names), so an answer cannot land on
	// the wrong merchant even if the model reorders or skips them.
	names := make([]string, len(groups))
	for i, g := range groups {
		u := g.Lines[0]
		dir := "payment"
		names[i] = g.Key + " (paid)"
		if u.Amount > 0 {
			dir = "money received"
			names[i] = g.Key + " (received)"
		}
		desc := u.Description
		if len(desc) > 160 {
			desc = desc[:160]
		}
		fmt.Fprintf(&user, "- %s | %s of %s %s (%d times) | %s\n", names[i], dir, money.Amount(abs(u.Amount)).String(), u.Currency, len(g.Lines), desc)
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type":     "array",
				"minItems": len(groups),
				"maxItems": len(groups),
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"merchant": map[string]any{"type": "string", "enum": names},
						"category": map[string]any{"type": "string", "enum": category.Assignable()},
					},
					"required": []string{"merchant", "category"},
				},
			},
		},
		"required": []string{"items"},
	}
	var out struct {
		Items []struct {
			Merchant string `json:"merchant"`
			Category string `json:"category"`
		} `json:"items"`
	}
	err := llm.JSON(ctx, model, llm.Request{
		Messages:  []llm.Message{{Role: "system", Content: sys.String()}, {Role: "user", Content: user.String()}},
		Schema:    schema,
		MaxTokens: 40 * len(groups),
	}, &out)
	if err != nil {
		return nil, err
	}
	cats := make([]string, len(groups))
	for _, it := range out.Items {
		for i, n := range names {
			if n == it.Merchant && cats[i] == "" {
				cats[i] = it.Category
				break
			}
		}
	}
	return cats, nil
}

func abs(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}
