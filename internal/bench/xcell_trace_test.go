package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestXCellLocateEvidenceRequiresTenOrderedCalls(t *testing.T) {
	workspace := t.TempDir()
	names := make([]string, 0, len(xcellDefinitions))
	for name := range xcellDefinitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			good := xcellTrace(agent, workspace, names, "")
			for _, tc := range []struct {
				name, trace string
				want        bool
			}{
				{"ordered ten calls", good, true},
				{"repository search then targeted read", xcellTrace(agent, workspace, names, "broad_search"), true},
				{"missing read", xcellTrace(agent, workspace, names, "missing_read"), false},
				{"wrong target", xcellTrace(agent, workspace, names, "wrong_target"), false},
				{"read before search result", xcellTrace(agent, workspace, names, "overlap"), false},
				{"one compound shell", xcellTrace(agent, workspace, names, "compound"), false},
				{"duplicate call id", xcellTrace(agent, workspace, names, "duplicate"), false},
				{"failed search", xcellTrace(agent, workspace, names, "failed_search"), false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					log := filepath.Join(t.TempDir(), "agent.log")
					if err := os.WriteFile(log, []byte(tc.trace), 0o600); err != nil {
						t.Fatal(err)
					}
					if got := xcellLocateEvidence(agent, log, workspace); got != tc.want {
						t.Fatalf("evidence=%v, want %v", got, tc.want)
					}
				})
			}
		})
	}
	for _, agent := range []string{"grok", "devin"} {
		log := filepath.Join(t.TempDir(), "agent.log")
		_ = os.WriteFile(log, []byte(`{"text":"done"}`), 0o600)
		if xcellLocateEvidence(agent, log, workspace) {
			t.Fatalf("%s final answer is not tool sequence evidence", agent)
		}
	}
}

func TestXCellReadTargetsSymlinkedWorkspace(t *testing.T) {
	workspace := t.TempDir()
	path := "internal/proxy/rewrite.go"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, path)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, path), []byte("func RewriteWith() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"file_path": filepath.Join(alias, path)})
	if !xcellReadTargets(xcellCall{tool: "Read", args: string(args)}, path, workspace) {
		t.Fatal("symlinked path to the same workspace file rejected")
	}
}

func xcellTrace(agent, workspace string, names []string, fault string) string {
	var lines []string
	add := func(v any) { raw, _ := json.Marshal(v); lines = append(lines, string(raw)) }
	for i, name := range names {
		path := filepath.Join(workspace, xcellDefinitions[name])
		if i == 0 && fault == "wrong_target" {
			path = filepath.Join(workspace, "other.go")
		}
		searchID, readID := fmt.Sprintf("search_%d", i), fmt.Sprintf("read_%d", i)
		definition := "func " + name + "("
		searchCommand := fmt.Sprintf("rg -n 'func %s(' %s", name, path)
		searchPath, searchResult := path, definition
		if i == 0 && fault == "broad_search" {
			searchPath, searchResult = workspace, xcellDefinitions[name]+":1:"+definition
			searchCommand = fmt.Sprintf("rg -n 'func %s(' %s", name, workspace)
		}
		readCommand := fmt.Sprintf("sed -n '1,999p' %s", path)
		if i == 0 && fault == "compound" {
			searchCommand += " && " + readCommand
		}
		if agent == "claude" {
			if i == 0 && fault == "compound" {
				add(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": searchID, "name": "Bash", "input": map[string]any{"command": searchCommand}}}}})
				add(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": searchID, "content": definition}}}})
				continue
			}
			add(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": searchID, "name": "Grep", "input": map[string]any{"pattern": name, "path": searchPath}}}}})
			if i == 0 && fault == "overlap" {
				add(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": readID, "name": "Read", "input": map[string]any{"file_path": path}}}}})
			}
			result := map[string]any{"type": "tool_result", "tool_use_id": searchID, "content": definition}
			if i == 0 && fault == "failed_search" {
				result["is_error"] = true
			}
			result["content"] = searchResult
			add(map[string]any{"type": "user", "message": map[string]any{"content": []any{result}}})
			if i == 0 && fault == "duplicate" {
				add(map[string]any{"type": "user", "message": map[string]any{"content": []any{result}}})
			}
			if !(i == 0 && fault == "overlap") {
				add(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": readID, "name": "Read", "input": map[string]any{"file_path": path}}}}})
			}
			if !(i == 0 && fault == "missing_read") {
				add(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": readID, "content": definition}}}})
			}
		} else {
			for _, call := range []struct{ id, command string }{{searchID, searchCommand}, {readID, readCommand}} {
				if i == 0 && fault == "compound" && call.id == readID {
					continue
				}
				add(map[string]any{"type": "item.started", "item": map[string]any{"id": call.id, "type": "command_execution", "command": call.command, "status": "in_progress"}})
				if i == 0 && fault == "overlap" && call.id == searchID {
					continue
				}
				if i == 0 && fault == "missing_read" && call.id == readID {
					continue
				}
				status, exitCode := "completed", 0
				if i == 0 && fault == "failed_search" && call.id == searchID {
					status, exitCode = "failed", 1
				}
				output := definition
				if call.id == searchID {
					output = searchResult
				}
				completed := map[string]any{"type": "item.completed", "item": map[string]any{"id": call.id, "type": "command_execution", "command": call.command, "status": status, "exit_code": exitCode, "aggregated_output": output}}
				add(completed)
				if i == 0 && fault == "duplicate" && call.id == searchID {
					add(completed)
				}
			}
			if i == 0 && fault == "overlap" {
				add(map[string]any{"type": "item.completed", "item": map[string]any{"id": searchID, "type": "command_execution", "command": searchCommand, "status": "completed", "exit_code": 0, "aggregated_output": definition}})
			}
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
