#!/usr/bin/env python3
"""Validate and aggregate the fixed tool-selection benchmark contract."""

from __future__ import annotations

import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any


MODES = {"baseline", "local", "jev", "hybrid"}


def load(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def validate_cases(data: dict[str, Any]) -> list[dict[str, Any]]:
    out = []
    seen = set()
    for case in data.get("cases", []):
        ident = case.get("id")
        catalog = case.get("catalog")
        labels = case.get("correct_tools")
        if not isinstance(ident, str) or not ident or ident in seen:
            raise ValueError("case id must be unique")
        seen.add(ident)
        if not isinstance(case.get("host"), str) or not case["host"]:
            raise ValueError(f"{ident}: host is required")
        if not isinstance(catalog, list) or not catalog or any(not isinstance(x, str) or not x for x in catalog) or len(set(catalog)) != len(catalog):
            raise ValueError(f"{ident}: catalog must be a unique non-empty name list")
        if not isinstance(labels, list) or not labels or any(x not in catalog for x in labels):
            raise ValueError(f"{ident}: correct_tools must be non-empty catalog members")
        if not isinstance(case.get("external_success"), str) or not case["external_success"]:
            raise ValueError(f"{ident}: external_success is required")
        out.append(case)
    if not out:
        raise ValueError("cases are required")
    return out


def missing(reason: str) -> dict[str, Any]:
    return {"value": None, "missing": True, "reason": reason}


def summarize(root: Path) -> dict[str, Any]:
    data = load(root / "input.json")
    cases = validate_cases(data)
    known = {case["id"]: case for case in cases}
    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for run in data.get("runs", []):
        if run.get("case_id") not in known:
            raise ValueError("run references unknown case")
        if run.get("mode") not in MODES:
            raise ValueError("run has invalid mode")
        grouped[run["mode"]].append(run)

    modes = {}
    for mode in sorted(MODES):
        runs = grouped[mode]
        eligible = [r for r in runs if r.get("reachable") and r.get("rewrite_supported", True)]
        retained = [r for r in eligible if set(r.get("selected_tools", [])) & set(known[r["case_id"]]["correct_tools"])]
        quality = [r for r in eligible if r.get("external_pass") is True]
        selection_tokens = [r.get("selection_tokens") for r in eligible]
        modes[mode] = {
            "runs": len(runs), "denominator": len(eligible),
            "correct_tools_retained": len(retained),
            "retention_rate": None if not eligible else len(retained) / len(eligible),
            "external_success_rate": None if not eligible else len(quality) / len(eligible),
            "wall_ms": [r.get("wall_ms") for r in eligible],
            "upstream_tokens": [r.get("upstream_tokens") for r in eligible],
            "selection_tokens": missing("not_reported") if any(v is None for v in selection_tokens) else {"value": sum(selection_tokens), "missing": False},
            "excluded": [{"case_id": r["case_id"], "reason": r.get("exclude_reason", "unreachable_or_unsupported")} for r in runs if r not in eligible],
        }
    return {"cases": len(cases), "modes": modes, "contract_valid": True}


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: summarize_selection_benchmark.py <fixture-dir>", file=sys.stderr)
        return 2
    json.dump(summarize(Path(argv[1])), sys.stdout, ensure_ascii=False, indent=2)
    print()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
