package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	reasonTopSetJev          = "top_set_jev"
	reasonPhaseJev           = "phase_jev"
	reasonInvalidJev         = "invalid_jev"
	reasonJevError           = "jev_error"
	reasonKeyMissing         = "key_missing"
	reasonDisabledByConfig   = "disabled_by_config"
	reasonCallFailed         = "call_failed"
	reasonAbstained          = "abstained"
	reasonCoverage           = "coverage"
	reasonMissing            = "missing"
	reasonInvalidDist        = "invalid"
	reasonCoverageShort      = "coverage_short"
	reasonCostGate           = "cost_gate"
	reasonLocalPassthrough   = "local_passthrough"
	reasonNoToolNeeded       = "no_tool_needed"
	reasonHostMeta           = "host_meta"
	reasonForcedUnavailable  = "forced_unavailable"
	reasonIneligibleForced   = "ineligible_forced"
	reasonAliasUnresolved    = "alias_unresolved"
	reasonLocalLookup        = "local_lookup"

	sourceLocal       = "local"
	sourceJev         = "jev"
	sourcePassthrough = "passthrough"

	applyNone   = "none"
	applyFilter = "filter"
	applyForced = "forced"
	applyDirect = "direct"
	// applyAdvise leaves the Claude request intact and appends the decision as a reminder.
	applyAdvise = "advise"

	adoptConfidence = 0.85
	needsToolYes    = 0.8
	needsToolNo     = 0.2
	// topPairMass is the probability the top two options must carry together
	// before an uncertain pick is narrowed to a shortlist instead of passed through.
	topPairMass = 0.90

	assistantPlanRunes = 1500
)

type RewriteStats struct {
	Host               host.ID  `json:"host"`
	ToolBefore         int      `json:"toolBefore"`
	ToolAfter          int      `json:"toolAfter"`
	ToolsBefore        []string `json:"toolsBefore,omitempty"`
	ToolsAfter         []string `json:"toolsAfter,omitempty"`
	HistoryTypes       []string `json:"historyTypes,omitempty"`
	UnsupportedHistory []string `json:"unsupportedHistory,omitempty"`
	HistoryIssues      []string `json:"historyIssues,omitempty"`
	Chosen             string   `json:"chosen"`
	Done               float64  `json:"done"`
	Gated              bool     `json:"gated"`
	CharsBefore        int      `json:"charsBefore"`
	CharsAfter         int      `json:"charsAfter"`
	CompactDropped     int      `json:"compactDropped"`
	Engine             string   `json:"engine"`

	Source           string             `json:"source,omitempty"`
	Reason           string             `json:"reason,omitempty"`
	CatalogRevision  string             `json:"catalogRevision,omitempty"`
	Confidence       float64            `json:"confidence,omitempty"`
	NeedsTool        float64            `json:"needsTool,omitempty"`
	LastActionFailed float64            `json:"lastActionFailed,omitempty"`
	Changed          bool               `json:"changed"`
	Apply            string             `json:"apply,omitempty"`
	OriginalModel    string             `json:"originalModel,omitempty"`
	SentModel        string             `json:"sentModel,omitempty"`
	Direct           bool               `json:"direct,omitempty"`
	DirectName       string             `json:"-"`
	DirectArgs       string             `json:"-"`
	Stream           bool               `json:"-"`
	ForcedTool       string             `json:"forcedTool,omitempty"`
	Protocol         string             `json:"protocol,omitempty"`
	CompactApplied   bool               `json:"compactApplied,omitempty"`
	ReasoningChanged bool               `json:"reasoningChanged,omitempty"`
	Probabilities    map[string]float64 `json:"probabilities,omitempty"`
	CandidateNames   []string           `json:"candidateNames,omitempty"`
	CandidateCount   int                `json:"candidateCount,omitempty"`
	MustKeep         []string           `json:"mustKeep,omitempty"`
	MissingFlags     []string           `json:"missingFlags,omitempty"`
	RuleVersion      string             `json:"ruleVersion,omitempty"`
	Concentration    float64            `json:"concentration,omitempty"`
	ConnectStatus    string             `json:"connectStatus,omitempty"`
	ProposedKept     []string           `json:"proposedKept,omitempty"`
	Shadow           bool               `json:"shadow,omitempty"`
	Transforms       []string           `json:"transforms,omitempty"`
}

func Rewrite(body []byte, h host.ID, client *jev.Client) ([]byte, RewriteStats, error) {
	return RewriteWith(context.Background(), body, h, client, DefaultOptions())
}

func RewriteWith(ctx context.Context, body []byte, h host.ID, client *jev.Client, opt Options) (out []byte, stats RewriteStats, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.Mode == "" {
		opt = DefaultOptions()
	}
	if opt.SelectionMode == "" {
		opt.SelectionMode = SelectionHybrid
	}
	stats = RewriteStats{Host: h, Engine: "local", Apply: applyNone, Source: sourcePassthrough}
	if client != nil && client.Live() {
		stats.Engine = "live"
	}
	var asked bool
	var callErr error
	var decision plan.Decision
	defer func() {
		recordDecision(&stats, opt.SelectionMode, client, stats.ToolsBefore, decision, asked, callErr)
		stats.Shadow = opt.Shadow
		if len(stats.ProposedKept) == 0 && len(decision.Set) > 0 {
			stats.ProposedKept = append([]string(nil), decision.Set...)
		}
		if len(stats.ProposedKept) == 0 && len(stats.ToolsBefore) > 0 {
			stats.ProposedKept = append([]string(nil), stats.ToolsBefore...)
		}
		stats.Transforms = appliedTransforms(opt, stats)
	}()

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		stats.Chosen = "passthrough:" + reasonNotJSON
		stats.Reason = reasonNotJSON
		return body, stats, err
	}
	stats.OriginalModel = modelName(root)
	stats.SentModel = stats.OriginalModel
	// Why: Claude Code resends history without our hints; restoring them keeps
	// earlier messages byte-identical so the prompt cache prefix survives.
	if h == host.Claude && opt.hints.reapply(root) {
		if b, err := json.Marshal(root); err == nil {
			body = b
			stats.Changed = true
		}
	}

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
	stats.ToolsBefore = names
	stats.ToolsAfter = names

	elig := inspectRequest(root)
	stats.Protocol = elig.Protocol
	stats.HistoryTypes = elig.HistoryTypes
	stats.UnsupportedHistory = elig.UnsupportedHistory
	stats.HistoryIssues = elig.HistoryIssues
	if !elig.OK {
		stats.Chosen = "passthrough:" + elig.Reason
		stats.Reason = elig.Reason
		return body, stats, nil
	}

	toolSpecs := plan.SpecsFrom(asMaps(filterable))
	stats.CatalogRevision = plan.RevisionOf(plan.CapabilitiesFromSpecs(toolSpecs, h))
	msgs, _ := locateHistory(root)
	stats.MissingFlags = collectMissingFlags(root)
	if refs := toolReferences(msgs); len(refs) > 0 {
		for name := range refs {
			if name != "" {
				stats.MustKeep = append(stats.MustKeep, name)
			}
		}
	}
	stats.MustKeep = uniqueNames(append(stats.MustKeep, stickyNames(tools)...))
	sort.Strings(stats.MustKeep)
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
	// Why: Claude history is compacted only when Claude Code asks for it
	// (native path). Rewriting old turns every request breaks prompt caching.
	if opt.Compaction != CompactionOff && opt.Transforms.Compact && h != host.Claude {
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
	// A fully resolved definition lookup is answered from the workspace.
	// Skill, MCP, and other tools are removed so the model only generates the reply.
	if h != host.Claude && applyLocalLookup(work, user, actions, opt) {
		stats.Apply = applyFilter
		stats.Reason = reasonLocalLookup
		stats.Source = sourceLocal
		stats.Chosen = plan.Respond
		stats.ToolAfter = 0
		stats.ToolsAfter = nil
		stats.Changed = true
		out, err = json.Marshal(work)
		if err != nil {
			return body, stats, err
		}
		return out, stats, nil
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
	// Why: Claude tool definitions head Anthropic's prompt cache, so narrowing
	// tools[] or setting tool_choice rebuilds the whole cache (and Opus 5.5
	// rejects forced tool_choice). Claude gets the decision as a reminder instead.
	advise := h == host.Claude && !opt.Shadow && opt.Transforms.Filter
	adviseClaude := func(tool string) ([]byte, RewriteStats, error) {
		stats.Chosen = tool
		stats.Done = decision.Done
		stats.Gated = decision.Gated
		msgs := asSlice(work["messages"])
		var last map[string]any
		if len(msgs) > 0 {
			last, _ = msgs[len(msgs)-1].(map[string]any)
		}
		key := toolResultKey(last)
		if key == "" {
			return withoutSelection()
		}
		next := tool + " (other tools remain available)"
		if tool == plan.Respond {
			next = "reply to the user without calling a tool"
		}
		appendHintBlock(last, opt.hints.remember(key, "<system-reminder>jev-routing: suggested next step: "+next+".</system-reminder>"))
		stats.Apply = applyAdvise
		stats.Changed = true
		if stats.Reason == "" {
			stats.Reason = stats.Source
		}
		out, err := json.Marshal(work)
		if err != nil {
			return body, stats, err
		}
		return out, stats, nil
	}
	localFallback := func() bool {
		if opt.SelectionMode != SelectionJev || decision.Passthrough || decision.Tool == plan.Respond {
			return false
		}
		available := map[string]bool{}
		for _, tool := range tools {
			if item, ok := tool.(map[string]any); ok {
				available[toolNameOf(item)] = true
			}
		}
		resolved := catalogAliasIn(available, decision.Tool)
		if resolved == "" {
			return false
		}
		decision.Tool = resolved
		decision.Set = []string{resolved}
		return true
	}
	if len(names) == 0 {
		stats.Chosen = "passthrough:" + reasonNoCatalog
		stats.Reason = reasonNoCatalog
		return withoutSelection()
	}

	decision = plan.DecideSpecs(user, actions, toolSpecs, h)
	stats.Source = sourceLocal
	stats.Confidence = decision.Confidence

	if plan.HostMeta(decision.Tool) {
		decision.Passthrough = true
		stats.Reason = reasonHostMeta
	}

	usedJev := false
	// Why: Pseudo local confidence (0.86 / score/8) is not a measured hit rate.
	// Hybrid skips Jev only for selected/constraint outcomes, never for word-match defer.
	shouldAskJev := opt.SelectionMode == SelectionJev ||
		(opt.SelectionMode == SelectionHybrid && decision.Outcome != plan.OutcomeSelected)
	if shouldAskJev && skipClassifier(len(names), stats.MissingFlags, opt.CostGateMax) {
		if len(stats.MissingFlags) > 0 {
			stats.Reason = reasonMissing
		} else {
			stats.Reason = reasonCostGate
		}
		stats.Chosen = "passthrough:" + stats.Reason
		return withoutSelection()
	}
	if shouldAskJev && (client == nil || !client.Live()) {
		// Why: A deferred hint must not shrink the catalog when Jev is missing.
		// An already-open local passthrough has nothing extra to preserve.
		if decision.Passthrough || decision.Tool == plan.Respond {
			stats.Reason = reasonLocalPassthrough
			stats.Chosen = "passthrough"
			return withoutSelection()
		}
		if localFallback() {
			// Why: A validated local decision keeps the catalog useful when JEV is unavailable.
			stats.Reason = reasonKeyMissing + "_local"
		} else {
			stats.Reason = reasonKeyMissing
			stats.Chosen = "passthrough:" + reasonKeyMissing
			return withoutSelection()
		}
	}
	if shouldAskJev && client != nil && client.Live() {
		asked = true
		live, verr, err := askNextTool(ctx, client, user, actions, toolSpecs, lastAssistantText(msgs), func() map[string]any {
			extra := judgmentExtras(user, actions, items, names, root, stats.MustKeep, stats.MissingFlags)
			extra["criteria_enabled"] = opt.Transforms.Criteria
			return extra
		}())
		if err != nil {
			callErr = err
			if localFallback() {
				// Why: A failed remote selection must not discard a safe local choice.
				stats.Reason = reasonCallFailed + "_local"
			} else {
				stats.Reason = reasonCallFailed
				stats.Chosen = "passthrough:" + reasonCallFailed
				return withoutSelection()
			}
		}
		if err == nil {
			stats.Source = sourceJev
			stats.Confidence = live.Confidence
			stats.NeedsTool = live.Done
			stats.LastActionFailed = live.LastFailed
			decision = live
			usedJev = true
			if verr != "" {
				stats.Reason = verr
			}
			if decision.Passthrough && !(advise && stats.Reason == reasonNoToolNeeded) {
				stats.Chosen = "passthrough:" + stats.Reason
				stats.ToolAfter = stats.ToolBefore
				stats.Done = decision.Done
				stats.Gated = decision.Gated
				return withoutSelection()
			}
		}
	}

	if decision.Passthrough || decision.Tool == plan.Respond {
		if advise && stats.Reason == reasonNoToolNeeded {
			return adviseClaude(plan.Respond)
		}
		if stats.Reason == "" {
			stats.Reason = reasonLocalPassthrough
		}
		stats.Chosen = "passthrough"
		stats.ToolAfter = stats.ToolBefore
		stats.Done = decision.Done
		stats.Gated = decision.Gated
		return withoutSelection()
	}

	keep := decision.Set
	if len(keep) == 0 {
		keep = []string{decision.Tool}
	}
	available := map[string]bool{}
	for _, t := range tools {
		if m, ok := t.(map[string]any); ok {
			available[toolNameOf(m)] = true
		}
	}
	if catalogAliasIn(available, decision.Tool) == "" {
		stats.Chosen = "passthrough:" + reasonAliasUnresolved
		stats.Reason = reasonAliasUnresolved
		stats.ToolAfter = stats.ToolBefore
		return withoutSelection()
	}
	kept, aliasesResolved := filterTools(tools, keep, toolReferences(msgs))
	if !aliasesResolved {
		stats.Chosen = "passthrough:" + reasonAliasUnresolved
		stats.Reason = reasonAliasUnresolved
		stats.ToolAfter = stats.ToolBefore
		return withoutSelection()
	}
	if plan.SequentialLocate(user) {
		kept = keepLocatePair(tools, kept)
	}
	if len(kept) == 0 {
		stats.Chosen = "passthrough:" + decision.Tool
		stats.Reason = "missing_tool"
		stats.ToolAfter = stats.ToolBefore
		return withoutSelection()
	}
	stats.ProposedKept = plan.ToolNames(asMaps(filterableTools(kept)))
	if opt.Shadow || !opt.Transforms.Filter {
		stats.Apply = applyNone
		stats.Chosen = decision.Tool
		if opt.Shadow {
			stats.Chosen = "shadow:" + decision.Tool
		}
		stats.ToolAfter = stats.ToolBefore
		stats.ToolsAfter = stats.ToolsBefore
		stats.Changed = stats.CompactApplied
		return withoutSelection()
	}
	if advise {
		return adviseClaude(decision.Tool)
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
	stats.ToolsAfter = plan.ToolNames(asMaps(filterableTools(kept)))
	stats.Changed = true
	if stats.Reason == "" {
		if usedJev {
			stats.Reason = sourceJev
		} else {
			stats.Reason = sourceLocal
		}
	}

	out, err = json.Marshal(work)
	if err != nil {
		return body, stats, err
	}
	return out, stats, nil
}

type eligibility struct {
	OK                 bool
	ForcedOK           bool
	Reason             string
	Protocol           string
	HistoryTypes       []string
	UnsupportedHistory []string
	HistoryIssues      []string
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
	types, unsupported, issues := historyShape(hist)
	if reason := historyReason(hist); reason != "" {
		return eligibility{Reason: reason, Protocol: protocol, HistoryTypes: types, UnsupportedHistory: unsupported, HistoryIssues: issues}
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
	case "prompt":
		forcedOK = false
	default:
		forcedOK = false
	}
	return eligibility{OK: true, ForcedOK: forcedOK, Protocol: protocol, HistoryTypes: types, UnsupportedHistory: unsupported, HistoryIssues: issues}
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
			continue
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
			continue
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
	if toolNameOf(m) == "" {
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

func historyShape(msgs []any) (types, unsupported, issues []string) {
	// Why: Record type labels instead of request bodies so unsupported formats are diagnosable without retaining prompts or tool arguments.
	seenTypes, seenUnsupported := map[string]bool{}, map[string]bool{}
	add := func(dst *[]string, seen map[string]bool, value string) {
		if value == "" || seen[value] || len(*dst) >= 32 {
			return
		}
		seen[value] = true
		*dst = append(*dst, value)
	}
	var content func(any, int)
	content = func(v any, item int) {
		parts, ok := v.([]any)
		if !ok {
			return
		}
		for _, raw := range parts {
			p, ok := raw.(map[string]any)
			if !ok {
				add(&types, seenTypes, "content:<non-object>")
				add(&unsupported, seenUnsupported, "content:<non-object>")
				add(&issues, seenUnsupported, fmt.Sprintf("input[%d].content:<non-object>", item))
				continue
			}
			typ, _ := p["type"].(string)
			label := "content:" + typ
			if typ == "" {
				label = "content:<missing>"
			}
			add(&types, seenTypes, label)
			switch typ {
			case "text", "output_text", "input_text", "tool_use", "tool_result", "thinking", "redacted_thinking", "tool_reference", "tool_addition", "tool_removal", "":
			case "image", "image_url", "input_image", "image_file":
				add(&unsupported, seenUnsupported, label)
				add(&issues, seenUnsupported, fmt.Sprintf("input[%d].%s", item, label))
			default:
				if _, hasText := p["text"]; !hasText {
					add(&unsupported, seenUnsupported, label)
					add(&issues, seenUnsupported, fmt.Sprintf("input[%d].%s", item, label))
				}
			}
			content(p["content"], item)
		}
	}
	for i, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			add(&types, seenTypes, "item:<non-object>")
			add(&unsupported, seenUnsupported, "item:<non-object>")
			add(&issues, seenUnsupported, fmt.Sprintf("input[%d]:<non-object>", i))
			continue
		}
		typ, _ := m["type"].(string)
		label := "item:" + typ
		if typ == "" {
			label = "item:<missing>"
		}
		add(&types, seenTypes, label)
		switch typ {
		case "agent_message":
			if _, ok := m["text"].(string); !ok {
				add(&unsupported, seenUnsupported, label)
				add(&issues, seenUnsupported, fmt.Sprintf("input[%d].%s", i, label))
			}
		case "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "local_shell_call", "local_shell_call_output",
			"tool_search_call", "web_search_call", "file_search_call", "computer_call", "computer_call_output",
			"image_generation_call", "code_interpreter_call", "shell_call", "shell_call_output", "apply_patch_call", "apply_patch_call_output",
			"mcp_call", "mcp_call_output", "message", "reasoning", "item_reference", "":
		default:
			if _, hasRole := m["role"]; !hasRole {
				add(&unsupported, seenUnsupported, label)
				issue := fmt.Sprintf("input[%d].%s", i, label)
				if tool, _ := m["name"].(string); tool != "" {
					issue += " tool=" + clip(tool, eventStrMax)
				}
				add(&issues, seenUnsupported, issue)
			}
		}
		content(m["content"], i)
		content(m["output"], i)
		content(m["input"], i)
	}
	return types, unsupported, issues
}

func isOpaqueAgentMessage(m map[string]any) bool {
	if m == nil {
		return false
	}
	if _, ok := m["encrypted_content"]; ok {
		return true
	}
	_, hasContent := m["content"].([]any)
	return hasContent
}

func messageReason(m map[string]any) string {
	typ, _ := m["type"].(string)
	switch typ {
	case "agent_message":
		if text, hasText := m["text"]; hasText {
			if _, ok := text.(string); !ok {
				return reasonUnknownHistory
			}
		}
		if isOpaqueAgentMessage(m) {
			return ""
		}
	case "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "local_shell_call", "local_shell_call_output",
		"tool_search_call", "web_search_call", "file_search_call", "computer_call", "computer_call_output",
		"image_generation_call", "code_interpreter_call", "shell_call", "shell_call_output", "apply_patch_call", "apply_patch_call_output",
		"mcp_call", "mcp_call_output", "message", "reasoning", "item_reference", "":
		// known Responses / Chat
	default:
		if _, hasRole := m["role"]; !hasRole {
			return reasonUnrecognizedFormat
		}
	}
	for _, key := range []string{"content", "output", "input"} {
		if v, ok := m[key]; ok {
			if reason := contentReason(v); reason != "" {
				return reason
			}
		}
	}
	return ""
}

func contentReason(v any) string {
	switch t := v.(type) {
	case string, nil, map[string]any:
		return ""
	case []any:
		for _, raw := range t {
			p, ok := raw.(map[string]any)
			if !ok {
				return reasonUnknownHistory
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "text", "output_text", "input_text", "tool_use", "tool_result", "thinking", "redacted_thinking", "tool_reference", "tool_addition", "tool_removal", "":
			case "image", "image_url", "input_image", "image_file", "encrypted_content":
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
	return nil
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
	return ""
}

type historySlot struct {
	prefix string
	msgs   []any
	write  func([]any)
}

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
	if action, ok := root["action"].(map[string]any); ok {
		for _, key := range []string{"messages", "history", "conversationHistory", "conversation_history"} {
			addSlice(action, key, "action."+key)
		}
	}
	return slots
}

func askNextTool(ctx context.Context, c *jev.Client, user string, actions []plan.Action, specs []plan.Spec, assistantPlan string, extra map[string]any) (plan.Decision, string, error) {
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
		criteriaEnabled, _ := extra["criteria_enabled"].(bool)
		criteria[s.Name] = criteriaFor(s.Name, desc, criteriaEnabled)
	}
	criteria[plan.Respond] = "stop calling tools and answer the user. Pick this only when no available tool is needed to make progress on the remaining request."
	qs := map[string]jev.Question{
		"next_tool":  {Type: "choice", Instructions: "Given `user_request`, `actions_taken` and the assistant's stated intention in `assistant_plan`, which single available tool should run next to make progress? Use each candidate's supplied description to determine its capabilities, including any operations it exposes through other tools. Do not assume capabilities from a host or tool name. Pick respond_to_user only when no tool is needed now.", Criteria: criteria},
		"needs_tool": {Type: "noul", Instructions: "A tool call is needed now to make progress on `user_request`. This is not a judgment that the overall user task is complete."},
		"task_phase": {Type: "choice", Optional: true, Instructions: "Assuming work on `user_request` continues, what kind of step comes next after `actions_taken`?", Criteria: map[string]string{
			plan.PhaseLocate:  "Finding where something is (search, list, glob)",
			plan.PhaseRead:    "Reading known files or fetching content",
			plan.PhaseModify:  "Editing, writing, or creating files",
			plan.PhaseExecute: "Running commands, tests, builds",
			plan.PhaseRespond: "No further tool is needed; answer the user",
		}},
		"last_action_failed": {Type: "noul", Optional: true, Instructions: "The most recent entry in `actions_taken` failed or returned an error. If `actions_taken` is empty, answer no."},
		"repeat_same_tool":   {Type: "noul", Optional: true, Instructions: "Assuming a tool is needed next, it is the same tool as the most recent entry in `actions_taken`. If `actions_taken` is empty, answer no."},
	}
	state := map[string]any{"user_request": user, "actions_taken": actions, "assistant_plan": assistantPlan}
	for k, v := range extra {
		if k == "" || v == nil {
			continue
		}
		state[k] = v
	}
	res, err := c.AskSelectionContext(ctx, state, qs)
	if err != nil {
		return plan.Decision{}, reasonCallFailed, err
	}
	choice, choiceOK := jev.ParseChoice(res, "next_tool")
	need, needOK := jev.ParseNoul(res, "needs_tool")
	if !choiceOK || !needOK {
		return plan.Decision{Passthrough: true}, reasonInvalidJev, nil
	}
	withProbs := func(d plan.Decision) plan.Decision {
		d.Probabilities = copyFloatMap(choice.Probabilities)
		return d
	}
	if !finite01(choice.Conf) || !finite01(need.Noul) {
		return plan.Decision{Passthrough: true}, reasonInvalidJev, nil
	}
	if _, ok := criteria[choice.Choice]; !ok {
		return plan.Decision{Passthrough: true}, reasonInvalidJev, nil
	}
	if choice.Choice == plan.Respond {
		return withProbs(plan.Decision{Tool: plan.Respond, Done: need.Noul, Passthrough: true, Confidence: choice.Conf}), reasonNoToolNeeded, nil
	}
	failed, _ := jev.ParseNoul(res, "last_action_failed")
	names := make([]string, 0, len(criteria))
	for n := range criteria {
		if n != plan.Respond {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	mustKeep, _ := extra["must_keep"].([]string)
	missing, _ := extra["missing_flags"].([]string)
	kept, why := adoptCandidates(choice.Probabilities, names, mustKeep, missing, defaultCoverage, 0)
	d := withProbs(plan.Decision{Tool: choice.Choice, Done: need.Noul, Confidence: choice.Conf, LastFailed: failed.Noul})
	switch why {
	case "coverage":
		d.Set = kept
		if d.Tool == plan.Respond || !containsName(kept, d.Tool) {
			if len(kept) > 0 {
				d.Tool = kept[0]
			}
		}
		return d, reasonCoverage, nil
	case "missing":
		d.Passthrough = true
		return d, reasonMissing, nil
	case "invalid":
		d.Passthrough = true
		return d, reasonInvalidDist, nil
	case "coverage_short":
		d.Passthrough = true
		return d, reasonCoverageShort, nil
	default:
		d.Passthrough = true
		return d, why, nil
	}
}

// rankChoices orders a choice distribution by probability, descending.
func rankChoices(probs map[string]float64) []plan.Rank {
	out := make([]plan.Rank, 0, len(probs))
	for name, p := range probs {
		out = append(out, plan.Rank{Name: name, P: p})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].P != out[j].P {
			return out[i].P > out[j].P
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// topSet keeps the leading pair when it carries nearly all the probability
// mass: the single pick is uncertain, the shortlist is not. respond_to_user
// counts toward the mass but never joins the catalog, so a pair containing it
// has no two real tools to keep.
func topSet(ranked []plan.Rank, inCatalog func(string) bool) []string {
	if len(ranked) < 2 || ranked[0].P+ranked[1].P < topPairMass {
		return nil
	}
	if !inCatalog(ranked[0].Name) || !inCatalog(ranked[1].Name) {
		return nil
	}
	return []string{ranked[0].Name, ranked[1].Name}
}

// phaseSet shortlists the catalog by the phase of work coming next. Tools the
// local classifier does not recognize are kept under every phase.
func phaseSet(res *jev.Response, specs []plan.Spec) []string {
	phase, ok := jev.ParseChoice(res, "task_phase")
	if !ok || phase.Conf < adoptConfidence || phase.Choice == plan.PhaseRespond {
		return nil
	}
	var names []string
	matched, eligible := 0, 0
	for _, s := range specs {
		if plan.HostMeta(s.Name) {
			names = append(names, s.Name) // a host UX tool is never dropped
			continue
		}
		eligible++
		if p := plan.PhaseOf(s); p == "" || p == phase.Choice {
			names = append(names, s.Name)
			matched++
		}
	}
	if matched == 0 || matched == eligible {
		return nil // nothing to restrict, or nothing left to run
	}
	return names
}

// withRepeatedTool keeps the last action's tool in a shortlist when Jev expects
// it to run again.
func withRepeatedTool(set []string, res *jev.Response, actions []plan.Action, inCatalog func(string) bool) []string {
	repeat, ok := jev.ParseNoul(res, "repeat_same_tool")
	if !ok || repeat.Noul < needsToolYes || len(actions) == 0 {
		return set
	}
	last := actions[len(actions)-1].Tool
	if !inCatalog(last) {
		return set
	}
	for _, n := range set {
		if n == last {
			return set
		}
	}
	return append(set, last)
}

func finite01(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func filterTools(tools []any, names []string, referenced map[string]bool) ([]any, bool) {
	available := map[string]bool{}
	for _, t := range tools {
		if m, ok := t.(map[string]any); ok {
			available[toolNameOf(m)] = true
		}
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		resolved := catalogAliasIn(available, n)
		if resolved != "" {
			want[resolved] = true
		}
	}
	if len(want) == 0 {
		return tools, false
	}
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
		if want[toolNameOf(m)] {
			kept = append(kept, t)
		} else if referenced[toolNameOf(m)] {
			// A surviving tool_reference must still resolve to its definition.
			sticky = append(sticky, t)
		}
	}
	if len(kept) == 0 {
		return tools[:0], true
	}
	return append(kept, sticky...), true
}

func catalogAliasIn(available map[string]bool, want string) string {
	if alias := plan.AliasIn(available, want); alias != "" {
		return alias
	}
	switch want {
	case "grep_search":
		return plan.AliasIn(available, "Grep")
	case "task":
		return plan.AliasIn(available, "Agent")
	default:
		return ""
	}
}

func locatePairName(n string) bool {
	switch strings.ToLower(n) {
	case "grep", "grep_search", "grep_files", "read", "read_file":
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
	if h != host.Claude {
		if _, ok := root["thinking"]; ok {
			root["thinking"] = map[string]any{"type": "disabled"}
		}
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

func responseCallType(typ string) bool {
	switch typ {
	case "function_call", "custom_tool_call", "local_shell_call", "shell_call",
		"apply_patch_call", "mcp_call", "computer_call":
		return true
	default:
		return false
	}
}

func responseOutputType(typ string) bool {
	switch typ {
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "shell_call_output",
		"apply_patch_call_output", "mcp_call_output", "computer_call_output":
		return true
	default:
		return false
	}
}

func responseToolType(typ string) bool {
	return responseCallType(typ) || responseOutputType(typ)
}

func responseToolName(typ string, m map[string]any) string {
	if n := str(m["name"]); n != "" {
		return n
	}
	switch typ {
	case "local_shell_call", "local_shell_call_output", "shell_call", "shell_call_output":
		return "shell"
	case "apply_patch_call", "apply_patch_call_output":
		return "apply_patch"
	case "computer_call", "computer_call_output":
		return "computer"
	case "mcp_call", "mcp_call_output":
		return "mcp"
	default:
		return ""
	}
}

func callArgs(m map[string]any, typ string) string {
	switch typ {
	case "custom_tool_call":
		if s, ok := m["input"].(string); ok {
			return s
		}
		if m["input"] != nil {
			if raw, err := json.Marshal(m["input"]); err == nil {
				return string(raw)
			}
		}
	}
	if s, ok := m["arguments"].(string); ok {
		return s
	}
	if m["arguments"] != nil {
		if raw, err := json.Marshal(m["arguments"]); err == nil {
			return string(raw)
		}
	}
	for _, key := range []string{"action", "call", "input", "command"} {
		if m[key] == nil {
			continue
		}
		if s, ok := m[key].(string); ok {
			return s
		}
		if raw, err := json.Marshal(m[key]); err == nil {
			return string(raw)
		}
	}
	return ""
}

func outputBody(m map[string]any) string {
	if s := firstString(m, "output", "result"); s != "" {
		return s
	}
	return textOf(m)
}

func textParts(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, raw := range arr {
		part, _ := raw.(map[string]any)
		if t, ok := part["text"].(string); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

func responsesToolItem(m map[string]any, typ, cid string) compact.Item {
	name := responseToolName(typ, m)
	if responseOutputType(typ) {
		body := outputBody(m)
		return compact.Item{
			ID: cid + "_r", Kind: compact.KindResult, PairID: cid, Tool: name,
			Chars: len(body), Preview: clip(body, 200), Body: body,
		}
	}
	args := callArgs(m, typ)
	return compact.Item{
		ID: cid, Kind: compact.KindCall, PairID: cid, Tool: name,
		Chars: len(args), Preview: clip(args, 200), Body: args,
	}
}

// setTruncatedOutput rewrites string and text-part outputs in place.
// An output array with no text (for example an image) stays as-is.
func setTruncatedOutput(m map[string]any, body string) {
	if arr, ok := m["output"].([]any); ok && textParts(arr) == "" {
		return
	}
	m["output"] = body
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
		case responseToolType(typ):
			cid := firstString(m, "call_id", "id")
			if cid == "" {
				cid = id()
			}
			items = append(items, responsesToolItem(m, typ, cid))
		case typ == "agent_message":
			text, _ := m["text"].(string)
			if text != "" {
				items = append(items, compact.Item{ID: id(), Kind: compact.KindText, Chars: len(text), Preview: clip(text, 200), Body: text})
			}
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

// lastAssistantText is the prose of the most recent assistant message, in any
// host format: the plan it just stated is the best hint at the next tool.
// Why: Stop at that message even when it is all tool calls — older prose
// describes a plan the assistant has already moved past.
func lastAssistantText(msgs []any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "agent_message" {
			text, _ := m["text"].(string)
			text = strings.TrimSpace(text)
			if runes := []rune(text); len(runes) > assistantPlanRunes {
				return string(runes[:assistantPlanRunes])
			}
			return text
		}
		role, _ := m["role"].(string)
		if role != "assistant" {
			continue
		}
		if typ, _ := m["type"].(string); typ != "" && typ != "message" {
			continue
		}
		text := strings.TrimSpace(textOf(m))
		if runes := []rune(text); len(runes) > assistantPlanRunes {
			return string(runes[:assistantPlanRunes])
		}
		return text
	}
	return ""
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
		if typ == "agent_message" && isOpaqueAgentMessage(m) {
			out = append(out, m)
			continue
		}
		if responseCallType(typ) {
			cid := firstString(m, "call_id", "id")
			if action[cid] == compact.ActionDrop {
				continue
			}
			out = append(out, m)
			continue
		}
		if responseOutputType(typ) {
			cid := firstString(m, "call_id", "id")
			act := action[cid+"_r"]
			if act == compact.ActionDrop {
				continue
			}
			if act == compact.ActionTruncate {
				if b, ok := body[cid+"_r"]; ok {
					setTruncatedOutput(m, b)
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

// hintStore remembers the reminder appended to each Claude user message, keyed
// by the message's first tool_use_id, so later requests can restore it.
type hintStore struct {
	mu    sync.Mutex
	text  map[string]string
	order []string
}

// ponytail: FIFO bound of 4096 keys; an evicted hint drops out of history once
// (one cache rebuild). Key per conversation if that ever shows up in costs.
const hintStoreMax = 4096

func newHintStore() *hintStore { return &hintStore{text: map[string]string{}} }

// remember records text for key unless a hint is already stored, and returns
// the stored hint so a retried request carries the same bytes.
func (s *hintStore) remember(key, text string) string {
	if s == nil {
		return text
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.text[key]; ok {
		return old
	}
	s.text[key] = text
	s.order = append(s.order, key)
	if len(s.order) > hintStoreMax {
		delete(s.text, s.order[0])
		s.order = s.order[1:]
	}
	return text
}

// reapply appends every stored hint to its user message; true when any was added.
func (s *hintStore) reapply(root map[string]any) bool {
	if s == nil {
		return false
	}
	changed := false
	for _, raw := range asSlice(root["messages"]) {
		msg, _ := raw.(map[string]any)
		key := toolResultKey(msg)
		if key == "" {
			continue
		}
		s.mu.Lock()
		text, ok := s.text[key]
		s.mu.Unlock()
		if ok && appendHintBlock(msg, text) {
			changed = true
		}
	}
	return changed
}

// toolResultKey is the tool_use_id of a user message's first tool_result, or "".
func toolResultKey(msg map[string]any) string {
	if msg == nil || msg["role"] != "user" {
		return ""
	}
	for _, raw := range asSlice(msg["content"]) {
		if b, ok := raw.(map[string]any); ok && b["type"] == "tool_result" {
			id, _ := b["tool_use_id"].(string)
			return id
		}
	}
	return ""
}

// appendHintBlock adds text as the message's last block unless it already is.
func appendHintBlock(msg map[string]any, text string) bool {
	content := asSlice(msg["content"])
	if n := len(content); n > 0 {
		if last, ok := content[n-1].(map[string]any); ok && last["type"] == "text" && last["text"] == text {
			return false
		}
	}
	msg["content"] = append(content, map[string]any{"type": "text", "text": text})
	return true
}
