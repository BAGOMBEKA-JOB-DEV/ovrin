// Package azure implements ovrin's [ovrin.OCR] and [ovrin.DocumentOCR] seams on
// top of Azure AI Document Intelligence.
//
//	import (
//	    "github.com/BAGOMBEKA-JOB-DEV/ovrin"
//	    ovrinazure "github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/azure"
//	)
//
//	c := ovrin.New(ovrin.WithOCR(ovrinazure.New(endpoint, key)))
//
// It is a separate Go module, because ovrin's core depends on nothing and a
// user running Tesseract locally has no business carrying an Azure client
// ([ADR-0009]).
//
// # It has no dependencies of its own either
//
// Document Intelligence is JSON over HTTPS and authenticates with a header, so
// this package speaks it with [net/http] and [encoding/json] and needs nothing
// else. The alternative, the azure-sdk-for-go module for it, brings azcore,
// azidentity and their transitive tree, and every dependency this module takes
// is inherited by everyone who imports it — the same argument [ADR-0009] makes
// about the core, one level down.
//
// The subscription key is taken explicitly and is never read from the
// environment (rule §6.4). [WithTokenSource] takes a Microsoft Entra ID access
// token from whatever the caller already uses, so managed identities and
// service principals work with the token library landing in the caller's
// go.mod rather than this one's.
//
// # Everything here is asynchronous, because the service is
//
// Document Intelligence has no synchronous endpoint. Every analysis is
// submitted, accepted with an operation URL, and polled until it finishes.
// This package polls it bounded by nothing but the caller's context: no
// deadline is invented here, because a timeout is a policy and policy belongs
// to the core (rule §6.2). How often it asks comes from the service's own
// Retry-After where it sends one, and from [WithPollInterval] otherwise —
// which is the one part of polling an adapter cannot avoid choosing.
//
// A reply to the submission that already carries a finished analysis is used as
// it stands rather than being thrown away in favour of a poll. Discarding a
// result the service has already returned would be dropping data the caller has
// already paid for (rule §6.1).
//
// # What a reading costs
//
// [ovrin.Recognition.Usage] carries one page unit per page, which is what this
// service bills, so the sum over a document's recognitions is what the document
// cost. Without it the OCR stage would be the one part of the pipeline whose
// spend never reaches [ovrin.Metadata.Usage] or a metric at all.
//
// # What this adapter silently ignores
//
// Rule §6.5 asks every adapter to document that, not only what it supports.
//
//   - Paragraphs, tables, key-value pairs, selection marks, barcodes,
//     formulas, styles and handwriting detection. [ovrin.Recognition] is words
//     and lines, so everything richer is dropped from it — and reachable
//     through [ovrin.Recognition.Raw]. Layout preservation is a v0.3 decision.
//   - The result's flat content. Lines are rebuilt from the word geometry
//     instead, because the flat text carries none.
//   - Page rotation. The service reports the angle it found; the polygons it
//     returns are already in page coordinates, so the angle has nowhere to go.
//   - Per-word language. Only the page's detected language reaches
//     [ovrin.Recognition.Language], and where the service reports several for
//     one page the most confident wins.
//   - The page's own confidence, because the service does not report one.
//     [ovrin.Recognition.Confidence] is the mean of the confidences of the
//     words it did report, and [Analysis.PageConfidenceDerived] says so,
//     because a caller weighing this against another provider's page
//     confidence needs to know that this one is second-hand.
//
// # What it refuses rather than degrading
//
//   - A page with no image, or with no size in points. The service measures a
//     rasterised page in its own pixels, so a page that does not state its own
//     size cannot have its boxes expressed in points, and returning pixels
//     labelled as points is exactly the silent degradation rule §6.1 forbids.
//   - Any document that is not a PDF. The service will read TIFF, BMP, HEIF
//     and Office formats too, but reports their geometry in pixels with no
//     resolution — or, for Office and HTML, with no geometry at all — so the
//     result could not be converted to points.
//   - A document the service read fewer pages of than it has.
//   - A result the service measured in a unit this package cannot convert.
//   - An operation URL pointing anywhere but the configured endpoint. The
//     service names the URL its result will appear at, and following one that
//     leads elsewhere would send the caller's subscription key to a host they
//     never configured.
//
// # Retry
//
// There is none, deliberately. Retry, backoff, fallback and timeouts belong to
// ovrin's core so that they are decided once rather than once per adapter
// (rule §6.2). Polling is not retrying: it is how this API returns a result.
//
// A Provider is safe for concurrent use by multiple goroutines.
//
// [ADR-0009]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0009-ocr-seam.md
package azure

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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// providerName appears in [ovrin.Provenance.Method] and on every error, so a
// result carries the evidence of which service read it.
//
// It names the product rather than the vendor: Azure sells more than one thing
// that reads a document, and a result that says only "azure" cannot be told
// from one produced by AI Vision's own read API.
const providerName = "azure-document-intelligence"

// DefaultAPIVersion is the Document Intelligence version this package speaks.
//
// It is exported because the version decides both the route and the shape of
// the reply, and a caller pinned to another one needs to know what they are
// changing. This package targets the 4.0 API and its
// /documentintelligence/... route; the 3.x preview versions served the same
// operations under /formrecognizer/... and are not reachable from here.
const DefaultAPIVersion = "2024-11-30"

// DefaultModel is the model an analysis uses.
//
// prebuilt-read is optical character recognition and nothing else, which is
// precisely what the [ovrin.OCR] seam is for. The document-shaped models return
// an invoice or a receipt object directly, and ovrin deliberately does not use
// them: mixing two extraction systems with different accuracy profiles makes a
// confidence figure uninterpretable (docs/feature-matrix.md).
const DefaultModel = "prebuilt-read"

// defaultPollInterval is how often an operation is asked whether it has
// finished, when the service does not say.
//
// It is not a timeout and it does not bound anything: the loop ends when the
// operation ends or when the caller's context does (rule §6.2).
const defaultPollInterval = time.Second

// Provider reads pages and documents through Azure AI Document Intelligence.
//
// It is safe for concurrent use by multiple goroutines: it holds no mutable
// state after construction, and every call builds its own request and its own
// decoder.
type Provider struct {
	endpoint   string
	key        string
	apiVersion string
	model      string
	locale     string
	hc         *http.Client

	token        func(ctx context.Context) (string, error)
	pollInterval time.Duration
}

// Option configures a [Provider]. Options are applied in order.
type Option func(*Provider)

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

// WithAPIVersion pins the API version requests are made against.
//
// The version is part of the contract this package decodes, so changing it can
// change the shape of the reply; it is exposed because an Azure region may not
// yet serve the newest one. An empty string is ignored.
func WithAPIVersion(v string) Option {
	return func(p *Provider) {
		if v != "" {
			p.apiVersion = v
		}
	}
}

// WithModel selects the model an analysis uses, for a caller who wants table
// and key-value detection or who has a custom model trained on their own
// documents.
//
// The default, [DefaultModel], is text and nothing else, and a recognition from
// it leaves [ovrin.Recognition.Layout] nil: that model does not look for
// structure. Every other model does, so a recognition from one carries a
// non-nil Layout — empty when the page held no tables — with the tables and
// key-value pairs mapped onto [ovrin.Table] and [ovrin.Pair]. "prebuilt-layout"
// is the one to ask for when structure is what is wanted; it is OCR plus
// structure and returns no document-shaped fields.
//
// A document-shaped model returns those fields as well, and ovrin does not use
// them: mixing two extraction systems with different accuracy profiles makes a
// confidence figure uninterpretable. They reach a caller through
// [ovrin.Recognition.Raw]. An empty string is ignored.
func WithModel(id string) Option {
	return func(p *Provider) {
		if id != "" {
			p.model = id
		}
	}
}

// WithLocale tells the service which language to expect, as a BCP-47 tag.
//
// The service detects language on its own and a locale is usually unnecessary;
// it matters for scripts that are ambiguous without one, where an unhinted
// result is not merely worse but wrong. [ovrin.Page] carries no language, so
// there is no request field this could be mapped from and it has to be an
// option.
func WithLocale(tag string) Option {
	return func(p *Provider) { p.locale = tag }
}

// WithTokenSource supplies a Microsoft Entra ID access token for each call,
// which is what managed identities and service principals produce.
//
// It is a function rather than a string because access tokens expire, and it
// takes a context so the caller's own refresh can be cancelled with the
// extraction that triggered it.
//
// This is the seam that keeps the module dependency-free. Azure's credential
// handling is a solved problem living in azidentity, and reimplementing it here
// would put security-relevant code in a package nobody asked to own it:
//
//	cred, err := azidentity.NewDefaultAzureCredential(nil)
//	p := azure.New(endpoint, "", azure.WithTokenSource(func(ctx context.Context) (string, error) {
//	    tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
//	        Scopes: []string{"https://cognitiveservices.azure.com/.default"},
//	    })
//	    return tok.Token, err
//	}))
//
// When set, the token is sent instead of the subscription key given to [New].
func WithTokenSource(fn func(ctx context.Context) (string, error)) Option {
	return func(p *Provider) { p.token = fn }
}

// WithPollInterval sets how often an operation is asked whether it has
// finished, where the service does not say.
//
// It is not a timeout: the polling loop ends when the operation ends or when
// the caller's context does, and nothing here invents a deadline (rule §6.2).
// It is exposed because how often to ask is the one part of polling an adapter
// cannot avoid choosing, and the right answer depends on how large the caller's
// documents are.
//
// A non-positive interval is ignored.
func WithPollInterval(d time.Duration) Option {
	return func(p *Provider) {
		if d > 0 {
			p.pollInterval = d
		}
	}
}

// New returns a Provider authenticating with a Document Intelligence
// subscription key.
//
// endpoint is the resource's own host, as it appears in the portal —
// https://myresource.cognitiveservices.azure.com — with no path and no trailing
// slash. There is no default: an Azure resource is per-account and per-region,
// so a package-level guess would be wrong for everyone.
//
// The key is taken explicitly and is never read from the environment: a library
// that reads the environment is how a program ends up talking to the wrong
// account (rule §6.4). Pass an empty key together with [WithTokenSource] to
// authenticate with Microsoft Entra ID instead, which is what most production
// deployments want.
//
// The returned Provider is safe for concurrent use by multiple goroutines.
func New(endpoint, key string, opts ...Option) *Provider {
	p := &Provider{
		endpoint:   strings.TrimSuffix(endpoint, "/"),
		key:        key,
		apiVersion: DefaultAPIVersion,
		model:      DefaultModel,
		// No timeout: bounding a call is the caller's context's job, and a
		// client timeout here would silently override it.
		hc:           &http.Client{},
		pollInterval: defaultPollInterval,
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
// in: the service measures a rasterised page in its own pixels, and every box
// is scaled into the page's own points before it is returned (ADR-0009).
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
	// exactly there. The service accepts both.
	if err := png.Encode(&encoded, page.Image); err != nil {
		return nil, p.fail(ovrin.ErrInternal, page.Number,
			"the page image could not be encoded")
	}

	raw, res, err := p.analyse(ctx, encoded.Bytes(), page.Number)
	if err != nil {
		return nil, err
	}
	structure := reportsStructure(p.ranModel(res))
	if len(res.Pages) == 0 {
		// Not an error. A blank page is a real thing a scanner produces, and
		// the core decides what an empty reading means (rule §2.6).
		rec := &ovrin.Recognition{
			Usage: ovrin.Usage{PageUnits: 1},
			Raw:   &Analysis{JSON: raw, Page: page.Number},
		}
		if structure {
			// It looked and there was nothing to find, which is not the same
			// as not looking. See [ovrin.Recognition.Layout].
			rec.Layout = &ovrin.Layout{}
		}
		return rec, nil
	}

	first := &res.Pages[0]
	sp, ok := newSpace(first, page.Width, page.Height)
	if !ok {
		return nil, p.fail(ovrin.ErrBadResponse, page.Number,
			"the provider reported no page geometry to convert from")
	}
	// The page number comes from the page that was recognised, not from the
	// reply: a page sent on its own comes back numbered 1 whatever page of the
	// caller's document it actually was.
	rec, err := normalise(res, first, page.Number, sp, raw, structure)
	if err != nil {
		// The cause names table and cell indexes and nothing a document could
		// occupy, so it is safe to attach; the message itself stays fixed
		// (rule §2.5, §7.5).
		return nil, p.fail(ovrin.ErrBadResponse, page.Number,
			"the structure the provider reported is not coherent").WithCause(err)
	}
	return rec, nil
}

// ranModel is the model an analysis was actually performed with.
//
// The reply's own modelId is preferred over the configured one, because that is
// what the service says it ran; the configured model is the fallback for a
// reply that omits it. Which model ran decides whether structure was looked
// for at all, so guessing it from the response's contents would collapse
// "found no tables" into "does not report tables".
func (p *Provider) ranModel(res *analyzeResult) string {
	if res != nil && res.ModelID != "" {
		return res.ModelID
	}
	return p.model
}

// RecogniseDocument implements [ovrin.DocumentOCR], reading every page of a PDF
// that the service rasterises on its own side.
//
// This is the route that lets a scanned PDF be extracted with no local renderer
// at all, which is what [ADR-0010] defers rasterising on. The bytes come from
// [ovrin.Document.Data]: they are already in memory by the time a Document
// exists, so an option to supply them again would be a second source of truth
// for the same document.
//
// Coordinates come back in inches, which convert to points exactly, so nothing
// is lost to rounding.
//
// [ADR-0010]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0010-no-cgo-in-core.md
func (p *Provider) RecogniseDocument(ctx context.Context, doc ovrin.Document) ([]*ovrin.Recognition, error) {
	if doc.Kind != ovrin.KindPDF {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			"the provider reads only pdf documents through this package, and this "+
				"one is "+doc.Kind.String())
	}
	if len(doc.Data) == 0 {
		return nil, p.fail(ovrin.ErrNoContent, 0, "the document is empty")
	}

	raw, res, err := p.analyse(ctx, doc.Data, 0)
	if err != nil {
		return nil, err
	}
	if doc.Pages > 0 && len(res.Pages) < doc.Pages {
		// Handing the caller the pages that did arrive is the silent
		// degradation rule §6.1 exists to prevent.
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			fmt.Sprintf("the provider read %d of the document's %d pages",
				len(res.Pages), doc.Pages))
	}

	// In page order, whatever order they arrived in: a result is a slice
	// indexed by position and a caller reading page three has no way to notice
	// it was handed page four.
	order := make([]int, len(res.Pages))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return res.Pages[order[a]].PageNumber < res.Pages[order[b]].PageNumber
	})

	structure := reportsStructure(p.ranModel(res))
	out := make([]*ovrin.Recognition, 0, len(res.Pages))
	for _, i := range order {
		pg := &res.Pages[i]
		number := pg.PageNumber
		if number <= 0 {
			number = i + 1
		}
		// No page size to scale against: an [ovrin.Document] carries a page
		// count and no geometry, so the unit the service measured in is the
		// only route to points there is.
		sp, ok := newSpace(pg, 0, 0)
		if !ok {
			return nil, p.fail(ovrin.ErrUnsupported, number,
				fmt.Sprintf("the provider measured the page in %q, which cannot be "+
					"converted to points without knowing the page's size",
					pg.Unit))
		}
		rec, err := normalise(res, pg, number, sp, raw, structure)
		if err != nil {
			return nil, p.fail(ovrin.ErrBadResponse, number,
				"the structure the provider reported is not coherent").WithCause(err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// analyse submits one document and polls until the operation ends.
//
// It returns the operation's own reply as it arrived, so that [Analysis.JSON]
// carries exactly what the service sent rather than a re-marshalled
// approximation of it.
func (p *Provider) analyse(ctx context.Context, content []byte, page int) (json.RawMessage, *analyzeResult, error) {
	body, err := json.Marshal(analyzeRequest{
		Base64Source: base64.StdEncoding.EncodeToString(content),
	})
	if err != nil {
		return nil, nil, p.fail(ovrin.ErrInternal, page, "the request could not be encoded")
	}

	raw, header, err := p.send(ctx, http.MethodPost, p.analyseURL(), body, page)
	if err != nil {
		return nil, nil, err
	}

	// A reply that already carries a finished analysis is used as it stands.
	// The documented flow is an accepted submission and an Operation-Location
	// to poll, but discarding a result the service has already returned in
	// order to insist on the header would be dropping data the caller has
	// already paid for (rule §6.1).
	if op, ok := finished(raw); ok {
		return p.result(raw, op, page)
	}

	location := header.Get("Operation-Location")
	if location == "" {
		return nil, nil, p.fail(ovrin.ErrBadResponse, page,
			"the provider accepted the document without saying where its result "+
				"would appear")
	}
	poll, err := p.pollURL(location)
	if err != nil {
		return nil, nil, err
	}

	for {
		raw, header, err := p.send(ctx, http.MethodGet, poll, nil, page)
		if err != nil {
			return nil, nil, err
		}
		var op operation
		if err := json.Unmarshal(raw, &op); err != nil {
			return nil, nil, p.fail(ovrin.ErrBadResponse, page,
				"the provider's reply was not the json the api documents")
		}
		switch op.Status {
		case statusNotStarted, statusRunning, "":
			// How long to wait is the service's own suggestion where it makes
			// one; nothing here invents a deadline around it (rule §6.2).
			if err := wait(ctx, retryAfter(header, p.pollInterval)); err != nil {
				return nil, nil, p.fail(nil, page,
					"the context ended before the analysis finished").WithCause(err)
			}
			continue
		default:
			return p.result(raw, &op, page)
		}
	}
}

// result turns a terminal operation into its analysis, or into an ovrin error.
func (p *Provider) result(raw json.RawMessage, op *operation, page int) (json.RawMessage, *analyzeResult, error) {
	if op.Status != statusSucceeded {
		// The service's own message is not quoted: Azure returns the offending
		// request in a validation error, and for OCR the request is the
		// document itself (rule §2.5, §7.5).
		code := ""
		if op.Error != nil {
			code = op.Error.Code
		}
		return nil, nil, p.fail(classifyCode(code), page,
			fmt.Sprintf("the provider reported the analysis %s", op.Status))
	}
	if op.AnalyzeResult == nil {
		return nil, nil, p.fail(ovrin.ErrBadResponse, page,
			"the provider reported a successful analysis and returned no result")
	}
	return raw, op.AnalyzeResult, nil
}

// finished reports whether a reply already carries a terminal operation, which
// is the case a submission answered with the result itself.
func finished(raw json.RawMessage) (*operation, bool) {
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		return nil, false
	}
	switch op.Status {
	case statusSucceeded, statusFailed:
		// Terminal either way. A success with no result is reported as the
		// unusable reply it is, rather than sent looking for a polling header
		// that a finished operation would not have sent.
		return &op, true
	default:
		return nil, false
	}
}

// analyseURL is where an analysis is submitted.
func (p *Provider) analyseURL() string {
	q := url.Values{}
	q.Set("api-version", p.apiVersion)
	if p.locale != "" {
		q.Set("locale", p.locale)
	}
	return p.endpoint + "/documentintelligence/documentModels/" +
		url.PathEscape(p.model) + ":analyze?" + q.Encode()
}

// pollURL validates the location the service named for its result.
//
// The service names the URL its result will appear at, and this package sends
// the caller's credential to it. A location that leads to another host would
// therefore hand that host the credential, so one is refused rather than
// followed — the same rule that stops ovrin fetching anything a document
// references (rule §7.4).
func (p *Provider) pollURL(location string) (string, error) {
	loc, err := url.Parse(location)
	if err != nil {
		return "", p.fail(ovrin.ErrBadResponse, 0,
			"the provider named a result location that is not a url")
	}
	base, err := url.Parse(p.endpoint)
	if err != nil {
		return "", p.fail(ovrin.ErrBadRequest, 0, "the configured endpoint is not a url")
	}
	if !strings.EqualFold(loc.Scheme, base.Scheme) || !strings.EqualFold(loc.Host, base.Host) {
		return "", p.fail(ovrin.ErrBadResponse, 0,
			"the provider named a result location on another host, and following it "+
				"would send the credential somewhere it was never configured for")
	}
	return loc.String(), nil
}

// retryAfter returns how long the service asked to be left alone for, or
// fallback when it did not say.
//
// Honouring it is mapping rather than deciding: the number is the provider's,
// not a policy invented here (rule §6.2).
func retryAfter(header http.Header, fallback time.Duration) time.Duration {
	v := header.Get("Retry-After")
	if v == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(v)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

// wait sleeps for d, or returns the context's error if it ends first.
//
// It starts no goroutine of its own: a timer that outlives the call is a
// goroutine nothing will ever join (rule §3.6).
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// send makes one request and returns the reply body and headers.
//
// It is where the credential is attached and where a transport failure becomes
// an ovrin sentinel; neither the credential nor the provider's own message ever
// reaches the error it returns.
func (p *Provider) send(ctx context.Context, method, endpoint string, body []byte, page int) (json.RawMessage, http.Header, error) {
	if p.endpoint == "" {
		return nil, nil, p.fail(ovrin.ErrBadRequest, page,
			"no endpoint is configured; an azure resource is per-account and "+
				"per-region, so there is no default to fall back to")
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, nil, p.fail(ovrin.ErrBadRequest, page, "the request could not be built")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	switch {
	case p.token != nil:
		token, err := p.token(ctx)
		if err != nil {
			// The caller's token source failed. Its error is attached rather
			// than described, because a token source's message is as likely to
			// contain a credential as anything in this package (rule §2.5).
			return nil, nil, p.fail(ovrin.ErrAuth, page,
				"an access token could not be obtained").WithCause(err)
		}
		if token == "" {
			return nil, nil, p.fail(ovrin.ErrAuth, page,
				"the configured token source returned an empty access token")
		}
		req.Header.Set("Authorization", "Bearer "+token)
	case p.key != "":
		// A header rather than the query, which is the service's own design
		// and a happy one: a credential in a query string reaches every access
		// log between here and Azure, and a *url.Error renders it into the
		// error chain as well.
		req.Header.Set("Ocp-Apim-Subscription-Key", p.key)
	default:
		return nil, nil, p.fail(ovrin.ErrAuth, page,
			"no subscription key and no token source are configured")
	}

	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, nil, p.transportFail(ctx, page, err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on the read path

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, p.transportFail(ctx, page, err)
	}
	if resp.StatusCode >= 300 {
		// The error code is read; the message never is. Azure quotes the
		// offending request back in a validation error, and for OCR the request
		// is the document itself (rule §2.5, §7.5).
		var envelope errorEnvelope
		// A body that does not decode leaves the classification to the status
		// alone, which is the safe direction.
		_ = json.Unmarshal(raw, &envelope) //nolint:errcheck // see above
		kind := classifyStatus(resp.StatusCode)
		if envelope.Error != nil && resp.StatusCode == http.StatusBadRequest {
			// A 400 is the one status Azure uses for several unrelated
			// conditions, so the code refines it. Everything else the status
			// already says unambiguously.
			kind = classifyCode(envelope.Error.Code)
		}
		return nil, nil, p.fail(kind, page,
			fmt.Sprintf("the provider returned http %d", resp.StatusCode))
	}
	return raw, resp.Header, nil
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
// A [url.Error] renders the URL it failed on. This package authenticates with a
// header, so its own URLs carry nothing secret and this is not the leak
// ocr/google found — but one of those URLs is named by the provider rather than
// written here, and the useful part of a transport failure is underneath it
// anyway: a DNS failure, a refused connection, a TLS mismatch.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

var _ ovrin.OCR = (*Provider)(nil)
var _ ovrin.DocumentOCR = (*Provider)(nil)
