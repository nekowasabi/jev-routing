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

	rawTools := extractRawTools(root)
	tools, toolsKey := extractTools(root)
	filterable := filterableTools(tools)
	names := plan.ToolNames(asMaps(filterable))
	stats.ToolBefore = len(names)
	stats.ToolAfter = len(names)

	elig := inspectRequest(root)
	stats.Protocol = elig.Protocol
	if !elig.OK {
		stats.Chosen = "passthrough:" + elig.Reason
		stats.Reason = elig.Reason
		return body, stats, nil
	}

	toolSpecs := plan.SpecsFrom(asMaps(filterable))
	msgs, _ := locateHistory(root)
	items, user := itemsFromMessages(msgs)
	if user == "" {
		user = fallbackUser(root)
	}
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

	// Why: Apply safe history compaction independently of tool selection;
	// an uncertain or unnecessary next tool does not invalidate stale history.
	work := cloneMap(root)
	if compactOK {
		workMsgs, writeHist := locateHistory(work)
		before, _ := json.Marshal(workMsgs)
		beforeItems, _ := itemsFromMessages(workMsgs)
		workMsgs = applyCompactToMessages(workMsgs, compaction)
		if workMsgs != nil {
			writeHist(workMsgs)
		}
		after, _ := json.Marshal(workMsgs)
		stats.CharsBefore = len(before)
		stats.CharsAfter = len(after)
		stats.CompactApplied = string(before) != string(after)
		afterItems, _ := itemsFromMessages(workMsgs)
		remaining := map[string]string{}
		for _, item := range afterItems {
			remaining[item.ID] = item.Body
		}
		for _, item := range beforeItems {
			if item.Kind != compact.KindCall && item.Kind != compact.KindResult {
				continue
			}
			if body, ok := remaining[item.ID]; !ok || body != item.Body {
				stats.CompactDropped++
			}
		}
	}
	withoutSelection := func() ([]byte, RewriteStats, error) {
		if !stats.CompactApplied {
			return body, stats, nil
		}
		out, err := json.Marshal(work)
		if err != nil {
			return body, stats, err
		}
		stats.Changed = true
		return out, stats, nil
	}
	if len(names) == 0 {
		stats.Chosen = "passthrough:" + reasonNoCatalog
		stats.Reason = reasonNoCatalog
		return withoutSelection()
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
			return withoutSelection()
		}
		stats.Source = sourceJev
		stats.Confidence = live.Confidence
		stats.NeedsTool = live.Done
		if verr != "" {
			stats.Reason = verr
			stats.Chosen = "passthrough:" + verr
			return withoutSelection()
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
		return withoutSelection()
	}

	if usedJev && decision.Done < needsToolYes {
		stats.Reason = reasonUncertainJev
		stats.Chosen = "passthrough:" + reasonUncertainJev
		stats.ToolAfter = stats.ToolBefore
		return withoutSelection()
	}

	kept := filterTools(tools, decision.Tool, toolReferences(msgs))
	if plan.SequentialLocate(user) {
		kept = keepLocatePair(tools, kept)
	}
	if len(kept) == 0 {
		stats.Chosen = "passthrough:" + decision.Tool
		stats.Reason = "missing_tool"
		stats.ToolAfter = stats.ToolBefore
		return withoutSelection()
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

	setTools(work, toolsKey, reconstructCatalog(rawTools, kept))
	if _, ok := work["additional_tools"]; ok && toolsKey != "responses_lite" {
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
	stats.ToolAfter = len(filterableTools(kept))
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
	protocol := "chat"
	if !hasMsg && !hasIn {
		switch {
		case looksCursorAgent(root):
			protocol = "cursor"
		case looksPromptChat(root):
			protocol = "prompt"
		default:
			return eligibility{Reason: reasonNotChat}
		}
	} else if hasIn && !hasMsg {
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
	case "cursor", "prompt":
		forcedOK = false
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
	filterable := 0
	stickyReason := ""
	for _, raw := range tools {
		m, ok := raw.(map[string]any)
		if !ok {
			return reasonUnrecognizedFormat
		}
		if isSticky(m) {
			if stickyReason == "" {
				if isExternalNamespace(m) || isMCPNamespace(m) {
					stickyReason = reasonNamespacedTools
				} else {
					stickyReason = reasonProviderExecuted
				}
			}
			continue
		}
		n := toolNameOf(m)
		if n == "" {
			if typ, _ := m["type"].(string); typ != "" && typ != "function" && typ != "custom" {
				return reasonUnrecognizedFormat
			}
			return reasonUnrecognizedFormat
		}
		seen[n]++
		if seen[n] > 1 {
			return reasonDuplicateNames
		}
		filterable++
	}
	if filterable == 0 {
		return stickyReason
	}
	return ""
}

func isProviderExecuted(m map[string]any) bool {
	typ, _ := m["type"].(string)
	switch typ {
	case "web_search", "file_search", "code_interpreter", "computer", "computer_use",
		"hosted", "server_tool", "mcp", "tool_search", "local_shell", "image_generation",
		"tool_search_tool_regex_20251119", "tool_search_tool_bm25_20251119":
		return true
	}
	if _, ok := m["server_label"]; ok {
		return true
	}
	return false
}

func unknownHostedType(m map[string]any) bool {
	if toolNameOf(m) != "" {
		return false
	}
	typ, _ := m["type"].(string)
	return typ != "" && typ != "function" && typ != "custom"
}

const defaultFunctionNamespace = "functions"

func localNamespace(ns string) bool {
	switch strings.ToLower(strings.TrimSpace(ns)) {
	case "", defaultFunctionNamespace, "default":
		return true
	default:
		return false
	}
}

func mcpPrefixed(s string) bool {
	s = strings.ToLower(s)
	return strings.HasPrefix(s, "mcp__") || strings.HasPrefix(s, "mcp.")
}

func namespaceString(m map[string]any) string {
	switch v := m["namespace"].(type) {
	case string:
		return v
	case map[string]any:
		if n, _ := v["name"].(string); n != "" {
			return n
		}
	}
	return ""
}

func isLocalNamespaceWrapper(m map[string]any) bool {
	typ, _ := m["type"].(string)
	return typ == "namespace" && localNamespace(str(m["name"]))
}

func isExternalNamespace(m map[string]any) bool {
	typ, _ := m["type"].(string)
	return typ == "namespace" && !localNamespace(str(m["name"]))
}

func isMCPNamespace(m map[string]any) bool {
	if mcpPrefixed(str(m["name"])) || mcpPrefixed(namespaceString(m)) {
		return true
	}
	return isExternalNamespace(m)
}

func isSticky(m map[string]any) bool {
	// Why: Discovery tools and deferred schemas keep later tools reachable;
	// removing even a deferred placeholder can disable dynamic tool loading.
	if deferred, _ := m["defer_loading"].(bool); deferred || toolNameOf(m) == "ToolSearch" {
		return true
	}
	if isProviderExecuted(m) || isExternalNamespace(m) {
		return true
	}
	// Why: Instead of whole-catalog skip on one nameless hosted type
	// (observed Grok x_search with keys [type] only), keep unknown-owner
	// items so named local functions stay filterable. Do not add the type
	// to isProviderExecuted — that would bulk-allow every unknown type.
	if unknownHostedType(m) {
		return true
	}
	if ns := namespaceString(m); ns != "" && !localNamespace(ns) {
		return true
	}
	return false
}

// isNamespaced reports a true MCP/external namespace. Local Codex tools
// live under namespace "functions" or names with "." / "__" that are not mcp-prefixed.
func isNamespaced(name string, m map[string]any) bool {
	if isExternalNamespace(m) {
		return true
	}
	if ns := namespaceString(m); ns != "" && !localNamespace(ns) {
		return true
	}
	return mcpPrefixed(name)
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
			case "text", "output_text", "input_text", "tool_use", "tool_result", "thinking", "redacted_thinking", "tool_reference", "":
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

func extractRawTools(root map[string]any) []any {
	if extra := inputToolCatalogs(root); len(extra) > 0 {
		out := append([]any{}, asSlice(root["tools"])...)
		out = append(out, asSlice(root["additional_tools"])...)
		for _, item := range extra {
			out = append(out, asSlice(item["tools"])...)
		}
		return out
	}
	if t := asSlice(root["tools"]); t != nil {
		if extra := asSlice(root["additional_tools"]); extra != nil {
			out := make([]any, 0, len(t)+len(extra))
			return append(append(out, t...), extra...)
		}
		return t
	}
	if t := asSlice(root["functions"]); t != nil {
		return t
	}
	return cursorToolDefs(root)
}

func extractTools(root map[string]any) ([]any, string) {
	if len(inputToolCatalogs(root)) > 0 {
		return flattenCatalog(extractRawTools(root)), "responses_lite"
	}
	if t := asSlice(root["tools"]); t != nil {
		if extra := asSlice(root["additional_tools"]); extra != nil {
			t = append(append([]any{}, t...), extra...)
		}
		return flattenCatalog(t), "tools"
	}
	if t := asSlice(root["functions"]); t != nil {
		return flattenCatalog(t), "functions"
	}
	if t := cursorToolDefs(root); t != nil {
		return t, "mcpTools"
	}
	return nil, "tools"
}

func flattenCatalog(tools []any) []any {
	var out []any
	for _, raw := range tools {
		m, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if isLocalNamespaceWrapper(m) {
			out = append(out, asSlice(m["tools"])...)
			continue
		}
		out = append(out, raw)
	}
	return out
}

func filterableTools(tools []any) []any {
	var out []any
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok || isSticky(m) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func reconstructCatalog(original, kept []any) []any {
	if len(original) == 0 {
		return kept
	}
	keep := map[string]bool{}
	for _, t := range kept {
		m, ok := t.(map[string]any)
		if !ok || isSticky(m) {
			continue
		}
		if n := toolNameOf(m); n != "" {
			keep[n] = true
		}
	}
	var out []any
	for _, raw := range original {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if isLocalNamespaceWrapper(m) {
			var inner []any
			for _, c := range asSlice(m["tools"]) {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				if keep[toolNameOf(cm)] || isSticky(cm) {
					inner = append(inner, c)
				}
			}
			if len(inner) == 0 {
				continue
			}
			cp := cloneMap(m)
			cp["tools"] = inner
			out = append(out, cp)
			continue
		}
		if isSticky(m) {
			out = append(out, raw)
			continue
		}
		if keep[toolNameOf(m)] {
			out = append(out, raw)
		}
	}
	return out
}

func setTools(root map[string]any, key string, tools []any) {
	if key == "responses_lite" {
		tools = flattenCatalog(tools)
		// Why: Lite carries client tools in input, while hosted tools may remain
		// at the top level. Filter each original location without moving schemas.
		for _, item := range inputToolCatalogs(root) {
			item["tools"] = reconstructCatalog(asSlice(item["tools"]), tools)
		}
		for _, field := range []string{"tools", "additional_tools"} {
			if original := asSlice(root[field]); original != nil {
				root[field] = reconstructCatalog(original, tools)
			}
		}
		return
	}
	if key == "mcpTools" {
		setCursorTools(root, tools)
		return
	}
	if key == "" {
		key = "tools"
	}
	root[key] = tools
}

func inputToolCatalogs(root map[string]any) []map[string]any {
	var catalogs []map[string]any
	for _, raw := range asSlice(root["input"]) {
		if item, ok := raw.(map[string]any); ok && item["type"] == "additional_tools" {
			catalogs = append(catalogs, item)
		}
	}
	return catalogs
}

func cursorToolDefs(root map[string]any) []any {
	for _, key := range []string{"mcpTools", "mcp_tools"} {
		switch v := root[key].(type) {
		case []any:
			if len(v) > 0 {
				return v
			}
		case map[string]any:
			for _, inner := range []string{"mcpTools", "mcp_tools", "tools"} {
				if t := asSlice(v[inner]); len(t) > 0 {
					return t
				}
			}
		}
	}
	if action, ok := root["action"].(map[string]any); ok {
		if t := cursorToolDefs(action); t != nil {
			return t
		}
	}
	return nil
}

func setCursorTools(root map[string]any, tools []any) {
	if setCursorToolsAt(root, tools) {
		return
	}
	if action, ok := root["action"].(map[string]any); ok {
		// Why: cursorToolDefs walks action; write the filtered catalog back
		// there instead of inventing a sibling top-level mcpTools key.
		if setCursorToolsAt(action, tools) {
			return
		}
		if cursorToolDefs(action) != nil {
			setCursorTools(action, tools)
			return
		}
	}
	root["mcpTools"] = map[string]any{"mcpTools": tools}
}

func setCursorToolsAt(root map[string]any, tools []any) bool {
	for _, key := range []string{"mcpTools", "mcp_tools"} {
		switch v := root[key].(type) {
		case []any:
			if len(v) > 0 {
				root[key] = tools
				return true
			}
		case map[string]any:
			for _, inner := range []string{"mcpTools", "mcp_tools", "tools"} {
				if t := asSlice(v[inner]); len(t) > 0 {
					v[inner] = tools
					return true
				}
			}
		}
	}
	return false
}

func looksCursorAgent(root map[string]any) bool {
	if cursorToolDefs(root) != nil {
		return true
	}
	_, hasAction := root["action"]
	if _, ok := root["conversationId"]; ok && hasAction {
		return true
	}
	if _, ok := root["conversation_id"]; ok && hasAction {
		return true
	}
	return false
}

func looksPromptChat(root map[string]any) bool {
	if _, ok := root["prompt"].(string); !ok {
		if _, ok := root["message"].(string); !ok {
			return false
		}
	}
	tools, _ := extractTools(root)
	return len(filterableTools(tools)) > 0
}

func fallbackUser(root map[string]any) string {
	if s, _ := root["prompt"].(string); s != "" {
		return s
	}
	if s, _ := root["message"].(string); s != "" {
		return s
	}
	if action, ok := root["action"].(map[string]any); ok {
		return userFromAction(action)
	}
	return ""
}

func userFromAction(action map[string]any) string {
	for _, k := range []string{"messages", "history", "conversationHistory", "conversation_history"} {
		if s := asSlice(action[k]); s != nil {
			_, user := itemsFromMessages(s)
			if user != "" {
				return user
			}
		}
	}
	for _, k := range []string{"userMessage", "user_message", "message"} {
		switch v := action[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			if s := textOf(v); s != "" {
				return s
			}
			if s := firstString(v, "text", "content", "prompt"); s != "" {
				return s
			}
		}
	}
	if s := firstString(action, "prompt", "text", "content"); s != "" {
		return s
	}
	// Why: Cursor AgentRunRequest nests the latest user turn under userMessageAction.
	for _, k := range []string{"userMessageAction", "user_message_action"} {
		if inner, ok := action[k].(map[string]any); ok {
			if s := userFromAction(inner); s != "" {
				return s
			}
		}
	}
	return ""
}

type historySlot struct {
	prefix string
	msgs   []any
	write  func([]any)
}

// Why: Cursor AgentRunRequest keeps chat history in conversationState
// (JSON strings) or nested action fields, not only top-level messages/input.
func locateHistory(root map[string]any) ([]any, func([]any)) {
	if slots := historySlots(root); len(slots) > 0 {
		return slots[0].msgs, slots[0].write
	}
	return nil, func([]any) {}
}

func historySlots(root map[string]any) []historySlot {
	var slots []historySlot
	addSlice := func(holder map[string]any, key, prefix string) {
		if holder == nil {
			return
		}
		raw := asSlice(holder[key])
		if len(raw) == 0 {
			return
		}
		h, k := holder, key
		slots = append(slots, historySlot{
			prefix: prefix,
			msgs:   raw,
			write:  func(compacted []any) { h[k] = compacted },
		})
	}
	addSlice(root, "messages", "messages")
	addSlice(root, "input", "input")
	for _, stateKey := range []string{"conversationState", "conversation_state"} {
		state, ok := root[stateKey].(map[string]any)
		if !ok {
			continue
		}
		for _, msgKey := range []string{"rootPromptMessagesJson", "root_prompt_messages_json"} {
			raw := asSlice(state[msgKey])
			if len(raw) == 0 {
				continue
			}
			msgs, asString := parsePromptMessages(raw)
			if len(msgs) == 0 {
				continue
			}
			st, mk, stringify := state, msgKey, asString
			slots = append(slots, historySlot{
				prefix: stateKey + "." + msgKey,
				msgs:   msgs,
				write: func(compacted []any) {
					st[mk] = encodePromptMessages(compacted, stringify)
				},
			})
		}
	}
	if action, ok := root["action"].(map[string]any); ok {
		for _, key := range []string{"messages", "history", "conversationHistory", "conversation_history"} {
			addSlice(action, key, "action."+key)
		}
		for _, umaKey := range []string{"userMessageAction", "user_message_action"} {
			uma, ok := action[umaKey].(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"conversationHistory", "conversation_history"} {
				addSlice(uma, key, "action."+umaKey+"."+key)
			}
		}
	}
	return slots
}

func parsePromptMessages(raw []any) ([]any, bool) {
	var msgs []any
	asString := false
	for _, el := range raw {
		switch v := el.(type) {
		case string:
			asString = true
			var obj map[string]any
			if json.Unmarshal([]byte(v), &obj) != nil || obj == nil {
				continue
			}
			msgs = append(msgs, obj)
		case map[string]any:
			msgs = append(msgs, v)
		}
	}
	return msgs, asString
}

func encodePromptMessages(msgs []any, asString bool) []any {
	if !asString {
		return msgs
	}
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		out = append(out, string(b))
	}
	return out
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
		// Why: Preserve the supplied capability description; a byte prefix can
		// omit the operations exposed by a code-execution tool or split UTF-8.
		criteria[s.Name] = desc
	}
	criteria[plan.Respond] = "stop calling tools and answer the user. Pick this only when no available tool is needed to make progress on the remaining request."
	qs := map[string]jev.Question{
		"next_tool":  {Type: "choice", Instructions: "Which single available tool should run next to make progress on the user request, given the actions already taken? Use each candidate's supplied description to determine its capabilities, including any operations it exposes through other tools. Do not assume capabilities from a host or tool name. Pick respond_to_user only when no tool is needed now.", Criteria: criteria},
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
	if !finite01(choice.Conf) || !finite01(need.Noul) {
		return plan.Decision{}, reasonInvalidJev, nil
	}
	if _, ok := criteria[choice.Choice]; !ok {
		return plan.Decision{}, reasonInvalidJev, nil
	}
	// Why: Noul returns a probability, not a separate confidence field.
	// Apply its probability thresholds below; only Choice has confidence.
	if choice.Conf < adoptConfidence {
		return plan.Decision{Tool: choice.Choice, Done: need.Noul, Confidence: choice.Conf}, reasonUncertainJev, nil
	}
	if plan.HostMeta(choice.Choice) {
		return plan.Decision{Tool: plan.Respond, Done: need.Noul, Passthrough: true, Confidence: choice.Conf}, "", nil
	}
	if choice.Choice == plan.Respond || need.Noul <= needsToolNo {
		return plan.Decision{Tool: plan.Respond, Done: need.Noul, Passthrough: true, Confidence: choice.Conf}, "", nil
	}
	if need.Noul < needsToolYes {
		return plan.Decision{Tool: choice.Choice, Done: need.Noul, Confidence: choice.Conf}, reasonUncertainJev, nil
	}
	return plan.Decision{Tool: choice.Choice, Done: need.Noul, Confidence: choice.Conf}, "", nil
}

func finite01(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func filterTools(tools []any, name string, referenced map[string]bool) []any {
	var kept, sticky []any
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if isSticky(m) {
			sticky = append(sticky, t)
			continue
		}
		if toolNameOf(m) == name {
			kept = append(kept, t)
		} else if referenced[toolNameOf(m)] {
			// A surviving tool_reference must still resolve to its definition.
			sticky = append(sticky, t)
		}
	}
	if len(kept) == 0 {
		return tools[:0]
	}
	return append(kept, sticky...)
}

func locatePairName(n string) bool {
	switch strings.ToLower(n) {
	case "grep", "grep_files", "read", "read_file":
		return true
	default:
		return false
	}
}

func keepLocatePair(tools, kept []any) []any {
	have := map[string]bool{}
	for _, t := range kept {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if n := toolNameOf(m); n != "" {
			have[n] = true
		}
	}
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		n := toolNameOf(m)
		if !locatePairName(n) || have[n] {
			continue
		}
		kept = append(kept, t)
		have[n] = true
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
			argsValue := m["arguments"]
			if typ == "custom_tool_call" {
				argsValue = m["input"]
			}
			args, isString := argsValue.(string)
			if !isString && argsValue != nil {
				if rawArgs, err := json.Marshal(argsValue); err == nil {
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
			if request := plan.WorkRequest(text); strings.TrimSpace(request) != "" {
				user = plan.PreferTaskText(user, request)
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
			Pinned: hasToolReference(b["content"]),
		})
	}
	return out
}

func hasToolReference(content any) bool {
	return len(toolReferences(content)) > 0
}

func toolReferences(content any) map[string]bool {
	names := map[string]bool{}
	var visit func(any)
	visit = func(content any) {
		for _, raw := range asSlice(content) {
			block, _ := raw.(map[string]any)
			if block["type"] == "tool_reference" {
				names[str(block["tool_name"])] = true
			}
			visit(block["content"])
		}
	}
	visit(content)
	return names
}

func applyCompactToMessages(msgs []any, res compact.Result) []any {
	if len(msgs) == 0 {
		return msgs
	}
	action := map[string]compact.Action{}
	body := map[string]string{}
	for i := range res.Items {
		body[res.Items[i].ID] = res.Items[i].Body
	}
	for _, d := range res.Decisions {
		action[d.ID] = d.Action
	}
	for _, d := range res.Decisions {
		if _, explicit := action[d.ID+"_r"]; !explicit {
			action[d.ID+"_r"] = d.Action
		}
	}
	// Why: Keep boundary predecessors instead of deleting empty messages there;
	// removing them can make an existing system message illegal for the host.
	for i := 1; i < len(msgs); i++ {
		m, _ := msgs[i].(map[string]any)
		if m["role"] != "system" {
			continue
		}
		items, _ := itemsFromMessages(msgs[i-1 : i])
		for _, item := range items {
			if item.Kind == compact.KindCall || item.Kind == compact.KindResult {
				if action[item.ID] == compact.ActionDrop {
					action[item.ID] = compact.ActionKeep
				}
			}
		}
	}
	// Why: Keep a non-thinking block rather than leaving an assistant message
	// containing only signed thinking after dropping all of its tool calls.
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		if m["role"] != "assistant" {
			continue
		}
		hasThinking, hasRemaining := false, false
		for _, rawBlock := range asSlice(m["content"]) {
			block, _ := rawBlock.(map[string]any)
			switch block["type"] {
			case "thinking", "redacted_thinking":
				hasThinking = true
			case "tool_use":
				if action[str(block["id"])] != compact.ActionDrop {
					hasRemaining = true
				}
			default:
				hasRemaining = true
			}
		}
		if hasThinking && !hasRemaining {
			for _, call := range toolCalls(m) {
				action[call.ID] = compact.ActionKeep
			}
		}
	}
	items, _ := itemsFromMessages(msgs)
	results := map[string]bool{}
	for _, item := range items {
		// Tool-search references define tools used later; neither drop nor
		// text truncation may remove them, even if a stale decision asks to.
		if item.Pinned {
			action[item.ID] = compact.ActionKeep
			action[item.PairID] = compact.ActionKeep
		}
		if item.Kind == compact.KindResult {
			results[item.PairID] = true
		}
	}
	for _, item := range items {
		if item.Kind != compact.KindCall {
			continue
		}
		if !results[item.PairID] || (action[item.ID] == compact.ActionDrop) != (action[item.PairID+"_r"] == compact.ActionDrop) {
			action[item.ID] = compact.ActionKeep
			action[item.PairID+"_r"] = compact.ActionKeep
		}
	}
	out := make([]any, 0, len(msgs))
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		m = cloneMap(m)
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
			tid := firstString(m, "tool_call_id", "call_id")
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
				if textOf(m) == "" && len(asSlice(m["content"])) == 0 {
					continue
				}
				delete(m, "tool_calls")
			} else {
				m["tool_calls"] = kept
			}
		}
		out = append(out, m)
	}
	// Why: Check the actual successor instead of pinning every adjacent call;
	// deleting a whole exchange is safe when another assistant turn follows.
	original := 0
	var restore []compact.Decision
	for i, raw := range out {
		m, _ := raw.(map[string]any)
		if m["role"] != "system" {
			continue
		}
		for original < len(msgs) {
			m, _ := msgs[original].(map[string]any)
			original++
			if m["role"] == "system" {
				break
			}
		}
		if i+1 == len(out) || original == len(msgs) {
			continue
		}
		next, _ := out[i+1].(map[string]any)
		successor, _ := msgs[original].(map[string]any)
		if next["role"] == "assistant" || successor["role"] != "assistant" {
			continue
		}
		calls := toolCalls(successor)
		if len(calls) == 0 {
			continue
		}
		for _, call := range calls {
			restore = append(restore, compact.Decision{ID: call.ID, Action: compact.ActionKeep}, compact.Decision{ID: call.ID + "_r", Action: compact.ActionKeep})
		}
	}
	if len(restore) > 0 {
		res.Decisions = append(append([]compact.Decision(nil), res.Decisions...), restore...)
		return applyCompactToMessages(msgs, res)
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
	content := m["content"]
	if content == nil {
		content = m["output"]
	}
	switch c := content.(type) {
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
