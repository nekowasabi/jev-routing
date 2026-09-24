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
	Task              string   `json:"task"`
	Agent             string   `json:"agent"`
	Model             string   `json:"model"`
	BaselineMode      string   `json:"baselineMode"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	TotalPairs        int      `json:"totalPairs"`
	ComparablePairs   int      `json:"comparablePairs"`
	MedianSavedTokens *float64 `json:"medianSavedTokens,omitempty"`
	MedianSavingsPct  *float64 `json:"medianSavingsPct,omitempty"`
	LowerSavingsPct   *float64 `json:"lowerSavingsPct,omitempty"`
	UpperSavingsPct   *float64 `json:"upperSavingsPct,omitempty"`
	Coverage          float64  `json:"coverage,omitempty"`
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
		out = append(out, effect)
	}
	return out
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
