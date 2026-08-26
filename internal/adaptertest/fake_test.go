package adaptertest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// fakeModel is a compliant [ovrin.Model] built from nothing but the standard
// library, speaking an OpenAI-shaped wire protocol.
//
// It exists so this package's own tests can run the contract suite against an
// adapter that obeys every rule the suite enforces. A suite that has never
// been run green against a correct adapter is a suite whose failures nobody
// can interpret.
//
// It is also, deliberately, the smallest complete worked example of the six
// rules in docs/providers.md: the instruction goes to the system role and the
// content to the user role, images are encoded once, the reply is handed back
// unparsed, statuses are classified onto ovrin's sentinels, and no provider
// text ever reaches an ovrin error message.
type fakeModel struct {
	url    string
	apiKey string
	model  string

	// vision reports whether the configured model can read page images. False
	// means images are refused rather than dropped (docs/rules.md §6.1).
	vision bool

	hc *http.Client
}

const fakeProviderName = "fake"

// newFakeModel returns an adapter pointed at baseURL.
func newFakeModel(baseURL, apiKey, model string, vision bool) *fakeModel {
	return &fakeModel{
		url:    baseURL,
		apiKey: apiKey,
		model:  model,
		vision: vision,
		// No timeout: bounding the call is the caller's context's job, and a
		// client timeout here would make the cancellation assertion pass for
		// the wrong reason.
		hc: &http.Client{},
	}
}

// wireResponse is as much of the vendor's reply as the adapter reads.
type wireResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (m *fakeModel) Generate(ctx context.Context, req ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	payload, err := m.payload(req)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, m.fail(ovrin.ErrBadRequest, "the request could not be encoded")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, m.fail(ovrin.ErrBadRequest, "the request could not be built")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.hc.Do(httpReq)
	if err != nil {
		return nil, m.transportFail(ctx)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on read path

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, m.transportFail(ctx)
	}
	if resp.StatusCode >= 300 {
		// Deliberately does not read the provider's message. It may quote the
		// request back, and an ovrin error message never carries document
		// content (docs/rules.md §2.5).
		return nil, m.fail(classifyStatus(resp.StatusCode),
			fmt.Sprintf("the provider returned http %d", resp.StatusCode))
	}

	var wire wireResponse
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, m.fail(ovrin.ErrUnavailable, "the provider's reply was not json")
	}
	if len(wire.Choices) == 0 {
		return nil, m.fail(ovrin.ErrBadResponse, "the provider's reply carried no choices")
	}
	if wire.Choices[0].FinishReason == "content_filter" {
		return nil, m.fail(ovrin.ErrBadResponse, "the model declined the request")
	}

	return &ovrin.ModelResponse{
		// Handed back exactly as it arrived. A model returning invalid JSON is
		// the core's to classify, so that it is one error and not one per
		// adapter (ADR-0007).
		JSON: []byte(wire.Choices[0].Message.Content),
		Usage: ovrin.Usage{
			InputTokens:  wire.Usage.PromptTokens,
			OutputTokens: wire.Usage.CompletionTokens,
		},
		Raw: &wire,
	}, nil
}

// payload maps the ovrin request onto the vendor's shape.
func (m *fakeModel) payload(req ovrin.ModelRequest) (map[string]any, error) {
	parts := make([]any, 0, len(req.Content))
	for _, c := range req.Content {
		if c.Reading == ovrin.ReadingVision && len(c.Image) > 0 {
			if !m.vision {
				return nil, m.fail(ovrin.ErrUnsupported,
					"model "+m.model+" cannot read page images")
			}
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					// c.Image is raw. Encoding it here, once, is the whole
					// contract: it arrives raw and leaves base64.
					"url": "data:" + c.MediaType + ";base64," +
						base64.StdEncoding.EncodeToString(c.Image),
				},
			})
		}
		if c.Text != "" {
			parts = append(parts, map[string]any{"type": "text", "text": c.Text})
		}
	}
	if len(parts) == 0 {
		return nil, m.fail(ovrin.ErrBadRequest, "the request carried no content")
	}

	payload := map[string]any{
		"model": m.model,
		// Two messages, never one. The boundary between them is what stops an
		// instruction inside a document being read as a directive (ADR-0017).
		"messages": []any{
			map[string]any{"role": "system", "content": req.Instruction},
			map[string]any{"role": "user", "content": parts},
		},
	}
	if len(req.Schema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(req.Schema, &schema); err != nil {
			return nil, m.fail(ovrin.ErrSchema, "the schema is not valid json")
		}
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "ovrin_extraction",
				"strict": true,
				"schema": schema,
			},
		}
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	return payload, nil
}

// fail builds a classified ovrin error with an adapter-authored message.
func (m *fakeModel) fail(kind error, message string) error {
	return &ovrin.Error{
		Op:       ovrin.OpGenerate,
		Provider: fakeProviderName,
		Kind:     kind,
		Message:  message,
	}
}

// transportFail reports a call that never produced a usable response.
//
// When the context ended it wraps the context's own error alongside the ovrin
// error, so one value answers both "what kind of failure was this?" and "was
// it ultimately a cancelled context?" (ADR-0019). Only the context error is
// attached: it is a fixed string, whereas a provider's error may quote the
// document.
func (m *fakeModel) transportFail(ctx context.Context) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", &ovrin.Error{
			Op:       ovrin.OpGenerate,
			Provider: fakeProviderName,
			Message:  "the context ended before the provider replied",
		}, ctxErr)
	}
	return m.fail(ovrin.ErrUnavailable, "the provider could not be reached")
}

// classifyStatus maps an HTTP status onto an ovrin sentinel.
func classifyStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized,
		status == http.StatusForbidden,
		status == http.StatusProxyAuthRequired:
		return ovrin.ErrAuth
	case status == http.StatusTooManyRequests:
		return ovrin.ErrRateLimit
	case status >= 500:
		return ovrin.ErrUnavailable
	case status >= 400:
		return ovrin.ErrBadRequest
	default:
		return ovrin.ErrUnavailable
	}
}

var _ ovrin.Model = (*fakeModel)(nil)
