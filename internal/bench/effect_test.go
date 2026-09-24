package bench

import "testing"

func effectRows(n, baseline, selection int) ComparisonFile {
	rows := make([]Comparison, n)
	for i := range rows {
		b, s, saved := baseline, selection, baseline-selection
		rows[i] = Comparison{Task: "task", Agent: "claude", Model: "claude-sonnet-5", Rep: i + 1, BaselineMode: "off", Status: "comparable", BaselineSolved: true, SelectionSolved: true, SelectionApplied: true, BaselineTokens: &b, SelectionTokens: &s, SavedTokens: &saved}
	}
	return ComparisonFile{SchemaVersion: 1, Comparisons: rows}
}

func TestAssessEffectsRequiresRepeatedPairedEvidence(t *testing.T) {
	policy := EffectPolicy{MinPairs: 6, MinSavingsPct: 0}
	decrease := AssessEffects(effectRows(6, 100, 80), policy)[0]
	if decrease.Status != "decrease" || decrease.ComparablePairs != 6 || decrease.LowerSavingsPct == nil || *decrease.LowerSavingsPct <= 0 {
		t.Fatalf("decrease=%+v", decrease)
	}
	increase := AssessEffects(effectRows(6, 100, 120), policy)[0]
	if increase.Status != "increase" || increase.UpperSavingsPct == nil || *increase.UpperSavingsPct >= 0 {
		t.Fatalf("increase=%+v", increase)
	}
	tooFew := AssessEffects(effectRows(5, 100, 80), policy)[0]
	if tooFew.Status != "hold" || tooFew.Reason != "insufficient_pairs" {
		t.Fatalf("too few=%+v", tooFew)
	}
	threshold := AssessEffects(effectRows(6, 100, 80), EffectPolicy{MinPairs: 6, MinSavingsPct: 25})[0]
	if threshold.Status != "hold" {
		t.Fatalf("threshold=%+v", threshold)
	}
}

func TestAssessEffectsDoesNotHideQualityOrMissingUsage(t *testing.T) {
	file := effectRows(6, 100, 80)
	file.Comparisons[0].Status = "incomparable"
	file.Comparisons[0].SavedTokens = nil
	file.Comparisons[0].Reasons = []string{"usage_incomplete"}
	got := AssessEffects(file, EffectPolicy{MinPairs: 6})[0]
	if got.Status != "incomparable" || got.TotalPairs != 6 {
		t.Fatalf("missing=%+v", got)
	}
	file.Comparisons[0].BaselineSolved = true
	file.Comparisons[0].SelectionSolved = false
	got = AssessEffects(file, EffectPolicy{MinPairs: 6})[0]
	if got.Status != "quality_worse" {
		t.Fatalf("quality=%+v", got)
	}
}
