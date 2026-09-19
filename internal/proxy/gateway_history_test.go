package proxy

import (
	"encoding/json"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestGatewayHistory(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		items := []compact.Item{
			{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "grep", Body: "G"},
			{ID: "c1_r", Kind: compact.KindResult, PairID: "c1", Tool: "grep", Body: "GREP_BODY"},
		}
		got := actionsFromItems(items)
		if len(got) != 1 || got[0].Result != "GREP_BODY" || got[0].Pending {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("parallel", func(t *testing.T) {
		items := []compact.Item{
			{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "grep", Body: "G"},
			{ID: "c2", Kind: compact.KindCall, PairID: "c2", Tool: "read_file", Body: "R"},
			{ID: "c1_r", Kind: compact.KindResult, PairID: "c1", Body: "GREP_BODY"},
			{ID: "c2_r", Kind: compact.KindResult, PairID: "c2", Body: "READ_BODY"},
		}
		got := actionsFromItems(items)
		if len(got) != 2 || got[0].Result != "GREP_BODY" || got[1].Result != "READ_BODY" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("reverse", func(t *testing.T) {
		items := []compact.Item{
			{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "grep", Body: "G"},
			{ID: "c2", Kind: compact.KindCall, PairID: "c2", Tool: "read_file", Body: "R"},
			{ID: "c2_r", Kind: compact.KindResult, PairID: "c2", Body: "READ_BODY"},
			{ID: "c1_r", Kind: compact.KindResult, PairID: "c1", Body: "GREP_BODY"},
		}
		got := actionsFromItems(items)
		if got[0].Result != "GREP_BODY" || got[1].Result != "READ_BODY" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("unknown-id", func(t *testing.T) {
		items := []compact.Item{
			{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "grep", Body: "G"},
			{ID: "x_r", Kind: compact.KindResult, PairID: "unknown", Body: "OTHER"},
		}
		got := actionsFromItems(items)
		if len(got) != 1 || got[0].Result != "" || !got[0].Pending {
			t.Fatalf("unknown id assigned: %+v", got)
		}
	})
	t.Run("incomplete", func(t *testing.T) {
		items := []compact.Item{
			{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "grep", Body: "G"},
		}
		got := actionsFromItems(items)
		if !got[0].Pending {
			t.Fatal("incomplete call should stay pending")
		}
	})
	t.Run("duplicate-last-wins", func(t *testing.T) {
		items := []compact.Item{
			{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "grep", Body: "G"},
			{ID: "c1_r", Kind: compact.KindResult, PairID: "c1", Body: "first"},
			{ID: "c1_r2", Kind: compact.KindResult, PairID: "c1", Body: "second"},
		}
		got := actionsFromItems(items)
		if got[0].Result != "second" {
			t.Fatalf("last result=%q", got[0].Result)
		}
	})
	t.Run("empty-body-complete", func(t *testing.T) {
		items := []compact.Item{
			{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "grep", Body: "G"},
			{ID: "c1_r", Kind: compact.KindResult, PairID: "c1", Body: ""},
		}
		got := actionsFromItems(items)
		if got[0].Pending || got[0].Result != "" {
			t.Fatalf("empty body should be complete: %+v", got)
		}
	})
}

func TestGatewayResponses(t *testing.T) {
	input := []any{
		map[string]any{"role": "user", "content": "The auth middleware test is failing. Find the test, read it, fix the assertion in place, and re-run the tests."},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "grep_files", "arguments": `{"p":"auth"}`},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "GREP_BODY"},
		map[string]any{"type": "function_call", "call_id": "c2", "name": "read_file", "arguments": `{"p":"t"}`},
		map[string]any{"type": "function_call_output", "call_id": "c2", "output": "READ_BODY"},
		map[string]any{"type": "custom_tool_call", "call_id": "c3", "name": "note", "arguments": "{}"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c3", "output": "NOTE"},
	}
	items, user := itemsFromMessages(input)
	acts := actionsFromItems(items)
	if plan.WorkRequest(user) == "" {
		t.Fatal("user empty")
	}
	by := map[string]plan.Action{}
	for _, a := range acts {
		by[a.Tool] = a
	}
	if by["grep_files"].Result != "GREP_BODY" || by["read_file"].Result != "READ_BODY" || by["note"].Result != "NOTE" {
		t.Fatalf("actions=%+v", acts)
	}
	req := map[string]any{
		"model": "gpt-5.6-terra",
		"input": input,
		"tools": []any{
			map[string]any{"type": "function", "name": "grep_files"},
			map[string]any{"type": "function", "name": "read_file"},
			map[string]any{"type": "function", "name": "apply_patch"},
			map[string]any{"type": "function", "name": "exec_command"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Codex, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "apply_patch" && stats.Chosen != "exec_command" && stats.ToolAfter != 1 {
		// After grep+read, remaining is edit (apply_patch) then bash.
		if stats.Chosen != "apply_patch" {
			t.Fatalf("want apply_patch after grep+read, got %+v", stats)
		}
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if stats.Changed && stats.Chosen != "apply_patch" {
		t.Fatalf("chosen=%s stats=%+v", stats.Chosen, stats)
	}
}

func TestGatewayEligibility(t *testing.T) {
	base := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "The auth middleware test is failing."},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}},
		},
	}
	mustPassthrough := func(t *testing.T, req map[string]any, reason string) {
		t.Helper()
		raw, _ := json.Marshal(req)
		out, stats, err := Rewrite(raw, host.Grok, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(raw) {
			t.Fatalf("body mutated for %s", reason)
		}
		if stats.Reason != reason && stats.Chosen != "passthrough:"+reason {
			t.Fatalf("reason=%s chosen=%s want %s", stats.Reason, stats.Chosen, reason)
		}
	}
	t.Run("none", func(t *testing.T) {
		req := cloneMap(base)
		req["tool_choice"] = "none"
		mustPassthrough(t, req, reasonExplicitToolChoice)
	})
	t.Run("required", func(t *testing.T) {
		req := cloneMap(base)
		req["tool_choice"] = "required"
		mustPassthrough(t, req, reasonExplicitToolChoice)
	})
	t.Run("named", func(t *testing.T) {
		req := cloneMap(base)
		req["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "grep"}}
		mustPassthrough(t, req, reasonExplicitToolChoice)
	})
	t.Run("previous_response_id", func(t *testing.T) {
		req := map[string]any{"model": "x", "input": []any{}, "previous_response_id": "resp_1", "tools": base["tools"]}
		mustPassthrough(t, req, reasonPreviousResponse)
	})
	t.Run("duplicate", func(t *testing.T) {
		req := cloneMap(base)
		req["tools"] = []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
		}
		mustPassthrough(t, req, reasonDuplicateNames)
	})
	t.Run("namespaced", func(t *testing.T) {
		req := cloneMap(base)
		req["tools"] = []any{
			map[string]any{"name": "Read"},
			map[string]any{"name": "mcp__slack__post_message"},
		}
		raw, _ := json.Marshal(req)
		_, stats, err := Rewrite(raw, host.Grok, nil)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Reason == reasonNamespacedTools {
			t.Fatalf("mixed local+MCP must stay filterable: %+v", stats)
		}
	})
	t.Run("mcp-namespace-only", func(t *testing.T) {
		req := map[string]any{
			"model": "x",
			"input": []any{map[string]any{"role": "user", "content": "hi"}},
			"tools": []any{
				map[string]any{"type": "namespace", "name": "mcp__slack", "tools": []any{
					map[string]any{"type": "function", "name": "post_message"},
				}},
			},
		}
		mustPassthrough(t, req, reasonNamespacedTools)
	})
	t.Run("invalid-json", func(t *testing.T) {
		out, stats, err := Rewrite([]byte("{"), host.Grok, nil)
		if err == nil {
			t.Fatal("want error")
		}
		if string(out) != "{" || stats.Reason != reasonNotJSON {
			t.Fatalf("out=%s stats=%+v", out, stats)
		}
	})
}

func TestGatewayGrokNames(t *testing.T) {
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Please run the tests"},
		},
		"tools": []any{
			grokFn("read_file", "read"),
			grokFn("grep", "search"),
			grokFn("run_terminal_command", "shell"),
			grokFn("spawn_subagent", "agent"),
		},
	}
	raw, _ := json.Marshal(req)
	_, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "run_terminal_command" {
		t.Fatalf("chosen=%s", stats.Chosen)
	}
	req2 := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Please run the tests"},
		},
		"tools": []any{
			grokFn("read_file", "read"),
			grokFn("run_terminal_cmd", "shell"),
			grokFn("task", "agent"),
		},
	}
	raw, _ = json.Marshal(req2)
	_, stats, err = Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "run_terminal_cmd" {
		t.Fatalf("legacy chosen=%s", stats.Chosen)
	}
}
