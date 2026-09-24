package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func clearOpt() Options {
	o := DefaultOptions()
	o.ClaudeClearToolUses = true
	return o
}

// --- request-side: appending the edit ---

func TestAddClearToolUsesEditKeepsExistingEditsAndOrder(t *testing.T) {
	root := map[string]any{
		"context_management": map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}},
	}
	if !addClearToolUsesEdit(root, clearOpt()) {
		t.Fatal("want added")
	}
	edits := root["context_management"].(map[string]any)["edits"].([]any)
	if len(edits) != 2 {
		t.Fatalf("edits=%v", edits)
	}
	if edits[0].(map[string]any)["type"] != "clear_thinking_20251015" {
		t.Fatalf("existing edit order changed: %v", edits)
	}
	added := edits[1].(map[string]any)
	if added["type"] != clearToolUsesEditType {
		t.Fatalf("appended edit type=%v", added["type"])
	}
	trigger := added["trigger"].(map[string]any)
	if trigger["type"] != "input_tokens" || trigger["value"] != defaultClearTrigger {
		t.Fatalf("trigger=%v", trigger)
	}
	keep := added["keep"].(map[string]any)
	if keep["type"] != "tool_uses" || keep["value"] != defaultClearKeep {
		t.Fatalf("keep=%v", keep)
	}
	atLeast := added["clear_at_least"].(map[string]any)
	if atLeast["type"] != "input_tokens" || atLeast["value"] != defaultClearAtLeast {
		t.Fatalf("clear_at_least=%v", atLeast)
	}
}

func TestClearToolUsesEditExcludeToolsOmittedWhenUnset(t *testing.T) {
	edit := clearToolUsesEdit(clearOpt())
	if _, ok := edit["exclude_tools"]; ok {
		t.Fatalf("exclude_tools present when unset: %v", edit)
	}
}

func TestClearToolUsesEditExcludeToolsSet(t *testing.T) {
	opt := clearOpt()
	opt.ClaudeClearExclude = []string{"web_search", "bash"}
	edit := clearToolUsesEdit(opt)
	got, ok := edit["exclude_tools"].([]any)
	if !ok || len(got) != 2 || got[0] != "web_search" || got[1] != "bash" {
		t.Fatalf("exclude_tools=%v", edit["exclude_tools"])
	}
}

func TestAddClearToolUsesEditNoopWhenAlreadyPresent(t *testing.T) {
	root := map[string]any{
		"context_management": map[string]any{"edits": []any{map[string]any{"type": "clear_tool_uses_20250919", "trigger": map[string]any{"type": "input_tokens", "value": 1}}}},
	}
	if addClearToolUsesEdit(root, clearOpt()) {
		t.Fatal("want no-op when a clear_tool_uses_* edit already exists")
	}
	edits := root["context_management"].(map[string]any)["edits"].([]any)
	if len(edits) != 1 {
		t.Fatalf("edits mutated: %v", edits)
	}
}

func TestAddClearToolUsesEditCreatesContextManagement(t *testing.T) {
	root := map[string]any{"model": "claude-opus-5-5"}
	if !addClearToolUsesEdit(root, clearOpt()) {
		t.Fatal("want added")
	}
	cm, ok := root["context_management"].(map[string]any)
	if !ok {
		t.Fatalf("context_management not created: %v", root)
	}
	edits := cm["edits"].([]any)
	if len(edits) != 1 || edits[0].(map[string]any)["type"] != clearToolUsesEditType {
		t.Fatalf("edits=%v", edits)
	}
}

func TestAddClearToolUsesEditLeavesNonObjectContextManagementAlone(t *testing.T) {
	root := map[string]any{"context_management": "unexpected"}
	if addClearToolUsesEdit(root, clearOpt()) {
		t.Fatal("want no-op for a non-object context_management")
	}
	if root["context_management"] != "unexpected" {
		t.Fatalf("context_management changed: %v", root["context_management"])
	}
}

func TestApplyClaudeClearToolUsesOffLeavesBytesUnchanged(t *testing.T) {
	raw := []byte(`{"model":"claude-opus-5-5","messages":[]}`)
	h := http.Header{}
	out, ok := applyClaudeClearToolUses(raw, h, DefaultOptions())
	if ok || string(out) != string(raw) {
		t.Fatalf("off by default must not change bytes: ok=%v out=%s", ok, out)
	}
	if h.Get("anthropic-beta") != "" {
		t.Fatalf("beta header set while off: %q", h.Get("anthropic-beta"))
	}
}

func TestApplyClaudeClearToolUsesPreservesOtherFields(t *testing.T) {
	req := claudeAdviseReq(claudeToolTurn("toolu_1")...)
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	out, ok := applyClaudeClearToolUses(raw, h, clearOpt())
	if !ok {
		t.Fatal("want applied")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"model", "tools", "tool_choice", "system", "thinking", "messages"} {
		if jsonOf(got[key]) != jsonOf(req[key]) {
			t.Fatalf("%s changed: %s", key, jsonOf(got[key]))
		}
	}
	cm := got["context_management"].(map[string]any)
	edits := cm["edits"].([]any)
	if len(edits) != 2 {
		t.Fatalf("edits=%v", edits)
	}
	if h.Get("anthropic-beta") != contextManagementBeta {
		t.Fatalf("beta header=%q", h.Get("anthropic-beta"))
	}
}

func TestOptionsFromEnvClaudeClearExclude(t *testing.T) {
	t.Setenv("JEV_CLAUDE_CLEAR_EXCLUDE", " web_search , bash ,")
	opt, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(opt.ClaudeClearExclude) != 2 || opt.ClaudeClearExclude[0] != "web_search" || opt.ClaudeClearExclude[1] != "bash" {
		t.Fatalf("ClaudeClearExclude=%v", opt.ClaudeClearExclude)
	}
}

func TestOptionsFromEnvClaudeClearExcludeUnsetIsEmpty(t *testing.T) {
	opt, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(opt.ClaudeClearExclude) != 0 {
		t.Fatalf("ClaudeClearExclude=%v", opt.ClaudeClearExclude)
	}
}

// --- header ---

func TestAddContextManagementBetaHeaderPreservesExistingValues(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
	addContextManagementBeta(h)
	if h.Get("anthropic-beta") != "claude-code-20250219,oauth-2025-04-20,"+contextManagementBeta {
		t.Fatalf("beta=%q", h.Get("anthropic-beta"))
	}
}

func TestAddContextManagementBetaHeaderNoDuplicate(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-beta", "claude-code-20250219,"+contextManagementBeta)
	addContextManagementBeta(h)
	if h.Get("anthropic-beta") != "claude-code-20250219,"+contextManagementBeta {
		t.Fatalf("beta duplicated: %q", h.Get("anthropic-beta"))
	}
}

func TestAddContextManagementBetaHeaderEmpty(t *testing.T) {
	h := http.Header{}
	addContextManagementBeta(h)
	if h.Get("anthropic-beta") != contextManagementBeta {
		t.Fatalf("beta=%q", h.Get("anthropic-beta"))
	}
}

// --- response parsing: JSON and SSE, matching /tmp/claude-ctxedit/inject_merge.log ---

func TestExtractClearEditsJSON(t *testing.T) {
	var obj map[string]any
	body := `{"context_management":{"applied_edits":[{"type":"clear_tool_uses_20250919","cleared_input_tokens":58,"cleared_tool_uses":2}]}}`
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatal(err)
	}
	toolUses, inputTokens, found := extractClearEdits(obj)
	if !found || toolUses != 2 || inputTokens != 58 {
		t.Fatalf("toolUses=%d inputTokens=%d found=%v", toolUses, inputTokens, found)
	}
}

func TestExtractClearEditsEmptyAppliedEdits(t *testing.T) {
	var obj map[string]any
	_ = json.Unmarshal([]byte(`{"context_management":{"applied_edits":[]}}`), &obj)
	_, _, found := extractClearEdits(obj)
	if found {
		t.Fatal("empty applied_edits must not be found")
	}
}

func TestClearEditCollectorJSON(t *testing.T) {
	c := newClearEditCollector("application/json")
	c.Write([]byte(`{"context_management":{"applied_edits":[{"type":"clear_tool_uses_20250919","cleared_input_tokens":58,"cleared_tool_uses":2}]}}`))
	toolUses, inputTokens, found := c.Finish()
	if !found || toolUses != 2 || inputTokens != 58 {
		t.Fatalf("toolUses=%d inputTokens=%d found=%v", toolUses, inputTokens, found)
	}
}

func TestClearEditCollectorSSE(t *testing.T) {
	c := newClearEditCollector("text/event-stream")
	c.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
	c.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":58},\"context_management\":{\"applied_edits\":[{\"type\":\"clear_tool_uses_20250919\",\"cleared_input_tokens\":58,\"cleared_tool_uses\":2}]}}\n\n"))
	toolUses, inputTokens, found := c.Finish()
	if !found || toolUses != 2 || inputTokens != 58 {
		t.Fatalf("toolUses=%d inputTokens=%d found=%v", toolUses, inputTokens, found)
	}
}

func TestClearEditCollectorSSENoAppliedEdits(t *testing.T) {
	c := newClearEditCollector("text/event-stream")
	c.Write([]byte("data: {\"type\":\"message_delta\",\"context_management\":{\"applied_edits\":[]}}\n\n"))
	_, _, found := c.Finish()
	if found {
		t.Fatal("empty applied_edits must not be found")
	}
}

// --- end-to-end through Server.Handler ---

func claudeClearReq() []byte {
	req := claudeAdviseReq(claudeToolTurn("toolu_1")...)
	raw, _ := json.Marshal(req)
	return raw
}

func newClaudeTestServer(t *testing.T, opt Options, upstream *httptest.Server) *Server {
	t.Helper()
	t.Setenv("ANTHROPIC_UPSTREAM", upstream.URL)
	srv, err := NewWithOptions("127.0.0.1:0", host.Claude, nil, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestHandlerClaudeClearAppliesEvenWithAdviseDisabled covers the placement
// requirement: the edit must apply on the fast path where RewriteWith
// returns the body unchanged with reasonClaudeAdviseOff (rewrite.go), since
// JEV_CLAUDE_ADVISE defaults off (docs/MEMO.md) and this feature is meant to
// be measured independent of advise.
func TestHandlerClaudeClearAppliesEvenWithAdviseDisabled(t *testing.T) {
	var gotBody map[string]any
	var gotBeta string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"context_management":{"applied_edits":[{"type":"clear_tool_uses_20250919","cleared_input_tokens":58,"cleared_tool_uses":2}]}}`))
	}))
	defer upstream.Close()

	opt := clearOpt()
	if opt.ClaudeAdvise {
		t.Fatal("test assumes ClaudeAdvise defaults off")
	}
	srv := newClaudeTestServer(t, opt, upstream)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(claudeClearReq())))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotBeta != contextManagementBeta {
		t.Fatalf("beta header not forwarded: %q", gotBeta)
	}
	cm, ok := gotBody["context_management"].(map[string]any)
	if !ok {
		t.Fatalf("context_management missing from forwarded body: %v", gotBody)
	}
	edits := cm["edits"].([]any)
	found := false
	for _, e := range edits {
		if e.(map[string]any)["type"] == clearToolUsesEditType {
			found = true
		}
	}
	if !found {
		t.Fatalf("clear_tool_uses edit missing: %v", edits)
	}

	events, _, _, _ := srv.Events().Snapshot(0)
	if len(events) == 0 || events[len(events)-1].ClearedToolUses != 2 || events[len(events)-1].ClearedInputTokens != 58 {
		t.Fatalf("event not recorded: %+v", events)
	}
}

func TestHandlerClaudeClearOffByDefaultDoesNotTouchRequest(t *testing.T) {
	var gotBody string
	var gotBeta string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	srv := newClaudeTestServer(t, DefaultOptions(), upstream)
	raw := claudeClearReq()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if gotBeta != "" {
		t.Fatalf("beta header added while off: %q", gotBeta)
	}
	if gotBody != string(raw) {
		t.Fatalf("body changed while off:\nwant %s\ngot  %s", raw, gotBody)
	}
}

func TestHandlerClaudeClearSkipsCountTokens(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("anthropic-beta") != "" {
			t.Errorf("beta header set on count_tokens: %q", r.Header.Get("anthropic-beta"))
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":1}`))
	}))
	defer upstream.Close()

	srv := newClaudeTestServer(t, clearOpt(), upstream)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(string(claudeClearReq())))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if !called {
		t.Fatal("count_tokens request was not forwarded")
	}
}
