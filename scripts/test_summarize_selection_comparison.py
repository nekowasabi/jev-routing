import unittest
from pathlib import Path

from summarize_selection_comparison import summarize


ROOT = Path(__file__).resolve().parent / "testdata" / "selection-comparison"


class SelectionComparison(unittest.TestCase):
    def setUp(self):
        self.out = summarize(ROOT)
        self.cmp = {c["id"]: c for c in self.out["comparisons"]}
        self.groups = {g["id"]: g for g in self.out["groups"]}

    def test_four_group_happy(self):
        self.assertTrue(self.out["baseline"]["present"])
        for key in ("base-forced", "base-args", "base-direct"):
            self.assertTrue(self.cmp[key]["comparable"])
            self.assertTrue(self.cmp[key]["adoptable"])
            self.assertFalse(self.cmp[key]["measured_live"])

    def test_retries_kept_in_denominator(self):
        self.assertEqual(self.groups["baseline"]["retries"], 1)
        self.assertTrue(self.groups["forced"]["denominator_includes_failures"])
        self.assertEqual(self.groups["forced"]["failures_included"], 1)

    def test_quality_drop_not_adoptable(self):
        c = self.cmp["quality"]
        self.assertTrue(c["comparable"])
        self.assertFalse(c["adoptable"])
        self.assertIsNone(c["improvement"])

    def test_cost_missing(self):
        self.assertTrue(self.groups["cost-missing"]["cost"]["missing"])
        self.assertEqual(self.cmp["cost"]["reason"], "cost_unknown")
        self.assertIsNone(self.cmp["cost"]["improvement"])

    def test_mismatch(self):
        self.assertFalse(self.cmp["mismatch"]["comparable"])

    def test_baseline_missing(self):
        out = summarize(ROOT / "no-baseline")
        self.assertFalse(out["baseline"]["present"])
        self.assertEqual(out["comparisons"][0]["reason"], "baseline_missing")
        self.assertIsNone(out["comparisons"][0].get("improvement"))


if __name__ == "__main__":
    unittest.main()
