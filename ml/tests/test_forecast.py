import datetime as dt
import unittest

from ledger_ml import forecast


def history(weeks=30, daily=-100.0):
    """Everyday spending of `daily` RON every day, a salary and rent every month."""
    start = dt.date(2026, 1, 1)
    out = []
    for i in range(weeks * 7):
        d = start + dt.timedelta(days=i)
        out.append({"date": d.isoformat(), "amount_ron": daily, "kind": "expense", "merchant": "SHOP"})
        if d.day == 25:
            out.append({"date": d.isoformat(), "amount_ron": 9000.0, "kind": "income", "merchant": "EMPLOYER"})
        if d.day == 1:
            out.append({"date": d.isoformat(), "amount_ron": -2500.0, "kind": "expense", "merchant": "LANDLORD"})
    return out


RECURRING = [
    {"merchant": "EMPLOYER", "cadence": "monthly", "next": "2026-08-25", "typical_ron": 9000.0, "active": True},
    {"merchant": "LANDLORD", "cadence": "monthly", "next": "2026-08-01", "typical_ron": -2500.0, "active": True},
]


class ForecastTest(unittest.TestCase):
    def request(self, **kw):
        h = history()
        end = max(x["date"] for x in h)
        start = (dt.date.fromisoformat(end) + dt.timedelta(days=1)).isoformat()
        return {"history": h, "recurring": RECURRING, "balance": 10000.0, "start": start, "days": 60, "paths": 500, **kw}

    def test_a_steady_life_is_forecast_exactly(self):
        # Every day costs the same and the scheduled flows never vary: no uncertainty.
        f = forecast.simulate(self.request())
        self.assertEqual(len(f["days"]), 60)
        self.assertEqual(f["p10"], f["p90"])
        start = dt.date.fromisoformat(f["days"][0])
        salaries = sum(1 for i in range(60) if (start + dt.timedelta(days=i)).day == 25)
        rents = sum(1 for i in range(60) if (start + dt.timedelta(days=i)).day == 1)
        self.assertAlmostEqual(f["end"]["p50"], 10000 - 100 * 60 + 9000 * salaries - 2500 * rents, places=2)
        self.assertAlmostEqual(f["everyday_spending_per_month"], -100 * 30.44, places=0)
        self.assertEqual(f["scheduled"], 2)

    def test_the_band_widens_with_uncertain_spending(self):
        req = self.request()
        for i, h in enumerate(req["history"]):
            if h["merchant"] == "SHOP":
                h["amount_ron"] = -20.0 if i % 3 else -260.0
        f = forecast.simulate(req)
        widths = [hi - lo for lo, hi in zip(f["p10"], f["p90"])]
        self.assertGreater(widths[-1], widths[5])
        self.assertTrue(all(lo <= mid <= hi for lo, mid, hi in zip(f["p10"], f["p50"], f["p90"])))

    def test_chance_below_threshold(self):
        self.assertEqual(forecast.simulate(self.request(threshold=-1e9))["chance_below_threshold"], 0.0)
        self.assertEqual(forecast.simulate(self.request(threshold=1e9))["chance_below_threshold"], 1.0)

    def test_the_same_seed_gives_the_same_forecast(self):
        self.assertEqual(forecast.simulate(self.request(seed=3)), forecast.simulate(self.request(seed=3)))

    def test_backtest_scores_against_what_happened(self):
        f = forecast.simulate(self.request())
        b = forecast.backtest(self.request(actual=f["p50"]))
        self.assertEqual(b["coverage_10_90"], 1.0)
        self.assertEqual(b["mean_abs_error"], 0.0)
        off = forecast.backtest(self.request(actual=[x + 1000 for x in f["p50"]]))
        self.assertAlmostEqual(off["end_error"], -1000.0, places=2)


if __name__ == "__main__":
    unittest.main()
