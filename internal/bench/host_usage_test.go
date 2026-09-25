package bench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyHostUsageReconcilesMainModel(t *testing.T) {
	for _, tc := range []struct {
		agent, model, line string
		usage              ModelUsage
	}{
		{"claude", "claude-sonnet-5", `{"type":"result","usage":{"input_tokens":2,"cache_read_input_tokens":30,"cache_creation_input_tokens":4,"output_tokens":5}}`, ModelUsage{Input: 2, Cached: 30, CacheWrite: 4, Output: 5}},
		{"codex", "gpt-5.6-terra", `{"type":"turn.completed","usage":{"input_tokens":40,"cached_input_tokens":20,"output_tokens":6}}`, ModelUsage{Input: 40, Cached: 20, Output: 6}},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.log")
			if err := os.WriteFile(path, []byte(tc.line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			run := RunRecord{Agent: tc.agent, AgentModel: tc.model, ModelUsage: map[string]ModelUsage{tc.model: tc.usage, "codex-auto-review": {Input: 20, Output: 2}}}
			if err := verifyHostUsage(run, path); err != nil {
				t.Fatal(err)
			}
			run.ModelUsage[tc.model] = ModelUsage{Input: tc.usage.Input + 1, Cached: tc.usage.Cached, CacheWrite: tc.usage.CacheWrite, Output: tc.usage.Output}
			if err := verifyHostUsage(run, path); err == nil {
				t.Fatal("mismatch accepted")
			}
		})
	}
}

// TestMatchHostSessionCorrectsCodexCompactApparentUsage covers docs/MEMO.md
// "計測上の教訓": Codex CLI folds the apparent usage of a Jev-synthesized
// compaction reply into its turn.completed total, even though the proxy
// never sent that request upstream, so the raw proxy/CLI totals mismatch.
// Adding CompactApparentInput/Output back to the proxy side must fix it.
func TestMatchHostSessionCorrectsCodexCompactApparentUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	// The CLI's turn total already includes the synthesized reply's apparent
	// usage (input_tokens=1, output_tokens=42 -- native_compact.go
	// compactUsage) on top of one real upstream call (40/6).
	line := `{"type":"turn.completed","usage":{"input_tokens":41,"cached_input_tokens":20,"output_tokens":48}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{
		Agent: "codex", AgentModel: "gpt-5.6-terra",
		ModelUsage: map[string]ModelUsage{"gpt-5.6-terra": {Input: 40, Cached: 20, Output: 6}},
	}
	if err := verifyHostUsage(run, path); err == nil {
		t.Fatal("uncorrected mismatch must be rejected")
	}
	run.CompactApparentInput = 1
	run.CompactApparentOutput = 42
	if err := verifyHostUsage(run, path); err != nil {
		t.Fatalf("corrected reconciliation still failed: %v", err)
	}
}

func TestMatchHostSessionFindsParentAmongSameModelChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	line := `{"type":"result","usage":{"input_tokens":2,"cache_read_input_tokens":30,"cache_creation_input_tokens":4,"output_tokens":5}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{
		Agent: "claude", AgentModel: "claude-sonnet-5",
		ModelUsage: map[string]ModelUsage{"claude-sonnet-5": {Input: 9, Cached: 40, CacheWrite: 4, Output: 8}},
		SessionUsage: map[string]SessionUsage{
			"parent": {Input: 2, Cached: 30, CacheWrite: 4, Output: 5},
			"child":  {Input: 7, Cached: 10, Output: 3},
		},
	}
	key, err := matchHostSession(run, path)
	if err != nil || key != "parent" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}
