package ovrinotel_test

import (
	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	ovrinotel "github.com/BAGOMBEKA-JOB-DEV/ovrin/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Option is passed to New alongside the providers, and reports every pipeline
// stage for the life of the Client.
func ExampleOption() {
	tp := sdktrace.NewTracerProvider()
	mp := sdkmetric.NewMeterProvider()

	// A real client also takes ovrin.WithModel(model); it is left out here so
	// the example needs no provider to compile.
	c := ovrin.New(ovrinotel.Option(tp, mp))
	_ = c
}

// Either provider may be nil, which turns that signal off. A caller who wants
// traces and has no metrics pipeline should not have to build a no-op meter to
// say so.
func ExampleOption_tracesOnly() {
	tp := sdktrace.NewTracerProvider()

	c := ovrin.New(ovrinotel.Option(tp, nil))
	_ = c
}
