package proxy

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

const adoptRuleVersion = "adopt-v1"
const concentrationDenomMin = 1e-6
const defaultCoverage = 0.9

// decidedCostGateMaxCandidates is the Phase 3 production default:
// catalogs of this size or smaller skip the classifier because ceil(N/2)
// saves at most one tool. Tests keep Options.CostGateMax=0.
const decidedCostGateMaxCandidates = 3

func recordDecision(stats *RewriteStats, selectionMode string, client *jev.Client, names []string, decision plan.Decision, asked bool, callErr error) {
	if stats == nil {
		return
	}
	stats.RuleVersion = adoptRuleVersion
	stats.ConnectStatus = jev.ResolveConnect(selectionMode, client, asked, callErr)
	if len(names) > 0 {
		stats.CandidateNames = append([]string(nil), names...)
		stats.CandidateCount = len(names)
	}
	if len(stats.Probabilities) == 0 {
		if len(decision.Probabilities) > 0 {
			stats.Probabilities = copyFloatMap(decision.Probabilities)
		} else if len(decision.Top) > 0 {
			stats.Probabilities = ranksToProbs(decision.Top)
		}
	}
	stats.Concentration = concentrationOf(stats.Probabilities)
}

func copyDecisionRecord(e *Event, s RewriteStats) {
	if e == nil {
		return
	}
	ev := EventFromStats(s)
	e.Probabilities = ev.Probabilities
	e.CandidateNames = ev.CandidateNames
	e.CandidateCount = ev.CandidateCount
	e.MustKeep = ev.MustKeep
	e.MissingFlags = ev.MissingFlags
	e.RuleVersion = ev.RuleVersion
	e.Concentration = ev.Concentration
	e.ConnectStatus = ev.ConnectStatus
	e.ProposedKept = ev.ProposedKept
	e.Shadow = ev.Shadow
	e.Transforms = ev.Transforms
	e.Engine = ev.Engine
	if ev.CompactDelta != 0 {
		e.CompactDelta = ev.CompactDelta
	}
}

func EventFromStats(s RewriteStats) Event {
	ev := Event{
		Host:               string(s.Host),
		Source:             s.Source,
		Reason:             s.Reason,
		Apply:              s.Apply,
		Chosen:             s.Chosen,
		Changed:            s.Changed,
		OriginalModel:      s.OriginalModel,
		SentModel:          s.SentModel,
		ToolBefore:         s.ToolBefore,
		ToolAfter:          s.ToolAfter,
		ToolsBefore:        append([]string(nil), s.ToolsBefore...),
		ToolsAfter:         append([]string(nil), s.ToolsAfter...),
		HistoryTypes:       append([]string(nil), s.HistoryTypes...),
		UnsupportedHistory: append([]string(nil), s.UnsupportedHistory...),
		HistoryIssues:      append([]string(nil), s.HistoryIssues...),
		CompactDropped:     s.CompactDropped,
		CompactApplied:     s.CompactApplied,
		ReasoningChanged:   s.ReasoningChanged,
		Protocol:           s.Protocol,
		Probabilities:      copyFloatMap(s.Probabilities),
		CandidateNames:     append([]string(nil), s.CandidateNames...),
		CandidateCount:     s.CandidateCount,
		MustKeep:           append([]string(nil), s.MustKeep...),
		MissingFlags:       append([]string(nil), s.MissingFlags...),
		RuleVersion:        s.RuleVersion,
		ConnectStatus:      s.ConnectStatus,
		ProposedKept:       append([]string(nil), s.ProposedKept...),
		Shadow:             s.Shadow,
		Transforms:         append([]string(nil), s.Transforms...),
		Engine:             s.Engine,
		CompactDelta:       s.CharsBefore - s.CharsAfter,
	}
	if s.Source != "" {
		c := s.Confidence
		ev.Confidence = &c
	}
	if s.NeedsTool != 0 || s.Source == sourceJev {
		n := s.NeedsTool
		ev.NeedsTool = &n
	}
	if len(s.Probabilities) > 0 || s.Concentration != 0 {
		c := s.Concentration
		if c == 0 {
			c = concentrationOf(s.Probabilities)
		}
		ev.Concentration = &c
	}
	return ev
}

func FormatStats(s RewriteStats) string {
	return FormatEvent(EventFromStats(s))
}

func FormatEvent(e Event) string {
	issues := "-"
	if len(e.HistoryIssues) > 0 {
		issues = strings.Join(e.HistoryIssues, ",")
	}
	return fmt.Sprintf("host=%s tools %d→%d chosen=%s conf=%s needs=%s top=%s connect=%s conc=%s rule=%s compact -%d chars engine=%s history_issues=%s",
		emptyDash(e.Host), e.ToolBefore, e.ToolAfter, emptyDash(e.Chosen),
		formatOptFloat(e.Confidence), formatOptFloat(e.NeedsTool), formatTop(e.Probabilities),
		emptyDash(e.ConnectStatus), formatOptFloat(e.Concentration), emptyDash(e.RuleVersion),
		e.CompactDelta, emptyDash(e.Engine), issues)
}

func RecalculateKept(e Event, coverage float64, maxKeep int) (kept []string, reason string) {
	return adoptCandidates(e.Probabilities, catalogToolNames(e), e.MustKeep, e.MissingFlags, coverage, maxKeep)
}

func adoptCandidates(probs map[string]float64, names, mustKeep, missing []string, coverage float64, maxKeep int) (kept []string, reason string) {
	if coverage <= 0 || coverage > 1 {
		coverage = defaultCoverage
	}
	names = uniqueNames(names)
	if len(names) == 0 {
		return nil, "no_candidates"
	}
	if len(missing) > 0 {
		return append([]string(nil), names...), "missing"
	}
	if !validDistribution(probs) {
		return append([]string(nil), names...), "invalid"
	}
	if maxKeep <= 0 {
		maxKeep = (len(names) + 1) / 2
	}
	ranked := rankChoices(probs)
	var acc float64
	var set []string
	for _, r := range ranked {
		if r.Name == plan.Respond {
			continue
		}
		if !containsName(names, r.Name) {
			continue
		}
		if len(set) >= maxKeep {
			break
		}
		set = append(set, r.Name)
		acc += r.P
		if acc >= coverage {
			break
		}
	}
	if acc < coverage {
		return unionNames(names, mustKeep), "coverage_short"
	}
	return unionNames(set, mustKeep), "coverage"
}

func skipClassifier(n int, missing []string, max int) bool {
	if len(missing) > 0 {
		return true
	}
	return max > 0 && n <= max
}

func concentrationOf(probs map[string]float64) float64 {
	if len(probs) == 0 {
		return 0
	}
	pRespond := probs[plan.Respond]
	pTop := 0.0
	for name, p := range probs {
		if name == plan.Respond {
			continue
		}
		if p > pTop {
			pTop = p
		}
	}
	den := 1 - pRespond
	if den < concentrationDenomMin {
		return 0
	}
	return pTop / den
}

func validDistribution(probs map[string]float64) bool {
	if len(probs) == 0 {
		return false
	}
	sum := 0.0
	pRespond := 0.0
	for name, p := range probs {
		if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return false
		}
		sum += p
		if name == plan.Respond {
			pRespond = p
		}
	}
	if sum <= 0 || sum > 1.01 {
		return false
	}
	return 1-pRespond >= concentrationDenomMin
}

func catalogToolNames(e Event) []string {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n == "" || n == plan.Respond || seen[n] {
			return
		}
		seen[n] = true
		names = append(names, n)
	}
	for _, n := range e.CandidateNames {
		add(n)
	}
	if len(names) == 0 {
		for n := range e.Probabilities {
			add(n)
		}
		sort.Strings(names)
	}
	return names
}

func extractObservedTools(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var names []string
	if json.Valid(raw) {
		names = append(names, observedToolsFromJSON(raw)...)
	} else {
		for _, ev := range sseDataPayloads(raw) {
			names = append(names, observedToolsFromJSON(ev)...)
		}
	}
	return uniqueNames(names)
}

func observedToolsFromJSON(raw []byte) []string {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var names []string
	collectCall := func(obj map[string]any) {
		if obj == nil {
			return
		}
		switch firstString(obj, "type") {
		case "function_call", "custom_tool_call", "local_shell_call", "tool_use":
			if n := firstString(obj, "name"); n != "" {
				names = append(names, n)
			}
		}
		names = append(names, toolsFromMessage(obj)...)
	}
	collectCall(root)
	if cb, ok := root["content_block"].(map[string]any); ok {
		collectCall(cb)
	}
	if item, ok := root["item"].(map[string]any); ok {
		collectCall(item)
	}
	for _, out := range asSlice(root["output"]) {
		obj, _ := out.(map[string]any)
		collectCall(obj)
	}
	if msg := choiceMessage(root); msg != nil {
		names = append(names, toolsFromMessage(msg)...)
	}
	msgs, _ := locateHistory(root)
	for _, m := range msgs {
		obj, _ := m.(map[string]any)
		names = append(names, toolsFromMessage(obj)...)
	}
	return names
}

func toolsFromMessage(obj map[string]any) []string {
	if obj == nil {
		return nil
	}
	var names []string
	if firstString(obj, "type") == "function_call" {
		if n := firstString(obj, "name"); n != "" {
			names = append(names, n)
		}
	}
	for _, tc := range asSlice(obj["tool_calls"]) {
		tcm, _ := tc.(map[string]any)
		if tcm == nil {
			continue
		}
		name := firstString(tcm, "name")
		if fn, ok := tcm["function"].(map[string]any); ok && name == "" {
			name = firstString(fn, "name")
		}
		if name != "" {
			names = append(names, name)
		}
	}
	for _, p := range asSlice(obj["content"]) {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		if firstString(pm, "type") == "tool_use" {
			if n := firstString(pm, "name"); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

func mergeObservedTools(dst, extra []string) []string {
	return uniqueNames(append(append([]string(nil), dst...), extra...))
}

func markObservedCoverage(e *Event) {
	if e == nil || len(e.ObservedTools) == 0 || len(e.ProposedKept) == 0 {
		return
	}
	hit := true
	var missed []string
	for _, name := range e.ObservedTools {
		if containsName(e.ProposedKept, name) {
			continue
		}
		hit = false
		missed = append(missed, name)
	}
	e.ShadowHit = &hit
	e.Misexcluded = missed
}

func copyFloatMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func ranksToProbs(ranks []plan.Rank) map[string]float64 {
	if len(ranks) == 0 {
		return nil
	}
	out := make(map[string]float64, len(ranks))
	for _, r := range ranks {
		out[r.Name] = r.P
	}
	return out
}

func formatTop(probs map[string]float64) string {
	if len(probs) == 0 {
		return "-"
	}
	ranked := rankChoices(probs)
	parts := make([]string, 0, len(ranked))
	for _, r := range ranked {
		parts = append(parts, fmt.Sprintf("%s:%.4g", r.Name, r.P))
	}
	return strings.Join(parts, ",")
}

func formatOptFloat(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.4g", *v)
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func uniqueNames(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range in {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func unionNames(base, extra []string) []string {
	return uniqueNames(append(append([]string(nil), base...), extra...))
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
