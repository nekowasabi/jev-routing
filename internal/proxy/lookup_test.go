package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestLocalLookupStripsToolsAndInjectsDefinitions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "proxy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package proxy\n\nfunc RewriteWith() {}\nfunc extractTools() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "rewrite.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JEV_LOOKUP_ROOT", root)
	prompt := "Where are RewriteWith and extractTools defined?"
	raw := chatReq(prompt, []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "Skill", "description": "run a skill"}},
		map[string]any{"type": "function", "function": map[string]any{"name": "mcp_search", "description": "mcp"}},
		map[string]any{"type": "function", "function": map[string]any{"name": "Bash", "description": "shell"}},
	})
	// Claude keeps its catalog: a stripped tools[] would rebuild its prompt cache.
	if _, stats, _ := RewriteWith(t.Context(), raw, host.Claude, nil, steerOpt()); stats.Reason == reasonLocalLookup {
		t.Fatalf("Claude took the local lookup: %+v", stats)
	}
	out, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, steerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonLocalLookup || stats.Apply != applyFilter || !stats.Changed {
		t.Fatalf("stats %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("tools still present: %v", got["tools"])
	}
	msgs := got["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, "internal/proxy/rewrite.go:3") || !strings.Contains(content, "internal/proxy/rewrite.go:4") {
		t.Fatalf("note %q", content)
	}
}

func TestLocalLookupKeepsToolsForRequiredSequence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "proxy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rewrite.go"), []byte("package proxy\nfunc RewriteWith() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JEV_LOOKUP_ROOT", root)
	raw := chatReq("Find RewriteWith. Search and then read its definition in separate sequential tool calls. Write answer.json.", workTools())
	_, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, steerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason == reasonLocalLookup || stats.ToolAfter == 0 {
		t.Fatalf("required tool sequence was bypassed: %+v", stats)
	}
}

func TestLocalLookupLeavesOpenTasksAlone(t *testing.T) {
	t.Setenv("JEV_LOOKUP_ROOT", t.TempDir())
	raw := chatReq("fix the failing test", workTools())
	out, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, steerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason == reasonLocalLookup {
		t.Fatalf("open task was treated as a lookup: %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tools"]; !ok && stats.Changed {
		t.Fatal("open task lost its catalog")
	}
}
