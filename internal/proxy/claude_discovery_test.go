package proxy

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestClaudeDiscoverySurvivesFiltering(t *testing.T) {
	read := map[string]any{"name": "Read", "input_schema": map[string]any{"type": "object"}}
	search := map[string]any{"name": "ToolSearch", "input_schema": map[string]any{"type": "object"}}
	deferred := map[string]any{"name": "project_lookup", "defer_loading": true, "input_schema": map[string]any{"type": "object"}}
	root := map[string]any{
		"system":   "Use the available tools.",
		"messages": []any{map[string]any{"role": "user", "content": "Review the pull request"}},
		"tools":    []any{read, search, deferred, map[string]any{"name": "Write", "input_schema": map[string]any{"type": "object"}}},
	}
	body, _ := json.Marshal(root)
	opt := localOpt()
	opt.Compaction, opt.Reasoning = CompactionOff, ReasoningPreserve
	out, stats, err := RewriteWith(nil, body, host.Claude, nil, opt)
	if err != nil || stats.Apply != applyFilter || stats.Chosen != "Read" {
		t.Fatalf("expected a filtered read: %+v %v", stats, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	names := map[string]any{}
	for _, raw := range asSlice(got["tools"]) {
		m := raw.(map[string]any)
		names[toolNameOf(m)] = m
	}
	for name, want := range map[string]any{"Read": read, "ToolSearch": search, "project_lookup": deferred} {
		if !reflect.DeepEqual(names[name], want) {
			t.Errorf("lost tool or discovery schema %s: %s", name, out)
		}
	}
	if _, ok := names["Write"]; ok {
		t.Fatal("unrelated eager tool was not filtered")
	}
}

func TestDiscoveryInfrastructureAndReferences(t *testing.T) {
	for _, searchType := range []string{"tool_search_tool_regex_20251119", "tool_search_tool_bm25_20251119"} {
		search := map[string]any{"type": searchType, "name": "tool_search"}
		if !isProviderExecuted(search) || !isSticky(search) {
			t.Fatalf("provider discovery must survive: %v", search)
		}
	}
	placeholder := map[string]any{"name": "DeferredToolPlaceholder", "defer_loading": true}
	search := map[string]any{"name": "ToolSearch"}
	read := map[string]any{"type": "function", "name": "Read"}
	grep := map[string]any{"type": "function", "name": "Grep"}
	write := map[string]any{"type": "function", "name": "Write"}
	tools := []any{grep, placeholder, search, read, write}
	refs := toolReferences([]any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": "search", "content": []any{
			map[string]any{"type": "tool_reference", "tool_name": "Grep"},
		}},
	}}})
	kept, resolved := filterTools(tools, []string{"Read"}, refs)
	if !resolved {
		t.Fatal("Read alias did not resolve")
	}
	if len(kept) != 4 || toolNameOf(kept[0].(map[string]any)) != "Read" {
		t.Fatalf("selected tool, discovery, or reference lost: %#v", kept)
	}
	if len(filterableTools(kept)) != 2 {
		t.Fatal("deferred/discovery tools must not enter normal selection")
	}
	wrapper := []any{map[string]any{"type": "namespace", "name": "functions", "tools": tools}}
	out := reconstructCatalog(wrapper, kept)
	inner := asSlice(out[0].(map[string]any)["tools"])
	if len(inner) != 4 {
		t.Fatalf("namespace reconstruction lost discovery: %#v", out)
	}
	for _, tool := range inner {
		if toolNameOf(tool.(map[string]any)) == "Write" {
			t.Fatal("unrelated eager tool retained")
		}
	}
	shapeBody, _ := json.Marshal(map[string]any{"tools": tools})
	shape := catalogShape(shapeBody)
	if shape.DeferredCount != 1 || !shape.DeferredPlaceholderPresent || !shape.ToolSearchPresent {
		t.Fatalf("missing discovery metadata: %+v", shape)
	}
}
