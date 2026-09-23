package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestAdoptCandidatesSeq5NoLongerNeedsToolGate(t *testing.T) {
	kept, why := adoptCandidates(
		map[string]float64{"grep": 0.93, "read_file": 0.04, plan.Respond: 0.03},
		[]string{"grep", "read_file", "write"},
		nil, nil, 0.9, 0,
	)
	if why != "coverage" || !containsName(kept, "grep") || containsName(kept, "write") {
		t.Fatalf("seq5-style high choice must keep coverage set: kept=%v why=%s", kept, why)
	}

	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	opt.Compaction = CompactionOff
	client := jevRaw(t, map[string]any{
		"next_tool": map[string]any{
			"type": "choice", "choice": "grep", "confidence": 0.93,
			"probabilities": map[string]float64{"grep": 0.93, "read_file": 0.04, "respond_to_user": 0.03},
		},
		"needs_tool": map[string]any{"type": "noul", "noul": 0.5, "confidence": 0.9},
	})
	_, stats, err := RewriteWith(nil, chatReq("summarize this repo's architecture for me", workTools()), host.Grok, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason == reasonUncertainJev || stats.Reason == reasonNoToolNeeded {
		t.Fatalf("needs_tool gray zone must not reject: %+v", stats)
	}
	if stats.Reason != reasonCoverage || !containsName(stats.ToolsAfter, "grep") {
		t.Fatalf("seq5 rewrite: %+v", stats)
	}
}

func TestAdoptCandidatesSpreadKeepsAll(t *testing.T) {
	kept, why := adoptCandidates(
		map[string]float64{"a": 0.3, "b": 0.25, "c": 0.2, "d": 0.15, plan.Respond: 0.1},
		[]string{"a", "b", "c", "d"},
		nil, nil, 0.9, 0,
	)
	if why != "coverage_short" || len(kept) != 4 {
		t.Fatalf("spread mass must keep all: kept=%v why=%s", kept, why)
	}
}

func TestSkipClassifierOnMissingFlags(t *testing.T) {
	if !skipClassifier(8, []string{missingImage}, 0) {
		t.Fatal("missing flags should skip the classifier")
	}
	if skipClassifier(8, nil, 0) {
		t.Fatal("disabled cost gate must still ask")
	}
	if !skipClassifier(3, nil, decidedCostGateMaxCandidates) || skipClassifier(4, nil, decidedCostGateMaxCandidates) {
		t.Fatal("production cost gate is N<=3")
	}
}

func TestJudgmentStateJSONHasNoSecrets(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"model": "codex",
		"input": []any{map[string]any{"type": "agent_message", "encrypted_content": "do-not-send"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "grep"}}},
	})
	var root map[string]any
	_ = json.Unmarshal(raw, &root)
	extra := judgmentExtras("hi", nil, nil, []string{"grep"}, root, nil, collectMissingFlags(root))
	blob, _ := json.Marshal(extra)
	if strings.Contains(string(blob), "do-not-send") {
		t.Fatalf("encrypted content leaked into judgment state: %s", blob)
	}
	if !containsName(extra["missing_flags"].([]string), missingOpaqueBlock) {
		t.Fatalf("flags=%v", extra["missing_flags"])
	}
}
