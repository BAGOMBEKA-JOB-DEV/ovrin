package prompt

import (
	"fmt"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// maxDepth bounds how far the field list descends.
//
// A schema deeper than this is rejected at stage 4, so this is not a policy
// about nesting — it is what makes rendering provably terminate even if a
// malformed Schema arrives with a cycle in it. Every limit has a finite
// default (docs/rules.md §5.2).
const maxDepth = 12

// Instruction builds the instruction from the schema, and only from the
// schema.
//
// The signature is the security control. There is no parameter here through
// which document text could arrive, so a reviewer can establish that the
// returned string contains no document content by reading one line rather than
// by tracing every path through the package (docs/rules.md §7.2,
// docs/adr/0017-untrusted-document-content.md).
//
// The result is deterministic: the same schema produces byte-identical output,
// which is what lets tests assert on it and what lets a provider cache the
// prefix of every request for a given type.
//
// The field descriptions rendered here come from the caller's own struct tags.
// Those are developer-authored and therefore trusted; a caller who builds a
// description out of untrusted input has put untrusted input into the
// instruction themselves, and no structure on this side can undo that.
func Instruction(s schema.Schema) string {
	var b strings.Builder
	b.Grow(4096)

	b.WriteString(taskSection)

	b.WriteString("\n## Fields\n\n")
	if name := collapse(s.Name); name != "" {
		fmt.Fprintf(&b, "The object being extracted is named %s. Extract only the fields listed here.\n\n", name)
	} else {
		b.WriteString("Extract only the fields listed here.\n\n")
	}
	writeFields(&b, s.Fields, "", 0)

	b.WriteString(rulesSection)
	b.WriteString(contentSection())
	return b.String()
}

// writeFields renders one level of the field list, in declaration order.
//
// Declaration order is the order the caller wrote their struct in, and it
// often carries meaning — an invoice's number before its total — so it is
// preserved rather than sorted.
func writeFields(b *strings.Builder, fields []schema.Field, parent string, depth int) {
	if len(fields) == 0 {
		return
	}
	if depth >= maxDepth {
		writeDepthNotice(b, depth)
		return
	}
	for _, f := range fields {
		writeField(b, f, qualify(parent, f.Key), depth)
	}
}

// writeField renders one field and everything nested under it.
func writeField(b *strings.Builder, f schema.Field, key string, depth int) {
	b.WriteString(indent(depth))
	b.WriteString("- ")
	b.WriteString(label(key))
	b.WriteString(" (")
	b.WriteString(attributes(f))
	b.WriteString(")")
	if d := collapse(f.Description); d != "" {
		b.WriteString(": ")
		b.WriteString(d)
	}
	b.WriteString("\n")

	if f.Elem != nil {
		if depth+1 >= maxDepth {
			writeDepthNotice(b, depth+1)
		} else {
			writeField(b, *f.Elem, elemKey(key, *f.Elem), depth+1)
		}
	}
	writeFields(b, f.Fields, key, depth+1)
}

// writeDepthNotice records that description stopped, rather than stopping
// silently. Dropping data without saying so is the one behaviour this project
// does not tolerate (docs/rules.md §6.1).
func writeDepthNotice(b *strings.Builder, depth int) {
	b.WriteString(indent(depth))
	b.WriteString("- (nesting limit reached; fields below this point are not described)\n")
}

// label is the name shown for a field.
func label(key string) string {
	if key == "" {
		return "(unnamed field)"
	}
	return key
}

// attributes renders the parenthesised type and rules of a field.
//
// Rules are rendered in tag order and verbatim. A rule this package has never
// heard of is still shown: the vocabulary is closed at stage 4, and silently
// omitting an unknown rule here would hide a schema bug rather than surface
// it.
func attributes(f schema.Field) string {
	parts := make([]string, 0, len(f.Rules)+2)
	parts = append(parts, kindName(f.Kind))
	if f.Optional {
		parts = append(parts, "optional")
	}
	for _, r := range f.Rules {
		name := collapse(r.Name)
		if name == "" {
			continue
		}
		if value := collapse(r.Value); value != "" {
			parts = append(parts, name+"="+value)
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

// kindName maps an extraction kind to a word a model will read the same way it
// reads the JSON Schema sent alongside.
//
// The names line up with JSON Schema's own type names rather than with Go's,
// because the model is being asked to satisfy the JSON Schema and two
// vocabularies for one concept is one too many.
func kindName(k schema.Kind) string {
	switch k {
	case schema.KindString:
		return "string"
	case schema.KindInt:
		return "integer"
	case schema.KindFloat:
		return "number"
	case schema.KindBool:
		return "boolean"
	case schema.KindTime:
		return "date or time"
	case schema.KindObject:
		return "object"
	case schema.KindArray:
		return "array"
	case schema.KindUnknown:
		return "value"
	default:
		// A kind added to the schema package but not here. Name it rather
		// than calling it "value": docs/rules.md §1.9.
		return collapse(k.String())
	}
}

// qualify joins a parent key to a child key.
//
// The schema package documents Field.Key as a path ("vendor.name") but a
// nested Field could reasonably carry either the full path or the leaf alone.
// Handling both means the rendered key is the real path under either
// convention, and no path is ever invented.
func qualify(parent, child string) string {
	parent, child = collapse(parent), collapse(child)
	switch {
	case parent == "":
		return child
	case child == "":
		return parent
	case strings.HasPrefix(child, parent+"."), strings.HasPrefix(child, parent+"["):
		return child
	default:
		return parent + "." + child
	}
}

// elemKey is the key shown for a slice's element.
func elemKey(parent string, elem schema.Field) string {
	if k := collapse(elem.Key); k != "" && k != parent {
		return qualify(parent, k)
	}
	return parent + "[]"
}

// indent is two spaces per level of nesting.
func indent(depth int) string {
	if depth <= 0 {
		return ""
	}
	return strings.Repeat("  ", depth)
}

// collapse reduces every run of whitespace to a single space and trims the
// ends.
//
// The input is the developer's own struct tag, not document content, so this
// is not a sanitiser. It is what keeps one field on one line: a description
// containing a newline would otherwise reflow the list and make the
// instruction's bytes depend on how a tag was wrapped.
func collapse(s string) string {
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}
