package ovrin

import (
	"reflect"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/ground"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// assemble walks the schema and the model's reply together, validating,
// grounding and scoring each field, and writing the values into a T.
//
// The walk is driven by the schema rather than by the reply, which is what
// makes a field the model omitted representable: it produces a FieldResult with
// Found false rather than simply not appearing. A reply-driven walk would
// silently lose exactly the information the caller most needs.
func assemble[T any](out *outcome, sch *schema.Schema, cfg *config) *Result[T] {
	var data T
	dst := reflect.ValueOf(&data).Elem()

	v := validate.New(
		validate.WithDateOrder(validate.DateOrder(cfg.dateOrder)),
	)

	a := &assembler{
		out:     out,
		cfg:     cfg,
		v:       v,
		fields:  make(map[string]FieldResult),
		suspect: pagesWithFindings(out.findings),
	}
	a.walk(sch.Fields, out.object, dst, "")

	res := &Result[T]{
		Data:     data,
		Fields:   a.fields,
		Metadata: out.meta,
		Valid:    a.valid,
		Reasons:  a.reasons,
	}
	res.Confidence = a.aggregate()
	res.NeedsReview = len(a.reasons) > 0
	return res
}

type assembler struct {
	out     *outcome
	cfg     *config
	v       *validate.Validator
	fields  map[string]FieldResult
	reasons []ReviewReason
	suspect map[int]bool

	valid    bool
	required []float64 // confidences of required fields
	optional []float64
}

// walk processes one level of the schema against one level of the reply.
func (a *assembler) walk(fields []schema.Field, object map[string]any, dst reflect.Value, prefix string) {
	a.valid = true
	for i := range fields {
		f := fields[i]
		key := f.Key
		if prefix != "" {
			key = prefix + "." + leaf(f.Key)
		}
		raw := lookup(object, leaf(f.Key))
		a.field(f, raw, dst, key)
	}
}

func (a *assembler) field(f schema.Field, raw any, dst reflect.Value, key string) {
	target := fieldByName(dst, f.GoName)

	switch f.Kind {
	case schema.KindObject:
		nested, _ := raw.(map[string]any)
		inner := target
		if inner.IsValid() && inner.Kind() == reflect.Pointer {
			if nested != nil {
				inner.Set(reflect.New(inner.Type().Elem()))
				inner = inner.Elem()
			}
		}
		for i := range f.Fields {
			sub := f.Fields[i]
			a.field(sub, lookup(nested, leaf(sub.Key)), inner, key+"."+leaf(sub.Key))
		}
		a.fields[key] = FieldResult{Found: nested != nil, Valid: true, Confidence: 1}
		return

	case schema.KindArray:
		items, _ := raw.([]any)
		if target.IsValid() && target.Kind() == reflect.Slice && len(items) > 0 {
			target.Set(reflect.MakeSlice(target.Type(), len(items), len(items)))
		}
		for i, item := range items {
			ek := schema.IndexKey(key, i)
			if f.Elem == nil {
				continue
			}
			var elem reflect.Value
			if target.IsValid() && i < target.Len() {
				elem = target.Index(i)
			}
			if f.Elem.Kind == schema.KindObject {
				nested, _ := item.(map[string]any)
				for j := range f.Elem.Fields {
					sub := f.Elem.Fields[j]
					a.field(sub, lookup(nested, leaf(sub.Key)), elem, ek+"."+leaf(sub.Key))
				}
				continue
			}
			a.scalar(*f.Elem, item, elem, ek)
		}
		a.fields[key] = FieldResult{Found: items != nil, Valid: true, Confidence: 1}
		return
	}

	a.scalar(f, raw, target, key)
}

// scalar validates, grounds and scores one leaf value.
func (a *assembler) scalar(f schema.Field, raw any, target reflect.Value, key string) {
	vr := a.v.Field(f, raw)

	if vr.Converted && target.IsValid() && target.CanSet() {
		set(target, vr.Value)
	}

	// Grounding: does this value actually appear in the document? A value that
	// does not was not read from it. On a vision reading there is no source
	// text at all, so the signal is absent rather than zero.
	var gr ground.Result
	if a.out.text != nil && vr.Found {
		gr = ground.Ground(a.out.text, vr.Value, ground.KindOf(vr.Value),
			ground.WithDateOrder(ground.DateOrder(a.cfg.dateOrder)))
	}

	ev := FieldEvidence{
		Field:      key,
		Value:      vr.Value,
		Found:      vr.Found,
		Reading:    a.out.reading,
		Grounding:  gr.Grounding,
		Validation: ruleResults(vr.Rules),
		Suspicious: a.suspect[gr.Page] || a.suspect[0],
	}
	if gr.Applicable {
		ev.Provenance = []Provenance{provenanceOf(gr, a.out.reading, a.out.provider)}
	}

	conf, signals := a.score(f, vr, gr, ev)

	fr := FieldResult{
		Value:      vr.Value,
		Found:      vr.Found,
		Confidence: conf,
		Valid:      vr.Valid(),
		Signals:    signals,
		Provenance: ev.Provenance,
	}
	if vr.Message != "" {
		fr.Errors = append(fr.Errors, &Error{Op: OpValidate, Field: key, Kind: ErrSchema, Message: vr.Message})
	}
	for _, r := range vr.Rules {
		if !r.Passed {
			fr.Errors = append(fr.Errors, &Error{Op: OpValidate, Field: key, Kind: ErrSchema, Message: r.Rule + ": " + r.Message})
		}
	}
	a.fields[key] = fr

	if !fr.Valid {
		a.valid = false
	}
	a.recordReasons(f, key, vr, gr, conf)
	if required(f) {
		a.required = append(a.required, conf)
	} else {
		a.optional = append(a.optional, conf)
	}
}

func (a *assembler) recordReasons(f schema.Field, key string, vr validate.Result, gr ground.Result, conf float64) {
	add := func(why string) { a.reasons = append(a.reasons, ReviewReason{Field: key, Why: why}) }

	switch {
	case required(f) && !vr.Found:
		add("a required field was not found in the document")
	case gr.Applicable && gr.Grounding == ground.NotFound:
		add(ground.ReasonNotFound)
	}
	if vr.Ambiguity != nil {
		add("the date is ambiguous and ovrin will not guess which reading is meant")
	}
	if a.suspect[gr.Page] || a.suspect[0] {
		add("the source page carried content that looked like an injection attempt")
	}
	if conf < a.cfg.reviewThreshold && vr.Found {
		add("confidence is below the review threshold")
	}
}

// aggregate is the mean over fields, weighted so that a missing optional field
// does not drag down a document that is otherwise clean.
func (a *assembler) aggregate() float64 {
	const optionalWeight = 0.5
	var sum, weight float64
	for _, c := range a.required {
		sum += c
		weight++
	}
	for _, c := range a.optional {
		sum += c * optionalWeight
		weight += optionalWeight
	}
	if weight == 0 {
		return 0
	}
	return round2(sum / weight)
}

func required(f schema.Field) bool {
	for _, r := range f.Rules {
		if r.Name == schema.RuleRequired {
			return true
		}
	}
	return false
}

func ruleResults(in []validate.RuleResult) []RuleResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]RuleResult, 0, len(in))
	for _, r := range in {
		out = append(out, RuleResult(r))
	}
	return out
}

func provenanceOf(gr ground.Result, rd Reading, provider string) Provenance {
	p := Provenance{Reading: rd, Page: gr.Page, Exact: gr.Exact, Method: methodOf(rd, provider)}
	if gr.Span != nil {
		s := Span(*gr.Span)
		p.Span = &s
	}
	if gr.Box != nil {
		b := Rect(*gr.Box)
		p.Box = &b
	}
	return p
}

func methodOf(rd Reading, provider string) string {
	switch rd {
	case ReadingText:
		return "text-layer"
	case ReadingOCR:
		if provider != "" {
			return "ocr:" + provider
		}
		return "ocr"
	case ReadingVision:
		return "vision"
	default:
		return rd.String()
	}
}

func pagesWithFindings(fs []normalise.Finding) map[int]bool {
	if len(fs) == 0 {
		return nil
	}
	m := make(map[int]bool, len(fs))
	for _, f := range fs {
		m[f.Page] = true
	}
	return m
}

// lookup reads a property from the reply, tolerating the absence of the whole
// object so that a missing branch reports every field under it as not found.
func lookup(object map[string]any, name string) any {
	if object == nil {
		return nil
	}
	if v, ok := object[name]; ok {
		return v
	}
	// The reply came from a schema we wrote, so an exact match is the norm.
	// A case-insensitive second look costs nothing and rescues a provider
	// that title-cased its keys.
	for k, v := range object {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}

// leaf returns the last path element of a field key, which is the property
// name in the reply: "vendor.name" is {"vendor":{"name":…}}.
func leaf(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		key = key[i+1:]
	}
	if i := strings.Index(key, "["); i >= 0 {
		key = key[:i]
	}
	return key
}

func fieldByName(dst reflect.Value, name string) reflect.Value {
	if !dst.IsValid() || dst.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return dst.FieldByName(name)
}

// set writes a validated value into the destination field, following one level
// of pointer so that an optional field distinguishes absent from zero.
func set(target reflect.Value, v any) {
	if v == nil {
		return
	}
	rv := reflect.ValueOf(v)
	if target.Kind() == reflect.Pointer {
		if !rv.Type().AssignableTo(target.Type().Elem()) {
			return
		}
		p := reflect.New(target.Type().Elem())
		p.Elem().Set(rv)
		target.Set(p)
		return
	}
	if rv.Type().AssignableTo(target.Type()) {
		target.Set(rv)
	}
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
