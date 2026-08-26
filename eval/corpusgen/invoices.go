package main

// invoices returns the invoices category.
//
// The category exercises nesting and slices: two parties, a line-item table,
// and totals that stand in a fixed arithmetic relationship to the lines above
// them. Every document's arithmetic is internally consistent, so a cross-field
// check has something true to find; a document with a genuine arithmetic error
// would be a useful addition and would need its ground truth to match the
// document rather than the arithmetic.
func invoices() []document {
	return []document{
		{
			Category:   "invoices",
			Name:       "001",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       1001,
			Notes: `A digital original with a real text layer, every optional field present.
This is the easiest document in the corpus and its score is a ceiling
rather than an expectation.`,
			Body: []string{
				"@H Nakawa Stationers Limited",
				"Plot 14 Jinja Road, Kampala",
				"TIN 1002938475",
				"@R",
				"@B TAX INVOICE",
				"",
				"Invoice no      INV-2026-0417",
				"Issued          14 Mar 2026",
				"Payment due     13 Apr 2026",
				"Your order no   PO-88213",
				"",
				"Bill to         Makindye Secondary School",
				"                PO Box 7712, Kampala",
				"                TIN 1009988771",
				"@R",
				"Description                        Qty     Unit price        Amount",
				"A4 paper 80gsm                      40         12,500       500,000",
				"Toner cartridge HP 26A               4        185,000       740,000",
				"@R",
				"                              Subtotal                    1,240,000",
				"                              VAT 18%                       223,200",
				"                              Total UGX                   1,463,200",
			},
			Expected: `{
  "number": "INV-2026-0417",
  "issued": "2026-03-14",
  "due": "2026-04-13",
  "purchase_order": "PO-88213",
  "vendor": {
    "name": "Nakawa Stationers Limited",
    "address": "Plot 14 Jinja Road, Kampala",
    "tax_id": "1002938475"
  },
  "bill_to": {
    "name": "Makindye Secondary School",
    "address": "PO Box 7712, Kampala",
    "tax_id": "1009988771"
  },
  "currency": "UGX",
  "subtotal": 1240000,
  "tax": 223200,
  "total": 1463200,
  "items": [
    {"description": "A4 paper 80gsm", "quantity": 40, "unit_price": 12500, "amount": 500000},
    {"description": "Toner cartridge HP 26A", "quantity": 4, "unit_price": 185000, "amount": 740000}
  ]
}`,
		},
		{
			Category:   "invoices",
			Name:       "002",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       1002,
			Exclude:    []string{"due"},
			Notes: `Clean digital, but deliberately incomplete: no purchase order, no tax
identifier on either party, and no subtotal or tax line — the invoice
shows a total only. Those five absences are the fabrication opportunities
this document exists to provide.

The due date is printed 03/04/2026, which is 3 April or 4 March depending
on where it was written. Two careful readers would disagree, so it is
excluded from scoring rather than being scored against a guess.`,
			Body: []string{
				"@H Bugolobi Hardware & Tools",
				"Plot 3 Luthuli Avenue, Kampala",
				"@R",
				"@B INVOICE",
				"",
				"Invoice no      BH-4471",
				"Issued          2026-02-09",
				"Due             03/04/2026",
				"",
				"Bill to         Sanyu Construction Co",
				"                Plot 22 Ntinda Road, Kampala",
				"@R",
				"Description                        Qty     Unit price        Amount",
				"Cement 50kg bag                     60         38,000     2,280,000",
				"Wheelbarrow heavy duty               3        195,000       585,000",
				"Steel bar 12mm 6m                   25         42,000     1,050,000",
				"@R",
				"                              Total UGX                   3,915,000",
			},
			Expected: `{
  "number": "BH-4471",
  "issued": "2026-02-09",
  "vendor": {
    "name": "Bugolobi Hardware & Tools",
    "address": "Plot 3 Luthuli Avenue, Kampala"
  },
  "bill_to": {
    "name": "Sanyu Construction Co",
    "address": "Plot 22 Ntinda Road, Kampala"
  },
  "currency": "UGX",
  "total": 3915000,
  "items": [
    {"description": "Cement 50kg bag", "quantity": 60, "unit_price": 38000, "amount": 2280000},
    {"description": "Wheelbarrow heavy duty", "quantity": 3, "unit_price": 195000, "amount": 585000},
    {"description": "Steel bar 12mm 6m", "quantity": 25, "unit_price": 42000, "amount": 1050000}
  ]
}`,
		},
		{
			Category:   "invoices",
			Name:       "003",
			Recipe:     goodScan(),
			Difficulty: "good-scan",
			Seed:       1003,
			Notes: `Office flatbed: rotated 0.7 degrees, one blur pass for the platen's
optical softness, Gaussian sensor noise at sigma 1.2, sixty single-pixel
dust specks. No text layer, so this document has to be
read by OCR or by vision.`,
			Body: []string{
				"@H MBARARA AGRO SUPPLIES LTD",
				"P.O. BOX 411, MBARARA",
				"TIN 1007766554",
				"@R",
				"@B SALES INVOICE",
				"",
				"INVOICE NO      MAS-2026-0093",
				"ISSUED          02 APR 2026",
				"DUE             02 MAY 2026",
				"ORDER NO        PO-2026-118",
				"",
				"BILL TO         KAZO DAIRY COOPERATIVE",
				"                P.O. BOX 88, KAZO",
				"                TIN 1004433221",
				"@R",
				"DESCRIPTION                    QTY    UNIT PRICE       AMOUNT",
				"DAIRY MEAL 70KG                 30        96,000    2,880,000",
				"MINERAL LICK BLOCK              50        14,500      725,000",
				"@R",
				"                        SUBTOTAL                    3,605,000",
				"                        VAT 18%                       648,900",
				"                        TOTAL UGX                   4,253,900",
			},
			Expected: `{
  "number": "MAS-2026-0093",
  "issued": "2026-04-02",
  "due": "2026-05-02",
  "purchase_order": "PO-2026-118",
  "vendor": {
    "name": "MBARARA AGRO SUPPLIES LTD",
    "address": "P.O. BOX 411, MBARARA",
    "tax_id": "1007766554"
  },
  "bill_to": {
    "name": "KAZO DAIRY COOPERATIVE",
    "address": "P.O. BOX 88, KAZO",
    "tax_id": "1004433221"
  },
  "currency": "UGX",
  "subtotal": 3605000,
  "tax": 648900,
  "total": 4253900,
  "items": [
    {"description": "DAIRY MEAL 70KG", "quantity": 30, "unit_price": 96000, "amount": 2880000},
    {"description": "MINERAL LICK BLOCK", "quantity": 50, "unit_price": 14500, "amount": 725000}
  ]
}`,
		},
		{
			Category:   "invoices",
			Name:       "004",
			Recipe:     poorScan(),
			Difficulty: "poor-scan",
			Seed:       1004,
			Notes: `The machine in the corridor: skewed 1.8 degrees, contrast cut to 0.58 with
the black point lifted, one blur pass, noise at sigma 9, four hundred and
twenty two-pixel specks, then JPEG at quality 34. Some of the specks land
on glyphs. Rendered at half the scale of the good scan as well, so there
are fewer pixels per stroke to begin with.`,
			Body: []string{
				"@H SOROTI MOTOR SPARES",
				"PLOT 9 GWERI ROAD, SOROTI",
				"TIN 1002211009",
				"@R",
				"@B INVOICE",
				"",
				"INVOICE NO      SMS-7742",
				"ISSUED          21 JAN 2026",
				"",
				"BILL TO         TESO TRANSPORT SACCO",
				"                P.O. BOX 25, SOROTI",
				"@R",
				"DESCRIPTION                    QTY    UNIT PRICE       AMOUNT",
				"BRAKE PAD SET                   12        78,000      936,000",
				"ENGINE OIL 20L                   6       210,000    1,260,000",
				"AIR FILTER                      18        23,500      423,000",
				"@R",
				"                        SUBTOTAL                    2,619,000",
				"                        VAT 18%                       471,420",
				"                        TOTAL UGX                   3,090,420",
			},
			Expected: `{
  "number": "SMS-7742",
  "issued": "2026-01-21",
  "vendor": {
    "name": "SOROTI MOTOR SPARES",
    "address": "PLOT 9 GWERI ROAD, SOROTI",
    "tax_id": "1002211009"
  },
  "bill_to": {
    "name": "TESO TRANSPORT SACCO",
    "address": "P.O. BOX 25, SOROTI"
  },
  "currency": "UGX",
  "subtotal": 2619000,
  "tax": 471420,
  "total": 3090420,
  "items": [
    {"description": "BRAKE PAD SET", "quantity": 12, "unit_price": 78000, "amount": 936000},
    {"description": "ENGINE OIL 20L", "quantity": 6, "unit_price": 210000, "amount": 1260000},
    {"description": "AIR FILTER", "quantity": 18, "unit_price": 23500, "amount": 423000}
  ]
}`,
		},
		{
			Category:   "invoices",
			Name:       "005",
			Recipe:     photograph(),
			Difficulty: "photograph",
			Seed:       1005,
			Notes: `A phone held over the document on a desk: keystoned 7 per cent because the
phone is not parallel to the page, rotated 1.4 degrees, one corner
shadowed, warm white balance from a tungsten bulb, downsampled by 1.5
because the photographer stood too far back, one blur pass and JPEG at
quality 46.

No subtotal or tax line, so those two fields are absent.`,
			Body: []string{
				"@H GULU PRINT WORKS",
				"PLOT 2 LABWORO ROAD, GULU",
				"@R",
				"@B INVOICE",
				"",
				"INVOICE NO      GPW-0331",
				"ISSUED          09 MAY 2026",
				"DUE             08 JUN 2026",
				"",
				"BILL TO         ACHOLI HERITAGE TRUST",
				"                P.O. BOX 190, GULU",
				"@R",
				"DESCRIPTION                    QTY    UNIT PRICE       AMOUNT",
				"A5 BOOKLET 24PP                500         3,200    1,600,000",
				"BANNER 3M X 1M                   4       145,000      580,000",
				"@R",
				"                        TOTAL UGX                   2,180,000",
			},
			Expected: `{
  "number": "GPW-0331",
  "issued": "2026-05-09",
  "due": "2026-06-08",
  "vendor": {
    "name": "GULU PRINT WORKS",
    "address": "PLOT 2 LABWORO ROAD, GULU"
  },
  "bill_to": {
    "name": "ACHOLI HERITAGE TRUST",
    "address": "P.O. BOX 190, GULU"
  },
  "currency": "UGX",
  "total": 2180000,
  "items": [
    {"description": "A5 BOOKLET 24PP", "quantity": 500, "unit_price": 3200, "amount": 1600000},
    {"description": "BANNER 3M X 1M", "quantity": 4, "unit_price": 145000, "amount": 580000}
  ]
}`,
		},
	}
}
