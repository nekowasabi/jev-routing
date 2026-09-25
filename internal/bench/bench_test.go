package bench

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSummarizeComparesMediansAndDropsContaminated(t *testing.T) {
	runs := []RunRecord{
		{Task: "chess-bugfix", Mode: "on", Rep: 1, Solved: true, Score: 1, Requests: 4, Metered: 4, Input: 50, Output: 40, Seconds: 10, Modes: map[string]int{"filter": 4}},
		{Task: "chess-bugfix", Mode: "off", Rep: 1, Solved: true, Score: 1, Requests: 8, Metered: 8, Input: 100, Output: 80, Seconds: 20, Modes: map[string]int{"passthrough": 8}},
		{Task: "chess-bugfix", Mode: "on", Rep: 2, Solved: false, Score: 0.5, Requests: 4, Input: 10, Output: 10, Seconds: 5, Isolation: &Isolation{Contaminated: true, ForeignReads: []string{"/tmp/other"}}, Modes: map[string]int{}},
	}
	got := Summarize(runs, &Prices{Input: 1, Cached: 0.1, Output: 2})
	if !strings.Contains(got, "Input tokens incl. cache, median | 50 (-50%) | 100 |") {
		t.Fatalf("summary missing input delta:\n%s", got)
	}
	if !strings.Contains(got, "Excluded as contaminated") || !strings.Contains(got, "chess-bugfix.on.2") {
		t.Fatalf("contaminated run was not named:\n%s", got)
	}
	if strings.Contains(got, "Only 0 run") {
		t.Fatalf("contaminated run was counted:\n%s", got)
	}
}

func TestSummarizeMissingMeterDoesNotClaimSavings(t *testing.T) {
	runs := []RunRecord{
		{Task: "chess-bugfix", Agent: "codex", Mode: "on", Requests: 1, Modes: map[string]int{"filter": 1}, MeterError: "usage missing"},
		{Task: "chess-bugfix", Agent: "codex", Mode: "off", Requests: 10, Metered: 10, Input: 100, JevCalls: 1, Modes: map[string]int{"passthrough": 10}},
	}
	got := Summarize(runs, nil)
	for _, row := range []string{
		"Input tokens incl. cache, median | n/a | 100 |",
		"LLM requests, median | n/a | 10 |",
		"Jev calls, median | n/a | 1 |",
		"Requests Jev steered | n/a | 0% |",
	} {
		if !strings.Contains(got, row) {
			t.Fatalf("incomplete meter must not report %q as savings:\n%s", row, got)
		}
	}
}

// TestSummarizeTotalTokensExcludesJevEvenWhenLarge is the fixed-data check
// for docs/MEMO.md "主指標から Jev を除外": the primary token metric is
// upstream input+output only, so a Jev usage far larger than the upstream
// usage must not move it at all.
func TestSummarizeTotalTokensExcludesJevEvenWhenLarge(t *testing.T) {
	runs := []RunRecord{
		{Task: "chess-bugfix", Agent: "codex", Mode: "on", Requests: 1, Metered: 1, Input: 10, Output: 2, JevInput: 50000, JevOutput: 90000},
		{Task: "chess-bugfix", Agent: "codex", Mode: "off", Requests: 1, Metered: 1, Input: 20, Output: 2},
	}
	got := Summarize(runs, nil)
	if !strings.Contains(got, "Total tokens (upstream), median | 12 (-45%) | 22 |") {
		t.Fatalf("Jev tokens leaked into the primary total-tokens metric:\n%s", got)
	}
	if !strings.Contains(got, "Jev tokens (separate from total), median | 140,000 | 0 |") {
		t.Fatalf("Jev tokens must still be reported separately:\n%s", got)
	}
}

func TestSummarizeUsesVerifiedSessionTotal(t *testing.T) {
	baseline, selection := 200, 150
	runs := []RunRecord{
		{Task: "xcell-module", Mode: "off", Requests: 3, Metered: 2, MeterError: "canceled response", TaskTokens: &baseline, UsageSource: "grok_cli_reconciled"},
		{Task: "xcell-module", Mode: "on", Requests: 3, Metered: 2, MeterError: "canceled response", TaskTokens: &selection, UsageSource: "grok_cli_reconciled"},
	}
	got := Summarize(runs, nil)
	if !strings.Contains(got, "Usage complete runs | 1 | 1 |") || !strings.Contains(got, "Total tokens (upstream), median | 150 (-25%) | 200 |") || !strings.Contains(got, "LLM requests, median | n/a | n/a |") {
		t.Fatalf("session total and request-level missing usage confused:\n%s", got)
	}
}

func TestSummarizeReportsCompaction(t *testing.T) {
	runs := []RunRecord{
		{Task: "chess-bugfix", Mode: "on", Rep: 1, Requests: 10, Metered: 10, CompactRequests: 3, CompactDropped: 7, CompactSavedTokens: 1200},
		{Task: "chess-bugfix", Mode: "off", Rep: 1, Requests: 10, Metered: 10},
	}
	got := Summarize(runs, nil)
	for _, row := range []string{
		"Requests compacted, median | 3 | 0 |",
		"Tool entries dropped or truncated, median | 7 | 0 |",
		"Input tokens saved by compaction (est.), median | 1,200 | 0 |",
	} {
		if !strings.Contains(got, row) {
			t.Fatalf("summary missing %q:\n%s", row, got)
		}
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
	got := Audit("codex", logPath, sandbox, "/home/someone", nil)
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
	got := Audit("claude", logPath, filepath.Join(dir, "sandbox"), "", nil)
	if !got.Contaminated || got.ToolCalls != 1 {
		t.Fatalf("audit = %+v", got)
	}
}

// TestAuditSnapshotClearsCommandsTheTextHeuristicCannotSeeAsCreates covers
// the on-disk evidence from a real bench run (chess_debug.mjs written via
// node's fs.writeFileSync, chess_debug.js via cp, chesscheck via a
// relative-path mkdir later referenced by its absolute path): none of
// those match the "was this created" regex, so before the snapshot they
// were misread as "found" (another run's file) and the run was wrongly
// marked contaminated.
func TestAuditSnapshotClearsCommandsTheTextHeuristicCannotSeeAsCreates(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "jev-bench-audit-test-")
	if err != nil {
		t.Skipf("no writable /var/tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TMPDIR", root)

	dir := t.TempDir()
	sandbox := filepath.Join(dir, "sandbox")
	logPath := filepath.Join(dir, "agent.log")

	// A sibling that was already there before this run's agent started —
	// still real contamination if the run reads it.
	preExisting := filepath.Join(root, "pre-existing")
	if err := os.WriteFile(preExisting, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	tmpSnapshot := snapshotTmp()

	// Made by this run via fs.writeFileSync (not `>`, `tee`, `mkdir` or
	// `touch`, so createdPath's regex never matches it).
	madeThisRun := filepath.Join(root, "chess_debug.mjs")
	if err := os.WriteFile(madeThisRun, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	body := "/bin/bash -lc 'node -e \"fs.writeFileSync(" + strconv.Quote(madeThisRun) + ", src)\"'\n" +
		"/bin/bash -lc 'cat " + preExisting + "'\n"
	if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Audit("codex", logPath, sandbox, "", tmpSnapshot)
	how := map[string]string{}
	for _, touch := range got.Outside {
		how[touch.Path] = touch.How
	}
	if how[madeThisRun] != "created" {
		t.Fatalf("path made this run via writeFileSync must be created, got %+v", got.Outside)
	}
	if how[preExisting] != "found" {
		t.Fatalf("path from before this run must stay found, got %+v", got.Outside)
	}
	if !got.Contaminated || len(got.ForeignReads) != 1 || got.ForeignReads[0] != preExisting {
		t.Fatalf("only the pre-existing path should contaminate, got %+v", got)
	}
}

func TestChartOmitsContaminated(t *testing.T) {
	dir := t.TempDir()
	runs := []RunRecord{
		{Task: "chess-bugfix", Agent: "codex", AgentModel: "gpt-5.6-sol", Mode: "on", Solved: true, Score: 1, Input: 1000, Output: 10, Requests: 2, Metered: 2, Seconds: 3},
		{Task: "chess-bugfix", Agent: "codex", AgentModel: "gpt-5.6-sol", Mode: "off", Solved: true, Score: 1, Input: 2000, Output: 20, Requests: 4, Metered: 4, Seconds: 6},
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

func TestChartRejectsIncompleteUsageAndMissingBaseline(t *testing.T) {
	base := RunRecord{Task: "chess-bugfix", Agent: "codex", AgentModel: "gpt-5.6-sol", Mode: "off", Requests: 1, Metered: 1, Input: 100}
	on := RunRecord{Task: base.Task, Agent: base.Agent, AgentModel: base.AgentModel, Mode: "on", Requests: 1, MeterError: "usage missing"}
	if err := Chart([]RunRecord{base, on}, t.TempDir()); err == nil {
		t.Fatal("chart accepted missing usage")
	}
	on.MeterError, on.Metered = "", 1
	if err := Chart([]RunRecord{on}, t.TempDir()); err == nil {
		t.Fatal("chart accepted an on run without a baseline")
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

func TestUsageFollowsEachProvider(t *testing.T) {
	claude := RunRecord{Task: "a", Agent: "claude", Mode: "on", Input: 14, Cached: 22629, CacheWrite: 55219, Output: 5143}
	codex := RunRecord{Task: "b", Agent: "codex", Mode: "on", Input: 1000, Cached: 800, Output: 50, Reasoning: 20}
	grok := RunRecord{Task: "c", Agent: "grok", Mode: "on", Input: 2000, Cached: 500, CacheWrite: 99, Output: 100}
	for _, tc := range []struct {
		run             RunRecord
		total, uncached int
		cost            float64 // at --prices 4,0.4,20,5
	}{
		{claude, 77862, 14, 0.388},
		{codex, 1000, 200, (200*4 + 800*0.4 + 50*20) / 1e6},
		{grok, 2000, 1500, (1500*4 + 500*0.4 + 100*20) / 1e6}, // stray CacheWrite is not billed
	} {
		if got := totalInput(tc.run); got != tc.total {
			t.Errorf("%s totalInput = %d, want %d", tc.run.Agent, got, tc.total)
		}
		if got := uncachedInput(tc.run); got != tc.uncached {
			t.Errorf("%s uncachedInput = %d, want %d", tc.run.Agent, got, tc.uncached)
		}
		for _, flag := range []string{"4,0.4,20,5", "4,0.4,20"} {
			prices, err := parsePrices(flag)
			if err != nil {
				t.Fatal(err)
			}
			if got := costOf(tc.run, *prices); math.Abs(got-tc.cost) > 0.001 {
				t.Errorf("%s --prices %s: cost = %f, want %f", tc.run.Agent, flag, got, tc.cost)
			}
		}
	}
	prices, _ := parsePrices("4,0.4,20,5")
	got := Summarize([]RunRecord{claude, codex, grok}, prices)
	section := func(task string) string {
		_, rest, _ := strings.Cut(got, "## "+task+"\n")
		body, _, _ := strings.Cut(rest, "\n\n")
		return body
	}
	for task, want := range map[string][]string{
		"a": {"Input tokens incl. cache, median | 77,862 |", "…of which cached | 29% |", "…uncached, median | 14 |", "…of which cache writes | 71% |"},
		"b": {"Input tokens incl. cache, median | 1,000 |", "…of which cached | 80% |", "…uncached, median | 200 |", "…of which reasoning | 20 |"},
		"c": {"Input tokens incl. cache, median | 2,000 |", "…of which cached | 25% |", "…uncached, median | 1,500 |"},
	} {
		body := section(task)
		for _, row := range want {
			if !strings.Contains(body, row) {
				t.Errorf("task %s missing %q:\n%s", task, row, body)
			}
		}
		if hasWrites := strings.Contains(body, "cache writes"); hasWrites != (task == "a") {
			t.Errorf("task %s cache-write row shown = %v:\n%s", task, hasWrites, body)
		}
		if hasReasoning := strings.Contains(body, "reasoning"); hasReasoning == (task == "a") {
			t.Errorf("task %s reasoning row shown = %v:\n%s", task, hasReasoning, body)
		}
	}
	// Only "on" was run; the empty "off" cells must not count as 0 runs.
	if !strings.Contains(got, "Only 1 run per cell") {
		t.Errorf("run-count note ignores unrun mode:\n%s", got)
	}
	if !strings.Contains(got, "The cache-write price was ignored for codex, grok") {
		t.Errorf("summary does not note the ignored cache-write price:\n%s", got)
	}
	if three, _ := parsePrices("4,0.4,20"); strings.Contains(Summarize([]RunRecord{codex}, three), "ignored") {
		t.Errorf("defaulted cache-write price should not be noted")
	}
}
