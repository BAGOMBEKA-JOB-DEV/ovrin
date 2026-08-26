package compare_test

import (
	"fmt"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/compare"
)

// ExampleValues is the failure ADR-0014 was written around. Both readings are
// well-formed numbers and both pass every rule ovrin has; only comparing them
// catches it.
func ExampleValues() {
	formatting := compare.Values("25,000", 25000.0, compare.KindNumber)
	fmt.Println("25,000 against 25000:", formatting.Equal)

	misread := compare.Values("25,000", "2,500", compare.KindNumber)
	fmt.Println("25,000 against 2,500: ", misread.Equal, "-", misread.Reason)

	signal, ok := misread.Signal()
	fmt.Println("agreement signal:     ", signal, ok)
	// Output:
	// 25,000 against 25000: true
	// 25,000 against 2,500:  false - the readings produced different values for this field
	// agreement signal:      0 true
}

// ExampleField shows what the pipeline does with two readings of one field:
// take the signal, keep every candidate, and resolve nothing.
func ExampleField() {
	got := compare.Field(compare.KindCurrency, []compare.Candidate{
		{Value: "$25,000.00", Reading: compare.ReadingOCR, Confidence: 0.71},
		{Value: "2500 USD", Reading: compare.ReadingVision, Confidence: 0.93},
	})

	fmt.Println("agree:", got.Agree)
	fmt.Println("best: ", got.Best.Value, "from", got.Best.Reading)
	for _, c := range got.Candidates {
		fmt.Printf("  candidate %-10v %v\n", c.Reading, c.Value)
	}
	// Output:
	// agree: false
	// best:  2500 USD from vision
	//   candidate vision     2500 USD
	//   candidate ocr        $25,000.00
}

// ExampleValues_currency shows the row of the comparison table that needs two
// facts to agree: the same amount in a different currency is a different value.
func ExampleValues_currency() {
	fmt.Println(compare.Equal("$100", "100 USD", compare.KindCurrency))
	fmt.Println(compare.Equal("100 USD", "100 EUR", compare.KindCurrency))
	// Output:
	// true
	// false
}
