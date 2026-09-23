package compact

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StateContext is the `context` sent with every Jev compaction request.
// It matches fast-jev-compaction src/state.ts STATE_CONTEXT.
const StateContext = "A coding assistant conversation is being compacted to free context. `history` is the whole conversation so far, oldest first; tool outputs are replaced by a short `result` note and long texts may be abridged. Each question asks whether one tool call, or the full output of that call, still needs to stay in the history verbatim. Whatever is not kept is deleted permanently, but the assistant can always re-run a tool or re-read a file."

const (
	textHead = 400
	textTail = 150
)

// FittedState is the history shown to Jev, shrunk to fit MaxStateTokens.
type FittedState struct {
	History []map[string]any
	Tokens  int
	// Stage reports which fitting stage was enough:
	// "full", "inputs<=200", "inputs<=60", "texts abridged",
	// "old messages collapsed", "old calls compacted",
	// "old messages left out", or "too_large".
	Stage string
}

// Preview renders items as the wire rows sent to Jev as `history`.
func Preview(items []Item) []map[string]any {
	return renderRows(items, 1000, false, false)
}

func rowsTokens(rows []map[string]any) int {
	b, err := json.Marshal(rows)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(b))
}

func shownInput(it Item, limit int) string {
	src := it.Body
	if src == "" {
		src = it.Preview
	}
	return clip(src, limit)
}

func renderRows(items []Item, inputLimit int, abridgeText, collapseText bool) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i, it := range items {
		row := map[string]any{"id": it.ID, "kind": it.Kind, "chars": it.Chars}
		if it.Tool != "" {
			row["tool"] = it.Tool
		}
		switch it.Kind {
		case KindResult:
			row["result"] = fmt.Sprintf("ok, %d chars (omitted)", it.Chars)
		case KindCall:
			if input := shownInput(it, inputLimit); input != "" {
				row["preview"] = input
			}
		case KindText:
			text := it.Body
			if text == "" {
				text = it.Preview
			}
			switch {
			case collapseText && !isPinned(i, len(items), 0):
				// collapse is applied per index by FitState, not here
				row["preview"] = text
			case abridgeText && len(text) > textHead+textTail+40:
				row["preview"] = abridge(text, textHead, textTail)
			case text != "":
				row["preview"] = text
			}
		}
		out = append(out, row)
	}
	return out
}

func abridge(text string, head, tail int) string {
	if len(text) <= head+tail+40 {
		return text
	}
	omitted := len(text) - head - tail
	return text[:head] + fmt.Sprintf("\n[… %d chars omitted …]\n", omitted) + text[len(text)-tail:]
}

// FitState shrinks the history stage by stage until it fits o.MaxStateTokens.
// Port of fitState in fast-jev-compaction/src/state.ts. It only affects what
// Jev is shown; keep/drop decisions still see every item. Unlike the TS
// original it never fails: an unfittable history comes back as stage
// "too_large" with the pinned rows kept, so one request is never lost.
// Message-run folding from the TS port is not applied: this harness scores a
// flat item list, and folding unrelated neighbors would hide a call id.
func FitState(items []Item, o Options) FittedState {
	o = Resolve(o)
	fit := func(rows []map[string]any, stage string) (FittedState, bool) {
		tokens := rowsTokens(rows)
		if tokens <= o.MaxStateTokens {
			return FittedState{History: rows, Tokens: tokens, Stage: stage}, true
		}
		return FittedState{}, false
	}

	rows := renderRows(items, 1000, false, false)
	if got, ok := fit(rows, "full"); ok {
		return got
	}
	for _, limit := range []int{200, 60} {
		rows = renderRows(items, limit, false, false)
		if got, ok := fit(rows, fmt.Sprintf("inputs<=%d", limit)); ok {
			return got
		}
	}

	var order []int
	var pinnedIdx []int
	for i := range items {
		if isPinned(i, len(items), o.PreserveRecent) {
			pinnedIdx = append(pinnedIdx, i)
		} else {
			order = append(order, i)
		}
	}
	// Abridge long texts oldest-first, pinned messages last.
	rows = renderRows(items, 60, false, false)
	for _, i := range append(append([]int{}, order...), pinnedIdx...) {
		if items[i].Kind != KindText {
			continue
		}
		text := items[i].Body
		if text == "" {
			text = items[i].Preview
		}
		if len(text) <= textHead+textTail+40 {
			continue
		}
		rows[i]["preview"] = abridge(text, textHead, textTail)
		if got, ok := fit(rows, "texts abridged"); ok {
			return got
		}
	}

	for _, i := range order {
		if items[i].Kind != KindText || rows[i]["preview"] == nil {
			continue
		}
		text := items[i].Body
		if text == "" {
			text = items[i].Preview
		}
		if text == "" {
			continue
		}
		rows[i]["preview"] = fmt.Sprintf("[… %d chars omitted …]", len(text))
		if got, ok := fit(rows, "old messages collapsed"); ok {
			return got
		}
	}

	for _, i := range order {
		if items[i].Kind != KindCall && items[i].Kind != KindResult {
			continue
		}
		if items[i].Kind == KindCall {
			input := shownInput(items[i], 60)
			input = strings.Join(strings.Fields(input), " ")
			rows[i]["preview"] = clip(fmt.Sprintf("%s %s → ok %dch", items[i].ID, items[i].Tool, items[i].Chars), 60)
			if input != "" && len(input) < 40 {
				rows[i]["preview"] = clip(items[i].ID+" "+items[i].Tool+" "+input, 80)
			}
		} else {
			delete(rows[i], "preview")
			rows[i]["result"] = fmt.Sprintf("ok %dch", items[i].Chars)
		}
		if got, ok := fit(rows, "old calls compacted"); ok {
			return got
		}
	}

	omitted := map[int]bool{}
	for _, i := range order {
		if items[i].Kind == KindCall || items[i].Kind == KindResult {
			continue
		}
		omitted[i] = true
		kept := make([]map[string]any, 0, len(rows))
		for j, row := range rows {
			if !omitted[j] {
				kept = append(kept, row)
			}
		}
		if got, ok := fit(kept, "old messages left out"); ok {
			return got
		}
	}

	kept := make([]map[string]any, 0, len(pinnedIdx))
	for _, i := range pinnedIdx {
		if i < len(rows) {
			kept = append(kept, rows[i])
		}
	}
	return FittedState{History: kept, Tokens: rowsTokens(kept), Stage: "too_large"}
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
