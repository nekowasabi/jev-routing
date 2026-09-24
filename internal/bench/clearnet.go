package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// clearRequest is one LLM inference request on a Claude "on" run's own path,
// in the fields the same-path net-reduction formula needs.
type clearRequest struct {
	ctx        int // input + cache_read + cache_creation
	cacheWrite int
	output     int
	cleared    int // clearedInputTokens: tokens this request's own input omitted because of an earlier clear
}

// clearSavings is the saved/extra part of the net formula: the part
// computable from proxy-events.json alone, without needing agent.log.
type clearSavings struct {
	saved, extra int
	cleared      bool // true once any request in the run actually cleared tokens
}

// computeClearSavings implements: saved = sum(cl_i); extra = sum over i with
// cl_i>0 of max(0, cw_i - expected_i), where expected_i is the cache write
// implied by the counterfactual context growing from request i-1 to i (the
// first request's expectation is its own cache write, i.e. extra=0 for it).
func computeClearSavings(reqs []clearRequest) clearSavings {
	var s clearSavings
	prevCf := 0
	for i, r := range reqs {
		cf := r.ctx + r.cleared
		expected := r.cacheWrite
		if i > 0 {
			expected = cf - prevCf
			if expected < 0 {
				expected = 0
			}
		}
		if r.cleared > 0 {
			s.cleared = true
			if extra := r.cacheWrite - expected; extra > 0 {
				s.extra += extra
			}
		}
		s.saved += r.cleared
		prevCf = cf
	}
	return s
}

func firstClearedIndex(reqs []clearRequest) int {
	for i, r := range reqs {
		if r.cleared > 0 {
			return i
		}
	}
	return len(reqs)
}

// toolCallSig is a tool call's identity for rework matching: same tool, same
// input, verbatim (no normalization -- a byte-different argument is a
// different call, not a repeat).
type toolCallSig struct{ name, input string }

// claudeAssistantTurns returns, in transcript order, the tool_use calls made
// in each assistant turn of a Claude Code stream-json log. Claude Code's
// verbose stream-json emits one "assistant" line per content block as it
// streams (thinking, then each tool_use, then text can each be their own
// line), all sharing the same message.id -- one turn is a run of consecutive
// lines with the same id, which is also one completed /v1/messages request.
// There is no request id shared with proxy-events.json to correlate against
// instead, so this order is the correlation; claudeClearRework verifies the
// turn count matches the request count before trusting it.
func claudeAssistantTurns(raw []byte) [][]toolCallSig {
	var turns [][]toolCallSig
	lastID := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Message struct {
				ID      string `json:"id"`
				Content []struct {
					Type  string          `json:"type"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Type != "assistant" {
			continue
		}
		if event.Message.ID == "" || event.Message.ID != lastID {
			turns = append(turns, nil)
			lastID = event.Message.ID
		}
		for _, block := range event.Message.Content {
			if block.Type == "tool_use" {
				turns[len(turns)-1] = append(turns[len(turns)-1], toolCallSig{name: block.Name, input: string(block.Input)})
			}
		}
	}
	return turns
}

// claudeClearRework sums the (ctx+output) cost of every request after
// firstClear whose turn contains a tool call that exactly repeats an earlier
// call now older than `keep` (i.e. the agent is re-fetching something the
// clear dropped). A turn is counted at most once even if it repeats several
// old calls. ok is false when turns and reqs are not the same length: the
// positional correlation (see claudeAssistantTurns) is then unverified, and
// callers must report rework as missing rather than guessing 0.
func claudeClearRework(turns [][]toolCallSig, reqs []clearRequest, keep, firstClear int) (tokens int, ok bool) {
	if len(turns) == 0 || len(turns) != len(reqs) {
		return 0, false
	}
	var history []toolCallSig
	for i, calls := range turns {
		if i > firstClear {
		matched:
			for _, call := range calls {
				for p, prior := range history {
					if prior == call && len(history)-p > keep {
						tokens += reqs[i].ctx + reqs[i].output
						break matched
					}
				}
			}
		}
		history = append(history, calls...)
	}
	return tokens, true
}

// clearRequestsFromSnapshot rebuilds, in Seq order, the per-request fields
// the net formula needs from a run's saved dashboard/events JSON (the same
// dashEvent shape gateway.go's meter() reads live).
func clearRequestsFromSnapshot(raw []byte) ([]clearRequest, error) {
	var body struct {
		Events []dashEvent `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	events := append([]dashEvent(nil), body.Events...)
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	var reqs []clearRequest
	for _, e := range events {
		if e.Reason == "not_llm_path" || e.Usage == nil || e.UsagePartial || e.Usage.InputTokens == nil || e.Usage.OutputTokens == nil {
			continue
		}
		reqs = append(reqs, clearRequest{
			ctx:        deref(e.Usage.InputTokens) + deref(e.Usage.CachedTokens) + deref(e.Usage.CacheWriteTokens),
			cacheWrite: deref(e.Usage.CacheWriteTokens),
			output:     deref(e.Usage.OutputTokens),
			cleared:    e.ClearedInputTokens,
		})
	}
	return reqs, nil
}

// applyClearNet fills run's ClearNet* fields by replaying its own
// proxy-events.json and agent.log under dir/<task>.<mode>.<rep>/. Only
// Claude "on" runs with --claude-clear are in scope; every other run is left
// untouched (nil fields, not zero -- this metric simply was not measured).
func applyClearNet(dir string, run *RunRecord) {
	if run.Mode != "on" || run.Agent != "claude" || !run.ClaudeClear {
		return
	}
	runDir := filepath.Join(dir, fmt.Sprintf("%s.%s.%d", run.Task, run.Mode, run.Rep))
	snapshot, err := os.ReadFile(filepath.Join(runDir, "proxy-events.json"))
	if err != nil {
		return
	}
	reqs, err := clearRequestsFromSnapshot(snapshot)
	if err != nil || len(reqs) == 0 {
		return
	}
	savings := computeClearSavings(reqs)
	saved, extra := savings.saved, savings.extra
	run.ClearSavedTokens, run.ClearExtraCacheWrite = &saved, &extra

	if !savings.cleared {
		// A run where --claude-clear never fired is a legitimate zero, not a
		// missing measurement (see comparison.go's jev_not_applied comment
		// for the same reasoning about --claude-clear's trigger).
		zero := 0
		run.ClearReworkTokens = &zero
		setClearNet(run, saved-extra, reqs)
		return
	}

	agentRaw, err := os.ReadFile(filepath.Join(runDir, "agent.log"))
	if err != nil {
		return // saved/extra known; rework/net/pct stay nil (missing)
	}
	turns := claudeAssistantTurns(agentRaw)
	rework, ok := claudeClearRework(turns, reqs, run.ClaudeClearKeep, firstClearedIndex(reqs))
	if !ok {
		return
	}
	run.ClearReworkTokens = &rework
	setClearNet(run, saved-extra-rework, reqs)
}

func setClearNet(run *RunRecord, net int, reqs []clearRequest) {
	run.ClearNetTokens = &net
	actual := 0
	for _, r := range reqs {
		actual += r.ctx + r.output
	}
	cfTotal := actual + net
	if cfTotal <= 0 {
		return
	}
	pct := float64(net) / float64(cfTotal)
	run.ClearNetPct = &pct
}
