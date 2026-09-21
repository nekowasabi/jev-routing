package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestParseTransformsIndependent(t *testing.T) {
	got, err := parseTransforms("compact=off,filter=on,criteria=on")
	if err != nil || got.Compact || !got.Filter || !got.Criteria {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := parseTransforms("nope=on"); err == nil {
		t.Fatal("unknown transform must fail")
	}
	if _, err := parseTransforms("compact"); err == nil {
		t.Fatal("bare name must fail")
	}
}

func TestCriteriaForUnregisteredStaysRaw(t *testing.T) {
	if got := criteriaFor("grep", "Search files", true); got != "Search files" {
		t.Fatalf("unregistered must stay raw: %q", got)
	}
	if got := criteriaFor("grep", "Search files", false); got != "Search files" {
		t.Fatalf("disabled must stay raw: %q", got)
	}
}

func TestCriteriaForRegisteredPair(t *testing.T) {
	old := contrastCriteria
	contrastCriteria = map[string]contrastSpec{
		"grep": {Covers: "search text", NotFor: "read whole files", Examples: []string{"find TODO"}},
	}
	t.Cleanup(func() { contrastCriteria = old })

	got := criteriaFor("grep", "raw desc", true)
	if !strings.Contains(got, "covers: search text") || !strings.Contains(got, "not_for: read whole files") || !strings.Contains(got, "examples: find TODO") {
		t.Fatalf("registered pair: %q", got)
	}
	if criteriaFor("grep", "raw desc", false) != "raw desc" {
		t.Fatal("disabled must stay raw")
	}
	if criteriaFor("read", "raw", true) != "raw" {
		t.Fatal("unregistered neighbor must stay raw")
	}
}

func TestCompactTransformOffLeavesHistory(t *testing.T) {
	fat := strings.Repeat("line of grep output\n", 200)
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it and fix it in place."},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "grep", "arguments": `{"pattern":"auth"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "name": "grep", "content": fat},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c2", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"x"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c2", "name": "read_file", "content": "file body"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c3", "type": "function", "function": map[string]any{"name": "search_replace", "arguments": `{}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c3", "name": "search_replace", "content": "ok"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
		},
	}
	raw, _ := json.Marshal(req)
	opt := DefaultOptions()
	opt.Transforms.Compact = false
	_, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CompactApplied || containsName(stats.Transforms, transformCompact) {
		t.Fatalf("compact=off must not rewrite history: %+v", stats)
	}
}

func TestFilterTransformOffLeavesCatalog(t *testing.T) {
	raw := evalCatalogBody(
		"Do not parallel. Sequential search and read the definition of RewriteWith.",
		[]string{"read_file", "grep", "search_replace", "run_terminal_cmd", "web_search", "send_feedback"},
	)
	opt := DefaultOptions()
	opt.Transforms.Filter = false
	out, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter != stats.ToolBefore || stats.Apply != applyNone {
		t.Fatalf("filter=off must leave the catalog: %+v", stats)
	}
	if containsName(stats.Transforms, transformFilter) {
		t.Fatalf("filter must not be recorded when disabled: %v", stats.Transforms)
	}
	if len(stats.ProposedKept) == 0 {
		t.Fatal("filter=off must still compute a proposed set")
	}
	if len(catalogNames(out)) != 6 {
		t.Fatalf("forwarded catalog changed: %v", catalogNames(out))
	}
}

func TestAppliedTransformsRecordedIndependently(t *testing.T) {
	raw := evalCatalogBody(
		"Do not parallel. Sequential search and read the definition of RewriteWith.",
		[]string{"read_file", "grep", "search_replace", "run_terminal_cmd", "web_search", "send_feedback"},
	)
	opt := DefaultOptions()
	_, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !containsName(stats.Transforms, transformFilter) {
		t.Fatalf("filter apply should record transform: %+v", stats)
	}
	if containsName(stats.Transforms, transformCriteria) {
		t.Fatalf("empty contrast map must not record criteria: %v", stats.Transforms)
	}

	opt.Shadow = true
	opt.Transforms.Criteria = true
	_, stats, err = RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if containsName(stats.Transforms, transformFilter) {
		t.Fatalf("shadow must not record filter: %v", stats.Transforms)
	}
	if !containsName(stats.Transforms, transformCriteria) {
		t.Fatalf("criteria flag must be recorded when enabled: %v", stats.Transforms)
	}
	ev := EventFromStats(stats)
	if ev.Shadow != stats.Shadow || !containsName(ev.Transforms, transformCriteria) {
		t.Fatalf("event lost transform ids: %+v", ev)
	}
}
