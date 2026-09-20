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

func TestDecideSpecsReadPromptDoesNotPickRunSubagent(t *testing.T) {
	specs := []Spec{
		{Name: "read", Desc: "Read a file from the workspace."},
		{Name: "grep", Desc: "Search file contents."},
		{Name: "run_subagent", Desc: "Launch a subagent that can read files, search the codebase, and handle complex multi-step tasks autonomously. Use this to explore thoroughly or delegate."},
	}
	d := DecideSpecs("Read README, then read go.mod. Reply with only the first line of each file.", nil, specs, host.Devin)
	if d.Tool == "run_subagent" && !d.Passthrough {
		t.Fatalf("run_subagent won a file-read prompt: %+v", d)
	}
	if d.Tool != "read" && !d.Passthrough {
		t.Fatalf("want read, got %+v", d)
	}
}

func TestDecideSpecsSearchAndReadDoNotPickExec(t *testing.T) {
	specs := []Spec{
		{Name: "read", Desc: "Read a file from the workspace."},
		{Name: "grep", Desc: "Search file contents."},
		{Name: "exec", Desc: "Run a shell command."},
	}
	if got := DecideSpecs("Search the source for the failing assertion.", nil, specs, host.Devin); got.Tool != "grep" {
		t.Fatalf("search selected %s", got.Tool)
	}
	if got := DecideSpecs("Read the known implementation file.", nil, specs, host.Devin); got.Tool != "read" {
		t.Fatalf("read selected %s", got.Tool)
	}
}

const xcellLocatePrompt = "ファイルを変更せず、RewriteWith、extractTools、applyCompactToMessages、DefaultOptions、DefaultUpstream の定義を調べてください。各関数について個別のツール呼び出しで定義を検索し、別のツール呼び出しで本文を読んで確認してください（合計10回以上、並列化せず順に実行）。最終回答は関数名をキー、リポジトリ相対パス:定義行番号を値にしたJSONオブジェクトだけにしてください。説明文や完了マーカーは不要です。"

func xcellDevinSpecs() []Spec {
	return []Spec{
		{Name: "read", Desc: "Read a file from the workspace."},
		{Name: "grep", Desc: "Search file contents."},
		{Name: "run_subagent", Desc: "Launch a subagent that can read files, search the codebase, and handle complex multi-step tasks autonomously. Use this to explore thoroughly or delegate."},
		{Name: "exec", Desc: "Run a shell command."},
		{Name: "web_search", Desc: "Search the web."},
	}
}

func TestDecideSpecsXCellJapaneseDoesNotPickRunSubagent(t *testing.T) {
	d := DecideSpecs(xcellLocatePrompt, nil, xcellDevinSpecs(), host.Devin)
	if d.Tool == "run_subagent" || d.Passthrough {
		t.Fatalf("run_subagent won x-cell prompt: %+v", d)
	}
	if d.Tool != "grep" && d.Tool != "read" {
		t.Fatalf("want grep or read, got %+v", d)
	}
}

func TestDecideSpecsXCellAfterGrepPicksRead(t *testing.T) {
	d := DecideSpecs(xcellLocatePrompt, []Action{{Tool: "grep"}}, xcellDevinSpecs(), host.Devin)
	if d.Tool != "read" {
		t.Fatalf("after grep want read, got %+v", d)
	}
}

func TestDecideSpecsXCellJapaneseWithPreambleDoesNotPickRunSubagent(t *testing.T) {
	preamble := strings.Repeat("You are Devin. Search the codebase thoroughly, explore relevant files, and delegate multi-step work with run_subagent. ", 20)
	d := DecideSpecs(preamble+"\n"+xcellLocatePrompt, nil, xcellDevinSpecs(), host.Devin)
	if d.Tool == "run_subagent" || d.Passthrough {
		t.Fatalf("run_subagent won polluted x-cell prompt: %+v", d)
	}
	if d.Tool != "grep" && d.Tool != "read" {
		t.Fatalf("DecideSpecs on polluted x-cell = %+v, want grep or read", d)
	}
}

func TestTaskTextIsolatesXCellAskFromPreamble(t *testing.T) {
	preamble := strings.Repeat("You are Devin. Search the codebase thoroughly, explore, and delegate with run_subagent. ", 20)
	got := taskText(preamble + "\n" + xcellLocatePrompt)
	if strings.Contains(strings.ToLower(got), "codebase thoroughly") {
		t.Fatalf("scored preamble: %q", got)
	}
	if !strings.Contains(got, "定義を検索") {
		t.Fatalf("lost ask: %q", got)
	}
	d := DecideSpecs(WorkRequest(preamble+"\n"+xcellLocatePrompt), nil, xcellDevinSpecs(), host.Devin)
	if d.Tool == "run_subagent" || d.Passthrough {
		t.Fatalf("WorkRequest polluted x-cell picked run_subagent: %+v", d)
	}
	if d.Tool != "grep" && d.Tool != "read" {
		t.Fatalf("DecideSpecs on WorkRequest polluted x-cell = %+v, want grep or read", d)
	}
}

func TestPreferTaskTextKeepsSequentialLocate(t *testing.T) {
	checkout := "checkout the workspace and continue"
	if got := PreferTaskText(xcellLocatePrompt, checkout); got != xcellLocatePrompt {
		t.Fatalf("later checkout replaced locate ask: %q", got)
	}
	if got := PreferTaskText(xcellLocatePrompt, ""); got != xcellLocatePrompt {
		t.Fatalf("empty next dropped locate ask: %q", got)
	}
	if got := PreferTaskText("", checkout); got != checkout {
		t.Fatalf("empty prev should take next: %q", got)
	}
	if sequentialLocate(PreferTaskText("", checkout)) {
		t.Fatal("checkout-only blob claimed as sequentialLocate")
	}
}

func TestRemainingSequentialLocateOmitsExec(t *testing.T) {
	g := Remaining(xcellLocatePrompt+" checkout the workspace and continue", nil, host.Devin)
	for _, names := range g {
		for _, n := range names {
			switch n {
			case "exec", "edit", "write", "playwright_navigate", "playwright_snapshot":
				t.Fatalf("sequentialLocate remaining added %s: %v", n, g)
			}
		}
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

func TestWorkRequestUsesLastUserQuery(t *testing.T) {
	first := "investigate the repo with subagents"
	second := "fix the typo in foo.go"
	got := WorkRequest("<user_query>\n" + first + "\n</user_query>\npreamble\n<user_query>\n" + second + "\n</user_query>\n")
	if got != second {
		t.Fatalf("got %q want last query", got)
	}
}

func TestDecideSpecsPassthroughAfterAgentStreak(t *testing.T) {
	specs := []Spec{
		{Name: "spawn_subagent", Desc: "Start a subagent"},
		{Name: "task", Desc: "Launch a task"},
		{Name: "read_file", Desc: "Read a file"},
		{Name: "search_replace", Desc: "Edit a file"},
	}
	actions := []Action{
		{Tool: "spawn_subagent"},
		{Tool: "task"},
		{Tool: "spawn_subagent"},
	}
	d := DecideSpecs("Explore the auth package thoroughly with subagents", actions, specs, host.Grok)
	if isAgent(d.Tool) && !d.Passthrough {
		t.Fatalf("chose Agent again after streak %+v", d)
	}
	if !d.Passthrough && d.Tool != Respond {
		t.Fatalf("want passthrough or Respond, got %+v", d)
	}
}

func TestDecideSpecsStopsAfterAgentStreak(t *testing.T) {
	specs := []Spec{
		{Name: "spawn_subagent", Desc: "Start a subagent"},
		{Name: "task", Desc: "Launch a task"},
		{Name: "read_file", Desc: "Read a file"},
	}
	actions := []Action{{Tool: "spawn_subagent"}, {Tool: "spawn_subagent"}}
	d := DecideSpecs("Explore the auth package thoroughly with subagents", actions, specs, host.Grok)
	if !d.Passthrough || d.Tool != Respond {
		t.Fatalf("want passthrough Respond after agent streak, got %+v", d)
	}
}

func TestPhaseOfIgnoresDescriptionProse(t *testing.T) {
	// Real descriptions name other phases: an edit tool tells you to read the
	// file first, a shell tool offers to find and list files.
	for _, tc := range []struct {
		spec Spec
		want string
	}{
		{Spec{Name: "Edit", Desc: "Performs exact string replacement in a file. You must Read the file before editing it, and the old string must match exactly."}, PhaseModify},
		{Spec{Name: "Write", Desc: "Writes a file to the local filesystem, overwriting if one exists. Read the existing file first; use Edit for partial changes."}, PhaseModify},
		{Spec{Name: "Bash", Desc: "Executes a bash command. Avoid using it to read files or to find and list paths; use the dedicated search tools for that."}, PhaseExecute},
		{Spec{Name: "Grep", Desc: "Searches file contents with a regular expression and lists every matching path, so you can then read the interesting ones."}, PhaseLocate},
		{Spec{Name: "Read", Desc: "Reads a file from the local filesystem. Use it when you know the path; it does not search, edit or run anything."}, PhaseRead},
		{Spec{Name: "sentry_get_issue", Desc: "Fetch one production error by id."}, PhaseRead},
		{Spec{Name: "wait", Desc: "Pause for a while."}, ""},
	} {
		if got := PhaseOf(tc.spec); got != tc.want {
			t.Errorf("PhaseOf(%s) = %q, want %q", tc.spec.Name, got, tc.want)
		}
	}
}
