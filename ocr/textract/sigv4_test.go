package textract

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The credential AWS publishes its Signature Version 4 test vectors with. It
// is a documented example, not a key.
const (
	vectorAccessKey = "AKIDEXAMPLE"
	vectorSecret    = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
)

// A signer verified only against itself is a signer nobody has checked: it
// would agree with its own bug for ever. These are AWS's own published
// examples, and reproducing them is the only evidence available offline that a
// request this package signs is one Textract will accept.
func TestSigningKeyMatchesTheAWSExample(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                      string
		secret, date, region, svc string
		want                      string
	}{
		{
			// From "Examples of the complete Version 4 signing process" in the
			// AWS general reference.
			name:   "the iam example from the signing walkthrough",
			secret: vectorSecret, date: "20120215", region: "us-east-1", svc: "iam",
			want: "f4780e2d9f65fa895f9c67b32ce1baf0b0d8a43505a000a1a9e090d414db404d",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := hex.EncodeToString(signingKey(tc.secret, tc.date, tc.region, tc.svc))
			if got != tc.want {
				t.Errorf("signingKey() = %s, want %s", got, tc.want)
			}
		})
	}
}

// The whole canonicalisation, end to end, against the aws-sig-v4-test-suite
// case every implementation starts from. A mistake anywhere in the canonical
// request, the credential scope or the signed header list changes this
// signature.
func TestSignV4MatchesTheAWSTestSuite(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	when := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

	signV4(req, nil, Credentials{
		AccessKeyID:     vectorAccessKey,
		SecretAccessKey: vectorSecret,
	}, "us-east-1", "service", when)

	const want = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"

	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization =\n%s\nwant\n%s", got, want)
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
		t.Errorf("X-Amz-Date = %q, want %q", got, "20150830T123600Z")
	}
}

// A session token is part of the signature, not an extra header bolted on
// beside it: temporary credentials whose token is unsigned are rejected, and
// the rejection says nothing about why.
func TestSignV4CoversTheSessionToken(t *testing.T) {
	t.Parallel()

	sign := func(token string) (auth, signed string) {
		req, err := http.NewRequest(http.MethodPost, "https://textract.eu-west-1.amazonaws.com/",
			strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		req.Header.Set("X-Amz-Target", targetDetect)
		signV4(req, []byte("{}"), Credentials{
			AccessKeyID:     vectorAccessKey,
			SecretAccessKey: vectorSecret,
			SessionToken:    token,
		}, "eu-west-1", signingService, time.Unix(0, 0))
		return req.Header.Get("Authorization"), req.Header.Get("X-Amz-Security-Token")
	}

	withToken, header := sign("FQoGZXIvYXdzEXAMPLE")
	withoutToken, empty := sign("")

	if header != "FQoGZXIvYXdzEXAMPLE" {
		t.Errorf("X-Amz-Security-Token = %q, want the token it was given", header)
	}
	if empty != "" {
		t.Errorf("X-Amz-Security-Token = %q for credentials with no token", empty)
	}
	if !strings.Contains(withToken, "x-amz-security-token") {
		t.Errorf("the session token is not in the signed header list: %s", withToken)
	}
	if withToken == withoutToken {
		t.Error("the signature is the same with and without a session token, so the " +
			"token is not covered by it")
	}
	for _, auth := range []string{withToken, withoutToken} {
		if strings.Contains(auth, vectorSecret) {
			t.Error("the secret access key appears in the Authorization header")
		}
	}
}

// The canonical path is what SigV4 signs, and a base URL written without a
// trailing slash parses to an empty one.
func TestCanonicalPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"an empty path is the root", "", "/"},
		{"the root is itself", "/", "/"},
		{"a path is left alone", "/v1/detect", "/v1/detect"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := canonicalPath(tc.in); got != tc.want {
				t.Errorf("canonicalPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
