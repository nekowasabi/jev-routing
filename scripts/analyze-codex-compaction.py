#!/usr/bin/env python3
"""Summarize Codex context-compaction frequency and cost from local rollout logs.

Sibling of analyze-codex-sessions.py: same data source (~/.codex/sessions),
same "no body/filename/credential in output" rule, same self-verification
style (assert that response-level totals reconcile with the session's own
running counters). This script additionally walks each session's `compacted`
records and the token_usage_record / response_item entries immediately
around them to measure compaction frequency and upstream cost.

Identification notes (see README-less report for caveats):
  - "pre-compaction request" = the token_usage_record nearest before the
    `compacted` line in the same file (causal-order assumption; high
    confidence, no thread/response id to cross-check beyond monotonic order).
  - "compaction request itself" = payload["latest_token_usage_record"]["usage"]
    when the host tags it explicitly (exact/high confidence); otherwise we
    fall back to the same pre-compaction request as a heuristic (labeled
    "heuristic" in output), and to null when the file has no token_usage_record
    at all (older/alternate rollout format; labeled "missing").
  - "host summary length" = payload["message"] when non-empty (plaintext,
    seen in some historical sessions), else the trailing replacement_history
    item of type "compaction" is checked -- but that item only ever carries
    an encrypted_content blob in this corpus, so its plaintext length is
    unrecoverable (labeled "missing: encrypted_only"). When neither is
    present the format simply has no isolable summary record (labeled
    "missing: not_isolable").
"""

import argparse
import collections
import hashlib
import json
import pathlib
import statistics


def median(values):
    return statistics.median(values) if values else 0


def pct(values, p):
    if not values:
        return 0
    values = sorted(values)
    k = min(len(values) - 1, int(round(p * (len(values) - 1))))
    return values[k]


FILE_OR_COMMAND_KEYS = ("path", "file_path", "command", "cmd", "file", "files")


def call_signature(name, raw_args):
    """Hash a (tool name, args) pair; only for calls that look like a file/command op."""
    args = raw_args
    if isinstance(args, str):
        try:
            args = json.loads(args)
        except (TypeError, ValueError):
            args = None
    if not isinstance(args, dict) or not any(k in args for k in FILE_OR_COMMAND_KEYS):
        return None
    normalized = f"{name}|{json.dumps(args, sort_keys=True, ensure_ascii=False)}"
    return hashlib.sha256(normalized.encode()).hexdigest()


def content_chars(content):
    if isinstance(content, str):
        return len(content)
    total = 0
    if isinstance(content, list):
        for part in content:
            if isinstance(part, dict) and isinstance(part.get("text"), str):
                total += len(part["text"])
    return total


def history_chars(replacement_history):
    return sum(content_chars(item.get("content")) for item in (replacement_history or []) if isinstance(item, dict))


def output_len(output):
    if output is None:
        return 0
    return len(output) if isinstance(output, str) else len(json.dumps(output, ensure_ascii=False))


def summarize(path, match_stats):
    source = None
    models = set()
    efforts = set()
    current_model = current_effort = None
    context_window = 0
    response_ids = set()
    usage_count = input_tokens = cached_tokens = output_tokens = 0
    last_thread_input = None
    last_tur = None  # (response_id, usage dict) -- nearest preceding token_usage_record

    seen_signatures = set()
    pending_reacq_call_ids = {}  # call_id -> compaction dict awaiting its output length
    open_compaction = None
    compactions = []

    with path.open(errors="replace") as stream:
        for line in stream:
            record = json.loads(line)
            payload = record.get("payload") or {}
            kind = record.get("type")

            if kind == "session_meta" and source is None:
                # first session_meta wins: a subagent thread can be re-stamped
                # thread_source="user" later in the same file if control is
                # handed to the interactive user, but the file's origin is
                # still the subagent fork (forked_from_id/agent_nickname).
                source = payload.get("source")

            elif kind == "turn_context":
                if payload.get("model"):
                    current_model = payload["model"]
                    models.add(current_model)
                if payload.get("effort"):
                    current_effort = payload["effort"]
                    efforts.add(current_effort)

            elif kind == "event_msg":
                info = payload.get("info") or {}
                context_window = info.get("model_context_window") or context_window

            elif kind == "response_item":
                item_type = payload.get("type")
                if item_type in ("function_call", "custom_tool_call"):
                    call_id = payload.get("call_id")
                    sig = call_signature(payload.get("name"), payload.get("arguments") if payload.get("arguments") is not None else payload.get("input"))
                    if sig is not None:
                        if open_compaction is not None and sig in open_compaction["_pre_signatures"]:
                            open_compaction["reacquisitions"] += 1
                            if call_id:
                                pending_reacq_call_ids[call_id] = open_compaction
                        seen_signatures.add(sig)
                elif item_type in ("function_call_output", "custom_tool_call_output"):
                    call_id = payload.get("call_id")
                    target = pending_reacq_call_ids.pop(call_id, None)
                    if target is not None:
                        target["reacquisition_output_chars"] += output_len(payload.get("output"))

            elif kind == "token_usage_record" and isinstance(payload.get("usage"), dict):
                response_id = payload.get("response_id")
                assert response_id not in response_ids, f"duplicate response in {path.name}"
                response_ids.add(response_id)
                usage = payload["usage"]
                current_input = usage.get("input_tokens") or 0
                current_cached = usage.get("cached_input_tokens") or 0
                assert 0 <= current_cached <= current_input, f"invalid cache usage in {path.name}"
                input_tokens += current_input
                cached_tokens += current_cached
                output_tokens += usage.get("output_tokens") or 0
                usage_count += 1
                last_thread_input = (payload.get("thread_token_usage") or {}).get("input_tokens")

                if open_compaction is not None and open_compaction["requests_after"] == 0 and open_compaction["first_after"] is None:
                    open_compaction["first_after"] = dict(input=current_input, cached=current_cached)
                if open_compaction is not None:
                    open_compaction["requests_after"] += 1

                last_tur = (response_id, usage)

            elif kind == "compacted":
                pre_input = pre_cached = pre_context_ratio = None
                if last_tur is not None:
                    _, pre_usage = last_tur
                    pre_input = pre_usage.get("input_tokens") or 0
                    pre_cached = pre_usage.get("cached_input_tokens") or 0
                    if context_window:
                        pre_context_ratio = round(pre_input / context_window, 4)

                latest_tur = payload.get("latest_token_usage_record")
                comp_method = comp_usage = None
                if isinstance(latest_tur, dict) and isinstance(latest_tur.get("usage"), dict):
                    u = latest_tur["usage"]
                    comp_usage = dict(input=u.get("input_tokens") or 0, cached=u.get("cached_input_tokens") or 0, output=u.get("output_tokens") or 0)
                    comp_method = "exact:latest_token_usage_record"
                    if last_tur is not None and last_tur[0] == latest_tur.get("response_id"):
                        match_stats["match"] += 1
                    else:
                        match_stats["mismatch"] += 1
                elif last_tur is not None:
                    _, u = last_tur
                    comp_usage = dict(input=u.get("input_tokens") or 0, cached=u.get("cached_input_tokens") or 0, output=u.get("output_tokens") or 0)
                    comp_method = "heuristic:nearest_preceding_tur"
                else:
                    comp_method = "missing:no_token_usage_record"

                message = payload.get("message") or ""
                rh = payload.get("replacement_history") or []
                summary_chars = summary_method = None
                if message:
                    summary_chars = len(message)
                    summary_method = "message_field"
                elif rh and isinstance(rh[-1], dict) and rh[-1].get("type") == "compaction":
                    summary_method = "missing:encrypted_only"
                else:
                    summary_method = "missing:not_isolable"

                comp = dict(
                    model=current_model, effort=current_effort,
                    pre_input=pre_input, pre_cached=pre_cached, context_window=context_window or None, pre_context_ratio=pre_context_ratio,
                    comp_method=comp_method, comp_usage=comp_usage,
                    summary_method=summary_method, summary_chars=summary_chars,
                    replacement_history_chars=history_chars(rh),
                    requests_after=0, first_after=None,
                    reacquisitions=0, reacquisition_output_chars=0,
                    _pre_signatures=frozenset(seen_signatures),
                )
                compactions.append(comp)
                open_compaction = comp

    if usage_count:
        assert last_thread_input == input_tokens, f"usage mismatch in {path.name}"
    if isinstance(source, dict):
        source = "subagent"
    for comp in compactions:
        del comp["_pre_signatures"]
    return dict(
        source=source or "unknown",
        model=next(iter(models)) if len(models) == 1 else "mixed" if models else "unknown",
        effort=next(iter(efforts)) if len(efforts) == 1 else "mixed" if efforts else "unknown",
        usage_count=usage_count, input=input_tokens, cached=cached_tokens, output=output_tokens,
        compactions=compactions,
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("start", help="first date, YYYY-MM-DD")
    parser.add_argument("end", help="last date, YYYY-MM-DD")
    args = parser.parse_args()
    assert args.start <= args.end

    files = sorted(pathlib.Path.home().joinpath(".codex/sessions").rglob("rollout-*.jsonl"))
    in_range = [p for p in files if args.start <= p.name[8:18] <= args.end]

    match_stats = collections.Counter()
    rows = [summarize(p, match_stats) for p in in_range]
    # self-check: our "compaction request == nearest preceding token_usage_record" identification
    # method must never contradict the host's own explicit tag when both are available.
    assert match_stats["mismatch"] == 0, f"compaction identification mismatch: {match_stats}"

    corpus_groups = {}
    for src in ("cli", "subagent", "exec", "vscode"):
        measured = [r for r in rows if r["source"] == src and r["usage_count"]]
        corpus_groups[src] = dict(
            sessions=len([r for r in rows if r["source"] == src]),
            measured=len(measured),
            input=sum(r["input"] for r in measured),
            output=sum(r["output"] for r in measured),
            cached=sum(r["cached"] for r in measured),
        )
    parent_sub_total_tokens = sum(corpus_groups[s]["input"] + corpus_groups[s]["output"] for s in ("cli", "subagent"))

    all_compactions = [(row["source"], c) for row in rows for c in row["compactions"]]
    sessions_with_compaction = sum(1 for row in rows if row["compactions"])
    per_session_counts = collections.Counter(len(row["compactions"]) for row in rows if row["compactions"])

    identified = [c for _, c in all_compactions if c["comp_usage"] is not None]
    comp_upstream_tokens = sum(c["comp_usage"]["input"] + c["comp_usage"]["output"] for c in identified)
    comp_upstream_input = sum(c["comp_usage"]["input"] for c in identified)
    comp_upstream_cached = sum(c["comp_usage"]["cached"] for c in identified)

    summary_lens = [c["summary_chars"] for _, c in all_compactions if c["summary_chars"] is not None]
    summary_missing_reasons = collections.Counter(c["summary_method"] for _, c in all_compactions if c["summary_chars"] is None)
    summary_x_requests_after = sum(c["summary_chars"] * c["requests_after"] for _, c in all_compactions if c["summary_chars"] is not None)

    hist_lens = [c["replacement_history_chars"] for _, c in all_compactions]
    pre_ratios = [c["pre_context_ratio"] for _, c in all_compactions if c["pre_context_ratio"] is not None]
    requests_after_list = [c["requests_after"] for _, c in all_compactions]
    first_after_input = [c["first_after"]["input"] for _, c in all_compactions if c["first_after"]]
    first_after_cached = [c["first_after"]["cached"] for _, c in all_compactions if c["first_after"]]

    reacq_total = sum(c["reacquisitions"] for _, c in all_compactions)
    reacq_output_chars = sum(c["reacquisition_output_chars"] for _, c in all_compactions)

    # Ceiling estimates over ALL compaction events (not just identified ones), per coordinator request:
    # (a) upstream cost of the compaction request itself; comp_usage already falls back from
    #     "exact" (host-tagged) to "heuristic" (nearest preceding token_usage_record) -- when
    #     neither exists (no token_usage_record at all before the `compacted` line in this file)
    #     the event is simply uncovered, not zero-filled.
    covered_events = len(identified)
    uncovered_events = len(all_compactions) - covered_events
    ceiling_a_tokens = comp_upstream_tokens  # sum of input+output over covered (exact+heuristic) events
    # (b) len(replacement_history)/4 (estimated tokens) x requests served afterward -- this is the
    #     repeated-summary-in-context load; it lands inside cached_input_tokens of later requests.
    ceiling_b_tokens = sum((c["replacement_history_chars"] / 4) * c["requests_after"] for _, c in all_compactions)

    model_effort_at_compaction = collections.Counter((c["model"], c["effort"]) for _, c in all_compactions)
    terra_medium = sum(v for (m, e), v in model_effort_at_compaction.items() if m == "gpt-5.6-terra" and e == "medium")

    result = dict(
        period=[args.start, args.end],
        files_in_period=len(in_range),
        corpus_groups=corpus_groups,
        parent_sub_total_tokens=parent_sub_total_tokens,
        sessions_with_compaction=sessions_with_compaction,
        sessions_with_compaction_fraction=round(sessions_with_compaction / len(in_range), 4) if in_range else 0,
        compaction_events_total=len(all_compactions),
        per_session_compaction_count_distribution=dict(sorted(per_session_counts.items())),
        by_source=dict(collections.Counter(src for src, _ in all_compactions)),
        model_effort_at_compaction=[dict(model=m, effort=e, count=v) for (m, e), v in model_effort_at_compaction.items()],
        gpt_5_6_terra_medium_compactions=terra_medium,
        pre_compaction_input_tokens=dict(p50=median([c["pre_input"] for _, c in all_compactions if c["pre_input"] is not None]),
                                          p90=pct([c["pre_input"] for _, c in all_compactions if c["pre_input"] is not None], 0.9),
                                          max=max([c["pre_input"] for _, c in all_compactions if c["pre_input"] is not None], default=0)),
        pre_compaction_context_ratio=dict(p50=median(pre_ratios), p90=pct(pre_ratios, 0.9), max=max(pre_ratios, default=0)),
        compaction_request_identification=dict(
            exact=sum(1 for c in identified if c["comp_method"] == "exact:latest_token_usage_record"),
            heuristic=sum(1 for c in identified if c["comp_method"] == "heuristic:nearest_preceding_tur"),
            missing=sum(1 for _, c in all_compactions if c["comp_usage"] is None),
        ),
        compaction_upstream_input_tokens=comp_upstream_input,
        compaction_upstream_cached_tokens=comp_upstream_cached,
        compaction_upstream_total_tokens=comp_upstream_tokens,
        compaction_upstream_fraction_of_parent_sub=round(comp_upstream_tokens / parent_sub_total_tokens, 6) if parent_sub_total_tokens else None,
        summary_chars_measured_count=len(summary_lens),
        summary_missing_reasons=dict(summary_missing_reasons),
        summary_chars=dict(p50=median(summary_lens), p90=pct(summary_lens, 0.9), max=max(summary_lens, default=0)) if summary_lens else None,
        summary_tokens_estimate_note="len/4, estimate only" if summary_lens else None,
        summary_x_requests_after_chars=summary_x_requests_after if summary_lens else None,
        summary_x_requests_after_note="upper-bound proxy; this repeated-summary load lands inside cached_input_tokens of later requests, not as separate spend",
        replacement_history_chars=dict(p50=median(hist_lens), p90=pct(hist_lens, 0.9), max=max(hist_lens, default=0)),
        requests_after_compaction=dict(p50=median(requests_after_list), p90=pct(requests_after_list, 0.9), max=max(requests_after_list, default=0)),
        first_request_after_compaction=dict(median_input=median(first_after_input), median_cached=median(first_after_cached)),
        reacquisition_calls_total=reacq_total,
        reacquisition_output_chars_total=reacq_output_chars,
        ceiling_a_compaction_request_upstream_tokens=dict(
            value=ceiling_a_tokens, covered_events=covered_events, uncovered_events=uncovered_events,
            fraction_of_parent_sub=round(ceiling_a_tokens / parent_sub_total_tokens, 6) if parent_sub_total_tokens else None,
            note="exact (host-tagged) + heuristic (nearest preceding token_usage_record) fallback; "
                 "uncovered events have zero token_usage_record before the compacted line in this file, so no approximation input exists",
        ),
        ceiling_b_history_over4_x_requests_after_tokens=dict(
            value=round(ceiling_b_tokens), events_covered=len(all_compactions),
            fraction_of_parent_sub=round(ceiling_b_tokens / parent_sub_total_tokens, 6) if parent_sub_total_tokens else None,
            note="estimate: len/4 per event x requests_after, summed over all compaction events; counts inside cached_input_tokens of later requests, not separate spend",
        ),
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
