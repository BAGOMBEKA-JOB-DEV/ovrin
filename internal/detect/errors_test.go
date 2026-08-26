package detect

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fromTheFixedVocabulary reports whether msg is one of the messages this
// package builds from literals.
//
// It is the mechanical form of the rule that no error may carry document
// content (docs/rules.md §2.5, §7.5): every message here begins with a phrase
// chosen in the source, so a message that begins with something else is a
// message that got its opening from the document.
func fromTheFixedVocabulary(msg string) bool {
	prefixes := []string{
		ErrUnsupportedFormat.Error(),
		ErrEncrypted.Error(),
	}
	for _, limit := range allLimits {
		prefixes = append(prefixes, limit.String()+" limit exceeded")
	}
	prefixes = append(prefixes, LimitUnknown.String()+" limit exceeded")
	for _, p := range prefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

func TestSentinelTextIsLowercaseAndUnpunctuated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "unsupported format", err: ErrUnsupportedFormat},
		{name: "limit exceeded", err: ErrLimitExceeded},
		{name: "encrypted", err: ErrEncrypted},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg := tc.err.Error()
			if msg == "" {
				t.Fatal("empty message")
			}
			if strings.ToLower(msg[:1]) != msg[:1] {
				t.Errorf("%q does not start lowercase", msg)
			}
			if strings.HasSuffix(msg, ".") {
				t.Errorf("%q is punctuated", msg)
			}
			// The "ovrin: " prefix belongs to the package boundary. Adding it
			// here would print it twice once the root wraps this.
			if strings.HasPrefix(msg, "ovrin:") {
				t.Errorf("%q carries the prefix the root package adds", msg)
			}
		})
	}
}

func TestErrorsCarryNoDocumentContent(t *testing.T) {
	t.Parallel()

	// A string that could only have come from the document. If it reaches an
	// error, it reaches a log line, and from there five systems nobody
	// audited.
	const marker = "SECRET-PATIENT-ID-42"

	corruptZip := buildZip(t, zipEntry{name: marker + ".txt", body: marker})
	corruptZip = corruptZip[:len(corruptZip)/2]

	tests := []struct {
		name string
		data []byte
	}{
		{name: "an unrecognised format whose bytes are the marker", data: []byte(marker)},
		{
			name: "a zip whose entry names are the marker",
			data: buildZip(t, zipEntry{name: marker + ".txt", body: marker}),
		},
		{name: "a zip that is corrupt and whose entry names are the marker", data: corruptZip},
		{
			name: "an ooxml container whose content types name the marker",
			data: buildZip(t,
				zipEntry{name: contentTypesName, body: "<Types>" + marker + "</Types>"},
				zipEntry{name: marker + ".xml", body: marker},
			),
		},
		{
			name: "an encrypted zip whose entry names are the marker",
			data: buildZip(t, zipEntry{name: marker + ".xml", body: marker, flags: zipEncryptedFlag}),
		},
		{
			name: "an encrypted pdf whose trailer names the marker",
			data: []byte("%PDF-1.4\ntrailer\n<</Root 1 0 R/Encrypt 9 0 R/Title(" + marker + ")>>\n%%EOF\n"),
		},
		{
			name: "a document over the source ceiling whose bytes are the marker",
			data: []byte(strings.Repeat(marker, 100)),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lim := DefaultLimits()
			lim.MaxSourceBytes = 64
			_, err := Detect(context.Background(), Bytes(tc.data), lim)
			if err == nil {
				t.Skip("no error to inspect")
			}
			msg := err.Error()
			if strings.Contains(msg, marker) {
				t.Errorf("error %q carries document content", msg)
			}
			if !fromTheFixedVocabulary(msg) {
				t.Errorf("error %q is not built from this package's fixed vocabulary", msg)
			}
		})
	}
}

func TestLimitErrorUnwrapsToTheSentinel(t *testing.T) {
	t.Parallel()

	err := exceeded(LimitPages, 1000)

	if !errors.Is(err, ErrLimitExceeded) {
		t.Error("a limit error does not answer errors.Is against ErrLimitExceeded")
	}
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatal("a limit error does not answer errors.As against *LimitError")
	}
	if le.Limit != LimitPages || le.Max != 1000 {
		t.Errorf("got %s at %d, want %s at 1000", le.Limit, le.Max, LimitPages)
	}
	if errors.Is(err, ErrUnsupportedFormat) || errors.Is(err, ErrEncrypted) {
		t.Error("a limit error answers to a sentinel that is not its own")
	}
}
