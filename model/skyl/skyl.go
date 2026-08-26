// Package skyl implements ovrin's Model seam on top of github.com/BAGOMBEKA-JOB-DEV/skyl,
// which is one Go interface over OpenAI, Gemini, and the long tail of hosts
// that speak OpenAI's wire format.
//
//	import (
//	    "github.com/BAGOMBEKA-JOB-DEV/ovrin"
//	    ovrinskyl "github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl"
//	)
//
//	c := ovrin.New(ovrin.WithModel(ovrinskyl.OpenAI(key, "gpt-5.2")))
//
// It is a separate Go module. Ovrin's core depends on nothing, so a user who
// runs a local model, or who already has an LLM client, pays nothing for this
// one existing ([ADR-0008]).
//
// # There is no Anthropic constructor, on purpose
//
// [OpenAI], [Gemini], [Ollama] and [Compatible] cover skyl's root module, whose
// adapters have no dependencies of their own. Anthropic is different:
// skyl/provider/anthropic is its own Go module because it carries the Anthropic
// SDK. A constructor here would put an import of it in this package, and an
// import in this package is a `require` in this module's go.mod — so every
// ovrin user pulling in this adapter for Ollama would also download and build
// Anthropic's SDK and its transitive dependencies. That is the exact tax the
// quarantine in [ADR-0008] exists to prevent, and the convenience of one
// constructor does not buy it back.
//
// Anthropic is still fully supported. Wire it explicitly, and the SDK lands in
// your go.mod, where you can see it:
//
//	import (
//	    "github.com/BAGOMBEKA-JOB-DEV/skyl"
//	    "github.com/BAGOMBEKA-JOB-DEV/skyl/provider/anthropic"
//	)
//
//	m := ovrinskyl.New(
//	    skyl.New(anthropic.New(key)),
//	    ovrinskyl.WithModelID("claude-opus-5"),
//	    // Anthropic requires an explicit output cap; skyl passes zero through.
//	    ovrinskyl.WithMaxTokens(8192),
//	)
//
// The same form reaches any skyl Provider, including one written outside both
// repositories.
//
// # What this adapter ignores
//
// Rule §6.5 asks every adapter to document that, not only what it supports.
//
//   - Streaming, tool calling and multi-turn conversation. Ovrin makes one
//     call and wants one JSON document, so skyl's [skyl.Client.Stream],
//     [skyl.Tool] and message history are never used.
//   - Prompt caching. skyl reports cache reads and writes as a breakdown of
//     the input count; [ovrin.Usage] has no cache fields, so the breakdown is
//     reachable only through [ovrin.ModelResponse.Raw].
//   - Thinking, TopP, Stop and ProviderOptions. Ovrin's [ovrin.ModelRequest]
//     has no equivalent to set them from, and an adapter does not decide
//     (rule §6.2).
//   - Vision capability is assumed. Nothing in a model identifier reveals it,
//     so images are sent unless [WithoutVision] says otherwise, and a model
//     that cannot read them fails with the provider's own error.
//
// # Retry
//
// Retry, backoff and per-attempt timeouts belong to the [skyl.Client] you
// construct, not to this adapter. That is why [New] takes a client rather than
// a [skyl.Provider]: rule §6.2 forbids an adapter from deciding retry policy,
// and skyl has already written and tested one.
//
// A Model is safe for concurrent use by multiple goroutines.
//
// [ADR-0008]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0008-skyl-is-an-adapter.md
package skyl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/skyl"
	"github.com/BAGOMBEKA-JOB-DEV/skyl/provider/gemini"
	"github.com/BAGOMBEKA-JOB-DEV/skyl/provider/openai"
	"github.com/BAGOMBEKA-JOB-DEV/skyl/provider/openaicompat"
)

// OllamaBaseURL is where Ollama serves its OpenAI-compatible API.
//
// It is exported because a remote Ollama is ordinary — the port is the same,
// the host is not — and [Compatible] is then one line away.
const OllamaBaseURL = "http://localhost:11434/v1"

// schemaName identifies the response schema to the provider.
//
// OpenAI requires a name and rejects a request without one; Gemini and the
// compatible hosts ignore it. Ovrin makes exactly one kind of call, so one
// constant is enough, and it satisfies OpenAI's ^[a-zA-Z0-9_-]+$.
const schemaName = "ovrin_extraction"

// Model adapts a [skyl.Client] to [ovrin.Model].
//
// It is safe for concurrent use by multiple goroutines: it holds no mutable
// state, and a [skyl.Client] is itself concurrency-safe.
type Model struct {
	client   *skyl.Client
	provider string

	modelID   string
	maxTokens int
	vision    bool
}

// Option configures a [Model]. Options are applied in order.
type Option func(*Model)

// WithModelID sets the provider's model identifier, which is passed through
// untouched.
//
// It is required: skyl never validates a model against a list, so a model
// released after this build works immediately, and a [Model] without one
// cannot make a call at all.
func WithModelID(id string) Option {
	return func(m *Model) { m.modelID = id }
}

// WithMaxTokens caps the reply length.
//
// Zero, the default, means the provider's default. It is an option rather than
// a fixed number because [ovrin.ModelRequest] has no field to carry one and an
// adapter does not invent policy (rule §6.2) — but Anthropic rejects a request
// without an explicit cap, so the explicit-wiring form in the package
// documentation needs this to work at all.
//
// Negative values are ignored: skyl rejects them, and failing here would turn
// a typo into an error a long way from its cause.
func WithMaxTokens(n int) Option {
	return func(m *Model) {
		if n >= 0 {
			m.maxTokens = n
		}
	}
}

// WithoutVision declares that the configured model cannot read page images.
//
// Page images are then refused with [ovrin.ErrUnsupported] naming the
// limitation, rather than sent to a model that will ignore them. Rule §6.1
// forbids the alternative: an adapter that dropped the images and answered
// from the text alone would return a much worse extraction that looks exactly
// like a good one.
//
// It is opt-in because nothing in a model identifier reveals whether the model
// has vision, and guessing wrong in the other direction would refuse work a
// model can do.
func WithoutVision() Option {
	return func(m *Model) { m.vision = false }
}

// New returns a Model dispatching through c.
//
// Taking a [skyl.Client] rather than a [skyl.Provider] is deliberate: retry,
// backoff, per-attempt timeouts and hooks live in the client, already written
// and tested, and rule §6.2 says an adapter must not reimplement them.
//
//	m := skyladapter.New(
//	    skyl.New(openai.New(key), skyl.WithMaxRetries(5)),
//	    skyladapter.WithModelID("gpt-5.2"),
//	)
//
// It panics if c is nil, matching [skyl.New]: a nil client is a programmer
// error that would otherwise surface as a confusing nil dereference on the
// first extraction, long after the mistake.
func New(c *skyl.Client, opts ...Option) *Model {
	if c == nil {
		panic("ovrin/model/skyl: New called with a nil *skyl.Client")
	}
	m := &Model{client: c, provider: "skyl", vision: true}
	if p := c.Provider(); p != nil {
		// Cached: a provider's name is fixed for the life of the client, and
		// this is read on every error path.
		m.provider = p.Name()
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// OpenAI returns a Model backed by OpenAI.
//
// It is the one-line form of [New] over skyl's OpenAI provider, which lives in
// skyl's root module and therefore costs no dependency.
func OpenAI(apiKey, model string) *Model {
	return New(skyl.New(openai.New(apiKey)), WithModelID(model))
}

// Gemini returns a Model backed by Google's Gemini API.
//
// Gemini accepts an OpenAPI 3.0 subset rather than full JSON Schema. Ovrin
// emits a fully expanded schema with no $ref, which is the narrowest dialect
// the major providers agree on, so this should hold — and where it does not,
// the failure arrives as [ovrin.ErrBadRequest] rather than as a silently
// relaxed constraint.
func Gemini(apiKey, model string) *Model {
	return New(skyl.New(gemini.New(apiKey)), WithModelID(model))
}

// Ollama returns a Model backed by a local Ollama, through its
// OpenAI-compatible API at [OllamaBaseURL].
//
// No credential: Ollama wants none. For a remote Ollama, or any other local
// runtime, use [Compatible] with its base URL.
//
// Many Ollama models have no vision. Add [WithoutVision] through [New] when
// yours does not, or page images will be sent to a model that ignores them.
func Ollama(model string) *Model {
	return New(skyl.New(openaicompat.New(
		openaicompat.WithBaseURL(OllamaBaseURL),
		openaicompat.WithName("ollama"),
	)), WithModelID(model))
}

// Compatible returns a Model backed by any host speaking OpenAI's
// chat-completions format — Groq, Together, vLLM, LM Studio, an internal
// gateway.
//
// baseURL is the API root with no trailing slash, such as
// "https://api.groq.com/openai/v1". An empty apiKey is sent as no credential
// at all, which is what local runtimes expect.
//
// It panics on an empty baseURL, because a provider that cannot reach a host
// is a programmer error rather than a runtime condition.
func Compatible(baseURL, apiKey, model string) *Model {
	opts := []openaicompat.Option{openaicompat.WithBaseURL(baseURL)}
	if apiKey != "" {
		opts = append(opts, openaicompat.WithAPIKey(apiKey))
	}
	return New(skyl.New(openaicompat.New(opts...)), WithModelID(model))
}

// Generate implements [ovrin.Model].
//
// The reply is returned as raw bytes. It is deliberately not unmarshalled: a
// model returning invalid JSON must produce one ovrin error with the offending
// bytes attached, and it can only be one error if every adapter hands the
// bytes back untouched ([ADR-0007]). A model that returns nothing at all is
// the same case, and reaches the core as zero bytes rather than as an
// adapter-invented failure.
//
// [ADR-0007]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0007-model-seam.md
func (m *Model) Generate(ctx context.Context, req ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	call, err := m.request(req)
	if err != nil {
		return nil, err
	}

	resp, err := m.client.Complete(ctx, call)
	if err != nil {
		return nil, m.classify(ctx, err)
	}

	return &ovrin.ModelResponse{
		JSON:  []byte(resp.Text()),
		Usage: mapUsage(resp.Usage),
		// The whole *skyl.Response, so a caller can reach the served model, the
		// stop reason, the cache breakdown, and the provider's own bytes.
		Raw: resp,
	}, nil
}

// request maps one ovrin request onto skyl's.
func (m *Model) request(req ovrin.ModelRequest) (*skyl.Request, error) {
	if m.modelID == "" {
		return nil, m.fail(ovrin.ErrBadRequest, 0,
			"no model identifier is configured; pass WithModelID")
	}

	parts, err := m.parts(req.Content)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, m.fail(ovrin.ErrBadRequest, 0, "the request carried no content to send")
	}

	call := &skyl.Request{
		Model: m.modelID,
		// Instruction goes to the system role and content to the user role,
		// and they are never joined. That separation is what stops an
		// instruction inside a document reaching a position where the model
		// reads it as a directive (ADR-0017, rule §7.2). This function is the
		// only place either value is assigned, and neither assignment can see
		// the other's field.
		System:    req.Instruction,
		Messages:  []skyl.Message{{Role: skyl.RoleUser, Parts: parts}},
		MaxTokens: m.maxTokens,
	}

	if req.Temperature != nil {
		// Copied rather than aliased: skyl holds the request across retries,
		// and a caller reusing the pointer must not be able to change a value
		// mid-flight.
		t := *req.Temperature
		call.Temperature = &t
	}

	if len(req.Schema) > 0 {
		// ovrin.ModelRequest.Schema is bytes so an adapter can pass it to a
		// provider verbatim; skyl.ResponseFormat.Schema is a map. Decoded once
		// here, and never rebuilt — reconstructing a schema is how a key goes
		// missing on the way to the wire.
		var schema map[string]any
		if err := json.Unmarshal(req.Schema, &schema); err != nil {
			return nil, m.fail(ovrin.ErrSchema, 0,
				"the json schema could not be decoded")
		}
		call.ResponseFormat = &skyl.ResponseFormat{Name: schemaName, Schema: schema}
	}

	return call, nil
}

// parts maps ovrin's content onto one user message's parts, in order.
//
// What is sent is decided by what a [ovrin.Content] actually carries rather
// than by its [ovrin.Reading] label, so a page that has both an image and a
// caption loses neither.
func (m *Model) parts(content []ovrin.Content) ([]skyl.Part, error) {
	parts := make([]skyl.Part, 0, len(content))

	for _, c := range content {
		if len(c.Image) > 0 {
			if !m.vision {
				return nil, m.fail(ovrin.ErrUnsupported, c.Page,
					"this model is configured as unable to read page images; "+
						"use a vision-capable model, or restrict the reading to text")
			}
			if c.MediaType == "" {
				return nil, m.fail(ovrin.ErrUnsupported, c.Page,
					"a page image arrived without a media type, which no provider accepts")
			}
			// c.Image is raw and skyl.Image.Data is raw, so the bytes go
			// across as they are. Encoding here as well as in skyl's provider
			// would corrupt the image in a way no status code reports.
			parts = append(parts, skyl.Image{MediaType: c.MediaType, Data: c.Image})
		}
		if c.Text != "" {
			parts = append(parts, skyl.Text{Text: c.Text})
		}
	}

	return parts, nil
}

// mapUsage carries skyl's token counts onto ovrin's.
//
// skyl normalises every provider onto one rule: InputTokens is the whole
// input, with cached tokens included in it rather than additional to it —
// which is the opposite of what Anthropic reports natively, and the reason
// summing the fields is wrong. [ovrin.Usage] has no cache fields, so
// InputTokens maps straight across and nothing is double-counted; a caller who
// wants the discount reads the [skyl.Response] out of
// [ovrin.ModelResponse.Raw].
//
// PageUnits stays zero. A model bills tokens; page units are what OCR
// providers bill.
func mapUsage(u skyl.Usage) ovrin.Usage {
	return ovrin.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
	}
}

// classify maps a skyl failure onto an ovrin sentinel.
//
// Every branch tests a sentinel with [errors.Is], never a message string: a
// provider rewording a response must not change how a program behaves
// (rule §2.2).
//
// The message is written here rather than taken from the provider. A provider
// error carries the provider's own text, and providers quote request fragments
// back in validation errors — copying that into an ovrin error would ship
// document content into whatever logs the caller keeps (rule §2.5, §7.5). The
// HTTP status is safe and is the one detail worth forwarding.
func (m *Model) classify(ctx context.Context, err error) error {
	// A caller who stopped waiting is not a provider failure, and gets no
	// sentinel: a fallback chain must not advance to the next provider with a
	// context that is already dead.
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		cause := ctx.Err()
		if cause == nil {
			cause = context.Canceled
		}
		return withCause(m.fail(nil, 0,
			"the context ended before the provider replied"), cause)
	}

	// A live context and an expired deadline means skyl's own per-attempt
	// timeout fired. That is a provider that did not answer, so a fallback
	// chain should advance past it.
	if errors.Is(err, context.DeadlineExceeded) {
		return withCause(m.fail(ovrin.ErrUnavailable, 0,
			"the provider did not reply before the attempt timeout"),
			context.DeadlineExceeded)
	}

	kind, message := ovrin.ErrUnavailable, "the provider could not be reached"
	switch {
	case errors.Is(err, skyl.ErrAuth):
		kind, message = ovrin.ErrAuth, "the provider rejected the credential"
	case errors.Is(err, skyl.ErrRateLimit):
		kind, message = ovrin.ErrRateLimit, "the provider is throttling"
	case errors.Is(err, skyl.ErrServer):
		// skyl folds a genuine 5xx and an undecodable reply into one sentinel,
		// so the wording covers both rather than claiming the wrong one.
		kind, message = ovrin.ErrUnavailable,
			"the provider failed on its side, or returned a reply that could not be read"
	case errors.Is(err, skyl.ErrBadRequest):
		kind, message = ovrin.ErrBadRequest, "the provider rejected the request"
	case errors.Is(err, skyl.ErrNotFound):
		// Not a distinct ovrin sentinel. The remedy is the same as any other
		// rejected request: change the model, or change the provider.
		kind, message = ovrin.ErrBadRequest, "the provider does not offer this model"
	case errors.Is(err, skyl.ErrUnsupported):
		kind, message = ovrin.ErrUnsupported,
			"the provider cannot express part of this request"
	case errors.Is(err, skyl.ErrRefusal):
		// A refusal is a reply ovrin cannot use, not a provider that is down.
		kind, message = ovrin.ErrBadResponse, "the model declined to answer"
	}

	var provErr *skyl.Error
	if errors.As(err, &provErr) && provErr.StatusCode != 0 {
		message = fmt.Sprintf("%s (http %d)", message, provErr.StatusCode)
	}
	return m.fail(kind, 0, message)
}

// fail builds a classified ovrin error naming this adapter and this stage.
func (m *Model) fail(kind error, page int, message string) *ovrin.Error {
	return &ovrin.Error{
		Op:       ovrin.OpGenerate,
		Provider: m.provider,
		Page:     page,
		Kind:     kind,
		Message:  message,
	}
}

// withCause returns e carrying cause, reachable through [errors.Is].
//
// ovrin's own Error has an unexported cause field and no exported way to set
// it, so an adapter outside the core package cannot attach one directly. Two
// %w verbs do the same job: errors.Is finds the sentinel through e's Unwrap
// and the cause through this one, and errors.As still recovers *ovrin.Error.
//
// Only a context error is ever passed here. Its text is fixed and carries
// neither credentials nor document content, which is not true of a provider's
// error — and unlike ovrin's own cause field, this one is printed.
func withCause(e *ovrin.Error, cause error) error {
	return fmt.Errorf("%w: %w", e, cause)
}

var _ ovrin.Model = (*Model)(nil)
