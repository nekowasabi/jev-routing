package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/jev"
)

// clearGateHarness drives a gate=jev Claude proxy against a fake upstream
// whose reported context size is set per request, and a fake Jev.
type clearGateHarness struct {
	t        *testing.T
	srv      *Server
	ctx      atomic.Int64 // input_tokens the upstream reports next
	jevCalls atomic.Int64
	lastEdit bool
	jevState map[string]any
	turns    int // Grep tool turns per request, each result 400 chars (210 tokens)
}

// toolTurn is one assistant tool_use plus its user tool_result of size chars.
func toolTurn(id, name string, size int) []any {
	return []any{
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{"pattern": "x"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("x", size)}}},
	}
}

func newClearGateHarness(t *testing.T, jevReply func(w http.ResponseWriter), key string) *clearGateHarness {
	t.Helper()
	h := &clearGateHarness{t: t, turns: 4}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.lastEdit = false
		if cm, ok := body["context_management"].(map[string]any); ok {
			for _, e := range asSlice(cm["edits"]) {
				if e.(map[string]any)["type"] == clearToolUsesEditType {
					h.lastEdit = true
				}
			}
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprintf(w, `{"usage":{"input_tokens":%d,"output_tokens":5}}`, h.ctx.Load())
	}))
	t.Cleanup(upstream.Close)
	fakeJev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.jevCalls.Add(1)
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		h.jevState, _ = req["state"].(map[string]any)
		jevReply(w)
	}))
	t.Cleanup(fakeJev.Close)
	opt := clearOpt()
	opt.ClaudeClearGate = ClearGateJev
	opt.ClaudeClearTrigger = 1000
	opt.ClaudeClearAtLeast = 200 // keep 3: 4 turns leave one 210-token result clearable
	h.srv = newClaudeTestServer(t, opt, upstream)
	h.srv.Client = &jev.Client{APIKey: key, BaseURL: fakeJev.URL, Model: "m", HTTP: fakeJev.Client()}
	return h
}

func jevChoice(choice string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		fmt.Fprintf(w, `{"answers":{"clear_gate":{"type":"choice","choice":%q,"confidence":0.9,"probabilities":{%q:0.9}}},"usage":{"input_tokens":321,"output_tokens":7}}`, choice, choice)
	}
}

// send posts one request of the conversation whose first message is task and
// returns its event.
func (h *clearGateHarness) send(task string, nextCtx int) Event {
	h.t.Helper()
	msgs := []any{map[string]any{"role": "user", "content": task}}
	for i := 0; i < h.turns; i++ {
		msgs = append(msgs, toolTurn(fmt.Sprintf("toolu_%d", i), "Grep", 400)...)
	}
	raw, _ := json.Marshal(claudeAdviseReq(msgs...))
	h.ctx.Store(int64(nextCtx))
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(raw))))
	if rec.Code != http.StatusOK {
		h.t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	events, _, _, _ := h.srv.Events().Snapshot(0)
	return events[len(events)-1]
}

func TestClearGateOffKeepsEditAndRecordsNoGate(t *testing.T) {
	h := newClearGateHarness(t, jevChoice(clearChoiceKeep), "k")
	h.srv.Options.ClaudeClearGate = ClearGateOff
	ev := h.send("fix the failing test", 5000)
	if !h.lastEdit || ev.ClearGate != "" || h.jevCalls.Load() != 0 {
		t.Fatalf("gate off changed behavior: edit=%v gate=%q jev=%d", h.lastEdit, ev.ClearGate, h.jevCalls.Load())
	}
}

func TestClearGateClearAppliesAfterThresholdOnly(t *testing.T) {
	h := newClearGateHarness(t, jevChoice(clearChoiceClear), "k")
	// First request: no observed size yet. Upstream reports 500 (< trigger 1000).
	if ev := h.send("fix the failing test", 500); h.lastEdit || ev.ClearGate != clearGatePending || ev.JevCalls != 0 {
		t.Fatalf("before threshold: edit=%v ev=%+v", h.lastEdit, ev)
	}
	// Last ctx 500: still pending. Upstream now reports 1200.
	if ev := h.send("fix the failing test", 1200); h.lastEdit || ev.ClearGate != clearGatePending {
		t.Fatalf("below threshold: edit=%v gate=%q", h.lastEdit, ev.ClearGate)
	}
	ev := h.send("fix the failing test", 1200)
	if !h.lastEdit || ev.ClearGate != clearGateClear || h.jevCalls.Load() != 1 {
		t.Fatalf("at threshold: edit=%v gate=%q jev=%d", h.lastEdit, ev.ClearGate, h.jevCalls.Load())
	}
	if ev.JevCalls != 1 || len(ev.JevAttempts) != 1 || ev.JevAttempts[0].InputTokens == nil || *ev.JevAttempts[0].InputTokens != 321 {
		t.Fatalf("Jev usage not on event: %+v", ev.JevAttempts)
	}
	if h.jevState["task"] != "fix the failing test" || h.jevState["tool_calls_total"] != float64(4) {
		t.Fatalf("jev state=%v", h.jevState)
	}
	calls := h.jevState["recent_tool_calls"].([]any)
	if c := calls[0].(map[string]any); c["name"] != "Grep" || c["args"] != `{"pattern":"x"}` {
		t.Fatalf("recent_tool_calls=%v", calls)
	}
	// Later requests keep clearing without asking again, even if ctx drops.
	if ev := h.send("fix the failing test", 100); !h.lastEdit || ev.ClearGate != clearGateClear || ev.JevCalls != 0 || h.jevCalls.Load() != 1 {
		t.Fatalf("after clear: edit=%v gate=%q jev=%d", h.lastEdit, ev.ClearGate, h.jevCalls.Load())
	}
	// A child conversation (different first message) is decided on its own.
	if ev := h.send("survey every file and report", 1200); h.lastEdit || ev.ClearGate != clearGatePending {
		t.Fatalf("child inherited parent decision: edit=%v gate=%q", h.lastEdit, ev.ClearGate)
	}
	h.srv.mu.Lock()
	total := h.srv.JevHTTP
	h.srv.mu.Unlock()
	if total != 1 {
		t.Fatalf("server JevHTTP=%d, want 1 (must equal summed event JevCalls)", total)
	}
}

func TestClearGateKeepNeverClears(t *testing.T) {
	h := newClearGateHarness(t, jevChoice(clearChoiceKeep), "k")
	h.send("survey every file and report", 1200)
	for i := 0; i < 3; i++ {
		if ev := h.send("survey every file and report", 2000); h.lastEdit || ev.ClearGate != clearGateKeep {
			t.Fatalf("keep: edit=%v gate=%q", h.lastEdit, ev.ClearGate)
		}
	}
	if h.jevCalls.Load() != 1 {
		t.Fatalf("jev asked %d times, want 1", h.jevCalls.Load())
	}
}

func TestClearGateJevFailureFailsClosedWithoutReask(t *testing.T) {
	for name, tc := range map[string]struct {
		reply  func(w http.ResponseWriter)
		key    string
		reason string
		calls  int64
	}{
		"http_error": {func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }, "k", reasonCallFailed, 1},
		"invalid":    {jevChoice("something_else"), "k", "invalid_id", 1},
		"no_key":     {jevChoice(clearChoiceClear), "", "no_key", 0},
	} {
		t.Run(name, func(t *testing.T) {
			h := newClearGateHarness(t, tc.reply, tc.key)
			h.send("fix the failing test", 1200)
			for i := 0; i < 2; i++ {
				ev := h.send("fix the failing test", 2000)
				if h.lastEdit || ev.ClearGate != clearGateError || ev.ClearGateReason != tc.reason {
					t.Fatalf("edit=%v gate=%q reason=%q", h.lastEdit, ev.ClearGate, ev.ClearGateReason)
				}
			}
			if h.jevCalls.Load() != tc.calls {
				t.Fatalf("jev asked %d times, want %d", h.jevCalls.Load(), tc.calls)
			}
		})
	}
}

func TestClearGateOptionFromEnv(t *testing.T) {
	t.Setenv("JEV_CLAUDE_CLEAR_GATE", "jev")
	if o, err := OptionsFromEnv(); err != nil || o.ClaudeClearGate != ClearGateJev {
		t.Fatalf("o=%q err=%v", o.ClaudeClearGate, err)
	}
	t.Setenv("JEV_CLAUDE_CLEAR_GATE", "maybe")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("want error for invalid JEV_CLAUDE_CLEAR_GATE")
	}
	if DefaultOptions().ClaudeClearGate != ClearGateOff {
		t.Fatal("default gate must be off")
	}
}

func TestClearGateWaitsForClearableResults(t *testing.T) {
	h := newClearGateHarness(t, jevChoice(clearChoiceClear), "k")
	h.turns = 3 // every result is within the last keep=3 tool uses
	for i := 0; i < 3; i++ {
		if ev := h.send("fix the failing test", 5000); h.lastEdit || ev.ClearGate != clearGatePending {
			t.Fatalf("ctx over trigger but nothing clearable: edit=%v gate=%q", h.lastEdit, ev.ClearGate)
		}
	}
	if h.jevCalls.Load() != 0 {
		t.Fatalf("asked with eligible < clear_at_least: %d", h.jevCalls.Load())
	}
	h.turns = 4 // one 210-token result is now outside keep: crosses 200
	if ev := h.send("fix the failing test", 5000); !h.lastEdit || ev.ClearGate != clearGateClear {
		t.Fatalf("eligible crossed: edit=%v gate=%q", h.lastEdit, ev.ClearGate)
	}
	h.turns = 6
	h.send("fix the failing test", 5000)
	if h.jevCalls.Load() != 1 {
		t.Fatalf("jev asked %d times, want 1", h.jevCalls.Load())
	}
}

func TestClearGateExcludedToolsNotCounted(t *testing.T) {
	h := newClearGateHarness(t, jevChoice(clearChoiceClear), "k")
	h.srv.Options.ClaudeClearExclude = []string{"Grep"}
	h.turns = 8
	for i := 0; i < 2; i++ {
		if ev := h.send("fix the failing test", 5000); h.lastEdit || ev.ClearGate != clearGatePending {
			t.Fatalf("excluded results counted: edit=%v gate=%q", h.lastEdit, ev.ClearGate)
		}
	}
	if h.jevCalls.Load() != 0 {
		t.Fatalf("asked for excluded-only results: %d", h.jevCalls.Load())
	}
}

func TestClearableTokensRule(t *testing.T) {
	opt := DefaultOptions()
	opt.ClaudeClearKeep = 1
	opt.ClaudeClearExclude = []string{"Grep"}
	msgs := []any{map[string]any{"role": "user", "content": "task"}}
	msgs = append(msgs, toolTurn("a", "Read", 380)...) // counted: 380*10/19 = 200
	msgs = append(msgs, toolTurn("b", "Grep", 380)...) // excluded tool
	msgs = append(msgs, toolTurn("c", "Bash", 380)...) // last keep=1 tool use
	if got := clearableTokens(map[string]any{"messages": msgs}, opt); got != 200 {
		t.Fatalf("clearableTokens=%d, want 200", got)
	}
}
