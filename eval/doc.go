// Package eval measures extraction quality against a committed corpus.
//
// It exists because every claim worth making about ovrin is a distribution,
// not a boolean. A unit test asserts that one document produces one value,
// which says nothing about how often extraction is right, whether a prompt
// change helped, or whether confidence below 0.70 actually corresponds to a
// wrong value. Without measurement a regression here does not crash — it
// returns a slightly wrong number slightly more often, and ships
// (docs/adr/0023-evaluation-corpus.md).
//
// The package splits into two halves on purpose.
//
// Everything in this file's package — the corpus loader, the type-aware
// comparison, the metric arithmetic and the report renderer — is a pure
// function of data and is unit-tested without a provider, offline, in the
// normal test suite. The half that costs money lives behind the eval build
// tag, so `go test ./...` never contacts a provider and never bills anybody:
//
//	go test -tags=eval ./eval/... -run TestCorpus
//
// # Not a stability promise
//
// This package is a measurement tool that happens to live in the same module
// as the library. Its exported symbols are exported because encoding/json
// needs them to be and because the corpus generator is a separate programme;
// they carry no compatibility guarantee and are excluded from api/ovrin.txt,
// which records only the root package.
//
// # What the numbers mean
//
// Read [Metrics] and [Calibration] before quoting a figure from here. Rule
// §3.8 forbids claiming an accuracy number this harness cannot reproduce, and
// the fastest way to break that rule by accident is to quote a ratio whose
// denominator you did not check.
package eval
