package bench

import (
	"encoding/json"
	"os"
	"sort"
)

// ComparisonFile is the allowlisted, versioned data consumed by the dashboard.
type ComparisonFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	Comparisons   []Comparison   `json:"comparisons"`
	Policy        EffectPolicy   `json:"policy"`
	Effects       []EffectResult `json:"effects"`
}

type Comparison struct {
	Task                   string   `json:"task"`
	Agent                  string   `json:"agent"`
	Model                  string   `json:"model"`
	Rep                    int      `json:"rep"`
	BaselineMode           string   `json:"baselineMode"`
	Status                 string   `json:"status"`
	Reasons                []string `json:"reasons"`
	BaselineSolved         bool     `json:"baselineSolved"`
	SelectionSolved        bool     `json:"selectionSolved"`
	SelectionApplied       bool     `json:"selectionApplied"`
	BaselineUsageSource    string   `json:"baselineUsageSource,omitempty"`
	SelectionUsageSource   string   `json:"selectionUsageSource,omitempty"`
	BaselineChildSessions  *int     `json:"baselineChildSessions,omitempty"`
	SelectionChildSessions *int     `json:"selectionChildSessions,omitempty"`
	BaselineChildTokens    *int     `json:"baselineChildTokens,omitempty"`
	SelectionChildTokens   *int     `json:"selectionChildTokens,omitempty"`
	BaselineTokens         *int     `json:"baselineTokens"`
	SelectionTokens        *int     `json:"selectionTokens"`
	SavedTokens            *int     `json:"savedTokens"`

	BaselineCompactRequested  *int `json:"baselineCompactRequested,omitempty"`
	SelectionCompactRequested *int `json:"selectionCompactRequested,omitempty"`
	// SelectionClearedToolUses/SelectionClearedInputTokens report the
	// intervention side's native context editing activity (--claude-clear).
	// Populated for every pair with an "on" run, not just ClaudeClear ones,
	// so the dashboard can show it was zero rather than absent.
	SelectionClearedToolUses    *int `json:"selectionClearedToolUses,omitempty"`
	SelectionClearedInputTokens *int `json:"selectionClearedInputTokens,omitempty"`
	// SelectionClaudeClear/SelectionClearNetPct carry the on run's same-path
	// net-reduction result (see clearnet.go) into AssessEffects' grouping,
	// separate from the on-vs-off comparison the rest of this row reports.
	SelectionClaudeClear bool     `json:"selectionClaudeClear,omitempty"`
	SelectionClearNetPct *float64 `json:"selectionClearNetPct,omitempty"`
}

// BuildComparisons never invents a token total for a missing or failed pair.
func BuildComparisons(runs []RunRecord) ComparisonFile {
	type key struct {
		task, agent string
		rep         int
	}
	type pair struct {
		off, on, direct *RunRecord
		duplicate       bool
	}
	pairs := map[key]*pair{}
	for i := range runs {
		run := &runs[i]
		if run.Mode != "off" && run.Mode != "on" && run.Mode != "direct" {
			continue
		}
		k := key{run.Task, run.Agent, run.Rep}
		p := pairs[k]
		if p == nil {
			p = &pair{}
			pairs[k] = p
		}
		if run.Mode == "off" {
			if p.off != nil {
				p.duplicate = true
			}
			p.off = run
		} else if run.Mode == "on" {
			if p.on != nil {
				p.duplicate = true
			}
			p.on = run
		} else {
			if p.direct != nil {
				p.duplicate = true
			}
			p.direct = run
		}
	}
	keys := make([]key, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].task != keys[j].task {
			return keys[i].task < keys[j].task
		}
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		return keys[i].rep < keys[j].rep
	})
	policy := EffectPolicy{MinPairs: 6}
	for _, run := range runs {
		if run.EffectMinPairs >= 6 {
			policy.MinPairs = run.EffectMinPairs
			policy.MinSavingsPct = run.EffectMinSavingsPct
			break
		}
	}
	out := ComparisonFile{SchemaVersion: 1, Comparisons: make([]Comparison, 0, len(keys)), Policy: policy}
	for _, k := range keys {
		p := pairs[k]
		row := Comparison{Task: k.task, Agent: k.agent, Rep: k.rep, BaselineMode: "off", Status: "incomparable", Reasons: []string{}}
		if p.on != nil {
			row.Model = p.on.AgentModel
			row.SelectionSolved = p.on.Solved
			row.SelectionApplied = p.on.JevApplied > 0
			if p.on.CodexCompactLimit > 0 {
				row.SelectionCompactRequested = &p.on.CompactRequested
			}
			row.SelectionUsageSource = p.on.UsageSource
			row.SelectionClearedToolUses = &p.on.ClearedToolUses
			row.SelectionClearedInputTokens = &p.on.ClearedInputTokens
			row.SelectionClaudeClear = p.on.ClaudeClear
			row.SelectionClearNetPct = p.on.ClearNetPct
			if p.on.ParentChildVerified {
				row.SelectionChildSessions = &p.on.ChildSessions
				row.SelectionChildTokens = &p.on.ChildTokens
			}
		} else {
			row.Reasons = append(row.Reasons, "missing_selection")
		}
		if p.off != nil {
			if p.off.CodexCompactLimit > 0 {
				row.BaselineCompactRequested = &p.off.CompactRequested
			}
			if row.Model == "" {
				row.Model = p.off.AgentModel
			}
			row.BaselineSolved = p.off.Solved
			row.BaselineUsageSource = p.off.UsageSource
			if p.off.ParentChildVerified {
				row.BaselineChildSessions = &p.off.ChildSessions
				row.BaselineChildTokens = &p.off.ChildTokens
			}
		} else {
			row.Reasons = append(row.Reasons, "missing_baseline")
		}
		if p.duplicate {
			row.Reasons = append(row.Reasons, "duplicate_run")
		}
		if p.off != nil && p.on != nil {
			baselineTokens, baselineOK := taskTokenTotal(*p.off)
			selectionTokens, selectionOK := taskTokenTotal(*p.on)
			if p.off.AgentModel == "" || p.on.AgentModel == "" || p.off.AgentModel != p.on.AgentModel || p.off.CompareKey == "" || p.off.CompareKey != p.on.CompareKey {
				row.Reasons = append(row.Reasons, "settings_mismatch")
			}
			if !modelVerified(*p.off) || !modelVerified(*p.on) {
				row.Reasons = append(row.Reasons, "model_unverified")
			}
			if p.off.Agent == "fake" {
				row.Reasons = append(row.Reasons, "synthetic_agent")
			}
			if !successfulRun(*p.off) || !successfulRun(*p.on) {
				row.Reasons = append(row.Reasons, "quality_failed")
			}
			if k.task == "dual-facts" && (!p.off.EvidenceComplete || !p.on.EvidenceComplete) {
				row.Reasons = append(row.Reasons, "required_tools_unverified")
			}
			if k.task == "xcell-locate" && (!p.off.EvidenceComplete || !p.on.EvidenceComplete) {
				row.Reasons = append(row.Reasons, "tool_sequence_unverified")
			}
			if k.task == "compact-facts" && (!p.off.EvidenceComplete || !p.on.EvidenceComplete) {
				row.Reasons = append(row.Reasons, "full_log_reads_unverified")
			}
			if k.task == "large-facts" && (!p.off.EvidenceComplete || !p.on.EvidenceComplete) {
				row.Reasons = append(row.Reasons, "full_log_reads_unverified")
			}
			childTask := k.task == "child-facts" || k.task == "child-survey"
			if childTask && (!p.off.EvidenceComplete || !p.on.EvidenceComplete || !p.off.ParentChildVerified || !p.on.ParentChildVerified || p.off.ChildSessions < 1 || p.on.ChildSessions < 1) {
				row.Reasons = append(row.Reasons, "child_session_unverified")
			}
			if !childTask && ((p.off.SubagentCalls > 0 && !p.off.ParentChildVerified) || (p.on.SubagentCalls > 0 && !p.on.ParentChildVerified)) {
				row.Reasons = append(row.Reasons, "child_session_unverified")
			}
			if !baselineOK || !selectionOK {
				row.Reasons = append(row.Reasons, "usage_incomplete")
			}
			if (k.agent == "claude" || k.agent == "codex") && (!p.off.HostUsageVerified || !p.on.HostUsageVerified) {
				row.Reasons = append(row.Reasons, "host_usage_unverified")
			}
			// Why: --claude-clear only fires past its input-token trigger, so a
			// run where it never fired is still a legitimate result of the
			// intervention, not an incomplete measurement. Gating those out
			// would keep only the longest runs and bias the comparison.
			// Why: Instead of requiring a compact request on either side, compare
			// all limit-policy runs. Reason: requiring one would select only some
			// execution paths; request counts are reported separately.
			if p.on.CodexCompactLimit == 0 && p.on.CodexToolOutputMax == 0 && !p.on.ClaudeClear && (p.on.JevCalls == 0 || p.on.JevApplied == 0) {
				row.Reasons = append(row.Reasons, "jev_not_applied")
			}
			if len(row.Reasons) == 0 {
				baseline := baselineTokens
				selection := selectionTokens
				saved := baseline - selection
				row.Status = "comparable"
				row.BaselineTokens, row.SelectionTokens, row.SavedTokens = &baseline, &selection, &saved
			}
		}
		if p.off != nil || p.direct == nil {
			out.Comparisons = append(out.Comparisons, row)
		}
		if p.direct != nil {
			direct := Comparison{Task: k.task, Agent: k.agent, Rep: k.rep, BaselineMode: "direct", Status: "incomparable", Reasons: []string{"direct_usage_unverified"}, BaselineSolved: p.direct.Solved}
			if p.on != nil {
				direct.Model = p.on.AgentModel
				direct.SelectionSolved = p.on.Solved
				direct.SelectionApplied = p.on.JevApplied > 0
			} else {
				direct.Model = p.direct.AgentModel
				direct.Reasons = append(direct.Reasons, "missing_selection")
			}
			out.Comparisons = append(out.Comparisons, direct)
		}
	}
	out.Effects = AssessEffects(out, policy)
	return out
}

func successfulRun(r RunRecord) bool {
	return r.ExitCode == 0 && !r.TimedOut && !r.VerifyTimedOut && r.Solved && r.Total > 0 && (r.Isolation == nil || !r.Isolation.Contaminated)
}

func modelVerified(r RunRecord) bool {
	if contains(r.Models, r.AgentModel) {
		return true
	}
	return r.Agent == "devin" && r.UsageSource == "devin_atif_steps" && r.HostTranscript != nil && r.HostTranscriptError == "" &&
		len(r.HostTranscript.ModelNames) == 1 && r.HostTranscript.ModelNames[0] == r.AgentModel
}

func WriteComparisonJSON(path string, runs []RunRecord) error {
	raw, err := json.MarshalIndent(BuildComparisons(runs), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
