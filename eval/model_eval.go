//go:build eval

package eval

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// chatModel is a minimal OpenAI-compatible [ovrin.Model] for the harness.
//
// # Why this is not model/skyl
//
// The harness lives in the root module, and the root module has zero external
// dependencies by rule §4.1. model/skyl is a separate module with its own
// requirements, so importing it here would put a provider SDK into the go.sum
// of every ovrin user in order to run a programme none of them run. Writing
// the eighty lines instead keeps that promise intact, and the whole file is
// behind the eval build tag, so it is not in anybody's binary and not in the
// ordinary test suite.
//
// It is deliberately the smallest thing that works. It is not an adapter
// anybody should copy: there is no retry, no fallback, no streaming and no
// contract-suite conformance, because those belong to a real adapter module
// (docs/providers.md) and to the core (§6.2), not to a measurement tool.
type chatModel struct {
	client   *http.Client
	baseURL  string
	apiKey   string
	model    string
	endpoint string
}

// modelFromEnv builds the harness's model from the environment, and reports
// the name to record in the report.
//
// The credentials are read here rather than passed in because the harness is a
// test and a test takes no arguments. A missing key is an error the caller
// turns into a skip: running the eval suite without credentials should say so
// once and stop, not fail twenty-five times.
func modelFromEnv() (ovrin.Model, string, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, "", errors.New("OPENAI_API_KEY is not set")
	}
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	name := os.Getenv("OVRIN_EVAL_MODEL")
	if name == "" {
		name = "gpt-5.2"
	}
	return &chatModel{
		client:   &http.Client{Timeout: 4 * time.Minute},
		baseURL:  strings.TrimSuffix(base, "/"),
		apiKey:   key,
		model:    name,
		endpoint: "/chat/completions",
	}, name, nil
}

// Generate maps one [ovrin.ModelRequest] onto a chat-completions call.
//
// Instruction becomes the system message and Content becomes the user
// message, and they are never concatenated. That separation is a security
// boundary, not a style preference (rules §7.2, ADR-0017): document text is
// data, and an adapter that joins the two removes the only structural defence
// against an instruction printed inside a document.
func (m *chatModel) Generate(ctx context.Context, req ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	parts := make([]map[string]any, 0, len(req.Content))
	for _, c := range req.Content {
		if len(c.Image) > 0 {
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": "data:" + c.MediaType + ";base64," +
						base64.StdEncoding.EncodeToString(c.Image),
				},
			})
			continue
		}
		parts = append(parts, map[string]any{"type": "text", "text": c.Text})
	}

	var schema any
	if len(req.Schema) > 0 {
		if err := json.Unmarshal(req.Schema, &schema); err != nil {
			return nil, fmt.Errorf("eval: the schema ovrin emitted is not JSON: %w", err)
		}
	}

	body := map[string]any{
		"model": m.model,
		"messages": []any{
			map[string]any{"role": "system", "content": req.Instruction},
			map[string]any{"role": "user", "content": parts},
		},
	}
	if schema != nil {
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "extraction",
				"strict": true,
				"schema": schema,
			},
		}
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+m.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // nothing to do on a failed close of a response we have read

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("eval: provider returned status %d and a body that is not JSON", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		// The provider's own message is reported, not the request. A request
		// body carries document content, and rules §2.5 keeps that out of
		// every error, log and trace.
		return nil, fmt.Errorf("eval: provider returned status %d: %s (%s)",
			resp.StatusCode, out.Error.Message, out.Error.Type)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("eval: provider returned no choices")
	}

	return &ovrin.ModelResponse{
		JSON: []byte(out.Choices[0].Message.Content),
		Usage: ovrin.Usage{
			InputTokens:  out.Usage.PromptTokens,
			OutputTokens: out.Usage.CompletionTokens,
		},
	}, nil
}
