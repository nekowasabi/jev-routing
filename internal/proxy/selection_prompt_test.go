package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

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
			"next_tool":  map[string]any{"type": "choice", "choice": "exec", "confidence": 0.9},
			"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
		}})
	})
	decision, reason, err := askNextTool(context.Background(), client, "Find the function definition", nil,
		[]plan.Spec{{Name: "exec", Desc: description}, {Name: "wait"}})
	if err != nil || reason != "" || decision.Tool != "exec" {
		t.Fatalf("decision=%+v reason=%s err=%v", decision, reason, err)
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
		{"yes-boundary", 0.8, 0.85, "exec", ""},
		{"no-boundary", 0.2, 0.9, plan.Respond, ""},
		{"uncertain", 0.5, 0.9, "exec", reasonUncertainJev},
		{"choice-still-uncertain", 0.99, 0.849, "exec", reasonUncertainJev},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := jevAnswers(t, "exec", tc.confidence, tc.probability, 0, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
					"next_tool":  map[string]any{"type": "choice", "choice": "exec", "confidence": tc.confidence},
					"needs_tool": map[string]any{"type": "noul", "noul": tc.probability},
				}})
			})
			decision, reason, err := askNextTool(context.Background(), client, "Find the function definition", nil,
				[]plan.Spec{{Name: "exec", Desc: "Run shell commands to search and read files"}})
			if err != nil || reason != tc.wantReason || decision.Tool != tc.wantTool || decision.Confidence != tc.confidence || decision.Done != tc.probability {
				t.Fatalf("decision=%+v reason=%s err=%v", decision, reason, err)
			}
		})
	}
}
