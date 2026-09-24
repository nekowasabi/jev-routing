package plan

import (
	"regexp"
	"strings"
)

// mixedIdent matches a single camel-case identifier. All-caps tokens such as
// JSON do not match, so a definition task does not treat them as symbols.
var mixedIdent = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*[A-Z][A-Za-z0-9]*`)

// DefinitionSymbols returns the camel-case names a sequential locate task asks
// for. An empty result means the task is not a bounded definition lookup.
// More than eight names is treated as unbounded and left to the model.
func DefinitionSymbols(task string) []string {
	// Keep the text before the locate marker. The names usually sit there,
	// and taskText drops that prefix.
	task = WorkRequest(task)
	if !sequentialLocate(task) && !strings.Contains(strings.ToLower(task), "where are") {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, tok := range mixedIdent.FindAllString(task, -1) {
		if seen[tok] || len(tok) < 4 || len(tok) > 80 {
			continue
		}
		if !hasLowerAndUpper(tok) {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	if len(out) == 0 || len(out) > 8 {
		return nil
	}
	return out
}

func hasLowerAndUpper(s string) bool {
	lower, upper := false, false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		}
	}
	return lower && upper
}
