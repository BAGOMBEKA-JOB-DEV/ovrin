package schema

import "time"

// Statement is the schema the statements category is extracted against.
//
// This is the category where the slice matters. A statement's value is its
// transaction list, and the failure worth measuring is an extractor that drops
// a row, duplicates one, or reads a credit as a debit — none of which shows up
// in a header field.
type Statement struct {
	Account      string        `ovrin:"the account number as printed on the statement,required"`
	AccountName  string        `ovrin:"the name the account is held in,required"`
	Bank         string        `ovrin:"the name of the bank or institution,required"`
	Currency     string        `ovrin:"currency code of the balances and transactions,required,format=currency"`
	PeriodStart  time.Time     `ovrin:"the first day of the statement period,required,format=date"`
	PeriodEnd    time.Time     `ovrin:"the last day of the statement period,required,format=date"`
	Opening      float64       `ovrin:"the opening balance at the start of the period"`
	Closing      float64       `ovrin:"the closing balance at the end of the period,required"`
	IBAN         string        `ovrin:"the IBAN\\, if the statement prints one,format=iban"`
	Transactions []Transaction `ovrin:"every transaction line on the statement\\, in the order printed"`
}

// Transaction is one line of a statement.
//
// Debit and Credit are separate fields rather than one signed amount because
// that is how the documents are printed, and asking a model to apply the sign
// itself moves an arithmetic decision into the part of the system that cannot
// be checked.
type Transaction struct {
	Date        time.Time `ovrin:"the date of the transaction,format=date"`
	Description string    `ovrin:"the narrative as printed"`
	Debit       float64   `ovrin:"the amount taken out\\, if this line is a debit,min=0"`
	Credit      float64   `ovrin:"the amount paid in\\, if this line is a credit,min=0"`
	Balance     float64   `ovrin:"the running balance printed on this line"`
}
