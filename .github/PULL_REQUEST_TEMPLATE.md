## What this changes

<!-- What was wrong or missing, and what you did about it. Not the diff — the
     diff is already visible. -->

## How it was verified

<!-- Paste the commands you ran and their output. "Tests pass" is not
     verification; the output is. -->

```text

```

## Checklist

Each item cites the rule it comes from. If one does not apply, say why rather
than deleting it.

- [ ] `make check` passes — `gofmt`, build, `go vet` under every build tag,
      `-race` and `-tags=sandbox`, `go mod tidy`, `golangci-lint`,
      `govulncheck`, docs — [§11](../docs/rules.md#11-formatting-and-tooling), [§3.3](../docs/rules.md#3-testing)
- [ ] `make ci` passes — adds the coverage floor, the zero-dependency
      assertion and the cgo-free cross-compile — [§4.1](../docs/rules.md#4-dependencies), [§4.3](../docs/rules.md#4-dependencies)
- [ ] No new dependency is AGPL or GPL — [§4.4](../docs/rules.md#4-dependencies)
- [ ] No document content or credential can reach an error, event or trace — [§2.5](../docs/rules.md#2-errors), [§7.5](../docs/rules.md#7-untrusted-input)
- [ ] Every new exported symbol has a doc comment saying *why* — [§1.1](../docs/rules.md#1-public-api), [§9.2](../docs/rules.md#9-documentation)
- [ ] Test cases are named so a failure identifies itself — [§3.2](../docs/rules.md#3-testing)
- [ ] Every goroutine started is asserted to stop — [§3.6](../docs/rules.md#3-testing)
- [ ] No coverage floor was lowered — [§3.7](../docs/rules.md#3-testing)
- [ ] Documentation is updated in this PR, not a follow-up — [§9.5](../docs/rules.md#9-documentation)
- [ ] `CHANGELOG.md` `[Unreleased]` updated for any user-visible change
- [ ] Any new decision has an ADR, and the ADR names its costs — [§9.4](../docs/rules.md#9-documentation)
- [ ] Commits are Conventional Commits and are signed off — [§10.1](../docs/rules.md#10-git), [§10.4](../docs/rules.md#10-git)

## Related

<!-- Issues, ADRs, prior PRs. If this contradicts an accepted ADR, say so
     here and explain — that needs a superseding ADR, not a quiet deviation. -->
