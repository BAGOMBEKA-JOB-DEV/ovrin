package main

// identity returns the identity category.
//
// Every document here belongs to the Republic of Kirunda, which does not
// exist, and every one of them says on its face that it is fictitious. That is
// deliberate on two counts. Rule §7.6 keeps real personal data out of the
// repository, and an identity document is nothing but personal data, so this
// is the one category where no amount of redaction could make a donated
// document acceptable. And a synthetic document that named a real state and a
// real issuing authority would be a forgery of that state's document, however
// clearly it was labelled in a metadata file nobody opens.
//
// A real identity document also carries a photograph, a hologram and a
// signature. None of those is here, and the numbers are shorter than most real
// ones. Read the figures from this category as measuring layout and field
// discrimination, not document security.
func identity() []document {
	return []document{
		{
			Category:   "identity",
			Name:       "001",
			Recipe:     cleanDigital(),
			Difficulty: "clean-digital",
			Seed:       5001,
			Notes: `A digital original with every field present, including an expiry date.
Three dates on one small document — birth, issue and expiry — which is the
discrimination this category is mostly testing.`,
			Body: []string{
				"@H Republic of Kirunda - National Identity Card",
				"@B This document is fictitious. No such state or authority exists.",
				"@R",
				"Surname             Nabbosa",
				"Given names         Sarah Immaculate",
				"Nationality         Kirundan",
				"Sex                 F",
				"Date of birth       12 Jul 1991",
				"Place of birth      Mityana",
				"Card number         CM91072100NBSK",
				"Date of issue       04 Feb 2024",
				"Date of expiry      03 Feb 2034",
				"Issuing authority   National Identification Authority",
			},
			Expected: `{
  "document_type": "national-id",
  "number": "CM91072100NBSK",
  "surname": "Nabbosa",
  "given_names": "Sarah Immaculate",
  "nationality": "Kirundan",
  "sex": "F",
  "date_of_birth": "1991-07-12",
  "place_of_birth": "Mityana",
  "issued": "2024-02-04",
  "expires": "2034-02-03",
  "issuing_authority": "National Identification Authority"
}`,
		},
		{
			Category:   "identity",
			Name:       "002",
			Recipe:     goodScan(),
			Difficulty: "good-scan",
			Seed:       5002,
			Notes: `Office flatbed: rotated 0.7 degrees, one blur pass, noise at sigma 1.2,
sixty specks.

The two lines at the foot are a machine-readable zone in the ICAO 9303
shape. Its check digits are invented and do not validate — nothing in this
repository claims otherwise. It is there because a real passport has one
and it is a dense band of characters that a reader may try to extract
fields from; the surname in the MRZ agrees with the printed surname, so an
extractor reading either is right.`,
			Body: []string{
				"@H REPUBLIC OF KIRUNDA",
				"@B PASSPORT - THIS DOCUMENT IS FICTITIOUS",
				"@R",
				"TYPE                P",
				"PASSPORT NO         KR0448127",
				"SURNAME             OKELLO",
				"GIVEN NAMES         DANIEL OTIM",
				"NATIONALITY         KIRUNDAN",
				"SEX                 M",
				"DATE OF BIRTH       03 NOV 1988",
				"PLACE OF BIRTH      GULU",
				"DATE OF ISSUE       15 AUG 2023",
				"DATE OF EXPIRY      14 AUG 2033",
				"AUTHORITY           DIRECTORATE OF CITIZENSHIP",
				"@R",
				"P<KRDOKELLO<<DANIEL<OTIM<<<<<<<<<<<<<<<<<<<<",
				"KR04481275KRD8811036M3308142<<<<<<<<<<<<<<02",
			},
			Expected: `{
  "document_type": "passport",
  "number": "KR0448127",
  "surname": "OKELLO",
  "given_names": "DANIEL OTIM",
  "nationality": "KIRUNDAN",
  "sex": "M",
  "date_of_birth": "1988-11-03",
  "place_of_birth": "GULU",
  "issued": "2023-08-15",
  "expires": "2033-08-14",
  "issuing_authority": "DIRECTORATE OF CITIZENSHIP"
}`,
		},
		{
			Category:   "identity",
			Name:       "003",
			Recipe:     goodScan(),
			Difficulty: "good-scan",
			Seed:       5003,
			Notes: `Office flatbed, same chain as 002.

A driving permit, which carries neither nationality nor place of birth, so
both fields are absent. It also carries a CLASSES line that no schema field
corresponds to; an extractor that files "B CM" under nationality has
fabricated.`,
			Body: []string{
				"@H REPUBLIC OF KIRUNDA - DRIVING PERMIT",
				"@B FICTITIOUS DOCUMENT",
				"@R",
				"PERMIT NO           DP-2026-338291",
				"SURNAME             BYARUHANGA",
				"GIVEN NAMES         PATRICK MUGISHA",
				"SEX                 M",
				"DATE OF BIRTH       22 SEP 1979",
				"DATE OF ISSUE       10 JAN 2026",
				"DATE OF EXPIRY      09 JAN 2029",
				"CLASSES             B CM",
				"AUTHORITY           TRANSPORT LICENSING BOARD",
			},
			Expected: `{
  "document_type": "driving-licence",
  "number": "DP-2026-338291",
  "surname": "BYARUHANGA",
  "given_names": "PATRICK MUGISHA",
  "sex": "M",
  "date_of_birth": "1979-09-22",
  "issued": "2026-01-10",
  "expires": "2029-01-09",
  "issuing_authority": "TRANSPORT LICENSING BOARD"
}`,
		},
		{
			Category:   "identity",
			Name:       "004",
			Recipe:     poorScan(),
			Difficulty: "poor-scan",
			Seed:       5004,
			Notes: `The machine in the corridor: skewed 1.8 degrees, contrast at 0.58, one
blur pass, noise at sigma 9, four hundred and twenty specks, JPEG quality
34, at half the scale of a good scan.

A residence permit whose holder's nationality is not the issuing state's,
which is the pair of fields most often conflated on this kind of document.`,
			Body: []string{
				"@H KIRUNDA IMMIGRATION SERVICE",
				"@B RESIDENCE PERMIT - FICTITIOUS DOCUMENT",
				"@R",
				"PERMIT NO           RP-77-004512",
				"SURNAME             VOLLMER",
				"GIVEN NAMES         ANNA MARIE",
				"NATIONALITY         GERMAN",
				"SEX                 F",
				"DATE OF BIRTH       30 MAR 1985",
				"PLACE OF BIRTH      FRANKFURT",
				"DATE OF ISSUE       01 JUN 2025",
				"DATE OF EXPIRY      31 MAY 2027",
				"AUTHORITY           DIRECTORATE OF IMMIGRATION CONTROL",
			},
			Expected: `{
  "document_type": "residence-permit",
  "number": "RP-77-004512",
  "surname": "VOLLMER",
  "given_names": "ANNA MARIE",
  "nationality": "GERMAN",
  "sex": "F",
  "date_of_birth": "1985-03-30",
  "place_of_birth": "FRANKFURT",
  "issued": "2025-06-01",
  "expires": "2027-05-31",
  "issuing_authority": "DIRECTORATE OF IMMIGRATION CONTROL"
}`,
		},
		{
			Category:   "identity",
			Name:       "005",
			Recipe:     photograph(),
			Difficulty: "photograph",
			Seed:       5005,
			Notes: `Photographed on a desk: keystoned 7 per cent, rotated 1.4 degrees, one
corner shadowed, warm white balance, downsampled by 1.5, one blur pass,
JPEG quality 46.

The card does not expire, and says so in words. The expiry field is
therefore absent, and this is the clearest fabrication trap in the corpus:
the line EXPIRY DOES NOT EXPIRE is right next to two other dates, and
producing either of them as the expiry would be a well-formed, validating,
wrong answer.`,
			Body: []string{
				"@H REPUBLIC OF KIRUNDA - NATIONAL IDENTITY CARD",
				"@B FICTITIOUS DOCUMENT",
				"@R",
				"CARD NUMBER         CM75031400ADKE",
				"SURNAME             ADIKINI",
				"GIVEN NAMES         ESTHER OCEN",
				"NATIONALITY         KIRUNDAN",
				"SEX                 F",
				"DATE OF BIRTH       14 MAR 1975",
				"PLACE OF BIRTH      SOROTI",
				"DATE OF ISSUE       09 SEP 2022",
				"EXPIRY              DOES NOT EXPIRE",
				"AUTHORITY           NATIONAL IDENTIFICATION AUTHORITY",
			},
			Expected: `{
  "document_type": "national-id",
  "number": "CM75031400ADKE",
  "surname": "ADIKINI",
  "given_names": "ESTHER OCEN",
  "nationality": "KIRUNDAN",
  "sex": "F",
  "date_of_birth": "1975-03-14",
  "place_of_birth": "SOROTI",
  "issued": "2022-09-09",
  "issuing_authority": "NATIONAL IDENTIFICATION AUTHORITY"
}`,
		},
	}
}
