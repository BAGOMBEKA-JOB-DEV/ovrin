# ADR-0016: `Explain` returns a data structure, not formatted text

**Status:** Accepted · **Date:** 2026-08-26

## Context

The original design sketched an explainability call producing something like:

```text
Field: Amount
Source: Page 1
Extraction: OCR → AI
OCR Confidence: 98%
AI Confidence: 91%
Validation: Passed
Final Confidence: 94%
```

That is a good thing for a developer to see at a terminal. It is a bad thing
for a library to return, because the moment it is a string, every consumer that
is not a terminal has to parse it back — and the consumers are review queues,
audit logs, dashboards, JSON APIs and support tools, none of which want text.

A returned string is also an API surface with no type safety. Adding a line
breaks every regex somebody wrote against it, and nothing catches that at
compile time.

## Decision

`Explain` returns a value.

```go
func (r *Result[T]) Explain(field string) (*Explanation, bool)

type Explanation struct {
    Field      string
    Value      any
    Found      bool
    Confidence float64
    Signals    []Signal        // every input, with weight and note
    Provenance []Provenance    // where it came from
    Candidates []Candidate     // competing readings, if any
    Validation []RuleResult    // each rule, whether it passed, and why not
    Reasons    []ReviewReason
}
```

The terminal rendering still exists — it is genuinely useful — but as a
`String()` method on `Explanation`, layered over the data:

```go
if e, ok := res.Explain("total"); ok {
    fmt.Println(e)               // the human rendering
    metrics.Record(e.Signals)    // the machine rendering
}
```

`Explanation` is assembled from data the pipeline already recorded for
[ADR-0013](0013-multi-signal-confidence.md) and
[ADR-0015](0015-provenance.md). It is a view, not a second source of truth, so
it cannot disagree with the `Result` it came from.

The `String()` output is explicitly **not** part of the compatibility promise
and says so in its doc comment. Anyone parsing it has taken a dependency we
will break.

## Consequences

**Good.** Review queues, audit stores and dashboards consume it directly.
`Explanation` marshals to JSON without any work. Adding a signal type does not
break consumers. The human rendering is still one `fmt.Println` away, so
nothing is lost for the terminal case.

**Bad.** It is more to type than a string return, and the quickstart example is
correspondingly less punchy. `Explanation` duplicates fields that are already
on `FieldResult`, which is a second place for them to drift — mitigated by
constructing it from `FieldResult` rather than alongside it, but not
eliminated. And `Value any` forces a type assertion in any consumer that wants
the typed value, which is a wart in a library whose entire premise is type
safety; the typed value is available on `Result.Data`, so this is a
convenience field that is deliberately untyped.

## Alternatives considered

- **Return a formatted string.** Rejected: every non-terminal consumer parses
  it back, and the format becomes an untyped, untested API.
- **Return `map[string]any`.** Rejected: no type safety, no discoverability, no
  godoc.
- **Write to an `io.Writer`.** Rejected: solves formatting flexibility and
  still gives machine consumers nothing.
- **Put `Explanation` on `FieldResult` directly and drop `Explain`.**
  Rejected: it would be built for every field on every extraction whether or
  not anyone looks at it, and most callers never do.
