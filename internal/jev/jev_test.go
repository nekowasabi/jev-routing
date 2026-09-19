package jev

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"
)

func transcript(n int) []compact.Item {
	items := []compact.Item{{ID: "u", Kind: compact.KindText, Chars: 20, Preview: "do the thing"}}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%d", i)
		items = append(items,
			compact.Item{ID: id + "c", Kind: compact.KindCall, PairID: id, Tool: "Read", Chars: 40, Preview: "Read file"},
			compact.Item{ID: id + "r", Kind: compact.KindResult, PairID: id, Tool: "Read", Chars: 3000, Preview: "body", Body: "body"},
		)
	}
	return items
}

// Jev that answers every question it is asked with 1.0 and counts requests.
func fakeJev(t *testing.T, calls *int64) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(calls, 1)
		var in struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("bad request: %v", err)
		}
		answers := map[string]any{}
		for k := range in.Questions {
			answers[k] = map[string]any{"type": "noul", "noul": 1.0, "confidence": 1.0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "answers": answers})
	}))
	t.Cleanup(srv.Close)
	return &Client{APIKey: "test", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}
}

func TestAskCompactBatchesRequests(t *testing.T) {
	var calls int64
	c := fakeJev(t, &calls)
	items := transcript(12)
	res, err := AskCompact(c, items, compact.Options{PreserveRecent: 2})
	if err != nil {
		t.Fatal(err)
	}
	cands := compact.CollectCandidates(items, 2)
	if calls == 0 || calls >= int64(len(cands)) {
		t.Fatalf("made %d requests for %d candidates; want fewer", calls, len(cands))
	}
	if res.Stats.Requests != int(calls) {
		t.Fatalf("Requests = %d, actual = %d", res.Stats.Requests, calls)
	}
	if res.Stats.StateStage != "full" || res.Stats.StateTokens <= 0 {
		t.Fatalf("state stats = %+v", res.Stats)
	}
}

func TestAskCompactSplitsWhenRequestBudgetIsSmall(t *testing.T) {
	var calls int64
	c := fakeJev(t, &calls)
	items := transcript(8)
	res, err := AskCompact(c, items, compact.Options{PreserveRecent: 2, MaxRequestTokens: 200})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Requests < 2 {
		t.Fatalf("want several batches, got %d", res.Stats.Requests)
	}
	if int64(res.Stats.Requests) != calls {
		t.Fatalf("Requests = %d, actual = %d", res.Stats.Requests, calls)
	}
}

func TestAskCompactReusesVerdictsAcrossCalls(t *testing.T) {
	var calls int64
	c := fakeJev(t, &calls)
	items := transcript(6)
	if _, err := AskCompact(c, items, compact.Options{PreserveRecent: 2}); err != nil {
		t.Fatal(err)
	}
	first := atomic.LoadInt64(&calls)
	if first == 0 {
		t.Fatal("first call made no requests")
	}
	res, err := AskCompact(c, items, compact.Options{PreserveRecent: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&calls); got != first {
		t.Fatalf("second call made %d extra requests; want 0", got-first)
	}
	if res.Stats.Requests != 0 {
		t.Fatalf("Requests = %d, want 0", res.Stats.Requests)
	}
}

func TestAskCompactFallsBackLocallyOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{APIKey: "test", BaseURL: srv.URL, HTTP: srv.Client()}
	items := transcript(4)
	res, err := AskCompact(c, items, compact.Options{PreserveRecent: 2})
	if err == nil || !strings.Contains(err.Error(), "jev") {
		t.Fatalf("want jev error, got %v", err)
	}
	if res.Stats.Dropped != 0 {
		t.Fatalf("uncertain compact must keep history, dropped=%d stats=%+v", res.Stats.Dropped, res.Stats)
	}
}

func TestAskFitsOversizedState(t *testing.T) {
	var posted []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "fake",
			"answers": map[string]any{"next_tool": map[string]any{"type": "choice", "choice": "grep", "confidence": 0.9}},
		})
	}))
	defer srv.Close()
	c := &Client{APIKey: "test", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}
	huge := strings.Repeat("。", 40_000)
	state := map[string]any{
		"user_request": "find the test",
		"actions_taken": []any{map[string]any{"Tool": "grep", "Result": huge, "Input": `{"q":"x"}`}},
	}
	qs := map[string]Question{
		"next_tool": {Type: "choice", Instructions: "pick", Criteria: map[string]string{"grep": "search"}},
	}
	raw := JointTokens(state, qs)
	if raw <= InputBudget {
		t.Fatalf("fixture too small: %d", raw)
	}
	if _, err := c.Ask(state, qs); err != nil {
		t.Fatal(err)
	}
	if len(posted) == 0 {
		t.Fatal("no POST")
	}
	var in struct {
		State     json.RawMessage            `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(posted, &in); err != nil {
		t.Fatal(err)
	}
	longest := 0
	for _, q := range in.Questions {
		if n := compact.EstimateTokens(string(q)); n > longest {
			longest = n
		}
	}
	joint := compact.EstimateTokens(string(in.State)) + longest
	if joint > InputBudget {
		t.Fatalf("posted joint %d > %d", joint, InputBudget)
	}
}
