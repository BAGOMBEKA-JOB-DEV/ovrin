package schema

import "time"

// Receipt is the schema the receipts category is extracted against.
//
// Deliberately flatter than [Invoice]. A receipt is a small document read
// under bad conditions, so the interesting failure is misreading a number
// rather than mis-navigating a structure, and a schema with fewer fields makes
// that the thing being measured.
type Receipt struct {
	Number   string    `ovrin:"receipt or transaction number as printed,required"`
	Vendor   string    `ovrin:"the trading name of the shop,required"`
	Issued   time.Time `ovrin:"date of the sale,required,format=date"`
	Currency string    `ovrin:"currency code of the amounts,required,format=currency"`
	Subtotal float64   `ovrin:"total before tax,min=0"`
	Tax      float64   `ovrin:"tax charged\\, if it is shown separately,min=0"`
	Total    float64   `ovrin:"total paid including tax,required,min=0"`
	Method   string    `ovrin:"how it was paid,enum=cash|card|mobile-money|cheque|transfer"`
	Cashier  string    `ovrin:"the name or code of the cashier\\, if one is printed"`
}
