import json
import tempfile
import unittest
from pathlib import Path

from summarize_selection_benchmark import summarize


ROOT = Path(__file__).parent / "testdata" / "selection-benchmark"


class SelectionBenchmark(unittest.TestCase):
    def test_contract_and_retention_denominator(self):
        out = summarize(ROOT)
        self.assertTrue(out["contract_valid"])
        self.assertEqual(out["modes"]["local"]["denominator"], 6)
        self.assertEqual(out["modes"]["local"]["correct_tools_retained"], 2)
        self.assertEqual(out["modes"]["local"]["excluded"][0]["reason"], "unsupported_host_format")

    def test_missing_selection_tokens_stays_missing(self):
        out = summarize(ROOT)
        self.assertTrue(out["modes"]["jev"]["selection_tokens"]["missing"])

    def test_invalid_label_is_rejected_before_measurement(self):
        data = json.loads((ROOT / "input.json").read_text())
        data["cases"][0]["correct_tools"] = ["not-in-catalog"]
        with tempfile.TemporaryDirectory() as tmp:
            Path(tmp, "input.json").write_text(json.dumps(data))
            with self.assertRaisesRegex(ValueError, "correct_tools"):
                summarize(Path(tmp))


if __name__ == "__main__":
    unittest.main()
