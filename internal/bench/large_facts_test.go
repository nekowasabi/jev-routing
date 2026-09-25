package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func largeFactsTask(t *testing.T) Task {
	t.Helper()
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == "large-facts" {
			return task
		}
	}
	t.Fatal("large-facts task missing")
	return Task{}
}

// TestLargeFactsFilesShapeAndFactPositions checks the six generated logs are
// each about 32KB of low-entropy, realistic-looking log text and that each
// fact line sits near the head (~5%), middle (~50%), or tail (~95%) of its
// file as specified.
//
// 32KB (~8K tokens at ordinary text's ~4 bytes/token) is deliberate: it must
// clear jev-routing's own 20000-byte proxy threshold, but stay under Codex's
// own code-mode exec output cap (~10K tokens), which truncates by *token*
// count. High-entropy hex text runs ~2.3 bytes/token, so an earlier version
// of this file (36.8KB of hex) was already cut to ~10KB by Codex itself
// before ever reaching the proxy -- see docs/MEMO.md.
func TestLargeFactsFilesShapeAndFactPositions(t *testing.T) {
	files := largeFactsFiles()
	if len(files) != 6 {
		t.Fatalf("want 6 files, got %d", len(files))
	}
	wantPct := map[int]float64{1: 0.05, 2: 0.05, 3: 0.5, 4: 0.5, 5: 0.95, 6: 0.95}
	for f := 1; f <= 6; f++ {
		name := "logs/large-" + string(rune('0'+f)) + ".txt"
		content, ok := files[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if len(content) <= 20000 {
			t.Fatalf("%s size = %d bytes, must exceed the 20000-byte proxy threshold", name, len(content))
		}
		if len(content) < 28000 || len(content) > 34000 {
			t.Fatalf("%s size = %d bytes, want ~32KB", name, len(content))
		}
		factLine := "fact" + string(rune('0'+f)) + "="
		idx := strings.Index(content, factLine)
		if idx < 0 {
			t.Fatalf("%s missing %s", name, factLine)
		}
		pos := float64(idx) / float64(len(content))
		if want := wantPct[f]; pos < want-0.1 || pos > want+0.1 {
			t.Fatalf("%s fact at %.2f of file, want near %.2f", name, pos, want)
		}
		// Exactly one fact line per file.
		if strings.Count(content, factLine) != 1 {
			t.Fatalf("%s has more than one %s", name, factLine)
		}
	}
}

func TestLargeFactsTaskRoundTrip(t *testing.T) {
	task := largeFactsTask(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := task.Setup(workspace); err != nil {
		t.Fatal(err)
	}
	start, err := verify(task, workspace, "")
	if err != nil || start.Solved {
		t.Fatalf("starting state passed: %+v, %v", start, err)
	}
	if err := task.Reference(workspace); err != nil {
		t.Fatal(err)
	}
	done, err := verify(task, workspace, "")
	if err != nil || !done.Solved || done.Total != 6 {
		t.Fatalf("reference failed: %+v, %v", done, err)
	}
}

// TestLargeFactsEvidenceToleratesRefetchesAfterFullRead mirrors
// TestCodexCompactEvidenceRequiresFullOrderedReads but for large-facts: an
// extra sed/rg re-read after the initial full `cat` must not fail evidence
// (truncation legitimately forces a narrower re-read to recover a fact cut
// from the middle) and must be counted in LargeFactsRefetches.
func TestLargeFactsEvidenceToleratesRefetchesAfterFullRead(t *testing.T) {
	task := largeFactsTask(t)
	workspace := t.TempDir()
	if err := task.Setup(workspace); err != nil {
		t.Fatal(err)
	}
	var lines []byte
	for f := 1; f <= 6; f++ {
		name := "logs/large-" + string(rune('0'+f)) + ".txt"
		content, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil {
			t.Fatal(err)
		}
		event, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "status": "completed", "command": "/bin/zsh -lc 'cat " + name + "'", "aggregated_output": string(content)}})
		lines = append(append(lines, event...), '\n')
	}
	log := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(log, lines, 0o600); err != nil {
		t.Fatal(err)
	}
	complete, refetches := largeFactsEvidence(log, workspace)
	if !complete || refetches != 0 {
		t.Fatalf("complete full reads: complete=%v refetches=%d", complete, refetches)
	}

	// A re-fetch (e.g. `sed -n` after a truncated cat) on file 1 must not
	// fail evidence, and must be counted once.
	refetch, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "status": "completed", "command": "/bin/zsh -lc 'sed -n \"1,5p\" logs/large-1.txt'", "aggregated_output": "partial"}})
	withRefetch := append(append([]byte{}, lines...), append(refetch, '\n')...)
	if err := os.WriteFile(log, withRefetch, 0o600); err != nil {
		t.Fatal(err)
	}
	complete, refetches = largeFactsEvidence(log, workspace)
	if !complete {
		t.Fatal("re-fetch after full read incorrectly failed evidence")
	}
	if refetches != 1 {
		t.Fatalf("want 1 refetch, got %d", refetches)
	}

	// A brace-expanded rg/grep re-check over all six files (as the model
	// actually issued in the pilot: `rg '^fact[1-6]=' logs/large-{1,2,3,4,5,6}.txt`)
	// must be counted as one refetch per file it names, not zero.
	braceRefetch, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "status": "completed", "command": "/bin/bash -lc \"rg '^fact[1-6]=' logs/large-{1,2,3,4,5,6}.txt\"", "aggregated_output": "matches"}})
	withBrace := append(append([]byte{}, withRefetch...), append(braceRefetch, '\n')...)
	if err := os.WriteFile(log, withBrace, 0o600); err != nil {
		t.Fatal(err)
	}
	complete, refetches = largeFactsEvidence(log, workspace)
	if !complete {
		t.Fatal("brace-expanded re-fetch incorrectly failed evidence")
	}
	// file 1 already had 1 refetch (the sed above) plus 1 more from the brace
	// command = 2; files 2-6 get 1 each from the brace command = 5. Total 7.
	if refetches != 7 {
		t.Fatalf("want 7 refetches (1 sed + 6 brace-expanded touches), got %d", refetches)
	}

	// Missing one file's full read must fail evidence.
	missingOne := bytes.SplitN(lines, []byte("\n"), 2)[1] // drop file 1's cat
	if err := os.WriteFile(log, missingOne, 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, _ := largeFactsEvidence(log, workspace); complete {
		t.Fatal("missing a full read accepted")
	}
}

func TestLargeFactsReferencedFilesParsesBraceForms(t *testing.T) {
	cases := []struct {
		command string
		want    []int
	}{
		{"cat logs/large-3.txt", []int{3}},
		{"rg foo logs/large-{1,2,3,4,5,6}.txt", []int{1, 2, 3, 4, 5, 6}},
		{"rg foo logs/large-{1..6}.txt", []int{1, 2, 3, 4, 5, 6}},
		{"rg foo logs/large-{4..6}.txt", []int{4, 5, 6}},
		{"rg foo logs/large-{1,3..5}.txt", []int{1, 3, 4, 5}},
		{"head -n1 logs/large-2.txt", []int{2}},
		{"sed -n '1,5p' logs/large-1.txt", []int{1}},
		{"echo no files here", nil},
	}
	for _, c := range cases {
		got := largeFactsReferencedFiles(c.command)
		if len(got) != len(c.want) {
			t.Fatalf("%q: got %v, want %v", c.command, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q: got %v, want %v", c.command, got, c.want)
			}
		}
	}
}

// TestCodexToolOutputTruncateFlagValidation covers run.go's
// --codex-tool-output-truncate guardrails: it requires --agent codex and
// --modes on,off.
func TestCodexToolOutputTruncateFlagValidation(t *testing.T) {
	// --list/--help would short-circuit before these checks run, so it is
	// deliberately omitted here: each case must fail validation (exit 2)
	// before run.go ever reaches claimLock or spawns an agent.
	if code := runCmd([]string{"--agent", "claude", "--codex-tool-output-truncate", "--tasks", "large-facts"}); code != 2 {
		t.Fatalf("non-codex agent: want exit 2, got %d", code)
	}
	if code := runCmd([]string{"--agent", "codex", "--codex-tool-output-truncate", "--modes", "on", "--tasks", "large-facts"}); code != 2 {
		t.Fatalf("modes != on,off: want exit 2, got %d", code)
	}
}

// TestCodexNativeCompactionFlagValidation covers run.go's
// --codex-native-compaction guardrails: it requires --agent codex,
// --codex-compact-limit N with no --codex-compact-baseline-limit (the same
// N must trigger compaction on both sides), and --modes off,on.
func TestCodexNativeCompactionFlagValidation(t *testing.T) {
	if code := runCmd([]string{"--agent", "codex", "--codex-native-compaction", "--tasks", "compact-facts"}); code != 2 {
		t.Fatalf("missing --codex-compact-limit: want exit 2, got %d", code)
	}
	if code := runCmd([]string{"--agent", "claude", "--codex-native-compaction", "--codex-compact-limit", "55000", "--tasks", "compact-facts"}); code != 2 {
		t.Fatalf("non-codex agent: want exit 2, got %d", code)
	}
	if code := runCmd([]string{"--agent", "codex", "--codex-native-compaction", "--codex-compact-limit", "55000", "--codex-compact-baseline-limit", "900000", "--tasks", "compact-facts"}); code != 2 {
		t.Fatalf("baseline-limit combined: want exit 2, got %d", code)
	}
	if code := runCmd([]string{"--agent", "codex", "--codex-native-compaction", "--codex-compact-limit", "55000", "--modes", "on", "--tasks", "compact-facts"}); code != 2 {
		t.Fatalf("modes != off,on: want exit 2, got %d", code)
	}
}
