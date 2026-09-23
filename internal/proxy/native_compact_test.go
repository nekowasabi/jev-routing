package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestNativeCompactionKindMarkers(t *testing.T) {
	codex := map[string]any{"input": []any{map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary."}},
	}}}
	if nativeCompactionKind(codex) != "codex" {
		t.Fatalf("codex kind %q", nativeCompactionKind(codex))
	}
	grok := map[string]any{"messages": []any{map[string]any{
		"role": "user", "content": "Your task is to produce a faithful summary so that a successor assistant can continue the work seamlessly after the earlier turns are discarded.",
	}}}
	if nativeCompactionKind(grok) != "grok" {
		t.Fatalf("grok kind %q", nativeCompactionKind(grok))
	}
	plain := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "fix the test"}}}
	if nativeCompactionKind(plain) != "" {
		t.Fatalf("plain request must not compact natively: %q", nativeCompactionKind(plain))
	}
	// A marker quoted inside a tool result is not the host's compaction prompt.
	quoted := map[string]any{"input": []any{
		map[string]any{"type": "function_call_output", "call_id": "c", "output": "CONTEXT CHECKPOINT COMPACTION"},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "continue"}}},
	}}
	if nativeCompactionKind(quoted) != "" {
		t.Fatal("tool output must not trigger native compaction")
	}
}

func TestResponsesShellPairCompacts(t *testing.T) {
	stale := strings.Repeat("shell output\n", 400)
	msgs := []any{
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "inspect the tree"}}},
		map[string]any{"type": "local_shell_call", "call_id": "sh1", "action": map[string]any{"command": []any{"ls"}}},
		map[string]any{"type": "local_shell_call_output", "call_id": "sh1", "output": stale},
		map[string]any{"type": "apply_patch_call", "call_id": "p1", "input": "*** Begin Patch\n*** End Patch"},
		map[string]any{"type": "apply_patch_call_output", "call_id": "p1", "output": "ok"},
		map[string]any{"type": "mcp_call", "call_id": "m1", "name": "docs", "arguments": "{}"},
		map[string]any{"type": "mcp_call_output", "call_id": "m1", "output": strings.Repeat("doc\n", 200)},
	}
	items, _ := itemsFromMessages(msgs)
	var sawShell, sawPatch, sawMCP bool
	for _, it := range items {
		switch it.Tool {
		case "shell":
			sawShell = true
		case "apply_patch":
			sawPatch = true
		case "docs":
			sawMCP = true
		}
	}
	if !sawShell || !sawPatch || !sawMCP {
		t.Fatalf("missing codex tool items: %+v", items)
	}
	got := applyCompactToMessages(msgs, compact.Result{Decisions: []compact.Decision{
		{ID: "sh1", Action: compact.ActionDrop},
		{ID: "sh1_r", Action: compact.ActionDrop},
		{ID: "m1_r", Action: compact.ActionTruncate},
	}, Items: []compact.Item{{ID: "m1_r", Body: "head\n[jev-compaction truncated]"}}})
	raw := mustJSON(t, got)
	if strings.Contains(raw, "sh1") || strings.Contains(raw, "shell output") {
		t.Fatalf("shell call survived: %s", raw)
	}
	if !strings.Contains(raw, "p1") || !strings.Contains(raw, "jev-compaction truncated") {
		t.Fatalf("patch should stay and mcp result should truncate: %s", raw)
	}
}

func TestGrokSummaryIsVerbatimAndLongEnough(t *testing.T) {
	text := grokSummary("fix auth", "[tool_call read_file] {\"path\":\"a.go\"}\n[tool_result c1] package auth", "[user] fix auth\n[tool_call read_file] {\"path\":\"a.go\"}")
	if !strings.HasPrefix(text, "<summary>\n1. Primary Request and Intent:") || !strings.Contains(text, "</summary>") {
		t.Fatalf("summary shape: %s", text)
	}
	if !strings.Contains(text, "package auth") || !strings.Contains(text, "a.go") {
		t.Fatalf("verbatim tool payload missing: %s", text)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(text, "<summary>\n"), "</summary>")
	if utf8.RuneCountInString(inner) < 500 {
		t.Fatalf("cleaned seed would be degenerate: %d", utf8.RuneCountInString(inner))
	}
}

func TestCodexNativeCompactionDoesNotHitUpstream(t *testing.T) {
	output := "HEADMARKER\n" + strings.Repeat("x", 2000) + "\nTAILMARKER"
	payload := map[string]any{
		"model": "gpt-test",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Fix the failing test. Never edit src/generated."}}},
			map[string]any{"type": "local_shell_call", "call_id": "old", "action": map[string]any{"command": []any{"ls", "-la"}}},
			map[string]any{"type": "local_shell_call_output", "call_id": "old", "output": output},
			map[string]any{"type": "apply_patch_call", "call_id": "p1", "input": "*** Begin Patch"},
			map[string]any{"type": "apply_patch_call_output", "call_id": "p1", "output": "patched"},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary."}}},
		},
	}
	raw, _ := json.Marshal(payload)
	s, upstream := testProxy(t, host.Codex, nil, DefaultOptions())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if *upstream != 0 {
		t.Fatal("native compaction must not call the summarizer")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, "response.completed") || !strings.Contains(out, "output_text") {
		t.Fatalf("want responses SSE, got %s", out)
	}
	if !strings.Contains(out, "Never edit src/generated") || !strings.Contains(out, "ls") || !strings.Contains(out, "HEADMARKER") {
		t.Fatalf("verbatim transcript missing: %s", out)
	}
	if strings.Contains(out, "TAILMARKER") || strings.Contains(out, "CONTEXT CHECKPOINT COMPACTION") {
		t.Fatalf("stale tail or compaction prompt leaked: %s", out)
	}
	if !strings.Contains(out, "jev-compaction truncated") {
		t.Fatalf("stale shell result was not truncated: %s", out)
	}
}

func TestGrokNativeCompactionChatJSON(t *testing.T) {
	body := `{
	  "model":"grok-test",
	  "messages":[
	    {"role":"user","content":"keep the auth bug thread"},
	    {"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"auth.go\"}"}}]},
	    {"role":"tool","tool_call_id":"c1","content":"func Auth() { return nil }"},
	    {"role":"user","content":"<summary_request>\nPlease summarize the conversation so far.\n</summary_request>"}
	  ]
	}`
	s, upstream := testProxy(t, host.Grok, nil, DefaultOptions())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if *upstream != 0 {
		t.Fatal("grok compaction must not call upstream")
	}
	out := rec.Body.String()
	var parsed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("chat json: %v %s", err, out)
	}
	choices, _ := parsed["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("want chat summary: %s", out)
	}
	msg, _ := choices[0].(map[string]any)["message"].(map[string]any)
	content, _ := msg["content"].(string)
	if choices[0].(map[string]any)["finish_reason"] != "stop" || !strings.Contains(content, "<summary>") {
		t.Fatalf("want chat summary: %s", content)
	}
	if !strings.Contains(content, "auth.go") || !strings.Contains(content, "func Auth()") {
		t.Fatalf("verbatim grok transcript missing: %s", content)
	}
	if utf8.RuneCountInString(content) < 500 {
		t.Fatalf("summary shorter than grok's degenerate floor: %d", utf8.RuneCountInString(content))
	}
}

func TestNativeCompactionRespectsOff(t *testing.T) {
	opt := DefaultOptions()
	opt.Compaction = CompactionOff
	body := `{"model":"gpt-test","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"CONTEXT CHECKPOINT COMPACTION"}]}]}`
	s, upstream := testProxy(t, host.Codex, nil, opt)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if *upstream != 1 {
		t.Fatalf("compaction off must forward, upstream=%d body=%s", *upstream, rec.Body.String())
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
