package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A stand-in for llama-server: it checks the request and answers like the real one.
func fakeServer(t *testing.T, answer string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			var body struct {
				Temperature    float64 `json:"temperature"`
				MaxTokens      int     `json:"max_tokens"`
				ResponseFormat struct {
					Type       string `json:"type"`
					JSONSchema struct {
						Schema map[string]any `json:"schema"`
					} `json:"json_schema"`
				} `json:"response_format"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Temperature != 0 || body.MaxTokens != 64 || body.ResponseFormat.Type != "json_schema" ||
				body.ResponseFormat.JSONSchema.Schema["type"] != "object" {
				t.Errorf("request: %+v", body)
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func request() Request {
	return Request{
		Messages:  []Message{{Role: "user", Content: "category of KAUFLAND?"}},
		Schema:    map[string]any{"type": "object", "properties": map[string]any{"category": map[string]any{"type": "string"}}},
		MaxTokens: 64,
	}
}

func TestClientSendsTheSchemaAndDecodesTheAnswer(t *testing.T) {
	srv := fakeServer(t, `{"category":"Groceries"}`, 200)
	c := NewClient(srv.URL)
	if !c.Healthy(context.Background()) {
		t.Fatal("not healthy")
	}
	var out struct{ Category string }
	if err := JSON(context.Background(), c, request(), &out); err != nil || out.Category != "Groceries" {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestClientErrors(t *testing.T) {
	var out struct{ Category string }
	if err := JSON(context.Background(), NewClient(fakeServer(t, "not json", 200).URL), request(), &out); err == nil ||
		!strings.Contains(err.Error(), "not the requested JSON") {
		t.Errorf("malformed answer: %v", err)
	}
	if _, err := NewClient(fakeServer(t, "", 503).URL).Complete(context.Background(), request()); err == nil ||
		!strings.Contains(err.Error(), "503") {
		t.Errorf("server error: %v", err)
	}
	if NewClient("http://127.0.0.1:1").Healthy(context.Background()) {
		t.Error("nothing listens on port 1")
	}
}
