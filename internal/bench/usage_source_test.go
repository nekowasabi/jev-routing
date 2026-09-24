package bench

import "testing"

func TestAssignTaskUsageKeepsSourceAndMissingRequestSeparate(t *testing.T) {
	grok := RunRecord{Agent: "grok", Mode: "on", Requests: 3, Metered: 2, MeterError: "canceled response missing usage", Input: 100, Cached: 40, Output: 10, JevInput: 5, JevOutput: 2, HostUsageVerified: true, OnlyUsageMissing: true, UnreportedUpstream: 1, CanceledUnreported: 1}
	assignTaskUsage(&grok)
	if grok.TaskTokens == nil || *grok.TaskTokens != 117 || grok.UsageSource != "grok_cli_reconciled" || grok.MeterError == "" {
		t.Fatalf("grok=%+v", grok)
	}
	devin := RunRecord{Agent: "devin", AgentModel: "gpt-5-6-terra-medium", Mode: "on", Requests: 2, MeterError: "Connect usage absent", JevInput: 5, JevOutput: 2, HostTranscript: &HostTranscriptUsage{PromptTokens: 100, CompletionTokens: 10, CachedTokens: 40, Steps: 2, ModelNames: []string{"gpt-5-6-terra-medium"}}, OnlyUsageMissing: true, UnreportedUpstream: 2}
	assignTaskUsage(&devin)
	if devin.TaskTokens == nil || *devin.TaskTokens != 117 || devin.UsageSource != "devin_atif_steps" || devin.MeterError == "" {
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
