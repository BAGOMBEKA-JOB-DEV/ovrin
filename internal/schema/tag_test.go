package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestSplitTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tag  string
		want []string
	}{
		{"description only", "total amount", []string{"total amount"}},
		{"description and one rule", "total amount,required", []string{"total amount", "required"}},
		{"description and two rules", "total,required,min=0", []string{"total", "required", "min=0"}},
		{"empty description", ",required", []string{"", "required"}},
		{"empty tag", "", []string{""}},
		{"escaped comma in description", `street\, city and postcode,required`, []string{"street, city and postcode", "required"}},
		{"two escaped commas", `a\, b\, c`, []string{"a, b, c"}},
		{"escaped backslash", `a path C:\\temp,required`, []string{`a path C:\temp`, "required"}},
		{"escaped backslash before a separator", `ends with a backslash\\,required`, []string{`ends with a backslash\`, "required"}},
		{"backslash before anything else is literal", `C:\temp`, []string{`C:\temp`}},
		{"trailing backslash", `trailing\`, []string{`trailing\`}},
		{"trailing comma yields an empty element", "total,", []string{"total", ""}},
		{"multi-byte description", "montant total en €,required", []string{"montant total en €", "required"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitTag(tt.tag)
			if len(got) != len(tt.want) {
				t.Fatalf("splitTag(%q) = %q, want %q", tt.tag, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitTag(%q)[%d] = %q, want %q", tt.tag, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		tag             string
		wantDescription string
		wantRules       []Rule
	}{
		{
			name:            "description only",
			tag:             "total amount including tax",
			wantDescription: "total amount including tax",
		},
		{
			name:            "description and required",
			tag:             "invoice number,required",
			wantDescription: "invoice number",
			wantRules:       []Rule{{Name: RuleRequired}},
		},
		{
			name:            "rules in tag order",
			tag:             "total amount,required,min=0,max=1000000",
			wantDescription: "total amount",
			wantRules:       []Rule{{Name: RuleRequired}, {Name: RuleMin, Value: "0"}, {Name: RuleMax, Value: "1000000"}},
		},
		{
			name:            "enum splits on pipes",
			tag:             "currency code,required,enum=UGX|USD|EUR|GBP",
			wantDescription: "currency code",
			wantRules:       []Rule{{Name: RuleRequired}, {Name: RuleEnum, Value: "UGX|USD|EUR|GBP"}},
		},
		{
			name:            "single enum alternative",
			tag:             "currency code,enum=UGX",
			wantDescription: "currency code",
			wantRules:       []Rule{{Name: RuleEnum, Value: "UGX"}},
		},
		{
			name:            "format rule",
			tag:             "date the invoice was issued,format=date",
			wantDescription: "date the invoice was issued",
			wantRules:       []Rule{{Name: RuleFormat, Value: "date"}},
		},
		{
			name:            "empty description leaves derivation to the caller",
			tag:             ",required",
			wantDescription: "",
			wantRules:       []Rule{{Name: RuleRequired}},
		},
		{
			name:            "escaped comma in description",
			tag:             `street\, city and postcode,required`,
			wantDescription: "street, city and postcode",
			wantRules:       []Rule{{Name: RuleRequired}},
		},
		{
			name:            "surrounding space in the description is trimmed",
			tag:             "  total amount  ,required",
			wantDescription: "total amount",
			wantRules:       []Rule{{Name: RuleRequired}},
		},
		{
			name:            "a description of only space derives instead",
			tag:             "   ,required",
			wantDescription: "",
			wantRules:       []Rule{{Name: RuleRequired}},
		},
		{
			name:            "negative min",
			tag:             "temperature,min=-40",
			wantDescription: "temperature",
			wantRules:       []Rule{{Name: RuleMin, Value: "-40"}},
		},
		{
			name:            "a rule value may contain an equals sign",
			tag:             "code,enum=a=1|b=2",
			wantDescription: "code",
			wantRules:       []Rule{{Name: RuleEnum, Value: "a=1|b=2"}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			description, rules, err := parseTag("Invoice.Field", tt.tag)
			if err != nil {
				t.Fatalf("parseTag(%q) = %v, want no error", tt.tag, err)
			}
			if description != tt.wantDescription {
				t.Errorf("description = %q, want %q", description, tt.wantDescription)
			}
			if len(rules) != len(tt.wantRules) {
				t.Fatalf("rules = %+v, want %+v", rules, tt.wantRules)
			}
			for i := range rules {
				if rules[i] != tt.wantRules[i] {
					t.Errorf("rules[%d] = %+v, want %+v", i, rules[i], tt.wantRules[i])
				}
			}
		})
	}
}

func TestParseTagErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		tag          string
		wantContains []string
	}{
		{
			name:         "a rule name with a typo in it",
			tag:          "total,requird",
			wantContains: []string{"unknown rule", `"requird"`, "Invoice.Total"},
		},
		{
			name:         "unescaped comma in a description",
			tag:          "name, address and postcode",
			wantContains: []string{"unknown rule", `" address and postcode"`},
		},
		{
			name:         "space before a real rule name is not a rule",
			tag:          "total, required",
			wantContains: []string{"unknown rule", `" required"`},
		},
		{
			name:         "rule name in the wrong case",
			tag:          "total,Required",
			wantContains: []string{"unknown rule", `"Required"`},
		},
		{
			name:         "required given a value",
			tag:          "total,required=yes",
			wantContains: []string{"rule required", "takes no value"},
		},
		{
			name:         "min with no value",
			tag:          "total,min",
			wantContains: []string{"rule min", "needs a value"},
		},
		{
			name:         "min with an empty value",
			tag:          "total,min=",
			wantContains: []string{"rule min", "needs a value"},
		},
		{
			name:         "max with no value",
			tag:          "total,max",
			wantContains: []string{"rule max", "needs a value"},
		},
		{
			name:         "format with no value",
			tag:          "issued,format",
			wantContains: []string{"rule format", "needs a value"},
		},
		{
			name:         "enum with no value",
			tag:          "currency,enum",
			wantContains: []string{"rule enum", "at least one alternative"},
		},
		{
			name:         "enum with an empty alternative",
			tag:          "currency,enum=UGX||USD",
			wantContains: []string{"rule enum", "empty alternative", "Invoice.Total"},
		},
		{
			name:         "enum with a trailing pipe",
			tag:          "currency,enum=UGX|USD|",
			wantContains: []string{"rule enum", "empty alternative"},
		},
		{
			name:         "enum with a leading pipe",
			tag:          "currency,enum=|UGX",
			wantContains: []string{"rule enum", "empty alternative"},
		},
		{
			name:         "trailing comma leaves an empty rule",
			tag:          "total,",
			wantContains: []string{"unknown rule", `""`},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := parseTag("Invoice.Total", tt.tag)
			if err == nil {
				t.Fatalf("parseTag(%q) = nil error, want an error", tt.tag)
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

// TestErrorMessagesAreLowercaseAndUnpunctuated guards docs/rules.md §2.3. The
// root package prefixes "ovrin: " at the boundary; a capital or a full stop
// here would read as two sentences jammed together.
func TestErrorMessagesAreLowercaseAndUnpunctuated(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"total,requird", "total,required=yes", "total,min", "currency,enum=A||B"} {
		_, _, err := parseTag("Invoice.Total", tag)
		if err == nil {
			t.Fatalf("parseTag(%q) = nil error, want an error", tag)
		}
		msg := err.Error()
		if strings.HasSuffix(msg, ".") {
			t.Errorf("message %q ends in a full stop", msg)
		}
		if r := []rune(msg)[0]; r >= 'A' && r <= 'Z' {
			t.Errorf("message %q starts with a capital", msg)
		}
	}
}
