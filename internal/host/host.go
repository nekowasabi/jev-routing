package host

import (
	"fmt"
	"os"
	"strings"
)

type ID string

const (
	Claude ID = "claude"
	Codex  ID = "codex"
	Grok   ID = "grok"
)

func Parse(s string) (ID, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "claude", "claude-code", "anthropic":
		return Claude, nil
	case "codex", "openai":
		return Codex, nil
	case "grok", "grok-build", "xai":
		return Grok, nil
	default:
		return "", fmt.Errorf("unknown host %q (claude|codex|grok)", s)
	}
}

func (h ID) Binary() string {
	switch h {
	case Claude:
		return "claude"
	case Codex:
		return "codex"
	default:
		return "grok"
	}
}

func (h ID) Label() string {
	switch h {
	case Claude:
		return "Claude Code"
	case Codex:
		return "Codex"
	default:
		return "Grok Build"
	}
}

// Native maps a Claude Code built-in onto the host's name. MCP plugin names stay.
func Native(h ID, claudeName string) string {
	if h == Claude {
		return claudeName
	}
	m := toCodex
	if h == Grok {
		m = toGrok
	}
	if v, ok := m[claudeName]; ok {
		return v
	}
	return claudeName
}

var toCodex = map[string]string{
	"Read": "read_file", "Edit": "apply_patch", "Write": "add_file",
	"Bash": "exec_command", "Glob": "list_dir", "Grep": "grep_files",
	"Agent": "spawn_agent", "Skill": "skill", "WebFetch": "web_fetch",
	"WebSearch": "web_search", "TodoWrite": "update_plan", "LSP": "lsp",
	"AskUserQuestion": "request_user_input", "EnterPlanMode": "switch_plan_mode",
	"ExitPlanMode": "propose_plan", "Monitor": "wait",
}

var toGrok = map[string]string{
	"Read": "read_file", "Edit": "search_replace", "Write": "search_replace",
	"Bash": "run_terminal_cmd", "Glob": "list_dir", "Grep": "grep",
	"Agent": "task", "Skill": "workflow", "WebFetch": "web_fetch",
	"WebSearch": "web_search", "TodoWrite": "todo_write", "LSP": "lsp",
	"AskUserQuestion": "ask_user_question", "EnterPlanMode": "enter_plan_mode",
	"ExitPlanMode": "exit_plan_mode", "Monitor": "monitor",
}

func ChildEnv(h ID, listen string) []string {
	env := os.Environ()
	drop := map[string]bool{}
	add := map[string]string{}
	switch h {
	case Claude:
		drop["ANTHROPIC_API_KEY"] = true
		drop["ANTHROPIC_AUTH_TOKEN"] = true
		add["ANTHROPIC_BASE_URL"] = "http://" + listen
	case Grok:
		drop["XAI_API_KEY"] = true
		drop["GROK_MODELS_BASE_URL"] = true
		add["GROK_CLI_CHAT_PROXY_BASE_URL"] = "http://" + listen + "/v1"
	case Codex:
		add["OPENAI_BASE_URL"] = "http://" + listen + "/v1"
		add["JEV_ROUTING_HOST"] = "codex"
	}
	out := make([]string, 0, len(env)+4)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] {
			continue
		}
		if _, ok := add[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range add {
		out = append(out, k+"="+v)
	}
	return out
}
