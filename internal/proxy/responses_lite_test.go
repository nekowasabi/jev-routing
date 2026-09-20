package proxy

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

// Responses Lite puts client schemas in input.additional_tools, not root.tools.
// Shape verified against Codex CLI traffic and openai/codex responses_lite.rs.
func TestResponsesLiteCatalogLocations(t *testing.T) {
	root := map[string]any{
		"model": "test", "tools": []any{map[string]any{"type": "web_search"}},
		"input": []any{
			map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{
				map[string]any{"type": "namespace", "name": "functions", "tools": []any{
					map[string]any{"type": "function", "name": "grep_files"},
					map[string]any{"type": "function", "name": "read_file"},
				}},
				map[string]any{"type": "namespace", "name": "mcp__example", "tools": []any{
					map[string]any{"type": "function", "name": "lookup"},
				}},
			}},
			map[string]any{"type": "message", "role": "user", "content": "Search the repo for the definition of authMiddleware"},
		},
	}
	for _, withHosted := range []bool{true, false} {
		if !withHosted {
			delete(root, "tools")
		}
		body, _ := json.Marshal(root)
		out, stats, err := RewriteWith(nil, body, host.Codex, nil, localOpt())
		if err != nil || !stats.Changed || stats.ToolBefore != 2 || stats.ToolAfter != 1 {
			t.Fatalf("hosted=%v stats=%+v err=%v", withHosted, stats, err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(root["tools"], got["tools"]) {
			t.Fatal("top-level hosted tools moved or changed")
		}
		catalog := inputToolCatalogs(got)[0]
		if catalog["role"] != "developer" {
			t.Fatal("catalog role lost")
		}
		defs := asSlice(catalog["tools"])
		if len(defs) != 2 {
			t.Fatalf("catalog lost local or external namespace: %s", out)
		}
		local := asSlice(defs[0].(map[string]any)["tools"])
		if len(local) != 1 || toolNameOf(local[0].(map[string]any)) != stats.Chosen {
			t.Fatalf("wrong selected tools: %s", out)
		}
		original := asSlice(inputToolCatalogs(root)[0]["tools"])
		if !reflect.DeepEqual(defs[1], original[1]) {
			t.Fatal("external namespace changed")
		}
	}
}
