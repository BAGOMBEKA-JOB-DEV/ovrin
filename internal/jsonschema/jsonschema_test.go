package jsonschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// invoiceSchema is the representative case: a nested object, a slice of
// objects, a date, an enum, bounds on three different kinds and a pointer
// field. It is the fixture the golden bytes below are asserted against.
func invoiceSchema() schema.Schema {
	return schema.Schema{
		Name: "Invoice",
		Fields: []schema.Field{
			{
				Key:         "number",
				GoName:      "Number",
				Description: "invoice number as printed by the vendor",
				Kind:        schema.KindString,
				Type:        reflect.TypeOf(""),
				Rules: []schema.Rule{
					{Name: schema.RuleRequired},
					{Name: schema.RuleMax, Value: "32"},
				},
			},
			{
				Key:         "issued",
				GoName:      "Issued",
				Description: "date the invoice was issued, not the due date",
				Kind:        schema.KindTime,
				Rules:       []schema.Rule{{Name: schema.RuleFormat, Value: "date"}},
			},
			{
				Key:         "vendor",
				GoName:      "Vendor",
				Description: "vendor information",
				Kind:        schema.KindObject,
				Fields: []schema.Field{
					{
						Key:         "name",
						GoName:      "Name",
						Description: "registered company name",
						Kind:        schema.KindString,
						Rules: []schema.Rule{
							{Name: schema.RuleRequired},
							{Name: schema.RuleMin, Value: "1"},
						},
					},
					{
						Key:         "tax_id",
						GoName:      "TaxID",
						Description: "tax identification number",
						Kind:        schema.KindString,
						Optional:    true,
					},
				},
			},
			{
				Key:         "items",
				GoName:      "Items",
				Description: "invoice line items",
				Kind:        schema.KindArray,
				Rules: []schema.Rule{
					{Name: schema.RuleMin, Value: "1"},
					{Name: schema.RuleMax, Value: "200"},
				},
				Elem: &schema.Field{
					Key:    "items",
					GoName: "Item",
					Kind:   schema.KindObject,
					Fields: []schema.Field{
						{
							Key:         "description",
							GoName:      "Description",
							Description: "item description",
							Kind:        schema.KindString,
						},
						{
							Key:         "quantity",
							GoName:      "Quantity",
							Description: "quantity",
							Kind:        schema.KindInt,
							Rules:       []schema.Rule{{Name: schema.RuleMin, Value: "0"}},
						},
						{
							Key:         "unit_price",
							GoName:      "UnitPrice",
							Description: "price per unit excluding tax",
							Kind:        schema.KindFloat,
							Rules:       []schema.Rule{{Name: schema.RuleMin, Value: "0"}},
						},
					},
				},
			},
			{
				Key:         "currency",
				GoName:      "Currency",
				Description: "currency code",
				Kind:        schema.KindString,
				Rules: []schema.Rule{
					{Name: schema.RuleRequired},
					{Name: schema.RuleEnum, Value: "UGX|USD|EUR|GBP"},
				},
			},
			{
				Key:         "paid",
				GoName:      "Paid",
				Description: "whether the invoice is marked paid",
				Kind:        schema.KindBool,
			},
			{
				Key:         "total",
				GoName:      "Total",
				Description: "total amount including tax",
				Kind:        schema.KindFloat,
				Rules: []schema.Rule{
					{Name: schema.RuleRequired},
					{Name: schema.RuleMin, Value: "0"},
				},
			},
		},
	}
}

// invoiceGolden is the exact output for invoiceSchema, written by hand and
// broken across lines by concatenation only — there is no whitespace in it.
// Asserting exact bytes rather than "the JSON is equivalent" is the point: the
// dialect, the member order and the property order are all things a provider or
// a prompt cache can see, and an equivalence check would pass while every one
// of them regressed.
const invoiceGolden = `{` +
	`"type":"object",` +
	`"title":"Invoice",` +
	`"properties":{` +
	`"number":{"type":["string","null"],"description":"invoice number as printed by the vendor","maxLength":32},` +
	`"issued":{"type":["string","null"],"description":"date the invoice was issued, not the due date","format":"date"},` +
	`"vendor":{"type":["object","null"],"description":"vendor information","properties":{` +
	`"name":{"type":["string","null"],"description":"registered company name","minLength":1},` +
	`"tax_id":{"type":["string","null"],"description":"tax identification number"}` +
	`},"required":["name","tax_id"],"additionalProperties":false},` +
	`"items":{"type":["array","null"],"description":"invoice line items","minItems":1,"maxItems":200,"items":{` +
	`"type":"object","properties":{` +
	`"description":{"type":["string","null"],"description":"item description"},` +
	`"quantity":{"type":["integer","null"],"description":"quantity","minimum":0},` +
	`"unit_price":{"type":["number","null"],"description":"price per unit excluding tax","minimum":0}` +
	`},"required":["description","quantity","unit_price"],"additionalProperties":false}},` +
	`"currency":{"type":["string","null"],"description":"currency code","enum":["UGX","USD","EUR","GBP",null]},` +
	`"paid":{"type":["boolean","null"],"description":"whether the invoice is marked paid"},` +
	`"total":{"type":["number","null"],"description":"total amount including tax","minimum":0}` +
	`},"required":["number","issued","vendor","items","currency","paid","total"],` +
	`"additionalProperties":false}`

func TestMarshalGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   schema.Schema
		want string
	}{
		{
			name: "nested object and slice of objects with rules on three kinds",
			in:   invoiceSchema(),
			want: invoiceGolden,
		},
		{
			name: "single string field",
			in: schema.Schema{
				Name: "Note",
				Fields: []schema.Field{{
					Key:         "body",
					GoName:      "Body",
					Description: "the note text",
					Kind:        schema.KindString,
				}},
			},
			want: `{"type":"object","title":"Note","properties":{` +
				`"body":{"type":["string","null"],"description":"the note text"}` +
				`},"required":["body"],"additionalProperties":false}`,
		},
		{
			name: "unnamed schema omits title",
			in: schema.Schema{Fields: []schema.Field{{
				Key: "body", GoName: "Body", Kind: schema.KindString,
			}}},
			want: `{"type":"object","properties":{"body":{"type":["string","null"]}},` +
				`"required":["body"],"additionalProperties":false}`,
		},
		{
			name: "schema with no fields still emits an empty required array",
			in:   schema.Schema{Name: "Empty"},
			want: `{"type":"object","title":"Empty","properties":{},"required":[],` +
				`"additionalProperties":false}`,
		},
		{
			name: "angle brackets and ampersands survive unescaped at every depth",
			in: schema.Schema{
				Name: "Terms",
				Fields: []schema.Field{
					{Key: "top", GoName: "Top", Kind: schema.KindString,
						Description: `charges < 5% & "fees"`},
					{Key: "nested", GoName: "Nested", Kind: schema.KindObject,
						Fields: []schema.Field{{Key: "inner", GoName: "Inner",
							Kind: schema.KindString, Description: `charges < 5% & "fees"`}}},
				},
			},
			want: `{"type":"object","title":"Terms","properties":{` +
				`"top":{"type":["string","null"],"description":"charges < 5% & \"fees\""},` +
				`"nested":{"type":["object","null"],"properties":{` +
				`"inner":{"type":["string","null"],"description":"charges < 5% & \"fees\""}` +
				`},"required":["inner"],"additionalProperties":false}` +
				`},"required":["top","nested"],"additionalProperties":false}`,
		},
		{
			name: "the same type used twice is expanded twice rather than referenced",
			in: schema.Schema{
				Name: "Transfer",
				Fields: []schema.Field{
					partyField("payer", "Payer", "who is paying"),
					partyField("payee", "Payee", "who is being paid"),
				},
			},
			want: `{"type":"object","title":"Transfer","properties":{` +
				`"payer":{"type":["object","null"],"description":"who is paying","properties":{` +
				`"name":{"type":["string","null"],"description":"legal name"}` +
				`},"required":["name"],"additionalProperties":false},` +
				`"payee":{"type":["object","null"],"description":"who is being paid","properties":{` +
				`"name":{"type":["string","null"],"description":"legal name"}` +
				`},"required":["name"],"additionalProperties":false}` +
				`},"required":["payer","payee"],"additionalProperties":false}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal returned an unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Marshal produced the wrong bytes\n got: %s\nwant: %s\n\nindented got:\n%s",
					got, tc.want, indent(t, got))
			}
		})
	}
}

// partyField builds two structurally identical nested objects, so the golden
// above can show that a repeated type is written out twice. $ref would be the
// obvious saving and is exactly what Gemini will not accept.
func partyField(key, goName, description string) schema.Field {
	return schema.Field{
		Key:         key,
		GoName:      goName,
		Description: description,
		Kind:        schema.KindObject,
		Fields: []schema.Field{{
			Key:         "name",
			GoName:      "Name",
			Description: "legal name",
			Kind:        schema.KindString,
		}},
	}
}

func TestMarshalFieldMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field schema.Field
		want  string
	}{
		{
			name:  "string becomes a nullable string",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString},
			want:  `{"type":["string","null"]}`,
		},
		{
			name:  "int becomes integer",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindInt},
			want:  `{"type":["integer","null"]}`,
		},
		{
			name:  "float becomes number",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindFloat},
			want:  `{"type":["number","null"]}`,
		},
		{
			name:  "bool becomes boolean",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindBool},
			want:  `{"type":["boolean","null"]}`,
		},
		{
			name: "time with format=date becomes a date string",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindTime,
				Rules: []schema.Rule{{Name: schema.RuleFormat, Value: "date"}}},
			want: `{"type":["string","null"],"format":"date"}`,
		},
		{
			name: "time with format=datetime becomes a date-time string",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindTime,
				Rules: []schema.Rule{{Name: schema.RuleFormat, Value: "datetime"}}},
			want: `{"type":["string","null"],"format":"date-time"}`,
		},
		{
			name:  "time with no format rule defaults to date-time",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindTime},
			want:  `{"type":["string","null"],"format":"date-time"}`,
		},
		{
			name: "description is carried verbatim",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString,
				Description: "the vendor's registered company name"},
			want: `{"type":["string","null"],"description":"the vendor's registered company name"}`,
		},
		{
			name: "numeric min and max become minimum and maximum",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindFloat,
				Rules: []schema.Rule{
					{Name: schema.RuleMin, Value: "0"},
					{Name: schema.RuleMax, Value: "1000.5"},
				}},
			want: `{"type":["number","null"],"minimum":0,"maximum":1000.5}`,
		},
		{
			name: "bounds are emitted in canonical order regardless of tag order",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindInt,
				Rules: []schema.Rule{
					{Name: schema.RuleMax, Value: "9"},
					{Name: schema.RuleMin, Value: "2"},
				}},
			want: `{"type":["integer","null"],"minimum":2,"maximum":9}`,
		},
		{
			name: "string min and max become minLength and maxLength",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{
					{Name: schema.RuleMin, Value: "2"},
					{Name: schema.RuleMax, Value: "8"},
				}},
			want: `{"type":["string","null"],"minLength":2,"maxLength":8}`,
		},
		{
			name: "array min and max become minItems and maxItems",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindArray,
				Rules: []schema.Rule{
					{Name: schema.RuleMin, Value: "1"},
					{Name: schema.RuleMax, Value: "3"},
				},
				Elem: &schema.Field{Key: "f", GoName: "Elem", Kind: schema.KindString}},
			want: `{"type":["array","null"],"minItems":1,"maxItems":3,"items":{"type":"string"}}`,
		},
		{
			name: "enum on a string gains null so the nullable union stays satisfiable",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{{Name: schema.RuleEnum, Value: "UGX|USD"}}},
			want: `{"type":["string","null"],"enum":["UGX","USD",null]}`,
		},
		{
			name: "enum on a non-string kind is left to internal/validate",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindInt,
				Rules: []schema.Rule{{Name: schema.RuleEnum, Value: "1|2"}}},
			want: `{"type":["integer","null"]}`,
		},
		{
			name: "the required rule is not mirrored into the node",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{{Name: schema.RuleRequired}}},
			want: `{"type":["string","null"]}`,
		},
		{
			name: "a required non-pointer field is still nullable",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindFloat,
				Rules: []schema.Rule{{Name: schema.RuleRequired}}},
			want: `{"type":["number","null"]}`,
		},
		{
			name: "a pointer field looks the same as a value field",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString,
				Optional: true},
			want: `{"type":["string","null"]}`,
		},
		{
			name: "a string format with no portable equivalent is not emitted",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{{Name: schema.RuleFormat, Value: "email"}}},
			want: `{"type":["string","null"]}`,
		},
		{
			name: "a rule outside the vocabulary is not emitted even when JSON Schema has that keyword",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{{Name: "pattern", Value: "^[0-9]+$"}}},
			want: `{"type":["string","null"]}`,
		},
		{
			name: "bounds on a kind with no equivalent are not emitted",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindTime,
				Rules: []schema.Rule{
					{Name: schema.RuleFormat, Value: "date"},
					{Name: schema.RuleMin, Value: "2020-01-01"},
				}},
			want: `{"type":["string","null"],"format":"date"}`,
		},
		{
			name: "slice elements are not nullable",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindArray,
				Elem: &schema.Field{Key: "f", GoName: "Elem", Kind: schema.KindString}},
			want: `{"type":["array","null"],"items":{"type":"string"}}`,
		},
		{
			name: "elements of a slice of pointers are nullable",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindArray,
				Elem: &schema.Field{Key: "f", GoName: "Elem", Kind: schema.KindString, Optional: true}},
			want: `{"type":["array","null"],"items":{"type":["string","null"]}}`,
		},
		{
			name: "element rules and descriptions reach the items node",
			field: schema.Field{Key: "f", GoName: "F", Kind: schema.KindArray,
				Elem: &schema.Field{Key: "f", GoName: "Elem", Kind: schema.KindInt,
					Description: "a page number",
					Rules:       []schema.Rule{{Name: schema.RuleMin, Value: "1"}}}},
			want: `{"type":["array","null"],"items":{"type":"integer","description":"a page number","minimum":1}}`,
		},
		{
			name:  "a nested key path contributes only its leaf segment",
			field: schema.Field{Key: "vendor.name", GoName: "Name", Kind: schema.KindString},
			want:  `{"type":["string","null"]}`,
		},
		{
			name:  "a slice element key path contributes only its leaf segment",
			field: schema.Field{Key: "items[0].unit_price", GoName: "UnitPrice", Kind: schema.KindFloat},
			want:  `{"type":["number","null"]}`,
		},
		{
			name:  "an index on a key of its own is stripped",
			field: schema.Field{Key: "items[0]", GoName: "Items", Kind: schema.KindString},
			want:  `{"type":["string","null"]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Marshal(schema.Schema{Fields: []schema.Field{tc.field}})
			if err != nil {
				t.Fatalf("Marshal returned an unexpected error: %v", err)
			}
			node := propertyOf(t, got, propertyNameOrFail(t, tc.field))
			if node != tc.want {
				t.Errorf("property node is wrong\n got: %s\nwant: %s", node, tc.want)
			}
		})
	}
}

func TestMarshalErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   schema.Schema
		want string // a substring the message must name
	}{
		{
			name: "kind is unset",
			in:   schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F"}}},
			want: `"F"`,
		},
		{
			name: "kind is not one this package knows",
			in:   schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.Kind("map")}}},
			want: `"map"`,
		},
		{
			name: "array with no element type",
			in:   schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.KindArray}}},
			want: "element type",
		},
		{
			name: "empty key",
			in:   schema.Schema{Fields: []schema.Field{{GoName: "F", Kind: schema.KindString}}},
			want: "empty key",
		},
		{
			name: "two fields whose keys share a leaf",
			in: schema.Schema{Fields: []schema.Field{
				{Key: "name", GoName: "Name", Kind: schema.KindString},
				{Key: "vendor.name", GoName: "VendorName", Kind: schema.KindString},
			}},
			want: "duplicate property name",
		},
		{
			name: "numeric bound that is not a number",
			in: schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.KindFloat,
				Rules: []schema.Rule{{Name: schema.RuleMin, Value: "lots"}}}}},
			want: "is not a number",
		},
		{
			name: "numeric bound with no JSON representation",
			in: schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.KindFloat,
				Rules: []schema.Rule{{Name: schema.RuleMax, Value: "Inf"}}}}},
			want: "no JSON representation",
		},
		{
			name: "length bound that is not whole",
			in: schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{{Name: schema.RuleMin, Value: "1.5"}}}}},
			want: "is not a whole number",
		},
		{
			name: "negative length bound",
			in: schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{{Name: schema.RuleMax, Value: "-1"}}}}},
			want: "is negative",
		},
		{
			name: "format on a time field that is not a date format",
			in: schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.KindTime,
				Rules: []schema.Rule{{Name: schema.RuleFormat, Value: "email"}}}}},
			want: "not a date format",
		},
		{
			name: "enum with an empty alternative",
			in: schema.Schema{Fields: []schema.Field{{Key: "f", GoName: "F", Kind: schema.KindString,
				Rules: []schema.Rule{{Name: schema.RuleEnum, Value: "UGX||USD"}}}}},
			want: "empty alternative",
		},
		{
			name: "nesting past the depth bound through objects",
			in:   deepSchema(maxDepth + 2),
			want: "nesting deeper than",
		},
		{
			name: "nesting past the depth bound through slices",
			in:   deepSliceSchema(maxDepth + 2),
			want: "nesting deeper than",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Marshal(tc.in)
			if err == nil {
				t.Fatalf("Marshal succeeded, want an error; produced %s", got)
			}
			if !errors.Is(err, ErrUnrepresentable) {
				t.Errorf("error is not ErrUnrepresentable: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not name %q: %v", tc.want, err)
			}
		})
	}
}

// deepSchema builds a chain of nested objects n levels deep, to exercise the
// depth bound. internal/schema rejects the recursive type that would produce
// this, so nothing but a hand-built Schema gets here.
func deepSchema(n int) schema.Schema {
	leaf := schema.Field{Key: "leaf", GoName: "Leaf", Kind: schema.KindString}
	for i := 0; i < n; i++ {
		leaf = schema.Field{
			Key:    "next",
			GoName: "Next",
			Kind:   schema.KindObject,
			Fields: []schema.Field{leaf},
		}
	}
	return schema.Schema{Name: "Deep", Fields: []schema.Field{leaf}}
}

// deepSliceSchema builds a chain of nested slices n levels deep. Slices reach
// the bound through fieldNode rather than through setObjectMembers, and a bound
// that only held for one of the two recursions would not be a bound.
func deepSliceSchema(n int) schema.Schema {
	elem := schema.Field{Key: "leaf", GoName: "Leaf", Kind: schema.KindString}
	for i := 0; i < n; i++ {
		inner := elem
		elem = schema.Field{
			Key:    "rows",
			GoName: "Rows",
			Kind:   schema.KindArray,
			Elem:   &inner,
		}
	}
	return schema.Schema{Name: "Deep", Fields: []schema.Field{elem}}
}

func TestMarshalIsByteIdenticalAcrossCalls(t *testing.T) {
	t.Parallel()

	// Prompt caching at every provider keys on the exact request bytes, and the
	// golden tests above would still pass if only the first call were stable.
	// Repeating the call is the only way to catch map iteration order leaking
	// into the output, which is the failure this guards.
	first, err := Marshal(invoiceSchema())
	if err != nil {
		t.Fatalf("Marshal returned an unexpected error: %v", err)
	}
	for i := 0; i < 200; i++ {
		got, err := Marshal(invoiceSchema())
		if err != nil {
			t.Fatalf("Marshal returned an unexpected error on call %d: %v", i, err)
		}
		if !bytes.Equal(got, first) {
			t.Fatalf("call %d differed\n got: %s\nfirst: %s", i, got, first)
		}
	}
}

func TestMarshalIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	// Marshal is documented as concurrency-safe (rule §5.1) and the pipeline
	// will call it from page workers. Under -race this fails loudly if any
	// state is ever added to the package.
	const goroutines = 16
	results := make(chan string, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			out, err := Marshal(invoiceSchema())
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- string(out)
		}()
	}
	for i := 0; i < goroutines; i++ {
		if got := <-results; got != invoiceGolden {
			t.Errorf("goroutine %d produced different bytes: %s", i, got)
		}
	}
}

func TestMarshalMeetsTheDialectInvariants(t *testing.T) {
	t.Parallel()

	// These are the properties a provider actually checks, asserted over the
	// whole document rather than by reading a golden. A golden proves one
	// schema is right today; this proves the shape is right for any schema that
	// reaches it, which is what stops an ErrBadRequest at generate time.
	tests := []struct {
		name string
		in   schema.Schema
	}{
		{name: "invoice with nesting and slices", in: invoiceSchema()},
		{name: "repeated type", in: schema.Schema{Name: "Transfer", Fields: []schema.Field{
			partyField("payer", "Payer", "who is paying"),
			partyField("payee", "Payee", "who is being paid"),
		}}},
		{name: "slice of slices", in: schema.Schema{Name: "Grid", Fields: []schema.Field{{
			Key: "rows", GoName: "Rows", Kind: schema.KindArray,
			Elem: &schema.Field{Key: "rows", GoName: "Row", Kind: schema.KindArray,
				Elem: &schema.Field{Key: "rows", GoName: "Cell", Kind: schema.KindString}},
		}}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal returned an unexpected error: %v", err)
			}
			if !json.Valid(got) {
				t.Fatalf("output is not valid JSON: %s", got)
			}
			checkNode(t, "$", got)
		})
	}
}

// checkNode asserts the dialect invariants on one node and recurses.
func checkNode(t *testing.T, path string, raw json.RawMessage) {
	t.Helper()

	keys, values, err := decodeObject(raw)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	byKey := make(map[string]json.RawMessage, len(keys))
	for i, key := range keys {
		// Every one of these is accepted by at least one provider and rejected
		// by at least one other, which is exactly why none is emitted.
		switch key {
		case "$ref", "$defs", "definitions", "$schema", "$id", "oneOf", "anyOf", "allOf", "not":
			t.Errorf("%s: emitted %q, which is outside the portable dialect", path, key)
		}
		byKey[key] = values[i]
	}

	if _, ok := byKey["type"]; !ok {
		t.Errorf("%s: node has no type", path)
	}

	properties, isObject := byKey["properties"]
	if !isObject {
		if items, ok := byKey["items"]; ok {
			checkNode(t, path+"[]", items)
		}
		return
	}

	if additional := string(byKey["additionalProperties"]); additional != "false" {
		t.Errorf("%s: additionalProperties is %q, want false", path, additional)
	}

	names, nodes, err := decodeObject(properties)
	if err != nil {
		t.Fatalf("%s: properties: %v", path, err)
	}
	var required []string
	if err := json.Unmarshal(byKey["required"], &required); err != nil {
		t.Fatalf("%s: required: %v", path, err)
	}
	if len(names) == 0 && required == nil {
		t.Errorf("%s: required is null, want an empty array", path)
	}
	if !reflect.DeepEqual(names, required) {
		t.Errorf("%s: required is %v, want every property in declaration order %v", path, required, names)
	}
	for i, name := range names {
		checkNode(t, path+"."+name, nodes[i])
	}
}

// decodeObject returns a JSON object's members in document order.
//
// encoding/json into a map would lose the order, which is half of what these
// tests are asserting, so the decoder is driven by hand.
func decodeObject(raw json.RawMessage) ([]string, []json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("reading opening token: %w", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, nil, fmt.Errorf("expected an object, got %v", token)
	}

	var (
		keys   []string
		values []json.RawMessage
	)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("reading a member name: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, nil, fmt.Errorf("member name %v is not a string", token)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, fmt.Errorf("reading member %q: %w", key, err)
		}
		keys = append(keys, key)
		values = append(values, value)
	}
	return keys, values, nil
}

// propertyOf returns the raw bytes of one property node of a marshalled schema.
func propertyOf(t *testing.T, document []byte, name string) string {
	t.Helper()

	keys, values, err := decodeObject(document)
	if err != nil {
		t.Fatalf("decoding the document: %v", err)
	}
	for i, key := range keys {
		if key != "properties" {
			continue
		}
		names, nodes, err := decodeObject(values[i])
		if err != nil {
			t.Fatalf("decoding properties: %v", err)
		}
		for j, candidate := range names {
			if candidate == name {
				return string(nodes[j])
			}
		}
	}
	t.Fatalf("no property named %q in %s", name, document)
	return ""
}

func propertyNameOrFail(t *testing.T, f schema.Field) string {
	t.Helper()

	name, err := propertyName(f)
	if err != nil {
		t.Fatalf("propertyName(%q): %v", f.Key, err)
	}
	return name
}

// indent makes a failing golden comparison readable. It is only ever called on
// a failure path.
func indent(t *testing.T, document []byte) string {
	t.Helper()

	var buf bytes.Buffer
	if err := json.Indent(&buf, document, "", "  "); err != nil {
		return string(document)
	}
	return buf.String()
}
