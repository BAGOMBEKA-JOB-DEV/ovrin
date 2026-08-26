// ovrin/otel is its own module because OpenTelemetry is a large dependency and
// the core has zero (docs/rules.md §4.1, ADR-0021). A user who wants no
// telemetry carries none of this graph.
module github.com/BAGOMBEKA-JOB-DEV/ovrin/otel

// OpenTelemetry v1.35.0 is the newest release whose own go directive is 1.22.0.
// Everything after it requires 1.23 or later, and v1.44 requires 1.25 — taking
// one of those would raise this module's floor above the rest of the
// repository for no API this module uses. A minimum is not a maximum: a user
// already on a newer OTel keeps it, because module resolution takes the higher
// version.
go 1.25.0

require (
	github.com/BAGOMBEKA-JOB-DEV/ovrin v0.1.0
	go.opentelemetry.io/otel v1.45.0
	go.opentelemetry.io/otel/metric v1.45.0
	go.opentelemetry.io/otel/sdk v1.45.0
	go.opentelemetry.io/otel/sdk/metric v1.45.0
	go.opentelemetry.io/otel/trace v1.45.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// The core module has no release tag yet, so the version above is a
// placeholder and this points it at the checkout beside us. A replace in a
// dependency's go.mod is ignored by whoever imports it, so this affects only
// builds of this module itself; it comes out when ovrin is tagged.
replace github.com/BAGOMBEKA-JOB-DEV/ovrin => ..
