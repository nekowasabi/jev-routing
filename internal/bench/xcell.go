package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var xcellDefinitions = map[string]string{
	"RewriteWith":            "internal/proxy/rewrite.go",
	"extractTools":           "internal/proxy/rewrite.go",
	"applyCompactToMessages": "internal/proxy/rewrite.go",
	"DefaultOptions":         "internal/proxy/options.go",
	"DefaultUpstream":        "internal/proxy/proxy.go",
}

func xcellSourceRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if raw, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.HasPrefix(string(raw), "module github.com/nekowasabi/jev-routing\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("x-cell tasks require the jev-routing source tree")
		}
		dir = parent
	}
}

func xcellTask(id string) Task {
	files := []string{"go.mod"}
	prompt := "Read go.mod. Write answer.json with exactly the module, go, and direct_requires keys; direct_requires is the count of require entries without // indirect. Do not change other files."
	title := "Read module facts"
	if id == "xcell-locate" {
		files = append(files, "internal/proxy/rewrite.go", "internal/proxy/options.go", "internal/proxy/proxy.go")
		prompt = "Find the definitions of RewriteWith, extractTools, applyCompactToMessages, DefaultOptions, and DefaultUpstream. For each name, make one separate search tool call for that name, wait for its result, then make one separate read tool call for its definition. Use at least ten distinct sequential tool calls; do not combine names in one search. Write answer.json with exactly those five names as keys and repo-relative path:definition-line as values. Do not change other files."
		title = "Locate five source definitions"
	}
	return Task{
		ID: id, Title: title, TimeoutMinutes: 10, Prompt: prompt,
		Setup: func(workspace string) error {
			root, err := xcellSourceRoot()
			if err != nil {
				return err
			}
			for _, name := range files {
				raw, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					return err
				}
				if err := writeFiles(workspace, map[string]string{name: string(raw)}); err != nil {
					return err
				}
				if err := writeFiles(filepath.Join(filepath.Dir(workspace), "xcell-source"), map[string]string{name: string(raw)}); err != nil {
					return err
				}
			}
			return nil
		},
		Verify: func(workspace string) (verdict, error) {
			return verifyXCell(id, workspace, files)
		},
		Reference: func(workspace string) error {
			want, err := xcellExpected(id, workspace)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(want)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(workspace, "answer.json"), append(raw, '\n'), 0o644)
		},
	}
}

func verifyXCell(id, workspace string, files []string) (verdict, error) {
	for _, name := range files {
		original, err := os.ReadFile(filepath.Join(filepath.Dir(workspace), "xcell-source", name))
		if err != nil {
			return verdict{}, err
		}
		actual, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil || string(actual) != string(original) {
			return verdict{Total: 1, Failed: []string{"source_changed"}}, nil
		}
	}
	want, err := xcellExpected(id, workspace)
	if err != nil {
		return verdict{}, err
	}
	return verifyAnswer(workspace, want)
}

func xcellExpected(id, workspace string) (map[string]any, error) {
	if id == "xcell-locate" {
		out := map[string]any{}
		for symbol, name := range xcellDefinitions {
			raw, err := os.ReadFile(filepath.Join(workspace, name))
			if err != nil {
				return nil, err
			}
			match := regexp.MustCompile(`^func ` + regexp.QuoteMeta(symbol) + `\(`)
			found := false
			for i, line := range strings.Split(string(raw), "\n") {
				if match.MatchString(line) {
					out[symbol] = fmt.Sprintf("%s:%d", name, i+1)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("definition %s missing", symbol)
			}
		}
		return out, nil
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod, goVersion, direct, block := "", "", 0, false
	for _, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "module "):
			mod = strings.Fields(line)[1]
		case strings.HasPrefix(line, "go "):
			goVersion = strings.Fields(line)[1]
		case line == "require (":
			block = true
		case block && line == ")":
			block = false
		case block && line != "" && !strings.HasPrefix(line, "//") && !strings.Contains(line, "// indirect"):
			direct++
		case strings.HasPrefix(line, "require ") && !strings.Contains(line, "// indirect"):
			direct++
		}
	}
	if mod == "" || goVersion == "" {
		return nil, fmt.Errorf("go.mod missing module or go")
	}
	return map[string]any{"module": mod, "go": goVersion, "direct_requires": float64(direct)}, nil
}

// xcellLocateEvidence requires ten distinct, completed host tool calls: a
// search followed by a read for each definition. A correct answer alone does
// not prove that the requested tool sequence happened.
func xcellLocateEvidence(agent, agentLog, workspace string) bool {
	raw, err := os.ReadFile(agentLog)
	if err != nil {
		return false
	}
	var calls []xcellCall
	switch agent {
	case "claude":
		calls = claudeXCellCalls(raw)
	case "codex":
		calls = codexXCellCalls(raw)
	default:
		// The Grok final JSON and Devin final text do not carry tool IDs,
		// arguments, results and order. Their proxy observations omit targets.
		return false
	}
	used := map[string]bool{}
	for symbol, path := range xcellDefinitions {
		search, read := -1, -1
		for i, call := range calls {
			if used[call.id] || !call.ok || call.duplicate || !strings.Contains(call.result, "func "+symbol+"(") {
				continue
			}
			if search < 0 && xcellSearch(call, symbol) && xcellSearchTargets(call, path, workspace) {
				search = i
				continue
			}
			if search >= 0 && i > search && call.start > calls[search].finish && xcellRead(call) && xcellReadTargets(call, path, workspace) {
				read = i
				break
			}
		}
		if search < 0 || read < 0 || calls[search].id == calls[read].id {
			return false
		}
		used[calls[search].id], used[calls[read].id] = true, true
	}
	return len(used) == 2*len(xcellDefinitions)
}

type xcellCall struct {
	id, tool, args, result string
	ok                     bool
	start, finish          int
	duplicate              bool
}

func claudeXCellCalls(raw []byte) []xcellCall {
	var calls []xcellCall
	seen := map[string]int{}
	order := 0
	for _, line := range strings.Split(string(raw), "\n") {
		order++
		var event struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					Name      string          `json:"name"`
					ID        string          `json:"id"`
					ToolUseID string          `json:"tool_use_id"`
					Input     json.RawMessage `json:"input"`
					Content   string          `json:"content"`
					IsError   bool            `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		for _, block := range event.Message.Content {
			switch {
			case event.Type == "assistant" && block.Type == "tool_use" && block.ID != "":
				if i, duplicate := seen[block.ID]; duplicate {
					calls[i].duplicate = true
					continue
				}
				seen[block.ID] = len(calls)
				calls = append(calls, xcellCall{id: block.ID, tool: block.Name, args: string(block.Input), start: order})
			case event.Type == "user" && block.Type == "tool_result" && block.ToolUseID != "":
				if i, ok := seen[block.ToolUseID]; ok {
					if calls[i].finish != 0 {
						calls[i].duplicate = true
					} else {
						calls[i].ok, calls[i].result, calls[i].finish = !block.IsError, block.Content, order
					}
				}
			}
		}
	}
	return calls
}

func codexXCellCalls(raw []byte) []xcellCall {
	var calls []xcellCall
	seen := map[string]int{}
	order := 0
	for _, line := range strings.Split(string(raw), "\n") {
		order++
		var event struct {
			Type string `json:"type"`
			Item struct {
				ID               string `json:"id"`
				Type             string `json:"type"`
				Command          string `json:"command"`
				AggregatedOutput string `json:"aggregated_output"`
				Status           string `json:"status"`
				ExitCode         *int   `json:"exit_code"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Item.ID == "" || event.Item.Type != "command_execution" {
			continue
		}
		switch event.Type {
		case "item.started":
			if i, duplicate := seen[event.Item.ID]; duplicate {
				calls[i].duplicate = true
				continue
			}
			seen[event.Item.ID] = len(calls)
			calls = append(calls, xcellCall{id: event.Item.ID, tool: "shell", args: event.Item.Command, start: order})
		case "item.completed":
			if i, ok := seen[event.Item.ID]; ok {
				if calls[i].finish != 0 {
					calls[i].duplicate = true
				} else {
					calls[i].ok = event.Item.ExitCode != nil && *event.Item.ExitCode == 0 && event.Item.Status == "completed" && calls[i].args == event.Item.Command
					calls[i].result, calls[i].finish = event.Item.AggregatedOutput, order
				}
			}
		}
	}
	return calls
}

func xcellSearchTargets(call xcellCall, path, workspace string) bool {
	var args map[string]any
	_ = json.Unmarshal([]byte(call.args), &args)
	if call.tool == "Grep" {
		file, _ := args["path"].(string)
		return file == path || filepath.Clean(file) == filepath.Join(workspace, path) || strings.Contains(call.result, path)
	}
	return xcellSafeShell(call.args) && (strings.Contains(call.args, path) || strings.Contains(call.result, path))
}

func xcellReadTargets(call xcellCall, path, workspace string) bool {
	if call.tool == "Read" {
		var args struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(call.args), &args) != nil {
			return false
		}
		if args.FilePath == path || filepath.Clean(args.FilePath) == filepath.Join(workspace, path) {
			return true
		}
		// Why: macOS reports the same temporary workspace through /var and /private/var.
		got, gotErr := os.Stat(args.FilePath)
		want, wantErr := os.Stat(filepath.Join(workspace, path))
		return gotErr == nil && wantErr == nil && os.SameFile(got, want)
	}
	return xcellSafeShell(call.args) && strings.Contains(call.args, path)
}

func xcellSafeShell(args string) bool {
	command := xcellCommand(args)
	return !strings.ContainsAny(command, ";|&<>`\n\r") && !strings.Contains(command, "$(")
}

func xcellSearch(call xcellCall, symbol string) bool {
	if call.tool == "Grep" {
		var args struct {
			Pattern string `json:"pattern"`
		}
		return json.Unmarshal([]byte(call.args), &args) == nil && strings.Contains(args.Pattern, symbol)
	}
	return (call.tool == "Bash" || call.tool == "shell") && xcellShellVerb(call.args, "rg", "grep", "git grep") && strings.Contains(call.args, symbol)
}

func xcellRead(call xcellCall) bool {
	if call.tool == "Read" {
		return true
	}
	return (call.tool == "Bash" || call.tool == "shell") && xcellShellVerb(call.args, "sed", "cat", "head", "tail")
}

func xcellShellVerb(raw string, verbs ...string) bool {
	raw = xcellCommand(raw)
	for _, prefix := range []string{"/bin/zsh -lc ", "/bin/bash -lc ", "/bin/sh -lc "} {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.Trim(raw[len(prefix):], "'\"")
			break
		}
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "rtk "))
	for _, verb := range verbs {
		if strings.HasPrefix(raw, verb+" ") {
			return true
		}
	}
	return false
}

func xcellCommand(raw string) string {
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(raw), &args) == nil && args.Command != "" {
		return args.Command
	}
	return raw
}
