# jev-routing: Research Notes

> Archived (2026-09-26). This document records what was tried to cut the token usage of AI coding agents (Claude Code, Codex, etc.) with Jev, the data, and why the project was stopped. It is an English translation of docs/MEMO.md, reorganized as: 1. Conclusion, 2. Approaches examined, 3. Trial and error and data (chronological log).

## Contents

1. [Conclusion](#1-conclusion)
2. [Lessons learned](#2-lessons-learned)
3. [Approaches examined](#3-approaches-examined)
4. [Trial and error and data](#4-trial-and-error-and-data)
   - [Claude Code advise / dual-facts](#analysis-of-the-increase-in-claude-code-dual-facts-2026-09-24)
   - [Claude Code native context editing + Jev clear gate](#claude-code-results-of-the-confirmation-series-2026-09-25)
   - [Codex tool-output truncation](#truncation-of-large-tool-results-results-of-the-6-pairs)
   - [Codex compaction](#compaction-threshold-confirmation-series-results-of-the-6-pairs)
   - [Codex tool steering (JEV_CODEX_STEER)](#codex-tool-steering-summary-and-discussion-of-trial-and-error-wrap-up-on-2026-09-26)
   - [Final conclusion and project closure](#final-conclusion-and-project-closure-2026-09-26)

## 1. Conclusion

**Goal**: reduce upstream tokens (parent + child input and output) used by AI agents such as Claude Code and Codex, while preserving quality. Jev (TypeSafe) usage is cheap and is not included in this metric.

**Jev-based tool selection and steering did not become a generic reduction method.** The effect depends heavily on the model, and the same approach can swing in the opposite direction.

| Target | Approach | Preregistered 6-pair reduction (median) | Verdict |
|---|---|---:|---|
| Codex `gpt-5.6-sol` | Force `tool_choice` while keeping the tool list (port of the jev-gateway approach) | **+19.30%** | Effective |
| Codex `gpt-5.6-terra` | Same | −37.08% (increase) | Not adopted |
| Codex `gpt-6-luna` | Same | −50.78% (increase) | Not adopted |
| Claude Code `claude-opus-5-5` | Advice on the next tool (`advise`) | +1.67% | Not adopted |
| Claude Code `claude-sonnet-5` | Same | Upstream difference ~0 | Not adopted |

On the models that increased, forcing the tool roughly doubled the number of upstream requests at most.

**The approach that did show an effect was shrinking context.**

| Target | Approach | Effect | Default |
|---|---|---|---|
| Claude Code | Anthropic-native context editing (`clear_tool_uses_20250919`) + Jev's clearing gate | Median net reduction of 11.1% on the same path in runs where clearing occurred. Estimated upper bound in real sessions is about 6.3% overall | Off (enable with `JEV_CLAUDE_CLEAR_TOOL_USES=on`) |
| Codex | Deterministic truncation of large tool results (omit the middle of anything over 20,000 bytes) | Median +8.19% under load testing (interval crosses zero) | On (disable with `JEV_CODEX_TOOL_OUTPUT_TRUNCATE=off`) |

---
**Measured token volumes (upstream input + output, excluding Jev. Each series is the preregistered 6 pairs, on the `chess-bugfix` task. Tool-result truncation only, `large-facts`)**

| Series | Baseline total | Intervention total | Change in total | Per-run median baseline→intervention | Median per-pair reduction | Improved/worse |
|---|---:|---:|---:|---|---:|---|
| Codex `gpt-5.6-sol` · tool steering | 15,127,871 | 11,509,869 | **−23.92%** | 2,339,036 → 1,912,187 | +19.30% | 4/2 |
| Codex `gpt-5.6-terra` · tool steering | 6,194,181 | 8,266,032 | +33.45% (increase) | 968,075 → 1,286,876 | −37.08% | 1/5 |
| Codex `gpt-6-luna` · tool steering | 3,371,403 | 5,510,014 | +63.43% (increase) | 497,227 → 812,886 | −50.78% | 1/5 |
| Claude Code `claude-opus-5-5` · advice | 701,885 | 680,083 | −3.11% | 118,671 → 119,439 | +1.67% | 3/3 |
| Codex `gpt-5.6-terra` · tool-result truncation | 4,179,472 | 3,959,848 | **−5.25%** | 666,336 → 646,678 | +8.19% | 4/2 |

"Change in total" compares the baseline and intervention sums across the 6 pairs (negative is a reduction). "Reduction" is the median of the per-pair `100×(baseline−intervention)/baseline` (positive is a reduction). Claude Code's context editing was evaluated by net reduction on the same path (median 11.1%), not by the total across runs, because the path differs sharply depending on whether clearing occurred.
---

**What showed no effect**: Jev-based replacement of `Read`, Jev-free deterministic synthesis of tool calls, and replacing or changing the threshold of Codex's compaction (compaction rarely occurs in real sessions, with an upper bound of 0.1–0.3%).

**Why the project was stopped**: the effect of Jev-based tool selection/steering depends strongly on the model and swings between a reduction and an increase, so it never became a generic technique that could be turned on everywhere. The only approaches that reliably held up were the context-shrinking ones (native context editing on Claude Code, tool-output truncation on Codex), and even their room for improvement in real sessions is only on the order of a few percent. Given that, further work was judged to have no realistic prospect of a bigger payoff, and the project was closed and archived.

## 2. Lessons learned

What did not work, and what we would do differently. Each item is backed by the data in section 4.

### Before building

1. **Estimate the ceiling on real sessions offline before implementing.** Several interventions were built and tuned on the bench before an offline look at real sessions showed there was almost nothing to win. Codex compaction went through many rejected pilots (thresholds 32,000–60,000) before we found it fires in only 12 of 201 parent sessions, capping the gain at 0.1–0.3%. Jev `Read` replacement was in the red at every threshold (best −6.7%), and deterministic tool-call synthesis had a 0.28% ceiling. A one-day replay would have ruled these out.
2. **Find where the cost is before choosing the lever.** Most of an agent's cost is re-reading the context every turn. Choosing the next tool changes neither the number of turns nor the context size, so tool advice cannot save tokens; adding Jev's judgment cost only makes it worse (`dual-facts`: −27.25%, 99% of the increase was Jev's cost). Savings have to come from fewer turns or a smaller context.
3. **Read the host and API contract first.** The Anthropic API rejects forced `tool_choice` under manual extended thinking and, on Claude Opus 5.5, under every thinking mode; adaptive thinking on other models such as Sonnet 5 allows it. Changing tool definitions invalidates the whole prompt cache, and changing `tool_choice` invalidates the cached message blocks, which hold the conversation history that dominates Claude Code's cost. So forcing was either impossible or expensive, and Claude Code was limited to "advice". We had assumed a blanket ban and did not re-check this per model. Codex (as observed through this proxy) resends the full history every request, and rewriting the candidate list coincided with the first request's cache read dropping from 24,320 to 0. Devin's Connect path reports no usage at all and Grok's cancel requests report none, which made the token KPI unmeasurable on those hosts. All of this was learned after implementation.

### Benchmarks and real work

4. **A bench is not real work.** The bench prefix was about 17,000 tokens with tool results dominating the context; real sessions start from a median 81,574-token fixed prefix (system prompt, tool definitions, agent and skill listings) and tool results are a median 3.8% of the final context. The 8–12% saving from context editing on the bench shrinks to a ~3% ceiling for real main sessions. Thresholds had to be lowered for clearing to fire at all on the bench, and the default 100,000 trigger was never verified live.
5. **A result on one model is not a result on another.** jev-gateway's approach reproduced on `gpt-5.6-sol` (median −31% upstream requests), and our port saved +19.30% there, but increased tokens by 37.08% on `gpt-5.6-terra` and 50.78% on `gpt-6-luna`, where forced tools up to doubled the request count. The mechanism (forcing `exec` may keep the model from ending the turn with text) is unverified. Steering has to be validated per model, and before interpreting a pair, check whether the intervention was actually applied: of sol's two worse pairs, one was barely steered (4 forced; 45 of 48 forwards were subagent-related), but the other was steered on 54 of 59 requests and still got worse, so "not applied" explains only half of it.

### Measurement

6. **Test the meter before trusting any number.** Most early "effects" were measurement bugs: missing usage zero-filled into "LLM requests −90% · Jev calls −100%"; Jev usage silently dropped by a snake_case/camelCase mismatch; `count_tokens` control requests counted as reasoning requests; Codex `spawn_agent` launch failures counted as subagent calls, making all 6 pairs incomparable; the Codex CLI counting a synthesized compaction response it never sent upstream; and a rework metric that counted unrelated re-runs (same-path net saving 4.93% → 12.75% after the fix). Fixed-response contract tests for the meter, "missing stays missing, never zero", and evidence that the intervention fired should come before the first real run.
7. **Pre-register, pair, and repeat.** Under identical conditions, run-to-run total-token differences ranged from −271% to +90% (Claude Code, A/A). An exploratory +13.11% on Codex compaction became −0.75% in the pre-registered series. Only pre-registered criteria, paired comparisons (an even count of at least 6), same-path metrics, and including every started run (not only successful pairs) held up.

### What held up

8. **Shrink context with native or deterministic mechanisms, rather than replacing the agent's decisions.** The two approaches that showed an effect were Anthropic's native context editing (with a single Jev gate per conversation) and deterministic truncation of large tool results that keeps the prompt-cache prefix stable. Interventions that second-guessed the model's next action were either neutral or model-dependent.

## 3. Approaches examined

| Approach | Host | What it does | Result (key number) | Verdict | Details |
|---|---|---|---|---|---|
| Tool selection via candidate filtering + cost gate (Codex, original path) | Codex | Narrows the local tool candidates sent upstream and only calls Jev when there are more than the cost-gate's candidate count (default max 3) | On standard Codex the candidate count stays at 3, so Jev is never invoked; the median reduction is not computable | Kept as-is (default) | [4. Breakdown and intervention decision for real Codex sessions](#breakdown-and-intervention-decision-for-real-codex-sessions-2026-09-25) |
| Claude Code advise (hint) — sonnet-5 dual-facts, and opus-5-5 chess-bugfix | Claude Code | Jev picks the next tool and appends it to the request as advice, without removing any tool from the tool list | sonnet-5/`dual-facts`: median −27.25% (increase); opus-5-5/`chess-bugfix`: median +1.67% (essentially 0) | Not adopted; disabled by default (`JEV_CLAUDE_ADVISE=on` to enable) | [4. Analysis of the increase in Claude Code `dual-facts`](#analysis-of-the-increase-in-claude-code-dual-facts-2026-09-24) · [4. `claude-opus-5-5` advise re-verification](#claude-code-claude-opus-5-5-advise-re-verification-results-and-judgment-2026-09-26) |
| Jev cost reduction for advise (skip no-target, shorten descriptions) | Claude Code | Skips the Jev call when there is no tool result to attach advice to, and shortens the candidate tool descriptions sent to Jev | Improved the median from −27.25% to −6.20%, still an increase | Not adopted on its own; superseded by disabling advise | [4. Claude Code: examining reduction of Jev judgment cost](#claude-code-examining-reduction-of-jev-judgment-cost-and-replacement-of-upstream-inference-2026-09-24) |
| Jev Read replacement (offline replay) | Claude Code | Offline replay of 200 real sessions' turns through the same Jev judgment request, to estimate the payoff of replacing a `Read` call with a synthesized one | Best-threshold net profit/loss about −6.7% (in the red at every threshold); estimated real-work net effect about −8% | Not adopted | [4. Reproduction experiment of Read replacement](#reproduction-experiment-of-read-replacement-in-real-work-2026-09-24) |
| Deterministic tool-call synthesis without Jev (polling rule) | Claude Code | Without calling Jev, deterministically re-issue an identical polling call (e.g. `TaskOutput`) when the previous result is still incomplete | Accuracy 17.9%, savings ceiling 0.28%; 13–16% of synthesized calls would have hijacked the response to the user | Not implemented | [4. Verification of the polling synthesis rule](#verification-of-the-polling-synthesis-rule-2026-09-24) |
| Claude Code native context editing + Jev clear gate | Claude Code | Adds Anthropic's native `clear_tool_uses_20250919` context edit, gated by a single per-conversation Jev Choice call deciding whether erasing old tool results is safe | Same-path net saving median 11.1% (12-pair) then 8.5% (confirmation series) in runs where clearing occurred; real-session ceiling ~6.3% overall | Adopted, off by default (`JEV_CLAUDE_CLEAR_TOOL_USES=on`, `JEV_CLAUDE_CLEAR_GATE=jev`) | [4. Claude Code: results of the confirmation series](#claude-code-results-of-the-confirmation-series-2026-09-25) · [4. Jev erasure gate results](#jev-erasure-gate-results-of-the-two-pre-registered-series-2026-09-25) · [4. Final settings](#final-settings-and-summary-of-expected-effect-2026-09-25) |
| Codex tool-output truncation | Codex | Deterministically truncates any tool result over a byte threshold (default 20,000) to its head and tail, applied identically to history on every request to preserve the prompt cache | Stress-test `large-facts`: median +8.19% (interval crosses zero) | Adopted, on by default (`JEV_CODEX_TOOL_OUTPUT_TRUNCATE=off` to disable) | [4. Truncation of large tool results: results](#truncation-of-large-tool-results-results-of-the-6-pairs) · [4. Wrap-up](#wrap-up-2026-09-25) |
| Codex compaction: Jev compaction replacement, lowered auto-compact threshold, offline ceiling analysis | Codex | Tried replacing Codex's own compaction summary with a Jev-produced one, and separately tried lowering the auto-compact trigger threshold | Jev replacement caused quality drops and unresolved usage-accounting mismatches; the threshold-only comparison gave a median of −0.75% (hold); real-session ceiling for compaction overall is 0.1–0.3% | Not adopted | [4. Pre-registration of Codex tool-selection/compaction benchmark](#pre-registration-of-codex-tool-selectioncompaction-benchmark-2026-09-25) · [4. Compaction-threshold confirmation series results](#compaction-threshold-confirmation-series-results-of-the-6-pairs) |
| jev-gateway reproduction (Codex 0.154, gpt-5.6-sol) | Codex | Reproduced the third-party jev-gateway project's own published benchmark configuration, to establish a reference point before porting its approach | Median −31% upstream requests, −30% input, −31% output, matching jev-gateway's published trend | Reference only (not an adoption decision) | [4. Codex tool steering: summary and discussion](#codex-tool-steering-summary-and-discussion-of-trial-and-error-wrap-up-on-2026-09-26) |
| Port of jev-gateway forced/none steering (`JEV_CODEX_STEER`): terra, sol, luna | Codex | Keeps the tool list unchanged but has Jev rewrite `tool_choice` to `forced` or `none` per request | terra: median −37.08% (increase); sol: median +19.30% (effective); luna: median −50.78% (increase) | Adopted for `gpt-5.6-sol` only; off by default elsewhere | [4. The fixed 6 pairs (terra) results](#the-fixed-6-pairs-results-and-decision) · [4. `gpt-5.6-sol` pre-registered 6 pairs](#gpt-56-sol-pre-registered-6-pairs-results-and-judgment-2026-09-26) · [4. `gpt-6-luna` pre-registered 6 pairs](#gpt-6-luna-pre-registered-6-pairs-results-and-judgment-2026-09-26) |
| Measurement fixes (usage-gap handling, failed-spawn miscounting, excluding Jev from the KPI) | All hosts | Fixed several measurement bugs: missing/partial usage being silently zero-filled, a Codex `spawn_agent` launch failure being miscounted as a real subagent call, and redefined the primary KPI to exclude Jev's own (cheap) token usage | Corrected several false "improvement"/"incomparable" readings that had been produced by these bugs | Fixes applied; not an intervention themselves | [4. External code review of the measurement fix](#external-code-review-of-the-measurement-fix-added-2026-09-24) · [4. Fixing and re-confirming Devin](#fixing-and-re-confirming-devin-and-measurement-failure-display-added-2026-09-24) · [4. Excluding Jev from the primary metric](#excluding-jev-from-the-primary-metric-2026-09-25) · [4. Result of the main measurement (spawn_agent fix)](#result-of-the-main-measurement-indeterminate-measurement-side-false-positive) |

## 4. Trial and error and data

This is the full chronological log translated from docs/MEMO.md. Early sections describe states that later sections supersede; later sections are authoritative.

Recording started 2026-09-24. This records, in chronological order, the progress of code/document investigation, fixed-wire tests, and real-host tests. Early "not yet done" / "not yet fixed" notes refer to the state at the time of recording; the current state is authoritative in the later sections.

**For the latest handoff, see "Handoff (end of 2026-09-25 session)" at the end.**

### Current outline and near-term work (updated 2026-09-24)

#### Premises for judgment

- The goal is **to keep output quality under external verification while reducing the actual measured total tokens of the parent/child hosts and Jev**. Reducing time is a secondary metric. A single correct answer, a reduction in candidates, or a successful call is not called a "saving effect."
- **Tool/skill selection and compaction are separate features.** For now, verify the selection path with `JEV_COMPACTION=off` / `JEV_REASONING=preserve`, and only run a comparison combining it with compaction after each has been established on its own.
- Treat "judgment success → applied to the host request → required tool/skill usage → handling of the result → correctness of the external outcome" as separate stages. Total tokens include Jev's skill-selection requests as well, and any run whose stage is unclear is not used to judge effect.
- Claude Code and Codex have been confirmed reachable via real CLIs using subscription authentication. The simulated upstream is limited to wire-contract tests, and the simulated Jev is limited to fixed tests of branching. Nothing will be published to GitHub until this is complete.

#### Recent empirical results

| Host/feature | Confirmed stage | Remaining obstacle |
|---|---|---|
| Claude Code tool advice | Reproduced and fixed the real request pattern ending in a trailing `system` message via a fixed test. Confirmed real-Jev advice application, two `Read`s, and a correct answer | Whether the advice reduces tokens is undetermined. A single-shot comparison on a two-tool task shows an increase |
| Claude Code skills | Fixed the frontmatter description, delivery target, and shared state between requests. Real Jev implicitly selects, and real Claude Code adopts the synthesized marker and answers correctly | The skill-task control failed quality, so the reduction amount is incomparable. Watch out for redelivery and Jev-judgment token cost |
| Codex tool selection | Confirmed real-Jev application, two MCP results, and three outcome items. CLI's main-model usage matches the proxy's per-model usage | Including the extra reasoning from the auto-approval review, a single-shot comparison shows a token increase. Repeated-run effect is undetermined |
| Grok Build tool selection | Confirmed real-Jev `read_file` application, the tool result, and `xcell-module` quality 3/3. CLI's main-model usage matches the proxy's completion requests | The canceled reasoning request at termination has no usage recorded, so the total token difference is incomparable |
| Devin CLI tool selection | Confirmed real-Jev read/write application, the Connect real-communication call ID/result, and `xcell-module` quality 3/3. Auxiliary usage appears in the host conversation record | The Connect reasoning response usage is missing entirely for all entries. Host auxiliary aggregation cannot be reconciled against per-request usage, so the total token difference is incomparable |

The pre-fix retest for Claude Code / skills was conducted with a standalone Claude Opus 5.5 · high review. The real Claude Code used `claude-sonnet-5` · `medium`, and real Jev used TypeSafe's service. The example where skill-selection Jev usage was omitted from the tally (stats at the time were 7,986/228, actual communication measured 9,258/511) is evidence from before the fix. After the fix, selection, delivery, and outcome were reconfirmed with the real host and fixed tests. A single-shot success of the outcome is not treated as a saving effect. [Selection path](internal/proxy/proxy.go#L870) · [Stats accumulation](internal/proxy/proxy.go#L580)

#### Near-term work list (fix the measurement first)

**A verification foundation for the main comparison and the Claude Code/skills fix have now been implemented.** The following checklist shows the original ordering and remaining work. The `direct` condition, measurement contracts across all hosts, and judgment of repeated-run effect are not yet complete. Bugs found were reproduced with a fixed test before being fixed.

**Stage 1: Benchmark and measurement contract**

- [ ] **V1: Fix the fields recorded per run and the judgment rules.** Based on the [verification spec](docs/requirements/unified-benchmark-validation.md), define host, real model, reasoning setting, source version, starting state, condition, the number of upstream-reasoning/control/Jev/child-session requests, parent/child and Jev input/output/cache usage, selection→application→tool-call ID→result, external outcome, and the reason for any missing value. Record `direct`, `passthrough`, and `selection` as separate conditions, and do not zero-fill unobtained values.
- [ ] **V2: Calibrate the measurement apparatus with fixed inputs.** Give it known usage amounts, multiple tools, child sessions, failures/interruptions, concurrent requests, and event truncation, and confirm that the run record and aggregation match the expected values. Fix here the known omission of Jev usage in the skill `capability` judgment. Cross-check CLI and proxy usage in the same run and record the difference.
- [x] **V3: Fix the tasks that can score an outcome, first.** Short P1/R1 are limited to connectivity checks. For tool selection, prepare independent multi-tool R2 and multi-step R3; for skills, prepare S1 whose instructions cannot be inferred from the task text alone, and score the required operations and the final outcome independently. Use the same scorer for the control condition too. Add failure-recovery R4 and sub-agent A1 only after measurement for the basic tasks is established.
- [x] **V4: Fix how the main metric is confirmed.** Recompute quality, application, measurement completeness, and total-token difference from saved runs into `comparison.json`, and have the dashboard display those numbers and the reasons for incomparability. Use the local `proxy-events.json` and per-model `runs.jsonl` for per-request root-cause analysis, and do not give the display side its own separate aggregation formula.
- [x] **V5: Record current behavior as a diagnosis.** Under the above contract, prioritize Claude Code and Codex, and try real-Jev, real-CLI `passthrough` and `selection`. Also leave the known zero Claude-advice cases and skill non-adoption as failures. Repeats at this stage are for understanding cause and variance and are not used to decide adoption of the saving effect.

**Stage 2: Fix intervention behavior and re-verify**

- [x] **F1: Fix Claude Code's advice application.** Turn the real request pattern with a trailing `system` message into a regression test, and reconfirm advice insertion, model usage, tool results, and external outcome using the same V1 fields.
- [x] **F2: Fix skill selection/delivery/adoption.** Handle the frontmatter description, the distinction from the host's `Skill` tool, and the `LastDelivered` conflict between requests, and verify Jev selection and explicit-name selection separately. Decide the delivery method, and do not call it a success until real Claude Code satisfies S1's outcome.
- [ ] **F3: Fill remaining gaps in evidence for other hosts.** Confirm Codex's real-Jev application example and the correspondence between Devin CLI's call ID and result, and turn Grok Build's working path into a regression test.

**Stage 3: Effect judgment**

- [ ] **E1: Pre-register conditions and run repeated comparisons.** Limited to hosts/tasks where quality, application, and measurement are established, compare `passthrough` and `selection` under identical conditions with alternating order. Show quality, total tokens, comparable count, degraded examples, and elapsed time for all starting runs. Verify compaction as a separate condition, and try combining it only after each single condition has succeeded on its own.

**Condition to advance to Stage 2**: V1–V4's recording, scoring, and missing-value judgment are fixed, and V5 can reproduce current failures without hiding them. **Condition to advance to effect judgment**: application of the judgment, required operations, external outcome, and the relevant parent/child session and Jev usage are all available for the target host/target feature. Do not read a tool-selection-only comparison (excluding skills) as an improvement of skills or of the product as a whole.

### User's policy

- The main goal is to reduce total token usage while maintaining task quality. Shortening execution time is a secondary goal.
- Treat compaction and the feature that offloads part of the LLM's processing to Jev as separate matters. The latter matters more for now; investigate the cause of the extreme increase in upstream LLM request counts.
- Organize, per host, the communication format, tool/skill invocation, and execution subject from primary sources. Fix the bench's definition of "worked correctly" first as well.
- Keep recording the discussion and investigation results continuously in this file. Do not change code before agreeing on the fix policy.

### Terminology and feature boundaries

To avoid confusion, count upstream-LLM reasoning requests, Jev judgment requests, host-executed tool calls, control HTTP requests, and child-agent reasoning requests separately. Even if the number of actual tool calls is the same, if parallel calls are serialized, the number of upstream-LLM requests can increase. This explanation is a causal hypothesis, not something demonstrated in the current runs. [Anthropic's parallel tool use](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use)

**Compaction**: the current code stops per-normal-request history compression and handles it in a separate branch only when the host requests compaction. [Normal request](internal/proxy/rewrite.go#L228) · [Compaction request](internal/proxy/proxy.go#L558) Even if the transformations in the code are made independent, there is no guarantee that compacted context does not indirectly affect later tool decisions, so verification with both features enabled simultaneously is also needed.

**Substitution by Jev**: on the normal path, what Jev mainly substitutes for is "the decision to pick the next candidate." Jev's `Choice` returns one item from a fixed set of candidates. [TypeSafe's spec](https://docs.typesafe.ai/primitives/choice) The current proxy passes that result to Claude Code as advice, and mainly narrows tool candidates for Codex and Grok Build. Free-form argument generation and actual tool execution remain with the model and the host. [Selection processing](internal/proxy/rewrite.go#L1242) · [Candidate rewriting](internal/proxy/rewrite.go#L473) `direct` requires additional conditions such as a specific Chat format with fixed arguments, and by default the target tool set is empty. [Application condition](internal/proxy/rewrite.go#L492) · [Default value](internal/proxy/options.go#L56)

### Confirmed implementation and per-host breakdown

| Host | Host-side responsibility confirmed from primary sources | Current proxy intervention | Remaining points to confirm |
|---|---|---|---|
| Claude Code | The model returns `tool_use`, Claude Code executes the client-side tool, and passes `tool_result` to the next request. [Official docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/how-tool-use-works) | Keeps the tool list and appends advice to the most recent tool result. [Implementation](internal/proxy/rewrite.go#L259) | On the first turn there is no place to append advice. The actual number of runs where only a Jev judgment occurred. [Existing test](internal/proxy/claude_advise_test.go#L92) |
| Codex | The model chooses a function call, and the client side executes it. [Official docs](https://developers.openai.com/api/docs/guides/function-calling) | Also extracts and narrows `input.additional_tools` from Responses Lite, and changes `tool_choice` and the reasoning setting depending on the condition. [Implementation](internal/proxy/rewrite.go#L452) | The effect of candidate changes on caching, parallelism, and re-planning. |
| Grok Build | Official sources have the tooled request format and the host-side execution handling. [Request format](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampling-types/src/types.rs#L52-L69) · [Execution handling](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/src/session/acp_session_impl/tool_dispatch.rs#L11-L60) | Narrows candidates for normal JSON requests. [Implementation](internal/proxy/rewrite.go#L473) | Classifying the increased requests into normal turns, re-planning, and in-host communication retries. [Retry handling](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampler/src/actor/request_task.rs#L93-L145) |
| Devin CLI | Skill bodies are injected into the conversation; tool permissions and execution are managed by Devin CLI. [Skills](https://docs.devin.ai/cli/extensibility/skills/overview) · [Commands](https://docs.devin.ai/cli/essential-commands) | There is a dedicated path for `GetChatMessage`/`GetDevstralStream` Connect communication. [Implementation](internal/proxy/proxy.go#L479) | The contract for this wire format has not been confirmed in official docs. Confirm candidate extraction, rewrite, and upstream acceptance over real communication. Current tests use a hand-built frame. [Test](internal/proxy/connect_devin_test.go#L18) |

Automatic application of skills, MCP, and CLI is in observation mode by default, and `JEV_AUTO_APPLY` is disabled by default as well. [Config](internal/proxy/options.go#L56) When enabled, there is a path by which skill-body delivery can be repeatedly injected into subsequent requests, but the frequency and token increase of this have not been measured. [Selection and delivery](internal/proxy/proxy.go#L828) · [Delivered result](internal/proxy/application.go#L123) MCP/CLI do not currently reach the execution-handling stage via the normal auto-apply path, which does not match [the README's description](README_ja.md#L227). [Implementation](internal/proxy/proxy.go#L909)

### Past observations, and what remains unconfirmed in the current run

- Commit `d7a96b4` stopped the per-request history-compression path for Codex/Grok, on the grounds that it broke prompt caching and increased non-cached input in a past `gpt-5.6-terra` comparison. This is **a past cause of increase**, and is not evidence for the cause of the request-count increase reported on the current `HEAD`. [Current code](internal/proxy/rewrite.go#L228)
- The saved [old chart](charts/202609231550-terra/comparison-dark.svg) shows an example of increased upstream request counts, but it is a single record per condition from before the fix commit, and the raw `runs.jsonl` is not present in the working environment. The current `HEAD`'s results, settings, and per-request events have also not been found in the working environment.
- For the current increase in request counts, it is necessary to distinguish serialization from candidate restriction, re-planning after a wrong selection, repetition of skill bodies, host-internal retries/child sessions, and measurement over-counting. Which is the primary cause is undetermined.

### Judgment problems in the current bench

1. `make test-selection-benchmark`'s description says `JEV_COMPACTION=off`/`JEV_REASONING=preserve`, but the [execution script](scripts/test-x-cell.sh#L93) fixes `on`/`legacy` for all proxy-side conditions. [Description](README_ja.md#L254) The current results cannot be read as the effect of tool selection alone.
2. The [pass criteria](scripts/summarize_x_cell.py#L248) require `filter`/`forced` completion and do not include Claude Code's current `advise`. Under the compaction-`on` condition it also requires compaction to be applied, but normal-request compaction has already been stopped.
3. [The bench's `Requests`](internal/bench/gateway.go#L201) counts all proxy events, and non-reasoning control requests can get mixed in. [Control-request record](internal/proxy/proxy.go#L703) A separate count is needed for upstream-LLM requests, Jev requests, and actual tool calls.
4. [TypeSafe's official API](https://docs.typesafe.ai/api) returns usage as `input_tokens`/`output_tokens`, but [the Jev-response receiving struct](internal/jev/jev.go#L90) expects `inputTokens`/`outputTokens`. Under the official-format response, usage can go missing. The current [bench record](internal/bench/record.go#L19) has `JevInput` but no Jev output, so total tokens cannot be fully computed.
5. [The aggregation](internal/bench/report.go#L113) excludes only contaminated runs. A median that includes failures or missing usage cannot, as-is, be used as evidence of savings. `bench-all.sh` produces separate outputs for the selection-only and compaction-only conditions with `--modes on`, so on its own it cannot produce a difference from the baseline. [Script](bench-all.sh#L3)

### Proposed verification contract (not yet agreed / not yet implemented)

First compare "no proxy" and "proxy pass-through only" to confirm the effect of the connection itself. Then, for the same host, same model, and same starting state task, compare **both features off / compaction only / selection only / both features on**. Under the selection-only condition keep the reasoning setting fixed, and treat automatic skill-body supply as a separate condition. Alternate the execution order and repeat, recording the settings and source version of each run.

| Feature | Correctness judgment | Effect judgment |
|---|---|---|
| Tool selection | Jev's decision is actually applied and accepted upstream. Required candidates are not excluded, calls and results correspond, and the task succeeds under external verification. Single tool, independent multiple tools, sequential steps, skills, and MCP/CLI are viewed as separate tasks. | Show the number of actual tool calls per response, parallelism, additional turns, retries, cache, and upstream request count, broken down by cause. |
| Compaction | Does not directly intervene in tool definitions/selection settings for normal reasoning requests, and the response to a compaction request is accepted. Required constraints and call/result correspondence are preserved, and the subsequent task succeeds. | Compare actual input tokens before and after compaction, including re-reads/retries in subsequent turns. |

The main KPI is the actual measured total of **parent/child agent upstream input (including cache) + upstream output + Jev input + Jev output**. If reasoning tokens are a subset of output, do not double-count. For Claude, input is `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`; for OpenAI format, `cached_tokens` is treated as a subset of `input_tokens`. [Anthropic's usage spec](https://platform.claude.com/docs/en/build-with-claude/prompt-caching) · [OpenAI's cache spec](https://developers.openai.com/api/docs/guides/prompt-caching) Do not zero-fill missing runs; display them as incomparable. Do not treat a quality failure as a cheap success. Elapsed time is a secondary metric.

### Inputs needed for the next discussion

- The **current-version** `runs.jsonl`, `comparison.json`, per-request proxy events, or their storage location, from when the request-count increase occurred. Raw credentials or request bodies are not needed.
- Which of upstream LLM, Jev, actual tools, or control requests that "request count" refers to. The relevant host, model, task, and runtime settings.
- The scope of the process to be delegated to Jev. Candidate selection, skill-body supply, and fixed-argument call synthesis have different current capabilities, so decide the success criteria individually.

### Review of verification items and result confirmation (added 2026-09-24)

The following is the result and proposal of a verification-design investigation; test execution, implementation changes, and adoption decisions for the measure have not yet been made.

#### The two current test suites and the dashboard

| System | What it can already measure | Why it cannot judge the main goal |
|---|---|---|
| `test-x-cell.sh` | Externally verifies exit code, final-answer correctness, and non-mutation using a separate worktree of the same commit. [Execution](scripts/test-x-cell.sh#L74) · [Verification](scripts/summarize_x_cell.py#L234) | Default is a single run. `locate` instructs a sequential search/read of 5 functions but does not score actual tool calls; `module` only checks `go.mod`. Even in the selection comparison, changes to compaction and the reasoning setting are mixed in, and Claude's `advise` is missing from the current pass criteria for application. [Task](scripts/summarize_x_cell.py#L16) · [Condition](scripts/test-x-cell.sh#L93) · [Pass criteria](scripts/summarize_x_cell.py#L248) |
| `jev-routing bench` | Scores three chess tasks with a separate hidden verifier, and records upstream usage, cache, time, and Jev calls. [Task](internal/bench/tasks.go#L115) · [Verification](internal/bench/verify.go#L46) | Default is a single run, zero additional catalog entries. `--catalog`'s added tools are for Claude/Codex only, and running them causes an error, so it mainly measures avoidance of unneeded candidates rather than execution of required tools. Outcome scoring does not look at actual tool count, parallelism, or selection consumption. [Default](internal/bench/run.go#L90) · [Additional tools](internal/bench/mcpstub.go#L160) |
| Dashboard | Displays per-request events, upstream usage, and selection/application state for a single proxy process. [Data supplied](internal/proxy/dashboard.go#L49) | [The usage bar chart](internal/proxy/assets/dashboard.js#L79) only sums upstream usage and does not compute Jev usage, per-host total input, difference from the comparison source, or outcome quality. "Comparison effect" is a state display (non-applied, missing, etc.) rather than an actual measured difference, and pasted comparison JSON is only formatted for display. [Display](internal/proxy/assets/dashboard.js#L608) · [Paste](internal/proxy/assets/dashboard.js#L796) |

Additional measurement limitations: `bench`'s `Requests` is a count of all events including non-reasoning ones, Jev usage records only input, and missing values become zero when summed. [Aggregation](internal/bench/gateway.go#L201) · [Record](internal/bench/record.go#L19) `ObservedTools` deduplicates tool names for storage, so multiple uses of the same tool, call IDs, and parallelism cannot be reconstructed. [Extraction](internal/proxy/decision_event.go#L250) · [Storage](internal/proxy/events.go#L70) Events are capped at the most recent 1000, and there is no guarantee that bench aggregation via the dashboard captures all events for a long run. [Cap](internal/proxy/events.go#L10) · [Read](internal/bench/gateway.go#L177)

#### Metric contract: store the value, source, and missing-ness together

| Rank | Metric | Definition and use in judgment |
|---|---|---|
| Main metric | Actual measured total tokens for the whole task | Parent/child upstream input (including cache) + upstream output + Jev input + Jev output. Attach a source and availability to each value; if even one required value is missing, mark the total as "missing." Do not substitute byte differences or estimated savings. |
| Quality condition | External outcome, execution completion, setting match | Confirm success/failure with a hidden verifier or a precomputed answer. Record host, model, reasoning setting, task, commit, candidate catalog, and execution mode, and do not compare sets that do not match. Keep failed runs and their consumption in a separate bucket. |
| Intervention condition | Selection → application → upstream acceptance → actual tool execution | Distinguish `advise`, `filter`, `forced`, `direct`, and skill-body supply, and track whether the selection was actually used. Confirm that required tools are preserved, that call IDs correspond to results, and the number and parallel grouping of actual tool calls. |
| Root-cause analysis | Breakdown of requests and usage | Count upstream reasoning, Jev, control requests, host-internal retries, and child sessions separately. Show cache read/write/non-cache within upstream input, reasoning within output, Jev wait time, and total elapsed time. |

`bench`'s [report](internal/bench/report.go#L251) splits on/off by task name and compares **medians of each group**, without checking model/candidate/setting matches or repeated pairing. `x-cell`'s [median report](scripts/summarize_x_cell.py#L123) includes runs that succeeded in quality even if incomparable, and `improved` requires median improvement across input, output, and time all at once. Both should be kept separate from the main-metric effect judgment. First produce the per-pair total-token difference and ratio, then show their distribution, the comparable/total-run count, success rate, and missing-value count, per host and task. Do not further merge medians across all hosts or different tasks into a single median, hiding individual degradations.

#### Verification order and adoption gate (proposal)

1. **Confirm the measurement contract**: cross-check per-host usage subset relationships and the Jev response format against fixed data, to reach a state where request count, actual tool count, missing values, and event truncation can be correctly judged. Runs that fail to satisfy this do not get a savings rate computed.
2. **Per-host wire contract**: for each actual wire format, confirm reasoning-request arrival, candidate extraction, transform application, upstream acceptance, and tool-call/result correspondence. Do not mix unsupported paths into the performance comparison.
3. **Selection-only outcome tasks**: disable compaction and keep the reasoning setting fixed; keep the short single-tool task for verifying the measurement apparatus. Add independent multi-tool tasks, staged investigation/fix tasks, failure recovery, skills, and MCP/CLI tasks. Score required operations and the final outcome independently. Investigate, per request, serialization from candidate restriction, wrongful exclusion, and re-planning.
4. **Compaction-only long tasks**: actually trigger the host's compaction request, and confirm it does not directly touch tool definitions for normal requests, that required constraints/evidence and call/result correspondence are preserved, and that the subsequent outcome succeeds. Do not call it success based solely on character-count differences before and after compaction.
5. **Combining both features**: after each passes independently, confirm the interaction. Use the same host, model, and starting state, alternate the order of comparison conditions, and repeat. Decide the repeat count from the variance seen in small preliminary measurements, and do not decide adoption from a single median or a re-aggregation of medians.

Not having degraded quality is the precondition for a savings claim. The improvement of the main metric is judged from the total-token difference across comparable repeated pairs. Also retain the success rate across all runs, the consumption of failed runs, the most-degraded task, and the change in request count/time at the same time. This avoids the bias of selecting only successful runs.

#### QA perspective and minimal task set (proposal)

| Technique | Cases confirmed for this product |
|---|---|
| Equivalence partitioning | Per-host wire format, no-tool/single/independent-multiple/sequential/skill/MCP, compaction request vs. normal request, per application method. |
| Boundary values | Candidate count 0/1/the 3rd and 4th at the cost-gate boundary, zero/one/multiple results, just before/at/over the event retention cap. |
| Decision table | Compaction on/off × selection on/off, quality pass/fail × usage complete/missing, selection established/not established × upstream accepted/rejected. |
| State transition | Candidate selection → request change → upstream acceptance → actual tool call → result → outcome confirmation. Compaction request → substitute response → resume. Also look at mid-way failures and duplication on resend. |
| Error guessing | Jev failure/timeout, invalid answer, unknown history, partial usage, cancellation, child session, re-injection of the same skill. |
| Checklist | Conformance to actual host communication, independent outcome verification, setting match, complete measurement across all events, no double-counting of cache subsets, agreement between the dashboard and saved reports. |

#### Dashboard display order (proposal)

The dashboard should not create a new source of truth for aggregation, and should display the same metric definitions as saved runs. Place **comparison source, intervention condition, outcome success rate, comparable-pairs/all-runs count, total tokens and per-pair difference, missing values** at the top. If there is no comparison source, show "no comparison." Next, place the cache breakdown of upstream input, upstream output, Jev input/output, and the breakdown of reasoning requests/control requests/Jev requests/actual tool calls/retries, and finally elapsed time. Allow drilling down into individual requests, so the path from selection candidates to execution result can be traced. Do not infer whole-task reduction from a single process's dashboard display.

### Unification policy for `x-cell` and `bench` (added 2026-09-24)

The candidate policy is to unify the verification suite's entry point, repetition, measurement, result format, and comparison into `jev-routing bench`, while keeping the reading task and chess task as separate tasks with an external scorer. Both use the same routing core, but `x-cell` uses the no-proxy control and the production `run` path, while `bench` uses the proxy-pass-through control and a direct-launch path. This difference will continue to be recorded as a condition after unification. [x-cell](scripts/test-x-cell.sh#L90) · [bench](internal/bench/gateway.go#L28)

So that results can be recomputed and displayed in a different environment, save a versioned, allowlist-based measurement record per run under Git management. Do not directly publish raw logs, request bodies, tool arguments, skill bodies, or absolute paths. The current `results/` and `artifacts/` are outside Git management. [gitignore](.gitignore) The dashboard reads and displays saved records using the same aggregation definitions, and does not infer a savings effect from a single live process display.

Specific conditions, the verification items P1/R1–R4/S1/M1/C1/A1/I1/W1, the adoption order, the dashboard, and the storage format proposal are summarized in [the final improvement proposal for unified benchmark and result display](docs/requirements/unified-benchmark-validation.md). Implementation, actual measurement, and publication to GitHub have not yet been done.

### Adversarial review and reflection (added 2026-09-24)

Following a read-only review by `ateam single claude-opus-5-5 high` (run ID `06adcabda4f9`), [the final improvement proposal for unified benchmark and result display](docs/requirements/unified-benchmark-validation.md) was updated. The review targets were this memo, the draft verification spec, and the current `x-cell`, `bench`, and dashboard. The reviewer made no file changes and ran no real tests. Below are the results of cross-checking the review's points against the current code.

| Point | Judgment and reflection |
|---|---|
| Reasoning-setting changes other than selection | Confirmed. So the `bench`'s default `legacy` can be distinguished from a change applied at selection time, added explicit specification of `preserve` and a `reasoningChanged` violation to the incomparable conditions. [Default](internal/proxy/options.go#L56) · [Application](internal/proxy/rewrite.go#L480) |
| Missing usage becoming zero | Confirmed. Treat meter failure, fixed timeout, and event cap as measurement failure, and mark old format as incomparable if a required field is missing. [Current handling](internal/bench/run.go#L329) |
| Comparing only successful pairs | Confirmed. Made the total tokens across all starting runs, including failures, the main comparison, and also show total tokens per successful outcome. Demoted success-pairs-only comparison to a supplementary role. [Old aggregation](scripts/summarize_x_cell.py#L123) |
| Verification of parent+child totals | Confirmed. Since some current launch specifications disable child agents, added judgment of completeness for the child-using A1 task and for parent/child measurement. [bench](internal/bench/agents.go#L61) · [x-cell](scripts/test-x-cell.sh#L92) |
| Difference in measurement source between direct/passthrough | Confirmed. Made the main comparison passthrough-vs-intervention using the same proxy measurement source, and use `direct` for total-amount comparison only when CLI and proxy calibration has been achieved. |
| Cache degradation | Keep the main KPI of total-token reduction. Since cache breakage can worsen cost/speed even with the same total tokens, added it as an independent caution and an open adoption constraint. Not yet defined as an automatic failure. |
| Simplification of missing-value judgment | Do not adopt `metered < requests`, since it also marks paths that call no control requests/upstream as missing. Only reasoning responses that require usage form the denominator. |
| Contamination of scorer output | Confirmed the path receiving JSON lines from the stdout of a Node process that reads workspace code. [Read side](internal/bench/verify.go#L64) · [Execution side](internal/bench/assets/chess/verify.mjs#L192) Actual occurrence of spoofing is unconfirmed. Added authenticity of scoring and classification of scorer failure to the wire contract. |
| At-a-glance readability of the dashboard | Added a fixed vocabulary of improved/degraded/pending/unjudgeable, priority display of quality and missing values, a host×task listing, isolation of estimated values, and drill-down to per-request granularity to the spec. |

The minimum practical savings margin, tolerable quality difference, repeat count, and whole-product task weighting are not fabricated unmeasured values; they will be registered before the main comparison, based on preliminary measurement and the user's objective. Until these are decided, no overall "improvement" judgment will be issued.

### Confirming the measurement contract via fixed responses (added 2026-09-24)

User's specification: verification using a real LLM is based on `gpt-5.6-terra` at reasoning level `medium`. This confirmation did not call an external LLM, using only fixed responses and existing local tests. Whether the host accepts this model will be confirmed before an actual run, and results of different models will not be mixed into the same pair.

`go test ./internal/jev ./internal/proxy ./internal/bench` succeeded for all three packages. Existing fixed-response tests such as `TestFakePipeline` also succeeded, confirming that normal-path aggregation works. On the other hand, using Go's `-overlay` to run the following contract tests without changing the working tree:

| Contract executed | Expected | Actual |
|---|---|---|
| TypeSafe official-format Jev usage `input_tokens=296`/`output_tokens=20` | Input/output obtained | Both `nil`, a failure. `go test -overlay=/tmp/jev_routing_official_usage_overlay.json ./internal/jev -run '^TestOfficialUsageContract$' -count=1` |
| Intervention-side usage missing, control-side input 100 | Incomparable | Report failed, showing input 0 / 100% reduction. `go test -overlay=/tmp/jev_routing_missing_usage_overlay.json ./internal/bench -run '^TestMissingUsageCannotClaimSavings$' -count=1` |
| One control POST + one reasoning POST | 1 reasoning request | Failed with `Requests=2`. `go test -overlay=/tmp/jev_routing_meter_contract_overlay.json ./internal/bench -run '^TestMeter(CountsInferenceOnly|RejectsTruncatedEvents)$' -count=1` |
| An event with `historyTruncated=true` | Rejected as measurement incomplete | Accepted, a failure. Same overlay test |

The temporary overlay files are under `/tmp`; the repository code and tests were not changed. The failures confirmed with fixed responses have been reflected into the measurement contract of [the final improvement proposal](docs/requirements/unified-benchmark-validation.md). Confirmation of partial usage, child sessions, host retries, exceeding 1000 events, and token-reduction judgment on the real service have not yet been done.

### Re-confirmation after fixing measurement gaps (added 2026-09-24)

Based on the user's "please re-confirm," the measurement gaps reproduced with the fixed responses above have been fixed. The Jev-usage JSON tags were aligned to the official snake_case, and in the bench, control POSTs and requests that call no upstream were separated out from the LLM request count. Jev output is now recorded, and missing/partially reported upstream/Jev usage and event truncation are stored as meter errors. Reports mark the usage difference of incomplete runs as `n/a`, and add Jev output to the total tokens. Comparison charts reject incomplete usage and a control with no input.

The four reproduction tests from the Go overlays in the previous section all succeeded with the same expected values after the fix. New fixed-response regression tests were also added. `TYPESAFE_API_KEY= JEV_API_KEY= go test ./... -count=1` succeeded for all packages, and `git diff --check` and `gofmt -d` on the target Go files show no diff. The real-service LLM was not called. The baseline `gpt-5.6-terra`/reasoning level `medium` used for actual runs is maintained.

This re-confirmation is **part of the measurement contract**. Full capture of child sessions, host-internal retries, real-service comparisons for selection/compaction alone, displaying results on the dashboard, and normalized results saved to GitHub are not yet implemented/verified.

### External code review of the measurement fix (added 2026-09-24)

`ateam single claude-opus-5-5 high` (run ID `bc17022b0998`) reviewed the above code, regression tests, and documents read-only. The reviewer made no file changes, ran no tests, and made no real-LLM calls. Below are the results of cross-checking its points against the code in the parent session. **The points in this section are not yet fixed.**

| Judgment | Point and basis | What to confirm/fix next |
|---|---|---|
| High, confirmed | The [Jev-attempt record](internal/proxy/proxy.go#L778) for Devin Connect does not store usage. Meanwhile, [bench measurement](internal/bench/gateway.go#L269) treats missing Jev input/output as an error. An on-condition run that actually called Jev over the Connect path becomes incomparable. [JSON path](internal/proxy/proxy.go#L539) does include usage. | Record input/output on the Connect side too, and add a fixed-response regression test. |
| Medium, confirmed | [The report](internal/bench/report.go#L151)'s LLM request count and Jev call count do not check `MeterError`. Even if `RunRecord` is empty due to a meter-fetch failure or history truncation, a request-count reduction compared against the control can be displayed. | Set the comparison values for request count, Jev call count, and steering rate to `n/a` for incomplete runs. |
| Medium, conditional | [The request-path judgment](internal/proxy/proxy.go#L818) treats `/messages/count_tokens` as a reasoning request too. If Claude Code actually uses this path and the response has no `usage`, it becomes incomparable. Actual occurrence is unconfirmed. | Confirm reachability via real communication or a fixed response before classifying it as a control request. |
| Medium, held | The current meter treats usage-less failure responses such as 429/529/502 as incomplete. The reviewer also suggested a zero-count option, but there is no primary basis that billing/usage is actually zero. | Do not assume zero; record failure and unknown-usage separately. Decide the effect on adoption in the verification spec. |
| Low, wording issue | Excluding direct responses from the denominator of upstream request count is valid, since they call no upstream LLM. The existing name "Requests Jev steered" is unclear about the distinction from host requests. | Clarify the metric names/descriptions. |
| Low, confirmed | Some places in [the verification spec](docs/requirements/unified-benchmark-validation.md) and this memo still describe the pre-fix implementation as "current." | Standardize the investigation-time wording to "pre-fix." |

Concerns rejected by counter-evidence: since a cached Jev decision does not count toward `JevCalls`'s HTTP count, matching it against the non-cached attempt count is valid. [JSON path](internal/proxy/proxy.go#L544) · [Connect path](internal/proxy/proxy.go#L798) An event's `oldestSeq > 1` is also valid as truncation detection in the current bench, which launches a fresh proxy for each run. This review did not evaluate the real-service frequency or token-reduction effect.

### Fixing and re-confirming Devin and measurement-failure display (added 2026-09-24)

Based on the user's request, the "high, confirmed" and "medium, confirmed" points of the above review were fixed. The Devin Connect Jev attempt now sets `usageInputTokens`/`usageOutputTokens` the same as the normal JSON path. [Implementation](internal/proxy/proxy.go#L778) Added fixed values 31/7 to the existing Connect integration test, reproducing the pre-fix `nil` before confirming the post-fix record. [Test](internal/proxy/connect_devin_test.go#L668)

Report-derived metrics such as LLM request count, Jev call count, steering rate, and compaction/failed request count are now set to `n/a` if the run under the same condition has a `MeterError` or a usage-count mismatch. [Implementation](internal/bench/report.go#L174) Fixed data with a measurement failure on the intervention side and 10 requests / 1 Jev call on the control side used to display "LLM requests −90% · Jev calls −100%" before the fix, and now shows the comparison values as `n/a` after the fix. [Test](internal/bench/bench_test.go#L30)

`TYPESAFE_API_KEY= JEV_API_KEY= go test ./... -count=1` exits 0 for all Go packages. `git diff --check` and `gofmt -d` on the target files show no diff. The real-service LLM was not called. Claude's `count_tokens` reachability during review, the actual usage of failed upstream responses, and host-internal retries remain unconfirmed.

### Fixed wire-contract tests for Claude Code and Codex (added 2026-09-24)

Per the user's specification, Claude Code and Codex, the primarily used hosts, were confirmed first. Claude's fixed request used `claude-sonnet-5` and `output_config.effort=medium`; Codex used `gpt-5.6-terra` and reasoning level `medium`. The real service was not called; verification used a simulated upstream. Claude's effort field and [`POST /v1/messages/count_tokens`](https://platform.claude.com/docs/en/api/typescript/messages/count_tokens) were cross-checked against [the official docs](https://platform.claude.com/docs/en/build-with-claude/effort).

- Claude: passed `/v1/messages/count_tokens` and `/v1/messages` through under the same model condition, confirming unmodified body forwarding, classification of control vs. reasoning requests, and response usage. Before the fix, the counting request was also counted as a reasoning request (2 total) and treated as usage-missing. After excluding the official counting path with `looksLikeLLM`, it obtained 1 reasoning + 1 control request, with input 10 · cache read 20 · write 30 · output 5. [Implementation](internal/proxy/proxy.go#L818) · [Test](internal/proxy/gateway_observe_test.go#L113)
- Codex: narrows local candidates from Responses Lite's `input.additional_tools`, keeps the provider-side `web_search`, and preserves the model and reasoning level `medium`. Obtained input 36 · output 8 · cache 22 · reasoning 3 from the simulated upstream's SSE, and confirmed `/v1/telemetry` as an unmodified control request. [Test](internal/proxy/codex_fixed_contract_test.go)
- The Codex fixed test also confirmed that the default `JEV_REASONING=legacy` rewrites `medium`, so per the selection-only comparison policy, `ReasoningPreserve` was explicitly set. Do not confuse the effect under the default setting with passing this fixed test.

`go test ./internal/proxy -run '^Test(ClaudeFixedMessagesAndCountTokens|CodexFixedWireContract)$' -count=1` and the same under `-race` succeeded. `TYPESAFE_API_KEY= JEV_API_KEY= go test ./... -count=1` also succeeded for all Go packages. These are fixed wire contracts, and do not yet demonstrate reachability or total-token reduction with the real CLI/real model for Claude Code/Codex.

### Real-CLI connectivity under subscription authentication (added 2026-09-24)

Per the user's question, the simulated upstream was kept as a fixed wire-contract test, and real-service connectivity using subscription authentication was separately confirmed. Credential values were neither displayed nor saved. Before/after execution, `codex login status` showed `Logged in using ChatGPT`, and `claude auth status --json` showed `authMethod=claude.ai` · `subscriptionType=max` · `apiProvider=firstParty`. `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY`, and `CODEX_ACCESS_TOKEN` were unset, and Claude's checked config files had no API-key override either. [OpenAI's auth docs](https://learn.chatgpt.com/docs/auth) · [Claude Code's env-var docs](https://code.claude.com/docs/en/env-vars)

Built `/tmp/jev-routing-subscription-smoke` from the current source, and had both CLIs read the module and Go version from `go.mod`. Claude Code used `claude-sonnet-5`/`--effort medium`, Codex used `gpt-5.6-terra`/`model_reasoning_effort=medium`. Each ran `baseline` and `filter`+`JEV_SELECTION_MODE=local`, with `JEV_REASONING=preserve`, `JEV_COMPACTION=off`, `JEV_AUTO_APPLY=off` fixed. The Jev key was unset, with zero external Jev calls. Forwarded to the default real service from the proxy without switching to an API-key method. [Launch environment](internal/host/host.go#L123) · [Default upstream](internal/proxy/proxy.go#L241)

| Host | Condition | Upstream requests | Selection applied | Input | Cache read | Cache write | Output | Total tokens |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| Claude Code | baseline | 2 | 0 | 4 | 105060 | 105641 | 289 | 210994 |
| Claude Code | filter · local | 2 | 0 | 4 | 188846 | 22100 | 534 | 211484 |
| Codex | baseline | 2 | 0 | 55779 | 31232 (subset of input) | 0 | 196 | 55975 |
| Codex | filter · local | 2 | 1 | 55299 | 3840 (subset of input) | 0 | 164 | 55463 |

All four runs exited the CLI with code 0, every upstream reasoning response was HTTP 200 and complete, and the final answer matched the module and Go version in `go.mod` for all of them. Summing per-request upstream input/output/cache with the host-specific formula matched the usage reported by the Claude Code/Codex CLI for each run. The working tree has no new changes from these runs. Local raw output and per-request records are kept at `/tmp/jev-<host>-subscription-{smoke,filter}.{out,err,json}`, and this table is a derived value that contains no request bodies or credentials.

**These differences are not a saving effect.** Each condition is a single independent session with different execution order and cache state. In Claude's filter run, application was 0 even with a local selection present, and Jev was unused by either host. Subscription-based proxy connectivity and usage retrieval were confirmed, but Jev's selection effect, repeated quality/total-token measurements, and billing amounts were not confirmed. Per official docs, Claude Code's default for MCP tool discovery differs under a non-first-party `ANTHROPIC_BASE_URL`, so this condition will also be recorded in later comparisons. [Claude Code's env-var docs](https://code.claude.com/docs/en/env-vars)

### Final effect judgment and timing of publication (added 2026-09-24)

Per the user's point, the order was clarified. The current four runs are a connectivity check of subscription authentication, communication, and usage retrieval, and Jev substitution has not been fixed/verified. The Jev key was also unset, and Claude's local selection had zero applications. Repeating in this state would only produce **a diagnosis of current behavior**, and is not used to decide final total-token reduction.

First, actually establish the path from Jev judgment to host application, and verify the required tool/skill consumption and external quality. Then, judge the final effect via a repeated comparison under pre-registered conditions. Even when measuring the current version for diagnosis, distinguish it from the post-fix results.

Publication of measurement records/documents to GitHub will not happen until all work is complete. Intermediate raw logs are kept locally, and the final record format and content will be audited before any publication.

### Confirming from Jev judgment to real-host usage and outcome (2026-09-24)

With `JEV_SELECTION_MODE=jev`, `JEV_ROUTING_MODE=filter`, `JEV_REASONING=preserve`, `JEV_COMPACTION=off`, `JEV_AUTO_APPLY=off`, had all four real CLIs perform a read-only `go.mod` extraction. Claude Code used `claude-sonnet-5`/`medium`, Codex used `gpt-5.6-terra`/`medium`, Devin CLI used `gpt-5-6-terra-medium`, and Grok Build used the default `grok-4.6-build`. The real Jev key was set in the environment but its value was neither displayed nor saved. Host API keys were unset, and Claude Code/Codex used subscription authentication as confirmed earlier. The final answer matched `module github.com/nekowasabi/jev-routing` and `go 1.25.0` from `go.mod` for all hosts.

| Host | Jev success/calls | Applied to the request | Evidence of tool execution | Judgment-side tokens this time |
|---|---:|---:|---|---:|
| Claude Code | 3/3 | 0 | CLI record and result of `Read` twice. Zero Jev advice insertions | Input 57,242 · output 6,410 |
| Codex | 1/1 | 0 | `go.mod` read command and correct answer present. No change, due to insufficient Jev candidate coverage | Input 8,543 · output 155 |
| Grok Build | 1/1 | 1 | `read_file` selected, candidates 25→2, same-named tool's result confirmed | Input 17,692 · output 334 |
| Devin CLI | 2/2 | 1 | `read` selected, candidates 25→1, correct answer. No record of tool-call ID or result | Input 33,572 · output 692 |

Codex separately confirmed, under the same settings with a simulated Jev judgment, choosing `exec`, a rewrite of candidates 3→1, execution, and a correct answer. Do not read a simulated judgment's success as a real-Jev success. For Claude Code, both simulated and real Jev judgments succeeded on their own, but `selectionApplied=0`. By implementation, Claude Code does not narrow tool candidates, and only appends advice when the most recent user message contains a `tool_result`. [Implementation](internal/proxy/rewrite.go#L259) · [Fixed test](internal/proxy/claude_advise_test.go#L50). Even in this sequential-read case, application meeting that condition was not observed. The mismatch between the real host's request shape and this condition needs investigation.

For skills, `TestRunAppliesSkillOnNormalPath`, which passes all four hosts through a simulated upstream, succeeded. However, this test names the skill explicitly, and since `plan.Route`'s explicit specification is chosen before Jev, it is not evidence that Jev chose the skill. [Path](internal/plan/routing.go#L73) · [Test](cmd/jev-routing/run_apply_test.go#L18). In a trial giving the real CLI a simulated Jev and a single skill, both Claude Code and Codex had an empty `lastDelivered`, and the final answer did not include the skill-specific `proof` value. Claude Code's answer noted that it judged the value to be an injected instruction, but since delivery from the proxy could not be confirmed, this is not interpreted as success or failure of skill adoption. The current `autoApply` includes the host's tools in the same candidate set, so if local tool selection takes priority, it does not proceed to Jev selection for the skill. Skill delivery and model-side adoption should be verified separately. [Implementation](internal/proxy/proxy.go#L833) · [Selection order](internal/plan/routing.go#L87)

**Current judgment remains only partially established.** Grok confirmed judgment → request change → same-named tool's result → correct answer. Codex confirmed the same chain in the simulated judgment, but had insufficient candidate coverage in the real-Jev run. Devin CLI went through judgment → request change → correct answer, but the correspondence with the tool result is missing. Claude Code and skills could not be confirmed as actually applied. A single-shot correct answer is not evidence that Jev maintained quality and reduced total tokens. In particular, judgment-side token consumption needs to be summed and compared repeatedly. Raw output/stats are stored locally at `/tmp/jev-<host>-real-application.{out,err,json}` and `/tmp/jev-<host>-mock-{application,skill}*`. Not published to GitHub.

### Bench/measurement/intervention implementation and single-shot diagnosis (2026-09-24)

The non-application described up through the previous section is the **pre-fix** record. Added `dual-facts`, which calls two demonstration MCP tools separately, and `skill-proof`, which scores implicit Jev skill selection by a synthesized outcome value, to `jev-routing bench`. The existing chess task is used for multi-step hidden scoring. The bench fixes `JEV_COMPACTION=off` · `JEV_REASONING=preserve`, and explicitly sets Claude Code to `claude-sonnet-5`, Codex to `gpt-5.6-terra`, with both at reasoning level `medium`. Run with subscription authentication as-is; host API keys unset.

Each run is stored in `runs.jsonl` and per-request `proxy-events.json`, and control/intervention pairs are stored in a versioned allowlisted `comparison.json`. Total tokens normalize the host's cache subset and sum upstream input/output and Jev input/output. The reduction value is set to `null` when Jev is not applied, the external outcome fails, the required two tools' call ID/result are missing, usage is missing, the setting/actual-sent model mismatch, or there is a mismatch with the CLI's main-model usage. Codex's `codex-auto-review` was not included in the CLI's turn usage, but was recorded by the proxy as a separate model's reasoning request and added to the total. The input/output of the two actual requests matched the difference between CLI and proxy. The dashboard reads the saved `comparison.json` in the browser and displays the same difference, quality, comparable count, and reason. Confirmed the file input for both simulated and real runs via UI operation.

| Real-CLI single pair | Control quality | Intervention quality | Control total tokens | Intervention total tokens | Control − Intervention |
|---|---:|---:|---:|---:|---:|
| Claude Code · `dual-facts` | 3/3 | 3/3 | 52,765 | 67,123 | **−14,358** |
| Codex · `dual-facts` | 3/3 | 3/3 | 140,962 | 156,555 | **−15,593** |
| Claude Code · `skill-proof` | 2/3 | 3/3 | — | — | **Incomparable: quality mismatch** |

The left/right MCP results of `dual-facts` and the CLI's main-model usage were cross-checked and matched for both hosts. Codex's tool was executed with a sandboxed auto-approval review. `skill-proof` confirmed selection by Jev, skill delivery, and adoption of the synthesized value by real Claude Code. The table is a **diagnosis of current behavior** for a single run per condition, not an adoption decision on a repeated saving effect. Notably, total tokens increase in the two-tool task. Stored at `/tmp/jev-bench-dual-claude-verified/`, `/tmp/jev-bench-dual-codex-final/`, and `/tmp/jev-bench-skill-live/`. Credential values and raw logs are not published to GitHub.

After this section's record, the `direct` condition, `x-cell`'s common runner, Claude Code parent/child measurement, and Devin's call-ID correspondence were added. The current dashboard's bench section manually selects a comparison-result file, showing a different saved result from the live-request lower display. A repeat-count- and threshold-fixed effect judgment has not been carried out.

### Real-bench diagnosis for Grok Build / Devin CLI (2026-09-24)

Ran the common `xcell-module` task with real Jev and real CLIs on both hosts. Both hosts' control and intervention quality passed 3/3. However, usage is missing, so the token-reduction amount was marked incomparable. Single-run time differences are not used for adoption. Logs are stored locally at `/tmp/jev-grok-xcell-20260924/`, `/tmp/jev-devin-xcell-20260924/`, and the post-fix Devin at `/tmp/jev-devin-wire-fixed/`.

| Host | Real Jev and application to the request | Call ID/result | Usage state |
|---|---|---|---|
| Grok Build · `grok-4.7` | Selected `read_file` on intervention and changed the request. Both control and intervention scored outcome 3/3 | Confirmed control 9/9, intervention 8/8 app-level results | Each `/v1/responses` at termination was canceled with no usage. Added a fix excluding control requests `/sessions/.../signals` and `/turn-deltas` from the reasoning count. Needs remeasurement |
| Devin CLI · `gpt-5-6-terra-medium` | Narrowed candidates from 25 to `read`/`write` etc. on intervention. Both control and intervention scored outcome 3/3 | Confirmed the real Connect's field6 has call ID, name, and arguments, and field7/field3 have result ID and body. Confirmed all four post-fix intervention calls share the same result ID | The Connect reasoning response provides no usage, and the proxy record has both input and output missing. No usage in CLI stdout either |

The old Devin observation had mistaken UUIDs and `call_` identifiers for the `Shell` tool-catalog entry as actual tool names, and could not record the actual calls/results. Examined the anonymized protobuf structure of a real request and added a fixed test that extracts only the corresponding fields. A real-host rerun cross-checked 4 calls and 4 results for `read`, `write`, and `find_file_by_name`. Devin's `--export` conversation-record output may include `final_metrics.total_prompt_tokens` etc., but generating this output for the bench and reconciling it with the proxy is not yet verified. Therefore, Devin's token-reduction KPI is not currently displayed.

Subsequently, added a fix to avoid mistaking UUIDs contained in native-history bodies for tool calls, and a fix to not count protobuf leaves without an ID as a call start. In the final code, `/tmp/jev-devin-final-wire/` showed quality 3/3, 4 real Jev calls, and cross-checked 3 sets of actual call ID and result for `read`, `write`, and `find_file_by_name`. Usage for the 5 reasoning responses remains entirely missing. Old-format `exec` results without an ID are associated with one incomplete call by existing compatibility handling, so they are not counted as evidence of strict ID correspondence.

Incorporated Devin's `--export` into the bench, verifying `session_id`, non-empty `steps`, and non-negative `final_metrics` of the conversation record, and stored only the numbers in `HostTranscript`. In the real run `/tmp/jev-devin-export-live/`, obtained `promptTokens=60,277`, `completionTokens=384`, `cachedTokens=0`, `steps=12`, with outcome 3/3. The raw conversation record was deleted after collection. Since this host-side aggregate cannot be cross-checked against per-request proxy usage, the missing value is not filled in, and the token-reduction KPI remains incomparable.

### Unifying `direct`/`x-cell` and parent/child measurement (2026-09-24)

`jev-routing bench` runs `--modes direct,off,on` with the same task and scorer. `direct` does not go through the proxy, and since the total usage of host/child/Jev cannot be confirmed, keeps a separate row for comparison type `direct` in `comparison.json` with the difference set to `null`. The main comparison of `off→on` is kept as a separate row. The old `scripts/test-x-cell.sh` is now a thin entry point calling the same `bench`, with `xcell-module` and `xcell-locate` registered to the common runner. Externally verifies the answer and that the source is unmodified. The sequential-10-call order specified in `xcell-locate`'s instructions is not currently a scoring target.

For real Claude Code (`claude-sonnet-5`/`medium`), a single `xcell-module` run had quality 3/3 for `direct`, `off`, and `on` alike. `off` total 69,610, `on` total 113,807, so `off−on` is **−44,197 tokens**. `direct−on` was shown as incomparable despite matching quality. Local record at `/tmp/jev-bench-direct-claude/`. This single pair's increase is not a judgment of repeated effect.

For real Claude Code's actual requests, `metadata.user_id` was converted to an anonymized per-run key. However, since the real `Agent` child session used the same key and the same model name as the parent, they could not be separated by key alone. Cross-check the main session's CLI usage against the **unique combination** of per-request input/cache read/write/output from the proxy, and classify the rest as child. Mark as incomparable when there are multiple solutions, missing usage, exceeding the verifiable request-count cap, or missing Agent results. Raw identifier values are not recorded. This method has been confirmed for short child tasks, and does not claim to identify an arbitrarily long parent/child session.

| Real Claude Code · `child-facts` single pair | Control | Jev intervention |
|---|---:|---:|
| External outcome | 3/3 | 3/3 |
| Parent session / child session | 1/1 | 1/1 |
| Child tokens (including Jev) | 17,818 | 37,945 |
| Parent+child+Jev total tokens | 81,627 | 122,579 |

Both conditions cross-checked the Agent call ID and result, the parent's CLI usage, and the two child-attributed requests, giving `off−on` = **−40,952 tokens**. The recomputed `comparison.json` also stores the child count and usage, and the dashboard displays it. Local record at `/tmp/jev-bench-child-claude-pair/`. The same identification has not been confirmed for child sessions of Codex, Grok Build, or Devin CLI. A run whose child cannot be observed and attributed does not display the difference.

For Grok Build, [the official headless-output spec](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/14-headless-mode.md)'s CLI main-model usage, after adding cache read to input, matched the proxy's completion requests (`/tmp/jev-bench-grok-usage-verified/`). Requests for the auxiliary model `grok-4.6` are recorded separately. Meanwhile, the single `grok-4.7` cancel request at termination had no usage, so matching against the host-side main-model total alone cannot prove zero consumption. Therefore the KPI remains incomparable. Devin CLI's `--export` aggregate is likewise a reference value only, and does not fill in the missing Connect response usage.

### Alternative usage sources, call order, and repeated-run judgment (2026-09-24)

Reconsidering the previous section's usage judgment, adopted **the verified session total as a separate source, without filling in per-request missing values**. For Grok Build, cross-check the CLI's `usage_is_incomplete`, per-model call counts, input/output, and cache breakdown against the proxy's completion requests. The individual usage of canceled requests remains unknown, but the CLI aggregate covers all completed main requests, with auxiliary requests added separately on the proxy side. For Devin CLI, use the session total only when each ATIF-v1.7 agent step's input/output/cache sum matches `final_metrics`, the model name matches the specified value, there is no child trace, and all of Connect's reasoning requests have missing usage. Both are stored with source `grok_cli_reconciled`/`devin_atif_steps`. `/tmp/jev-grok-host-source-final/`'s `xcell-module` had quality 3/3 each, total 269,843→269,919 (control − intervention **−76**). `/tmp/jev-devin-leaf-verify-2/` also had quality 3/3 each, and after confirming the ATIF model name `gpt-5-6-terra-medium`, gave a comparable single pair of 322,758→299,994 (**+22,764**). Both are single-pair diagnoses, not an adoption decision on the saving effect.

`xcell-locate`, in addition to the 5 answer items, also confirms per-definition distinct call IDs for search and read, results, targets, and order. In the real Claude Code run, `/var`/`/private/var` pointing to the same file on the read path was rejected by string comparison, so this was changed to a same-file check. A subsequent trial confirmed 10 operations and a correct answer on the intervention side, but the control side searched multiple names at once, so it was marked `tool_sequence_unverified`. The task text was clarified to require independent search/read per name. In Codex, a trial where local definition search deleted all tools and the answer was not written to a file resulted in quality 0/5. Explicitly sequential tool tasks were changed to not be substituted by local search. Afterward, both off/on confirmed 10 operations and a correct answer, but with a candidate count of 3, Jev calls were 0 due to the savings gate, making it incomparable as `jev_not_applied`. Even in a trial adding 2 candidates, the actually sent candidate count was still 3, with quality 4/5 and 0 Jev calls. Do not read a correct answer or reduced request count as a Jev improvement.

For child tasks, separated Grok's `spawn_subagent` and `get_command_or_subagent_output`, and Devin's `run_subagent` and `read_subagent`, each as one launch and one result-fetch, so as not to miscount the child count as 2. Codex's real trial had a failed child launch and outcome 0/3, with no independent child request either. Call ID and result reception alone cannot prove child reasoning, so independent usage is required. Grok's parent CLI's 9 requests and the proxy's 11 usage-bearing requests could be uniquely split into 9 parent + 2 child, but an additional cancel request had missing usage, making the parent+child total incomparable. Devin's real trial had outcome 3/3 and confirmed child launch and result fetch, but the retained ATIF had no child trace, child reference, child model, or child usage, and the 14 Connect reasoning requests also had missing usage. The parent ATIF is not diverted to the parent+child total, making it incomparable. Per [Devin's sub-agent spec](https://docs.devin.ai/cli/subagents), `subagent_explore`'s model may be chosen independently of the parent's specified model. Added a fix to exclude `--keep`-retained work areas from the next bench's orphan cleanup, and the raw ATIF was deleted locally after parsing.

Repeated-run effect fixes `--min-pairs 6` · `--min-savings-pct 0` in advance, and is judged by the median total-token reduction rate per pair and an exact binomial order-statistic 95%+ interval. If external quality degrades, that takes priority as a fail; if there are incomparable pairs, effect judgment is impossible; if the interval straddles the threshold, it is held. In real `dual-facts` runs of 6 pairs each, Claude Code showed median **−14,377.5 tokens** · reduction rate **−27.24%**, judged as an increase; Codex showed median **−13,432.5 tokens** · reduction rate **−9.55%**, but the interval straddled zero and was held. Local records at `/tmp/jev-bench-dual-claude-reps6/` and `/tmp/jev-bench-dual-codex-reps6/`. The single-shot Grok/Devin pairs are held for insufficient repetition. Not published to GitHub.

### Handoff to another machine (work stopped 2026-09-24)

#### Purpose / current state

Handle `direct`/`off`/`on`, `x-cell` tasks, and outcome/actual-application/total-tokens for four hosts using the common `jev-routing bench`, and for runs with a child, judge without zero-filling missing values. The main metric is the upstream parent/child and Jev's **cache-inclusive total tokens**. Quality is a precondition, time is a secondary metric. Compaction is a separate condition from selection. Code and this memo are **uncommitted** on the `bdd5251` work tree. Publication/push to GitHub, per the user's instruction, has not been done until all work is complete.

#### Completed changes and rationale

- Implemented in `internal/bench/run.go` · `comparison.go` · `effect.go` · `report.go` and the dashboard: same-setting pairing, sourced total tokens, gating on quality/Jev-application/required-operations/missing-values, and effect judgment for 6+ pairs. `direct` scores the same task's quality, but the total-amount difference is `null`. `scripts/test-x-cell.sh` calls the common runner, defaulting to Claude `claude-sonnet-5`/medium, Codex `gpt-5.6-terra`/medium, and Devin `gpt-5-6-terra-medium`.
- `internal/bench/xcell.go` confirms `xcell-locate`'s independent 5 searches and 5 reads by call ID, successful result, target, and order. Accepts macOS's `/var`/`/private/var` as the same file. `internal/proxy/lookup.go` no longer answers a task specifying sequential operations using local search alone.
- `internal/bench/grok_usage.go` cross-checks Grok CLI's full model usage against completed requests. When auxiliary requests are mixed in under the same proxy model name, only accepts a **unique subset** of per-request usage. Does not fabricate individual usage for canceled requests. `internal/bench/devin_transcript.go` verifies ATIF step sums, final aggregate, and model. `internal/bench/usage_source.go` only adopts a verified session total. `report.go` also displays this source's total.
- `internal/bench/child_hosts.go` · `gateway.go` separate child launch and result fetch, and for Codex and Grok require independent child-request usage attribution. A run where Devin's child usage/model cannot be confirmed is incomparable. Places a `.bench-keep` in `--keep`-retained work areas, excluding them from subsequent bench orphan cleanup. Raw ATIF is deleted in normal runs.

#### Measured results and storage locations

| Target | Record | Judgment |
|---|---|---|
| Claude Code `dual-facts` 6 pairs | `/tmp/jev-bench-dual-claude-reps6/` | All comparable, reduction-rate median −27.24%, **increase** |
| Codex `dual-facts` 6 pairs | `/tmp/jev-bench-dual-codex-reps6/` | All comparable, reduction-rate median −9.55%, interval straddles zero, **held** |
| Grok Build `xcell-module` 1 pair | `/tmp/jev-grok-same-model-fixed-20260924/` | Quality 3/3 each, reconciled total 404,364→270,444, effect held due to single run. Also confirmed the summary total display via `bench report` |
| Devin CLI `xcell-module` 1 pair | `/tmp/jev-devin-leaf-verify-2/` | Quality 3/3 each, ATIF-sourced total 322,758→299,994, effect held due to single run |
| Claude Code `child-facts` 1 pair | `/tmp/jev-bench-child-claude-pair/` | Uniquely attributed 1 parent + 1 child each, 81,627→122,579 |
| Grok Build repeated-run interruption | `/tmp/jev-grok-xcell-reps6-fixed-20260924/` | 2 pairs had quality/application/usage all available, differences −10,889/−64,401. During the 3rd pair's control run, received the user's stop instruction and stopped on the termination signal. The 3rd pair is incomplete, **repeated-run effect not judged** |

For `xcell-locate`, there are trials confirming 10-operation evidence on Claude Code's intervention side and on both Codex conditions. However, there is a trial where Claude's control merged the searches, a trial where Codex did not call Jev due to the savings gate, and a trial with Codex quality 4/5, so these reduction values are `null` in `comparison.json`. In Grok/Devin's current final logs, the target/order cannot be proven, so this task is incomparable. The child task is also incomparable for both, since Grok's cancel usage and Devin's child trace/model/usage are missing, making the parent+child total incomparable for both. The Devin ATIF originals were deleted after analysis.

#### Verified scope

Before work stopped, `go test ./... -count=1` succeeded for all Go packages. `python3 scripts/test_summarize_x_cell.py` succeeded with 17 cases, `node --test internal/proxy/assets/dashboard.test.mjs` succeeded with 17 cases, and `bash -n scripts/test-x-cell.sh` and `git diff --check` also succeeded. After these latter two language tests, only Go, documentation, and the shell's host-model arguments were changed, and the shell syntax was re-verified. The long-running Grok repetition was terminated midway by the user's stop instruction, and the incomplete part is not counted as success.

#### Next steps

1. **Move the work tree.** There are many untracked/uncommitted files, including `docs/MEMO.md` and `docs/requirements/unified-benchmark-validation.md`. Getting only the Git commits on another machine will not bring over these changes. Transfer the work tree safely, then confirm agreement with `git status --short`. The raw `/tmp/jev-*` logs are for local diagnosis only; scrutinize the content before transferring/publishing. Do not push to GitHub.
2. **Complete the repetitions.** Do not use Grok Build's interrupted series for effect judgment; after confirming the environment, take a new 6 pairs with `JEV_SELECTION_MODE=jev go run ./cmd/jev-routing bench --agent grok --tasks xcell-module --modes off,on --reps 6 --out <new local result destination>`. Then take the same task/6 pairs for Devin CLI with `--agent devin --model gpt-5-6-terra-medium`. This uses the real service and quota. For each pair, check `comparison.json`'s quality, Jev application, and usage source before reading the effect column. If an incomparable result appears mid-way, fix the cause, and do not retroactively insert success values into old data.
3. **Handle unmet measurement contracts.** Grok's child cancel requests and Devin's child different-model/usage cannot currently be obtained. Unless the provider offers a complete child-usage source, keep `child_session_unverified`/`usage_incomplete`. Grok/Devin's `xcell-locate` operation order also cannot be proven from the raw logs. The between-group estimate of "total tokens per outcome including failures" described in `docs/requirements/unified-benchmark-validation.md` is not yet implemented, and the current effect judgment is limited to cases where all pairs pass quality.
4. **Finally, organize the outcome.** Load the four hosts' results into the dashboard, and confirm the totals, sources, and reasons for incomparability match `comparison.json`. Do not publish until the necessary fixes are complete.

#### Constraints and notes

Claude Code/Codex used subscription authentication, with host API keys unset. TypeSafe key values were neither displayed nor saved. Do not place secrets, raw logs, or the work tree directly on GitHub. `--keep` retains the work area and Devin's raw ATIF, so process them locally after diagnosis. The other machine's auth state, tool versions, and quota are unconfirmed.

### Grok Build / Devin CLI 6-pair repetition (after the 2026-09-24 handoff)

Carried out step 2 of the handoff. In the first Grok series `/tmp/jev-grok-xcell-reps6-20260924b/`, only rep1's control run had its main model resolve to `grok-4.6`, becoming incomparable with `settings_mismatch` (effect judgment `incomplete_pairs`). The cause was that `bench` fills in the default model for Claude/Codex only when `--model` is unspecified, and left model resolution for the Grok CLI up to its own startup handling without passing `--model`. `~/.grok/config.toml`'s default is `grok-4.7`, but all 8 requests were `grok-4.6` only on the series' first launch. It remains unconfirmed why only the first launch behaves this way internally in the CLI. Added `grok` → `grok-4.7` to the [default-value fill-in](internal/bench/run.go#L140), excluded this series from effect judgment, and retook it fresh with `--model grok-4.7` explicit. The auxiliary session-title-generation request that appears in every run (`grok-4.6`, input approx. 480) appears to be a CLI feature, and is added separately as an auxiliary-model request.

| Host · task | Record | Comparable/total pairs | Median total-token difference | Median reduction rate (95%+ interval) | Judgment |
|---|---|---:|---:|---|---|
| Devin CLI `gpt-5-6-terra-medium` · `xcell-module` | `/tmp/jev-devin-xcell-reps6-20260924b/` | 6/6 | +87,730.5 | 57.56% (41.86%–71.07%) | **Decrease** |
| Grok Build `grok-4.7` · `xcell-module` | `/tmp/jev-grok-xcell-reps6-20260924c/` | 6/6 | +14,339.5 | 11.30% (−2.51%–48.73%) | **Held** (interval straddles zero) |

Both series had quality 3/3 across all 12 runs, Jev application on the intervention side, the real model as specified, and were judged with the pre-fixed `--min-pairs 6` · `--min-savings-pct 0`. Devin's usage source is `devin_atif_steps` (ATIF step sums, matching `final_metrics`, no child trace), and Connect's per-request usage remains missing. So Devin's "decrease" is a judgment from this alternative source of verified session totals. Devin's intervention-side totals were consistent across all 6 pairs at roughly 63,000–82,000, while the control side varied from 112,723 to 228,230. Grok's per-pair differences were −2,064 / −218 / −3,172 / +28,897 / +30,568 / +123,160, with 3 pairs showing a slight increase. Control rep6 (252,758) is an outlier.

This judgment is limited to `xcell-module` (a single reading task). Do not read it into other tasks, skills, sub-agents, or whole-product improvement. It is also not compared across hosts, since it differs in task from Claude Code/Codex's `dual-facts` results (increase/held).

### Analysis of the increase in Claude Code `dual-facts` (2026-09-24)

We adopted a policy of improving hosts one at a time, and started with Claude Code. Since the record of the previous six pairs exists only on the previous machine, we re-measured six pairs with `--catalog 2`, `claude-sonnet-5`/`medium` (`/tmp/jev-bench-dual-claude-reps6-b/`). All pairs were comparable with quality 3/3, and the per-pair difference ranged from −14,096 to −14,425. The median reduction rate was **−27.25%** (interval −27.32% to −26.54%), and the verdict was **increase**. This reproduced the previous result (−27.24%).

| Item (median of on−off) | Tokens | Share of total difference |
|---|---:|---:|
| Jev input | +13,661.5 | 94.9% |
| Jev output | +618 | 4.3% |
| Upstream input + cache read + write + output | +118.5 | 0.8% |

We confirmed that in all 12 runs, the total tokens matched the sum of each item. In rep1 only, the upstream cache read/write swapped by about ±8,000, but the impact on the total was small; the cause was not identified.

By request (rep2), both off and on had three upstream inference requests, with nearly identical order, tools, and upstream usage. On called Jev once per request for all three requests, averaging about 4,550 input and 206 output per call (per-call breakdown was not recorded, so this is an even split estimate). The verdict matched the actual next action in all three cases. However, the first was observation-only and not applied, and the second and third were inserted as advice (upstream input +38 to +40). No cache-prefix breakage from the advice was observed.

**Conclusion**: Claude Code's current intervention (`advise`) does not remove tool candidates ([implementation](internal/proxy/rewrite.go#L262)) and does not change the number of requests, so it has no mechanism to reduce upstream tokens. In this task, the main model already solves it in the shortest possible three requests, so there is no room for advice to improve things. 99% of the increase is the cost of Jev's judgment, which sends the description of all candidate tools and recent history for every request ([judgment request](internal/proxy/rewrite.go#L1248), [accompanying info](internal/proxy/judgment.go#L110)). To achieve a net reduction on Claude Code, one of the following is needed: (1) omit Jev calls that have no effect (judgments that are not applied, judgments on the final response), (2) shrink the Jev input, or (3) switch to an intervention that actually reduces upstream inference or turns. Also, if the task is not one where the main model wastes extra turns from hesitation, the effect of (3) cannot be measured. Which to adopt is undecided.

### Claude Code: examining reduction of Jev judgment cost and replacement of upstream inference (2026-09-24)

Per the user's choice, we implemented (1) and (2) from the previous section. (1) On Claude's advise path, Jev is not called for requests that have no `tool_result` to insert advice into (reason `no_advise_target`; this judgment was already being discarded unapplied). (2) From Jev's judgment request, we removed `recent_tool_results` whose content duplicates `actions_taken`. Also, only for Claude, we shortened the candidate tool description text to the first paragraph and within 300 bytes ([shortenCriteria](internal/proxy/transform.go)). We did not change the tool definitions sent upstream. We did not change the judgment requests for other hosts. We added regression tests, and `go test ./...` passed with 557 tests. Not committed.

| `dual-facts` · `claude-sonnet-5`/`medium` · 6 pairs | Before fix `/tmp/jev-bench-dual-claude-reps6-b/` | After fix `/tmp/jev-bench-dual-claude-reps6-c/` |
|---|---:|---:|
| Median of baseline−intervention | −14,388 | −3,276 |
| Median reduction rate (interval) | −27.25% | −6.20% (−6.25% to −6.16%) |
| Jev calls/run | 3 | 2 |
| Median Jev input | 13,661.5 | 2,748.5 |
| Median Jev output | 618 | 408 |
| Quality/comparable | 3/3 · 6/6 | 3/3 · 6/6 |

Even after the fix, the judgment was still `Write`→`respond_to_user`, matching the actual next action. The verdict is still **increase**. Because advise does not reduce upstream requests, even reducing Jev's cost only gets us back, at best, to the same amount as the baseline. As the user pointed out, this is not a fundamental solution.

To achieve a net reduction, Jev's judgment must replace upstream inference. The existing `direct` cannot be used with Claude. Claude's requests return early in the advise branch ([rewrite.go](internal/proxy/rewrite.go#L457)), the applicability condition is limited to `chat` format ([rewrite.go](internal/proxy/rewrite.go#L498)), and the only response format that can be synthesized is OpenAI Chat format ([direct.go](internal/proxy/direct.go#L174)). Argument synthesis is also limited to cases where every schema field is `const` ([direct.go](internal/proxy/direct.go#L48)).

The three upstream requests in `dual-facts` are: (1) a parallel call of two MCP tools, (2) a `Write` with a value computed from the results, and (3) the final response. Only (1) can be replaced without arguments. Since Jev's Choice returns only one candidate, even if we replace only one tool in (1), one additional upstream inference is needed to call the remaining tool, so almost no net reduction occurs. A rough estimate for the case where the two calls are combined into one synthesized response is 17,000 − Jev's roughly 1,600 ≈ a **net reduction of about 15,000 (about 30%)** (not measured). However, this saving strongly depends on the property of the task that "the first move is decided without arguments." In general work where the main model decides the arguments, Jev would need to generate the arguments as well, which risks overriding the main model's judgment when misjudged. To avoid a bench-only optimization, the direction was left to the user's decision.

### Reproduction experiment of Read replacement in real work (2026-09-24)

Per the user's choice, we measured the room for replacement in real work. We randomly sampled 200 of the 1,622 Claude Code main sessions from the last 30 days (seed 20260924, 1,943 turns). Turns consisting only of tool_use with no text, where every argument's value already appears in the earlier conversation or tool results, were treated as replacement candidates. There were 250 candidates, 11.4% of total cost. 197 of them were `Read`. Subtracting the Jev cost (assuming 1,600 per call), the upper bound is 11.25% of total cost. Aggregation is in `/tmp/claude-turn-audit/`. This environment's records have no `isSidechain` subagents, so children could not be aggregated.

Next, for 100 candidate turns and 100 non-candidate turns, we sent the same judgment request as the proxy to real Jev to predict the next tool and the Read path. The next tool used the actual code such as `askNextTool`, called via `go test -overlay`. Since tool definitions were not in the records, we approximated them with the existing tool names and short descriptions. Since the repository has no implementation for path selection, we imitated it in Python using the same HTTP contract. We did not save the original conversation text or the sent request bodies. The record is in `/tmp/jev-replay/`. 21 cases failed due to exceeding the Jev input limit and were excluded as incomparable (179 measured).

We recalculated for the parent session, limited to `Read` replacement (applying the threshold to the smaller of the confidences of the two questions). The threshold table created by the subagent also included non-Read predictions in the replacement count, so we did not adopt it.

| Threshold | Replacements | Exact match | Wrong path | Mis-replacement to non-Read | Upstream saved | Loss from mis-replacement (assuming that turn's cost ×1) |
|---|---:|---:|---:|---:|---:|---:|
| None | 49 | 16 | 27 | 6 | 1,439,709 | 3,170,276 |
| 0.5 | 18 | 9 | 6 | 3 | 761,155 | 813,907 |
| 0.7 | 6 | 5 | 1 | 0 | 433,888 | 70,317 |
| 0.9 | 1 | 1 | 0 | 0 | 175,572 | 0 |

The measured Jev usage was 2,057,185, about 8% of the sample's total upstream cost of 25,400,945. In real work, the median input for a single judgment is 7,406 (max 33,234), far exceeding `dual-facts`'s roughly 1,370. Even at the best threshold of 0.7, the net profit/loss is about −1.69 million (−6.7%). Since the sample over-includes candidates, the actual composition is expected to be even worse.

**Conclusion**: Replacing `Read` does not pay off. More importantly, the current advise for Claude does not reduce upstream and adds a Jev cost of several thousand tokens per turn. In real work, this is estimated to be a net increase of about 8% (an estimate from a sample, not a repeated comparison of actual runs). Most of Claude Code's cost is dominated by re-reading context each turn (the sample average is about 130,000 per turn). Therefore, savings require reducing the number of turns or the size of the context, and choosing the next tool has little effect on either. The next direction awaits the user's judgment.

### Claude Code: analysis of structural reduction without Jev (2026-09-24)

Per the user's choice (A), we examined, using real-work records, the room for making the main model's turn unnecessary using only the proxy's deterministic processing, without Jev. The target was the last 30 days, 172 main sessions, 2,017 turns, with a total cost of 266,467,486. Aggregation is in `/tmp/claude-structural/structural_audit.py` (5 asserts).

| Turns that could potentially be avoided | Count | Share of total cost | Verdict |
|---|---:|---:|---|
| Waiting/polling (Bash including sleep, TaskOutput, etc.) | 41 | 3.0% | The only candidate. A deterministic rule where the proxy synthesizes the same call if the result of the same polling call is still running could be conceivable. An existing example of returning a response without calling upstream is [native_compact.go](internal/proxy/native_compact.go) |
| Retrying the same tool right after an error | 165 | 6.6% | Nearly impossible. Retries involve the main model fixing arguments, which cannot be synthesized without a model |
| Turns consisting only of ToolSearch | 64 | 2.4% | Not recommended. Passing the schema up front would bloat the context of all subsequent turns, and also runs counter to the selection feature's policy |
| Short intermediate text | 44 | 2.7% | Not possible. The proxy can only get involved after inference has finished |
| Re-calling a read with the same arguments | 5 | 0.2% | No effect |
| Only record-only tools | 0 | 0% | Not applicable |
| Total after deduplication | 318 | 14.8% | Given the judgments above, the realistic ceiling is about 3% |

On the context-size side, the results of `Read` and large responses from doctrine-family MCP tools (median 17,000 to 29,000 characters) are large. On the other hand, the injected text from hooks differs in content every time, so there was almost no exact-match duplication. Cutting these belongs to the domain of compaction and is treated separately from the selection feature.

**Conclusion**: For the proxy to omit upstream inference, it would need to synthesize the main model's next response without a model, and the only thing that can be done deterministically is something like a repeat of the same status check. As the next step, we will measure, from the records, the match rate of the polling synthesis rule (whether the main model made the same call again when the previous polling call's result was still running) and judge the safety of synthesis.

### Verification of the polling synthesis rule (2026-09-24)

We verified the sole candidate from the previous section using only conversation records (no external services used). The target was all 1,617 main sessions from the last 30 days, 15,541 turns, with a total cost of 2,286,146,622. Aggregation is in `/tmp/claude-polling/analyze.py` (4 asserts). Polling types were defined as TaskOutput/Monitor/BashOutput, status/wait-family MCP tools, Bash including sleep, and Bash that repeats the same command within the same session.

| Metric | R1: synthesize the same call if the previous result is incomplete | R2: synthesize if the previous result is identical to the one before it |
|---|---:|---:|
| Triggered | 106 | 32 |
| Exact match | 19 (17.9%) | 3 (9.4%) |
| Argument changed | 24 | 8 |
| Different tool | 47 | 15 |
| Text only (completion report/response to user) | 14 | 5 |
| Ceiling of savings (share of total cost) | 0.28% | 0.05% |

About 80% resulted in mis-synthesis, and 13-16% would have hijacked the response to the user. For calls that include sleep, synthesizing does not change the wait time. **Not implemented.** For Claude Code, choosing to use Jev goes into the red due to real-work judgment cost, and deterministic synthesis without Jev has almost no room either. Most of the cost is re-reading context each turn, and the room for reduction lies on the context-shrinking (compaction) side.

### Analysis of stopping Claude's advice and compaction (2026-09-24)

**Advice disabled by default**: Per the user's decision, Claude's normal requests now, by default, call neither Jev nor local selection, forwarding the body unchanged (reason `claude_advise_disabled`, [implementation](internal/proxy/rewrite.go#L197)). The previous advice is enabled with `JEV_CLAUDE_ADVISE=on`. We enable this only for the bench's Claude `on` condition, to keep the comparison intact. Skill delivery, compression requests, and other hosts are unchanged. `go test ./...` passed with 559 tests. Not committed. The combination of shadow observation and Claude has not been verified.

**Reference: jev-gateway's benchmark**: The increase/decrease in Claude Code in [jev-gateway](https://github.com/vinilana/jev-gateway#benchmark) arises from changes in the work path via the same `hint` (advice) method. The author himself writes, regarding Claude Code, "not lower cost or latency." The LLM request count is −26% to +47%, linked to the increase/decrease in tokens. The tokens in the table do not include Jev's usage (only the cost is noted separately), and each is the unpaired median of 5 runs. We position our `dual-facts`, a task with a fixed path, as showing the structural profit/loss of advice (a net increase) without variance.

**Compaction analysis**: We extracted and analyzed 21 sessions/269 turns from the last 30 days (`/tmp/claude-compaction/analyze.py`; the sample is small, so this is a rough guide). Contributions to context growth were, in order, MCP results 22.4%, assistant output 19.8%, Read 16.3%, Bash 14.2%. Net amounts by policy (share of total cost) are as follows. S1 (truncate a new large tool result to its head and tail on first send; changes only unc­ached content, so it does not break the prefix) is +4.2% above 2,000 tokens and +2.2% above 5,000 tokens. S2 (independently erase results from K turns ago) is −18% to −46% (an approximation of the worst case) due to cache rewriting, and is therefore not viable. S3 (deduplication) is +0.02%.

**Confirmation of native context editing**: We observed real Claude Code with subscription authentication (`authMethod=claude.ai`) through a minimal relay proxy (`/tmp/claude-ctxedit/`; credentials and request bodies not saved). Claude Code already sends `anthropic-beta: context-management-2025-06-27` and `context_management.edits=[clear_thinking_20251015]` on every request, and the response also returns `applied_edits`. When we appended `clear_tool_uses_20250919` (minimal trigger, keep, and clear_at_least) while keeping the existing edit, all requests were accepted with 200, and `applied_edits` returned `cleared_tool_uses: 2, cleared_input_tokens: 58`. The answer was also correct. If erasure is batched using `clear_at_least` from the [official documentation](https://platform.claude.com/docs/en/build-with-claude/context-editing), it may be possible to suppress the frequency of cache rebuilding, which is S2's weakness. The effect on total tokens is not yet measured (only one run each).

### Reasons for abandoning tool-call replacement in Claude Code (for transcription into README, 2026-09-25)

**Conclusion**: For Claude Code, neither replacing/guiding tool selection with Jev (`hint`/`advise`) nor deterministic tool-call synthesis without Jev could reduce total tokens while preserving quality. Therefore, Jev advice for Claude Code is now disabled by default (enabled with `JEV_CLAUDE_ADVISE=on`), and we switched the strategy for token reduction to Anthropic's native context editing (context shrinking).

**Why it doesn't structurally reduce**
- Claude Code runs with extended thinking enabled, and the Anthropic API rejects tool forcing (`tool_choice`) during thinking. Furthermore, changing the tool list or `tool_choice` invalidates the prompt cache. This means all the proxy can do is advise the next move without removing tools. (Corrected 2026-09-26: per Anthropic's docs this applies to manual extended thinking and, in every mode, to Opus 5.5; adaptive thinking on other models allows forcing. Changing `tool_choice` invalidates the cached message blocks; changing tool definitions invalidates the whole cache.)
- Advice changes neither the number of upstream requests nor the size of the context. Meanwhile, Jev's judgment cost is added every turn. In tasks where the main model already solves it via the shortest path, Jev's cost becomes a pure net increase.

**What was investigated and the results**

| Investigation | Method | Result |
|---|---|---|
| Structural profit/loss of advice | `dual-facts` (a task with a fixed path), `claude-sonnet-5`/`medium`, 6 pairs | Median total tokens −27.25% (increase). 99% of the increase is the cost of Jev's judgment; upstream requests and usage were nearly identical to baseline |
| Reducing Jev cost | Skip judgment for requests where advice cannot be attached, and shorten candidate description text | Improved to −6.20%, but still an increase. At best, this only gets back to the same amount as baseline |
| Room for replacement in real work | Aggregated 200 real sessions from the last 30 days (1,943 turns) | "Tool-only turns" whose arguments consist only of already-seen values were 11.4% of cost (mostly `Read`). The upper bound after subtracting Jev cost is 11.25% |
| Accuracy of `Read` replacement by Jev | Reproduction experiment sending the same judgment request as the proxy to real Jev, for 200 real turns | In real work, the median input for one Jev judgment is 7,406 tokens. Even at the best threshold, net profit/loss is about −6.7%, in the red at every threshold. The current advice is estimated to be a net increase of about 8% in real work |
| Deterministic synthesis without Jev | Verified a rule that synthesizes polling with the same call, over 1,617 real sessions/15,541 turns | Accuracy 17.9%, savings ceiling 0.28%. 13-16% would hijack the response to the user, so not implemented |
| Comparison with other products | [jev-gateway's benchmark](https://github.com/vinilana/jev-gateway#benchmark) | Same `hint` method. The author himself states, regarding Claude Code, "not lower cost or latency." The increase/decrease is linked to changes in request count (−26% to +47%), i.e., variance in the path, and the table's tokens do not include the Jev portion |

**Action taken**: For Claude's normal requests, we now, by default, forward them unchanged without calling Jev. Since most of Claude Code's cost is re-reading context each turn, reduction is done on the context-shrinking side. A custom history rewrite breaks the cache and goes into the red (estimate −18% to −46%). Therefore, we adopted the approach of having the proxy add Anthropic's native feature `clear_tool_uses_20250919`, which erases old tool results server-side (`JEV_CLAUDE_CLEAR_TOOL_USES=on`). We confirmed this is accepted even by Claude Code with subscription authentication.

### Claude Code: measurement of native context editing (2026-09-25)

We measured with `jev-routing bench --agent claude --tasks chess-bugfix --modes off,on --claude-clear` (trigger 30000, clear_at_least 10000, keep 3, `claude-sonnet-5`/`medium`). We made three fixes to the bench to complete the measurement. (1) Run each agent in bwrap and give each run a dedicated tmpfs `/tmp` (so remnants of other runs aren't visible, and it is discarded on exit). (2) Do the audit's "created" judgment from a snapshot taken at start (writes via `cp`/`node` and creation of relative paths were being mistakenly judged as contamination). (3) Fixed a bug where stale sandboxes remained due to pid reuse, by unconditionally cleaning up orphans after acquiring the lock. Runs with no erasure are also not excluded (excluding them would create a selection bias where only long runs remain). `go test ./...` passed with 583 tests.

| rep | baseline → intervention | baseline−intervention |
|---:|---|---:|
| 1 | 1,517,732 → 1,064,659 | +453,073 |
| 2 | 2,014,989 → 804,403 | +1,210,586 |
| 3 | 1,473,057 → 1,746,272 | −273,215 |
| 4 | 1,091,615 → 450,045 (no erasure) | +641,570 |
| 5 | 1,328,669 → 1,516,428 | −187,759 |
| 6 | 1,645,054 → 906,904 | +738,150 |

The record is in `/tmp/jev-bench-claude-clear-reps6-c/`. All 6 pairs were comparable, contamination was 0, and quality was 36/36 on all runs. The median reduction rate was **+37.4%**, but since the interval −18.5% to +60.1% straddles zero, the verdict is **inconclusive**. In the 5 runs where erasure occurred, the tokens removed from the input (summed per request) ranged from 144,840 to 293,748, exceeding the increment in cache-write (the difference from the baseline median, roughly 20,000 to 110,000). In other words, assuming the path is unchanged, each run is estimated to be net positive. On the other hand, variance in the main model's path (about 4x under the same condition) masks the difference.

**Pre-registration (recorded before seeing the results)**: We will add 6 more pairs without changing the settings (`/tmp/jev-bench-claude-clear-reps6-c2/`) and, combined with the above, **judge once with 12 pairs**. If still inconclusive with 12 pairs, we will change to a setting that reduces the number of erasures (raise clear_at_least), pre-register 12 pairs again, and measure. We will not stop early or extend based on interim results.


### Claude Code: verdict on 12 pairs of context editing, and pre-registration of v2 (2026-09-25)

As pre-registered, we added 6 more pairs with the same settings (`/tmp/jev-bench-claude-clear-reps6-c2/`). Combined with the earlier 6 pairs, renumbering reps to 7-12, we recomputed with `bench report` (`/tmp/jev-bench-claude-clear-12/`). All 12 pairs were comparable, quality was 36/36 on all runs, contamination was 0, and erasure occurred in 11/12 pairs. The median reduction rate was **+10.3%**, with an interval of −18.5% to +44.9%, and the verdict is **inconclusive**. Of the 6 added pairs, 4 of 6 showed an increase (differences −106,337/−775,617/+641,683/−353,508/+421,968/−101,473).

By request, the erasure mechanism itself is estimated to be net positive. In runs where erasure occurred, the tokens removed from the input (summed per request) ranged from 145,000 to 440,000 per run. Meanwhile, cache-write was about 45,000-64,000 for off versus 75,000-165,000 for on, with 3-6 write spikes per run. However, the median number of upstream requests increased, from 32.5 for off to 36 for on. In the earlier analysis too, most of the rework was re-reading of `Read` files. We estimate that erasure causes file content to be lost, triggering re-reads that add turns and offsetting the mechanism's savings (causality not verified).

**Pre-registration of v2 (recorded before seeing the results)**: We will change to `exclude_tools: ["Read"]` (leaving file content intact, and erasing only things like old test output) and `clear_at_least` 20000 (to reduce the number of erasures and cache rebuilds). trigger 30000 and keep 3 remain unchanged. We will measure 12 pairs with the same task and model, and judge once with 12 pairs. If still inconclusive with v2, we will pre-register the next setting again based on the measured values (rework, write, erasure amount).

### Claude Code: v2 results, the same-path net saving metric, and pre-registration of a confirmation series (2026-09-25)

**v2 verification failed to establish**: In the 12-pair v2 run (`exclude_tools: ["Read"]`, clear_at_least 20000, `/tmp/jev-bench-claude-clear-v2-12/`), all 24 runs had quality 36/36, but erasure never occurred. With Read excluded, the amount erasable that is older than keep 3 never reached 20,000 (context grew to at most 91,130). For the pre-registered verdict, rep10's baseline became incomparable due to a mismatch between host usage and the proxy, making the verdict indeterminate (11/12). This series effectively became an A/A test between identical conditions, and the per-pair "reduction rate" was scattered from −271% to +90% (median +20.9%). Even comparing total tokens across runs of `chess-bugfix`, judging an effect of the 10-30% scale with 12 pairs is fundamentally difficult.

**Same-path net saving metric**: Separately from the between-run comparison, we implemented in the bench a metric that computes, for on runs where erasure occurred, the counterfactual on the same path from the API's `applied_edits` ([clearnet.go](internal/bench/clearnet.go)). From the sum of amounts removed from the input per request, we subtract the cache-write increase caused by erasure (cache_creation exceeding the counterfactual context increment) and rework (the cost of turns that, after the first erasure, re-executed the same tool and input as a call older than keep). Runs where turns and requests cannot be matched are treated as missing, not filled with zero. Recomputing the between-run comparison (inconclusive verdict) is kept alongside, not replaced. Recomputing v1's 12 pairs (`/tmp/jev-bench-claude-clear-12/`), the net saving was positive in all 11 runs where erasure occurred, with a median of **12.0%** (2.8% to 17.7%). Without subtracting rework, the median estimate was 14.5%. Limitation: rework only counts exact-match re-executions; the effect of erasure indirectly changing the main model's judgment can only be captured by the between-run comparison.

**Pre-registration of a confirmation series (recorded before seeing the results)**: Since this metric was defined while looking at the same 12 runs, we will confirm reproducibility with new data. We will measure 6 new pairs with v1 settings (trigger 30000, clear_at_least 10000, keep 3, no Read exclusion) (`/tmp/jev-bench-claude-clear-confirm/`). Pass conditions: quality passes on all runs, the same-path net saving is positive for all on runs where erasure occurred, and the median is at least 5%. The between-run comparison verdict will also be reported alongside.

### Claude Code: results of the confirmation series (2026-09-25)

The pre-registered confirmation series (`/tmp/jev-bench-claude-clear-confirm/`, v1 settings, `claude-sonnet-5`/`medium`, bwrap isolation) satisfied all pass conditions.

| Pass condition | Result |
|---|---|
| Quality passes on all runs | All 12 runs 36/36, 0 contamination |
| Same-path net saving is positive for all on runs where erasure occurred | Erasure occurred in 6/6 runs, all 6 positive (4.8% to 14.8%) |
| Median same-path net saving ≥5% | **8.5%** |

By on run (savings/added write/rework/net saving/rate): 92,560/10,061/44,291/38,208/4.8%, 96,793/7,096/27,158/62,539/9.8%, 50,241/13,487/0/36,754/5.2%, 117,320/6,833/26,123/84,364/11.6%, 91,812/22,908/0/68,904/7.2%, 158,705/9,547/0/149,158/14.8%.

The between-run comparison was also judged as a **decrease** this time (median 44.7%, interval 19.4% to 75.6%, on was smaller in 6/6 pairs). However, given that in the A/A-equivalent v2 series the effect size for identical conditions was scattered from −271% to +90%, we consider a large share of this effect size to be due to chance, and we do not use it as evidence.

**Conclusion**: For Claude Code, when the proxy adds Anthropic's native `clear_tool_uses_20250919` (trigger 30000, clear_at_least 10000, keep 3), total tokens on the same path decrease while preserving quality. In two independent series (v1's 12 pairs: median 12.0%; confirmation series: median 8.5%), all 17 runs where erasure occurred were positive.

**Limitations**: The target was one task, `chess-bugfix`, and one model. The thresholds were chosen to trigger erasure within the bench's context (max 40,000-90,000 tokens). Default values suited to real work's context (median about 130,000/turn) (such as the API default trigger of 100000) have not been verified. Rework only counts exact-match re-execution. The feature is disabled by default (enabled with `JEV_CLAUDE_CLEAR_TOOL_USES=on`). All changes are uncommitted and not published to GitHub.

(Addendum 2026-09-25: The above changes were subsequently committed as `d1dcfe9`/`786840c`/`d31a508` and pushed to `origin/main`.)

### Claude Code: investigation of settings for real work and variance (2026-09-25)

User's policy: since a reduction was confirmed but variance is large, we will look for a practically usable setting. We used external LLMs only for one calibration bench run, and investigated the rest through offline reproduction of conversation records. Scripts are in the session scratchpad `ctxedit/` (`extract.py`, `sim.py` [with a selftest], `sweep.py`, `calib.py`).

**Determining the server-side erasure rule empirically**: For one `chess-bugfix` on run (trigger 30000, clear_at_least 10000, keep 3, quality 36/36, same-path net saving 4.9%), we examined the per-request `applied_edits`. It first triggered at req15, erasing 13 items totaling 10,329 tokens. Thereafter, up to req35, the context grew to 61,071 and the amount of erasable old results also increased, but the erasure amount stayed around 10,100-10,600, not increasing, and the count decreased from 13 to 12. The only rule consistent with this behavior is: "every request, recompute from the whole history, and erase results older than keep, oldest first, until reaching clear_at_least (do not erase if it can't be reached)." A rule of "erase everything older than keep" or a rule where erasure state accumulates cannot explain the decrease in count. **Therefore, the savings per request are essentially capped at clear_at_least.** Also, when the boundary item swaps, the cache is rewritten from that point (a write of about 19,600 at req20). Based on a single run.

**Ceiling for real work (fixed path, no rework)**: We reproduced the above rule against 569 main sessions from the last 30 days (9,748 requests). Tool result size was calibrated at a median of 1.9 characters/token, based on the context increment right after a single large result.

| Fact | Value |
|---|---|
| Session's initial request context (fixed prefix: system, tool definitions, instructions, skill list, etc.) | Median 81,574 |
| Growth in context within a session | Median 19,109 (p90 75,568) |
| Share of final context that is tool results | Median 3.8% (p90 15.3%) |

| trigger·clear_at_least (keep 3) | Total token reduction across all usage | Sessions triggered | Median reduction rate of triggered sessions (p10 to p90) | Sessions that become a loss with price weighting |
|---|---:|---:|---|---:|
| 100000·10000 | 3.0% | 28% | 4.6% (1.5 to 7.6%) | 73% |
| 100000·20000 | 3.2% | 16% | 6.7% (2.0 to 11.4%) | 64% |
| 100000·40000 | 2.8% | 6% | 10.6% (4.1 to 16.4%) | 42% |
| 60000·40000 | 2.8% | 6% | 10.6% (4.1 to 16.4%) | 42% |

Price weighting was computed with cache-read 0.1, 1-hour cache-write 2.0, output 5.0 (ratios relative to input 1.0). Claude Code's records are `ephemeral_1h` writes. Even in sessions that show a reduction under the main KPI (total tokens including cache), under pricing the cost of cache rewriting often exceeds the savings from reads. It is unconfirmed which weighting the subscription usage quota counts closer to.

**Conclusion (tentative)**:
1. Most of real work's context is not tool results but a fixed prefix of about 80,000. The 8-12% seen in the bench (whose prefix is about 17,000, with tool results dominating the context) does not carry over to real work. The ceiling for real work is about 3% of the total (before subtracting rework).
2. There are two main causes of variance. (a) The difference in erasable amount per session. This cannot be controlled by settings. (b) Rework. In the calibration run, against a saving of 216,044, 123,476 was counted as rework. However, this includes `npm test` re-runs unrelated to erasure, so it is an overestimate.
3. clear_at_least is what can be controlled by setting. The larger it is, the rarer triggering becomes, and the larger and more stable the effect is when it does trigger (p10 4.1% at C=40000). trigger has almost no effect between 60000 and 100000.
4. This offline reproduction does not include rework or the actual impact on the path. Confirming this closer to real work requires live measurement with `--user-tools` (not yet done).

### Fix to the rework metric, and a pilot under conditions closer to real work (2026-09-25)

**Fixed the definition of rework** ([clearnet.go](internal/bench/clearnet.go)): The old definition treated "a re-execution of the same tool and same input as a call older than keep, after the first erasure" as rework. This ends up counting calls that occur regardless of erasure, such as an `npm test` re-run after an edit. Under the new definition, something counts as rework only if it satisfies all three of the following: (1) it is the same tool and same input; (2) the original call was, in that request, **actually erased** (determined from the per-request `clearedToolUses` and the oldest-first erasure rule; `exclude_tools` are excluded from numbering); (3) the result body **exactly matches** the original. When the result is missing and cannot be determined, it is treated as missing (nil), not filled with 0. Recomputing the calibration run (`chess-bugfix`, trigger 30000, clear_at_least 10000), rework went from 123,476 to 0, and the same-path net saving went from 67,983 (4.93%) to 191,459 (12.75%). The only candidate was a re-read of `Read src/chess.js`, but it was excluded because the file had been edited in between and the result did not match. **Limitation**: if a file was re-read because of erasure but had already been edited, it is not counted. Thus, the new definition is a lower bound on rework.

**Added `--no-hooks` to the bench**: When used together with `--user-tools`, the user's MCP, plugin, and skill definitions remain the same as the real environment, while only hooks are stopped via `--settings {"disableAllHooks":true}`. This was also included in the comparison key. `go test ./...` passed for all packages, not committed.

**Pilot** (`chess-engine`, `--user-tools --no-hooks`, trigger 100000, clear_at_least 20000, keep 3, `claude-sonnet-5`/`medium`, 1 on run): the prefix was 64,603 (smaller than the real-work median of 81,574, since there is no hook portion). It finished in 17 requests, and the context reached a maximum of 107,047, but **erasure never occurred**. The total of tool results was only about 6,000 tokens, and the context growth was mostly the prefix and the model's output (the input to `Write`; about 23,000 at req3). External quality was 21/36 (58%), and the agent's own 27 tests passed. Since no erasure occurred, this quality is unrelated to erasure.

**Estimate for `clear_tool_inputs`** (569 real sessions, same rule): even if tool inputs are also made erasable, the overall reduction is only +0.5 to 0.9 points (2.8%→3.7% at C=40000).

**Judgment**: The offline reproduction and the pilot reached the same conclusion. Under conditions closer to real work, there are almost no erasable tool results. Measuring 6 more pairs under this setup would just produce an A/A comparison with no erasure, so pre-registration and the main measurement have **not been carried out**. The next direction awaits the user's judgment.

### Claude Code: shrinking the fixed prefix (2026-09-25)

Per the user's choice (2), we broke down the fixed prefix that dominates real work's context. Using a local relay (`scratchpad/prefix/relay.py`; only the body is saved, auth headers are not saved), we captured one `claude -p` request (hooks disabled, `claude-sonnet-5`). The body was 149,319 characters. The breakdown: system-role blocks within `messages` were 71,799 characters, `system` was 28,162 characters, and the 12 `tools` were 42,902 characters (MCP tools are lazily loaded via ToolSearch). Among the system-role blocks, the **agent list (80 types) accounted for about 33,600 characters, and the skill list (118 entries) for 28,702 characters**.

In real use over the last 30 days, only 10 agent types were actually called (general-purpose 625, Explore 53, fork 36, file-ops-delegate 31, doctrine-executor-light 16, etc.). The `doctrine-module-*` (29 types) and `doctrine-spec-*` (4 types) were never used. These are documents whose descriptions themselves say "module" or "spec document," and remain in the list even after `doctrine-orchestrator` was retired. As for plugins, figma, claude-security, and diagram-design were called 0 times, and slack's MCP was called 7 times.

We measured whether these could be trimmed using official settings alone, using the input + cache read + write for the same `claude -p "Reply with OK only."` (no relay, each measured twice with matching values).

| Setting (`--settings`) | Fixed prefix | Difference |
|---|---:|---:|
| Hooks disabled only (baseline) | 67,819 | — |
| + `permissions.deny` with 33 `Agent(doctrine-module-*/spec-*)` entries | 59,107 | −8,712 (−12.8%) |
| + disable slack/figma/claude-security/diagram-design in `enabledPlugins` | 62,130 | −5,689 |
| Both | 53,418 | **−14,401 (−21.2%)** |

Agents denied via `permissions.deny` actually disappeared from the request body's list (confirmed via the relay). Denying all 55 unused doctrine types gave −16,465 (from the relay-based baseline of 62,749). However, since `workflows/template-doctrine-{orders,post}.js` references `doctrine-nco-collector`, `doctrine-aggregator`, `doctrine-reflector`, and `doctrine-staff-*`, this broader scope is not recommended without verifying Workflow behavior. Note that going through the relay (a non-first-party `ANTHROPIC_BASE_URL`) makes the baseline about 5,000 smaller. This is consistent with the known difference from the ToolSearch default.

**Estimated impact**: Over the last 30 days, main-session requests numbered 9,748, with total tokens of 1,177M. Cutting 14,401 per request with both settings gives a reduction of about 140M (about 12%). This is about 4x the ceiling for context editing (about 3%), and involves neither rework nor cache breakage. However, this is an estimate assuming the prefix stays the same size throughout a session. Since what is cut is mainly cache reads, the reduction rate in monetary terms would be smaller. We expect the quality impact to be small, since only unused items are cut, but this has not been measured.

**Where to implement**: We will not implement this in the jev-routing proxy. Rewriting the list depends on Claude Code's internal format and is fragile, and the same effect can be achieved with official settings. The place to change is the user's own Claude Code settings (source is `private_dotfiles`), and applying it awaits the user's judgment. Shrinking the skill list (setting `disable-model-invocation: true` on unused user skills) requires editing SKILL.md and has not been measured.

**User's judgment (2026-09-25)**: Disabling skills/agents that will be used, just to reduce the count, would be putting the cart before the horse; the goal is a reduction achieved via jev-routing. The above setting-change proposal is not adopted.

### Subagent consumption and re-estimation of context editing (2026-09-25)

In the records over the last 30 days (deduplicated by request message.id), **the main session totaled 1,223M (10,225 requests) and subagents totaled 1,173M (12,164 requests), nearly the same amount**. For both, 94% is cache read, 6% is cache write, and output is 0.1-0.4%. Therefore, total tokens are determined almost entirely by "number of requests × context size," and interventions that adjust output or reasoning amount have no effect. 87% of subagent consumption is by general-purpose.

Subagents have a small median initial-request context of 35,197, while the per-request context grows to a median of 79,944. That is, unlike main, most of the context is history such as tool results. We reproduced this with the same erasure rule (oldest first, up to clear_at_least) for 583 subagent sessions (11,596 requests, 1,147M) (fixed path, no rework, 1.9 characters/token).

| trigger·clear_at_least (keep 3) | Subagent total token reduction | Triggered | Median reduction rate when triggered (p10 to p90) | Price-weighted overall | Share that becomes a loss with pricing |
|---|---:|---:|---|---:|---:|
| 100000·20000 | 8.0% | 35% | 10.6% (3.8 to 16.5%) | −2.5% | 79% |
| 60000·40000 | 10.1% | 22% | 15.8% (6.8 to 22.9%) | +0.6% | 62% |
| 60000·60000 | 10.0% | 15% | 18.5% (8.5 to 29.6%) | +1.8% | 56% |

Combined with main (ceiling about 3%), the setting 60000·40000 is estimated at about 6.4% overall ((2.8%×1,223M + 10.1%×1,147M)/total). For subagents, the effect when triggered is large, and even the lower bound of variance (p10) is high. However, under price weighting, it remains true that many sessions come out at a loss. Rework has not yet been measured. This needs to be confirmed with live measurement that actually uses subagents (a `child-facts`-type task).

### The subagent-read task `child-survey` and a pilot (2026-09-25)

We added `child-survey`. The parent delegates once to a subagent; the child reads eight Go source files (about 250KB) in this repository in full via Read, and returns, for each file, the number of top-level funcs and the name of the last func. The parent writes that to answer.json. External scoring computes 16 items using go/parser. As evidence, the child is required to have Read all eight files.

We fixed three measurement flaws. (1) Because a Claude parent and child share the same session key, the existing matching would succeed as "success" based on the parent alone and never proceed to child attribution. Now, when unconfirmed children remain, it proceeds to `claudeChildAttribution` (matching each stream-json message's usage against the proxy's requests uniquely). (2) The same-path net saving is now computed per request sequence for parent and child separately. (3) The bench's child process was leaking the parent Claude Code session's environment variables (`CLAUDE_CODE_SESSION_ID`, the messaging socket, etc.), which we removed. `CLAUDE_CODE_SUBAGENT_MODEL` is kept, since it's the user's real setting (`opus`), and was recorded and included in the comparison key.

In a pilot 1 pair (parent `claude-sonnet-5`/`medium`, child `claude-opus-5-5`, trigger 60000, clear_at_least 40000, keep 3), both conditions had quality 16/16. In on, erasure occurred in the child (21 items, per-request sum 381,387), total tokens went from 1,102,603 to 693,549 (baseline−intervention 409,054), the same-path net saving was computed as +317,796 (31.4%), and rework was 0. Attribution matched: all of the child's requests were opus, and all of the parent's requests were sonnet. With only 1 pair, this is not used for judging effect.

**Pre-registration (recorded before seeing the results)**: With the same settings (child is the user's real setting of opus, isolated settings with no hooks/no user tools), we will measure `child-survey` for 6 off/on pairs. The verdict will be made once, via the default `--min-pairs 6`/`--min-savings-pct 0`, with no early stopping or extension. We will also check whether the same-path net saving for on runs where erasure occurred is all positive, with a median of at least 5%.

**Correction to the pre-registration (before measurement started, 2026-09-25)**: Per the user's instruction, we will fix even the child to the bench-standard `claude-sonnet-5`/`medium` (`CLAUDE_CODE_SUBAGENT_MODEL=claude-sonnet-5`). The above pilot, where the child was opus, and the opus series stopped right after starting run 1 (`survey-reps6/`), will not be used for judging effect. The new series is saved in `survey-reps6-sonnet/`. The pass conditions are the same as above.

### Results of the `child-survey` 6 pairs (2026-09-25)

The record is in `scratchpad/ctxedit/survey-reps6-sonnet/`. Both parent and child are `claude-sonnet-5`/`medium`, trigger 60000, clear_at_least 40000, keep 3. Parent-child attribution was confirmed (`parentChildVerified`) in all 12 runs, with 0 measurement errors.

| rep | Baseline (quality/total tokens/requests) | Intervention (quality/total tokens/requests) | Baseline−intervention | Intervention's erasure (count/per-request sum) | Same-path net saving | Rework |
|---:|---|---|---:|---|---:|---:|
| 1 | 15/16·794,820·11 | 16/16·1,184,571·18 | −389,751 | 179·2,641,056 | 59.6% | 699,727 |
| 2 | 16/16·516,915·7 | 16/16·601,452·12 | −84,537 | 25·464,292 | 30.0% | 136,846 |
| 3 | 15/16·1,075,455·12 | 16/16·506,161·11 | +569,294 | 14·280,336 | 32.3% | 0 |
| 4 | 16/16·497,731·6 | 15/16·455,459·9 | +42,272 | 14·280,984 | 34.8% | 0 |
| 5 | 15/16·684,728·9 | 15/16·870,302·15 | −185,574 | 33·579,046 | 37.1% | 0 |
| 6 | 15/16·795,569·11 | 15/16·479,513·10 | +316,056 | 14·280,687 | 33.5% | 0 |

**Pre-registered verdict**: `quality_worse` (at rep4, only the intervention side scored 15/16). Comparable pairs are 1/6 (since only pairs where quality is a perfect score on both sides count as comparable). **No effect is established.**

Observations:
- The task's own quality was unstable. Even at baseline, 4 of 6 runs scored 15/16, with the child miscounting the func count by one. The total scoring items were 92/96 for baseline and 93/96 for intervention, so it cannot be said intervention made things worse. However, under the pass rule, almost no comparable pairs remain.
- The between-run total token difference was widely scattered, from −389,751 to +569,294, with a median of about −4%. Intervention tends to increase request count (median 11.5 vs. 10).
- The same-path net saving was positive in all 6 runs (30-60%), but this is a value under the assumption that the path is unchanged. In rep1/rep2, rework — re-fetching the same content that had been erased — was 699,727 and 136,846 respectively. In rep1, erasure reached 179 items, and the child repeatedly re-read the erased files. **This task, which reports a summary of what was read at the end, makes erasure prone to triggering re-reads.** Real-work subagents also often summarize their findings at the end, so they carry the same risk.
- Therefore, the offline estimate (about 10% with no rework) was not reproduced in a real run that includes rework.

### Implementation and pre-registration of Jev-based erasure gating (2026-09-25)

We implemented `JEV_CLAUDE_CLEAR_GATE=jev` (`--claude-clear-gate jev` in the bench) ([clear_gate.go](internal/proxy/clear_gate.go)). Conversation identity is built from the model and the first user message body, so parent and subagent become separate conversations. Once the previous response's context reaches trigger − clear_at_least/2, we ask Jev a single Choice question. The options are `clear_old_results` (old outputs are done being used, sequential work) or `keep_all_results` (needed because everything is reported together at the end). The input is the task text (up to 1,500 characters) and the tool-call history (names and short arguments); result bodies are not included. Until the decision, and under `keep`, no edit is attached; from `clear` onward, the edit is kept attached continuously (reverting midway would break the cache). If Jev fails, no erasure occurs. The judgment's Jev usage is tallied into events and total tokens. `go test ./...` passed.

Pilot (1 on run each, trigger 60000, clear_at_least 40000): `child-survey`'s child was judged `keep` (Jev input 1,049/output 38), and `chess-bugfix` was judged `clear` (1,705/38). The latter did not reach the threshold, so actual erasure did not occur. `child-survey`'s definition of `lines` was ambiguous (Read displays an extra blank line number at the end), causing an off-by-one across all files, resulting in 8/16. We therefore changed to `last_line` (the line number of the last top-level func's `func` keyword), and confirmed a 16/16 selftest.

**Pre-registration (recorded before seeing the results)**: Both parent and child at `claude-sonnet-5`/`medium`, trigger 30000, clear_at_least 10000, keep 3, `--claude-clear-gate jev`. We will measure `child-survey` and `chess-bugfix`, 6 off/on pairs each, saved in `survey-gate6/`/`bugfix-gate6/`. No early stopping or extension. Pass conditions:
1. `child-survey`: the effect verdict is not `quality_worse`. The between-run total-token verdict is not "increase" (i.e., the gate chooses `keep` harmlessly).
2. `chess-bugfix`: the effect verdict is not `quality_worse`. In on runs, the gate chooses `clear`, and for runs where erasure occurred, the same-path net saving is all positive, with a median of at least 5%.
3. For both series, report the gate's decision (clear/keep/error) and Jev usage alongside.

### Jev erasure gate: results of the two pre-registered series (2026-09-25)

The record is in `scratchpad/ctxedit/survey-gate6/`/`bugfix-gate6/`. All 24 runs had perfect quality (`child-survey` 16/16, `chess-bugfix` 36/36), all pairs were comparable, and there were 0 measurement errors.

| Series | Jev's decision | Jev usage/run | On runs where erasure occurred | Same-path net saving (runs with erasure) | Rework | Between-run effect verdict |
|---|---|---:|---:|---|---|---|
| `child-survey` | 6/6 `keep` | Input 974-1,429/output 38 | 0/6 | — (0 due to no erasure) | 0 | Inconclusive (median +30.6%, interval −112.2% to +37.2%) |
| `chess-bugfix` | 6/6 `clear` | Input 823-940/output 38 | 5/6 | 2.9-14.2%, median **11.1%**, all positive | 34,344 in rep5 only | Inconclusive (median +32.3%, interval −63.1% to +76.7%) |

**Pre-registered verdict: both series pass.**
1. `child-survey`: not `quality_worse`, and the between-run verdict is not "increase." The gate chose `keep` in all runs, and the erasure/re-read (rework up to 699,727) previously seen without the gate did not occur. Since on does not erase, the between-run difference (−601,026 to +399,793) is essentially variance between effectively identical conditions (A/A).
2. `chess-bugfix`: not `quality_worse`, and the gate chose `clear` in all runs. The same-path net saving in the 5 runs where erasure occurred was all positive, with a median of 11.1% (≥5%). rep2 had a short path that did not reach the threshold, so no erasure occurred.

Observation: Jev's judgment cost is one call per conversation, about 1,000 tokens, staying at 0.1-0.2% of total tokens. The gate stopped erasure in the "report everything together at the end" type where erasure is harmful, and allowed erasure through for sequential work. The between-run comparison was scattered on the level of an A/A for both series, and is not grounds for effect. The grounds are the same-path metric and the avoidance of harm (quality, rework).

**Limitations**: The target is 2 tasks and 1 model, and generalization of Jev's judgment (accuracy across the diverse tasks of real work) is unverified. The thresholds (trigger 30000, clear_at_least 10000) are values suited to the bench, and default values suited to real work (main's prefix of about 80,000) need to be decided separately. The same-path metric assumes the path is unchanged, and rework only counts exact-match re-fetches (a lower bound).

### Estimate of thresholds for real work (2026-09-25)

We combined the last 30 days' 569 main sessions and 583 subagent sessions and estimated with the same erasure rule (fixed path, no rework, gate assumed to always be `clear` as an upper bound).

| trigger·clear_at_least (keep 3) | Overall total token reduction | Main | Sub | Price-weighted overall | Triggered (main/sub) | Triggered session reduction p10·median | Share that becomes a loss with pricing |
|---|---:|---:|---:|---:|---|---|---:|
| 100000·10000 | 4.0% | 3.0% | 5.0% | −3.1% | 28%/39% | 1.9%·5.3% | 83% |
| 100000·20000 (current default) | 5.6% | 3.2% | 8.0% | −1.3% | 15%/35% | 3.0%·9.2% | 74% |
| 100000·40000 | 6.3% | 2.8% | 9.9% | +0.5% | 6%/22% | 6.1%·14.7% | 59% |
| 100000·60000 | 5.9% | 1.8% | 10.0% | +1.1% | 2%/15% | 7.5%·17.6% | 52% |
| 60000·40000 | 6.4% | 2.8% | 10.1% | +0.5% | 6%/22% | 6.2%·15.0% | 57% |

Observations:
- trigger has almost no effect between 40000 and 100000. What determines triggering is whether "the amount of results older than keep has accumulated past clear_at_least," and in real work the context already exceeds trigger first.
- clear_at_least 40000 is near the maximum for total token reduction, and is not a loss under price weighting either (+0.5%). The lower bound of triggered sessions (p10 6.1%) is also high, with low variance. At 60000, triggering becomes even rarer, and while pricing improves, total token reduction drops.
- Recommendation: **trigger 100000 (API default), clear_at_least 40000, keep 3, gate `jev`**. The ceiling estimate is about 6.3% overall. In practice, it will be lower by the amount of conversations where the gate chooses `keep`, plus rework.

**Issue with the gate's query timing**: The current gate asks Jev once the previous context has reached trigger − clear_at_least/2. Since real work's main sessions have a prefix alone of about 80,000, under the above recommended value, almost every conversation would be judged right after the first response, with no history yet. Jev's own cost is small (about 1,000-1,500 per conversation, estimated at about 0.07% of the total). However, the material for judgment is only the task text. It would be better to ask once the erasable target (tool results older than keep) reaches clear_at_least, since there would be more judgment material, and queries would be limited to conversations where erasure is actually possible (about 6% of main, about 22% of sub). The proxy can count the size of tool results from the request body, so this is implementable.

### Change to the gate's query timing and update of defaults (2026-09-25)

The gate now asks Jev once, per conversation, only when all three of the following conditions are met: (1) the conversation's usage has been observed at least once, (2) the previous context ≥ trigger, and (3) the tool results estimated as erasable in the outgoing request body (older than keep, not excluded, converted from `tool_result` character count at 1.9 characters/token) ≥ clear_at_least. 1.9 is the real-work median, and since it tends to overestimate the token count, the query happens somewhat early. The default clear_at_least was changed from 20000 to 40000 (trigger 100000, keep 3 unchanged). The README was also updated. `go test ./...` passed.

Regression check (1 on run each, `claude-sonnet-5`/`medium`):
- `child-survey` (default 100000·40000): asked at the request after the child's context reached 110,690 (seq10, 130,364), and chose `keep` (Jev input 1,253). No erasure, quality 16/16.
- `chess-bugfix` (30000·10000): asked at the request after context 31,723 (seq11), and chose `clear` (Jev input 1,175). The server's erasure started at seq17 (about 10,000-11,000/request), quality 36/36, same-path net saving 9.4%. The estimate was about 6 requests earlier than the actual trigger, "early" as intended.

Since the bench's context does not reach the default trigger of 100000, the `clear` side has not been confirmed at the default value. Confirming the clear side at default values requires a long, real-work-scale session.

### Final settings and summary of expected effect (2026-09-25)

**Final recommended settings** (context editing itself remains disabled by default): `JEV_CLAUDE_CLEAR_TOOL_USES=on`, `JEV_CLAUDE_CLEAR_GATE=jev`, trigger 100000 (default), clear_at_least 40000 (new default), keep 3 (default). The gate requires a Jev key; without a key, no erasure occurs.

**Expected effect** (reproduction of real sessions from the last 30 days; upper bound assuming the gate always chooses clear and there is no rework):

| Target | Overall total token reduction | Sessions where erasure occurred | Min/median/max within those |
|---|---:|---:|---|
| Main (569 sessions) | 2.8% | 36 sessions (6%) | 2.0%/10.6%/20.0% |
| Subagents (583 sessions) | 9.9% | 129 sessions (22%) | 1.4%/15.8%/30.2% |
| Total | About 6.3% | — | Median across all sessions is 0% (most sessions see no erasure) |

The actual reduction will be smaller than this ceiling by the amount of conversations where the gate chooses `keep` and by rework. As live reference values (bench thresholds 30000·10000), `chess-bugfix` (clear) had same-path net saving of 2.9%/11.1%/14.2% (min/median/max), and `child-survey` (keep) had 0%, with both at perfect quality. Whether the clear side actually works in a live run at the default value (trigger 100000) has not yet been confirmed.

**Outstanding**: Live confirmation of the default values on a real-work-scale, long session. Checking the validity of the gate's judgment across the diverse tasks of real work. The README's Claude Code section has been updated to reflect this history and settings.

### Handoff (end of 2026-09-25 session)

#### Current state

- All changes are pushed to `origin/main` (latest `d21a40f`). Only this handoff section was uncommitted at the time it was appended.
- Claude Code: replacement via tool selection has been abandoned (`JEV_CLAUDE_ADVISE` is disabled by default); the main line for reduction is Anthropic's native context editing plus Jev's clear gate. The recommended settings are `JEV_CLAUDE_CLEAR_TOOL_USES=on`, `JEV_CLAUDE_CLEAR_GATE=jev`, trigger 100000, clear_at_least 40000, keep 3 (context editing itself is disabled by default). See the "Claude Code: ..." sections of this memo and the `## Claude Code` section of the README for the background and numbers.
- User's policy: keep using Claude Code in real work for a while and collect records. The next improvement target is Codex.
- Decisions already confirmed by the user: the idea of disabling skills/agents/plugins the user intends to use, in order to shrink the fixed prefix, is not adopted (because that would not be a reduction from jev-routing). The benchmark baseline is `claude-sonnet-5`/`medium` for both parent and child (for Codex, `gpt-5.6-terra`/`medium`).

#### Claude Code: procedure for recording real-world usage (performed by the user)

```bash
cd ~/repos/jev-routing && go build -o ~/.local/bin/jev-routing ./cmd/jev-routing && mkdir -p ~/jev-dogfood
# Keep the proxy resident (requires TYPESAFE_API_KEY or JEV_API_KEY. JEV_COMPACTION=off so only context editing is measured)
JEV_CLAUDE_CLEAR_TOOL_USES=on JEV_CLAUDE_CLEAR_GATE=jev JEV_COMPACTION=off \
  nohup jev-routing serve --host claude --listen 127.0.0.1:8787 >> ~/jev-dogfood/serve.log 2>&1 &
# Persist events (the proxy only keeps the most recent 1000 events in memory. Usage is appended later, so fetch 100 at a time with overlap and take the last line per (instanceId, seq) when aggregating)
nohup bash -c '
U=http://127.0.0.1:8787/dashboard/events; F=~/jev-dogfood/events.jsonl; since=0; cur=
while sleep 30; do
  r=$(curl -s "$U?since=$since") || continue
  iid=$(jq -r .router.instanceId <<<"$r") || continue
  if [ "$iid" != "$cur" ]; then cur=$iid; since=0; r=$(curl -s "$U?since=0") || continue; fi
  jq -c --arg i "$iid" ".events[]|.+{instanceId:\$i}" <<<"$r" >> "$F"
  max=$(jq "[.events[].seq]|max // 0" <<<"$r")
  [ "$max" -gt 0 ] && since=$(( max>100 ? max-100 : 0 ))
done' > /dev/null 2>&1 &
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN
ANTHROPIC_BASE_URL=http://127.0.0.1:8787 claude
```

`jev-routing run claude` works with the same environment variables (arguments go after `--`). However, since events disappear when the process exits, use `serve` for aggregation. With a non-first-party `ANTHROPIC_BASE_URL`, ToolSearch's default changes and the fixed prefix shrinks by about 5,000 tokens. For this reason, do not directly compare against past sessions that ran without the proxy.

**Work for one to two weeks later (not yet started)**: build an aggregation script that cross-references `~/jev-dogfood/events.jsonl` with `~/.claude/projects/**`. From per-request usage and `clearedInputTokens`, compute the same-path net saving (the same formula as `internal/bench/clearnet.go`), rework (re-fetching the same content of an already-cleared call), a breakdown of gate decisions (`clearGate`: clear/keep/error, `clearGateReason`), and Jev's own usage. What we want to confirm is whether the clear side actually kicks in at the default value (trigger 100000). If evaluating native compaction (`JEV_COMPACTION` default on: replacing Claude Code's `/compact` and auto-compaction with Jev's drop/truncate results), compare it over a separate period.

#### Codex: next steps (not yet started)

1. **Decompose the cause of the increase**: retake 2-3 `dual-facts` pairs (`jev-routing bench --agent codex --model gpt-5.6-terra --effort medium --tasks dual-facts --catalog 2 --modes off,on --reps 3`, `JEV_SELECTION_MODE=jev`, with the reasoning setting pinned by the bench to `preserve`), and per request break down (a) Jev's judgment cost, (b) changes in upstream cache read/write (suspected — not yet verified — that narrowing candidates changes the tool list per request and breaks the prompt cache), and (c) changes in request count and `codex-auto-review`'s auxiliary requests. For Claude, 99% of the increase was Jev's own cost. The raw logs from the previous 6 pairs (median reduction rate −9.55%, interval crossing zero, held) only exist on the previous machine.
2. **Room for reduction in real sessions (offline, no cost)**: from the records under `~/.codex/sessions`, aggregate the breakdown of fixed prefix, tool results, cache, and subagents, and estimate where room exists among tool selection, history shrinking, and compaction (see the script below for the method used with Claude).
3. Based on 1 and 2, choose a direction (reduce Jev's cost, a cache-stable selection fixed once per conversation, evaluation of native-compaction replacement, or doing nothing).
4. Implement it, and judge against the pre-registered 6 pairs. `xcell-locate` had few candidates, so Jev was never invoked due to the cost cutoff. Codex's subagent attempt was 0/3 due to the child failing to launch. Keep these as cautions when choosing tasks.

#### Operational notes

- The raw benchmark measurement data (`survey-reps6-sonnet/`, `survey-gate6/`, `bugfix-gate6/`, `gate2-*/`, etc.) and the analysis scripts (`extract.py`, `sim.py` [with the observed clear rule `stateless_min` and a selftest], `sweep.py`, `calib.py`, `prefix/relay.py`) are in the previous session's scratchpad (`/private/tmp/claude-502/…/scratchpad/ctxedit/`), and are likely to disappear once the session ends. The numbers have already been transcribed into this memo. The scripts can be rebuilt if needed.
- Previously, when a benchmark was launched from inside Claude Code, the parent session's `CLAUDE_CODE_*` environment variables leaked into the child (now fixed). `CLAUDE_CODE_SUBAGENT_MODEL` is intentionally inherited and included in both the record and the comparison key. To get comparable runs, pin it explicitly (e.g., `CLAUDE_CODE_SUBAGENT_MODEL=claude-sonnet-5`).
- Run-to-run benchmark comparisons (the total token difference between separately run baseline and intervention) scatter by −271% to +90% for Claude even under identical conditions (A/A). For grounding the effect, use same-path metrics plus quality and rework, and pre-register the judgment criteria in this memo before looking at results.
- The checklist at the top of this memo ("Immediate work list", V1-E1) is stale; much of it has since been implemented in later sections. It has not been updated.

### Re-measurement of the cause of the Codex `dual-facts` increase (2026-09-25)

Item 1 of the handoff was carried out. `gpt-5.6-terra`/`medium`, `--catalog 2 --modes off,on --reps 3`, `JEV_SELECTION_MODE=jev`, bench-fixed `JEV_COMPACTION=off`/`JEV_REASONING=preserve`. With the current `JEV_COST_GATE_MAX=3`, this task has 3 or fewer candidates, so Jev was invoked zero times across all intervention runs, and all 3 pairs were `jev_not_applied`, hence incomparable. So the series investigating the cause of the increase from past Jev usage was retaken in a separate directory with `JEV_COST_GATE_MAX=0` explicitly set. Raw data is saved at `/tmp/jev-codex-recheck-okQAaF/results/` (default gate) and `/tmp/jev-codex-recheck-okQAaF/ungated/` (gate disabled). Quality was 3/3 on all runs in both.

All 6 runs with the gate disabled passed usage/model/quality cross-checks, and all 3 pairs were comparable in `comparison.json`. Per-request `proxy-events.json` was cross-referenced against `runs.jsonl`, confirming that the sums of input, cache reads, output, and per-model request counts matched. Differences are intervention minus baseline. Total tokens are upstream input (including cache reads) plus upstream output plus Jev input/output; cache reads are not added separately.

| Pair | Total token diff | Jev input/output | Upstream input/output diff | Upstream request count diff | Non-cache input diff | `codex-auto-review` request count |
|---|---:|---:|---:|---:|---:|---|
| 1 | +81,082 | +13,122 | +67,960 | +2 | +33,058 | 2→2 |
| 2 | −118,050 | +13,141 | −131,191 | −4 | +37,200 | 2→2 |
| 3 | −17,826 | +13,097 | −30,923 | −1 | +41,580 | 2→2 |

- Jev was called twice per intervention run, and the selection was actually applied in only 1 of those calls. `codex-auto-review` issued 2 requests every time under both conditions; the input/output sum difference was +102/+94/+17, so it is not the main source of difference for these 3 pairs.
- In the first main-model request, the baseline's cache read was 24,320 all 3 times, while the intervention's was 0 all 3 times. Under intervention, the candidate list was rewritten from 3 to 2, and the first request's non-cache input increased substantially. This is consistent with prefix-cache destruction caused by the change in the candidate list, but the upstream cache key was not directly verified, so causation is unconfirmed. Codex's cache-write volume is not reported in usage and cannot be decomposed from measurements.
- The main model's request count varied by +2/−4/−1 under intervention, and the sign of the total difference also swung. These 3 pairs are for cause diagnosis and do not reach the 6 comparable pairs needed to decide on adopting a reduction effect. Under the default gate, Jev's cost for this task is 0, so the increase seen with past Jev usage should not be read as the effect of the current default value.

### Breakdown and intervention decision for real Codex sessions (2026-09-25)

`python3 scripts/analyze-codex-sessions.py 2026-08-26 2026-09-24` aggregated `~/.codex/sessions` over the completed 30-day period, excluding in-progress records. Of 475 files, 339 have per-response usage. For each file, it was verified that the sum of per-response input matches the final session cumulative total. The main targets were the interactive parent (`cli`) and subagents; short automated runs (`exec`) were tallied separately. Message bodies, filenames, and credentials are not included in the aggregate output.

| Target | With usage | Total input | Cache read / input | Median first-request input | Median first developer-instruction char count | Total tool-result char count |
|---|---:|---:|---:|---:|---:|---:|
| Interactive parent | 201 | 1,284,623,609 | 98.1% (per-session median 94.5%) | 40,732 | 59,128 | 33,795,224 |
| Subagent | 33 | 73,567,083 | 95.3% (same 92.1%) | 40,647 | 70,690 | 6,555,788 |
| Parent with single `gpt-5.6-terra`/`medium` config | 25 | 26,178,547 | 92.7% (same 90.5%) | 41,669 | 58,781 | 2,612,820 |

Subagents account for about 5.4% of the combined parent+child input/output. Of the parent's first developer instructions, the portion whose body was identical across 5 or more other parent sessions had a median of 57,796 characters. This is a character-count observation showing that the fixed prefix is large, not the actual token breakdown of the sent prompt (including tool definitions, etc.) or the reducible amount. Of the parent's 7,337 tool results, 538 exceeded 20,000 characters, accounting for 47% of the result character count. There is room to truncate large results on first occurrence, but missing-necessary-information and re-fetch have not been measured. Tool-result character counts have not been converted into the token count of repeated transmissions. Session records alone cannot separate out the exact sent token counts for the fixed prefix and tool results respectively, Jev usage, or cache-write volume.

Compaction was confirmed via a separate `compacted` record. Of the 201 parent sessions, 12 had it, occurring 14 times in total; there were 0 for the 33 subagent sessions and the 25 parent sessions with the standard model/reasoning configuration. 6 parent sessions had a single request whose input reached over half of the recorded context window. Counting compaction by input-halving alone would only catch 2 cases, so `compacted` is used to count compaction occurrences.

**Adoption decision: keep Codex's default cost gate (`JEV_COST_GATE_MAX=3`) and do not implement a new intervention this time (★5).** Under the current default for `dual-facts`, Jev's cost is 0, and forcing Jev usage came with the loss of the first cache read and a judgment cost of about 13k tokens. Real sessions are dominated by cache reads; pinning selection per conversation (★1) lacks a safe way to identify Codex conversations and invalidate on candidate changes, and the first-request candidate rewrite still remains. The idea of shrinking only Jev's input (★2) has no effect on the default path where candidates are 3 or fewer. Shrinking old tool results in history afterward (★1) has unverified effects on the cache prefix and on re-fetching. Truncating large tool results on first occurrence (★2) is a separate task requiring a comparison that includes quality. Evaluating replacement of existing native compaction (★2) should wait until a long real session that triggers it is obtained. None of these have a verified effect on the reduction of the overall real workload, and the difference across 3 pairs is not treated as a causal effect.

### Pre-registration of Codex tool-selection/compaction benchmark (2026-09-25)

At the user's additional request, external outcomes and usage are repeatedly confirmed with the real CLI. **Tool selection** was single-shot tested on standard `code_mode_host=true` `dual-facts --catalog 4`, confirming 0 Jev calls and `jev_not_applied`. MCP is outside the narrowing target as an external namespace, and the local candidates numbered 3, staying within the default cost gate. The legacy-style tool listing also had 0/3 on both sides because the bench MCP was not exposed. The median selection effect under the standard condition is set to "not computable," and run-to-run coincidental differences are not called 0% or a reduction.

**Codex's Jev compaction replacement** repeatedly re-compacted at threshold 32,000, bloating the request body, and at 45,000 with fewer than 20 stages, a trial re-executed already-completed reads and quality dropped to 0/3. It turned out the compaction instruction was overriding Jev's goal, and there was no upstream fallback for a summary that did not shrink enough — both fixed. On the real CLI, some trials had increased request counts, and because the CLI includes the synthesized response in its own usage, cross-checking against the real upstream also failed. The total-token effect cannot be judged. Therefore Codex-side replacement is disabled by default and only enabled under an explicitly specified experimental configuration. The pilots up to this point are not used for judging the effect.

**Conditions for the upcoming 6 pairs (fixed before seeing results)**: the task is `compact-facts`, which has external scoring and full-text-read evidence for 20 files instead of 10. Parent model `gpt-5.6-terra`/`medium`, with selection and Jev compaction replacement disabled on both sides. Baseline uses the Codex standard compaction threshold of 900000, intervention uses 55000, `--modes off,on --reps 6 --timeout-min 7`, alternating order. In the single-shot pilot, quality was 3/3 on both sides, usage cross-check succeeded, baseline had 0 compaction requests, and intervention had 1 — but the total difference is not judged as an effect.

The primary metric is the median of `100 × (baseline total tokens − intervention total tokens) / baseline total tokens` per pair. The total is upstream input+output plus Jev input/output (Jev is 0 in this series). Cache reads are within input. All 6 pairs are run without stopping early or adding more, and quality, compaction request count, request count, comparable-pair count, per-pair difference, median, min/max, and elapsed time are reported for all started runs. Comparability requires 3/3 quality on both sides, full-text reads of the 20 files, 0 baseline compaction requests and at least 1 for intervention, matching model/reasoning settings, matching CLI-and-proxy usage, and no missing data or contamination. Adoption criterion: no quality degradation, 6 comparable pairs, and a median total-token reduction of 5% or more. However, since this is a separate-path series of 6 pairs, causation is not asserted.

#### Re-registering the compaction-threshold comparison conditions (after completing the first series)

The above 6 pairs completed as planned; all 12 runs had 3/3 quality, correct read order/no duplication across the 20 files, and successful cross-checks of host usage. Only 2 runs on the intervention side had explicit compaction requests, so only 2/6 comparable pairs were pre-registered — falling short of the criterion for judging the effect. For reference, the median total-token difference across all 6 pairs was a 13.11% reduction, but since this includes 4 pairs without a compaction trigger, it is not called the effect of compaction firing. Raw data is at `/tmp/jev-codex-bench-next-MRE2Pb/native-twenty-55k-reps6/`.

Looking at the request-body observations, even in pairs with 0 explicit compaction requests, for the same 22-request example, the baseline's body increment per tool read was about 4,826 bytes, while the low-threshold side was about 1,187 bytes from partway through. The initial guess was that the threshold also affects the amount of tool-result history carried forward, but in [Codex's config-handling code](https://github.com/openai/codex/blob/rust-v0.157.0-alpha.11.1/codex-rs/models-manager/src/model_info.rs#L19-L46) and [history-storage code](https://github.com/openai/codex/blob/rust-v0.157.0-alpha.11.1/codex-rs/core/src/context_manager/history.rs#L441-L453) of the same CLI version used in execution, the compaction threshold and the tool-result cap are separate settings, and no direct control path was confirmed. The cause of the shortened request body remains unidentified. Therefore, the next series evaluates not "the compaction event alone" but **Codex's overall context-limit configuration**. Selecting only runs that fired would select the sample by execution path, so intervention runs with 0 compaction requests are also included in the comparison, with request counts reported separately. Both baseline and intervention proxy paths are unified to `baseline`.

**Pre-registration of the new 6 pairs**: task, model, reasoning effort, thresholds 900000/55000, alternating execution order, 7-minute cap, and full-text reads of 20 files each are the same as the previous series. Jev selection and Jev compaction replacement are both disabled. All runs require 3/3 quality, read-trace evidence, matching upstream and CLI usage, no missing data or contamination, and the same comparison key. Presence/absence of compaction requests is recorded but is not a comparability condition. The primary metric is the median total-token reduction rate across 6 pairs; adoption criterion: no quality degradation, 6 comparable pairs, median ≥5%. Each pair's difference, range, request count, in/out-of-cache, and elapsed time are also reported. This new series is run independently to 6 pairs and is not merged with the numbers from the previous series.

#### Compaction-threshold confirmation series: results of the 6 pairs

Saved at `/tmp/jev-codex-bench-next-MRE2Pb/context-policy-reps6/`. All 12 runs used `gpt-5.6-terra`/`medium`, had 3/3 quality, read the 20 files once each in order 1→20, matched CLI-and-proxy usage, and had no timeouts or missing data. The sum of per-request usage was recomputed and confirmed to match `runs.jsonl`, and total tokens were recomputed and confirmed to match the sum of input/output. Differences are baseline−intervention; rate is difference/baseline total. All baseline compaction requests were 0.

| Pair | Baseline total | Intervention total | Reduction rate | Request count baseline→intervention | Intervention compaction requests |
|---|---:|---:|---:|---|---:|
| 1 | 1,118,315 | 1,249,163 | −11.70% | 22→30 | 3 |
| 2 | 1,141,829 | 1,099,231 | +3.73% | 24→23 | 0 |
| 3 | 1,160,649 | 1,221,472 | −5.24% | 22→28 | 2 |
| 4 | 1,026,570 | 1,118,717 | −8.98% | 22→27 | 3 |
| 5 | 1,252,316 | 1,093,965 | +12.64% | 24→26 | 2 |
| 6 | 1,231,341 | 1,024,846 | +16.77% | 24→24 | 2 |

**The pre-registered median across the 6 pairs is −0.75% (an increase of 9,112.5 tokens).** The range is −11.70% to +16.77%, with 3 pairs improved and 3 worsened. `comparison.json` treats all 6 pairs as comparable, but the effect judgment is `hold` because the interval straddles the threshold. It falls short of the 5% adoption criterion, so the lower threshold of 55000 is not adopted for real use. On the intervention side, explicit compaction occurred in 5 of the 6 runs, but pairs with 0 compaction also showed differences, and the source of the difference observed in path variance versus request bodies cannot be separated. The intervention side's median non-cache input was 192,847 versus 53,046 for baseline, and the median time was 150 seconds versus 95 seconds. There are concerns about worsening not only total volume but also cost and latency. The exploratory series' +13.11% did not reproduce in the confirmation series.

The final configuration leaves Codex's standard compaction threshold unchanged, and keeps the Jev summary replacement — which caused quality and usage problems — disabled by default. The default cost gate for tool selection is also kept. Under standard Codex, Jev selection does not fire because candidates number 3, so the median improvement rate for tool selection is not computable. Measuring another host or a practical condition with 4 or more candidates would require a new task and pre-registration.

#### Pilot conditions rejected and history of fixes (addendum)

The following are trials not counted toward the formal effect judgment. Raw logs are saved locally at `/tmp/jev-routing-compact-pilot-*/` and `/tmp/jev-codex-bench-next-MRE2Pb/`, but being temporary storage, they may disappear. Quality is the 3 external scoring items per run.

| Target/condition | Observation and reason for rejection | Next action |
|---|---|---|
| Tool selection, standard Codex, `dual-facts --catalog 4` | Both baseline and intervention 3/3, but 0 Jev calls. The additional MCP is outside the narrowing target as an external namespace, leaving local candidates at 3. The total difference between separate runs is not a selection effect | Keep the default cost gate; median not computable |
| Legacy-style tool listing `features.code_mode_host=false` | The bench MCP was not exposed, giving 0/3 on both sides | Abandoned adopting a path that differs from the standard configuration |
| Jev compaction replacement, repeated-sentence log, threshold 50000 | Both sides 3/3, but 0 compaction requests on intervention | Changed the read result to a high-entropy deterministic log to confirm firing |
| Same log, threshold 32000 | 9 compaction warnings on baseline, 7 Jev replacements on intervention. Intervention's request body bloated from 134,612 to 806,643 bytes, and usage/tool-result cross-check failed | Rejected the too-low threshold. Added a safety net to forward upstream when the summary does not shrink enough |
| High-entropy 6-stage, threshold 45000, 4 rounds before/after fixes | Intervention-side quality swung 3/3, 3/3, 0/3, 3/3 in fix order, and all 4 rounds had a mismatch between CLI and real upstream usage (`taskTokens=null`). The 0/3 run re-read already-read files and produced no answer. Even after quality recovered, one run had request count increase from 9 (baseline) to 18 (intervention) | Implemented, in order: prevention of stale compaction-instruction misdetection, forwarding when the previous summary was not shortened enough, and excluding the compaction instruction from Jev's goal. Total-volume effect deemed incomparable, and Codex's Jev replacement disabled by default |
| Codex standard compaction, 6-stage, threshold 45000/60000 | 45000: intervention compacted twice, both sides 3/3, single-run total volume increased. 60000: quality 3/3 but 0 compaction, effect unclear from run-to-run difference alone | Extended the task so work continues after compaction |
| Standard compaction, 10-stage, threshold 50000/55000 | 50000: intervention compacted twice, both sides 3/3, but baseline's first short `cat` output was missing from CLI records, so the full-text-read evidence was missing. 55000: 0 compaction. Both single-shot | Added verifiable output at the edge logs too and extended to 20 stages. Also fixed the scoring evidence so read duplication/order violations are not missed |
| Standard compaction, 20-stage, threshold 55000 single-shot and first 6 pairs | Single-shot: both sides 3/3, intervention compaction fired once, apparent reduction was large — but in the first 6 pairs, intervention firing was only 2/6. Does not meet the precondition that firing is required | Did not adopt the earlier +13.11%; re-registered the overall-configuration comparison as a separate series and retook 6 pairs |

**Measurement lesson**: the Codex CLI adds the apparent input/output of Jev's synthesized compaction response into its own usage, but the proxy does not actually send it upstream, so it is not added to billable usage. A Jev-replacement run that does not correct for this discrepancy is treated as incomparable. On the other hand, the standard compaction threshold comparison had both conditions with Jev selection/replacement turned off, went through the same `baseline` proxy path, and cross-checked CLI and proxy usage. The per-column median differences in `summary.md` are not the median of per-pair differences. Adoption judgments use the comparable pairs in `comparison.json` and per-pair differences recomputed from per-request records.

### Codex: deterministic truncation of large tool results (2026-09-25)

**Implementation**: with `JEV_CODEX_TOOL_OUTPUT_MAX=N` (default 0 = disabled), any tool result (`function_call_output`, etc.) included in a Codex Responses request that exceeds N bytes is replaced with the first N/2 and last N/2 bytes plus an omission note. Because Codex resends the entire history on every request, all results — not just the newest — are transformed by the same rule every time, preserving the prefix cache. This is independent of routing/selection/compaction settings and does not call Jev.

**Rationale**: of the 2,301 tool results sent in the most recent 150 sessions, 305 (13%) exceeded 20,000 bytes but accounted for 54% of the bytes. Codex on `gpt-5.6-terra` has a `truncation_policy` of tokens 10000, and the model specifies `max_output_tokens` for `exec` on each call (2,062 exec calls, 1,288 with an explicit value, ranging 1000-30000). Large outputs arise when the model chooses a large cap.

**Bench task `large-facts`**: 6 low-entropy logs of about 32KB each with facts embedded one line at a time near the start, middle, and end, followed by a full `cat` and then answering. The task text instructs setting `max_output_tokens` to 12000 or more (in a pilot without this instruction, two sets, the model chose a small cap and each result came out to about 10KB, so truncation never fired). This task is therefore a stress test for "when a large output is requested," not the reduction rate for the overall real workload. The pilot (run3, not used for the effect judgment) had 6/6 on both sides, 42 truncations on intervention (including re-application per request), and non-cache input per request went from about 13.5k to about 9k, with no observed prefix-cache breakage.

**Conditions for the upcoming 6 pairs (fixed before seeing results)**: `gpt-5.6-terra`/`medium`, `--tasks large-facts --modes off,on --reps 6 --codex-tool-output-max 20000`. Both sides use the same `baseline` proxy path, Jev selection and Jev compaction replacement are disabled, and the only difference is the intervention side's truncation. No stopping early or adding more. Comparability requires full quality on both sides, full-text read evidence for the 6 files, matching CLI-and-proxy usage, no missing data or contamination, and the same comparison key. Truncation firing is not a comparability condition (to avoid sample selection by path); the count is reported separately. The primary metric is the median of `100 × (baseline total tokens − intervention total tokens) / baseline total tokens` per pair. Adoption criterion: no quality degradation on the intervention side, 6 comparable pairs, median reduction ≥5%. Per-pair difference, range, request count, in/out-of-cache input, re-fetch count, and elapsed time are also reported. Even if adopted, the default stays disabled; enabling it for real use is a separate decision.

#### Truncation of large tool results: results of the 6 pairs

Saved at `/tmp/jev-codex-trunc-reps6/`. All 12 runs used `gpt-5.6-terra`/`medium`, had 6/6 quality, full-text read evidence for the 6 files, matching CLI-and-proxy usage, and no timeouts, missing data, or contamination. Execution order alternated per rep. Totals were recomputed from per-request `proxy-events.json` and matched `runs.jsonl`/`comparison.json` in all runs. Differences are baseline−intervention; rate is difference/baseline total.

| Pair | Baseline total | Intervention total | Reduction rate | Request count baseline→intervention | Intervention truncation count | Re-fetch baseline→intervention | Non-cache input baseline→intervention | Elapsed sec baseline→intervention |
|---|---:|---:|---:|---|---:|---|---|---|
| 1 | 628,546 | 644,338 | −2.51% | 9→12 | 41 | 0→6 | 104,995→76,562 | 46.8→59.6 |
| 2 | 626,894 | 541,287 | +13.66% | 9→10 | 39 | 0→6 | 97,597→70,486 | 49.2→49.6 |
| 3 | 575,399 | 801,236 | −39.25% | 9→13 | 56 | 0→1 | 100,284→85,873 | 43.5→78.9 |
| 4 | 939,950 | 679,631 | +27.69% | 12→12 | 45 | 1→0 | 117,000→78,695 | 61.7→62.5 |
| 5 | 704,126 | 646,068 | +8.25% | 10→11 | 49 | 1→7 | 142,778→79,104 | 43.2→58.8 |
| 6 | 704,557 | 647,288 | +8.13% | 10→11 | 49 | 1→1 | 113,992→79,230 | 53.4→69.1 |

**The pre-registered median across the 6 pairs is +8.19% (a reduction of 57,663.5 tokens).** The range is −39.25% to +27.69%, with 4 improved and 2 worsened. The pre-registered adoption criterion (no quality degradation, 6 comparable pairs, median ≥5%) was met. However, the effect judgment in `comparison.json` is `hold` (`interval_crosses_threshold`) because the interval straddles the threshold, and the variance is large. The worst case, pair 3, is due to a path difference where the intervention-side request count increased from 9 to 13.

On metrics comparable along the same path, there is a consistent difference. Non-cache input decreased in all 6 pairs (median 109,493.5→78,899.5). On the other hand, the intervention side often re-fetches the truncated middle section with `rg` etc. (0-7 re-fetches), and request count increased in 5 pairs. The median elapsed time went from 48.0 to 61.05 seconds, so latency worsened.

**Decision**: since the pre-registered criterion was met, adopt it as an experimental feature. The default stays disabled (`JEV_CODEX_TOOL_OUTPUT_MAX=0`), enabled with `JEV_CODEX_TOOL_OUTPUT_MAX=20000`. This task is a stress test for when the model chooses a large output cap; it is not the reduction rate for the overall real workload, and given the wide interval, causation is not asserted. Whether to enable it for real use is left to the user, weighing the worsened latency and increased re-fetching. Known display issue: `summary.md`'s "Requests Jev steered" also counts requests that became `changed` due to this truncation (`JevCalls=0`).

#### Default value change (2026-09-25)

At the user's instruction, Codex's tool-result truncation was enabled by default (fixed threshold of 20000 bytes). The environment variable is now only the on/off switch `JEV_CODEX_TOOL_OUTPUT_TRUNCATE` (default on); the numeric `JEV_CODEX_TOOL_OUTPUT_MAX` was removed. The bench's comparison condition was replaced with `--codex-tool-output-truncate`. The results of the 6 pairs above are based on the same rule at threshold 20000.

#### Wrap-up (2026-09-25)

By the user's decision, Codex tool-result truncation is now considered done for this round. The outcome: enabled by default (threshold 20000 bytes, disabled via `JEV_CODEX_TOOL_OUTPUT_TRUNCATE=off`), with the stress test `large-facts` showing a median total-token reduction rate of +8.19% across 6 pairs (interval straddles zero, `hold`). What remains unverified is the reduction rate for the overall real workload and the latency worsening from re-fetching. The background was recorded in the README's `## Codex` section. The next improvement target is compaction.

### Excluding Jev from the primary metric (2026-09-25)

At the user's instruction, the primary token-reduction metric was changed to "the agent's (parent and child) upstream input (including cache) plus upstream output," excluding Jev's input/output. Jev's usage fee is very low, so there is no need to include it in the calculation. Note that this memo's prior primary KPI (upstream + Jev input/output) reflects judgments that included Jev. The bench's `comparison.json`, effect judgments, and dashboard have all been aligned to the same definition. Jev's usage is kept as a separate figure.

### Codex: porting and pre-registration of the jev-gateway-style tool-steering approach (2026-09-25)

**Implementation**: with `JEV_CODEX_STEER=on` (default off), while keeping the tool list of a Codex Responses request unchanged, Jev's judgment rewrites `tool_choice` to `forced` (distinguishing function/custom types) or `none`. Requests are forwarded as-is when confidence is below 0.7, when there is a contradiction between "which tool" and "whether a tool is needed" judgments, for namespace/hosted selection, for conversations containing `agent_message`, and for `previous_response_id`. If upstream returns 400/422, the original is resent once. Candidate narrowing and the cost gate are not used. This was aligned with jev-gateway 0.3.1's `decide.js` and related code. `direct`, `hint`, the two-stage selection for over 120 items, and decompression of compressed bodies were not ported. The rationale: jev-gateway itself was reproduced under its published conditions (Codex 0.154, `gpt-5.6-sol`, `chess-bugfix`, a bare environment) over 5 pairs, yielding a median of −31% upstream requests, −30% input, −31% output.

**Pilot** (not used for the effect judgment): `chess-bugfix`, 1 pair, both sides 36/36. Intervention steering: forced 29, none 1, forward 0, upstream rejections 0. Upstream input/output went from 1,448,879 to 1,390,928, requests from 33 to 30. The baseline side called a subagent once, and the child session could not be verified, giving `child_session_unverified` and making it incomparable.

**Main measurement conditions (fixed before seeing results)**: `gpt-5.6-terra`/`medium`, Codex 0.158, `--tasks chess-bugfix --modes off,on --codex-steer --reps 6` (alternating order). The primary metric is the median of `100×(baseline−intervention)/baseline` for upstream input (including cache) plus upstream output. Jev's usage is reported separately. Comparability requires quality maxed on both sides, successful upstream usage cross-check, no missing data or contamination, the same comparison key, and no `child_session_unverified`. If comparable pairs fall short of 6, add more pairs under the same conditions and stop once 6 comparable pairs are reached (the total pair cap is 10; if still short at the cap, the judgment is indeterminate). Adoption criterion: no quality degradation on the intervention side, 6 comparable pairs, median reduction rate ≥5%. All pairs' differences, range, request count, in/out-of-cache input, elapsed time, mode breakdown, and subagent calls are also reported.

#### Result of the main measurement: indeterminate (measurement-side false positive)

All 6 pairs at `/tmp/jev-codex-steer-reps6/` had `child_session_unverified` on every pair, giving 0 comparable (rep6 also had missing usage data). Since it became impossible to reach 6 comparable pairs even with 4 more pairs, only 1 additional run was taken before stopping (`/tmp/jev-codex-steer-extra/`). Per the pre-registered rule, this is treated as indeterminate. The cause was on the measurement side. `gpt-5.6-terra` calls `spawn_agent` almost every time, but it fails internally within Codex (`collab spawn failed: no thread with id`), and no child request is actually made. The proxy was recording this as a successful launch even when the result was an error, causing the bench to mark it incomparable as an unverified child session. The effect numbers were never examined. Fixed so that a launch failure is not counted.

#### Re-registration: the fixed 6 pairs (fixed before seeing results)

Aside from the measurement fix, the conditions are the same as the previous registration: `gpt-5.6-terra`/`medium`, Codex 0.158, `--tasks chess-bugfix --modes off,on --codex-steer --reps 6` (alternating order). The primary metric is the median reduction rate for upstream input (including cache) plus upstream output; Jev is reported separately. Comparability requires quality maxed on both sides, successful upstream usage cross-check, no missing data or contamination, the same comparison key, and no `child_session_unverified` (a launch failure is not counted as a child session). If comparable pairs fall short of 6, add more pairs under the same conditions and stop at 6 comparable (total pair cap 10; indeterminate if still short). Adoption criterion: no quality degradation on the intervention side, 6 comparable pairs, median reduction rate ≥5%. The previous series' 6 pairs are not used for the effect judgment.

#### The fixed 6 pairs: results and decision

Saved at `/tmp/jev-codex-steer-v2/`, with additions at `-extra/` through `-extra3/` (9 pairs total). As pre-registered, stopped once comparable pairs reached 6. Quality was 36/36 across all 18 runs. The 3 incomparable pairs (v2.4, extra.1, extra2.1) had a combination of missing usage data, failed upstream usage cross-check, and unverified child sessions. extra.1's intervention side had 26 forwards due to `agent_message`. `spawn_agent` launch failures (`collab spawn failed:`) occurred 12 times across the 18 runs, and after the fix, none of these were counted as subagent calls.

| Pair | Upstream total baseline→intervention | Reduction rate | Request count baseline→intervention | Intervention forced/none/forward |
|---|---|---:|---|---|
| v2.1 | 909,217→1,801,139 | −98.10% | 20→39 | 30/1/8 (low confidence) |
| v2.2 | 1,344,008→1,204,840 | +10.35% | 29→25 | 23/1/1 |
| v2.3 | 963,375→1,297,962 | −34.73% | 21→32 | 31/1/0 |
| v2.5 | 1,204,016→1,678,800 | −39.43% | 23→36 | 35/1/0 |
| v2.6 | 800,791→1,275,789 | −59.32% | 19→29 | 23/1/5 |
| extra3.1 | 972,774→1,007,502 | −3.57% | 21→21 | 20/1/0 |

**Pre-registered judgment: median reduction rate −37.08% (an increase), min −98.10%, max +10.35%, 1 improved, 5 worsened. Not adopted. `JEV_CODEX_STEER` remains off by default.** The increase is mainly due to an increase in upstream request count (up to about 2x at most). Non-cache input was at nearly the same level. On the intervention side, `exec` was forced for nearly every request. This is consistent with 1 pair run with jev-gateway itself under the same configuration (terra, Codex 0.158) (request count 19→42). The reproduction result under jev-gateway's published conditions (`gpt-5.6-sol`, Codex 0.154) (−31% requests) does not hold under this configuration. Whether the model or the Codex version is the cause has not been separated.

**Cause isolation (diagnostic, not used for adoption decision)**: run 5 pairs under the same conditions as the terra series but with only the model changed to `gpt-5.6-sol` (`/tmp/jev-codex-steer-sol/`). If request count also increases under Sol, Codex 0.158 (or this bench's configuration) is the cause; if it decreases, it is specific to terra. No additions or early stopping.

#### Cause isolation: Sol's results (stopped at 3 pairs on the user's instruction)

Saved at `/tmp/jev-codex-steer-sol/`. Conditions the same as the terra series, with only the model changed to `gpt-5.6-sol`/`medium`. At the user's instruction, judgments of incomparability due to missing data, cross-check failure, or unverified child sessions are ignored, and the raw token amounts of upstream (input+output) are compared instead. Quality passed on all 6 runs.

| Pair | Upstream total baseline→intervention | Reduction rate | Request count baseline→intervention |
|---|---|---:|---|
| 1 | 3,038,822→1,357,950 | +55.3% | 68→32 |
| 2 | 2,092,434→2,431,213 | −16.2% | 46→51 |
| 3 | 2,180,823→1,769,076 | +18.9% | 44→44 |

The median reduction rate across the 3 pairs is +18.9% (2 improved, 1 worsened). Partway through the 4th pair, the run was stopped at the user's instruction.

### Codex tool steering: summary and discussion of trial and error (wrap-up on 2026-09-26)

**History**
1. The old Codex path (candidate narrowing + cost gate) never calls Jev on standard Codex because candidates number 3. Forcing Jev use broke the cache by changing the tool list.
2. Following a report of jev-gateway's effect on Codex, jev-gateway itself was reproduced under its published conditions (Codex 0.154, `gpt-5.6-sol`, a bare environment) over 5 pairs. Upstream request count −31%, input −30%, output −31% (median), reproducing the published trend.
3. Jev was excluded from the primary metric (Jev is cheap, per the user's instruction). This change meant that past red-ink judgments made on the grounds of Jev's cost needed to be re-evaluated on upstream alone.
4. Ported jev-gateway's `forced`/`none` approach as `JEV_CODEX_STEER` (default off). It rewrites only `tool_choice` while keeping the tool list unchanged.
5. The pre-registered 6 pairs on `gpt-5.6-terra`/`medium`, Codex 0.158 gave a median reduction rate of −37.08% (1 improved, 5 worsened), not adopted. Request count increased up to about 2x. Along the way, there was a measurement bug that misdetected Codex's internal `spawn_agent` launch failure as a subagent call, making all pairs incomparable; it was fixed (`internal/proxy/application.go`).
6. In a diagnostic with only the model changed to Sol, the median across 3 pairs was +18.9% (2 improved, 1 worsened).

**Discussion**
- The effect appears to depend strongly on the model. Sol showed a reduction, matching the jev-gateway reproduction, while terra showed an increase. Since Sol shows a reduction even on Codex 0.158, the model difference is likely the main factor rather than the Codex version (Sol is only 3 pairs and unconfirmed).
- The increase for terra is due to increased upstream request count. Forcing `exec` may have prevented the model from breaking off with text responses, extending the turn (unverified).
- For either model, per-run request-count variance is large (44-68 for Sol's baseline). Judgments swing with few pairs.
- Current conclusion: `JEV_CODEX_STEER` stays off by default. Whether it is worth enabling per model needs to be confirmed with a pre-registered series (6+ pairs) for Sol.

**Next steps (not yet started)**
- Run a pre-registered 6-pair series for `gpt-5.6-sol`. If effective, consider a setting that enables it by default per model.
- Confirm from per-request records the mechanism behind terra's request-count increase (whether the model returned a text response right before the forced `exec`).
- The Codex compaction improvement was shelved because the real-session ceiling was only about 0.1-0.3%. Only the measurement fixes (correcting the cross-check for synthesized responses, `--codex-native-compaction`) have been implemented.

---

### Policy: jev-routing does nothing for `gpt-5.6-terra` (2026-09-26, user decision, not yet implemented)

By the user's decision, when Codex's model is `gpt-5.6-terra`, jev-routing will not intervene at all and will pass requests through unchanged. There are two reasons. First, for terra, tool steering increased upstream tokens in the pre-registered 6 pairs (median reduction rate −37.08%), with no prospect of improvement. Second, terra is an older model whose future usage is expected to decline. Because terra has standard performance, it has been used as the benchmark baseline model up to now.

- The scope is all Codex-path interventions: tool steering (`JEV_CODEX_STEER`), tool-result truncation (`JEV_CODEX_TOOL_OUTPUT_TRUNCATE`, default on), compaction replacement, and so on. The model is assumed to be judged from the `model` field of the request body.
- Note: the adoption basis for tool-result truncation (the `large-facts` 6 pairs, median +8.19%) was measured on terra. Implementing this policy would mean losing that effect for terra as well.
- The benchmark baseline model going forward will move from terra to another model. The candidate is `gpt-5.6-sol`, which showed a reduction direction under tool steering. The pre-registration will be revised once the baseline model is decided.

### Codex: pre-registered 6 pairs for `gpt-5.6-sol` (2026-09-26)

The earlier 3-pair diagnostic (+55.3%/−16.2%/+18.9%, median +18.9%) was stopped early and is not used for the adoption decision. From here, this is formally pre-registered and run independently.

**Conditions**: `gpt-5.6-sol`/`medium`, Codex 0.158, `--tasks chess-bugfix --modes off,on --codex-steer --reps 6` (alternating order). Both sides use the same `baseline` proxy path, Jev compaction replacement is disabled, and tool-result truncation stays at its default (on) identically on both sides. The primary metric is the median of `100×(baseline−intervention)/baseline` for upstream input (including cache) plus upstream output. Jev's usage is reported separately.

**Comparability judgment (changed by the user's instruction)**: missing usage data, CLI-and-proxy cross-check failure, and unverified child sessions are not treated as reasons for incomparability. Only runs where both sides have full quality and both sides have recorded upstream input/output values are compared (if missing, it is noted individually and that one point excluded from the median calculation). Contaminated runs are excluded.

**Iteration and stopping**: run 6 pairs as one series. If quality degrades, record the reason but do not stop early. After completing 6 pairs, judge whether the median meets the adoption criterion (no quality degradation, median reduction rate ≥5%). If met, judge that "it is worth enabling by default per model"; if not, judge "keep off by default for Sol too."

**Position of this series**: this is not a diagnostic but a formal pre-registration. The numbers are not changed after seeing the results.

---

#### `gpt-5.6-sol` pre-registered 6 pairs: results and judgment (2026-09-26)

Saved at `/tmp/jev-codex-steer-sol-v2/`. All 12 runs completed, no contamination, no failed requests. Quality was full marks (36/36) across all 12 runs. Ignoring incomparability judgments (missing data, cross-check failure, unverified child session), the judgment used the raw token amounts of upstream (input+output, excluding Jev) (per the user's instruction).

| Pair | Upstream total baseline→intervention | Reduction rate | Request count baseline→intervention |
|---|---|---:|---|
| 1 | 3,469,355→1,018,807 | +70.63% | 69→23 |
| 2 | 2,117,809→2,088,821 | +1.37% | 51→51 |
| 3 | 2,394,135→2,758,020 | −15.20% | 48→59 |
| 4 | 2,098,055→2,531,710 | −20.67% | 45→52 |
| 5 | 2,283,937→1,376,958 | +39.71% | 45→27 |
| 6 | 2,764,580→1,735,553 | +37.22% | 53→40 |

**The median reduction rate is +19.30% (4 improved, 2 worsened). Meets the pre-registered adoption criterion (no quality degradation, median ≥5%).** In the `tool_choice` breakdown on the intervention side (re-aggregated from proxy-events.json and corrected on 2026-09-26): worsened pair 4 had 45 of 48 forwards due to `agent_message` (subagent-related conversation) and only 4 forced requests, so steering was effectively not applied. Worsened pair 3, by contrast, had 54 forced and only 4 forwards (all low_confidence): it got worse while steered. The 4 improved pairs had 12–24 forced requests, and two of them also had many `agent_message` forwards (pair 2: 28 of 35; pair 6: 18 of 27). The earlier text attributed "28 of 35" to pair 3; it is pair 2's figure. This is the opposite direction from terra's pre-registered 6 pairs (median −37.08%, 5/6 worsened), supporting the view that the effect depends strongly on the model.

**Decision: for `gpt-5.6-sol`, it is judged worthwhile to enable `JEV_CODEX_STEER` by default per model.** However, with 4 improved and 2 worsened, the median is somewhat pulled up by an outlier (pair 1's +70.63%), so the variance is large. Implementation (branching by model name, with terra off and sol on) and its subsequent confirmation are not yet started.

**Next steps (not yet started)**
- Implement branching the default value by model name (`gpt-5.6-terra` fixed off, `gpt-5.6-sol` fixed on, other models judged as needed).
- After implementation, take a confirmation series (6+ pairs) under the same conditions to confirm the implementation behaves as intended.
- Decide whether to run the same diagnostic and pre-registration for other Codex models (luna, astra, etc.).

---

### Codex: pre-registered 6 pairs for `gpt-6-luna` (2026-09-26)

Following terra (median −37.08%, not adopted) and Sol (median +19.30%, meets adoption criterion), run the same verification for `gpt-6-luna`.

**Conditions**: `gpt-6-luna`/`medium`, Codex 0.158, `--tasks chess-bugfix --modes off,on --codex-steer --reps 6` (alternating order). Both sides use the same `baseline` proxy path, Jev compaction replacement is disabled, and tool-result truncation stays at its default (on) identically on both sides. The primary metric is the median of `100×(baseline−intervention)/baseline` for upstream input (including cache) plus upstream output. Jev's usage is reported separately.

**Comparability judgment**: missing usage data, CLI-and-proxy cross-check failure, and unverified child sessions are not treated as reasons for incomparability. Only runs where both sides have full quality and both sides have recorded upstream input/output values are compared. Contaminated runs are excluded.

**Iteration and stopping**: run 6 pairs as one series without stopping early. After completing 6 pairs, judge whether the median meets the adoption criterion (no quality degradation, median reduction rate ≥5%).

---

### Claude Code: re-verification of the `claude-opus-5-5` advise (2026-09-26)

Re-verify the past `claude-sonnet-5`/`dual-facts` judgment (total-token median −27.25%, then a metric that included Jev), under the current definition that excludes Jev from the primary metric, with the model changed to `claude-opus-5-5` and the task changed to `chess-bugfix` (to align with the Codex-side verification).

**Conditions**: `claude-opus-5-5`/`medium`, `--tasks chess-bugfix --modes off,on --reps 6` (alternating order). `--claude-clear` is not specified (measuring advise alone; the bench automatically sets `JEV_CLAUDE_ADVISE=on` when `agent=claude` and `mode=on`). The primary metric is the median of `100×(baseline−intervention)/baseline` for upstream input (including cache) plus upstream output. Jev's usage is reported separately.

**Comparability judgment**: apply the existing bench incomparability judgments (missing data, cross-check failure, unverified child session) as-is. If comparable pairs fall short of 6, add more pairs under the same conditions per the pre-registration and stop at 6 comparable (total pair cap 10). If still short at the cap, the judgment is indeterminate.

**Adoption**: the adoption criterion is no quality degradation, 6 comparable pairs, median reduction rate ≥5%.

#### `gpt-6-luna` pre-registered 6 pairs: results and judgment (2026-09-26)

Saved at `/tmp/jev-codex-steer-luna/`. All 12 runs completed. Judged using the raw token amounts of upstream (input+output, excluding Jev).

| Pair | Upstream total baseline→intervention | Reduction rate | Request count baseline→intervention |
|---|---|---:|---|
| 1 | 828,942→741,982 | +10.49% | 22→20 |
| 2 | 741,339→962,025 | −29.77% | 21→28 |
| 3 | 464,183→794,824 | −71.23% | 15→23 |
| 4 | 342,485→830,948 | −142.62% | 12→22 |
| 5 | 498,165→649,238 | −30.33% | 16→19 |
| 6 | 496,289→1,530,997 | −208.49% | 16→36 |

**The median reduction rate is −50.78% (1 improved, 5 worsened). Not adopted.** As with terra, upstream request count increased, driving up tokens.

#### Claude Code: `claude-opus-5-5` advise re-verification: results and judgment (2026-09-26)

Saved at `/tmp/jev-claude-advise-opus/`. All 12 runs completed, all 6 pairs comparable (missing data, cross-check failure, unverified child session, and contamination all 0). Quality was full marks (36/36) on all runs.

| Pair | Upstream total baseline→intervention | Reduction rate | Request count baseline→intervention |
|---|---|---:|---|
| 1 | 127,044→146,263 | −15.13% | 8→9 |
| 2 | 134,787→109,893 | +18.47% | 8→7 |
| 3 | 94,741→133,438 | −40.85% | 6→8 |
| 4 | 140,989→90,229 | +36.00% | 8→6 |
| 5 | 94,027→71,276 | +24.20% | 6→5 |
| 6 | 110,297→128,984 | −16.94% | 7→8 |

**The median reduction rate is +1.67% (3 improved, 3 worsened). Falls short of the adoption criterion (≥5%); not adopted.** Jev's usage (reported separately) on the intervention side ranged from 30,210 to 69,290 input.

### Final conclusion and project closure (2026-09-26)

Judging that further deep-diving offers no prospect of further effect, the project is closed and the repository archived.

**Jev-based tool selection/steering**: the only case where upstream tokens were reduced while preserving quality was Codex's `gpt-5.6-sol` (pre-registered 6 pairs, median +19.30%). The same approach on `gpt-5.6-terra` gave −37.08% and on `gpt-6-luna` gave −50.78%, both increases instead. Claude Code's advise gave +1.67% (essentially 0) on `claude-opus-5-5`, and the upstream difference was also essentially 0 on `claude-sonnet-5`. The effect depends strongly on the model and is not a general-purpose reduction method.

**Effects that were confirmed (approaches that shrink context)**
- Claude Code: native context editing (`clear_tool_uses_20250919`) plus Jev's clear gate. On runs where clearing occurred, the median same-path net saving was 11.1%. The estimated ceiling over 30 days of real sessions was about 6.3% overall. Default: disabled.
- Codex: deterministic truncation of large tool results. Median +8.19% in the stress test `large-facts` (interval straddles zero). Enabled by default.

**Effects that were not found**: Claude Code's Jev-based `Read` replacement (a ceiling of about +1.4% upstream-only at a threshold of 0.7), Jev-free deterministic synthesis of tool calls (a saving ceiling of 0.28%), Codex's compaction replacement and compaction-threshold change (a real-session ceiling of 0.1-0.3%, threshold comparison −0.75%).

**Measurement lesson**: even under identical conditions, run-to-run total-token differences scatter widely (−271% to +90% for Claude). Pre-registration before seeing results, pair comparisons, and same-path metrics were essential. Jev's usage was excluded from the primary metric because it is cheap.
