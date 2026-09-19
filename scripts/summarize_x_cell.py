#!/usr/bin/env python3
"""Summarize saved x-cell observations. Standard library only. No network."""

from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


LIVE_TARGETS = {
    "RewriteWith": "internal/proxy/rewrite.go",
    "extractTools": "internal/proxy/rewrite.go",
    "applyCompactToMessages": "internal/proxy/rewrite.go",
    "DefaultOptions": "internal/proxy/options.go",
    "DefaultUpstream": "internal/proxy/proxy.go",
}


def expected_live_answers(worktree: Path) -> dict[str, str]:
    answers = {}
    for name, filename in LIVE_TARGETS.items():
        for line, text in enumerate((worktree / filename).read_text().splitlines(), 1):
            if re.match(r"^func " + re.escape(name) + r"\(", text):
                answers[name] = f"{filename}:{line}"
                break
        else:
            raise ValueError(f"missing definition: {name}")
    return answers


def extract_cli_payload(host: str, raw: str) -> tuple[dict[str, Any], str]:
    """Parse a CLI --output-format json payload. Grok emits `text`; Claude uses `result`."""
    if host == "codex":
        data: dict[str, Any] = {}
        messages: list[str] = []
        for line in raw.splitlines():
            try:
                event = json.loads(line)
            except (json.JSONDecodeError, ValueError):
                continue
            if event.get("type") == "turn.completed":
                data = event
            item = event.get("item") or {}
            if event.get("type") == "item.completed" and item.get("type") == "agent_message":
                messages.append(item.get("text", ""))
        return data, messages[-1] if messages else ""
    try:
        data = json.loads(raw)
    except (json.JSONDecodeError, ValueError):
        return {}, raw if host == "devin" else ""
    result = data.get("result", data.get("text", ""))
    if isinstance(result, dict):
        result = json.dumps(result, ensure_ascii=False)
    elif result is None:
        result = ""
    elif not isinstance(result, str):
        result = str(result)
    if host == "devin" and not result:
        result = raw
    return data, result


def parse_live_answers(result: str, expected: dict[str, str]) -> dict[str, str]:
    """Recover the answer object from a CLI final string. Grok prepends host text."""
    text = result.strip()
    blobs: list[Any] = []
    if "```" in text:
        blocks = re.findall(r"```json[ \t]*\r?\n(.*?)```", text, re.DOTALL)
        if len(blocks) == 1 and text.count("```") == 2:
            blobs.append(blocks[0].strip())
    else:
        blobs.append(text)
        decoder = json.JSONDecoder()
        i = 0
        while True:
            start = text.find("{", i)
            if start < 0:
                break
            try:
                obj, end = decoder.raw_decode(text, start)
            except json.JSONDecodeError:
                i = start + 1
                continue
            blobs.append(obj)
            i = max(end, start + 1)
    for blob in blobs:
        if isinstance(blob, str):
            try:
                blob = json.loads(blob)
            except (ValueError, TypeError):
                continue
        if isinstance(blob, dict) and blob.keys() == expected.keys():
            return {str(k): str(v) for k, v in blob.items()}
    return {}


def live_quality(case: dict[str, Any], result: str, worktree: Path,
                 expected: dict[str, str]) -> dict[str, Any]:
    answers = parse_live_answers(result, expected)
    status = subprocess.run(
        ["git", "status", "--porcelain", "--untracked-files=all"],
        cwd=worktree, capture_output=True, text=True, check=True,
    )
    return external_quality({
        **case, "expected_answers": expected,
        "external_checks": {"worktree_clean": not status.stdout,
                            "answers": answers, "sources": list(expected.values())},
    })


def live_acceptance(baseline: dict[str, Any], jev: dict[str, Any]) -> dict[str, Any]:
    failures = []
    for case in (baseline, jev):
        if case.get("exit_code") != 0 or not case.get("quality", {}).get("success"):
            failures.append(f"{case.get('mode')}: external_quality_failed")
    stats = jev.get("proxy_stats") or {}
    if stats.get("selectionApplied", 0) <= 0:
        failures.append("selection_not_applied")
    completed = [event for event in (stats.get("events") or [])
                 if isinstance(event.get("upstreamStatus"), int)
                 and 200 <= event["upstreamStatus"] < 300
                 and event.get("upstreamFinish") == "complete"
                 and not event.get("canceled")]
    if not any(event.get("apply") in ("filter", "forced") for event in completed):
        failures.append("selection_not_completed_upstream")
    if stats.get("compaction") == "on":
        if stats.get("compactionApplied", 0) <= 0:
            failures.append("compaction_not_applied")
        if not any(event.get("compactApplied") is True and event.get("apply") != "direct"
                   for event in completed):
            failures.append("compaction_not_completed_upstream")
    elif stats.get("compaction") != "off":
        failures.append("compaction_setting_missing")
    return {"valid": not failures, "failures": failures}


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def missing(reason: str) -> dict[str, Any]:
    return {"value": None, "missing": True, "reason": reason}


def observed(value: Any, source: str, scope: str = "event") -> dict[str, Any]:
    return {"value": value, "missing": False, "source": source, "scope": scope}


def add_usage(acc: dict[str, Any], usage: dict[str, Any] | None, source: str) -> None:
    if not usage:
        return
    for key in ("input_tokens", "output_tokens"):
        if usage.get(key) is None:
            continue
        slot = acc.setdefault(key, {"value": 0, "missing": False, "source": source})
        slot["value"] += usage[key]
    # cache/reasoning are subsets; record separately and do not add into totals again
    for key in ("cached_input_tokens", "cache_creation_input_tokens", "reasoning_tokens"):
        if usage.get(key) is None:
            continue
        slot = acc.setdefault(key, {"value": 0, "missing": False, "source": source})
        slot["value"] += usage[key]


def external_quality(case: dict[str, Any]) -> dict[str, Any]:
    exit_code = case.get("exit_code")
    checks = case.get("external_checks") or {}
    worktree_clean = checks.get("worktree_clean")
    answers = checks.get("answers") or {}
    expected = case.get("expected_answers") or {}
    matched = bool(expected) and all(answers.get(k) == v for k, v in expected.items())
    sources_ok = all(bool(s) for s in (checks.get("sources") or expected and [True] or []))
    success = exit_code == 0 and worktree_clean is True and matched and sources_ok
    return {
        "success": success,
        "exit_code": exit_code,
        "worktree_clean": worktree_clean,
        "answers_matched": matched,
        "self_reported_pass": bool(case.get("success_marker")),
        "used_self_report": False,
    }


def summarize_case(case: dict[str, Any]) -> dict[str, Any]:
    usage_acc: dict[str, Any] = {}
    events = case.get("events") or []
    seen_ids: set[str] = set()
    for ev in events:
        eid = ev.get("id")
        if eid and eid in seen_ids:
            continue
        if eid:
            seen_ids.add(eid)
        add_usage(usage_acc, ev.get("usage"), "event")
    if not events and case.get("usage"):
        add_usage(usage_acc, case.get("usage"), "host_aggregate")
    for key in ("input_tokens", "output_tokens", "cached_input_tokens", "reasoning_tokens"):
        usage_acc.setdefault(key, missing("not_reported"))

    cost = case.get("total_cost_usd")
    jev_cost = case.get("jev_cost_usd")
    if case.get("prices"):
        # only when explicit unit prices and matching usage exist
        prices = case["prices"]
        if usage_acc["input_tokens"]["missing"] or usage_acc["output_tokens"]["missing"]:
            cost_obs = missing("usage_incomplete_for_price")
        else:
            cost_obs = observed(
                usage_acc["input_tokens"]["value"] * prices.get("input", 0)
                + usage_acc["output_tokens"]["value"] * prices.get("output", 0),
                "priced",
            )
    elif cost is None:
        cost_obs = missing("no_price_table")
    else:
        cost_obs = observed(cost, "host")

    parent_child = case.get("parent_child")
    if parent_child is None:
        family = missing("parent_child_unverified")
    else:
        family = observed(parent_child, "provided")

    return {
        "id": case.get("id"),
        "host": case.get("host"),
        "mode": case.get("mode"),
        "settings": case.get("settings") or {},
        "source_fingerprint": case.get("source_fingerprint"),
        "usage": usage_acc,
        "cost_usd": cost_obs,
        "jev_cost_usd": missing("not_observed") if jev_cost is None else observed(jev_cost, "host"),
        "wall_ms": observed(case["wall_ms"], "timer") if case.get("wall_ms") is not None else missing("no_timer"),
        "parent_child": family,
        "quality": external_quality(case),
        "retries": case.get("retries") or [],
        "optimization_applied": bool(case.get("optimization_applied")),
        "observed_scope": case.get("observed_scope") or "single-process",
    }


def comparable(a: dict[str, Any], b: dict[str, Any]) -> tuple[bool, str | None]:
    keys = ("compaction", "reasoning", "model", "task_id")
    sa, sb = a.get("settings") or {}, b.get("settings") or {}
    for k in keys:
        if sa.get(k) != sb.get(k):
            return False, f"settings.{k}"
    if a.get("source_fingerprint") and b.get("source_fingerprint"):
        if a["source_fingerprint"] != b["source_fingerprint"] and sa.get("allow_source_diff"):
            pass
        elif a["source_fingerprint"] != b["source_fingerprint"] and a.get("mode") == b.get("mode"):
            return False, "source_fingerprint"
    return True, None


def delta(a: dict[str, Any], b: dict[str, Any], field: str) -> dict[str, Any]:
    left, right = a.get(field), b.get(field)
    if not isinstance(left, dict) or not isinstance(right, dict):
        return missing("not_numeric")
    if left.get("missing") or right.get("missing") or left.get("value") is None or right.get("value") is None:
        return missing("one_or_both_missing")
    return observed(left["value"] - right["value"], "derived")


def summarize_dir(root: Path) -> dict[str, Any]:
    data = load_json(root / "input.json")
    cases = [summarize_case(c) for c in data.get("cases", [])]
    by_id = {c["id"]: c for c in cases}
    comparisons = []
    for spec in data.get("comparisons", []):
        left, right = by_id.get(spec["left"]), by_id.get(spec["right"])
        if left is None or right is None:
            comparisons.append({"id": spec.get("id"), "valid": False, "reason": "missing_case"})
            continue
        ok, why = comparable(left, right)
        q_ok = left["quality"]["success"] and right["quality"]["success"]
        comparisons.append({
            "id": spec.get("id"),
            "valid": ok,
            "reason": why,
            "quality_both_pass": q_ok,
            "performance_claim": q_ok and ok,
            "wall_ms": delta(left, right, "wall_ms"),
            "cost_usd": delta(left, right, "cost_usd"),
            "measured": False,
        })
    return {
        "cases": cases,
        "comparisons": comparisons,
        "scope": "saved-fixtures",
        "measured_live": False,
    }


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("usage: summarize_x_cell.py <fixture-dir>", file=sys.stderr)
        return 2
    out = summarize_dir(Path(argv[1]))
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
