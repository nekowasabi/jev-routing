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
