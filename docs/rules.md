# Engineering rules

These are not aspirational: CI enforces most of them, and review enforces the
rest. If a rule blocks something genuinely necessary, change the rule in a PR
and say why. Do not route around it silently.

Rules are numbered so that other documents can cite them. `AGENTS.md`, the pull
request template, `CONTRIBUTING.md` and a good many doc comments do exactly
that. When you renumber a rule you break those citations, so do not renumber —
deprecate in place and add at the end.

**Contents:** [1. Public API](#1-public-api) · [2. Errors](#2-errors) ·
[3. Testing](#3-testing) · [4. Dependencies](#4-dependencies) ·
[5. Concurrency and resources](#5-concurrency-and-resources) ·
[6. Adapters](#6-adapters) · [7. Untrusted input](#7-untrusted-input) ·
[8. Confidence and provenance](#8-confidence-and-provenance) ·
[9. Documentation](#9-documentation) · [10. Git](#10-git) ·
[11. Formatting and tooling](#11-formatting-and-tooling)

---

## 1. Public API

**1.1 — Every exported symbol has a doc comment**, starting with its name, in
full sentences. A symbol nobody can explain in two sentences is a symbol that
has not been designed yet.

**1.2 — Before v1, breaking changes are allowed but must appear in the
changelog**, with a migration note. After v1 they require a new major version.
See [ADR-0024](adr/0024-versioning-and-stability.md).

**1.3 — Accept interfaces, return structs.** `New` returns `*Client`, never an
interface. Callers who need to substitute ovrin in their own tests substitute
at their own seam, not ours.

**1.4 — Configuration is functional options, never an exported config struct.**
An exported struct field is a permanent API commitment that costs nothing to
add and everything to remove. `Option` values are opaque and can change shape.

**1.5 — `context.Context` is the first parameter of every function that does
I/O**, and it is actually honoured: cancellation must abort the work, not merely
be recorded. A `_ context.Context` parameter outside a fake is a bug.

**1.6 — Panic only on programmer error at construction time.** `New` panics on
a nil `Model` because the alternative is a nil dereference on the first
extraction, thousands of lines away from the mistake. Nothing else panics.
Runtime problems are errors.

**1.7 — The exported surface stays small.** Types that exist to serve the
pipeline live in `internal/`. If a caller genuinely needs one, exporting it is
a deliberate decision with an ADR, not a convenience.

**1.8 — Optional scalars are pointers.** `*float64`, not `float64`, wherever
"unset" and "zero" mean different things. Document that `nil` and `&T{}` are
not the same.

**1.9 — Enums are string types with a documented unknown member.** `type Kind
string`, not `iota`. A provider that returns something we have never seen maps
to the unknown member; it never invents a new constant and never silently maps
to the zero value.

**1.10 — Generic type parameters go on package-level functions, not methods.**
`ovrin.Extract[T](ctx, c, src)`, not `c.Extract[T](...)`. Generic methods
require Go 1.27 and our floor is 1.22. See
[ADR-0003](adr/0003-go-floor-and-generics.md).

---

## 2. Errors

**2.1 — Wrap with `%w`.** Every error that crosses a function boundary either
adds context and wraps, or is returned untouched. Never `fmt.Errorf("...: %v",
err)`.

**2.2 — Classify, do not stringify.** Nothing anywhere may branch on the text
of an error message. Provider responses are classified into sentinels at the
adapter boundary and every decision downstream reads the sentinel.

**2.3 — Error strings are lowercase and unpunctuated**, prefixed `ovrin: ` at
the package boundary. `errors.New("ovrin: no provider configured for this document")`.

**2.4 — Never discard an error.** `_ =` requires a comment on the same line
saying why the error cannot matter here.

**2.5 — Errors never contain document content or credentials.** A document is
somebody's invoice, medical form or identity paper; an error string is a log
line that ends up in five systems nobody audited. Errors name the field, the
page and the failure — never the value. Enforced by a test in the shared
adapter suite. See also [§7.5](#7-untrusted-input).

**2.6 — A failed field is not a failed extraction.** Errors are returned for
conditions that make the whole result meaningless — the source could not be
read, no reading could be produced, the context was cancelled. Everything else
is recorded on the field and the caller decides. See
[ADR-0004](adr/0004-partial-results.md).

---

## 3. Testing

**3.1 — Adapters are tested by a shared contract suite** in
`internal/adaptertest`. A rule added there is enforced everywhere at once and
no adapter can regress behind another's tests. An adapter that cannot pass the
suite is not finished.

**3.2 — Table tests have named cases**, and the name identifies the case. The
name is printed on failure, so it must be `"scanned page with no text layer"`,
not `"case 3"`.

**3.3 — No network in unit tests.** `httptest.Server` or the offline sandbox,
never a real endpoint. A test suite that needs the internet is a test suite
that is red on aeroplanes and in CI outages. See
[ADR-0022](adr/0022-offline-testing.md).

**3.4 — Fakes, not mocks.** Hand-written structs that implement the seam. No
mocking framework, no assertion library — the standard `testing` package and
`t.Errorf` are enough, and they never need upgrading.

**3.5 — Fixtures are real documents, committed, and small.** A synthetic PDF
that our own writer produced proves only that we can read our own writing. Test
against files produced by Word, LaTeX, scanners and phone cameras. Redact them
before committing; see [§7.6](#7-untrusted-input).

**3.6 — Every test that starts a goroutine asserts it stopped.** Streaming,
page-parallel work and provider fallback all leak on early return if nobody
checks. Take the leak baseline after any test server is up.

**3.7 — Coverage floors are enforced per module in CI and never lowered to make
a red build green.** If a change genuinely makes a package harder to cover, say
so in the PR and move the floor deliberately in its own commit.

**3.8 — Extraction quality is measured, not asserted.** Correctness of the
pipeline is a unit test; accuracy of extraction is an evaluation run against a
committed corpus with published numbers. Never claim an accuracy figure that
`go test -tags=eval ./...` cannot reproduce. See
[ADR-0023](adr/0023-evaluation-corpus.md).

---

## 4. Dependencies

**4.1 — The core module has zero external dependencies.** Not "few". Zero.
Adding one requires an ADR that explains what could not be written in a hundred
lines of standard library. See [ADR-0001](adr/0001-name-and-module-path.md) and
[ADR-0002](adr/0002-flat-package-layout.md).

**4.2 — Every adapter is its own module.** A user who wants Tesseract should
not inherit the AWS SDK, and a user who wants neither should inherit nothing.
The dependency rule is structural: it cannot be violated by accident because an
import of the wrong thing from the core module will not compile.

**4.3 — No cgo in the core module, ever.** Cross-compilation and
`CGO_ENABLED=0` static builds are the reason people choose Go for this. cgo is
permitted only in submodules whose documentation says so on the first line. See
[ADR-0010](adr/0010-no-cgo-in-core.md).

**4.4 — No AGPL, anywhere.** Ovrin is Apache-2.0 and is meant to be embeddable
in closed products. That excludes UniPDF and MuPDF regardless of technical
merit. Check the licence before you check the benchmark. See
[ADR-0025](adr/0025-licence-policy.md).

**4.5 — Vendor nothing; pin everything.** `go.sum` is the pin. GitHub Actions
are pinned by commit SHA with the version in a trailing comment.

---

## 5. Concurrency and resources

**5.1 — Everything exported is safe for concurrent use by multiple
goroutines**, and says so in its doc comment. A `*Client` is built once and
shared; if that is not true of a type, the doc comment must say it is not.

**5.2 — Every limit has a default and the default is finite.** Page count,
decompressed bytes, image dimensions, recursion depth, concurrent pages, total
wall time. A library that processes untrusted documents with unbounded limits
is a denial-of-service vector wearing a bow tie. See
[ADR-0020](adr/0020-resource-limits.md).

**5.3 — Parallelism is bounded and configurable.** Page-level work runs
concurrently up to a cap the caller sets. Defaulting to `GOMAXPROCS` and hoping
is not a policy.

**5.4 — Cancellation propagates everywhere.** A cancelled context stops OCR,
stops model calls, stops page workers, and returns promptly. "Promptly" means
the next check, not the next page.

**5.5 — No global state.** No package-level registry, no `init()` side effects,
no default client. Two `*Client` values in one process must not be able to
observe each other.

---

## 6. Adapters

**6.1 — Never silently drop data.** An adapter that cannot represent something
the caller asked for returns `ErrUnsupported` naming what it could not do. The
one behaviour we will not tolerate is quietly producing a worse answer than the
caller believes they asked for.

**6.2 — Adapters map, they do not decide.** Retry, fallback, timeouts, limits
and confidence live in the core. An adapter translates our request into a
vendor's wire format and translates the response back. An adapter with a retry
loop in it is a bug.

**6.3 — Every adapter ends with a compile-time assertion.**
`var _ ovrin.OCR = (*Provider)(nil)`.

**6.4 — Adapters take their credential explicitly**, as `New(credential,
opts ...Option)`; an adapter needing none takes options only, as
`New(opts ...Option)`. No adapter reads the environment itself. Reading `os.Getenv` inside a library is
how a program ends up talking to the wrong account.

**6.5 — An adapter documents what it silently ignores.** Not just what it
supports. See `docs/feature-matrix.md`; a matrix that lists only the green
cells is exactly the thing this rule rejects.

---

## 7. Untrusted input

**7.1 — Every document is hostile until proven otherwise.** Documents arrive
from claimants, customers, applicants and email. Parse them as you would parse
a packet off the wire. See [`docs/threat-model.md`](threat-model.md).

**7.2 — Document text is data, never instruction.** Text recovered from a
document is never concatenated into a position where a model could read it as a
directive. It is delimited, labelled as untrusted, and the schema — not the
document — determines what is asked for. See
[ADR-0017](adr/0017-untrusted-document-content.md).

**7.3 — Decompression is bounded.** Every decompressor is wrapped in a limited
reader and every recursive parser has a depth limit. A 600 KB file that expands
to 10 GB in memory is a documented, published PDF attack, not a hypothetical.

**7.4 — Never fetch what a document points at.** No URL in a document is
followed, no external entity is resolved, no remote font or image is loaded.
Server-side request forgery via a crafted document is not a feature we are
adding.

**7.5 — Document content never reaches logs, traces, metrics or errors.** Hooks
and spans carry field counts, page numbers, byte counts, durations and
confidence — never field values, and never the text a field was read from. A caller who wants values has the `Result`.

**7.6 — Committed fixtures contain no real personal data.** Redact before
committing. A repository is forever and `git rm` is not deletion.

---

## 8. Confidence and provenance

**8.1 — Confidence is computed from named signals, and the signals are on the
record.** No score is ever produced that the caller cannot decompose into the
inputs that made it. See [ADR-0013](adr/0013-multi-signal-confidence.md).

**8.2 — Model self-reported confidence is not confidence.** Neither are token
logprobs: one major provider exposes none at all, and under constrained JSON
output they saturate near 1.0 and stop discriminating. They may be one signal
among several; they may never be the score.

**8.3 — Every field carries where it came from.** Which reading produced it,
which page, which span. A value with no provenance cannot be reviewed, audited
or debugged, and this library exists to be used in places where all three are
required. See [ADR-0015](adr/0015-provenance.md).

**8.4 — Disagreement is a result, not an error.** When two readings of the same
document produce different values, both are recorded, the field is marked for
review, and neither is silently preferred. See
[ADR-0014](adr/0014-cross-validation.md).

**8.5 — Never fabricate a value to satisfy a schema.** A field that could not
be found is absent and marked absent. Returning a plausible zero because the
struct has a `float64` in it is the single worst thing this library could do.

---

## 9. Documentation

**9.1 — Every exported symbol has a doc comment** (this is [§1.1](#1-public-api),
restated because it is a documentation rule too). Use `[Bracket]` doc links so
godoc resolves them.

**9.2 — Doc comments say why, not only what.** The what is usually visible in
the signature. Name the trade-off, and cite the rule or ADR that settled it.

**9.3 — Examples are tests.** `example_test.go`, `package ovrin_test`, with
`// Output:` where the output is deterministic. An example that does not compile
is worse than no example.

**9.4 — A decision that shapes the code gets an ADR.** An ADR that lists no
downsides has not finished thinking; every real decision costs something, so
say what.

**9.5 — Documents state what is not done.** Roadmaps mark what is deferred and
why; the feature matrix has a column for silently-ignored; the README's status
section says what is missing. A document that only lists successes is
marketing.

---

## 10. Git

**10.1 — Conventional Commits.** `type(scope): lowercase imperative subject`.
Types: `feat fix docs test refactor chore`. Breaking changes take `!` and a
`BREAKING CHANGE:` footer.

**10.2 — Branches are `type/slug`**, matching the commit types.

**10.3 — All work lands by pull request. `main` is always releasable.**

**10.4 — Every commit is signed off.** The Developer Certificate of Origin
applies; `git commit -s`, or enable the hook with
`git config core.hooksPath .githooks`. CI rejects unsigned commits.

**10.5 — Commit bodies explain the change, not the diff.** For anything
substantial, say what was wrong, what you did, and how you know it works.

**10.6 — One decision per ADR, and ADR numbers are permanent.** Never edit an
accepted ADR to change its decision — supersede it with a new one and mark the
old one superseded.

---

## 11. Formatting and tooling

**11.1 — `gofmt` is the formatter and CI fails on unformatted code.** No
opinions, no `.editorconfig` arguments.

**11.2 — `go vet` passes, including under every build tag.** A tagged file that
nobody vets rots.

**11.3 — `golangci-lint` passes with the configuration in the repository.** The
linter set is deliberately small: a lint rule nobody believes in gets
suppressed everywhere and stops meaning anything.

**11.4 — `go mod tidy` leaves no diff.** Checked in CI, in every module.

**11.5 — `govulncheck` passes in every module** on every pull request.

**11.6 — Markdown prose is hard-wrapped at 80 columns.** Tables, links and code
fences are exempt. Reviewing a one-line-per-paragraph diff is miserable.
