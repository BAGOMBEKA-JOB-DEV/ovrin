package pdfium_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/render/pdfium"
)

// benchDoc is a corpus invoice: one A4 page of text, which is the shape of
// document this renderer exists for.
func benchDoc(b *testing.B) ovrin.Document {
	b.Helper()
	data, err := os.ReadFile("../../eval/corpus/invoices/001.pdf")
	if err != nil {
		b.Fatalf("reading the corpus: %v", err)
	}
	return ovrin.Document{Kind: ovrin.KindPDF, Size: int64(len(data)), Data: data}
}

// BenchmarkStartup measures the one-off cost of compiling the PDFium
// WebAssembly module, which is paid by the first Render in a process.
//
// It is reported separately because it is the cost that most surprises people:
// a service that renders one page per request pays it once, a CLI that renders
// one page pays it every time.
// It is measured per instance count as well, because each instance is its own
// Wazero runtime: the compiled code is cached and shared, so the second and
// later runtimes should cost far less than the first, and this is where that
// claim is checked.
func BenchmarkStartup(b *testing.B) {
	doc := benchDoc(b)
	for _, n := range []int{1, 2, 4, 8} {
		n := n
		b.Run(instanceName(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				r := pdfium.New(pdfium.WithInstances(n))
				if _, err := r.Render(context.Background(), doc, 1, 150); err != nil {
					b.Fatalf("Render: %v", err)
				}
				if err := r.Close(); err != nil {
					b.Fatalf("Close: %v", err)
				}
			}
		})
	}
}

// BenchmarkRender measures steady-state rendering with a warm pool, which is
// the number that matters for bulk work.
func BenchmarkRender(b *testing.B) {
	doc := benchDoc(b)
	for _, dpi := range []int{72, 150, 300, 600} {
		dpi := dpi
		b.Run(dpiName(dpi), func(b *testing.B) {
			r := pdfium.New(pdfium.WithInstances(1))
			defer r.Close()
			if _, err := r.Render(context.Background(), doc, 1, dpi); err != nil {
				b.Fatalf("warming: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := r.Render(context.Background(), doc, 1, dpi); err != nil {
					b.Fatalf("Render: %v", err)
				}
			}
		})
	}
}

// BenchmarkRenderParallel measures whether the instance pool actually buys
// throughput, since each instance is a separate WebAssembly module and the
// memory cost of one is not small.
func BenchmarkRenderParallel(b *testing.B) {
	doc := benchDoc(b)
	for _, n := range []int{1, 2, 4, 8} {
		n := n
		b.Run(instanceName(n), func(b *testing.B) {
			r := pdfium.New(pdfium.WithInstances(n))
			defer r.Close()
			if _, err := r.Render(context.Background(), doc, 1, 300); err != nil {
				b.Fatalf("warming: %v", err)
			}
			b.ResetTimer()
			// Parallelism is left at GOMAXPROCS goroutines for every case, so
			// the only thing varying is how many of them can render at once.
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := r.Render(context.Background(), doc, 1, 300); err != nil {
						b.Fatalf("Render: %v", err)
					}
				}
			})
		})
	}
}

func dpiName(dpi int) string    { return "dpi_" + strconv.Itoa(dpi) }
func instanceName(n int) string { return "instances_" + strconv.Itoa(n) }
