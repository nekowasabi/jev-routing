package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

// countingJevAnswers wraps jevAnswers with a call counter so tests can assert
// Jev was (or was not) invoked, not just how the response was applied.
func countingJevAnswers(t *testing.T, choice string) (*jev.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	c := jevAnswers(t, choice, 0.95, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"next_tool":  map[string]any{"type": "choice", "choice": choice, "confidence": 0.95, "probabilities": adoptTestProbs(choice)},
				"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
			},
		})
	})
	return c, &calls
}

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

// TestClaudeAdviseSkipsJevWithoutTarget covers change 1: a Claude request
// with no trailing tool_result has nowhere for adviseClaude to attach its
// hint, so Jev must not be called at all. Once a tool_use/tool_result pair
// exists, Jev is asked and the advice is inserted as before.
func TestClaudeAdviseSkipsJevWithoutTarget(t *testing.T) {
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	client, calls := countingJevAnswers(t, "Grep")

	prompt := map[string]any{"role": "user", "content": "find where sessions are stored"}
	req1 := claudeAdviseReq(prompt)
	raw1, _ := json.Marshal(req1)
	out1, stats1, err := RewriteWith(t.Context(), raw1, host.Claude, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("jev called with no advise target: %d calls", calls.Load())
	}
	if stats1.Apply == applyAdvise || stats1.Reason != reasonNoAdviseTarget || string(out1) != string(raw1) {
		t.Fatalf("turn1: unexpected stats/output: %+v out=%s", stats1, out1)
	}

	turn2 := append([]any{prompt}, claudeToolTurn("toolu_1")...)
	raw2, _ := json.Marshal(claudeAdviseReq(turn2...))
	out2, stats2, err := RewriteWith(t.Context(), raw2, host.Claude, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("jev not called with an advise target: %d calls", calls.Load())
	}
	if stats2.Apply != applyAdvise || !strings.Contains(string(out2), "jev-routing") {
		t.Fatalf("turn2: advice not applied: %+v out=%s", stats2, out2)
	}
}

// TestClaudeAdviseOffByDefaultSkipsJev covers the decision recorded in
// docs/MEMO.md: a Jev judgment for Claude's advise path costs a median
// 7,406 input tokens but never narrows tools[], so it must not fire unless
// JEV_CLAUDE_ADVISE=on (Options.ClaudeAdvise) is explicitly set.
func TestClaudeAdviseOffByDefaultSkipsJev(t *testing.T) {
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	opt.ClaudeAdvise = false
	client, calls := countingJevAnswers(t, "Grep")

	prompt := map[string]any{"role": "user", "content": "find where sessions are stored"}
	turn2 := append([]any{prompt}, claudeToolTurn("toolu_1")...)
	raw, _ := json.Marshal(claudeAdviseReq(turn2...))
	out, stats, err := RewriteWith(t.Context(), raw, host.Claude, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("jev called despite advise off: %d calls", calls.Load())
	}
	if stats.Apply == applyAdvise || stats.Reason != reasonClaudeAdviseOff || string(out) != string(raw) {
		t.Fatalf("unexpected stats/output: %+v out=%s", stats, out)
	}
}

// TestClaudeAdviseOnCallsJev is the counterpart of
// TestClaudeAdviseOffByDefaultSkipsJev: with the option enabled, Jev is
// asked and the advice is inserted exactly as before this change.
func TestClaudeAdviseOnCallsJev(t *testing.T) {
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	opt.ClaudeAdvise = true
	client, calls := countingJevAnswers(t, "Grep")

	prompt := map[string]any{"role": "user", "content": "find where sessions are stored"}
	turn2 := append([]any{prompt}, claudeToolTurn("toolu_1")...)
	raw, _ := json.Marshal(claudeAdviseReq(turn2...))
	out, stats, err := RewriteWith(t.Context(), raw, host.Claude, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("jev not called with advise on: %d calls", calls.Load())
	}
	if stats.Apply != applyAdvise || !strings.Contains(string(out), "jev-routing") {
		t.Fatalf("advice not applied: %+v out=%s", stats, out)
	}
}

// TestCodexFirstTurnStillAsksJev guards change 1's scope: only Claude's
// advise path skips Jev when there is no insertion target. Codex has no
// advise mechanism, so its first turn must ask Jev exactly as before.
func TestCodexFirstTurnStillAsksJev(t *testing.T) {
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	client, calls := countingJevAnswers(t, "grep")
	body, _ := json.Marshal(map[string]any{
		"model": "gpt-5.6-terra",
		"messages": []any{
			map[string]any{"role": "user", "content": "find where sessions are stored"},
		},
		"tools": workTools(),
	})
	if _, _, err := rewriteSteer(body, host.Codex, client); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("codex first turn should still ask jev once: %d calls", calls.Load())
	}
}
