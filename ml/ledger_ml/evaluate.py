"""Evaluations of the parts that use the language model, on the synthetic statements (whose truth is
known), through the real `ledger` binary and the real local model.

    python -m ledger_ml.evaluate categories [--corrections 0,5,15]
    python -m ledger_ml.evaluate questions

categories: imports the statements with no categories and lets rules and the model sort them, then
compares every line with its true category. Then it plays the user: it corrects the most frequent
merchants the system got wrong, a few at a time, and measures again (the corrections apply to every
transaction of the merchant, retrain the classifier, and become examples for the model).

questions: imports the statements with their true categories (so that only the questions are
measured), starts the server, and asks each question of QUESTIONS. The expected answers are computed
here from the truth, never from the program under test. An answer counts as right when the numbers
(or names) expected appear in the result.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DATA = ROOT / "testdata" / "synthetic"
FILES = ["bt_2025-04_2026-09.csv", "bt_2026-09.pdf", "ing_2025-04_2025-12.csv", "ing_2025-12_2026-09.csv", "revolut_2025-04_2026-09.csv"]
RESULTS = ROOT / "ml" / "eval"

INCOME = {"Salary", "Other Income"}
NEUTRAL = {"Transfer", "Exchange"}


def ledger_binary() -> str:
    exe = ROOT / "server" / "bin" / ("ledger.exe" if os.name == "nt" else "ledger")
    if not exe.exists():
        sys.exit(f"build the server first (scripts/build.sh): {exe} is missing")
    return str(exe)


def run(args: list[str], **kw) -> str:
    out = subprocess.run([ledger_binary(), *args], capture_output=True, text=True, encoding="utf-8", **kw)
    if out.returncode != 0:
        raise RuntimeError(f"ledger {' '.join(args)}: {out.stderr.strip()}")
    return out.stdout


def load_truth() -> dict:
    return json.loads((DATA / "truth.json").read_text(encoding="utf-8"))


def truth_keys(truth: dict) -> dict[str, dict]:
    ids = {"bt": truth["accounts"]["bt"], "ing": "ING", "revolut": "Revolut"}
    seen: Counter = Counter()
    out = {}
    for tx in truth["transactions"]:
        base = f"{ids[tx['account']]}|{tx['currency']}|{tx['date']}|{tx['amount']}"
        out[f"{base}|{seen[base]}"] = tx
        seen[base] += 1
    return out


def fresh_db(labels: bool, llm: bool) -> str:
    db = Path(tempfile.mkdtemp(prefix="ledger-eval-")) / "ledger.duckdb"
    args = ["import", "--db", str(db), *[str(DATA / f) for f in FILES]]
    if labels:
        args += ["--labels", str(DATA / "truth.json")]
    if not llm:
        args.append("--no-llm")
    run(args)
    run(["rates", "--db", str(db), str(DATA / "rates.csv")])
    return str(db)


# --- categories ---------------------------------------------------------------------------------------

def measure(db: str, keys: dict[str, dict]) -> dict:
    rows = json.loads(run(["transactions", "--db", db, "--json", "--no-llm"]) or "[]")
    right, per_cat, wrong = 0, defaultdict(lambda: [0, 0]), Counter()
    n = 0
    for r in rows:
        tx = keys.get(r["Key"])
        if tx is None:
            continue
        n += 1
        want = tx["category"]
        per_cat[want][1] += 1
        if r["Category"] == want:
            right += 1
            per_cat[want][0] += 1
        else:
            wrong[(tx["merchant"], want, r["Category"], r["Key"])] += 1
    return {"lines": n, "accuracy": right / n if n else 0, "per_category": {c: v[0] / v[1] for c, v in sorted(per_cat.items())},
            "errors": wrong}


def categories(corrections: list[int]) -> dict:
    truth = load_truth()
    keys = truth_keys(truth)
    t0 = time.time()
    db = fresh_db(labels=False, llm=True)
    first = time.time() - t0
    report = {"import_seconds": round(first, 1), "steps": []}
    corrected = 0
    for target in corrections:
        if target > corrected:
            # The user corrects the merchants with the most wrong lines first.
            m = measure(db, keys)
            by_merchant: Counter = Counter()
            example = {}
            for (merchant, want, got, key), n in m["errors"].items():
                by_merchant[(merchant, want)] += n
                example[(merchant, want)] = key
            for (merchant, want), _ in by_merchant.most_common(target - corrected):
                run(["correct", "--db", db, "--no-llm", example[(merchant, want)], want])
                corrected += 1
            run(["categorize", "--db", db, "--relabel"])
        m = measure(db, keys)
        top = [f"{merchant}: {want} taken for {got} ({n})" for (merchant, want, got, _), n in m["errors"].most_common(8)]
        report["steps"].append({"corrections": corrected, "accuracy": round(m["accuracy"], 4), "lines": m["lines"],
                                "per_category": {k: round(v, 3) for k, v in m["per_category"].items()}, "top_errors": top})
        print(f"{corrected:3d} corrections: {100 * m['accuracy']:.1f}% of {m['lines']} lines right", flush=True)
    return report


# --- questions ----------------------------------------------------------------------------------------

class Facts:
    """The truth as answers: money in RON at each day's rate, rounded per line as the views do."""

    def __init__(self, truth: dict):
        self.tx = truth["transactions"]
        self.rates = sorted(truth["rates"].items())

    def rate(self, cur: str, day: str) -> float:
        if cur == "RON":
            return 1.0
        best = self.rates[0][1]
        for d, r in self.rates:
            if d <= day:
                best = r
            else:
                break
        return best

    def ron(self, tx: dict) -> float:
        return round(tx["amount"] / 100 * self.rate(tx["currency"], tx["date"]), 2)

    def spent(self, cats=None, start="0000", end="9999", merchant=None, currency=None) -> float:
        total = 0.0
        for t in self.tx:
            if t["category"] in INCOME or t["category"] in NEUTRAL or t["kind"] in ("transfer", "exchange"):
                continue
            if cats and t["category"] not in cats:
                continue
            if not (start <= t["date"] <= end):
                continue
            if merchant and merchant.lower() not in (t["merchant"] + " " + t["description"]).lower():
                continue
            if currency:
                if t["currency"] != currency:
                    continue
                total -= t["amount"] / 100
            else:
                total -= self.ron(t)
        return round(total, 2)

    def earned(self, cats=None, start="0000", end="9999") -> float:
        return round(sum(self.ron(t) for t in self.tx if t["category"] in (cats or INCOME) and start <= t["date"] <= end and t["kind"] == "income"), 2)

    def by(self, key, cats=None, start="0000", end="9999") -> list[tuple[str, float]]:
        acc: dict[str, float] = defaultdict(float)
        for t in self.tx:
            if t["category"] in INCOME or t["category"] in NEUTRAL or t["kind"] in ("transfer", "exchange"):
                continue
            if (cats and t["category"] not in cats) or not (start <= t["date"] <= end):
                continue
            acc[key(t)] -= self.ron(t)
        return sorted(acc.items(), key=lambda kv: -kv[1])

    def balances(self, truth: dict) -> dict[str, float]:
        op = truth["openings"]
        b = {"bt|RON": op["bt"], "ing|RON": op["ing"], "revolut|RON": op["revolut"]["RON"], "revolut|EUR": op["revolut"]["EUR"]}
        for t in self.tx:
            b[f"{t['account']}|{t['currency']}"] += t["amount"]
        return {k: v / 100 for k, v in b.items()}


FOOD = ["Groceries", "Restaurants", "Food Delivery"]


def questions(truth: dict) -> list[dict]:
    f = Facts(truth)
    months = sorted({t["date"][:7] for t in f.tx})
    by_month_rest = f.by(lambda t: t["date"][:7], ["Restaurants"])
    groceries_monthly = [f.spent(["Groceries"], m + "-01", m + "-31") for m in months]
    bal = f.balances(truth)
    subs_2026 = f.by(lambda t: t["merchant"], ["Subscriptions"], "2026-01-01", "2026-12-31")
    top_2025 = f.by(lambda t: t["merchant"], None, "2025-01-01", "2025-12-31")
    rides_july = sum(1 for t in f.tx if t["merchant"] in ("Bolt", "Uber") and t["date"].startswith("2026-07"))
    q = [
        ("Cât am cheltuit pe cumpărături de la supermarket în martie 2026?", "numbers", [f.spent(["Groceries"], "2026-03-01", "2026-03-31")]),
        ("Cât am cheltuit pe mâncare în martie 2026 față de februarie 2026?", "numbers",
         [f.spent(FOOD, "2026-03-01", "2026-03-31"), f.spent(FOOD, "2026-02-01", "2026-02-28")]),
        ("How much did I spend on fuel in 2025?", "numbers", [f.spent(["Fuel"], "2025-01-01", "2025-12-31")]),
        ("Care a fost salariul meu total în 2025?", "numbers", [f.earned(["Salary"], "2025-01-01", "2025-12-31")]),
        ("How much did I spend at Kaufland in total?", "numbers", [f.spent(merchant="kaufland")]),
        ("Cât am plătit pe abonamente în 2026?", "numbers", [f.spent(["Subscriptions"], "2026-01-01", "2026-12-31")]),
        ("What were my top 3 spending categories in August 2025?", "labels",
         [c for c, _ in f.by(lambda t: t["category"], None, "2025-08-01", "2025-08-31")[:3]]),
        ("În ce lună am cheltuit cel mai mult la restaurante?", "label_or_number", [by_month_rest[0][0], by_month_rest[0][1]]),
        ("How much did I spend in euros in August 2025?", "numbers", [f.spent(None, "2025-08-01", "2025-08-31", currency="EUR")]),
        ("Cât am plătit chirie în total?", "numbers", [f.spent(["Rent"])]),
        ("How much did Netflix cost me in 2026?", "numbers", [f.spent(["Subscriptions"], "2026-01-01", "2026-12-31", merchant="netflix")]),
        ("Care este soldul fiecărui cont?", "numbers", [round(v, 2) for v in bal.values()]),
        ("How many Bolt or Uber rides did I take in July 2026?", "numbers", [rides_july]),
        ("Cât am cheltuit pe utilități în iarnă, din decembrie 2025 până în februarie 2026?", "numbers",
         [f.spent(["Utilities"], "2025-12-01", "2026-02-28")]),
        ("What is my average monthly spending on groceries?", "numbers", [round(sum(groceries_monthly) / len(groceries_monthly), 2)]),
        ("Câți bani am retras cash în 2026?", "numbers", [f.spent(["Cash"], "2026-01-01", "2026-12-31")]),
        ("How much interest have I earned in total?", "numbers", [f.earned(["Other Income"])]),
        ("Cât am plătit comisioane bancare în 2026?", "numbers", [f.spent(["Fees"], "2026-01-01", "2026-12-31")]),
        ("Cât am cheltuit pe sănătate anul acesta?", "numbers", [f.spent(["Health"], "2026-01-01", "2026-12-31")]),
        ("Which merchant did I spend the most money at in 2025?", "label", [top_2025[0][0]]),
        ("How much did I spend in total in September 2026?", "numbers", [f.spent(None, "2026-09-01", "2026-09-30")]),
        # Right if it gives the larger month's total (alone, or next to the other's).
        ("Am cheltuit mai mult în iulie sau în august 2026?", "numbers",
         [max(f.spent(None, "2026-07-01", "2026-07-31"), f.spent(None, "2026-08-01", "2026-08-31"))]),
        ("What was my total income in 2026?", "numbers", [f.earned(None, "2026-01-01", "2026-12-31")]),
        ("Cât am economisit în 2025, adică venituri minus cheltuieli?", "numbers",
         [round(f.earned(None, "2025-01-01", "2025-12-31") - f.spent(None, "2025-01-01", "2025-12-31"), 2)]),
        ("Ce abonamente am plătit în 2026 și cât m-a costat fiecare?", "labels", [m for m, _ in subs_2026]),
    ]
    return [{"question": a, "kind": k, "expected": e} for a, k, e in q]


def numbers_in(rows) -> list[float]:
    out = []
    for row in rows:
        for v in row:
            if isinstance(v, (int, float)) and not isinstance(v, bool):
                out.append(float(v))
    return out


def texts_in(rows) -> list[str]:
    return [str(v).lower() for row in rows for v in row if isinstance(v, str)]


def close(a: float, b: float) -> bool:
    return abs(a - b) <= max(1.0, 0.005 * abs(b))


def label_match(expected: str, texts: list[str]) -> bool:
    e = expected.lower().replace(" (rent)", "")
    words = [w for w in e.replace(".", " ").split() if len(w) > 2] or [e]
    return any(e in t or all(w in t for w in words) for t in texts)


def grade(item: dict, answer: dict) -> bool:
    rows = answer.get("rows") or []
    nums, texts = numbers_in(rows), texts_in(rows)
    kind, exp = item["kind"], item["expected"]
    if kind == "numbers":
        # The sign is a matter of presentation ("-2800" withdrawn is as right as "2800").
        return all(any(close(abs(n), abs(e)) for n in nums) for e in exp)
    if kind == "labels":
        return all(label_match(e, texts) for e in exp)
    if kind == "label":
        return label_match(exp[0], texts[:3] if texts else [])
    if kind == "label_or_number":
        month = exp[0]
        return any(month in t for t in texts) or any(month.replace("-", "") in t.replace("-", "") for t in texts)
    return False


def free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def ask_all() -> dict:
    truth = load_truth()
    items = questions(truth)
    db = fresh_db(labels=True, llm=False)
    port = free_port()
    server = subprocess.Popen([ledger_binary(), "serve", "--db", db, "--addr", f"127.0.0.1:{port}"], stdout=subprocess.DEVNULL,
                              stderr=subprocess.DEVNULL)
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(f"http://127.0.0.1:{port}/api/status", timeout=1)
                break
            except OSError:
                time.sleep(0.2)
        results, right = [], 0
        for i, item in enumerate(items):
            t0 = time.time()
            req = urllib.request.Request(f"http://127.0.0.1:{port}/api/ask", data=json.dumps({"question": item["question"]}).encode(),
                                         headers={"Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(req, timeout=600) as r:
                    answer = json.load(r)
                error = None
            except urllib.error.HTTPError as e:
                body = json.load(e)
                answer, error = body.get("answer") or {}, body.get("error")
            ok = error is None and grade(item, answer)
            right += ok
            results.append({**item, "ok": ok, "sql": answer.get("sql"), "rows": (answer.get("rows") or [])[:6], "error": error,
                            "retried": len(answer.get("rejected") or []), "seconds": round(time.time() - t0, 1)})
            print(f"{'ok ' if ok else 'BAD'} {time.time() - t0:5.1f}s  {item['question']}", flush=True)
            if not ok:
                print(f"     expected {item['expected']}; got {(answer.get('rows') or [])[:4]} {error or ''}\n     {answer.get('sql')}", flush=True)
        return {"questions": len(items), "right": right, "accuracy": round(right / len(items), 3), "results": results}
    finally:
        server.terminate()


def main():
    # Questions are in Romanian: print them whatever the console's code page.
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("what", choices=["categories", "questions"])
    ap.add_argument("--corrections", default="0,5,15")
    a = ap.parse_args()
    RESULTS.mkdir(parents=True, exist_ok=True)
    if a.what == "categories":
        out = categories([int(x) for x in a.corrections.split(",")])
    else:
        out = ask_all()
        print(f"\n{out['right']}/{out['questions']} questions answered right")
    out["date"] = dt.date.today().isoformat()
    (RESULTS / f"{a.what}.json").write_text(json.dumps(out, indent=1, ensure_ascii=False, default=str), encoding="utf-8")


if __name__ == "__main__":
    main()
