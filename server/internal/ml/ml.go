// Package ml runs the Python side of Ledger (ml/ledger_ml): the classifier that learns from your
// corrections and the balance forecast. Each call is one process, with a JSON request on stdin
// and a JSON answer on stdout.
package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Bridge struct {
	Python string // the interpreter ("python", or a virtualenv's)
	Dir    string // the ml/ directory (it holds the ledger_ml package)
}

// Find locates ml/ next to the server directory (the repository layout) or under root.
func Find(root string) *Bridge {
	py := os.Getenv("LEDGER_PYTHON")
	if py == "" {
		py = "python"
		if _, err := exec.LookPath(py); err != nil {
			py = "python3"
		}
	}
	return &Bridge{Python: py, Dir: filepath.Join(root, "ml")}
}

// Call runs `python -m ledger_ml.<module> <args>` with req as JSON and decodes the answer into out.
func (b *Bridge) Call(ctx context.Context, module string, args []string, req any, out any) error {
	in, err := json.Marshal(req)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, b.Python, append([]string{"-m", "ledger_ml." + module}, args...)...)
	cmd.Dir = b.Dir
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 && len(msg) > 400 {
			msg = msg[i+1:]
		}
		return fmt.Errorf("python ledger_ml.%s: %v: %s", module, err, msg)
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("python ledger_ml.%s: bad answer: %w", module, err)
	}
	return nil
}

// Example is one labeled transaction for the classifier.
type Example struct {
	Merchant    string  `json:"merchant"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Category    string  `json:"category,omitempty"`
	Weight      float64 `json:"weight,omitempty"`
}

type Prediction struct {
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
}

// Train fits the classifier on examples and saves it at model (a .npz file).
func (b *Bridge) Train(ctx context.Context, model string, examples []Example) (bool, error) {
	var out struct {
		Trained bool `json:"trained"`
	}
	err := b.Call(ctx, "classify", []string{"train"}, map[string]any{"model": model, "examples": examples}, &out)
	return out.Trained, err
}

// Predict classifies items with a trained model; nil if no model was trained yet.
func (b *Bridge) Predict(ctx context.Context, model string, items []Example) ([]Prediction, error) {
	var out struct {
		Predictions []Prediction `json:"predictions"`
	}
	err := b.Call(ctx, "classify", []string{"predict"}, map[string]any{"model": model, "items": items}, &out)
	return out.Predictions, err
}
