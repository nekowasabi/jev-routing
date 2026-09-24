package bench

// assignTaskUsage chooses a complete task-level source without fabricating
// usage for an individual canceled or usage-less upstream response.
func assignTaskUsage(run *RunRecord) {
	if run == nil || run.Mode == "direct" {
		return
	}
	if run.MeterError == "" && measured(*run) && run.Requests > 0 {
		total := totalInput(*run) + run.Output + run.JevInput + run.JevOutput
		run.TaskTokens, run.UsageSource = &total, "proxy"
		return
	}
	if run.Agent == "grok" && run.HostUsageVerified && run.OnlyUsageMissing && run.CanceledUnreported > 0 &&
		run.CanceledUnreported == run.UnreportedUpstream && run.Metered+run.UnreportedUpstream == run.Requests {
		// The complete CLI prompt ledger reconciles every completed main-model call;
		// proxy usage separately includes side-model calls and Jev.
		total := totalInput(*run) + run.Output + run.JevInput + run.JevOutput
		run.TaskTokens, run.UsageSource = &total, "grok_cli_reconciled"
		return
	}
	if run.Agent == "devin" && run.HostTranscript != nil && run.HostTranscriptError == "" && run.OnlyUsageMissing &&
		run.UnreportedUpstream == run.Requests && run.Metered == 0 && run.SubagentCalls == 0 &&
		len(run.HostTranscript.ModelNames) == 1 && run.HostTranscript.ModelNames[0] == run.AgentModel {
		// ATIF-v1 agent-step totals equal final_metrics; prompt tokens include cache.
		total := run.HostTranscript.PromptTokens + run.HostTranscript.CompletionTokens + run.JevInput + run.JevOutput
		run.TaskTokens, run.UsageSource = &total, "devin_atif_steps"
		run.HostUsageVerified = true
	}
}

func taskTokenTotal(run RunRecord) (int, bool) {
	if run.TaskTokens != nil && run.UsageSource != "" {
		return *run.TaskTokens, true
	}
	// Older local records remain recomputable when all proxy usage was complete.
	if run.UsageSource == "" && run.MeterError == "" && measured(run) && run.Requests > 0 {
		return totalInput(run) + run.Output + run.JevInput + run.JevOutput, true
	}
	return 0, false
}
