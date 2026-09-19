package plan

import (
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestDecideSpecsDoesNotPickSendFeedbackOnPreamble(t *testing.T) {
	preamble := strings.Repeat("user message session tool output draft feedback review comments. ", 80)
	query := "デッドコードを調査し、不要なコードを削除してください。"
	specs := []Spec{
		{Name: "read_file", Desc: "Read a file"},
		{Name: "grep", Desc: "Search files"},
		{Name: "spawn_subagent", Desc: "Start a subagent"},
		{Name: "send_feedback", Desc: strings.Repeat("Save or update user feedback for later review. Drafts, messages, session, tool output, user request. ", 40)},
	}
	d := DecideSpecs(preamble+"\n"+query, nil, specs, host.Grok)
	if d.Tool == "send_feedback" && !d.Passthrough {
		t.Fatalf("send_feedback pinned %+v", d)
	}
	d2 := DecideSpecs(WorkRequest(preamble+"\n<user_query>\n"+query+"\n</user_query>"), nil, specs, host.Grok)
	if d2.Tool == "send_feedback" && !d2.Passthrough {
		t.Fatalf("tagged query pinned send_feedback %+v", d2)
	}
}

func TestWorkRequestUsesUserQueryNotPreamble(t *testing.T) {
	preamble := strings.Repeat("user message session tool output draft feedback review. ", 200)
	want := "grep for the failing test and fix it"
	got := WorkRequest(preamble + "\n<user_query>\n" + want + "\n</user_query>\n")
	if got != want {
		t.Fatalf("got %q", got)
	}
	plain := "just a short prompt"
	if WorkRequest(plain) != plain {
		t.Fatalf("untagged request was rewritten")
	}
}

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

func TestPendingAgentAllowsRespond(t *testing.T) {
	d := Decide("/ship", []Action{{Tool: "Agent", Pending: true}}, nil, host.Claude)
	if d.Tool != Respond || d.Confidence < 0.8 {
		t.Fatalf("pending subagent should let the host stop and wait, got %+v", d)
	}
}

func TestPRReviewNotCreate(t *testing.T) {
	p := "Review GitHub PR 842. Fetch the PR, read the changed local files, and leave a review comment. Do not open a new pull request."
	g0 := Remaining(p, nil, host.Grok)
	if g0[0][0] != "github_get_pr" {
		t.Fatalf("got %v", g0)
	}
	after := Remaining(p, []Action{{Tool: "github_get_pr"}, {Tool: "read_file"}}, host.Grok)
	if after[0][0] != "github_pr_review" && after[0][0] != "github_comment" {
		t.Fatalf("got %v", after[0])
	}
}

func TestUnknownPromptDoesNotEndSession(t *testing.T) {
	specs := []Spec{
		{Name: "Read"}, {Name: "Grep"}, {Name: "Agent", Desc: "Launch a new agent"}, {Name: "Bash"},
	}
	d := DecideSpecs("summarize this repo's architecture for me", nil, specs, host.Claude)
	if !d.Passthrough && d.Tool == Respond {
		t.Fatalf("unknown prompt ended the session %+v", d)
	}
}

func TestExplorePicksAgentOrTask(t *testing.T) {
	p := "Explore the auth package thoroughly and report how sessions are stored."
	d := DecideSpecs(p, nil, []Spec{
		{Name: "Read"},
		{Name: "Grep"},
		{Name: "Agent", Desc: "Launch a new agent to handle complex multi-step tasks autonomously"},
		{Name: "Bash"},
	}, host.Claude)
	if d.Passthrough {
		t.Fatal("should pick a tool, not passthrough")
	}
	if d.Tool != "Agent" {
		t.Fatalf("got %s", d.Tool)
	}
	d2 := DecideSpecs(p, nil, []Spec{
		{Name: "Read"},
		{Name: "Task", Desc: "Launch a new agent"},
		{Name: "Grep"},
	}, host.Claude)
	if d2.Tool != "Task" && d2.Tool != "Agent" {
		t.Fatalf("Task alias got %s", d2.Tool)
	}
}

func TestSubagentBriefDoesNotRespond(t *testing.T) {
	brief := "You are a subagent. Search src/ for session cookie parsing and return the file path and a 4-line summary. Do not edit."
	d := DecideSpecs(brief, nil, []Spec{
		{Name: "Read", Desc: "Read a file"},
		{Name: "Grep", Desc: "Search file contents"},
		{Name: "Glob", Desc: "Find files by glob"},
		{Name: "Agent", Desc: "Launch a new agent"},
	}, host.Claude)
	if d.Tool == Respond && !d.Passthrough {
		t.Fatalf("subagent brief was treated as done %+v", d)
	}
}
