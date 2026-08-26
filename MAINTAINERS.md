# Maintainers

| Name | GitHub | Areas |
|---|---|---|
| BAGOMBEKA JOB | [@BAGOMBEKA-JOB-DEV](https://github.com/BAGOMBEKA-JOB-DEV) | everything |

## Bus factor

**One.**

That is stated plainly because you may be deciding whether to depend on this,
and it is the most important fact about the project's sustainability.

What reduces the risk:

- **Apache-2.0.** Anyone can fork, at any time, for any reason.
- **The architecture is documented.** Thirty-one ADRs record not just what was
  decided but why, and what it cost. A fork inherits the reasoning, not just
  the code.
- **Zero dependencies in the core.** There is very little that can rot
  underneath it.
- **No hosted service.** Nothing stops working if the maintainer does.

What does not reduce it: nothing about the code quality. A well-tested library
with one maintainer is still a library with one maintainer.

## Becoming a maintainer

There is no formal process, because there has been no occasion to use one. In
practice: contribute consistently, review other people's work, and demonstrate
judgement about what belongs in the project and what does not — which for a
library is mostly judgement about what to decline.

Owning an adapter module is a natural first step. Adapters are self-contained,
have a defined contract, and their maintenance burden is legible.

If you are interested, open an issue and say so.

## Load-bearing files

Changes to these need the maintainer's review regardless of who else approves,
because they encode decisions rather than implementation:

```text
/docs/rules.md          the engineering standard
/docs/adr/              the decisions
/model.go /ocr.go /render.go    the seams
/SECURITY.md
/.github/
```

## If this project becomes unmaintained

If there is no response to issues or pull requests for **90 days**, treat ovrin
as unmaintained and fork it. You do not need permission — Apache-2.0 already
grants it, and this paragraph exists so nobody wastes a month waiting politely.

A fork should rename the module path. Please also open an issue here saying
where it lives, so people arriving at this repository can find it.
