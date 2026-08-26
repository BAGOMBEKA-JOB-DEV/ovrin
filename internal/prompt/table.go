package prompt

import (
	"sort"
	"strconv"
	"strings"
)

// Table is one table a provider recognised on a page, in the only shape this
// stage needs: a grid of cells, and which of them label the others.
//
// It mirrors the part of the root package's Table that a rendering depends on
// and no more. Page numbers, boxes and confidences are deliberately absent —
// the content marker already states the page, and geometry and confidence are
// grounding and scoring inputs that have no business in a prompt. Content that
// is not here cannot be leaked here, which is the same argument [PageContent]
// makes.
//
// It is declared here rather than imported because the root package imports the
// pipeline, and a package the root imports cannot import the root back.
type Table struct {
	// Cells are the cells the provider read, in any order. Rendering sorts a
	// copy, so a caller's slice is never reordered underneath it.
	//
	// A position no cell covers is a position the provider read nothing at,
	// which is a fact about the document and not a gap to be filled.
	Cells []Cell
}

// Cell is one cell of a [Table].
type Cell struct {
	// Row and Column are 0-based indexes into the table's grid.
	Row    int
	Column int

	// RowSpan and ColumnSpan are how many rows and columns the cell covers,
	// where zero and one both mean one — providers differ on whether they
	// report a span of one, and the root package's Cell normalises it the same
	// way.
	RowSpan    int
	ColumnSpan int

	// Header records that the cell labels other cells rather than carrying a
	// value of its own. It is a bool rather than the root package's CellKind
	// because the only question a rendering asks is whether the first row is a
	// heading row; which kind of header it is changes nothing here.
	Header bool

	// Text is the cell's content as the provider read it. It is untrusted
	// document content and is rendered inside the content markers with the
	// rest of the page.
	Text string
}

// The bounds on a rendered grid.
//
// A table's shape comes from a provider, so it is an untrusted number: a
// response placing a cell at row two billion would otherwise have this stage
// write two billion empty fields into a request. Every limit has a finite
// default, checked before anything is built from it (docs/rules.md §5.2,
// ADR-0020).
//
// The values are far above any real page — a table with more than 256 columns
// or 1024 rows is not something a page holds — so reaching one means the
// response is wrong rather than the document unusual. What is dropped is always
// reported in the block; dropping data without saying so is the one behaviour
// this project does not tolerate (docs/rules.md §6.1).
const (
	maxTableColumns = 256
	maxTableRows    = 1024

	// maxTableCells bounds the grid after both dimensions have been bounded,
	// because a single cell in the far corner of a 256 by 1024 grid would
	// otherwise render a quarter of a million empty fields for one value.
	maxTableCells = 4096
)

// pageBody is the document text of one page, tables included.
//
// It is what goes between the content markers, and it is computed before the
// boundary identifier is drawn so that the identifier can be checked against
// everything the request will carry rather than against the prose alone. A
// table cell that happened to contain the identifier would otherwise slip past
// the check that makes marker forgery impossible.
//
// The tables follow the page's own text rather than replacing it. The words of
// a table are in the text as well, and the duplication is the point: the text
// is what grounding matches a value against, and the grid is what says which
// column the value came from.
func pageBody(p PageContent) string {
	if len(p.Tables) == 0 {
		// The overwhelmingly common case, and it must return the page's text
		// byte for byte: this package does not edit document content.
		return p.Text
	}

	var b strings.Builder
	b.Grow(len(p.Text) + 512)
	b.WriteString(p.Text)
	for i := range p.Tables {
		rendered := renderTable(i, p.Tables[i])
		if rendered == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(rendered)
	}
	return b.String()
}

// renderTable writes one table as a pipe-delimited grid, or returns an empty
// string for a table with nothing in it.
//
// A pipe grid rather than prose because it is the one tabular form every model
// has read a great deal of, and because it puts a value and its column heading
// in the same visual line — which is the whole reason a table is worth carrying
// across the OCR seam. A run of words in reading order says the 40 is on the
// page; a grid says it is the Quantity of the A4 Paper.
//
// The output is a pure function of the table: cells are sorted by position on a
// copy, nothing is read from a map, and no identifier or timestamp appears. Two
// runs over the same recognition produce the same bytes, which is what lets a
// provider cache and a test assert.
func renderTable(index int, t Table) string {
	cells, rows, columns, dropped := grid(t)
	if rows == 0 || columns == 0 {
		// A table the provider declared and read nothing in. There is no grid
		// to draw and an empty one would say the positions were read as blank.
		return ""
	}

	var b strings.Builder
	b.WriteString("[table ")
	b.WriteString(strconv.Itoa(index + 1))
	b.WriteString(": ")
	b.WriteString(strconv.Itoa(rows))
	b.WriteString(" rows, ")
	b.WriteString(strconv.Itoa(columns))
	b.WriteString(" columns]")
	if dropped {
		b.WriteString("\n[table ")
		b.WriteString(strconv.Itoa(index + 1))
		b.WriteString(": cells outside this grid were reported and are not shown]")
	}

	at := byPosition(cells)
	for r := 0; r < rows; r++ {
		b.WriteString("\n|")
		for c := 0; c < columns; c++ {
			b.WriteString(" ")
			if cell, ok := at[position{r, c}]; ok {
				b.WriteString(cellText(cell.Text))
			}
			b.WriteString(" |")
		}
		if r == 0 && headed(cells) {
			// The separator is markdown's way of saying "the row above labels
			// the columns", and it is written only when the provider said so.
			// Writing it unconditionally would claim a first row is a heading
			// on every table whose provider does not classify cells.
			b.WriteString("\n|")
			for c := 0; c < columns; c++ {
				b.WriteString(" --- |")
			}
		}
	}
	return b.String()
}

// position is a grid coordinate, and is the key of the lookup a rendering
// walks. It is a struct rather than a packed integer so that a very wide
// column index cannot collide with a row.
type position struct {
	row, column int
}

// byPosition maps each cell to its own position, and only its own.
//
// A cell spanning three columns appears once, at its origin, and the positions
// it covers are left empty. That is how a merged cell renders in every pipe
// table, and repeating the text into the covered positions would say the
// document stated the value three times.
//
// A position that is empty because a span covers it and one that is empty
// because the provider read nothing there render identically, and deliberately:
// both mean no value is stated at that position, which is exactly what a blank
// field says. Marking one of them would put text in the block that the document
// does not contain.
func byPosition(cells []Cell) map[position]Cell {
	out := make(map[position]Cell, len(cells))
	for _, c := range cells {
		// Later cells do not overwrite earlier ones: the sort is by position,
		// so the first at a position is the one nearest the top left, and a
		// provider reporting two cells in one place has already made a mistake
		// the root package's Layout.Check names.
		if _, seen := out[position{c.Row, c.Column}]; !seen {
			out[position{c.Row, c.Column}] = c
		}
	}
	return out
}

// grid returns the cells to render in position order, the size of the grid that
// holds them, and whether anything was left out.
//
// The size is derived from the cells rather than taken from the provider's
// declared row and column counts. A declared count is an untrusted number that
// would be used to size a loop; the cells are what there is to show, and a
// declared row with nothing in it renders as a blank line that tells a model
// nothing.
func grid(t Table) (cells []Cell, rows, columns int, dropped bool) {
	cells = make([]Cell, 0, len(t.Cells))
	for _, c := range t.Cells {
		if c.Row < 0 || c.Column < 0 || c.Row >= maxTableRows || c.Column >= maxTableColumns {
			dropped = true
			continue
		}
		cells = append(cells, c)
		if end := c.Row + span(c.RowSpan); end > rows {
			rows = end
		}
		if end := c.Column + span(c.ColumnSpan); end > columns {
			columns = end
		}
	}
	if rows > maxTableRows {
		rows, dropped = maxTableRows, true
	}
	if columns > maxTableColumns {
		columns, dropped = maxTableColumns, true
	}
	// Bounded after both dimensions are, so the multiplication cannot overflow
	// into a small positive number and pass the check it exists to fail.
	if columns > 0 && rows*columns > maxTableCells {
		rows, dropped = max1(maxTableCells/columns), true
	}

	sort.SliceStable(cells, func(i, j int) bool {
		if cells[i].Row != cells[j].Row {
			return cells[i].Row < cells[j].Row
		}
		return cells[i].Column < cells[j].Column
	})
	return cells, rows, columns, dropped
}

// headed reports whether any cell in the first row labels the others.
func headed(cells []Cell) bool {
	for _, c := range cells {
		if c.Row > 0 {
			return false // sorted by row: nothing after this is in row 0
		}
		if c.Header {
			return true
		}
	}
	return false
}

// cellText is a cell's own text, fitted into one field of the grid.
//
// Two edits are made and no others, and each is made because not making it
// would misstate the document rather than merely render it awkwardly:
//
//   - Whitespace is collapsed to single spaces. A newline inside a cell would
//     push the rest of that cell onto a line of its own, where it reads as a
//     row it never belonged to. The page text this sits beside has already been
//     whitespace-collapsed by normalisation, so this is the same treatment and
//     not a second convention.
//   - A vertical bar is escaped as markdown escapes it. An unescaped one splits
//     one field into two and shifts every value after it one column left, which
//     is precisely the error a grid exists to prevent: it would attribute a
//     total to a quantity column.
//
// Nothing else is touched. Zero-width characters, direction overrides and
// instruction-shaped text are passed through exactly as the surrounding page
// text is, because stripping them means the operator never learns they are
// under attack (ADR-0017).
func cellText(s string) string {
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	if strings.ContainsRune(s, '|') {
		s = strings.ReplaceAll(s, "|", `\|`)
	}
	return s
}

// span normalises a reported span, where zero and one both mean one.
func span(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// max1 keeps a derived bound from collapsing to nothing.
func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
