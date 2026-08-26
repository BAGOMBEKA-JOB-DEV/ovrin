# Changelog

All notable changes to this project are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Until v1.0.0, breaking changes may land in minor releases. They will always be
listed here with a migration note.

Modules in this repository version independently and are tagged with their path
prefix. Entries below say which module they affect where it is not the core.

## [Unreleased]

### Added

- **The design.** This release contains no code. It establishes the
  architecture, the public API specification, the pipeline, the schema grammar,
  the confidence model and the threat model for a Go library that turns
  documents into typed, validated data.

  Twenty-five architecture decision records in [`docs/adr/`](docs/adr/) settle
  the load-bearing questions, each with the alternatives that were rejected and
  the costs that were accepted. The ones that shape the most: the core has zero
  dependencies and no cgo, schemas are Go structs read by reflection,
  confidence is multi-signal because logprobs are unavailable or saturated, and
  document content is treated as untrusted input at every stage.

  Written before the code deliberately. Discovering during implementation that
  confidence cannot come from logprobs, or that prompt construction has to sit
  on the core's side of the seam for the security property to hold, would have
  produced a worse design and a rewrite.

- **The name.** The project was renamed from *vellum* to **ovrin** before any
  code existed. `vellum` collided with `github.com/blevesearch/vellum` — a
  widely-imported Go package of the same name — with vellum.ai, an active
  platform in the same problem space, and with a registered US word mark in the
  software services class. `ovrin` has no Go-namespace collision.
  [ADR-0001](docs/adr/0001-name-and-module-path.md) records the sweep,
  including the candidates that were rejected and the collisions `ovrin` does
  have.

- **Documentation.** Eighteen documents covering the idea, getting started, the
  architecture, all nine pipeline stages, the tag grammar, the confidence
  model, explainability, the threat model, data handling, provider authoring,
  the feature matrix, evaluation, the engineering rules, the roadmap, the
  project plan, a glossary, and an honest guide to
  [validating ovrin before adopting it](docs/validating.md).

- **`AGENTS.md`.** An operating contract for coding agents and new
  contributors, whose rules cite `docs/rules.md` section numbers so that a
  review comment can point at a line rather than an opinion.

### Notes

- No release has been made. The install commands in the README will not work
  yet.
- The Go API in the documentation is a specification, not a description. It
  will change as it meets real documents.
- No accuracy figure is published, and none will be until the evaluation
  harness can reproduce it
  ([ADR-0023](docs/adr/0023-evaluation-corpus.md)).
- Confidence weights are provisional. Confidence is documented as a ranking
  signal, not a probability, until it is calibrated.

[Unreleased]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/commits/main
