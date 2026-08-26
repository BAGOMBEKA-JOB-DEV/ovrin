package schema

import "time"

// Invoice is the schema the invoices category is extracted against.
//
// It carries three deliberately optional fields — PurchaseOrder, Due and Tax —
// because a corpus with no absent fields cannot measure fabrication: with
// nothing to invent, the rate is 0.00 for the wrong reason.
type Invoice struct {
	Number        string     `ovrin:"invoice number as printed by the vendor\\, not the purchase order number,required"`
	Issued        time.Time  `ovrin:"date the invoice was issued\\, not the date payment is due,required,format=date"`
	Due           *time.Time `ovrin:"date payment is due\\, if one is printed,format=date"`
	PurchaseOrder string     `ovrin:"the buyer's purchase order number\\, if one is printed"`
	Vendor        Party      `ovrin:"the business issuing the invoice"`
	BillTo        Party      `ovrin:"the business being invoiced"`
	Currency      string     `ovrin:"currency code of every amount on the invoice,required,format=currency"`
	Subtotal      float64    `ovrin:"total before tax,min=0"`
	Tax           float64    `ovrin:"tax charged\\, if any is shown separately,min=0"`
	Total         float64    `ovrin:"total amount payable including tax,required,min=0"`
	Items         []LineItem `ovrin:"the line items being invoiced"`
}

// Party is a business named on a document, used for both sides of an invoice.
type Party struct {
	Name    string `ovrin:"the registered trading name\\, not a contact person's name"`
	Address string `ovrin:"the full postal address as printed\\, on one line"`
	TaxID   string `ovrin:"tax identification number\\, if one is printed"`
}

// LineItem is one row of an invoice.
type LineItem struct {
	Description string  `ovrin:"what the line is for\\, as printed"`
	Quantity    float64 `ovrin:"how many units,min=0"`
	UnitPrice   float64 `ovrin:"price of one unit excluding tax,min=0"`
	Amount      float64 `ovrin:"the line total as printed,min=0"`
}
