package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YUStulinu/Ledger/server/internal/app"
	"github.com/YUStulinu/Ledger/server/internal/llm"
	"github.com/YUStulinu/Ledger/server/internal/testenv"
)

// sqlModel answers every question with the same query.
type sqlModel struct{ sql string }

func (m sqlModel) Complete(ctx context.Context, req llm.Request) (string, error) {
	b, _ := json.Marshal(map[string]string{"sql": m.sql})
	return string(b), nil
}

func newServer(t *testing.T) (*Server, *app.App) {
	t.Helper()
	a, err := app.Open(app.Config{Root: testenv.Root(), DB: filepath.Join(t.TempDir(), "ledger.duckdb"), NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return New(a), a
}

func do(t *testing.T, s *Server, req *http.Request, into any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if into != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("%s %s: %v: %s", req.Method, req.URL, err, rec.Body.String())
		}
	}
	return rec.Code
}

func upload(t *testing.T, names ...string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(testenv.Root(), "testdata", "synthetic", n))
		if err != nil {
			t.Fatal(err)
		}
		f, _ := w.CreateFormFile("files", n)
		f.Write(data)
	}
	w.Close()
	req := httptest.NewRequest("POST", "/api/import", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// waitJob waits for the background categorization to finish.
func waitJob(t *testing.T, s *Server) Job {
	t.Helper()
	for range 600 {
		var j Job
		do(t, s, httptest.NewRequest("GET", "/api/job", nil), &j)
		if !j.Running {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the categorization did not finish")
	return Job{}
}

func TestImportListCorrectAndAsk(t *testing.T) {
	s, a := newServer(t)

	var imp struct {
		Imported []struct {
			File       string `json:"file"`
			New        int    `json:"new"`
			Duplicates int    `json:"duplicates"`
		} `json:"imported"`
	}
	if code := do(t, s, upload(t, "bt_2025-04_2026-09.csv", "bt_2026-09.pdf"), &imp); code != 200 {
		t.Fatalf("import: %d", code)
	}
	newLines := 0
	for _, r := range imp.Imported {
		newLines += r.New
	}
	if newLines == 0 {
		t.Fatalf("nothing imported: %+v", imp)
	}
	if j := waitJob(t, s); j.Error != "" {
		t.Fatalf("categorization: %s", j.Error)
	}

	// The same files again: nothing new.
	do(t, s, upload(t, "bt_2025-04_2026-09.csv"), &imp)
	waitJob(t, s)
	if len(imp.Imported) != 1 || imp.Imported[0].New != 0 {
		t.Errorf("re-import added lines: %+v", imp.Imported)
	}

	var st struct {
		Lines int  `json:"lines"`
		LLM   bool `json:"llm"`
	}
	do(t, s, httptest.NewRequest("GET", "/api/status", nil), &st)
	if st.Lines != newLines || st.LLM {
		t.Errorf("status: %+v, want %d lines and no LLM", st, newLines)
	}

	var list struct {
		Total int `json:"total"`
		Items []struct {
			Key      string `json:"key"`
			Category string `json:"category"`
			Merchant string `json:"merchant"`
			Source   string `json:"source"`
		} `json:"items"`
	}
	do(t, s, httptest.NewRequest("GET", "/api/transactions?q=kaufland&limit=500", nil), &list)
	if list.Total == 0 || len(list.Items) != list.Total {
		t.Fatalf("search: %d of %d", len(list.Items), list.Total)
	}
	for _, it := range list.Items {
		if !strings.Contains(strings.ToUpper(it.Merchant), "KAUFLAND") {
			t.Errorf("search for kaufland found %q", it.Merchant)
		}
	}

	// A correction applies to every line of the merchant.
	body, _ := json.Marshal(map[string]string{"key": list.Items[0].Key, "category": "Groceries"})
	var upd struct{ Updated int }
	if code := do(t, s, httptest.NewRequest("POST", "/api/transactions/category", bytes.NewReader(body)), &upd); code != 200 || upd.Updated != list.Total {
		t.Errorf("correct: %d, updated %d of %d", code, upd.Updated, list.Total)
	}
	do(t, s, httptest.NewRequest("GET", "/api/transactions?q=kaufland&limit=500", nil), &list)
	for _, it := range list.Items {
		if it.Category != "Groceries" || it.Source != "user" {
			t.Errorf("after the correction: %+v", it)
		}
	}
	body, _ = json.Marshal(map[string]string{"key": list.Items[0].Key, "category": "Nonsense"})
	if code := do(t, s, httptest.NewRequest("POST", "/api/transactions/category", bytes.NewReader(body)), nil); code != 400 {
		t.Errorf("an unknown category: %d, want 400", code)
	}

	for _, path := range []string{"/api/overview", "/api/recurring", "/api/alerts", "/api/categories"} {
		var v any
		if code := do(t, s, httptest.NewRequest("GET", path, nil), &v); code != 200 {
			t.Errorf("%s: %d", path, code)
		}
	}

	// Questions: the answer, and the refusal of a query that writes.
	a.UseModel(sqlModel{"SELECT round(sum(amount_ron), 2) AS total FROM expenses WHERE category = 'Groceries'"})
	var ans struct {
		Rows [][]any `json:"rows"`
		SQL  string  `json:"sql"`
	}
	if code := do(t, s, httptest.NewRequest("POST", "/api/ask", strings.NewReader(`{"question":"groceries?"}`)), &ans); code != 200 || len(ans.Rows) != 1 {
		t.Errorf("ask: %d %+v", code, ans)
	}
	a.UseModel(sqlModel{"DELETE FROM bank_lines"})
	var refused struct {
		Error  string `json:"error"`
		Answer struct {
			Rejected []struct{ Error string } `json:"rejected"`
		} `json:"answer"`
	}
	if code := do(t, s, httptest.NewRequest("POST", "/api/ask", strings.NewReader(`{"question":"delete everything"}`)), &refused); code != 422 ||
		len(refused.Answer.Rejected) == 0 {
		t.Errorf("a writing query: %d %+v", code, refused)
	}
	do(t, s, httptest.NewRequest("GET", "/api/status", nil), &st)
	if st.Lines != newLines {
		t.Errorf("lines after the refused query: %d, want %d", st.Lines, newLines)
	}
}

func TestCrossOriginWritesAreRefused(t *testing.T) {
	s, _ := newServer(t)
	req := httptest.NewRequest("POST", "/api/categorize", nil)
	req.Header.Set("Origin", "http://evil.example")
	if code := do(t, s, req, nil); code != http.StatusForbidden {
		t.Errorf("cross-origin POST: %d, want 403", code)
	}
	req = httptest.NewRequest("POST", "/api/ask", strings.NewReader(`{"question":"x"}`))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	if code := do(t, s, req, nil); code == http.StatusForbidden {
		t.Errorf("same-origin POST refused")
	}
}
