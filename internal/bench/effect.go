package bench

import (
	"math"
	"sort"
)

// EffectPolicy is fixed before a repeated benchmark starts.
type EffectPolicy struct {
	MinPairs      int     `json:"minPairs"`
	MinSavingsPct float64 `json:"minSavingsPct"`
}

type EffectResult struct {
	Task            string `json:"task"`
	Agent           string `json:"agent"`
	Model           string `json:"model"`
	BaselineMode    string `json:"baselineMode"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	TotalPairs      int    `json:"totalPairs"`
	ComparablePairs int    `json:"comparablePairs"`
	// ClearedPairs is how many of TotalPairs had the intervention side
	// actually clear at least one tool use (--claude-clear can end a run
	// without ever crossing its trigger; that is a valid result, not noise).
	ClearedPairs      int      `json:"clearedPairs,omitempty"`
	MedianSavedTokens *float64 `json:"medianSavedTokens,omitempty"`
	MedianSavingsPct  *float64 `json:"medianSavingsPct,omitempty"`
	LowerSavingsPct   *float64 `json:"lowerSavingsPct,omitempty"`
	UpperSavingsPct   *float64 `json:"upperSavingsPct,omitempty"`
	Coverage          float64  `json:"coverage,omitempty"`

	// ClearNet* summarizes native context editing's same-path net reduction
	// (clearnet.go) across this group's --claude-clear "on" runs. This is a
	// different measurement from the fields above: those compare separate
	// on/off runs against each other (noisy, per the reason this metric was
	// added -- see docs/MEMO.md); this compares each on run against its own
	// counterfactual and does not need a baseline run at all.
	ClearNetRuns        int `json:"clearNetRuns,omitempty"`
	ClearNetClearedRuns int `json:"clearNetClearedRuns,omitempty"`
	// ClearNetMedianPct/Min/Max are fractions (0.145, not 14.5).
	ClearNetMedianPct     *float64 `json:"clearNetMedianPct,omitempty"`
	ClearNetMinPct        *float64 `json:"clearNetMinPct,omitempty"`
	ClearNetMaxPct        *float64 `json:"clearNetMaxPct,omitempty"`
	ClearNetAllPositive   bool     `json:"clearNetAllPositive,omitempty"`
	ClearNetReworkMissing int      `json:"clearNetReworkMissing,omitempty"`
}

func AssessEffects(file ComparisonFile, policy EffectPolicy) []EffectResult {
	if policy.MinPairs < 6 {
		policy.MinPairs = 6 // exact 95% median interval needs at least six pairs
	}
	type key struct{ task, agent, model, baseline string }
	groups := map[key][]Comparison{}
	for _, row := range file.Comparisons {
		k := key{row.Task, row.Agent, row.Model, row.BaselineMode}
		groups[k] = append(groups[k], row)
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].task != keys[j].task {
			return keys[i].task < keys[j].task
		}
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		return keys[i].baseline < keys[j].baseline
	})
	out := make([]EffectResult, 0, len(keys))
	for _, k := range keys {
		rows := groups[k]
		effect := EffectResult{Task: k.task, Agent: k.agent, Model: k.model, BaselineMode: k.baseline, Status: "hold", TotalPairs: len(rows)}
		var saved, pct []float64
		qualityLoss, incomplete := false, false
		for _, row := range rows {
			if row.BaselineSolved && !row.SelectionSolved {
				qualityLoss = true
			}
			if row.SelectionClearedToolUses != nil && *row.SelectionClearedToolUses > 0 {
				effect.ClearedPairs++
			}
			if row.Status != "comparable" || row.BaselineTokens == nil || row.SelectionTokens == nil || row.SavedTokens == nil || *row.BaselineTokens <= 0 {
				incomplete = true
				continue
			}
			effect.ComparablePairs++
			saved = append(saved, float64(*row.SavedTokens))
			pct = append(pct, 100*float64(*row.SavedTokens)/float64(*row.BaselineTokens))
		}
		switch {
		case qualityLoss:
			effect.Status, effect.Reason = "quality_worse", "selection_quality_regressed"
		case incomplete:
			effect.Status, effect.Reason = "incomparable", "incomplete_pairs"
		case len(pct) < policy.MinPairs:
			effect.Reason = "insufficient_pairs"
		default:
			effect.MedianSavedTokens = median(saved)
			effect.MedianSavingsPct = median(pct)
			effect.LowerSavingsPct, effect.UpperSavingsPct, effect.Coverage = medianInterval(pct)
			if effect.LowerSavingsPct == nil {
				effect.Reason = "interval_unavailable"
			} else if *effect.LowerSavingsPct > policy.MinSavingsPct {
				effect.Status = "decrease"
			} else if *effect.UpperSavingsPct < 0 {
				effect.Status = "increase"
			} else {
				effect.Reason = "interval_crosses_threshold"
			}
		}
		assessClearNet(&effect, rows)
		out = append(out, effect)
	}
	return out
}

// assessClearNet fills the group's same-path net-reduction summary from its
// --claude-clear "on" runs, independent of the on/off comparability status
// computed above (a run's own counterfactual doesn't need a paired baseline).
func assessClearNet(effect *EffectResult, rows []Comparison) {
	// Why: median/min/max describe the cleared-run population (a run that
	// never fired --claude-clear has a legitimate, uninteresting net=0 that
	// would just compress the range toward zero); ClearNetRuns/ClearedRuns
	// still count every --claude-clear "on" run so the summary states what
	// fraction of runs even reached the trigger.
	var firedPcts []float64
	for _, row := range rows {
		if !row.SelectionClaudeClear {
			continue
		}
		effect.ClearNetRuns++
		fired := row.SelectionClearedInputTokens != nil && *row.SelectionClearedInputTokens > 0
		if !fired {
			continue
		}
		effect.ClearNetClearedRuns++
		if row.SelectionClearNetPct != nil {
			firedPcts = append(firedPcts, *row.SelectionClearNetPct)
		} else {
			effect.ClearNetReworkMissing++
		}
	}
	if effect.ClearNetRuns == 0 {
		return
	}
	effect.ClearNetMedianPct = median(firedPcts)
	if len(firedPcts) > 0 {
		sorted := append([]float64(nil), firedPcts...)
		sort.Float64s(sorted)
		lo, hi := sorted[0], sorted[len(sorted)-1]
		effect.ClearNetMinPct, effect.ClearNetMaxPct = &lo, &hi
	}
	effect.ClearNetAllPositive = effect.ClearNetReworkMissing == 0
	for _, v := range firedPcts {
		if v <= 0 {
			effect.ClearNetAllPositive = false
		}
	}
}

// medianInterval uses an exact order-statistic interval with at least 95% coverage.
func medianInterval(values []float64) (lower, upper *float64, coverage float64) {
	n := len(values)
	if n < 1 {
		return nil, nil, 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	prob, tail, best := math.Exp2(-float64(n)), 0.0, 0
	for k := 1; k <= n/2; k++ {
		tail += prob
		c := 1 - 2*tail
		if c >= 0.95 {
			best, coverage = k, c
		}
		prob *= float64(n-k+1) / float64(k)
	}
	if best == 0 {
		return nil, nil, 0
	}
	lo, hi := sorted[best-1], sorted[n-best]
	return &lo, &hi, coverage
}
