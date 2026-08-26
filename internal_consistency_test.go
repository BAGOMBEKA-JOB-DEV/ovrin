package ovrin

import (
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// Packages under internal/ cannot import the root — the root imports them, and
// Go rejects the cycle (docs/architecture.md, "Layout"). So a package needing a
// value the root also declares must declare its own copy, and nothing about
// that duplication keeps the two in step.
//
// These tests close that. They live in the root because the root is the only
// package that can see both sides, and because the root's values are the ones
// users read: when they disagree, the internal copy is what changes.
//
// Anything duplicated across the boundary belongs here. A new duplicate with no
// entry is drift waiting to happen.

func TestDetectKindsMatchTheRoot(t *testing.T) {
	t.Parallel()

	// Exhaustive on purpose: a Kind added to one side and not the other is
	// precisely the drift this catches, and a loop over one side's values
	// would not notice a member the other side lacks.
	cases := []struct {
		name     string
		root     Kind
		internal detect.Kind
	}{
		{"unknown", KindUnknown, detect.KindUnknown},
		{"pdf", KindPDF, detect.KindPDF},
		{"png", KindPNG, detect.KindPNG},
		{"jpeg", KindJPEG, detect.KindJPEG},
		{"tiff", KindTIFF, detect.KindTIFF},
		{"webp", KindWebP, detect.KindWebP},
		{"docx", KindDOCX, detect.KindDOCX},
		{"xlsx", KindXLSX, detect.KindXLSX},
		{"csv", KindCSV, detect.KindCSV},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if string(tc.root) != string(tc.internal) {
				t.Errorf("ovrin.Kind%s = %q, detect.Kind%s = %q; the wire values must match",
					tc.name, tc.root, tc.name, tc.internal)
			}
		})
	}

	// A Kind the root declares and this test does not list would pass
	// silently, so assert the count as well. The number is what the root
	// declares; if you added a Kind, add a case above.
	const rootKinds = 9
	if len(cases) != rootKinds {
		t.Errorf("this test covers %d kinds, the root declares %d", len(cases), rootKinds)
	}
}

func TestDetectLimitsMatchTheRoot(t *testing.T) {
	t.Parallel()

	// internal/detect carries its defaults on a Limits struct rather than as
	// loose constants, so the comparison is field by field against the root's
	// Default* values.
	lim := detect.DefaultLimits()

	cases := []struct {
		name     string
		root     int64
		internal int64
	}{
		{"max source bytes", DefaultMaxSourceBytes, lim.MaxSourceBytes},
		{"max decompressed bytes", DefaultMaxDecompressedBytes, lim.MaxDecompressedBytes},
		{"max stream bytes", DefaultMaxStreamBytes, lim.MaxStreamBytes},
		{"max text bytes", DefaultMaxTextBytes, lim.MaxTextBytes},
		{"max pages", int64(DefaultMaxPages), int64(lim.MaxPages)},
		{"max depth", int64(DefaultMaxDepth), int64(lim.MaxDepth)},
		{"max objects", int64(DefaultMaxObjects), int64(lim.MaxObjects)},
		{"max page pixels", int64(DefaultMaxPagePixels), int64(lim.MaxPagePixels)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.root != tc.internal {
				t.Errorf("root = %d, internal/detect = %d; a limit documented in "+
					"ADR-0020 and enforced with a different number is worse than "+
					"no limit, because the documentation is then a lie", tc.root, tc.internal)
			}
		})
	}
}
