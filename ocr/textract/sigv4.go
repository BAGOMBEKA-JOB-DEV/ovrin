package textract

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The constants of AWS Signature Version 4.
//
// They are spelled out rather than imported because the whole of SigV4 for a
// JSON API is the eighty lines below, and taking the AWS SDK for them would
// put dozens of modules into the dependency tree of everyone who imports this
// adapter — the same argument ADR-0009 makes about the core, one level down.
const (
	signingAlgorithm  = "AWS4-HMAC-SHA256"
	signingService    = "textract"
	signingTerminator = "aws4_request"

	// amzDateFormat and dateStampFormat are the two forms of the same instant
	// SigV4 asks for: one in the request, one in the credential scope.
	amzDateFormat   = "20060102T150405Z"
	dateStampFormat = "20060102"
)

// Credentials are an AWS access key, taken explicitly.
//
// It is a struct rather than three arguments because a session token is
// optional and a positional empty string for it is the kind of call nobody can
// read. Nothing here is ever read from the environment: a library that reads
// the environment is how a program ends up talking to the wrong account
// (rule §6.4).
type Credentials struct {
	// AccessKeyID and SecretAccessKey are the long-lived or temporary key
	// pair. The secret is used to derive a signing key and is never sent.
	AccessKeyID     string
	SecretAccessKey string

	// SessionToken accompanies temporary credentials — those from
	// AssumeRole, an instance profile, or IAM Roles for Service Accounts. It
	// is empty for a long-lived user key.
	SessionToken string
}

// valid reports whether the credentials can sign anything at all.
func (c Credentials) valid() bool {
	return c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// signedHeaderNames are the headers this package signs, lower-cased.
//
// SigV4 lets a signer choose which headers to cover, and covering exactly the
// ones this package sets is what keeps a signature valid through a proxy that
// adds its own. Host, the date and the target are the minimum AWS requires;
// the content type and the session token are signed because Textract rejects a
// signature that omits a header it can see.
var signedHeaderNames = []string{
	"content-type",
	"host",
	"x-amz-date",
	"x-amz-security-token",
	"x-amz-target",
}

// signV4 signs req in place with AWS Signature Version 4.
//
// payload must be exactly the bytes of the request body, because the signature
// covers their hash: signing a body that is then re-encoded produces a request
// AWS rejects with an error that says nothing about why.
//
// service is a parameter, rather than the constant it always is in this
// package, so that the published AWS test vectors — which sign a service named
// "service" — can be reproduced exactly. A signer verified only against itself
// is a signer nobody has checked.
//
// now is a parameter for the same reason: a signature is only reproducible if
// the instant is. Callers pass time.Now().
func signV4(req *http.Request, payload []byte, creds Credentials, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format(amzDateFormat)
	dateStamp := now.Format(dateStampFormat)

	payloadHash := hexSHA256(payload)
	req.Header.Set("X-Amz-Date", amzDate)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	var canonicalHeaders strings.Builder
	signed := make([]string, 0, len(signedHeaderNames))
	for _, name := range signedHeaderNames {
		value := req.Header.Get(name)
		if name == "host" {
			value = host
		}
		if value == "" {
			continue
		}
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteString(":")
		// SigV4 folds runs of whitespace in a header value. Nothing this
		// package sends contains any, but a signature that is right only for
		// the values it happens to send now is a trap for the next change.
		canonicalHeaders.WriteString(strings.Join(strings.Fields(value), " "))
		canonicalHeaders.WriteString("\n")
		signed = append(signed, name)
	}
	// The names are already in the order SigV4 wants, but sorting is the
	// contract rather than the order of a literal above.
	sort.Strings(signed)
	signedHeaders := strings.Join(signed, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalPath(req.URL.EscapedPath()),
		req.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, signingTerminator}, "/")
	stringToSign := strings.Join([]string{
		signingAlgorithm,
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(
		signingKey(creds.SecretAccessKey, dateStamp, region, service),
		[]byte(stringToSign)))

	req.Header.Set("Authorization", signingAlgorithm+
		" Credential="+creds.AccessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+
		", Signature="+signature)
}

// signingKey derives the key a signature is computed with.
//
// The four-step chain is what scopes a signature to one day, one region and
// one service, so a signature that leaks cannot be replayed against another of
// either. The secret itself never leaves this function.
func signingKey(secret, dateStamp, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	k = hmacSHA256(k, []byte(region))
	k = hmacSHA256(k, []byte(service))
	return hmacSHA256(k, []byte(signingTerminator))
}

// canonicalPath returns the path SigV4 signs.
//
// Textract serves every operation from "/" — the operation travels in the
// X-Amz-Target header — so the double-encoding rule AWS applies to path
// segments of other services never has anything to act on here. An empty path,
// which is what a URL with no trailing slash parses to, is "/".
func canonicalPath(escaped string) string {
	if escaped == "" {
		return "/"
	}
	return escaped
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	// hash.Hash.Write never returns an error, which is why the interface
	// documents it and why ignoring it here is not a discarded failure.
	h.Write(data) //nolint:errcheck // documented never to fail
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
