package plan

import "testing"

func TestSubagentRouting(t *testing.T) {
	cat, err := LoadCatalog("testdata/capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	brief := "review the diff"
	t.Run("explicit-role", func(t *testing.T) {
		got := RouteSubagent(SubagentRequest{Text: "review", Brief: brief, Catalog: cat, ExplicitRoles: []string{"reviewer"}}, nil)
		if got.Outcome != RouteSelected || got.Role != "reviewer" || got.Brief != brief || got.Launcher != "Agent" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("already-running", func(t *testing.T) {
		got := RouteSubagent(SubagentRequest{Text: "review", Brief: brief, Catalog: cat, RunningRoles: []string{"reviewer"}, ExplicitRoles: []string{"reviewer"}}, nil)
		if got.Outcome != RouteNoDecision {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("missing-brief", func(t *testing.T) {
		got := RouteSubagent(SubagentRequest{Text: "review", Catalog: cat, ExplicitRoles: []string{"reviewer"}}, nil)
		if got.ReasonCode != "missing_brief" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("forbid", func(t *testing.T) {
		got := RouteSubagent(SubagentRequest{Text: "review", Brief: brief, Catalog: cat, ForbidDelegate: true}, nil)
		if got.Outcome != RouteNoDecision {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("recover-by-ids", func(t *testing.T) {
		hist := []HistoryItem{
			{MessageID: "m1", AssignmentID: "a1", Kind: "progress", Body: "working"},
			{MessageID: "m2", AssignmentID: "a1", Kind: "final", Body: "done"},
			{MessageID: "m3", AssignmentID: "a2", Kind: "final", Body: "other"},
		}
		got := RecoverSubagent(hist, "a1")
		if !got.Matched || got.Kind != "final" || got.MessageID != "m2" {
			t.Fatalf("%+v", got)
		}
		if RecoverSubagent(hist, "missing").Matched {
			t.Fatal("matched missing")
		}
	})
	t.Run("child-command-not-executed", func(t *testing.T) {
		if err := ChildCommandAllowed([]string{"rm", "-rf", "/"}, []string{"go test"}); err == nil {
			t.Fatal("allowed unapproved")
		}
	})
}
