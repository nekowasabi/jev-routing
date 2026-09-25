# jev-routing

[日本語版はこちら](README_ja.md)

## What this is

jev-routing is a local proxy that sits between a coding-agent CLI (Claude Code / Codex / Grok Build / Devin CLI) and the upstream LLM API. It does not replace the agent: it accepts the requests the agent already sends, reshapes them right before they go out, and forwards them upstream.

Agent CLIs keep resending the same growing payload every turn: the full tool schema catalog, a swollen history of tool calls and results, and the previous step's thinking. That accumulation is the main source of latency, token spend, and wrong tool picks. jev-routing inserts four things at that point.

1. **History compaction** — only `tool_use` / `tool_result` are scored and then dropped or truncated. User and assistant prose is untouched and nothing is summarized
2. **One schema for tool selection** — Jev is asked for the next tool and for done in the same call, so the step's `tools[]` shrinks to a single schema (zero when the step is a plain response)
3. **thinking / reasoning stripping** — reasoning blocks that the next decision does not need are removed

What you get from adopting it:

- **Fewer bytes and tokens on the wire** — the per-request tool catalog and the bloated tool history go away
- **Fewer wrong tool picks** — the model only sees the tools that are meaningful at that moment
- **No paying for a big model on a small job** — model and effort are chosen to match the difficulty
- **Observability** — a loopback-only, read-only dashboard shows what was rewritten and what was not applied, and why
- **Low adoption cost** — a single Go binary. No Node, one environment variable on the agent side, and your existing logins keep working
- **Fail-safe design** — when uncertain it does not narrow the candidates, unsupported history shapes pass through unrewritten, and real tool execution and approval stay on the host

---

A Jev harness for Claude Code / Codex / **Grok Build** / **Devin CLI**. A single Go binary.

Before each request, the binary:

1. Drops / truncates the conversation's tool results using the same judgment as [fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction) (prose is never summarized)
2. Asks Jev for the next tool (Choice) and for done (Noul) at the same time
3. Reduces that step's `tools[]` to **one schema** (zero when the answer is respond)
4. Strips thinking / reasoning

## Installation

Node is not required. Go 1.22+.

```bash
go install github.com/nekowasabi/jev-routing/cmd/jev-routing@latest
```

From source:

```bash
git clone https://github.com/nekowasabi/jev-routing.git
cd jev-routing
go install ./cmd/jev-routing
```

The key is optional except when `JEV_SELECTION_MODE=jev`, which refuses to start without it. Without a key, `hybrid` and `local` use the on-device classifier.

```bash
export TYPESAFE_API_KEY=ts_...    # https://console.typesafe.ai/settings/keys
```

## Running

```bash
jev-routing run grok              # points GROK_CLI_CHAT_PROXY_BASE_URL at the proxy
jev-routing run claude            # points ANTHROPIC_BASE_URL at the proxy
jev-routing run codex             # connects through the proxy with OpenAI login
jev-routing run devin             # points DEVIN_API_URL at the proxy
```

`run` tries `127.0.0.1:8787` first and automatically picks a free port if it is in use. If `JEV_LISTEN` is set, that address takes precedence.

### Launching inside tmux

```bash
jev-routing run --tmux codex
```

Inside an attached tmux session it launches the host in the current pane, so tmux is not nested and the pane border stays single. From outside tmux, or from a state where only a stale `TMUX` environment variable remains, it creates an independent tmux session per run.

Your existing `grok login` / `claude login` / `codex login` / `devin auth` keep working as they are.

To set the environment by hand:

```bash
# Grok Build
unset XAI_API_KEY GROK_MODELS_BASE_URL
export GROK_CLI_CHAT_PROXY_BASE_URL=http://127.0.0.1:8787/v1
jev-routing serve --host grok &
grok

# Claude Code
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
jev-routing serve --host claude &
claude

# Devin CLI
export DEVIN_API_URL=http://127.0.0.1:8787
jev-routing serve --host devin &
devin
```

Only when starting Codex manually, configure `~/.codex/config.toml`.

```toml
model_provider = "jev"

[model_providers.jev]
name = "jev-routing"
base_url = "http://127.0.0.1:8787/v1"
wire_api = "responses"
requires_openai_auth = true
```


Devin CLI is assumed to use `/messages` and `/sessions` under `DEVIN_API_URL` (default `https://api.devin.ai`), with `prompt`/`message` + `tools[]` JSON. For Codex ChatGPT login, the `functions` namespace is expanded from `additional_tools` inside the Responses Lite `input`, and local tools are narrowed while keeping their original position. External namespaces and provider-side execution tools are left in place.

## Benchmark

`jev-routing bench` runs the chess tasks ported from [jev-gateway-bench](https://github.com/vinilana/jev-gateway-bench) (MIT, Copyright (c) 2026 Vinicius Lana), plus shared `x-cell`, two-tool, skill, and child-session tasks. In ordinary selection comparisons, on is `JEV_ROUTING_MODE=filter` (or `--on-mode forced`) and off is metered `baseline`. Codex context-limit comparisons use `baseline` on both sides and change only the host limit. `direct` bypasses the proxy and is always incomparable for total-token savings. Each run has a fresh workspace and an external verifier.

```bash
jev-routing bench --list
jev-routing bench selftest
jev-routing bench --agent fake --tasks chess-bugfix --reps 1
jev-routing bench --agent codex --tasks chess-bugfix --reps 1
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks dual-facts --catalog 2 --reps 2
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks skill-proof --reps 1
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks child-facts --reps 1
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks xcell-module --modes direct,off,on
jev-routing bench --agent codex --model gpt-5.6-terra --effort medium --tasks compact-facts --modes off,on --codex-compact-baseline-limit 900000 --codex-compact-limit 55000 --reps 6
jev-routing bench report results/<dir> --prices 1.25,0.125,10
```

Agents are `codex`, `claude`, `grok`, `devin`, and `fake`. Real agents spend real quota. Start with one task and `--reps 1`. `--agent fake` checks the pipeline without a model. Live Jev intervention requires a TypeSafe key; set `JEV_SELECTION_MODE=local` to measure only the on-device classifier. Node.js is required to score chess tasks.

`--prices` takes `in,cached,out[,cachewrite]` in USD per million tokens; cache write defaults to 1.25× input and applies only to `claude`. Input totals include cache: for `claude` that adds the cache reads and cache writes Anthropic reports apart from input; for `codex` and `grok` cached tokens are already inside input.

Results land in `results/<timestamp>/` (`runs.jsonl`, `comparison.json`, `summary.md`, and a directory per run). `comparison.json` records total tokens and savings only for pairs with matching settings, successful quality checks, required application or task evidence when applicable, and complete usage. Total tokens (`baselineTokens`/`selectionTokens`/`savedTokens`, and `summary.md`'s "Total tokens (upstream), median") counts only the parent/child agent CLI's upstream input (including cache) plus upstream output; Jev's own input/output (`jevInput`/`jevOutput`) is recorded separately and excluded from this total and from the effect decision, and a gap in Jev's own usage no longer makes a pair incomparable. Codex `compact-facts` compares the host's context limits with Jev selection and replacement disabled; it checks full reads of 20 files and scores the answer. A run without an explicit compact request still counts in that setting comparison, with request counts reported separately. Other incomparable pairs carry reasons and no savings number. Claude Code and Codex main-model usage is reconciled against their CLI totals; a child-session task additionally requires unique parent/child attribution. Additional model calls such as Codex auto-review remain in the proxy total. Grok's complete headless CLI usage can reconcile a canceled response without assigning it a fabricated per-request zero. Devin's validated ATIF step totals can supply a session total when the Connect responses omit usage. Child runs remain incomparable unless every child's usage and model are attributable. Per-run `proxy-events.json` is a local diagnostic record. `bench audit` re-reads agent logs.

### How to measure

```bash
jev-routing bench --agent codex --model gpt-5.6-terra --reps 4 --catalog 40 --prices 1.25,0.125,10
JEV_TRANSFORMS=filter=off jev-routing bench --agent codex --model gpt-5.6-terra --reps 4 --catalog 40 --modes on --prices 1.25,0.125,10
```

- Pin the model with `--model` and reasoning with `--effort`. Use comparable pairs in `comparison.json` as the measured token KPI; `summary.md`, uncached input, cost, and wall-clock time are diagnostic. Rewriting history or tools can shrink the input while disrupting the provider's prompt cache.
- Use an even `--reps`, at least 6 for an effect decision. The on/off order alternates per rep, so an even count balances which mode runs second. With `--reps 1`, on always runs first.
- `--catalog N` (`codex` and `claude` only) adds a reproducible MCP catalog. Its first two tools return separate facts for `dual-facts`; remaining tools are error-returning distractors. Prefer it over `--user-tools`, which depends on the local setup.
- `JEV_TRANSFORMS` (e.g. `filter=off`, `compact=off`) ablates the on runs only; off is always the baseline. On-only results need a matching off baseline before a relative chart can be generated.
- Per-request decisions are in `results/<dir>/<task>.<mode>.<rep>/gateway.log` (tools before→after, chosen, apply, conf).
- `compact-facts` uses an explicitly low Codex limit to exercise context management; its short task does not establish savings for long sessions at the normal limit.

## Compaction

The history-compaction judgment was ported to Go with reference to [tamaratran/fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction) ([MIT License](https://github.com/tamaratran/fast-jev-compaction/blob/main/LICENSE), Copyright (c) 2025). It does not summarize; it follows the same drop / truncate contract for `tool_use` / `tool_result`. The state Jev sees is fitted in the same stages as the current library: tool inputs capped at 1000, then 200, then 60 characters, long texts abridged, old texts collapsed, old calls reduced to one line, then old call-less entries left out.

- User and assistant prose is never touched
- Only tool_use and tool_result are scored with noul
- `keepResult` → keep both
- `keepCall` only → truncate the result to the first 300 characters
- Both below threshold → drop both
- The first and the most recent entries are pinned

Codex and Grok Build cannot return a replacement transcript from `PreCompact` the way Claude Code's `session.compact` hook can.

- Codex local compaction (`codex-rs/core/src/compact.rs`) runs when the provider is not OpenAI/Azure (`RemoteCompactionSupport::Unsupported` in `model-provider`). It asks for a `CONTEXT CHECKPOINT COMPACTION` summary, then keeps recent user messages plus that summary. Tool results are not items in the replacement history.
- Grok Build full-replace (`xai-grok-compaction` `code_compaction`) rebuilds `[system, user prefix, AGENTS.md, last query, recent tail, summary]`. The summary must be one `<summary>` block of numbered sections, at least 500 characters after cleaning. Older tool calls survive only inside that block.

Codex compaction requests pass through to Codex's own summarizer by default. `JEV_CODEX_NATIVE_COMPACTION=on` enables the experimental retained-transcript replacement; it falls back to the host summarizer when the returned text is less than 25% shorter than the original history. Grok compaction requests receive a `<summary>` block of the retained transcript. Claude is compacted only when Claude Code sends its own compaction request; the answer is likewise a `<summary>` of the retained transcript, and when the reduction is under 25% the request goes upstream for Claude Code's own summary. Ordinary turns are not compacted.

Even when tool selection is uncertain, history compaction is still applied where it is safe to do so. Claude's `system` boundary, signed thinking, tool references, and the pairing of calls with their results are all preserved.

```bash
jev-routing compact < transcript.json
```

## Supported tools

The proxy treats the runtime catalog included in the request as the source of truth and never invents unknown tools. The table below lists the built-in names the selection logic maps to roles. Tools added by MCP, Skills, and Plugins are handled under the names given in the runtime catalog.

| Role | Claude Code | Codex | Grok Build | Devin CLI |
|---|---|---|---|---|
| Read | Read | read_file | read_file | read |
| Edit | Edit | apply_patch | search_replace | edit |
| Write | Write | add_file | write | write |
| Shell | Bash | exec_command | run_terminal_cmd | exec |
| Search | Grep / Glob | grep_files / list_dir | grep_search / list_dir | grep / glob |
| Web | WebSearch / WebFetch | web_search / web_fetch | web_search / web_fetch | web_search / webfetch |
| Subagent | Agent | spawn_agent | task | run_subagent / read_subagent |
| Task management | TodoWrite | update_plan | todo_write / get_task_output / kill_task | todo_write |
| MCP | ToolSearch / MCP tools | `mcp__<server>__<tool>` | search_tool / use_tool | mcp_list_tools / mcp_call_tool / mcp_read_resource |

### Scope per product

- [Claude Code](https://code.claude.com/docs/en/tools-reference): the `tool_use` / `tool_result` history shapes are accepted. Built-in names vary with the runtime and feature flags, so they are not kept as a fixed allow list. The tool catalog is not narrowed for Claude, because tool definitions sit at the head of Anthropic's prompt cache; the decision is appended to the last tool-result message as a reminder instead.
- Codex: the history shapes for `functions.*`, `custom_tool_call`, the Responses built-in tools, and MCP calls are accepted.
- [Grok Build](https://docs.x.ai/build/features/permissions): `read_file`, `search_replace`, `grep_search`, `list_dir`, `run_terminal_cmd`, `web_search`, `web_fetch`, `todo_write`, `task`, `kill_task`, `get_task_output`, `memory_search`, `memory_get`, `search_tool`, `use_tool`, `lsp`, and conditionally `write` are handled from the runtime catalog.
- [Devin CLI](https://docs.devin.ai/cli/reference/permissions#tool-based-permissions): `read`, `write`, `edit`, `apply_patch`, notebooks, search, shell, `webfetch`, tasks, Skills, subagents, permissions, and MCP management tools are handled from the runtime catalog. The ATIF export format will not be added to the history judgment on speculation until its public schema can be confirmed.

For history shapes, Claude's `tool_use` / `tool_result`, the `*_call` shapes of Codex and Responses, and MCP's `mcp_call` are explicitly accepted. History containing images passes through on the safe side.

## Dashboard

The read-only `http://127.0.0.1:<port>/dashboard` can be opened only while listening on loopback. On a public listener it returns 404. Settings cannot be changed from the page. Pricing is not shown. No CORS headers are added, and only GET is accepted.

```bash
jev-routing run --dashboard grok
JEV_SELECTION_MODE=local jev-routing serve --host codex --listen 127.0.0.1:8787
```

`run --dashboard` opens the dashboard in a browser after startup. With `serve`, open the same URL by hand. The page refreshes every 2 seconds and covers only the current process.

- Routing overview (counts by decision source and by application)
- Status of the six categories (model and effort, subagents, skills, MCP, CLI, plugins, compaction). Unobserved stays unobserved
- Application list, covering the proxy's rewrites (subagents, skills, MCP, CLI, plugins)
- Token consumption aggregated from upstream responses (input, output, cache, reasoning)
- Recent requests (sequence number, host, decision source, application, selected tool, reason, changes, tool substitution, jev, tokens, time)
- Filters by host / decision source / application, and per-row detail (decision ID, operation ID). j/k moves between rows, Enter opens the detail, r reconnects
- Selecting saved `comparison.json` at the top of the page shows measured token savings, quality, comparable-pair count, and reasons. The file stays in the browser.

The browser side keeps up to 1000 entries and the table shows the most recent 200. When the connection drops it shows the last update time and "disconnected", and reconnecting re-fetches the history. `?sample=1` shows mock values for checking the display and is labeled as a sample on the page.

## Comparison experiments (disabled by default)

These are read once at startup. Invalid values make startup fail.

| Variable | Values | Default |
|---|---|---|
| `JEV_ROUTING_MODE` | `baseline` / `filter` / `forced` | `filter` |
| `JEV_COMPACTION` | `off` / `on` | `on` |
| `JEV_CODEX_NATIVE_COMPACTION` | `off` / `on` | `off` (experimental Codex compaction replacement; Codex uses its own summary by default) |
| `JEV_CODEX_TOOL_OUTPUT_TRUNCATE` | `off` / `on` | `on` (truncates any Codex tool output over 20000 bytes, keeping the head and tail and omitting the middle; applied the same way to the entire resent history every request, so the prompt cache prefix survives) |
| `JEV_REASONING` | `preserve` / `legacy` | `legacy` |
| `JEV_SELECTION_MODE` | `local` / `jev` / `hybrid` | `hybrid` |
| `JEV_SHADOW` | `on` / `off` | `off` |
| `JEV_CLAUDE_ADVISE` | `on` / `off` | `off` (Claude's advise path skips Jev and passes the request through unchanged) |
| `JEV_CLAUDE_CLEAR_TOOL_USES` | `on` / `off` | `off` (appends Anthropic's native [context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing) `clear_tool_uses_20250919` edit to Claude requests) |
| `JEV_CLAUDE_CLEAR_TRIGGER` | integer, `input_tokens` | `100000` |
| `JEV_CLAUDE_CLEAR_AT_LEAST` | integer, `input_tokens` | `40000` |
| `JEV_CLAUDE_CLEAR_KEEP` | integer, `tool_uses` | `3` |
| `JEV_CLAUDE_CLEAR_EXCLUDE` | comma-separated tool names | empty (omits `exclude_tools`) |
| `JEV_CLAUDE_CLEAR_GATE` | `off` / `jev` | `off` (`jev`: once a conversation's last context reaches the trigger and the request holds at least `clear_at_least` estimated clearable tool-result tokens, ask Jev once whether earlier tool outputs will be needed again, and add the edit only if not; Jev failure or no key means no clearing) |
| `JEV_TRANSFORMS` | `compact=on/off,filter=on/off,criteria=on/off` | `compact=on,filter=on,criteria=off` |
| `JEV_COST_GATE_MAX` | integer ≥ 0 | `3` |
| `JEV_ARGS_MODEL` + `JEV_ARGS_TOOLS` | a model identifier and comma-separated exact-match names | empty (disabled) |
| `JEV_DIRECT_TOOLS` | allowed names of Chat functions with no arguments / constant arguments | empty (disabled) |
| `JEV_RUN_ID` | ID for comparison | auto-generated |
| `JEV_AUTO_APPLY` | `on` / `off` | `off` (the `examples/*.sh` adoption samples use `on`) |
| `JEV_APPLICATION_POLICY` | `required` / `fallback` | `required` only when auto-apply is newly enabled |
| `JEV_KIND_MODES` | e.g. `skill=apply,mcp_tool=observe` | new kinds default to `observe`, ateam to `fixed` |

`forced` pins `tool_choice` only for requests that have a verified, real Jev answer. Local scoring alone never forces it. `JEV_ARGS_MODEL` is for `forced` only and transparently swaps just the sending model for allowed tools. Pricing and compatibility are never guessed. `JEV_DIRECT_TOOLS` is effective only together with `forced` and cannot be combined with ARGS_MODEL. Out-of-scope requests and invalid schemas are returned upstream. Real tool execution and approval remain with the host. There is no automatic retry on upstream rejection.

`JEV_SELECTION_MODE=local` uses local rules only and never asks Jev for a selection. `jev` delegates eligible selections to Jev, and if Jev is unconfigured, invalid, uncertain, or fails, the candidates are not narrowed. `hybrid` uses only the local rules that are conclusive and hands the pending cases, such as word matches, to Jev. If Jev is not connected, the candidates are not narrowed.

`JEV_SHADOW=on` scores the candidate set without rewriting the request. `JEV_TRANSFORMS` turns compaction, tool-catalog filtering, and contrast criteria on or off independently; with `filter=off` ordinary requests pass through unchanged without a Jev call, and criteria stays off until a confused pair is registered. `JEV_COST_GATE_MAX` skips the classifier when the candidate count is at most this value.

On ordinary proxy requests the same decision function is called automatically, and it goes on to supply the body of the selected skill, invoke MCP/CLI, and verify the result. When `JEV_AUTO_APPLY=on`, only targets whose per-kind mode is `apply` are started, and under `required` a non-delivery, an unsupported case, or an unconsumed selection is not treated as a successful exit. `fallback` reverts to the legacy settings only when explicitly specified. A selection log or narrowed candidates alone do not count as an application having completed. Unknown executions are never retried automatically. The dashboard shows the movable points, the reasons for non-application, and the comparison effects in Japanese. Missing data and "no comparison" are left as missing / no comparison, and mock values are labeled as samples.

A mock run is not evidence of approval compatibility on a real host, nor of a real-service speedup or cost improvement. Read/search versus free-form commands and diffs should be evaluated as separate tasks.

## Verification

```bash
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./...
go vet ./...
node --test internal/proxy/assets/dashboard.test.mjs
python3 -m unittest discover -s scripts -p 'test_summarize_x_cell.py'
python3 -m unittest discover -s scripts -p 'test_summarize_selection_benchmark.py'
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
```

## Measured comparison

This is not part of the normal test suite. Each target is run once in a separate worktree built from the same commit, and the token usage and elapsed time of the bare CLI versus going through `jev-routing` are saved as JSON.

```bash
make test-x-cell           # Claude Code → Codex → Grok Build → Devin
make test-x-cell claude    # one product only
make test-selection-benchmark claude # compare baseline/local/jev/hybrid on one product
```

Results land in `artifacts/x-cell/<datetime>/<host>/comparison.json`. Each host also appends one JSON line to `~/.local/state/jev-routing/x-cell.jsonl` (`JEV_XCELL_LOG` overrides the path). The line keeps the commit, each mode's timing and token counts, and the reduction when the run is comparable, so repeated runs can be compared over time. Runs that are not comparable are kept too. Use only results with `comparable: true` (`valid: true`) for comparison. Not reaching the proxy, `rewritten=0` (passthrough only), or a mismatch in the completion condition yields `comparable: false` and no reduction figures. Billed tokens vary a lot with cache state across independent sessions, so they are not compared on a single run. Instead, `routing_request_chars` (the reduction in bytes of the JSON body the proxy actually received and sent upstream) and the differences in output tokens and runtime are recorded. Codex with ChatGPT login uses `-m gpt-5.6-terra` (override with `CODEX_MODEL`). The short name `terra` returns 400.

`make test-selection-benchmark` fixes `JEV_COMPACTION=off` and `JEV_REASONING=preserve` for all proxy conditions. If the history contains an unsupported content type (for example Claude's `tool_addition`), the result is `unknown_history` and it passes through unrewritten. That result is a normal fail-safe stop: even if external quality passes, it is not scored in the selection comparison. Check `invalid_reason` in `comparison.json` and re-run with supported history shapes only.

Re-aggregating saved observations (no external CLI, no network):

```bash
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
python3 scripts/summarize_selection_benchmark.py scripts/testdata/selection-benchmark
```

A self-reported `CHECK: PASS` alone is not treated as success. Costs are reported only when the unit price and its source are both available, and missing data is never converted into 0 or into a reduction rate. Groups whose comparison conditions (compaction, reasoning, task) do not match are not comparable. A selection comparison without a baseline revision does not produce an improvement rate.

A single-run difference is affected by model variance, prompt caching, and service congestion. To claim an effect, run multiple times and compare the median of each condition. Passing a mock fixture is not called a measured efficiency improvement.

## Claude Code

Launch with `jev-routing run claude` as shown in [Running](#running). By default an ordinary Claude request is forwarded unchanged and Jev is never called; `JEV_CLAUDE_ADVISE=on` re-enables the previous tool-selection advice path, but it does not reduce tokens (see below).

The only reduction path kept for Claude Code is Anthropic's native [context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing): with `JEV_CLAUDE_CLEAR_TOOL_USES=on` the proxy appends the `clear_tool_uses_20250919` edit so old tool results are cleared server-side. The `clear_thinking_20251015` edit Claude Code already sends is kept as-is. It works under subscription (claude.ai) login. It is off by default. The variables are listed in [Comparison experiments](#comparison-experiments-disabled-by-default); details of every run are in [docs/MEMO.md](docs/MEMO.md).

Recommended configuration:

| Variable | Value | Note |
|---|---|---|
| `JEV_CLAUDE_CLEAR_TOOL_USES` | `on` | enables the edit |
| `JEV_CLAUDE_CLEAR_GATE` | `jev` | Jev decides once per conversation whether to clear; needs a Jev key, otherwise it fails closed (no clearing) |
| `JEV_CLAUDE_CLEAR_TRIGGER` | `100000` | default |
| `JEV_CLAUDE_CLEAR_AT_LEAST` | `40000` | default |
| `JEV_CLAUDE_CLEAR_KEEP` | `3` | default |

```bash
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
JEV_CLAUDE_CLEAR_TOOL_USES=on JEV_CLAUDE_CLEAR_GATE=jev jev-routing serve --host claude &
claude
```

To measure it with the benchmark (bench contexts do not reach 100k, so the thresholds are lowered):

```bash
jev-routing bench --agent claude --tasks chess-bugfix --modes off,on --reps 6 \
  --claude-clear --claude-clear-gate jev --claude-clear-trigger 30000 --claude-clear-at-least 10000
```

- `--user-tools` runs with the user's real tool configuration; add `--no-hooks` with it so the user's hooks do not run.
- Task `child-survey` has a subagent read 8 source files and report at the end; it exercises clearing inside a subagent.
- `CLAUDE_CODE_SUBAGENT_MODEL` is inherited from the environment and recorded. Pin it (for example `claude-sonnet-5`) for comparable runs.
- `bench` gives each agent its own `/tmp` via `bwrap` when available on Linux.

### Why tool-call replacement was abandoned for Claude Code

Claude Code runs with extended thinking on, and the Anthropic API refuses `tool_choice` forcing while thinking; changing the tool list or `tool_choice` also breaks the prompt cache. That leaves only advising the next tool, not steering it, and advice cannot narrow the upstream request or history size — only the per-turn Jev judgment cost, which stacks every turn.

| Investigation | Method | Result |
|---|---|---|
| Structural cost of advice | `dual-facts` (fixed-path task), claude-sonnet-5/medium, 6 pairs | Median total tokens −27.25% (increase); 99% of the increase is the Jev judgment cost |
| Reducing the Jev cost | Skip judgment for requests advice can't attach to, shorten candidate descriptions | Improves to −6.20%, still a net increase; best case only ties the baseline |
| Replacement headroom in real sessions | Reproduced the same judgment call against 200 real Claude Code turns | Real-session Jev input has a median of 7,406 tokens; net is negative at every threshold |
| Deterministic synthesis without Jev | Rule-based synthesis over real sessions | 17.9% accuracy, 0.28% savings ceiling |
| Cross-check against another product's bench | [jev-gateway](https://github.com/vinilana/jev-gateway#benchmark)'s `hint` approach | Its own author describes Claude Code results as "not lower cost" |

### Context editing: what we tried

Metric: same-path net savings = tokens cleared from a request's input, minus the extra cache-write tokens clearing caused, minus rework. Rework counts only re-fetches of calls that were actually cleared and returned identical content (earlier numbers also counted test re-runs, so they are conservative). Run-to-run total-token comparisons were A/A-level noise in every series and are not used as evidence.

1. **Server behavior, measured live.** `clear_tool_uses` is recomputed on each request and clears the oldest eligible results only until `clear_at_least` is reached. Savings per request are therefore capped at about `clear_at_least`.
2. **Bench v1** (`chess-bugfix`, trigger 30000 / at-least 10000 / keep 3): same-path net median 12.0% (12 pairs) and 8.5% (pre-registered confirmation series). Quality passed on every run.
3. **Real work is different.** In 569 real main sessions (30 days) the first request is already a median 81,574-token fixed prefix (system prompt, tool definitions, agent and skill listings). Tool results are a median 3.8% of the final context. The offline replay ceiling for main sessions is about 3%. A pilot with the user's real tool configuration (`--user-tools --no-hooks`, chess-engine) never cleared.
4. **Shrinking the prefix was rejected.** Disabling unused agents and plugins cut the prefix by 21%, but removing capabilities the user may use is not jev-routing's job.
5. **Subagents matter as much as main sessions.** They consume about the same (subagents 1,173M vs main sessions 1,223M tokens in 30 days) and their context is mostly tool results (first request median 35k, median context 80k). Offline ceiling: about 10% of subagent tokens.
6. **Subagents without a gate failed.** `child-survey`, 6 pairs, claude-sonnet-5/medium, 60000/40000: no effect. Clearing caused re-reading (rework up to 699,727 tokens in one run). The pre-registered decision failed.
7. **Jev gate** (`JEV_CLAUDE_CLEAR_GATE=jev`). Once per conversation (parent and each subagent separately), when the conversation's context ≥ trigger and the estimated clearable tool results in the request ≥ `clear_at_least`, Jev chooses `clear_old_results` or `keep_all_results` from the task text and the tool-call history (no tool results). "clear" sticks for the rest of the conversation, because turning the edit off again would break the cache. Errors fail closed. Pre-registered, 2 × 6 pairs at 30000/10000:

| Task | Gate decision | Same-path net savings | Rework | Quality |
|---|---|---|---|---|
| `child-survey` | keep 6/6 | no clearing | none | 16/16 |
| `chess-bugfix` | clear 6/6 | median 11.1% (2.9%–14.2%) over the 5 runs that cleared | 34,344 in 1 of 6 runs, 0 in the rest | 36/36 |

The gate costs about 800–1,400 input + 38 output tokens per conversation (0.1–0.2% of a run's total).

### Expected effect with the recommended settings

Offline estimate over 30 days of real sessions. It is an upper bound: it assumes the gate always clears and there is no rework.

| | Overall reduction | Sessions where clearing fires | Min / median / max among those |
|---|---|---|---|
| Main (569) | 2.8% | 6% | 2.0% / 10.6% / 20.0% |
| Subagents (583) | 9.9% | 22% | 1.4% / 15.8% / 30.2% |
| Combined | ≈6.3% | — | per-session median 0% (most sessions never clear) |

Price-weighted (cache read 0.1, 1h cache write 2.0) the recommended setting is about +0.5%, i.e. roughly cost-neutral.

### Limitations

- Two bench tasks and one model (claude-sonnet-5, medium).
- The gate's generalization to real tasks is unverified.
- The "clear" path has not been verified live at the default 100000 trigger; bench contexts do not reach it.
- The same-path metric assumes the path would be unchanged without clearing.
- Rework counts only identical re-fetches, so it is a lower bound.

## Codex

Launch with `jev-routing run codex`. The only reduction enabled by default for Codex is truncating large tool results; `JEV_CODEX_TOOL_OUTPUT_TRUNCATE=off` disables it. Tool selection and Jev-based compaction replacement do not reduce tokens by default, for the reasons below. Details of every experiment are in [docs/MEMO.md](docs/MEMO.md).

### Tool-result truncation (enabled by default)

Any tool result in a Codex request (`function_call_output`, etc.) over 20,000 bytes is replaced with the first 10,000 bytes, the last 10,000 bytes, and an omission note. The note nudges the model to re-fetch the omitted part with `sed -n` or `rg` if it is needed. Because Codex resends the full history on every request, the same rule is applied to every entry in the history each time, not just the newest result, so the prompt cache prefix survives. It does not call Jev.

- **Rationale**: Of 2,301 tool results sent in recent real sessions, 13% were over 20,000 bytes, but they accounted for 54% of the bytes. Codex on `gpt-5.6-terra` truncates tool results at 10,000 tokens, and the model chooses `max_output_tokens` on each call. Large results occur when the model chooses a large limit.
- **Bench**: Task `large-facts` (recover facts near the start, middle, and end of six ~32KB logs), `gpt-5.6-terra`/`medium`, 6 pre-registered pairs. Quality was perfect across all 12 runs; median total-token reduction +8.19% (range −39.25% to +27.69%, 4 improved, 2 worse). Non-cached input fell in all 6 pairs (median 109,494→78,900). On the other hand, re-fetches of the omitted parts increased, request counts rose in 5 pairs, and median elapsed time grew from 48.0s to 61.1s. Because the interval crosses zero, the bench's effect verdict is `hold`.
- This task is a stress test for when the model asks for large output (the task prompt instructs it to set `max_output_tokens` to 12,000 or more). The reduction rate for real work as a whole is unverified.

```bash
jev-routing bench --agent codex --model gpt-5.6-terra --effort medium \
  --tasks large-facts --modes off,on --reps 6 --codex-tool-output-truncate
```

### Approaches not adopted

| Investigation | Method | Result |
|---|---|---|
| Tool selection (default) | Standard Codex, `dual-facts --catalog 4` | Local candidates were 3, so the cost gate (`JEV_COST_GATE_MAX=3`) stopped it and Jev was never called. The added MCP tool is an external namespace and excluded from narrowing. Effect not computable |
| Tool selection (Jev forced) | `JEV_COST_GATE_MAX=0`, `dual-facts`, 3 pairs | About 13k Jev tokens per run. The candidate list changed, and the first request's cache read went 24,320→0. Total differences swung between +81,082/−118,050/−17,826; kept as diagnosis only |
| Jev-based compaction replacement | Replace Codex's compaction request with a Jev summary | Runs with 0/3 quality, bloated request bodies, and mismatches between CLI and upstream usage occurred. Disabled by default |
| Lowering the compaction threshold | Standard Codex compaction, threshold 900000 vs. 55000, `compact-facts`, 6 pre-registered pairs | Median reduction −0.75% (range −11.70% to +16.77%). Not adopted |

### Limitations

- The bench covers one truncation task and one model (`gpt-5.6-terra`, `medium`).
- The truncation effect is a stress-test figure; the reduction rate for real work and the extent of latency worsened by re-fetches are unverified.
- The real-session analysis relies on local Codex records (`~/.codex/sessions`).
