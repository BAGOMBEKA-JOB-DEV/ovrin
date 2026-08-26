package validate

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// field builds a schema.Field the way the schema package would, so a test says
// what it means rather than filling in a struct literal every time.
func field(kind schema.Kind, goType any, rules ...schema.Rule) schema.Field {
	f := schema.Field{Key: "test", GoName: "Test", Kind: kind, Rules: rules}
	if goType != nil {
		f.Type = reflect.TypeOf(goType)
	}
	return f
}

func rule(name, value string) schema.Rule { return schema.Rule{Name: name, Value: value} }

// ruleByName finds one rule result, so a test asserts on the rule it means.
func ruleByName(t *testing.T, r Result, want string) RuleResult {
	t.Helper()
	for _, rr := range r.Rules {
		if rr.Rule == want {
			return rr
		}
	}
	t.Fatalf("no result for rule %q; got %+v", want, r.Rules)
	return RuleResult{}
}

func TestFieldRequired(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		kind      schema.Kind
		goType    any
		raw       any
		wantFound bool
	}{
		{"a value the model did not return at all", schema.KindString, "", nil, false},
		{"an explicit JSON null", schema.KindFloat, float64(0), nil, false},
		{"an empty string from a model with nothing to report", schema.KindString, "", "", false},
		{"a string of only whitespace", schema.KindString, "", "   \t ", false},
		{"a genuine zero, which is a value and not an absence", schema.KindFloat, float64(0), float64(0), true},
		{"a genuine false, which is a value and not an absence", schema.KindBool, false, false, true},
		{"an empty slice, which is the document saying there are none", schema.KindArray, []string{}, []any{}, true},
		{"ordinary text", schema.KindString, "", "Acme Ltd", true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v := New()
			r := v.Field(field(c.kind, c.goType, rule(schema.RuleRequired, "")), c.raw)
			if r.Found != c.wantFound {
				t.Errorf("Found = %v, want %v", r.Found, c.wantFound)
			}
			req := ruleByName(t, r, "required")
			if req.Passed != c.wantFound {
				t.Errorf("required passed = %v, want %v", req.Passed, c.wantFound)
			}
			if r.Valid() != c.wantFound {
				t.Errorf("Valid = %v, want %v", r.Valid(), c.wantFound)
			}
		})
	}
}

func TestFieldAbsenceIsNeverAZeroValue(t *testing.T) {
	t.Parallel()
	v := New()
	for _, c := range []struct {
		name   string
		kind   schema.Kind
		goType any
	}{
		{"a missing float is not 0.00", schema.KindFloat, float64(0)},
		{"a missing int is not 0", schema.KindInt, int(0)},
		{"a missing string is not the empty string", schema.KindString, ""},
		{"a missing bool is not false", schema.KindBool, false},
		{"a missing date is not the zero time", schema.KindTime, time.Time{}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := v.Field(field(c.kind, c.goType), nil)
			if r.Found || r.Converted || r.Value != nil {
				t.Errorf("Found=%v Converted=%v Value=%#v, want absent with no value",
					r.Found, r.Converted, r.Value)
			}
		})
	}
}

func TestFieldOnlyRequiredIsEvaluatedForAnAbsentValue(t *testing.T) {
	t.Parallel()
	v := New()
	f := field(schema.KindString, "",
		rule(schema.RuleFormat, schema.FormatEmail),
		rule(schema.RuleMin, "3"),
		rule(schema.RuleEnum, "a@b.com|c@d.com"),
	)
	r := v.Field(f, nil)
	if !r.Valid() {
		t.Fatalf("an absent optional field must not fail its other rules; got %+v", r.Rules)
	}
}

func TestFieldMinMax(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		kind       schema.Kind
		goType     any
		rules      []schema.Rule
		raw        any
		wantPassed map[string]bool
	}{
		{
			name: "a float at its minimum passes", kind: schema.KindFloat, goType: float64(0),
			rules: []schema.Rule{rule(schema.RuleMin, "0")}, raw: "0",
			wantPassed: map[string]bool{"min=0": true},
		},
		{
			name: "a negative total below a minimum of zero fails", kind: schema.KindFloat, goType: float64(0),
			rules: []schema.Rule{rule(schema.RuleMin, "0")}, raw: "-12.50",
			wantPassed: map[string]bool{"min=0": false},
		},
		{
			name: "a number above its maximum fails", kind: schema.KindInt, goType: int(0),
			rules: []schema.Rule{rule(schema.RuleMax, "100")}, raw: "101",
			wantPassed: map[string]bool{"max=100": false},
		},
		{
			name: "min on a string is a length in runes", kind: schema.KindString, goType: "",
			rules: []schema.Rule{rule(schema.RuleMin, "3")}, raw: "ab",
			wantPassed: map[string]bool{"min=3": false},
		},
		{
			name: "a string length counts runes and not bytes", kind: schema.KindString, goType: "",
			rules: []schema.Rule{rule(schema.RuleMax, "3")}, raw: "አዲስ",
			wantPassed: map[string]bool{"max=3": true},
		},
		{
			name: "min on a slice is a number of elements", kind: schema.KindArray, goType: []string{},
			rules: []schema.Rule{rule(schema.RuleMin, "1")}, raw: []any{},
			wantPassed: map[string]bool{"min=1": false},
		},
		{
			name: "a slice with enough elements passes", kind: schema.KindArray, goType: []string{},
			rules: []schema.Rule{rule(schema.RuleMin, "1"), rule(schema.RuleMax, "2")}, raw: []any{1, 2},
			wantPassed: map[string]bool{"min=1": true, "max=2": true},
		},
		{
			name: "both bounds on one field are reported separately", kind: schema.KindInt, goType: int(0),
			rules: []schema.Rule{rule(schema.RuleMin, "1"), rule(schema.RuleMax, "5")}, raw: "9",
			wantPassed: map[string]bool{"min=1": true, "max=5": false},
		},
		{
			name: "a bound that is not evaluable is not silently passed", kind: schema.KindFloat, goType: float64(0),
			rules: []schema.Rule{rule(schema.RuleMin, "0")}, raw: "not a number",
			wantPassed: map[string]bool{"min=0": false},
		},
		{
			name: "a bound the schema wrote wrongly fails loudly", kind: schema.KindFloat, goType: float64(0),
			rules: []schema.Rule{rule(schema.RuleMin, "lots")}, raw: "5",
			wantPassed: map[string]bool{"min=lots": false},
		},
		{
			name: "a bound on a type it cannot apply to fails loudly", kind: schema.KindBool, goType: false,
			rules: []schema.Rule{rule(schema.RuleMin, "1")}, raw: "yes",
			wantPassed: map[string]bool{"min=1": false},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := New().Field(field(c.kind, c.goType, c.rules...), c.raw)
			for name, want := range c.wantPassed {
				if got := ruleByName(t, r, name).Passed; got != want {
					t.Errorf("rule %s passed = %v, want %v (results %+v)", name, got, want, r.Rules)
				}
			}
		})
	}
}

func TestFieldEnum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		rules      []schema.Rule
		raw        string
		wantPassed bool
	}{
		{"a listed alternative passes", []schema.Rule{rule(schema.RuleEnum, "UGX|USD|EUR")}, "USD", true},
		{"the last alternative passes", []schema.Rule{rule(schema.RuleEnum, "UGX|USD|EUR")}, "EUR", true},
		{"an unlisted value fails", []schema.Rule{rule(schema.RuleEnum, "UGX|USD|EUR")}, "GBP", false},
		{"matching is exact, so a different case fails", []schema.Rule{rule(schema.RuleEnum, "UGX|USD")}, "usd", false},
		{
			"enum compares the normalised value, so a currency format lowercases nothing",
			[]schema.Rule{rule(schema.RuleFormat, schema.FormatCurrency), rule(schema.RuleEnum, "UGX|USD")},
			"usd", true,
		},
		{
			"a value that failed its format cannot pass its enum",
			[]schema.Rule{rule(schema.RuleFormat, schema.FormatCurrency), rule(schema.RuleEnum, "UGX|USD")},
			"dollars", false,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := New().Field(field(schema.KindString, "", c.rules...), c.raw)
			got := ruleByName(t, r, "enum="+c.rules[len(c.rules)-1].Value).Passed
			if got != c.wantPassed {
				t.Errorf("enum passed = %v, want %v (results %+v)", got, c.wantPassed, r.Rules)
			}
		})
	}
}

func TestFieldFormatsAreWiredToTheirNormalisers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		format string
		raw    string
		want   any
		ok     bool
	}{
		{"a date normalises to midnight UTC", schema.FormatDate, "3 April 2026",
			time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC), true},
		{"a datetime keeps its time", schema.FormatDatetime, "2026-04-03T14:30:00Z",
			time.Date(2026, 4, 3, 14, 30, 0, 0, time.UTC), true},
		{"an email is lowercased", schema.FormatEmail, "Amina.Nakato@Example.COM", "amina.nakato@example.com", true},
		{"a phone with a country code becomes E.164", schema.FormatPhone, "+256 (0) 41 4234567", "+256414234567", true},
		{"a currency code is uppercased", schema.FormatCurrency, "ugx", "UGX", true},
		{"an IBAN loses its printed spacing", schema.FormatIBAN, "gb82 west 1234 5698 7654 32", "GB82WEST12345698765432", true},
		{"a BIC is uppercased", schema.FormatSWIFT, "sbicugkx", "SBICUGKX", true},
		{"a UUID is lowercased and hyphenated", schema.FormatUUID, "{6BA7B8109DAD11D180B400C04FD430C8}",
			"6ba7b810-9dad-11d1-80b4-00c04fd430c8", true},
		{"a date that is not one fails", schema.FormatDate, "sometime last week", nil, false},
		{"an email that is not one fails", schema.FormatEmail, "nobody at example dot com", nil, false},
		{"a currency code nobody issues fails", schema.FormatCurrency, "XYZ", nil, false},
		{"an IBAN with a broken checksum fails", schema.FormatIBAN, "GB82WEST12345698765433", nil, false},
		{"a BIC of the wrong length fails", schema.FormatSWIFT, "SBICUG", nil, false},
		{"a UUID missing a digit fails", schema.FormatUUID, "6ba7b810-9dad-11d1-80b4-00c04fd430c", nil, false},
		{"a phone with letters in it fails", schema.FormatPhone, "+256 41 CALLNOW", nil, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			kind, goType := schema.KindString, any("")
			if c.format == schema.FormatDate || c.format == schema.FormatDatetime {
				kind, goType = schema.KindTime, any(time.Time{})
			}
			f := field(kind, goType, rule(schema.RuleFormat, c.format))
			r := New().Field(f, c.raw)

			if r.Converted != c.ok {
				t.Fatalf("Converted = %v, want %v (message %q)", r.Converted, c.ok, r.Message)
			}
			fr := ruleByName(t, r, "format="+c.format)
			if fr.Passed != c.ok {
				t.Errorf("format rule passed = %v, want %v", fr.Passed, c.ok)
			}
			if c.ok {
				if !reflect.DeepEqual(r.Value, c.want) {
					t.Errorf("Value = %#v, want %#v", r.Value, c.want)
				}
				return
			}
			if r.Value != nil {
				t.Errorf("Value = %#v, want no value at all", r.Value)
			}
			if fr.Message == "" {
				t.Error("a failed format rule must say why")
			}
			if r.Raw == "" {
				t.Error("Raw must carry the text so a reviewer sees what was there")
			}
		})
	}
}

func TestFieldDateFormatOnAStringNormalisesToRFC3339(t *testing.T) {
	t.Parallel()
	r := New().Field(field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatDate)), "3 April 2026")
	if !r.Converted || r.Value != "2026-04-03" {
		t.Errorf("Value = %#v, want %q", r.Value, "2026-04-03")
	}
}

func TestFieldAmbiguousDateProducesNoValue(t *testing.T) {
	t.Parallel()
	f := field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDate))

	r := New().Field(f, "03/04/2026")
	if r.Converted || r.Value != nil {
		t.Fatalf("an ambiguous date must not resolve itself: Converted=%v Value=%#v", r.Converted, r.Value)
	}
	if r.Ambiguity == nil {
		t.Fatal("Ambiguity must be set so a reviewer sees both readings")
	}
	wantDay := time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)
	wantMonth := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if !r.Ambiguity.DayFirst.Equal(wantDay) || !r.Ambiguity.MonthFirst.Equal(wantMonth) {
		t.Errorf("Ambiguity = %+v, want %v and %v", r.Ambiguity, wantDay, wantMonth)
	}
	if ruleByName(t, r, "format=date").Passed {
		t.Error("an unresolved date must not report its format rule as passed")
	}

	resolved := New(WithDateOrder(DayFirst)).Field(f, "03/04/2026")
	if !resolved.Converted || resolved.Ambiguity != nil {
		t.Fatalf("a date order must resolve it: %+v", resolved)
	}
	if got := resolved.Value.(time.Time); !got.Equal(wantDay) {
		t.Errorf("Value = %v, want %v", got, wantDay)
	}
}

func TestFieldCompositesAreLeftToTheCaller(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		kind   schema.Kind
		goType any
		raw    any
	}{
		{"a slice field", schema.KindArray, []string{}, []any{"a", "b"}},
		{"a struct field", schema.KindObject, struct{}{}, map[string]any{"name": "Acme"}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := New().Field(field(c.kind, c.goType, rule(schema.RuleRequired, "")), c.raw)
			if !r.Found {
				t.Error("Found = false, want true")
			}
			if r.Converted {
				t.Error("Converted = true; a composite is assembled by the caller")
			}
			if !r.Valid() {
				t.Errorf("Valid = false, want true: %+v", r.Rules)
			}
			if s := SchemaSignal(r); s != 1 {
				t.Errorf("SchemaSignal = %v, want 1 for a present composite", s)
			}
		})
	}
}

func TestFieldUnknownRuleIsNotSilentlyPassed(t *testing.T) {
	t.Parallel()
	// The schema package rejects a rule name outside the vocabulary with
	// ErrSchema before extraction starts. One arriving here anyway is a bug in
	// that package, and a rule that quietly passes would hide it.
	r := New().Field(field(schema.KindString, "", rule("nearly", "1|9")), "x")
	if r.Valid() {
		t.Error("a rule name the schema package should have rejected must not pass silently")
	}
	if got := ruleByName(t, r, "nearly=1|9"); got.Message == "" {
		t.Error("an unknown rule must say what is wrong with it")
	}
}

// TestMessagesNeverEchoTheValue enforces docs/rules.md §2.5 and §7.5: a message
// ends up in logs, traces and audit stores, and a document is somebody's
// invoice. Every message here is generated from a value containing a token that
// must not appear in it.
func TestMessagesNeverEchoTheValue(t *testing.T) {
	t.Parallel()
	const secret = "zqxjvw"
	cases := []struct {
		name  string
		field schema.Field
		raw   any
	}{
		{"a number that is not one", field(schema.KindFloat, float64(0), rule(schema.RuleMin, "0")), secret},
		{"an integer field given a fraction", field(schema.KindInt, int(0)), "12.5" + secret},
		{"a bool that is neither", field(schema.KindBool, false), secret},
		{"a date that is not one", field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDate)), secret},
		{"an email that is not one", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatEmail)), secret},
		{"a phone that is not one", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatPhone)), secret},
		{"a currency that is not one", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatCurrency)), secret},
		{"an IBAN that is not one", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatIBAN)), "GB82WEST1234569876543" + secret},
		{"a BIC that is not one", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatSWIFT)), secret},
		{"a UUID that is not one", field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatUUID)), secret},
		{"an enum with an unlisted value", field(schema.KindString, "", rule(schema.RuleEnum, "a|b")), secret},
		{"a string past its maximum length", field(schema.KindString, "", rule(schema.RuleMax, "2")), secret},
		{"a number past its maximum", field(schema.KindFloat, float64(0), rule(schema.RuleMax, "1")), "999" + secret + "9"},
		{"an ambiguous date", field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDate)), "03/04/2026"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := New().Field(c.field, c.raw)
			texts := []string{r.Message}
			for _, rr := range r.Rules {
				texts = append(texts, rr.Message)
			}
			for _, text := range texts {
				if text == "" {
					continue
				}
				if strings.Contains(strings.ToLower(text), secret) {
					t.Errorf("message %q contains the value", text)
				}
				if strings.Contains(text, "03/04/2026") {
					t.Errorf("message %q contains the value", text)
				}
			}
		})
	}
}

func TestFieldRawIsCarriedForTheReviewer(t *testing.T) {
	t.Parallel()
	r := New().Field(field(schema.KindFloat, float64(0)), "twenty five thousand")
	if r.Raw != "twenty five thousand" {
		t.Errorf("Raw = %q, want the text as extracted", r.Raw)
	}
	if r.Converted {
		t.Error("Converted = true, want false")
	}
	if r.Message == "" {
		t.Error("a value that did not convert must say why")
	}
}

func TestValidIsDerivedFromTheRules(t *testing.T) {
	t.Parallel()
	if !(Result{}).Valid() {
		t.Error("a field with no rules is valid")
	}
	r := Result{Rules: []RuleResult{{Rule: "required", Passed: true}, {Rule: "min=0", Passed: false}}}
	if r.Valid() {
		t.Error("one failed rule makes a field invalid")
	}
}
