package schema

import (
	"reflect"
	"testing"
	"time"
)

// Reflection is the one stage whose cost is a property of the caller's program
// rather than of the document, and the cache is what turns it from a per-call
// cost into a per-type one. That is the claim this file exists to check: a
// service extracting the same struct from ten thousand invoices should reflect
// over it once and pay a map lookup nine thousand nine hundred and ninety-nine
// times.
//
// So the two are measured side by side. Reflect is the cold path — walking the
// fields, parsing every tag, recursing into nested structs and slice elements —
// and Cache.Of is what every extraction after the first actually calls. If the
// gap between them ever stops being large, the cache has stopped working and
// nothing else in the library would notice.

// Kept reachable so the compiler cannot delete the reflection.
var benchSchema *Schema

// benchLineItem is the element of a slice field, which is the case that makes
// reflection recursive.
type benchLineItem struct {
	Description string  `ovrin:"what was supplied"`
	Quantity    float64 `ovrin:"how many,min=0"`
	UnitPrice   float64 `ovrin:"price per unit,min=0"`
	Amount      float64 `ovrin:"line total,min=0"`
}

// benchParty is a nested struct, which is the other recursive case.
type benchParty struct {
	Name    string `ovrin:"legal name,required"`
	Address string `ovrin:"postal address"`
	TaxID   string `ovrin:"tax identifier"`
}

// benchInvoice is the shape of the corpus invoices and of the example in
// docs/getting-started.md: a dozen scalar fields, two nested structs, a slice
// of line items, and formats and rules on most of them. It is the realistic
// unit of work for this package.
type benchInvoice struct {
	Number        string          `ovrin:"invoice number,required"`
	Issued        time.Time       `ovrin:"date of issue,required,format=date"`
	Due           *time.Time      `ovrin:"payment due date,format=date"`
	PurchaseOrder string          `ovrin:"purchase order reference"`
	Vendor        benchParty      `ovrin:"the supplier"`
	BillTo        benchParty      `ovrin:"the customer"`
	Currency      string          `ovrin:"currency,required,format=currency"`
	Subtotal      float64         `ovrin:"net total,min=0"`
	Tax           float64         `ovrin:"tax charged,min=0"`
	Total         float64         `ovrin:"gross total,required,min=0"`
	Contact       string          `ovrin:"accounts contact,format=email"`
	Telephone     string          `ovrin:"accounts telephone,format=phone"`
	IBAN          string          `ovrin:"bank account,format=iban"`
	Items         []benchLineItem `ovrin:"the line items"`
}

// benchFlat is a small schema of the kind a receipt or an identity document
// gets, measured alongside the invoice so the cost can be read against the
// number of fields rather than as one figure.
type benchFlat struct {
	Merchant string    `ovrin:"merchant name,required"`
	Date     time.Time `ovrin:"date of purchase,format=date"`
	Total    float64   `ovrin:"amount paid,required,min=0"`
}

// BenchmarkReflect measures the cold path: what the first extraction of a type
// pays, and what a process that creates a fresh client per request pays every
// time.
func BenchmarkReflect(b *testing.B) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"flat_3_fields", reflect.TypeOf(benchFlat{})},
		{"invoice_nested_and_slice", reflect.TypeOf(benchInvoice{})},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s, err := Reflect(c.typ)
				if err != nil {
					b.Fatalf("Reflect: %v", err)
				}
				benchSchema = s
			}
		})
	}
}

// BenchmarkCacheOf measures the warm path, which is what a long-lived client
// actually runs. The cache is warmed before the timer starts, so what is
// measured is a mutex and a map lookup and nothing else — and the difference
// from BenchmarkReflect above is the whole justification for the type existing.
func BenchmarkCacheOf(b *testing.B) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"flat_3_fields", reflect.TypeOf(benchFlat{})},
		{"invoice_nested_and_slice", reflect.TypeOf(benchInvoice{})},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			var cache Cache
			if _, err := cache.Of(c.typ); err != nil {
				b.Fatalf("warming the cache: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s, err := cache.Of(c.typ)
				if err != nil {
					b.Fatalf("Cache.Of: %v", err)
				}
				benchSchema = s
			}
		})
	}
}

// BenchmarkCacheOfParallel measures the warm path under contention, because the
// lock is held across the reflection rather than only around the map. That is a
// deliberate choice — it buys pointer identity for a given type — and this is
// where its price is visible: concurrent extractions of the same type serialise
// on this mutex, briefly, before every one of them goes on to a network call.
func BenchmarkCacheOfParallel(b *testing.B) {
	var cache Cache
	typ := reflect.TypeOf(benchInvoice{})
	if _, err := cache.Of(typ); err != nil {
		b.Fatalf("warming the cache: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	// The result is used rather than stored: writing to the package-level sink
	// from several goroutines would be a data race in a file whose whole
	// subject is a lock.
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s, err := cache.Of(typ)
			if err != nil {
				b.Fatalf("Cache.Of: %v", err)
			}
			if len(s.Fields) == 0 {
				b.Fatal("the cache returned a schema with no fields")
			}
		}
	})
}
