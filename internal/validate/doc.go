// Package validate converts extracted values into their Go types and checks
// them against the rules a schema declares.
//
// Nothing in this package returns an error for a value that failed a rule. A
// failed rule is data — a [RuleResult] — because a document with eleven good
// fields and one illegible one is the normal case, and discarding the eleven to
// report the one is the failure this library exists to prevent. See
// docs/adr/0004-partial-results.md and docs/rules.md §2.6. Errors are reserved
// for conditions that make the whole extraction meaningless, and this package
// has none of those: it does no I/O and reads no document.
//
// # Nothing is fabricated
//
// A value that cannot be converted is reported as not converted, with
// [Result.Converted] false and [Result.Value] nil. It is never replaced by a
// zero value, because a caller cannot tell a real 0.00 from a fabricated one
// and a payments system that cannot tell those apart eventually pays the wrong
// amount (docs/rules.md §8.5).
//
// The same applies to dates. 03/04/2026 is 3 April or 4 March depending on who
// printed it, and picking one is a guess with a 50% error rate. When no
// [DateOrder] resolves it, the value is reported ambiguous, both readings are
// offered on [Result.Ambiguity], and no value is produced.
//
// # No document content in messages
//
// [RuleResult.Message] names the problem and never echoes the value: these
// strings reach explanations, logs, traces and audit stores (docs/rules.md §2.5
// and §7.5). The text as extracted is carried on [Result.Raw] instead, which is
// data for the caller to put on the field so a reviewer sees what was actually
// there — not a log line.
package validate
