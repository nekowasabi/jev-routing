package bench

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestComputeCompareKeyExcludeToolsIsDistinguishing covers the --claude-clear-exclude
// requirement: two otherwise-identical runs must not be treated as the same
// condition when their exclude_tools configuration differs.
func TestComputeCompareKeyExcludeToolsIsDistinguishing(t *testing.T) {
	base := compareKeySettings{Task: "t", Agent: "claude", Model: "m", ClaudeClear: true}
	withExclude := base
	withExclude.ClaudeClearExclude = "web_search,bash"

	if computeCompareKey(base) != computeCompareKey(base) {
		t.Fatal("identical settings must produce the same key")
	}
	if computeCompareKey(base) == computeCompareKey(withExclude) {
		t.Fatal("claudeClearExclude difference must change the compare key")
	}
}

// TestComputeCompareKeyClearGate: an unset gate must not change existing keys,
// and the jev gate must not pair with ungated runs.
func TestComputeCompareKeyClearGate(t *testing.T) {
	base := compareKeySettings{Task: "t", Agent: "claude", Model: "m", ClaudeClear: true}
	if got, _ := json.Marshal(base); strings.Contains(string(got), "ClaudeClearGate") {
		t.Fatalf("unset gate changed the key input:\n%s", got)
	}
	gated := base
	gated.ClaudeClearGate = "jev"
	if computeCompareKey(base) == computeCompareKey(gated) {
		t.Fatal("clear gate difference must change the compare key")
	}
}

func TestComputeCompareKeyCodexCompactLimits(t *testing.T) {
	base := compareKeySettings{Task: "compact-facts", Agent: "codex", CodexCompactLimit: 45000, CodexCompactBaselineLimit: 900000}
	changed := base
	changed.CodexCompactBaselineLimit = 800000
	if computeCompareKey(base) == computeCompareKey(changed) {
		t.Fatal("baseline auto-compact limit must distinguish benchmark series")
	}
	changed = base
	changed.CodexNativeCompaction = true
	if computeCompareKey(base) == computeCompareKey(changed) {
		t.Fatal("Codex Jev replacement setting must distinguish benchmark series")
	}
}
