package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestGrokRewriteStripsCatalog(t *testing.T) {
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it, fix the assertion in place, and re-run the tests."},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "search_replace"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "mcp_edit_file"}},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter != 1 {
		t.Fatalf("tools after=%d chosen=%s", stats.ToolAfter, stats.Chosen)
	}
	if stats.Chosen != "grep" {
		t.Fatalf("chosen %s", stats.Chosen)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	tools := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("kept %d tools", len(tools))
	}
}

func TestClaudeRespondStripsAll(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "you are claude",
		"messages": []any{
			map[string]any{"role": "user", "content": "thanks, that's all"},
		},
		"tools": []any{
			map[string]any{"name": "Read"},
			map[string]any{"name": "Bash"},
		},
		"thinking": map[string]any{"type": "enabled", "budget_tokens": 8000},
	}
	raw, _ := json.Marshal(req)
	_, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("unknown first turn must not strip the catalog %+v", stats)
	}
}

func TestExploreKeepsAgent(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "you are claude",
		"messages": []any{
			map[string]any{"role": "user", "content": "Explore the auth package thoroughly and report how sessions are stored."},
		},
		"tools": []any{
			map[string]any{"name": "Read", "description": "Read a file"},
			map[string]any{"name": "Grep", "description": "Search file contents"},
			map[string]any{"name": "Agent", "description": "Launch a new agent to handle complex multi-step tasks autonomously"},
			map[string]any{"name": "Bash", "description": "Run a shell command"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "Agent" {
		t.Fatalf("chosen %s %+v", stats.Chosen, stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	tools := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("kept %d", len(tools))
	}
	if tools[0].(map[string]any)["name"] != "Agent" {
		t.Fatalf("kept %+v", tools[0])
	}
}

func TestUnknownToolNameFailsOpen(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "x",
		"messages": []any{
			map[string]any{"role": "user", "content": "Explore the repo."},
		},
		"tools": []any{
			map[string]any{"name": "Read"},
			map[string]any{"name": "Task", "description": "Launch a new agent"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("must not empty the catalog %+v", stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if len(got["tools"].([]any)) == 0 {
		t.Fatal("empty tools")
	}
}

func TestCompactTruncatesStaleToolResult(t *testing.T) {
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
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CharsAfter >= stats.CharsBefore && stats.CompactDropped == 0 {
		t.Fatalf("compaction did nothing %+v outlen=%d inlen=%d", stats, len(out), len(raw))
	}
}

func TestGrokMissingCatalogDoesNotWriteEmptyTools(t *testing.T) {
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "フィボナッチ数列を出力するコードを作成して"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "passthrough:no-catalog" {
		t.Fatalf("chosen %s %+v", stats.Chosen, stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("must not insert tools key: %+v", got["tools"])
	}
}

func TestFibonacciWithCatalogDoesNotStrip(t *testing.T) {
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "フィボナッチ数列を出力するコードを作成して"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search_replace"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "task"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("must not empty catalog %+v", stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if len(got["tools"].([]any)) == 0 {
		t.Fatal("stripped all tools")
	}
}
