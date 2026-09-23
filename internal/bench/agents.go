package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
)

type agentCmd struct {
	File string
	Args []string
	Env  []string
	Dir  string
}

func agentCommand(agent, listen, workspace, prompt, model string, userTools bool) (agentCmd, error) {
	switch agent {
	case "codex":
		var rest []string
		if !userTools {
			rest = append(rest, "-c", "mcp_servers={}", "-c", "plugins={}")
		}
		rest = append(rest, "exec", "--ephemeral")
		if model != "" {
			rest = append(rest, "-m", model)
		}
		rest = append(rest, "--skip-git-repo-check", "--sandbox", "workspace-write", "-C", workspace, prompt)
		return agentCmd{File: "codex", Args: host.CommandArgs(host.Codex, listen, rest), Env: host.ChildEnv(host.Codex, listen), Dir: workspace}, nil
	case "claude":
		args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose", "--no-session-persistence"}
		if model != "" {
			args = append(args, "--model", model)
		}
		if !userTools {
			args = append(args, "--strict-mcp-config", "--setting-sources", "", "--disable-slash-commands", "--tools", "Bash,Edit,Write,Read,Glob,Grep")
		}
		args = append(args, "--permission-mode", "acceptEdits",
			"--allowedTools", "Read", "Edit", "Write", "Glob", "Grep", "Bash(node:*)", "Bash(npm test:*)", "Bash(npm run:*)", "Bash(ls:*)", "Bash(cat:*)", "Bash(git diff:*)", "Bash(git status:*)")
		return agentCmd{File: "claude", Args: args, Env: host.ChildEnv(host.Claude, listen), Dir: workspace}, nil
	case "grok":
		args := []string{"--single", prompt, "--output-format", "json", "--no-plan", "--no-subagents", "--permission-mode", "bypassPermissions"}
		if model != "" {
			args = append(args, "--model", model)
		}
		return agentCmd{File: "grok", Args: args, Env: host.ChildEnv(host.Grok, listen), Dir: workspace}, nil
	case "devin":
		args := []string{"--permission-mode", "dangerous", "--respect-workspace-trust", "false", "-p", "--", prompt}
		return agentCmd{File: "devin", Args: args, Env: host.ChildEnv(host.Devin, listen), Dir: workspace}, nil
	case "fake":
		return agentCmd{}, nil
	default:
		return agentCmd{}, fmt.Errorf("unknown agent %q (codex|claude|grok|devin|fake)", agent)
	}
}

// runFakeAgent sends a few Responses API turns, then writes the reference solution.
// It exercises metering without an LLM or quota.
func runFakeAgent(origin, workspace, taskID string, log io.Writer) error {
	tools := []any{
		map[string]any{"type": "function", "name": "exec_command", "description": "Run a shell command.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"cmd": map[string]string{"type": "string"}}}},
		map[string]any{"type": "custom", "name": "apply_patch", "description": "Edit files by applying a patch."},
	}
	input := []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Fix the failing tests."}}}}
	client := &http.Client{Timeout: 30 * time.Second}
	for turn := 0; turn < 4; turn++ {
		body, _ := json.Marshal(map[string]any{"model": "fake-model", "stream": true, "input": input, "tools": tools, "tool_choice": "auto"})
		res, err := client.Post(strings.TrimRight(origin, "/")+"/v1/responses", "application/json", strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode >= 400 {
			return fmt.Errorf("fake upstream status %d", res.StatusCode)
		}
		input = append(input,
			map[string]any{"type": "function_call", "name": "exec_command", "arguments": `{"cmd":"npm test"}`, "call_id": fmt.Sprintf("call_%d", turn)},
			map[string]any{"type": "function_call_output", "call_id": fmt.Sprintf("call_%d", turn), "output": map[bool]string{true: "all passing", false: "1 failing"}[turn == 3]},
		)
	}
	source := EngineWithoutSan()
	if taskID == "chess-san" {
		source = Solution()
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "chess.js"), []byte(source), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(log, "fake agent wrote the reference solution")
	return nil
}
