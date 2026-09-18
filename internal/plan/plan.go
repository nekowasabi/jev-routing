package plan

import (
	"regexp"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
)

const Respond = "respond_to_user"

type Action struct {
	Tool   string
	Result string
	Input  string
}

type Decision struct {
	Tool        string
	Confidence  float64
	Done        float64
	Gated       bool
	Passthrough bool
	Top         []Rank
}

type Rank struct {
	Name string
	P    float64
}

type Spec struct {
	Name string
	Desc string
}

type Signals struct {
	FailingTest, Typo, Health, PRReview, E2E, Sentry bool
	RunTests, Explore                                bool
}

func Extract(text string) Signals {
	t := strings.ToLower(text)
	has := func(p string) bool { return strings.Contains(t, p) }
	return Signals{
		FailingTest: regexp.MustCompile(`failing test|test is failing|落ちている`).MatchString(t),
		Typo:        has("typo") || has("recieve") || has("誤字"),
		Health:      has("/api/health") || has("health route") || has("health ルート"),
		PRReview:    has("pr 842") || has("pull request") || has("review github") || has("pr をレビュー"),
		E2E:         has("playwright") || has("flaky") || has("e2e") || has("checkout"),
		Sentry:      has("sentry") || has("auth-219") || has("production error"),
		RunTests:    has("re-run") || has("run the tests") || has("run typecheck") || has("再実行"),
		Explore: !nestedAgent(t) && (has("explore") || has("subagent") || has("sub-agent") ||
			has("parallel") || has("look through") || has("codebase") || has("thoroughly") ||
			has("調査") || has("探索")),
	}
}

func nestedAgent(t string) bool {
	return strings.Contains(t, "you are a subagent") ||
		strings.Contains(t, "you are an agent") ||
		strings.Contains(t, "you are a claude code")
}

func Native(h host.ID, claude string) string { return host.Native(h, claude) }

func Remaining(request string, actions []Action, h host.ID) [][]string {
	s := Extract(request)
	done := map[string]bool{}
	for _, a := range actions {
		done[a.Tool] = true
	}
	var goals [][]string
	add := func(claude ...string) {
		names := make([]string, len(claude))
		for i, n := range claude {
			names[i] = Native(h, n)
		}
		for _, n := range names {
			if done[n] {
				return
			}
		}
		goals = append(goals, names)
	}
	if s.Explore {
		add("Agent")
	}
	if s.Sentry {
		add("sentry_get_issue")
	}
	if s.PRReview {
		add("github_get_pr")
	}
	if s.E2E {
		add("playwright_navigate", "playwright_snapshot")
	}
	if s.Typo || s.FailingTest || s.Sentry || s.Health {
		add("Grep", "Glob")
	}
	if s.PRReview {
		add("Read", "github_get_file")
	} else if s.Typo || s.FailingTest || s.Sentry || s.Health || s.E2E {
		add("Read")
	}
	if s.Health {
		add("Write")
		add("Edit")
	} else if s.Typo || s.FailingTest || s.Sentry || s.E2E {
		add("Edit")
	}
	if s.PRReview {
		add("github_pr_review", "github_comment")
	}
	if s.RunTests || s.FailingTest || s.Health || s.E2E || s.Sentry {
		add("Bash")
	}
	return goals
}

func Decide(request string, actions []Action, available []string, h host.ID) Decision {
	specs := make([]Spec, len(available))
	for i, n := range available {
		specs[i] = Spec{Name: n}
	}
	return DecideSpecs(request, actions, specs, h)
}

func DecideSpecs(request string, actions []Action, specs []Spec, h host.ID) Decision {
	available := make([]string, 0, len(specs))
	set := map[string]bool{}
	for _, s := range specs {
		if s.Name == "" {
			continue
		}
		available = append(available, s.Name)
		set[s.Name] = true
	}
	if len(available) == 0 {
		for _, n := range defaultAvailable(h) {
			available = append(available, n)
			set[n] = true
			specs = append(specs, Spec{Name: n})
		}
	}

	goals := Remaining(request, actions, h)
	for _, g := range goals {
		for _, n := range g {
			if set[n] {
				return Decision{Tool: n, Confidence: 0.86, Done: 0.08, Top: ranks(g)}
			}
			if alias := aliasIn(set, n); alias != "" {
				return Decision{Tool: alias, Confidence: 0.8, Done: 0.08, Top: ranks(g)}
			}
		}
	}

	best, score := scoreCatalog(request, actions, specs)
	if best != "" && score >= 0.25 {
		return Decision{Tool: best, Confidence: clamp01(score / 8), Done: 0.08}
	}

	// Unknown prompt: never end the host loop. Claude Code still has Agent/Task/MCP
	// tools the 6-task heuristic never heard of; stripping tools[] makes the session stop.
	return Decision{Tool: Respond, Done: 0, Passthrough: true, Confidence: 0.2}
}

func aliasIn(set map[string]bool, want string) string {
	if set[want] {
		return want
	}
	alts := map[string][]string{
		"Agent": {"Task", "task", "spawn_agent"},
		"Task":  {"Agent", "spawn_agent"},
		"Grep":  {"grep", "grep_files"},
		"Read":  {"read_file"},
		"Edit":  {"search_replace", "apply_patch"},
		"Bash":  {"run_terminal_cmd", "exec_command"},
	}
	for _, a := range alts[want] {
		if set[a] {
			return a
		}
	}
	lower := strings.ToLower(want)
	for n := range set {
		if strings.ToLower(n) == lower {
			return n
		}
	}
	return ""
}

func scoreCatalog(request string, actions []Action, specs []Spec) (string, float64) {
	req := strings.ToLower(request)
	used := map[string]int{}
	for _, a := range actions {
		used[a.Tool]++
	}
	best := ""
	bestS := 0.0
	for _, spec := range specs {
		s := 0.0
		name := spec.Name
		blob := strings.ToLower(name + " " + spec.Desc)
		for _, w := range words(req) {
			if len(w) < 4 {
				continue
			}
			if strings.Contains(blob, w) {
				s++
			}
		}
		switch {
		case isAgent(name):
			if nestedAgent(req) {
				s -= 8
			} else if hasAny(req, "explore", "subagent", "sub-agent", "parallel", "look through",
				"codebase", "thoroughly", "delegate", "spawn", "調査", "探索") {
				s += 5
			} else {
				s -= 1.5
			}
		case isGrep(name):
			if hasAny(req, "find", "search", "grep", "where", "探") {
				s += 3
			}
		case isRead(name):
			if hasAny(req, "read", "open", "show", "見て") {
				s += 2
			}
		case isEdit(name):
			if hasAny(req, "fix", "edit", "patch", "replace", "直") {
				s += 2
			}
		}
		if used[name] > 0 && !isRead(name) {
			s -= 3
		}
		if s > bestS {
			best, bestS = name, s
		}
	}
	return best, bestS
}

func isAgent(n string) bool {
	n = strings.ToLower(n)
	return n == "agent" || n == "task" || n == "spawn_agent" || strings.Contains(n, "subagent")
}

func isGrep(n string) bool {
	n = strings.ToLower(n)
	return n == "grep" || n == "grep_files" || strings.Contains(n, "search")
}

func isRead(n string) bool {
	n = strings.ToLower(n)
	return n == "read" || n == "read_file"
}

func isEdit(n string) bool {
	n = strings.ToLower(n)
	return n == "edit" || n == "search_replace" || n == "apply_patch"
}

func hasAny(t string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(t, n) {
			return true
		}
	}
	return false
}

func words(s string) []string {
	return regexp.MustCompile(`[a-z0-9_]+|[ぁ-んァ-ン一-龯]+`).FindAllString(strings.ToLower(s), -1)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func ranks(names []string) []Rank {
	out := make([]Rank, 0, len(names))
	p := 0.7
	for _, n := range names {
		out = append(out, Rank{Name: n, P: p})
		p *= 0.5
	}
	return out
}

func defaultAvailable(h host.ID) []string {
	base := []string{"Read", "Edit", "Write", "Bash", "Glob", "Grep", "Agent",
		"github_get_pr", "github_pr_review", "github_comment", "github_create_pr",
		"playwright_navigate", "playwright_snapshot", "sentry_get_issue", "sentry_resolve"}
	out := make([]string, len(base))
	for i, n := range base {
		out[i] = Native(h, n)
	}
	return out
}

func ToolNames(tools []map[string]any) []string {
	var names []string
	for _, t := range tools {
		if n := toolName(t); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func SpecsFrom(tools []map[string]any) []Spec {
	out := make([]Spec, 0, len(tools))
	for _, t := range tools {
		n := toolName(t)
		if n == "" {
			continue
		}
		desc, _ := t["description"].(string)
		if desc == "" {
			if fn, ok := t["function"].(map[string]any); ok {
				desc, _ = fn["description"].(string)
			}
		}
		out = append(out, Spec{Name: n, Desc: desc})
	}
	return out
}

func toolName(t map[string]any) string {
	if n, ok := t["name"].(string); ok && n != "" {
		return n
	}
	if fn, ok := t["function"].(map[string]any); ok {
		if n, ok := fn["name"].(string); ok {
			return n
		}
	}
	return ""
}
