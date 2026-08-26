package layout

import "sort"

// Order puts a provider's output into the order the rest of ovrin assumes, and
// fills in the boxes a provider left empty.
//
// It exists so that "cells are in reading order" is something an adapter
// achieves by calling one function rather than something each adapter
// reimplements — three implementations of reading order is three orders. It is
// the counterpart of [Layout.Check]: Check says whether the structure is
// coherent, Order says how it is arranged.
//
// The sorts are stable, so a provider that already emits a defensible order
// keeps it wherever this has no opinion. Order mutates the receiver's slices in
// place and does not copy: a Layout is built once by an adapter and handed on,
// and copying every cell to sort it would be the largest allocation in the
// package for no benefit.
func (l *Layout) Order() {
	for i := range l.Tables {
		l.Tables[i].order()
	}
	sort.SliceStable(l.Tables, func(i, j int) bool {
		return before(l.Tables[i].Page, l.Tables[i].Box, l.Tables[j].Page, l.Tables[j].Box)
	})
	sort.SliceStable(l.Pairs, func(i, j int) bool {
		return before(l.Pairs[i].Page, l.Pairs[i].Box(), l.Pairs[j].Page, l.Pairs[j].Box())
	})
}

// order sorts one table's cells and fills its box.
func (t *Table) order() {
	sort.SliceStable(t.Cells, func(i, j int) bool {
		if t.Cells[i].Row != t.Cells[j].Row {
			return t.Cells[i].Row < t.Cells[j].Row
		}
		return t.Cells[i].Column < t.Cells[j].Column
	})
	if t.Box.Zero() {
		// Derived rather than assumed. A provider that reports cell geometry
		// but no table geometry — and they exist — would otherwise produce a
		// table nothing could highlight, and the union of its cells is not a
		// guess: it is exactly the region the cells occupy.
		var box Rect
		for _, c := range t.Cells {
			box = box.Union(c.Box)
		}
		t.Box = box
	}
}

// before is reading order for two page regions: earlier page first, then higher
// on the page, then further left.
//
// It is a simple top-to-bottom, left-to-right rule and deliberately not the
// recursive cut internal/normalise runs over words. A table is already a
// two-dimensional object with its own internal order, so the only question here
// is which of two tables comes first, and a page with two tables side by side is
// rare enough that the extra machinery would cost more than it settles. A
// region with no geometry sorts last, because it has nothing to compare and
// putting it first would move it ahead of tables whose position is known.
func before(pageA int, a Rect, pageB int, b Rect) bool {
	if pageA != pageB {
		return pageA < pageB
	}
	if a.Zero() != b.Zero() {
		return b.Zero()
	}
	if a.Zero() {
		return false
	}
	if a.MinY != b.MinY {
		return a.MinY < b.MinY
	}
	return a.MinX < b.MinX
}
