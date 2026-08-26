package main

import (
	"bytes"
	"fmt"
	"strings"
)

// PDF page geometry, in points. A4, because these are documents from a part of
// the world that uses A4.
const (
	pageWidth   = 595.0
	pageHeight  = 842.0
	pdfMargin   = 56.0
	pdfLeading  = 12.0
	pdfBodySize = 9.0
	pdfHeadSize = 13.0
)

// The body of every generated PDF is set in Courier because Courier is
// metrically fixed: a document's money column is aligned by padding the string,
// and doing that in a proportional face would need a Helvetica metrics table —
// four hundred numbers in aid of a fixture.

// writePDF renders body as a PDF with a real text layer.
//
// A hand-written PDF, for the same reason the font is hand-written: no
// dependency may enter this module, and the subset needed here — a catalogue,
// a page tree, one uncompressed content stream per page and two base-14 fonts
// — is a few dozen lines. Uncompressed on purpose: a corpus document that a
// person can read in a text editor is a corpus document whose ground truth can
// be checked without running anything.
//
// This produces the clean-digital difficulty and nothing else. A PDF this
// programme wrote proves only that ovrin can read this programme's writing
// (rules §3.5), which is exactly why the same body is also drawn, rotated,
// blurred and re-compressed into the harder difficulties.
func writePDF(body []string) []byte {
	pages := paginate(body)

	var objs [][]byte
	add := func(format string, args ...any) int {
		objs = append(objs, []byte(fmt.Sprintf(format, args...)))
		return len(objs)
	}

	// Object numbering is assigned up front because a page dictionary has to
	// name its content stream and both fonts before any of them are written.
	nCatalog := 1
	nPages := 2
	nFontBody := 3
	nFontHead := 4
	firstPage := 5

	objs = append(objs, nil, nil, nil, nil) // reserved for the four above

	kids := make([]string, 0, len(pages))
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", firstPage+i*2))
	}

	objs[nCatalog-1] = []byte("<< /Type /Catalog /Pages 2 0 R >>")
	objs[nPages-1] = []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "), len(pages)))
	objs[nFontBody-1] = []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	objs[nFontHead-1] = []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")

	for i, p := range pages {
		stream := contentStream(p)
		pageNum := firstPage + i*2
		contentNum := pageNum + 1
		add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %.0f %.0f] "+
			"/Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> /Contents %d 0 R >>",
			nPages, pageWidth, pageHeight, nFontBody, nFontHead, contentNum)
		add("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	// A binary comment marks the file as containing binary data, which is what
	// stops a well-meaning transfer from mangling it.
	buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})

	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", i+1)
		buf.Write(o)
		buf.WriteString("\nendobj\n")
	}

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, nCatalog, xref)
	return buf.Bytes()
}

// paginate splits a body into pages that fit inside the margins.
func paginate(body []string) [][]string {
	usable := pageHeight - 2*pdfMargin
	perPage := int(usable / pdfLeading)
	if perPage < 1 {
		perPage = 1
	}
	var pages [][]string
	for i := 0; i < len(body); i += perPage {
		j := i + perPage
		if j > len(body) {
			j = len(body)
		}
		pages = append(pages, body[i:j])
	}
	if len(pages) == 0 {
		pages = [][]string{{}}
	}
	return pages
}

// contentStream draws one page's lines.
func contentStream(lines []string) string {
	var b strings.Builder
	y := pageHeight - pdfMargin
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "@R"):
			// A rule. Drawn as a thin filled rectangle rather than a stroked
			// path so the content stream needs no graphics state at all.
			fmt.Fprintf(&b, "%.2f %.2f %.2f %.2f re f\n",
				pdfMargin, y-2, pageWidth-2*pdfMargin, 0.5)
		case strings.HasPrefix(l, "@H "):
			fmt.Fprintf(&b, "BT /F2 %.1f Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n",
				pdfHeadSize, pdfMargin, y-pdfHeadSize, pdfEscape(l[3:]))
		case strings.HasPrefix(l, "@B "):
			fmt.Fprintf(&b, "BT /F2 %.1f Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n",
				pdfBodySize, pdfMargin, y-pdfBodySize, pdfEscape(l[3:]))
		case strings.TrimSpace(l) == "":
			// Nothing to draw, but the line still advances.
		default:
			fmt.Fprintf(&b, "BT /F1 %.1f Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n",
				pdfBodySize, pdfMargin, y-pdfBodySize, pdfEscape(l))
		}
		y -= pdfLeading
	}
	return b.String()
}

// pdfEscape quotes the three characters a PDF literal string cannot carry
// raw, and drops anything outside WinAnsi rather than emitting bytes the
// declared encoding cannot represent.
func pdfEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 32 || r > 126:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
