package bench

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPartitionHostRequestsSeparatesSameModelChild(t *testing.T) {
	log := filepath.Join(t.TempDir(), "agent.log")
	cli := `{"type":"result","usage":{"input_tokens":6,"cache_read_input_tokens":42057,"cache_creation_input_tokens":21617,"output_tokens":542}}`
	if err := os.WriteFile(log, []byte(cli+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := []byte(`{"events":[
		{"seq":1,"usage":{"inputTokens":2,"cachedTokens":0,"cacheWriteTokens":20735,"outputTokens":348}},
		{"seq":2,"usage":{"inputTokens":2,"cachedTokens":0,"cacheWriteTokens":8828,"outputTokens":104}},
		{"seq":3,"usage":{"inputTokens":2,"cachedTokens":8828,"cacheWriteTokens":157,"outputTokens":3}},
		{"seq":4,"usage":{"inputTokens":2,"cachedTokens":20735,"cacheWriteTokens":587,"outputTokens":169}},
		{"seq":5,"usage":{"inputTokens":2,"cachedTokens":21322,"cacheWriteTokens":295,"outputTokens":25}}
	]}`)
	parent, child, tokens, err := partitionHostRequests("claude", log, snapshot)
	if err != nil || !reflect.DeepEqual(parent, []int64{1, 4, 5}) || !reflect.DeepEqual(child, []int64{2, 3}) || tokens != 17924 {
		t.Fatalf("parent=%v child=%v tokens=%d err=%v", parent, child, tokens, err)
	}
}

func TestPartitionHostRequestsRejectsAmbiguousAttribution(t *testing.T) {
	log := filepath.Join(t.TempDir(), "agent.log")
	cli := `{"type":"result","usage":{"input_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}}`
	if err := os.WriteFile(log, []byte(cli+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := []byte(`{"events":[{"seq":1,"usage":{"inputTokens":1,"outputTokens":1}},{"seq":2,"usage":{"inputTokens":1,"outputTokens":1}}]}`)
	if _, _, _, err := partitionHostRequests("claude", log, snapshot); err == nil {
		t.Fatal("ambiguous parent-child attribution accepted")
	}
}
