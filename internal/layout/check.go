package layout

import (
	"errors"
	"fmt"
)

// The ways a layout can be structurally incoherent.
//
// They exist so that an adapter's contract test can assert a specific fault
// rather than the presence of some error, and so that nothing branches on
// message text (docs/rules.md §2.2). They are unprefixed and untyped for the
// same reason internal/prompt's are: the root package classifies them onto its
// own sentinels and attaches the stage.
var (
	// ErrRange means a cell falls outside the table's declared size, or a
	// table declares a negative size or a page below 1.
	ErrRange = errors.New("cell outside the table's declared size")

	// ErrOverlap means two cells cover the same grid position. It is the
	// mistake a span-flattening adapter makes, and it matters because
	// [Table.At] would then return whichever cell happened to be stored first.
	ErrOverlap = errors.New("two cells cover the same position")

	// ErrConfidence means a confidence is outside 0..1 — almost always a
	// provider reporting a percentage that nobody divided by a hundred. It is
	// checked here because a confidence of 87 would otherwise pass silently
	// into a score and make every other signal irrelevant.
	ErrConfidence = errors.New("confidence outside 0..1")
)

// maxGrid bounds the grid a check will materialise.
//
// The overlap check needs to know which positions are taken, and a hostile or
// broken provider response declaring a table of two billion rows would
// otherwise allocate for it. Above the bound the overlap check is skipped and
// the size itself is reported instead, so the response is refused rather than
// half-checked. Every limit has a finite default, checked before allocation
// (docs/rules.md §5.2, ADR-0020).
const maxGrid = 1 << 20

// Check reports the first structural mistake in the layout, or nil.
//
// It is what an adapter's contract test runs, and it is the reason those tests
// can be shared: "the cells are inside the table, nothing overlaps, and every
// confidence is a probability" is the same requirement whichever provider
// produced them.
//
// The errors name page, table and cell indexes and nothing else. A cell's text
// is document content and never appears in one (docs/rules.md §2.5).
func (l Layout) Check() error {
	for i, t := range l.Tables {
		if err := t.check(); err != nil {
			return fmt.Errorf("table %d: %w", i, err)
		}
	}
	for i, p := range l.Pairs {
		if p.Page < 1 {
			return fmt.Errorf("pair %d: %w: page %d is not 1-based", i, ErrRange, p.Page)
		}
		for _, c := range []struct {
			what string
			conf float64
		}{{"key", p.Key.Confidence}, {"value", p.Value.Confidence}, {"pair", p.Confidence}} {
			if err := confidence(c.conf); err != nil {
				return fmt.Errorf("pair %d %s: %w", i, c.what, err)
			}
		}
	}
	return nil
}

// check reports the first structural mistake in one table.
func (t Table) check() error {
	if t.Page < 1 {
		return fmt.Errorf("%w: page %d is not 1-based", ErrRange, t.Page)
	}
	if t.Rows < 0 || t.Columns < 0 {
		return fmt.Errorf("%w: %d rows by %d columns", ErrRange, t.Rows, t.Columns)
	}
	if err := confidence(t.Confidence); err != nil {
		return err
	}

	for i, c := range t.Cells {
		if c.Row < 0 || c.Column < 0 || c.RowSpan < 0 || c.ColumnSpan < 0 {
			return fmt.Errorf("cell %d: %w: negative row, column or span", i, ErrRange)
		}
		if c.Row+c.Rows() > t.Rows || c.Column+c.Columns() > t.Columns {
			return fmt.Errorf("cell %d: %w: rows %d..%d and columns %d..%d in a table of %d by %d",
				i, ErrRange, c.Row, c.Row+c.Rows()-1, c.Column, c.Column+c.Columns()-1, t.Rows, t.Columns)
		}
		if err := confidence(c.Confidence); err != nil {
			return fmt.Errorf("cell %d: %w", i, err)
		}
	}

	// Each dimension is bounded before they are multiplied: two large ints
	// multiplied overflow into a small positive one, and the bound would then
	// pass on a table that is exactly what it exists to refuse.
	if t.Rows > maxGrid || t.Columns > maxGrid || t.Rows*t.Columns > maxGrid {
		return fmt.Errorf("%w: %d cells declared, limit %d", ErrRange, t.Rows*t.Columns, maxGrid)
	}
	// Allocated only after the size has been checked against the bound, never
	// before (docs/rules.md §5.2).
	taken := make([]int, t.Rows*t.Columns)
	for i := range taken {
		taken[i] = -1
	}
	for i, c := range t.Cells {
		for r := c.Row; r < c.Row+c.Rows(); r++ {
			for col := c.Column; col < c.Column+c.Columns(); col++ {
				at := r*t.Columns + col
				if taken[at] >= 0 {
					return fmt.Errorf("cells %d and %d: %w: row %d, column %d",
						taken[at], i, ErrOverlap, r, col)
				}
				taken[at] = i
			}
		}
	}
	return nil
}

// confidence reports whether a value is a probability.
//
// NaN fails, which the comparison gives for free: a NaN confidence is neither
// below zero nor above one, so it is caught by requiring that it be within
// range rather than by rejecting what is outside it.
func confidence(v float64) error {
	if v >= 0 && v <= 1 {
		return nil
	}
	return fmt.Errorf("%w: %g", ErrConfidence, v)
}
