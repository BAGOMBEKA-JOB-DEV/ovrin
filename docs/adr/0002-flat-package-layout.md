# ADR-0002: A flat root package with the implementation in `internal/`

**Status:** Accepted · **Date:** 2026-08-26

## Context

The original design sketched a package per pipeline stage:

```text
ovrin/
  document/   parser/   ocr/   extract/   validate/   confidence/   pipeline/
```

That layout mirrors the pipeline diagram, which makes it appealing. It also
publishes every internal boundary as public API.

Go's package system has a specific consequence that this layout runs into
immediately: packages cannot import each other cyclically, and pipeline stages
genuinely do share types. `confidence` needs the OCR result and the validation
result. `validate` needs the schema. `extract` needs the document and produces
something `validate` consumes. Under a package-per-stage layout those shared
types have to live somewhere neutral — a `types` or `core` package that
everything imports — and at that point the boundaries have become paperwork
rather than structure.

The second cost is larger. Every exported identifier in `parser`, `extract`,
`validate` and `confidence` becomes a compatibility commitment, and each of
those packages is exactly where the design is least settled. We would be
freezing the parts we most expect to change.

The comparison case is `skyl`, this maintainer's other Go library: a flat root
package of nine files with eight `internal/` packages behind it. Its public
surface is 27 types. It has not needed a stage-shaped layout.

## Decision

The public API is one package at the module root. Implementation lives in
`internal/`, which the Go toolchain makes unimportable from outside the module.

```text
ovrin.go        Client, Option, New, Extract[T], Result[T], FieldResult
source.go       Source, Document, Kind, detection
schema.go       struct-tag reflection into Schema
model.go        the Model seam
ocr.go          the OCR seam
render.go       the Renderer seam
confidence.go   Signal, Score
errors.go       sentinels and *Error
internal/       pipeline, prompt, normalise, pdf, adaptertest, sandbox, testutil
```

Adapters are separate modules under `model/`, `ocr/` and `render/`, for the
dependency reason in [ADR-0009](0009-ocr-seam.md), not for a layering reason.

Promoting something out of `internal/` is a deliberate act requiring an ADR
(rule [§1.7](../rules.md#1-public-api)). Moving something into `internal/`
after v1 is a breaking change, which is precisely why the default is to start
there.

## Consequences

**Good.** The public surface is small and reviewable — a reader can hold it in
their head, and `CODEOWNERS` can protect it file by file. No import cycles,
because there is one package. The pipeline can be restructured freely without
a major version, since none of it is visible. Users get one import line.

**Bad.** The root package will grow larger than a package-per-stage layout
would produce, and large files invite the assumption that the code is
unstructured even when it is not. Contributors have to learn that the file
layout inside `internal/` is where the architecture actually lives, which is
one more thing `AGENTS.md` must explain. Callers who genuinely want just the
PDF text extractor cannot have it without us exporting one, and each such
request is an ADR rather than a five-minute change.

We accept this: an over-large package is a refactor, whereas an over-large
public API is a permanent tax on every user.

## Alternatives considered

- **Package per pipeline stage**, as originally sketched. Rejected: forces a
  shared types package, and publishes the least-settled parts of the design as
  API.
- **`pkg/` and `cmd/` layout.** Rejected: `pkg/` adds a path element that
  conveys nothing, and the Go standard library, the standard `x/` repositories
  and skyl all decline it.
- **A `core` package with the root as a thin façade.** Rejected: two names for
  every type, and the façade drifts.
