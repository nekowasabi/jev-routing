package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevinTranscriptIsIsolatedAndNumericOnly(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	cmd, err := agentCommand("devin", "", workspace, "read file", "gpt-5-6-terra-medium", "medium", false, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(filepath.Dir(workspace), "devin-transcript.json")
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--export "+wantPath) || !strings.Contains(args, "--model gpt-5-6-terra-medium") {
		t.Fatalf("Devin export/model arguments missing: %v", cmd.Args)
	}
	raw := `{"schema_version":"ATIF-v1.7","session_id":"session-private","agent":{"model_name":"gpt-5-6-terra-medium"},"steps":[{"source":"agent","message":"secret text","metrics":{"prompt_tokens":120,"completion_tokens":7,"cached_tokens":80}}],"final_metrics":{"total_prompt_tokens":120,"total_completion_tokens":7,"total_cached_tokens":80,"total_steps":1}}`
	if err := os.WriteFile(wantPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readDevinTranscript(wantPath)
	if err != nil || got.PromptTokens != 120 || got.CompletionTokens != 7 || got.CachedTokens != 80 || got.Steps != 1 || len(got.ModelNames) != 1 || got.ModelNames[0] != "gpt-5-6-terra-medium" {
		t.Fatalf("usage=%+v err=%v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), "session-private") || strings.Contains(string(encoded), "secret text") {
		t.Fatalf("transcript text leaked into metric record: %s", encoded)
	}
}

func TestDevinTranscriptRejectsIncompleteMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.json")
	for _, raw := range []string{
		`{`,
		`{"session_id":"","steps":[{}],"final_metrics":{"total_prompt_tokens":1,"total_completion_tokens":2,"total_cached_tokens":0,"total_steps":1}}`,
		`{"session_id":"s","steps":[],"final_metrics":{"total_prompt_tokens":1,"total_completion_tokens":2,"total_cached_tokens":0,"total_steps":1}}`,
		`{"session_id":"s","steps":[{}],"final_metrics":{"total_prompt_tokens":1,"total_completion_tokens":2,"total_steps":1}}`,
		`{"schema_version":"ATIF-v1.7","session_id":"s","steps":[{"source":"agent","metrics":{"prompt_tokens":10,"completion_tokens":2,"cached_tokens":1}}],"final_metrics":{"total_prompt_tokens":11,"total_completion_tokens":2,"total_cached_tokens":1,"total_steps":1}}`,
		`{"schema_version":"ATIF-v1.7","session_id":"s","steps":[{"source":"agent","metrics":{"prompt_tokens":10,"completion_tokens":2,"cached_tokens":1}}],"final_metrics":{"total_prompt_tokens":10,"total_completion_tokens":2,"total_cached_tokens":1,"total_steps":1}}`,
		`{"schema_version":"ATIF-v1.7","session_id":"s","steps":[{"source":"agent","metrics":{"prompt_tokens":10,"completion_tokens":2,"cached_tokens":1}}],"subagent_trajectories":[{}],"final_metrics":{"total_prompt_tokens":10,"total_completion_tokens":2,"total_cached_tokens":1,"total_steps":1}}`,
		`{"schema_version":"ATIF-v1.7","session_id":"s","usage_is_incomplete":true,"steps":[{"source":"agent","metrics":{"prompt_tokens":10,"completion_tokens":2,"cached_tokens":1}}],"final_metrics":{"total_prompt_tokens":10,"total_completion_tokens":2,"total_cached_tokens":1,"total_steps":1}}`,
		`{"schema_version":"ATIF-v1.7","session_id":"s","continued_trajectory_ref":"other.json","steps":[{"source":"agent","metrics":{"prompt_tokens":10,"completion_tokens":2,"cached_tokens":1}}],"final_metrics":{"total_prompt_tokens":10,"total_completion_tokens":2,"total_cached_tokens":1,"total_steps":1}}`,
		`{"schema_version":"ATIF-v1.7","session_id":"s","steps":[{"source":"agent","metrics":{"prompt_tokens":10,"completion_tokens":2,"cached_tokens":1},"observation":{"results":[{"subagent_trajectory_ref":[{"trajectory_path":"child.json"}]}]}}],"final_metrics":{"total_prompt_tokens":10,"total_completion_tokens":2,"total_cached_tokens":1,"total_steps":1}}`,
	} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := readDevinTranscript(path); err == nil || got != nil {
			t.Fatalf("accepted incomplete transcript: %+v", got)
		}
	}
}

func TestDevinTranscriptUsesStepModelOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.json")
	raw := `{"schema_version":"ATIF-v1.7","session_id":"s","agent":{"model_name":"main"},"steps":[{"source":"agent","metrics":{"prompt_tokens":10,"completion_tokens":2}},{"source":"agent","model_name":"helper","metrics":{"prompt_tokens":20,"completion_tokens":3,"cached_tokens":4}}],"final_metrics":{"total_prompt_tokens":30,"total_completion_tokens":5,"total_cached_tokens":4,"total_steps":2}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readDevinTranscript(path)
	if err != nil || len(got.ModelNames) != 2 || got.ModelNames[0] != "helper" || got.ModelNames[1] != "main" {
		t.Fatalf("models=%+v err=%v", got, err)
	}
}
