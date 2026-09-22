import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from summarize_x_cell import (
    LIVE_TARGETS,
    MODULE_PROMPT,
    append_xcell_history,
    expected_live_answers,
    expected_module_facts,
    extract_cli_payload,
    live_acceptance,
    live_quality,
    median_report,
    prompt_for,
    summarize_dir,
    xcell_history_record,
)


ROOT = Path(__file__).resolve().parent / "testdata" / "x-cell"


class SummarizeXCell(unittest.TestCase):
    def setUp(self):
        self.out = summarize_dir(ROOT)
        self.by = {c["id"]: c for c in self.out["cases"]}
        self.cmp = {c["id"]: c for c in self.out["comparisons"]}

    def test_dedup_and_no_double_count(self):
        u = self.by["filter-search"]["usage"]
        self.assertEqual(u["input_tokens"]["value"], 140)
        self.assertEqual(u["output_tokens"]["value"], 28)
        self.assertEqual(u["cached_input_tokens"]["value"], 10)
        self.assertNotEqual(u["input_tokens"]["value"], 140 + 10)

    def test_fake_pass_is_not_success(self):
        q = self.by["fake-pass"]["quality"]
        self.assertTrue(q["self_reported_pass"])
        self.assertFalse(q["success"])

    def test_correct_without_marker_is_success(self):
        q = self.by["correct-no-marker"]["quality"]
        self.assertFalse(q["self_reported_pass"])
        self.assertTrue(q["success"])

    def test_missing_usage_not_zero(self):
        u = self.by["usage-missing"]["usage"]
        self.assertTrue(u["input_tokens"]["missing"])
        self.assertIsNone(u["input_tokens"]["value"])

    def test_aligned_comparison(self):
        c = self.cmp["filter-vs-forced"]
        self.assertTrue(c["valid"])
        self.assertTrue(c["quality_both_pass"])
        self.assertFalse(c["measured"])
        self.assertEqual(c["wall_ms"]["value"], 300)

    def test_misaligned_invalid(self):
        c = self.cmp["misaligned-cmp"]
        self.assertFalse(c["valid"])
        self.assertIn("settings", c["reason"])

    def test_cost_missing_without_prices(self):
        self.assertTrue(self.by["filter-search"]["cost_usd"]["missing"])
        self.assertTrue(self.cmp["missing-usage-cmp"]["cost_usd"]["missing"])

    def test_not_live(self):
        self.assertFalse(self.out["measured_live"])

    def test_live_requires_applied_selection_and_compaction(self):
        baseline = {"mode": "baseline", "exit_code": 0, "quality": {"success": True}}
        jev = {**baseline, "mode": "jev", "proxy_stats": {
            "selectionApplied": 1, "compactionApplied": 1, "compaction": "on",
            "events": [{"apply": "filter", "compactApplied": True,
                        "upstreamStatus": 200, "upstreamFinish": "complete"}]}}
        self.assertTrue(live_acceptance(baseline, jev)["valid"])

    def test_local_lookup_answer_does_not_require_history_compaction(self):
        baseline = {"mode": "baseline", "exit_code": 0, "quality": {"success": True}}
        jev = {**baseline, "mode": "hybrid", "proxy_stats": {
            "selectionApplied": 1, "compactionApplied": 0, "compaction": "on",
            "events": [{"apply": "filter", "reason": "local_lookup", "compactApplied": False,
                        "upstreamStatus": 200, "upstreamFinish": "complete"}]}}
        self.assertTrue(live_acceptance(baseline, jev)["valid"])

    def test_live_requires_successful_modified_requests(self):
        baseline = {"mode": "baseline", "exit_code": 0, "quality": {"success": True}}
        completed = {"apply": "filter", "compactApplied": True,
                     "upstreamStatus": 200, "upstreamFinish": "complete"}
        unmodified = {**completed, "apply": "none", "compactApplied": False}
        for events in ([], [{**completed, "upstreamStatus": 400}, unmodified],
                       [{**completed, "upstreamStatus": None}],
                       [{**completed, "upstreamFinish": "error"}],
                       [{**completed, "canceled": True}],
                       [{**completed, "apply": "direct", "upstreamFinish": "direct"}],
                       [{**completed, "compactApplied": False}],
                       [{**completed, "apply": "none"}]):
            jev = {**baseline, "mode": "jev", "proxy_stats": {
                "selectionApplied": 1, "compactionApplied": 1, "compaction": "on", "events": events}}
            self.assertFalse(live_acceptance(baseline, jev)["valid"], events)
        jev["proxy_stats"]["events"] = [
            {**completed, "compactApplied": False}, {**completed, "apply": "none"}]
        self.assertTrue(live_acceptance(baseline, jev)["valid"])
        for field in ("selectionApplied", "compactionApplied"):
            bad = {**jev, "proxy_stats": {**jev["proxy_stats"], field: 0}}
            self.assertFalse(live_acceptance(baseline, bad)["valid"])
        for case in ({**jev, "exit_code": 1}, {**jev, "quality": {"success": False}},
                     {**jev, "proxy_stats": {"rewritten": 9, "jevHTTP": 9}}):
            self.assertFalse(live_acceptance(baseline, case)["valid"])
        jev["proxy_stats"].update(compaction="off", compactionApplied=0)
        self.assertTrue(live_acceptance(baseline, jev)["valid"])

    def test_live_external_answers_and_dirty_worktree(self):
        with tempfile.TemporaryDirectory() as tmp:
            worktree = Path(tmp)
            subprocess.run(["git", "init", "-q", tmp], check=True)
            expected = {"RewriteWith": "internal/proxy/rewrite.go:88"}
            case = {"exit_code": 0, "success_marker": True}
            self.assertTrue(live_quality(case, json.dumps(expected), worktree, expected)["success"])
            block = "```json\n" + json.dumps(expected) + "\n```"
            self.assertTrue(live_quality(case, "定義を確認しました。\n" + block + "\n以上です。", worktree, expected)["success"])
            grok_preamble = "リポジトリを検索します。\n🧠 **KB知見を注入済みです。**\n\n" + json.dumps(expected)
            self.assertTrue(live_quality(case, grok_preamble, worktree, expected)["success"])
            for answer in ("CHECK: PASS", "{}", "[]", '{"RewriteWith":"wrong:1"}',
                           block + "\n" + block, "```json\ninvalid\n```",
                           block + "\n```text\n別の回答\n```",
                           json.dumps({**expected, "extra": "wrong:1"})):
                self.assertFalse(live_quality(case, answer, worktree, expected)["success"])
            (worktree / "unexpected").write_text("changed")
            self.assertFalse(live_quality(case, json.dumps(expected), worktree, expected)["success"])

    def test_grok_json_text_payload_yields_inner_answer(self):
        expected = {"RewriteWith": "internal/proxy/rewrite.go:88"}
        # Keys-only envelope from a real grok --output-format json payload.
        payload = {
            "text": "リポジトリを検索します。\n🧠 **KB知見を注入済みです。**\n\n" + json.dumps(expected),
            "stopReason": "end_turn",
            "sessionId": "s",
            "requestId": "r",
            "thought": "",
            "usage": {"inputTokens": 1, "outputTokens": 1},
            "num_turns": 1,
            "total_cost_usd": 0,
            "total_cost_usd_ticks": 0,
            "modelUsage": {},
        }
        data, result = extract_cli_payload("grok", json.dumps(payload))
        self.assertEqual(data.get("stopReason"), "end_turn")
        self.assertNotIn("result", data)
        with tempfile.TemporaryDirectory() as tmp:
            worktree = Path(tmp)
            subprocess.run(["git", "init", "-q", tmp], check=True)
            self.assertTrue(live_quality({"exit_code": 0}, result, worktree, expected)["success"])

    @unittest.skipUnless(shutil.which("jq"), "live harness requires jq")
    def test_history_appends_compact_rows(self):
        with tempfile.TemporaryDirectory() as tmp:
            host = Path(tmp) / "claude"
            (host / "baseline").mkdir(parents=True)
            (host / "hybrid").mkdir()
            baseline = {
                "host": "claude", "mode": "baseline", "commit": "abc", "exit_code": 0,
                "wall_ms": 10.5, "usage": {"input_tokens": 1, "output_tokens": 2},
                "quality": {"success": True}, "events": [{"huge": True}],
                "total_cost_usd": 0.1, "duration_api_ms": 3, "num_turns": 4,
            }
            hybrid = {
                **baseline, "mode": "hybrid", "wall_ms": 8, "rewritten": 2,
                "routing_chars_before": 100, "routing_chars_after": 40,
                "proxy_stats": {"requests": 2, "events": [{"x": 1}], "compaction": "on"},
            }
            (host / "baseline" / "result.json").write_text(json.dumps(baseline))
            (host / "hybrid" / "result.json").write_text(json.dumps(hybrid))
            (host / "comparison.json").write_text(json.dumps({
                "host": "claude", "comparable": True, "valid": True, "invalid_reason": None,
                "reduction": {"wall_ms": 2.5}, "baseline": baseline, "jev": hybrid,
            }))
            record = xcell_history_record(
                host, run_id="r1", benchmark=False, recorded_at="2026-09-22T00:00:00+00:00",
            )
            self.assertEqual(record["commit"], "abc")
            self.assertIsNone(record["modes"]["baseline"]["usage"]["cached_input_tokens"])
            self.assertEqual(record["modes"]["hybrid"]["proxy"], {"requests": 2, "compaction": "on"})
            self.assertNotIn("events", json.dumps(record["modes"]))
            self.assertEqual(record["reduction"]["wall_ms"], 2.5)
            log = Path(tmp) / "history.jsonl"
            append_xcell_history(log, record)
            append_xcell_history(log, {**record, "run_id": "r2", "comparable": False, "reduction": None})
            lines = [json.loads(line) for line in log.read_text().splitlines()]
            self.assertEqual([line["run_id"] for line in lines], ["r1", "r2"])
            self.assertIsNone(lines[1]["reduction"])

    def test_module_facts_count_direct_requires(self):
        self.assertNotIn("定義を調べ", MODULE_PROMPT)
        self.assertNotIn("並列化せず", MODULE_PROMPT)
        self.assertNotIn("検索", MODULE_PROMPT)
        self.assertEqual(prompt_for("module"), MODULE_PROMPT)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "go.mod").write_text(
                "module example.com/app\n\ngo 1.22.0\n\n"
                "require golang.org/x/net v1.2.3\n\n"
                "require (\n\texample.com/direct v1.0.0\n\texample.com/skip v0.1.0 // indirect\n)\n"
            )
            self.assertEqual(expected_module_facts(root), {
                "module": "example.com/app",
                "go": "1.22.0",
                "direct_requires": "2",
            })
        repo = Path(__file__).resolve().parents[1]
        live = expected_module_facts(repo)
        self.assertEqual(live["module"], "github.com/nekowasabi/jev-routing")
        self.assertEqual(live["direct_requires"], "1")

    def test_repeat_median_requires_three_quality_successes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for index, saved in enumerate((10, -5, 30), start=1):
                rep = root / f"r{index:02d}" / "claude"
                for mode, wall in (("baseline", 100), ("hybrid", 100 - saved)):
                    mode_dir = rep / mode
                    mode_dir.mkdir(parents=True)
                    (mode_dir / "result.json").write_text(json.dumps({
                        "wall_ms": wall,
                        "usage": {"output_tokens": 50 if mode == "baseline" else 40,
                                  "input_tokens": 20 if mode == "baseline" else 12},
                        "quality": {"success": True},
                    }))
                (rep / "comparison.json").write_text(json.dumps({"comparable": False}))
            report = median_report(root, "module")
            slot = report["hosts"]["claude"]
            self.assertEqual(slot["samples"], 3)
            self.assertEqual(slot["median_wall_ms_saved"], 10)
            self.assertEqual(slot["median_output_tokens_saved"], 10)
            self.assertTrue(slot["improved"])

    def test_live_harness_exit_tracks_acceptance(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            scripts = root / "scripts"
            scripts.mkdir()
            for name in ("test-x-cell.sh", "summarize_x_cell.py"):
                shutil.copy(Path(__file__).parent / name, scripts / name)
            for name, filename in LIVE_TARGETS.items():
                path = root / filename
                path.parent.mkdir(parents=True, exist_ok=True)
                with path.open("a") as out:
                    out.write(f"func {name}() {{}}\n")
            subprocess.run(["git", "init", "-q", tmp], check=True)
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                            "commit", "-qm", "fixture"], cwd=root, check=True)
            expected = expected_live_answers(root)
            fakebin = root / "fakebin"
            fakebin.mkdir()
            # Correct answers and application counters cannot override a failed child.
            commands = {
                "claude": "#!/bin/sh\nif [ -n \"${JEV_RUN_STATS:-}\" ]; then printf '%s\\n' '{\"requests\":1,\"selectionApplied\":1,\"compactionApplied\":1,\"compaction\":\"on\",\"events\":[{\"apply\":\"filter\",\"compactApplied\":true,\"upstreamStatus\":200,\"upstreamFinish\":\"complete\"}]}' >\"$JEV_RUN_STATS\"; fi\nprintf '%s\\n' '" + json.dumps({"result": json.dumps(expected)}) + "'\nexit \"$FAKE_EXIT\"\n",
                "go": "#!/bin/sh\nmkdir -p \"$(dirname \"$3\")\"\nprintf '%s\\n' '#!/bin/sh' 'shift; cli=$1; shift; shift; exec \"$cli\" \"$@\"' >\"$3\"\nchmod +x \"$3\"\n",
                "codex": f"#!{sys.executable}\n" + "import json, os, sys\nfrom pathlib import Path\n"
                    + "with open(os.environ['ARGV_LOG'], 'a') as out: out.write(json.dumps(sys.argv[1:]) + '\\n')\n"
                    + "if os.environ.get('JEV_RUN_STATS'): assert os.environ.get('JEV_REASONING') == 'legacy'\n"
                    + "stats = {'reasoning': os.environ.get('JEV_REASONING'), 'selectionApplied': 1, 'compactionApplied': 1, 'compaction': 'on', 'events': [{'apply': 'filter', 'compactApplied': True, 'upstreamStatus': 200, 'upstreamFinish': 'complete'}]}\n"
                    + "if os.environ.get('JEV_RUN_STATS'): Path(os.environ['JEV_RUN_STATS']).write_text(json.dumps(stats))\n"
                    + "print(json.dumps({'type': 'item.completed', 'item': {'type': 'agent_message', 'text': " + repr(json.dumps(expected)) + "}}))\n"
                    + "print(json.dumps({'type': 'turn.completed', 'usage': {}}))\nsys.exit(int(os.environ['FAKE_EXIT']))\n",
            }
            for name, code in commands.items():
                path = fakebin / name
                path.write_text(code)
                path.chmod(0o755)
            home = root / "home"
            history = home / ".local" / "state" / "jev-routing" / "x-cell.jsonl"
            base_env = {key: value for key, value in os.environ.items() if key != "JEV_XCELL_LOG"}
            seen = 0
            for host, exit_code in (("claude", 7), ("claude", 0), ("codex", 7), ("codex", 0)):
                proc = subprocess.run(["bash", str(scripts / "test-x-cell.sh"), host], cwd=root,
                                      env={**base_env, "HOME": str(home),
                                           "PATH": str(fakebin) + os.pathsep + os.environ["PATH"],
                                           "FAKE_EXIT": str(exit_code), "CODEX_MODEL": "gpt-5.6-terra",
                                           "JEV_REASONING": "legacy",
                                           "ARGV_LOG": str(root / "codex-argv.jsonl")}, capture_output=True, text=True)
                self.assertEqual(proc.returncode, int(exit_code != 0), proc.stdout + proc.stderr)
                self.assertIn("結果: ", proc.stdout, proc.stdout + proc.stderr)
                run_path = Path(proc.stdout.split("結果: ", 1)[1].strip())
                data = json.loads((run_path / host / "comparison.json").read_text())
                self.assertEqual(data["valid"], exit_code == 0)
                self.assertEqual(data["baseline"]["exit_code"], exit_code)
                self.assertEqual(data["jev"]["events"], [{"apply": "filter", "compactApplied": True, "upstreamStatus": 200, "upstreamFinish": "complete"}])
                if exit_code:
                    self.assertIsNone(data["reduction"])
                if host == "codex":
                    for mode in ("baseline", "jev"):
                        self.assertEqual(data[mode]["model"], "gpt-5.6-terra")
                        self.assertEqual(data[mode]["effort"], "low")
                        self.assertEqual(data[mode]["routing_reasoning"], "legacy" if mode == "jev" else None)
                seen += 1
                self.assertIn(f"履歴: {history}", proc.stdout, proc.stdout + proc.stderr)
                rows = [json.loads(line) for line in history.read_text().splitlines()]
                self.assertEqual(len(rows), seen)
                self.assertEqual(rows[-1]["host"], host)
                self.assertEqual(rows[-1]["run_id"], run_path.name)
                self.assertEqual(rows[-1]["comparable"], exit_code == 0)
                self.assertEqual(rows[-1]["modes"]["baseline"]["exit_code"], exit_code)
                self.assertIn("hybrid", rows[-1]["modes"])
                if exit_code:
                    self.assertIsNone(rows[-1]["reduction"])
            calls = [json.loads(line) for line in (root / "codex-argv.jsonl").read_text().splitlines()]
            self.assertEqual(len(calls), 4)
            for args in calls:
                self.assertEqual(args[args.index("--model") + 1], "gpt-5.6-terra")
                self.assertEqual(args[args.index("-c") + 1], 'model_reasoning_effort="low"')


if __name__ == "__main__":
    unittest.main()
