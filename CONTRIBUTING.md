# Contributing to ovrin

Thank you for considering it. This document covers the mechanics. The
engineering standard is [`docs/rules.md`](docs/rules.md), and review will
enforce it.

> **Ovrin is pre-v1.** The library is implemented and every feature on the
> roadmap through v0.3 exists; what v1.0 waits on is evidence rather than code
> ([ADR-0024](docs/adr/0024-versioning-and-stability.md)). The most valuable
> contributions today are corpus documents, provider adapters, and
> disagreement with an ADR that is wrong.

---

## Setup

```bash
git clone https://github.com/BAGOMBEKA-JOB-DEV/ovrin.git
cd ovrin
make setup      # sign-off hook, then golangci-lint and govulncheck at CI's versions
make check      # the whole gate, across all nine modules
```

`make` on its own lists every target. You need Go 1.22 or newer to build;
`make check` additionally wants `golangci-lint` and `govulncheck`, which
`make setup` installs.

**No credentials are required to build, test or contribute.** That is
deliberate ([ADR-0022](docs/adr/0022-offline-testing.md)) — the default suite
runs entirely against in-process fakes and loopback servers, offline.

If you would rather not install a toolchain at all, there is a container with
all of it pinned:

```bash
make docker-ci        # the whole gate, in Docker
make docker-shell     # a shell with the toolchain, your checkout mounted
```

The container is worth knowing about for one more reason: it ships Tesseract's
English language data, so the six engine-backed tests in `ocr/tesseract` that
skip on most machines actually run there.

---

## Branches

| Prefix | For |
|---|---|
| `feat/` | New capability |
| `fix/` | A defect |
| `docs/` | Documentation only |
| `test/` | Tests only |
| `refactor/` | No behaviour change |
| `chore/` | Tooling, CI, dependencies |

`main` is always releasable. All work lands by pull request.

---

## Commits

[Conventional Commits](https://www.conventionalcommits.org/):

```text
feat(ocr): add Azure Document Intelligence adapter
fix(pdf): handle ToUnicode CMaps with surrogate pairs
docs(adr): record why confidence is not derived from logprobs
test(sandbox): serve a truncated response mid-body
```

Breaking changes take `!` and a `BREAKING CHANGE:` footer.

For anything substantial, **the body explains the change, not the diff**: what
was wrong, what you did, and how you know it works. The diff already shows
what changed.

### Sign off every commit

Ovrin uses the [Developer Certificate of Origin](https://developercertificate.org/).
By signing off you state you have the right to submit the work under
Apache-2.0.

```bash
git commit -s -m "fix(pdf): handle ToUnicode CMaps with surrogate pairs"
```

Or set `git config core.hooksPath .githooks` once and it is added for you. CI
rejects unsigned commits. There is no contributor licence agreement
([ADR-0025](docs/adr/0025-licence-policy.md)).

---

## Before you open a pull request

```bash
make check
```

That is the whole gate: `gofmt`, build, `go vet` under every build tag, the
test suite with the race detector, the same again over real sockets against
the adversarial fake, `go mod tidy` leaving no diff, `golangci-lint`,
`govulncheck`, and the documentation checks — across **every module**, so
there is nothing to repeat by hand.

`make ci` adds what only CI used to do: the coverage floor, the
zero-dependency assertion and the cgo-free cross-compile.

CI runs these very targets ([`ci.yml`](.github/workflows/ci.yml)) at both each
module's declared Go floor and the newest release. That is the point of the
Makefile — the gate and the description of the gate cannot drift apart when
there is only one of them. If you find yourself typing a `go` command that no
target covers, add the target.

Individual pieces, when you want a faster loop:

```bash
make test                 # just the offline suite
make lint                 # just golangci-lint
make build MODULES=otel   # one module, the way CI's matrix does it
make                      # list every target
```

---

## What review will ask

- Does the core still have zero dependencies? ([`rules.md` §4.1](docs/rules.md#4-dependencies))
- Does `CGO_ENABLED=0` still build? ([§4.3](docs/rules.md#4-dependencies))
- Could document content reach an error, event or trace? ([§2.5](docs/rules.md#2-errors), [§7.5](docs/rules.md#7-untrusted-input))
- Does every exported symbol have a doc comment saying *why*? ([§1.1](docs/rules.md#1-public-api), [§9.2](docs/rules.md#9-documentation))
- Are test cases named so a failure identifies itself? ([§3.2](docs/rules.md#3-testing))
- Is every goroutine asserted to stop? ([§3.6](docs/rules.md#3-testing))
- Does an unreadable field come back absent rather than zero? ([§8.5](docs/rules.md#8-confidence-and-provenance))
- Does a new limit have a finite default? ([§5.2](docs/rules.md#5-concurrency-and-resources))
- Is there an ADR, and does it name its costs? ([§9.4](docs/rules.md#9-documentation))
- Was the documentation updated in the same commit?

---

## Adding a provider adapter

Full guide in [`docs/providers.md`](docs/providers.md). In short:

1. **Open an issue first.** Every in-tree adapter is a module the maintainer
   must release and keep working, and the bus factor is one. We may ask you to
   keep it in your own repository and link to it — that is not a rejection.
   Out-of-tree adapters are first class.
2. `ocr/<name>/` or `model/<name>/`, with its own `go.mod` and `LICENSE`.
3. `New(credential string, opts ...Option) *Provider`, functional options, no
   exported config struct.
4. `var _ ovrin.OCR = (*Provider)(nil)` at the bottom of the file.
5. Map the provider's errors onto ovrin's sentinels. Never branch on message
   text.
6. Normalise: coordinates to page points with a top-left origin, confidence to
   0–1, words into reading order.
7. Wire it into `internal/adaptertest`. It must pass the whole suite.
8. Add sandbox support in `internal/sandbox` so it is testable offline.
9. Add a column to [`docs/feature-matrix.md`](docs/feature-matrix.md),
   **including the ⚠️ silently-ignored cells**. An adapter contributed without
   them is incomplete.
10. Add a CI matrix row.

---

## Contributing evaluation documents

The most valuable contribution to the project, and the one most likely to
arrive with a licence problem. **Read
[`docs/evaluation.md`](docs/evaluation.md#contributing-documents) before
spending effort.**

The hard constraint: every document must be **redistributable** and contain
**no real personal data**. Public forms, synthetic documents, or donated ones
with written permission and every identifier replaced by a synthetic value of
the same shape. A repository is forever and `git rm` is not deletion.

Documents that are hard in a specific, describable way are worth far more than
clean ones.

---

## Architecture decision records

A change that shapes the code needs an ADR
([`docs/adr/`](docs/adr/README.md)).

- One decision per record.
- Never edit an accepted record to change its decision — supersede it.
- Numbers are permanent; other documents cite them.
- **Alternatives considered is not optional.**
- **An ADR that lists no downsides has not finished thinking.** Every real
  decision costs something. Say what.

Disagreeing with an existing ADR is welcome and is best done as a pull request
proposing a superseding one. The old record stays.

---

## Reporting bugs

Use the issue templates. For an extraction problem, the single most useful
thing you can include is **a document that reproduces it and that you are
allowed to share**. If you cannot share it, describe its structure — producer,
scanned or digital, language, layout, where the field sits on the page.

**Do not report security issues as public issues.** See
[`SECURITY.md`](SECURITY.md).

---

## License

By contributing you agree your work is licensed under Apache-2.0, and you sign
off under the DCO stating you have the right to submit it.
