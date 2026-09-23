package compact

import "testing"

func longItems(n int) []Item {
	items := []Item{{ID: "u", Kind: KindText, Chars: 900, Preview: repeat("some user prompt text ", 40)}}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i%26))
		items = append(items,
			Item{ID: id + "c", Kind: KindCall, PairID: id, Tool: "Read", Chars: 60, Preview: repeat("path/to/file ", 10)},
			Item{ID: id + "r", Kind: KindResult, PairID: id, Tool: "Read", Chars: 5000, Preview: repeat("file body ", 20)},
		)
	}
	return items
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestFitStateFullWhenUnderBudget(t *testing.T) {
	f := FitState(longItems(2), Options{})
	if f.Stage != "full" {
		t.Fatalf("stage = %q, want full", f.Stage)
	}
	if f.Tokens <= 0 || len(f.History) != 5 {
		t.Fatalf("got %+v", f)
	}
}

func TestFitStateShrinksThroughStages(t *testing.T) {
	items := longItems(10)
	full := FitState(items, Options{})
	stages := map[string]bool{}
	for _, budget := range []int{full.Tokens - 1, full.Tokens / 2, full.Tokens / 8, 5} {
		f := FitState(items, Options{MaxStateTokens: budget, PreserveRecent: 2})
		stages[f.Stage] = true
		if f.Stage == "full" {
			t.Fatalf("budget %d should not fit", budget)
		}
		if f.Stage != "too_large" && f.Tokens > budget {
			t.Fatalf("stage %q kept %d tokens over budget %d", f.Stage, f.Tokens, budget)
		}
	}
	if len(stages) < 2 {
		t.Fatalf("expected several stages, got %v", stages)
	}
	if !stages["too_large"] {
		t.Fatalf("tiny budget should reach too_large, got %v", stages)
	}
}

func TestFitStateKeepsPinnedEntries(t *testing.T) {
	items := longItems(10)
	f := FitState(items, Options{MaxStateTokens: 1, PreserveRecent: 3})
	if f.Stage != "too_large" {
		t.Fatalf("stage = %q", f.Stage)
	}
	// first item + the newest 3 survive every stage.
	if len(f.History) != 4 {
		t.Fatalf("pinned rows = %d, want 4: %+v", len(f.History), f.History)
	}
	if f.History[0]["id"] != "u" {
		t.Fatalf("first pinned row lost: %+v", f.History[0])
	}
}

func TestFitStateTruncatesToolInputs(t *testing.T) {
	items := []Item{
		{ID: "u", Kind: KindText, Chars: 20, Preview: "fix the test"},
		{ID: "c", Kind: KindCall, PairID: "c", Tool: "Read", Chars: 800, Body: repeat("file_path=src/a.ts ", 40)},
		{ID: "r", Kind: KindResult, PairID: "c", Tool: "Read", Chars: 20, Body: "ok"},
	}
	full := FitState(items, Options{MaxStateTokens: 100000})
	if full.Stage != "full" {
		t.Fatalf("stage %s", full.Stage)
	}
	var preview string
	for _, row := range full.History {
		if row["id"] == "c" {
			preview, _ = row["preview"].(string)
		}
	}
	if len(preview) < 200 {
		t.Fatalf("full stage should keep a long input, got %q", preview)
	}
	shrunk := FitState(items, Options{MaxStateTokens: full.Tokens - 1, PreserveRecent: 0})
	if shrunk.Stage != "inputs<=200" && shrunk.Stage != "inputs<=60" && shrunk.Stage != "old calls compacted" {
		t.Fatalf("stage %q tokens %d budget %d", shrunk.Stage, shrunk.Tokens, full.Tokens-1)
	}
}

func TestBatchCandidatesSplitsOnBudget(t *testing.T) {
	items := longItems(12)
	cands := CollectCandidates(items, 0)
	if len(cands) < 6 {
		t.Fatalf("want candidates, got %d", len(cands))
	}
	one := BatchCandidates(cands, 0, Options{})
	if len(one) != 1 {
		t.Fatalf("default budget should hold one batch, got %d", len(one))
	}
	many := BatchCandidates(cands, 0, Options{MaxRequestTokens: 200})
	if len(many) < 2 || len(many) > len(cands) {
		t.Fatalf("want several batches, got %d", len(many))
	}
	total := 0
	for _, b := range many {
		total += len(b)
	}
	if total != len(cands) {
		t.Fatalf("batches lost candidates: %d of %d", total, len(cands))
	}
}

func TestBatchCandidatesNoRoomStillSends(t *testing.T) {
	cands := CollectCandidates(longItems(3), 0)
	batches := BatchCandidates(cands, 10_000, Options{MaxRequestTokens: 30})
	if len(batches) != len(cands) {
		t.Fatalf("want one batch per candidate, got %d of %d", len(batches), len(cands))
	}
}
