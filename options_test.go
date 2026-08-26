// The wiring behind the limit and policy options, tested where it can break
// silently.
//
// Drift caught here: a With* option writing the wrong config field. The values
// themselves are guarded in internal/detect and the enforcement is tested
// there, but nothing exercised the path from WithMaxObjects(n) through config
// to detect.Limits — so a copy-paste that made WithMaxObjects write maxDepth
// would compile, pass every other test, and silently disable a limit that the
// documentation still promises. Every assertion below is about which field a
// value lands on, not about what the value then does.
//
// This is an internal test because config and limitsOf are unexported and are
// the two halves of the wiring. Testing it from outside could only observe the
// limits through their effects, which is the thing internal/detect already
// covers.
package ovrin

import (
	"context"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// configField reads one knob off config as a float64.
//
// Every knob is a number, so one accessor type covers all of them and the
// cross-wiring check below can compare any field against any other. Named
// accessors rather than reflection because config's fields are unexported and
// a failure that names the field is worth more than one that names an index.
type configField struct {
	name string
	get  func(*config) float64
}

// configFields is every field the options under test can write. A new limit
// option belongs here as well as in the table: an option whose field is not
// listed is one this test cannot prove is not cross-wired.
var configFields = []configField{
	{"maxSourceBytes", func(c *config) float64 { return float64(c.maxSourceBytes) }},
	{"maxDecompressedBytes", func(c *config) float64 { return float64(c.maxDecompressedBytes) }},
	{"maxStreamBytes", func(c *config) float64 { return float64(c.maxStreamBytes) }},
	{"maxTextBytes", func(c *config) float64 { return float64(c.maxTextBytes) }},
	{"maxPages", func(c *config) float64 { return float64(c.maxPages) }},
	{"maxDepth", func(c *config) float64 { return float64(c.maxDepth) }},
	{"maxObjects", func(c *config) float64 { return float64(c.maxObjects) }},
	{"maxPagePixels", func(c *config) float64 { return float64(c.maxPagePixels) }},
	{"concurrency", func(c *config) float64 { return float64(c.concurrency) }},
	{"reviewThreshold", func(c *config) float64 { return c.reviewThreshold }},
	{"minTextDensity", func(c *config) float64 { return c.minTextDensity }},
	{"maxReplacementRatio", func(c *config) float64 { return c.maxReplacementRatio }},
	{"minDecodableRatio", func(c *config) float64 { return c.minDecodableRatio }},
}

// limitField reads one ceiling off the detect.Limits that limitsOf builds.
type limitField struct {
	name string
	get  func(detect.Limits) float64
}

// limitFields is every ceiling limitsOf fills in. concurrency and the four
// policy thresholds are deliberately absent: they are not resource limits and
// do not belong in this struct, which is itself an assertion — see
// TestPolicyOptionsDoNotReachTheResourceLimits.
var limitFields = []limitField{
	{"MaxSourceBytes", func(l detect.Limits) float64 { return float64(l.MaxSourceBytes) }},
	{"MaxDecompressedBytes", func(l detect.Limits) float64 { return float64(l.MaxDecompressedBytes) }},
	{"MaxStreamBytes", func(l detect.Limits) float64 { return float64(l.MaxStreamBytes) }},
	{"MaxTextBytes", func(l detect.Limits) float64 { return float64(l.MaxTextBytes) }},
	{"MaxPages", func(l detect.Limits) float64 { return float64(l.MaxPages) }},
	{"MaxDepth", func(l detect.Limits) float64 { return float64(l.MaxDepth) }},
	{"MaxObjects", func(l detect.Limits) float64 { return float64(l.MaxObjects) }},
	{"MaxPagePixels", func(l detect.Limits) float64 { return float64(l.MaxPagePixels) }},
}

// optionCase is one option, the value it is given, and where that value must
// end up.
//
// The values are distinct primes and near-primes rather than round numbers,
// and no two rows share one: two options writing the same field is exactly the
// bug being hunted, and identical values would hide it.
type optionCase struct {
	name string
	opt  Option

	// field is the config field the option must write, and want the value it
	// must write there.
	field string
	want  float64

	// limit is the detect.Limits field the value must reach through limitsOf,
	// and is empty for an option that is not a resource limit.
	limit string
}

func optionCases() []optionCase {
	return []optionCase{
		{"WithMaxSourceBytes", WithMaxSourceBytes(1000003), "maxSourceBytes", 1000003, "MaxSourceBytes"},
		{"WithMaxDecompressedBytes", WithMaxDecompressedBytes(2000003), "maxDecompressedBytes", 2000003, "MaxDecompressedBytes"},
		{"WithMaxStreamBytes", WithMaxStreamBytes(3000003), "maxStreamBytes", 3000003, "MaxStreamBytes"},
		{"WithMaxTextBytes", WithMaxTextBytes(4000003), "maxTextBytes", 4000003, "MaxTextBytes"},
		{"WithMaxPages", WithMaxPages(101), "maxPages", 101, "MaxPages"},
		{"WithMaxDepth", WithMaxDepth(103), "maxDepth", 103, "MaxDepth"},
		{"WithMaxObjects", WithMaxObjects(107), "maxObjects", 107, "MaxObjects"},
		{"WithMaxPagePixels", WithMaxPagePixels(109), "maxPagePixels", 109, "MaxPagePixels"},
		{"WithConcurrency", WithConcurrency(113), "concurrency", 113, ""},
		{"WithReviewThreshold", WithReviewThreshold(0.11), "reviewThreshold", 0.11, ""},
		{"WithMinTextDensity", WithMinTextDensity(0.13), "minTextDensity", 0.13, ""},
		{"WithMaxReplacementRatio", WithMaxReplacementRatio(0.17), "maxReplacementRatio", 0.17, ""},
		{"WithMinDecodableRatio", WithMinDecodableRatio(0.19), "minDecodableRatio", 0.19, ""},
	}
}

// Each option writes its own field and nothing else.
//
// The second half is the point. Asserting only that WithMaxObjects(107) makes
// maxObjects 107 would pass for an option that also, or instead, wrote
// maxDepth — so every other field is checked against the default it started
// at, and a value that moved somewhere it was not asked to move names both
// fields in the failure.
func TestEachOptionWritesItsOwnConfigField(t *testing.T) {
	t.Parallel()

	base := defaults()
	for _, tt := range optionCases() {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := defaults()
			tt.opt.apply(&cfg)

			hit := false
			for _, f := range configFields {
				got, was := f.get(&cfg), f.get(&base)
				if f.name == tt.field {
					hit = true
					if got != tt.want {
						t.Errorf("%s(%v) left config.%s = %v, want %v",
							tt.name, tt.want, f.name, got, tt.want)
					}
					continue
				}
				if got != was {
					t.Errorf("%s(%v) also changed config.%s from %v to %v; it is wired to the wrong field",
						tt.name, tt.want, f.name, was, got)
				}
			}
			if !hit {
				t.Fatalf("%s names config field %q, which is not in configFields; the test cannot see it",
					tt.name, tt.field)
			}
		})
	}
}

// Each limit option reaches detect.Limits as the ceiling it names.
//
// config is only half the wiring: limitsOf is what hands the numbers to the
// parsers that enforce them, and a value that lands on the right config field
// and is then copied to the wrong Limits field is the same silently disabled
// limit.
func TestEachLimitOptionReachesDetectLimits(t *testing.T) {
	t.Parallel()

	base := defaults()
	baseLim := limitsOf(&base)

	for _, tt := range optionCases() {
		if tt.limit == "" {
			continue
		}
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := defaults()
			tt.opt.apply(&cfg)
			lim := limitsOf(&cfg)

			hit := false
			for _, f := range limitFields {
				got, was := f.get(lim), f.get(baseLim)
				if f.name == tt.limit {
					hit = true
					if got != tt.want {
						t.Errorf("%s(%v) left limitsOf().%s = %v, want %v",
							tt.name, tt.want, f.name, got, tt.want)
					}
					continue
				}
				if got != was {
					t.Errorf("%s(%v) also changed limitsOf().%s from %v to %v; limitsOf copies the wrong field",
						tt.name, tt.want, f.name, was, got)
				}
			}
			if !hit {
				t.Fatalf("%s names limit %q, which is not in limitFields; the test cannot see it",
					tt.name, tt.limit)
			}
		})
	}
}

// The policy thresholds and the concurrency bound are not resource limits and
// must not appear in the struct the parsers are handed.
//
// They are policy: how good an answer has to be, how much of the host to use.
// A threshold that leaked into detect.Limits would be enforced as a ceiling on
// bytes or pages, which is a limit nobody configured.
func TestPolicyOptionsDoNotReachTheResourceLimits(t *testing.T) {
	t.Parallel()

	base := defaults()
	baseLim := limitsOf(&base)

	for _, tt := range optionCases() {
		if tt.limit != "" {
			continue
		}
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := defaults()
			tt.opt.apply(&cfg)
			lim := limitsOf(&cfg)

			for _, f := range limitFields {
				if got, was := f.get(lim), f.get(baseLim); got != was {
					t.Errorf("%s(%v) changed limitsOf().%s from %v to %v; a policy threshold is being enforced as a resource limit",
						tt.name, tt.want, f.name, was, got)
				}
			}
		})
	}
}

// The same options, through the constructor a caller actually uses.
//
// New copies defaults and overlays the options, so this is the one path a
// caller's WithMaxPages(500) travels. Testing apply alone would not notice a
// New that dropped the options on the floor.
func TestNewCarriesEveryOptionOntoTheClient(t *testing.T) {
	t.Parallel()

	for _, tt := range optionCases() {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := New(WithModel(nilReplyModel{}), tt.opt)
			for _, f := range configFields {
				if f.name != tt.field {
					continue
				}
				if got := f.get(&c.cfg); got != tt.want {
					t.Errorf("New(%s(%v)) left Client config.%s = %v, want %v",
						tt.name, tt.want, f.name, got, tt.want)
				}
			}
		})
	}
}

// nilReplyModel satisfies WithModel, which New requires and panics without. It
// is never called: every test in this file stops at configuration.
type nilReplyModel struct{}

func (nilReplyModel) Generate(context.Context, ModelRequest) (*ModelResponse, error) {
	return &ModelResponse{JSON: []byte(`{}`)}, nil
}
