package bench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexDualFactEvidenceRequiresTwoCompletedResults(t *testing.T) {
	left := `{"type":"item.completed","item":{"id":"item_1","type":"mcp_tool_call","server":"bench","tool":"bench_left_fact","status":"completed","error":null,"result":{"content":[{"type":"text","text":"left=17"}]}}}`
	right := `{"type":"item.completed","item":{"id":"item_2","type":"mcp_tool_call","server":"bench","tool":"bench_right_fact","status":"completed","error":null,"result":{"content":[{"type":"text","text":"right=23"}]}}}`
	path := filepath.Join(t.TempDir(), "agent.log")
	for _, tc := range []struct {
		name, lines string
		want        bool
	}{
		{"both", left + "\n" + right, true},
		{"one", left, false},
		{"duplicate id", left + "\n" + `{"type":"item.completed","item":{"id":"item_1","type":"mcp_tool_call","server":"bench","tool":"bench_right_fact","status":"completed","error":null,"result":{"content":[{"type":"text","text":"right=23"}]}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.lines+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := codexDualFactEvidence(path); got != tc.want {
				t.Fatalf("got %t want %t", got, tc.want)
			}
		})
	}
}
