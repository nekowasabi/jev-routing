//go:build jev_live

// Live TypeSafe checks: Jev's next-tool choice given a Grok-like catalog
// that includes send_feedback. Not part of `go test ./...`.
//
//	go test -tags jev_live ./internal/proxy -run TestLiveJevCatalogAccuracy -count=1 -v
//	make test-jev-live
package proxy

import (
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestLiveJevCatalogAccuracy(t *testing.T) {
	client := jev.FromEnv()
	if !client.Live() {
		t.Fatal("TYPESAFE_API_KEY or JEV_API_KEY required for -tags jev_live")
	}

	specs := plan.SpecsFrom(asMaps(grokCatalog()))
	criteria := map[string]string{}
	allowed := map[string]bool{plan.Respond: true}
	for _, s := range specs {
		desc := s.Desc
		if desc == "" {
			desc = s.Name
		}
		if len(desc) > 240 {
			desc = desc[:240]
		}
		criteria[s.Name] = desc
		allowed[s.Name] = true
	}
	if _, ok := criteria["send_feedback"]; !ok {
		t.Fatal("fixture catalog must include send_feedback")
	}

	cases := []struct {
		name, prompt string
	}{
		{"dead-code", "デッドコードを調査し、不要なコードを削除してください。"},
		{"failing-test", "The auth middleware test is failing. Find it, fix the assertion in place, and re-run the tests."},
	}
	qs := map[string]jev.Question{
		"next_tool": {
			Type:         "choice",
			Instructions: "Which single tool should run next? Agent or Task launches a Claude Code subagent — pick it for broad exploration or parallel work. Pick respond_to_user only when the user request is fully satisfied.",
			Criteria:     criteria,
		},
		"needs_tool": {Type: "noul", Instructions: "A tool call is needed now to make progress. This is not a judgment that the overall user task is complete."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := client.Ask(map[string]any{
				"user_request":  plan.WorkRequest(tc.prompt),
				"actions_taken": []plan.Action{},
			}, qs)
			if err != nil {
				t.Fatal(err)
			}
			choice := jev.ChoiceOf(res, "next_tool")
			t.Logf("host=%s choice=%s needs_tool=%.2f", host.Grok, choice, jev.NoulOf(res, "needs_tool"))
			if choice == "" {
				t.Fatal("empty next_tool choice")
			}
			if !allowed[choice] {
				t.Fatalf("choice %q is not in the catalog", choice)
			}
			if plan.HostMeta(choice) {
				t.Fatalf("Jev picked host meta tool %q for %q", choice, tc.prompt)
			}
		})
	}
}
