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

// large-facts writes six deterministic, low-entropy log files (~32KB each,
// ~8K tokens at ordinary text's ~4 bytes/token) with one fact line hidden in
// each: files 1-2 near the start (~5%), 3-4 near the middle (~50%), 5-6 near
// the end (~95%). It exercises JEV_CODEX_TOOL_OUTPUT_TRUNCATE truncation,
// which cuts the middle of any tool output over the threshold -- so a fact placed
// there is only recoverable by re-reading with a narrower command, while
// head/tail facts survive the cut.
//
// The filler is realistic log text (timestamp, worker id, job id, duration,
// status), not random hex: Codex's own code-mode exec truncates a command's
// output by *token* count (~10K tokens), and high-entropy hex text runs
// about 2.3 bytes/token -- a 36.8KB hex file was already cut to ~10KB by
// Codex itself before jev-routing's proxy ever saw it, well under the
// 20000-byte threshold. Ordinary text's ~4 bytes/token keeps a ~32KB file
// under Codex's own ~10K-token cap while staying over the proxy's 20000-byte
// threshold, matching what real sessions' large tool results look like
// (docs/MEMO.md: ordinary text, not high-entropy blobs).
const largeFactsLines = 430

var largeFactsPosition = map[int]float64{1: 0.05, 2: 0.05, 3: 0.5, 4: 0.5, 5: 0.95, 6: 0.95}

func largeFactValue(file int) int { return 1000 + file*37 }

// largeFactsLogLine is a realistic, low-entropy log line: a timestamp, a
// small fixed vocabulary, and small counters that increment deterministically
// (so every line is unique without any random or high-entropy content).
func largeFactsLogLine(file, i int) string {
	sec := i % 86400
	hour, min, s := sec/3600, (sec/60)%60, sec%60
	worker := i % 8
	job := file*100000 + i
	duration := 5 + (i*7)%250
	status := "ok"
	if i%37 == 0 {
		status = "warn"
	}
	return fmt.Sprintf("2026-09-25T%02d:%02d:%02dZ INFO worker-%d processed job %d in %dms status=%s\n",
		hour, min, s, worker, job, duration, status)
}

func largeFactsFiles() map[string]string {
	files := make(map[string]string, 6)
	for f := 1; f <= 6; f++ {
		factLine := int(float64(largeFactsLines) * largeFactsPosition[f])
		if factLine < 1 {
			factLine = 1
		}
		if factLine > largeFactsLines {
			factLine = largeFactsLines
		}
		var log strings.Builder
		for i := 1; i <= largeFactsLines; i++ {
			if i == factLine {
				fmt.Fprintf(&log, "fact%d=%d\n", f, largeFactValue(f))
				continue
			}
			log.WriteString(largeFactsLogLine(f, i))
		}
		files[fmt.Sprintf("logs/large-%d.txt", f)] = log.String()
	}
	return files
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
			ID: "large-facts", Title: "Recover six facts hidden across six large logs", TimeoutMinutes: 12,
			Prompt: "Read logs/large-1.txt through logs/large-6.txt. For each file, run `cat` on the complete file, as its own tool call, before you answer; do not skip a file or summarize instead of reading it. Show each file's full output at once; do not let the output get cut off partway through (if your command-execution tool lets you set an output limit, set max_output_tokens to at least 12000). Each file contains exactly one line of the form factN=VALUE. After reading all six files, write answer.json with integer keys fact1 through fact6, one value per file. Do not modify the logs.",
			Setup: func(workspace string) error {
				return writeFiles(workspace, largeFactsFiles())
			},
			Verify: func(workspace string) (verdict, error) {
				want := map[string]any{}
				for f := 1; f <= 6; f++ {
					want[fmt.Sprintf("fact%d", f)] = float64(largeFactValue(f))
				}
				return verifyAnswer(workspace, want)
			},
			Reference: func(workspace string) error {
				var b strings.Builder
				b.WriteString("{")
				for f := 1; f <= 6; f++ {
					if f > 1 {
						b.WriteString(",")
					}
					fmt.Fprintf(&b, `"fact%d":%d`, f, largeFactValue(f))
				}
				b.WriteString("}\n")
				return writeFiles(workspace, map[string]string{"answer.json": b.String()})
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
