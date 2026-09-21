package plan

import (
	"regexp"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
)

const Respond = "respond_to_user"

const (
	OutcomeSelected = "selected"
	OutcomeExcluded = "excluded"
	OutcomeDefer    = "defer"

	ReasonPendingAgent     = "pending_agent"
	ReasonAgentStreak      = "agent_streak"
	ReasonSequentialLocate = "sequential_locate"
	ReasonWordMatch        = "word_match"
	ReasonCatalogScore     = "catalog_score"
	ReasonUnknown          = "unknown"
)

type Action struct {
	Tool    string
	Result  string
	Input   string
	Pending bool
}

type Decision struct {
	Tool        string
	Confidence  float64
	Done        float64
	Gated       bool
	Passthrough bool
	Top         []Rank
	// Set is a shortlist to filter the catalog down to when Tool alone is not
	// confident enough to adopt. Empty means "filter to Tool" as before.
	Set []string
	// LastFailed is the reported probability that the most recent action failed.
	LastFailed float64
	// Outcome is selected, excluded, or defer. Hybrid uses this, not Confidence,
	// to decide whether Jev can be skipped.
	Outcome string
	// ReasonCode is the local branch that produced Outcome.
	ReasonCode string
	Excluded   []string
	// Probabilities is the full classifier distribution when known.
	Probabilities map[string]float64
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
		E2E:         has("playwright") || has("flaky") || has("e2e"),
		Sentry:      has("sentry") || has("auth-219") || has("production error"),
		RunTests:    has("re-run") || has("run the tests") || has("run typecheck") || has("再実行"),
		Explore: !nestedAgent(t) && !sequentialLocate(t) && (has("explore") || has("subagent") || has("sub-agent") ||
			has("parallel") || has("look through") || has("thoroughly") ||
			has("調査") || has("探索")),
	}
}

func nestedAgent(t string) bool {
	return strings.Contains(t, "you are a subagent") ||
		strings.Contains(t, "you are an agent") ||
		strings.Contains(t, "you are a claude code")
}

func sequentialLocate(t string) bool {
	t = strings.ToLower(t)
	if explicitShellAsk(t) {
		return false
	}
	if strings.Contains(t, "検索") && strings.Contains(t, "読") {
		return true
	}
	if strings.Contains(t, "定義を調べ") || strings.Contains(t, "定義を検索") {
		return true
	}
	if strings.Contains(t, "並列化せず") || strings.Contains(t, "順に") || strings.Contains(t, "個別のツール") {
		return true
	}
	if strings.Contains(t, "do not parallel") || strings.Contains(t, "don't parallel") {
		return true
	}
	return strings.Contains(t, "sequential") && strings.Contains(t, "search") && strings.Contains(t, "read")
}

func SequentialLocate(t string) bool { return sequentialLocate(t) }

func explicitShellAsk(t string) bool {
	t = strings.ToLower(t)
	return strings.Contains(t, "echo ") ||
		strings.Contains(t, "shell command") ||
		strings.Contains(t, "exec tool") ||
		strings.Contains(t, "run this exact")
}

func PreferTaskText(prev, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return prev
	}
	if explicitShellAsk(next) {
		return next
	}
	if sequentialLocate(prev) && !sequentialLocate(next) {
		return prev
	}
	return next
}

func taskText(s string) string {
	s = WorkRequest(s)
	if i := sequentialAskStart(s); i > 0 {
		return strings.TrimSpace(s[i:])
	}
	return s
}

func sequentialAskStart(s string) int {
	low := strings.ToLower(s)
	markers := []string{
		"定義を調べ", "定義を検索", "ファイルを変更せず",
		"並列化せず", "個別のツール",
		"do not parallel", "don't parallel",
	}
	best := -1
	for _, m := range markers {
		if i := strings.Index(low, strings.ToLower(m)); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if i := strings.Index(s, "検索"); i >= 0 && strings.Contains(s, "読") && (best < 0 || i < best) {
		best = i
	}
	if strings.Contains(low, "sequential") && strings.Contains(low, "search") && strings.Contains(low, "read") {
		if i := strings.Index(low, "sequential"); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best <= 0 {
		return -1
	}
	return best
}

func preferLocateTool(specs []Spec) string {
	var grep, read string
	for _, s := range specs {
		n := strings.ToLower(s.Name)
		switch {
		case n == "grep" || n == "grep_files":
			if grep == "" {
				grep = s.Name
			}
		case isRead(s.Name):
			if read == "" {
				read = s.Name
			}
		}
	}
	if grep != "" {
		return grep
	}
	return read
}

func Native(h host.ID, claude string) string { return host.Native(h, claude) }

var systemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// WorkRequest is the text local scoring and Jev next-tool should see.
// Grok prepends a large injected preamble and wraps the real ask in
// <user_query>; scoring that blob as the request pins send_feedback.
func WorkRequest(s string) string {
	// Why: Strip host reminders before finding the user query; skill catalogs
	// contain task keywords and may themselves mention user_query wrappers.
	s = systemReminder.ReplaceAllString(s, "")
	const open, close = "<user_query>", "</user_query>"
	// Why: Instead of Index (first tag), adopted LastIndex of the open tag. Reason: later Grok turns wrap a new ask; scoring the original first query shrinks the catalog to Agent forever.
	i := strings.LastIndex(s, open)
	if i < 0 {
		return s
	}
	s = s[i+len(open):]
	if j := strings.Index(s, close); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// HostMeta is a host UX tool, not a work schema. Shrinking the catalog to
// only this name (send_feedback) is what Grok sessions get stuck on.
func HostMeta(name string) bool {
	switch strings.ToLower(name) {
	case "send_feedback", "session_title":
		return true
	default:
		return false
	}
}

func Remaining(request string, actions []Action, h host.ID) [][]string {
	request = taskText(request)
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
	locate := sequentialLocate(request)
	if locate {
		add("Grep")
		add("Read")
	} else if s.Explore {
		add("Agent")
	}
	if s.Sentry {
		add("sentry_get_issue")
	}
	if s.PRReview {
		add("github_get_pr")
	}
	if s.E2E && !locate {
		add("playwright_navigate", "playwright_snapshot")
	}
	if s.Typo || s.FailingTest || s.Sentry || s.Health {
		add("Grep", "Glob")
	}
	if s.PRReview {
		add("Read", "github_get_file")
	} else if s.Typo || s.FailingTest || s.Sentry || s.Health || (s.E2E && !locate) {
		add("Read")
	}
	if !locate {
		if s.Health {
			add("Write")
			add("Edit")
		} else if s.Typo || s.FailingTest || s.Sentry || s.E2E {
			add("Edit")
		}
	}
	if s.PRReview {
		add("github_pr_review", "github_comment")
	}
	if !locate && (s.RunTests || s.FailingTest || s.Health || s.E2E || s.Sentry) {
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

// pendingAgent reports whether a subagent (Agent/Task) launch is still running
// in the background with no tool_result yet. Forcing another tool call while
// true starves the host into looping on filler commands until the subagent
// reports back — see the /ship Bash(true) loop this guards against.
func pendingAgent(actions []Action) bool {
	for _, a := range actions {
		if a.Pending && isAgent(a.Tool) {
			return true
		}
	}
	return false
}

func agentStreak(actions []Action) int {
	n := 0
	for i := len(actions) - 1; i >= 0; i-- {
		if !isAgentTool(actions[i].Tool) {
			break
		}
		n++
	}
	return n
}

func isAgentTool(name string) bool {
	if name == "Agent" || name == "spawn_subagent" || name == "task" {
		return true
	}
	return isAgent(name)
}

func decideFromGoals(request string, specs []Spec, set map[string]bool, goals [][]string, outcome, reason string) (Decision, bool) {
	for _, g := range goals {
		for _, n := range g {
			if set[n] {
				if sequentialLocate(request) && isAgent(n) {
					if alt := preferLocateTool(specs); alt != "" {
						return Decision{Tool: alt, Confidence: 0.86, Done: 0.08, Top: ranks(g), Outcome: outcome, ReasonCode: reason}, true
					}
				}
				return Decision{Tool: n, Confidence: 0.86, Done: 0.08, Top: ranks(g), Outcome: outcome, ReasonCode: reason}, true
			}
			if alias := AliasIn(set, n); alias != "" {
				if sequentialLocate(request) && isAgent(alias) {
					if alt := preferLocateTool(specs); alt != "" {
						return Decision{Tool: alt, Confidence: 0.8, Done: 0.08, Top: ranks(g), Outcome: outcome, ReasonCode: reason}, true
					}
				}
				return Decision{Tool: alias, Confidence: 0.8, Done: 0.08, Top: ranks(g), Outcome: outcome, ReasonCode: reason}, true
			}
		}
	}
	return Decision{}, false
}

func DecideSpecs(request string, actions []Action, specs []Spec, h host.ID) Decision {
	if pendingAgent(actions) {
		return Decision{Tool: Respond, Done: 0, Passthrough: true, Confidence: 0.9, Outcome: OutcomeSelected, ReasonCode: ReasonPendingAgent}
	}
	// Why: Instead of shrinking to Agent again after a spawn storm, adopted passthrough once consecutive Agent tools reach 2. Reason: stale first-goal scoring plus catalog shrink to spawn_subagent loops Grok on canned child replies.
	if agentStreak(actions) >= 2 {
		return Decision{Tool: Respond, Done: 0, Passthrough: true, Confidence: 0.9, Outcome: OutcomeSelected, ReasonCode: ReasonAgentStreak}
	}
	request = taskText(request)
	available := make([]string, 0, len(specs))
	set := map[string]bool{}
	var excluded []string
	for _, s := range specs {
		if s.Name == "" {
			continue
		}
		if HostMeta(s.Name) {
			excluded = append(excluded, s.Name)
			continue
		}
		available = append(available, s.Name)
		set[s.Name] = true
	}
	if len(available) == 0 {
		for _, n := range defaultAvailable(h) {
			if HostMeta(n) {
				continue
			}
			available = append(available, n)
			set[n] = true
			specs = append(specs, Spec{Name: n})
		}
	}
	if explicitShellAsk(request) {
		if name := AliasIn(set, "Bash"); name != "" {
			return Decision{Tool: name, Confidence: 0.9, Done: 0.08, Outcome: OutcomeSelected, ReasonCode: ReasonCatalogScore, Excluded: excluded}
		}
	}

	goals := Remaining(request, actions, h)
	if sequentialLocate(request) {
		if d, ok := decideFromGoals(request, specs, set, goals, OutcomeSelected, ReasonSequentialLocate); ok {
			d.Excluded = excluded
			return d
		}
		if alt := preferLocateTool(specs); alt != "" {
			return Decision{Tool: alt, Confidence: 0.86, Done: 0.08, Outcome: OutcomeSelected, ReasonCode: ReasonSequentialLocate, Excluded: excluded}
		}
	} else if d, ok := decideFromGoals(request, specs, set, goals, OutcomeDefer, ReasonWordMatch); ok {
		d.Excluded = excluded
		return d
	}

	best, score := scoreCatalog(request, actions, specs)
	if sequentialLocate(request) && (isAgent(best) || best == "" || score < 0.25) {
		if alt := preferLocateTool(specs); alt != "" {
			return Decision{Tool: alt, Confidence: 0.86, Done: 0.08, Outcome: OutcomeSelected, ReasonCode: ReasonSequentialLocate, Excluded: excluded}
		}
	}
	if best != "" && score >= 0.25 {
		return Decision{Tool: best, Confidence: clamp01(score / 8), Done: 0.08, Outcome: OutcomeDefer, ReasonCode: ReasonCatalogScore, Excluded: excluded}
	}

	// Unknown prompt: never end the host loop. Claude Code still has Agent/Task/MCP
	// tools the 6-task heuristic never heard of; stripping tools[] makes the session stop.
	return Decision{Tool: Respond, Done: 0, Passthrough: true, Confidence: 0.2, Outcome: OutcomeDefer, ReasonCode: ReasonUnknown, Excluded: excluded}
}

func AliasIn(set map[string]bool, want string) string {
	if set[want] {
		return want
	}
	alts := map[string][]string{
		"Agent":                {"Task", "task", "spawn_agent", "spawn_subagent"},
		"Task":                 {"Agent", "spawn_agent", "spawn_subagent", "task"},
		"spawn_subagent":       {"Agent", "Task", "task", "spawn_agent"},
		"Grep":                 {"grep", "grep_search", "grep_files"},
		"Read":                 {"read_file"},
		"Edit":                 {"search_replace", "apply_patch"},
		"Bash":                 {"run_terminal_cmd", "run_terminal_command", "exec_command", "shell", "shell_command", "exec"},
		"run_terminal_cmd":     {"run_terminal_command", "exec_command", "shell", "shell_command", "Bash"},
		"run_terminal_command": {"run_terminal_cmd", "exec_command", "shell", "shell_command", "Bash"},
		"exec_command":         {"shell", "shell_command", "run_terminal_command", "Bash"},
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
	request = taskText(request)
	requestText := strings.ToLower(request)
	requestWords := words(requestText)
	usageCount := map[string]int{}
	for _, a := range actions {
		usageCount[a.Tool]++
	}
	best := ""
	bestScore := 0.0
	for _, spec := range specs {
		name := spec.Name
		if HostMeta(name) {
			continue
		}
		score := 0.0
		searchText := strings.ToLower(name)
		// Why: Orchestrator descriptions mention read/file/codebase and steal
		// simple file tasks (Devin run_subagent 25→1 on a README read).
		if !isAgent(name) {
			searchText = strings.ToLower(name + " " + spec.Desc)
		}
		for _, w := range requestWords {
			if len(w) < 4 {
				continue
			}
			if strings.Contains(searchText, w) {
				score++
			}
		}
		switch {
		case isAgent(name):
			if nestedAgent(requestText) {
				score -= 8
			} else if sequentialLocate(requestText) {
				score -= 1.5
			} else if hasAny(requestText, "explore", "subagent", "sub-agent", "parallel", "look through",
				"codebase", "thoroughly", "delegate", "spawn", "調査", "探索") {
				score += 5
			} else {
				score -= 1.5
			}
		case isGrep(name):
			if hasAny(requestText, "find", "search", "grep", "where", "探", "検索") {
				score += 3
			}
		case isRead(name):
			if hasAny(requestText, "read", "open", "show", "見て", "読") {
				score += 2
			}
		case isEdit(name):
			if hasAny(requestText, "fix", "edit", "patch", "replace", "直") {
				score += 2
			}
		case isExec(name):
			if explicitShellAsk(requestText) {
				score += 5
			} else if hasAny(requestText, "find", "search", "grep", "where", "探", "検索", "read", "open", "show", "見て", "読") {
				score -= 4
			}
		}
		if usageCount[name] > 0 && !isRead(name) {
			score -= 3
		}
		if score > bestScore {
			best, bestScore = name, score
		}
	}
	return best, bestScore
}

// Coarse task phases, matching the task_phase criteria asked of Jev.
const (
	PhaseLocate  = "locate"
	PhaseRead    = "read"
	PhaseModify  = "modify"
	PhaseExecute = "execute"
	PhaseRespond = "respond"
)

// PhaseOf classifies a tool from its name. An unrecognized tool returns ""
// and callers keep it under every phase.
// Why: Descriptions are not classifiable — an edit tool's prose mentions
// reading the file first, and a shell tool's prose mentions find and list.
func PhaseOf(s Spec) string {
	n := strings.ToLower(s.Name)
	switch {
	// Why: A name can carry two verbs (search_replace, apply_patch); the
	// action it performs wins over the material it works on.
	case hasAny(n, "edit", "write", "create", "patch"):
		return PhaseModify
	case hasAny(n, "bash", "exec", "run", "shell", "command"):
		return PhaseExecute
	case hasAny(n, "grep", "glob", "search", "list", "find"):
		return PhaseLocate
	case hasAny(n, "read", "fetch", "get", "view"):
		return PhaseRead
	}
	return ""
}

func isAgent(n string) bool {
	n = strings.ToLower(n)
	return n == "agent" || n == "task" || n == "spawn_agent" || n == "spawn_subagent" || strings.Contains(n, "subagent")
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

func isExec(n string) bool {
	n = strings.ToLower(n)
	return n == "exec" || n == "exec_command" || n == "bash" || strings.Contains(n, "terminal") || strings.Contains(n, "shell")
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
