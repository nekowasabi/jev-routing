package plan

import (
	"testing"

	"github.com/nekowasabi/jev-routing-go/internal/host"
)

func TestGrokMapsGrepThenReadThenSearchReplace(t *testing.T) {
	p := "The auth middleware test is failing. Find the test, read it, fix the assertion in place, and re-run the tests."
	g0 := Remaining(p, nil, host.Grok)
	if g0[0][0] != "grep" && g0[0][0] != "list_dir" {
		t.Fatalf("first goal %v", g0[0])
	}
	after := Remaining(p, []Action{{Tool: "grep"}}, host.Grok)
	if after[0][0] != "read_file" {
		t.Fatalf("after grep %v", after[0])
	}
	afterRead := Remaining(p, []Action{{Tool: "grep"}, {Tool: "read_file"}}, host.Grok)
	if afterRead[0][0] != "search_replace" {
		t.Fatalf("after read %v", afterRead[0])
	}
}

func TestCodexMapsApplyPatch(t *testing.T) {
	p := "There is a typo 'recieve' somewhere in the repo. Grep for it and fix it with an in-place edit."
	g0 := Remaining(p, nil, host.Codex)
	if g0[0][0] != "grep_files" {
		t.Fatalf("got %v", g0[0])
	}
}

func TestGatePrematureRespond(t *testing.T) {
	p := "The auth middleware test is failing. Find it and fix it in place."
	d := Decide(p, nil, nil, host.Claude)
	if d.Tool == Respond {
		t.Fatalf("responded too early %+v", d)
	}
}

func TestPRReviewNotCreate(t *testing.T) {
	p := "Review GitHub PR 842. Fetch the PR, read the changed local files, and leave a review comment. Do not open a new pull request."
	g0 := Remaining(p, nil, host.Grok)
	if g0[0][0] != "github_get_pr" {
		t.Fatalf("got %v", g0)
	}
	after := Remaining(p, []Action{{Tool: "github_get_pr"}, {Tool: "read_file"}}, host.Grok)
	joined := ""
	for _, n := range after[0] {
		joined += n + ","
	}
	if after[0][0] != "github_pr_review" && after[0][0] != "github_comment" {
		t.Fatalf("got %v", after[0])
	}
}
