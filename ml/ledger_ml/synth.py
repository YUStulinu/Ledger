"""Synthetic bank statements with a known truth.

Eighteen months in the life of one person with three banks: a BT current account (salary, rent, most
card payments), an ING account (utilities by direct debit, savings transfers) and Revolut (RON and EUR
pockets, top-ups from the BT card, travel abroad). The statements are written in the export formats of
each bank, so the importers can be tested on realistic files. Alongside them goes everything that is
true about them: the merchant and category of every transaction, the subscriptions, the injected
anomalies (a double charge, an unusually large purchase, a surprise fee, a price increase) and the
exchange rates. The text-to-SQL evaluation computes its expected answers from this truth, never from
the program under test.

    python -m ledger_ml.synth --out testdata/synthetic [--seed 7]
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import random
import textwrap
from dataclasses import asdict, dataclass, field
from pathlib import Path

from . import pdfwrite

START = dt.date(2025, 4, 1)
MONTHS = 18
END = dt.date(2026, 9, 30)

OWNER = "IONESCU MARIA"
BT_IBAN = "RO49BTRLRONCRT0123456701"
ING_IBAN = "RO66INGB0000999901234567"
REVOLUT_IBAN = "LT603250012345678901"
BT_CARD, ING_CARD, REVOLUT_CARD = "1234", "5678", "4321"

# Balances on the first day.
OPENINGS = {"bt": 865035, "ing": 1589000, "revolut": {"RON": 15218, "EUR": 3750}}

MONTHS_RO = ["ianuarie", "februarie", "martie", "aprilie", "mai", "iunie", "iulie", "august", "septembrie", "octombrie", "noiembrie", "decembrie"]

# The category of every merchant (the truth the classifier is measured against).
CATEGORIES = [
    "Groceries", "Restaurants", "Food Delivery", "Transport", "Fuel", "Rent", "Utilities", "Phone & Internet",
    "Subscriptions", "Health", "Shopping", "Entertainment", "Travel", "Fees", "Cash", "Salary", "Other Income",
    "Transfer", "Exchange",
]


@dataclass
class Event:
    date: dt.date
    account: str  # bt, ing, revolut
    currency: str
    amount: int  # bani / cents, signed: negative leaves the account
    merchant: str
    category: str
    kind: str  # expense, income, transfer, exchange
    raw: str  # how the merchant appears on the statement (store numbers and all)
    channel: str = "pos"  # pos, online, op (bank transfer), direct_debit, atm, fee, topup, exchange, refund, interest
    city: str = "BUCURESTI"
    recurring: str | None = None
    anomaly: str | None = None
    counterparty: str = ""
    note: str = ""
    time: str = "12:00:00"
    order: int = 0
    id: int = 0
    pair: int | None = None  # the other side of a transfer between own accounts
    extra: dict = field(default_factory=dict)


def money(x: float) -> int:
    return int(round(x * 100))


def fmt_ro(bani: int) -> str:
    """1234567 -> '12.345,67' (the Romanian format BT and ING use)."""
    s = f"{abs(bani) // 100:,}".replace(",", ".")
    return f"{'-' if bani < 0 else ''}{s},{abs(bani) % 100:02d}"


def fmt_dot(bani: int) -> str:
    return f"{bani / 100:.2f}"


def business_day_before(d: dt.date) -> dt.date:
    while d.weekday() >= 5:
        d -= dt.timedelta(days=1)
    return d


def month_starts():
    y, m = START.year, START.month
    for _ in range(MONTHS):
        yield dt.date(y, m, 1)
        m += 1
        if m == 13:
            y, m = y + 1, 1


def last_day(d: dt.date) -> dt.date:
    nxt = dt.date(d.year + (d.month == 12), d.month % 12 + 1, 1)
    return nxt - dt.timedelta(days=1)


class World:
    def __init__(self, seed: int):
        self.r = random.Random(seed)
        self.events: list[Event] = []
        self.rates: dict[dt.date, int] = {}  # EUR in RON, x10000

    def add(self, **kw) -> Event:
        e = Event(**kw)
        e.time = kw.get("time") or f"{self.r.randint(7, 22):02d}:{self.r.randint(0, 59):02d}:{self.r.randint(0, 59):02d}"
        self.events.append(e)
        return e

    # --- exchange rates ---------------------------------------------------------------------------

    def make_rates(self):
        rate = 4.9770
        d = START - dt.timedelta(days=7)
        while d <= END:
            if d.weekday() < 5:
                rate += self.r.gauss(0, 0.0035) + (4.99 - rate) * 0.02
                self.rates[d] = int(round(rate * 10000))
            d += dt.timedelta(days=1)

    def eur_rate(self, d: dt.date) -> float:
        while d not in self.rates:
            d -= dt.timedelta(days=1)
        return self.rates[d] / 10000

    # --- the life ---------------------------------------------------------------------------------

    def pick(self, items):
        return items[self.r.randrange(len(items))]

    def day(self, month: dt.date, lo: int, hi: int) -> dt.date:
        return month.replace(day=min(self.r.randint(lo, hi), last_day(month).day))

    def card(self, d, merchant, category, amount, raw, account="bt", currency="RON", channel="pos", city="BUCURESTI", **kw):
        return self.add(date=d, account=account, currency=currency, amount=-money(amount), merchant=merchant, category=category,
                        kind="expense", raw=raw, channel=channel, city=city, **kw)

    def build(self):
        r = self.r
        self.make_rates()
        for month in month_starts():
            m_idx = (month.year - START.year) * 12 + month.month - START.month
            name = MONTHS_RO[month.month - 1]
            # Salary, with a raise in January 2026.
            salary = 9850.00 if month < dt.date(2026, 1, 1) else 10850.00
            pay = business_day_before(month.replace(day=25))
            self.add(date=pay, account="bt", currency="RON", amount=money(salary), merchant="Northwind Software", category="Salary",
                     kind="income", raw="NORTHWIND SOFTWARE SRL", channel="op", counterparty="RO02BACX0000001234567890",
                     note=f"Salariu {name} {month.year}", recurring="salary", time="09:12:00")
            # Rent to the landlord, by bank transfer.
            self.add(date=self.day(month, 1, 3), account="bt", currency="RON", amount=-money(2500), merchant="Popescu Andrei (rent)",
                     category="Rent", kind="expense", raw="POPESCU ANDREI", channel="op", counterparty="RO35RNCB0082000000001234",
                     note=f"Chirie {name} {month.year}", recurring="rent")
            # Utilities by direct debit from ING: electricity all year, gas heavier in winter.
            winter = month.month in (11, 12, 1, 2, 3)
            self.add(date=self.day(month, 9, 12), account="ing", currency="RON", amount=-money(r.uniform(140, 210) + (60 if winter else 0)),
                     merchant="Enel Energie", category="Utilities", kind="expense", raw="ENEL ENERGIE MUNTENIA SA", channel="direct_debit",
                     recurring="enel")
            self.add(date=self.day(month, 14, 17), account="ing", currency="RON", amount=-money(r.uniform(380, 520) if winter else r.uniform(55, 95)),
                     merchant="Engie", category="Utilities", kind="expense", raw="ENGIE ROMANIA SA", channel="direct_debit", recurring="engie")
            # Internet and phone, online by card.
            self.card(month.replace(day=15), "Digi", "Phone & Internet", 62.00, "DIGI RCS RDS", channel="online", recurring="digi")
            self.card(self.day(month, 19, 21), "Orange", "Phone & Internet", 45.00, "ORANGE ROMANIA", channel="online", recurring="orange")
            # Subscriptions: Netflix raises its price in March 2026; Dropbox in EUR is the forgotten one.
            netflix = 49.99 if month < dt.date(2026, 3, 1) else 59.99
            self.card(month.replace(day=5), "Netflix", "Subscriptions", netflix, "NETFLIX.COM", channel="online", city="AMSTERDAM",
                      recurring="netflix", anomaly="price_increase" if month == dt.date(2026, 3, 1) else None)
            self.card(month.replace(day=12), "Spotify", "Subscriptions", 26.99, "Spotify", account="revolut", channel="online",
                      city="STOCKHOLM", recurring="spotify")
            self.card(month.replace(day=2), "World Class", "Subscriptions", 249.00, "WORLD CLASS ROMANIA", recurring="gym")
            if month >= dt.date(2025, 6, 1):
                self.card(month.replace(day=22), "Dropbox", "Subscriptions", 11.99, "Dropbox", account="revolut", currency="EUR",
                          channel="online", city="DUBLIN", recurring="dropbox")
            # Public transport pass, bought on the first working day.
            self.card(business_day_before(month.replace(day=1) + dt.timedelta(days=2)), "STB", "Transport", 80.00, "STB SA", recurring="stb")
            # Savings: 1000 RON from BT to ING every month (transfer between own accounts).
            d = self.day(month, 26, 28)
            out = self.add(date=d, account="bt", currency="RON", amount=-money(1500), merchant="Own account (ING)", category="Transfer",
                           kind="transfer", raw=OWNER, channel="op", counterparty=ING_IBAN, note="Economii", recurring="savings")
            inc = self.add(date=d + dt.timedelta(days=1 if d.weekday() < 4 else 3), account="ing", currency="RON", amount=money(1500),
                           merchant="Own account (BT)", category="Transfer", kind="transfer", raw=OWNER, channel="op",
                           counterparty=BT_IBAN, note="Economii")
            out.pair, inc.pair = id(inc), id(out)
            # Interest on the ING savings.
            self.add(date=last_day(month), account="ing", currency="RON", amount=money(2 + m_idx * 1.1 + r.uniform(0, 2)), merchant="ING interest",
                     category="Other Income", kind="income", raw="DOBANDA", channel="interest", time="23:00:00")
            # The monthly BT fee.
            self.add(date=last_day(month), account="bt", currency="RON", amount=-money(5.00), merchant="Banca Transilvania", category="Fees",
                     kind="expense", raw="COMISION ADMINISTRARE CONT", channel="fee", recurring="bt_fee", time="23:30:00")
            # Cash.
            self.add(date=self.day(month, 1, 28), account="bt", currency="RON", amount=-money(self.pick([200, 300, 400, 500])), merchant="ATM",
                     category="Cash", kind="expense", raw=f"ATM BT {r.randint(1000, 9999)}", channel="atm")
            self.daily(month)
            self.occasional(month)
        self.trips()
        self.anomalies()
        self.fund_revolut()
        self.finish()

    def daily(self, month: dt.date):
        r = self.r
        groceries = [("Kaufland", "KAUFLAND"), ("Lidl", "LIDL DISCOUNT"), ("Mega Image", "MEGA IMAGE"), ("Carrefour", "CARREFOUR"), ("Profi", "PROFI")]
        cafes = [("Starbucks", "STARBUCKS"), ("Ted's Coffee", "TEDS COFFEE CO"), ("McDonald's", "MCDONALDS"), ("KFC", "KFC"), ("La Mama", "LA MAMA")]
        delivery = [("Glovo", "GLOVO*FOOD"), ("Tazz", "TAZZ BY EMAG"), ("Bolt Food", "BOLT.EU/O/")]
        d = month
        while d <= last_day(month) and d <= END:
            weekend = d.weekday() >= 5
            if r.random() < (0.45 if weekend else 0.30):
                name, raw = self.pick(groceries)
                amount = math.exp(r.gauss(math.log(120), 0.55))
                account = "ing" if r.random() < 0.2 else "bt"
                self.card(d, name, "Groceries", min(amount, 450), f"{raw} {r.randint(100, 9999):04d}", account=account)
            if r.random() < (0.25 if weekend else 0.15):
                name, raw = self.pick(cafes)
                self.card(d, name, "Restaurants", r.uniform(18, 140), raw, account="revolut" if r.random() < 0.4 else "bt")
            if r.random() < 0.12:
                name, raw = self.pick(delivery)
                suffix = f"{d:%y%m%d}{r.randint(1000, 9999)}" if raw.startswith("BOLT") else ""
                self.card(d, name, "Food Delivery", r.uniform(55, 160), raw + suffix, account="revolut" if r.random() < 0.5 else "bt",
                          channel="online")
            if r.random() < 0.35:
                name, raw = self.pick([("Bolt", "BOLT.EU/R/"), ("Uber", "UBER *TRIP")])
                suffix = f"{d:%y%m%d}{r.randint(1000, 9999)}" if raw.startswith("BOLT") else ""
                self.card(d, name, "Transport", r.uniform(14, 62), raw + suffix, account="revolut" if r.random() < 0.6 else "bt",
                          channel="online")
            d += dt.timedelta(days=1)

    def occasional(self, month: dt.date):
        r = self.r
        for _ in range(r.randint(1, 2)):
            name, raw = self.pick([("OMV", "OMV"), ("Petrom", "PETROM"), ("MOL", "MOL")])
            self.card(self.day(month, 1, 28), name, "Fuel", r.uniform(220, 360), f"{raw} {r.randint(1000, 9999)}")
        for _ in range(r.randint(1, 3)):
            name, raw, lo, hi = self.pick([("eMAG", "EMAG.RO", 60, 900), ("Altex", "ALTEX ROMANIA", 80, 1200), ("Decathlon", "DECATHLON", 50, 450),
                                           ("H&M", "H&M 0123", 60, 350), ("Zara", "ZARA AFI", 90, 450)])
            self.card(self.day(month, 1, 28), name, "Shopping", r.uniform(lo, hi), raw, channel="online" if name in ("eMAG", "Altex") else "pos")
        if r.random() < 0.8:
            name, raw = self.pick([("Catena", "FARMACIA CATENA"), ("Dr. Max", "DR.MAX"), ("Help Net", "HELP NET FARMA")])
            self.card(self.day(month, 1, 28), name, "Health", r.uniform(20, 180), f"{raw} {r.randint(10, 999)}")
        if r.random() < 0.15:
            self.card(self.day(month, 1, 28), "Dent Estet", "Health", 450.00, "CLINICA DENT ESTET")
        if r.random() < 0.5:
            self.card(self.day(month, 1, 28), "Cinema City", "Entertainment", r.uniform(40, 90), "CINEMA CITY AFI")
        if r.random() < 0.15:
            self.card(self.day(month, 1, 28), "Eventim", "Entertainment", r.uniform(150, 400), "EVENTIM.RO", channel="online")

    def trips(self):
        r = self.r
        # Barcelona, August 2025: flight booked in June, euros changed before leaving, paying in EUR there.
        self.card(dt.date(2025, 6, 14), "Wizz Air", "Travel", 890.00, "WIZZ AIR", account="revolut", channel="online", city="BUDAPEST")
        self.exchange(dt.date(2025, 8, 5), 2500.00)
        self.card(dt.date(2025, 8, 9), "Booking.com", "Travel", 612.40, "Booking.com", account="revolut", currency="EUR", channel="online",
                  city="AMSTERDAM")
        for day in range(10, 18):
            d = dt.date(2025, 8, day)
            for _ in range(r.randint(1, 3)):
                name = self.pick(["Cerveceria Catalana", "La Boqueria", "Bar Brutal", "Can Paixano"])
                self.card(d, name, "Restaurants", r.uniform(12, 65), name, account="revolut", currency="EUR", city="BARCELONA")
            if r.random() < 0.5:
                self.card(d, "TMB", "Transport", r.uniform(2.55, 12.15), "TMB METRO", account="revolut", currency="EUR", city="BARCELONA")
        # Vienna, May 2026, by train.
        self.exchange(dt.date(2026, 5, 6), 1500.00)
        self.card(dt.date(2026, 5, 7), "OBB", "Travel", 89.90, "OBB PERSONENVERKEHR", account="revolut", currency="EUR", channel="online",
                  city="WIEN")
        for day in range(8, 12):
            d = dt.date(2026, 5, day)
            for _ in range(r.randint(1, 2)):
                name = self.pick(["Figlmueller", "Naschmarkt", "Cafe Central"])
                self.card(d, name, "Restaurants", r.uniform(14, 58), name, account="revolut", currency="EUR", city="WIEN")

    def topup(self, d: dt.date, time: str, ron: int):
        out = self.add(date=d, account="bt", currency="RON", amount=-ron, merchant="Own account (Revolut)", category="Transfer",
                       kind="transfer", raw="REVOLUT**4321*", channel="online", city="DUBLIN", time=time)
        inc = self.add(date=d, account="revolut", currency="RON", amount=ron, merchant="Own account (BT)", category="Transfer",
                       kind="transfer", raw=f"Top-up by *{BT_CARD}", channel="topup", time=time)
        out.pair, inc.pair = id(inc), id(out)

    def fund_revolut(self):
        """Revolut cannot go below zero: an auto top-up from the BT card when the RON pocket runs low, and a
        RON-to-EUR exchange before a euro payment the EUR pocket cannot cover (as the app does)."""
        bal = dict(OPENINGS["revolut"])
        for e in sorted(list(self.events), key=lambda e: (e.date, e.time, e.amount)):
            if e.account != "revolut" or e.amount >= 0 or e.channel in ("topup",):
                if e.account == "revolut":
                    bal[e.currency] += e.amount
                continue
            at = dt.datetime.strptime(e.time, "%H:%M:%S")
            earlier = (at - dt.timedelta(minutes=1)).strftime("%H:%M:%S")
            # A top-up comes before the exchange it pays for.
            earliest = (at - dt.timedelta(minutes=2)).strftime("%H:%M:%S")
            if e.currency == "EUR" and bal["EUR"] + e.amount < 0 and e.channel != "exchange":
                need_eur = -(bal["EUR"] + e.amount) + money(50)
                ron = money(math.ceil(need_eur / 100 * self.eur_rate(e.date) * 1.002))
                if bal["RON"] - ron < money(50):
                    top = money(math.ceil((ron - bal["RON"]) / 100 / 100) * 100 + 300)
                    self.topup(e.date, earliest, top)
                    bal["RON"] += top
                self.exchange(e.date, ron / 100, time=earlier)
                bal["RON"] -= ron
                bal["EUR"] += self.events[-1].amount
            if e.currency == "RON" and bal["RON"] + e.amount < money(50):
                top = money(math.ceil(-(bal["RON"] + e.amount) / 100 / 100) * 100 + 300)
                self.topup(e.date, earliest, top)
                bal["RON"] += top
            bal[e.currency] += e.amount

    def exchange(self, d: dt.date, ron: float, time: str | None = None):
        rate = self.eur_rate(d) * 1.002  # Revolut's weekday markup
        eur = math.floor(ron / rate * 100) / 100
        out = self.add(date=d, account="revolut", currency="RON", amount=-money(ron), merchant="Revolut exchange", category="Exchange",
                       kind="exchange", raw="Exchanged to EUR", channel="exchange", time=time)
        inc = self.add(date=d, account="revolut", currency="EUR", amount=money(eur), merchant="Revolut exchange", category="Exchange",
                       kind="exchange", raw="Exchanged from RON", channel="exchange", time=out.time)
        out.pair, inc.pair = id(inc), id(out)

    def anomalies(self):
        # A double charge at eMAG.
        for _ in range(2):
            self.card(dt.date(2026, 2, 14), "eMAG", "Shopping", 349.99, "EMAG.RO", channel="online", anomaly="duplicate", time="19:41:07")
        # A grocery bill five times the usual.
        self.card(dt.date(2026, 4, 18), "Kaufland", "Groceries", 1980.00, "KAUFLAND 2091", anomaly="unusual_amount")
        # A surprise fee.
        self.add(date=dt.date(2026, 7, 3), account="bt", currency="RON", amount=-money(25.00), merchant="Banca Transilvania", category="Fees",
                 kind="expense", raw="COMISION RETRAGERE NUMERAR ALTE BANCI", channel="fee", anomaly="unexpected_fee")
        # A refund for a return at eMAG.
        self.add(date=dt.date(2026, 3, 2), account="bt", currency="RON", amount=money(219.90), merchant="eMAG", category="Shopping",
                 kind="income", raw="EMAG.RO", channel="refund")

    def finish(self):
        self.events = [e for e in self.events if START <= e.date <= END]
        key = {id(e): e for e in self.events}
        self.events.sort(key=lambda e: (e.date, e.time, e.account, e.currency, e.amount))
        for i, e in enumerate(self.events):
            e.id = i + 1
        for e in self.events:
            if e.pair is not None:
                other = key.get(e.pair)
                e.pair = other.id if other else None


# --- writing the statements -------------------------------------------------------------------------

def balances(events: list[Event], opening: int) -> list[int]:
    out, b = [], opening
    for e in events:
        b += e.amount
        out.append(b)
    return out


def bt_description(e: Event, rng: random.Random) -> str:
    d = e.date.strftime("%d/%m/%Y")
    value = fmt_ro(abs(e.amount))
    if e.channel == "pos":
        return (f"Plata la POS non-BT cu card VISA;POS {d} TID:{rng.randint(10000000, 99999999)} {e.raw} {e.city} RO;"
                f"valoare tranzactie: {value} RON RRN:{rng.randint(100000000000, 999999999999)} comision tranzactie 0,00 RON;")
    if e.channel == "online":
        return (f"Plata online cu card VISA;{e.raw} {e.city} {'IE' if e.city == 'DUBLIN' else 'RO'};"
                f"valoare tranzactie: {value} RON RRN:{rng.randint(100000000000, 999999999999)};")
    if e.channel == "op" and e.amount > 0:
        return f"Incasare OP - canal electronic;{e.raw};{e.counterparty};{e.note};REF: {rng.randint(10**15, 10**16 - 1)}"
    if e.channel == "op":
        return f"Plata OP - canal electronic;{e.raw};{e.counterparty};{e.note};REF: {rng.randint(10**15, 10**16 - 1)}"
    if e.channel == "atm":
        return f"Retragere numerar ATM;{e.raw} {e.city} RO;valoare tranzactie: {value} RON;"
    if e.channel == "refund":
        return f"Incasare Instant - Retur cumparaturi;{e.raw};valoare tranzactie: {value} RON;"
    if e.channel == "fee":
        return e.raw.capitalize()
    raise ValueError(e.channel)


def write_bt(events: list[Event], rng: random.Random, path: Path, opening: int):
    """BT24 'Extras de cont' CSV: an account header, then one row per transaction."""
    rows = [e for e in events if e.account == "bt"]
    bal = balances(rows, opening)
    lines = [
        "Banca Transilvania S.A.",
        f"Titular cont:,{OWNER}",
        f"Numar cont:,{BT_IBAN}",
        "Moneda:,RON",
        f"Perioada:,{START:%d.%m.%Y} - {END:%d.%m.%Y}",
        f"Sold initial:,\"{fmt_ro(opening)}\"",
        "",
        "Data tranzactie,Data valuta,Descriere,Referinta tranzactiei,Debit,Credit,Sold contabil",
    ]
    for e, b in zip(rows, bal):
        desc = bt_description(e, rng).replace('"', "'")
        debit = f'"{fmt_ro(-e.amount)}"' if e.amount < 0 else ""
        credit = f'"{fmt_ro(e.amount)}"' if e.amount > 0 else ""
        lines.append(f"{e.date:%Y-%m-%d},{e.date:%Y-%m-%d},\"{desc}\",{rng.randint(10**9, 10**10 - 1)},{debit},{credit},\"{fmt_ro(b)}\"")
        e.extra["description"] = desc
    lines.append(f"Sold final:,\"{fmt_ro(bal[-1] if bal else opening)}\"")
    path.write_text("\r\n".join(lines) + "\r\n", encoding="utf-8-sig")


def write_bt_pdf(events: list[Event], path: Path, opening: int, month: dt.date):
    """One month of the BT account as the PDF statement a branch would print."""
    rows = [e for e in events if e.account == "bt"]
    bal = balances(rows, opening)
    before = [b for e, b in zip(rows, bal) if e.date < month]
    start_balance = before[-1] if before else opening
    lines = [
        "BANCA TRANSILVANIA S.A.",
        "EXTRAS DE CONT",
        f"Titular: {OWNER}",
        f"Cont: {BT_IBAN}   Moneda: RON",
        f"Perioada: {month:%d.%m.%Y} - {last_day(month):%d.%m.%Y}",
        f"SOLD ANTERIOR {fmt_ro(start_balance)}",
        f"{'Data':<11}{'Descriere':<60}{'Debit':>14}{'Credit':>14}",
    ]
    final = start_balance
    for e, b in zip(rows, bal):
        if month <= e.date <= last_day(month):
            parts = [p for p in e.extra["description"].split(";") if p and not p.startswith("REF:")]
            text = " ".join(parts)
            # The description wraps onto the lines below, as on a printed statement.
            wrapped = textwrap.wrap(text, 58)[:4]
            debit = fmt_ro(-e.amount) if e.amount < 0 else ""
            credit = fmt_ro(e.amount) if e.amount > 0 else ""
            # Fixed columns: the amount's position says whether it is a debit or a credit.
            lines.append(f"{e.date:%d.%m.%Y} {wrapped[0]:<60}{debit:>14}{credit:>14}")
            for more in wrapped[1:]:
                lines.append(f"{'':11}{more}")
            final = b
    lines.append(f"SOLD FINAL {fmt_ro(final)}")
    pdfwrite.write(path, lines)


def ing_date(d: dt.date) -> str:
    return f"{d.day:02d} {MONTHS_RO[d.month - 1]} {d.year}"


def write_ing(events: list[Event], path: Path, opening: int, start: dt.date, end: dt.date):
    """ING Home'Bank CSV: every transaction spans several rows; only the first has the date and the amount."""
    rows = [e for e in events if e.account == "ing" and start <= e.date <= end]
    before = sum(e.amount for e in events if e.account == "ing" and e.date < start)
    lines = ['"Data","","Detalii tranzactie","","Debit","Credit"']
    for e in rows:
        debit = fmt_ro(-e.amount) if e.amount < 0 else ""
        credit = fmt_ro(e.amount) if e.amount > 0 else ""
        if e.channel == "pos":
            kind, details = "Cumparare POS", [f"Nr. card: ****{ING_CARD}", f"Terminal: {e.raw} {e.city} RO", f"Data: {e.date:%d-%m-%Y}"]
        elif e.channel == "direct_debit":
            kind, details = "Plata debit direct", [f"Beneficiar: {e.raw}", f"Referinta: {e.id:010d}"]
        elif e.channel == "op" and e.amount > 0:
            kind, details = "Incasare", [f"Ordonator: {e.raw}", f"Din contul: {e.counterparty}", f"Detalii: {e.note}"]
        elif e.channel == "interest":
            kind, details = "Dobanda", ["Dobanda cont economii"]
        else:
            raise ValueError(e.channel)
        lines.append(f'"{ing_date(e.date)}","","{kind}","","{debit}","{credit}"')
        for line in details:
            lines.append(f'"","","{line}","","",""')
        e.extra["description"] = "; ".join([kind] + details)
    final = opening + before + sum(e.amount for e in rows)
    lines.append(f'"","","Sold initial","","{fmt_ro(opening + before)}",""')
    lines.append(f'"","","Sold final","","{fmt_ro(final)}",""')
    path.write_text("\r\n".join(lines) + "\r\n", encoding="utf-8")


def write_revolut(events: list[Event], path: Path, opening: dict[str, int]):
    """Revolut account statement CSV: all currencies in one file, one balance per currency."""
    rows = [e for e in events if e.account == "revolut"]
    bal = dict(opening)
    lines = ["Type,Product,Started Date,Completed Date,Description,Amount,Fee,Currency,State,Balance"]
    for e in rows:
        bal[e.currency] += e.amount
        kind = {"topup": "TOPUP", "exchange": "EXCHANGE", "refund": "CARD_REFUND"}.get(e.channel, "CARD_PAYMENT")
        started = f"{e.date:%Y-%m-%d} {e.time}"
        completed = f"{e.date + dt.timedelta(days=0 if kind in ('TOPUP', 'EXCHANGE') else 1):%Y-%m-%d} 08:{e.id % 60:02d}:00"
        desc = e.raw.replace(",", " ")
        lines.append(f"{kind},Current,{started},{completed},{desc},{fmt_dot(e.amount)},0.00,{e.currency},COMPLETED,{fmt_dot(bal[e.currency])}")
        e.extra["description"] = desc
    # Revolut also lists what never went through; importers must skip it.
    lines.append(f"CARD_PAYMENT,Current,2026-09-12 21:03:11,,Steam,-59.99,0.00,RON,DECLINED,")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def write_rates(world: World, path: Path):
    lines = ["date,currency,ron"]
    for d in sorted(world.rates):
        lines.append(f"{d:%Y-%m-%d},EUR,{world.rates[d] / 10000:.4f}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def generate(out: Path, seed: int = 7) -> dict:
    world = World(seed)
    world.build()
    rng = random.Random(seed + 1)
    out.mkdir(parents=True, exist_ok=True)
    openings = OPENINGS
    write_bt(world.events, rng, out / "bt_2025-04_2026-09.csv", openings["bt"])
    write_bt_pdf(world.events, out / "bt_2026-09.pdf", openings["bt"], dt.date(2026, 9, 1))
    # Two ING exports that overlap by two weeks, as people actually download them.
    write_ing(world.events, out / "ing_2025-04_2025-12.csv", openings["ing"], START, dt.date(2025, 12, 31))
    write_ing(world.events, out / "ing_2025-12_2026-09.csv", openings["ing"], dt.date(2025, 12, 15), END)
    write_revolut(world.events, out / "revolut_2025-04_2026-09.csv", openings["revolut"])
    write_rates(world, out / "rates.csv")
    truth = {
        "owner": OWNER,
        "period": [START.isoformat(), END.isoformat()],
        "openings": openings,
        "accounts": {"bt": BT_IBAN, "ing": ING_IBAN, "revolut": REVOLUT_IBAN},
        "categories": CATEGORIES,
        "transactions": [
            {**{k: v for k, v in asdict(e).items() if k not in ("extra", "order")}, "date": e.date.isoformat(),
             "description": e.extra.get("description", "")}
            for e in world.events
        ],
        "rates": {d.isoformat(): r / 10000 for d, r in sorted(world.rates.items())},
    }
    (out / "truth.json").write_text(json.dumps(truth, indent=1, ensure_ascii=False), encoding="utf-8")
    return truth


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", default="testdata/synthetic")
    ap.add_argument("--seed", type=int, default=7)
    a = ap.parse_args()
    truth = generate(Path(a.out), a.seed)
    n = len(truth["transactions"])
    print(f"wrote {n} transactions over {MONTHS} months to {a.out}")


if __name__ == "__main__":
    main()
