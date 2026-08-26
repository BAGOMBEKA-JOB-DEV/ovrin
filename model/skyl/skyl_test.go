package skyl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/adaptertest"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/sandbox"
	"github.com/BAGOMBEKA-JOB-DEV/skyl"
	"github.com/BAGOMBEKA-JOB-DEV/skyl/provider/openaicompat"
)

const (
	testAPIKey       = "sk-ovrin-adapter-test-key"
	testRequestModel = "model-that-was-asked-for"

	// testReply carries redundant whitespace deliberately: the contract suite
	// compares byte-for-byte, so a compact fixture could not tell a
	// passthrough from an adapter that re-marshalled the reply.
	testReply = `{ "ovrin_test_property": "found" }`
)

// newTestModel builds the adapter pointed at a test server.
//
// Retries are off. Retry is the [skyl.Client]'s business rather than this
// adapter's (rule §6.2), and leaving the default on would make every error
// assertion pay a backoff.
func newTestModel(baseURL string, opts ...Option) *Model {
	provider := openaicompat.New(
		openaicompat.WithBaseURL(baseURL),
		openaicompat.WithAPIKey(testAPIKey),
		openaicompat.WithName("sandbox"),
	)
	client := skyl.New(provider,
		skyl.WithMaxRetries(0),
		skyl.WithTimeout(30*time.Second),
	)
	return New(client, append([]Option{WithModelID(testRequestModel)}, opts...)...)
}

// ---------------------------------------------------------------------------
// The shared contract
// ---------------------------------------------------------------------------

// The suite in internal/adaptertest is the barrier: an adapter that cannot
// pass it is not finished (rule §3.1).
func TestContract(t *testing.T) {
	const servedModel = "model-that-actually-served-it"

	content, err := json.Marshal(testReply)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	successBody := `{"id":"chatcmpl-1","object":"chat.completion","model":"` + servedModel + `",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":` + string(content) +
		`},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":137,"completion_tokens":42,` +
		`"prompt_tokens_details":{"cached_tokens":64}}}`

	adaptertest.Model(t, adaptertest.ModelSuite{
		Name: "skyl",
		New: func(baseURL string) ovrin.Model {
			return newTestModel(baseURL + "/v1")
		},
		NewWithoutVision: func(baseURL string) ovrin.Model {
			return newTestModel(baseURL+"/v1", WithoutVision())
		},
		APIKey:       testAPIKey,
		RequestModel: testRequestModel,
		WantModel:    servedModel,
		ServedModel: func(raw any) string {
			resp, ok := raw.(*skyl.Response)
			if !ok {
				return ""
			}
			return resp.Model
		},
		SuccessBody: successBody,
		WantJSON:    testReply,
		// 64 of the 137 input tokens came from cache. skyl already reports
		// cached tokens as a subset of the input count, so ovrin's input count
		// is 137 — not 201, and not 73.
		WantUsage: ovrin.Usage{InputTokens: 137, OutputTokens: 42},
		ErrorBody: `{"error":{"message":"something went wrong",` +
			`"type":"invalid_request_error","code":"bad"}}`,
		EchoErrorBody: func(echo string) string {
			quoted, err := json.Marshal("Invalid prompt. Offending content: " + echo)
			if err != nil {
				return `{"error":{"message":"encode failed"}}`
			}
			return `{"error":{"message":` + string(quoted) +
				`,"type":"invalid_request_error"}}`
		},
	})
}

// ---------------------------------------------------------------------------
// Classification, over a real socket
// ---------------------------------------------------------------------------

// Every provider failure has to arrive as one of ovrin's sentinels, because
// nothing downstream may read a message string (rule §2.2).
//
// These run against the offline sandbox rather than a handler returning a
// fixed body, so the transport failures — a truncated body, a connection
// dropped mid-response — are genuinely produced rather than simulated
// (ADR-0022).
func TestClassification(t *testing.T) {
	t.Parallel()

	srv := sandbox.New(sandbox.WithAPIKey(testAPIKey))
	t.Cleanup(srv.Close)

	tests := []struct {
		name  string
		fault sandbox.Fault
		want  error
	}{
		{"401 is an authentication failure", sandbox.FaultUnauthorized, ovrin.ErrAuth},
		{"403 is an authentication failure", sandbox.FaultForbidden, ovrin.ErrAuth},
		{"429 with Retry-After is a rate limit", sandbox.FaultRateLimitRetryAfter, ovrin.ErrRateLimit},
		{"429 without Retry-After is a rate limit", sandbox.FaultRateLimitNoRetryAfter, ovrin.ErrRateLimit},
		{"400 is a rejected request", sandbox.FaultBadRequest, ovrin.ErrBadRequest},
		{"404 is a rejected request", sandbox.FaultNotFound, ovrin.ErrBadRequest},
		{"500 is an unavailable provider", sandbox.FaultServerError, ovrin.ErrUnavailable},
		{"503 is an unavailable provider", sandbox.FaultUnavailable, ovrin.ErrUnavailable},
		{"a refusal is an unusable response", sandbox.FaultRefusal, ovrin.ErrBadResponse},

		// skyl folds an undecodable reply into its server sentinel, so these
		// reach ovrin as an unavailable provider rather than a bad response.
		// It is the honest answer for a body that never decoded far enough to
		// be called a reply, and it is retryable, which a 200 carrying rubbish
		// usually is.
		{"malformed JSON is an unavailable provider", sandbox.FaultMalformedJSON, ovrin.ErrUnavailable},
		{"an HTML error page is an unavailable provider", sandbox.FaultWrongContentType, ovrin.ErrUnavailable},
		{"an empty choices array is an unavailable provider", sandbox.FaultEmptyChoices, ovrin.ErrUnavailable},
		{"a truncated body is an unavailable provider", sandbox.FaultTruncatedBody, ovrin.ErrUnavailable},
		{"a disconnection mid-body is an unavailable provider", sandbox.FaultDisconnectMidBody, ovrin.ErrUnavailable},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The fault travels in the model identifier, which skyl passes
			// through untouched, so one server serves every case.
			m := newTestModel(srv.BaseURL(), WithModelID(sandbox.FaultModel(tc.fault)))

			resp, err := m.Generate(context.Background(), testRequest())
			if err == nil {
				t.Fatalf("Generate() error = nil, want %v", tc.want)
			}
			if resp != nil {
				t.Error("Generate() returned both a response and an error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}

			var e *ovrin.Error
			if !errors.As(err, &e) {
				t.Fatalf("errors.As did not recover *ovrin.Error from %v", err)
			}
			if e.Op != ovrin.OpGenerate {
				t.Errorf("Error.Op = %q, want %q", e.Op, ovrin.OpGenerate)
			}
			if e.Provider != "sandbox" {
				t.Errorf("Error.Provider = %q, want %q", e.Provider, "sandbox")
			}
		})
	}
}

// A provider that quotes the request back in its error message is the one way
// document content reaches a log line without anybody deciding to put it there
// (rule §2.5). skyl does carry the provider's message on its own error, so
// this only passes because the adapter writes its own.
func TestErrorsCarryNoProviderText(t *testing.T) {
	t.Parallel()

	srv := sandbox.New(sandbox.WithAPIKey(testAPIKey),
		sandbox.WithFault(sandbox.FaultEchoPrompt))
	t.Cleanup(srv.Close)

	m := newTestModel(srv.BaseURL())

	_, err := m.Generate(context.Background(), testRequest())
	if err == nil {
		t.Fatal("Generate() error = nil, want a failure")
	}
	if strings.Contains(err.Error(), documentCanary) {
		t.Errorf("document content reached the error message: %v", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Errorf("the credential reached the error message: %v", err)
	}

	var e *ovrin.Error
	if errors.As(err, &e) && strings.Contains(e.Message, documentCanary) {
		t.Errorf("document content reached ovrin.Error.Message: %q", e.Message)
	}

	// The provider's own error still carried it, which is what makes the
	// assertion above meaningful rather than vacuous.
	var provErr *skyl.Error
	if !errors.As(err, &provErr) {
		return
	}
	if !strings.Contains(provErr.Message, documentCanary) {
		t.Skip("the provider error did not quote the prompt; nothing was there to leak")
	}
}

// ---------------------------------------------------------------------------
// Mapping
// ---------------------------------------------------------------------------

// A model that returns something that is not JSON is the core's to classify.
// The adapter hands the bytes back untouched so that the failure is one ovrin
// error rather than a different one per provider (ADR-0007).
func TestInvalidJSONReplyIsHandedBackUnparsed(t *testing.T) {
	t.Parallel()

	srv := sandbox.New(sandbox.WithAPIKey(testAPIKey),
		sandbox.WithFault(sandbox.FaultInvalidJSONReply))
	t.Cleanup(srv.Close)

	resp, err := newTestModel(srv.BaseURL()).Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v; an unparseable reply is not the adapter's "+
			"failure to report", err)
	}
	if len(resp.JSON) == 0 {
		t.Fatal("ModelResponse.JSON is empty; the offending bytes must survive")
	}
	if json.Valid(resp.JSON) {
		t.Errorf("ModelResponse.JSON = %q, want the invalid document the model produced",
			resp.JSON)
	}
}

// Vendors disagree about whether cached tokens are inside the input count.
// skyl normalises to "inside", ovrin has no cache fields, so the number must
// pass straight through — neither summed nor subtracted.
func TestUsageKeepsCachedTokensInsideTheInputCount(t *testing.T) {
	t.Parallel()

	srv := sandbox.New(sandbox.WithAPIKey(testAPIKey),
		sandbox.WithUsage(sandbox.Usage{Input: 1000, Output: 20, Cached: 900}))
	t.Cleanup(srv.Close)

	resp, err := newTestModel(srv.BaseURL()).Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	want := ovrin.Usage{InputTokens: 1000, OutputTokens: 20}
	if resp.Usage != want {
		t.Errorf("Usage = %+v, want %+v; 900 cached tokens are part of the 1000, "+
			"so the total is neither 1900 nor 100", resp.Usage, want)
	}
	if resp.Usage.PageUnits != 0 {
		t.Errorf("Usage.PageUnits = %d, want 0; a model bills tokens", resp.Usage.PageUnits)
	}
}

// The schema has to reach the provider's structured-output mode intact, under
// a name OpenAI will accept.
func TestSchemaReachesTheProvider(t *testing.T) {
	t.Parallel()

	srv := sandbox.New(sandbox.WithAPIKey(testAPIKey))
	t.Cleanup(srv.Close)

	if _, err := newTestModel(srv.BaseURL()).Generate(context.Background(), testRequest()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Model          string `json:"model"`
		System         string `json:"-"`
		ResponseFormat *struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Name   string         `json:"name"`
				Strict bool           `json:"strict"`
				Schema map[string]any `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
		Temperature *float64 `json:"temperature"`
	}
	if err := json.Unmarshal(srv.LastBody(), &sent); err != nil {
		t.Fatalf("the request body is not JSON: %v", err)
	}

	if sent.ResponseFormat == nil || sent.ResponseFormat.JSONSchema == nil {
		t.Fatalf("no response_format reached the wire\nbody: %s", srv.LastBody())
	}
	if got := sent.ResponseFormat.JSONSchema.Name; got != schemaName {
		t.Errorf("json_schema.name = %q, want %q; OpenAI requires one", got, schemaName)
	}
	props, _ := sent.ResponseFormat.JSONSchema.Schema["properties"].(map[string]any)
	if _, ok := props["ovrin_test_property"]; !ok {
		t.Errorf("the schema was rebuilt rather than passed through; "+
			"got properties %v", props)
	}
	if sent.Temperature == nil {
		t.Error("the request's temperature did not reach the provider")
	}
	if sent.Model != testRequestModel {
		t.Errorf("model = %q, want %q", sent.Model, testRequestModel)
	}
}

// Raw is the caller's escape hatch, and what it must carry is the whole skyl
// response — the served model, the stop reason, the cache breakdown ovrin's
// own Usage cannot express, and the provider's untouched bytes.
func TestRawCarriesTheSkylResponse(t *testing.T) {
	t.Parallel()

	srv := sandbox.New(sandbox.WithAPIKey(testAPIKey),
		sandbox.WithUsage(sandbox.Usage{Input: 100, Output: 10, Cached: 40}))
	t.Cleanup(srv.Close)

	resp, err := newTestModel(srv.BaseURL()).Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	raw, ok := resp.Raw.(*skyl.Response)
	if !ok {
		t.Fatalf("ModelResponse.Raw is %T, want *skyl.Response", resp.Raw)
	}
	if raw.Model != sandbox.DefaultModel {
		t.Errorf("Raw.Model = %q, want %q read from the response", raw.Model, sandbox.DefaultModel)
	}
	if raw.Model == testRequestModel {
		t.Error("Raw.Model echoes the requested model")
	}
	if raw.Usage.CacheReadTokens != 40 {
		t.Errorf("Raw.Usage.CacheReadTokens = %d, want 40; the cache breakdown ovrin "+
			"drops must still be reachable here", raw.Usage.CacheReadTokens)
	}
	if len(raw.Raw) == 0 {
		t.Error("Raw.Raw is empty; the provider's own bytes must survive")
	}
}

// A request the adapter cannot represent fails locally, before a round trip.
// None of these may reach the provider: a call that cannot succeed should not
// cost one.
func TestRequestsRefusedBeforeTheWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     []Option
		request  ovrin.ModelRequest
		want     error
		wantPage int
	}{
		{
			name:    "no model identifier",
			opts:    []Option{WithModelID("")},
			request: testRequest(),
			want:    ovrin.ErrBadRequest,
		},
		{
			name:    "no content at all",
			request: ovrin.ModelRequest{Instruction: "extract", Schema: []byte(testSchema)},
			want:    ovrin.ErrBadRequest,
		},
		{
			name: "content that carries nothing",
			request: ovrin.ModelRequest{
				Instruction: "extract",
				Content:     []ovrin.Content{{Reading: ovrin.ReadingText, Page: 1}},
				Schema:      []byte(testSchema),
			},
			want: ovrin.ErrBadRequest,
		},
		{
			name:    "a schema that is not json",
			request: requestWithSchema([]byte(`{"type":`)),
			want:    ovrin.ErrSchema,
		},
		{
			name:     "a page image with no media type",
			request:  requestWithImage(""),
			want:     ovrin.ErrUnsupported,
			wantPage: 2,
		},
		{
			name:     "a page image for a model without vision",
			opts:     []Option{WithoutVision()},
			request:  requestWithImage("image/png"),
			want:     ovrin.ErrUnsupported,
			wantPage: 2,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := sandbox.New(sandbox.WithAPIKey(testAPIKey))
			t.Cleanup(srv.Close)

			m := newTestModel(srv.BaseURL(), tc.opts...)
			resp, err := m.Generate(context.Background(), tc.request)

			if err == nil {
				t.Fatalf("Generate() error = nil, want %v", tc.want)
			}
			if resp != nil {
				t.Error("Generate() returned both a response and an error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}
			if got := srv.Requests(); got != 0 {
				t.Errorf("the provider was called %d times; a request that cannot be "+
					"represented must fail before the round trip", got)
			}

			var e *ovrin.Error
			if !errors.As(err, &e) {
				t.Fatalf("errors.As did not recover *ovrin.Error from %v", err)
			}
			if e.Page != tc.wantPage {
				t.Errorf("Error.Page = %d, want %d", e.Page, tc.wantPage)
			}
			if strings.Contains(err.Error(), documentCanary) {
				t.Errorf("document content reached the error: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Construction
// ---------------------------------------------------------------------------

func TestNewPanicsOnNilClient(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("New(nil) did not panic; a nil client would surface as a nil " +
				"dereference on the first extraction instead")
		}
	}()
	_ = New(nil)
}

// The convenience constructors exist so the quickstart is one line. Each must
// actually wire the provider it names.
func TestConvenienceConstructors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		build        func() *Model
		wantProvider string
		wantModelID  string
	}{
		{
			name:         "OpenAI names the openai provider",
			build:        func() *Model { return OpenAI("key", "gpt-5.2") },
			wantProvider: "openai",
			wantModelID:  "gpt-5.2",
		},
		{
			name:         "Gemini names the gemini provider",
			build:        func() *Model { return Gemini("key", "gemini-3.6-pro") },
			wantProvider: "gemini",
			wantModelID:  "gemini-3.6-pro",
		},
		{
			name:         "Ollama names itself rather than the compatible adapter",
			build:        func() *Model { return Ollama("llama3.3") },
			wantProvider: "ollama",
			wantModelID:  "llama3.3",
		},
		{
			name:         "Compatible reaches any OpenAI-shaped host",
			build:        func() *Model { return Compatible("https://api.groq.com/openai/v1", "key", "kimi") },
			wantProvider: "openai-compatible",
			wantModelID:  "kimi",
		},
		{
			name:         "Compatible accepts no credential, for local runtimes",
			build:        func() *Model { return Compatible("http://localhost:8000/v1", "", "qwen") },
			wantProvider: "openai-compatible",
			wantModelID:  "qwen",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := tc.build()
			if m.provider != tc.wantProvider {
				t.Errorf("provider = %q, want %q", m.provider, tc.wantProvider)
			}
			if m.modelID != tc.wantModelID {
				t.Errorf("model id = %q, want %q", m.modelID, tc.wantModelID)
			}
			if !m.vision {
				t.Error("vision is off by default; nothing in a model identifier " +
					"reveals capability, and refusing work a model can do is the " +
					"worse guess")
			}
		})
	}
}

func TestCompatiblePanicsWithoutABaseURL(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Compatible(\"\", ...) did not panic")
		}
	}()
	_ = Compatible("", "key", "model")
}

func TestOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []Option
		verify func(t *testing.T, m *Model)
	}{
		{
			name: "WithMaxTokens sets the cap",
			opts: []Option{WithMaxTokens(4096)},
			verify: func(t *testing.T, m *Model) {
				t.Helper()
				if m.maxTokens != 4096 {
					t.Errorf("maxTokens = %d, want 4096", m.maxTokens)
				}
			},
		},
		{
			name: "WithMaxTokens ignores a negative cap",
			opts: []Option{WithMaxTokens(4096), WithMaxTokens(-1)},
			verify: func(t *testing.T, m *Model) {
				t.Helper()
				if m.maxTokens != 4096 {
					t.Errorf("maxTokens = %d, want the earlier 4096 to survive", m.maxTokens)
				}
			},
		},
		{
			name: "WithoutVision turns images off",
			opts: []Option{WithoutVision()},
			verify: func(t *testing.T, m *Model) {
				t.Helper()
				if m.vision {
					t.Error("vision is still on after WithoutVision()")
				}
			},
		},
		{
			name: "options apply in order",
			opts: []Option{WithModelID("first"), WithModelID("second")},
			verify: func(t *testing.T, m *Model) {
				t.Helper()
				if m.modelID != "second" {
					t.Errorf("model id = %q, want %q", m.modelID, "second")
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := New(skyl.New(openaicompat.New(
				openaicompat.WithBaseURL("http://127.0.0.1:1/v1"))), tc.opts...)
			tc.verify(t, m)
		})
	}
}

// The maximum-tokens cap has to actually reach the provider, or the Anthropic
// wiring the package documentation recommends cannot work.
func TestMaxTokensReachesTheProvider(t *testing.T) {
	t.Parallel()

	srv := sandbox.New(sandbox.WithAPIKey(testAPIKey))
	t.Cleanup(srv.Close)

	m := newTestModel(srv.BaseURL(), WithMaxTokens(1234))
	if _, err := m.Generate(context.Background(), testRequest()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !strings.Contains(string(srv.LastBody()), "1234") {
		t.Errorf("the token cap never reached the wire\nbody: %s", srv.LastBody())
	}
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const (
	documentCanary = "OVRIN_MODEL_SKYL_DOCUMENT_CANARY"

	testSchema = `{"type":"object",` +
		`"properties":{"ovrin_test_property":{"type":"string"}},` +
		`"required":["ovrin_test_property"],"additionalProperties":false}`
)

func testRequest() ovrin.ModelRequest {
	temperature := 0.0
	return ovrin.ModelRequest{
		Instruction: "Extract the fields described by the schema.",
		Content: []ovrin.Content{{
			Reading: ovrin.ReadingText,
			Page:    1,
			Text:    documentCanary,
		}},
		Schema:      []byte(testSchema),
		Temperature: &temperature,
	}
}

func requestWithSchema(schema []byte) ovrin.ModelRequest {
	req := testRequest()
	req.Schema = schema
	return req
}

// requestWithImage adds a page image on page 2, so an error naming the page
// has a number that is not the default.
func requestWithImage(mediaType string) ovrin.ModelRequest {
	req := testRequest()
	req.Content = append(req.Content, ovrin.Content{
		Reading:   ovrin.ReadingVision,
		Page:      2,
		Image:     []byte("\x89PNG\r\n\x1a\nOVRIN_IMAGE_BYTES"),
		MediaType: mediaType,
	})
	return req
}
