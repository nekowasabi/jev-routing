import json
import unittest
from pathlib import Path

from summarize_x_cell import summarize_dir


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


if __name__ == "__main__":
    unittest.main()
