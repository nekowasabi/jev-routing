package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func claudeAdviseReq(msgs ...any) map[string]any {
	return map[string]any{
		"model":              "claude-opus-5-5",
		"system":             []any{map[string]any{"type": "text", "text": "you are claude", "cache_control": map[string]any{"type": "ephemeral"}}},
		"tools":              []any{map[string]any{"name": "Read"}, map[string]any{"name": "Grep"}, map[string]any{"name": "Bash"}},
		"tool_choice":        map[string]any{"type": "auto"},
		"thinking":           map[string]any{"type": "adaptive"},
		"context_management": map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015"}}},
		"messages":           msgs,
	}
}

func claudeToolTurn(id string) []any {
	return []any{
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Grep", "input": map[string]any{"pattern": "x"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "hit", "cache_control": map[string]any{"type": "ephemeral"}}}},
	}
}

func rewriteClaude(t *testing.T, opt Options, choice string, req map[string]any) (map[string]any, []byte, RewriteStats) {
	t.Helper()
	raw, _ := json.Marshal(req)
	out, stats, err := RewriteWith(t.Context(), raw, host.Claude, jevAnswers(t, choice, 0.95, 0.9, 0.9, nil), opt)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	return got, out, stats
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestClaudeAdviseKeepsRequestAndHistoryStable(t *testing.T) {
	for choice, want := range map[string]string{"Grep": "suggested next step: Grep (other tools remain available).", plan.Respond: "suggested next step: reply to the user without calling a tool."} {
		opt := steerOpt()
		opt.SelectionMode = SelectionJev
		prompt := map[string]any{"role": "user", "content": "find where sessions are stored"}
		turn1 := append([]any{prompt}, claudeToolTurn("toolu_1")...)
		req1 := claudeAdviseReq(turn1...)
		got1, _, stats := rewriteClaude(t, opt, choice, req1)
		if stats.Apply != applyAdvise || stats.Chosen != choice || !stats.Changed || stats.ToolAfter != stats.ToolBefore {
			t.Fatalf("%s: stats %+v", choice, stats)
		}
		for _, key := range []string{"tools", "tool_choice", "system", "thinking", "context_management"} {
			if jsonOf(got1[key]) != jsonOf(req1[key]) {
				t.Fatalf("%s: %s changed: %s", choice, key, jsonOf(got1[key]))
			}
		}
		msgs1 := asSlice(got1["messages"])
		last := asSlice(msgs1[2].(map[string]any)["content"])
		hint := "<system-reminder>jev-routing: " + want + "</system-reminder>"
		if len(last) != 2 || jsonOf(last[1]) != jsonOf(map[string]any{"type": "text", "text": hint}) {
			t.Fatalf("%s: last user message %s", choice, jsonOf(last))
		}
		if jsonOf(msgs1[:2]) != jsonOf(turn1[:2]) {
			t.Fatalf("%s: earlier messages changed", choice)
		}

		// Claude Code resends history without the hint and adds a turn.
		req2 := claudeAdviseReq(append(turn1, claudeToolTurn("toolu_2")...)...)
		got2, out2, _ := rewriteClaude(t, opt, choice, req2)
		msgs2 := asSlice(got2["messages"])
		if jsonOf(msgs2[:3]) != jsonOf(msgs1) {
			t.Fatalf("%s: shared prefix differs:\n%s\n%s", choice, jsonOf(msgs2[:3]), jsonOf(msgs1))
		}
		if newest := asSlice(msgs2[4].(map[string]any)["content"]); len(newest) != 2 || !strings.Contains(jsonOf(newest[1]), "jev-routing") {
			t.Fatalf("%s: new turn has no hint: %s", choice, jsonOf(newest))
		}
		if _, again, _ := rewriteClaude(t, opt, choice, req2); string(again) != string(out2) {
			t.Fatalf("%s: second rewrite is not byte-identical", choice)
		}
	}
}

func TestClaudeFirstTurnGetsNoHint(t *testing.T) {
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	req := claudeAdviseReq(map[string]any{"role": "user", "content": "find where sessions are stored"})
	raw, _ := json.Marshal(req)
	out, stats, err := RewriteWith(t.Context(), raw, host.Claude, jevAnswers(t, "Grep", 0.95, 0.9, 0.9, nil), opt)
	if err != nil || stats.Apply == applyAdvise || string(out) != string(raw) {
		t.Fatalf("first turn changed: %+v err=%v\n%s", stats, err, out)
	}
}

func TestClaudeAdviseBeforeTrailingSystemMessage(t *testing.T) {
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	msgs := append(claudeToolTurn("toolu_1"), map[string]any{"role": "system", "content": "runtime note"})
	got, _, stats := rewriteClaude(t, opt, "Read", claudeAdviseReq(msgs...))
	if stats.Apply != applyAdvise {
		t.Fatalf("advice was not applied: %+v", stats)
	}
	out := asSlice(got["messages"])
	result := asSlice(out[1].(map[string]any)["content"])
	if len(result) != 2 || !strings.Contains(jsonOf(result[1]), "suggested next step: Read") {
		t.Fatalf("tool result has no advice: %s", jsonOf(result))
	}
	if jsonOf(out[2]) != jsonOf(msgs[2]) {
		t.Fatalf("trailing system message changed: %s", jsonOf(out[2]))
	}
}

func TestClaudeAdviseDoesNotAttachToOldToolResult(t *testing.T) {
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	msgs := append(claudeToolTurn("toolu_1"), map[string]any{"role": "user", "content": "new request"})
	_, _, stats := rewriteClaude(t, opt, "Read", claudeAdviseReq(msgs...))
	if stats.Apply == applyAdvise {
		t.Fatalf("advice attached to an earlier turn: %+v", stats)
	}
}
