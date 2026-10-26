"""The balance forecast: where your money will be over the next months, with how sure that is.

A Monte Carlo simulation over many possible futures:

* scheduled flows (salary, rent, bills, subscriptions; found by the Go server's recurring-payment
  detector) land on their dates. A bill whose amount varied in the past varies the same way in the
  simulation;
* everyday spending (everything that is not scheduled) is drawn from your own past days: each
  simulated day takes the spending of a random real day from the same weekday in the last 26 weeks
  (Saturdays are not Tuesdays).

The answer is a band per day (the median, and the 10th and 90th percentiles) and the chance of the
balance going below a threshold.

    python -m ledger_ml.forecast < {"history": [...], "recurring": [...], "balance": ..., "start": ..., "days": 90}
"""

from __future__ import annotations

import datetime as dt
import json
import sys
from collections import defaultdict

import numpy as np


def _date(s: str) -> dt.date:
    return dt.date.fromisoformat(s[:10])


def _add_period(d: dt.date, cadence: str) -> dt.date:
    if cadence == "weekly":
        return d + dt.timedelta(days=7)
    if cadence == "yearly":
        try:
            return d.replace(year=d.year + 1)
        except ValueError:
            return d + dt.timedelta(days=365)
    month = d.month % 12 + 1
    year = d.year + (d.month == 12)
    for day in (d.day, 30, 29, 28):
        try:
            return dt.date(year, month, day)
        except ValueError:
            continue
    raise ValueError(d)


def simulate(req: dict) -> dict:
    rng = np.random.default_rng(int(req.get("seed", 1)))
    start = _date(req["start"])
    days = int(req.get("days", 90))
    paths = int(req.get("paths", 2000))
    balance = float(req["balance"])
    recurring = [r for r in req.get("recurring", []) if r.get("active", True)]
    scheduled_names = {r["merchant"] for r in recurring}

    # Past amounts of each scheduled flow, for its variability (relative to its typical amount).
    spread: dict[str, list[float]] = defaultdict(list)
    for h in req["history"]:
        if h["merchant"] in scheduled_names:
            spread[h["merchant"]].append(float(h["amount_ron"]))

    # Everyday spending per past day (outflows that are not scheduled), by weekday, last 26 weeks.
    history_end = max((_date(h["date"]) for h in req["history"]), default=start - dt.timedelta(days=1))
    window_start = history_end - dt.timedelta(weeks=26)
    daily: dict[dt.date, float] = defaultdict(float)
    for h in req["history"]:
        d = _date(h["date"])
        if d < window_start or h["merchant"] in scheduled_names or h.get("kind") != "expense":
            continue
        daily[d] += float(h["amount_ron"])  # negative: spending
    by_weekday: dict[int, list[float]] = defaultdict(list)
    d = window_start
    while d <= history_end:
        by_weekday[d.weekday()].append(daily.get(d, 0.0))
        d += dt.timedelta(days=1)

    flows = np.zeros((paths, days))
    # Everyday spending.
    for i in range(days):
        pool = np.array(by_weekday[(start + dt.timedelta(days=i)).weekday()] or [0.0])
        flows[:, i] += rng.choice(pool, size=paths)
    # Scheduled flows.
    for r in recurring:
        nxt = _date(r["next"])
        while nxt < start:
            nxt = _add_period(nxt, r["cadence"])
        past = np.array(spread.get(r["merchant"], [])[-12:])
        typical = float(r["typical_ron"])
        while (nxt - start).days < days:
            i = (nxt - start).days
            if len(past) >= 3 and np.std(past) > 0.02 * abs(typical):
                flows[:, i] += rng.choice(past, size=paths)
            else:
                flows[:, i] += typical
            nxt = _add_period(nxt, r["cadence"])

    balances = balance + np.cumsum(flows, axis=1)
    p10, p50, p90 = np.percentile(balances, [10, 50, 90], axis=0)
    threshold = float(req.get("threshold", 0))
    below = float((balances.min(axis=1) < threshold).mean())
    out_days = [(start + dt.timedelta(days=i)).isoformat() for i in range(days)]
    low = int(np.argmin(p50))
    return {
        "start_balance": balance,
        "days": out_days,
        "p10": [round(float(x), 2) for x in p10],
        "p50": [round(float(x), 2) for x in p50],
        "p90": [round(float(x), 2) for x in p90],
        "end": {"p10": round(float(p10[-1]), 2), "p50": round(float(p50[-1]), 2), "p90": round(float(p90[-1]), 2)},
        "lowest": {"date": out_days[low], "p50": round(float(p50[low]), 2), "p10": round(float(p10[low]), 2)},
        "threshold": threshold,
        "chance_below_threshold": below,
        "everyday_spending_per_month": round(float(np.mean([sum(v) / max(len(v), 1) for v in by_weekday.values()]) * 30.44), 2),
        "scheduled": len(recurring),
    }


def backtest(req: dict) -> dict:
    """Forecast from a past date and compare with what actually happened: `actual` is the real
    balance of each day after the start."""
    f = simulate(req)
    actual = req["actual"]
    n = min(len(actual), len(f["p50"]))
    inside = sum(1 for i in range(n) if f["p10"][i] <= actual[i] <= f["p90"][i])
    errors = [abs(f["p50"][i] - actual[i]) for i in range(n)]
    return {
        **f,
        "actual": actual[:n],
        "coverage_10_90": inside / n if n else 0.0,
        "mean_abs_error": float(np.mean(errors)) if errors else 0.0,
        "end_error": f["p50"][n - 1] - actual[n - 1] if n else 0.0,
    }


def main() -> None:
    req = json.load(sys.stdin)
    out = backtest(req) if "actual" in req else simulate(req)
    json.dump(out, sys.stdout)


if __name__ == "__main__":
    main()
