package jsonschema

import (
	"errors"
	"strings"
	"testing"
)

func TestObjectMarshalJSONPreservesInsertionOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		members []member
		want    string
	}{
		{
			name: "no members",
			want: `{}`,
		},
		{
			name:    "one member",
			members: []member{{"type", "string"}},
			want:    `{"type":"string"}`,
		},
		{
			name: "members stay in insertion order rather than sorting",
			members: []member{
				{"type", "object"},
				{"additionalProperties", false},
				{"required", []string{"b", "a"}},
			},
			want: `{"type":"object","additionalProperties":false,"required":["b","a"]}`,
		},
		{
			name: "reverse alphabetical order survives, which a map could not do",
			members: []member{
				{"zebra", 1},
				{"yak", 2},
				{"aardvark", 3},
			},
			want: `{"zebra":1,"yak":2,"aardvark":3}`,
		},
		{
			name:    "nested objects are encoded through the same path",
			members: []member{{"items", &object{members: []member{{"type", "string"}}}}},
			want:    `{"items":{"type":"string"}}`,
		},
		{
			name:    "keys are escaped by encoding/json",
			members: []member{{`a"b`, 1}},
			want:    `{"a\"b":1}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := &object{}
			for _, m := range tc.members {
				o.set(m.key, m.val)
			}
			got, err := o.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON returned an unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("MarshalJSON produced %s, want %s", got, tc.want)
			}
		})
	}
}

func TestObjectMarshalJSONRejectsAnUnencodableObject(t *testing.T) {
	t.Parallel()

	// Neither case is reachable through Marshal: duplicate property names are
	// caught by name in setObjectMembers, and every value set here is a string,
	// number, bool, slice or *object. They are asserted anyway because the
	// alternative to failing is emitting a duplicate JSON key, which decodes
	// differently in every parser that reads it.
	tests := []struct {
		name    string
		members []member
		want    string
	}{
		{
			name:    "a duplicate member name",
			members: []member{{"type", "string"}, {"type", "integer"}},
			want:    `duplicate member "type"`,
		},
		{
			name:    "a value encoding/json cannot represent",
			members: []member{{"items", make(chan int)}},
			want:    `encoding member "items"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := &object{}
			for _, m := range tc.members {
				o.set(m.key, m.val)
			}
			got, err := o.MarshalJSON()
			if err == nil {
				t.Fatalf("MarshalJSON succeeded, want an error; produced %s", got)
			}
			if !errors.Is(err, ErrUnrepresentable) {
				t.Errorf("error is not ErrUnrepresentable: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not name %q: %v", tc.want, err)
			}
		})
	}
}
