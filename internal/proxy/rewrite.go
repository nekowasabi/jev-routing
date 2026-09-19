package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

const (
	reasonNotJSON            = "not_json"
	reasonNotChat            = "not_chat"
	reasonNoCatalog          = "no-catalog"
	reasonExplicitToolChoice = "explicit_tool_choice"
	reasonPreviousResponse   = "previous_response_id"
	reasonDuplicateNames     = "duplicate_names"
	reasonUnrecognizedFormat = "unrecognized_format"
	reasonNamespacedTools    = "namespaced_tools"
	reasonProviderExecuted   = "provider_executed"
	reasonUnknownHistory     = "unknown_history"
	reasonImages             = "images"
	reasonLegacyFunctions    = "legacy_functions"
	reasonBaseline           = "baseline"
	reasonUncertainJev       = "uncertain_jev"
	reasonInvalidJev         = "invalid_jev"
	reasonJevError           = "jev_error"
	reasonLocalPassthrough   = "local_passthrough"
	reasonNoToolNeeded       = "no_tool_needed"
	reasonHostMeta           = "host_meta"
	reasonForcedUnavailable  = "forced_unavailable"
	reasonIneligibleForced   = "ineligible_forced"

	sourceLocal       = "local"
	sourceJev         = "jev"
	sourcePassthrough = "passthrough"

	applyNone   = "none"
	applyFilter = "filter"
	applyForced = "forced"
	applyDirect = "direct"

	adoptConfidence = 0.8
	needsToolYes    = 0.8
	needsToolNo     = 0.2
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

	Source           string  `json:"source,omitempty"`
	Reason           string  `json:"reason,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
	NeedsTool        float64 `json:"needsTool,omitempty"`
	Changed          bool    `json:"changed"`
	Apply            string  `json:"apply,omitempty"`
	OriginalModel    string  `json:"originalModel,omitempty"`
	SentModel        string  `json:"sentModel,omitempty"`
	Direct           bool    `json:"direct,omitempty"`
	DirectName       string  `json:"-"`
	DirectArgs       string  `json:"-"`
	Stream           bool    `json:"-"`
	ForcedTool       string  `json:"forcedTool,omitempty"`
	Protocol         string  `json:"protocol,omitempty"`
	CompactApplied   bool    `json:"compactApplied,omitempty"`
	ReasoningChanged bool    `json:"reasoningChanged,omitempty"`
}

func Rewrite(body []byte, h host.ID, client *jev.Client) ([]byte, RewriteStats, error) {
	return RewriteWith(context.Background(), body, h, client, DefaultOptions())
}

func RewriteWith(ctx context.Context, body []byte, h host.ID, client *jev.Client, opt Options) ([]byte, RewriteStats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.Mode == "" {
		opt = DefaultOptions()
	}
	stats := RewriteStats{Host: h, Engine: "local", Apply: applyNone, Source: sourcePassthrough}
	if client != nil && client.Live() {
		stats.Engine = "live"
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		stats.Chosen = "passthrough:" + reasonNotJSON
		stats.Reason = reasonNotJSON
		return body, stats, err
	}
	stats.OriginalModel = modelName(root)
	stats.SentModel = stats.OriginalModel

	if opt.Mode == ModeBaseline {
		stats.Chosen = "passthrough:" + reasonBaseline
		stats.Reason = reasonBaseline
		return body, stats, nil
	}

	tools, toolsKey := extractTools(root)
	names := plan.ToolNames(asMaps(tools))
	stats.ToolBefore = len(names)
	stats.ToolAfter = len(names)

	elig := inspectRequest(root)
	stats.Protocol = elig.Protocol
	if !elig.OK {
		stats.Chosen = "passthrough:" + elig.Reason
		stats.Reason = elig.Reason
		return body, stats, nil
	}

	toolSpecs := plan.SpecsFrom(asMaps(tools))
	if len(names) == 0 {
		stats.Chosen = "passthrough:" + reasonNoCatalog
		stats.Reason = reasonNoCatalog
		stats.ToolAfter = 0
		return body, stats, nil
	}

	msgs := asSlice(root["messages"])
	if msgs == nil {
		msgs = asSlice(root["input"])
	}
	items, user := itemsFromMessages(msgs)
	actions := actionsFromItems(items)
	user = plan.WorkRequest(user)

	preserve := 2
	if len(items) > 16 {
		preserve = 6
	}
	compOpts := compact.Options{Goal: user, PreserveRecent: preserve}
	compaction := compact.Result{}
	compactOK := false
	if opt.Compaction != CompactionOff {
		compaction = compact.CompactLocal(items, compOpts)
		if client != nil && client.Live() && len(items) > 4 {
			if live, err := jev.AskCompactContext(ctx, client, items, compOpts); err == nil {
				compaction = live
			}
			// On error, AskCompact keeps uncertain items; still use that result if returned.
			// If the call failed entirely with keep semantics, live still has keeps.
		}
		compactOK = true
	}

	decision := plan.DecideSpecs(user, actions, toolSpecs, h)
	stats.Source = sourceLocal
	stats.Confidence = decision.Confidence

	if plan.HostMeta(decision.Tool) {
		decision.Passthrough = true
		stats.Reason = reasonHostMeta
	}

	usedJev := false
	if client != nil && client.Live() && len(names) > 0 && decision.Confidence < adoptConfidence {
		live, verr, err := askNextTool(ctx, client, user, actions, toolSpecs)
		if err != nil {
			stats.Reason = reasonJevError
			stats.Chosen = "passthrough:" + reasonJevError
			return body, stats, nil
		}
		if verr != "" {
			stats.Reason = verr
			stats.Chosen = "passthrough:" + verr
			return body, stats, nil
		}
		decision = live
		usedJev = true
		stats.Source = sourceJev
		stats.Confidence = decision.Confidence
		stats.NeedsTool = decision.Done
	}

	if decision.Passthrough || decision.Tool == plan.Respond || (usedJev && decision.Done <= needsToolNo) {
		if stats.Reason == "" {
			if usedJev && decision.Done <= needsToolNo {
				stats.Reason = reasonNoToolNeeded
			} else {
				stats.Reason = reasonLocalPassthrough
			}
		}
		stats.Chosen = "passthrough"
		stats.ToolAfter = stats.ToolBefore
		stats.Done = decision.Done
		stats.Gated = decision.Gated
		return body, stats, nil
	}

	if usedJev && decision.Done < needsToolYes {
		stats.Reason = reasonUncertainJev
		stats.Chosen = "passthrough:" + reasonUncertainJev
		stats.ToolAfter = stats.ToolBefore
		return body, stats, nil
	}

	kept := filterTools(tools, decision.Tool)
	if len(kept) == 0 {
		stats.Chosen = "passthrough:" + decision.Tool
		stats.Reason = "missing_tool"
		stats.ToolAfter = stats.ToolBefore
		return body, stats, nil
	}

	// Decision is confirmed: apply compaction and catalog changes to a copy.
	work := cloneMap(root)
	if compactOK {
		workMsgs := asSlice(work["messages"])
		key := "messages"
		if workMsgs == nil {
			workMsgs = asSlice(work["input"])
			key = "input"
		}
		workMsgs = applyCompactToMessages(workMsgs, compaction)
		work[key] = workMsgs
		stats.CharsBefore = compaction.Stats.CharsBefore
		stats.CharsAfter = compaction.Stats.CharsAfter
		stats.CompactDropped = compaction.Stats.Dropped + compaction.Stats.Truncated
		stats.CompactApplied = true
	}

	apply := applyFilter
	if opt.Mode == ModeForced && usedJev {
		if !elig.ForcedOK {
			stats.Reason = reasonIneligibleForced
			// Still apply filter (same candidate limit) — forced is the extra constraint.
			apply = applyFilter
		} else {
			apply = applyForced
			if err := applyForcedChoice(work, elig.Protocol, decision.Tool); err != nil {
				stats.Reason = reasonIneligibleForced
				apply = applyFilter
			} else {
				stats.ForcedTool = decision.Tool
			}
		}
	}

	setTools(work, toolsKey, kept)
	if _, ok := work["additional_tools"]; ok {
		delete(work, "additional_tools")
	}
	if apply != applyForced {
		delete(work, "tool_choice")
	}
	if opt.Reasoning == ReasoningLegacy {
		before, _ := json.Marshal(work["reasoning"])
		disableThinking(work, h, modelName(work))
		after, _ := json.Marshal(work["reasoning"])
		stats.ReasoningChanged = string(before) != string(after)
	}

	if apply == applyForced && opt.ArgsModel != "" && opt.ArgsToolSet()[decision.Tool] {
		work["model"] = opt.ArgsModel
		stats.SentModel = opt.ArgsModel
	}

	stats.Stream = streamTrue(root)
	if apply == applyForced && opt.DirectToolSet()[decision.Tool] && elig.Protocol == "chat" {
		if args, ok := directArgsFor(kept[0]); ok {
			stats.Direct = true
			stats.DirectName = decision.Tool
			stats.DirectArgs = args
			apply = applyDirect
		}
	}

	stats.Apply = apply
	stats.Chosen = decision.Tool
	stats.Done = decision.Done
	stats.Gated = decision.Gated
	stats.ToolAfter = 1
	stats.Changed = true
	if stats.Reason == "" {
		if usedJev {
			stats.Reason = sourceJev
		} else {
			stats.Reason = sourceLocal
		}
	}

	out, err := json.Marshal(work)
	if err != nil {
		return body, stats, err
	}
	return out, stats, nil
}

type eligibility struct {
	OK       bool
	ForcedOK bool
	Reason   string
	Protocol string
}

func inspectRequest(root map[string]any) eligibility {
	if _, ok := root["functions"]; ok && asSlice(root["tools"]) == nil {
		return eligibility{Reason: reasonLegacyFunctions, Protocol: "legacy"}
	}
	if _, ok := root["previous_response_id"]; ok {
		if s, _ := root["previous_response_id"].(string); s != "" {
			return eligibility{Reason: reasonPreviousResponse, Protocol: "responses"}
		}
	}
	_, hasMsg := root["messages"]
	_, hasIn := root["input"]
	if !hasMsg && !hasIn {
		return eligibility{Reason: reasonNotChat}
	}
	protocol := "chat"
	if hasIn && !hasMsg {
		protocol = "responses"
	} else if looksAnthropic(root) {
		protocol = "anthropic"
	}

	if reason, ok := toolChoiceReason(root["tool_choice"]); !ok {
		return eligibility{Reason: reason, Protocol: protocol}
	}

	tools, _ := extractTools(root)
	if reason := catalogReason(tools); reason != "" {
		return eligibility{Reason: reason, Protocol: protocol}
	}

	hist := asSlice(root["messages"])
	if hist == nil {
		hist = asSlice(root["input"])
	}
	if reason := historyReason(hist); reason != "" {
		return eligibility{Reason: reason, Protocol: protocol}
	}

	forcedOK := true
	switch protocol {
	case "chat":
		if streamTrue(root) {
			// Streaming Chat may still be filtered; forced+direct handle stream in proxy.
		}
	case "responses":
		// Forced allowed for non-namespace function/custom with full history (already checked).
	case "anthropic":
		if thinkingEnabled(root) || hasCacheControl(root) {
			forcedOK = false
		}
	default:
		forcedOK = false
	}
	return eligibility{OK: true, ForcedOK: forcedOK, Protocol: protocol}
}

func streamTrue(root map[string]any) bool {
	if b, ok := root["stream"].(bool); ok {
		return b
	}
	return false
}

func thinkingEnabled(root map[string]any) bool {
	th, ok := root["thinking"].(map[string]any)
	if !ok {
		return false
	}
	typ, _ := th["type"].(string)
	return typ != "" && typ != "disabled"
}

func hasCacheControl(root map[string]any) bool {
	if _, ok := root["cache_control"]; ok {
		return true
	}
	for _, raw := range asSlice(root["system"]) {
		m, ok := raw.(map[string]any)
		if ok {
			if _, ok := m["cache_control"]; ok {
				return true
			}
		}
	}
	return false
}

func looksAnthropic(root map[string]any) bool {
	if _, ok := root["thinking"]; ok {
		return true
	}
	if _, ok := root["system"]; ok && asSlice(root["messages"]) != nil {
		for _, raw := range asSlice(root["tools"]) {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, hasFn := m["function"]; !hasFn {
				if _, hasName := m["name"]; hasName {
					return true
				}
			}
		}
	}
	return false
}

func toolChoiceReason(v any) (string, bool) {
	if v == nil {
		return "", true
	}
	if s, ok := v.(string); ok {
		switch s {
		case "", "auto":
			return "", true
		default:
			return reasonExplicitToolChoice, false
		}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return reasonExplicitToolChoice, false
	}
	typ, _ := m["type"].(string)
	if typ == "" || typ == "auto" {
		return "", true
	}
	return reasonExplicitToolChoice, false
}

func catalogReason(tools []any) string {
	seen := map[string]int{}
	for _, raw := range tools {
		m, ok := raw.(map[string]any)
		if !ok {
			return reasonUnrecognizedFormat
		}
		if isProviderExecuted(m) {
			return reasonProviderExecuted
		}
		n := toolNameOf(m)
		if n == "" {
			if typ, _ := m["type"].(string); typ != "" && typ != "function" && typ != "custom" {
				return reasonUnrecognizedFormat
			}
			return reasonUnrecognizedFormat
		}
		if isNamespaced(n, m) {
			return reasonNamespacedTools
		}
		seen[n]++
		if seen[n] > 1 {
			return reasonDuplicateNames
		}
	}
	return ""
}

func isProviderExecuted(m map[string]any) bool {
	typ, _ := m["type"].(string)
	switch typ {
	case "web_search", "file_search", "code_interpreter", "computer", "computer_use",
		"hosted", "server_tool", "mcp":
		return true
	}
	if _, ok := m["server_label"]; ok {
		return true
	}
	return false
}

func isNamespaced(name string, m map[string]any) bool {
	if _, ok := m["namespace"]; ok {
		return true
	}
	if strings.Contains(name, "__") {
		return true
	}
	if i := strings.IndexByte(name, '.'); i > 0 && i < len(name)-1 {
		return true
	}
	return false
}

func historyReason(msgs []any) string {
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			return reasonUnrecognizedFormat
		}
		if reason := messageReason(m); reason != "" {
			return reason
		}
	}
	return ""
}

func messageReason(m map[string]any) string {
	typ, _ := m["type"].(string)
	switch typ {
	case "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output",
		"message", "reasoning", "item_reference", "":
		// known Responses / Chat
	default:
		if _, hasRole := m["role"]; !hasRole {
			return reasonUnrecognizedFormat
		}
	}
	if v, ok := m["content"]; ok {
		if reason := contentReason(v); reason != "" {
			return reason
		}
	}
	if v, ok := m["output"]; ok {
		if reason := contentReason(v); reason != "" {
			return reason
		}
	}
	return ""
}

func contentReason(v any) string {
	switch t := v.(type) {
	case string, nil:
		return ""
	case []any:
		for _, raw := range t {
			p, ok := raw.(map[string]any)
			if !ok {
				return reasonUnknownHistory
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "text", "output_text", "input_text", "tool_use", "tool_result", "":
			case "image", "image_url", "input_image", "image_file":
				return reasonImages
			default:
				if _, ok := p["text"]; ok {
					continue
				}
				return reasonUnknownHistory
			}
			if reason := contentReason(p["content"]); reason != "" {
				return reason
			}
		}
		return ""
	default:
		return reasonUnknownHistory
	}
}

func applyForcedChoice(root map[string]any, protocol, name string) error {
	if name == "" || name == plan.Respond {
		return fmt.Errorf("no tool")
	}
	switch protocol {
	case "chat":
		root["tool_choice"] = map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name},
		}
	case "responses":
		root["tool_choice"] = map[string]any{"type": "function", "name": name}
	case "anthropic":
		root["tool_choice"] = map[string]any{"type": "tool", "name": name}
	default:
		return fmt.Errorf("protocol")
	}
	return nil
}

func extractTools(root map[string]any) ([]any, string) {
	if t := asSlice(root["tools"]); t != nil {
		if extra := asSlice(root["additional_tools"]); extra != nil {
			out := make([]any, 0, len(t)+len(extra))
			out = append(out, t...)
			out = append(out, extra...)
			return out, "tools"
		}
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

func askNextTool(ctx context.Context, c *jev.Client, user string, actions []plan.Action, specs []plan.Spec) (plan.Decision, string, error) {
	criteria := map[string]string{}
	for _, s := range specs {
		if plan.HostMeta(s.Name) {
			continue
		}
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
		"next_tool":  {Type: "choice", Instructions: "Which single tool should run next? Agent or Task launches a Claude Code subagent — pick it for broad exploration or parallel work. Pick respond_to_user only when no tool is needed now.", Criteria: criteria},
		"needs_tool": {Type: "noul", Instructions: "A tool call is needed now to make progress. This is not a judgment that the overall user task is complete."},
	}
	state := map[string]any{"user_request": user, "actions_taken": actions}
	res, err := c.AskContext(ctx, state, qs)
	if err != nil {
		return plan.Decision{}, reasonJevError, err
	}
	choice, choiceOK := jev.ParseChoice(res, "next_tool")
	need, needOK := jev.ParseNoul(res, "needs_tool")
	if !choiceOK || !needOK {
		return plan.Decision{}, reasonInvalidJev, nil
	}
	if !finite01(choice.Conf) || !finite01(need.Conf) || !finite01(need.Noul) {
		return plan.Decision{}, reasonInvalidJev, nil
	}
	if _, ok := criteria[choice.Choice]; !ok {
		return plan.Decision{}, reasonInvalidJev, nil
	}
	if choice.Conf < adoptConfidence || need.Conf < adoptConfidence {
		return plan.Decision{}, reasonUncertainJev, nil
	}
	if plan.HostMeta(choice.Choice) {
		return plan.Decision{Tool: plan.Respond, Done: need.Noul, Passthrough: true, Confidence: choice.Conf}, "", nil
	}
	if choice.Choice == plan.Respond || need.Noul <= needsToolNo {
		return plan.Decision{Tool: plan.Respond, Done: need.Noul, Passthrough: true, Confidence: choice.Conf}, "", nil
	}
	if need.Noul < needsToolYes {
		return plan.Decision{}, reasonUncertainJev, nil
	}
	return plan.Decision{Tool: choice.Choice, Done: need.Noul, Confidence: choice.Conf}, "", nil
}

func finite01(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func filterTools(tools []any, name string) []any {
	var kept []any
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if toolNameOf(m) == name {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return tools[:0]
	}
	return kept
}

func toolNameOf(m map[string]any) string {
	if n, _ := m["name"].(string); n != "" {
		return n
	}
	if fn, ok := m["function"].(map[string]any); ok {
		if n, _ := fn["name"].(string); n != "" {
			return n
		}
	}
	return ""
}

func disableThinking(root map[string]any, h host.ID, model string) {
	if h == host.Codex && isAstra(model) {
		return
	}
	if _, ok := root["thinking"]; ok {
		root["thinking"] = map[string]any{"type": "disabled"}
	}
	effort := reasoningOff(h, model)
	if h == host.Codex {
		root["reasoning"] = map[string]any{"effort": effort, "context": "all_turns"}
	} else if _, ok := root["reasoning"]; ok {
		root["reasoning"] = map[string]any{"effort": effort}
	}
	if _, ok := root["reasoning_effort"]; ok {
		root["reasoning_effort"] = effort
	}
	removeClearThinkingEdit(root)
}

func modelName(root map[string]any) string {
	model, _ := root["model"].(string)
	return model
}

func reasoningOff(h host.ID, model string) string {
	if h == host.Grok {
		return "low"
	}
	return "none"
}

func isAstra(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "gpt-6-astra")
}

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
		typ, _ := m["type"].(string)
		role, _ := m["role"].(string)
		switch {
		case typ == "function_call" || typ == "custom_tool_call":
			cid := firstString(m, "call_id", "id")
			if cid == "" {
				cid = id()
			}
			name, _ := m["name"].(string)
			args, _ := m["arguments"].(string)
			if args == "" {
				if rawArgs, err := json.Marshal(m["arguments"]); err == nil && string(rawArgs) != "null" {
					args = string(rawArgs)
				}
			}
			items = append(items, compact.Item{
				ID: cid, Kind: compact.KindCall, PairID: cid, Tool: name,
				Chars: len(args), Preview: clip(args, 200), Body: args,
			})
		case typ == "function_call_output" || typ == "custom_tool_call_output":
			cid := firstString(m, "call_id", "id")
			body := firstString(m, "output", "result")
			if body == "" {
				body = textOf(m)
			}
			items = append(items, compact.Item{
				ID: cid + "_r", Kind: compact.KindResult, PairID: cid, Chars: len(body),
				Preview: clip(body, 200), Body: body, Tool: str(m["name"]),
			})
		case role == "user":
			text := textOf(m)
			if text != "" {
				user = text
			}
			items = append(items, compact.Item{ID: id(), Kind: compact.KindText, Chars: len(text), Preview: clip(text, 200), Body: text})
			for _, tr := range toolResults(m) {
				items = append(items, tr)
			}
		case role == "assistant":
			text := textOf(m)
			if text != "" {
				items = append(items, compact.Item{ID: id(), Kind: compact.KindText, Chars: len(text), Preview: clip(text, 200), Body: text})
			}
			for _, tc := range toolCalls(m) {
				items = append(items, tc)
			}
		case role == "tool":
			body := textOf(m)
			tid, _ := m["tool_call_id"].(string)
			if tid == "" {
				tid = firstString(m, "call_id")
			}
			if tid == "" {
				tid = id()
			}
			items = append(items, compact.Item{
				ID: tid + "_r", Kind: compact.KindResult, PairID: tid, Chars: len(body),
				Preview: clip(body, 200), Body: body, Tool: str(m["name"]),
			})
		case role == "function":
			body := textOf(m)
			tid := firstString(m, "name")
			items = append(items, compact.Item{
				ID: tid + "_r", Kind: compact.KindResult, PairID: tid, Chars: len(body),
				Preview: clip(body, 200), Body: body, Tool: tid,
			})
		}
	}
	return items, user
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
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
		typ, _ := m["type"].(string)
		if typ == "function_call" || typ == "custom_tool_call" {
			cid := firstString(m, "call_id", "id")
			if action[cid] == compact.ActionDrop {
				continue
			}
			out = append(out, m)
			continue
		}
		if typ == "function_call_output" || typ == "custom_tool_call_output" {
			cid := firstString(m, "call_id", "id")
			act := action[cid+"_r"]
			if act == compact.ActionDrop {
				continue
			}
			if act == compact.ActionTruncate {
				if b, ok := body[cid+"_r"]; ok {
					m["output"] = b
				}
			}
			out = append(out, m)
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
	lastResult := map[string]string{}
	haveResult := map[string]bool{}
	for _, it := range items {
		if it.Kind != compact.KindResult {
			continue
		}
		pid := it.PairID
		if pid == "" {
			continue
		}
		lastResult[pid] = it.Body
		haveResult[pid] = true
	}
	seen := map[string]int{}
	var out []plan.Action
	for _, it := range items {
		if it.Kind != compact.KindCall {
			continue
		}
		pid := it.PairID
		if pid == "" {
			pid = it.ID
		}
		if pid == "" {
			out = append(out, plan.Action{Tool: it.Tool, Input: it.Body, Pending: true})
			continue
		}
		if idx, ok := seen[pid]; ok {
			out[idx].Input = it.Body
			out[idx].Tool = it.Tool
			if haveResult[pid] {
				out[idx].Result = lastResult[pid]
				out[idx].Pending = false
			}
			continue
		}
		a := plan.Action{Tool: it.Tool, Input: it.Body, Pending: !haveResult[pid]}
		if haveResult[pid] {
			a.Result = lastResult[pid]
		}
		seen[pid] = len(out)
		out = append(out, a)
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
	if s, ok := m["output"].(string); ok {
		return s
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

func cloneMap(m map[string]any) map[string]any {
	b, err := json.Marshal(m)
	if err != nil {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return m
	}
	return out
}

func FormatStats(s RewriteStats) string {
	return fmt.Sprintf("host=%s tools %d→%d chosen=%s compact -%d chars engine=%s",
		s.Host, s.ToolBefore, s.ToolAfter, s.Chosen, s.CharsBefore-s.CharsAfter, s.Engine)
}
