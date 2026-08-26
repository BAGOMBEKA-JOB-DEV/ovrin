// Package google implements ovrin's [ovrin.OCR] and [ovrin.DocumentOCR] seams
// on top of the Google Cloud Vision REST API.
//
//	import (
//	    "github.com/BAGOMBEKA-JOB-DEV/ovrin"
//	    ovringoogle "github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/google"
//	)
//
//	c := ovrin.New(ovrin.WithOCR(ovringoogle.New(apiKey)))
//
// It is a separate Go module, because ovrin's core depends on nothing and a
// user running Tesseract locally has no business carrying a Google client
// ([ADR-0009]).
//
// # It has no dependencies of its own either
//
// The Vision API is JSON over HTTPS, and this package speaks it with
// [net/http] and [encoding/json]. The alternative, cloud.google.com/go/vision,
// is tens of megabytes of generated code with its own transitive tree, and
// every dependency this module takes is inherited by everyone who imports it —
// which is the same argument [ADR-0009] makes about the core, one level down.
// So the whole module is standard library only, and a user pays for this
// adapter with one `require` and no build-time cost.
//
// The cost is authentication. Google's own credential handling lives in
// golang.org/x/oauth2/google, and reimplementing service-account JWT signing
// here would be a hundred lines of security-relevant code that nobody asked
// this package to own. Instead:
//
//   - [New] takes a Vision API key, which is a query parameter and needs no
//     library at all. This is the one-line path, and it is what most callers
//     of an OCR API actually have.
//   - [WithTokenSource] takes an access token from whatever the caller already
//     uses. Service accounts, workload identity federation and Application
//     Default Credentials all reach this package through it, and the
//     dependency lands in the caller's go.mod where they can see it.
//
// # Whole documents, with no local renderer
//
// [Provider.RecogniseDocument] sends a PDF to Google and lets it rasterise
// server-side, which is the route that makes scanned PDFs work before a local
// renderer exists ([ADR-0010]). [ovrin.Document] carries a document's kind,
// page count and size but not its bytes, so the content is supplied through
// [WithDocumentContent] — see that option for why.
//
// # What this adapter silently ignores
//
// Rule §6.5 asks every adapter to document that, not only what it supports.
//
//   - Per-symbol boxes and confidences. Vision reports geometry down to the
//     character; [ovrin.Word] is the smallest unit ovrin models, so symbols
//     are concatenated into words and their own geometry is dropped. It is
//     reachable through [ovrin.Recognition.Raw].
//   - Block types. Vision labels a block TEXT, TABLE, PICTURE, RULER or
//     BARCODE. Ovrin has no equivalent, so every block carrying words is read
//     as words and the label is dropped rather than being used to skip
//     anything — dropping a table's words would be a much worse answer.
//   - Per-block, per-paragraph and per-word language. Only the page's detected
//     language reaches [ovrin.Recognition.Language].
//   - Vision's own flat `text`, and the legacy `textAnnotations` list. Lines
//     are rebuilt from the word hierarchy instead, because the flat text
//     carries no geometry.
//   - Page units. OCR is billed per page and [ovrin.Recognition] has no usage
//     field, so there is nowhere to report what a call cost.
//   - Multi-column reading order is approximate. Vision's blocks are sorted
//     into reading order and the lines within a block are kept in the order
//     Vision produced them, which preserves columns where Vision detected them
//     as separate blocks and does not where it did not.
//
// # What it refuses rather than degrading
//
//   - A page with no image, or with no size in points. There is no way to
//     return page-point coordinates for a page that does not say how big it
//     is, and returning pixels labelled as points is exactly the silent
//     degradation rule §6.1 forbids.
//   - Any document that is not a PDF. Vision's synchronous file endpoint also
//     accepts TIFF and GIF, but reports their geometry in pixels with no
//     resolution, so the result could not be converted to points.
//   - A document of more than five pages. That is the synchronous endpoint's
//     limit; beyond it Vision requires the asynchronous, Cloud-Storage-backed
//     API, which is a different product. Returning the first five pages
//     silently is the failure §6.1 exists to prevent.
//
// # Retry
//
// There is none, deliberately. Retry, backoff, fallback and timeouts belong to
// ovrin's core so that they are decided once rather than once per adapter
// (rule §6.2).
//
// A Provider is safe for concurrent use by multiple goroutines.
//
// [ADR-0009]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0009-ocr-seam.md
// [ADR-0010]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0010-no-cgo-in-core.md
package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// DefaultBaseURL is where Google serves the Vision API.
//
// It is exported because the only sane way to override it — a regional
// endpoint such as https://eu-vision.googleapis.com, or a test server — is to
// know what is being overridden.
const DefaultBaseURL = "https://vision.googleapis.com"

// providerName appears in [ovrin.Provenance.Method] and on every error, so a
// result carries the evidence of which service read it.
//
// It names the product rather than the vendor: Google sells at least three
// things that read a document, and a result that says only "google" cannot be
// told from one produced by Document AI.
const providerName = "google-vision"

// maxSyncPages is the page limit of Vision's synchronous file endpoint.
//
// Beyond it the API requires asyncBatchAnnotate, which writes its output to
// Cloud Storage and is a different product with a different failure model. A
// document with more pages is refused rather than truncated (rule §6.1).
const maxSyncPages = 5

// Provider reads pages and documents through the Google Cloud Vision API.
//
// It is safe for concurrent use by multiple goroutines: it holds no mutable
// state after construction.
type Provider struct {
	apiKey  string
	baseURL string
	hc      *http.Client

	token         func(ctx context.Context) (string, error)
	languageHints []string
	document      func(ctx context.Context, doc ovrin.Document) ([]byte, error)
}

// Option configures a [Provider]. Options are applied in order.
type Option func(*Provider)

// WithBaseURL points the provider at another endpoint — a regional one such as
// https://eu-vision.googleapis.com, where data residency requires it, or a test
// server.
//
// The URL is the API root with no trailing slash and no version segment. An
// empty string is ignored, so a caller reading a base URL out of their own
// configuration does not have to branch on it being unset.
func WithBaseURL(u string) Option {
	return func(p *Provider) {
		if u != "" {
			p.baseURL = strings.TrimSuffix(u, "/")
		}
	}
}

// WithHTTPClient supplies the client every call is made through.
//
// This is where a caller puts a proxy, a custom transport, connection limits or
// instrumentation. It is deliberately not where a timeout belongs: bounding a
// call is the context's job, and a client timeout here would apply to ovrin's
// own retry attempts in a way ovrin cannot see (rule §6.2).
//
// A nil client is ignored.
func WithHTTPClient(hc *http.Client) Option {
	return func(p *Provider) {
		if hc != nil {
			p.hc = hc
		}
	}
}

// WithTokenSource supplies an OAuth 2.0 access token for each call, which is
// what service accounts, workload identity federation and Application Default
// Credentials all produce.
//
// It is a function rather than a string because access tokens expire, and it
// takes a context so the caller's own refresh can be cancelled with the
// extraction that triggered it.
//
// This is the seam that keeps the module dependency-free. Google's credential
// handling is a solved problem living in golang.org/x/oauth2/google, and
// reimplementing JWT signing here would put security-relevant code in a package
// nobody asked to own it:
//
//	ts, err := gauth.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
//	p := google.New("", google.WithTokenSource(func(ctx context.Context) (string, error) {
//	    tok, err := ts.Token()
//	    if err != nil {
//	        return "", err
//	    }
//	    return tok.AccessToken, nil
//	}))
//
// When set, the token is sent instead of the API key given to [New].
func WithTokenSource(fn func(ctx context.Context) (string, error)) Option {
	return func(p *Provider) { p.token = fn }
}

// WithLanguageHints tells Vision which languages to expect, as BCP-47 tags.
//
// Vision detects language on its own and hints are usually unnecessary; they
// matter for scripts that are ambiguous without them, where an unhinted result
// is not merely worse but wrong. [ovrin.Page] carries no language, so there is
// no request field this could be mapped from and it has to be an option.
func WithLanguageHints(codes ...string) Option {
	return func(p *Provider) { p.languageHints = append([]string(nil), codes...) }
}

// WithDocumentContent supplies the bytes behind an [ovrin.Document], which is
// what [Provider.RecogniseDocument] uploads.
//
// It exists because [ovrin.Document] carries a document's kind, page count and
// size but not its content, so an [ovrin.DocumentOCR] cannot reach what it is
// being asked to read from its argument alone. Until the core closes that gap
// an adapter has to be told, and being told explicitly is better than a
// provider quietly holding one document's bytes for the life of a program.
//
// It is a function rather than a byte slice so that one Provider can serve many
// documents concurrently, and so that a caller streaming from disk or object
// storage does not have to buffer every document in advance.
//
// Without it, [Provider.RecogniseDocument] returns [ovrin.ErrUnsupported]
// naming what it could not do, rather than returning nothing and calling it an
// empty document.
func WithDocumentContent(fn func(ctx context.Context, doc ovrin.Document) ([]byte, error)) Option {
	return func(p *Provider) { p.document = fn }
}

// New returns a Provider authenticating with a Vision API key.
//
// The key is taken explicitly and is never read from the environment: a library
// that reads the environment is how a program ends up talking to the wrong
// account (rule §6.4).
//
// Pass an empty key together with [WithTokenSource] to authenticate as a
// service account instead; that is the form most production deployments want,
// and an API key is the form most people trying the library out have.
//
// The returned Provider is safe for concurrent use by multiple goroutines.
func New(apiKey string, opts ...Option) *Provider {
	p := &Provider{
		apiKey:  apiKey,
		baseURL: DefaultBaseURL,
		// No timeout: bounding a call is the caller's context's job, and a
		// client timeout here would silently override it.
		hc: &http.Client{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name implements [ovrin.OCR]. It appears in [ovrin.Provenance.Method], so a
// result records that this service read it.
func (p *Provider) Name() string { return providerName }

// Recognise implements [ovrin.OCR], reading one rasterised page.
//
// The page's Width and Height are what the returned coordinates are expressed
// in: Vision reports boxes in the pixel space of the image it was sent, and
// every one of them is scaled into the page's own points before it is returned
// (ADR-0009).
func (p *Provider) Recognise(ctx context.Context, page ovrin.Page) (*ovrin.Recognition, error) {
	if page.Image == nil {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			"the page carries no image, and there is nothing to recognise")
	}
	if page.Width <= 0 || page.Height <= 0 {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			"the page does not say how large it is in points, so a box could not "+
				"be converted out of the provider's pixels")
	}

	var encoded bytes.Buffer
	// PNG rather than JPEG: OCR reads glyph edges, and JPEG's artefacts sit
	// exactly there. Vision accepts both.
	if err := png.Encode(&encoded, page.Image); err != nil {
		return nil, p.fail(ovrin.ErrInternal, page.Number,
			"the page image could not be encoded")
	}

	body, err := json.Marshal(imagesAnnotateRequest{
		Requests: []imageRequest{{
			Image:        imageSource{Content: base64.StdEncoding.EncodeToString(encoded.Bytes())},
			Features:     []feature{{Type: featureDocumentText}},
			ImageContext: p.imageContext(),
		}},
	})
	if err != nil {
		return nil, p.fail(ovrin.ErrInternal, page.Number,
			"the request could not be encoded")
	}

	raw, err := p.post(ctx, "/v1/images:annotate", body, page.Number)
	if err != nil {
		return nil, err
	}

	var envelope imagesAnnotateResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, p.fail(ovrin.ErrBadResponse, page.Number,
			"the provider's reply was not the json the api documents")
	}
	if e := envelope.Error; e != nil {
		return nil, p.fail(classifyCode(e.Code), page.Number, e.summary())
	}
	if len(envelope.Responses) == 0 {
		return nil, p.fail(ovrin.ErrBadResponse, page.Number,
			"the provider returned no annotation for the page")
	}

	rec, err := p.page(envelope.Responses[0], page.Number, page.Width, page.Height)
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// RecogniseDocument implements [ovrin.DocumentOCR], reading every page of a PDF
// that Google rasterises on its own side.
//
// This is the route that lets a scanned PDF be extracted with no local renderer
// at all, which is what [ADR-0010] defers rasterising on. The content comes
// from [WithDocumentContent]; see that option for why it cannot come from doc.
//
// Coordinates come back in the PDF's own points, which is already the space
// ovrin wants, so nothing is scaled and nothing is lost to rounding.
//
// [ADR-0010]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0010-no-cgo-in-core.md
func (p *Provider) RecogniseDocument(ctx context.Context, doc ovrin.Document) ([]*ovrin.Recognition, error) {
	if doc.Kind != ovrin.KindPDF {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			"the provider reads only pdf documents through this endpoint, and this "+
				"one is "+doc.Kind.String())
	}
	if doc.Pages > maxSyncPages {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			fmt.Sprintf("the document has %d pages and the synchronous endpoint "+
				"reads at most %d; the rest would be dropped silently",
				doc.Pages, maxSyncPages))
	}
	if p.document == nil {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			"no document content is configured; ovrin.Document carries a document's "+
				"size but not its bytes, so pass WithDocumentContent")
	}

	content, err := p.document(ctx, doc)
	if err != nil {
		// The caller's own error is attached rather than described: it is
		// theirs to read, and nothing here may quote it into a message that
		// could carry the document (rule §2.5).
		return nil, p.fail(ovrin.ErrNoContent, 0,
			"the document content could not be read").WithCause(err)
	}
	if len(content) == 0 {
		return nil, p.fail(ovrin.ErrNoContent, 0, "the document is empty")
	}

	req := fileRequest{
		InputConfig: inputConfig{
			MimeType: "application/pdf",
			Content:  base64.StdEncoding.EncodeToString(content),
		},
		Features:     []feature{{Type: featureDocumentText}},
		ImageContext: p.imageContext(),
	}
	if doc.Pages > 0 {
		req.Pages = make([]int, 0, doc.Pages)
		for i := 1; i <= doc.Pages; i++ {
			req.Pages = append(req.Pages, i)
		}
	}
	body, err := json.Marshal(filesAnnotateRequest{Requests: []fileRequest{req}})
	if err != nil {
		return nil, p.fail(ovrin.ErrInternal, 0, "the request could not be encoded")
	}

	raw, err := p.post(ctx, "/v1/files:annotate", body, 0)
	if err != nil {
		return nil, err
	}

	var envelope filesAnnotateResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, p.fail(ovrin.ErrBadResponse, 0,
			"the provider's reply was not the json the api documents")
	}
	if e := envelope.Error; e != nil {
		return nil, p.fail(classifyCode(e.Code), 0, e.summary())
	}
	if len(envelope.Responses) == 0 {
		return nil, p.fail(ovrin.ErrBadResponse, 0,
			"the provider returned no annotation for the document")
	}
	file := envelope.Responses[0]
	if e := file.Error; e != nil {
		return nil, p.fail(classifyCode(e.Code), 0, e.summary())
	}
	// A document longer than what came back was truncated by the endpoint.
	// Handing the caller the pages that did arrive would be the silent
	// degradation rule §6.1 exists to prevent.
	if file.TotalPages > 0 && file.TotalPages > len(file.Responses) {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			fmt.Sprintf("the provider read %d of the document's %d pages; the "+
				"remainder needs the asynchronous api",
				len(file.Responses), file.TotalPages))
	}

	out := make([]*ovrin.Recognition, 0, len(file.Responses))
	for i, page := range file.Responses {
		number := i + 1
		var ctxt struct {
			Context struct {
				PageNumber int `json:"pageNumber"`
			} `json:"context"`
		}
		if err := json.Unmarshal(page, &ctxt); err == nil && ctxt.Context.PageNumber > 0 {
			number = ctxt.Context.PageNumber
		}
		// A PDF page's geometry is reported in points already, so the source
		// space and the target space are the same one and nothing is scaled.
		rec, err := p.page(page, number, 0, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// page decodes one annotated page and normalises it.
//
// dstW and dstH are the page size in points, or zero to keep the provider's own
// coordinate space — which is what a PDF page needs, because Vision reports it
// in points to begin with.
func (p *Provider) page(raw json.RawMessage, number int, dstW, dstH float64) (*ovrin.Recognition, error) {
	var resp imageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, p.fail(ovrin.ErrBadResponse, number,
			"the provider's page annotation was not the json the api documents")
	}
	// Vision reports a per-page failure inside a 200, so a status code alone
	// does not tell a caller whether the page was read.
	if e := resp.Error; e != nil {
		return nil, p.fail(classifyCode(e.Code), number, e.summary())
	}
	if resp.FullTextAnnotation == nil || len(resp.FullTextAnnotation.Pages) == 0 {
		// Not an error. A blank page is a real thing a scanner produces, and
		// the core decides what an empty reading means (rule §2.6).
		return &ovrin.Recognition{Raw: &Annotation{JSON: raw}}, nil
	}
	return normalise(&resp.FullTextAnnotation.Pages[0], number, dstW, dstH, raw), nil
}

// imageContext returns the language hints, or nil when there are none, so that
// the field is omitted rather than sent empty.
func (p *Provider) imageContext() *imageContext {
	if len(p.languageHints) == 0 {
		return nil
	}
	return &imageContext{LanguageHints: p.languageHints}
}

// post sends one request and returns the reply body.
//
// It is where the credential is attached and where a transport failure becomes
// an ovrin sentinel; neither the credential nor the provider's own message ever
// reaches the error it returns.
func (p *Provider) post(ctx context.Context, path string, body []byte, page int) ([]byte, error) {
	endpoint := p.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, p.fail(ovrin.ErrBadRequest, page, "the request could not be built")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	switch {
	case p.token != nil:
		token, err := p.token(ctx)
		if err != nil {
			// The caller's token source failed. Its error is attached rather
			// than described, because a token source's message is as likely to
			// contain a credential as anything in this package (rule §2.5).
			return nil, p.fail(ovrin.ErrAuth, page,
				"an access token could not be obtained").WithCause(err)
		}
		if token == "" {
			return nil, p.fail(ovrin.ErrAuth, page,
				"the configured token source returned an empty access token")
		}
		req.Header.Set("Authorization", "Bearer "+token)
	case p.apiKey != "":
		q := req.URL.Query()
		q.Set("key", p.apiKey)
		req.URL.RawQuery = q.Encode()
	default:
		return nil, p.fail(ovrin.ErrAuth, page,
			"no api key and no token source are configured")
	}

	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, p.transportFail(ctx, page, err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on the read path

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, p.transportFail(ctx, page, err)
	}
	if resp.StatusCode >= 300 {
		// Deliberately does not read the provider's message. Vision quotes the
		// offending request back in a validation error, and for OCR the request
		// is the document itself (rule §2.5, §7.5). The status code is safe and
		// is the one detail worth forwarding.
		return nil, p.fail(classifyStatus(resp.StatusCode), page,
			fmt.Sprintf("the provider returned http %d", resp.StatusCode))
	}
	return raw, nil
}

// transportFail reports a call that never produced a usable response.
//
// A context that ended is not a provider failure and gets no sentinel: a
// fallback chain must not advance to the next provider with a context that is
// already dead. The context's own error is attached so that one value answers
// both "what kind of failure was this?" and "was it ultimately a cancelled
// context?" (ADR-0019).
func (p *Provider) transportFail(ctx context.Context, page int, cause error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.fail(nil, page,
			"the context ended before the provider replied").WithCause(ctxErr)
	}
	// The transport error is attached, never described: a *url.Error renders
	// the request URL, and the request URL carries the api key.
	return p.fail(ovrin.ErrUnavailable, page,
		"the provider could not be reached").WithCause(redact(cause))
}

// fail builds a classified ovrin error naming this adapter and this stage.
//
// Every message here is written in this package rather than taken from the
// provider, and every one of them is a fixed string: an error carries the
// operation, the page and the provider, and nothing a document could occupy
// (rule §2.5, §7.5).
func (p *Provider) fail(kind error, page int, message string) *ovrin.Error {
	return &ovrin.Error{
		Op:       ovrin.OpOCR,
		Provider: providerName,
		Page:     page,
		Kind:     kind,
		Message:  message,
	}
}

// redact returns an error safe to attach as a cause.
//
// A [url.Error] renders the URL it failed on, and this package puts the API key
// in the query — so attaching one unchanged would put the credential in every
// log line that prints the wrapped error. Only the underlying transport error
// survives, which is where the useful part (a DNS failure, a refused
// connection, a TLS mismatch) lives anyway.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

var _ ovrin.OCR = (*Provider)(nil)
var _ ovrin.DocumentOCR = (*Provider)(nil)
