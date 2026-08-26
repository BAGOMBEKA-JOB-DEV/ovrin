# ADR-0028: `Reading` and `ReadingMode` are different types

**Status:** Accepted · **Date:** 2026-08-26 · **Amends** [ADR-0012](0012-text-first-ocr-on-demand.md), [ADR-0015](0015-provenance.md)

## Context

`Reading` is used for two different jobs, and one value fits only one of them.

As a **record of what happened**, it appears on `Provenance.Reading` and
`Candidate.Reading`, and every type comment in the documentation enumerates
exactly three values: `ReadingText`, `ReadingOCR`, `ReadingVision`. That is
correct — a value was read by exactly one of them.

As a **request for what should happen**, it is the argument to `WithReading`,
and there the documentation uses a fourth value, `ReadingBoth`, plus an implied
adaptive default that has no name at all.

Putting all of them in one type makes `Provenance{Reading: ReadingBoth}`
representable, which is meaningless — a value cannot have been read by two
readings at once; that is what `Candidates` is for. It also leaves the default
unnameable, so the most common configuration is the one you cannot write down.

## Decision

Two types.

```go
// Reading is how a value was actually read. It appears on Provenance
// and Candidate, and describes the past.
type Reading string

const (
    ReadingUnknown Reading = ""
    ReadingText    Reading = "text"
    ReadingOCR     Reading = "ocr"
    ReadingVision  Reading = "vision"
)

// ReadingMode selects how a document should be read. It is the argument
// to WithReading, and describes an intention.
type ReadingMode string

const (
    ReadingAuto   ReadingMode = "auto"    // the default: staged, per ADR-0012
    ModeText      ReadingMode = "text"
    ModeOCR       ReadingMode = "ocr"
    ModeVision    ReadingMode = "vision"
    ModeBoth      ReadingMode = "both"    // two readings, per ADR-0014
)
```

`ReadingAuto` is the zero-value-adjacent default and is named, so the adaptive
behaviour ADR-0012 specifies can be written down and asked for explicitly.
`ReadingUnknown` is the unknown member rule
[§1.9](../rules.md#1-public-api) requires.

Existing usage changes: `WithReading(ovrin.ReadingBoth)` becomes
`WithReading(ovrin.ModeBoth)`, and `WithReading(ovrin.ReadingVision)` becomes
`WithReading(ovrin.ModeVision)`.

## Consequences

**Good.** `Provenance{Reading: ModeBoth}` does not compile, so the meaningless
state is unrepresentable rather than merely discouraged. The default acquisition
strategy has a name for the first time, which means it can be requested,
documented and tested. Each type's constant set is short and complete, so the
type comments that enumerate them are no longer lying by omission.

**Bad.** Two types where the documentation had one, with confusingly similar
names — `ReadingText` and `ModeText` are one letter apart in concept and eight
in spelling, and somebody will reach for the wrong one. The `Reading` prefix on
one set and `Mode` on the other is inconsistent as naming goes; the alternative,
prefixing both fully, produces `ReadingModeBoth`, which is worse to type. And it
is two enums to keep in step: adding a reading means touching both.

## Alternatives considered

- **One type with four members.** Rejected: makes `Provenance{Reading:
  ReadingBoth}` representable and leaves the default unnameable.
- **One type, with `ReadingBoth` documented as invalid on `Provenance`.**
  Rejected: a comment is not a type. Rule
  [§8.4](../rules.md#8-confidence-and-provenance) turns disagreement into data
  precisely so it does not depend on discipline; the same reasoning applies
  here.
- **`WithReading` takes a variadic `...Reading`** — `WithReading(ReadingOCR,
  ReadingVision)` for two readings. Rejected: it reads well and it makes
  `WithReading()` with no arguments meaningless, three readings silently
  accepted when only two are implemented, and the adaptive default still
  unnameable.
- **Full prefixes on both** — `ReadingModeAuto`, `ReadingModeBoth`. Rejected as
  noise at every call site, for a distinction the type checker already enforces.
