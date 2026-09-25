package bench

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed assets/chess
var chessFS embed.FS

// Task is one agentic coding task. Setup writes the starting workspace.
// Verify is the hidden checker the agent is not given.
type Task struct {
	ID               string
	Title            string
	TimeoutMinutes   int
	Prompt           string
	Setup            func(workspace string) error
	Verify           func(workspace string) (verdict, error)
	Reference        func(workspace string) error
	RequiresSubagent bool
	// San asks the verifier for algebraic-notation checks.
	San bool
}

type bug struct {
	what string
	from string
	to   string
}

// Bugs are the slips injected into chess-bugfix. Each one, alone, fails a hidden check.
var Bugs = []bug{
	{"en passant removes the wrong square", "board[index(fileOf(move.to), rankOf(move.from))] = null;", "board[index(fileOf(move.from), rankOf(move.to))] = null;"},
	{"kingside castling through an attacked square", "empty: [5, 6], safe: [5, 6]", "empty: [5, 6], safe: [6]"},
	{"a knight step is off by one", "[-2, 1], [-1, 2]]", "[-2, 2], [-1, 2]]"},
	{"no promotion to bishop", `of "qrbn")`, `of "qrn")`},
	{"castling right survives the rook's capture", "for (const square of [move.from, move.to]) {", "for (const square of [move.from]) {"},
}

func mustAsset(name string) string {
	b, err := chessFS.ReadFile("assets/chess/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var (
	refHeader = regexp.MustCompile(`(?s)^// Reference solution.*?\n\n`)
	sanMarks  = regexp.MustCompile(`(?m)^[ \t]*// </?san>\n`)
	sanBlock  = regexp.MustCompile(`(?s)(?m)^[ \t]*// <san>\n.*?// </san>\n`)
)

func engineSource(san bool) string {
	source := refHeader.ReplaceAllString(mustAsset("reference/chess.js"), "")
	if san {
		return sanMarks.ReplaceAllString(source, "")
	}
	return sanBlock.ReplaceAllString(source, "")
}

// Solution is a complete engine, including algebraic notation.
func Solution() string { return engineSource(true) }

// EngineWithoutSan is the working engine the SAN task starts from.
func EngineWithoutSan() string { return engineSource(false) }

func inject(source string, bugs []bug) (string, error) {
	for _, b := range bugs {
		if strings.Count(source, b.from) != 1 {
			return "", fmt.Errorf("bug %q no longer matches the reference exactly once", b.what)
		}
		source = strings.Replace(source, b.from, b.to, 1)
	}
	return source, nil
}

func manifest(name string) string {
	return "{\n" +
		`  "name": "` + name + `",` + "\n" +
		`  "version": "1.0.0",` + "\n" +
		`  "private": true,` + "\n" +
		`  "type": "module",` + "\n" +
		`  "scripts": {` + "\n" +
		`    "test": "node --test"` + "\n" +
		"  }\n" +
		"}\n"
}

func writeFiles(workspace string, files map[string]string) error {
	for name, content := range files {
		path := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Tasks is the chess suite: build, debug, extend. Routing may pay off on one kind of turn and not another.
func Tasks() ([]Task, error) {
	spec := mustAsset("SPEC.md")
	specSAN := mustAsset("SPEC-SAN.md")
	smoke := mustAsset("fixtures/smoke.test.js")
	perft := mustAsset("fixtures/perft.test.js")
	buggy, err := inject(EngineWithoutSan(), Bugs)
	if err != nil {
		return nil, err
	}
	return []Task{
		xcellTask("xcell-module"),
		xcellTask("xcell-locate"),
		{
			ID: "compact-facts", Title: "Recover early and late facts after reading staged logs", TimeoutMinutes: 10,
			Prompt: "Read logs/stage-1.txt through logs/stage-20.txt in numerical order. Run `cat` once for each complete file, as separate tool calls; do not search or summarize the files with another command. After all twenty reads, write answer.json with integer keys first, last, and sum, using the first value from stage 1 and the last value from stage 20. Do not modify the logs.",
			Setup: func(workspace string) error {
				files := map[string]string{}
				for stage := 1; stage <= 20; stage++ {
					var log strings.Builder
					if stage == 1 {
						log.WriteString("first=37\n")
					}
					lines := 240
					if stage == 1 || stage == 20 {
						lines = 20
					}
					for line := 1; line <= lines; line++ {
						payload := sha256.Sum256([]byte(fmt.Sprintf("%d/%d", stage, line)))
						fmt.Fprintf(&log, "stage %d event %03d payload %x\n", stage, line, payload)
					}
					if stage == 20 {
						log.WriteString("last=61\n")
					}
					files[fmt.Sprintf("logs/stage-%d.txt", stage)] = log.String()
				}
				return writeFiles(workspace, files)
			},
			Verify: func(workspace string) (verdict, error) {
				return verifyAnswer(workspace, map[string]any{"first": float64(37), "last": float64(61), "sum": float64(98)})
			},
			Reference: func(workspace string) error {
				return writeFiles(workspace, map[string]string{"answer.json": `{"first":37,"last":61,"sum":98}` + "\n"})
			},
		},
		{
			ID: "child-facts", Title: "Delegate one fact and combine two values", TimeoutMinutes: 6,
			Prompt:           "Delegate reading left.txt to a child agent exactly once using this host's subagent tool. In the parent session, read right.txt yourself. Write answer.json with integer keys left, right, and sum. The sum must equal left + right. Do not change the fact files.",
			RequiresSubagent: true,
			Setup: func(workspace string) error {
				return writeFiles(workspace, map[string]string{"left.txt": "left=17\n", "right.txt": "right=23\n"})
			},
			Verify: func(workspace string) (verdict, error) {
				return verifyAnswer(workspace, map[string]any{"left": float64(17), "right": float64(23), "sum": float64(40)})
			},
			Reference: func(workspace string) error {
				return writeFiles(workspace, map[string]string{"answer.json": `{"left":17,"right":23,"sum":40}` + "\n"})
			},
		},
		xcellTask("child-survey"),
		{
			ID: "dual-facts", Title: "Combine two independent file facts", TimeoutMinutes: 5,
			Prompt: "Call both bench MCP tools bench_left_fact and bench_right_fact. They hold separate values that are not in this workspace. Write answer.json with integer keys left, right, and sum. The sum must equal left + right.",
			Setup: func(workspace string) error {
				return writeFiles(workspace, map[string]string{"README.md": "The two values are supplied only by the bench MCP tools named in the task.\n"})
			},
			Verify: func(workspace string) (verdict, error) {
				return verifyAnswer(workspace, map[string]any{"left": float64(17), "right": float64(23), "sum": float64(40)})
			},
			Reference: func(workspace string) error {
				return writeFiles(workspace, map[string]string{"answer.json": `{"left":17,"right":23,"sum":40}` + "\n"})
			},
		},
		{
			ID: "skill-proof", Title: "Use a routed skill to complete a file fact", TimeoutMinutes: 5,
			Prompt: "Read go.mod and write answer.json with the exact module and go directives. If task context supplies a proof value, include it as the proof field. Do not inspect files outside this workspace.",
			Setup: func(workspace string) error {
				if err := writeFiles(workspace, map[string]string{"go.mod": "module example.org/skill-bench\n\ngo 1.25.0\n"}); err != nil {
					return err
				}
				return writeFiles(filepath.Dir(workspace), map[string]string{"skills/module-proof/SKILL.md": "---\nname: module-proof\ndescription: Use when returning module and Go version facts from go.mod.\n---\nInclude the exact proof value PLUM-91 in answer.json.\n"})
			},
			Verify: func(workspace string) (verdict, error) {
				return verifyAnswer(workspace, map[string]any{"module": "example.org/skill-bench", "go": "1.25.0", "proof": "PLUM-91"})
			},
			Reference: func(workspace string) error {
				return writeFiles(workspace, map[string]string{"answer.json": `{"module":"example.org/skill-bench","go":"1.25.0","proof":"PLUM-91"}` + "\n"})
			},
		},
		{
			ID: "chess-engine", Title: "Build a chess rules engine from a spec", TimeoutMinutes: 30,
			Prompt: "Implement the chess rules engine described in README.md as src/chess.js. It must follow the API and the " +
				"details in README.md exactly, use no dependencies, and make `npm test` pass. Add tests of your own as you " +
				"see fit, and keep going until you are confident every rule is right.",
			Setup: func(workspace string) error {
				return writeFiles(workspace, map[string]string{
					"package.json":       manifest("chess-engine"),
					"README.md":          spec,
					"src/chess.js":       "// Implement the Chess class described in README.md.\n",
					"test/smoke.test.js": smoke,
				})
			},
		},
		{
			ID: "chess-bugfix", Title: "Find and fix the bugs in a chess engine", TimeoutMinutes: 20,
			Prompt: "`npm test` fails in this project. src/chess.js is a chess rules engine that is supposed to implement " +
				"README.md, and it has several bugs. Find and fix all of them. Do not change the tests, the public API, or " +
				"package.json, and do not add dependencies.",
			Setup: func(workspace string) error {
				return writeFiles(workspace, map[string]string{
					"package.json":       manifest("chess-bugfix"),
					"README.md":          spec,
					"src/chess.js":       buggy,
					"test/perft.test.js": perft,
				})
			},
		},
		{
			ID: "chess-san", Title: "Add algebraic notation to a working chess engine", TimeoutMinutes: 20, San: true,
			Prompt: "src/chess.js is a working chess rules engine. Add the three members described under \"Standard Algebraic " +
				"Notation\" in README.md: san(uci), moveSan(san) and history(). Everything that works today must keep " +
				"working, `npm test` must pass, and no dependencies may be added. Add tests for the new behaviour.",
			Setup: func(workspace string) error {
				return writeFiles(workspace, map[string]string{
					"package.json":       manifest("chess-san"),
					"README.md":          spec + specSAN,
					"src/chess.js":       EngineWithoutSan(),
					"test/perft.test.js": perft,
				})
			},
		},
	}, nil
}
