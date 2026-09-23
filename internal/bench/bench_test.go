package bench

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarizeComparesMediansAndDropsContaminated(t *testing.T) {
	runs := []RunRecord{
		{Task: "chess-bugfix", Mode: "on", Rep: 1, Solved: true, Score: 1, Requests: 4, Input: 50, Output: 40, Seconds: 10, Modes: map[string]int{"filter": 4}},
		{Task: "chess-bugfix", Mode: "off", Rep: 1, Solved: true, Score: 1, Requests: 8, Input: 100, Output: 80, Seconds: 20, Modes: map[string]int{"passthrough": 8}},
		{Task: "chess-bugfix", Mode: "on", Rep: 2, Solved: false, Score: 0.5, Requests: 4, Input: 10, Output: 10, Seconds: 5, Isolation: &Isolation{Contaminated: true, ForeignReads: []string{"/tmp/other"}}, Modes: map[string]int{}},
	}
	got := Summarize(runs, &Prices{Input: 1, Cached: 0.1, Output: 2})
	if !strings.Contains(got, "Input tokens, median | 50 (-50%) | 100 |") {
		t.Fatalf("summary missing input delta:\n%s", got)
	}
	if !strings.Contains(got, "Excluded as contaminated") || !strings.Contains(got, "chess-bugfix.on.2") {
		t.Fatalf("contaminated run was not named:\n%s", got)
	}
	if strings.Contains(got, "Only 0 run") {
		t.Fatalf("contaminated run was counted:\n%s", got)
	}
}

func TestAuditFlagsForeignReadButNotAgentsSearch(t *testing.T) {
	dir := t.TempDir()
	sandbox := filepath.Join(dir, "sandbox")
	logPath := filepath.Join(dir, "agent.log")
	body := "/bin/bash -lc rg --files --hidden --iglob '**/AGENTS.md' --glob '!.git/**'\n" +
		"/bin/bash -lc 'echo hi > /tmp/check.mjs'\n" +
		"/bin/bash -lc cat /tmp/other-run/chess.js\n"
	if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Audit("codex", logPath, sandbox, "/home/someone")
	if !got.Audited || !got.Contaminated {
		t.Fatalf("audit = %+v", got)
	}
	if len(got.ForeignReads) != 1 || got.ForeignReads[0] != "/tmp/other-run/chess.js" {
		t.Fatalf("foreign reads = %v", got.ForeignReads)
	}
	created := false
	for _, touch := range got.Outside {
		if touch.Path == "/tmp/check.mjs" && touch.How == "created" {
			created = true
		}
	}
	if !created {
		t.Fatalf("created path missing: %+v", got.Outside)
	}
}

func TestAuditClaudeToolUse(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	line := `{"message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"cat /tmp/stolen.js"}}]}}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Audit("claude", logPath, filepath.Join(dir, "sandbox"), "")
	if !got.Contaminated || got.ToolCalls != 1 {
		t.Fatalf("audit = %+v", got)
	}
}

func TestChartOmitsContaminated(t *testing.T) {
	dir := t.TempDir()
	runs := []RunRecord{
		{Task: "chess-bugfix", Agent: "codex", AgentModel: "gpt-5.6-sol", Mode: "on", Solved: true, Score: 1, Input: 1000, Output: 10, Requests: 2, Seconds: 3},
		{Task: "chess-bugfix", Agent: "codex", AgentModel: "gpt-5.6-sol", Mode: "off", Solved: true, Score: 1, Input: 2000, Output: 20, Requests: 4, Seconds: 6},
		{Task: "chess-san", Agent: "codex", Mode: "on", Solved: true, Score: 1, Input: 9, Isolation: &Isolation{Contaminated: true}},
	}
	if err := Chart(runs, dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "comparison-light.svg"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "GPT-5.6 Sol · bugfix") || strings.Contains(text, "· san") {
		t.Fatalf("chart labels:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "comparison-dark.svg")); err != nil {
		t.Fatal(err)
	}
}

func TestList(t *testing.T) {
	if code := Run([]string{"--list"}); code != 0 {
		t.Fatal(code)
	}
}

func TestBugsMatchOnce(t *testing.T) {
	source := EngineWithoutSan()
	if strings.Contains(source, "// <san>") {
		t.Fatal("SAN block leaked into the bugfix baseline")
	}
	if _, err := inject(source, Bugs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Solution(), "history") {
		t.Fatal("solution dropped algebraic notation")
	}
}

func TestSelftest(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	if code := Selftest(os.Stdout); code != 0 {
		t.Fatal(code)
	}
}

func TestFakePipeline(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	t.Setenv("JEV_SELECTION_MODE", "local")
	t.Setenv("JEV_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("BENCH_FAKE_UPSTREAM", "")
	out := t.TempDir()
	code := Run([]string{"--agent", "fake", "--tasks", "chess-bugfix", "--modes", "on,off", "--reps", "1", "--port", "0", "--out", out})
	if code != 0 {
		t.Fatalf("bench exit %d", code)
	}
	runs, err := loadRuns(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d", len(runs))
	}
	byMode := map[string]RunRecord{}
	for _, run := range runs {
		byMode[run.Mode] = run
		if !run.Solved || run.Passed != run.Total || run.Total == 0 {
			t.Fatalf("unsolved %+v", run)
		}
		if run.Requests != 4 || run.Input != 48000 || run.Cached != 36000 {
			t.Fatalf("meter %+v", run)
		}
		if run.Isolation == nil || !run.Isolation.Audited || run.Isolation.Contaminated {
			t.Fatalf("isolation %+v", run.Isolation)
		}
	}
	if byMode["off"].Modes["passthrough"] != 4 {
		t.Fatalf("baseline was rewritten: %+v", byMode["off"].Modes)
	}
	if byMode["off"].Output != 1600 {
		t.Fatalf("baseline output = %d, want 1600", byMode["off"].Output)
	}
	if byMode["on"].Output != 320 {
		t.Fatalf("routed output = %d, want 320 (tool_choice replaced)", byMode["on"].Output)
	}
	summary, err := os.ReadFile(filepath.Join(out, "summary.md"))
	if err != nil || !strings.Contains(string(summary), "chess-bugfix") {
		t.Fatalf("summary: %v\n%s", err, summary)
	}
}
