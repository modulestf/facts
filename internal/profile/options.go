package profile

import (
	"fmt"
	"slices"
)

// optionsBlock is the optional constants block that holds the profile's
// options. A profile without it has DefaultOptions.
const optionsBlock = "profile-options"

// ConsumersInRepo is the consumers value of a module whose callers all live
// in the same repository.
const ConsumersInRepo = "in-repo"

// Options are the profile-options block: format 1 and four closed-set values.
// Any value other than the default relaxes a review: see Gates.
type Options struct {
	Format     int    `json:"format"`
	Consumers  string `json:"consumers"`
	Partitions string `json:"partitions"`
	Examples   string `json:"examples"`
	Release    string `json:"release"`
}

// DefaultOptions are the options of a profile with no profile-options block.
var DefaultOptions = Options{Format: 1, Consumers: "public", Partitions: "portable", Examples: "required", Release: "registry"}

// optionValues are the closed value sets of the option keys, the default
// first.
var optionValues = map[string][]string{
	"consumers":  {"public", ConsumersInRepo},
	"partitions": {"portable", "fixed"},
	"examples":   {"required", "none"},
	"release":    {"registry", "none"},
}

func defaultValue(key string) string { return optionValues[key][0] }

// Gate is one row of the gate table: an option value that turns the listed
// rules not_applicable, with the reason profile_option.
type Gate struct {
	Option string   `json:"option"`
	Value  string   `json:"value"`
	Rules  []string `json:"rules"`
}

// Gates is the gate table. consumers: in-repo gates no rule; it only feeds
// the in-repo downgrade. profile.required-file-missing is never gated: a
// profile with examples: none leaves its example entries empty instead.
var Gates = []Gate{
	{Option: "partitions", Value: "fixed", Rules: []string{"structure.arn-partition-hardcoded"}},
	{Option: "examples", Value: "none", Rules: []string{
		"examples.argument-undemonstrated", "examples.disabled-call-missing", "examples.feature-undemonstrated",
		"examples.submodule-missing", "examples.versions-missing",
	}},
	{Option: "release", Value: "none", Rules: []string{
		"profile.changelog-handwritten", "profile.commit-type-mismatch", "profile.migration-notes-misplaced",
		"profile.owner-identifier", "profile.provider-meta-missing", "profile.release-config-changed",
		"profile.template-drift", "profile.title-misspelling", "profile.title-uninformative",
		"profile.unrelated-change-bundled",
	}},
}

// DowngradeRules are the rules whose candidates in an in-repo downgrade area
// carry downgrade "in-repo" and the one severity MEDIUM, sorted.
var DowngradeRules = []string{
	"compat.default-changed", "compat.generated-policy-narrowed", "compat.type-narrowed",
	"compat.variable-now-required", "compat.variable-removed",
}

// value returns the option value of key.
func (o Options) value(key string) string {
	switch key {
	case "consumers":
		return o.Consumers
	case "partitions":
		return o.Partitions
	case "examples":
		return o.Examples
	case "release":
		return o.Release
	}
	return ""
}

// Relaxing reports whether any option differs from the default.
func (o Options) Relaxing() bool { return o != DefaultOptions }

// Gated reports whether the options turn rule id not_applicable.
func (o Options) Gated(id string) bool {
	for _, g := range Gates {
		if o.value(g.Option) == g.Value && slices.Contains(g.Rules, id) {
			return true
		}
	}
	return false
}

// GatedRules lists the rules the options gate, sorted.
func (o Options) GatedRules() []string {
	out := []string{}
	for _, g := range Gates {
		if o.value(g.Option) == g.Value {
			out = append(out, g.Rules...)
		}
	}
	slices.Sort(out)
	return out
}

// optionsFrom reads the profile-options block, or returns the defaults when
// there is none. Every key is required and every value is from its closed
// set.
func optionsFrom(blocks map[string]*block) (Options, error) {
	b, ok := blocks[optionsBlock]
	if !ok {
		return DefaultOptions, nil
	}
	if err := checkKeys(optionsBlock, b, []string{"format", "consumers", "partitions", "examples", "release"}); err != nil {
		return Options{}, err
	}
	if v := b.values["format"]; v != "1" {
		return Options{}, fmt.Errorf("block %s: unknown format %q", optionsBlock, v)
	}
	o := Options{Format: 1}
	for _, key := range []string{"consumers", "partitions", "examples", "release"} {
		v := b.values[key]
		if !slices.Contains(optionValues[key], v) {
			return Options{}, fmt.Errorf("block %s: key %s has an unknown value %q", optionsBlock, key, v)
		}
		switch key {
		case "consumers":
			o.Consumers = v
		case "partitions":
			o.Partitions = v
		case "examples":
			o.Examples = v
		case "release":
			o.Release = v
		}
	}
	return o, nil
}
