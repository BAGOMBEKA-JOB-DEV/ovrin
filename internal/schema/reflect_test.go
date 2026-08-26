package schema

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The schema from docs/schema.md, reproduced exactly so that a change to the
// documented example fails here rather than in somebody's application.

type invoice struct {
	Number   string    `ovrin:"invoice number,required"`
	Issued   time.Time `ovrin:"date the invoice was issued,format=date"`
	Vendor   vendor    `ovrin:"vendor information"`
	Items    []item    `ovrin:"invoice line items"`
	Currency string    `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
	Total    float64   `ovrin:"total amount including tax,required,min=0"`
	Internal string    // no tag: not part of the schema
	Skipped  string    `ovrin:"-"`
	JSONOnly string    `json:"json_only"`
}

type vendor struct {
	Name    string `ovrin:"registered company name,required"`
	Address string `ovrin:"full postal address"`
	TaxID   string `ovrin:"tax identification number"`
}

type item struct {
	Description string  `ovrin:"item description"`
	Quantity    int     `ovrin:"quantity,min=0"`
	UnitPrice   float64 `ovrin:"price per unit excluding tax,min=0"`
}

// flatten renders a schema as one line per field, depth first, so a whole
// expectation reads as a list instead of forty separate assertions.
func flatten(fields []Field) []string {
	var out []string
	for _, f := range fields {
		line := f.Key + " " + f.Kind.String()
		if f.Optional {
			line += " optional"
		}
		for _, r := range f.Rules {
			line += " " + r.Name
			if r.Value != "" {
				line += "=" + r.Value
			}
		}
		out = append(out, line)
		if f.Elem != nil {
			out = append(out, flatten([]Field{*f.Elem})...)
		}
		out = append(out, flatten(f.Fields)...)
	}
	return out
}

func fieldByKey(t *testing.T, fields []Field, key string) Field {
	t.Helper()
	for _, f := range fields {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("no field with key %q in %v", key, flatten(fields))
	return Field{}
}

func compareLines(t *testing.T, got, want []string) {
	t.Helper()
	for i := range want {
		if i >= len(got) {
			t.Errorf("missing field %q", want[i])
			continue
		}
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
	for i := len(want); i < len(got); i++ {
		t.Errorf("unexpected field %q", got[i])
	}
}

func TestReflectDocumentedInvoice(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(invoice{}))
	if err != nil {
		t.Fatalf("Reflect(invoice) = %v, want no error", err)
	}
	if s.Name != "invoice" {
		t.Errorf("Name = %q, want %q", s.Name, "invoice")
	}

	// Declaration order, the keys from docs/schema.md, and the rules in tag
	// order. Untagged, json-tagged and "-" fields are absent.
	want := []string{
		"number string required",
		"issued time format=date",
		"vendor object",
		"vendor.name string required",
		"vendor.address string",
		"vendor.tax_id string",
		"items array",
		"items[] object",
		"items[].description string",
		"items[].quantity int min=0",
		"items[].unit_price float min=0",
		"currency string required enum=UGX|USD|EUR|GBP",
		"total float required min=0",
	}
	compareLines(t, flatten(s.Fields), want)
}

func TestReflectDescriptions(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(invoice{}))
	if err != nil {
		t.Fatalf("Reflect(invoice) = %v, want no error", err)
	}

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"description of a scalar", "number", "invoice number"},
		{"description of a nested object describes the whole object", "vendor", "vendor information"},
		{"description of a slice describes the slice", "items", "invoice line items"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := fieldByKey(t, s.Fields, tt.key).Description; got != tt.want {
				t.Errorf("Description of %q = %q, want %q", tt.key, got, tt.want)
			}
		})
	}

	// A slice element has no description of its own: the grammar has nowhere
	// to write one, and the slice's description covers the whole list.
	if got := fieldByKey(t, s.Fields, "items").Elem.Description; got != "" {
		t.Errorf("element description = %q, want empty", got)
	}
}

func TestReflectGoNamesAndTypes(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(invoice{}))
	if err != nil {
		t.Fatalf("Reflect(invoice) = %v, want no error", err)
	}
	total := fieldByKey(t, s.Fields, "total")
	if total.GoName != "Total" {
		t.Errorf("GoName = %q, want %q", total.GoName, "Total")
	}
	if total.Type != reflect.TypeOf(float64(0)) {
		t.Errorf("Type = %v, want float64", total.Type)
	}
	items := fieldByKey(t, s.Fields, "items")
	if items.Type != reflect.TypeOf([]item{}) {
		t.Errorf("slice Type = %v, want []item", items.Type)
	}
	if items.Elem.Type != reflect.TypeOf(item{}) {
		t.Errorf("element Type = %v, want item", items.Elem.Type)
	}
	if items.Elem.GoName != "Items" {
		t.Errorf("element GoName = %q, want %q: an element points at the Go field it came from", items.Elem.GoName, "Items")
	}
}

type everyKind struct {
	Str       string     `ovrin:"a string"`
	Int       int        `ovrin:"an int"`
	Int8      int8       `ovrin:"an int8"`
	Int16     int16      `ovrin:"an int16"`
	Int32     int32      `ovrin:"an int32"`
	Int64     int64      `ovrin:"an int64"`
	Uint      uint       `ovrin:"a uint"`
	Uint8     uint8      `ovrin:"a uint8"`
	Uint16    uint16     `ovrin:"a uint16"`
	Uint32    uint32     `ovrin:"a uint32"`
	Uint64    uint64     `ovrin:"a uint64"`
	Float32   float32    `ovrin:"a float32"`
	Float64   float64    `ovrin:"a float64"`
	Bool      bool       `ovrin:"a bool"`
	Time      time.Time  `ovrin:"a time,format=datetime"`
	Nested    vendor     `ovrin:"a nested struct"`
	Slice     []string   `ovrin:"a slice"`
	PtrStr    *string    `ovrin:"an optional string"`
	PtrTime   *time.Time `ovrin:"an optional time,format=date"`
	PtrNested *vendor    `ovrin:"an optional nested struct"`
	PtrSlice  *[]string  `ovrin:"an optional slice"`
	Named     currency   `ovrin:"a named string type"`
}

type currency string

func TestReflectSupportedTypes(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(everyKind{}))
	if err != nil {
		t.Fatalf("Reflect(everyKind) = %v, want no error", err)
	}
	byGoName := map[string]Field{}
	for _, f := range s.Fields {
		byGoName[f.GoName] = f
	}

	tests := []struct {
		name         string
		goName       string
		wantKind     Kind
		wantOptional bool
	}{
		{"string", "Str", KindString, false},
		{"int", "Int", KindInt, false},
		{"int8", "Int8", KindInt, false},
		{"int16", "Int16", KindInt, false},
		{"int32", "Int32", KindInt, false},
		{"int64", "Int64", KindInt, false},
		{"uint", "Uint", KindInt, false},
		{"uint8", "Uint8", KindInt, false},
		{"uint16", "Uint16", KindInt, false},
		{"uint32", "Uint32", KindInt, false},
		{"uint64", "Uint64", KindInt, false},
		{"float32", "Float32", KindFloat, false},
		{"float64", "Float64", KindFloat, false},
		{"bool", "Bool", KindBool, false},
		{"time.Time", "Time", KindTime, false},
		{"nested struct", "Nested", KindObject, false},
		{"slice", "Slice", KindArray, false},
		{"pointer to string is optional", "PtrStr", KindString, true},
		{"pointer to time is optional", "PtrTime", KindTime, true},
		{"pointer to struct is optional", "PtrNested", KindObject, true},
		{"pointer to slice is optional", "PtrSlice", KindArray, true},
		{"named string type", "Named", KindString, false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, ok := byGoName[tt.goName]
			if !ok {
				t.Fatalf("no field %s in schema", tt.goName)
			}
			if f.Kind != tt.wantKind {
				t.Errorf("Kind = %s, want %s", f.Kind, tt.wantKind)
			}
			if f.Optional != tt.wantOptional {
				t.Errorf("Optional = %t, want %t", f.Optional, tt.wantOptional)
			}
		})
	}

	// The pointer is kept on Type: Optional says a pointer is there, Type says
	// what validate has to build.
	if got := byGoName["PtrStr"].Type; got != reflect.TypeOf((*string)(nil)) {
		t.Errorf("Type of PtrStr = %v, want *string", got)
	}
	// A named type is preserved so validate can convert into it rather than
	// into its underlying kind.
	if got := byGoName["Named"].Type; got != reflect.TypeOf(currency("")) {
		t.Errorf("Type of Named = %v, want currency", got)
	}
}

type nesting struct {
	Vendor  vendor      `ovrin:"vendor"`
	Items   []item      `ovrin:"items"`
	Matrix  [][]string  `ovrin:"a slice of slices"`
	Ptrs    []*item     `ovrin:"a slice of pointers"`
	Times   []time.Time `ovrin:"a slice of times"`
	VATRate float64     `ovrin:"vat rate"`
}

func TestReflectKeys(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(nesting{}))
	if err != nil {
		t.Fatalf("Reflect(nesting) = %v, want no error", err)
	}
	want := []string{
		"vendor object",
		"vendor.name string required",
		"vendor.address string",
		"vendor.tax_id string",
		"items array",
		"items[] object",
		"items[].description string",
		"items[].quantity int min=0",
		"items[].unit_price float min=0",
		"matrix array",
		"matrix[] array",
		"matrix[][] string",
		"ptrs array",
		"ptrs[] object optional",
		"ptrs[].description string",
		"ptrs[].quantity int min=0",
		"ptrs[].unit_price float min=0",
		"times array",
		"times[] time",
		"vat_rate float",
	}
	compareLines(t, flatten(s.Fields), want)
}

type derived struct {
	InvoiceNumber string  `ovrin:",required"`
	VATRate       float64 `ovrin:""`
	UnitPrice     float64 `ovrin:",min=0"`
}

func TestReflectDerivedDescriptions(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(derived{}))
	if err != nil {
		t.Fatalf("Reflect(derived) = %v, want no error", err)
	}

	tests := []struct {
		name            string
		key             string
		wantDescription string
	}{
		{"two words from an empty first element", "invoice_number", "invoice number"},
		{"an empty tag derives too", "vat_rate", "vat rate"},
		{"derivation coexists with rules", "unit_price", "unit price"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := fieldByKey(t, s.Fields, tt.key).Description; got != tt.wantDescription {
				t.Errorf("Description = %q, want %q", got, tt.wantDescription)
			}
		})
	}
}

type onlyPointerRoot struct {
	Name string `ovrin:"a name"`
}

type siblings struct {
	Vendor vendor `ovrin:"the vendor"`
	Buyer  vendor `ovrin:"the buyer"`
}

type escaped struct {
	Address string `ovrin:"street\\, city and postcode,required"`
}

func TestReflectAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		typ   reflect.Type
		check func(t *testing.T, s *Schema)
	}{
		{
			name: "a pointer to a struct is the same schema as the struct",
			typ:  reflect.TypeOf(&onlyPointerRoot{}),
			check: func(t *testing.T, s *Schema) {
				if s.Name != "onlyPointerRoot" {
					t.Errorf("Name = %q, want %q", s.Name, "onlyPointerRoot")
				}
			},
		},
		{
			name: "the same type twice in different branches is not a cycle",
			typ:  reflect.TypeOf(siblings{}),
			check: func(t *testing.T, s *Schema) {
				compareLines(t, flatten(s.Fields), []string{
					"vendor object",
					"vendor.name string required",
					"vendor.address string",
					"vendor.tax_id string",
					"buyer object",
					"buyer.name string required",
					"buyer.address string",
					"buyer.tax_id string",
				})
			},
		},
		{
			name: "an escaped comma survives into the description",
			typ:  reflect.TypeOf(escaped{}),
			check: func(t *testing.T, s *Schema) {
				if got, want := s.Fields[0].Description, "street, city and postcode"; got != want {
					t.Errorf("Description = %q, want %q", got, want)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := Reflect(tt.typ)
			if err != nil {
				t.Fatalf("Reflect(%s) = %v, want no error", tt.typ, err)
			}
			tt.check(t, s)
		})
	}
}

// One type per rejection, named for what is wrong with it.

type unknownRule struct {
	Total float64 `ovrin:"total amount,requird"`
}

type minOnBool struct {
	Paid bool `ovrin:"whether it is paid,min=0"`
}

type minNotANumber struct {
	Total float64 `ovrin:"total amount,min=lots"`
}

type minFractionalLength struct {
	Name string `ovrin:"a name,min=1.5"`
}

type minNegativeLength struct {
	Name string `ovrin:"a name,min=-1"`
}

type maxOnObject struct {
	Vendor vendor `ovrin:"the vendor,max=3"`
}

type enumOnInt struct {
	Count int `ovrin:"a count,enum=1|2"`
}

type enumEmptyAlternative struct {
	Currency string `ovrin:"currency code,enum=UGX||USD"`
}

type formatOnInt struct {
	Count int `ovrin:"a count,format=date"`
}

type formatOnSlice struct {
	Tags []string `ovrin:"some tags,format=email"`
}

type unknownFormat struct {
	Email string `ovrin:"an email,format=isbn"`
}

type wrongFormatOnTime struct {
	Issued time.Time `ovrin:"when it was issued,format=email"`
}

type timeWithoutFormat struct {
	Issued time.Time `ovrin:"when it was issued"`
}

type twoFormats struct {
	Issued time.Time `ovrin:"when it was issued,format=date,format=datetime"`
}

type mapField struct {
	Meta map[string]string `ovrin:"some metadata"`
}

type anyField struct {
	Meta any `ovrin:"some metadata"`
}

type interfaceField struct {
	Meta interface{ String() string } `ovrin:"some metadata"`
}

type chanField struct {
	C chan int `ovrin:"a channel"`
}

type funcField struct {
	F func() `ovrin:"a function"`
}

type arrayField struct {
	A [3]string `ovrin:"a fixed-size array"`
}

type complexField struct {
	C complex128 `ovrin:"a complex number"`
}

type uintptrField struct {
	P uintptr `ovrin:"a uintptr"`
}

type doublePointerField struct {
	P **string `ovrin:"a pointer to a pointer"`
}

type unsupportedElement struct {
	Rows []map[string]int `ovrin:"some rows"`
}

type unsupportedInNested struct {
	Vendor nestedBad `ovrin:"the vendor"`
}

type nestedBad struct {
	Meta map[string]string `ovrin:"some metadata"`
}

type noTags struct {
	Number string `json:"number"`
	Total  float64
}

type nestedNoTags struct {
	Vendor untaggedVendor `ovrin:"the vendor"`
}

type untaggedVendor struct {
	Name string `json:"name"`
}

type unexportedTagged struct {
	total float64 `ovrin:"total amount"`
}

type recurDirect struct {
	Name string       `ovrin:"a name"`
	Next *recurDirect `ovrin:"the next one"`
}

type recurA struct {
	Name string `ovrin:"a name"`
	B    recurB `ovrin:"a b"`
}

type recurB struct {
	A *recurA `ovrin:"an a"`
}

type recurSlice struct {
	Name     string       `ovrin:"a name"`
	Children []recurSlice `ovrin:"the children"`
}

func TestReflectRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		typ          reflect.Type
		wantContains []string
	}{
		{
			name:         "a rule name with a typo in it names the rule and the field",
			typ:          reflect.TypeOf(unknownRule{}),
			wantContains: []string{"unknown rule", `"requird"`, "unknownRule.Total"},
		},
		{
			name:         "min on a bool names the rule the field and the type",
			typ:          reflect.TypeOf(minOnBool{}),
			wantContains: []string{"rule min", "minOnBool.Paid", "bool"},
		},
		{
			name:         "min on a number needs a number",
			typ:          reflect.TypeOf(minNotANumber{}),
			wantContains: []string{"rule min", "minNotANumber.Total", "needs a number"},
		},
		{
			name:         "min on a string is a length so it needs a whole number",
			typ:          reflect.TypeOf(minFractionalLength{}),
			wantContains: []string{"rule min", "minFractionalLength.Name", "whole number"},
		},
		{
			name:         "a negative length is rejected",
			typ:          reflect.TypeOf(minNegativeLength{}),
			wantContains: []string{"rule min", "minNegativeLength.Name", "whole number"},
		},
		{
			name:         "max on a nested object",
			typ:          reflect.TypeOf(maxOnObject{}),
			wantContains: []string{"rule max", "maxOnObject.Vendor", "vendor"},
		},
		{
			name:         "enum on an int",
			typ:          reflect.TypeOf(enumOnInt{}),
			wantContains: []string{"rule enum", "enumOnInt.Count", "int"},
		},
		{
			name:         "enum with an empty alternative names the field",
			typ:          reflect.TypeOf(enumEmptyAlternative{}),
			wantContains: []string{"rule enum", "empty alternative", "enumEmptyAlternative.Currency"},
		},
		{
			name:         "format on an int",
			typ:          reflect.TypeOf(formatOnInt{}),
			wantContains: []string{"rule format", "formatOnInt.Count", "int"},
		},
		{
			name:         "format on a slice",
			typ:          reflect.TypeOf(formatOnSlice{}),
			wantContains: []string{"rule format", "formatOnSlice.Tags", "[]string"},
		},
		{
			name:         "unknown format value",
			typ:          reflect.TypeOf(unknownFormat{}),
			wantContains: []string{"unknown format", `"isbn"`, "unknownFormat.Email"},
		},
		{
			name:         "a non-date format on a time.Time",
			typ:          reflect.TypeOf(wrongFormatOnTime{}),
			wantContains: []string{`"email"`, "wrongFormatOnTime.Issued", "time.Time", "date", "datetime"},
		},
		{
			name:         "a time.Time with no format at all",
			typ:          reflect.TypeOf(timeWithoutFormat{}),
			wantContains: []string{"timeWithoutFormat.Issued", "time.Time", "format=date", "format=datetime"},
		},
		{
			name:         "two contradictory formats",
			typ:          reflect.TypeOf(twoFormats{}),
			wantContains: []string{"twoFormats.Issued", "more than one format"},
		},
		{
			name:         "a map field",
			typ:          reflect.TypeOf(mapField{}),
			wantContains: []string{"mapField.Meta", "unsupported type", "map[string]string"},
		},
		{
			name:         "an any field",
			typ:          reflect.TypeOf(anyField{}),
			wantContains: []string{"anyField.Meta", "unsupported type", "interface {}"},
		},
		{
			name:         "a non-empty interface field",
			typ:          reflect.TypeOf(interfaceField{}),
			wantContains: []string{"interfaceField.Meta", "unsupported type"},
		},
		{
			name:         "a channel field",
			typ:          reflect.TypeOf(chanField{}),
			wantContains: []string{"chanField.C", "unsupported type", "chan int"},
		},
		{
			name:         "a func field",
			typ:          reflect.TypeOf(funcField{}),
			wantContains: []string{"funcField.F", "unsupported type", "func()"},
		},
		{
			name:         "a fixed-size array is not a slice",
			typ:          reflect.TypeOf(arrayField{}),
			wantContains: []string{"arrayField.A", "unsupported type", "[3]string"},
		},
		{
			name:         "a complex field",
			typ:          reflect.TypeOf(complexField{}),
			wantContains: []string{"complexField.C", "unsupported type", "complex128"},
		},
		{
			name:         "a uintptr is not a document number",
			typ:          reflect.TypeOf(uintptrField{}),
			wantContains: []string{"uintptrField.P", "unsupported type", "uintptr"},
		},
		{
			name:         "a pointer to a pointer",
			typ:          reflect.TypeOf(doublePointerField{}),
			wantContains: []string{"doublePointerField.P", "unsupported type", "**string"},
		},
		{
			name:         "an unsupported slice element names the element",
			typ:          reflect.TypeOf(unsupportedElement{}),
			wantContains: []string{"unsupportedElement.Rows[]", "unsupported type", "map[string]int"},
		},
		{
			name:         "an unsupported type inside a nested struct",
			typ:          reflect.TypeOf(unsupportedInNested{}),
			wantContains: []string{"unsupportedInNested.Vendor.Meta", "unsupported type"},
		},
		{
			name:         "a struct with no tagged fields names the type",
			typ:          reflect.TypeOf(noTags{}),
			wantContains: []string{"noTags", "no ovrin-tagged fields"},
		},
		{
			name:         "a nested struct with no tagged fields names the nested type",
			typ:          reflect.TypeOf(nestedNoTags{}),
			wantContains: []string{"untaggedVendor", "no ovrin-tagged fields"},
		},
		{
			name:         "a tagged unexported field cannot be written",
			typ:          reflect.TypeOf(unexportedTagged{}),
			wantContains: []string{"unexportedTagged.total", "unexported"},
		},
		{
			name:         "a type that refers to itself directly",
			typ:          reflect.TypeOf(recurDirect{}),
			wantContains: []string{"recursive type", "recurDirect.Next", "schema.recurDirect -> schema.recurDirect"},
		},
		{
			name:         "a cycle through another type names the whole cycle",
			typ:          reflect.TypeOf(recurA{}),
			wantContains: []string{"recursive type", "schema.recurA -> schema.recurB -> schema.recurA"},
		},
		{
			name:         "a cycle through a slice",
			typ:          reflect.TypeOf(recurSlice{}),
			wantContains: []string{"recursive type", "schema.recurSlice -> schema.recurSlice"},
		},
		{
			name:         "a non-struct type cannot be a schema",
			typ:          reflect.TypeOf(""),
			wantContains: []string{"string", "not a struct"},
		},
		{
			name:         "a map cannot be a schema",
			typ:          reflect.TypeOf(map[string]string{}),
			wantContains: []string{"map[string]string", "not a struct"},
		},
		{
			name:         "a pointer to a non-struct cannot be a schema",
			typ:          reflect.TypeOf(new(int)),
			wantContains: []string{"not a struct"},
		},
		{
			name:         "a pointer to a pointer to a struct is one pointer too many",
			typ:          reflect.TypeOf(new(*onlyPointerRoot)),
			wantContains: []string{"not a struct", "**"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := Reflect(tt.typ)
			if err == nil {
				t.Fatalf("Reflect(%s) = %v, want an error", tt.typ, flatten(s.Fields))
			}
			if !errors.Is(err, ErrSchema) {
				t.Errorf("errors.Is(err, ErrSchema) = false for %v", err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestReflectNilType(t *testing.T) {
	t.Parallel()

	var nilType reflect.Type
	_, err := Reflect(nilType)
	if err == nil {
		t.Fatal("Reflect(nil) = nil error, want an error")
	}
	if !errors.Is(err, ErrSchema) {
		t.Errorf("errors.Is(err, ErrSchema) = false for %v", err)
	}
}

// TestReflectSliceOfTimesNeedsNoFormat records a deliberate exemption: a slice
// element carries no rules because the grammar has nowhere to write them, so
// requiring a format on it would make []time.Time undeclarable.
func TestReflectSliceOfTimesNeedsNoFormat(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(nesting{}))
	if err != nil {
		t.Fatalf("Reflect(nesting) = %v, want no error", err)
	}
	times := fieldByKey(t, s.Fields, "times")
	if times.Elem == nil || times.Elem.Kind != KindTime {
		t.Fatalf("element of times = %+v, want a time", times.Elem)
	}
	if len(times.Elem.Rules) != 0 {
		t.Errorf("element rules = %+v, want none", times.Elem.Rules)
	}
}

func TestCache(t *testing.T) {
	t.Parallel()

	t.Run("a type is reflected once and shared", func(t *testing.T) {
		t.Parallel()
		var c Cache
		first, err := c.Of(reflect.TypeOf(invoice{}))
		if err != nil {
			t.Fatalf("Of(invoice) = %v, want no error", err)
		}
		second, err := c.Of(reflect.TypeOf(invoice{}))
		if err != nil {
			t.Fatalf("Of(invoice) = %v, want no error", err)
		}
		if first != second {
			t.Errorf("Of returned %p then %p, want the same schema", first, second)
		}
	})

	t.Run("distinct types get distinct schemas", func(t *testing.T) {
		t.Parallel()
		var c Cache
		a, err := c.Of(reflect.TypeOf(invoice{}))
		if err != nil {
			t.Fatalf("Of(invoice) = %v, want no error", err)
		}
		b, err := c.Of(reflect.TypeOf(vendor{}))
		if err != nil {
			t.Fatalf("Of(vendor) = %v, want no error", err)
		}
		if a == b || a.Name == b.Name {
			t.Errorf("Of(invoice) and Of(vendor) returned %q and %q", a.Name, b.Name)
		}
	})

	t.Run("a failure is remembered", func(t *testing.T) {
		t.Parallel()
		var c Cache
		_, first := c.Of(reflect.TypeOf(unknownRule{}))
		_, second := c.Of(reflect.TypeOf(unknownRule{}))
		if first == nil || second == nil {
			t.Fatalf("Of(unknownRule) = %v then %v, want an error both times", first, second)
		}
		if !errors.Is(first, ErrSchema) {
			t.Errorf("errors.Is(err, ErrSchema) = false for %v", first)
		}
		if first.Error() != second.Error() {
			t.Errorf("errors differ: %q then %q", first, second)
		}
	})

	t.Run("concurrent use returns one schema", func(t *testing.T) {
		t.Parallel()
		var (
			c       Cache
			wg      sync.WaitGroup
			mu      sync.Mutex
			results []*Schema
		)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := c.Of(reflect.TypeOf(invoice{}))
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					t.Errorf("Of(invoice) = %v, want no error", err)
					return
				}
				results = append(results, s)
			}()
		}
		wg.Wait()
		if len(results) != 32 {
			t.Fatalf("got %d results, want 32", len(results))
		}
		for i, s := range results {
			if s != results[0] {
				t.Fatalf("result %d is a different schema from result 0", i)
			}
		}
	})
}

// TestUnexportedFieldIsConstructible keeps the unexported test field above
// referenced from real code, so it reads as deliberate rather than as dead.
func TestUnexportedFieldIsConstructible(t *testing.T) {
	t.Parallel()

	if got := (unexportedTagged{total: 1}).total; got != 1 {
		t.Errorf("total = %v, want 1", got)
	}
}

type allFormats struct {
	Date     string `ovrin:"a date as text,format=date"`
	Datetime string `ovrin:"a datetime as text,format=datetime"`
	Email    string `ovrin:"an email address,format=email"`
	Phone    string `ovrin:"a phone number,format=phone"`
	Currency string `ovrin:"a currency code,format=currency"`
	IBAN     string `ovrin:"an iban,format=iban"`
	SWIFT    string `ovrin:"a swift code,format=swift"`
	UUID     string `ovrin:"a uuid,format=uuid"`
}

func TestReflectAcceptsEveryFormat(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(allFormats{}))
	if err != nil {
		t.Fatalf("Reflect(allFormats) = %v, want no error", err)
	}
	want := []string{
		"date string format=date",
		"datetime string format=datetime",
		"email string format=email",
		"phone string format=phone",
		"currency string format=currency",
		"iban string format=iban",
		"swift string format=swift",
		"uuid string format=uuid",
	}
	compareLines(t, flatten(s.Fields), want)
}

type applicableRules struct {
	Name     string    `ovrin:"a name,required,min=1,max=64"`
	Count    int       `ovrin:"a count,required,min=0,max=10"`
	Total    float64   `ovrin:"a total,min=-1.5,max=1e6"`
	Tags     []string  `ovrin:"some tags,required,min=1,max=5"`
	Paid     bool      `ovrin:"whether it is paid,required"`
	Issued   time.Time `ovrin:"when it was issued,required,format=date"`
	Vendor   vendor    `ovrin:"the vendor,required"`
	Optional *float64  `ovrin:"an optional total,min=0"`
}

func TestReflectAcceptsApplicableRules(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(applicableRules{}))
	if err != nil {
		t.Fatalf("Reflect(applicableRules) = %v, want no error", err)
	}
	want := []string{
		"name string required min=1 max=64",
		"count int required min=0 max=10",
		"total float min=-1.5 max=1e6",
		"tags array required min=1 max=5",
		"tags[] string",
		"paid bool required",
		"issued time required format=date",
		"vendor object required",
		"vendor.name string required",
		"vendor.address string",
		"vendor.tax_id string",
		"optional float optional min=0",
	}
	compareLines(t, flatten(s.Fields), want)
}

// TestReflectAnonymousStructs checks the name given to a type that has none.
// Anonymous structs are rare in a schema but a type with no name must still be
// nameable in an error.
func TestReflectAnonymousStructs(t *testing.T) {
	t.Parallel()

	root := struct {
		Name   string `ovrin:"a name"`
		Nested struct {
			Inner string `ovrin:"an inner name"`
		} `ovrin:"a nested anonymous struct"`
	}{}

	s, err := Reflect(reflect.TypeOf(root))
	if err != nil {
		t.Fatalf("Reflect(anonymous struct) = %v, want no error", err)
	}
	if !strings.HasPrefix(s.Name, "struct {") {
		t.Errorf("Name = %q, want the struct's full description", s.Name)
	}
	compareLines(t, flatten(s.Fields), []string{
		"name string",
		"nested object",
		"nested.inner string",
	})
}

func TestKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{"the zero value says so", KindUnknown, "unknown"},
		{"a known kind is its own name", KindString, "string"},
		{"array", KindArray, "array"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("Kind(%q).String() = %q, want %q", string(tt.kind), got, tt.want)
			}
		})
	}
}
