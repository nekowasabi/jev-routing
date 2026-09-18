package compact

import (
	"testing"
)

func pair(id, tool, result string, chars int) []Item {
	return []Item{
		{ID: id, Kind: KindCall, PairID: id, Tool: tool, Chars: 40, Preview: tool + " args"},
		{ID: id + "_r", Kind: KindResult, PairID: id, Tool: tool, Chars: chars, Preview: result, Body: result},
	}
}

func TestDecideCallKeepResult(t *testing.T) {
	c := Candidate{Items: pair("1", "Read", "file body", 400)}
	d := DecideCall(c, map[string]float64{"result_1_r": 0.9, "call_1": 0.2}, 0.5)
	if d[0].Action != ActionKeep || d[1].Action != ActionKeep {
		t.Fatalf("got %+v", d)
	}
}

func TestDecideCallDropResultKeepCall(t *testing.T) {
	c := Candidate{Items: pair("1", "Read", "file body", 400)}
	d := DecideCall(c, map[string]float64{"result_1_r": 0.1, "call_1": 0.8}, 0.5)
	if d[0].Action != ActionKeep || d[1].Action != ActionTruncate {
		t.Fatalf("got %+v", d)
	}
}

func TestDecideCallDropBoth(t *testing.T) {
	c := Candidate{Items: pair("1", "Read", "file body", 400)}
	d := DecideCall(c, map[string]float64{"result_1_r": 0.1, "call_1": 0.1}, 0.5)
	if d[0].Action != ActionDrop || d[1].Action != ActionDrop {
		t.Fatalf("got %+v", d)
	}
}

func TestPinnedUntouched(t *testing.T) {
	c := Candidate{Items: pair("1", "Read", "file body", 400), Pinned: true}
	d := DecideCall(c, map[string]float64{"result_1_r": 0, "call_1": 0}, 0.5)
	if !d[0].Pinned || d[0].Action != ActionKeep {
		t.Fatalf("got %+v", d)
	}
}

func TestLocalDropsSupersededRead(t *testing.T) {
	items := []Item{
		{ID: "u", Kind: KindText, Chars: 20, Preview: "fix the test"},
		{ID: "g", Kind: KindCall, PairID: "g", Tool: "Grep", Chars: 10},
		{ID: "g_r", Kind: KindResult, PairID: "g", Tool: "Grep", Chars: 8000, Body: "hits...", Preview: "hits"},
		{ID: "r", Kind: KindCall, PairID: "r", Tool: "Read", Chars: 10},
		{ID: "r_r", Kind: KindResult, PairID: "r", Tool: "Read", Chars: 4000, Body: "file...", Preview: "file"},
		{ID: "e", Kind: KindCall, PairID: "e", Tool: "Edit", Chars: 10},
		{ID: "e_r", Kind: KindResult, PairID: "e", Tool: "Edit", Chars: 20, Body: "ok", Preview: "ok"},
	}
	res := CompactLocal(items, Options{PreserveRecent: 2, TruncateHeadChars: 300})
	if res.Stats.CharsAfter >= res.Stats.CharsBefore {
		t.Fatalf("expected reduction %+v", res.Stats)
	}
	if res.Stats.Dropped+res.Stats.Truncated == 0 {
		t.Fatalf("expected stale results to drop/truncate %+v", res)
	}
}

func TestUserTextNeverDropped(t *testing.T) {
	items := []Item{
		{ID: "u", Kind: KindText, Chars: 44, Preview: "fix the failing test. never edit generated."},
		{ID: "g", Kind: KindCall, PairID: "g", Tool: "Grep", Chars: 10},
		{ID: "g_r", Kind: KindResult, PairID: "g", Tool: "Grep", Chars: 100, Body: "x"},
	}
	res := CompactLocal(items, Options{PreserveRecent: 0})
	found := false
	for _, it := range res.Items {
		if it.ID == "u" {
			found = true
			if it.Preview != items[0].Preview {
				t.Fatalf("user text mutated: %q", it.Preview)
			}
		}
	}
	if !found {
		t.Fatal("user text disappeared")
	}
}

func TestEstimateTokensPositive(t *testing.T) {
	if EstimateTokens("hello world 123") < 2 {
		t.Fatal("too small")
	}
}
