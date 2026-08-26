// Package schema holds the Go structs each corpus category is extracted
// against.
//
// They live here rather than in the harness because they are part of what is
// being measured. A description is untyped input to a typed system: changing
// the prose changes extraction results with no compiler signal at all
// (docs/schema.md), so a schema change is a change worth a fresh eval run and
// a fresh committed report. Keeping them in their own files makes that change
// a reviewable diff.
//
// Every struct here is also a worked example of the tag grammar, and they are
// written the way the documentation says to write them: descriptions that say
// which of several similar values is meant, and an explicit "if one is
// printed" on fields the corpus deliberately leaves absent — a model told a
// field is optional is much less likely to invent one, and fabrication rate is
// the metric this corpus most wants to measure.
package schema
