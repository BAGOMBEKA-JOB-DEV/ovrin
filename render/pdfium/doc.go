// Package pdfium rasterises PDF pages using PDFium compiled to WebAssembly.
//
// This package uses no cgo. PDFium runs as a WebAssembly module under Wazero,
// a pure-Go runtime, so a program importing this package still cross-compiles
// and still builds with CGO_ENABLED=0 — which is most of why
// docs/adr/0010-no-cgo-in-core.md chose this over native PDFium bindings. The
// cost is speed: rendering under Wazero is materially slower than native
// PDFium, and the embedded WASM blob is roughly four megabytes.
//
// It exists so that scanned PDFs can be read offline. Without a renderer a
// scan can only be read by a cloud OCR provider that rasterises server-side,
// which air-gapped and data-residency-constrained deployments cannot use.
//
// # Before reaching for it
//
// Most extractions need no renderer at all, and the three ways to avoid one
// are all cheaper than this package:
//
//  1. A PDF with a text layer is read directly. This is most PDFs.
//  2. A [ovrin.DocumentOCR] provider takes the PDF and rasterises server-side.
//  3. PNG, JPEG and TIFF inputs are already images.
//
// # Licences
//
// This package is Apache-2.0. It depends on github.com/klippa-app/go-pdfium
// (MIT), github.com/tetratelabs/wazero (Apache-2.0) and, inside the WASM blob,
// PDFium (BSD-3-Clause). Nothing in the tree is GPL or AGPL, which rule §4.4
// forbids outright.
package pdfium
