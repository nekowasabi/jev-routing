package bench

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

// Isolation is what a run touched outside its own sandbox.
// Contaminated means it read a path it did not create, so its numbers are not the task.
type Isolation struct {
	Audited      bool     `json:"audited"`
	ToolCalls    int      `json:"toolCalls,omitempty"`
	Outside      []Touch  `json:"outside,omitempty"`
	ForeignReads []string `json:"foreignReads,omitempty"`
	Contaminated bool     `json:"contaminated"`
}

// Touch is one path outside the sandbox.
type Touch struct {
	Path string `json:"path"`
	How  string `json:"how"`
}

// Places an agent may touch without it meaning anything: the toolchain and the system.
var harmless = regexp.MustCompile(`^/(usr|bin|etc|dev|proc|lib|opt|nix|snap)\b|/\.nvm/|/node_modules/|^/home/[^/]+/\.(npm|cache|nvm|codex|claude)\b`)
var pathRe = regexp.MustCompile(`~?/(?:home|tmp|mnt|root|var|Users)/[^\s"'` + "`" + `\\)<>|;,]*`)
var shellLine = regexp.MustCompile(`^/bin/(ba)?sh -lc `)
var toolWord = regexp.MustCompile(`\b(cat|sed|diff)\b`)

type toolCall struct {
	tool string
	text string
}

// Audit reads the agent log and lists paths outside the sandbox.
// Codex and Claude Code are parsed the way jev-gateway-bench parses them.
// Other agents are audited only when their log contains the same shell lines.
func Audit(agent, agentLog, sandbox, home string) Isolation {
	raw, err := os.ReadFile(agentLog)
	if err != nil {
		return Isolation{Audited: false}
	}
	var calls []toolCall
	switch agent {
	case "codex":
		calls = codexCommands(string(raw))
	case "claude":
		calls = claudeCommands(string(raw))
	default:
		calls = codexCommands(string(raw))
	}
	return inspect(calls, sandbox, home)
}

// codexDualFactEvidence checks completed MCP results in the host's JSONL trace.
// Responses Lite does not always expose these calls through proxy applications.
func codexDualFactEvidence(agentLog string) bool {
	raw, err := os.ReadFile(agentLog)
	if err != nil {
		return false
	}
	seen := map[string]string{}
	want := map[string]string{"bench_left_fact": "left=17", "bench_right_fact": "right=23"}
	for _, line := range strings.Split(string(raw), "\n") {
		var event struct {
			Type string `json:"type"`
			Item struct {
				ID     string          `json:"id"`
				Type   string          `json:"type"`
				Server string          `json:"server"`
				Tool   string          `json:"tool"`
				Status string          `json:"status"`
				Error  json.RawMessage `json:"error"`
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Type != "item.completed" || event.Item.Type != "mcp_tool_call" ||
			event.Item.Server != "bench" || event.Item.Status != "completed" || event.Item.ID == "" ||
			(len(event.Item.Error) > 0 && string(event.Item.Error) != "null") {
			continue
		}
		if expected, ok := want[event.Item.Tool]; ok && len(event.Item.Result.Content) == 1 && event.Item.Result.Content[0].Text == expected {
			seen[event.Item.Tool] = event.Item.ID
		}
	}
	left, right := seen["bench_left_fact"], seen["bench_right_fact"]
	return left != "" && right != "" && left != right
}

func inspect(calls []toolCall, sandbox, home string) Isolation {
	seen := map[string]string{}
	var order []string
	for _, call := range calls {
		// The bench catalog stub touches nothing; its arguments are not reads.
		if strings.HasPrefix(call.tool, "mcp__bench__") || skipAgentsSearch(call.text) {
			continue
		}
		for _, path := range pathsIn(call.text) {
			if strings.HasPrefix(path, sandbox) || harmless.MatchString(path) {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			how := "found"
			if call.tool == "Write" || createdPath(call.text, path) {
				how = "created"
			}
			seen[path] = how
			order = append(order, path)
		}
	}
	var outside []Touch
	var foreign []string
	for _, path := range order {
		shown := path
		if home != "" && strings.HasPrefix(path, home) {
			shown = "~" + path[len(home):]
		}
		outside = append(outside, Touch{Path: shown, How: seen[path]})
		if seen[path] == "found" {
			foreign = append(foreign, shown)
		}
	}
	if outside == nil {
		outside = []Touch{}
	}
	if foreign == nil {
		foreign = []string{}
	}
	return Isolation{
		Audited:      true,
		ToolCalls:    len(calls),
		Outside:      outside,
		ForeignReads: foreign,
		Contaminated: len(foreign) > 0,
	}
}

func pathsIn(text string) []string {
	var out []string
	for _, loc := range pathRe.FindAllStringIndex(text, -1) {
		if loc[0] > 0 {
			prev := text[loc[0]-1]
			if isPathPrefix(prev) {
				continue
			}
		}
		out = append(out, text[loc[0]:loc[1]])
	}
	return out
}

func isPathPrefix(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '.' || b == ':' || b == '-'
}

func skipAgentsSearch(text string) bool {
	if !strings.Contains(text, "AGENTS.md") {
		return false
	}
	return !readsNonAgentsPath(text)
}

// readsNonAgentsPath is the lookaround-free form of the original audit rule:
// cat/sed/diff of a slash after which AGENTS.md never appears.
func readsNonAgentsPath(text string) bool {
	for _, loc := range toolWord.FindAllStringIndex(text, -1) {
		window := text[loc[1]:]
		if i := strings.IndexAny(window, "|&;"); i >= 0 {
			window = window[:i]
		}
		offset := loc[1]
		rest := window
		for {
			j := strings.Index(rest, "/")
			if j < 0 {
				break
			}
			abs := offset + j
			if !strings.Contains(text[abs:], "AGENTS.md") {
				return true
			}
			offset += j + 1
			rest = rest[j+1:]
		}
	}
	return false
}

func createdPath(text, path string) bool {
	re := regexp.MustCompile(`(>|tee\s+(-a\s+)?|mkdir\s+(-p\s+)?|touch\s+)\s*` + regexp.QuoteMeta(path))
	return re.MatchString(text)
}

func codexCommands(log string) []toolCall {
	var out []toolCall
	for _, line := range strings.Split(log, "\n") {
		if shellLine.MatchString(line) {
			out = append(out, toolCall{tool: "shell", text: line})
		}
	}
	return out
}

func claudeCommands(log string) []toolCall {
	var out []toolCall
	for _, line := range strings.Split(log, "\n") {
		var msg struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil || len(msg.Message.Content) == 0 {
			continue
		}
		var blocks []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(msg.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_use" {
				out = append(out, toolCall{tool: block.Name, text: string(block.Input)})
			}
		}
	}
	return out
}
