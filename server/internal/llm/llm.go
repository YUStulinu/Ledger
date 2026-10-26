// Package llm talks to a local language model served by llama.cpp's llama-server, and starts that
// server when needed. Nothing leaves the machine.
//
// Every request asks for JSON matching a schema. llama-server turns the schema into a grammar that
// constrains decoding, so the model cannot produce malformed JSON or a category that does not exist:
// the output is always parseable, and only its content can be wrong.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request asks for a JSON answer that matches Schema.
type Request struct {
	Messages []Message
	// Schema is a JSON Schema for the answer.
	Schema    map[string]any
	MaxTokens int
}

// Model is anything that answers requests: the local server, or a fake in tests.
type Model interface {
	Complete(ctx context.Context, req Request) (string, error)
}

// Client calls a llama-server (or any server with the OpenAI chat completions API).
type Client struct {
	URL  string // e.g. http://127.0.0.1:8091
	HTTP *http.Client
}

func NewClient(url string) *Client {
	return &Client{URL: url, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	body := map[string]any{
		"messages":    req.Messages,
		"temperature": 0,
		"seed":        1,
		"max_tokens":  req.MaxTokens,
	}
	if req.MaxTokens == 0 {
		body["max_tokens"] = 1024
	}
	if req.Schema != nil {
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "answer", "strict": true, "schema": req.Schema},
		}
	}
	data, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("the language model is not reachable at %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("language model: %s: %s", resp.Status, bytes.TrimSpace(raw))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("language model: unexpected response: %.200s", raw)
	}
	return out.Choices[0].Message.Content, nil
}

// Healthy says whether the server is up and has its model loaded.
func (c *Client) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// JSON completes a request and decodes the answer into out.
func JSON(ctx context.Context, m Model, req Request, out any) error {
	text, err := m.Complete(ctx, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("language model: the answer is not the requested JSON: %w (%.200s)", err, text)
	}
	return nil
}
