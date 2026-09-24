package bench

import "testing"

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
