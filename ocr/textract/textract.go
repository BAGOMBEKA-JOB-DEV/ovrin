// Package textract implements ovrin's [ovrin.OCR] and [ovrin.DocumentOCR]
// seams on top of the Amazon Textract API.
//
//	import (
//	    "github.com/BAGOMBEKA-JOB-DEV/ovrin"
//	    ovrintextract "github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/textract"
//	)
//
//	c := ovrin.New(ovrin.WithOCR(ovrintextract.New("us-east-1", ovrintextract.Credentials{
//	    AccessKeyID:     id,
//	    SecretAccessKey: secret,
//	})))
//
// It is a separate Go module, because ovrin's core depends on nothing and a
// user running Tesseract locally has no business carrying an AWS client
// ([ADR-0009]).
//
// # It has no dependencies of its own either
//
// Textract is JSON over HTTPS, and this package speaks it with [net/http] and
// [encoding/json]. The one thing that looks like it needs a library is request
// signing, and Signature Version 4 for a JSON API is eighty lines of
// HMAC-SHA256 over [crypto/hmac] — see sigv4.go, which reproduces AWS's own
// published test vectors. The alternative, aws-sdk-go-v2, is a dozen modules
// with their own transitive tree, and every dependency this module takes is
// inherited by everyone who imports it — the same argument [ADR-0009] makes
// about the core, one level down. So the whole module is standard library
// only.
//
// Credentials are taken explicitly, as [Credentials] or through
// [WithCredentialsProvider] for the refreshing kind. Nothing is read from the
// environment and no instance metadata endpoint is contacted: a library that
// resolves its own credentials is how a program talks to the wrong account
// (rule §6.4).
//
// # Whole documents
//
// [Provider.RecogniseDocument] reads a multi-page PDF or TIFF, and the shape of
// Textract's API decides how:
//
//   - The synchronous operation accepts the document's bytes inline and reads
//     a single page.
//   - The asynchronous operation reads as many pages as the document has, and
//     accepts nothing but an Amazon S3 object — its request has no field a
//     document's bytes could travel in.
//
// So a document of more than one page needs [WithDocumentLocation], naming the
// S3 object the caller has already put it in, and a Provider configured with
// one uses it for every document rather than choosing per call. Uploading it here instead would
// mean this package choosing a bucket, a key, an encryption mode and a
// lifecycle on the caller's behalf, which is deciding rather than mapping
// (rule §6.2) — and it would put a copy of every document somewhere the caller
// did not ask for one. A document that cannot be read is refused, naming what
// was missing, rather than half-read.
//
// The asynchronous operation is a submit-and-poll API, and this package polls
// it bounded by nothing but the caller's context: no deadline is invented here,
// because a timeout is a policy and policy belongs to the core (rule §6.2).
// [WithPollInterval] sets how often it asks, which is the one part of polling
// an adapter cannot avoid choosing.
//
// # What a reading costs
//
// [ovrin.Recognition.Usage] carries one page unit per page, which is what
// Textract bills, so the sum over a document's recognitions is what the
// document cost. Without it the OCR stage would be the one part of the pipeline
// whose spend never reaches [ovrin.Metadata.Usage] or a metric at all.
//
// # What this adapter silently ignores
//
// Rule §6.5 asks every adapter to document that, not only what it supports.
//
//   - Block identifiers and the relationship graph. Ovrin's [ovrin.Recognition]
//     is words and lines, so the PAGE → LINE → WORD graph is flattened and the
//     identifiers that expressed it are dropped. Reachable through
//     [ovrin.Recognition.Raw].
//   - Per-word TextType. Textract labels each word PRINTED or HANDWRITING, and
//     [ovrin.Word] has nowhere to record it, so it reaches a caller through
//     [Analysis] alone.
//   - Everything AnalyzeDocument adds. Tables, forms, key-value pairs,
//     signatures, queries and layout are a different, more expensive operation
//     that this package deliberately does not call — see
//     docs/feature-matrix.md on why provider-side extraction is unused.
//   - The page's own confidence, because Textract does not report one.
//     [ovrin.Recognition.Confidence] is the mean of the confidences of the
//     lines Textract did report, and [Analysis.PageConfidenceDerived] says so,
//     because a caller weighing this against another provider's page
//     confidence needs to know that this one is second-hand.
//   - Warnings on a fully successful job. A job that reports a page it could
//     not read is refused rather than returned short, so a warning only ever
//     reaches [Analysis].
//
// # What it refuses rather than degrading
//
//   - A page with no image, or with no size in points. Textract reports every
//     box as a fraction of the page and never says how large the page is, so a
//     page that does not state its own size cannot have its boxes expressed in
//     points, and returning fractions labelled as points is exactly the silent
//     degradation rule §6.1 forbids.
//   - A document without [WithPageSize], for the same reason and with no page
//     to read the size from.
//   - Any document that is not a PDF or a TIFF.
//   - A document of more than one page with no [WithDocumentLocation], naming
//     both limits.
//   - A job that ended in PARTIAL_SUCCESS. Some pages were not read, and
//     handing back the rest is the silent truncation §6.1 exists to prevent.
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
package textract

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
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// providerName appears in [ovrin.Provenance.Method] and on every error, so a
// result carries the evidence of which service read it.
//
// It names the product rather than the vendor: AWS sells more than one thing
// that reads a document, and a result that says only "aws" cannot be told from
// one produced by Bedrock.
const providerName = "aws-textract"

// contentTypeJSON is the media type AWS's JSON-RPC protocol uses. It is not
// application/json, and Textract rejects a request that says it is.
const contentTypeJSON = "application/x-amz-json-1.1"

// maxSyncPages is the page limit of Textract's synchronous operation.
//
// One. The synchronous operation is the only one that accepts a document's
// bytes, and it reads a single page of a PDF or TIFF; a longer document is
// refused rather than truncated (rule §6.1) unless [WithDocumentLocation]
// makes the asynchronous operation available.
const maxSyncPages = 1

// defaultPollInterval is how often an asynchronous job is asked whether it has
// finished.
//
// It is not a timeout and it does not bound anything: the loop ends when the
// job ends or when the caller's context does (rule §6.2). One second is short
// enough that a small document is not left waiting and long enough that a large
// one does not spend its life being asked.
const defaultPollInterval = time.Second

// S3Object names a document already stored in Amazon S3.
//
// It exists because Textract's asynchronous operation — the only one that reads
// more than one page — has no field a document's bytes can travel in. See
// [WithDocumentLocation].
type S3Object struct {
	// Bucket and Name are the bucket and the key.
	Bucket string
	Name   string

	// Version selects a version of the object, for a versioned bucket. Empty
	// means the current one.
	Version string
}

// DefaultEndpoint returns where AWS serves Textract in a region.
//
// It is exported because the only sane way to override it — a VPC endpoint, a
// FIPS endpoint such as textract-fips.us-east-1.amazonaws.com, or a test server
// — is to know what is being overridden.
func DefaultEndpoint(region string) string {
	return "https://textract." + region + ".amazonaws.com"
}

// Provider reads pages and documents through Amazon Textract.
//
// It is safe for concurrent use by multiple goroutines: it holds no mutable
// state after construction, and every call builds its own request, its own
// signature and its own decoder.
type Provider struct {
	region  string
	creds   Credentials
	baseURL string
	hc      *http.Client

	credentials  func(ctx context.Context) (Credentials, error)
	location     func(ctx context.Context, doc ovrin.Document) (S3Object, error)
	pageW, pageH float64
	pollInterval time.Duration
}

// Option configures a [Provider]. Options are applied in order.
type Option func(*Provider)

// WithBaseURL points the provider at another endpoint — a VPC or FIPS endpoint,
// or a test server.
//
// The URL is the API root with no trailing slash. An empty string is ignored,
// so a caller reading a base URL out of their own configuration does not have
// to branch on it being unset.
//
// The region given to [New] still decides the credential scope a request is
// signed for, because that is what AWS verifies the signature against and it
// is not always what the hostname says.
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

// WithCredentialsProvider supplies the credentials for each call, which is what
// temporary credentials need.
//
// It is a function rather than a struct because credentials from AssumeRole, an
// instance profile or IAM Roles for Service Accounts expire, and it takes a
// context so the caller's own refresh can be cancelled with the extraction that
// triggered it.
//
// This is the seam that keeps the module dependency-free. Resolving AWS
// credentials is a solved problem living in aws-sdk-go-v2's config package, and
// reimplementing the metadata-endpoint dance here would put security-relevant
// code in a package nobody asked to own it:
//
//	cfg, err := config.LoadDefaultConfig(ctx)
//	p := textract.New(cfg.Region, textract.Credentials{},
//	    textract.WithCredentialsProvider(func(ctx context.Context) (textract.Credentials, error) {
//	        c, err := cfg.Credentials.Retrieve(ctx)
//	        if err != nil {
//	            return textract.Credentials{}, err
//	        }
//	        return textract.Credentials{
//	            AccessKeyID:     c.AccessKeyID,
//	            SecretAccessKey: c.SecretAccessKey,
//	            SessionToken:    c.SessionToken,
//	        }, nil
//	    }))
//
// When set, it is used instead of the credentials given to [New].
func WithCredentialsProvider(fn func(ctx context.Context) (Credentials, error)) Option {
	return func(p *Provider) { p.credentials = fn }
}

// WithDocumentLocation names the Amazon S3 object a document has already been
// uploaded to, which is what [Provider.RecogniseDocument] reads for a document
// of more than one page.
//
// It exists because Textract's asynchronous operation is the only one that
// reads a whole document, and it accepts an S3 object rather than bytes. This
// package does not upload the document itself: choosing a bucket, a key, an
// encryption mode and a lifecycle on the caller's behalf is deciding rather
// than mapping (rule §6.2), and it would leave a copy of every document
// somewhere the caller did not ask for one.
//
// It is a function of the document so that one Provider can serve many
// documents concurrently, and so that a caller who uploads on demand can do so
// when the document is actually about to be read.
//
// Without it, a document of more than [maxSyncPages] page is refused with
// [ovrin.ErrUnsupported] naming the limit, rather than half-read.
func WithDocumentLocation(fn func(ctx context.Context, doc ovrin.Document) (S3Object, error)) Option {
	return func(p *Provider) { p.location = fn }
}

// WithPageSize declares how large a document's pages are, in points.
//
// Textract reports every box as a fraction of the page in both axes and never
// says how large the page is. [ovrin.Page] carries its own size, so
// [Provider.Recognise] needs none of this; [ovrin.Document] carries a page
// count and no geometry, so [Provider.RecogniseDocument] cannot convert a
// fraction into a point without being told.
//
// A default of US Letter would be the worst possible answer: every box on
// every A4 document would be wrong by four per cent, silently, in a value
// nothing downstream can sanity-check. So without this, a document is refused
// naming what was missing (rule §6.1).
//
// One size applies to every page. A document whose pages differ in size cannot
// be described by it, and is a document to render locally instead.
func WithPageSize(width, height float64) Option {
	return func(p *Provider) { p.pageW, p.pageH = width, height }
}

// WithPollInterval sets how often an asynchronous job is asked whether it has
// finished.
//
// It is not a timeout: the polling loop ends when the job ends or when the
// caller's context does, and nothing here invents a deadline (rule §6.2). It is
// exposed because how often to ask is the one part of polling an adapter cannot
// avoid choosing, and the right answer depends on how large the caller's
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

// New returns a Provider signing its requests with an AWS access key.
//
// The signature differs from the [ovrin.OCR] adapters that take a single API
// key because an AWS credential is a pair and a request is signed for one
// region; both are needed before a call can be made at all, so neither is an
// option that could be forgotten.
//
// The credentials are taken explicitly and are never read from the environment
// (rule §6.4). Pass a zero [Credentials] together with
// [WithCredentialsProvider] to resolve them per call instead, which is what
// temporary credentials need.
//
// The returned Provider is safe for concurrent use by multiple goroutines.
func New(region string, creds Credentials, opts ...Option) *Provider {
	p := &Provider{
		region:  region,
		creds:   creds,
		baseURL: DefaultEndpoint(region),
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

// Recognise implements [ovrin.OCR], reading one rasterised page through
// Textract's synchronous text detection.
//
// The page's Width and Height are what the returned coordinates are expressed
// in: Textract reports every box as a fraction of the page in both axes, and
// each one is multiplied into the page's own points before it is returned
// (ADR-0009).
func (p *Provider) Recognise(ctx context.Context, page ovrin.Page) (*ovrin.Recognition, error) {
	if page.Image == nil {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			"the page carries no image, and there is nothing to recognise")
	}
	sp := space{width: page.Width, height: page.Height}
	if !sp.valid() {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			"the page does not say how large it is in points, so a box could not "+
				"be converted out of the provider's fractions")
	}

	var encoded bytes.Buffer
	// PNG rather than JPEG: OCR reads glyph edges, and JPEG's artefacts sit
	// exactly there. Textract accepts both.
	if err := png.Encode(&encoded, page.Image); err != nil {
		return nil, p.fail(ovrin.ErrInternal, page.Number,
			"the page image could not be encoded")
	}

	raw, err := p.call(ctx, targetDetect, detectRequest{
		Document: requestDocument{
			Bytes: base64.StdEncoding.EncodeToString(encoded.Bytes()),
		},
	}, page.Number)
	if err != nil {
		return nil, err
	}

	var resp analyzeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, p.fail(ovrin.ErrBadResponse, page.Number,
			"the provider's reply was not the json the api documents")
	}
	// The page number comes from the page that was recognised, not from the
	// reply: a synchronous reply numbers its only page 1 whatever page of the
	// caller's document it actually was.
	return normalise(resp.Blocks, page.Number, sp, raw), nil
}

// RecogniseDocument implements [ovrin.DocumentOCR], reading every page of a PDF
// or TIFF that Textract rasterises on its own side.
//
// This is the route that lets a scanned PDF be extracted with no local renderer
// at all, which is what [ADR-0010] defers rasterising on. Which of Textract's
// two operations serves it depends on what the caller supplied: where
// [WithDocumentLocation] names an Amazon S3 object, the asynchronous operation
// reads the document from there, and otherwise the document's own bytes are
// sent inline — which reads a single page and refuses anything longer, because
// the asynchronous operation accepts nothing but S3. Both need [WithPageSize],
// because Textract's geometry is a fraction of a page whose size it never
// states.
//
// [ADR-0010]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0010-no-cgo-in-core.md
func (p *Provider) RecogniseDocument(ctx context.Context, doc ovrin.Document) ([]*ovrin.Recognition, error) {
	if doc.Kind != ovrin.KindPDF && doc.Kind != ovrin.KindTIFF {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			"the provider reads only pdf and tiff documents, and this one is "+
				doc.Kind.String())
	}
	sp := space{width: p.pageW, height: p.pageH}
	if !sp.valid() {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			"no page size is configured; the provider reports every box as a "+
				"fraction of the page and never says how large the page is, so pass "+
				"WithPageSize")
	}

	var (
		blocks []block
		pages  int
		raw    json.RawMessage
		err    error
	)
	if p.location != nil {
		blocks, pages, raw, err = p.asynchronous(ctx, doc)
	} else {
		blocks, pages, raw, err = p.synchronous(ctx, doc)
	}
	if err != nil {
		return nil, err
	}
	return p.split(blocks, pages, doc.Pages, sp, raw)
}

// synchronous sends a document's own bytes, which is the only route Textract
// offers for them and which reads a single page.
//
// The document is read from [ovrin.Document.Data]: the bytes are already in
// memory by the time a Document exists, so an option to supply them again would
// be a second source of truth for the same document.
func (p *Provider) synchronous(ctx context.Context, doc ovrin.Document) ([]block, int, json.RawMessage, error) {
	if doc.Pages > maxSyncPages {
		return nil, 0, nil, p.fail(ovrin.ErrUnsupported, 0,
			fmt.Sprintf("the document has %d pages; the operation that accepts a "+
				"document inline reads %d, and the one that reads more accepts only "+
				"an s3 object, so pass WithDocumentLocation",
				doc.Pages, maxSyncPages))
	}
	if len(doc.Data) == 0 {
		return nil, 0, nil, p.fail(ovrin.ErrNoContent, 0, "the document is empty")
	}

	raw, err := p.call(ctx, targetDetect, detectRequest{
		Document: requestDocument{
			Bytes: base64.StdEncoding.EncodeToString(doc.Data),
		},
	}, 0)
	if err != nil {
		return nil, 0, nil, err
	}
	var resp analyzeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, nil, p.fail(ovrin.ErrBadResponse, 0,
			"the provider's reply was not the json the api documents")
	}
	return resp.Blocks, metadataPages(&resp), raw, nil
}

// asynchronous submits a job and polls it until it ends or the caller's context
// does.
//
// Nothing here invents a deadline. A polling loop with a timeout of its own
// would be a second policy disagreeing with the core's (rule §6.2), and the
// caller's context is the one bound that is already correct.
func (p *Provider) asynchronous(ctx context.Context, doc ovrin.Document) ([]block, int, json.RawMessage, error) {
	loc, err := p.location(ctx, doc)
	if err != nil {
		// The caller's own error is attached rather than described: it is
		// theirs to read, and nothing here may quote it into a message that
		// could carry the document (rule §2.5).
		return nil, 0, nil, p.fail(ovrin.ErrNoContent, 0,
			"the document's location could not be determined").WithCause(err)
	}
	if loc.Bucket == "" || loc.Name == "" {
		return nil, 0, nil, p.fail(ovrin.ErrNoContent, 0,
			"the configured document location names no s3 object")
	}

	raw, err := p.call(ctx, targetStart, startRequest{
		DocumentLocation: documentLocation{S3Object: s3Object{
			Bucket:  loc.Bucket,
			Name:    loc.Name,
			Version: loc.Version,
		}},
	}, 0)
	if err != nil {
		return nil, 0, nil, err
	}
	var start startResponse
	if err := json.Unmarshal(raw, &start); err != nil || start.JobID == "" {
		return nil, 0, nil, p.fail(ovrin.ErrBadResponse, 0,
			"the provider accepted the document without returning a job to poll")
	}

	var (
		blocks []block
		pages  int
		first  json.RawMessage
		token  string
	)
	for {
		raw, err := p.call(ctx, targetGet, getRequest{JobID: start.JobID, NextToken: token}, 0)
		if err != nil {
			return nil, 0, nil, err
		}
		var resp analyzeResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, 0, nil, p.fail(ovrin.ErrBadResponse, 0,
				"the provider's reply was not the json the api documents")
		}

		switch resp.JobStatus {
		case jobSucceeded:
		case jobInProgress:
			if err := wait(ctx, p.pollInterval); err != nil {
				return nil, 0, nil, p.fail(nil, 0,
					"the context ended before the job finished").WithCause(err)
			}
			continue
		case jobFailed:
			// The provider's own StatusMessage is not quoted: Textract puts the
			// offending request in some of them (rule §2.5).
			return nil, 0, nil, p.fail(ovrin.ErrBadRequest, 0,
				"the provider reported that the job failed")
		case jobPartial:
			// Some pages were read and some were not. Handing back the ones
			// that were is the silent truncation rule §6.1 exists to prevent.
			return nil, 0, nil, p.fail(ovrin.ErrUnsupported, 0,
				fmt.Sprintf("the provider could not read %d of the document's pages",
					len(resp.Warnings)))
		default:
			return nil, 0, nil, p.fail(ovrin.ErrBadResponse, 0,
				"the provider reported no job status this package knows")
		}

		if first == nil {
			first = raw
		}
		blocks = append(blocks, resp.Blocks...)
		if n := metadataPages(&resp); n > pages {
			pages = n
		}
		if resp.NextToken == "" {
			return blocks, pages, first, nil
		}
		if resp.NextToken == token {
			// A provider repeating its own continuation token would be polled
			// for ever, and a loop bounded only by a context is a loop that
			// must not be able to spin.
			return nil, 0, nil, p.fail(ovrin.ErrBadResponse, 0,
				"the provider repeated its continuation token")
		}
		token = resp.NextToken
	}
}

// split turns one response's blocks into one [ovrin.Recognition] per page, in
// page order.
//
// declared is the page count the caller's [ovrin.Document] claimed, or zero when
// it did not know. A provider that read fewer pages than the document has
// truncated it, and returning the pages that did arrive is the silent
// degradation rule §6.1 forbids.
func (p *Provider) split(blocks []block, reported, declared int, sp space, raw json.RawMessage) ([]*ovrin.Recognition, error) {
	byPage := make(map[int][]block, reported)
	highest := 0
	for _, b := range blocks {
		n := b.Page
		if n <= 0 {
			// A synchronous reply omits the page number, because it read one.
			n = 1
		}
		byPage[n] = append(byPage[n], b)
		if n > highest {
			highest = n
		}
	}

	count := reported
	if highest > count {
		// The provider returned blocks for a page its own metadata did not
		// count. Trusting the metadata would drop them.
		count = highest
	}
	if count == 0 {
		count = 1
	}
	if declared > 0 && count < declared {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			fmt.Sprintf("the provider read %d of the document's %d pages",
				count, declared))
	}

	out := make([]*ovrin.Recognition, 0, count)
	for n := 1; n <= count; n++ {
		// A page with no blocks is a blank page, which is a real thing a
		// scanner produces; the core decides what an empty reading means
		// (rule §2.6).
		out = append(out, normalise(byPage[n], n, sp, raw))
	}
	return out, nil
}

// metadataPages returns the page count a reply declared, or zero.
func metadataPages(resp *analyzeResponse) int {
	if resp.DocumentMetadata == nil {
		return 0
	}
	return resp.DocumentMetadata.Pages
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

// call sends one signed request and returns the reply body.
//
// It is where the credential is attached and where a transport failure becomes
// an ovrin sentinel; neither the credential nor the provider's own message ever
// reaches the error it returns.
func (p *Provider) call(ctx context.Context, target string, payload any, page int) (json.RawMessage, error) {
	if p.region == "" {
		return nil, p.fail(ovrin.ErrBadRequest, page,
			"no aws region is configured, and a request cannot be signed without one")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, p.fail(ovrin.ErrInternal, page, "the request could not be encoded")
	}

	creds := p.creds
	if p.credentials != nil {
		creds, err = p.credentials(ctx)
		if err != nil {
			// The caller's credential source failed. Its error is attached
			// rather than described, because such a message is as likely to
			// contain a credential as anything in this package (rule §2.5).
			return nil, p.fail(ovrin.ErrAuth, page,
				"credentials could not be obtained").WithCause(err)
		}
	}
	if !creds.valid() {
		return nil, p.fail(ovrin.ErrAuth, page,
			"no access key and no credentials provider are configured")
	}

	// Textract is a JSON-RPC style API: every operation is a POST to the root
	// and the operation travels in a header.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/", bytes.NewReader(body))
	if err != nil {
		return nil, p.fail(ovrin.ErrBadRequest, page, "the request could not be built")
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("X-Amz-Target", target)
	// The signature covers the bytes that are actually sent, which is why the
	// encoded body is signed rather than the value it came from.
	signV4(req, body, creds, p.region, signingService, time.Now())

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
		// The exception name is read; the message never is. Textract quotes the
		// offending request back in a validation error, and for OCR the request
		// is the document itself (rule §2.5, §7.5).
		var e apiError
		// A body that does not decode leaves the classification to the status
		// alone, which is the safe direction.
		_ = json.Unmarshal(raw, &e) //nolint:errcheck // see above
		return nil, p.fail(classifyException(resp.StatusCode, &e), page,
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
// A [url.Error] renders the URL it failed on. Textract signs its requests, so
// nothing secret is in the URL and this is not the leak ocr/google found — but
// a caller may point this package at a base URL of their own with a token in
// it, and only the underlying transport error is useful anyway. That is where
// the diagnosis lives: a DNS failure, a refused connection, a TLS mismatch.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

var _ ovrin.OCR = (*Provider)(nil)
var _ ovrin.DocumentOCR = (*Provider)(nil)
