import filecmp
import json
import tempfile
import unittest
from pathlib import Path

from ledger_ml import synth

TESTDATA = Path(__file__).resolve().parents[2] / "testdata" / "synthetic"


class SynthTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.out = Path(cls.tmp.name)
        cls.truth = synth.generate(cls.out)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def test_the_committed_statements_are_what_the_generator_writes(self):
        # The Go tests and the evaluation read testdata/synthetic: it must not drift from the code.
        names = sorted(p.name for p in TESTDATA.iterdir())
        self.assertEqual(names, sorted(p.name for p in self.out.iterdir()))
        match, mismatch, errors = filecmp.cmpfiles(TESTDATA, self.out, names, shallow=False)
        self.assertEqual((mismatch, errors), ([], []), "run: cd ml && python -m ledger_ml.synth --out ../testdata/synthetic")

    def test_the_planted_anomalies_are_there(self):
        txs = self.truth["transactions"]
        emag = [t for t in txs if t["date"] == "2026-02-14" and "EMAG" in t["merchant"].upper()]
        self.assertEqual(len(emag), 2, "the double charge")
        self.assertTrue(any(t["date"] == "2026-04-18" and "KAUFLAND" in t["merchant"].upper() for t in txs))
        netflix = sorted({t["amount"] for t in txs if "NETFLIX" in t["merchant"].upper()})
        self.assertEqual(len(netflix), 2, "the price increase")

    def test_every_transaction_has_a_known_category(self):
        known = set(self.truth["categories"]) | {"Transfer", "Exchange"}
        for t in self.truth["transactions"]:
            self.assertIn(t["category"], known, t)

    def test_truth_is_valid_json(self):
        json.loads((self.out / "truth.json").read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
