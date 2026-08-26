package schema

import "time"

// Form is the schema the forms category is extracted against.
//
// It models an invented business-licence application rather than any real
// government form: copying a real form's wording would put somebody else's
// copyright into a repository that promises every document in it is
// redistributable (ADR-0023).
//
// The boolean fields are the point of this category. A tick box read as true
// when it is empty is a fabrication that validates perfectly, which is exactly
// the failure a well-formed invented value causes in production.
type Form struct {
	Reference   string    `ovrin:"the application reference number printed on the form,required"`
	Received    time.Time `ovrin:"the date the office stamped the form as received,format=date"`
	Applicant   Applicant `ovrin:"the person applying"`
	Business    string    `ovrin:"the trading name the licence is applied for"`
	Activity    string    `ovrin:"the business activity described on the form"`
	District    string    `ovrin:"the district the premises are in"`
	Employees   int       `ovrin:"the number of employees stated,min=0"`
	FeePaid     float64   `ovrin:"the fee paid\\, if the form records one,min=0"`
	Renewal     bool      `ovrin:"whether the renewal box is ticked rather than the new-application box"`
	TaxCleared  bool      `ovrin:"whether the tax-clearance box is ticked"`
	Declaration string    `ovrin:"the name signed under the declaration\\, if the form is signed"`
}

// Applicant is the person named on a form.
type Applicant struct {
	Name    string `ovrin:"the applicant's full name as written"`
	Phone   string `ovrin:"the applicant's telephone number,format=phone"`
	Email   string `ovrin:"the applicant's email address\\, if one is given,format=email"`
	Address string `ovrin:"the applicant's postal address\\, on one line"`
}
