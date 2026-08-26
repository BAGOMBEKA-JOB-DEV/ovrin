package retry

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// The marker words for the reply block.
//
// They are deliberately not internal/prompt's words. A block of document text
// and a block of model output are different material with different provenance,
// and one instruction describing both under one label would make the retry
// instruction say something untrue about where the bytes came from. They are
// constants so that the instruction, which describes the markers, and the block,
// which carries them, cannot drift apart: both read these and a test asserts the
// instruction contains them.
const (
	beginMarker = "BEGIN UNTRUSTED MODEL REPLY"
	endMarker   = "END UNTRUSTED MODEL REPLY"
)

const (
	// boundaryBytes is the length of the random identifier before hex
	// encoding. 128 bits is chosen so that an attacker who sees one request's
	// identifier learns nothing about the next, and so that guessing is not a
	// strategy — not because a collision argument needs it.
	boundaryBytes = 16

	// boundaryAttempts bounds the search for an identifier that does not
	// already occur in the reply. With a working entropy source one attempt
	// always succeeds, so this exists to make the loop provably terminate on a
	// degenerate reader rather than because a retry is expected.
	boundaryAttempts = 8
)

// boundary returns a random identifier that does not occur in the reply.
//
// The reply is the one thing here an attacker has partial influence over: a
// document that induces the model to echo text chooses some of these bytes. The
// check turns "the reply cannot contain this identifier" from a probability
// into a verified fact for this request, at the cost of one substring search
// over bytes that are about to be copied anyway.
//
// entropy is nil in production, meaning crypto/rand.
func boundary(entropy io.Reader, reply []byte) (string, error) {
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
		if !bytes.Contains(reply, []byte(id)) {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: %d identifiers already occurred in the reply", ErrBoundary, boundaryAttempts)
}

// delimit wraps the reply between the two markers.
//
// The bytes are written verbatim: this package never edits a reply, for the
// same reason internal/prompt never edits document text — a model shown a
// cleaned-up version of its own answer is being asked about a different answer.
// A newline is written on each side regardless of what the reply ends in, so
// both markers always sit alone on a line. A stray blank line is a much smaller
// problem than an end marker a reply could push into the middle of one.
func delimit(id string, reply []byte) string {
	var b strings.Builder
	b.Grow(len(reply) + 2*len(id) + 120)
	fmt.Fprintf(&b, "[%s id=%s]\n", beginMarker, id)
	b.Write(reply)
	fmt.Fprintf(&b, "\n[%s id=%s]", endMarker, id)
	return b.String()
}
