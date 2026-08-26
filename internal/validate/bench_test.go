package validate

import (
	"fmt"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// Validation runs once per field, after the model has replied and before
// anything is scored. It does no I/O, so it is pure computation on short
// strings — but it is the stage with the widest spread between its cases, and
// the spread is the reason for a sub-benchmark per format rather than one
// number for the stage.
//
// A currency code is three letters checked against a table. A date is a
// sequence of layouts tried in order until one parses, and the layouts that
// parse last cost the most. An IBAN is a modulo-97 checksum over up to
// thirty-four characters rearranged first. A phone number is a country lookup
// followed by a rebuild into E.164. These are not the same work, and a schema
// full of dates does not cost what a schema full of enums costs.
//
// The failure paths are measured alongside the successes, because a validation
// failure is data rather than an error here (ADR-0004) and a document with an
// illegible field is the normal case, not the exception. If failing were much
// more expensive than succeeding, a batch of poor scans would cost more to
// process than a batch of clean ones for a reason nobody had chosen.

// Kept reachable so the compiler cannot delete the validation.
var (
	benchResult Result
	benchCross  []CrossFieldResult
)

// BenchmarkFieldFormat measures one field per format, valid input, which is the
// per-format price list for the stage.
func BenchmarkFieldFormat(b *testing.B) {
	v := New(WithDateOrder(DayFirst))

	cases := []struct {
		name  string
		field schema.Field
		raw   any
	}{
		{"date", field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDate)), "14/03/2026"},
		{"datetime", field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDatetime)), "2026-03-14T09:30:00Z"},
		{"email", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatEmail)), "Accounts@Nakawa-Stationers.example"},
		{"phone", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatPhone)), "+256 414 259 000"},
		{"currency", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatCurrency)), "ugx"},
		{"iban", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatIBAN)), "GB33 BUKB 2020 1555 5555 55"},
		{"swift", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatSWIFT)), "buk bgb 22 xxx"},
		{"uuid", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatUUID)), "{123E4567-E89B-12D3-A456-426614174000}"},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			// Checked once, outside the loop. A case that has stopped
			// validating measures the refusal path under the name of the
			// success path, which is worse than measuring nothing.
			if r := v.Field(c.field, c.raw); !r.Valid() {
				b.Fatalf("%s no longer validates: %+v", c.name, r.Rules)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchResult = v.Field(c.field, c.raw)
			}
		})
	}
}

// BenchmarkFieldFormatInvalid measures the same formats over input that fails,
// which is the path a poor scan takes. The checksummed formats are the ones to
// watch: a wrong IBAN is rejected by the checksum, which runs to the end, so
// there is no early exit to be had.
func BenchmarkFieldFormatInvalid(b *testing.B) {
	v := New(WithDateOrder(DayFirst))

	cases := []struct {
		name  string
		field schema.Field
		raw   any
	}{
		{"date", field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDate)), "the fourteenth"},
		{"email", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatEmail)), "accounts at nakawa"},
		{"phone", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatPhone)), "call the office"},
		{"currency", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatCurrency)), "shillings"},
		{"iban", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatIBAN)), "GB33 BUKB 2020 1555 5555 54"},
		{"uuid", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatUUID)), "123e4567-e89b-12d3-a456"},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			if r := v.Field(c.field, c.raw); r.Valid() {
				b.Fatalf("%s now validates, so this case no longer measures the failure path", c.name)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchResult = v.Field(c.field, c.raw)
			}
		})
	}
}

// BenchmarkFieldKind measures the conversions with no format attached, so that
// the cost of a format can be read against the cost of the plain field it was
// put on. Most fields in most schemas are one of these.
func BenchmarkFieldKind(b *testing.B) {
	v := New()

	cases := []struct {
		name  string
		field schema.Field
		raw   any
	}{
		{"string", field(schema.KindString, "", rule(schema.RuleRequired, "")), "Nakawa Stationers Limited"},
		{"number_from_json", field(schema.KindFloat, float64(0)), 1463200.0},
		{"number_from_text", field(schema.KindFloat, float64(0)), "1,463,200.00"},
		{"int", field(schema.KindInt, 0), "40"},
		{"bool", field(schema.KindBool, false), "yes"},
		{"enum", field(schema.KindString, "", rule(schema.RuleEnum, "paid|unpaid|overdue")), "overdue"},
		{"min_max", field(schema.KindFloat, float64(0), rule(schema.RuleMin, "0"), rule(schema.RuleMax, "10000000")), 1463200.0},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			if r := v.Field(c.field, c.raw); !r.Valid() {
				b.Fatalf("%s no longer validates: %+v", c.name, r.Rules)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchResult = v.Field(c.field, c.raw)
			}
		})
	}
}

// BenchmarkCrossField measures the rules that run over the whole set of fields
// rather than over one. A sum over line items is the common case and the one
// that scales with the document: an invoice with two hundred lines checks two
// hundred values against a total.
func BenchmarkCrossField(b *testing.B) {
	for _, items := range []int{4, 40, 200} {
		items := items
		b.Run(fmt.Sprintf("items_%d", items), func(b *testing.B) {
			f := Fields{"subtotal": 0.0, "tax": 223200.0, "total": 0.0}
			var subtotal float64
			for i := 0; i < items; i++ {
				amount := float64(500000 + i*13)
				f[ElementKey("items", i, "amount")] = amount
				subtotal += amount
			}
			f["subtotal"] = subtotal
			f["total"] = subtotal + 223200.0

			v := New(WithCrossFieldRules(
				SumItems("subtotal", "items", DefaultTolerance, "amount"),
				Sum("total", DefaultTolerance, "subtotal", "tax"),
			))

			for _, r := range v.CrossField(f) {
				if !r.Passed {
					b.Fatalf("rule %q fails on the fixture, so the failure path is what is measured", r.Name)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchCross = v.CrossField(f)
			}
		})
	}
}
