package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

// Native compaction, from the primary sources:
//
// Codex (openai/codex codex-rs/model-provider/src/provider.rs) enables remote
// compaction v2 only for OpenAI and Azure. A custom provider such as this
// proxy is Unsupported, so /compact runs the local path in
// codex-rs/core/src/compact.rs: the model is asked for a CONTEXT CHECKPOINT
// summary and the client then keeps recent user messages plus
// SUMMARY_PREFIX + that assistant text. Assistant turns and tool results are
// not retained as items.
//
// Grok Build (xai-org/grok-build code_compaction) full-replaces the session.
// The sampler is prompted to return one <summary> block (minimum 500
// characters after cleaning; numbered sections). The host rebuilds
// [system, user prefix, AGENTS.md, last query, recent tail, summary].
// Older tool calls survive only inside that summary. PreCompact hooks can
// stop compaction; they cannot install a replacement transcript
// (xai-grok-hooks event PreCompact is source-only).
//
// Claude Code can replace its summary with the original messages via the
// fast-jev-compaction session.compact hook. Codex and Grok cannot. When
// their compaction prompt shows up on the wire, this proxy runs the same
// drop/truncate decisions and returns that retained transcript as the
// assistant message the host will store, instead of forwarding a lossy
// summarizer call.

func nativeCompactionEnabled(opt Options) bool {
	return opt.Compaction != CompactionOff && opt.Transforms.Compact && opt.Mode != ModeBaseline && !opt.Shadow
}

func nativeCompactionKind(root map[string]any) string {
	if s, _ := root["instructions"].(string); s != "" {
		if k := markerKind(s); k != "" {
			return k
		}
	}
	msgs, _ := locateHistory(root)
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		typ, _ := m["type"].(string)
		if role == "assistant" || role == "tool" || role == "function" || responseToolType(typ) {
			continue
		}
		if role != "user" && role != "developer" && role != "" && typ != "message" {
			continue
		}
		if k := markerKind(textOf(m)); k != "" {
			return k
		}
	}
	return ""
}

// Claude Code's compact prompt (full and partial variants) opens with the
// no-tools preamble and asks for "a detailed summary of ...".
const (
	claudeCompactNoTools = "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."
	claudeCompactTask    = "Your task is to create a detailed summary of"
)

// claudeMinReduction mirrors fast-jev-compaction minReductionRatio: below it,
// Claude Code's own summary is worth more than a barely shorter transcript.
const claudeMinReduction = 0.25

func lastUserIndex(msgs []any) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if m, ok := msgs[i].(map[string]any); ok && m["role"] == "user" {
			return i
		}
	}
	return -1
}

func claudeUserText(m map[string]any) string {
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, raw := range c {
			part, _ := raw.(map[string]any)
			if part["type"] == "text" {
				s, _ := part["text"].(string)
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

func claudeCompactionRequest(root map[string]any) bool {
	msgs, _ := root["messages"].([]any)
	i := lastUserIndex(msgs)
	if i < 0 {
		return false
	}
	text := claudeUserText(msgs[i].(map[string]any))
	return strings.Contains(text, claudeCompactNoTools) && strings.Contains(text, claudeCompactTask)
}

// withoutCompactPrompt drops the compaction request's text blocks. The prompt
// can share a user turn with trailing tool_result blocks, so those stay.
func withoutCompactPrompt(msgs []any) []any {
	i := lastUserIndex(msgs)
	if i < 0 {
		return msgs
	}
	out := append([]any{}, msgs[:i]...)
	blocks, _ := msgs[i].(map[string]any)["content"].([]any)
	var keep []any
	for _, raw := range blocks {
		if part, _ := raw.(map[string]any); part["type"] != "text" {
			keep = append(keep, raw)
		}
	}
	if len(keep) > 0 {
		m := cloneMap(msgs[i].(map[string]any))
		m["content"] = keep
		out = append(out, m)
	}
	return out
}

// claudeFallback returns why Claude Code should get its own summary instead.
func claudeFallback(stats RewriteStats) string {
	if !stats.CompactApplied {
		return "retention failed: " + stats.Reason
	}
	if stats.CharsBefore == 0 || float64(stats.CharsBefore-stats.CharsAfter)/float64(stats.CharsBefore) < claudeMinReduction {
		return fmt.Sprintf("reduction below %.0f%% (%d -> %d chars)", claudeMinReduction*100, stats.CharsBefore, stats.CharsAfter)
	}
	return ""
}

func markerKind(s string) string {
	switch {
	case strings.Contains(s, "CONTEXT CHECKPOINT COMPACTION"):
		return "codex"
	case strings.Contains(s, "<summary_request>"):
		return "grok"
	case strings.Contains(s, "successor assistant can continue the work seamlessly"):
		return "grok"
	case strings.Contains(s, "another AI assistant to continue working on the task"):
		return "grok"
	default:
		return ""
	}
}

func retainNative(ctx context.Context, raw []byte, client *jev.Client, opt Options, kind string) (string, RewriteStats) {
	stats := RewriteStats{Reason: "fast-jev-native", Chosen: "fast-jev-compaction", CompactApplied: true, Changed: true}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		stats.Reason = reasonNotJSON
		stats.Chosen = "passthrough:" + reasonNotJSON
		stats.CompactApplied = false
		stats.Changed = false
		return "", stats
	}
	stats.OriginalModel = modelName(root)
	stats.SentModel = stats.OriginalModel
	msgs, _ := locateHistory(root)
	if kind == "claude" {
		msgs = withoutCompactPrompt(msgs)
	}
	items, user := itemsFromMessages(msgs)
	preserve := 2
	if len(items) > 16 || kind == "claude" {
		preserve = 6
	}
	compOpts := compact.Options{Goal: user, PreserveRecent: preserve}
	compaction := compact.CompactLocal(items, compOpts)
	if client != nil && client.Live() && len(items) > 4 {
		if live, err := jev.AskCompactContext(ctx, client, items, compOpts); err == nil {
			compaction = live
		}
	}
	before, _ := json.Marshal(msgs)
	compacted := applyCompactToMessages(msgs, compaction)
	if compacted == nil {
		compacted = msgs
	}
	after, _ := json.Marshal(compacted)
	stats.CharsBefore = len(before)
	stats.CharsAfter = len(after)
	stats.CompactDropped = compaction.Stats.Dropped + compaction.Stats.Truncated
	users, tools, transcript := renderRetained(compacted)
	switch kind {
	case "grok":
		return grokSummary(users, tools, transcript), stats
	case "claude":
		return claudeSummary(transcript), stats
	}
	return codexSummary(transcript), stats
}

func renderRetained(msgs []any) (users, tools, transcript string) {
	var u, toolLog, all strings.Builder
	write := func(dst *strings.Builder, line string) {
		dst.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			dst.WriteByte('\n')
		}
	}
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		role, _ := m["role"].(string)
		if responseCallType(typ) {
			line := fmt.Sprintf("[tool_call %s %s] %s", firstString(m, "call_id", "id"), responseToolName(typ, m), callArgs(m, typ))
			write(&toolLog, line)
			write(&all, line)
			continue
		}
		if responseOutputType(typ) {
			line := fmt.Sprintf("[tool_result %s] %s", firstString(m, "call_id", "id"), outputBody(m))
			write(&toolLog, line)
			write(&all, line)
			continue
		}
		if role == "tool" || role == "function" {
			id := firstString(m, "tool_call_id", "call_id", "name")
			line := fmt.Sprintf("[tool_result %s] %s", id, textOf(m))
			write(&toolLog, line)
			write(&all, line)
			continue
		}
		for _, call := range toolCalls(m) {
			line := fmt.Sprintf("[tool_call %s %s] %s", call.ID, call.Tool, call.Body)
			write(&toolLog, line)
			write(&all, line)
		}
		for _, result := range toolResults(m) {
			line := fmt.Sprintf("[tool_result %s] %s", result.PairID, result.Body)
			write(&toolLog, line)
			write(&all, line)
		}
		text := strings.TrimSpace(proseOf(m))
		if text == "" {
			continue
		}
		if markerKind(text) != "" && role != "assistant" {
			continue
		}
		label := role
		if label == "" {
			label = typ
			if label == "" {
				label = "text"
			}
		}
		line := "[" + label + "] " + text
		write(&all, line)
		if role == "user" || role == "developer" || (typ == "message" && role != "assistant") {
			write(&u, text)
		}
	}
	return strings.TrimSpace(u.String()), strings.TrimSpace(toolLog.String()), strings.TrimSpace(all.String())
}

func proseOf(m map[string]any) string {
	content := m["content"]
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, raw := range c {
			part, _ := raw.(map[string]any)
			typ, _ := part["type"].(string)
			if typ == "tool_use" || typ == "tool_result" || typ == "thinking" || typ == "redacted_thinking" {
				continue
			}
			if t, ok := part["text"].(string); ok {
				b.WriteString(t)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	if s, ok := m["text"].(string); ok {
		return s
	}
	return ""
}

func orNone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "None"
	}
	return s
}

func grokSummary(users, tools, transcript string) string {
	body := fmt.Sprintf(`1. Primary Request and Intent: %s
2. Key Technical Concepts: None
3. Files and Code Sections: %s
4. Errors and Fixes: None
5. Problem Solving: None
6. All User Messages: %s
7. Pending Tasks: None
8. Current Work: %s
9. Optional Next Step: Continue from the retained transcript. Do not redo a dropped tool call unless its result was truncated.
`, orNone(users), orNone(tools), orNone(users), orNone(transcript))
	// Grok treats a cleaned summary under 500 characters as degenerate and retries.
	for utf8.RuneCountInString(body) < 500 {
		body += "\nVerbatim retained transcript (fast-jev-compaction, not a paraphrase):\n" + orNone(transcript) + "\n"
		if strings.TrimSpace(transcript) == "" && strings.TrimSpace(users) == "" {
			body += "No tool output was retained because this conversation had no compactable tool results. User and assistant prose is copied above and was not summarized.\n"
		}
	}
	return "<summary>\n" + body + "</summary>"
}

func codexSummary(transcript string) string {
	if strings.TrimSpace(transcript) == "" {
		transcript = "(no retained tool output or prose)"
	}
	return "fast-jev-compaction kept the following transcript verbatim. Stale tool calls were dropped or truncated. User and assistant prose was not summarized.\n\n" + transcript
}

// claudeSummary fits Claude Code's formatter: <analysis> is stripped and
// <summary> becomes "Summary:". Blank lines collapse, so entries are one per line.
func claudeSummary(transcript string) string {
	return "<analysis>fast-jev-compaction: retained verbatim tool history</analysis>\n<summary>\n" + orNone(transcript) + "\n</summary>"
}

func nativeCompactionKindJSON(raw []byte, h host.ID) string {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	if h == host.Claude && claudeCompactionRequest(root) {
		return "claude"
	}
	return nativeCompactionKind(root)
}

func nativeStream(raw []byte, r *http.Request, h host.ID) bool {
	var root map[string]any
	_ = json.Unmarshal(raw, &root)
	if b, ok := root["stream"].(bool); ok {
		return b
	}
	if wantsNativeStream(r) {
		return true
	}
	// Codex local compaction always reads a Responses stream
	// (compact.rs drain_to_completed). A missing stream flag still expects SSE.
	return h == host.Codex || nativeWire(r.URL.Path, h) == "responses"
}

func writeNativeCompaction(w http.ResponseWriter, r *http.Request, h host.ID, model, text string, stream bool) {
	if model == "" {
		model = "fast-jev-compaction"
	}
	switch nativeWire(r.URL.Path, h) {
	case "responses":
		if stream {
			w.Header().Set("content-type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(responsesCompactSSE(model, text))
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(responsesCompactJSON(model, text))
	case "anthropic":
		if stream {
			w.Header().Set("content-type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(anthropicCompactSSE(model, text))
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(anthropicCompactJSON(model, text))
	default:
		if stream {
			w.Header().Set("content-type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(chatCompactSSE(model, text))
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chatCompactJSON(model, text))
	}
}

func wantsNativeStream(r *http.Request) bool {
	if r == nil {
		return false
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		return true
	}
	return false
}

func nativeWire(path string, h host.ID) string {
	p := strings.ToLower(path)
	switch {
	case strings.Contains(p, "/responses"):
		return "responses"
	case strings.Contains(p, "/chat/completions"):
		return "chat"
	case strings.Contains(p, "/messages"):
		return "anthropic"
	case h == host.Codex:
		return "responses"
	case h == host.Claude:
		return "anthropic"
	default:
		return "chat"
	}
}

func compactUsage(text string) map[string]any {
	out := compact.EstimateTokens(text)
	if out < 1 {
		out = 1
	}
	return map[string]any{
		"input_tokens":  1,
		"output_tokens": out,
		"total_tokens":  out + 1,
	}
}

func responsesCompactJSON(model, text string) []byte {
	body := map[string]any{
		"id":     "resp_fast_jev",
		"object": "response",
		"model":  model,
		"status": "completed",
		"output": []any{map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "output_text",
				"text": text,
			}},
		}},
		"usage": compactUsage(text),
	}
	raw, _ := json.Marshal(body)
	return raw
}

func responsesCompactSSE(model, text string) []byte {
	item, _ := json.Marshal(map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "output_text",
				"text": text,
			}},
		},
	})
	completed, _ := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_fast_jev",
			"model":  model,
			"status": "completed",
			"usage":  compactUsage(text),
		},
	})
	return []byte("event: response.output_item.done\ndata: " + string(item) + "\n\nevent: response.completed\ndata: " + string(completed) + "\n\n")
}

func chatCompactJSON(model, text string) []byte {
	body := map[string]any{
		"id":     "chatcmpl-fast-jev",
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": text,
			},
			"finish_reason": "stop",
		}},
		"usage": compactUsage(text),
	}
	raw, _ := json.Marshal(body)
	return raw
}

func chatCompactSSE(model, text string) []byte {
	chunk, _ := json.Marshal(map[string]any{
		"id":     "chatcmpl-fast-jev",
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": text,
			},
			"finish_reason": nil,
		}},
	})
	stop, _ := json.Marshal(map[string]any{
		"id":     "chatcmpl-fast-jev",
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}},
	})
	return []byte("data: " + string(chunk) + "\n\ndata: " + string(stop) + "\n\ndata: [DONE]\n\n")
}

func anthropicCompactJSON(model, text string) []byte {
	usage := compactUsage(text)
	body := map[string]any{
		"id":    "msg_fast_jev",
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []any{map[string]any{
			"type": "text",
			"text": text,
		}},
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens":  usage["input_tokens"],
			"output_tokens": usage["output_tokens"],
		},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func anthropicCompactSSE(model, text string) []byte {
	var b strings.Builder
	event := func(name string, data map[string]any) {
		data["type"] = name
		raw, _ := json.Marshal(data)
		b.WriteString("event: " + name + "\ndata: " + string(raw) + "\n\n")
	}
	event("message_start", map[string]any{"message": map[string]any{
		"id": "msg_fast_jev", "type": "message", "role": "assistant", "model": model,
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
	}})
	event("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	event("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": text}})
	event("content_block_stop", map[string]any{"index": 0})
	event("message_delta", map[string]any{
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": compactUsage(text)["output_tokens"]},
	})
	event("message_stop", map[string]any{})
	return []byte(b.String())
}
