#!/usr/bin/env python3
"""Summarize local Codex sessions without printing message contents."""

import argparse
import collections
import hashlib
import json
import pathlib
import statistics


def median(values):
    return statistics.median(values) if values else 0


def summarize(path):
    source = None
    models = set()
    efforts = set()
    first_developer = []
    usage_count = input_tokens = cached_tokens = output_tokens = 0
    first_input = first_cached = max_input = tool_chars = tool_count = 0
    long_tool_chars = long_tool_count = 0
    large_input_resets = 0
    compactions = 0
    previous_input = None
    last_thread_input = None
    context_window = 0
    response_ids = set()

    with path.open(errors="replace") as stream:
        for line in stream:
            record = json.loads(line)
            payload = record.get("payload") or {}
            kind = record.get("type")
            if kind == "session_meta":
                source = payload.get("source")
            elif kind == "compacted":
                compactions += 1
            elif kind == "turn_context":
                if payload.get("model"):
                    models.add(payload["model"])
                if payload.get("effort"):
                    efforts.add(payload["effort"])
            elif kind == "event_msg":
                info = payload.get("info") or {}
                context_window = info.get("model_context_window") or payload.get("model_context_window") or context_window
            elif kind == "response_item":
                item_type = payload.get("type")
                if usage_count == 0 and item_type == "message" and payload.get("role") in ("developer", "system"):
                    for part in payload.get("content") or []:
                        text = part.get("text") if isinstance(part, dict) else None
                        if text:
                            first_developer.append((hashlib.sha256(text.encode()).hexdigest(), len(text)))
                elif item_type in ("custom_tool_call_output", "function_call_output"):
                    output = payload.get("output")
                    length = len(output) if isinstance(output, str) else len(json.dumps(output, ensure_ascii=False)) if output is not None else 0
                    tool_chars += length
                    tool_count += 1
                    if length > 20000:
                        long_tool_chars += length
                        long_tool_count += 1
            elif kind == "token_usage_record" and isinstance(payload.get("usage"), dict):
                response_id = payload.get("response_id")
                assert response_id not in response_ids, f"duplicate response in {path.name}"
                response_ids.add(response_id)
                usage = payload["usage"]
                current_input = usage.get("input_tokens") or 0
                current_cached = usage.get("cached_input_tokens") or 0
                assert 0 <= current_cached <= current_input, f"invalid cache usage in {path.name}"
                if usage_count == 0:
                    first_input, first_cached = current_input, current_cached
                if previous_input is not None and previous_input > 5000 and current_input * 2 <= previous_input:
                    large_input_resets += 1
                previous_input = current_input
                max_input = max(max_input, current_input)
                input_tokens += current_input
                cached_tokens += current_cached
                output_tokens += usage.get("output_tokens") or 0
                usage_count += 1
                last_thread_input = (payload.get("thread_token_usage") or {}).get("input_tokens")

    if usage_count:
        assert last_thread_input == input_tokens, f"usage mismatch in {path.name}"
    if isinstance(source, dict):
        source = "subagent"
    return dict(source=source or "unknown", model=next(iter(models)) if len(models) == 1 else "mixed" if models else "unknown",
                effort=next(iter(efforts)) if len(efforts) == 1 else "mixed" if efforts else "unknown", usage_count=usage_count,
                input=input_tokens, cached=cached_tokens, output=output_tokens,
                first_input=first_input, first_cached=first_cached, max_input=max_input,
                tool_chars=tool_chars, tool_count=tool_count, long_tool_chars=long_tool_chars,
                long_tool_count=long_tool_count, developer=first_developer,
                context_window=context_window, large_input_resets=large_input_resets,
                compactions=compactions)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("start", help="first date, YYYY-MM-DD")
    parser.add_argument("end", help="last date, YYYY-MM-DD")
    args = parser.parse_args()
    assert args.start <= args.end
    files = sorted(pathlib.Path.home().joinpath(".codex/sessions").rglob("rollout-*.jsonl"))
    rows = [summarize(path) for path in files if args.start <= path.name[8:18] <= args.end]
    frequency = collections.Counter(hash_value for row in rows if row["source"] == "cli" and row["usage_count"]
                                    for hash_value, _ in set(row["developer"]))
    groups = {}
    for source in ("cli", "subagent", "exec", "vscode", "cli_terra_medium", "subagent_terra_medium"):
        base_source = source.split("_terra_medium")[0]
        all_rows = [row for row in rows if row["source"] == base_source
                    and ("_terra_medium" not in source or (row["model"], row["effort"]) == ("gpt-5.6-terra", "medium"))]
        measured = [row for row in all_rows if row["usage_count"]]
        total_input = sum(row["input"] for row in measured)
        total_cached = sum(row["cached"] for row in measured)
        groups[source] = dict(
            files=len(all_rows), measured=len(measured), responses=sum(row["usage_count"] for row in measured),
            input=total_input, cached=total_cached, uncached=total_input - total_cached,
            output=sum(row["output"] for row in measured),
            cached_fraction=round(total_cached / total_input, 4) if total_input else 0,
            median_session_cached_fraction=round(median([row["cached"] / row["input"] for row in measured if row["input"]]), 4),
            median_first_input=median([row["first_input"] for row in measured]),
            median_first_cached=median([row["first_cached"] for row in measured]),
            median_developer_chars=median([sum(length for _, length in row["developer"]) for row in measured]),
            median_repeated_developer_chars=(median([sum(length for hash_value, length in row["developer"] if frequency[hash_value] >= 5) for row in measured])
                                             if base_source == "cli" else None),
            tool_outputs=sum(row["tool_count"] for row in measured),
            tool_output_chars=sum(row["tool_chars"] for row in measured),
            tool_outputs_over_20000_chars=sum(row["long_tool_count"] for row in measured),
            tool_output_chars_over_20000=sum(row["long_tool_chars"] for row in measured),
            median_tool_output_chars=median([row["tool_chars"] for row in measured]),
            sessions_with_tool_outputs=sum(row["tool_count"] > 0 for row in measured),
            sessions_above_half_context=sum(row["context_window"] > 0 and row["max_input"] * 2 >= row["context_window"] for row in measured),
            sessions_with_large_input_reset=sum(row["large_input_resets"] > 0 for row in measured),
            compactions=sum(row["compactions"] for row in measured),
            sessions_with_compaction=sum(row["compactions"] > 0 for row in measured),
            models=dict(collections.Counter(row["model"] for row in measured)),
        )
    print(json.dumps(dict(period=[args.start, args.end], files=len(rows), measured=sum(row["usage_count"] > 0 for row in rows), groups=groups), ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
