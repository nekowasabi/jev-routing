#!/usr/bin/env python3
"""Summarize saved x-cell observations. Standard library only. No network."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any


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
