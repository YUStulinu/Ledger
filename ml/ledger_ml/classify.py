"""The transaction classifier that learns from your corrections.

A multinomial logistic regression (softmax) over hashed features of a transaction: character
n-grams of the merchant name (so "MEGA IMAGE" and "MEGA IMAGE EXPRES" look alike), the words of
the description, whether money went out or came in, and the size of the amount. It is written
with numpy alone: the model is a weight matrix, trained by gradient descent with L2 regularization.

It complements the language model: the language model knows the world (that KAUFLAND sells
groceries), the classifier knows you (that you file DECATHLON under Shopping, or a friend's name
under Rent). The server asks it first and only accepts confident answers.

A classifier trained on a handful of corrections is confidently wrong about everything else (with
five merchants seen, every new one looks most like one of those five). So an answer only counts
when the transaction resembles one it learned from: the cosine similarity of their merchant
n-grams must reach SIMILAR; otherwise its confidence is zero and the language model decides.

Called by the Go server with JSON on stdin and stdout:

    python -m ledger_ml.classify train   < {"model": path, "examples": [{merchant, description, amount, category}]}
    python -m ledger_ml.classify predict < {"model": path, "items": [{merchant, description, amount}]}
"""

from __future__ import annotations

import json
import math
import re
import sys
import zlib
from pathlib import Path

import numpy as np

DIM = 1 << 15  # hashed feature space
SIMILAR = 0.6  # how close (cosine of merchant n-grams) to a learned merchant an answer must be


def _hash(token: str) -> int:
    return zlib.crc32(token.encode("utf-8")) & (DIM - 1)


def merchant_features(merchant: str) -> dict[int, float]:
    """The merchant's character n-grams alone, L2-normalized (for the similarity check)."""
    f: dict[int, float] = {}
    m = " " + re.sub(r"\s+", " ", merchant.upper()).strip() + " "
    for n in (3, 4, 5):
        for i in range(len(m) - n + 1):
            k = _hash(f"m{n}:{m[i : i + n]}")
            f[k] = f.get(k, 0.0) + 1.0
    norm = math.sqrt(sum(v * v for v in f.values())) or 1.0
    return {k: v / norm for k, v in f.items()}


def similarity(a: dict[int, float], b: dict[int, float]) -> float:
    if len(a) > len(b):
        a, b = b, a
    return sum(v * b.get(k, 0.0) for k, v in a.items())


def features(merchant: str, description: str, amount: float) -> dict[int, float]:
    """Sparse feature vector (index -> value), L2-normalized."""
    f: dict[int, float] = {}

    def add(tok: str, w: float = 1.0):
        i = _hash(tok)
        f[i] = f.get(i, 0.0) + w

    m = " " + re.sub(r"\s+", " ", merchant.upper()).strip() + " "
    for n in (3, 4, 5):
        for i in range(len(m) - n + 1):
            add(f"m{n}:{m[i : i + n]}")
    for word in m.split():
        add(f"mw:{word}", 2.0)
    for word in re.findall(r"[A-Za-z]{3,}", description.upper())[:40]:
        add(f"dw:{word}", 0.5)
    add("dir:in" if amount > 0 else "dir:out", 2.0)
    add(f"size:{min(int(math.log10(abs(amount) + 1) * 2), 12)}")
    add("bias", 1.0)
    norm = math.sqrt(sum(v * v for v in f.values())) or 1.0
    return {k: v / norm for k, v in f.items()}


def _matrix(rows: list[dict[int, float]]) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """CSR-like arrays: row index, column, value."""
    r, c, v = [], [], []
    for i, row in enumerate(rows):
        for k, x in row.items():
            r.append(i)
            c.append(k)
            v.append(x)
    return np.array(r, dtype=np.int64), np.array(c, dtype=np.int64), np.array(v, dtype=np.float64)


def _scores(W: np.ndarray, r, c, v, n: int) -> np.ndarray:
    out = np.zeros((n, W.shape[1]))
    np.add.at(out, r, W[c] * v[:, None])
    return out


def _softmax(z: np.ndarray) -> np.ndarray:
    z = z - z.max(axis=1, keepdims=True)
    e = np.exp(z)
    return e / e.sum(axis=1, keepdims=True)


def train(examples: list[dict], epochs: int = 300, lr: float = 2.0, l2: float = 1e-4) -> dict:
    labels = sorted({e["category"] for e in examples})
    index = {c: i for i, c in enumerate(labels)}
    rows = [features(e.get("merchant", ""), e.get("description", ""), float(e.get("amount", 0))) for e in examples]
    y = np.array([index[e["category"]] for e in examples])
    weight = np.array([float(e.get("weight", 1.0)) for e in examples])
    r, c, v = _matrix(rows)
    n, k = len(rows), len(labels)
    W = np.zeros((DIM, k))
    Y = np.zeros((n, k))
    Y[np.arange(n), y] = 1.0
    wsum = weight.sum()
    for _ in range(epochs):
        P = _softmax(_scores(W, r, c, v, n))
        G = (P - Y) * (weight / wsum)[:, None]  # gradient of the weighted cross-entropy per row
        grad = np.zeros_like(W)
        np.add.at(grad, c, G[r] * v[:, None])
        W -= lr * (grad + l2 * W)
    P = _softmax(_scores(W, r, c, v, n))
    acc = float((P.argmax(axis=1) == y).mean()) if n else 0.0
    merchants = sorted({e.get("merchant", "").upper().strip() for e in examples})
    return {"labels": labels, "W": W, "train_accuracy": acc, "merchants": merchants}


def predict(model: dict, items: list[dict]) -> list[dict]:
    rows = [features(i.get("merchant", ""), i.get("description", ""), float(i.get("amount", 0))) for i in items]
    if not rows:
        return []
    r, c, v = _matrix(rows)
    P = _softmax(_scores(model["W"], r, c, v, len(rows)))
    known = [merchant_features(m) for m in model.get("merchants", [])]
    out = []
    for item, p in zip(items, P):
        j = int(p.argmax())
        mf = merchant_features(item.get("merchant", ""))
        sim = max((similarity(mf, k) for k in known), default=0.0)
        conf = float(p[j]) if sim >= SIMILAR else 0.0
        out.append({"category": model["labels"][j], "confidence": conf, "similarity": round(sim, 3)})
    return out


def save(model: dict, path: str) -> None:
    W = model["W"]
    nz = np.nonzero(np.abs(W).sum(axis=1) > 0)[0]  # most hashed rows are never used: store the others
    np.savez_compressed(path, rows=nz, W=W[nz], labels=np.array(model["labels"]), merchants=np.array(model.get("merchants", [])))


def load(path: str) -> dict:
    d = np.load(path, allow_pickle=False)
    W = np.zeros((DIM, len(d["labels"])))
    W[d["rows"]] = d["W"]
    return {"labels": [str(x) for x in d["labels"]], "W": W, "merchants": [str(x) for x in d["merchants"]] if "merchants" in d else []}


def main() -> None:
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    req = json.load(sys.stdin)
    if cmd == "train":
        examples = req["examples"]
        if len({e["category"] for e in examples}) < 2:
            json.dump({"trained": False, "reason": "need examples of at least two categories"}, sys.stdout)
            return
        model = train(examples)
        Path(req["model"]).parent.mkdir(parents=True, exist_ok=True)
        save(model, req["model"])
        json.dump({"trained": True, "examples": len(examples), "categories": len(model["labels"]),
                   "train_accuracy": model["train_accuracy"]}, sys.stdout)
    elif cmd == "predict":
        if not Path(req["model"]).exists():
            json.dump({"predictions": None}, sys.stdout)
            return
        json.dump({"predictions": predict(load(req["model"]), req["items"])}, sys.stdout)
    else:
        sys.exit("usage: python -m ledger_ml.classify train|predict < request.json")


if __name__ == "__main__":
    main()
