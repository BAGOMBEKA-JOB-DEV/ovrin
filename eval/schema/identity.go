package schema

import "time"

// Identity is the schema the identity category is extracted against.
//
// Every document in this category is wholly invented. No real identity
// document is in this repository and none may be added: rule §7.6 keeps real
// personal data out, and an identity document is nothing but personal data, so
// this is the one category where a donated document can never be made
// acceptable by redaction.
//
// The fields are the ones an identity document actually carries, because the
// shape of a document number and the layout of a machine-readable zone are
// precisely the signal an extractor is being tested on.
type Identity struct {
	DocumentType     string     `ovrin:"what kind of document this is,required,enum=national-id|passport|driving-licence|residence-permit"`
	Number           string     `ovrin:"the document number as printed,required"`
	Surname          string     `ovrin:"the family name as printed,required"`
	GivenNames       string     `ovrin:"the given names as printed\\, in the order printed,required"`
	Nationality      string     `ovrin:"the nationality as printed"`
	Sex              string     `ovrin:"sex as printed on the document,enum=M|F|X"`
	DateOfBirth      time.Time  `ovrin:"date of birth,required,format=date"`
	PlaceOfBirth     string     `ovrin:"place of birth\\, if the document prints one"`
	Issued           time.Time  `ovrin:"the date the document was issued,format=date"`
	Expires          *time.Time `ovrin:"the date the document expires\\, if it expires,format=date"`
	IssuingAuthority string     `ovrin:"the authority that issued the document"`
}
