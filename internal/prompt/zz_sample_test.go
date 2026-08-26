package prompt

import (
	"fmt"
	"testing"
)

func TestZZSample(t *testing.T) {
	tb := Table{Cells: []Cell{
		{Row: 0, Column: 0, Header: true, Text: "Description"},
		{Row: 0, Column: 1, Header: true, Text: "Quantity"},
		{Row: 0, Column: 2, Header: true, Text: "Unit price"},
		{Row: 1, Column: 0, Text: "A4 Paper"},
		{Row: 1, Column: 1, Text: "40"},
		{Row: 1, Column: 2, Text: "3.50"},
		{Row: 2, Column: 0, ColumnSpan: 2, Text: "Toner | refill"},
		{Row: 2, Column: 2, Text: "74.00"},
	}}
	fmt.Println(pageBody(PageContent{Number: 3, Text: "INVOICE\nAcme Corporation", Tables: []Table{tb}}))
}
