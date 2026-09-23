package bench

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/proxy"
)

const helpText = `jev-routing bench — same coding task, routing on and routing off.

Usage:
  jev-routing bench [options]
  jev-routing bench selftest
  jev-routing bench report <results dir> [--prices in,cached,out[,cachewrite]]
  jev-routing bench audit <results dir>...
  jev-routing bench chart <results dir>... [--out charts]
  jev-routing bench fake-upstream [--port 8798]

Options:
  --agent codex|claude|grok|devin|fake   who does the work (default codex)
  --host NAME                            alias of --agent
  --model NAME                           model passed to the agent
  --user-tools                           keep your MCP servers, plugins, skills and settings
  --tasks a,b                            task ids (default: all)
  --modes on,off                         routing states to compare (default on,off)
  --on-mode filter|forced                what "on" means (default filter; "off" is always baseline)
  --reps N                               repetitions of every task in every mode (default 1)
  --timeout-min N                        override each task's own time limit
  --prices in,cached,out[,cachewrite]    USD per million tokens (cachewrite default 1.25×in)
  --port N                               port for the per-run proxy (default 8890)
  --out DIR                              results directory (default results/<timestamp>)
  --keep                                 keep the workspaces
  --list                                 show tasks and options

Tasks:
  chess-engine   Build a chess rules engine from a spec (30 min)
  chess-bugfix   Find and fix five injected bugs (20 min)
  chess-san      Add algebraic notation to a working engine (20 min)

Routing on is JEV_ROUTING_MODE=filter (or --on-mode forced). Routing off is baseline:
the proxy meters the request and does not rewrite it. Each run has its own workspace
and its own proxy, so the tokens belong to that run.

Real agents spend real quota. Start with one task and --reps 1.
--agent fake writes the reference solution through the proxy and spends nothing.
Node.js is required for the hidden chess verifier.

The tasks and verifier are ported from github.com/vinilana/jev-gateway-bench (MIT).
`

// Run is the jev-routing bench command.
func Run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprint(os.Stdout, helpText)
			return 0
		case "run":
			return runCmd(args[1:])
		case "selftest":
			return Selftest(os.Stdout)
		case "report":
			return reportCmd(args[1:])
		case "audit":
			return auditCmd(args[1:])
		case "chart":
			return chartCmd(args[1:])
		case "fake-upstream":
			return fakeUpstreamCmd(args[1:])
		}
	}
	return runCmd(args)
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentFlag := fs.String("agent", "", "agent")
	hostFlag := fs.String("host", "", "alias of --agent")
	model := fs.String("model", "", "model")
	userTools := fs.Bool("user-tools", false, "keep user tools")
	taskIDs := fs.String("tasks", "", "task ids")
	modeFlag := fs.String("modes", "on,off", "on,off")
	onMode := fs.String("on-mode", proxy.ModeFilter, "filter|forced")
	repsFlag := fs.String("reps", "1", "repetitions")
	portFlag := fs.String("port", "8890", "port")
	outFlag := fs.String("out", "", "output dir")
	timeoutFlag := fs.String("timeout-min", "", "timeout")
	pricesFlag := fs.String("prices", "", "prices")
	keep := fs.Bool("keep", false, "keep workspaces")
	list := fs.Bool("list", false, "list")
	help := fs.Bool("help", false, "help")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprint(os.Stderr, helpText)
		return 2
	}
	if *list || *help {
		fmt.Fprint(os.Stdout, helpText)
		return 0
	}
	agent := *agentFlag
	if agent == "" {
		agent = *hostFlag
	}
	if agent == "" {
		agent = "codex"
	}
	if agent != "fake" {
		if _, err := host.Parse(agent); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	if *onMode != proxy.ModeFilter && *onMode != proxy.ModeForced {
		fmt.Fprintf(os.Stderr, "--on-mode must be filter or forced, not %q\n", *onMode)
		return 2
	}
	tasks, err := Tasks()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	chosen, err := selectTasks(tasks, *taskIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	modes, err := parseModes(*modeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	reps, err := strconv.Atoi(*repsFlag)
	if err != nil || reps < 1 {
		fmt.Fprintln(os.Stderr, "--reps must be a positive integer")
		return 2
	}
	port, err := strconv.Atoi(*portFlag)
	if err != nil || port < 0 || port > 65535 {
		fmt.Fprintln(os.Stderr, "--port must be 0-65535")
		return 2
	}
	prices, err := parsePrices(*pricesFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	opt, err := proxy.OptionsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	line, sterr := jev.StartupStatus(jev.FromEnv(), opt.SelectionMode)
	fmt.Fprintln(os.Stderr, line)
	if sterr != nil && contains(modes, "on") {
		return 2
	}

	release, err := claimLock()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer release()

	if agent == "fake" && os.Getenv("BENCH_FAKE_UPSTREAM") == "" {
		origin, stop, err := ServeFakeUpstream("127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_ = os.Setenv("BENCH_FAKE_UPSTREAM", origin)
		defer func() {
			stop()
			_ = os.Unsetenv("BENCH_FAKE_UPSTREAM")
		}()
	}

	outDir := *outFlag
	if outDir == "" {
		stamp := time.Now().UTC().Format("2006-01-02T15-04-05.000Z")
		stamp = strings.NewReplacer(":", "-", ".", "-").Replace(stamp)
		outDir = filepath.Join("results", stamp)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	type step struct {
		task Task
		mode string
		rep  int
	}
	var plan []step
	for rep := 1; rep <= reps; rep++ {
		order := append([]string(nil), modes...)
		if rep%2 == 0 {
			for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
				order[i], order[j] = order[j], order[i]
			}
		}
		for _, task := range chosen {
			for _, mode := range order {
				plan = append(plan, step{task, mode, rep})
			}
		}
	}
	plural := "s"
	if len(plan) == 1 {
		plural = ""
	}
	fmt.Printf("%d run%s with %s; results in %s\n\n", len(plan), plural, agent, outDir)
	fmt.Fprintln(os.Stderr, "Real agents spend real quota. Routing on rewrites; routing off is a metering baseline.")

	var runs []RunRecord
	var sandbox string
	for i, step := range plan {
		if ctx.Err() != nil {
			if sandbox != "" && !*keep {
				_ = os.RemoveAll(sandbox)
			}
			return 130
		}
		label := fmt.Sprintf("%s.%s.%d", step.task.ID, step.mode, step.rep)
		runDir := filepath.Join(outDir, label)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		sandbox, err = os.MkdirTemp("", fmt.Sprintf("jev-bench-%d-", os.Getpid()))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		workspace := filepath.Join(sandbox, "workspace")
		scratch := filepath.Join(sandbox, "tmp")
		if err := os.MkdirAll(workspace, 0o755); err != nil || os.MkdirAll(scratch, 0o755) != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := step.task.Setup(workspace); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		gitQuiet(workspace, "-c", "init.defaultBranch=main", "init", "-q")
		gitQuiet(workspace, "add", "-A")
		gitQuiet(workspace, "-c", "user.name=bench", "-c", "user.email=bench@localhost", "commit", "-q", "-m", "task")

		fmt.Printf("[%d/%d] %s … ", i+1, len(plan), label)
		logFile, _ := os.Create(filepath.Join(runDir, "gateway.log"))
		var logW io.Writer = io.Discard
		if logFile != nil {
			logW = logFile
		}
		gw, err := startGateway(gatewayHost(agent), fmt.Sprintf("127.0.0.1:%d", port), routingMode(step.mode == "on", *onMode), label, logW, upstreamFor(agent))
		if err != nil {
			if logFile != nil {
				_ = logFile.Close()
			}
			fmt.Fprintf(os.Stderr, "\n%s\n", err)
			if !*keep {
				_ = os.RemoveAll(sandbox)
			}
			return 1
		}
		var outcome procResult
		agentLog := filepath.Join(runDir, "agent.log")
		if agent == "fake" {
			f, _ := os.Create(agentLog)
			started := time.Now()
			ferr := runFakeAgent(gw.origin, workspace, step.task.ID, f)
			if f != nil {
				_ = f.Close()
			}
			outcome.Seconds = time.Since(started).Seconds()
			if ferr != nil {
				outcome.ExitCode = 1
				_ = os.WriteFile(agentLog, []byte(ferr.Error()+"\n"), 0o644)
			}
		} else {
			cmd, err := agentCommand(agent, gw.addr(), workspace, step.task.Prompt, *model, *userTools)
			if err != nil {
				gw.Close()
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			cmd.Env = append(cmd.Env, "TMPDIR="+scratch)
			minutes := step.task.TimeoutMinutes
			if *timeoutFlag != "" {
				n, err := strconv.Atoi(*timeoutFlag)
				if err != nil || n < 1 {
					gw.Close()
					fmt.Fprintln(os.Stderr, "--timeout-min must be a positive integer")
					return 2
				}
				minutes = n
			}
			outcome = runProc(ctx, cmd.File, cmd.Args, cmd.Dir, cmd.Env, agentLog, time.Duration(minutes)*time.Minute, nil)
		}
		usage, merr := gw.meter()
		gw.Close()
		if logFile != nil {
			_ = logFile.Close()
		}
		if merr != nil {
			fmt.Fprintf(os.Stderr, "\nmeter: %v\n", merr)
		}
		verdict, verr := verify(step.task, workspace, filepath.Join(runDir, "verify.log"))
		if verr != nil {
			fmt.Fprintf(os.Stderr, "\nverify: %v\n", verr)
		}
		gitQuiet(workspace, "add", "-A")
		_ = runProc(context.Background(), "git", []string{"diff", "--cached", "--stat"}, workspace, nil, filepath.Join(runDir, "diff.stat"), 30*time.Second, nil)

		home, _ := os.UserHomeDir()
		isolation := Audit(agent, agentLog, sandbox, home)
		record := usage
		record.Task = step.task.ID
		record.Agent = agent
		record.AgentModel = *model
		record.UserTools = *userTools
		record.Mode = step.mode
		record.Rep = step.rep
		record.ExitCode = outcome.ExitCode
		record.TimedOut = outcome.TimedOut
		record.Seconds = outcome.Seconds
		record.Passed = verdict.Passed
		record.Total = verdict.Total
		record.Score = verdict.Score
		record.Solved = verdict.Solved
		record.Failed = verdict.Failed
		record.VerifyTimedOut = verdict.TimedOut
		record.Isolation = &isolation
		if *keep {
			record.Workspace = workspace
		}
		if record.Modes == nil {
			record.Modes = map[string]int{}
		}
		runs = append(runs, record)
		if err := writeRuns(filepath.Join(outDir, "runs.jsonl"), runs); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		if !*keep {
			_ = os.RemoveAll(sandbox)
		}
		sandbox = ""
		extra := ""
		if record.TimedOut {
			extra += " (agent timed out)"
		}
		if isolation.Contaminated {
			extra += " (CONTAMINATED, excluded: read " + strings.Join(isolation.ForeignReads, ", ") + ")"
		}
		fmt.Printf("%d/%d checks, %d requests, %s in / %s out, %d s%s\n",
			record.Passed, record.Total, record.Requests, commaInt(record.Input), commaInt(record.Output), int(record.Seconds+0.5), extra)
	}
	report := Summarize(runs, prices)
	if err := os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(report), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("\n" + report)
	return 0
}

func (g *gateway) addr() string {
	if g == nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(g.origin, "http://"), "https://")
}

func gatewayHost(agent string) host.ID {
	if agent == "fake" {
		return host.Codex
	}
	h, err := host.Parse(agent)
	if err != nil {
		return host.Codex
	}
	return h
}

func upstreamFor(agent string) map[string]string {
	switch agent {
	case "fake":
		return map[string]string{"CODEX_UPSTREAM": strings.TrimRight(os.Getenv("BENCH_FAKE_UPSTREAM"), "/")}
	case "codex":
		return map[string]string{"CODEX_UPSTREAM": codexUpstream()}
	case "claude":
		if os.Getenv("ANTHROPIC_UPSTREAM") == "" {
			if u := os.Getenv("JEV_CLAUDE_UPSTREAM_BASE_URL"); u != "" {
				return map[string]string{"ANTHROPIC_UPSTREAM": u}
			}
		}
	case "grok":
		if os.Getenv("GROK_OAUTH_UPSTREAM") == "" {
			if u := os.Getenv("JEV_GROK_UPSTREAM_BASE_URL"); u != "" {
				return map[string]string{"GROK_OAUTH_UPSTREAM": u}
			}
		}
	case "devin":
		if os.Getenv("DEVIN_UPSTREAM") == "" {
			if u := os.Getenv("JEV_DEVIN_UPSTREAM_BASE_URL"); u != "" {
				return map[string]string{"DEVIN_UPSTREAM": u}
			}
		}
	}
	return nil
}

func codexUpstream() string {
	if u := os.Getenv("JEV_CODEX_UPSTREAM_BASE_URL"); u != "" {
		return u
	}
	if u := os.Getenv("CODEX_UPSTREAM"); u != "" {
		return u
	}
	home, _ := os.UserHomeDir()
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	raw, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err == nil {
		var auth struct {
			AuthMode string `json:"auth_mode"`
			Tokens   any    `json:"tokens"`
			APIKey   string `json:"OPENAI_API_KEY"`
		}
		if json.Unmarshal(raw, &auth) == nil && (auth.AuthMode == "chatgpt" || (auth.Tokens != nil && auth.APIKey == "")) {
			return "https://chatgpt.com/backend-api/codex"
		}
	}
	return "https://api.openai.com/v1"
}

func gitQuiet(dir string, args ...string) {
	_ = runProc(context.Background(), "git", args, dir, nil, "", 30*time.Second, nil)
}

func selectTasks(tasks []Task, ids string) ([]Task, error) {
	if strings.TrimSpace(ids) == "" {
		return tasks, nil
	}
	var out []Task
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		var found *Task
		for i := range tasks {
			if tasks[i].ID == id {
				found = &tasks[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("unknown task %q (run with --list)", id)
		}
		out = append(out, *found)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tasks selected")
	}
	return out, nil
}

func parseModes(s string) ([]string, error) {
	var modes []string
	for _, mode := range strings.Split(s, ",") {
		mode = strings.TrimSpace(mode)
		if mode == "" {
			continue
		}
		if mode != "on" && mode != "off" {
			return nil, fmt.Errorf("--modes takes on, off, or on,off")
		}
		modes = append(modes, mode)
	}
	if len(modes) == 0 {
		return nil, fmt.Errorf("--modes takes on, off, or on,off")
	}
	return modes, nil
}

func parsePrices(s string) (*Prices, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 3 && len(parts) != 4 {
		return nil, fmt.Errorf("--prices takes in,cached,out[,cachewrite] USD per million tokens")
	}
	var nums [4]float64
	for i, part := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("--prices: %w", err)
		}
		nums[i] = n
	}
	if len(parts) == 3 {
		nums[3] = 1.25 * nums[0] // Anthropic 5-minute cache write price
	}
	return &Prices{Input: nums[0], Cached: nums[1], Output: nums[2], CacheWrite: nums[3], CacheWriteGiven: len(parts) == 4}, nil
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func writeRuns(path string, runs []RunRecord) error {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, run := range runs {
		if err := enc.Encode(run); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func loadRuns(dir string) ([]RunRecord, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		return nil, err
	}
	var runs []RunRecord
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var run RunRecord
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func reportCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: jev-routing bench report <results dir> [--prices in,cached,out[,cachewrite]]")
		return 2
	}
	dir := args[0]
	if _, err := os.Stat(filepath.Join(dir, "runs.jsonl")); err != nil {
		if found, _ := filepath.Glob(filepath.Join(dir, "*", "runs.jsonl")); len(found) > 0 {
			dir = filepath.Dir(found[len(found)-1]) // timestamp dirs sort lexically; last is newest
		}
	}
	pricesFlag := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--prices" && i+1 < len(args) {
			pricesFlag = args[i+1]
			i++
		}
	}
	prices, err := parsePrices(pricesFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	runs, err := loadRuns(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Print(Summarize(runs, prices))
	return 0
}

var (
	workdirRe = regexp.MustCompile(`(?m)^workdir: (.+)$`)
	cwdRe     = regexp.MustCompile(`"cwd":"([^"]+)"`)
)

func auditCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: jev-routing bench audit <results dir>...")
		return 2
	}
	home, _ := os.UserHomeDir()
	code := 0
	for _, dir := range args {
		runs, err := loadRuns(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
			continue
		}
		audited := 0
		var bad []string
		for i := range runs {
			logPath := filepath.Join(dir, fmt.Sprintf("%s.%s.%d", runs[i].Task, runs[i].Mode, runs[i].Rep), "agent.log")
			raw, err := os.ReadFile(logPath)
			if err != nil {
				continue
			}
			workspace := ""
			if m := workdirRe.FindSubmatch(raw); m != nil {
				workspace = string(m[1])
			} else if m := cwdRe.FindSubmatch(raw); m != nil {
				workspace = string(m[1])
			}
			if workspace == "" {
				continue
			}
			sandbox := workspace
			if strings.HasSuffix(workspace, "/workspace") {
				sandbox = strings.TrimSuffix(workspace, "/workspace")
			}
			iso := Audit(runs[i].Agent, logPath, sandbox, home)
			runs[i].Isolation = &iso
			if iso.Audited {
				audited++
			}
			if iso.Contaminated {
				bad = append(bad, fmt.Sprintf("%s.%s.%d -> %s", runs[i].Task, runs[i].Mode, runs[i].Rep, strings.Join(iso.ForeignReads, " ")))
			}
		}
		if err := writeRuns(filepath.Join(dir, "runs.jsonl"), runs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
			continue
		}
		fmt.Printf("%s: %d/%d audited, %d contaminated", dir, audited, len(runs), len(bad))
		if len(bad) > 0 {
			fmt.Printf(": %s", strings.Join(bad, "; "))
		}
		fmt.Println()
	}
	return code
}

func chartCmd(args []string) int {
	outDir := "charts"
	var dirs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--out" && i+1 < len(args) {
			outDir = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--") {
			fmt.Fprintf(os.Stderr, "unknown option %s\n", args[i])
			return 2
		}
		dirs = append(dirs, args[i])
	}
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: jev-routing bench chart <results dir> [<results dir> …] [--out charts]")
		return 2
	}
	var runs []RunRecord
	for _, dir := range dirs {
		loaded, err := loadRuns(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		runs = append(runs, loaded...)
	}
	if err := Chart(runs, outDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func fakeUpstreamCmd(args []string) int {
	fs := flag.NewFlagSet("fake-upstream", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.String("port", "8798", "listen port")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	origin, stop, err := ServeFakeUpstream("127.0.0.1:" + *port)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer stop()
	fmt.Printf("fake upstream on %s\n", origin)
	fmt.Fprintf(os.Stderr, "BENCH_FAKE_UPSTREAM=%s jev-routing bench --agent fake\n", origin)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	return 0
}

func claimLock() (func(), error) {
	tmp := os.TempDir()
	entries, _ := os.ReadDir(tmp)
	for _, entry := range entries {
		name := entry.Name()
		m := benchDirRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		if pid == 0 || !alive(pid) {
			_ = os.RemoveAll(filepath.Join(tmp, name))
		}
	}
	lock := filepath.Join(tmp, "jev-bench.lock")
	if raw, err := os.ReadFile(lock); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if pid != 0 && pid != os.Getpid() && alive(pid) {
			return nil, fmt.Errorf("another benchmark is running (pid %d). Series must not overlap: agents can read each other's workspaces", pid)
		}
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(lock) }, nil
}

var benchDirRe = regexp.MustCompile(`^jev-bench-(\d+)-`)

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
