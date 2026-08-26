package main

// forms returns the forms category.
//
// Every document here is an invented "Form DTL-1" for a district trading
// licence. It is not any real government form: copying a real form's wording
// would put somebody else's copyright into a repository that promises
// everything in it is redistributable (ADR-0023), and a licence problem
// discovered later cannot be fixed by deletion.
//
// The tick boxes are the point of the category. A box read as ticked when it
// is empty produces a well-formed boolean that passes every validation rule
// and is wrong, which is precisely the failure fabrication rate exists to
// count.
func forms() []document {
	return []document{
		{
			Category:   "forms",
			Name:       "001",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       3001,
			Notes: `A digital original with every field completed and both boxes in Section C
answered. The telephone is written +256 772 445 118 and the schema
declares format=phone, so ground truth records the E.164 form; the email
is already lowercase, so normalisation is a no-op there.`,
			Body: []string{
				"@H District Trading Licence Application",
				"@B Invented Form DTL-1 - this form is fictitious",
				"@R",
				"Reference       DTL-2026-004512",
				"Date received   11 Mar 2026",
				"@R",
				"@B Section A - Applicant",
				"Full name       Miriam Nakato Ssebugwawo",
				"Telephone       +256 772 445 118",
				"Email           miriam.nakato@example.org",
				"Postal address  P.O. Box 3391, Kampala",
				"@R",
				"@B Section B - Business",
				"Trading name    Nakato Tailoring Works",
				"Activity        Tailoring and garment repair",
				"District        Kampala",
				"Employees       7",
				"@R",
				"@B Section C - Declarations",
				"New application [ ]        Renewal [X]",
				"Tax clearance attached [X]",
				"Fee paid UGX    120,000",
				"@R",
				"Signed          Miriam Nakato Ssebugwawo",
			},
			Expected: `{
  "reference": "DTL-2026-004512",
  "received": "2026-03-11",
  "applicant": {
    "name": "Miriam Nakato Ssebugwawo",
    "phone": "+256772445118",
    "email": "miriam.nakato@example.org",
    "address": "P.O. Box 3391, Kampala"
  },
  "business": "Nakato Tailoring Works",
  "activity": "Tailoring and garment repair",
  "district": "Kampala",
  "employees": 7,
  "fee_paid": 120000,
  "renewal": true,
  "tax_cleared": true,
  "declaration": "Miriam Nakato Ssebugwawo"
}`,
		},
		{
			Category:   "forms",
			Name:       "002",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       3002,
			Notes: `Clean digital, deliberately incomplete: no email, no fee, and the form is
unsigned. Those three fields are absent from ground truth.

Both boxes in Section C are answered, and both answers are false. That is
the distinction this document exists to test: "the box is empty" and "we
could not read the box" are different results, and only one of them is a
value.`,
			Body: []string{
				"@H District Trading Licence Application",
				"@B Invented Form DTL-1 - this form is fictitious",
				"@R",
				"Reference       DTL-2026-004980",
				"Date received   02 Apr 2026",
				"@R",
				"@B Section A - Applicant",
				"Full name       Joseph Kiprotich Wanyama",
				"Telephone       +256 701 903 226",
				"Postal address  P.O. Box 77, Mbale",
				"@R",
				"@B Section B - Business",
				"Trading name    Wanyama Boda Repairs",
				"Activity        Motorcycle repair",
				"District        Mbale",
				"Employees       2",
				"@R",
				"@B Section C - Declarations",
				"New application [X]        Renewal [ ]",
				"Tax clearance attached [ ]",
			},
			Expected: `{
  "reference": "DTL-2026-004980",
  "received": "2026-04-02",
  "applicant": {
    "name": "Joseph Kiprotich Wanyama",
    "phone": "+256701903226",
    "address": "P.O. Box 77, Mbale"
  },
  "business": "Wanyama Boda Repairs",
  "activity": "Motorcycle repair",
  "district": "Mbale",
  "employees": 2,
  "renewal": false,
  "tax_cleared": false
}`,
		},
		{
			Category:   "forms",
			Name:       "003",
			Recipe:     goodScan(),
			Difficulty: "good-scan",
			Seed:       3003,
			Notes: `Office flatbed: rotated 0.7 degrees, one blur pass, noise at sigma 1.2,
sixty specks.

The email is printed in capitals, as the whole form is. format=email
lowercases, so ground truth records the lowercase form — a comparison that
insisted on the printed case would report a failure on a correct answer.`,
			Body: []string{
				"@H DISTRICT TRADING LICENCE APPLICATION",
				"@B INVENTED FORM DTL-1 - THIS FORM IS FICTITIOUS",
				"@R",
				"REFERENCE       DTL-2026-005114",
				"DATE RECEIVED   19 APR 2026",
				"@R",
				"@B SECTION A - APPLICANT",
				"FULL NAME       GRACE ATIM ODONG",
				"TELEPHONE       +256 782 110 447",
				"EMAIL           GRACE.ATIM@EXAMPLE.ORG",
				"POSTAL ADDRESS  P.O. BOX 210, LIRA",
				"@R",
				"@B SECTION B - BUSINESS",
				"TRADING NAME    ATIM PRODUCE STORE",
				"ACTIVITY        RETAIL OF CEREALS AND PULSES",
				"DISTRICT        LIRA",
				"EMPLOYEES       4",
				"@R",
				"@B SECTION C - DECLARATIONS",
				"NEW APPLICATION [ ]        RENEWAL [X]",
				"TAX CLEARANCE ATTACHED [X]",
				"FEE PAID UGX    85,000",
				"@R",
				"SIGNED          GRACE ATIM ODONG",
			},
			Expected: `{
  "reference": "DTL-2026-005114",
  "received": "2026-04-19",
  "applicant": {
    "name": "GRACE ATIM ODONG",
    "phone": "+256782110447",
    "email": "grace.atim@example.org",
    "address": "P.O. BOX 210, LIRA"
  },
  "business": "ATIM PRODUCE STORE",
  "activity": "RETAIL OF CEREALS AND PULSES",
  "district": "LIRA",
  "employees": 4,
  "fee_paid": 85000,
  "renewal": true,
  "tax_cleared": true,
  "declaration": "GRACE ATIM ODONG"
}`,
		},
		{
			Category:   "forms",
			Name:       "004",
			Recipe:     poorScan(),
			Difficulty: "poor-scan",
			Seed:       3004,
			Notes: `The machine in the corridor: skewed 1.8 degrees, contrast at 0.58, one
blur pass, noise at sigma 9, four hundred and twenty specks, JPEG quality
34, at half the scale of a good scan. The tick boxes are two characters
wide and this is where they become genuinely hard to read.

The signature is initialled "P. M. BYARUHANGA" while the applicant is
named in full, so declaration and applicant.name are different strings and
an extractor that copies one into the other is wrong.`,
			Body: []string{
				"@H DISTRICT TRADING LICENCE APPLICATION",
				"@B INVENTED FORM DTL-1 - THIS FORM IS FICTITIOUS",
				"@R",
				"REFERENCE       DTL-2026-005390",
				"DATE RECEIVED   28 APR 2026",
				"@R",
				"@B SECTION A - APPLICANT",
				"FULL NAME       PATRICK MUGISHA BYARUHANGA",
				"TELEPHONE       +256 772 660 341",
				"POSTAL ADDRESS  P.O. BOX 4, FORT PORTAL",
				"@R",
				"@B SECTION B - BUSINESS",
				"TRADING NAME    RWENZORI COFFEE HULLERS",
				"ACTIVITY        COFFEE HULLING AND GRADING",
				"DISTRICT        KABAROLE",
				"EMPLOYEES       11",
				"@R",
				"@B SECTION C - DECLARATIONS",
				"NEW APPLICATION [ ]        RENEWAL [X]",
				"TAX CLEARANCE ATTACHED [ ]",
				"FEE PAID UGX    150,000",
				"@R",
				"SIGNED          P. M. BYARUHANGA",
			},
			Expected: `{
  "reference": "DTL-2026-005390",
  "received": "2026-04-28",
  "applicant": {
    "name": "PATRICK MUGISHA BYARUHANGA",
    "phone": "+256772660341",
    "address": "P.O. BOX 4, FORT PORTAL"
  },
  "business": "RWENZORI COFFEE HULLERS",
  "activity": "COFFEE HULLING AND GRADING",
  "district": "KABAROLE",
  "employees": 11,
  "fee_paid": 150000,
  "renewal": true,
  "tax_cleared": false
}`,
		},
		{
			Category:   "forms",
			Name:       "005",
			Recipe:     cleanDigital(),
			Difficulty: "multi-column",
			Seed:       3005,
			Notes: `A digital original laid out in two columns, which is the difficulty here:
reading order. A reader that takes the page line by line across the full
width will interleave Section A with Section C and pair the wrong label
with the wrong value.

The form is unsigned, so declaration is absent. The office-use column
carries a zone, an officer code and a stamp, none of which is a field of
the schema; an extractor that puts "insp-22" in declaration has
fabricated.`,
			Body: []string{
				"@H District Trading Licence Application",
				"@B Invented Form DTL-1 - this form is fictitious",
				"@R",
				"Reference    DTL-2026-005722          Section C - Declarations",
				"Received     06 May 2026              New application [ ]   Renewal [X]",
				"                                      Tax clearance attached [X]",
				"Section A - Applicant                 Fee paid UGX 210,000",
				"Full name    Esther Adikini Ocen",
				"Telephone    +256 753 220 984         Section D - Office use",
				"Email        esther.ocen@example.org  Zone            4",
				"Address      P.O. Box 62, Soroti      Officer         insp-22",
				"                                      Stamp           received",
				"Section B - Business",
				"Trading name Ocen Grain Millers",
				"Activity     Maize milling",
				"District     Soroti",
				"Employees    9",
			},
			Expected: `{
  "reference": "DTL-2026-005722",
  "received": "2026-05-06",
  "applicant": {
    "name": "Esther Adikini Ocen",
    "phone": "+256753220984",
    "email": "esther.ocen@example.org",
    "address": "P.O. Box 62, Soroti"
  },
  "business": "Ocen Grain Millers",
  "activity": "Maize milling",
  "district": "Soroti",
  "employees": 9,
  "fee_paid": 210000,
  "renewal": true,
  "tax_cleared": true
}`,
		},
	}
}
