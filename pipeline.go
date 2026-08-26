package ovrin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/img"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/jsonschema"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/office"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/pdf"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/prompt"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/retry"
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

	// unread are pages no reading could serve. They are reported rather than
	// left silently blank: a page ovrin could not read is data it dropped, and
	// dropping data silently is the one thing an adapter or a stage may never
	// do (docs/rules.md §6.1).
	unread []int

	// notes are things the caller should know that are not failures — a part
	// deliberately not extracted, hidden text found and kept.
	notes []string
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

	// ModeBoth takes a second, independent reading. Two readings fail in
	// uncorrelated ways — OCR misreads glyphs, a model misassigns fields — so
	// when they agree they are probably both right, and when they differ at
	// least one is definitely wrong and we know which field
	// (ADR-0014). It roughly doubles cost, which is why it is asked for
	// rather than assumed.
	var second *reading
	if cfg.reading == ModeBoth {
		second, err = stageSecondReading(ctx, cfg, data, doc, rd, &meta)
		if err != nil {
			// A second reading that cannot be taken is not a failure: the
			// first one stands, and the agreement signal is simply absent
			// rather than zero. Refusing the whole extraction because a
			// second opinion was unavailable would be worse than the single
			// opinion the caller would otherwise have had.
			cfg.emit(ctx, Event{Op: OpAcquire, Err: err})
			second = nil
		}
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

	// A parse failure is not handled here: stageRetry judges it along with
	// everything else, because "the reply is not JSON" is the clearest case of
	// a formatting mistake worth one more ask.
	var object map[string]any
	_ = json.Unmarshal(raw, &object) //nolint:errcheck // a nil object is the failure, and stageRetry reads it

	// Stage 6b — ask again, once, when the reply was malformed rather than
	// merely disappointing. See stageRetry.
	_, object = stageRetry(ctx, cfg, req, sch, raw, object, &meta)

	if object == nil {
		// The bytes are attached to nothing: they are the model's reply, and a
		// reply that failed to parse may contain document content quoted back.
		return nil, &Error{
			Op:      OpGenerate,
			Kind:    ErrBadResponse,
			Message: "the reply is not a JSON object",
		}
	}

	// The second reading goes through the same prompt, schema and model. Only
	// the content differs, which is the point: two readings of one document,
	// compared field by field.
	var secondObject map[string]any
	if second != nil {
		if obj, err := generateFrom(ctx, cfg, sch, jsonSchema, second, &meta); err == nil {
			secondObject = obj
			meta.Readings = append(meta.Readings, second.kinds...)
		} else {
			cfg.emit(ctx, Event{Op: OpGenerate, Err: err})
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
		unread:   rd.unread,
		notes:    rd.notes,
		second:   secondObject,
		secondReading: func() Reading {
			if second == nil {
				return ReadingUnknown
			}
			return primaryReading(second.kinds)
		}(),
		meta: meta,
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
	unread   []int
	notes    []string

	// second is the reply from a second, independent reading, and is nil
	// unless ModeBoth was asked for. secondReading names which reading it was.
	second        map[string]any
	secondReading Reading

	meta Metadata
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

	doc := Document{Kind: Kind(kind), Size: int64(len(data)), Data: data}
	if doc.Kind == KindPNG || doc.Kind == KindJPEG || doc.Kind == KindWebP {
		doc.Pages = 1
	}
	meta.Kind = doc.Kind
	cfg.emit(ctx, Event{Op: OpDetect, Duration: time.Since(start), Bytes: doc.Size, Pages: doc.Pages})
	return data, doc, nil
}

// stageAcquire obtains content, choosing a reading per docs/pipeline.md stage 2.
//
// Phase 2 implements the image paths. A PDF needs either a text-layer parser or
// a renderer, and neither exists yet, so it is refused with an error that names
// the remedies rather than returning nothing and letting the caller guess.
func stageAcquire(ctx context.Context, cfg *config, data []byte, doc Document, meta *Metadata) (*reading, error) {
	start := time.Now()

	// A PDF that carries its own characters is read directly. It is exact and
	// nearly free, and rendering those characters to pixels for a model to
	// read back would be a lossy round trip through the one reading that
	// cannot be wrong (ADR-0012).
	if doc.Kind == KindPDF && cfg.reading != ModeOCR && cfg.reading != ModeVision {
		rd, err := acquireByTextLayer(ctx, cfg, doc, meta, start)
		if err == nil {
			return rd, nil
		}
		// No usable text layer is not a failure: it is the ordinary state of a
		// scan, and the whole point of the staged design is that the next
		// reading takes over. Anything else — encryption, a filter we do not
		// implement, a limit — is real and stops here.
		if !errors.Is(err, ErrNoContent) || cfg.reading == ModeText {
			return nil, err
		}
	}

	// DOCX, XLSX and CSV carry their own text. Like a text-layer PDF they take
	// the exact, nearly-free path: no OCR, no renderer, no model call to
	// acquire content.
	if doc.Kind == KindDOCX || doc.Kind == KindXLSX || doc.Kind == KindCSV {
		return acquireByOffice(ctx, cfg, doc, start)
	}

	switch doc.Kind {
	case KindPNG, KindJPEG, KindTIFF, KindWebP:
	case KindPDF:
		// The text layer was unusable and we are past it. A scanned PDF needs
		// either a provider that takes the whole document or a renderer to
		// turn its pages into images, and this build has no renderer.
		if _, ok := cfg.ocr.(DocumentOCR); !ok {
			return nil, &Error{
				Op:   OpAcquire,
				Kind: ErrNoProvider,
				Message: "this PDF has no usable text layer, and reading it needs " +
					"either an OCR provider that accepts a document whole (WithOCR) " +
					"or a renderer to rasterise its pages, which this build does not " +
					"yet ship",
			}
		}
		return nil, &Error{
			Op:      OpAcquire,
			Kind:    ErrNoContent,
			Message: "the document-level provider did not read this PDF",
		}
	default:
		return nil, &Error{
			Op:   OpAcquire,
			Kind: ErrNoProvider,
			Message: fmt.Sprintf("%s is not readable yet: this build handles PDFs with "+
				"a text layer, and images (PNG, JPEG) through OCR or a vision-capable model", doc.Kind),
		}
	}

	page, err := img.Decode(data, img.Kind(doc.Kind), cfg.maxPagePixels)
	if err != nil {
		cfg.emit(ctx, Event{Op: OpAcquire, Duration: time.Since(start), Err: err})
		return nil, classify(OpAcquire, "", err)
	}

	// A provider that accepts the whole document rasterises server-side, which
	// is what lets a scanned document be read with no local renderer at all —
	// the route ADR-0010 relies on while render/pdfium does not exist. Prefer
	// it: it is one call rather than one per page, and it sees the document
	// rather than an image we chose the resolution of.
	if d, ok := cfg.ocr.(DocumentOCR); ok && cfg.reading != ModeVision {
		rd, err := acquireByDocumentOCR(ctx, cfg, d, doc, meta, start)
		if err == nil {
			return rd, nil
		}
		// ErrUnsupported here means the provider cannot take this format
		// whole. That is not a failure: fall through to the per-page path
		// rather than refusing a document another route can read.
		if !errors.Is(err, ErrUnsupported) {
			return nil, err
		}
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
		doc:   Document{Kind: doc.Kind, Pages: 1, Size: doc.Size, Data: doc.Data},
		kinds: []Reading{ReadingVision},
		pages: []prompt.PageContent{{
			Number:    1,
			Reading:   prompt.Reading(ReadingVision),
			Image:     data,
			MediaType: mediaTypeOf(doc.Kind),
		}},
	}, nil
}

// acquireByOffice reads a DOCX, XLSX or CSV.
//
// These formats have no page geometry — a DOCX has no fixed layout until it is
// rendered, and a spreadsheet cell has a grid position rather than a point on
// paper — so internal/office reports zero boxes and internal/normalise abstains
// from the checks that need them rather than running them against a guess. The
// cost is real and worth stating: a value extracted from one of these cannot
// be highlighted on a rendered page, only located in the text.
func acquireByOffice(ctx context.Context, cfg *config, doc Document, start time.Time) (*reading, error) {
	lim := limitsOf(cfg)
	d, err := office.Extract(doc.Data, detect.Kind(doc.Kind), lim,
		detect.NewCounter(detect.LimitDecompressedBytes, lim.MaxDecompressedBytes))
	if err != nil {
		cfg.emit(ctx, Event{Op: OpAcquire, Duration: time.Since(start), Err: err})
		return nil, classify(OpAcquire, "", err)
	}
	if len(d.Pages) > cfg.maxPages {
		return nil, &Error{Op: OpAcquire, Kind: ErrLimitExceeded,
			Message: "page count exceeds the limit, raise with WithMaxPages"}
	}

	nStart := time.Now()
	res := normalise.Normalise(normalise.Input{Pages: d.Pages})
	cfg.emit(ctx, Event{Op: OpNormalise, Duration: time.Since(nStart), Bytes: int64(len(res.Text))})

	if int64(len(res.Text)) > cfg.maxTextBytes {
		return nil, &Error{Op: OpNormalise, Kind: ErrLimitExceeded,
			Message: "extracted text exceeds the limit, raise with WithMaxTextBytes"}
	}

	kinds := make([]Reading, len(d.Pages))
	content := make([]prompt.PageContent, 0, len(d.Pages))
	for i := range d.Pages {
		kinds[i] = ReadingText
		content = append(content, prompt.PageContent{
			Number:  i + 1,
			Reading: prompt.Reading(ReadingText),
			Text:    pageText(res, i),
		})
	}
	cfg.emit(ctx, Event{Op: OpAcquire, Duration: time.Since(start), Pages: len(d.Pages)})

	rd := &reading{
		doc:   Document{Kind: doc.Kind, Pages: len(d.Pages), Size: doc.Size, Data: doc.Data},
		kinds: kinds,
		text:  res,
		pages: content,
	}

	// Parts deliberately not extracted, and hidden runs, are reported rather
	// than silently absent (docs/rules.md §6.1). Hidden text in particular is
	// invisible to a reviewer and visible to the model, which is the shape of
	// an injection.
	for _, part := range d.Skipped {
		rd.notes = append(rd.notes, fmt.Sprintf("%s was not extracted", part))
	}
	if d.HiddenRuns > 0 {
		rd.notes = append(rd.notes, fmt.Sprintf(
			"%d run(s) are marked hidden: text a reviewer cannot see and the model can",
			d.HiddenRuns))
	}
	return rd, nil
}

// acquireByTextLayer reads a PDF's own characters.
//
// It returns an error wrapping ErrNoContent when the text layer is missing or
// unusable, which the caller treats as "try the next reading" rather than as a
// failure. Every other error is real: an encrypted document, a filter we do not
// implement, a limit reached.
func acquireByTextLayer(ctx context.Context, cfg *config, doc Document, meta *Metadata, start time.Time) (*reading, error) {
	lim := limitsOf(cfg)
	d, err := pdf.Open(doc.Data, lim, detect.NewCounter(detect.LimitDecompressedBytes, lim.MaxDecompressedBytes))
	if err != nil {
		cfg.emit(ctx, Event{Op: OpAcquire, Duration: time.Since(start), Err: err})
		return nil, classify(OpAcquire, "", err)
	}

	n := d.NumPages()
	if n == 0 {
		return nil, &Error{Op: OpAcquire, Kind: ErrNoContent, Message: "the document has no pages"}
	}
	if n > cfg.maxPages {
		return nil, &Error{Op: OpAcquire, Kind: ErrLimitExceeded,
			Message: "page count exceeds the limit, raise with WithMaxPages"}
	}

	th := pdf.Thresholds{
		MinTextDensity:      cfg.minTextDensity,
		MaxReplacementRatio: cfg.maxReplacementRatio,
		MinDecodableRatio:   cfg.minDecodableRatio,
	}

	pages := make([]normalise.Page, 0, n)
	var unusable []int
	usable := 0
	for i := 1; i <= n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, classify(OpAcquire, "", err)
		}
		page, err := d.Page(i)
		if err != nil {
			return nil, classify(OpAcquire, "", err)
		}
		// A page that fails the heuristic contributes no words rather than
		// plausible rubbish. A broken ToUnicode table produces text of the
		// right shape and the wrong letters, and passing that on would poison
		// every stage after it (ADR-0011).
		if !page.Usable(th) {
			// Contributing an empty page here made a scanned appendix bound
			// onto a digital contract silently blank: the model never saw it
			// and nothing said so. The page is recorded as needing another
			// reading instead (docs/pipeline.md stage 2).
			pages = append(pages, normalise.Page{
				Number: i, Width: page.Content.Width, Height: page.Content.Height,
			})
			unusable = append(unusable, i)
			continue
		}
		usable++
		pages = append(pages, page.Content)
	}

	if usable == 0 {
		return nil, &Error{Op: OpAcquire, Kind: ErrNoContent,
			Message: "no page has a usable text layer"}
	}

	nStart := time.Now()
	res := normalise.Normalise(normalise.Input{Pages: pages, Metadata: d.Metadata()})
	cfg.emit(ctx, Event{Op: OpNormalise, Duration: time.Since(nStart), Bytes: int64(len(res.Text))})

	if int64(len(res.Text)) > cfg.maxTextBytes {
		return nil, &Error{Op: OpNormalise, Kind: ErrLimitExceeded,
			Message: "extracted text exceeds the limit, raise with WithMaxTextBytes"}
	}

	// A page the text layer could not serve is read another way, if there is
	// another way. Acquisition is per page, not per document: one file can
	// carry a digital contract and a scanned appendix, and taking one path for
	// the whole of it loses whichever half it did not choose.
	byPage := map[int]prompt.PageContent{}
	var unread []int
	for _, n := range unusable {
		c, rd, err := acquirePage(ctx, cfg, doc, n)
		if err != nil {
			// The rest of the document is readable, so a page that cannot be
			// filled is reported and the extraction continues. Refusing the
			// whole document because one appendix page needs a renderer
			// nobody configured would discard everything that did work.
			cfg.emit(ctx, Event{Op: OpAcquire, Page: n, Duration: time.Since(start), Err: err})
			unread = append(unread, n)
			continue
		}
		byPage[n] = c
		_ = rd
	}

	kinds := make([]Reading, len(pages))
	content := make([]prompt.PageContent, 0, len(pages))
	for i := range pages {
		n := i + 1
		if c, ok := byPage[n]; ok {
			kinds[i] = Reading(c.Reading)
			content = append(content, c)
			continue
		}
		kinds[i] = ReadingText
		content = append(content, prompt.PageContent{
			Number:  n,
			Reading: prompt.Reading(ReadingText),
			Text:    pageText(res, i),
		})
	}
	cfg.emit(ctx, Event{Op: OpAcquire, Duration: time.Since(start), Pages: len(pages)})

	return &reading{
		doc:    Document{Kind: doc.Kind, Pages: len(pages), Size: doc.Size, Data: doc.Data},
		kinds:  kinds,
		text:   res,
		pages:  content,
		unread: unread,
	}, nil
}

// stageSecondReading takes an independent second reading of the same document.
//
// It deliberately picks a reading the first one did not use: comparing two OCR
// passes over the same pixels would agree with itself, which is agreement that
// proves nothing. Text against vision, or OCR against vision, fail in ways that
// have nothing to do with each other, and that independence is the whole
// source of the signal (ADR-0014).
func stageSecondReading(ctx context.Context, cfg *config, data []byte, doc Document, first *reading, meta *Metadata) (*reading, error) {
	start := time.Now()
	took := primaryReading(first.kinds)

	// Vision is the reading almost nothing else uses, so it is the usual
	// second opinion. When the first reading was already vision, OCR is the
	// alternative, and without an OCR provider there is no second reading to
	// take.
	if took == ReadingVision {
		if cfg.ocr == nil {
			return nil, &Error{Op: OpAcquire, Kind: ErrNoProvider,
				Message: "a second reading needs an OCR provider; configure one with WithOCR"}
		}
		alt := *cfg
		alt.reading = ModeOCR
		return stageAcquire(ctx, &alt, data, doc, meta)
	}

	pages, err := visionPages(ctx, cfg, doc, first)
	if err != nil {
		return nil, err
	}
	cfg.emit(ctx, Event{Op: OpAcquire, Duration: time.Since(start), Pages: len(pages)})

	kinds := make([]Reading, len(pages))
	for i := range kinds {
		kinds[i] = ReadingVision
	}
	return &reading{doc: first.doc, kinds: kinds, pages: pages}, nil
}

// visionPages renders each page so a vision model can read it.
func visionPages(ctx context.Context, cfg *config, doc Document, first *reading) ([]prompt.PageContent, error) {
	// An image source is already an image: it needs no renderer, and sending
	// the original bytes is better than rendering our own copy of them.
	if doc.Kind != KindPDF {
		return []prompt.PageContent{{
			Number:    1,
			Reading:   prompt.Reading(ReadingVision),
			Image:     doc.Data,
			MediaType: mediaTypeOf(doc.Kind),
		}}, nil
	}
	if cfg.renderer == nil {
		return nil, &Error{Op: OpAcquire, Kind: ErrNoProvider,
			Message: "a vision reading of a PDF needs a renderer; configure one with WithRenderer"}
	}

	n := len(first.pages)
	if n == 0 {
		n = 1
	}
	out := make([]prompt.PageContent, 0, n)
	for i := 1; i <= n; i++ {
		img, err := cfg.renderer.Render(ctx, doc, i, renderDPI)
		if err != nil {
			return nil, classify(OpRender, "", err)
		}
		b, err := encodePNG(img)
		if err != nil {
			return nil, &Error{Op: OpRender, Page: i, Kind: ErrInternal,
				Message: "the rendered page could not be encoded"}
		}
		out = append(out, prompt.PageContent{
			Number: i, Reading: prompt.Reading(ReadingVision),
			Image: b, MediaType: "image/png",
		})
	}
	return out, nil
}

// generateFrom runs the prompt and model stages over one reading's content.
func generateFrom(ctx context.Context, cfg *config, sch *schema.Schema, jsonSchema []byte, rd *reading, meta *Metadata) (map[string]any, error) {
	req, err := stagePrompt(ctx, cfg, sch, jsonSchema, rd)
	if err != nil {
		return nil, err
	}
	raw, usage, err := stageGenerate(ctx, cfg, req, meta)
	if err != nil {
		return nil, err
	}
	meta.Usage.InputTokens += usage.InputTokens
	meta.Usage.OutputTokens += usage.OutputTokens
	meta.Usage.PageUnits += usage.PageUnits

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, &Error{Op: OpGenerate, Kind: ErrBadResponse,
			Message: "the reply is not a JSON object"}
	}
	return obj, nil
}

// acquirePage reads one page of a document the text layer could not serve.
//
// It is the per-page half of stage 2: rasterise, then OCR, then vision, taking
// the first that can serve this page. The order is the same as for a whole
// document and for the same reason — OCR yields text, and text is what
// grounding needs, so a value read this way can still be checked against the
// document it came from.
func acquirePage(ctx context.Context, cfg *config, doc Document, page int) (prompt.PageContent, Reading, error) {
	if cfg.renderer == nil {
		return prompt.PageContent{}, ReadingUnknown, &Error{
			Op:   OpAcquire,
			Page: page,
			Kind: ErrNoProvider,
			Message: "this page has no usable text layer, and reading it needs a " +
				"renderer to rasterise it — configure one with WithRenderer",
		}
	}

	start := time.Now()
	img, err := cfg.renderer.Render(ctx, doc, page, renderDPI)
	cfg.emit(ctx, Event{Op: OpRender, Page: page, Duration: time.Since(start), Err: err})
	if err != nil {
		return prompt.PageContent{}, ReadingUnknown, classify(OpRender, "", err)
	}

	b := img.Bounds()
	p := Page{
		Number: page,
		Image:  img,
		Width:  float64(b.Dx()) * pointsPerInch / renderDPI,
		Height: float64(b.Dy()) * pointsPerInch / renderDPI,
		DPI:    renderDPI,
	}

	if cfg.ocr != nil {
		oStart := time.Now()
		rec, err := cfg.ocr.Recognise(ctx, p)
		name := cfg.ocr.Name()
		cfg.emit(ctx, Event{Op: OpOCR, Provider: name, Page: page, Duration: time.Since(oStart), Err: err})
		if err != nil {
			return prompt.PageContent{}, ReadingUnknown, classify(OpOCR, name, err)
		}
		res := normalise.Normalise(normalise.Input{
			Pages: []normalise.Page{normalisePage(page, p.Width, p.Height, rec)},
		})
		return prompt.PageContent{
			Number:  page,
			Reading: prompt.Reading(ReadingOCR),
			Text:    res.Text,
		}, ReadingOCR, nil
	}

	// No OCR provider: the page image goes to the model. That reading produces
	// no source text, so nothing read from it can be grounded — which is why
	// it is the last choice rather than the first.
	png, err := encodePNG(img)
	if err != nil {
		return prompt.PageContent{}, ReadingUnknown, &Error{
			Op: OpAcquire, Page: page, Kind: ErrInternal,
			Message: "the rendered page could not be encoded",
		}
	}
	return prompt.PageContent{
		Number:    page,
		Reading:   prompt.Reading(ReadingVision),
		Image:     png,
		MediaType: "image/png",
	}, ReadingVision, nil
}

// renderDPI is the resolution pages are rasterised at when the text layer
// cannot serve them.
//
// 300 is what scanners produce and what OCR engines are tuned for. Lower loses
// small print; higher costs memory and time for accuracy no engine uses.
const renderDPI = 300

// pointsPerInch is fixed by the typographic point, which is what page geometry
// is expressed in throughout ovrin.
const pointsPerInch = 72.0

// encodePNG turns a rendered page into bytes an adapter can send.
//
// PNG rather than JPEG: a rasterised page is line art, where JPEG's artefacts
// land exactly on the strokes an OCR engine reads.
func encodePNG(m image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// acquireByDocumentOCR hands the whole document to a provider that rasterises
// it itself.
func acquireByDocumentOCR(ctx context.Context, cfg *config, d DocumentOCR, doc Document, meta *Metadata, start time.Time) (*reading, error) {
	recs, err := d.RecogniseDocument(ctx, doc)
	name := d.Name()
	cfg.emit(ctx, Event{Op: OpOCR, Provider: name, Duration: time.Since(start), Pages: len(recs), Err: err})
	if err != nil {
		return nil, classify(OpOCR, name, err)
	}
	if len(recs) == 0 {
		return nil, &Error{Op: OpOCR, Provider: name, Kind: ErrNoContent,
			Message: "the provider returned no pages"}
	}
	meta.Providers[OpOCR] = name

	pages := make([]normalise.Page, 0, len(recs))
	for i, rec := range recs {
		if rec == nil {
			continue
		}
		pages = append(pages, normalisePage(i+1, 0, 0, rec))
	}

	nStart := time.Now()
	res := normalise.Normalise(normalise.Input{Pages: pages})
	cfg.emit(ctx, Event{Op: OpNormalise, Duration: time.Since(nStart), Bytes: int64(len(res.Text))})

	if int64(len(res.Text)) > cfg.maxTextBytes {
		return nil, &Error{Op: OpNormalise, Kind: ErrLimitExceeded,
			Message: "extracted text exceeds the limit, raise with WithMaxTextBytes"}
	}

	kinds := make([]Reading, len(pages))
	content := make([]prompt.PageContent, 0, len(pages))
	for i := range pages {
		kinds[i] = ReadingOCR
		content = append(content, prompt.PageContent{
			Number:  i + 1,
			Reading: prompt.Reading(ReadingOCR),
			Text:    pageText(res, i),
		})
	}

	return &reading{
		doc:      Document{Kind: doc.Kind, Pages: len(pages), Size: doc.Size, Data: doc.Data},
		kinds:    kinds,
		provider: name,
		text:     res,
		pages:    content,
	}, nil
}

// normalisePage converts one Recognition into the shape normalise reads.
func normalisePage(number int, width, height float64, rec *Recognition) normalise.Page {
	words := make([]normalise.Word, 0, len(rec.Words))
	for _, w := range rec.Words {
		words = append(words, normalise.Word{
			Text:       w.Text,
			Box:        normalise.Rect(w.Box),
			Confidence: w.Confidence,
		})
	}
	return normalise.Page{Number: number, Width: width, Height: height, Words: words}
}

// pageText returns the normalised text belonging to one page.
func pageText(res *normalise.Result, i int) string {
	if i >= len(res.Pages) {
		return res.Text
	}
	// Body excludes the page marker, which is text ovrin inserted rather than
	// document content.
	body := res.Pages[i].Body
	if body.Start < 0 || body.End > len(res.Text) || body.Start > body.End {
		return res.Text
	}
	return res.Text[body.Start:body.End]
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
		doc:      Document{Kind: doc.Kind, Pages: 1, Size: doc.Size, Data: doc.Data},
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

// toModelRequest converts the internal request into the one a provider sees.
func toModelRequest(req prompt.Request) ModelRequest {
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
	}
}

// stageRetry asks once more when the reply was malformed rather than merely
// disappointing, and returns the reply to use.
//
// It returns the original reply unchanged whenever it can, which is nearly
// always: retry.Assess is deliberately reluctant, and a value that failed a
// min, max, enum or format rule is the document disagreeing with the schema.
// Asking again for that would be asking the model to invent something that
// satisfies the rule, which is rule 8.5's cardinal sin. Only a reply that is
// not usable JSON, or a field whose value could not be converted to its
// declared type, is worth a second request.
//
// The document is not re-sent: the model has read it, and the follow-up carries
// only the schema and what was wrong. That also bounds the cost of this to one
// short request, which is why it needs no configuration.
func stageRetry(ctx context.Context, cfg *config, req ModelRequest, sch *schema.Schema,
	raw []byte, object map[string]any, meta *Metadata,
) ([]byte, map[string]any) {
	failures := retry.Assess(raw, replyResults(sch, object, cfg))
	if len(failures) == 0 {
		return raw, object
	}

	orig := prompt.Request{
		Instruction: req.Instruction,
		Schema:      req.Schema,
		Temperature: req.Temperature,
	}
	second, err := retry.Build(orig, *sch, raw, failures)
	if err != nil {
		// Build refuses to retry a retry, which is how "once" is enforced.
		// There is nothing to report: the first reply is still the answer.
		return raw, object
	}

	start := time.Now()
	resp, err := cfg.model.Generate(ctx, toModelRequest(second))
	cfg.emit(ctx, Event{Op: OpGenerate, Duration: time.Since(start), Attempt: 2, Err: err})
	if err != nil || resp == nil || len(resp.JSON) == 0 {
		// A failed second attempt loses nothing. The first reply is returned
		// and its fields are marked however they deserve.
		return raw, object
	}

	var retried map[string]any
	if err := json.Unmarshal(resp.JSON, &retried); err != nil {
		return raw, object
	}
	if len(retry.Assess(resp.JSON, replyResults(sch, retried, cfg))) >= len(failures) {
		// No better. Keep the first, so a second reply that is differently
		// wrong cannot make the result worse than not retrying at all.
		return raw, object
	}

	meta.Usage.InputTokens += resp.Usage.InputTokens
	meta.Usage.OutputTokens += resp.Usage.OutputTokens
	meta.Retried = true
	return resp.JSON, retried
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
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		// A path that does not exist, or that we may not read, is the caller's
		// to fix. Falling through to ErrInternal told them to file a bug
		// against ovrin for their own typo — the exact misdirection ADR-0030
		// warns ErrInternal must never become.
		return (&Error{Op: op, Provider: provider, Kind: ErrNoContent,
			Message: "the source could not be opened"}).WithCause(err)
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
