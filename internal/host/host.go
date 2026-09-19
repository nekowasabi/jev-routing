package host

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type ID string

const (
	Claude ID = "claude"
	Codex  ID = "codex"
	Grok   ID = "grok"
	Cursor ID = "cursor"
	Devin  ID = "devin"
)

func Parse(s string) (ID, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "claude", "claude-code", "anthropic":
		return Claude, nil
	case "codex", "openai":
		return Codex, nil
	case "grok", "grok-build", "xai":
		return Grok, nil
	case "cursor", "cursor-agent", "cursor-cli":
		return Cursor, nil
	case "devin", "devin-cli", "cognition":
		return Devin, nil
	default:
		return "", fmt.Errorf("unknown host %q (claude|codex|grok|cursor|devin)", s)
	}
}

func (h ID) Binary() string {
	switch h {
	case Claude:
		return "claude"
	case Codex:
		return "codex"
	case Cursor:
		return "cursor-agent"
	case Devin:
		return "devin"
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
	case Cursor:
		return "Cursor Agent CLI"
	case Devin:
		return "Devin CLI"
	default:
		return "Grok Build"
	}
}

// Native maps a Claude Code built-in onto the host's name. MCP plugin names stay.
func Native(h ID, claudeName string) string {
	if h == Claude {
		return claudeName
	}
	var m map[string]string
	switch h {
	case Codex:
		m = toCodex
	case Grok:
		m = toGrok
	case Cursor:
		m = toCursor
	case Devin:
		m = toDevin
	default:
		return claudeName
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
	// Why: Confirmed Grok catalog uses run_terminal_command / spawn_subagent.
	// Legacy run_terminal_cmd / task stay aliases in plan.aliasIn only.
	"Bash": "run_terminal_command", "Glob": "list_dir", "Grep": "grep",
	"Agent": "spawn_subagent", "Skill": "workflow", "WebFetch": "web_fetch",
	"WebSearch": "web_search", "TodoWrite": "todo_write", "LSP": "lsp",
	"AskUserQuestion": "ask_user_question", "EnterPlanMode": "enter_plan_mode",
	"ExitPlanMode": "exit_plan_mode", "Monitor": "monitor",
}

// Cursor catalog IDs from docs.cursor.com plus cursor-agent 2026.08.31 index.js.
var toCursor = map[string]string{
	// Why: Instead of Bash identity, adopted Shell. Reason: docs describe the shell tool; JS maps Bash:"Shell".
	"Bash": "Shell",
	// Why: Instead of lowercase task, adopted Task. Reason: cursor-agent Claude-compat table maps Agent:"Task" (PascalCase).
	"Agent": "Task",
	"Task":  "Task", // identity: catalog id is already Task
	// Why: Instead of leaving Edit identity, adopted Write. Reason: cursor-agent's own Claude-compat table maps Edit to Write; StrReplace is absent in this version.
	"Edit":            "Write",
	"TodoWrite":       "updateTodos", // Why: JS name is updateTodos, not TodoWrite.
	"AskUserQuestion": "askQuestion", // Why: JS identifier is askQuestion, not request_user_input.
	"EnterPlanMode":   "createPlan",  // Why: JS createPlanToolCall is the plan-spawn analog; ExitPlanMode not confirmed so omitted.
}

// Devin StableToolName values from the CLI binary plus docs.devin.ai/cli/subagents.md.
var toDevin = map[string]string{
	"Read":  "read",
	"Edit":  "edit",
	"Write": "write",
	// Why: Instead of shell_command, adopted exec. Reason: StableToolName enum is exec; shell_command is an internal type, not the model-facing name.
	"Bash": "exec",
	"Glob": "glob",
	"Grep": "grep",
	// Why: Instead of spawn_agent, adopted run_subagent. Reason: official subagent docs; parent also has read_subagent but Native(Agent) is run_subagent only.
	"Agent":           "run_subagent",
	"WebSearch":       "web_search",
	"WebFetch":        "webfetch", // Why: toolbox webfetch.rs / binary tool name webfetch, not WebFetch.
	"TodoWrite":       "todo_write",
	"AskUserQuestion": "ask_user_question",
	"EnterPlanMode":   "write_plan",
	"ExitPlanMode":    "exit_plan_mode",
}

func Advertise(h ID, listen string) string {
	if h != Cursor {
		return listen
	}
	// Why: Cursor treats only "localhost" as local; 127.0.0.1 is replaced by agentnUrl.
	hostpart, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	switch hostpart {
	case "127.0.0.1", "::1":
		return net.JoinHostPort("localhost", port)
	}
	return listen
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
	case Cursor:
		// Why: Instead of OPENAI_BASE_URL, adopted CURSOR_API_ENDPOINT plus CURSOR_API_BASE_URL.
		// Reason: cursor-agent --help documents CURSOR_API_ENDPOINT; index.js also reads CURSOR_API_BASE_URL.
		u := "http://" + Advertise(h, listen)
		add["CURSOR_API_ENDPOINT"] = u
		add["CURSOR_API_BASE_URL"] = u
		add["JEV_ROUTING_HOST"] = "cursor"
	case Devin:
		// Why: Instead of OPENAI_BASE_URL, adopted DEVIN_API_URL plus WINDSURF_API_SERVER_URL.
		// Reason: Devin CLI documents DEVIN_API_URL as the API base override; live auth status
		// sends SeatManagement to WINDSURF_API_SERVER_URL, not DEVIN_API_URL alone.
		add["DEVIN_API_URL"] = "http://" + listen
		add["WINDSURF_API_SERVER_URL"] = "http://" + listen
		add["JEV_ROUTING_HOST"] = "devin"
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

func ChildArgs(h ID, listen string) []string {
	if h == Cursor {
		// Why: Instead of env-only, adopted --endpoint flag. Reason: help documents it as the public override; env can be missed by subprocesses.
		return []string{"--endpoint", "http://" + Advertise(h, listen)}
	}
	if h != Codex {
		return nil
	}
	// Why: Use a custom provider with OpenAI login rather than the built-in API-key provider.
	// The latter selects stored API credentials, which may not have Responses write scope.
	return []string{
		"--config", `model_provider="jev"`,
		"--config", `model_providers.jev.name="jev-routing"`,
		"--config", "model_providers.jev.base_url=" + strconv.Quote("http://"+listen+"/v1"),
		"--config", `model_providers.jev.wire_api="responses"`,
		"--config", `model_providers.jev.requires_openai_auth=true`,
	}
}

// CommandArgs keeps Codex's global config array in one parser scope.
func CommandArgs(h ID, listen string, args []string) []string {
	if h != Codex {
		return append(ChildArgs(h, listen), args...)
	}
	var configs, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case (arg == "-c" || arg == "--config") && i+1 < len(args) && args[i+1] != "--":
			i++
			configs = append(configs, "--config", args[i])
		case strings.HasPrefix(arg, "--config="):
			configs = append(configs, "--config", strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			configs = append(configs, "--config", strings.TrimPrefix(strings.TrimPrefix(arg, "-c"), "="))
		default:
			rest = append(rest, arg)
		}
	}
	// Why: Clap replaces a global Append array when -c appears after exec.
	// Merge every override before the subcommand; routing overrides stay last.
	return append(append(configs, ChildArgs(h, listen)...), rest...)
}
