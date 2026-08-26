package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ParseExpected reads NNN.expected.json and flattens it to the field keys
// [ovrin.Result].Fields uses: "total", "vendor.name", "items[0].unit_price".
//
// Flattening happens at load rather than at comparison because the two sides
// of a comparison must be the same shape. Ground truth is a JSON tree and a
// Result is a flat map, and a scorer that walks both shapes at once acquires a
// second, subtly different idea of what a field key is.
//
// Numbers are decoded as [json.Number] so that 2500000.00 keeps the digits the
// labeller wrote. Round-tripping money through float64 before comparison would
// make the comparison's own precision a term in the accuracy figure.
//
// Only leaves are recorded. A container carries no value of its own — an
// object is right exactly when its members are — and recording "vendor"
// alongside "vendor.name" would count one correct vendor twice. An empty
// object or array is the one exception: it has no leaves, and "the document
// lists no line items" is a fact ground truth must be able to state.
func ParseExpected(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("expected.json: %w", err)
	}
	if _, ok := tree.(map[string]any); !ok {
		return nil, fmt.Errorf("expected.json: top level is %T, want an object", tree)
	}
	out := map[string]any{}
	flatten("", tree, out)
	return out, nil
}

// flatten walks a decoded JSON tree, writing leaves into out under their field
// key.
func flatten(prefix string, v any, out map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			if prefix != "" {
				out[prefix] = t
			}
			return
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := k
			if prefix != "" {
				child = prefix + "." + k
			}
			flatten(child, t[k], out)
		}
	case []any:
		if len(t) == 0 {
			if prefix != "" {
				out[prefix] = t
			}
			return
		}
		for i, e := range t {
			flatten(prefix+"["+strconv.Itoa(i)+"]", e, out)
		}
	default:
		// null is absence written out. Ground truth expresses absence by
		// omitting the key, and a null that slipped in means the same thing;
		// recording it would make a correctly-absent field look like a miss.
		if v == nil {
			return
		}
		out[prefix] = v
	}
}

// leafKeys returns the subset of keys that carry a value of their own — those
// with no descendant in the same set.
//
// A [ovrin.Result] reports both "items" and "items[0].total". Scoring both
// would count a slice once as a container and again for every member, so the
// container is dropped whenever it has members. A container with no members is
// itself a leaf, and its emptiness is a claim worth scoring.
func leafKeys(keys []string) []string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	out := make([]string, 0, len(sorted))
	for i, k := range sorted {
		hasChild := false
		for j := i + 1; j < len(sorted); j++ {
			if !strings.HasPrefix(sorted[j], k) {
				break
			}
			if rest := sorted[j][len(k):]; strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "[") {
				hasChild = true
				break
			}
		}
		if !hasChild {
			out = append(out, k)
		}
	}
	return out
}
