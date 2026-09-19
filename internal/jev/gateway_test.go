package jev

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"
)

func TestGatewayInputBudget(t *testing.T) {
	orig := map[string]any{
		"user_request": "keep this",
		"goal":         "keep goal",
		"actions_taken": []any{
			map[string]any{"Tool": "grep", "Input": `{"q":"x"}`, "Result": strings.Repeat("結果本文\n", 40_000)},
		},
	}
	qs := map[string]Question{
		"next_tool": {Type: "choice", Instructions: "must keep instructions", Criteria: map[string]string{"grep": "search files"}},
	}
	if JointTokens(orig, qs) <= InputBudget {
		t.Fatalf("fixture too small")
	}
	copyBefore, _ := json.Marshal(orig)
	fitted, outQ, err := FitInputCopy(orig, qs)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(orig)
	if string(copyBefore) != string(after) {
		t.Fatal("FitInput mutated original")
	}
	if JointTokens(fitted, outQ) > InputBudget {
		t.Fatalf("still over budget")
	}
	m := fitted.(map[string]any)
	if m["user_request"] != "keep this" || m["goal"] != "keep goal" {
		t.Fatalf("protected fields changed: %+v", m)
	}
	if outQ["next_tool"].Instructions != "must keep instructions" {
		t.Fatal("instructions truncated")
	}
	if _, ok := outQ["next_tool"].Criteria["grep"]; !ok {
		t.Fatal("criteria key lost")
	}

	protected := map[string]any{"user_request": strings.Repeat("制約", 80_000)}
	hugeQ := map[string]Question{"q": {Type: "choice", Instructions: strings.Repeat("rule ", 20_000), Criteria: map[string]string{"a": "b"}}}
	_, _, err = FitInputCopy(protected, hugeQ)
	if err != ErrOverBudget {
		t.Fatalf("want over-budget, got %v", err)
	}

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	defer srv.Close()
	c := &Client{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Ask(protected, hugeQ); err == nil {
		t.Fatal("Ask must not send over-budget protected input")
	}
	if atomic.LoadInt64(&calls) != 0 {
		t.Fatal("sent request over budget")
	}
}

func TestGatewayVerdictCache(t *testing.T) {
	var n int64
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		var in struct {
			Questions map[string]json.RawMessage `json:"questions"`
			State     map[string]any             `json:"state"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		answers := map[string]any{}
		for k := range in.Questions {
			answers[k] = map[string]any{"type": "noul", "noul": 0.9, "confidence": 1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "x", "answers": answers})
	}))
	defer okSrv.Close()
	c := &Client{APIKey: "k", BaseURL: okSrv.URL, Model: "m", HTTP: okSrv.Client()}
	items := []compact.Item{
		{ID: "u", Kind: compact.KindText, Body: "goal a", Chars: 6},
		{ID: "c1", Kind: compact.KindCall, PairID: "c1", Tool: "Read", Body: "args1", Chars: 5},
		{ID: "c1_r", Kind: compact.KindResult, PairID: "c1", Tool: "Read", Body: "BODY1", Chars: 5},
		{ID: "t2", Kind: compact.KindText, Body: "later", Chars: 5},
		{ID: "t3", Kind: compact.KindText, Body: "tail", Chars: 4},
	}
	o := compact.Options{Goal: "goal-a", PreserveRecent: 1}
	if _, err := AskCompact(c, items, o); err != nil {
		t.Fatal(err)
	}
	first := atomic.LoadInt64(&n)
	if first == 0 {
		t.Fatal("no calls")
	}
	if _, err := AskCompact(c, items, o); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&n) != first {
		t.Fatalf("same input re-asked: %d -> %d", first, n)
	}
	o2 := o
	o2.Goal = "goal-b"
	if _, err := AskCompact(c, items, o2); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&n) == first {
		t.Fatal("goal change must invalidate cache")
	}
	items2 := append([]compact.Item{}, items...)
	items2[2].Body = "BODY2"
	before := atomic.LoadInt64(&n)
	if _, err := AskCompact(c, items2, o); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&n) == before {
		t.Fatal("result body change must invalidate cache")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{"call_c1": map[string]any{"type": "nope"}},
		})
	}))
	defer bad.Close()
	cBad := &Client{APIKey: "k", BaseURL: bad.URL, HTTP: bad.Client()}
	start := atomic.LoadInt64(&n)
	_, _ = AskCompact(cBad, items, o)
	_, _ = AskCompact(cBad, items, o)
	if atomic.LoadInt64(&n) <= start+1 {
		t.Fatal("invalid answers must not be cached")
	}

	var wg sync.WaitGroup
	c3 := &Client{APIKey: "k", BaseURL: okSrv.URL, Model: "m2", HTTP: okSrv.Client()}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = AskCompact(c3, items, compact.Options{Goal: "parallel", PreserveRecent: 0})
		}()
	}
	wg.Wait()
}

func TestGatewayDecisionParse(t *testing.T) {
	res := &Response{Answers: map[string]json.RawMessage{
		"needs_tool": json.RawMessage(`{"type":"noul","noul":0,"confidence":1}`),
		"next_tool":  json.RawMessage(`{"type":"choice","choice":"grep","confidence":0.9}`),
	}}
	n, ok := ParseNoul(res, "needs_tool")
	if !ok || n.Noul != 0 {
		t.Fatalf("zero must be valid, got %+v ok=%v", n, ok)
	}
	if _, ok := ParseNoul(res, "missing"); ok {
		t.Fatal("missing should be invalid")
	}
	bad := &Response{Answers: map[string]json.RawMessage{"needs_tool": json.RawMessage(`{"type":"noul","noul":2,"confidence":1}`)}}
	if _, ok := ParseNoul(bad, "needs_tool"); ok {
		t.Fatal("out of range")
	}
}
