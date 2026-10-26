import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from ledger_ml import classify

ML = Path(__file__).resolve().parents[1]

EXAMPLES = [
    {"merchant": "KAUFLAND", "description": "Plata la POS KAUFLAND BUCURESTI", "amount": -180.5, "category": "Groceries"},
    {"merchant": "MEGA IMAGE", "description": "Plata la POS MEGA IMAGE 0123", "amount": -64.2, "category": "Groceries"},
    {"merchant": "DECATHLON", "description": "Plata la POS DECATHLON", "amount": -320.0, "category": "Shopping"},
    {"merchant": "EMAG.RO", "description": "Plata online EMAG.RO", "amount": -499.99, "category": "Shopping"},
    {"merchant": "POPESCU ANDREI", "description": "Plata OP chirie", "amount": -2500.0, "category": "Rent"},
]


class ClassifierTest(unittest.TestCase):
    def setUp(self):
        self.model = classify.train(EXAMPLES)

    def test_learns_the_examples(self):
        self.assertEqual(self.model["train_accuracy"], 1.0)
        preds = classify.predict(self.model, EXAMPLES)
        self.assertEqual([p["category"] for p in preds], [e["category"] for e in EXAMPLES])

    def test_a_variant_of_a_known_merchant_is_recognized(self):
        [p] = classify.predict(self.model, [{"merchant": "MEGA IMAGE EXPRES", "description": "Plata la POS MEGA IMAGE EXPRES", "amount": -30}])
        self.assertEqual(p["category"], "Groceries")
        self.assertGreater(p["confidence"], 0)
        self.assertGreaterEqual(p["similarity"], classify.SIMILAR)

    def test_an_unknown_merchant_gets_no_confidence(self):
        # Five merchants seen: a new one would look most like one of them; the similarity gate
        # leaves it to the language model instead.
        [p] = classify.predict(self.model, [{"merchant": "FARMACIA TEI", "description": "Plata la POS FARMACIA TEI", "amount": -45}])
        self.assertEqual(p["confidence"], 0.0)
        self.assertLess(p["similarity"], classify.SIMILAR)

    def test_save_and_load_keep_the_predictions(self):
        with tempfile.TemporaryDirectory() as d:
            path = str(Path(d) / "m.npz")
            classify.save(self.model, path)
            loaded = classify.load(path)
        a = classify.predict(self.model, EXAMPLES)
        b = classify.predict(loaded, EXAMPLES)
        self.assertEqual([p["category"] for p in a], [p["category"] for p in b])
        for x, y in zip(a, b):
            self.assertAlmostEqual(x["confidence"], y["confidence"], places=9)

    def test_command_line(self):
        with tempfile.TemporaryDirectory() as d:
            path = str(Path(d) / "m.npz")
            run = lambda cmd, req: json.loads(
                subprocess.run([sys.executable, "-m", "ledger_ml.classify", cmd], input=json.dumps(req), capture_output=True,
                               text=True, check=True, cwd=ML).stdout)
            self.assertTrue(run("train", {"model": path, "examples": EXAMPLES})["trained"])
            out = run("predict", {"model": path, "items": [{"merchant": "KAUFLAND", "description": "", "amount": -10}]})
            self.assertEqual(out["predictions"][0]["category"], "Groceries")
            # One category is not enough to learn from.
            self.assertFalse(run("train", {"model": path, "examples": EXAMPLES[:2]})["trained"])


if __name__ == "__main__":
    unittest.main()
