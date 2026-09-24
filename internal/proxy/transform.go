package proxy

import (
	"strings"
	"unicode/utf8"
)

const (
	transformCompact  = "compact"
	transformFilter   = "filter"
	transformCriteria = "criteria"
)

// TransformOptions enables or disables each request transform independently.
type TransformOptions struct {
	Compact  bool
	Filter   bool
	Criteria bool
}

func defaultTransforms() TransformOptions {
	return TransformOptions{Compact: true, Filter: true, Criteria: false}
}

func parseTransforms(v string) (TransformOptions, error) {
	out := defaultTransforms()
	if strings.TrimSpace(v) == "" {
		return out, nil
	}
	for _, part := range strings.Split(v, ",") {
		name, mode, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" {
			return out, errInvalidTransforms(v)
		}
		on := mode == "on" || mode == "1" || mode == "true"
		off := mode == "off" || mode == "0" || mode == "false"
		if !on && !off {
			return out, errInvalidTransforms(v)
		}
		switch name {
		case transformCompact:
			out.Compact = on
		case transformFilter:
			out.Filter = on
		case transformCriteria:
			out.Criteria = on
		default:
			return out, errInvalidTransforms(v)
		}
	}
	return out, nil
}

type invalidTransforms string

func (e invalidTransforms) Error() string {
	return "invalid JEV_TRANSFORMS " + string(e) + " (compact|filter|criteria=on|off)"
}

func errInvalidTransforms(v string) error { return invalidTransforms(v) }

func appliedTransforms(opt Options, stats RewriteStats) []string {
	var out []string
	if opt.Transforms.Compact && stats.CompactApplied {
		out = append(out, transformCompact)
	}
	if opt.Transforms.Filter && stats.Apply != "" && stats.Apply != applyNone && !opt.Shadow {
		out = append(out, transformFilter)
	}
	if opt.Transforms.Criteria {
		out = append(out, transformCriteria)
	}
	return out
}

// contrastCriteria is the limited Phase 4 pair list. Empty until Phase 3
// identifies a confused pair against real tool definitions.
var contrastCriteria = map[string]contrastSpec{}

type contrastSpec struct {
	Covers   string   `json:"covers"`
	NotFor   string   `json:"not_for"`
	Examples []string `json:"examples"`
}

const criteriaByteLimit = 300

// shortenCriteria trims a Jev judgment candidate description to its first
// paragraph, then to at most criteriaByteLimit bytes, cutting at the last
// sentence end within that budget so the meaning stays intact. This only
// changes what is sent to Jev for the next_tool choice; the upstream tool
// definition itself is untouched.
func shortenCriteria(desc string) string {
	if i := strings.Index(desc, "\n\n"); i >= 0 {
		desc = desc[:i]
	}
	if len(desc) <= criteriaByteLimit {
		return desc
	}
	cut := criteriaByteLimit
	for cut > 0 && !utf8.RuneStart(desc[cut]) {
		cut--
	}
	window := desc[:cut]
	end := -1
	if i := strings.LastIndex(window, "。"); i >= 0 {
		if e := i + len("。"); e > end {
			end = e
		}
	}
	if i := strings.LastIndex(window, ". "); i >= 0 {
		if e := i + 1; e > end { // keep the period, drop the trailing space
			end = e
		}
	}
	if end > 0 {
		return window[:end]
	}
	return window + "…"
}

func criteriaFor(name, desc string, enabled bool) string {
	if !enabled {
		return desc
	}
	spec, ok := contrastCriteria[name]
	if !ok || spec.Covers == "" {
		return desc
	}
	parts := []string{"covers: " + spec.Covers}
	if spec.NotFor != "" {
		parts = append(parts, "not_for: "+spec.NotFor)
	}
	if len(spec.Examples) > 0 {
		parts = append(parts, "examples: "+strings.Join(spec.Examples, ", "))
	}
	return strings.Join(parts, "; ")
}
