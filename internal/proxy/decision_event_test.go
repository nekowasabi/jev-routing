package proxy

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestFormatStatsFromEventIncludesDecisionFields(t *testing.T) {
	stats := RewriteStats{
		Host:          host.Grok,
		ToolBefore:    3,
		ToolAfter:     2,
		Chosen:        "grep",
		Confidence:    0.93,
		NeedsTool:     0.88,
		Source:        sourceJev,
		Engine:        "live",
		ConnectStatus: jev.StatusLive,
		RuleVersion:   adoptRuleVersion,
		Probabilities: map[string]float64{"grep": 0.7, "read_file": 0.2, plan.Respond: 0.1},
		HistoryIssues: []string{"input[1].item:local_shell_call tool=exec"},
		CharsBefore:   100,
		CharsAfter:    80,
	}
	ev := EventFromStats(stats)
	got := FormatStats(stats)
	fromEvent := FormatEvent(ev)
	if got != fromEvent {
		t.Fatalf("FormatStats and FormatEvent diverged:\n%s\n%s", got, fromEvent)
	}
	for _, want := range []string{
		"history_issues=input[1].item:local_shell_call tool=exec",
		"conf=0.93",
		"needs=0.88",
		"top=grep:0.7,read_file:0.2,respond_to_user:0.1",
		"connect=live",
		"rule=adopt-v1",
		"host=grok",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stats=%q missing %q", got, want)
		}
	}
	if ev.CandidateCount != 0 && ev.Probabilities["grep"] != 0.7 {
		t.Fatalf("event=%+v", ev)
	}
	if ev.Concentration == nil || math.Abs(*ev.Concentration-0.7/0.9) > 1e-9 {
		t.Fatalf("concentration=%v", ev.Concentration)
	}
}

func TestRecalculateKeptFromSavedEvent(t *testing.T) {
	e := Event{
		CandidateNames: []string{"grep", "read_file", "write"},
		Probabilities:  map[string]float64{"grep": 0.5, "read_file": 0.4, "write": 0.05, plan.Respond: 0.05},
		MustKeep:       []string{"write"},
	}
	kept, reason := RecalculateKept(e, 0.9, 0)
	if reason != "coverage" || len(kept) != 3 {
		t.Fatalf("kept=%v reason=%s", kept, reason)
	}
	if !containsName(kept, "write") || !containsName(kept, "grep") {
		t.Fatalf("must-keep or coverage lost: %v", kept)
	}

	short := Event{
		CandidateNames: []string{"a", "b", "c", "d"},
		Probabilities:  map[string]float64{"a": 0.3, "b": 0.2, "c": 0.2, "d": 0.2, plan.Respond: 0.1},
	}
	kept, reason = RecalculateKept(short, 0.9, 0)
	if reason != "coverage_short" || len(kept) != 4 {
		t.Fatalf("coverage short should keep all: kept=%v reason=%s", kept, reason)
	}

	missing := Event{
		CandidateNames: []string{"a", "b"},
		Probabilities:  map[string]float64{"a": 0.9, "b": 0.1},
		MissingFlags:   []string{"image"},
	}
	kept, reason = RecalculateKept(missing, 0.9, 1)
	if reason != "missing" || len(kept) != 2 {
		t.Fatalf("missing should keep all: kept=%v reason=%s", kept, reason)
	}

	invalid := Event{
		CandidateNames: []string{"a", "b"},
		Probabilities:  map[string]float64{"a": 2, "b": -1},
	}
	kept, reason = RecalculateKept(invalid, 0.9, 1)
	if reason != "invalid" || len(kept) != 2 {
		t.Fatalf("invalid should keep all: kept=%v reason=%s", kept, reason)
	}
}

func TestExtractObservedTools(t *testing.T) {
	chat, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{"id": "1", "function": map[string]any{"name": "grep"}},
					map[string]any{"id": "2", "function": map[string]any{"name": "read_file"}},
				},
			},
		}},
	})
	got := extractObservedTools(chat)
	if !containsName(got, "grep") || !containsName(got, "read_file") {
		t.Fatalf("chat tools=%v", got)
	}

	claude, _ := json.Marshal(map[string]any{
		"content": []any{map[string]any{"type": "tool_use", "id": "x", "name": "Read"}},
	})
	got = extractObservedTools(claude)
	if !containsName(got, "Read") {
		t.Fatalf("claude tools=%v", got)
	}

	sse := []byte("data: " + string(chat) + "\n\n")
	got = extractObservedTools(sse)
	if !containsName(got, "grep") {
		t.Fatalf("sse tools=%v", got)
	}
}

func TestRecordDecisionSetsConnectAndRule(t *testing.T) {
	stats := RewriteStats{}
	recordDecision(&stats, SelectionHybrid, nil, []string{"grep", "read_file"}, plan.Decision{
		Probabilities: map[string]float64{"grep": 0.8, plan.Respond: 0.2},
	}, false, nil)
	if stats.ConnectStatus != jev.StatusKeyMissing || stats.RuleVersion != adoptRuleVersion {
		t.Fatalf("stats=%+v", stats)
	}
	if stats.CandidateCount != 2 || stats.Probabilities["grep"] != 0.8 {
		t.Fatalf("candidates=%+v", stats)
	}
	if math.Abs(stats.Concentration-1) > 1e-9 {
		t.Fatalf("conc=%v", stats.Concentration)
	}
}
