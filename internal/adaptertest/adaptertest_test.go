package adaptertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

const (
	fakeAPIKey       = "sk-adaptertest-secret-key"
	fakeRequestModel = "model-that-was-asked-for"
	fakeServedModel  = "model-that-actually-served-it"

	// fakeReply carries redundant whitespace on purpose. The suite compares
	// byte-for-byte, so a compact fixture could not tell a passthrough from an
	// adapter that unmarshalled and re-marshalled the reply.
	fakeReply = `{ "ovrin_test_property": "found" }`
)

// fakeSuccessBody is a vendor-shaped completion carrying fakeReply.
func fakeSuccessBody(t *testing.T) string {
	t.Helper()
	content, err := json.Marshal(fakeReply)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	return `{"id":"resp-1","model":"` + fakeServedModel + `",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":` +
		string(content) + `},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":137,"completion_tokens":42}}`
}

// The contract suite has to pass an adapter that obeys the contract, or its
// failures mean nothing. This is that adapter, and this is the whole suite.
func TestModelSuiteAcceptsACompliantAdapter(t *testing.T) {
	success := fakeSuccessBody(t)

	Model(t, ModelSuite{
		Name: "fake",
		New: func(baseURL string) ovrin.Model {
			return newFakeModel(baseURL, fakeAPIKey, fakeRequestModel, true)
		},
		NewWithoutVision: func(baseURL string) ovrin.Model {
			return newFakeModel(baseURL, fakeAPIKey, fakeRequestModel, false)
		},
		APIKey:       fakeAPIKey,
		RequestModel: fakeRequestModel,
		WantModel:    fakeServedModel,
		ServedModel: func(raw any) string {
			w, ok := raw.(*wireResponse)
			if !ok {
				return ""
			}
			return w.Model
		},
		SuccessBody: success,
		WantJSON:    fakeReply,
		WantUsage:   ovrin.Usage{InputTokens: 137, OutputTokens: 42},
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

// separation guards a security boundary, so it has to be able to fail. A check
// that only ever passes is decoration.
func TestSeparationDetectsConcatenation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want separationReport
	}{
		{
			name: "two roles keep the canaries in different strings",
			body: `{"messages":[` +
				`{"role":"system","content":"do the thing ` + InstructionCanary + `"},` +
				`{"role":"user","content":"` + ContentCanary + `"}]}`,
			want: separationReport{instruction: true, content: true},
		},
		{
			name: "one concatenated prompt puts both in one string",
			body: `{"prompt":"do the thing ` + InstructionCanary +
				`\n\n` + ContentCanary + `"}`,
			want: separationReport{both: true},
		},
		{
			name: "a nested multipart body still separates them",
			body: `{"messages":[` +
				`{"role":"system","content":"` + InstructionCanary + `"},` +
				`{"role":"user","content":[{"type":"text","text":"` + ContentCanary + `"}]}]}`,
			want: separationReport{instruction: true, content: true},
		},
		{
			name: "an instruction that never left is visible as such",
			body: `{"messages":[{"role":"user","content":"` + ContentCanary + `"}]}`,
			want: separationReport{content: true},
		},
		{
			name: "content that never left is visible as such",
			body: `{"messages":[{"role":"system","content":"` + InstructionCanary + `"}]}`,
			want: separationReport{instruction: true},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := separation([]byte(tc.body))
			if err != nil {
				t.Fatalf("separation() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("separation() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSeparationRejectsNonJSON(t *testing.T) {
	t.Parallel()

	if _, err := separation([]byte("not json at all")); err == nil {
		t.Error("separation() error = nil for a body that is not JSON")
	}
}

// The privacy check is the one that catches a leak nothing else catches, so it
// is tested against an error that actually leaks.
func TestLeaksFindsWhatItMustFind(t *testing.T) {
	t.Parallel()

	compliant := &ovrin.Error{
		Op:       ovrin.OpGenerate,
		Provider: "fake",
		Kind:     ovrin.ErrBadRequest,
		Message:  "the provider returned http 400",
	}
	leakyMessage := &ovrin.Error{
		Op:       ovrin.OpGenerate,
		Provider: "fake",
		Kind:     ovrin.ErrBadRequest,
		Message:  "the provider rejected: " + ContentCanary,
	}

	tests := []struct {
		name      string
		err       error
		needle    string
		wantLeak  bool
		wantWhere string
	}{
		{
			name:   "an adapter-authored message leaks nothing",
			err:    compliant,
			needle: ContentCanary,
		},
		{
			name:      "a forwarded provider message leaks the document",
			err:       leakyMessage,
			needle:    ContentCanary,
			wantLeak:  true,
			wantWhere: "the error message",
		},
		{
			name: "a leak in a wrapper is found through the rendered string",
			err: fmt.Errorf("calling the provider: %w",
				errors.New("rejected "+ContentCanary)),
			needle:    ContentCanary,
			wantLeak:  true,
			wantWhere: "the error message",
		},
		{
			name:   "an empty needle is never a leak",
			err:    leakyMessage,
			needle: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			where, leaked := leaks(tc.err, tc.needle)
			if leaked != tc.wantLeak {
				t.Errorf("leaks() = %v, want %v", leaked, tc.wantLeak)
			}
			if tc.wantWhere != "" && where != tc.wantWhere {
				t.Errorf("leaks() where = %q, want %q", where, tc.wantWhere)
			}
		})
	}
}

// A cause attached only to the struct, never to the message, must stay
// invisible to the privacy check — otherwise an adapter cannot keep
// errors.Is(err, context.Canceled) working without failing the suite.
func TestLeaksIgnoresAnUnprintedCause(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("%w: %w", &ovrin.Error{
		Op:       ovrin.OpGenerate,
		Provider: "fake",
		Message:  "the context ended before the provider replied",
	}, context.Canceled)

	if where, leaked := leaks(err, ContentCanary); leaked {
		t.Errorf("leaks() reported a leak in %s for an error carrying none", where)
	}
	if !errors.Is(err, context.Canceled) {
		t.Error("the wrapping must keep errors.Is(err, context.Canceled) working")
	}
	var e *ovrin.Error
	if !errors.As(err, &e) {
		t.Error("the wrapping must keep errors.As(err, &*ovrin.Error) working")
	}
}

func TestStringLeaves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want []string
	}{
		{"a flat object yields its values", `{"a":"one","b":"two"}`, []string{"one", "two"}},
		{"keys are not values", `{"onlykey":1}`, nil},
		{"arrays are walked", `["one",["two"]]`, []string{"one", "two"}},
		{"nesting is walked", `{"a":{"b":{"c":"deep"}}}`, []string{"deep"}},
		{"non-strings are skipped", `{"a":1,"b":true,"c":null,"d":"kept"}`, []string{"kept"}},
		{"object keys are visited in sorted order", `{"b":"second","a":"first"}`,
			[]string{"first", "second"}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var decoded any
			if err := json.Unmarshal([]byte(tc.json), &decoded); err != nil {
				t.Fatalf("Unmarshal() = %v", err)
			}
			got := stringLeaves(decoded)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("stringLeaves() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "whitespace is removed", in: `{ "a": 1 }`, want: `{"a":1}`},
		{name: "already compact is unchanged", in: `{"a":1}`, want: `{"a":1}`},
		{name: "invalid json is an error", in: `{`, wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := compact(tc.in)
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("compact() error = %v, want an error: %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("compact() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The fixture guard is the difference between a suite that proves something
// and one that cannot. Its own predicate is checked here, because valid()
// itself can only report through *testing.T.
func TestFixtureGuardRejectsACompactWantJSON(t *testing.T) {
	t.Parallel()

	compacted, err := compact(fakeReply)
	if err != nil {
		t.Fatalf("compact() = %v", err)
	}
	if compacted == fakeReply {
		t.Error("the fixture reply is already compact, so the suite's byte-for-byte " +
			"comparison could not detect an adapter that re-marshalled the reply")
	}
}
