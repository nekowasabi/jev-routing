package bench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
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
  --effort low|medium|high                reasoning effort for Claude/Codex (default medium)
  --user-tools                           keep your MCP servers, plugins, skills and settings
  --no-hooks                             Claude: disable the user's hooks (use with --user-tools)
  --catalog N                            add N bench MCP tools (first 2 return evidence; default 0)
  --tasks a,b                            task ids (default: chess suite)
  --modes on,off[,direct]                routing states to compare (default on,off)
  --on-mode filter|forced                what "on" means (default filter; "off" is always baseline)
  --claude-clear                         Claude "on": enable native context editing (clear_tool_uses_20250919)
                                          instead of JEV_CLAUDE_ADVISE, so the two are measured separately
  --claude-clear-trigger N               input_tokens trigger (default 100000)
  --claude-clear-at-least N              input_tokens clear_at_least (default 40000)
  --claude-clear-keep N                  tool_uses keep (default 3)
  --claude-clear-exclude names           comma-separated tool names the edit must never clear
  --claude-clear-gate off|jev            let Jev decide once per conversation whether to clear (default off)
  --reps N                               repetitions of every task in every mode (default 1)
  --min-pairs N                          paired repeats required for effect decision (default 6)
  --min-savings-pct P                    predeclared practical savings threshold (default 0)
  --timeout-min N                        override each task's own time limit
  --prices in,cached,out[,cachewrite]    USD per million tokens (cachewrite default 1.25×in)
  --port N                               port for the per-run proxy (default 8890)
  --out DIR                              results directory (default results/<timestamp>)
  --keep                                 keep the workspaces
  --list                                 show tasks and options

Tasks:
  child-facts    Delegate one fact to a child session (6 min)
  child-survey   Delegate an 8-file source survey to one child session (15 min)
  dual-facts     Obtain two independent facts from bench MCP tools (5 min; --catalog 2)
  skill-proof    Apply a routed skill and prove its use (5 min; Claude Code)
  xcell-module   Read go.mod facts from this source tree (10 min)
  xcell-locate   Locate five definitions in this source tree (10 min)
  chess-engine   Build a chess rules engine from a spec (30 min)
  chess-bugfix   Find and fix five injected bugs (20 min)
  chess-san      Add algebraic notation to a working engine (20 min)

Routing on is JEV_ROUTING_MODE=filter (or --on-mode forced). Routing off is baseline:
the proxy meters the request and does not rewrite it. Direct bypasses the proxy;
its full token total is unverified and cannot prove a saving.
The command writes comparison.json; load it in the local dashboard to view the token KPI.

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
	effort := fs.String("effort", "medium", "reasoning effort")
	userTools := fs.Bool("user-tools", false, "keep user tools")
	noHooks := fs.Bool("no-hooks", false, "Claude: disable the user's hooks")
	catalog := fs.Int("catalog", 0, "stub MCP tools")
	taskIDs := fs.String("tasks", "", "task ids")
	modeFlag := fs.String("modes", "on,off", "on,off")
	onMode := fs.String("on-mode", proxy.ModeFilter, "filter|forced")
	claudeClear := fs.Bool("claude-clear", false, "enable native context editing on Claude's on condition, instead of advise")
	claudeClearTrigger := fs.Int("claude-clear-trigger", proxy.DefaultOptions().ClaudeClearTrigger, "input_tokens trigger")
	claudeClearAtLeast := fs.Int("claude-clear-at-least", proxy.DefaultOptions().ClaudeClearAtLeast, "input_tokens clear_at_least")
	claudeClearKeep := fs.Int("claude-clear-keep", proxy.DefaultOptions().ClaudeClearKeep, "tool_uses keep")
	claudeClearExclude := fs.String("claude-clear-exclude", "", "comma-separated tool names to exclude from clearing")
	claudeClearGate := fs.String("claude-clear-gate", proxy.ClearGateOff, "off|jev: let Jev decide per conversation whether to clear (needs --claude-clear)")
	repsFlag := fs.String("reps", "1", "repetitions")
	minPairs := fs.Int("min-pairs", 6, "minimum paired repeats for an effect decision")
	minSavingsPct := fs.Float64("min-savings-pct", 0, "minimum practical token savings percent")
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
	if *model == "" {
		switch agent {
		case "claude":
			*model = "claude-sonnet-5"
		case "codex":
			*model = "gpt-5.6-terra"
		case "grok":
			// Why: without --model the Grok CLI resolves its own default at startup, and the first run of a series picked grok-4.6
			*model = "grok-4.7"
		}
	}
	if agent != "fake" {
		if _, err := host.Parse(agent); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	if *noHooks && agent != "claude" {
		fmt.Fprintln(os.Stderr, "--no-hooks is supported for claude only")
		return 2
	}
	if *effort != "low" && *effort != "medium" && *effort != "high" {
		fmt.Fprintln(os.Stderr, "--effort must be low, medium, or high")
		return 2
	}
	if _, err := catalogTools(*catalog); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *onMode != proxy.ModeFilter && *onMode != proxy.ModeForced {
		fmt.Fprintf(os.Stderr, "--on-mode must be filter or forced, not %q\n", *onMode)
		return 2
	}
	if *claudeClearGate != proxy.ClearGateOff && (*claudeClearGate != proxy.ClearGateJev || !*claudeClear) {
		fmt.Fprintf(os.Stderr, "--claude-clear-gate must be off, or jev together with --claude-clear, not %q\n", *claudeClearGate)
		return 2
	}
	// Why: omit the gate from records and compare keys while off, so runs
	// recorded before this flag existed keep pairing with new ones.
	clearGate := ""
	if *claudeClearGate != proxy.ClearGateOff {
		clearGate = *claudeClearGate
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
	for _, task := range chosen {
		if task.ID == "dual-facts" && agent != "fake" && (*catalog < 2 || (agent != "claude" && agent != "codex")) {
			fmt.Fprintln(os.Stderr, "dual-facts requires Claude or Codex with --catalog 2 or greater")
			return 2
		}
		if task.ID == "skill-proof" && agent != "fake" && agent != "claude" {
			fmt.Fprintln(os.Stderr, "skill-proof currently requires Claude Code")
			return 2
		}
	}
	modes, err := parseModes(*modeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if agent == "fake" && contains(modes, "direct") {
		fmt.Fprintln(os.Stderr, "direct requires a real agent")
		return 2
	}
	reps, err := strconv.Atoi(*repsFlag)
	if err != nil || reps < 1 {
		fmt.Fprintln(os.Stderr, "--reps must be a positive integer")
		return 2
	}
	if *minPairs < 6 || *minSavingsPct < 0 || *minSavingsPct >= 100 {
		fmt.Fprintln(os.Stderr, "--min-pairs must be at least 6 and --min-savings-pct must be in [0,100)")
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
	controlled := map[string]string{
		"JEV_COMPACTION": "off", "JEV_REASONING": "preserve", "JEV_AUTO_APPLY": "off",
		"JEV_KIND_MODES": "skill=observe,mcp_tool=observe,cli=observe,plugin=observe",
	}
	if _, set := os.LookupEnv("JEV_SELECTION_MODE"); !set && agent != "fake" {
		controlled["JEV_SELECTION_MODE"] = "jev"
	}
	restore := overrideBenchEnv(controlled)
	defer restore()
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
	catalogNote := ""
	if *catalog > 0 {
		catalogNote = fmt.Sprintf(" (catalog: %d stub MCP tools)", *catalog)
	}
	fmt.Printf("%d run%s with %s%s; results in %s\n\n", len(plan), plural, agent, catalogNote, outDir)
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
		if *keep {
			if err := os.WriteFile(filepath.Join(sandbox, ".bench-keep"), nil, 0o600); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
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

		sandboxMethod := "none"
		if agent != "fake" && bwrapAvailable() {
			sandboxMethod = "bwrap"
		}
		// Why: captured right before the agent runs, so Audit can tell paths
		// this run made outside its sandbox from paths that were already
		// there (see inspect's tmpSnapshot use). Under bwrap the run's /tmp
		// is its own empty tmpfs (see sandbox.go's bwrapWrap): nothing could
		// already be in it, so every /tmp path is this run's own and no
		// host snapshot is needed. Only the fallback shares the host /tmp
		// across runs.
		tmpSnapshot := map[string]bool{}
		if sandboxMethod != "bwrap" {
			tmpSnapshot = snapshotTmp()
		}

		fmt.Printf("[%d/%d] %s … ", i+1, len(plan), label)
		logFile, _ := os.Create(filepath.Join(runDir, "gateway.log"))
		var logW io.Writer = io.Discard
		if logFile != nil {
			logW = logFile
		}
		var gw *gateway
		gatewayEnv := map[string]string{}
		if step.task.ID == "skill-proof" {
			gatewayEnv["JEV_SKILL_DIR"] = filepath.Join(sandbox, "skills")
			if step.mode == "on" {
				gatewayEnv["JEV_AUTO_APPLY"] = "on"
				gatewayEnv["JEV_KIND_MODES"] = "skill=apply,mcp_tool=observe,cli=observe,plugin=observe"
			}
		}
		// Why: JEV_CLAUDE_ADVISE defaults off (docs/MEMO.md), which would make
		// Claude's "on" bench condition indistinguishable from "off". The bench
		// still needs advise applied to measure it, so force it on here only.
		// --claude-clear is a separate Claude "on" condition (native context
		// editing) and must not also enable advise, or the two effects mix.
		if agent == "claude" && step.mode == "on" && !*claudeClear {
			gatewayEnv["JEV_CLAUDE_ADVISE"] = "on"
		}
		if agent == "claude" && step.mode == "on" && *claudeClear {
			gatewayEnv["JEV_CLAUDE_CLEAR_TOOL_USES"] = "on"
			gatewayEnv["JEV_CLAUDE_CLEAR_TRIGGER"] = strconv.Itoa(*claudeClearTrigger)
			gatewayEnv["JEV_CLAUDE_CLEAR_AT_LEAST"] = strconv.Itoa(*claudeClearAtLeast)
			gatewayEnv["JEV_CLAUDE_CLEAR_KEEP"] = strconv.Itoa(*claudeClearKeep)
			if *claudeClearExclude != "" {
				gatewayEnv["JEV_CLAUDE_CLEAR_EXCLUDE"] = *claudeClearExclude
			}
			gatewayEnv["JEV_CLAUDE_CLEAR_GATE"] = *claudeClearGate
		}
		if step.mode == "direct" {
			// No proxy is constructed for the native-host control.
		} else if len(gatewayEnv) > 0 {
			err = withEnv(gatewayEnv, func() error {
				var startErr error
				gw, startErr = startGateway(gatewayHost(agent), fmt.Sprintf("127.0.0.1:%d", port), routingMode(step.mode == "on", *onMode), label, logW, upstreamFor(agent))
				return startErr
			})
		} else {
			gw, err = startGateway(gatewayHost(agent), fmt.Sprintf("127.0.0.1:%d", port), routingMode(step.mode == "on", *onMode), label, logW, upstreamFor(agent))
		}
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
			listen := ""
			if gw != nil {
				listen = gw.addr()
			}
			cmd, err := agentCommand(agent, listen, workspace, step.task.Prompt, *model, *effort, *userTools, *noHooks, *catalog, step.task.RequiresSubagent)
			if err != nil {
				if gw != nil {
					gw.Close()
				}
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			cmd.Env = append(cmd.Env, "TMPDIR="+scratch)
			minutes := step.task.TimeoutMinutes
			if *timeoutFlag != "" {
				n, err := strconv.Atoi(*timeoutFlag)
				if err != nil || n < 1 {
					if gw != nil {
						gw.Close()
					}
					fmt.Fprintln(os.Stderr, "--timeout-min must be a positive integer")
					return 2
				}
				minutes = n
			}
			file, args := cmd.File, cmd.Args
			if sandboxMethod == "bwrap" {
				file, args = bwrapWrap(file, args, sandbox)
			}
			outcome = runProc(ctx, file, args, cmd.Dir, cmd.Env, agentLog, time.Duration(minutes)*time.Minute, nil)
		}
		var usage RunRecord
		var merr error
		if gw != nil {
			usage, merr = gw.meter(step.task.ID)
		} else {
			usage.MeterError = "direct run has no proxy usage; complete host and child usage unverified"
		}
		if gw != nil && len(gw.snapshot) > 0 {
			if err := os.WriteFile(filepath.Join(runDir, "proxy-events.json"), gw.snapshot, 0o600); err != nil {
				gw.Close()
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		if gw != nil {
			gw.Close()
		}
		if logFile != nil {
			_ = logFile.Close()
		}
		if merr != nil {
			usage.MeterError = merr.Error()
			fmt.Fprintf(os.Stderr, "\nmeter: %v\n", merr)
		}
		verdict, verr := verify(step.task, workspace, filepath.Join(runDir, "verify.log"))
		if verr != nil {
			fmt.Fprintf(os.Stderr, "\nverify: %v\n", verr)
		}
		gitQuiet(workspace, "add", "-A")
		_ = runProc(context.Background(), "git", []string{"diff", "--cached", "--stat"}, workspace, nil, filepath.Join(runDir, "diff.stat"), 30*time.Second, nil)

		home, _ := os.UserHomeDir()
		isolation := Audit(agent, agentLog, sandbox, home, tmpSnapshot)
		isolation.SandboxMethod = sandboxMethod
		// Why: --keep means "leave the run's footprint for inspection"; a run
		// asked to keep its sandbox must also keep what it left in /tmp.
		if *keep {
			isolation.CleanupSkipped = true
		} else if sandboxMethod != "bwrap" {
			// Under bwrap there is nothing to clean: the run's /tmp was its
			// own tmpfs, already gone with the process, and never reached
			// the host's /tmp. Cleanup stays for the fallback (no bwrap).
			cleaned := cleanupOutsideTmp(isolation.Outside)
			isolation.Cleaned = cleaned.Cleaned
			isolation.CleanupFailed = cleaned.Failed
		}
		record := usage
		record.Task = step.task.ID
		record.Agent = agent
		record.AgentModel = *model
		record.AgentEffort = *effort
		record.EffectMinPairs = *minPairs
		record.EffectMinSavingsPct = *minSavingsPct
		record.ApprovalMode = map[string]string{"claude": "acceptEdits", "codex": "approve-for-me", "grok": "bypassPermissions", "devin": "dangerous", "fake": "none"}[agent]
		record.SourceRevision = sourceRevision()
		// Why: Instead of clearing CLAUDE_CODE_SUBAGENT_MODEL, keep it and key on
		// it. Reason: it is the user's real setting, and a child on another model
		// changes the measured tokens, so such runs must never pair.
		subagentModel := ""
		if agent == "claude" {
			subagentModel = os.Getenv("CLAUDE_CODE_SUBAGENT_MODEL")
		}
		record.CompareKey = computeCompareKey(compareKeySettings{
			Task: step.task.ID, Agent: agent, Model: *model, Effort: *effort,
			Approval: record.ApprovalMode, MinPairs: *minPairs, MinSavingsPct: *minSavingsPct,
			UserTools: *userTools, NoHooks: *noHooks, Catalog: *catalog, Source: record.SourceRevision,
			Selection: opt.SelectionMode, Reasoning: opt.Reasoning, Compaction: opt.Compaction,
			Transforms: opt.Transforms, CostGate: opt.CostGateMax, KindModes: opt.KindModes,
			ApplicationPolicy: opt.ApplicationPolicy, Shadow: opt.Shadow,
			ClaudeClear: *claudeClear, ClaudeClearTrigger: *claudeClearTrigger,
			ClaudeClearAtLeast: *claudeClearAtLeast, ClaudeClearKeep: *claudeClearKeep,
			ClaudeClearExclude: *claudeClearExclude,
			ClaudeClearGate:    clearGate,
			SubagentModel:      subagentModel,
			// Why: bwrap and the fallback give different isolation
			// guarantees; mixing their runs into one comparison would
			// average over that difference instead of reporting it.
			SandboxMethod: sandboxMethod,
		})
		record.SubagentModel = subagentModel
		record.UserTools = *userTools
		record.NoHooks = *noHooks
		record.Catalog = *catalog
		if agent == "claude" && *claudeClear {
			record.ClaudeClear = true
			record.ClaudeClearTrigger = *claudeClearTrigger
			record.ClaudeClearAtLeast = *claudeClearAtLeast
			record.ClaudeClearKeep = *claudeClearKeep
			record.ClaudeClearExclude = *claudeClearExclude
			record.ClaudeClearGate = clearGate
		}
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
		if agent == "devin" {
			transcript := filepath.Join(sandbox, "devin-transcript.json")
			if hostUsage, err := readDevinTranscript(transcript); err != nil {
				record.HostTranscriptError = err.Error()
			} else {
				record.HostTranscript = hostUsage
			}
			if !*keep {
				_ = os.Remove(transcript)
			}
		}
		if step.mode != "direct" && (agent == "claude" || agent == "codex") {
			mainKey, err := matchHostSession(record, agentLog)
			if err == nil {
				record.HostUsageVerified = true
				classifySessions(&record, mainKey)
				if step.task.RequiresSubagent && record.ChildSessions != record.SubagentCalls {
					record.ParentChildVerified = false
				}
				if record.ParentChildVerified {
					record.AttributionMethod = "session_key"
				}
			}
			// Why: Instead of partitioning only when session matching fails, also
			// partition a Claude run whose session keys found no child. Reason:
			// Claude Code's child shares the parent's key, and when the child runs
			// on another model (CLAUDE_CODE_SUBAGENT_MODEL) the parent-only CLI
			// usage still matches the main model, so session matching succeeds
			// without ever separating the child.
			if step.task.RequiresSubagent && !record.ParentChildVerified && record.EvidenceComplete && record.SubagentCalls == 1 && (err != nil || agent == "claude") {
				partition := func() ([]int64, []int64, int, error) {
					if agent == "codex" {
						return codexChildAttribution(agentLog, gw.snapshot)
					}
					parent, child, tokens, err := claudeChildAttribution(agentLog, gw.snapshot)
					if err == nil {
						return parent, child, tokens, nil
					}
					// Keep the subset search that verified short runs before.
					if parent, child, tokens, fallbackErr := partitionHostRequests(agent, agentLog, gw.snapshot); fallbackErr == nil {
						return parent, child, tokens, nil
					}
					return nil, nil, 0, err
				}
				parent, child, tokens, partitionErr := partition()
				if partitionErr == nil {
					record.HostUsageVerified = true
					record.ParentChildVerified = true
					record.ChildSessions = record.SubagentCalls
					record.ChildTokens = tokens
					record.ParentRequestSeqs = parent
					record.ChildRequestSeqs = child
					record.AttributionMethod = "usage_partition"
				} else if err != nil {
					err = partitionErr
				}
			}
			if !record.HostUsageVerified {
				if record.MeterError != "" {
					record.MeterError += "; "
				}
				record.MeterError += err.Error()
			}
		}
		if step.mode != "direct" && agent == "grok" && step.task.RequiresSubagent {
			parent, child, tokens, err := grokChildAttribution(agentLog, gw.snapshot)
			if err != nil {
				if record.MeterError != "" {
					record.MeterError += "; "
				}
				record.MeterError += err.Error()
			} else {
				record.HostUsageVerified = true
				record.ParentChildVerified = true
				record.ChildSessions = record.SubagentCalls
				record.ChildTokens = tokens
				record.ParentRequestSeqs = parent
				record.ChildRequestSeqs = child
				record.AttributionMethod = "usage_partition"
			}
		} else if step.mode != "direct" && agent == "grok" {
			if model, err := matchGrokHostUsage(record, agentLog, gw.snapshot); err != nil {
				if record.MeterError != "" {
					record.MeterError += "; "
				}
				record.MeterError += err.Error()
			} else {
				record.AgentModel = model
				record.HostUsageVerified = true
			}
		}
		if step.task.ID == "dual-facts" && agent == "codex" && !record.EvidenceComplete {
			record.EvidenceComplete = codexDualFactEvidence(agentLog)
		}
		if step.task.ID == "xcell-locate" && agent != "fake" {
			record.EvidenceComplete = xcellLocateEvidence(agent, agentLog, workspace)
		}
		if step.task.ID == "child-survey" && agent == "claude" {
			record.EvidenceComplete = record.EvidenceComplete && childSurveyEvidence(agentLog)
		}
		assignTaskUsage(&record)
		applyClearNet(outDir, &record)
		if *keep {
			record.Workspace = workspace
		}
		if record.Modes == nil {
			record.Modes = map[string]int{}
		}
		runs = append(runs, record)
		if err := writeRuns(filepath.Join(outDir, "runs.jsonl"), runs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := WriteComparisonJSON(filepath.Join(outDir, "comparison.json"), runs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
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
	fmt.Println("Dashboard data:", filepath.Join(outDir, "comparison.json"))
	return 0
}

func overrideBenchEnv(values map[string]string) func() {
	type oldValue struct {
		value string
		set   bool
	}
	old := make(map[string]oldValue, len(values))
	for key, value := range values {
		before, set := os.LookupEnv(key)
		old[key] = oldValue{before, set}
		_ = os.Setenv(key, value)
	}
	return func() {
		for key, before := range old {
			if before.set {
				_ = os.Setenv(key, before.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
}

// compareKeySettings is everything that must match between two runs for
// them to be considered the same condition (see BuildComparisons in
// comparison.go, which rejects pairs whose CompareKey differs).
type compareKeySettings struct {
	Task, Agent, Model, Effort, Approval, Source string
	MinPairs, Catalog, CostGate                  int
	MinSavingsPct                                float64
	UserTools, NoHooks, Shadow                   bool
	// SubagentModel is omitted when empty so keys of runs without it stay stable.
	SubagentModel                                           string `json:",omitempty"`
	Selection, Reasoning, Compaction, ApplicationPolicy     string
	Transforms                                              proxy.TransformOptions
	KindModes                                               map[string]string
	ClaudeClear                                             bool
	ClaudeClearTrigger, ClaudeClearAtLeast, ClaudeClearKeep int
	ClaudeClearExclude, SandboxMethod                       string
	ClaudeClearGate                                         string `json:",omitempty"`
}

// computeCompareKey hashes compareKeySettings; two runs get the same
// CompareKey iff every field above matches byte-for-byte.
func computeCompareKey(s compareKeySettings) string {
	settings, _ := json.Marshal(s)
	fingerprint := sha256.Sum256(settings)
	return hex.EncodeToString(fingerprint[:])
}

func sourceRevision() string {
	info, ok := debug.ReadBuildInfo()
	revision, modified := "unknown", "unknown"
	if ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value
			}
		}
	}
	if revision != "unknown" {
		return revision + ":" + modified
	}
	path, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "unknown"
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))[:16]
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
		var defaults []Task
		for _, task := range tasks {
			if strings.HasPrefix(task.ID, "chess-") {
				defaults = append(defaults, task)
			}
		}
		return defaults, nil
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
		if mode != "on" && mode != "off" && mode != "direct" {
			return nil, fmt.Errorf("--modes takes on, off, or direct")
		}
		modes = append(modes, mode)
	}
	if len(modes) == 0 {
		return nil, fmt.Errorf("--modes takes on, off, or direct")
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
	// Why: runs.jsonl from before this metric existed has no ClearNet*
	// fields; recompute them from each run's own proxy-events.json/agent.log
	// every time report runs, and persist so later reads (dashboard,
	// scripts) don't have to.
	for i := range runs {
		applyClearNet(dir, &runs[i])
	}
	if err := writeRuns(filepath.Join(dir, "runs.jsonl"), runs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := WriteComparisonJSON(filepath.Join(dir, "comparison.json"), runs); err != nil {
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
			// tmpSnapshot is nil: a past run's /tmp state at start time was
			// never recorded, so re-auditing falls back to the text heuristic.
			iso := Audit(runs[i].Agent, logPath, sandbox, home, nil)
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
	// Why: only one bench process may hold this lock at a time, so once it
	// is ours every leftover jev-bench-* directory is an orphan from a past
	// run and safe to sweep unconditionally. The old sweep instead trusted
	// the pid embedded in the directory name (removing it only when that
	// pid was not alive) — but pids wrap around and get reused, so a
	// directory left by a long-dead run whose pid happened to equal this
	// process's own pid was never swept. The next run's audit then found
	// it and misclassified that run as contaminated.
	entries, _ := os.ReadDir(tmp)
	for _, entry := range entries {
		name := entry.Name()
		if !benchDirRe.MatchString(name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(tmp, name, ".bench-keep")); err == nil {
			continue
		}
		_ = os.RemoveAll(filepath.Join(tmp, name))
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
