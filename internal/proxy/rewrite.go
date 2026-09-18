package proxy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

type RewriteStats struct {
	Host           host.ID `json:"host"`
	ToolBefore     int     `json:"toolBefore"`
	ToolAfter      int     `json:"toolAfter"`
	Chosen         string  `json:"chosen"`
	Done           float64 `json:"done"`
	Gated          bool    `json:"gated"`
	CharsBefore    int     `json:"charsBefore"`
	CharsAfter     int     `json:"charsAfter"`
	CompactDropped int     `json:"compactDropped"`
	Engine         string  `json:"engine"`
}

func Rewrite(body []byte, h host.ID, client *jev.Client) ([]byte, RewriteStats, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, RewriteStats{}, err
	}
	if !isChatTurn(root) {
		return body, RewriteStats{Host: h, Chosen: "passthrough:not-chat"}, nil
	}
	tools, toolsKey := extractTools(root)
	toolSpecs := plan.SpecsFrom(asMaps(tools))
	names := plan.ToolNames(asMaps(tools))
	msgs := asSlice(root["messages"])
	if msgs == nil {
		msgs = asSlice(root["input"])
	}
	items, user := itemsFromMessages(msgs)
	actions := actionsFromItems(items)

	if len(names) == 0 {
		// Grok Build often omits tools[] and lets cli-chat-proxy inject the catalog.
		// Writing "tools": [] disables that injection.
		disableThinking(root, h)
		out, err := json.Marshal(root)
		return out, RewriteStats{Host: h, ToolBefore: 0, ToolAfter: 0, Chosen: "passthrough:no-catalog"}, err
	}
	preserve := 2
	if len(items) > 16 {
		preserve = 6
	}
	compaction := compact.CompactLocal(items, compact.Options{Goal: user, PreserveRecent: preserve})
	if client != nil && client.Live() && len(items) > 4 {
		if live, err := jev.AskCompact(client, items, compact.Options{Goal: user, PreserveRecent: preserve}); err == nil {
			compaction = live
		}
	}
	msgs = applyCompactToMessages(msgs, compaction)
	if _, ok := root["messages"]; ok {
		root["messages"] = msgs
	} else if _, ok := root["input"]; ok {
		root["input"] = msgs
	}

	decision := plan.DecideSpecs(user, actions, toolSpecs, h)
	// Local goal-based match (>=0.8) is trustworthy; skip the per-request Jev round trip.
	if client != nil && client.Live() && len(names) > 0 && decision.Confidence < 0.8 {
		if live, err := askNextTool(client, user, actions, toolSpecs); err == nil && live.Tool != "" {
			decision = live
		}
	}

	stats := RewriteStats{
		Host:           h,
		ToolBefore:     len(names),
		Chosen:         decision.Tool,
		Done:           decision.Done,
		Gated:          decision.Gated,
		CharsBefore:    compaction.Stats.CharsBefore,
		CharsAfter:     compaction.Stats.CharsAfter,
		CompactDropped: compaction.Stats.Dropped + compaction.Stats.Truncated,
		Engine:         "local",
	}
	if client != nil && client.Live() {
		stats.Engine = "live"
	}

	if decision.Passthrough {
		stats.Chosen = "passthrough"
		stats.ToolAfter = stats.ToolBefore
		out, err := json.Marshal(root)
		return out, stats, err
	}

	if decision.Tool == plan.Respond || (decision.Done >= 0.5 && !decision.Gated) {
		setTools(root, toolsKey, []any{})
		delete(root, "tool_choice")
		disableThinking(root, h)
		stats.ToolAfter = 0
		out, err := json.Marshal(root)
		return out, stats, err
	}

	kept := filterTools(tools, decision.Tool)
	if len(kept) == 0 {
		stats.Chosen = "passthrough:" + decision.Tool
		stats.ToolAfter = stats.ToolBefore
		out, err := json.Marshal(root)
		return out, stats, err
	}
	setTools(root, toolsKey, kept)
	root["tool_choice"] = toolChoice(h, decision.Tool, root)
	disableThinking(root, h)
	stats.ToolAfter = 1
	out, err := json.Marshal(root)
	return out, stats, err
}

func isChatTurn(root map[string]any) bool {
	_, hasMsg := root["messages"]
	_, hasIn := root["input"]
	return hasMsg || hasIn
}

func extractTools(root map[string]any) ([]any, string) {
	if t := asSlice(root["tools"]); t != nil {
		return t, "tools"
	}
	if t := asSlice(root["functions"]); t != nil {
		return t, "functions"
	}
	return nil, "tools"
}

func setTools(root map[string]any, key string, tools []any) {
	if key == "" {
		key = "tools"
	}
	root[key] = tools
}

func askNextTool(c *jev.Client, user string, actions []plan.Action, specs []plan.Spec) (plan.Decision, error) {
	criteria := map[string]string{}
	for _, s := range specs {
		desc := s.Desc
		if desc == "" {
			desc = s.Name
		}
		if len(desc) > 240 {
			desc = desc[:240]
		}
		criteria[s.Name] = desc
	}
	criteria[plan.Respond] = "stop calling tools and answer the user. Do not pick this if any requested work remains, including launching a subagent (Agent/Task)."
	qs := map[string]jev.Question{
		"next_tool": {Type: "choice", Instructions: "Which single tool should run next? Agent or Task launches a Claude Code subagent — pick it for broad exploration or parallel work. Pick respond_to_user only when the user request is fully satisfied.", Criteria: criteria},
		"done":      {Type: "noul", Instructions: "The user request is fully satisfied; no further tool call is needed, including no subagent."},
	}
	state := map[string]any{"user_request": user, "actions_taken": actions}
	res, err := c.Ask(state, qs)
	if err != nil {
		return plan.Decision{}, err
	}
	tool := jev.ChoiceOf(res, "next_tool")
	done := jev.NoulOf(res, "done")
	if tool == plan.Respond || tool == "" {
		return plan.Decision{Tool: plan.Respond, Done: 0, Passthrough: true, Confidence: 0.3}, nil
	}
	return plan.Decision{Tool: tool, Done: done, Confidence: 0.8}, nil
}

func filterTools(tools []any, name string) []any {
	var kept []any
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		n, _ := m["name"].(string)
		if n == "" {
			if fn, ok := m["function"].(map[string]any); ok {
				n, _ = fn["name"].(string)
			}
		}
		if n == name {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return tools[:0]
	}
	return kept
}

func toolChoice(h host.ID, name string, root map[string]any) any {
	// Anthropic Messages
	if _, ok := root["system"]; ok || h == host.Claude {
		return map[string]any{"type": "tool", "name": name}
	}
	// Codex Responses
	if _, ok := root["input"]; ok || h == host.Codex {
		return map[string]any{"type": "function", "name": name}
	}
	// Grok / OpenAI chat completions
	return map[string]any{"type": "function", "function": map[string]any{"name": name}}
}

func disableThinking(root map[string]any, h host.ID) {
	if _, ok := root["thinking"]; ok {
		root["thinking"] = map[string]any{"type": "disabled"}
	}
	off := reasoningOff(h)
	if _, ok := root["reasoning"]; ok {
		root["reasoning"] = map[string]any{"effort": off}
	}
	if _, ok := root["reasoning_effort"]; ok {
		root["reasoning_effort"] = off
	}
	removeClearThinkingEdit(root)
}

// reasoningOff is the cheapest effort the host accepts when we want no extra thinking.
func reasoningOff(h host.ID) string {
	if h == host.Grok {
		// Why: Instead of effort "none" (OpenAI/Codex disable), adopted "low".
		// xAI grok-4.5/4.6 reject "none"; reasoning cannot be disabled.
		return "low"
	}
	return "none"
}

// removeClearThinkingEdit drops the clear_thinking_20251015 context-management
// strategy, which the API rejects when thinking is disabled.
func removeClearThinkingEdit(root map[string]any) {
	cm, ok := root["context_management"].(map[string]any)
	if !ok {
		return
	}
	edits, ok := cm["edits"].([]any)
	if !ok {
		return
	}
	kept := edits[:0]
	for _, e := range edits {
		m, ok := e.(map[string]any)
		if ok && m["type"] == "clear_thinking_20251015" {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		delete(cm, "edits")
	} else {
		cm["edits"] = kept
	}
}

func itemsFromMessages(msgs []any) ([]compact.Item, string) {
	var items []compact.Item
	user := ""
	n := 0
	id := func() string {
		n++
		return "m" + strconv.Itoa(n)
	}
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "user":
			text := textOf(m)
			if user == "" && text != "" {
				user = text
			}
			items = append(items, compact.Item{ID: id(), Kind: compact.KindText, Chars: len(text), Preview: clip(text, 200), Body: text})
			for _, tr := range toolResults(m) {
				items = append(items, tr)
			}
		case "assistant":
			text := textOf(m)
			if text != "" {
				items = append(items, compact.Item{ID: id(), Kind: compact.KindText, Chars: len(text), Preview: clip(text, 200), Body: text})
			}
			for _, tc := range toolCalls(m) {
				items = append(items, tc)
			}
		case "tool":
			body := textOf(m)
			tid, _ := m["tool_call_id"].(string)
			if tid == "" {
				tid = id()
			}
			items = append(items, compact.Item{
				ID: tid + "_r", Kind: compact.KindResult, PairID: tid, Chars: len(body),
				Preview: clip(body, 200), Body: body, Tool: str(m["name"]),
			})
		}
	}
	return items, user
}

func toolCalls(m map[string]any) []compact.Item {
	var out []compact.Item
	if tcs, ok := m["tool_calls"].([]any); ok {
		for _, raw := range tcs {
			tc, _ := raw.(map[string]any)
			id, _ := tc["id"].(string)
			fn, _ := tc["function"].(map[string]any)
			name, _ := fn["name"].(string)
			args, _ := fn["arguments"].(string)
			out = append(out, compact.Item{
				ID: id, Kind: compact.KindCall, PairID: id, Tool: name,
				Chars: len(args), Preview: clip(args, 200), Body: args,
			})
		}
	}
	content := m["content"]
	if arr, ok := content.([]any); ok {
		for _, raw := range arr {
			b, _ := raw.(map[string]any)
			typ, _ := b["type"].(string)
			if typ != "tool_use" {
				continue
			}
			id, _ := b["id"].(string)
			name, _ := b["name"].(string)
			input, _ := json.Marshal(b["input"])
			out = append(out, compact.Item{
				ID: id, Kind: compact.KindCall, PairID: id, Tool: name,
				Chars: len(input), Preview: clip(string(input), 200), Body: string(input),
			})
		}
	}
	return out
}

func toolResults(m map[string]any) []compact.Item {
	var out []compact.Item
	content := m["content"]
	arr, ok := content.([]any)
	if !ok {
		return out
	}
	for _, raw := range arr {
		b, _ := raw.(map[string]any)
		typ, _ := b["type"].(string)
		if typ != "tool_result" {
			continue
		}
		id, _ := b["tool_use_id"].(string)
		text := textOf(b)
		if text == "" {
			if c, ok := b["content"].(string); ok {
				text = c
			}
		}
		out = append(out, compact.Item{
			ID: id + "_r", Kind: compact.KindResult, PairID: id, Chars: len(text),
			Preview: clip(text, 200), Body: text, Tool: str(b["name"]),
		})
	}
	return out
}

func applyCompactToMessages(msgs []any, res compact.Result) []any {
	action := map[string]compact.Action{}
	body := map[string]string{}
	for i := range res.Items {
		body[res.Items[i].ID] = res.Items[i].Body
	}
	for _, d := range res.Decisions {
		action[d.ID] = d.Action
		action[d.ID+"_r"] = d.Action
	}
	out := make([]any, 0, len(msgs))
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		role, _ := m["role"].(string)
		if role == "tool" {
			tid, _ := m["tool_call_id"].(string)
			act := action[tid+"_r"]
			if act == compact.ActionDrop {
				continue
			}
			if act == compact.ActionTruncate {
				if b, ok := body[tid+"_r"]; ok {
					m["content"] = b
				}
			}
			out = append(out, m)
			continue
		}
		if content, ok := m["content"].([]any); ok {
			// Why: nil slice marshals to JSON null; the API requires content to stay an array.
			kept := make([]any, 0, len(content))
			for _, c := range content {
				b, ok := c.(map[string]any)
				if !ok {
					kept = append(kept, c)
					continue
				}
				typ, _ := b["type"].(string)
				id := ""
				if typ == "tool_use" {
					id, _ = b["id"].(string)
				} else if typ == "tool_result" {
					id, _ = b["tool_use_id"].(string)
					id = id + "_r"
				}
				if id != "" && action[id] == compact.ActionDrop {
					continue
				}
				if id != "" && action[id] == compact.ActionTruncate {
					if nb, ok := body[id]; ok {
						if typ == "tool_result" {
							b["content"] = nb
						}
					}
				}
				kept = append(kept, b)
			}
			if len(kept) == 0 && len(content) > 0 {
				// Why: an empty content array is rejected ("must have non-empty content").
				// compact.go always drops a tool_use and its tool_result together, so
				// dropping the whole message cannot orphan the other half.
				continue
			}
			m["content"] = kept
		}
		if tcs, ok := m["tool_calls"].([]any); ok {
			kept := make([]any, 0, len(tcs))
			for _, c := range tcs {
				b, _ := c.(map[string]any)
				id, _ := b["id"].(string)
				if action[id] == compact.ActionDrop {
					continue
				}
				kept = append(kept, c)
			}
			if len(kept) == 0 && len(tcs) > 0 {
				if s, _ := m["content"].(string); s == "" {
					// Why: same as above — an assistant turn left with neither text nor
					// tool_calls is an empty message; its paired tool results are dropped too.
					continue
				}
				delete(m, "tool_calls")
			} else {
				m["tool_calls"] = kept
			}
		}
		out = append(out, m)
	}
	return out
}

func actionsFromItems(items []compact.Item) []plan.Action {
	var out []plan.Action
	for _, it := range items {
		if it.Kind == compact.KindCall {
			out = append(out, plan.Action{Tool: it.Tool, Input: it.Body})
		}
		if it.Kind == compact.KindResult && len(out) > 0 && out[len(out)-1].Result == "" {
			out[len(out)-1].Result = it.Body
		}
	}
	return out
}

func textOf(m map[string]any) string {
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, raw := range c {
			part, _ := raw.(map[string]any)
			if t, ok := part["text"].(string); ok {
				b.WriteString(t)
			}
		}
		return b.String()
	}
	return ""
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asMaps(v []any) []map[string]any {
	out := make([]map[string]any, 0, len(v))
	for _, x := range v {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func FormatStats(s RewriteStats) string {
	return fmt.Sprintf("host=%s tools %d→%d chosen=%s compact -%d chars engine=%s",
		s.Host, s.ToolBefore, s.ToolAfter, s.Chosen, s.CharsBefore-s.CharsAfter, s.Engine)
}
