package compare

import "sort"

// Reading is which acquisition path produced a value.
//
// It mirrors ovrin.Reading rather than importing it, as internal/prompt's does
// and for the same reason: the root package imports the pipeline that imports
// this one, and a library may not have an import cycle in it. The string
// values match, so the conversion at the boundary is a cast.
type Reading string

// The readings, matching the root package's constants.
const (
	// ReadingUnknown is the zero value, for a candidate whose origin was not
	// recorded.
	ReadingUnknown Reading = ""

	// ReadingText is a PDF's own text layer.
	ReadingText Reading = "text"

	// ReadingOCR is optical character recognition of a rasterised page.
	ReadingOCR Reading = "ocr"

	// ReadingVision is a multimodal model reading a page image.
	ReadingVision Reading = "vision"
)

// String returns the reading name, or "unknown" for the zero value, so that a
// message never reads "reading=".
func (r Reading) String() string {
	if r == ReadingUnknown {
		return "unknown"
	}
	return string(r)
}

// Candidate is one reading's answer for a field.
//
// It mirrors ovrin.Candidate with the provenance replaced by a confidence: the
// pipeline holds the provenance and this package has no use for it, while
// ranking needs a number and ovrin.Candidate has nowhere to keep one. The
// caller converts.
type Candidate struct {
	// Value is this reading's answer.
	Value any

	// Reading is which reading produced it.
	Reading Reading

	// Confidence is this reading's own confidence in the value, on 0..1, and
	// is what [Rank] orders by. Zero for a caller that has no per-reading
	// score yet, in which case ranking keeps the order it was given.
	Confidence float64
}

// FieldResult is the comparison of every reading of one field.
//
// It reports whether the readings agree and hands back all of them. It never
// discards one: two readings that disagree mean at least one is wrong, and
// which one is not knowable from here (ADR-0014).
type FieldResult struct {
	// Agree reports that every reading that produced a value produced the
	// same value. It is false when Applicable is false.
	Agree bool

	// Applicable reports whether an agreement signal exists at all. It is
	// false for a single reading, for a field no reading found, and for
	// values with no comparison kind. An inapplicable signal is absent, not
	// zero (docs/confidence.md §Signals).
	Applicable bool

	// Kind is the kind the values were compared under.
	Kind Kind

	// Best is the highest-confidence candidate, and is the value a caller who
	// ignores everything else should use — that is what makes this feature
	// cost nothing to not use (ADR-0014). It is the zero Candidate when no
	// reading produced a value.
	//
	// Best is a ranking, not a resolution. When Agree is false the other
	// candidates are still here, the field still needs review, and nothing
	// about picking a value to display settles which reading was right.
	Best Candidate

	// Candidates is every reading that produced a value, ranked, Best first.
	// It is populated whether or not the readings agreed, so a caller can
	// record provenance for an agreed value as easily as for a disputed one.
	Candidates []Candidate

	// Missing is the readings that produced no value, in the order given. A
	// field one reading found and another did not is not a disagreement about
	// its value; see [absent].
	Missing []Reading

	// Fallback reports that at least one pair could not be read as Kind and
	// was compared as text.
	Fallback bool

	// Reason says why, and never contains a value (docs/rules.md §7.5). The
	// values are on Candidates, where the caller can decide what to do with
	// them.
	Reason string
}

// Signal returns the agreement signal for the field, and whether there is one.
func (f FieldResult) Signal() (float64, bool) {
	if !f.Applicable {
		return 0, false
	}
	if f.Agree {
		return Agree, true
	}
	return Disagree, true
}

// Field compares every reading of one field and reports whether they agree.
//
// It is the entry point the pipeline uses: give it the readings, get back the
// agreement signal, the ranked candidates and the readings that found nothing.
// It resolves nothing — [FieldResult.Best] is the higher-confidence answer for
// a caller who needs one value to show, not a verdict on which reading was
// right (docs/rules.md §8.4).
//
// Readings agree when every pair of them agrees, rather than when each matches
// the first. Some comparisons are deliberately generous — an ambiguous date
// matches either reading of itself — and generosity is not transitive, so
// checking against the first reading alone would make the answer depend on the
// order the readings arrived in.
func Field(kind Kind, readings []Candidate, opts ...Option) FieldResult {
	o := newOptions(opts)
	out := FieldResult{Kind: kind}

	present := make([]Candidate, 0, len(readings))
	for _, c := range readings {
		if absent(c.Value) {
			out.Missing = append(out.Missing, c.Reading)
			continue
		}
		present = append(present, c)
	}
	out.Candidates = Rank(present)
	if len(out.Candidates) > 0 {
		out.Best = out.Candidates[0]
	}
	switch len(present) {
	case 0:
		out.Reason = ReasonNone
		return out
	case 1:
		out.Reason = ReasonSingle
		if len(out.Missing) > 0 {
			out.Reason = ReasonAbsent
		}
		return out
	}

	for i := 0; i < len(present); i++ {
		for j := i + 1; j < len(present); j++ {
			r := compare(present[i].Value, present[j].Value, kind, o, 0)
			if !r.Applicable {
				if out.Reason == "" {
					out.Reason = r.Reason
				}
				continue
			}
			out.Applicable = true
			out.Kind = r.Kind
			out.Fallback = out.Fallback || r.Fallback
			if !r.Equal {
				out.Agree = false
				out.Reason = ReasonDisagree
				return out
			}
		}
	}
	if !out.Applicable {
		return out
	}
	out.Agree = true
	out.Reason = ""
	if out.Fallback {
		out.Reason = ReasonFallback
	}
	return out
}

// Rank orders candidates by confidence, highest first, and returns a new
// slice.
//
// Stable, and that is the whole point of it being a named function: candidates
// with equal confidence keep the order the caller supplied. No fixed
// preference between readings is defensible — OCR wins on printed amounts, a
// model wins on layout-dependent assignment (ADR-0014) — so this package will
// not invent one by sorting on the reading name, and it will not shuffle the
// caller's order either, because a review queue whose top candidate changes
// between runs of the same document is not reviewable.
//
// It does not modify the input.
func Rank(candidates []Candidate) []Candidate {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]Candidate, len(candidates))
	copy(out, candidates)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Confidence > out[j].Confidence
	})
	return out
}
