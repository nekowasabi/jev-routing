package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func codexOpt(max int) Options {
	o := DefaultOptions()
	o.Mode = ModeBaseline
	o.CodexToolOutputMax = max
	return o
}

func TestCodexToolOutputMaxUnderThresholdUnchanged(t *testing.T) {
	req := map[string]any{
		"model": "gpt-5.6-terra",
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": "short output"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := RewriteWith(nil, raw, host.Codex, nil, codexOpt(20000))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) {
		t.Fatalf("body changed under threshold:\nwant %s\ngot  %s", raw, out)
	}
	if stats.ToolOutputTruncated != 0 || stats.ToolOutputTruncatedBytes != 0 {
		t.Fatalf("unexpected truncation stats: %+v", stats)
	}
}

func TestCodexToolOutputMaxDisabledByDefault(t *testing.T) {
	long := strings.Repeat("x", 50000)
	req := map[string]any{
		"input": []any{map[string]any{"type": "function_call_output", "call_id": "c1", "output": long}},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := RewriteWith(nil, raw, host.Codex, nil, codexOpt(0))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) || stats.ToolOutputTruncated != 0 {
		t.Fatalf("expected no-op when disabled: stats=%+v", stats)
	}
}

func TestCodexToolOutputMaxNonCodexUnchanged(t *testing.T) {
	long := strings.Repeat("y", 50000)
	req := map[string]any{
		"input": []any{map[string]any{"type": "function_call_output", "call_id": "c1", "output": long}},
	}
	raw, _ := json.Marshal(req)
	opt := codexOpt(20000)
	out, stats, err := RewriteWith(nil, raw, host.Claude, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) || stats.ToolOutputTruncated != 0 {
		t.Fatalf("expected non-Codex host untouched: stats=%+v", stats)
	}
}

func TestCodexToolOutputMaxTruncatesOverThreshold(t *testing.T) {
	max := 200
	long := strings.Repeat("a", 1000)
	req := map[string]any{
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": long},
			map[string]any{"type": "function_call", "call_id": "c2", "name": "shell"},
		},
	}
	raw, _ := json.Marshal(req)
	out1, stats1, err := RewriteWith(nil, raw, host.Codex, nil, codexOpt(max))
	if err != nil {
		t.Fatal(err)
	}
	if stats1.ToolOutputTruncated != 1 {
		t.Fatalf("want 1 truncated item, got %+v", stats1)
	}
	if stats1.ToolOutputTruncatedBytes != 1000-max {
		t.Fatalf("want %d omitted bytes, got %d", 1000-max, stats1.ToolOutputTruncatedBytes)
	}

	var got map[string]any
	if err := json.Unmarshal(out1, &got); err != nil {
		t.Fatal(err)
	}
	items := got["input"].([]any)
	out := items[0].(map[string]any)["output"].(string)
	if !strings.Contains(out, "[jev-routing: omitted 800 of 1000 bytes from the middle of this tool output") {
		t.Fatalf("missing/mismatched note: %s", out)
	}
	if !strings.HasPrefix(out, "a") || !strings.HasSuffix(out, "a") {
		t.Fatalf("expected head/tail preserved: %s", out)
	}
	// The other item is untouched.
	call := items[1].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "c2" || call["name"] != "shell" {
		t.Fatalf("unrelated item changed: %v", call)
	}

	// Determinism: applying to the same raw input twice yields identical bytes.
	out2, stats2, err := RewriteWith(nil, raw, host.Codex, nil, codexOpt(max))
	if err != nil {
		t.Fatal(err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("non-deterministic output:\n%s\n%s", out1, out2)
	}
	if stats1.ToolOutputTruncated != stats2.ToolOutputTruncated || stats1.ToolOutputTruncatedBytes != stats2.ToolOutputTruncatedBytes {
		t.Fatalf("non-deterministic stats: %+v vs %+v", stats1, stats2)
	}
}

func TestCodexToolOutputMaxAppliesUnderBaselineMode(t *testing.T) {
	long := strings.Repeat("b", 1000)
	req := map[string]any{
		"input": []any{map[string]any{"type": "shell_call_output", "call_id": "c1", "output": long}},
	}
	raw, _ := json.Marshal(req)
	opt := codexOpt(200)
	if opt.Mode != ModeBaseline {
		t.Fatal("test setup expects baseline mode")
	}
	out, stats, err := RewriteWith(nil, raw, host.Codex, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonBaseline {
		t.Fatalf("expected baseline passthrough reason, got %q", stats.Reason)
	}
	if stats.ToolOutputTruncated != 1 {
		t.Fatalf("truncation should still apply under baseline: %+v", stats)
	}
	if string(out) == string(raw) {
		t.Fatal("expected body to be truncated even in baseline mode")
	}
}

func TestCodexToolOutputMaxUTF8Boundary(t *testing.T) {
	// A multibyte rune (3-byte) straddling the cut points on both sides.
	max := 40
	middle := strings.Repeat("x", 200)
	s := strings.Repeat("あ", 20) + middle + strings.Repeat("い", 20)
	req := map[string]any{
		"input": []any{map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": s}},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := RewriteWith(nil, raw, host.Codex, nil, codexOpt(max))
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolOutputTruncated != 1 {
		t.Fatalf("expected truncation: %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	// json.Unmarshal itself would fail on invalid UTF-8 escaped incorrectly,
	// but confirm explicitly that the value round-trips as valid UTF-8 text.
	items := got["input"].([]any)
	text := items[0].(map[string]any)["output"].(string)
	if !json.Valid(mustMarshal(t, text)) {
		t.Fatalf("output is not valid JSON-encodable text: %q", text)
	}
}

func mustMarshal(t *testing.T, s string) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCodexToolOutputMaxContentItemArray(t *testing.T) {
	max := 100
	longText := strings.Repeat("z", 500)
	req := map[string]any{
		"input": []any{
			map[string]any{
				"type":    "function_call_output",
				"call_id": "c1",
				"output": []any{
					map[string]any{"type": "text", "text": longText},
					map[string]any{"type": "image", "image_url": "data:short"},
				},
			},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := RewriteWith(nil, raw, host.Codex, nil, codexOpt(max))
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolOutputTruncated != 1 {
		t.Fatalf("expected one truncated text part: %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	items := got["input"].([]any)
	parts := items[0].(map[string]any)["output"].([]any)
	text := parts[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "jev-routing: omitted") {
		t.Fatalf("text part not truncated: %s", text)
	}
	img := parts[1].(map[string]any)
	if img["image_url"] != "data:short" {
		t.Fatalf("non-text part changed: %v", img)
	}
}

func TestCodexToolOutputMaxEnvParsing(t *testing.T) {
	t.Setenv("JEV_CODEX_TOOL_OUTPUT_MAX", "")
	o, err := OptionsFromEnv()
	if err != nil || o.CodexToolOutputMax != 0 {
		t.Fatalf("unset should default to 0/disabled: o=%+v err=%v", o, err)
	}
	t.Setenv("JEV_CODEX_TOOL_OUTPUT_MAX", "20000")
	o, err = OptionsFromEnv()
	if err != nil || o.CodexToolOutputMax != 20000 {
		t.Fatalf("want 20000: o=%+v err=%v", o, err)
	}
	t.Setenv("JEV_CODEX_TOOL_OUTPUT_MAX", "-1")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("expected error for negative value")
	}
	t.Setenv("JEV_CODEX_TOOL_OUTPUT_MAX", "notanumber")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("expected error for non-numeric value")
	}
}

// TestCodexToolOutputMaxStablePrefixAcrossGrowingHistory mirrors real Codex
// traffic: request 2 resends everything request 1 had plus more turns. The
// truncated bytes for the item shared by both requests must match exactly,
// or the upstream prompt-cache prefix breaks on every additional turn.
func TestCodexToolOutputMaxStablePrefixAcrossGrowingHistory(t *testing.T) {
	max := 300
	shared := strings.Repeat("shared-log-line\n", 100)
	req1 := map[string]any{
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": shared},
		},
	}
	req2 := map[string]any{
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": shared},
			map[string]any{"type": "function_call_output", "call_id": "c2", "output": "new turn output"},
		},
	}
	raw1, _ := json.Marshal(req1)
	raw2, _ := json.Marshal(req2)
	out1, _, err := RewriteWith(nil, raw1, host.Codex, nil, codexOpt(max))
	if err != nil {
		t.Fatal(err)
	}
	out2, _, err := RewriteWith(nil, raw2, host.Codex, nil, codexOpt(max))
	if err != nil {
		t.Fatal(err)
	}
	var got1, got2 map[string]any
	_ = json.Unmarshal(out1, &got1)
	_ = json.Unmarshal(out2, &got2)
	item1 := got1["input"].([]any)[0].(map[string]any)["output"].(string)
	item2 := got2["input"].([]any)[0].(map[string]any)["output"].(string)
	if item1 != item2 {
		t.Fatalf("truncated output for the shared item diverged across requests:\n%q\n%q", item1, item2)
	}
}
