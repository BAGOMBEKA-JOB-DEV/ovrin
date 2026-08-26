package normalise

import "sort"

// line is a run of words sharing a baseline, in left-to-right order.
type line struct {
	words []int // indexes into Page.Words
	box   Rect
}

// block is a run of lines that reading-order analysis decided belong
// together. Blocks are separated by a blank line in the output, and a
// hyphenated word is only rejoined across a line break inside one — which is
// why splitting too eagerly costs more than splitting too little.
type block struct {
	lines []line
}

func hasGeometry(ws []Word) bool {
	for _, w := range ws {
		if !w.Box.Zero() {
			return true
		}
	}
	return false
}

func allHinted(ws []Word, idx []int) bool {
	for _, i := range idx {
		if ws[i].Line < 0 {
			return false
		}
	}
	return true
}

// blocksOf returns a page's words in reading order, grouped into lines and
// blocks.
//
// The algorithm is a recursive XY-cut over word boxes: split on horizontal
// whitespace bands, then on vertical gutters, and recurse. It is the classic
// answer, it is a heuristic and not a parse, and it is what stops a
// two-column page from being read straight across the gutter into interleaved
// nonsense.
//
// The cut runs over words rather than over lines because a line is not
// knowable before the columns are: group first and the top line of the left
// column and the top line of the right column become one line, after which no
// gutter is visible to anything.
//
// With no geometry there is nothing to cut on, and the reading's own order is
// the only order there is.
func blocksOf(p *Page, o options) []block {
	if len(p.Words) == 0 {
		return nil
	}
	idx := make([]int, len(p.Words))
	for i := range p.Words {
		idx[i] = i
	}
	if !hasGeometry(p.Words) {
		return []block{{lines: linesIn(p, idx)}}
	}

	med := medianHeight(p)
	rowGap := o.leadingRatio * med
	colGap := o.gutterRatio * p.Width
	if floor := 1.2 * med; colGap < floor {
		colGap = floor
	}

	var leaves [][]int
	cut(p, idx, rowGap, colGap, o.maxCutDepth, &leaves)

	out := make([]block, 0, len(leaves))
	for _, leaf := range leaves {
		if ls := linesIn(p, leaf); len(ls) > 0 {
			out = append(out, block{lines: ls})
		}
	}
	return out
}

func cut(p *Page, idx []int, rowGap, colGap float64, depth int, out *[][]int) {
	if len(idx) <= 1 || depth <= 0 {
		*out = append(*out, idx)
		return
	}
	if rows := horizontalCut(p, idx, rowGap); len(rows) > 1 {
		for _, r := range rows {
			cut(p, r, rowGap, colGap, depth-1, out)
		}
		return
	}
	// A gutter is a property of a stack of lines, not of one line. Cutting a
	// single line vertically turns a wide gap between two words — a label and
	// its value, a figure right-aligned in a table — into two columns, and
	// then reads them as if they were paragraphs.
	bands := linesIn(p, idx)
	if len(bands) < 2 {
		*out = append(*out, idx)
		return
	}
	if regions, ok := splitAtStraddlers(p, idx, bands, colGap); ok {
		for _, b := range regions {
			cut(p, b, rowGap, colGap, depth-1, out)
		}
		return
	}
	if cols := verticalCut(p, idx, colGap); len(cols) > 1 {
		for _, c := range cols {
			cut(p, c, rowGap, colGap, depth-1, out)
		}
		return
	}
	*out = append(*out, idx)
}

// horizontalCut splits on whitespace bands running the width of the region.
func horizontalCut(p *Page, idx []int, gap float64) [][]int {
	if gap <= 0 || len(idx) == 0 {
		return [][]int{idx}
	}
	sorted := byTopLeft(p, idx)
	var out [][]int
	cur := []int{sorted[0]}
	bottom := p.Words[sorted[0]].Box.MaxY
	for _, i := range sorted[1:] {
		b := p.Words[i].Box
		if b.MinY-bottom >= gap {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, i)
		if b.MaxY > bottom {
			bottom = b.MaxY
		}
	}
	return append(out, cur)
}

// verticalCut splits on gutters: x intervals of at least gap that no word
// crosses.
func verticalCut(p *Page, idx []int, gap float64) [][]int {
	bands := gutters(p, idx, gap)
	if len(bands) <= 1 {
		return [][]int{idx}
	}
	out := make([][]int, len(bands))
	for _, i := range idx {
		x := p.Words[i].Box.MinX
		for j, b := range bands {
			if x >= b[0] && x <= b[1] {
				out[j] = append(out[j], i)
				break
			}
		}
	}
	trimmed := out[:0]
	for _, c := range out {
		if len(c) > 0 {
			trimmed = append(trimmed, c)
		}
	}
	return trimmed
}

// gutters merges the x extents of a region's words and returns the merged
// intervals, treating a run of at least gap points that nothing crosses as a
// boundary.
func gutters(p *Page, idx []int, gap float64) [][2]float64 {
	if len(idx) == 0 {
		return nil
	}
	xs := make([][2]float64, 0, len(idx))
	for _, i := range idx {
		b := p.Words[i].Box
		xs = append(xs, [2]float64{b.MinX, b.MaxX})
	}
	sort.Slice(xs, func(a, b int) bool { return xs[a][0] < xs[b][0] })
	out := [][2]float64{xs[0]}
	for _, x := range xs[1:] {
		last := &out[len(out)-1]
		if x[0]-last[1] >= gap {
			out = append(out, x)
			continue
		}
		if x[1] > last[1] {
			last[1] = x[1]
		}
	}
	return out
}

// splitAtStraddlers isolates the bands of text that span a region — a
// full-width heading sitting straight on top of two columns, with no
// whitespace under it — into regions of their own.
//
// Only when doing so reveals a gutter those bands were hiding. Without that
// guard a single-column page of justified text is cut at every line, which is
// worse than the problem being solved: blocks bound hyphenation repair, and a
// block per line repairs nothing.
func splitAtStraddlers(p *Page, idx []int, bands []line, colGap float64) ([][]int, bool) {
	var minX, maxX float64
	for n, i := range idx {
		b := p.Words[i].Box
		if n == 0 || b.MinX < minX {
			minX = b.MinX
		}
		if n == 0 || b.MaxX > maxX {
			maxX = b.MaxX
		}
	}
	width := maxX - minX
	if width <= 0 {
		return nil, false
	}

	wide := make([]bool, len(bands))
	var narrow []int
	straddles := false
	for j, b := range bands {
		if b.box.width() >= 0.8*width {
			wide[j] = true
			straddles = true
			continue
		}
		narrow = append(narrow, b.words...)
	}
	if !straddles || len(narrow) == 0 {
		return nil, false
	}
	if len(verticalCut(p, narrow, colGap)) <= 1 {
		return nil, false
	}

	var out [][]int
	var cur []int
	for j, b := range bands {
		if wide[j] {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			out = append(out, b.words)
			continue
		}
		cur = append(cur, b.words...)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	if len(out) <= 1 {
		return nil, false
	}
	return out, true
}

// linesIn groups a region's words into lines, in reading order.
//
// A reading's own line grouping is trusted whenever every word in the region
// carries one: the reading saw baselines, font sizes and text-showing order,
// and this package sees rectangles. Geometry is the fallback, and with
// neither the region is one line in the order the reading produced it — which
// is the honest answer for a source that gave no positions at all.
func linesIn(p *Page, idx []int) []line {
	if len(idx) == 0 {
		return nil
	}
	var out []line
	switch {
	case allHinted(p.Words, idx):
		order := make([]int, 0, 8)
		byLine := make(map[int][]int, 8)
		for _, i := range idx {
			k := p.Words[i].Line
			if _, ok := byLine[k]; !ok {
				order = append(order, k)
			}
			byLine[k] = append(byLine[k], i)
		}
		for _, k := range order {
			out = append(out, newLine(p, byLine[k]))
		}
	case !hasGeometryIn(p, idx):
		out = []line{newLine(p, idx)}
	default:
		for _, i := range byTopLeft(p, idx) {
			b := p.Words[i].Box
			placed := false
			for j := range out {
				if overlapsVertically(out[j].box, b) {
					out[j].words = append(out[j].words, i)
					out[j].box = out[j].box.Union(b)
					placed = true
					break
				}
			}
			if !placed {
				out = append(out, line{words: []int{i}, box: b})
			}
		}
	}

	for j := range out {
		sortAcross(p, out[j].words)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].box.Zero() || out[b].box.Zero() {
			return false
		}
		if out[a].box.MinY != out[b].box.MinY {
			return out[a].box.MinY < out[b].box.MinY
		}
		return out[a].box.MinX < out[b].box.MinX
	})
	return out
}

func newLine(p *Page, idx []int) line {
	l := line{words: idx}
	for _, i := range idx {
		l.box = l.box.Union(p.Words[i].Box)
	}
	return l
}

func hasGeometryIn(p *Page, idx []int) bool {
	for _, i := range idx {
		if !p.Words[i].Box.Zero() {
			return true
		}
	}
	return false
}

// overlapsVertically reports whether two boxes share more than half the
// height of the shorter of them, which is the test for being on one line.
// Half is a judgement: a superscript overlaps its own line by less than that
// and the line below by less still.
func overlapsVertically(a, b Rect) bool {
	if a.Zero() || b.Zero() {
		return false
	}
	lo, hi := a.MinY, a.MaxY
	if b.MinY > lo {
		lo = b.MinY
	}
	if b.MaxY < hi {
		hi = b.MaxY
	}
	shared := hi - lo
	if shared <= 0 {
		return false
	}
	shorter := a.height()
	if b.height() < shorter {
		shorter = b.height()
	}
	if shorter <= 0 {
		return true
	}
	return shared/shorter > 0.5
}

// byTopLeft returns idx ordered by the top then the left edge of each word.
func byTopLeft(p *Page, idx []int) []int {
	out := make([]int, len(idx))
	copy(out, idx)
	sort.SliceStable(out, func(a, b int) bool {
		ba, bb := p.Words[out[a]].Box, p.Words[out[b]].Box
		if ba.MinY != bb.MinY {
			return ba.MinY < bb.MinY
		}
		return ba.MinX < bb.MinX
	})
	return out
}

// sortAcross puts a line's words in left-to-right order, keeping the
// reading's own order where there is no geometry to sort by.
func sortAcross(p *Page, idx []int) {
	sort.SliceStable(idx, func(a, b int) bool {
		ba, bb := p.Words[idx[a]].Box, p.Words[idx[b]].Box
		if ba.Zero() || bb.Zero() {
			return false
		}
		return ba.MinX < bb.MinX
	})
}

// medianHeight is the median height of a page's word boxes, and is the unit
// every threshold in reading-order analysis is expressed in. Absolute point
// values would be wrong for a page typeset at eight points and wrong again
// for one at twenty-four.
func medianHeight(p *Page) float64 {
	hs := make([]float64, 0, len(p.Words))
	for _, w := range p.Words {
		if h := w.Box.height(); h > 0 {
			hs = append(hs, h)
		}
	}
	if len(hs) == 0 {
		return 10
	}
	sort.Float64s(hs)
	return hs[len(hs)/2]
}
