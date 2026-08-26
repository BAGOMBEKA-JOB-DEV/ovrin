# ADR-0006: The `ovrin` struct tag grammar

**Status:** Accepted · **Date:** 2026-08-26

## Context

[ADR-0005](0005-schemas-are-go-structs.md) settles that the schema is a Go
struct with tags. The tag has to carry two different kinds of information:

1. A **description** — natural-language prose, aimed at a language model,
   telling it what this field is. `"total amount including tax"` extracts
   correctly where `"total"` picks up the pre-tax subtotal.
2. **Rules** — machine-checked constraints applied after extraction:
   `required`, `min=0`, a format, an enumeration.

These are consumed by different stages and are unlike each other. The
description is free text with spaces and punctuation; rules are a small closed
vocabulary. Go struct tags are a single string per key, so both must share one
string, and the syntax has to keep them apart without quoting rules that make
the common case ugly.

The prevailing Go convention — `json:"name,omitempty"`, `validate:"required,
min=0"` — is comma-separated. Descriptions contain commas ("name, address and
postcode"), which is the whole difficulty.

## Decision

The tag value is a comma-separated list. **The first element is the
description; every subsequent element is a rule.**

```text
ovrin:"<description>[,<rule>[,<rule>...]]"
```

A rule is `name` or `name=value`. The rule vocabulary is closed and known at
compile time of ovrin itself, which is what makes the split unambiguous: when
parsing, an element is a rule only if its name is in the vocabulary. Anything
else is an error naming the unknown rule, never a silent skip
(rule [§6.1](../rules.md#6-adapters)).

```go
type Invoice struct {
    Number   string    `ovrin:"invoice number,required"`
    Issued   time.Time `ovrin:"date the invoice was issued,format=date"`
    Currency string    `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
    Total    float64   `ovrin:"total amount including tax,required,min=0"`
    Notes    string    `ovrin:"free-text notes"`
    Internal string    // no tag: not part of the schema
}
```

Commas inside a description are written `\,`. A description needing a literal
backslash writes `\\`. This is the one escape in the grammar and it is expected
to be rare; a description that needs several commas is usually a description
that should be shorter.

Three special forms:

- `ovrin:"-"` excludes a field explicitly, matching `encoding/json`.
- An empty description (`ovrin:",required"`) derives the description from the
  field name, splitting camel case: `InvoiceNumber` becomes
  `"invoice number"`.
- A field with no `ovrin` tag at all is not part of the schema.

The rule vocabulary for v0.1 is `required`, `min`, `max`, `format` and `enum`.
Adding a rule is a minor release; changing what an existing rule means is
breaking. The authoritative list, with each rule's exact semantics per Go type,
is [`docs/schema.md`](../schema.md) — this ADR fixes the grammar, not the
vocabulary.

## Consequences

**Good.** The common case — a description and nothing else — is the shortest
possible tag. The syntax matches what Go programmers already type for `json`
and for every validator in the ecosystem, so it needs no explanation. The
closed rule vocabulary means a typo like `requried` is a loud error rather than
a rule that silently does not apply, which is the standard failure of
open-vocabulary tag parsers.

**Bad.** The escape for commas is a genuine wart, and it will be discovered by
somebody writing `"name, address and postcode"` and getting an error about an
unknown rule ` address and postcode`. Tags become long — a description plus
three rules approaches a hundred characters, on a line Go will not let you
wrap. The description is a prompt fragment, so a change in prose can change
extraction results with no compiler signal at all, which makes it an untyped
input to a typed system. And the closed vocabulary means users cannot add a
project-specific rule without patching ovrin; a `Validator` interface will be
needed eventually, and that is a future ADR.

**Neutral.** Deriving descriptions from field names is convenient and slightly
magical. It is opt-in by writing an empty first element, so nobody gets it by
accident.

## Alternatives considered

- **Separate tag keys** — `ovrin:"total amount"` plus
  `ovrinrules:"required,min=0"`. Rejected: two tags to keep in sync, and the
  description tag is far more common, so the split penalises the common case
  with a second key on every field that has any rule.
- **Rules first, description last.** Rejected: the description is required and
  the rules are optional, so putting the optional part first means the common
  case is `ovrin:",total amount"` with a leading comma nobody will remember.
- **Semicolon between the two sections** — `ovrin:"total amount;required,
  min=0"`. Rejected: no escape needed for commas, but it is a syntax unlike
  everything else in Go, and semicolons in prose are not rare enough to make
  the problem disappear — it relocates the escape rather than removing it.
- **Quote descriptions containing commas** — `ovrin:"'name, address',required"`.
  Rejected: nested quoting inside a Go string literal that is itself inside a
  backtick literal. Unreadable.
