// Result.Explain, exercised against a real extraction.
//
// Gap closed here: Explain is the flagship explainability API and nothing ran
// it. explain_golden_test.go hand-builds an Explanation literal and tests only
// String, and the example named ExampleResult_Explain_fabrication reads
// res.Fields directly — so the method that assembles an Explanation from a
// Result had never been called by a test at all. Both of its returns are
// covered below, because the second one is what a caller gets for a field name
// that is not in the schema and returning a usable-looking zero Explanation
// there would be worse than returning nothing.
//
// The assertions are all of the same shape: an Explanation may not say
// anything the Result it came from does not. Its doc comment promises it
// "cannot disagree with the Result it came from", and that promise is only
// true if something checks it.
package ovrin_test

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// explainModel replies with a fixed object, so the test states exactly what the
// model said and asserts what ovrin made of it.
type explainModel struct{ reply map[string]any }

func (m explainModel) Generate(context.Context, ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	b, err := json.Marshal(m.reply)
	if err != nil {
		return nil, err
	}
	return &ovrin.ModelResponse{JSON: b}, nil
}

// explainDoc has one field the model answers and one it does not. The absent
// one is required, so it carries a review reason, which is the only way to
// check that Explain gathers the reasons for the field it was asked about and
// not the reasons for its neighbours.
type explainDoc struct {
	Vendor  string  `ovrin:"vendor name,required"`
	Total   float64 `ovrin:"total amount,required,min=0"`
	Balance float64 `ovrin:"closing balance,required"`
}

// explained runs one extraction over a small CSV and returns the result.
//
// A CSV rather than an image: this file is about Explain, and a source that
// needs no OCR provider and no renderer keeps the fixture out of the way of
// the thing being tested.
func explained(t *testing.T) *ovrin.Result[explainDoc] {
	t.Helper()

	c := ovrin.New(ovrin.WithModel(explainModel{reply: map[string]any{
		"vendor": "Northwind Traders",
		"total":  2500.0,
	}}))
	res, err := ovrin.Extract[explainDoc](context.Background(), c,
		ovrin.Bytes([]byte("vendor,total\nNorthwind Traders,2500.00\n")))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return res
}

// An Explanation is the field it describes, or it is worthless: a review queue
// that shows a value from one field beside the confidence of another is worse
// than one that shows nothing.
func TestExplainDescribesTheFieldItNames(t *testing.T) {
	t.Parallel()

	res := explained(t)

	for _, key := range []string{"vendor", "total", "balance"} {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			e, ok := res.Explain(key)
			if !ok {
				t.Fatalf("Explain(%q) returned ok = false for a field that is in the schema", key)
			}
			f := res.Fields[key]

			if e.Field != key {
				t.Errorf("Explain(%q).Field = %q", key, e.Field)
			}
			if !reflect.DeepEqual(e.Value, f.Value) {
				t.Errorf("Explain(%q).Value = %v, but Fields[%q].Value = %v", key, e.Value, key, f.Value)
			}
			if e.Found != f.Found {
				t.Errorf("Explain(%q).Found = %v, but Fields[%q].Found = %v", key, e.Found, key, f.Found)
			}
			if e.Confidence != f.Confidence {
				t.Errorf("Explain(%q).Confidence = %v, but Fields[%q].Confidence = %v",
					key, e.Confidence, key, f.Confidence)
			}
			if !reflect.DeepEqual(e.Signals, f.Signals) {
				t.Errorf("Explain(%q).Signals = %v, but Fields[%q].Signals = %v",
					key, e.Signals, key, f.Signals)
			}
			if !reflect.DeepEqual(e.Provenance, f.Provenance) {
				t.Errorf("Explain(%q).Provenance = %v, but Fields[%q].Provenance = %v",
					key, e.Provenance, key, f.Provenance)
			}
			if !reflect.DeepEqual(e.Candidates, f.Candidates) {
				t.Errorf("Explain(%q).Candidates = %v, but Fields[%q].Candidates = %v",
					key, e.Candidates, key, f.Candidates)
			}
			if !reflect.DeepEqual(e.Validation, f.Validation) {
				t.Errorf("Explain(%q).Validation = %v, but Fields[%q].Validation = %v",
					key, e.Validation, key, f.Validation)
			}
		})
	}
}

// The Scorer contract says a caller must be able to check a confidence by
// hand. An Explanation is where they do it, so the signals it carries have to
// account for the number it reports beside them.
func TestExplainedSignalsAccountForTheConfidence(t *testing.T) {
	t.Parallel()

	res := explained(t)
	e, ok := res.Explain("total")
	if !ok {
		t.Fatal(`Explain("total") returned ok = false`)
	}
	if len(e.Signals) == 0 {
		t.Fatal("the explanation carries no signals, so its confidence cannot be checked by hand")
	}

	var sum, weight float64
	capped := false
	for _, s := range e.Signals {
		if s.Value < 0 || s.Value > 1 {
			t.Errorf("signal %q has value %v, which is not on 0..1", s.Name, s.Value)
		}
		// A ceiling is recorded as a signal with no weight; it explains a
		// confidence lower than the mean rather than contributing to it.
		if strings.HasPrefix(s.Name, "capped:") {
			capped = true
			continue
		}
		sum += s.Value * s.Weight
		weight += s.Weight
	}
	if weight == 0 {
		t.Fatal("every weighted signal has zero weight, so the mean is undefined")
	}

	mean := math.Round(sum/weight*100) / 100
	switch {
	case !capped && math.Abs(e.Confidence-mean) > 0.005:
		t.Errorf("Explain(\"total\").Confidence = %v, but its signals average to %v and nothing caps it",
			e.Confidence, mean)
	case capped && e.Confidence > mean+0.005:
		t.Errorf("Explain(\"total\").Confidence = %v is above the mean of %v although a ceiling was applied",
			e.Confidence, mean)
	}
}

// Explain collects the reasons for its own field and no others.
//
// The bug this prevents is a reviewer being told a field needs attention
// because a different field does. Balance was never answered and is required;
// vendor and total were read and are clean, so the reasons must not be shared
// between them.
func TestExplainCarriesOnlyItsOwnFieldsReasons(t *testing.T) {
	t.Parallel()

	res := explained(t)
	if !res.NeedsReview {
		t.Fatal("a required field was absent and the result was not flagged for review")
	}

	for _, key := range []string{"vendor", "total", "balance"} {
		e, ok := res.Explain(key)
		if !ok {
			t.Fatalf("Explain(%q) returned ok = false", key)
		}

		var want []ovrin.ReviewReason
		for _, r := range res.Reasons {
			if r.Field == key {
				want = append(want, r)
			}
		}
		if !reflect.DeepEqual(e.Reasons, want) {
			t.Errorf("Explain(%q).Reasons = %v, want the %d reason(s) Result.Reasons records for that field: %v",
				key, e.Reasons, len(want), want)
		}
		for _, r := range e.Reasons {
			if r.Field != key {
				t.Errorf("Explain(%q) carries a reason belonging to field %q: %v", key, r.Field, r)
			}
		}
	}

	// The fixture only proves the filtering works if there is something to
	// filter out.
	e, _ := res.Explain("balance")
	if len(e.Reasons) == 0 {
		t.Error("the absent required field carries no reason, so nothing was filtered and the test above proved little")
	}
	if e, _ := res.Explain("total"); len(e.Reasons) != 0 {
		t.Errorf("a field that read cleanly carries reasons: %v", e.Reasons)
	}
}

// A key that is not in the schema is not a field with nothing known about it,
// and the two must not be confused: the second return is how a caller tells a
// typo from a value that could not be read.
func TestExplainReportsAnUnknownFieldRatherThanInventingOne(t *testing.T) {
	t.Parallel()

	res := explained(t)

	for _, key := range []string{"", "no_such_field", "vendor.name", "Total", "items[0]"} {
		e, ok := res.Explain(key)
		if ok {
			t.Errorf("Explain(%q) returned ok = true for a key that is not in Fields", key)
		}
		if e != nil {
			t.Errorf("Explain(%q) returned a non-nil Explanation alongside ok = false: %+v", key, e)
		}
	}

	// Keys are the Go field path in snake case, and a field that exists must
	// still be found — otherwise the loop above would pass for a broken
	// lookup.
	if _, ok := res.Explain("balance"); !ok {
		t.Error(`Explain("balance") returned ok = false, so the negative cases above prove nothing`)
	}
}
