package eval

import (
	"strings"
	"testing"
)

// TestParseMeta covers the hand-rolled metadata reader.
//
// It is hand-rolled to keep the module free of a YAML dependency, which means
// this table is the only thing standing between a corpus and a document filed
// under the wrong difficulty. The first case is the exact example from
// docs/evaluation.md, wrapped continuation and trailing comment included,
// because a parser that cannot read the documentation's own example is a
// parser whose documentation is wrong.
func TestParseMeta(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		check func(t *testing.T, m Meta)
		err   bool
	}{
		{
			name: "the example from docs/evaluation.md",
			in: "source: public-form          # public-form | synthetic | donated\n" +
				"licence: CC0-1.0\n" +
				"redacted: names, account numbers and dates of birth replaced with\n" +
				"          synthetic values of the same shape\n" +
				"difficulty: poor-scan        # clean-digital | good-scan | poor-scan |\n" +
				"                             # photograph | handwritten | multi-column\n" +
				"pages: 3\n" +
				"language: en\n" +
				"notes: |\n" +
				"  Skewed about 4 degrees. Staple hole through the top-left of page 2.\n" +
				"  Total appears twice — once in the summary box, once in the footer.\n",
			check: func(t *testing.T, m Meta) {
				if m.Source != "public-form" {
					t.Errorf("source = %q", m.Source)
				}
				if m.Licence != "CC0-1.0" {
					t.Errorf("licence = %q", m.Licence)
				}
				if !strings.HasPrefix(m.Redacted, "names, account numbers") ||
					!strings.HasSuffix(m.Redacted, "of the same shape") {
					t.Errorf("redacted = %q; the wrapped continuation was not joined", m.Redacted)
				}
				if m.Difficulty != "poor-scan" {
					t.Errorf("difficulty = %q; the trailing comment was not stripped", m.Difficulty)
				}
				if m.Pages != 3 {
					t.Errorf("pages = %d", m.Pages)
				}
				if m.Language != "en" {
					t.Errorf("language = %q", m.Language)
				}
				if !strings.Contains(m.Notes, "Staple hole") ||
					!strings.Contains(m.Notes, "once in the footer") {
					t.Errorf("notes = %q; the literal block lost a line", m.Notes)
				}
				if strings.Contains(m.Notes, "  Skewed") {
					t.Errorf("notes = %q; the block indent was not stripped", m.Notes)
				}
			},
		},
		{
			name: "a literal block ends at the next key",
			in:   "notes: |\n  first\n  second\ndifficulty: clean-digital\n",
			check: func(t *testing.T, m Meta) {
				if m.Notes != "first\nsecond" {
					t.Errorf("notes = %q", m.Notes)
				}
				if m.Difficulty != "clean-digital" {
					t.Errorf("difficulty = %q; the key after a block was swallowed", m.Difficulty)
				}
			},
		},
		{
			name: "a blank line inside a literal block is kept",
			in:   "notes: |\n  first\n\n  second\n",
			check: func(t *testing.T, m Meta) {
				if m.Notes != "first\n\nsecond" {
					t.Errorf("notes = %q", m.Notes)
				}
			},
		},
		{
			name: "an exclude list",
			in:   "exclude:\n  - due\n  - items[0].amount\n",
			check: func(t *testing.T, m Meta) {
				if len(m.Exclude) != 2 || m.Exclude[0] != "due" || m.Exclude[1] != "items[0].amount" {
					t.Errorf("exclude = %v", m.Exclude)
				}
			},
		},
		{
			name: "a hash inside a value is not a comment",
			in:   "notes: order #4471 was cancelled\n",
			check: func(t *testing.T, m Meta) {
				if m.Notes != "order #4471 was cancelled" {
					t.Errorf("notes = %q", m.Notes)
				}
			},
		},
		{
			name: "quoted values are unquoted",
			in:   "difficulty: \"good-scan\"\nsource: 'synthetic'\n",
			check: func(t *testing.T, m Meta) {
				if m.Difficulty != "good-scan" || m.Source != "synthetic" {
					t.Errorf("difficulty = %q source = %q", m.Difficulty, m.Source)
				}
			},
		},
		{
			name: "the American spelling of licence is accepted",
			in:   "license: CC0-1.0\n",
			check: func(t *testing.T, m Meta) {
				if m.Licence != "CC0-1.0" {
					t.Errorf("licence = %q", m.Licence)
				}
			},
		},
		{
			name: "a comment line is skipped",
			in:   "# a note to a reviewer\ndifficulty: photograph\n",
			check: func(t *testing.T, m Meta) {
				if m.Difficulty != "photograph" {
					t.Errorf("difficulty = %q", m.Difficulty)
				}
			},
		},
		{
			name: "a misspelled key is refused rather than ignored",
			in:   "dificulty: poor-scan\n",
			err:  true,
		},
		{
			name: "a page count that is not a number is refused",
			in:   "pages: several\n",
			err:  true,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			m, err := ParseMeta(c.in)
			if c.err {
				if err == nil {
					t.Fatalf("ParseMeta accepted %q", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMeta: %v", err)
			}
			c.check(t, m)
		})
	}
}

// TestValidateMeta covers the four fields that make a document admissible.
//
// The licensing constraint is absolute (rule §7.6, ADR-0023) and a repository
// is forever, so a document with no recorded licence must fail to load rather
// than be quietly scored.
func TestValidateMeta(t *testing.T) {
	full := Meta{
		Source: "synthetic", Licence: "CC0-1.0",
		Redacted: "nothing was ever real", Difficulty: "clean-digital",
	}
	cases := []struct {
		name string
		meta Meta
		ok   bool
	}{
		{"a complete record", full, true},
		{"no source", Meta{Licence: "CC0-1.0", Redacted: "x", Difficulty: "clean-digital"}, false},
		{"no licence", Meta{Source: "synthetic", Redacted: "x", Difficulty: "clean-digital"}, false},
		{"no redaction note", Meta{Source: "synthetic", Licence: "CC0-1.0", Difficulty: "clean-digital"}, false},
		{"no difficulty", Meta{Source: "synthetic", Licence: "CC0-1.0", Redacted: "x"}, false},
		{
			"a difficulty outside the vocabulary",
			Meta{Source: "synthetic", Licence: "CC0-1.0", Redacted: "x", Difficulty: "quite hard"},
			false,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := validateMeta(c.meta)
			if (err == nil) != c.ok {
				t.Errorf("validateMeta = %v, want ok=%v", err, c.ok)
			}
		})
	}
}
