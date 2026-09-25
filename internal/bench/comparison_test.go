package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexContextLimitComparisonIncludesNoCompaction(t *testing.T) {
	baseTokens, onTokens := 100, 80
	base := RunRecord{
		Task: "compact-facts", Agent: "codex", AgentModel: "gpt-5.6-terra", Mode: "off", Rep: 1,
		CompareKey: "same", CodexCompactLimit: 45000, CodexCompactBaselineLimit: 900000, Solved: true, Total: 3,
		EvidenceComplete: true,
		Models:           []string{"gpt-5.6-terra"}, HostUsageVerified: true,
		TaskTokens: &baseTokens, UsageSource: "proxy",
	}
	on := base
	on.Mode, on.TaskTokens, on.CompactRequested = "on", &onTokens, 1
	rows := BuildComparisons([]RunRecord{base, on}).Comparisons
	if len(rows) != 1 || rows[0].Status != "comparable" || rows[0].SavedTokens == nil || *rows[0].SavedTokens != 20 || rows[0].SelectionCompactRequested == nil || *rows[0].SelectionCompactRequested != 1 {
		t.Fatalf("compaction pair = %+v", rows)
	}
	base.CompactRequested = 1
	rows = BuildComparisons([]RunRecord{base, on}).Comparisons
	if len(rows) != 1 || rows[0].Status != "comparable" || rows[0].BaselineCompactRequested == nil || *rows[0].BaselineCompactRequested != 1 {
		t.Fatalf("baseline compact request excluded = %+v", rows)
	}
	base.CompactRequested = 0
	on.CompactRequested = 0
	rows = BuildComparisons([]RunRecord{base, on}).Comparisons
	if len(rows) != 1 || rows[0].Status != "comparable" || rows[0].SelectionCompactRequested == nil || *rows[0].SelectionCompactRequested != 0 {
		t.Fatalf("no-compaction policy pair = %+v", rows)
	}
}

func TestCodexCompactEvidenceRequiresFullOrderedReads(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	for _, task := range tasks {
		if task.ID != "compact-facts" {
			continue
		}
		if err := task.Setup(workspace); err != nil {
			t.Fatal(err)
		}
		var lines []byte
		firstFive := 0
		for stage := 1; stage <= 20; stage++ {
			if stage == 6 {
				firstFive = len(lines)
			}
			name := fmt.Sprintf("logs/stage-%d.txt", stage)
			content, err := os.ReadFile(filepath.Join(workspace, name))
			if err != nil {
				t.Fatal(err)
			}
			event, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "status": "completed", "command": "/bin/zsh -lc 'cat " + name + "'", "aggregated_output": string(content)}})
			lines = append(append(lines, event...), '\n')
		}
		log := filepath.Join(t.TempDir(), "agent.log")
		if err := os.WriteFile(log, lines, 0o600); err != nil {
			t.Fatal(err)
		}
		if !codexCompactEvidence(log, workspace) {
			t.Fatal("complete ordered reads rejected")
		}
		first := lines[:bytes.IndexByte(lines, '\n')+1]
		if err := os.WriteFile(log, append(append([]byte{}, first...), lines...), 0o600); err != nil {
			t.Fatal(err)
		}
		if codexCompactEvidence(log, workspace) {
			t.Fatal("duplicate read accepted")
		}
		if err := os.WriteFile(log, lines[:firstFive], 0o600); err != nil {
			t.Fatal(err)
		}
		if codexCompactEvidence(log, workspace) {
			t.Fatal("missing last read accepted")
		}
		return
	}
	t.Fatal("compact-facts task missing")
}

func TestComparisonJSONUsesMeasuredJevAppliedPairsOnly(t *testing.T) {
	base := RunRecord{Task: "task", Agent: "claude", AgentModel: "claude-sonnet-5", CompareKey: "same", Models: []string{"claude-sonnet-5"}, Mode: "off", Rep: 1, Requests: 1, Metered: 1, Input: 100, Output: 10, Passed: 1, Total: 1, Solved: true, HostUsageVerified: true}
	on := base
	on.Mode = "on"
	on.Input = 70
	on.Output = 0
	on.JevInput = 5
	on.JevOutput = 2
	on.JevCalls = 1
	on.JevApplied = 1
	path := filepath.Join(t.TempDir(), "comparison.json")
	if err := WriteComparisonJSON(path, []RunRecord{base, on}); err != nil {
		t.Fatal(err)
	}
	var got ComparisonFile
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &got) != nil {
		t.Fatalf("read comparison: %v", err)
	}
	if got.SchemaVersion != 1 || len(got.Comparisons) != 1 {
		t.Fatalf("shape: %+v", got)
	}
	row := got.Comparisons[0]
	if row.Status != "comparable" || row.SavedTokens == nil || *row.SavedTokens != 33 {
		t.Fatalf("wrong total (Claude cache rules and Jev output): %+v", row)
	}
	on.JevApplied = 0
	on.MeterError = "missing jev usage"
	got = BuildComparisons([]RunRecord{base, on})
	row = got.Comparisons[0]
	if row.Status != "incomparable" || row.SavedTokens != nil || len(row.Reasons) == 0 {
		t.Fatalf("incomplete run claimed savings: %+v", row)
	}
	on.MeterError = ""
	on.JevApplied = 1
	on.CompareKey = "different"
	row = BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "incomparable" || row.SavedTokens != nil {
		t.Fatalf("settings mismatch claimed savings: %+v", row)
	}
	on.CompareKey = "same"
	on.SubagentCalls = 1
	row = BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "incomparable" || row.SavedTokens != nil {
		t.Fatalf("unattributed subagent claimed savings: %+v", row)
	}
}

// TestComparisonClaudeClearIsComparableWithoutJevCalls covers the
// --claude-clear condition: it deliberately makes no Jev calls, so
// jev_not_applied must not gate it; clearedToolUses>0 is the applied signal.
func TestComparisonClaudeClearIsComparableWithoutJevCalls(t *testing.T) {
	base := RunRecord{Task: "task", Agent: "claude", AgentModel: "claude-sonnet-5", CompareKey: "same", Models: []string{"claude-sonnet-5"}, Mode: "off", Rep: 1, Requests: 1, Metered: 1, Input: 100, Output: 10, Passed: 1, Total: 1, Solved: true, HostUsageVerified: true}
	on := base
	on.Mode = "on"
	on.Input = 70
	on.Output = 0
	on.ClaudeClear = true
	on.ClearedToolUses = 2
	on.ClearedInputTokens = 58
	row := BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "comparable" {
		t.Fatalf("claude-clear pair with no Jev calls must be comparable: %+v", row)
	}
	for _, reason := range row.Reasons {
		if reason == "jev_not_applied" {
			t.Fatalf("jev_not_applied must not gate a claude-clear run: %+v", row)
		}
	}

	// A run where the trigger never fired (clearedToolUses=0) is still a
	// legitimate outcome of the intervention, not an incomplete measurement:
	// excluding it would keep only the longest runs and bias the comparison.
	on.ClearedToolUses = 0
	row = BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "comparable" {
		t.Fatalf("clearedToolUses=0 must stay comparable: %+v", row)
	}
	if row.SelectionClearedToolUses == nil || *row.SelectionClearedToolUses != 0 {
		t.Fatalf("selectionClearedToolUses must report 0, not absent: %+v", row)
	}
}

// TestEffectReportsClearedPairRatio covers "消去が発生したペア数／全ペア数":
// AssessEffects must count how many pairs actually cleared, separately from
// whether they were statistically comparable.
func TestEffectReportsClearedPairRatio(t *testing.T) {
	base := RunRecord{Task: "task", Agent: "claude", AgentModel: "claude-sonnet-5", CompareKey: "same", Models: []string{"claude-sonnet-5"}, Mode: "off", Requests: 1, Metered: 1, Input: 100, Passed: 1, Total: 1, Solved: true, HostUsageVerified: true}
	var runs []RunRecord
	for rep := 1; rep <= 6; rep++ {
		off := base
		off.Rep = rep
		on := off
		on.Mode = "on"
		on.ClaudeClear = true
		on.Input = 90
		if rep%2 == 0 {
			on.ClearedToolUses = 3
			on.ClearedInputTokens = 40
		}
		runs = append(runs, off, on)
	}
	effects := BuildComparisons(runs).Effects
	if len(effects) != 1 {
		t.Fatalf("effects = %+v", effects)
	}
	if effects[0].TotalPairs != 6 || effects[0].ClearedPairs != 3 {
		t.Fatalf("cleared/total = %d/%d, want 3/6: %+v", effects[0].ClearedPairs, effects[0].TotalPairs, effects[0])
	}
}

func TestReportRebuildsDashboardComparison(t *testing.T) {
	dir := t.TempDir()
	if err := writeRuns(filepath.Join(dir, "runs.jsonl"), []RunRecord{{Task: "x", Agent: "fake", Mode: "off", Rep: 1}}); err != nil {
		t.Fatal(err)
	}
	if code := reportCmd([]string{dir}); code != 0 {
		t.Fatalf("report exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "comparison.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDirectComparisonStaysUnverified(t *testing.T) {
	direct := RunRecord{Task: "x", Agent: "claude", AgentModel: "claude-sonnet-5", Mode: "direct", Rep: 1, Solved: true}
	on := direct
	on.Mode = "on"
	on.JevApplied = 1
	got := BuildComparisons([]RunRecord{direct, on})
	if len(got.Comparisons) != 1 || got.Comparisons[0].BaselineMode != "direct" || got.Comparisons[0].SavedTokens != nil || got.Comparisons[0].Status != "incomparable" {
		t.Fatalf("direct claimed complete savings: %+v", got)
	}
}

func TestDirectDoesNotHideComparableProxyPair(t *testing.T) {
	off := RunRecord{Task: "x", Agent: "claude", AgentModel: "claude-sonnet-5", Models: []string{"claude-sonnet-5"}, CompareKey: "same", Mode: "off", Rep: 1, Requests: 1, Metered: 1, Input: 100, Solved: true, Total: 1, HostUsageVerified: true}
	on := off
	on.Mode = "on"
	on.JevCalls, on.JevApplied = 1, 1
	direct := off
	direct.Mode = "direct"
	rows := BuildComparisons([]RunRecord{direct, off, on}).Comparisons
	if len(rows) != 2 || rows[0].BaselineMode != "off" || rows[0].Status != "comparable" || rows[1].BaselineMode != "direct" || rows[1].SavedTokens != nil {
		t.Fatalf("direct affected primary pair: %+v", rows)
	}
}

func TestChildFactsNeedsParentAndChildEvidence(t *testing.T) {
	base := RunRecord{Task: "child-facts", Agent: "claude", AgentModel: "claude-sonnet-5", Models: []string{"claude-sonnet-5"}, CompareKey: "same", Mode: "off", Rep: 1, Requests: 1, Metered: 1, Input: 10, Solved: true, Total: 1, HostUsageVerified: true, EvidenceComplete: true, ParentChildVerified: true, ChildSessions: 1, ChildTokens: 5}
	on := base
	on.Mode = "on"
	on.JevCalls = 1
	on.JevApplied = 1
	on.JevInput = 2
	on.JevOutput = 1
	row := BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "comparable" {
		t.Fatalf("complete pair rejected: %+v", row)
	}
	if row.BaselineChildTokens == nil || *row.BaselineChildTokens != 5 {
		t.Fatalf("child total missing: %+v", row)
	}
	on.ParentChildVerified = false
	row = BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "incomparable" || row.SavedTokens != nil {
		t.Fatalf("unverified child claimed savings: %+v", row)
	}
}

func TestXCellLocateNeedsSequenceEvidence(t *testing.T) {
	base := RunRecord{Task: "xcell-locate", Agent: "claude", AgentModel: "claude-sonnet-5", Models: []string{"claude-sonnet-5"}, CompareKey: "same", Mode: "off", Rep: 1, Requests: 1, Metered: 1, Input: 10, Solved: true, Total: 5, HostUsageVerified: true}
	on := base
	on.Mode = "on"
	on.JevCalls, on.JevApplied = 1, 1
	row := BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "incomparable" || row.SavedTokens != nil {
		t.Fatalf("missing sequence claimed savings: %+v", row)
	}
	base.EvidenceComplete, on.EvidenceComplete = true, true
	row = BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "comparable" {
		t.Fatalf("verified sequence rejected: %+v", row)
	}
}

func TestDevinATIFModelCanVerifyComparisonWithoutProxyModelField(t *testing.T) {
	b, s := 100, 90
	base := RunRecord{Task: "xcell-module", Agent: "devin", AgentModel: "gpt-5-6-terra-medium", CompareKey: "same", Mode: "off", Rep: 1, Requests: 2, Solved: true, Total: 3, UsageSource: "devin_atif_steps", TaskTokens: &b, HostTranscript: &HostTranscriptUsage{ModelNames: []string{"gpt-5-6-terra-medium"}}}
	on := base
	on.Mode, on.TaskTokens, on.JevCalls, on.JevApplied = "on", &s, 1, 1
	row := BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "comparable" || row.SavedTokens == nil || *row.SavedTokens != 10 {
		t.Fatalf("ATIF source rejected: %+v", row)
	}
	on.HostTranscript = &HostTranscriptUsage{ModelNames: []string{"other"}}
	row = BuildComparisons([]RunRecord{base, on}).Comparisons[0]
	if row.Status != "incomparable" || row.SavedTokens != nil {
		t.Fatalf("ATIF model mismatch accepted: %+v", row)
	}
}
