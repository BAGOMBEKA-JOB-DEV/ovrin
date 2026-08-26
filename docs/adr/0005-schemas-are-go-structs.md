# ADR-0005: Schemas are Go structs read by reflection

**Status:** Accepted · **Date:** 2026-08-26

## Context

Something has to tell ovrin what to extract. The candidates are a Go struct
annotated with tags, a JSON Schema document loaded at runtime, a bespoke schema
DSL, or generated Go code produced from one of the former.

The requirement that decides it is that the result must be a typed Go value.
`res.Data.Total` has to be a `float64` known at compile time — that type safety
is the reason to write this in Go instead of calling a Python service. Any
design where the schema is a runtime artefact ends with the result being
`map[string]any`, and at that point the library has no advantage over posting
the file to an API.

Go's reflection over struct tags is the established mechanism for exactly this
job. `encoding/json`, `database/sql` wrappers, every popular validator and
every ORM in the ecosystem use it. Users already know the shape.

## Decision

The schema is a Go type. Field descriptions and validation rules are carried in
an `ovrin` struct tag, read by reflection at extraction time.

```go
type Invoice struct {
    Number string  `ovrin:"invoice number,required"`
    Vendor Vendor  `ovrin:"vendor information"`
    Items  []Item  `ovrin:"invoice line items"`
    Total  float64 `ovrin:"total amount including tax,required,min=0"`
}
```

Reflection produces an internal `Schema`, which is what the pipeline actually
uses: it drives the JSON Schema sent to the model, the validation pass and the
keys of `Result.Fields`. The tag grammar is specified separately in
[ADR-0006](0006-tag-grammar.md).

Reflection over a given type happens once per `*Client` and is cached. A type
that cannot produce a valid schema — an unsupported field type, a malformed tag,
a recursive type without a depth bound — is an error from `Extract`, returned
before any provider is contacted, so the failure is immediate and free.

Fields without an `ovrin` tag are skipped. This is deliberate: a struct that
already exists in an application, carrying database and JSON tags, can be used
as a schema by adding tags only to the fields that come from documents.

## Consequences

**Good.** The result is a real typed value with compile-time field access.
Renaming a field is a refactor the compiler checks, not a string that silently
stops matching. The schema and the destination cannot drift apart, because they
are the same declaration. No build step, no code generation, no second file to
keep in sync. The idiom is one every Go programmer already knows.

**Bad.** Struct tags are strings, so the tag contents are unchecked by the
compiler; `requried` is a typo the compiler accepts and only ovrin can
diagnose. Reflection is slower than generated code and the cost is paid at
runtime — mitigated by caching, not eliminated. Schemas cannot be loaded from a
configuration file or supplied by an end user at runtime, which rules out a
class of "let the customer define their own form" applications until a
`SchemaOf` escape hatch exists. Tag strings get long once a field has a
description and three rules, and Go has no line-continuation for them.

## Alternatives considered

- **Runtime JSON Schema documents.** Rejected: the result degrades to
  `map[string]any` and the library's central advantage disappears. Worth
  offering later as a *secondary* entry point for the customer-defined-forms
  case; it is not the primary one.
- **A builder DSL — `ovrin.Object().Field("total", ovrin.Number())`.**
  Rejected: verbose, and the built schema and the destination struct are two
  declarations that will disagree.
- **Code generation from a schema file.** Rejected: a build step and generated
  files in the tree, to buy tag validation we can do at runtime for free at
  first call.
- **Reuse the `json` tag for names and a separate tag for descriptions.**
  Rejected: overloads a tag with existing meaning, and JSON names are for wire
  compatibility while descriptions are prompts for a model — coupling them
  means one cannot change without the other.
