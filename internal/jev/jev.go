package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/nekowasabi/jev-routing/internal/compact"
)

const DefaultURL = "https://api.typesafe.ai/v1/systemone"

type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	Model   string
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
	cands := compact.CollectCandidates(items, o.PreserveRecent)
	if !c.Live() {
		return compact.CompactLocal(items, o), nil
	}
	answers := map[string]float64{}
	requests := 0
	for _, cand := range cands {
		if cand.Pinned {
			continue
		}
		qs := compact.QuestionsFor(cand)
		jq := map[string]Question{}
		for k, v := range qs {
			jq[k] = Question{Type: v.Type, Instructions: v.Instructions}
		}
		state := map[string]any{
			"context": "coding-agent transcript; tool bodies omitted",
			"goal":    o.Goal,
			"history": preview(items),
		}
		res, err := c.Ask(state, jq)
		if err != nil {
			return compact.CompactLocal(items, o), err
		}
		requests++
		for k := range jq {
			answers[k] = NoulOf(res, k)
		}
	}
	out := compact.Compact(items, answers, o)
	out.Stats.Requests = requests
	out.Stats.StateStage = "live"
	return out, nil
}

func preview(items []compact.Item) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		row := map[string]any{"id": it.ID, "kind": it.Kind, "chars": it.Chars}
		if it.Tool != "" {
			row["tool"] = it.Tool
		}
		if it.Kind == compact.KindResult {
			row["result"] = fmt.Sprintf("ok, %d chars (omitted)", it.Chars)
		} else if it.Preview != "" {
			row["preview"] = clip(it.Preview, 200)
		}
		out = append(out, row)
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
