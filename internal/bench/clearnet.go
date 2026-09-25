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
	seq        int64
	key        usageKey
	ctx        int // input + cache_read + cache_creation
	cacheWrite int
	output     int
	cleared    int // clearedInputTokens: tokens this request's own input omitted because of an earlier clear
	// clearedUses is clearedToolUses: how many of the oldest (non-excluded)
	// tool uses the server cleared from this request's context.
	clearedUses int
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

// toolCallSig is a tool call's identity for rework matching: same tool, same
// input, verbatim (no normalization -- a byte-different argument is a
// different call, not a repeat).
type toolCallSig struct{ name, input string }

// claudeToolCall is one tool_use block: its identity plus the tool_use id that
// links it to its tool_result.
type claudeToolCall struct {
	toolCallSig
	id string
}

// usageKey is the input side of one request's usage: input, cache read,
// cache write. Claude Code prints it on every assistant line of a message, and
// the proxy meters the same response, so it identifies which request a turn is.
type usageKey struct{ input, cached, cacheWrite int }

// claudeTurn is one assistant message: its tool calls, the Agent tool_use id
// it runs under (parent_tool_use_id; "" for the parent session), and its usage.
type claudeTurn struct {
	calls    []claudeToolCall
	parent   string
	key      usageKey
	hasUsage bool
}

// claudeTranscript is what claudeClearRework needs from a Claude Code
// stream-json log: the tool_use calls per assistant turn, and each tool_use
// id's tool_result text.
type claudeTranscript struct {
	turns   []claudeTurn
	results map[string]string
}

// parseClaudeTranscript reads a Claude Code stream-json log. Claude Code's
// verbose stream-json emits one "assistant" line per content block as it
// streams (thinking, then each tool_use, then text can each be their own
// line), all sharing the same message.id -- one turn is every line with that
// id, which is also one completed /v1/messages request. A child's lines can
// interleave with its parent's (the Agent tool starts while the parent still
// streams), so lines group by id rather than by adjacency.
// There is no request id shared with proxy-events.json to correlate against
// instead, so this order is the correlation; claudeClearRework verifies the
// turn count matches the request count before trusting it. tool_result
// blocks arrive on "user" lines; their content is a string or a list of
// blocks whose text parts are concatenated.
func parseClaudeTranscript(raw []byte) claudeTranscript {
	t := claudeTranscript{results: map[string]string{}}
	byID := map[string]int{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Type            string `json:"type"`
			ParentToolUseID string `json:"parent_tool_use_id"`
			Message         struct {
				ID    string `json:"id"`
				Usage *struct {
					Input      *int `json:"input_tokens"`
					CacheRead  *int `json:"cache_read_input_tokens"`
					CacheWrite *int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					Content   json.RawMessage `json:"content"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Type {
		case "assistant":
			turn, seen := byID[event.Message.ID]
			if event.Message.ID == "" || !seen {
				turn = len(t.turns)
				t.turns = append(t.turns, claudeTurn{parent: event.ParentToolUseID})
				if event.Message.ID != "" {
					byID[event.Message.ID] = turn
				}
				if u := event.Message.Usage; u != nil && u.Input != nil && u.CacheRead != nil && u.CacheWrite != nil {
					t.turns[turn].key = usageKey{*u.Input, *u.CacheRead, *u.CacheWrite}
					t.turns[turn].hasUsage = true
				}
			}
			for _, block := range event.Message.Content {
				if block.Type == "tool_use" {
					t.turns[turn].calls = append(t.turns[turn].calls, claudeToolCall{toolCallSig{block.Name, string(block.Input)}, block.ID})
				}
			}
		case "user":
			for _, block := range event.Message.Content {
				if block.Type != "tool_result" {
					continue
				}
				var text string
				if json.Unmarshal(block.Content, &text) != nil {
					var parts []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}
					_ = json.Unmarshal(block.Content, &parts)
					var b strings.Builder
					for _, p := range parts {
						if p.Type == "text" {
							b.WriteString(p.Text)
						}
					}
					text = b.String()
				}
				t.results[block.ToolUseID] = text
			}
		}
	}
	return t
}

// claudeClearRework sums the (ctx+output) cost of every request whose turn
// re-fetches content the server actually cleared: a tool call X that exactly
// repeats (same name, verbatim input) an earlier call P, where P was among
// the cleared tool uses in that request's context and X's tool_result equals
// P's verbatim. "Cleared" is positional: the server clears the oldest tool
// uses first, never the excluded tools, so P is cleared iff its ordinal among
// non-excluded tool_use calls is < that request's clearedUses. A turn is
// counted at most once. ok is false when turns and reqs are not the same
// length (the positional correlation, see parseClaudeTranscript, is then
// unverified) or when a candidate repeat's tool_result is missing; callers
// must then report rework as missing rather than guessing 0.
//
// Why: Instead of counting any repeat older than keep after the first clear, count only repeats of calls the server actually cleared that returned identical content. Reason: re-running tests/commands after edits returns new output and is not caused by clearing; the live calibration run counted such re-runs as rework.
func claudeClearRework(t claudeTranscript, reqs []clearRequest, exclude map[string]bool) (tokens int, ok bool) {
	if len(t.turns) == 0 || len(t.turns) != len(reqs) {
		return 0, false
	}
	type prior struct {
		call    claudeToolCall
		ordinal int // -1 for excluded tools, which are never cleared
	}
	var history []prior
	next := 0
	for i, turn := range t.turns {
		hit := false
		for _, call := range turn.calls {
			for _, p := range history {
				if p.call.toolCallSig != call.toolCallSig || p.ordinal < 0 || p.ordinal >= reqs[i].clearedUses {
					continue
				}
				now, ok1 := t.results[call.id]
				before, ok2 := t.results[p.call.id]
				if !ok1 || !ok2 {
					return 0, false
				}
				if now == before {
					hit = true
				}
			}
		}
		if hit {
			tokens += reqs[i].ctx + reqs[i].output
		}
		for _, call := range turn.calls {
			ord := -1
			if !exclude[call.name] {
				ord = next
				next++
			}
			history = append(history, prior{call, ord})
		}
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
			seq:         e.Seq,
			key:         usageKey{deref(e.Usage.InputTokens), deref(e.Usage.CachedTokens), deref(e.Usage.CacheWriteTokens)},
			ctx:         deref(e.Usage.InputTokens) + deref(e.Usage.CachedTokens) + deref(e.Usage.CacheWriteTokens),
			cacheWrite:  deref(e.Usage.CacheWriteTokens),
			output:      deref(e.Usage.OutputTokens),
			cleared:     e.ClearedInputTokens,
			clearedUses: e.ClearedToolUses,
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
	agentRaw, logErr := os.ReadFile(filepath.Join(runDir, "agent.log"))
	var transcript claudeTranscript
	if logErr == nil {
		transcript = parseClaudeTranscript(agentRaw)
	}
	// Why: Instead of one Seq-ordered stream, compute the parent and each
	// child's requests separately. Reason: a child has its own context, so
	// interleaving its requests breaks both computeClearSavings' counterfactual
	// growth and claudeClearRework's turn/request correlation.
	streams := [][]clearRequest{reqs}
	child := run.SubagentCalls > 0 || len(run.ChildRequestSeqs) > 0
	for _, turn := range transcript.turns {
		child = child || turn.parent != ""
	}
	if child {
		var ok bool
		if streams, ok = splitClearStreams(reqs, run.ParentRequestSeqs, run.ChildRequestSeqs); !ok {
			return // child requests not attributed: every clear field stays nil
		}
	}
	saved, extra, cleared := 0, 0, false
	for _, stream := range streams {
		s := computeClearSavings(stream)
		saved, extra, cleared = saved+s.saved, extra+s.extra, cleared || s.cleared
	}
	run.ClearSavedTokens, run.ClearExtraCacheWrite = &saved, &extra

	if !cleared {
		// A run where --claude-clear never fired is a legitimate zero, not a
		// missing measurement (see comparison.go's jev_not_applied comment
		// for the same reasoning about --claude-clear's trigger).
		zero := 0
		run.ClearReworkTokens = &zero
		setClearNet(run, saved-extra, reqs)
		return
	}

	if logErr != nil {
		return // saved/extra known; rework/net/pct stay nil (missing)
	}
	exclude := map[string]bool{}
	for _, name := range strings.Split(run.ClaudeClearExclude, ",") {
		exclude[strings.TrimSpace(name)] = true
	}
	rework := 0
	if !child {
		r, ok := claudeClearRework(transcript, reqs, exclude)
		if !ok {
			return
		}
		rework = r
	}
	for i := 0; child && i < len(streams); i++ {
		sub := claudeTranscript{results: transcript.results}
		for _, turn := range transcript.turns {
			if (turn.parent == "") == (i == 0) {
				sub.turns = append(sub.turns, turn)
			}
		}
		aligned, ok := alignClaudeTurns(sub.turns, streams[i], i > 0)
		if !ok {
			return
		}
		r, ok := claudeClearRework(sub, aligned, exclude)
		if !ok {
			return
		}
		rework += r
	}
	run.ClearReworkTokens = &rework
	setClearNet(run, saved-extra-rework, reqs)
}

// splitClearStreams returns [parent, child] requests by the run's verified
// attribution; ok is false unless every request is in exactly one of them.
func splitClearStreams(reqs []clearRequest, parentSeqs, childSeqs []int64) ([][]clearRequest, bool) {
	side := map[int64]int{}
	for _, seq := range parentSeqs {
		side[seq] = 1
	}
	for _, seq := range childSeqs {
		if side[seq] != 0 {
			return nil, false
		}
		side[seq] = 2
	}
	streams := make([][]clearRequest, 2)
	for _, r := range reqs {
		if side[r.seq] == 0 {
			return nil, false
		}
		streams[side[r.seq]-1] = append(streams[side[r.seq]-1], r)
	}
	return streams, len(streams[0]) > 0 && len(streams[1]) > 0
}

// alignClaudeTurns returns, per turn, the one request whose usageKey equals
// the turn's, so claudeClearRework can correlate them without relying on
// order. ok is false if a turn lacks usage or matches zero or several
// requests, or a request is left unmatched.
//
// Why: Instead of rejecting every child stream with an unlogged request,
// allow the child's last request (allowFinal). Reason: Claude Code does not
// print a child's final report message -- it returns as the Agent
// tool_result -- and a last request made no tool call (one would have been
// followed by another request), so it cannot hold rework.
func alignClaudeTurns(turns []claudeTurn, reqs []clearRequest, allowFinal bool) ([]clearRequest, bool) {
	used := make([]bool, len(reqs))
	var out []clearRequest
	for _, turn := range turns {
		if !turn.hasUsage {
			return nil, false
		}
		match := -1
		for i, r := range reqs {
			if r.key != turn.key {
				continue
			}
			if match >= 0 {
				return nil, false
			}
			match = i
		}
		if match < 0 || used[match] {
			return nil, false
		}
		used[match] = true
		out = append(out, reqs[match])
	}
	for i := range reqs {
		if !used[i] && !(allowFinal && i == len(reqs)-1) {
			return nil, false
		}
	}
	return out, true
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
