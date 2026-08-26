package eval

import (
	"sort"
	"strings"
	"testing"

	intschema "github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// TestCategorySchemasReflect checks that every category schema is a legal
// ovrin schema.
//
// A malformed tag is ErrSchema at the first Extract call, which during an eval
// run means discovering it after paying for the provider calls that came
// before it. This test moves that discovery to the offline suite.
func TestCategorySchemasReflect(t *testing.T) {
	for _, cat := range Categories {
		cat := cat
		t.Run(cat, func(t *testing.T) {
			s, err := schemaOf(cat)
			if err != nil {
				t.Fatalf("reflecting the %s schema: %v", cat, err)
			}
			if len(s.Fields) == 0 {
				t.Fatalf("the %s schema has no fields", cat)
			}
			keys := schemaKeys(s)
			if len(keys) == 0 {
				t.Fatalf("the %s schema yielded no field keys", cat)
			}
			for _, k := range keys {
				if k != strings.ToLower(k) {
					t.Errorf("field key %q is not lowercase; Result.Fields keys are", k)
				}
			}
		})
	}
}

// TestEveryCategoryHasARunner guards the one place where a corpus directory
// name and a Go type are tied together. A category with no runner would be
// scored against nothing and report flawless results.
func TestEveryCategoryHasARunner(t *testing.T) {
	for _, cat := range Categories {
		if _, err := RunnerFor(cat); err != nil {
			t.Errorf("%s: %v", cat, err)
		}
	}
	if _, err := RunnerFor("nonexistent"); err == nil {
		t.Error("RunnerFor accepted a category that does not exist")
	}
}

// schemaOf reflects one category's schema type.
func schemaOf(category string) (*intschema.Schema, error) {
	t, err := SchemaType(category)
	if err != nil {
		return nil, err
	}
	return intschema.Reflect(t)
}

// schemaKeys returns every field key a schema can produce, with slice indices
// left empty as [intschema] writes them.
func schemaKeys(s *intschema.Schema) []string {
	var out []string
	var walk func(fs []intschema.Field)
	walk = func(fs []intschema.Field) {
		for _, f := range fs {
			out = append(out, f.Key)
			walk(f.Fields)
			if f.Elem != nil {
				out = append(out, f.Elem.Key)
				walk(f.Elem.Fields)
			}
		}
	}
	walk(s.Fields)
	sort.Strings(out)
	return out
}
