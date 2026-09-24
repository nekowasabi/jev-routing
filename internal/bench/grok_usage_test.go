package bench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchGrokHostUsageSeparatesHelperModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	cli := `{"usage":{"input_tokens":60,"output_tokens":10,"cache_read_input_tokens":40,"cache_creation_input_tokens":0,"total_tokens":110},"modelUsage":{"grok-4.7-build":{"inputTokens":60,"outputTokens":10,"cacheReadInputTokens":40,"cacheCreationInputTokens":0,"modelCalls":2}}}`
	if err := os.WriteFile(path, []byte(cli), 0o644); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{ModelUsage: map[string]ModelUsage{
		"grok-4.7": {Requests: 2, Input: 100, Cached: 40, Output: 10},
		"grok-4.6": {Requests: 1, Input: 20, Cached: 2, Output: 3},
	}}
	model, err := matchGrokHostUsage(run, path)
	if err != nil || model != "grok-4.7" {
		t.Fatalf("model=%q err=%v", model, err)
	}
	run.ModelUsage["grok-4.7"] = ModelUsage{Requests: 3, Input: 100, Cached: 40, Output: 10}
	canceled := []byte(`{"events":[{"sentModel":"grok-4.7","upstreamFinish":"canceled","canceled":true,"usage":null}]}`)
	if model, err := matchGrokHostUsage(run, path, canceled); err != nil || model != "grok-4.7" {
		t.Fatalf("canceled request model=%q err=%v", model, err)
	}
	run.ModelUsage["grok-4.7"] = ModelUsage{Requests: 2, Input: 101, Cached: 40, Output: 10}
	if _, err := matchGrokHostUsage(run, path); err == nil {
		t.Fatal("mismatch accepted")
	}
}

func TestMatchGrokHostUsageSeparatesSameModelHelperByUniqueRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	cli := `{"usage":{"input_tokens":60,"output_tokens":10,"cache_read_input_tokens":40,"cache_creation_input_tokens":0,"total_tokens":110},"modelUsage":{"grok-4.6-build":{"inputTokens":60,"outputTokens":10,"cacheReadInputTokens":40,"cacheCreationInputTokens":0,"modelCalls":2}}}`
	if err := os.WriteFile(path, []byte(cli), 0o600); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{ModelUsage: map[string]ModelUsage{"grok-4.6": {Requests: 4, Input: 120, Cached: 42, Output: 13}}}
	snapshot := []byte(`{"events":[{"sentModel":"grok-4.6","usage":{"inputTokens":20,"cachedTokens":2,"outputTokens":3}},{"sentModel":"grok-4.6","usage":{"inputTokens":40,"cachedTokens":10,"outputTokens":4}},{"sentModel":"grok-4.6","usage":{"inputTokens":60,"cachedTokens":30,"outputTokens":6}},{"sentModel":"grok-4.6","canceled":true,"upstreamFinish":"canceled","usage":null}]}`)
	model, err := matchGrokHostUsage(run, path, snapshot)
	if err != nil || model != "grok-4.6" {
		t.Fatalf("model=%q err=%v", model, err)
	}
	if _, err := matchGrokHostUsage(run, path, []byte(`{"events":[{"sentModel":"grok-4.6","usage":{"inputTokens":40,"cachedTokens":10,"outputTokens":4}},{"sentModel":"grok-4.6","usage":{"inputTokens":60,"cachedTokens":30,"outputTokens":6}},{"sentModel":"grok-4.6","usage":{"inputTokens":60,"cachedTokens":30,"outputTokens":6}}]}`)); err == nil {
		t.Fatal("ambiguous request subset accepted")
	}
}

func TestMatchGrokHostUsageRejectsIncompleteSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	cli := `{"usage_is_incomplete":true,"usage":{"input_tokens":60,"output_tokens":10,"cache_read_input_tokens":40,"cache_creation_input_tokens":0,"total_tokens":110},"modelUsage":{"grok-4.7-build":{"inputTokens":60,"outputTokens":10,"cacheReadInputTokens":40,"modelCalls":1}}}`
	if err := os.WriteFile(path, []byte(cli), 0o600); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{ModelUsage: map[string]ModelUsage{
		"grok-4.7": {Requests: 2, Input: 100, Cached: 40, Output: 10},
	}}
	canceled := []byte(`{"events":[{"sentModel":"grok-4.7","upstreamFinish":"canceled","canceled":true,"usage":null}]}`)
	if model, err := matchGrokHostUsage(run, path, canceled); err == nil || model != "" {
		t.Fatalf("accepted incomplete snapshot: model=%q err=%v", model, err)
	}
}

func TestMatchGrokHostUsageRequiresReconciledPromptTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	run := RunRecord{ModelUsage: map[string]ModelUsage{
		"grok-4.7": {Requests: 1, Input: 100, Cached: 40, Output: 10},
	}}
	for _, cli := range []string{
		`{"modelUsage":{"grok-4.7-build":{"inputTokens":60,"outputTokens":10,"cacheReadInputTokens":40,"modelCalls":1}}}`,
		`{"usage":{"input_tokens":60,"output_tokens":10,"cache_read_input_tokens":40,"cache_creation_input_tokens":0,"total_tokens":111},"modelUsage":{"grok-4.7-build":{"inputTokens":60,"outputTokens":10,"cacheReadInputTokens":40,"modelCalls":1}}}`,
	} {
		if err := os.WriteFile(path, []byte(cli), 0o600); err != nil {
			t.Fatal(err)
		}
		if model, err := matchGrokHostUsage(run, path); err == nil || model != "" {
			t.Fatalf("accepted unverified prompt total: model=%q err=%v", model, err)
		}
	}
}
