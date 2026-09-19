#!/usr/bin/env python3
"""Aggregate selection-efficiency comparison fixtures. Stdlib only."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

BASELINE_REV = "f6b9dcfd2d0164e525b6ff981e17e6df26932e29"


def load(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def miss(reason: str) -> dict[str, Any]:
    return {"value": None, "missing": True, "reason": reason}


def summarize(root: Path) -> dict[str, Any]:
    data = load(root / "input.json")
    groups = data.get("groups") or []
    baseline = next((g for g in groups if g.get("role") == "baseline"), None)
    if baseline is None or not baseline.get("revision"):
        baseline_state = {"present": False, "reason": "baseline_revision_missing"}
    elif baseline.get("revision") != data.get("required_baseline_revision", BASELINE_REV):
        baseline_state = {"present": False, "reason": "baseline_revision_mismatch", "got": baseline.get("revision")}
    else:
        baseline_state = {"present": True, "revision": baseline["revision"]}

    out_groups = []
    for g in groups:
        settings = g.get("settings") or {}
        quality = g.get("quality") or {}
        usage = g.get("usage") or {}
        cost = g.get("cost")
        if cost is None or (isinstance(cost, dict) and cost.get("amount") is None):
            cost_obs = miss("cost_unknown")
        else:
            cost_obs = {"value": cost.get("amount") if isinstance(cost, dict) else cost, "missing": False, "unit": (cost or {}).get("unit"), "source": (cost or {}).get("source")}
        failed = g.get("failures") or []
        retries = g.get("retries") or []
        out_groups.append({
            "id": g.get("id"),
            "role": g.get("role"),
            "revision": g.get("revision"),
            "settings": settings,
            "local_ratio": g.get("local_ratio"),
            "jev_calls": g.get("jev_calls"),
            "forced_accepted": g.get("forced_accepted"),
            "direct_rate": g.get("direct_rate"),
            "usage": usage,
            "cost": cost_obs,
            "quality_pass": bool(quality.get("external_pass")),
            "quality_drop": bool(quality.get("drop")),
            "task_kind": g.get("task_kind"),
            "failures_included": len(failed),
            "retries": len(retries),
            "wall_ms": g.get("wall_ms"),
            "denominator_includes_failures": True,
        })

    comparisons = []
    by = {g["id"]: g for g in out_groups}
    for spec in data.get("comparisons") or []:
        left, right = by.get(spec["left"]), by.get(spec["right"])
        if left is None or right is None:
            comparisons.append({"id": spec.get("id"), "comparable": False, "reason": "missing_group"})
            continue
        if not baseline_state["present"] and spec.get("needs_baseline"):
            comparisons.append({"id": spec.get("id"), "comparable": False, "reason": "baseline_missing", "improvement": None})
            continue
        ls, rs = left["settings"], right["settings"]
        for k in ("compaction", "reasoning", "task_id", "history"):
            if ls.get(k) != rs.get(k):
                comparisons.append({"id": spec.get("id"), "comparable": False, "reason": f"settings.{k}", "improvement": None})
                break
        else:
            if left["quality_drop"] or right["quality_drop"] or not left["quality_pass"] or not right["quality_pass"]:
                comparisons.append({"id": spec.get("id"), "comparable": True, "adoptable": False, "reason": "quality", "improvement": None})
                continue
            if left["cost"]["missing"] or right["cost"]["missing"]:
                comparisons.append({"id": spec.get("id"), "comparable": True, "adoptable": False, "reason": "cost_unknown", "improvement": None})
                continue
            comparisons.append({
                "id": spec.get("id"),
                "comparable": True,
                "adoptable": True,
                "improvement": None if spec.get("report_improvement") is False else right["cost"]["value"] - left["cost"]["value"],
                "measured_live": False,
            })
    return {
        "baseline": baseline_state,
        "groups": out_groups,
        "comparisons": comparisons,
        "required_baseline_revision": data.get("required_baseline_revision", BASELINE_REV),
        "measured_live": False,
    }


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("usage: summarize_selection_comparison.py <fixture-dir>", file=sys.stderr)
        return 2
    json.dump(summarize(Path(argv[1])), sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
