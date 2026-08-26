---
name: Feature request
about: Propose a capability
labels: enhancement
---

## The problem

What you are trying to do, and what stops you. Not the solution yet.

## Why the existing API cannot do it

Ovrin has three seams — `Model`, `OCR` and `Renderer` — and a great many
requests turn out to be implementable as an adapter or a decorator without any
change to ovrin. Please check first, and say what you found.

## What you propose

## What it would cost

Ovrin's ADRs all name their downsides, and requests are held to the same
standard. What would this make worse? Consider: a new dependency in the core is
disallowed outright ([§4.1](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/rules.md#4-dependencies)); a new exported
type is permanent; a new option is one more thing to document and get wrong.

## Have you checked the roadmap and the ADRs?

- [ ] Not already listed in [`docs/roadmap.md`](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/roadmap.md), including
      the "Deferred, deliberately" section
- [ ] Not already decided against in [`docs/adr/`](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/README.md) —
      and if it is, I have said which ADR and why I think it should be
      superseded
