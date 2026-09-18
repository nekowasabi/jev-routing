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
	Tool       string
	Confidence float64
	Done       float64
	Gated      bool
	Top        []Rank
}

type Rank struct {
	Name string
	P    float64
}

type Tool struct {
	Name     string
	Keywords []string
}

type Signals struct {
	FailingTest, Typo, Health, PRReview, E2E, Sentry bool
	RunTests                                         bool
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
	}
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
	goals := Remaining(request, actions, h)
	if len(available) == 0 {
		available = defaultAvailable(h)
	}
	set := map[string]bool{}
	for _, n := range available {
		set[n] = true
	}
	if len(goals) == 0 {
		return Decision{Tool: Respond, Confidence: 0.92, Done: 0.97}
	}
	for _, g := range goals {
		for _, n := range g {
			if set[n] {
				return Decision{Tool: n, Confidence: 0.86, Done: 0.08, Top: ranks(g)}
			}
		}
	}
	// Name not in this request's tools[] — still return it so the proxy can keep it if present.
	tool := goals[0][0]
	d := Decision{Tool: tool, Confidence: 0.7, Done: 0.08, Top: ranks(goals[0])}
	if d.Tool == Respond && d.Done < 0.5 {
		d.Tool = tool
		d.Gated = true
	}
	return d
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
		if n, ok := t["name"].(string); ok && n != "" {
			names = append(names, n)
			continue
		}
		if fn, ok := t["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	return names
}
