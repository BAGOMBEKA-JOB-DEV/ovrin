package schema

import (
	"reflect"
	"testing"
)

func TestFieldKeyAndDerivedDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		goName          string
		wantKey         string
		wantDescription string
	}{
		{"single word", "Number", "number", "number"},
		{"two words", "UnitPrice", "unit_price", "unit price"},
		{"three words", "InvoiceLineTotal", "invoice_line_total", "invoice line total"},
		{"leading initialism", "VATRate", "vat_rate", "vat rate"},
		{"trailing initialism", "TaxID", "tax_id", "tax id"},
		{"initialism alone", "ID", "id", "id"},
		{"initialism between words", "CustomerVATNumber", "customer_vat_number", "customer vat number"},
		{"run of capitals with no lowercase word", "HTTPSURL", "httpsurl", "httpsurl"},
		{"digits stay with their word", "Address2", "address2", "address2"},
		{"digit before a new word", "Line1Item", "line1_item", "line1 item"},
		{"lowercase first rune", "iPhoneModel", "i_phone_model", "i phone model"},
		{"already lowercase", "total", "total", "total"},
		{"underscore separates words", "My_Field", "my_field", "my field"},
		{"nothing splittable", "_", "_", "_"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := fieldKey(tt.goName); got != tt.wantKey {
				t.Errorf("fieldKey(%q) = %q, want %q", tt.goName, got, tt.wantKey)
			}
			if got := derivedDescription(tt.goName); got != tt.wantDescription {
				t.Errorf("derivedDescription(%q) = %q, want %q", tt.goName, got, tt.wantDescription)
			}
		})
	}
}

func TestIndexKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sliceKey string
		index    int
		want     string
	}{
		{"first element of a top-level slice", "items", 0, "items[0]"},
		{"tenth element", "items", 9, "items[9]"},
		{"slice inside a nested object", "vendor.contacts", 2, "vendor.contacts[2]"},
		{"element of an element", "matrix[0]", 1, "matrix[0][1]"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IndexKey(tt.sliceKey, tt.index); got != tt.want {
				t.Errorf("IndexKey(%q, %d) = %q, want %q", tt.sliceKey, tt.index, got, tt.want)
			}
		})
	}
}

// TestIndexKeyRebasesAChildKey checks the recipe documented on IndexKey: the
// static key of a field inside a slice element becomes a runtime key by
// swapping the element prefix. If this stops holding, Result.Fields keys stop
// matching the ones docs/schema.md promises.
func TestIndexKeyRebasesAChildKey(t *testing.T) {
	t.Parallel()

	s, err := Reflect(reflect.TypeOf(invoice{}))
	if err != nil {
		t.Fatalf("Reflect(invoice) = %v, want no error", err)
	}
	items := fieldByKey(t, s.Fields, "items")
	child := fieldByKey(t, items.Elem.Fields, "items[].unit_price")

	got := IndexKey(items.Key, 0) + child.Key[len(items.Elem.Key):]
	if want := "items[0].unit_price"; got != want {
		t.Errorf("rebased key = %q, want %q", got, want)
	}
}
