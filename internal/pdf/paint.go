package pdf

// This file is the smallest amount of painting a text reader can get away with
// and still answer one question: what colour is the paper under this word?
//
// It is not rendering. No path is filled, no pixel is produced and no shape is
// followed round a curve. What is tracked is a bounding box, a clip box and
// whether a fill covered the page — which is enough to recognise the
// background rectangle every generator lays down first, and enough to know
// when something else painted the page and the answer is therefore unknown
// (docs/adr/0017-untrusted-document-content.md mitigation 4).

// rect is an axis-aligned box. It is this package's working shape rather than
// normalise.Rect because it lives in PDF user space, y upwards, and converting
// at the boundary is what keeps the two conventions from being mixed up
// (docs/adr/0015-provenance.md).
type rect struct {
	minX, minY, maxX, maxY float64
}

// empty reports whether the box encloses nothing.
func (r rect) empty() bool { return r.maxX <= r.minX || r.maxY <= r.minY }

// area returns the enclosed area, which is zero for an empty box.
func (r rect) area() float64 {
	if r.empty() {
		return 0
	}
	return (r.maxX - r.minX) * (r.maxY - r.minY)
}

// intersect returns the overlap of two boxes, which may be empty.
func (r rect) intersect(o rect) rect {
	if r.minX < o.minX {
		r.minX = o.minX
	}
	if r.minY < o.minY {
		r.minY = o.minY
	}
	if r.maxX > o.maxX {
		r.maxX = o.maxX
	}
	if r.maxY > o.maxY {
		r.maxY = o.maxY
	}
	return r
}

// coverFraction is how much of the page a fill must reach to be the page's
// background.
//
// It is a fraction rather than an exact containment because a generator fills
// the crop box, or the media box inset by a hairline, or the box it rounded to
// two decimal places — and because a background that misses the last half
// point of the margin is still the background.
const coverFraction = 0.98

// covers reports whether this box paints essentially all of the page.
func (r rect) covers(page rect) bool {
	a := page.area()
	return a > 0 && r.intersect(page).area() >= coverFraction*a
}

// pathState is the current path, reduced to what background inference needs.
//
// A path costs O(1) here however many segments it has: a content stream of a
// million lineto operators grows a bounding box and nothing else
// (docs/adr/0020-resource-limits.md).
type pathState struct {
	bbox rect
	has  bool

	// shoelace is twice the signed area of the subpaths so far, accumulated
	// by the shoelace formula as points arrive. It costs two multiplications
	// per point and no storage, which keeps a path O(1) however many segments
	// it has.
	//
	// It exists because a bounding box is a terrible estimate of a thin
	// diagonal: a hairline from corner to corner has a box the size of the
	// page and an area of almost nothing, and tallying the box made this
	// package report "the page is painted, I cannot tell you its colour" for
	// a document that is plainly white paper with a line drawn on it.
	shoelace float64

	// first and last are the current subpath's start and its most recent
	// point, in device space, which is all the shoelace sum needs.
	firstX, firstY float64
	lastX, lastY   float64

	// rectsOnly reports that every subpath so far was an axis-aligned
	// rectangle, which is the only shape whose bounding box is the shape.
	rectsOnly bool

	// rectArea is the total area of those rectangles, clipped to the page. A
	// background written as two half-page rectangles is as much a background
	// as one written as a whole-page rectangle.
	rectArea float64

	// covers reports that one rectangle covered the page on its own.
	covers bool
}

// resetPath starts a new path. A path begins empty and made of rectangles,
// because a path with no segments in it has broken no rule yet.
func (ip *interp) resetPath() {
	ip.path = pathState{rectsOnly: true}
}

// pathPoint extends the current path's bounding box by one point in user
// space.
//
// Bézier control points are included rather than the curve they describe. That
// makes the box a little larger than the ink, which is the safe direction: a
// box that is too large can only make this package less certain that a fill
// was the background, never more.
func (ip *interp) pathPoint(x, y float64) {
	px, py := ip.gs.ctm.apply(x, y)
	if !ip.path.has {
		ip.path.bbox = rect{px, py, px, py}
		ip.path.has = true
		ip.path.firstX, ip.path.firstY = px, py
		ip.path.lastX, ip.path.lastY = px, py
		return
	}
	// Shoelace: accumulate the cross product of consecutive points. The
	// subpath is treated as closed back to its first point, which is what a
	// fill does regardless of whether the content stream said so.
	ip.path.shoelace += ip.path.lastX*py - px*ip.path.lastY
	ip.path.lastX, ip.path.lastY = px, py
	b := &ip.path.bbox
	if px < b.minX {
		b.minX = px
	}
	if py < b.minY {
		b.minY = py
	}
	if px > b.maxX {
		b.maxX = px
	}
	if py > b.maxY {
		b.maxY = py
	}
}

// pathRect adds a re rectangle.
//
// A rectangle only stays a rectangle under a matrix with no rotation and no
// skew. Under any other it is a parallelogram whose bounding box is bigger
// than it is, so it contributes its corners to the box and forfeits the
// exactness that lets a fill be taken for the background.
func (ip *interp) pathRect(x, y, w, h float64) {
	ip.pathPoint(x, y)
	ip.pathPoint(x+w, y)
	ip.pathPoint(x, y+h)
	ip.pathPoint(x+w, y+h)
	// A quarter turn maps an axis-aligned rectangle onto an axis-aligned
	// rectangle, so requiring b and c to be zero rejects a page that is
	// genuinely covered. Either the diagonal or the anti-diagonal is enough;
	// anything else (a 45-degree rotation, a skew) is not a rectangle any more
	// and its bounding box overstates it.
	m := ip.gs.ctm
	diagonal := m[1] == 0 && m[2] == 0
	quarterTurn := m[0] == 0 && m[3] == 0
	if !diagonal && !quarterTurn {
		ip.path.rectsOnly = false
		return
	}
	ax, ay := ip.gs.ctm.apply(x, y)
	bx, by := ip.gs.ctm.apply(x+w, y+h)
	r := rect{min2(ax, bx), min2(ay, by), max2(ax, bx), max2(ay, by)}
	ip.path.rectArea += r.intersect(ip.page).area()
	if r.covers(ip.page) {
		ip.path.covers = true
	}
}

func min2(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max2(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// pathCoversPage reports whether filling the current path would paint the
// whole page.
func (ip *interp) pathCoversPage() bool {
	p := ip.path
	if !p.has {
		return false
	}
	if p.covers {
		return true
	}
	// A background split into tiles: the box round the lot covers the page
	// and the tiles add up to it. Requiring both is what keeps a diagonal
	// hairline from corner to corner — whose box also covers the page — from
	// being read as paper.
	return p.rectsOnly && p.bbox.covers(ip.page) && p.rectArea >= coverFraction*ip.page.area()
}

// fillPath consumes the current path as a filled area.
//
// Only paint laid down before the first glyph is considered. A generator draws
// the paper and then the ink, in that order, and a fill that arrives after the
// text is not underneath it.
func (ip *interp) fillPath() {
	if ip.chars > 0 || !ip.path.has {
		return
	}
	if ip.pathCoversPage() && ip.gs.clipExact && ip.gs.clip.covers(ip.page) {
		if ip.gs.fillKnown {
			// A later full-page fill replaces an earlier one, and clears the
			// paint that was accumulated over it: it is now underneath.
			ip.bg, ip.bgKnown, ip.painted = ip.gs.fill, true, 0
			return
		}
		// A full-page fill in a colour this package will not convert — a
		// pattern, a Separation — is paper of an unknown colour, which is a
		// different answer from no paper at all.
		ip.bgUnknown = true
		return
	}
	ip.opaquePaint(ip.path.bbox)
}

// opaquePaint records paint whose colour was not taken.
//
// It is what makes a page whose background is an image, a shading or a mosaic
// of small fills report no background rather than the white of bare paper. A
// bounding box is used, which over-states the ink, which errs towards
// answering "unknown" — the answer that suppresses the check rather than the
// one that fires it (docs/adr/0017, "Bad": a false positive costs an
// operator's attention).
func (ip *interp) opaquePaint(r rect) {
	if ip.chars > 0 {
		return
	}
	// Clipping is what the ink can actually reach. A full-page fill clipped to
	// a corner paints the corner, and charging it for the page made this
	// package answer "unknown" for a document with a hundred-point swatch on
	// it.
	visible := r.intersect(ip.gs.clip).intersect(ip.page)
	if visible.area() <= 0 {
		return
	}
	if visible.covers(ip.page) && ip.pathFillsItsBox() {
		ip.bgUnknown = true
		return
	}
	ip.painted += ip.coveredArea(visible)
}

// pathFillsItsBox reports whether the path is dense enough in its own bounding
// box for that box to stand for the ink.
//
// A rectangle fills its box exactly. A diagonal hairline fills almost none of
// it, and treating the box as ink is what made a line across the page read as
// a painted page.
func (ip *interp) pathFillsItsBox() bool {
	box := ip.path.bbox.area()
	if box <= 0 {
		return true // a degenerate box cannot overstate anything
	}
	return ip.coveredArea(ip.path.bbox) >= coverFraction*box
}

// coveredArea is the ink the current path lays down inside r.
//
// For a path made only of rectangles the rectangles' own area is exact. For
// anything else the shoelace area of the outline is used, scaled by how much of
// the bounding box r represents, since the ink is not distributed evenly and no
// cheaper estimate is honest.
func (ip *interp) coveredArea(r rect) float64 {
	if ip.path.rectsOnly && ip.path.rectArea > 0 {
		return min2(ip.path.rectArea, r.area())
	}
	area := ip.path.shoelaceArea()
	if area <= 0 {
		return 0
	}
	box := ip.path.bbox.area()
	if box <= 0 {
		return min2(area, r.area())
	}
	return min2(area*r.area()/box, r.area())
}

// shoelaceArea closes the last subpath and returns the unsigned area.
func (p pathState) shoelaceArea() float64 {
	if !p.has {
		return 0
	}
	s := p.shoelace + p.lastX*p.firstY - p.firstX*p.lastY
	if s < 0 {
		s = -s
	}
	return s / 2
}

// unitSquare is the box an image or a form occupies before its matrix, which
// is where the specification puts every image.
var unitSquare = rect{0, 0, 1, 1}

// ctmBox returns the current transformation matrix applied to a box, as the
// box round the four transformed corners.
func (ip *interp) ctmBox(r rect) rect {
	x0, y0 := ip.gs.ctm.apply(r.minX, r.minY)
	x1, y1 := ip.gs.ctm.apply(r.maxX, r.minY)
	x2, y2 := ip.gs.ctm.apply(r.minX, r.maxY)
	x3, y3 := ip.gs.ctm.apply(r.maxX, r.maxY)
	return rect{
		minX: min4(x0, x1, x2, x3), minY: min4(y0, y1, y2, y3),
		maxX: max4(x0, x1, x2, x3), maxY: max4(y0, y1, y2, y3),
	}
}

// applyClip narrows the clip box by the current path.
//
// The clip is a box round a region that may be any shape, so it is only ever
// an over-estimate of what is still visible. clipExact records whether it is
// exact; a fill is only taken for the background through an exact clip,
// because a fill that covers the page through a clip we have over-estimated
// may be covering a postage stamp.
func (ip *interp) applyClip() {
	if !ip.path.has {
		return
	}
	ip.gs.clip = ip.gs.clip.intersect(ip.path.bbox)
	if !ip.path.rectsOnly {
		ip.gs.clipExact = false
	}
}

// paint handles the path, painting, clipping and shading operators.
//
// It reports whether it recognised the operator, so that the interpreter's own
// switch stays the list of text operators it was and this stays the list of
// the ones that put ink on paper.
func (ip *interp) paint(op operator, ops []Object) bool {
	switch op {
	case "m", "l":
		if len(ops) >= 2 {
			ip.path.rectsOnly = false
			ip.pathPoint(num(ops, 1), num(ops, 0))
		}
	case "c":
		if len(ops) >= 6 {
			ip.path.rectsOnly = false
			ip.pathPoint(num(ops, 5), num(ops, 4))
			ip.pathPoint(num(ops, 3), num(ops, 2))
			ip.pathPoint(num(ops, 1), num(ops, 0))
		}
	case "v", "y":
		if len(ops) >= 4 {
			ip.path.rectsOnly = false
			ip.pathPoint(num(ops, 3), num(ops, 2))
			ip.pathPoint(num(ops, 1), num(ops, 0))
		}
	case "re":
		if len(ops) >= 4 {
			ip.pathRect(num(ops, 3), num(ops, 2), num(ops, 1), num(ops, 0))
		}
	case "h":
		// Closing a subpath adds no point that is not already in the box.
	case "W", "W*":
		ip.pendingClip = true
	case "f", "F", "f*", "B", "B*", "b", "b*":
		ip.fillPath()
		ip.endPath()
	case "S", "s":
		// A stroke paints a line, not an area. A page-wide stroke is a
		// border, and a border is not the paper.
		ip.endPath()
	case "n":
		ip.endPath()
	case "sh":
		// A shading paints the whole clip region in colours that vary across
		// it, so there is no single colour to take. Wherever it lands, the
		// paper under it is not something this package can name.
		ip.opaquePaint(ip.gs.clip)
	default:
		return false
	}
	return true
}

// endPath applies a pending clip and starts a new path, which is what every
// path-painting operator does last.
func (ip *interp) endPath() {
	if ip.pendingClip {
		ip.applyClip()
		ip.pendingClip = false
	}
	ip.resetPath()
}

// background returns the colour of the page's paper, and whether the page said
// enough for that to be known.
//
// Unpainted paper is white — that is the imaging model, not an assumption:
// §8.4.1 of the specification starts every page white and there is no
// transparent paper to print on. What would be an assumption is calling a page
// white without having accounted for what was painted on it, and that is what
// bgUnknown and the painted-area tally are for: a page whose background is an
// image, a shading, a pattern, a Separation ink or a mosaic of fills reports
// no background at all, so the white-on-dark designs that exist in quantity
// are never mistaken for an attack (docs/adr/0017, "Bad").
func (ip *interp) background() (colour, bool) {
	switch {
	case ip.bgKnown:
		return ip.bg, true
	case ip.bgUnknown:
		return colour{}, false
	case ip.page.area() > 0 && ip.painted > paintedFraction*ip.page.area():
		return colour{}, false
	}
	return colour{1, 1, 1}, true
}

// paintedFraction is how much of a page may be painted by things whose colour
// was not taken before the page is no longer treated as bare paper.
//
// Half is deliberately generous downwards: a page with a shaded table, a
// letterhead band and a logo is still white paper, and a page with more paint
// than that is one where guessing costs more than skipping the check.
const paintedFraction = 0.5
