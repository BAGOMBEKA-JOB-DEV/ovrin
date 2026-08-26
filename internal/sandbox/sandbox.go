// Package sandbox serves a provider's wire protocol over a real socket,
// adversarially, with no credential and no cost.
//
// # Why a socket rather than a fake
//
// An in-process fake proves that the adapter's mapping agrees with itself. It
// cannot prove anything about the bytes: a body that serialises wrongly, a
// response whose content type is a lie, a connection that dies half way
// through a JSON document, a cancelled context that leaves a goroutine reading
// a socket nobody will ever write to. Those failures only exist once there is
// a socket, and they are the failures adapters actually ship
// (docs/adr/0022-offline-testing.md).
//
// # Why it misbehaves on purpose
//
// A sandbox that only serves happy paths tests nothing an in-process fake
// would not. This one serves malformed JSON, truncated bodies, wrong content
// types, 429 with and without Retry-After, 500s, and a disconnection in the
// middle of the body — on request, so a test names the failure it wants
// instead of waiting for a real provider to have a bad day.
//
// A fault is selected in either of two ways:
//
//	srv := sandbox.New(sandbox.WithFault(sandbox.FaultServerError))
//
// or, without touching the server at all, by asking for a model whose name is
// the fault. Model identifiers are passed through untouched by every adapter
// worth the name, so this reaches the sandbox through code that has no idea
// the sandbox exists:
//
//	model := sandbox.FaultModel(sandbox.FaultRateLimitRetryAfter)
//
// # Deliberate limits
//
// There is no model here. Replies come from a fixed string, and token counts
// are configured rather than counted, so the sandbox says nothing about how a
// real model behaves. It is a protocol, not an oracle.
//
// A Server is safe for concurrent use by multiple goroutines.
package sandbox

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// DefaultAPIKey is the credential the sandbox accepts when none is configured.
//
// It is a fixed, well-known string on purpose. This server guards nothing, and
// a random-looking secret would suggest otherwise.
const DefaultAPIKey = "sandbox-key"

// DefaultModel is the model the sandbox reports having served.
//
// It deliberately differs from anything a caller would ask for, so a test that
// requests one model and reads this one back has proved the adapter read the
// response instead of echoing the request.
const DefaultModel = "sandbox-model-that-served-it"

// DefaultReply is the assistant content the sandbox returns: a small JSON
// object, because every caller of this package is exercising structured
// output.
const DefaultReply = `{"ok":true,"served_by":"sandbox"}`

// Fault names a way for the sandbox to misbehave.
//
// The zero value serves the happy path. Every other value is a failure some
// provider has actually produced, which is the bar for adding one: a fault
// nobody has seen tests a code path nobody needs.
type Fault string

// The faults the sandbox can serve.
const (
	// FaultNone serves a well-formed successful completion.
	FaultNone Fault = ""

	// FaultMalformedJSON returns 200 and a body that is not JSON at all,
	// which is what a proxy erroring in front of a provider looks like.
	FaultMalformedJSON Fault = "malformed-json"

	// FaultTruncatedBody returns 200 with a Content-Length that promises more
	// than it delivers, then closes. The client reads a short body and must
	// surface an error rather than a half-decoded response.
	FaultTruncatedBody Fault = "truncated-body"

	// FaultWrongContentType returns 200 and an HTML error page under a
	// text/html content type — the signature of a load balancer or captive
	// portal answering instead of the provider.
	FaultWrongContentType Fault = "wrong-content-type"

	// FaultRateLimitRetryAfter returns 429 with a Retry-After header, the
	// case a retry policy is supposed to honour.
	FaultRateLimitRetryAfter Fault = "rate-limit-retry-after"

	// FaultRateLimitNoRetryAfter returns 429 with no Retry-After header, the
	// case a retry policy must survive without one. Providers do both.
	FaultRateLimitNoRetryAfter Fault = "rate-limit-no-retry-after"

	// FaultServerError returns 500 with the provider's error envelope.
	FaultServerError Fault = "server-error"

	// FaultUnavailable returns 503, which a caller must treat like 500 rather
	// than like a client error.
	FaultUnavailable Fault = "unavailable"

	// FaultDisconnectMidBody returns 200, writes a chunk of a valid response,
	// flushes it, and then drops the connection without the terminating
	// chunk. The client has already seen bytes, so there is no status code
	// left to fail with.
	FaultDisconnectMidBody Fault = "disconnect-mid-body"

	// FaultUnauthorized returns 401.
	FaultUnauthorized Fault = "unauthorized"

	// FaultForbidden returns 403, which classifies as an authentication
	// failure even though its status does not say so.
	FaultForbidden Fault = "forbidden"

	// FaultBadRequest returns 400.
	FaultBadRequest Fault = "bad-request"

	// FaultNotFound returns 404, the answer to a model identifier that does
	// not exist for this account.
	FaultNotFound Fault = "not-found"

	// FaultEchoPrompt returns 400 whose human-readable message quotes the
	// prompt back.
	//
	// This is the privacy trap. Real providers do quote request fragments in
	// their validation errors, so an adapter that forwards a provider's
	// message into its own error message ships document content into whatever
	// logs the caller keeps (docs/rules.md §2.5, §7.5). Nothing else in the
	// sandbox can catch that.
	FaultEchoPrompt Fault = "echo-prompt"

	// FaultRefusal returns 200 with a content-filter finish reason and no
	// content, which is a failure wearing a success's status code.
	FaultRefusal Fault = "refusal"

	// FaultEmptyChoices returns 200 with an empty choices array: a
	// well-formed envelope carrying nothing.
	FaultEmptyChoices Fault = "empty-choices"

	// FaultInvalidJSONReply returns a perfectly valid completion whose
	// assistant content is not valid JSON.
	//
	// The envelope is fine, so this is not a transport failure; it is the
	// model failing to honour the schema. An adapter must hand the bytes back
	// unparsed and let the core classify them, so that every provider fails
	// this the same way (docs/adr/0007-model-seam.md).
	FaultInvalidJSONReply Fault = "invalid-json-reply"

	// FaultHang accepts the request and never answers, until the client's
	// context is cancelled or the server is closed. It is how cancellation
	// and goroutine lifetime get tested without a timer.
	FaultHang Fault = "hang"
)

// FaultModelPrefix marks a model identifier that selects a fault.
//
// It is a prefix rather than a header because a model identifier is the one
// field every adapter passes through untouched, so a test can reach the
// sandbox without the adapter offering any hook of its own.
const FaultModelPrefix = "sandbox-fault-"

// FaultModel returns the model identifier that asks the sandbox for f.
//
// Use it in place of a real model name:
//
//	m := myadapter.New(key, sandbox.FaultModel(sandbox.FaultServerError))
func FaultModel(f Fault) string { return FaultModelPrefix + string(f) }

// faultFromModel reports the fault a model identifier asks for, if any.
func faultFromModel(model string) (Fault, bool) {
	rest, ok := strings.CutPrefix(model, FaultModelPrefix)
	if !ok {
		return FaultNone, false
	}
	switch f := Fault(rest); f {
	case FaultMalformedJSON, FaultTruncatedBody, FaultWrongContentType,
		FaultRateLimitRetryAfter, FaultRateLimitNoRetryAfter,
		FaultServerError, FaultUnavailable, FaultDisconnectMidBody,
		FaultUnauthorized, FaultForbidden, FaultBadRequest, FaultNotFound,
		FaultEchoPrompt, FaultRefusal, FaultEmptyChoices,
		FaultInvalidJSONReply, FaultHang:
		return f, true
	default:
		return FaultNone, false
	}
}

// Usage is the token accounting the sandbox reports.
//
// Cached is a subset of Input, not an addition to it, matching what OpenAI's
// prompt_tokens_details.cached_tokens means. An adapter that adds the two
// over-reports, and this is the fixture that catches it.
type Usage struct {
	Input  int
	Output int
	Cached int
}

// DefaultUsage is deliberately asymmetric, and its cached figure is a strict
// subset of its input figure, so a mapping that swaps or sums fields produces
// a number no correct mapping produces.
var DefaultUsage = Usage{Input: 137, Output: 42, Cached: 64}

// Server is a running sandbox.
//
// It is safe for concurrent use by multiple goroutines, and must be closed.
type Server struct {
	srv     *httptest.Server
	closing chan struct{}
	once    sync.Once

	mu       sync.Mutex
	apiKey   string
	fault    Fault
	queue    []Fault
	model    string
	reply    string
	usage    Usage
	requests int
	bodies   [][]byte
}

// Option configures a [Server].
type Option func(*Server)

// WithAPIKey sets the bearer token the sandbox requires.
//
// An empty key means the sandbox accepts an unauthenticated request, which is
// the behaviour local runtimes such as Ollama actually have.
func WithAPIKey(key string) Option {
	return func(s *Server) { s.apiKey = key }
}

// WithFault sets the fault served for every request that does not name one
// itself.
func WithFault(f Fault) Option {
	return func(s *Server) { s.fault = f }
}

// WithModel sets the model the sandbox reports having served.
//
// Set it to something the caller did not ask for. That difference is the only
// thing separating an adapter that reads the response from one that echoes the
// request, and the two are indistinguishable when the fixture uses one name.
func WithModel(id string) Option {
	return func(s *Server) { s.model = id }
}

// WithReply sets the assistant content returned on the happy path.
func WithReply(content string) Option {
	return func(s *Server) { s.reply = content }
}

// WithUsage sets the token counts reported. Cached must be a subset of input.
func WithUsage(u Usage) Option {
	return func(s *Server) { s.usage = u }
}

// New starts a sandbox and returns it. The caller must Close it.
func New(opts ...Option) *Server {
	s := &Server{
		closing: make(chan struct{}),
		apiKey:  DefaultAPIKey,
		model:   DefaultModel,
		reply:   DefaultReply,
		usage:   DefaultUsage,
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("/v1/models", s.models)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "sandbox": true})
	})
	// Anything else answers in the provider's error shape rather than with
	// net/http's plain-text 404, so an adapter pointed at the wrong path sees
	// a provider error and not a decoding failure.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.record(nil)
		apiError(w, http.StatusNotFound, "invalid_request_error", "unknown_url",
			"Unrecognised request URL: "+r.Method+" "+r.URL.Path)
	})

	s.srv = httptest.NewServer(mux)
	return s
}

// Close stops the server and releases every hung request.
//
// Closing releases [FaultHang] first. httptest waits for handlers to return,
// so a hung handler with nobody left to cancel it would deadlock Close — which
// would look like a test that hangs for no reason.
func (s *Server) Close() {
	s.once.Do(func() {
		close(s.closing)
		s.srv.Close()
	})
}

// URL is the server's root, with no trailing slash.
func (s *Server) URL() string { return s.srv.URL }

// BaseURL is the OpenAI API root, which is what an adapter's WithBaseURL
// option wants: the ".../v1" that "/chat/completions" is appended to.
func (s *Server) BaseURL() string { return s.srv.URL + "/v1" }

// Client returns an HTTP client wired to this server, for tests that want to
// speak the protocol directly.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// SetFault replaces the default fault. It applies to every subsequent request
// that does not name a fault in its model identifier.
//
// Named with the Set prefix because a bare Fault would read as a getter beside
// the type of the same name.
func (s *Server) SetFault(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = f
}

// Enqueue schedules faults for the next requests, one each, in order.
//
// It exists for the sequences a single setting cannot express — a 429 followed
// by success is how a retry policy is proved to retry rather than merely to
// survive. Requests beyond the queue fall back to the default fault.
func (s *Server) Enqueue(faults ...Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, faults...)
}

// Requests returns how many requests have been served, so a test can assert
// that a retry actually reached the wire.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Bodies returns every request body received, in order.
//
// The slice and its contents are copies, so a caller can read them while the
// server keeps serving.
func (s *Server) Bodies() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.bodies))
	for i, b := range s.bodies {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

// LastBody returns the most recent request body, or nil when none has arrived.
func (s *Server) LastBody() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return nil
	}
	return append([]byte(nil), s.bodies[len(s.bodies)-1]...)
}

// record files a request body and returns the fault to serve for it.
func (s *Server) record(body []byte) Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	if body != nil {
		s.bodies = append(s.bodies, body)
	}
	if len(s.queue) > 0 {
		f := s.queue[0]
		s.queue = s.queue[1:]
		return f
	}
	return s.fault
}

// settings returns the configured reply shape under one lock acquisition.
func (s *Server) settings() (model, reply string, usage Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model, s.reply, s.usage
}

// authorised reports whether r carries the configured credential.
func (s *Server) authorised(r *http.Request) bool {
	s.mu.Lock()
	key := s.apiKey
	s.mu.Unlock()

	if key == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+key
}
