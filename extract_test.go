package ovrin_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// replyModel returns whatever it is given, so a test can state precisely what
// the model said and assert what ovrin made of it.
type replyModel struct{ reply map[string]any }

func (m replyModel) Generate(context.Context, ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	b, err := json.Marshal(m.reply)
	if err != nil {
		return nil, err
	}
	return &ovrin.ModelResponse{JSON: b}, nil
}

type wordOCR struct{ words []string }

func (wordOCR) Name() string { return "test" }

func (o wordOCR) Recognise(_ context.Context, p ovrin.Page) (*ovrin.Recognition, error) {
	rec := &ovrin.Recognition{Confidence: 0.95}
	// Lay the words out inside the page. Words positioned outside the media box
	// are one of the things normalise flags as suspicious, so a fixture that
	// ignored the page size would be testing that detector rather than this.
	x, y := 0.0, p.Height/2
	step := p.Width / float64(len(o.words)+1)
	for _, w := range o.words {
		x += step
		rec.Words = append(rec.Words, ovrin.Word{
			Text: w, Confidence: 0.95,
			Box: ovrin.Rect{MinX: x, MinY: y, MaxX: x + step*0.8, MaxY: y + p.Height*0.05},
		})
	}
	return rec, nil
}

func extract[T any](t *testing.T, reply map[string]any, words []string, opts ...ovrin.Option) *ovrin.Result[T] {
	t.Helper()
	base := []ovrin.Option{
		ovrin.WithModel(replyModel{reply: reply}),
		ovrin.WithOCR(wordOCR{words: words}),
	}
	c := ovrin.New(append(base, opts...)...)
	res, err := ovrin.Extract[T](context.Background(), c, ovrin.Bytes(testPNG()))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return res
}

// An absent field must be distinguishable from a field that is genuinely zero.
// This is why Found exists at all: a payments system that cannot tell "the
// total is zero" from "we could not read the total" will eventually pay the
// wrong amount (docs/rules.md §8.5).
func TestAbsentFieldIsNotAZeroValue(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Total   float64 `ovrin:"total amount"`
		Balance float64 `ovrin:"closing balance"`
	}

	// The document really does say the balance is zero. It says nothing at all
	// about the total.
	res := extract[Doc](t, map[string]any{"balance": 0.0}, []string{"Balance", "0.00"})

	total, balance := res.Fields["total"], res.Fields["balance"]

	if total.Found {
		t.Error("a field the model never returned is reported as found")
	}
	if !balance.Found {
		t.Error("a field the model returned as zero is reported as not found")
	}
	if res.Data.Total != 0 || res.Data.Balance != 0 {
		t.Errorf("Data = %+v, want both zero", res.Data)
	}

	// Both are 0 in Data. Only Fields can tell them apart, and that is the
	// entire point of the type.
	if total.Found == balance.Found {
		t.Fatal("an absent field and a zero field are indistinguishable, which is the bug this type exists to prevent")
	}
}

// docs/confidence.md claims every score decomposes into its signals. A reader
// must be able to do the arithmetic, so this does it.
func TestConfidenceIsTheWeightedMeanOfItsSignals(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Vendor string `ovrin:"vendor name,required"`
	}
	res := extract[Doc](t, map[string]any{"vendor": "Kampala Supplies"},
		[]string{"Kampala", "Supplies"})

	f := res.Fields["vendor"]
	if len(f.Signals) == 0 {
		t.Fatal("no signals were recorded, so the confidence cannot be checked")
	}

	var sum, weight float64
	capped := false
	for _, s := range f.Signals {
		if s.Value < 0 || s.Value > 1 {
			t.Errorf("signal %q has value %v, which is not on 0..1", s.Name, s.Value)
		}
		if strings.HasPrefix(s.Name, "capped:") {
			capped = true
			continue
		}
		sum += s.Value * s.Weight
		weight += s.Weight
	}
	if weight == 0 {
		t.Fatal("every signal has zero weight, so the mean is undefined")
	}

	// Rounded to two places, as Score reports it.
	mean := math.Round(sum/weight*100) / 100
	switch {
	case !capped && math.Abs(f.Confidence-mean) > 0.005:
		t.Errorf("confidence = %v, but its signals average to %v and nothing caps it",
			f.Confidence, mean)
	case capped && f.Confidence > mean+0.005:
		t.Errorf("confidence = %v is above the mean of %v although a ceiling was applied",
			f.Confidence, mean)
	}

	// An absent signal must be excluded from the denominator, not scored zero:
	// this reading has no agreement signal because only one reading ran.
	for _, s := range f.Signals {
		if s.Name == ovrin.SignalAgreement && s.Weight != 0 {
			t.Error("an agreement signal was weighted although only one reading ran")
		}
	}
}

// New promises to panic on a nil provider, because the alternative is a nil
// dereference on the first extraction, a long way from the mistake
// (docs/rules.md §1.6).
func TestNewPanicsOnANilProvider(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func()
	}{
		{"a nil model", func() { ovrin.New(ovrin.WithModel(nil)) }},
		{"a nil OCR provider", func() { ovrin.New(ovrin.WithOCR(nil)) }},
		{"a nil renderer", func() { ovrin.New(ovrin.WithRenderer(nil)) }},
		{"a nil scorer", func() { ovrin.New(ovrin.WithScorer(nil)) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Error("did not panic")
				}
			}()
			tc.call()
		})
	}
}

// Omitting a model entirely is configuration rather than programmer error, so
// it is an error from Extract rather than a panic from New — and the message
// says what to do about it.
func TestNoModelIsAnErrorNotAPanic(t *testing.T) {
	t.Parallel()

	type Doc struct {
		X string `ovrin:"a field"`
	}
	c := ovrin.New()
	res, err := ovrin.Extract[Doc](context.Background(), c, ovrin.Bytes(testPNG()))

	if !errors.Is(err, ovrin.ErrNoProvider) {
		t.Fatalf("Extract error = %v, want ErrNoProvider", err)
	}
	if res != nil {
		// ADR-0004: an error means nothing usable came back.
		t.Error("a Result was returned alongside an error")
	}
}

// Provider options configure a Client. Passing one to a single call is
// meaningless, and being refused is better than being ignored (§6.1).
func TestProviderOptionsAreRefusedPerCall(t *testing.T) {
	t.Parallel()

	type Doc struct {
		X string `ovrin:"a field"`
	}
	c := ovrin.New(ovrin.WithModel(replyModel{reply: map[string]any{"x": "y"}}))

	_, err := ovrin.Extract[Doc](context.Background(), c, ovrin.Bytes(testPNG()),
		ovrin.WithModel(replyModel{reply: map[string]any{"x": "z"}}))

	if !errors.Is(err, ovrin.ErrBadRequest) {
		t.Fatalf("Extract error = %v, want ErrBadRequest", err)
	}
}

// A malformed schema costs nothing: it is refused before a provider is
// contacted, so a typo in a tag is not a billed round trip.
func TestSchemaErrorsCostNothing(t *testing.T) {
	t.Parallel()

	type Bad struct {
		// A rule name that is not in the closed vocabulary. "mandatory" is
		// what somebody arriving from another validation library reasonably
		// tries, and the closed vocabulary is what makes it a loud error
		// rather than a rule that silently does nothing.
		X string `ovrin:"a field,mandatory"`
	}
	called := false
	c := ovrin.New(ovrin.WithModel(spyModel{called: &called}))

	_, err := ovrin.Extract[Bad](context.Background(), c, ovrin.Bytes(testPNG()))
	if !errors.Is(err, ovrin.ErrSchema) {
		t.Fatalf("Extract error = %v, want ErrSchema", err)
	}
	if called {
		t.Error("the model was contacted despite the schema being invalid")
	}
}

type spyModel struct{ called *bool }

func (m spyModel) Generate(context.Context, ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	*m.called = true
	return &ovrin.ModelResponse{JSON: []byte(`{}`)}, nil
}

// The cross_field signal is the one that catches a misread digit every other
// signal accepts: a wrong total is still a number, still passes its type, its
// format and its range. Only its relationship to the other fields betrays it.
func TestCrossFieldSignalFires(t *testing.T) {
	t.Parallel()

	type Invoice struct {
		Subtotal float64 `ovrin:"subtotal before tax,required,min=0"`
		VAT      float64 `ovrin:"tax amount,required,min=0"`
		Total    float64 `ovrin:"total including tax,required,min=0"`
	}

	rule := ovrin.Sum("total", ovrin.Tolerance{Absolute: 0.01}, "subtotal", "vat")
	words := []string{"SUBTOTAL", "1,240,000", "VAT", "223,200", "TOTAL", "1,463,200"}

	t.Run("consistent totals score and pass", func(t *testing.T) {
		t.Parallel()
		res := extract[Invoice](t, map[string]any{
			"subtotal": 1240000.0, "vat": 223200.0, "total": 1463200.0,
		}, words, ovrin.WithCrossField(rule))

		total := res.Fields["total"]
		if !hasSignal(total.Signals, ovrin.SignalCrossField) {
			t.Fatalf("no cross_field signal on total; signals: %v", names(total.Signals))
		}
		if !res.Valid {
			t.Errorf("Valid = false on a consistent document")
		}
	})

	t.Run("a total that does not add up is caught", func(t *testing.T) {
		t.Parallel()
		// Every field is a well-formed positive number satisfying min=0. Only
		// the arithmetic is wrong.
		res := extract[Invoice](t, map[string]any{
			"subtotal": 1240000.0, "vat": 223200.0, "total": 9999999.0,
		}, words, ovrin.WithCrossField(rule))

		if res.Valid {
			t.Error("Valid = true although the total does not add up")
		}
		if !res.NeedsReview {
			t.Error("NeedsReview = false although a cross-field rule failed")
		}

		total := res.Fields["total"]
		var cf ovrin.Signal
		for _, s := range total.Signals {
			if s.Name == ovrin.SignalCrossField {
				cf = s
			}
		}
		if cf.Name == "" {
			t.Fatalf("no cross_field signal; signals: %v", names(total.Signals))
		}
		if cf.Value != 0 {
			t.Errorf("cross_field = %v on a failing rule, want 0", cf.Value)
		}

		found := false
		for _, r := range res.Reasons {
			if r.Field == "total" && contains(r.Why, "cross-field") {
				found = true
			}
		}
		if !found {
			t.Errorf("no review reason names the cross-field failure; reasons: %v", res.Reasons)
		}
	})

	t.Run("a rule whose inputs are missing is not a failure", func(t *testing.T) {
		t.Parallel()
		// The subtotal was never extracted, so the sum cannot be checked. That
		// is not the total's fault: the missing field is already reported by
		// its own required rule, and blaming the total would punish the
		// document twice for one absence.
		res := extract[Invoice](t, map[string]any{
			"vat": 223200.0, "total": 1463200.0,
		}, words, ovrin.WithCrossField(rule))

		total := res.Fields["total"]
		if hasSignal(total.Signals, ovrin.SignalCrossField) {
			t.Error("a cross_field signal was recorded although the rule could not run")
		}
	})
}

func hasSignal(signals []ovrin.Signal, name string) bool {
	for _, s := range signals {
		if s.Name == name {
			return true
		}
	}
	return false
}

func names(signals []ovrin.Signal) []string {
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		out = append(out, s.Name)
	}
	return out
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
