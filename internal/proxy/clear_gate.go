package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

const (
	ClearGateOff = "off"
	ClearGateJev = "jev"

	// Event.ClearGate values.
	clearGatePending = "pending"
	clearGateClear   = "clear"
	clearGateKeep    = "keep"
	clearGateError   = "error"
	clearGateAsking  = "asking" // internal only: a request is asking Jev now

	clearGateQuestion    = "clear_gate"
	clearChoiceClear     = "clear_old_results"
	clearChoiceKeep      = "keep_all_results"
	clearGateTaskRunes   = 1500
	clearGateArgRunes    = 80
	clearGateRecentCalls = 30
	clearGateStoreMax    = 4096

	// Why: instead of the ~2.6 chars/token measured on the bench's English
	// code, adopted 1.9, the median for tool results in real Claude Code
	// sessions. Reason: the lower divisor overestimates tokens, so Jev is
	// asked slightly before the server could clear rather than after.
	clearGateCharsPerTokenX10 = 19
)

type clearGateCtxKey struct{}

type clearGateEntry struct {
	lastCtx  int
	observed bool
	decision string
	reason   string
}

// clearGateStore remembers, per conversation, the last observed context size
// and the one-time Jev decision on whether clear_tool_uses may run.
type clearGateStore struct {
	mu    sync.Mutex
	conv  map[string]*clearGateEntry
	order []string
}

func newClearGateStore() *clearGateStore {
	return &clearGateStore{conv: map[string]*clearGateEntry{}}
}

// ponytail: FIFO bound like hintStore; an evicted conversation is re-asked
// once. Key by recency if long-lived gateways ever churn past 4096.
func (s *clearGateStore) entry(key string) *clearGateEntry {
	e, ok := s.conv[key]
	if !ok {
		e = &clearGateEntry{}
		s.conv[key] = e
		s.order = append(s.order, key)
		if len(s.order) > clearGateStoreMax {
			delete(s.conv, s.order[0])
			s.order = s.order[1:]
		}
	}
	return e
}

// observe records the conversation's context size from a response's usage.
func (s *clearGateStore) observe(key string, u *NormalizedUsage) {
	if s == nil || key == "" || u == nil {
		return
	}
	n := 0
	for _, p := range []*int{u.InputTokens, u.CachedTokens, u.CacheWriteTokens} {
		if p != nil {
			n += *p
		}
	}
	s.mu.Lock()
	e := s.entry(key)
	e.lastCtx, e.observed = n, true
	s.mu.Unlock()
}

// clearGateKey identifies a conversation. Why: instead of metadata.user_id,
// adopted model + first user message text. Reason: Claude Code sends the same
// user_id for parent and subagent requests, while each conversation's first
// message is stable across its requests and differs between parent and child.
func clearGateKey(root map[string]any) string {
	msgs := asSlice(root["messages"])
	if len(msgs) == 0 {
		return ""
	}
	first, _ := msgs[0].(map[string]any)
	sum := sha256.Sum256([]byte(str(root["model"]) + "\x00" + textOf(first)))
	return hex.EncodeToString(sum[:12])
}

// clearableTokens estimates the tool-result tokens clear_tool_uses could
// remove from this request: tool_result text in messages, except results of
// the last opt.ClaudeClearKeep tool_use blocks and of tools named in
// opt.ClaudeClearExclude (names resolved via the assistant tool_use ids).
func clearableTokens(root map[string]any, opt Options) int {
	names := map[string]string{}
	var ids []string
	for _, raw := range asSlice(root["messages"]) {
		m, _ := raw.(map[string]any)
		if str(m["role"]) != "assistant" {
			continue
		}
		for _, b := range asMaps(asSlice(m["content"])) {
			if str(b["type"]) == "tool_use" {
				names[str(b["id"])] = str(b["name"])
				ids = append(ids, str(b["id"]))
			}
		}
	}
	skip := map[string]bool{}
	for _, id := range ids[max(len(ids)-opt.ClaudeClearKeep, 0):] {
		skip[id] = true
	}
	excluded := map[string]bool{}
	for _, n := range opt.ClaudeClearExclude {
		excluded[n] = true
	}
	chars := 0
	for _, raw := range asSlice(root["messages"]) {
		m, _ := raw.(map[string]any)
		if str(m["role"]) != "user" {
			continue
		}
		for _, b := range asMaps(asSlice(m["content"])) {
			id := str(b["tool_use_id"])
			if str(b["type"]) != "tool_result" || skip[id] || excluded[names[id]] {
				continue
			}
			chars += utf8.RuneCountInString(textOf(b))
		}
	}
	return chars * 10 / clearGateCharsPerTokenX10
}

// gate reports whether this Claude request may carry the clear_tool_uses edit.
// It asks Jev at most once per conversation, and only when the previous
// response's context size reached the trigger and this request carries at
// least clear_at_least clearable tool-result tokens (clearableTokens), i.e.
// when the server's edit could actually fire.
// Why: a "clear" decision is never revisited. Reason: flipping back would
// resend content the server already cleared and break the prompt cache.
func (s *clearGateStore) gate(ctx context.Context, c *jev.Client, raw []byte, opt Options) (key string, clear bool, state, reason string) {
	var root map[string]any
	if s == nil || json.Unmarshal(raw, &root) != nil {
		return "", false, clearGateError, "unparsed"
	}
	key = clearGateKey(root)
	if key == "" {
		return "", false, clearGatePending, ""
	}
	eligible := clearableTokens(root, opt)
	s.mu.Lock()
	e := s.entry(key)
	switch e.decision {
	case clearGateClear:
		s.mu.Unlock()
		return key, true, clearGateClear, ""
	case clearGateKeep, clearGateError:
		d, r := e.decision, e.reason
		s.mu.Unlock()
		return key, false, d, r
	case clearGateAsking:
		s.mu.Unlock()
		return key, false, clearGatePending, ""
	}
	if !e.observed || e.lastCtx < opt.ClaudeClearTrigger || eligible < opt.ClaudeClearAtLeast {
		s.mu.Unlock()
		return key, false, clearGatePending, ""
	}
	e.decision = clearGateAsking
	s.mu.Unlock()

	state, reason = askClearGate(ctx, c, root)
	s.mu.Lock()
	e.decision, e.reason = state, reason
	s.mu.Unlock()
	return key, state == clearGateClear, state, reason
}

// askClearGate asks one Choice question. State carries the task and the
// tool-call history only (no tool results) to stay well under 2k tokens.
func askClearGate(ctx context.Context, c *jev.Client, root map[string]any) (state, reason string) {
	msgs := asSlice(root["messages"])
	first, _ := msgs[0].(map[string]any)
	items, _ := itemsFromMessages(msgs)
	var calls []map[string]string
	for _, it := range items {
		if it.Kind == compact.KindCall {
			calls = append(calls, map[string]string{"name": it.Tool, "args": clipRunes(it.Body, clearGateArgRunes)})
		}
	}
	total := len(calls)
	if len(calls) > clearGateRecentCalls {
		calls = calls[len(calls)-clearGateRecentCalls:]
	}
	st := map[string]any{
		"task":              clipRunes(strings.TrimSpace(plan.WorkRequest(textOf(first))), clearGateTaskRunes),
		"tool_calls_total":  total,
		"recent_tool_calls": calls,
	}
	qs := map[string]jev.Question{clearGateQuestion: {
		Type: "choice",
		Instructions: "An agent is working on `task` and has made the tool calls in `recent_tool_calls` (the most recent last). " +
			"Its context is getting large, so the outputs of older tool calls could be removed from what it sees. " +
			"For the rest of this task, will the agent need to see the outputs of its earlier tool calls again?",
		Criteria: map[string]string{
			clearChoiceClear: "Older tool outputs have already been used. The agent works step by step (for example edit, run, fix loops) and acts on the latest output, so it will not need to see earlier outputs again.",
			clearChoiceKeep:  "The agent is gathering information to combine at the end (surveys, audits, comparisons, summaries or reports across many files or sources), so earlier outputs will be needed again.",
		},
	}}
	res, err := c.AskContext(ctx, st, qs)
	if err != nil {
		if !c.Live() {
			return clearGateError, "no_key"
		}
		return clearGateError, reasonCallFailed
	}
	choice, why := jev.ValidateChoice(res, clearGateQuestion, map[string]bool{clearChoiceClear: true, clearChoiceKeep: true})
	if why != "" {
		return clearGateError, why
	}
	if choice.Choice == clearChoiceClear {
		return clearGateClear, ""
	}
	return clearGateKeep, ""
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
