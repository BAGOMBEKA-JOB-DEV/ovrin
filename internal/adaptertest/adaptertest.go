// Package adaptertest is the contract suite every ovrin adapter must pass.
//
// An adapter translates between ovrin's types and a vendor's wire format. The
// rules it must obey are identical regardless of vendor (docs/rules.md §6, and
// the six rules in docs/providers.md): map without deciding, never silently
// drop data, classify errors onto ovrin's sentinels, keep credentials and
// document content out of every error, honour cancellation, and leak nothing.
//
// Writing those assertions once means a new adapter inherits them by filling
// in a suite, and no adapter can regress behind another adapter's tests
// (docs/rules.md §3.1). A rule added here is enforced everywhere at once.
//
// This package is in ovrin's core module, so it depends on the standard
// library and on ovrin itself and on nothing else — a test helper that pulled
// in a vendor SDK would put that SDK in the core's dependency graph, which is
// the whole thing ADR-0008 exists to prevent. Everything vendor-specific
// therefore arrives as a field on the suite: the payloads differ by necessity,
// but nothing asserted about them does.
package adaptertest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// The canaries the suite plants in a request.
//
// They are distinctive strings rather than realistic prose because every
// assertion built on them is a substring search, and a realistic phrase would
// eventually collide with a provider's own error text and turn a real leak
// into a green test.
const (
	// InstructionCanary is planted in [ovrin.ModelRequest.Instruction], which
	// ovrin builds and which never contains document content.
	InstructionCanary = "OVRIN_INSTRUCTION_CANARY"

	// ContentCanary is planted in [ovrin.Content], which is untrusted document
	// material. It must never reach an error string (docs/rules.md §2.5).
	ContentCanary = "OVRIN_DOCUMENT_CANARY"
)

// ImageCanary is the page image the suite sends.
//
// It is raw bytes with a PNG signature, because [ovrin.Content.Image] is raw
// and an adapter that base64-encodes an already-encoded value corrupts the
// image in a way no status code reports.
var ImageCanary = []byte("\x89PNG\r\n\x1a\nOVRIN_IMAGE_CANARY_BYTES")

// TestSchema is the JSON Schema the suite asks for.
//
// The property name is one no adapter models, so finding it on the wire proves
// the caller's schema was transmitted rather than something reconstructed.
const TestSchema = `{"type":"object",` +
	`"properties":{"ovrin_test_property":{"type":"string"}},` +
	`"required":["ovrin_test_property"],"additionalProperties":false}`

// ModelSuite describes one [ovrin.Model] adapter well enough to exercise the
// shared contract.
//
// The payload fields are vendor-specific by necessity — the whole point of an
// adapter is that its wire format differs. Everything asserted about them is
// vendor-neutral.
type ModelSuite struct {
	// Name identifies the adapter in test output.
	Name string

	// New builds the adapter pointed at a test server, with the credential in
	// APIKey and the model in RequestModel already configured.
	//
	// It must configure the adapter for no retries, or the error assertions
	// pay a backoff each. Retry is the core's business anyway
	// (docs/rules.md §6.2).
	New func(baseURL string) ovrin.Model

	// NewWithoutVision builds the adapter configured for a model that cannot
	// read images, so the suite can check that page images are refused rather
	// than dropped.
	//
	// Optional. Leaving it nil skips the one assertion that catches an adapter
	// silently answering a vision request from the text alone, which is the
	// failure docs/rules.md §6.1 singles out as never acceptable — so leave it
	// nil only when the adapter genuinely cannot express the case.
	NewWithoutVision func(baseURL string) ovrin.Model

	// APIKey is the credential New bakes in, so the suite can assert it never
	// reaches an error.
	APIKey string

	// RequestModel is the model identifier New configures. The suite checks it
	// reaches the wire.
	RequestModel string

	// WantModel is the model SuccessBody reports having served.
	//
	// It must differ from RequestModel. Providers can and do serve a different
	// model than the one asked for, and a fixture that uses one name for both
	// cannot tell an adapter that reads the response from one that echoes the
	// request. The suite refuses a fixture that does.
	WantModel string

	// ServedModel reads the served model out of [ovrin.ModelResponse.Raw].
	//
	// Raw is the provider's own value, so only the adapter's own test knows
	// its type. Optional; nil skips the check.
	ServedModel func(raw any) string

	// SuccessBody is a minimal successful completion in the vendor's format.
	SuccessBody string

	// WantJSON is the reply bytes SuccessBody encodes, exactly.
	//
	// It should carry redundant whitespace. The comparison is byte-for-byte,
	// so a fixture that is already compact cannot tell an adapter that passed
	// the bytes through from one that unmarshalled and re-marshalled them —
	// and an adapter must not unmarshal at all (ADR-0007). The suite refuses a
	// fixture that cannot make the distinction.
	WantJSON string

	// WantUsage is the token accounting SuccessBody reports, mapped onto
	// ovrin's [ovrin.Usage].
	//
	// Its input and output counts must differ and both be non-zero, so a
	// mapping that swaps or drops a field is visible.
	WantUsage ovrin.Usage

	// ErrorBody is an error payload in the vendor's format, used with a
	// variety of status codes.
	ErrorBody string

	// EchoErrorBody builds an error payload whose human-readable message
	// quotes the text it is given.
	//
	// This is the privacy trap. Real providers quote request fragments back in
	// validation errors, so an adapter that copies a provider's message into
	// its own ships document content into the caller's logs. Optional: when
	// nil the suite echoes the raw request body instead, which is a weaker
	// version of the same check because most vendor error parsers will not
	// find a message in it.
	EchoErrorBody func(echo string) string

	// SkipImageEncoding turns off the assertions about how image bytes appear
	// on the wire, for an adapter whose transport does not base64 them into a
	// JSON body.
	SkipImageEncoding bool
}

// Model runs the whole contract against a [ovrin.Model] adapter.
//
// The assertions run in sequence rather than in parallel: one of them counts
// goroutines, and a goroutine count is a property of the process, so a
// concurrent sibling test would make it report another test's work as this
// adapter's leak.
func Model(t *testing.T, s ModelSuite) {
	t.Helper()

	if !s.valid(t) {
		return
	}

	t.Run(s.Name+"/returns the reply bytes unparsed", s.testReplyBytes)
	t.Run(s.Name+"/populates Raw", s.testRaw)
	t.Run(s.Name+"/maps usage", s.testUsage)
	t.Run(s.Name+"/reads the served model from the response", s.testServedModel)
	t.Run(s.Name+"/sends the schema to the provider", s.testSchemaReachesTheWire)
	t.Run(s.Name+"/keeps instruction and content apart", s.testInstructionContentSeparation)
	t.Run(s.Name+"/encodes image bytes exactly once", s.testImageEncoding)
	t.Run(s.Name+"/classifies failures onto ovrin sentinels", s.testErrorClassification)
	t.Run(s.Name+"/never puts the credential in an error", s.testCredentialNeverLeaks)
	t.Run(s.Name+"/never puts document content in an error", s.testContentNeverLeaks)
	t.Run(s.Name+"/refuses images it cannot read", s.testUnsupportedRatherThanDegraded)
	t.Run(s.Name+"/aborts promptly on cancellation", s.testContextCancellation)
	t.Run(s.Name+"/is safe for concurrent use", s.testConcurrency)
	t.Run(s.Name+"/leaks no goroutine", s.testNoGoroutineLeak)
}

// valid checks the fixture before the fixture checks the adapter.
//
// Each of these is a way for the suite to pass without proving anything, which
// is worse than failing: a green contract suite is the evidence an adapter is
// finished.
func (s ModelSuite) valid(t *testing.T) bool {
	t.Helper()

	ok := true
	fail := func(format string, args ...any) {
		t.Errorf(format, args...)
		ok = false
	}

	if s.Name == "" {
		fail("ModelSuite.Name is empty; test output would not say which adapter failed")
	}
	if s.New == nil {
		fail("ModelSuite.New is nil; there is nothing to test")
	}
	if s.SuccessBody == "" {
		fail("ModelSuite.SuccessBody is empty")
	}
	if s.ErrorBody == "" {
		fail("ModelSuite.ErrorBody is empty")
	}
	if s.RequestModel == "" {
		fail("ModelSuite.RequestModel is empty")
	}
	if s.WantModel != "" && s.WantModel == s.RequestModel {
		fail("ModelSuite.WantModel equals RequestModel (%q); the fixture cannot tell "+
			"an adapter that reads the response from one that echoes the request",
			s.WantModel)
	}
	if s.WantJSON == "" {
		fail("ModelSuite.WantJSON is empty")
	} else if compacted, err := compact(s.WantJSON); err == nil && compacted == s.WantJSON {
		fail("ModelSuite.WantJSON is already compact; give it redundant whitespace, "+
			"or the fixture cannot tell a byte-for-byte passthrough from an adapter "+
			"that unmarshalled and re-marshalled the reply (ADR-0007). got: %s", s.WantJSON)
	}
	if s.WantUsage.InputTokens == 0 || s.WantUsage.OutputTokens == 0 {
		fail("ModelSuite.WantUsage has a zero token count; a mapping that drops a "+
			"field would still pass. got: %+v", s.WantUsage)
	}
	if s.WantUsage.InputTokens == s.WantUsage.OutputTokens {
		fail("ModelSuite.WantUsage has equal input and output counts; a mapping that " +
			"swaps them would still pass")
	}
	return ok
}

// ---------------------------------------------------------------------------
// The request under test
// ---------------------------------------------------------------------------

// request is the extraction call the suite makes.
func (s ModelSuite) request() ovrin.ModelRequest {
	temperature := 0.0
	return ovrin.ModelRequest{
		Instruction: "Extract the fields described by the schema. " + InstructionCanary,
		Content: []ovrin.Content{{
			Reading: ovrin.ReadingText,
			Page:    1,
			Text:    ContentCanary,
		}},
		Schema:      []byte(TestSchema),
		Temperature: &temperature,
	}
}

// requestWithImage is [ModelSuite.request] with a page image added.
func (s ModelSuite) requestWithImage() ovrin.ModelRequest {
	req := s.request()
	req.Content = append(req.Content, ovrin.Content{
		Reading:   ovrin.ReadingVision,
		Page:      2,
		Image:     ImageCanary,
		MediaType: "image/png",
	})
	return req
}

// ---------------------------------------------------------------------------
// Servers
// ---------------------------------------------------------------------------

// recorder keeps every request body a test server received.
type recorder struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (r *recorder) add(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, b)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// last returns the newest body, or nil when none arrived.
func (r *recorder) last() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return nil
	}
	return r.bodies[len(r.bodies)-1]
}

// serve starts a server answering every request with status and body, and
// returns the adapter pointed at it.
//
// The handler ignores the path: adapters disagree about where an endpoint
// lives, and the suite has no business knowing.
func (s ModelSuite) serve(t *testing.T, status int, body string) (ovrin.Model, *recorder) {
	t.Helper()
	return s.serveFunc(t, func(raw []byte) (int, string) { return status, body })
}

// serveFunc starts a server whose answer is computed from the request body,
// for the assertions that need the provider to react to what it was sent.
func (s ModelSuite) serveFunc(t *testing.T, answer func(raw []byte) (int, string)) (ovrin.Model, *recorder) {
	t.Helper()

	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			raw = nil
		}
		rec.add(raw)

		status, body := answer(raw)
		// Real providers always declare JSON, and some clients refuse to
		// decode a body without it — so the fake must too, or it is not a fake
		// of anything real.
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		// A failed write means the adapter under test hung up, which the
		// assertion will report far more usefully than this handler could.
		_, _ = io.WriteString(w, body) //nolint:errcheck // see above
	}))
	t.Cleanup(srv.Close)

	return s.New(srv.URL), rec
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

// An adapter must not unmarshal the reply. A model returning invalid JSON has
// to produce one ovrin error with the offending bytes attached, and it can
// only be one error if every adapter hands the bytes back untouched
// (ADR-0007, docs/providers.md).
func (s ModelSuite) testReplyBytes(t *testing.T) {
	m, _ := s.serve(t, http.StatusOK, s.SuccessBody)

	resp, err := m.Generate(context.Background(), s.request())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got := string(resp.JSON); got != s.WantJSON {
		t.Errorf("ModelResponse.JSON = %q, want %q byte-for-byte; the adapter must "+
			"not parse or reformat the reply", got, s.WantJSON)
	}
}

// Raw is the caller's escape hatch from ovrin's abstraction, so an adapter
// that forgets it removes the only way to reach anything ovrin does not model.
func (s ModelSuite) testRaw(t *testing.T) {
	m, _ := s.serve(t, http.StatusOK, s.SuccessBody)

	resp, err := m.Generate(context.Background(), s.request())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if resp.Raw == nil {
		t.Error("ModelResponse.Raw is nil; it must carry the provider's own response")
	}
}

func (s ModelSuite) testUsage(t *testing.T) {
	m, _ := s.serve(t, http.StatusOK, s.SuccessBody)

	resp, err := m.Generate(context.Background(), s.request())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if resp.Usage != s.WantUsage {
		t.Errorf("ModelResponse.Usage = %+v, want %+v", resp.Usage, s.WantUsage)
	}
}

// Providers can and do serve a different model than the one requested, so the
// response must report what actually ran rather than what was asked for.
func (s ModelSuite) testServedModel(t *testing.T) {
	m, rec := s.serve(t, http.StatusOK, s.SuccessBody)

	resp, err := m.Generate(context.Background(), s.request())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if body := string(rec.last()); !strings.Contains(body, s.RequestModel) {
		t.Errorf("the configured model %q never reached the wire\nbody: %s",
			s.RequestModel, body)
	}

	if s.ServedModel == nil || s.WantModel == "" {
		t.Skip("suite does not expose the served model")
	}
	got := s.ServedModel(resp.Raw)
	if got == s.RequestModel {
		t.Fatalf("the served model reads back as the requested model %q; the adapter "+
			"echoed the request instead of reading the response", s.RequestModel)
	}
	if got != s.WantModel {
		t.Errorf("served model = %q, want %q read from the response", got, s.WantModel)
	}
}

// The schema must arrive intact. This cannot assert a location — the wire
// shapes share no key — so it asserts that the schema's own contents appear
// somewhere in the encoded body, which is what catches an adapter that
// rebuilds the schema rather than passing it through.
func (s ModelSuite) testSchemaReachesTheWire(t *testing.T) {
	m, rec := s.serve(t, http.StatusOK, s.SuccessBody)

	if _, err := m.Generate(context.Background(), s.request()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := string(rec.last())
	if body == "" {
		t.Fatal("no request body was sent")
	}
	if !strings.Contains(body, "ovrin_test_property") {
		t.Errorf("the schema never reached the provider; docs/rules.md §6.1 forbids "+
			"dropping request data silently\nbody: %s", body)
	}
}

// The separation between Instruction and Content is a security boundary, not a
// formatting preference: an injected instruction inside a document must never
// reach a position where the model reads it as a directive
// (ADR-0017, docs/rules.md §7.2).
//
// The check is vendor-neutral because it asserts a shape rather than a field
// name: somewhere in the encoded request there is a string carrying the
// instruction, somewhere there is a different string carrying the content, and
// nowhere is there one string carrying both. Concatenation cannot satisfy that
// regardless of which keys a vendor uses.
func (s ModelSuite) testInstructionContentSeparation(t *testing.T) {
	m, rec := s.serve(t, http.StatusOK, s.SuccessBody)

	if _, err := m.Generate(context.Background(), s.request()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	sep, err := separation(rec.last())
	if err != nil {
		t.Fatalf("the request body is not JSON, so the suite cannot check the "+
			"instruction/content boundary: %v\nbody: %s", err, rec.last())
	}
	sawInstruction, sawContent, sawBoth := sep.instruction, sep.content, sep.both

	if sawBoth {
		t.Error("the instruction and the document content arrived in the same string; " +
			"ADR-0017 forbids concatenating them, because that is the position an " +
			"injected instruction needs to be read as a directive")
	}
	if !sawInstruction {
		t.Errorf("the instruction never reached the wire\nbody: %s", rec.last())
	}
	if !sawContent {
		t.Errorf("the document content never reached the wire\nbody: %s", rec.last())
	}
}

// [ovrin.Content.Image] is raw bytes. An adapter that treats it as already
// encoded produces a body a provider will reject, or worse, accept as a
// corrupt image and answer from nothing.
func (s ModelSuite) testImageEncoding(t *testing.T) {
	if s.SkipImageEncoding {
		t.Skip("suite does not base64 images into a JSON body")
	}

	m, rec := s.serve(t, http.StatusOK, s.SuccessBody)

	if _, err := m.Generate(context.Background(), s.requestWithImage()); err != nil {
		t.Fatalf("Generate() with an image error = %v", err)
	}

	body := string(rec.last())
	once := base64.StdEncoding.EncodeToString(ImageCanary)
	twice := base64.StdEncoding.EncodeToString([]byte(once))

	if strings.Contains(body, twice) {
		t.Error("the image was base64-encoded twice; ovrin.Content.Image is raw bytes " +
			"and encoding it again corrupts it silently")
	}
	if !strings.Contains(body, once) {
		t.Errorf("the image never reached the wire in its encoded form\nbody: %s", body)
	}
}

// Nothing downstream may branch on the text of a provider's message, so every
// failure has to arrive as one of ovrin's sentinels (docs/rules.md §2.2).
func (s ModelSuite) testErrorClassification(t *testing.T) {
	// Fixed, not configurable. A rule added here is enforced for every
	// adapter at once, which is the reason this suite exists.
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"401 is an authentication failure", http.StatusUnauthorized, ovrin.ErrAuth},
		{"403 is an authentication failure", http.StatusForbidden, ovrin.ErrAuth},
		{"429 is a rate limit", http.StatusTooManyRequests, ovrin.ErrRateLimit},
		{"400 is a rejected request", http.StatusBadRequest, ovrin.ErrBadRequest},
		{"500 is an unavailable provider", http.StatusInternalServerError, ovrin.ErrUnavailable},
		{"503 is an unavailable provider", http.StatusServiceUnavailable, ovrin.ErrUnavailable},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m, _ := s.serve(t, tc.status, s.ErrorBody)

			resp, err := m.Generate(context.Background(), s.request())
			if err == nil {
				t.Fatalf("Generate() error = nil, want a failure for status %d", tc.status)
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
			if e.Provider == "" {
				t.Error("Error.Provider is empty; a result must carry evidence of " +
					"which adapter served it")
			}
		})
	}
}

// A credential in an error string ends up in the logs of everyone who prints
// the error (docs/rules.md §2.5, §7.5).
func (s ModelSuite) testCredentialNeverLeaks(t *testing.T) {
	if s.APIKey == "" {
		t.Skip("suite has no credential to check")
	}

	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	} {
		m, _ := s.serve(t, status, s.ErrorBody)
		_, err := m.Generate(context.Background(), s.request())
		if err == nil {
			t.Errorf("status %d: Generate() error = nil", status)
			continue
		}
		assertAbsent(t, err, "the API key", s.APIKey)
	}
}

// A document is somebody's invoice or medical record, and an error string is a
// log line that ends up in systems nobody audited (docs/rules.md §2.5).
//
// The trap is a provider that quotes the request back in its error message,
// which real providers do. An adapter that copies the provider's message into
// its own passes every other test here and still leaks.
//
// What is checked is the rendered message — [ovrin.Error.Error] and
// [ovrin.Error.Message] — rather than the whole unwrap chain. A provider error
// attached as a cause is reachable through errors.As but is never printed by
// ovrin, and dropping it would cost errors.Is(err, context.DeadlineExceeded),
// which ovrin's own error documentation promises.
func (s ModelSuite) testContentNeverLeaks(t *testing.T) {
	echo := s.EchoErrorBody
	if echo == nil {
		// Weaker, but still a leak if the adapter dumps a body into a message.
		echo = func(string) string { return "" }
	}

	m, _ := s.serveFunc(t, func(raw []byte) (int, string) {
		if body := echo(ContentCanary); body != "" {
			return http.StatusBadRequest, body
		}
		return http.StatusBadRequest, string(raw)
	})

	_, err := m.Generate(context.Background(), s.request())
	if err == nil {
		t.Fatal("Generate() error = nil, want a failure")
	}

	assertAbsent(t, err, "document content", ContentCanary)
	assertAbsent(t, err, "the encoded page image",
		base64.StdEncoding.EncodeToString(ImageCanary))
}

// An adapter that cannot serve a request says so. It never quietly produces a
// worse answer than the caller believes they asked for, which docs/rules.md
// §6.1 names as the one behaviour that is never acceptable.
func (s ModelSuite) testUnsupportedRatherThanDegraded(t *testing.T) {
	if s.NewWithoutVision == nil {
		t.Skip("suite does not expose a model that cannot read images")
	}

	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body) //nolint:errcheck // a short read is still evidence
		rec.add(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, s.SuccessBody) //nolint:errcheck // the assertion reports the failure
	}))
	t.Cleanup(srv.Close)

	resp, err := s.NewWithoutVision(srv.URL).Generate(context.Background(), s.requestWithImage())
	if err == nil {
		t.Fatal("Generate() answered a vision request from a model without vision; " +
			"it must return ErrUnsupported rather than answer from the text alone")
	}
	if resp != nil {
		t.Error("Generate() returned both a response and an error")
	}
	if !errors.Is(err, ovrin.ErrUnsupported) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrUnsupported)
	}

	// Refusing before the call and refusing after the provider does are both
	// compliant; sending a request with the image stripped out is not.
	if rec.count() > 0 && !s.SkipImageEncoding {
		want := base64.StdEncoding.EncodeToString(ImageCanary)
		if !strings.Contains(string(rec.last()), want) {
			t.Error("a request was sent without the image; an adapter that cannot " +
				"represent an image must refuse, not drop it and proceed on the text")
		}
	}
}

// A hung provider must not outlive the caller's context, or it exhausts the
// caller's resources (docs/rules.md §5.4).
func (s ModelSuite) testContextCancellation(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-blocked:
		}
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })

	m := s.New(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.Generate(ctx, s.request())
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Generate() returned nil after the context was cancelled")
		}
		// ovrin's error model promises that one value answers both "what kind
		// of failure was this?" and "was it ultimately a cancelled context?".
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want errors.Is(err, context.Canceled) to hold", err)
		}
		assertAbsent(t, err, "document content", ContentCanary)
	case <-time.After(5 * time.Second):
		t.Fatal("Generate() ignored context cancellation and is still running")
	}
}

// Everything exported is safe for concurrent use by multiple goroutines
// (docs/rules.md §5.1). Run under -race, this is where a shared request struct
// or a reused buffer surfaces.
func (s ModelSuite) testConcurrency(t *testing.T) {
	m, _ := s.serve(t, http.StatusOK, s.SuccessBody)

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	bodies := make(chan string, n)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			resp, err := m.Generate(context.Background(), s.request())
			if err != nil {
				errs <- err
				return
			}
			bodies <- string(resp.JSON)
		}()
	}
	wg.Wait()
	close(errs)
	close(bodies)

	for err := range errs {
		t.Errorf("concurrent Generate() error = %v", err)
	}
	for got := range bodies {
		if got != s.WantJSON {
			t.Errorf("concurrent Generate() JSON = %q, want %q", got, s.WantJSON)
		}
	}
}

// A leaked goroutine never shows up in a test's wall clock; it surfaces as
// unbounded memory growth in production. -race does not catch it either — a
// goroutine that is merely blocked is not a data race (docs/rules.md §3.6).
//
// Early return is included deliberately: the abandoned path is the one nobody
// exercises by hand.
func (s ModelSuite) testNoGoroutineLeak(t *testing.T) {
	m, _ := s.serve(t, http.StatusOK, s.SuccessBody)

	blocked := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-blocked:
		}
	}))
	t.Cleanup(func() { close(blocked); hung.Close() })
	hungModel := s.New(hung.URL)

	// Warm up before the baseline. A test server's accept loop and the HTTP
	// client's persistent connections are created on first use and torn down
	// by t.Cleanup, which runs after the deferred check below — counting them
	// in the baseline is what keeps this measuring the adapter rather than
	// net/http.
	if _, err := m.Generate(context.Background(), s.request()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	warmCtx, warmCancel := context.WithCancel(context.Background())
	go warmCancel()
	_, _ = hungModel.Generate(warmCtx, s.request()) //nolint:errcheck // warm-up only

	defer checkNoGoroutineLeaks(t)()

	for i := 0; i < 5; i++ {
		if _, err := m.Generate(context.Background(), s.request()); err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
	}
	// And the early-return path: a caller that gives up part way through.
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go cancel()
		_, _ = hungModel.Generate(ctx, s.request()) //nolint:errcheck // abandonment is the point
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// assertAbsent fails when needle appears anywhere ovrin renders an error.
func assertAbsent(t *testing.T, err error, what, needle string) {
	t.Helper()
	if where, leaked := leaks(err, needle); leaked {
		t.Errorf("%s appears in %s: %v", what, where, err)
	}
}

// leaks reports whether needle appears anywhere ovrin renders err, and names
// where.
//
// What is inspected is the rendered message — [ovrin.Error.Error] and
// [ovrin.Error.Message] — rather than the whole unwrap chain. §2.5's stated
// rationale is the log line, an attached cause is not printed by ovrin, and an
// adapter needs to attach the context error so that
// errors.Is(err, context.DeadlineExceeded) keeps working (ADR-0019).
func leaks(err error, needle string) (where string, leaked bool) {
	if err == nil || needle == "" {
		return "", false
	}
	if strings.Contains(err.Error(), needle) {
		return "the error message", true
	}
	var e *ovrin.Error
	if errors.As(err, &e) && strings.Contains(e.Message, needle) {
		return "ovrin.Error.Message", true
	}
	return "", false
}

// separationReport is what [separation] found in an encoded request.
type separationReport struct {
	// instruction is true when some string carries the instruction alone.
	instruction bool

	// content is true when some string carries the document content alone.
	content bool

	// both is true when a single string carries both, which is the
	// concatenation ADR-0017 forbids.
	both bool
}

// separation decodes an encoded request and reports how the canaries are
// distributed across its string values.
//
// Pulled out of the assertion so the predicate itself can be tested: a check
// that cannot fail is not a check, and this one is the suite's only guard on a
// security boundary.
func separation(body []byte) (separationReport, error) {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return separationReport{}, err
	}

	var r separationReport
	for _, leaf := range stringLeaves(decoded) {
		hasInstruction := strings.Contains(leaf, InstructionCanary)
		hasContent := strings.Contains(leaf, ContentCanary)
		switch {
		case hasInstruction && hasContent:
			r.both = true
		case hasInstruction:
			r.instruction = true
		case hasContent:
			r.content = true
		}
	}
	return r, nil
}

// stringLeaves returns every string value in a decoded JSON document.
//
// Values only. Keys are the adapter's vocabulary and a canary can never be
// one, so collecting them would only add noise.
func stringLeaves(v any) []string {
	var out []string
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case string:
			out = append(out, n)
		case []any:
			for _, item := range n {
				walk(item)
			}
		case map[string]any:
			// Sorted, so a failure message reads the same way twice.
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(n[k])
			}
		}
	}
	walk(v)
	return out
}

// compact returns s with insignificant JSON whitespace removed.
func compact(s string) (string, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return "", err
	}
	return b.String(), nil
}

// leakSettleTimeout bounds how long the leak check waits for goroutines to
// wind down. A goroutine that is genuinely exiting does so in microseconds;
// this budget exists so a slow runner does not produce a false positive.
const leakSettleTimeout = 2 * time.Second

// checkNoGoroutineLeaks fails the test if it finishes with more goroutines
// running than it started with.
//
// Call it at the top of a test, noting the trailing parentheses — the call
// returns the check, and defer runs it when the test ends:
//
//	defer checkNoGoroutineLeaks(t)()
func checkNoGoroutineLeaks(t *testing.T) func() {
	t.Helper()

	// Let anything winding down from earlier finish first, so this measures
	// the test rather than its predecessor.
	before := stableGoroutineCount()

	return func() {
		t.Helper()

		deadline := time.Now().Add(leakSettleTimeout)
		var after int
		for {
			after = runtime.NumGoroutine()
			if after <= before {
				return
			}
			if time.Now().After(deadline) {
				break
			}
			// Yield rather than spin: the goroutines being waited on need
			// scheduler time to reach their exit.
			time.Sleep(5 * time.Millisecond)
		}

		t.Errorf("goroutine leak: %d before, %d after (waited %v)\n\n%s",
			before, after, leakSettleTimeout, interestingStacks())
	}
}

// stableGoroutineCount waits for the goroutine count to stop moving, then
// returns it.
//
// Without this, a baseline taken while an earlier test's connections are still
// closing reads low, and the next test is blamed for the difference.
func stableGoroutineCount() int {
	deadline := time.Now().Add(leakSettleTimeout)
	prev := runtime.NumGoroutine()

	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		cur := runtime.NumGoroutine()
		if cur == prev {
			return cur
		}
		prev = cur
	}
	return prev
}

// interestingStacks renders the goroutine dump with the runtime's own
// bookkeeping removed, so the report shows the leak rather than the scheduler.
func interestingStacks() string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]

	var keep []string
	for _, stack := range strings.Split(string(buf), "\n\n") {
		if stack == "" || isRuntimeNoise(stack) {
			continue
		}
		keep = append(keep, stack)
	}
	sort.Strings(keep)

	const maxReported = 10
	if len(keep) > maxReported {
		keep = keep[:maxReported]
	}
	return strings.Join(keep, "\n\n")
}

// runtimeNoise names goroutines that are always present in a test binary and
// are never the leak being hunted.
//
// Deliberately narrow. A broad marker such as "runtime.gopark" would match
// almost every parked goroutine — including the leaked one — and quietly turn
// the report empty, which is worse than noisy.
var runtimeNoise = []string{
	"testing.tRunner",       // the harness running each test
	"testing.(*M).Run",      // the test binary's main
	"os/signal.signal_recv", // signal handler, started by the runtime
}

func isRuntimeNoise(stack string) bool {
	for _, marker := range runtimeNoise {
		if strings.Contains(stack, marker) {
			return true
		}
	}
	return false
}
