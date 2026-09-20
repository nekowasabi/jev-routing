package jev

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nekowasabi/jev-routing/internal/compact"
)

const (
	DefaultURL = "https://api.typesafe.ai/v1/systemone"
	// InputBudget is TypeSafe's cap for state plus the longest question (tokens).
	InputBudget = 32_000
)

// ErrOverBudget is returned when protected fields alone exceed InputBudget.
var ErrOverBudget = fmt.Errorf("jev input exceeds budget after shrinking shrinkable fields")

type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	Model   string

	// decided caches validated compact noul answers keyed by a fingerprint of
	// the complete evaluation input, not question IDs alone.
	decidedMu sync.Mutex
	decided   map[string]map[string]float64
	askCache  map[string]*Response
}

func FromEnv() *Client {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		key = os.Getenv("JEV_API_KEY")
	}
	url := os.Getenv("JEV_BASE_URL")
	if url == "" {
		url = DefaultURL
	}
	model := os.Getenv("JEV_MODEL")
	if model == "" {
		model = "jev-latest"
	}
	return &Client{APIKey: key, BaseURL: url, Model: model, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) Live() bool { return c != nil && c.APIKey != "" }

type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
	// Optional marks a speculative question whose answer the caller may not
	// need. A missing answer for it still leaves the response cacheable.
	Optional bool `json:"-"`
}

type Noul struct {
	Type string  `json:"type"`
	Noul float64 `json:"noul"`
	Conf float64 `json:"confidence"`
}

type Choice struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Conf          float64            `json:"confidence"`
}

type Response struct {
	Model     string                     `json:"model"`
	Answers   map[string]json.RawMessage `json:"answers"`
	LatencyMs int                        `json:"latencyMs"`
	Usage     *Usage                     `json:"usage,omitempty"`
}

// Usage is reported Jev-side usage when the service includes it. Missing is nil.
type Usage struct {
	InputTokens  *int `json:"inputTokens,omitempty"`
	OutputTokens *int `json:"outputTokens,omitempty"`
}

// Attempt records one real HTTP try. Request-scoped; never stored on Client.
type Attempt struct {
	Purpose   string
	Started   time.Time
	Duration  time.Duration
	OK        bool
	Cached    bool
	Status    int
	ErrKind   string
	Usage     *Usage
	Questions int
}

type attemptKey struct{}

// WithAttemptHook attaches a per-request callback. The shared Client stays immutable.
func WithAttemptHook(ctx context.Context, hook func(Attempt)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, attemptKey{}, hook)
}

func attemptHook(ctx context.Context) func(Attempt) {
	if ctx == nil {
		return nil
	}
	h, _ := ctx.Value(attemptKey{}).(func(Attempt))
	return h
}

func (c *Client) Ask(state any, questions map[string]Question) (*Response, error) {
	return c.AskContext(context.Background(), state, questions)
}

func (c *Client) AskContext(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	return c.doAsk(ctx, "ask", state, questions, true)
}

// AskSelectionContext labels a tool-selection request in request statistics.
func (c *Client) AskSelectionContext(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	return c.doAsk(ctx, "selection", state, questions, true)
}

func (c *Client) doAsk(ctx context.Context, purpose string, state any, questions map[string]Question, useCache bool) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !c.Live() {
		return nil, fmt.Errorf("no TYPESAFE_API_KEY")
	}
	if useCache {
		if cached, ok := c.lookup(ctx, purpose, state, questions); ok {
			return cached, nil
		}
	}
	fittedState, fittedQ, err := FitInputCopy(state, questions)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"model":     c.Model,
		"state":     fittedState,
		"questions": fittedQ,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+c.APIKey)
	started := time.Now()
	res, err := c.HTTP.Do(req)
	att := Attempt{Purpose: purpose, Started: started, Duration: time.Since(started), Questions: len(questions)}
	if err != nil {
		att.ErrKind = errKind(ctx, err)
		if h := attemptHook(ctx); h != nil {
			h(att)
		}
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	att.Status = res.StatusCode
	if res.StatusCode >= 300 {
		att.ErrKind = "http"
		if h := attemptHook(ctx); h != nil {
			h(att)
		}
		return nil, fmt.Errorf("jev %s: %s", res.Status, clip(string(raw), 400))
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		att.ErrKind = "decode"
		if h := attemptHook(ctx); h != nil {
			h(att)
		}
		return nil, err
	}
	att.OK = true
	att.Usage = out.Usage
	if h := attemptHook(ctx); h != nil {
		h(att)
	}
	if useCache {
		c.storeValid(ctx, purpose, state, questions, &out)
	}
	return &out, nil
}

func errKind(ctx context.Context, err error) string {
	if ctx != nil && ctx.Err() != nil {
		if ctx.Err() == context.Canceled {
			return "canceled"
		}
		return "deadline"
	}
	return "network"
}

func NoulOf(r *Response, id string) float64 {
	v, ok := ParseNoul(r, id)
	if !ok {
		return 0
	}
	return v.Noul
}

func ChoiceOf(r *Response, id string) string {
	v, ok := ParseChoice(r, id)
	if !ok {
		return ""
	}
	return v.Choice
}

// ParseNoul returns a validated noul answer. Missing, wrong type, non-finite, or
// out-of-range values are invalid. A present numeric zero is valid.
func ParseNoul(r *Response, id string) (Noul, bool) {
	if r == nil || r.Answers == nil {
		return Noul{}, false
	}
	raw, ok := r.Answers[id]
	if !ok || len(raw) == 0 {
		return Noul{}, false
	}
	var n Noul
	if json.Unmarshal(raw, &n) != nil || n.Type != "noul" {
		return Noul{}, false
	}
	var value struct {
		Noul *float64 `json:"noul"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Noul == nil || !finite01(n.Noul) || !finite01(n.Conf) {
		return Noul{}, false
	}
	return n, true
}

// ParseChoice returns a validated choice answer.
func ParseChoice(r *Response, id string) (Choice, bool) {
	if r == nil || r.Answers == nil {
		return Choice{}, false
	}
	raw, ok := r.Answers[id]
	if !ok || len(raw) == 0 {
		return Choice{}, false
	}
	var c Choice
	if json.Unmarshal(raw, &c) != nil || (c.Type != "" && c.Type != "choice") {
		return Choice{}, false
	}
	var value struct {
		Confidence *float64 `json:"confidence"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Confidence == nil || c.Choice == "" || !finite01(c.Conf) {
		return Choice{}, false
	}
	return c, true
}

func finite01(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func AskCompact(c *Client, items []compact.Item, o compact.Options) (compact.Result, error) {
	return AskCompactContext(context.Background(), c, items, o)
}

func AskCompactContext(ctx context.Context, c *Client, items []compact.Item, o compact.Options) (compact.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	o = compact.Resolve(o)
	if c == nil || !c.Live() {
		return compact.CompactLocal(items, o), nil
	}
	var unPinnedCandidates []compact.Candidate
	for _, cand := range compact.CollectCandidates(items, o.PreserveRecent) {
		if !cand.Pinned {
			unPinnedCandidates = append(unPinnedCandidates, cand)
		}
	}
	fitted := compact.FitState(items, o)

	answers := map[string]float64{}
	var uncachedCandidates []compact.Candidate
	for _, cand := range unPinnedCandidates {
		key := compactKey(c, o, items, cand)
		c.decidedMu.Lock()
		cached, ok := c.decided[key]
		c.decidedMu.Unlock()
		if ok && completeAnswers(cand, cached) {
			for k, v := range cached {
				answers[k] = v
			}
			if h := attemptHook(ctx); h != nil {
				h(Attempt{Purpose: "compact", Cached: true, OK: true, Questions: len(cached)})
			}
			continue
		}
		uncachedCandidates = append(uncachedCandidates, cand)
	}

	if len(uncachedCandidates) == 0 {
		out := compact.Compact(items, answers, o)
		out.Stats.Requests = 0
		out.Stats.StateStage = fitted.Stage
		out.Stats.StateTokens = fitted.Tokens
		return out, nil
	}

	state := map[string]any{
		"context": "coding-agent transcript; tool bodies omitted",
		"goal":    o.Goal,
		"history": fitted.History,
	}
	batches := compact.BatchCandidates(uncachedCandidates, fitted.Tokens, o)

	results := make([]map[string]float64, len(batches))
	errs := make([]error, len(batches))
	var wg sync.WaitGroup
	for i, batch := range batches {
		wg.Add(1)
		go func(i int, batch []compact.Candidate) {
			defer wg.Done()
			jq := map[string]Question{}
			for _, cand := range batch {
				for k, v := range compact.QuestionsFor(cand) {
					jq[k] = Question{Type: v.Type, Instructions: v.Instructions}
				}
			}
			res, err := c.doAsk(ctx, "compact", state, jq, false)
			if err != nil {
				errs[i] = err
				return
			}
			got := make(map[string]float64, len(jq))
			for k := range jq {
				if n, ok := ParseNoul(res, k); ok {
					got[k] = n.Noul
				}
			}
			results[i] = got
		}(i, batch)
	}
	wg.Wait()

	var firstErr error
	for i, err := range errs {
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for k, v := range results[i] {
			answers[k] = v
		}
		for _, cand := range batches[i] {
			key := compactKey(c, o, items, cand)
			subset := map[string]float64{}
			complete := true
			for qid := range compact.QuestionsFor(cand) {
				v, ok := results[i][qid]
				if !ok {
					complete = false
					break
				}
				subset[qid] = v
			}
			if !complete {
				continue
			}
			c.decidedMu.Lock()
			if c.decided == nil {
				c.decided = map[string]map[string]float64{}
			}
			c.decided[key] = subset
			c.decidedMu.Unlock()
		}
	}

	out := compact.Compact(items, answers, o)
	out.Stats.Requests = len(batches)
	if firstErr != nil {
		// Failed batches contribute no answers; Compact keeps those items.
		out.Stats.StateStage = fitted.Stage
		out.Stats.StateTokens = fitted.Tokens
		return out, firstErr
	}
	out.Stats.StateStage = fitted.Stage
	out.Stats.StateTokens = fitted.Tokens
	return out, nil
}

func completeAnswers(cand compact.Candidate, cached map[string]float64) bool {
	if cached == nil {
		return false
	}
	for k := range compact.QuestionsFor(cand) {
		if _, ok := cached[k]; !ok {
			return false
		}
	}
	return true
}

func compactKey(c *Client, o compact.Options, items []compact.Item, cand compact.Candidate) string {
	type row struct {
		ID, Kind, Tool, Body, PairID string
	}
	hist := make([]row, 0, len(items))
	for _, it := range items {
		hist = append(hist, row{ID: it.ID, Kind: string(it.Kind), Tool: it.Tool, Body: it.Body, PairID: it.PairID})
	}
	cands := make([]row, 0, len(cand.Items))
	for _, it := range cand.Items {
		cands = append(cands, row{ID: it.ID, Kind: string(it.Kind), Tool: it.Tool, Body: it.Body, PairID: it.PairID})
	}
	qs := compact.QuestionsFor(cand)
	payload := map[string]any{
		"purpose":    "compact",
		"goal":       o.Goal,
		"history":    hist,
		"candidate":  cands,
		"questions":  qs,
		"threshold":  o.KeepThreshold,
		"preserve":   o.PreserveRecent,
		"truncate":   o.TruncateHeadChars,
		"maxState":   o.MaxStateTokens,
		"maxRequest": o.MaxRequestTokens,
		"model":      c.Model,
		"url":        c.BaseURL,
	}
	return fingerprint(payload)
}

func (c *Client) lookup(ctx context.Context, purpose string, state any, questions map[string]Question) (*Response, bool) {
	key := askKey(c, purpose, state, questions)
	c.decidedMu.Lock()
	r, ok := c.askCache[key]
	c.decidedMu.Unlock()
	if !ok || r == nil {
		return nil, false
	}
	if h := attemptHook(ctx); h != nil {
		h(Attempt{Purpose: purpose, Cached: true, OK: true, Questions: len(questions)})
	}
	cp := *r
	return &cp, true
}

func (c *Client) storeValid(_ context.Context, purpose string, state any, questions map[string]Question, res *Response) {
	if res == nil || !answersValidForCache(res, questions) {
		return
	}
	key := askKey(c, purpose, state, questions)
	cp := *res
	c.decidedMu.Lock()
	if c.askCache == nil {
		c.askCache = map[string]*Response{}
	}
	c.askCache[key] = &cp
	c.decidedMu.Unlock()
}

func answersValidForCache(res *Response, questions map[string]Question) bool {
	if res == nil {
		return false
	}
	for id, q := range questions {
		if q.Optional && (res.Answers == nil || len(res.Answers[id]) == 0) {
			continue
		}
		switch q.Type {
		case "noul":
			if _, ok := ParseNoul(res, id); !ok {
				return false
			}
		case "choice":
			if _, ok := ParseChoice(res, id); !ok {
				return false
			}
		default:
			if res.Answers == nil || len(res.Answers[id]) == 0 {
				return false
			}
		}
	}
	return true
}

func askKey(c *Client, purpose string, state any, questions map[string]Question) string {
	return fingerprint(map[string]any{
		"purpose":   purpose,
		"state":     state,
		"questions": questions,
		"model":     c.Model,
		"url":       c.BaseURL,
	})
}

func fingerprint(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// JointTokens is state plus the longest question, matching TypeSafe's 32k input cap.
func JointTokens(state any, questions map[string]Question) int {
	st := 0
	if b, err := json.Marshal(state); err == nil {
		st = compact.EstimateTokens(string(b))
	}
	longest := 0
	for _, q := range questions {
		b, err := json.Marshal(q)
		if err != nil {
			continue
		}
		if n := compact.EstimateTokens(string(b)); n > longest {
			longest = n
		}
	}
	return st + longest
}

// FitInput shrinks a copy of state until JointTokens is within InputBudget.
// The input objects are not mutated. Protected identity fields are not cut.
func FitInput(state any, questions map[string]Question) (any, map[string]Question) {
	out, qs, _ := FitInputCopy(state, questions)
	return out, qs
}

// FitInputCopy is FitInput that reports ErrOverBudget when protected fields overflow.
func FitInputCopy(state any, questions map[string]Question) (any, map[string]Question, error) {
	qs := cloneQuestions(questions)
	copied := cloneAny(state)
	if JointTokens(copied, qs) <= InputBudget {
		return copied, qs, nil
	}
	copied = shrinkAny(copied, qs)
	if JointTokens(copied, qs) <= InputBudget {
		return copied, qs, nil
	}
	return copied, qs, ErrOverBudget
}

func cloneQuestions(in map[string]Question) map[string]Question {
	out := make(map[string]Question, len(in))
	for k, q := range in {
		if q.Criteria != nil {
			c := make(map[string]string, len(q.Criteria))
			for ck, cv := range q.Criteria {
				c[ck] = cv
			}
			q.Criteria = c
		}
		out[k] = q
	}
	return out
}

func cloneAny(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

func protectedKey(k string) bool {
	switch k {
	case "goal", "user_request", "id", "call_id", "tool_call_id", "pairId", "pair_id",
		"tool", "name", "choice", "type", "kind", "instructions", "criteria":
		return true
	default:
		return false
	}
}

func shrinkAny(state any, qs map[string]Question) any {
	// Binary-search the max suffix we can keep on shrinkable strings.
	// Walk a copy and shorten Result / result / preview / body / history text.
	if JointTokens(state, qs) <= InputBudget {
		return state
	}
	root, ok := state.(map[string]any)
	if !ok {
		return state
	}
	// Prefer shrinking actions_taken results and history bodies.
	shrinkTargets := collectShrinkStrings(root, nil, nil)
	for _, t := range shrinkTargets {
		if JointTokens(root, qs) <= InputBudget {
			return root
		}
		runes := []rune(t.get())
		if len(runes) == 0 {
			continue
		}
		lo, hi := 0, len(runes)
		best := 0
		for lo <= hi {
			mid := (lo + hi) / 2
			t.set(string(runes[:mid]))
			if JointTokens(root, qs) <= InputBudget {
				best = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		t.set(safeCut(runes, best))
	}
	return root
}

type strRef struct {
	get func() string
	set func(string)
}

func collectShrinkStrings(v any, path []string, out []strRef) []strRef {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			key := k
			switch child.(type) {
			case string:
				if !shrinkableField(key) {
					continue
				}
				out = append(out, strRef{
					get: func() string {
						cur, _ := t[key].(string)
						return cur
					},
					set: func(nv string) { t[key] = nv },
				})
			default:
				out = collectShrinkStrings(child, append(path, key), out)
			}
		}
	case []any:
		for i := range t {
			idx := i
			out = collectShrinkStrings(t[idx], path, out)
		}
	}
	return out
}

func shrinkableField(k string) bool {
	switch k {
	case "Result", "result", "preview", "body", "Body", "output", "content", "text", "history":
		return true
	default:
		return !protectedKey(k)
	}
}

func safeCut(runes []rune, n int) string {
	if n <= 0 {
		return ""
	}
	if n > len(runes) {
		n = len(runes)
	}
	s := string(runes[:n])
	if !utf8.ValidString(s) {
		return stringsToValid(s)
	}
	return s
}

func stringsToValid(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string([]rune(s))
}
