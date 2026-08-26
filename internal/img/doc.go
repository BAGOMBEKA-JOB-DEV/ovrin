// Package img decodes an image source into pages the pipeline can read.
//
// It is the acquisition path for documents that are already images — a scan
// exported as PNG, a photograph of a receipt. There is nothing to parse: the
// bytes are the page.
//
// The one thing this package must get right is the pixel limit. A decoded
// image is width × height × 4 bytes in memory, so a file declaring 60,000 ×
// 60,000 asks for fourteen gibibytes from a few kilobytes on disk — the image
// equivalent of the decompression bomb in
// docs/adr/0020-resource-limits.md. Every decoder here reads the *header*
// first and refuses on the declared dimensions, so the allocation never
// happens. Decoding to discover the size is exactly the mistake the limit
// exists to prevent.
//
// This package cannot import the root: the root imports the pipeline, which
// imports this. So it declares its own Kind and its own sentinels, and the
// pipeline converts. See docs/architecture.md, "Layout".
package img
