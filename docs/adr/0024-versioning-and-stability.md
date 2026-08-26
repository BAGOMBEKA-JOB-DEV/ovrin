# ADR-0024: Pre-v1 stability policy and per-module versioning

**Status:** Accepted · **Date:** 2026-08-26

## Context

Ovrin is a multi-module repository: a core, and adapter modules for models, OCR
and rendering, plus `otel`. They mature at different rates. The core's `Result`
shape may need to change while the Tesseract adapter is finished; the Google
OCR adapter may churn because Google's SDK does while the core is stable.

Go's module system versions each module independently and derives the version
from a git tag whose prefix is the module's subdirectory. That mechanism
already fits; the decision is what to promise.

The temptation is to reach v1.0 quickly because it signals seriousness. It is
the wrong instinct here. A v1.0 promises no incompatible changes, and several
of ovrin's central shapes — `FieldResult`, the confidence signal set, the
`Model` seam — are explicitly provisional. Confidence weights are uncalibrated
by their own ADR. Promising stability over a design that has not met real
documents converts every discovered mistake into either a v2 or a permanent
wart.

## Decision

**Ovrin is pre-v1 and stays there until the design has been used on real
documents by people who are not the maintainer.**

Before v1.0:

- Breaking changes may land in minor releases. Every one appears in
  [`CHANGELOG.md`](../../CHANGELOG.md) with a migration note
  (rule [§1.2](../rules.md#1-public-api)).
- The README's status section says plainly that the API is not frozen.
- Deprecation is preferred where it costs little: mark, keep working for one
  minor release, then remove.

**Each module versions independently**, tagged with its path prefix:

```text
v0.2.0                    the core
model/skyl/v0.1.0
ocr/tesseract/v0.1.0
render/pdfium/v0.1.0
otel/v0.1.0
```

An adapter depends on a specific core version. The core never depends on an
adapter — structurally, so it cannot happen by accident.

**Each module declares its own Go floor**, and CI builds every module at both
its floor and the newest release
(rule [§11.2](../rules.md#11-formatting-and-tooling)). An adapter forced higher
by a vendor SDK does not drag the core up with it.

**`internal/` is not API.** Anything there may change without notice, which is
the entire reason [ADR-0002](0002-flat-package-layout.md) puts most of the
implementation in it.

**The route to v1.0** is four conditions, all of which are about evidence
rather than time:

1. The evaluation corpus has real documents in every category and reports have
   been committed across at least two provider generations.
2. Confidence weights are calibrated, with published expected calibration error
   ([ADR-0013](0013-multi-signal-confidence.md)).
3. At least one production deployment that is not the maintainer's, and its
   feedback incorporated.
4. No known API change we would want to make.

Until all four hold, ovrin stays on v0. Reaching them is expected to take
longer than it sounds.

## Consequences

**Good.** Mistakes discovered against real documents can be fixed rather than
frozen. Adapters ship on their own schedule. Users get an honest signal about
maturity instead of a v1 that means nothing. And the conditions for v1 are
written down, so it is a decision against criteria rather than a mood.

**Bad.** v0 deters adoption — some organisations will not take a v0 dependency
regardless of quality, and those are disproportionately the government and
banking users this library targets. Breaking changes in minor releases mean
users must read the changelog before upgrading, and most will not. Five modules
is five release processes for one maintainer, and versions will drift out of
step in confusing ways. And a v1 gated on external production use is gated on
something the maintainer does not control.

## Alternatives considered

- **Ship v1.0 early to signal seriousness.** Rejected: it promises stability
  over a design whose own ADRs describe parts of it as provisional. A v1 that
  is followed by v2 six months later signals less than an honest v0.
- **One version for the whole repository.** Rejected: forces a release of every
  module for a change to one, and every user's `go.sum` churns for changes that
  do not affect them.
- **Separate repositories per adapter.** Rejected: shared history, shared
  issues and shared CI are worth more than the extra isolation, which the
  module boundary already provides.
- **`v0.x` forever, never declare v1.** Rejected: it is honest and it is also
  an excuse. Writing down the conditions is better than declining to have any.
