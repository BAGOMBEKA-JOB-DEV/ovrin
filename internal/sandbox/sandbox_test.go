package sandbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/sandbox"
)

// exchange is one round trip, with the two kinds of failure kept apart.
//
// Half of what this sandbox exists to serve is a response whose headers are
// fine and whose body is not, and a helper that returned one error could not
// tell that from a request that never connected.
type exchange struct {
	resp *http.Response
	body []byte

	// dialErr means no response ever arrived.
	dialErr error

	// readErr means the headers arrived but the body did not survive.
	readErr error
}

// post sends a minimal chat-completions request.
//
// It deliberately does not decode: half the point of the sandbox is bodies
// that cannot be decoded, and a helper that decoded for its callers could not
// express them.
func post(t *testing.T, s *sandbox.Server, model string) exchange {
	t.Helper()

	body := `{"model":` + quote(model) + `,` +
		`"messages":[{"role":"user","content":"hello"}]}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		s.BaseURL()+"/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sandbox.DefaultAPIKey)

	resp, err := s.Client().Do(req)
	if err != nil {
		return exchange{dialErr: err}
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	raw, readErr := io.ReadAll(resp.Body)
	return exchange{resp: resp, body: raw, readErr: readErr}
}

// mustPost is post for the cases where any failure is a broken sandbox rather
// than the fault under test.
func mustPost(t *testing.T, s *sandbox.Server, model string) (*http.Response, []byte) {
	t.Helper()
	ex := post(t, s, model)
	if ex.dialErr != nil {
		t.Fatalf("request failed: %v", ex.dialErr)
	}
	if ex.readErr != nil {
		t.Fatalf("response body did not survive: %v", ex.readErr)
	}
	return ex.resp, ex.body
}

func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// The happy path has to be right before any of the faults mean anything: a
// sandbox that cannot serve a valid completion is not serving a protocol.
func TestServeSuccess(t *testing.T) {
	t.Parallel()

	s := sandbox.New()
	defer s.Close()

	resp, raw := mustPost(t, s, "any-model")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, raw)
	}

	// The served model must differ from the requested one, or no test built on
	// this sandbox can tell an adapter that reads the response from one that
	// echoes the request.
	if got.Model == "any-model" {
		t.Error("the sandbox echoed the requested model; it must report its own")
	}
	if got.Model != sandbox.DefaultModel {
		t.Errorf("model = %q, want %q", got.Model, sandbox.DefaultModel)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(got.Choices))
	}
	if got.Choices[0].Message.Content != sandbox.DefaultReply {
		t.Errorf("content = %q, want %q", got.Choices[0].Message.Content, sandbox.DefaultReply)
	}
	if got.Usage.PromptTokens != sandbox.DefaultUsage.Input {
		t.Errorf("prompt_tokens = %d, want %d", got.Usage.PromptTokens, sandbox.DefaultUsage.Input)
	}
	if got.Usage.PromptTokensDetails.CachedTokens != sandbox.DefaultUsage.Cached {
		t.Errorf("cached_tokens = %d, want %d",
			got.Usage.PromptTokensDetails.CachedTokens, sandbox.DefaultUsage.Cached)
	}
	if got.Usage.PromptTokensDetails.CachedTokens >= got.Usage.PromptTokens {
		t.Error("the cached count must be a strict subset of the prompt count, " +
			"or the fixture cannot catch an adapter that sums them")
	}
}

func TestStatusFaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		fault          sandbox.Fault
		wantStatus     int
		wantRetryAfter bool
	}{
		{"unauthorized is 401", sandbox.FaultUnauthorized, http.StatusUnauthorized, false},
		{"forbidden is 403", sandbox.FaultForbidden, http.StatusForbidden, false},
		{"bad request is 400", sandbox.FaultBadRequest, http.StatusBadRequest, false},
		{"not found is 404", sandbox.FaultNotFound, http.StatusNotFound, false},
		{"server error is 500", sandbox.FaultServerError, http.StatusInternalServerError, false},
		{"unavailable is 503", sandbox.FaultUnavailable, http.StatusServiceUnavailable, false},
		{"rate limit with Retry-After is 429", sandbox.FaultRateLimitRetryAfter,
			http.StatusTooManyRequests, true},
		{"rate limit without Retry-After is 429", sandbox.FaultRateLimitNoRetryAfter,
			http.StatusTooManyRequests, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := sandbox.New(sandbox.WithFault(tc.fault))
			defer s.Close()

			resp, raw := mustPost(t, s, "any-model")
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if got := resp.Header.Get("Retry-After") != ""; got != tc.wantRetryAfter {
				t.Errorf("Retry-After present = %v, want %v", got, tc.wantRetryAfter)
			}

			// Every failure must still arrive in the provider's own error
			// envelope; an adapter's classifier reads that shape.
			var env struct {
				Error *struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("error body is not JSON: %v\nbody: %s", err, raw)
			}
			if env.Error == nil || env.Error.Message == "" {
				t.Errorf("error envelope missing or empty: %s", raw)
			}
		})
	}
}

// A body the client cannot decode is the failure an in-process fake can never
// produce, so each of these is checked at the byte level rather than through a
// decoder that would hide the difference.
func TestBodyFaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fault       sandbox.Fault
		wantStatus  int
		wantCT      string
		wantReadErr bool
		wantJSONErr bool
	}{
		{
			name:        "malformed JSON arrives whole but does not parse",
			fault:       sandbox.FaultMalformedJSON,
			wantStatus:  http.StatusOK,
			wantCT:      "application/json",
			wantJSONErr: true,
		},
		{
			name:        "wrong content type serves HTML under a 200",
			fault:       sandbox.FaultWrongContentType,
			wantStatus:  http.StatusOK,
			wantCT:      "text/html",
			wantJSONErr: true,
		},
		{
			name:        "truncated body stops short of its Content-Length",
			fault:       sandbox.FaultTruncatedBody,
			wantStatus:  http.StatusOK,
			wantCT:      "application/json",
			wantReadErr: true,
		},
		{
			name:        "disconnect mid-body drops the connection after a chunk",
			fault:       sandbox.FaultDisconnectMidBody,
			wantStatus:  http.StatusOK,
			wantCT:      "application/json",
			wantReadErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := sandbox.New(sandbox.WithFault(tc.fault))
			defer s.Close()

			ex := post(t, s, "any-model")
			if ex.dialErr != nil {
				t.Fatalf("request failed before a response arrived: %v", ex.dialErr)
			}
			if ex.resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", ex.resp.StatusCode, tc.wantStatus)
			}
			if ct := ex.resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, tc.wantCT) {
				t.Errorf("Content-Type = %q, want prefix %q", ct, tc.wantCT)
			}
			if gotErr := ex.readErr != nil; gotErr != tc.wantReadErr {
				t.Errorf("body read error = %v, want an error: %v", ex.readErr, tc.wantReadErr)
			}
			if tc.wantJSONErr {
				var into map[string]any
				if err := json.Unmarshal(ex.body, &into); err == nil {
					t.Errorf("body decoded as JSON but should not have: %s", ex.body)
				}
			}
		})
	}
}

// The reply is where the model, rather than the transport, gets things wrong.
func TestReplyFaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fault      sandbox.Fault
		wantFinish string
		check      func(t *testing.T, raw []byte)
	}{
		{
			name:       "invalid JSON reply keeps a valid envelope",
			fault:      sandbox.FaultInvalidJSONReply,
			wantFinish: "stop",
			check: func(t *testing.T, raw []byte) {
				t.Helper()
				content := contentOf(t, raw)
				var into map[string]any
				if err := json.Unmarshal([]byte(content), &into); err == nil {
					t.Errorf("assistant content parsed as JSON but should not have: %q", content)
				}
			},
		},
		{
			name:       "refusal reports content_filter with null content",
			fault:      sandbox.FaultRefusal,
			wantFinish: "content_filter",
			check: func(t *testing.T, raw []byte) {
				t.Helper()
				if !bytes.Contains(raw, []byte(`"content":null`)) {
					t.Errorf("content must be explicitly null, not absent: %s", raw)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := sandbox.New(sandbox.WithFault(tc.fault))
			defer s.Close()

			resp, raw := mustPost(t, s, "any-model")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 — this fault hides inside a success",
					resp.StatusCode)
			}
			if got := finishOf(t, raw); got != tc.wantFinish {
				t.Errorf("finish_reason = %q, want %q", got, tc.wantFinish)
			}
			tc.check(t, raw)
		})
	}
}

func TestEmptyChoices(t *testing.T) {
	t.Parallel()

	s := sandbox.New(sandbox.WithFault(sandbox.FaultEmptyChoices))
	defer s.Close()

	_, raw := mustPost(t, s, "any-model")
	var got struct {
		Choices []any `json:"choices"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(got.Choices) != 0 {
		t.Errorf("choices = %d, want 0", len(got.Choices))
	}
}

// FaultEchoPrompt is the trap that catches an adapter forwarding a provider's
// message into its own error, so the sandbox has to actually put the prompt
// there.
func TestEchoPromptQuotesTheRequest(t *testing.T) {
	t.Parallel()

	s := sandbox.New(sandbox.WithFault(sandbox.FaultEchoPrompt))
	defer s.Close()

	resp, raw := mustPost(t, s, "any-model")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !bytes.Contains(raw, []byte("hello")) {
		t.Errorf("the error message must quote the prompt back, or it traps nothing: %s", raw)
	}
}

// A model identifier reaches the sandbox through code that knows nothing about
// it, which is the only channel available when the adapter under test offers
// no hook of its own.
func TestFaultSelectedByModelID(t *testing.T) {
	t.Parallel()

	s := sandbox.New()
	defer s.Close()

	resp, _ := mustPost(t, s, sandbox.FaultModel(sandbox.FaultServerError))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 — the model identifier asked for it", resp.StatusCode)
	}

	// An unrecognised fault name must not be mistaken for one.
	resp, _ = mustPost(t, s, sandbox.FaultModelPrefix+"no-such-fault")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for an unknown fault name", resp.StatusCode)
	}
}

func TestEnqueueServesFaultsInOrder(t *testing.T) {
	t.Parallel()

	s := sandbox.New()
	defer s.Close()
	s.Enqueue(sandbox.FaultRateLimitRetryAfter, sandbox.FaultServerError)

	want := []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusOK}
	for i, wantStatus := range want {
		resp, _ := mustPost(t, s, "any-model")
		if resp.StatusCode != wantStatus {
			t.Errorf("request %d: status = %d, want %d", i, resp.StatusCode, wantStatus)
		}
	}
	if got := s.Requests(); got != len(want) {
		t.Errorf("Requests() = %d, want %d", got, len(want))
	}

	// Past the queue, the default applies — and the default can be changed.
	s.SetFault(sandbox.FaultForbidden)
	resp, _ := mustPost(t, s, "any-model")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("after SetFault: status = %d, want 403", resp.StatusCode)
	}
}

func TestAuthorisation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		serverKey  string
		header     string
		wantStatus int
	}{
		{"the configured key is accepted", "secret-key", "Bearer secret-key", http.StatusOK},
		{"a wrong key is rejected", "secret-key", "Bearer wrong-key", http.StatusUnauthorized},
		{"a missing header is rejected", "secret-key", "", http.StatusUnauthorized},
		{"no configured key accepts anything", "", "", http.StatusOK},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := sandbox.New(sandbox.WithAPIKey(tc.serverKey))
			defer s.Close()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
				s.BaseURL()+"/chat/completions",
				strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
			if err != nil {
				t.Fatalf("NewRequest() = %v", err)
			}
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			resp, err := s.Client().Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()         //nolint:errcheck // test cleanup
			raw, _ := io.ReadAll(resp.Body) //nolint:errcheck // status is what matters here

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			// The sandbox must not model a provider that leaks credentials,
			// or every adapter built against it inherits the habit.
			if tc.serverKey != "" && bytes.Contains(raw, []byte(tc.serverKey)) {
				t.Errorf("the sandbox echoed the credential in its response: %s", raw)
			}
		})
	}
}

// A hung request must end when the caller stops waiting, and the server must
// still close afterwards — a sandbox that deadlocks its own Close turns one
// broken test into a suite that never finishes.
func TestHangEndsOnCancellation(t *testing.T) {
	t.Parallel()

	s := sandbox.New(sandbox.WithFault(sandbox.FaultHang))
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.BaseURL()+"/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("NewRequest() = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+sandbox.DefaultAPIKey)

	done := make(chan error, 1)
	go func() {
		resp, err := s.Client().Do(req)
		if resp != nil {
			resp.Body.Close() //nolint:errcheck // test cleanup
		}
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("the hung request returned success after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hung request ignored cancellation")
	}
}

// Closing must release a hung request even when nobody cancelled it.
func TestCloseReleasesAHungRequest(t *testing.T) {
	t.Parallel()

	s := sandbox.New(sandbox.WithFault(sandbox.FaultHang))

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		resp, err := s.Client().Post(s.BaseURL()+"/chat/completions", "application/json",
			strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
		if err == nil && resp != nil {
			resp.Body.Close() //nolint:errcheck // test cleanup
		}
	}()
	<-started

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		s.Close()
	}()

	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close() deadlocked on a hung request")
	}
	<-done
}

func TestRecordsRequestBodies(t *testing.T) {
	t.Parallel()

	s := sandbox.New()
	defer s.Close()

	mustPost(t, s, "first-model")
	mustPost(t, s, "second-model")

	bodies := s.Bodies()
	if len(bodies) != 2 {
		t.Fatalf("Bodies() = %d entries, want 2", len(bodies))
	}
	if !bytes.Contains(bodies[0], []byte("first-model")) {
		t.Errorf("first body did not record the request: %s", bodies[0])
	}
	if !bytes.Contains(s.LastBody(), []byte("second-model")) {
		t.Errorf("LastBody() returned the wrong request: %s", s.LastBody())
	}

	// The copy must be a copy: a caller mutating what it read must not be able
	// to corrupt what the next reader sees.
	bodies[0][0] = 'X'
	if s.Bodies()[0][0] == 'X' {
		t.Error("Bodies() handed out the server's own buffer")
	}
}

// The server is shared by every goroutine an adapter's concurrency test
// starts, so it has to survive that itself.
func TestConcurrentRequests(t *testing.T) {
	t.Parallel()

	s := sandbox.New()
	defer s.Close()

	const n = 16
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			ex := post(t, s, "any-model")
			if ex.dialErr != nil {
				errs <- ex.dialErr
				return
			}
			errs <- ex.readErr
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent request %d failed: %v", i, err)
		}
	}
	if got := s.Requests(); got != n {
		t.Errorf("Requests() = %d, want %d", got, n)
	}
}

func TestUnknownPathAnswersInTheProviderShape(t *testing.T) {
	t.Parallel()

	s := sandbox.New()
	defer s.Close()

	resp, err := s.Client().Get(s.URL() + "/v1/nope")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()         //nolint:errcheck // test cleanup
	raw, _ := io.ReadAll(resp.Body) //nolint:errcheck // shape is what matters here

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	var env struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Error == nil {
		t.Errorf("an unknown path must answer in the provider's error shape, got: %s", raw)
	}
}

// contentOf pulls the assistant content out of a completion body.
func contentOf(t *testing.T, raw []byte) string {
	t.Helper()
	var got struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(got.Choices) == 0 {
		t.Fatalf("response carried no choices: %s", raw)
	}
	return got.Choices[0].Message.Content
}

func finishOf(t *testing.T, raw []byte) string {
	t.Helper()
	var got struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(got.Choices) == 0 {
		t.Fatalf("response carried no choices: %s", raw)
	}
	return got.Choices[0].FinishReason
}
