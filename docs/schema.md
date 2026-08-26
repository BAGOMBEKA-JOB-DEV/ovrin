# Schemas

A schema is a Go struct. Ovrin reads it by reflection and it drives everything:
what the model is asked for, what is validated, and what appears in
`Result.Fields`.

```go
type Invoice struct {
    Number   string    `ovrin:"invoice number,required"`
    Issued   time.Time `ovrin:"date the invoice was issued,format=date"`
    Vendor   Vendor    `ovrin:"vendor information"`
    Items    []Item    `ovrin:"invoice line items"`
    Currency string    `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
    Total    float64   `ovrin:"total amount including tax,required,min=0"`
}
```

**Contents:** [Grammar](#grammar) · [Descriptions](#descriptions) ·
[Rules](#rules) · [Types](#types) · [Nesting](#nesting) ·
[Field keys](#field-keys) · [Errors](#errors)

---

## Grammar

```text
ovrin:"<description>[,<rule>[,<rule>...]]"
```

The first element is the description. Every element after it is a rule
([ADR-0006](adr/0006-tag-grammar.md)).

| Form | Meaning |
|---|---|
| `ovrin:"total amount"` | description only |
| `ovrin:"total amount,required,min=0"` | description and two rules |
| `ovrin:",required"` | description derived from the field name |
| `ovrin:"-"` | excluded from the schema |
| *(no tag)* | excluded from the schema |

A comma inside a description is written `\,`; a literal backslash is `\\`.

```go
type Applicant struct {
    Address string `ovrin:"street\, city and postcode,required"`
}
```

This is the grammar's one wart. A description needing several commas is usually
a description that should be shorter.

A field with no `ovrin` tag is skipped entirely, which means an existing
application struct carrying `json` and `db` tags can be used as a schema by
tagging only the fields that come from documents.

---

## Descriptions

The description is a prompt fragment. It is the single highest-leverage thing
in a schema and it is worth more attention than the rules.

| Poor | Better | Why |
|---|---|---|
| `"total"` | `"total amount including tax"` | `"total"` picks up the pre-tax subtotal about as often as not |
| `"date"` | `"date the invoice was issued, not the due date"` | invoices carry several dates |
| `"name"` | `"the vendor's registered company name"` | otherwise it may return a contact person |
| `"number"` | `"invoice number as printed by the vendor"` | distinguishes it from a purchase-order number |

Write the description as an instruction to a careful reader who has the
document and no context. Say which of several similar values you mean. Name
units and currencies where ambiguity is possible. If a field is often absent,
say so — `"purchase order number, if one is printed"` — because a model told a
field is optional is much less likely to invent one.

**Descriptions are untyped input to a typed system.** Changing the prose can
change extraction results with no compiler signal at all. That is why the
evaluation corpus exists ([ADR-0023](adr/0023-evaluation-corpus.md)): a
description change is a change worth measuring.

**Derived descriptions.** An empty first element derives the description from
the field name by splitting camel case: `InvoiceNumber` becomes
`"invoice number"`, `VATRate` becomes `"vat rate"`. Convenient for obvious
fields, and worse than a written description for everything else.

---

## Rules

The vocabulary is closed. An unrecognised rule name is an `ErrSchema` naming
it, never a silent skip.

| Rule | Applies to | Meaning |
|---|---|---|
| `required` | any | The field must be present. Absence sets `Valid` false. |
| `min=N` | numeric, string, slice | Numeric minimum; minimum length for strings and slices. |
| `max=N` | numeric, string, slice | Numeric maximum; maximum length for strings and slices. |
| `format=F` | string, `time.Time` | The value must match a known format. |
| `enum=A\|B\|C` | string | The value must be one of the alternatives. |

### `format`

| Format | Accepts | Normalised to |
|---|---|---|
| `date` | most written and numeric date forms | `time.Time`, midnight UTC |
| `datetime` | date with a time | `time.Time` |
| `email` | RFC 5322 addressable form | lowercased |
| `phone` | digits with separators, optional country code | E.164 where a country can be determined |
| `currency` | ISO 4217 code | uppercased |
| `iban`, `swift` | with checksum validation | uppercased, spaces removed |
| `uuid` | any RFC 4122 form | lowercased, hyphenated |

A value that cannot be parsed to the declared format sets `Valid` false and
records the raw text on the field, so a reviewer sees what was actually there.

**Ambiguous dates.** `03/04/2026` is 3 April or 4 March depending on locale.
Ovrin does not guess: an ambiguous date produces a review reason and a lowered
`format` signal. `WithDateOrder(ovrin.DayFirst)` resolves it for a corpus you
know.

### `required` and absence

`required` reports a *validation* outcome; it does not cause an error and does
not stop extraction ([ADR-0004](adr/0004-partial-results.md)). A required
field that is missing sets `Result.Valid` false, sets `NeedsReview`, and adds a
reason. The other eleven fields are still returned.

---

## Types

| Go type | Notes |
|---|---|
| `string` | |
| `int`, `int8`…`int64`, `uint`… | Rejected with `Valid` false if the value is not integral |
| `float32`, `float64` | Thousands separators and currency symbols are stripped before parsing |
| `bool` | Accepts yes/no, true/false, checked/unchecked, Y/N |
| `time.Time` | Requires `format=date` or `format=datetime` |
| `[]T` | Repeated items — line items, dependants, transactions |
| `struct` | Nested object |
| `*T` | Optional. `nil` distinguishes absent from zero |
| `map[string]T` | **Not supported.** Use a struct — a schema needs known keys |
| `any`, `interface{}` | **Not supported.** No schema can be derived |
| channels, funcs | **Not supported** |

An unsupported type is an `ErrSchema` at the first `Extract` call, before any
provider is contacted.

**`*T` versus `Found`.** Both express absence and they are not redundant.
`FieldResult.Found` is always available and always accurate. A pointer field
additionally makes absence visible on `Data` itself, which matters when the
struct is passed on to code that never sees the `Result`. Use pointers for
fields where a downstream consumer must not confuse absent with zero.

---

## Nesting

Structs and slices nest to any depth within the recursion limit
([ADR-0020](adr/0020-resource-limits.md)).

```go
type Invoice struct {
    Vendor Vendor `ovrin:"vendor information"`
    Items  []Item `ovrin:"invoice line items"`
}

type Vendor struct {
    Name    string `ovrin:"registered company name,required"`
    Address string `ovrin:"full postal address"`
    TaxID   string `ovrin:"tax identification number"`
}

type Item struct {
    Description string  `ovrin:"item description"`
    Quantity    int     `ovrin:"quantity,min=0"`
    UnitPrice   float64 `ovrin:"price per unit excluding tax,min=0"`
}
```

The description on a nested field describes the whole object —
`"vendor information"`, not a repetition of its members.

A struct that refers to itself, directly or through another type, is rejected
with `ErrSchema`. There is no way to bound the schema sent to the model, and a
model given an unbounded schema will produce unbounded output.

---

## Field keys

`Result.Fields` is keyed by path, lowercased with dots for nesting and indices
for slice elements:

```go
res.Fields["number"]
res.Fields["vendor.name"]
res.Fields["items[0].unit_price"]
```

Multi-word Go field names become snake case: `UnitPrice` is `unit_price`,
`VATRate` is `vat_rate`.

Slice keys depend on how many items were extracted, so a caller that iterates
`Fields` sees exactly the items that were found. `res.Fields["items"]` also
exists and describes the slice as a whole — whether it was found, and its
aggregate confidence.

---

## Errors

Every schema problem is `ErrSchema`, raised at the first `Extract` for a type,
before any provider call:

| Problem | Message names |
|---|---|
| Unknown rule name | the rule and the field |
| Rule inapplicable to the type — `min` on a `bool` | the rule, the field and the type |
| Unsupported field type | the field and the type |
| Recursive type | the cycle |
| Malformed `enum` — empty alternative | the field |
| A struct with no tagged fields at all | the type |

The last is deliberate. A struct where every `ovrin` tag was forgotten would
otherwise extract nothing and report success.

## See also

- [ADR-0005](adr/0005-schemas-are-go-structs.md) — why structs and reflection
- [ADR-0006](adr/0006-tag-grammar.md) — why the grammar is shaped this way
- [`pipeline.md`](pipeline.md) — where schemas are consumed
- [`confidence.md`](confidence.md) — how validation feeds scoring
