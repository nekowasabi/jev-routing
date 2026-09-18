package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/nekowasabi/jev-routing/internal/compact"
)

const DefaultURL = "https://api.typesafe.ai/v1/systemone"

type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	Model   string

	// Verdicts for tool calls already judged, keyed by question id ("call_<id>",
	// "result_<id>", "summary_<id>"). The host resends the whole transcript on every
	// stateless Messages API call, so without this every proxied request re-asks Jev
	// about every old tool pair (O(n^2) requests as the conversation grows).
	// ponytail: unbounded in-process map, no TTL — a verdict never changes and the
	// process dies with the conversation; add an LRU only if memory ever shows up.
	decidedMu sync.Mutex
	decided   map[string]float64
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
}

func (c *Client) Ask(state any, questions map[string]Question) (*Response, error) {
	if !c.Live() {
		return nil, fmt.Errorf("no TYPESAFE_API_KEY")
	}
	body, err := json.Marshal(map[string]any{
		"model":     c.Model,
		"state":     state,
		"questions": questions,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("jev %s: %s", res.Status, clip(string(raw), 400))
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func NoulOf(r *Response, id string) float64 {
	if r == nil {
		return 0
	}
	raw, ok := r.Answers[id]
	if !ok {
		return 0
	}
	var n Noul
	if json.Unmarshal(raw, &n) == nil && n.Type == "noul" {
		return n.Noul
	}
	return 0
}

func ChoiceOf(r *Response, id string) string {
	if r == nil {
		return ""
	}
	raw, ok := r.Answers[id]
	if !ok {
		return ""
	}
	var c Choice
	if json.Unmarshal(raw, &c) == nil {
		return c.Choice
	}
	return ""
}

func AskCompact(c *Client, items []compact.Item, o compact.Options) (compact.Result, error) {
	o = compact.Resolve(o)
	if !c.Live() {
		return compact.CompactLocal(items, o), nil
	}
	var unPinnedCandidates []compact.Candidate
	for _, cand := range compact.CollectCandidates(items, o.PreserveRecent) {
		if !cand.Pinned {
			unPinnedCandidates = append(unPinnedCandidates, cand)
		}
	}
	fitted := compact.FitState(items, o)

	// Reuse cached verdicts; only ask Jev about candidates it has never judged.
	answers := map[string]float64{}
	var uncachedCandidates []compact.Candidate
	c.decidedMu.Lock()
	for _, cand := range unPinnedCandidates {
		cached := true
		for k := range compact.QuestionsFor(cand) {
			v, ok := c.decided[k]
			if !ok {
				cached = false
				break
			}
			answers[k] = v
		}
		if !cached {
			uncachedCandidates = append(uncachedCandidates, cand)
		}
	}
	c.decidedMu.Unlock()

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

	// One request per batch, all in flight at once; each goroutine owns its slot.
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
			res, err := c.Ask(state, jq)
			if err != nil {
				errs[i] = err
				return
			}
			answers := make(map[string]float64, len(jq))
			for k := range jq {
				answers[k] = NoulOf(res, k)
			}
			results[i] = answers
		}(i, batch)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return compact.CompactLocal(items, o), err
		}
	}

	c.decidedMu.Lock()
	if c.decided == nil {
		c.decided = map[string]float64{}
	}
	for _, m := range results {
		for k, v := range m {
			answers[k] = v
			c.decided[k] = v
		}
	}
	c.decidedMu.Unlock()

	out := compact.Compact(items, answers, o)
	out.Stats.Requests = len(batches)
	out.Stats.StateStage = fitted.Stage
	out.Stats.StateTokens = fitted.Tokens
	return out, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
