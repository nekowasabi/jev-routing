package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestSelectionUsesCompleteCandidateCapabilities(t *testing.T) {
	// The useful capability occurs past the old cutoff, which also split UTF-8.
	description := strings.Repeat("x", 239) + "関数 tools.exec_command を呼び出してシェルで検索できます。"
	client := jevAnswers(t, "exec", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Questions map[string]jev.Question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		next := request.Questions["next_tool"]
		if got := next.Criteria["exec"]; got != description || !utf8.ValidString(got) {
			t.Errorf("candidate capability was truncated or corrupted: %q", got)
		}
		if next.Criteria["wait"] != "wait" {
			t.Error("missing-description fallback changed")
		}
		for _, text := range []string{next.Instructions, next.Criteria[plan.Respond]} {
			for _, hostSpecific := range []string{"Claude", "Agent/Task"} {
				if strings.Contains(text, hostSpecific) {
					t.Errorf("host-specific selection instruction: %s", text)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
			"next_tool":  map[string]any{"type": "choice", "choice": "exec", "confidence": 0.9, "probabilities": adoptTestProbs("exec")},
			"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
		}})
	})
	decision, reason, err := askNextTool(context.Background(), client, "Find the function definition", nil,
		[]plan.Spec{{Name: "exec", Desc: description}, {Name: "wait"}}, "", nil)
	if err != nil || reason != reasonCoverage || decision.Tool != "exec" {
		t.Fatalf("decision=%+v reason=%s err=%v", decision, reason, err)
	}
}

// jevRaw answers with the exact payload the test supplies.
func jevRaw(t *testing.T, answers map[string]any) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	t.Cleanup(srv.Close)
	return &jev.Client{APIKey: "k", BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}
}

func uncertainChoice(probabilities map[string]float64, extra map[string]any) map[string]any {
	answers := map[string]any{
		"next_tool":  map[string]any{"type": "choice", "choice": "grep", "confidence": 0.6, "probabilities": probabilities},
		"needs_tool": map[string]any{"type": "noul", "noul": 0.9},
	}
	for k, v := range extra {
		answers[k] = v
	}
	return answers
}

func TestUncertainChoiceKeepsConfidentTopSet(t *testing.T) {
	client := jevRaw(t, uncertainChoice(map[string]float64{"grep": 0.55, "read_file": 0.38, "run_terminal_command": 0.07}, nil))
	_, stats, err := Rewrite(chatReq("summarize this repo's architecture for me", workTools()), host.Grok, client)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCoverage || strings.Join(stats.ToolsAfter, ",") != "grep,read_file" {
		t.Fatalf("reason=%s tools=%v", stats.Reason, stats.ToolsAfter)
	}
}

func TestUncertainChoiceWithSpreadMassPassesThrough(t *testing.T) {
	client := jevRaw(t, uncertainChoice(map[string]float64{"grep": 0.5, "read_file": 0.3, "run_terminal_command": 0.2}, nil))
	_, stats, err := Rewrite(chatReq("summarize this repo's architecture for me", workTools()), host.Grok, client)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCoverageShort || stats.ToolAfter != stats.ToolBefore {
		t.Fatalf("reason=%s tools %d→%d", stats.Reason, stats.ToolBefore, stats.ToolAfter)
	}
}

// phaseSpecs carries descriptions as verbose as a real host's, each naming
// phases other than its own.
func phaseSpecs() []plan.Spec {
	return []plan.Spec{
		{Name: "grep", Desc: "Search file contents with a regular expression and list the matching paths so you can read them afterwards."},
		{Name: "read_file", Desc: "Read a file from the workspace. Use it when you already know the path; it cannot search, edit or run anything."},
		{Name: "run_terminal_command", Desc: "Execute a shell command in the workspace. Prefer the dedicated tools when you only need to find, list or read files."},
	}
}

func TestUncertainChoiceRestrictsToTaskPhase(t *testing.T) {
	client := jevRaw(t, uncertainChoice(map[string]float64{"grep": 0.5, "read_file": 0.3, "run_terminal_command": 0.2}, map[string]any{
		"task_phase": map[string]any{"type": "choice", "choice": plan.PhaseExecute, "confidence": 0.9},
	}))
	decision, reason, err := askNextTool(context.Background(), client, "run the tests", nil, phaseSpecs(), "", nil)
	if err != nil || reason != reasonCoverageShort || !decision.Passthrough {
		t.Fatalf("decision=%+v reason=%s err=%v", decision, reason, err)
	}
}

func TestPhaseSetKeepsRepeatedTool(t *testing.T) {
	client := jevRaw(t, uncertainChoice(map[string]float64{"grep": 0.5, "read_file": 0.3, "run_terminal_command": 0.2}, map[string]any{
		"task_phase":       map[string]any{"type": "choice", "choice": plan.PhaseExecute, "confidence": 0.9},
		"repeat_same_tool": map[string]any{"type": "noul", "noul": 0.9},
	}))
	decision, reason, err := askNextTool(context.Background(), client, "run the tests",
		[]plan.Action{{Tool: "grep", Result: "no match"}}, phaseSpecs(), "", nil)
	if err != nil || reason != reasonCoverageShort || !decision.Passthrough {
		t.Fatalf("decision=%+v reason=%s err=%v", decision, reason, err)
	}
}

func TestSelectionStateCarriesAssistantPlan(t *testing.T) {
	var got string
	client := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State struct {
				AssistantPlan string `json:"assistant_plan"`
			} `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		got = request.State.AssistantPlan
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
			"next_tool":  map[string]any{"type": "choice", "choice": "grep", "confidence": 0.9},
			"needs_tool": map[string]any{"type": "noul", "noul": 0.9},
		}})
	})
	body, _ := json.Marshal(map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "summarize this repo's architecture for me"},
			map[string]any{"role": "assistant", "content": "I read the old plan, which no longer applies"},
			map[string]any{"role": "assistant", "content": "I'll now edit foo.go",
				"tool_calls": []any{map[string]any{"id": "c1", "function": map[string]any{"name": "grep", "arguments": "{}"}}}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": "hit"},
		},
		"tools": workTools(),
	})
	if _, _, err := Rewrite(body, host.Grok, client); err != nil {
		t.Fatal(err)
	}
	if got != "I'll now edit foo.go" {
		t.Fatalf("assistant_plan=%q", got)
	}
}

func TestSelectionAcceptsOfficialNoulWithoutConfidence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		probability float64
		confidence  float64
		wantTool    string
		wantReason  string
	}{
		{"yes-boundary", 0.8, 0.85, "exec", reasonCoverage},
		{"no-boundary", 0.2, 0.9, "exec", reasonCoverage},
		{"uncertain", 0.5, 0.9, "exec", reasonCoverage},
		{"choice-still-uncertain", 0.99, 0.849, "exec", reasonCoverage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := jevAnswers(t, "exec", tc.confidence, tc.probability, 0, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
					"next_tool":  map[string]any{"type": "choice", "choice": "exec", "confidence": tc.confidence, "probabilities": adoptTestProbs("exec")},
					"needs_tool": map[string]any{"type": "noul", "noul": tc.probability},
				}})
			})
			decision, reason, err := askNextTool(context.Background(), client, "Find the function definition", nil,
				[]plan.Spec{{Name: "exec", Desc: "Run shell commands to search and read files"}}, "", nil)
			if err != nil || reason != tc.wantReason || decision.Tool != tc.wantTool || decision.Confidence != tc.confidence || decision.Done != tc.probability {
				t.Fatalf("decision=%+v reason=%s err=%v", decision, reason, err)
			}
		})
	}
}
