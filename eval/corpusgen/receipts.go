package main

// receipts returns the receipts category.
//
// Deliberately flatter than the invoices. A receipt is a small document read
// under bad conditions, so the failure worth measuring here is misreading a
// number rather than mis-navigating a structure, and this category carries the
// worst image quality in the corpus.
func receipts() []document {
	return []document{
		{
			Category:   "receipts",
			Name:       "001",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       2001,
			Notes: `A digital original with a real text layer. The payment method is printed
"Mobile money" and the schema constrains it to mobile-money, so ground
truth records the constrained value: the enum is part of what the field
means, not a formatting preference.`,
			Body: []string{
				"@H Kabalagala Fresh Market",
				"Plot 6 Ggaba Road, Kampala",
				"@R",
				"@B SALES RECEIPT",
				"",
				"Receipt no      KFM-2026-114552",
				"Date            18 Mar 2026",
				"Cashier         T. Nabirye",
				"@R",
				"Item                                  Qty        Amount",
				"Maize flour 5kg                         2        18,000",
				"Cooking oil 3L                          1        27,500",
				"Sugar 2kg                               3        21,000",
				"@R",
				"Subtotal                                         66,500",
				"VAT 18%                                          11,970",
				"Total UGX                                        78,470",
				"",
				"Paid by         Mobile money",
			},
			Expected: `{
  "number": "KFM-2026-114552",
  "vendor": "Kabalagala Fresh Market",
  "issued": "2026-03-18",
  "currency": "UGX",
  "subtotal": 66500,
  "tax": 11970,
  "total": 78470,
  "method": "mobile-money",
  "cashier": "T. Nabirye"
}`,
		},
		{
			Category:   "receipts",
			Name:       "002",
			Recipe:     goodScan(),
			Difficulty: "good-scan",
			Seed:       2002,
			Notes: `Office flatbed: rotated 0.7 degrees, one blur pass, sensor noise at sigma
2.2, sixty dust specks.

No cashier, no subtotal and no separate tax line, so three fields are
absent. The quantity is fractional — 32.50 litres — which is the kind of
number a reader primed on whole units gets wrong.`,
			Body: []string{
				"@H JINJA ROAD FUEL STATION",
				"KAMPALA",
				"@R",
				"RECEIPT NO   JRF-889201",
				"DATE         27 MAR 2026",
				"PUMP         3",
				"@R",
				"PETROL 32.50 L @ 5,290           171,925",
				"@R",
				"TOTAL UGX                        171,925",
				"",
				"PAID BY      CARD",
			},
			Expected: `{
  "number": "JRF-889201",
  "vendor": "JINJA ROAD FUEL STATION",
  "issued": "2026-03-27",
  "currency": "UGX",
  "total": 171925,
  "method": "card"
}`,
		},
		{
			Category:   "receipts",
			Name:       "003",
			Recipe:     poorScan(),
			Difficulty: "poor-scan",
			Seed:       2003,
			Notes: `The machine in the corridor: skewed 1.8 degrees, contrast at 0.58, one
blur pass, noise at sigma 9, four hundred and twenty specks, JPEG quality
34, rendered at half the scale of a good scan.`,
			Body: []string{
				"@H MASAKA GENERAL STORE",
				"PLOT 11 BROADWAY, MASAKA",
				"@R",
				"RECEIPT NO   MGS-0044821",
				"DATE         05 FEB 2026",
				"CASHIER      R. SSEMPA",
				"@R",
				"SOAP BAR 800G          4        12,800",
				"RICE 10KG              1        46,000",
				"SALT 1KG               6         4,800",
				"TEA LEAVES 500G        2        17,400",
				"@R",
				"SUBTOTAL                        81,000",
				"VAT 18%                         14,580",
				"TOTAL UGX                       95,580",
				"",
				"PAID BY      CASH",
			},
			Expected: `{
  "number": "MGS-0044821",
  "vendor": "MASAKA GENERAL STORE",
  "issued": "2026-02-05",
  "currency": "UGX",
  "subtotal": 81000,
  "tax": 14580,
  "total": 95580,
  "method": "cash",
  "cashier": "R. SSEMPA"
}`,
		},
		{
			Category:   "receipts",
			Name:       "004",
			Recipe:     fadedThermal(),
			Difficulty: "poor-scan",
			Seed:       2004,
			Notes: `A till receipt that spent a fortnight in a wallet: thermal print faded to
grey ink on darkened stock, contrast at 0.45 with the black point lifted
24 levels, two blur passes, skewed 1.3 degrees, JPEG quality 40.

The document shows the total, the cash tendered and the change. Only the
total is the total, and the 40,000 tendered is the trap: it is the largest
number on the receipt.`,
			Body: []string{
				"@H NTINDA MINI MART",
				"@R",
				"RECEIPT NO   2026-0210-7731",
				"DATE         10 FEB 2026",
				"@R",
				"BREAD LOAF             2         7,000",
				"MILK 1L                3        10,500",
				"EGGS TRAY              1        14,000",
				"@R",
				"TOTAL UGX                       31,500",
				"CASH                            40,000",
				"CHANGE                           8,500",
			},
			Expected: `{
  "number": "2026-0210-7731",
  "vendor": "NTINDA MINI MART",
  "issued": "2026-02-10",
  "currency": "UGX",
  "total": 31500,
  "method": "cash"
}`,
		},
		{
			Category:   "receipts",
			Name:       "005",
			Recipe:     photograph(),
			Difficulty: "photograph",
			Seed:       2005,
			Notes: `Photographed on a desk: keystoned 7 per cent, rotated 1.4 degrees, one
corner shadowed, warm white balance, downsampled by 1.5, one blur pass,
JPEG quality 46.`,
			Body: []string{
				"@H ENTEBBE ROAD PHARMACY",
				"PLOT 40 ENTEBBE ROAD, KAMPALA",
				"@R",
				"RECEIPT NO   ERP-55219",
				"DATE         14 APR 2026",
				"CASHIER      A. OKELLO",
				"@R",
				"PARACETAMOL 500MG X20    2         9,000",
				"ORS SACHET               5         7,500",
				"BANDAGE 5CM              1         6,200",
				"@R",
				"SUBTOTAL                          22,700",
				"VAT 18%                            4,086",
				"TOTAL UGX                         26,786",
				"",
				"PAID BY      MOBILE MONEY",
			},
			Expected: `{
  "number": "ERP-55219",
  "vendor": "ENTEBBE ROAD PHARMACY",
  "issued": "2026-04-14",
  "currency": "UGX",
  "subtotal": 22700,
  "tax": 4086,
  "total": 26786,
  "method": "mobile-money",
  "cashier": "A. OKELLO"
}`,
		},
	}
}
