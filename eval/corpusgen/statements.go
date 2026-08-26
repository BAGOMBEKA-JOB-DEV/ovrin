package main

// statements returns the statements category.
//
// This is the category where the slice matters. A statement's value is its
// transaction list, and the failures worth measuring — a dropped row, a
// duplicated row, a credit read as a debit — do not show up in any header
// field.
//
// Debit and credit are separate fields on every line and only one of them is
// ever filled in, so each transaction carries a guaranteed absence. A reader
// that writes 0 into the empty one has fabricated a value that validates
// perfectly.
func statements() []document {
	return []document{
		{
			Category:   "statements",
			Name:       "001",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       4001,
			Notes: `A digital original with a real text layer. Four transactions, each with
exactly one of debit and credit filled in, and a running balance that
reconciles from the opening balance to the closing one.

No IBAN — this is a domestic account and none is printed.`,
			Body: []string{
				"@H Victoria Commercial Bank",
				"@B Statement of account",
				"@R",
				"Account name     Ocen Grain Millers Ltd",
				"Account number   0142200987651",
				"Currency         UGX",
				"Period           01 Feb 2026 to 28 Feb 2026",
				"Opening balance      4,120,000",
				"Closing balance      4,090,000",
				"@R",
				"Date         Description                     Debit       Credit      Balance",
				"04 Feb 2026  Deposit - maize sales                      890,000    5,010,000",
				"09 Feb 2026  Cheque 004412 - fuel          310,000                 4,700,000",
				"17 Feb 2026  Transfer to payroll         1,250,000                 3,450,000",
				"25 Feb 2026  Deposit - flour sales                      640,000    4,090,000",
			},
			Expected: `{
  "account": "0142200987651",
  "account_name": "Ocen Grain Millers Ltd",
  "bank": "Victoria Commercial Bank",
  "currency": "UGX",
  "period_start": "2026-02-01",
  "period_end": "2026-02-28",
  "opening": 4120000,
  "closing": 4090000,
  "transactions": [
    {"date": "2026-02-04", "description": "Deposit - maize sales", "credit": 890000, "balance": 5010000},
    {"date": "2026-02-09", "description": "Cheque 004412 - fuel", "debit": 310000, "balance": 4700000},
    {"date": "2026-02-17", "description": "Transfer to payroll", "debit": 1250000, "balance": 3450000},
    {"date": "2026-02-25", "description": "Deposit - flour sales", "credit": 640000, "balance": 4090000}
  ]
}`,
		},
		{
			Category:   "statements",
			Name:       "002",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       4002,
			Notes: `A digital original in EUR with an IBAN, which exercises format=iban and
the decimal amounts a two-decimal currency brings. The IBAN printed is the
example from the ISO 13616 registry documentation, chosen because it is
the one IBAN in the world that is certainly not anybody's account; it is
recorded here without spaces because that is the normalised form.

Amounts carry decimal places, so this is the document that would catch a
comparison doing its arithmetic in binary floating point.`,
			Body: []string{
				"@H Rhein Handelsbank AG",
				"@B Account statement",
				"@R",
				"Account holder   Vollmer Werkzeuge GmbH",
				"Account number   0532013000",
				"IBAN             DE89 3704 0044 0532 0130 00",
				"Currency         EUR",
				"Period           01 Mar 2026 to 31 Mar 2026",
				"Opening balance      18,430.55",
				"Closing balance      21,905.05",
				"@R",
				"Date         Description                     Debit       Credit      Balance",
				"03 Mar 2026  SEPA credit - Kessler KG                  4,200.00    22,630.55",
				"11 Mar 2026  Card purchase - fuel           185.50                 22,445.05",
				"22 Mar 2026  SEPA debit - insurance         540.00                 21,905.05",
			},
			Expected: `{
  "account": "0532013000",
  "account_name": "Vollmer Werkzeuge GmbH",
  "bank": "Rhein Handelsbank AG",
  "currency": "EUR",
  "period_start": "2026-03-01",
  "period_end": "2026-03-31",
  "opening": 18430.55,
  "closing": 21905.05,
  "iban": "DE89370400440532013000",
  "transactions": [
    {"date": "2026-03-03", "description": "SEPA credit - Kessler KG", "credit": 4200.00, "balance": 22630.55},
    {"date": "2026-03-11", "description": "Card purchase - fuel", "debit": 185.50, "balance": 22445.05},
    {"date": "2026-03-22", "description": "SEPA debit - insurance", "debit": 540.00, "balance": 21905.05}
  ]
}`,
		},
		{
			Category:   "statements",
			Name:       "003",
			Recipe:     goodScan(),
			Difficulty: "good-scan",
			Seed:       4003,
			Notes: `Office flatbed: rotated 0.7 degrees, one blur pass, noise at sigma 1.2,
sixty specks. Two
transactions, one of each sign.`,
			Body: []string{
				"@H PEARL SAVINGS AND CREDIT BANK",
				"@B STATEMENT OF ACCOUNT",
				"@R",
				"ACCOUNT NAME     KAZO DAIRY COOPERATIVE",
				"ACCOUNT NUMBER   3300114552",
				"CURRENCY         UGX",
				"PERIOD           01 MAR 2026 TO 31 MAR 2026",
				"OPENING BALANCE      760,000",
				"CLOSING BALANCE    1,145,000",
				"@R",
				"DATE         DESCRIPTION                 DEBIT      CREDIT     BALANCE",
				"06 MAR 2026  MILK DELIVERY PAYMENT                 520,000   1,280,000",
				"14 MAR 2026  VET SUPPLIES              135,000               1,145,000",
			},
			Expected: `{
  "account": "3300114552",
  "account_name": "KAZO DAIRY COOPERATIVE",
  "bank": "PEARL SAVINGS AND CREDIT BANK",
  "currency": "UGX",
  "period_start": "2026-03-01",
  "period_end": "2026-03-31",
  "opening": 760000,
  "closing": 1145000,
  "transactions": [
    {"date": "2026-03-06", "description": "MILK DELIVERY PAYMENT", "credit": 520000, "balance": 1280000},
    {"date": "2026-03-14", "description": "VET SUPPLIES", "debit": 135000, "balance": 1145000}
  ]
}`,
		},
		{
			Category:   "statements",
			Name:       "004",
			Recipe:     poorScan(),
			Difficulty: "poor-scan",
			Seed:       4004,
			Notes: `The machine in the corridor: skewed 1.8 degrees, contrast at 0.58, one
blur pass, noise at sigma 9, four hundred and twenty specks, JPEG quality
34, at half the scale of a good scan.

Four transactions in a table whose columns are close together, which is
what makes a debit read as a credit here rather than merely misread.`,
			Body: []string{
				"@H ALBERTINE MICROFINANCE LTD",
				"@B STATEMENT OF ACCOUNT",
				"@R",
				"ACCOUNT NAME     RWENZORI COFFEE HULLERS",
				"ACCOUNT NUMBER   7781200034",
				"CURRENCY         UGX",
				"PERIOD           01 APR 2026 TO 30 APR 2026",
				"OPENING BALANCE    2,050,000",
				"CLOSING BALANCE    1,394,500",
				"@R",
				"DATE         DESCRIPTION                 DEBIT      CREDIT     BALANCE",
				"02 APR 2026  LOAN REPAYMENT            420,000               1,630,000",
				"09 APR 2026  COFFEE SALE DEPOSIT                   915,000   2,545,000",
				"18 APR 2026  ELECTRICITY BILL          214,500               2,330,500",
				"26 APR 2026  WAGES                     936,000               1,394,500",
			},
			Expected: `{
  "account": "7781200034",
  "account_name": "RWENZORI COFFEE HULLERS",
  "bank": "ALBERTINE MICROFINANCE LTD",
  "currency": "UGX",
  "period_start": "2026-04-01",
  "period_end": "2026-04-30",
  "opening": 2050000,
  "closing": 1394500,
  "transactions": [
    {"date": "2026-04-02", "description": "LOAN REPAYMENT", "debit": 420000, "balance": 1630000},
    {"date": "2026-04-09", "description": "COFFEE SALE DEPOSIT", "credit": 915000, "balance": 2545000},
    {"date": "2026-04-18", "description": "ELECTRICITY BILL", "debit": 214500, "balance": 2330500},
    {"date": "2026-04-26", "description": "WAGES", "debit": 936000, "balance": 1394500}
  ]
}`,
		},
		{
			Category:   "statements",
			Name:       "005",
			Recipe:     photograph(),
			Difficulty: "photograph",
			Seed:       4005,
			Notes: `Photographed on a desk: keystoned 7 per cent, rotated 1.4 degrees, one
corner shadowed, warm white balance, downsampled by 1.5, one blur pass,
JPEG quality 46. Two transactions.`,
			Body: []string{
				"@H LAKESIDE COMMUNITY BANK",
				"@B STATEMENT OF ACCOUNT",
				"@R",
				"ACCOUNT NAME     ACHOLI HERITAGE TRUST",
				"ACCOUNT NUMBER   5520019987",
				"CURRENCY         UGX",
				"PERIOD           01 MAY 2026 TO 31 MAY 2026",
				"OPENING BALANCE      318,000",
				"CLOSING BALANCE      742,000",
				"@R",
				"DATE         DESCRIPTION                 DEBIT      CREDIT     BALANCE",
				"07 MAY 2026  GRANT RECEIPT                        600,000      918,000",
				"19 MAY 2026  PRINTING SERVICES         176,000                 742,000",
			},
			Expected: `{
  "account": "5520019987",
  "account_name": "ACHOLI HERITAGE TRUST",
  "bank": "LAKESIDE COMMUNITY BANK",
  "currency": "UGX",
  "period_start": "2026-05-01",
  "period_end": "2026-05-31",
  "opening": 318000,
  "closing": 742000,
  "transactions": [
    {"date": "2026-05-07", "description": "GRANT RECEIPT", "credit": 600000, "balance": 918000},
    {"date": "2026-05-19", "description": "PRINTING SERVICES", "debit": 176000, "balance": 742000}
  ]
}`,
		},
	}
}
