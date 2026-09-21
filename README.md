# jev-routing

[日本語版はこちら](README_ja.md)

## What this is

jev-routing is a local proxy that sits between a coding-agent CLI (Claude Code / Codex / Grok Build / Devin CLI) and the upstream LLM API. It does not replace the agent: it accepts the requests the agent already sends, reshapes them right before they go out, and forwards them upstream.

Agent CLIs keep resending the same growing payload every turn: the full tool schema catalog, a swollen history of tool calls and results, and the previous step's thinking. That accumulation is the main source of latency, token spend, and wrong tool picks. jev-routing inserts four things at that point.

1. **History compaction** — only `tool_use` / `tool_result` are scored and then dropped or truncated. User and assistant prose is untouched and nothing is summarized
2. **One schema for tool selection** — Jev is asked for the next tool and for done in the same call, so the step's `tools[]` shrinks to a single schema (zero when the step is a plain response)
3. **thinking / reasoning stripping** — reasoning blocks that the next decision does not need are removed
4. **Model / effort routing** — `route --json` picks, among the candidate pairs, the cheapest one that is still strong enough for the difficulty

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
jev-routing route --json < request.json   # ateam / diagnostics. returns the chosen model as JSON
```

`run` tries `127.0.0.1:8787` first and automatically picks a free port if it is in use. If `JEV_LISTEN` is set, that address takes precedence.

`route --json` is the selection entry point for ateam and for diagnostics. Given `model_mode` / `effort_mode` and candidate `pairs`, it returns the pair applied to the response's `model`. The keys are `model` / `effort` / `source` / `reason_code` / `asked`. If Jev is not connected and there is more than one candidate, `reason_code` is `no_match` and it falls back to the legacy values of `legacy_model` and `effort`. ateam uses this automatic selection only for `ateam auto review`; without `auto` it uses the fixed values from the roster. Each selection appends one line to `~/.local/state/jev-routing/model-routes.jsonl`. The destination can be changed with `JEV_MODEL_LOG`.

Model selection for `ateam auto` does not use the `next_tool` classifier meant for capability; it uses a dedicated `model_pair` question. Candidate pairs carry `difficulty` and `cost`, and Jev picks the cheapest / smallest pair that is still sufficient. When confidence is too low to adopt a pair, the applied values stay at the legacy ones and the rejected pair is recorded in `rejected_id`.

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

## Compaction

The history-compaction judgment was ported to Go with reference to [tamaratran/fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction) ([MIT License](https://github.com/tamaratran/fast-jev-compaction/blob/main/LICENSE), Copyright (c) 2025). It does not summarize; it follows the same drop / truncate contract for `tool_use` / `tool_result`.

- User and assistant prose is never touched
- Only tool_use and tool_result are scored with noul
- `keepResult` → keep both
- `keepCall` only → truncate the result to the first 300 characters
- Both below threshold → drop both
- The first and the most recent entries are pinned

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

- [Claude Code](https://code.claude.com/docs/en/tools-reference): the `tool_use` / `tool_result` history shapes are accepted. Built-in names vary with the runtime and feature flags, so they are not kept as a fixed allow list.
- Codex: the history shapes for `functions.*`, `custom_tool_call`, the Responses built-in tools, and MCP calls are accepted.
- [Grok Build](https://docs.x.ai/build/features/permissions): `read_file`, `search_replace`, `grep_search`, `list_dir`, `run_terminal_cmd`, `web_search`, `web_fetch`, `todo_write`, `task`, `kill_task`, `get_task_output`, `memory_search`, `memory_get`, `search_tool`, `use_tool`, `lsp`, and conditionally `write` are handled from the runtime catalog.
- [Devin CLI](https://docs.devin.ai/cli/reference/permissions#tool-based-permissions): `read`, `write`, `edit`, `apply_patch`, notebooks, search, shell, `webfetch`, tasks, Skills, subagents, permissions, and MCP management tools are handled from the runtime catalog. The ATIF export format will not be added to the history judgment on speculation until its public schema can be confirmed.

For history shapes, Claude's `tool_use` / `tool_result`, the `*_call` shapes of Codex and Responses, and MCP's `mcp_call` are explicitly accepted. History containing images passes through on the safe side.

## Dashboard

The read-only `http://127.0.0.1:<port>/dashboard` can be opened only while listening on loopback. On a public listener it returns 404. Settings cannot be changed from the page. Pricing is not shown. No CORS headers are added, and only GET is accepted.

```bash
jev-routing run --dashboard grok
```

`run --dashboard` opens the dashboard in a browser after startup. With `serve`, open the same URL by hand. The page refreshes every 2 seconds and covers only the current process.

- Routing overview (counts by decision source and by application)
- Status of the six categories (model and effort, subagents, skills, MCP, CLI, plugins, compaction). Unobserved stays unobserved
- Application list. In addition to the proxy's rewrites, the model and effort chosen by `route --json` are emitted as `kind=model`, where capability is the applied model and callId is the reason such as `jev` / `no_match`
- Token consumption aggregated from upstream responses (input, output, cache, reasoning)
- Recent requests (sequence number, host, decision source, application, selected tool, reason, changes, tool substitution, jev, tokens, time)
- Filters by host / decision source / application, and per-row detail (decision ID, operation ID). j/k moves between rows, Enter opens the detail, r reconnects
- Pasting Comparison JSON (displayed locally only; nothing is sent)

The browser side keeps up to 1000 entries and the table shows the most recent 200. When the connection drops it shows the last update time and "disconnected", and reconnecting re-fetches the history. `?sample=1` shows mock values for checking the display and is labeled as a sample on the page.

## Comparison experiments (disabled by default)

These are read once at startup. Invalid values make startup fail.

| Variable | Values | Default |
|---|---|---|
| `JEV_ROUTING_MODE` | `baseline` / `filter` / `forced` | `filter` |
| `JEV_COMPACTION` | `off` / `on` | `on` |
| `JEV_REASONING` | `preserve` / `legacy` | `legacy` |
| `JEV_SELECTION_MODE` | `local` / `jev` / `hybrid` | `hybrid` |
| `JEV_SHADOW` | `on` / `off` | `off` |
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

`JEV_SHADOW=on` scores the candidate set without rewriting the request. `JEV_TRANSFORMS` turns compaction, tool-catalog filtering, and contrast criteria on or off independently; criteria stays off until a confused pair is registered. `JEV_COST_GATE_MAX` skips the classifier when the candidate count is at most this value.

On ordinary proxy requests the same decision function is called automatically, and it goes on to supply the body of the selected skill, invoke MCP/CLI, and verify the result. When `JEV_AUTO_APPLY=on`, only targets whose per-kind mode is `apply` are started, and under `required` a non-delivery, an unsupported case, or an unconsumed selection is not treated as a successful exit. `fallback` reverts to the legacy settings only when explicitly specified. `jev-routing route --json` is the same entry point used by ateam and diagnostics, and it is not assumed that an LLM will call it on its own. The model-selection JSON is `model` / `effort` / `reason_code`. A selection log or narrowed candidates alone do not count as an application having completed. Unknown executions are never retried automatically. The dashboard shows the movable points, the reasons for non-application, and the comparison effects in Japanese. Missing data and "no comparison" are left as missing / no comparison, and mock values are labeled as samples.

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

Results land in `artifacts/x-cell/<datetime>/<host>/comparison.json`. Use only results with `comparable: true` (`valid: true`) for comparison. Not reaching the proxy, `rewritten=0` (passthrough only), or a mismatch in the completion condition yields `comparable: false` and no reduction figures. Billed tokens vary a lot with cache state across independent sessions, so they are not compared on a single run. Instead, `routing_request_chars` (the reduction in bytes of the JSON body the proxy actually received and sent upstream) and the differences in output tokens and runtime are recorded. Codex with ChatGPT login uses `-m gpt-5.6-terra` (override with `CODEX_MODEL`). The short name `terra` returns 400.

`make test-selection-benchmark` fixes `JEV_COMPACTION=off` and `JEV_REASONING=preserve` for all proxy conditions. If the history contains an unsupported content type (for example Claude's `tool_addition`), the result is `unknown_history` and it passes through unrewritten. That result is a normal fail-safe stop: even if external quality passes, it is not scored in the selection comparison. Check `invalid_reason` in `comparison.json` and re-run with supported history shapes only.

Re-aggregating saved observations (no external CLI, no network):

```bash
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
python3 scripts/summarize_selection_benchmark.py scripts/testdata/selection-benchmark
```

A self-reported `CHECK: PASS` alone is not treated as success. Costs are reported only when the unit price and its source are both available, and missing data is never converted into 0 or into a reduction rate. Groups whose comparison conditions (compaction, reasoning, task) do not match are not comparable. A selection comparison without a baseline revision does not produce an improvement rate.

A single-run difference is affected by model variance, prompt caching, and service congestion. To claim an effect, run multiple times and compare the median of each condition. Passing a mock fixture is not called a measured efficiency improvement.
