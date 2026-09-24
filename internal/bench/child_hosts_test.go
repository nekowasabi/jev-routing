package bench

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPartitionCodexRequestsUsingParentCLIUsage(t *testing.T) {
	log := filepath.Join(t.TempDir(), "agent.log")
	// The root CLI turn reports its own usage; the proxy also sees the child.
	if err := os.WriteFile(log, []byte(`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := []byte(`{"events":[
		{"seq":1,"usage":{"inputTokens":45,"cachedTokens":20,"outputTokens":4}},
		{"seq":2,"usage":{"inputTokens":20,"cachedTokens":4,"outputTokens":3},"jevAttempts":[{"inputTokens":4,"outputTokens":1}]},
		{"seq":3,"usage":{"inputTokens":55,"cachedTokens":20,"outputTokens":6}}
	]}`)
	parent, child, tokens, err := partitionHostRequests("codex", log, snapshot)
	if err != nil || !reflect.DeepEqual(parent, []int64{1, 3}) || !reflect.DeepEqual(child, []int64{2}) || tokens != 28 {
		t.Fatalf("parent=%v child=%v tokens=%d err=%v", parent, child, tokens, err)
	}
}

func TestCodexChildAttributionRequiresIndependentChildUsage(t *testing.T) {
	log := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(log, []byte(`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	applications := `"applications":[{"kind":"subagent","capabilityId":"subagent:host:spawn_agent@obs","state":"verified","callId":"call_1","hasResult":true}]`
	// A paired tool result does not mean the child launched successfully.
	failedLaunch := []byte(`{` + applications + `,"events":[{"seq":1,"usage":{"inputTokens":45,"cachedTokens":20,"outputTokens":4}},{"seq":2,"usage":{"inputTokens":55,"cachedTokens":20,"outputTokens":6}}]}`)
	if _, _, _, err := codexChildAttribution(log, failedLaunch); err == nil {
		t.Fatal("accepted paired error result without a child request")
	}
	success := []byte(`{` + applications + `,"events":[{"seq":1,"usage":{"inputTokens":45,"cachedTokens":20,"outputTokens":4}},{"seq":2,"usage":{"inputTokens":20,"cachedTokens":4,"outputTokens":3}},{"seq":3,"usage":{"inputTokens":55,"cachedTokens":20,"outputTokens":6}}]}`)
	parent, child, tokens, err := codexChildAttribution(log, success)
	if err != nil || !reflect.DeepEqual(parent, []int64{1, 3}) || !reflect.DeepEqual(child, []int64{2}) || tokens != 23 {
		t.Fatalf("parent=%v child=%v tokens=%d err=%v", parent, child, tokens, err)
	}
}

func TestGrokChildAttributionUsesUniqueParentModelUsage(t *testing.T) {
	log := filepath.Join(t.TempDir(), "agent.log")
	host := `{"usage":{"input_tokens":60,"output_tokens":10,"cache_read_input_tokens":40,"cache_creation_input_tokens":0},"modelUsage":{"grok-4.7-build":{"inputTokens":60,"outputTokens":10,"cacheReadInputTokens":40,"cacheCreationInputTokens":0,"modelCalls":2}}}`
	if err := os.WriteFile(log, []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
	base := `{"applications":[{"kind":"subagent","capabilityId":"subagent:host:spawn_subagent@obs","state":"verified","callId":"call_1","hasResult":true},{"kind":"subagent","capabilityId":"subagent:host:get_command_or_subagent_output@obs","state":"verified","callId":"call_2","hasResult":true}],"events":[{"seq":1,"sentModel":"grok-4.7-build","usage":{"inputTokens":45,"cachedTokens":20,"outputTokens":4}},{"seq":2,"sentModel":"grok-4.7-build","usage":{"inputTokens":20,"cachedTokens":4,"outputTokens":3}},{"seq":3,"sentModel":"grok-4.7-build","usage":{"inputTokens":55,"cachedTokens":20,"outputTokens":6}}]}`
	parent, child, tokens, err := grokChildAttribution(log, []byte(base))
	if err != nil || !reflect.DeepEqual(parent, []int64{1, 3}) || !reflect.DeepEqual(child, []int64{2}) || tokens != 23 {
		t.Fatalf("parent=%v child=%v tokens=%d err=%v", parent, child, tokens, err)
	}
	// A canceled inference with no usage cannot be silently assigned to a side.
	canceled := []byte(`{"applications":[{"kind":"subagent","capabilityId":"subagent:host:spawn_subagent@obs","state":"verified","callId":"call_1","hasResult":true}],"events":[{"seq":1,"sentModel":"grok-4.7-build","usage":{"inputTokens":45,"cachedTokens":20,"outputTokens":4}},{"seq":2,"sentModel":"grok-4.7-build","usage":{"inputTokens":20,"cachedTokens":4,"outputTokens":3}},{"seq":3,"sentModel":"grok-4.7-build","usage":{"inputTokens":55,"cachedTokens":20,"outputTokens":6}},{"seq":4,"sentModel":"grok-4.7-build","reason":"baseline","canceled":true}]}`)
	if _, _, _, err := grokChildAttribution(log, canceled); err == nil {
		t.Fatal("accepted canceled inference without attributable usage")
	}
}

func TestChildLaunchExcludesResultRetrieval(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"subagent:host:Agent@obs", true},
		{"subagent:host:spawn_agent@obs", true},
		{"subagent:host:spawn_subagent@obs", true},
		{"subagent:host:run_subagent@obs", true},
		{"subagent:host:read_subagent@obs", false},
		{"subagent:host:get_command_or_subagent_output@obs", false},
	} {
		if got := isChildLaunch(tc.id); got != tc.want {
			t.Errorf("%s: got %t want %t", tc.id, got, tc.want)
		}
	}
}
