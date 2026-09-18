package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
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

func TestGrokRewriteDoesNotSendReasoningNone(t *testing.T) {
	req := map[string]any{
		"model": "grok-4.6",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"reasoning":        map[string]any{"effort": "high"},
		"reasoning_effort": "high",
	}
	raw, _ := json.Marshal(req)
	out, _, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"none"`) {
		t.Fatalf("xAI rejects effort none: %s", out)
	}
	r, ok := got["reasoning"].(map[string]any)
	if !ok || r["effort"] != "low" {
		t.Fatalf("reasoning=%v", got["reasoning"])
	}
	if got["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort=%v", got["reasoning_effort"])
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

func TestCompactDropsMessageWhenAllBlocksDropped(t *testing.T) {
	res := compact.Result{Decisions: []compact.Decision{{ID: "t1", Action: compact.ActionDrop}}}
	msgs := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "dropped"},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "kept"},
		}},
	}
	out := applyCompactToMessages(msgs, res)
	if len(out) != 1 {
		t.Fatalf("want 1 message, got %d: %+v", len(out), out)
	}
	m := out[0].(map[string]any)
	if c, _ := m["content"].([]any); len(c) != 1 {
		t.Fatalf("wrong message survived: %+v", m)
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

func TestMCPToolsSurviveUnknownPrompt(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "claude",
		"messages": []any{
			map[string]any{"role": "user", "content": "Slack に今日のまとめを投稿して"},
		},
		"tools": []any{
			map[string]any{"name": "Read"},
			map[string]any{"name": "Bash"},
			map[string]any{"name": "mcp__slack__post_message", "description": "Post a message to Slack"},
			map[string]any{"name": "mcp__github__create_issue", "description": "Create a GitHub issue"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("MCP catalog was stripped %+v", stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	kept := map[string]bool{}
	for _, rawTool := range got["tools"].([]any) {
		kept[rawTool.(map[string]any)["name"].(string)] = true
	}
	if stats.Chosen == "passthrough" && !kept["mcp__slack__post_message"] {
		t.Fatal("passthrough dropped the Slack MCP tool")
	}
}

// A high-confidence local decision must not cost a Jev round trip.
func TestRewriteSkipsLiveWhenLocalConfident(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "answers": map[string]any{}})
	}))
	defer srv.Close()
	client := &jev.Client{APIKey: "test", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}
	if !client.Live() {
		t.Fatal("client not live")
	}

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
		},
	}
	raw, _ := json.Marshal(req)
	_, stats, err := Rewrite(raw, host.Grok, client)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "grep" {
		t.Fatalf("chosen %s; want the local decision", stats.Chosen)
	}
	if n := atomic.LoadInt64(&calls); n != 0 {
		t.Fatalf("made %d live Jev requests; want 0", n)
	}
}
