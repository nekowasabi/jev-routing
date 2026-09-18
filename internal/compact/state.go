package compact

import (
	"encoding/json"
	"fmt"
)

// FittedState is the history shown to Jev, shrunk to fit MaxStateTokens.
type FittedState struct {
	History []map[string]any
	Tokens  int
	Stage   string // "full" | "old messages collapsed" | "old calls compacted" | "old entries left out" | "too_large"
}

// Preview renders items as the wire rows sent to Jev as `history`.
func Preview(items []Item) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		row := map[string]any{"id": it.ID, "kind": it.Kind, "chars": it.Chars}
		if it.Tool != "" {
			row["tool"] = it.Tool
		}
		if it.Kind == KindResult {
			row["result"] = fmt.Sprintf("ok, %d chars (omitted)", it.Chars)
		} else if it.Preview != "" {
			row["preview"] = clip(it.Preview, previewChars)
		}
		out = append(out, row)
	}
	return out
}

func rowsTokens(rows []map[string]any) int {
	b, err := json.Marshal(rows)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(b))
}

// FitState shrinks the history stage by stage until it fits o.MaxStateTokens.
// Port of fitState in fast-jev-compaction/src/state.ts, adapted to the flat
// item model. It only affects what Jev is shown; keep/drop decisions elsewhere
// still see every item. Unlike the TS original it never fails: an unfittable
// history comes back as stage "too_large" so one request is never lost.
func FitState(items []Item, o Options) FittedState {
	o = Resolve(o)
	rows := Preview(items)
	tokens := rowsTokens(rows)
	if tokens <= o.MaxStateTokens {
		return FittedState{History: rows, Tokens: tokens, Stage: "full"}
	}

	// Oldest-first indices of entries that are not pinned.
	var reductionOrder []int
	for i := range items {
		if !isPinned(i, len(items), o.PreserveRecent) {
			reductionOrder = append(reductionOrder, i)
		}
	}

	for _, i := range reductionOrder {
		if items[i].Kind != KindText || rows[i]["preview"] == nil {
			continue
		}
		rows[i]["preview"] = fmt.Sprintf("[omitted %d chars]", items[i].Chars)
		if tokens = rowsTokens(rows); tokens <= o.MaxStateTokens {
			return FittedState{History: rows, Tokens: tokens, Stage: "old messages collapsed"}
		}
	}

	for _, i := range reductionOrder {
		if items[i].Kind != KindCall && items[i].Kind != KindResult {
			continue
		}
		delete(rows[i], "preview")
		delete(rows[i], "result")
		if tokens = rowsTokens(rows); tokens <= o.MaxStateTokens {
			return FittedState{History: rows, Tokens: tokens, Stage: "old calls compacted"}
		}
	}

	omitted := map[int]bool{}
	for _, i := range reductionOrder {
		omitted[i] = true
		kept := make([]map[string]any, 0, len(rows))
		for j, row := range rows {
			if !omitted[j] {
				kept = append(kept, row)
			}
		}
		if tokens = rowsTokens(kept); tokens <= o.MaxStateTokens {
			return FittedState{History: kept, Tokens: tokens, Stage: "old entries left out"}
		}
	}

	remainingRows := make([]map[string]any, 0, len(rows))
	for j, row := range rows {
		if !omitted[j] {
			remainingRows = append(remainingRows, row)
		}
	}
	return FittedState{History: remainingRows, Tokens: rowsTokens(remainingRows), Stage: "too_large"}
}

// BatchCandidates splits candidates into batches whose questions, together with
// the state, fit one request. Port of batchCalls; where TS throws because the
// state leaves no room, this sends the candidate alone instead.
func BatchCandidates(cands []Candidate, stateTokens int, o Options) [][]Candidate {
	o = Resolve(o)
	budget := o.MaxRequestTokens - stateTokens - requestOverheadTokens
	var batches [][]Candidate
	var current []Candidate
	currentTokens := 0
	for _, c := range cands {
		tokens := 0
		if b, err := json.Marshal(QuestionsFor(c)); err == nil {
			tokens = EstimateTokens(string(b))
		}
		if len(current) > 0 && currentTokens+tokens > budget {
			batches = append(batches, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, c)
		currentTokens += tokens
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}
