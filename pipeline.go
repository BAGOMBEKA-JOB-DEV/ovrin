package ovrin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/img"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/jsonschema"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/prompt"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// The pipeline lives in the root package rather than under internal/ because
// it touches nearly the whole public type set — Model, OCR, Page, Content,
// FieldResult, Signal, Provenance, Metadata, Event. An internal package would
// have to declare a local twin of each and convert at every stage boundary,
// since nothing under internal/ may import the root. That is a great deal of
// mechanical code to buy a package boundary the compiler already gives us
// through unexported identifiers.
//
// docs/architecture.md records this.

// reading is the outcome of the acquisition stage: the document's content in
// whatever form the pipeline managed to obtain it.
type reading struct {
	doc      Document
	pages    []prompt.PageContent // what goes to the model
	text     *normalise.Result    // nil on a vision-only reading
	kinds    []Reading            // which reading served each page
	provider string               // the OCR provider, if one served
}

// run executes the nine stages and returns everything Extract needs to build a
// Result. It never touches the Client, so concurrent extractions with different
// per-call options cannot interfere.
func run(ctx context.Context, cfg *config, src Source, sch *schema.Schema) (*outcome, error) {
	started := time.Now()
	meta := Metadata{Providers: map[Op]string{}}

	// Stage 1 — detect. Format by content, never by name.
	data, doc, err := stageDetect(ctx, cfg, src, &meta)
	if err != nil {
		return nil, err
	}

	// Stage 2 and 3 — acquire and normalise.
	rd, err := stageAcquire(ctx, cfg, data, doc, &meta)
	if err != nil {
		return nil, err
	}
	meta.Readings = rd.kinds
	meta.Pages = rd.doc.Pages

	// Stage 4 — schema. Reflection already happened in Extract, which is why
	// a malformed tag costs nothing: it fails before a provider is contacted.
	jsonSchema, err := stageSchema(ctx, cfg, sch)
	if err != nil {
		return nil, err
	}

	// Stage 5 — prompt. The instruction is built from the schema and never
	// from the document; see internal/prompt.
	req, err := stagePrompt(ctx, cfg, sch, jsonSchema, rd)
	if err != nil {
		return nil, err
	}

	// Stage 6 — generate.
	raw, usage, err := stageGenerate(ctx, cfg, req, &meta)
	if err != nil {
		return nil, err
	}
	meta.Usage = usage

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		// The bytes are attached to nothing: they are the model's reply, and a
		// reply that failed to parse may contain document content quoted back.
		return nil, &Error{
			Op:      OpGenerate,
			Kind:    ErrBadResponse,
			Message: "the reply is not a JSON object",
		}
	}

	meta.Kind = rd.doc.Kind
	meta.Duration = time.Since(started)

	return &outcome{
		object:   object,
		text:     rd.text,
		reading:  primaryReading(rd.kinds),
		provider: rd.provider,
		findings: findingsOf(rd.text),
		meta:     meta,
	}, nil
}

// outcome is everything the assembly step needs. It is separate from Result[T]
// because assembly is generic and the pipeline is not — a generic pipeline
// would have to be re-instantiated for every schema type for no benefit.
type outcome struct {
	object   map[string]any
	text     *normalise.Result
	reading  Reading
	provider string
	findings []normalise.Finding
	meta     Metadata
}

func stageDetect(ctx context.Context, cfg *config, src Source, meta *Metadata) ([]byte, Document, error) {
	start := time.Now()
	lim := limitsOf(cfg)

	ds, err := detectSource(src)
	if err != nil {
		return nil, Document{}, classify(OpDetect, "", err)
	}

	data, err := detect.Load(ctx, ds, lim)
	if err != nil {
		cfg.emit(ctx, Event{Op: OpDetect, Duration: time.Since(start), Err: err})
		return nil, Document{}, classify(OpDetect, "", err)
	}

	kind, err := detect.Identify(data, lim)
	if err != nil {
		cfg.emit(ctx, Event{Op: OpDetect, Duration: time.Since(start), Err: err})
		return nil, Document{}, classify(OpDetect, "", err)
	}

	doc := Document{Kind: Kind(kind), Bytes: int64(len(data))}
	if doc.Kind == KindPNG || doc.Kind == KindJPEG || doc.Kind == KindWebP {
		doc.Pages = 1
	}
	meta.Kind = doc.Kind
	cfg.emit(ctx, Event{Op: OpDetect, Duration: time.Since(start), Bytes: doc.Bytes, Pages: doc.Pages})
	return data, doc, nil
}

// stageAcquire obtains content, choosing a reading per docs/pipeline.md stage 2.
//
// Phase 2 implements the image paths. A PDF needs either a text-layer parser or
// a renderer, and neither exists yet, so it is refused with an error that names
// the remedies rather than returning nothing and letting the caller guess.
func stageAcquire(ctx context.Context, cfg *config, data []byte, doc Document, meta *Metadata) (*reading, error) {
	start := time.Now()

	switch doc.Kind {
	case KindPNG, KindJPEG, KindTIFF, KindWebP:
	default:
		return nil, &Error{
			Op:   OpAcquire,
			Kind: ErrNoProvider,
			Message: fmt.Sprintf("%s is not readable yet: this build handles images "+
				"(PNG, JPEG) through OCR or a vision-capable model", doc.Kind),
		}
	}

	page, err := img.Decode(data, img.Kind(doc.Kind), cfg.maxPagePixels)
	if err != nil {
		cfg.emit(ctx, Event{Op: OpAcquire, Duration: time.Since(start), Err: err})
		return nil, classify(OpAcquire, "", err)
	}

	// An OCR provider gives text, and text is what grounding needs. Prefer it
	// over vision for that reason alone: a vision reading produces no source
	// text, so a value cannot be checked against the document it came from.
	if cfg.ocr != nil && cfg.reading != ModeVision {
		rd, err := acquireByOCR(ctx, cfg, page, doc, meta, start)
		if err != nil {
			return nil, err
		}
		return rd, nil
	}

	if cfg.reading == ModeOCR || cfg.reading == ModeText {
		return nil, &Error{
			Op:      OpAcquire,
			Kind:    ErrNoProvider,
			Message: "the requested reading needs an OCR provider; configure one with WithOCR",
		}
	}

	cfg.emit(ctx, Event{Op: OpAcquire, Page: 1, Duration: time.Since(start), Pages: 1})
	return &reading{
		doc:   Document{Kind: doc.Kind, Pages: 1, Bytes: doc.Bytes},
		kinds: []Reading{ReadingVision},
		pages: []prompt.PageContent{{
			Number:    1,
			Reading:   prompt.Reading(ReadingVision),
			Image:     data,
			MediaType: mediaTypeOf(doc.Kind),
		}},
	}, nil
}

func acquireByOCR(ctx context.Context, cfg *config, page *img.Page, doc Document, meta *Metadata, start time.Time) (*reading, error) {
	rec, err := cfg.ocr.Recognise(ctx, Page{
		Number: page.Number,
		Image:  page.Image,
		Width:  page.Width,
		Height: page.Height,
		DPI:    page.DPI,
	})
	name := cfg.ocr.Name()
	cfg.emit(ctx, Event{Op: OpOCR, Provider: name, Page: 1, Duration: time.Since(start), Err: err})
	if err != nil {
		return nil, classify(OpOCR, name, err)
	}
	meta.Providers[OpOCR] = name

	words := make([]normalise.Word, 0, len(rec.Words))
	for _, w := range rec.Words {
		words = append(words, normalise.Word{
			Text:       w.Text,
			Box:        normalise.Rect(w.Box),
			Confidence: w.Confidence,
		})
	}

	nStart := time.Now()
	res := normalise.Normalise(normalise.Input{Pages: []normalise.Page{{
		Number: 1,
		Width:  page.Width,
		Height: page.Height,
		Words:  words,
	}}})
	cfg.emit(ctx, Event{Op: OpNormalise, Duration: time.Since(nStart), Bytes: int64(len(res.Text))})

	if int64(len(res.Text)) > cfg.maxTextBytes {
		return nil, &Error{
			Op:      OpNormalise,
			Kind:    ErrLimitExceeded,
			Message: "extracted text exceeds the limit, raise with WithMaxTextBytes",
		}
	}

	return &reading{
		doc:      Document{Kind: doc.Kind, Pages: 1, Bytes: doc.Bytes},
		kinds:    []Reading{ReadingOCR},
		provider: name,
		text:     res,
		pages: []prompt.PageContent{{
			Number:  1,
			Reading: prompt.Reading(ReadingOCR),
			Text:    res.Text,
		}},
	}, nil
}

func stageSchema(ctx context.Context, cfg *config, sch *schema.Schema) ([]byte, error) {
	start := time.Now()
	b, err := jsonschema.Marshal(*sch)
	cfg.emit(ctx, Event{Op: OpSchema, Duration: time.Since(start), Fields: len(sch.Fields), Err: err})
	if err != nil {
		return nil, classify(OpSchema, "", err)
	}
	return b, nil
}

func stagePrompt(ctx context.Context, cfg *config, sch *schema.Schema, jsonSchema []byte, rd *reading) (ModelRequest, error) {
	start := time.Now()
	req, err := prompt.Build(*sch, jsonSchema, rd.pages)
	cfg.emit(ctx, Event{Op: OpPrompt, Duration: time.Since(start), Err: err})
	if err != nil {
		return ModelRequest{}, classify(OpPrompt, "", err)
	}

	content := make([]Content, 0, len(req.Content))
	for _, c := range req.Content {
		content = append(content, Content{
			Reading:   Reading(c.Reading),
			Page:      c.Page,
			Text:      c.Text,
			Image:     c.Image,
			MediaType: c.MediaType,
		})
	}
	return ModelRequest{
		Instruction: req.Instruction,
		Content:     content,
		Schema:      req.Schema,
		Temperature: req.Temperature,
	}, nil
}

func stageGenerate(ctx context.Context, cfg *config, req ModelRequest, meta *Metadata) ([]byte, Usage, error) {
	if cfg.model == nil {
		return nil, Usage{}, &Error{
			Op:      OpGenerate,
			Kind:    ErrNoProvider,
			Message: "no model is configured; pass one with WithModel",
		}
	}
	start := time.Now()
	resp, err := cfg.model.Generate(ctx, req)
	cfg.emit(ctx, Event{Op: OpGenerate, Duration: time.Since(start), Attempt: 1, Err: err})
	if err != nil {
		return nil, Usage{}, classify(OpGenerate, "", err)
	}
	if resp == nil || len(resp.JSON) == 0 {
		return nil, Usage{}, &Error{
			Op:      OpGenerate,
			Kind:    ErrBadResponse,
			Message: "the provider returned no content",
		}
	}
	meta.Providers[OpGenerate] = "model"
	return resp.JSON, resp.Usage, nil
}

// emit calls the configured hook, if there is one. Hooks run synchronously on
// the calling goroutine, which is documented: a hook that blocks slows the
// extraction, and that is the caller's to manage.
func (c *config) emit(ctx context.Context, ev Event) {
	if c.hook == nil {
		return
	}
	c.hook(ctx, ev)
}

func limitsOf(c *config) detect.Limits {
	return detect.Limits{
		MaxSourceBytes:       c.maxSourceBytes,
		MaxDecompressedBytes: c.maxDecompressedBytes,
		MaxStreamBytes:       c.maxStreamBytes,
		MaxTextBytes:         c.maxTextBytes,
		MaxPages:             c.maxPages,
		MaxDepth:             c.maxDepth,
		MaxObjects:           c.maxObjects,
		MaxPagePixels:        c.maxPagePixels,
	}
}

// detectSource converts the closed public Source into the internal one. The
// two sets are the same three shapes, and the conversion exists only because
// internal packages cannot import the root.
func detectSource(src Source) (detect.Source, error) {
	switch s := src.(type) {
	case readerSource:
		return detect.Reader(s.r), nil
	case bytesSource:
		return detect.Bytes(s.b), nil
	case fileSource:
		return detect.File(s.path), nil
	case nil:
		return nil, &Error{Op: OpDetect, Kind: ErrNoContent, Message: "no source was given"}
	default:
		// Unreachable: Source is closed. If it ever is reached, something in
		// this package added a Source and forgot this switch.
		return nil, &Error{Op: OpDetect, Kind: ErrInternal, Message: "unknown source kind"}
	}
}

func mediaTypeOf(k Kind) string {
	switch k {
	case KindPNG:
		return "image/png"
	case KindJPEG:
		return "image/jpeg"
	case KindTIFF:
		return "image/tiff"
	case KindWebP:
		return "image/webp"
	default:
		return ""
	}
}

func primaryReading(kinds []Reading) Reading {
	if len(kinds) == 0 {
		return ReadingUnknown
	}
	return kinds[0]
}

func findingsOf(r *normalise.Result) []normalise.Finding {
	if r == nil {
		return nil
	}
	return r.Findings
}

// classify turns an internal package's error into an *Error carrying the right
// sentinel and Op, so that a caller tests a sentinel rather than a message.
func classify(op Op, provider string, err error) error {
	if err == nil {
		return nil
	}
	var already *Error
	if errors.As(err, &already) {
		return err
	}

	kind := ErrInternal
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return (&Error{Op: op, Provider: provider, Kind: ErrUnavailable,
			Message: "the context ended before the stage completed"}).WithCause(err)
	case errors.Is(err, detect.ErrUnsupportedFormat), errors.Is(err, img.ErrUnsupportedFormat):
		kind = ErrUnsupportedFormat
	case errors.Is(err, detect.ErrLimitExceeded), errors.Is(err, img.ErrLimitExceeded):
		kind = ErrLimitExceeded
	case errors.Is(err, detect.ErrEncrypted):
		kind = ErrEncrypted
	case errors.Is(err, img.ErrDecode):
		kind = ErrNoContent
	case errors.Is(err, schema.ErrSchema):
		kind = ErrSchema
	case errors.Is(err, prompt.ErrSchema):
		kind = ErrSchema
	case errors.Is(err, prompt.ErrNoContent):
		kind = ErrNoContent
	case errors.Is(err, prompt.ErrBoundary), errors.Is(err, prompt.ErrAmbiguousContent):
		// ovrin's fault, not the document's. See ADR-0030.
		kind = ErrInternal
	}

	return (&Error{
		Op:       op,
		Provider: provider,
		Kind:     kind,
		Message:  err.Error(),
	}).WithCause(err)
}
