package prompt

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// The marker words. They are constants so that the instruction, which
// describes the markers, and the content, which carries them, cannot drift
// apart: both read these and a test asserts the instruction contains them.
const (
	beginMarker = "BEGIN UNTRUSTED DOCUMENT CONTENT"
	endMarker   = "END UNTRUSTED DOCUMENT CONTENT"
)

const (
	// boundaryBytes is the length of the random identifier before hex
	// encoding. 128 bits is far more than a collision argument needs; it is
	// chosen so that an attacker who sees one request's identifier learns
	// nothing about the next, and so that guessing is not a strategy.
	boundaryBytes = 16

	// boundaryAttempts bounds the search for an identifier that does not
	// already occur in the content. With a working entropy source one attempt
	// always succeeds, so this exists to make the loop provably terminate on a
	// degenerate reader rather than because a retry is expected.
	boundaryAttempts = 8
)

// boundary returns a random identifier that appears in none of the pages.
//
// The check is what turns "a document cannot contain this identifier" from a
// probability into a verified fact for this request. It costs one substring
// search per page over content that is about to be copied anyway.
//
// entropy is nil in production, meaning crypto/rand. It is a parameter so
// tests can pin the identifier; it is never a package variable, because a
// package variable is global state two clients could observe (docs/rules.md
// §5.5).
func boundary(entropy io.Reader, pages []PageContent) (string, error) {
	src := entropy
	if src == nil {
		src = rand.Reader
	}
	buf := make([]byte, boundaryBytes)
	for attempt := 0; attempt < boundaryAttempts; attempt++ {
		if _, err := io.ReadFull(src, buf); err != nil {
			return "", fmt.Errorf("%w: %w", ErrBoundary, err)
		}
		id := hex.EncodeToString(buf)
		if !occurs(id, pages) {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: %d identifiers already occurred in the content", ErrBoundary, boundaryAttempts)
}

// occurs reports whether id appears in any page's text.
//
// Only the identifier is searched for, not the marker words. A document
// containing the words is harmless — without the identifier the words are
// ordinary text, and the instruction says so — and searching for them would
// turn a legitimate document that quotes this library's documentation into a
// failed extraction.
func occurs(id string, pages []PageContent) bool {
	for _, p := range pages {
		if strings.Contains(p.Text, id) {
			return true
		}
	}
	return false
}
