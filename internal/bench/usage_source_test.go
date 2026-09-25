package bench

import "testing"

func TestAssignTaskUsageKeepsSourceAndMissingRequestSeparate(t *testing.T) {
	// JevInput/JevOutput are recorded but excluded from TaskTokens -- see
	// docs/MEMO.md "主指標から Jev を除外". grok/devin totals below are
	// upstream-only: 100+10=110 and 100+10=110.
	grok := RunRecord{Agent: "grok", Mode: "on", Requests: 3, Metered: 2, MeterError: "canceled response missing usage", Input: 100, Cached: 40, Output: 10, JevInput: 5, JevOutput: 2, HostUsageVerified: true, OnlyUsageMissing: true, UnreportedUpstream: 1, CanceledUnreported: 1}
	assignTaskUsage(&grok)
	if grok.TaskTokens == nil || *grok.TaskTokens != 110 || grok.UsageSource != "grok_cli_reconciled" || grok.MeterError == "" {
		t.Fatalf("grok=%+v", grok)
	}
	devin := RunRecord{Agent: "devin", AgentModel: "gpt-5-6-terra-medium", Mode: "on", Requests: 2, MeterError: "Connect usage absent", JevInput: 5, JevOutput: 2, HostTranscript: &HostTranscriptUsage{PromptTokens: 100, CompletionTokens: 10, CachedTokens: 40, Steps: 2, ModelNames: []string{"gpt-5-6-terra-medium"}}, OnlyUsageMissing: true, UnreportedUpstream: 2}
	assignTaskUsage(&devin)
	if devin.TaskTokens == nil || *devin.TaskTokens != 110 || devin.UsageSource != "devin_atif_steps" || devin.MeterError == "" {
		t.Fatalf("devin=%+v", devin)
	}
	devin.SubagentCalls = 1
	devin.TaskTokens, devin.UsageSource = nil, ""
	assignTaskUsage(&devin)
	if devin.TaskTokens != nil {
		t.Fatalf("unattributed child accepted: %+v", devin)
	}
	devin.SubagentCalls = 0
	devin.HostTranscript.ModelNames = []string{"different-model"}
	assignTaskUsage(&devin)
	if devin.TaskTokens != nil {
		t.Fatalf("model mismatch accepted: %+v", devin)
	}
}

// TestAssignTaskUsageExcludesCompactApparentUsage covers docs/MEMO.md
// "計測上の教訓": the billed total is upstream real usage only (Jev usage is
// recorded separately and excluded -- see "主指標から Jev を除外").
// A synthesized compaction reply's apparent usage (CompactApparentInput/
// Output) must never be added in, even though it is tracked for the
// CLI/proxy reconciliation correction (host_usage.go).
func TestAssignTaskUsageExcludesCompactApparentUsage(t *testing.T) {
	codex := RunRecord{
		Agent: "codex", Mode: "on", Requests: 1, Metered: 1,
		Input: 900, Output: 60,
		CompactApparentInput: 1, CompactApparentOutput: 42,
	}
	assignTaskUsage(&codex)
	if codex.TaskTokens == nil || *codex.TaskTokens != 960 {
		t.Fatalf("taskTokens = %+v, want 960 (900 input + 60 output, apparent 1+42 excluded)", codex.TaskTokens)
	}
}
