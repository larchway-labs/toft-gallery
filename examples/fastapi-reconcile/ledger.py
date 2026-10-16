"""The register: two sides of a month, and the exceptions between them.

Embedded SQLite, on the most durable path this install offers. Seeded from a
fixed random seed on an empty database, so two people looking at two installs
are looking at the same three months and can talk about row 4021 and mean the
same row.
"""

from __future__ import annotations

import os
import random
import sqlite3
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import date, timedelta
from pathlib import Path
from typing import Iterator

import platform_seam as seam

PERIODS = ("2026-04", "2026-05", "2026-06")

COUNTERPARTIES = [
    ("Ardent Freight", "5100 Logistics"),
    ("Brindle & Co", "6200 Professional fees"),
    ("Calder Utilities", "6410 Utilities"),
    ("Dunmore Print", "6110 Marketing"),
    ("Eastgate Property", "6010 Rent"),
    ("Fenwick Software", "6310 Software"),
    ("Greyline Travel", "6210 Travel"),
    ("Halloran Supplies", "5200 Consumables"),
    ("Ivory Data", "6320 Data services"),
    ("Kestrel Insurance", "6420 Insurance"),
    ("Lomond Hardware", "5300 Equipment"),
    ("Marchmont Legal", "6220 Legal"),
    ("Northbank Payroll", "7000 Payroll"),
    ("Orrell Catering", "6120 Hospitality"),
    ("Pellew Maritime", "5110 Shipping"),
    ("Quarry Cloud", "6330 Hosting"),
]

# The four ways a line ends up on the exception list. The words are the
# accountant's, not the platform's, and they are what the table filters on.
KIND_LABELS = {
    "unmatched_ledger": "In the ledger, not on the statement",
    "unmatched_bank": "On the statement, not in the ledger",
    "amount_variance": "Matched, amounts differ",
    "duplicate": "Paid twice",
}
KIND_TONES = {
    "unmatched_ledger": "att",
    "unmatched_bank": "dan",
    "amount_variance": "att",
    "duplicate": "dan",
}

SCHEMA = """
CREATE TABLE IF NOT EXISTS ledger (
  id          INTEGER PRIMARY KEY,
  period      TEXT NOT NULL,
  txn_date    TEXT NOT NULL,
  reference   TEXT NOT NULL,
  counterparty TEXT NOT NULL,
  account     TEXT NOT NULL,
  amount      INTEGER NOT NULL          -- pence, always
);
CREATE TABLE IF NOT EXISTS bank (
  id          INTEGER PRIMARY KEY,
  period      TEXT NOT NULL,
  value_date  TEXT NOT NULL,
  reference   TEXT NOT NULL,
  narrative   TEXT NOT NULL,
  amount      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS exceptions (
  id          INTEGER PRIMARY KEY,
  run_id      INTEGER NOT NULL,
  period      TEXT NOT NULL,
  kind        TEXT NOT NULL,
  ledger_id   INTEGER,
  bank_id     INTEGER,
  reference   TEXT NOT NULL,
  counterparty TEXT NOT NULL,
  amount      INTEGER NOT NULL,
  delta       INTEGER NOT NULL DEFAULT 0,
  txn_date    TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'open',   -- open | accepted | written_off
  note        TEXT NOT NULL DEFAULT '',
  decided_at  TEXT
);
CREATE TABLE IF NOT EXISTS closes (
  id          INTEGER PRIMARY KEY,
  period      TEXT NOT NULL,
  started_at  TEXT NOT NULL,
  finished_at TEXT,
  trigger     TEXT NOT NULL,        -- page | job | schedule
  job_run_id  TEXT NOT NULL DEFAULT '',
  ledger_rows INTEGER NOT NULL DEFAULT 0,
  bank_rows   INTEGER NOT NULL DEFAULT 0,
  matched     INTEGER NOT NULL DEFAULT 0,
  exceptions  INTEGER NOT NULL DEFAULT 0,
  value_at_risk INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS ix_exc_period ON exceptions(period, status);
CREATE INDEX IF NOT EXISTS ix_closes_period ON closes(period, id DESC);
"""


@dataclass(frozen=True)
class Where:
    """Where the register landed, and what that costs.

    Said out loud on the Platform tab because it is the single most surprising
    thing about running a stateful app on a platform: on a shared-mode install
    with no storage attached, the database is on scratch and a redeploy starts
    the month again.
    """

    path: Path
    source: str
    durable: bool
    detail: str


_where: Where | None = None


def where() -> Where:
    """Resolve the database location once, most durable first."""
    global _where
    if _where is not None:
        return _where

    override = os.environ.get("RECONCILE_DATA_DIR")
    if override:
        _where = Where(Path(override) / "reconcile.db", "RECONCILE_DATA_DIR", True,
                       "An operator named this directory. Whatever it is mounted on decides.")
    elif (mount := seam.first_writable_mount()) is not None:
        _where = Where(Path(mount.path) / "reconcile.db", f"storage: {mount.name}", True,
                       "An attached storage mount. Survives redeploys, rollbacks and a moved workload.")
    elif (scratch := seam.scratch_dir()) is not None:
        _where = Where(scratch / "reconcile.db", "TOFT_SCRATCH_DIR", False,
                       "Scratch is the app's own corner of the disk and is wiped on every "
                       "deploy and restart. The three months below are seeded, so this is fine "
                       "for an example and would not be for a real close.")
    else:
        _where = Where(Path("./data/reconcile.db"), "./data", True,
                       "Running outside Toft, so the register is beside the source.")

    _where.path.parent.mkdir(parents=True, exist_ok=True)
    return _where


@contextmanager
def conn() -> Iterator[sqlite3.Connection]:
    c = sqlite3.connect(where().path, timeout=10, isolation_level=None)
    c.row_factory = sqlite3.Row
    c.execute("PRAGMA journal_mode=WAL")
    c.execute("PRAGMA foreign_keys=ON")
    try:
        yield c
    finally:
        c.close()


def init() -> None:
    with conn() as c:
        c.executescript(SCHEMA)
        if c.execute("SELECT COUNT(*) FROM ledger").fetchone()[0] == 0:
            _seed(c)


# ── the seed ────────────────────────────────────────────────────────────────
#
# A fixed seed, and every anomaly is planted rather than hoped for: an example
# whose interesting rows depend on the random number generator is an example
# that is boring on somebody else's screen.

def _seed(c: sqlite3.Connection) -> None:
    rng = random.Random(20260401)
    lid = bid = 0

    for period in PERIODS:
        y, m = (int(p) for p in period.split("-"))
        first = date(y, m, 1)
        days = (date(y + (m == 12), (m % 12) + 1, 1) - first).days
        n = rng.randint(132, 168)

        for _ in range(n):
            lid += 1
            cp, account = rng.choice(COUNTERPARTIES)
            when = first + timedelta(days=rng.randrange(days))
            amount = rng.choice([
                rng.randrange(4_50, 480_00),
                rng.randrange(480_00, 3_400_00),
                rng.randrange(3_400_00, 18_000_00),
            ])
            ref = f"{cp[:3].upper()}-{period[2:4]}{period[5:7]}-{lid:04d}"
            c.execute(
                "INSERT INTO ledger(id, period, txn_date, reference, counterparty, account, amount)"
                " VALUES (?,?,?,?,?,?,?)",
                (lid, period, when.isoformat(), ref, cp, account, amount))

            # The bank's side, with the four planted anomalies.
            roll = rng.random()
            if roll < 0.050:
                continue                                    # never cleared
            drift = timedelta(days=rng.choice([0, 0, 0, 1, 1, 2]))
            bank_amount = amount
            if roll < 0.115:
                # A spread on purpose: the small ones sit inside the default
                # tolerance and the large ones do not, so moving the tolerance on
                # the Settings tab visibly changes the exception list rather than
                # being a number with no consequence.
                bank_amount = amount + rng.choice(
                    [-85, -42, 96, 137, -3_75, 8_40, -14_20, 45_00, -112_60])
            bid += 1
            c.execute(
                "INSERT INTO bank(id, period, value_date, reference, narrative, amount)"
                " VALUES (?,?,?,?,?,?)",
                (bid, period, (when + drift).isoformat(), ref,
                 f"{cp.upper()} PAYMENT {ref[-4:]}", bank_amount))
            if roll > 0.972:                                # and the same one again
                bid += 1
                c.execute(
                    "INSERT INTO bank(id, period, value_date, reference, narrative, amount)"
                    " VALUES (?,?,?,?,?,?)",
                    (bid, period, (when + drift + timedelta(days=1)).isoformat(), ref,
                     f"{cp.upper()} PAYMENT {ref[-4:]} (RE-PRESENTED)", bank_amount))

        # A handful of statement lines nobody in the ledger is expecting: bank
        # charges, a reversal, an unidentified credit.
        for narrative, amount in (
            ("BANK CHARGES", rng.randrange(18_00, 64_00)),
            ("FX ADJUSTMENT", rng.randrange(3_00, 41_00)),
            ("UNIDENTIFIED CREDIT", -rng.randrange(120_00, 900_00)),
            ("STANDING ORDER 4471", rng.randrange(240_00, 1_800_00)),
            ("CARD ACQUIRER SETTLEMENT", rng.randrange(80_00, 620_00)),
        ):
            bid += 1
            when = first + timedelta(days=rng.randrange(days))
            c.execute(
                "INSERT INTO bank(id, period, value_date, reference, narrative, amount)"
                " VALUES (?,?,?,?,?,?)",
                (bid, period, when.isoformat(), f"BNK-{bid:04d}", narrative, amount))


# ── reads the pages want ────────────────────────────────────────────────────

def periods() -> list[str]:
    return list(PERIODS)


def latest_close(period: str) -> sqlite3.Row | None:
    with conn() as c:
        return c.execute(
            "SELECT * FROM closes WHERE period=? AND finished_at IS NOT NULL"
            " ORDER BY id DESC LIMIT 1", (period,)).fetchone()


def closes(limit: int = 25) -> list[sqlite3.Row]:
    with conn() as c:
        return c.execute("SELECT * FROM closes ORDER BY id DESC LIMIT ?", (limit,)).fetchall()


def exceptions(period: str, kind: str = "", status: str = "") -> list[sqlite3.Row]:
    """The open list for the newest finished close of a period."""
    close = latest_close(period)
    if close is None:
        return []
    sql = "SELECT * FROM exceptions WHERE run_id=?"
    args: list = [close["id"]]
    if kind:
        sql += " AND kind=?"
        args.append(kind)
    if status:
        sql += " AND status=?"
        args.append(status)
    sql += " ORDER BY ABS(amount) DESC, id"
    with conn() as c:
        return c.execute(sql, args).fetchall()


def exception(exc_id: int) -> sqlite3.Row | None:
    with conn() as c:
        return c.execute("SELECT * FROM exceptions WHERE id=?", (exc_id,)).fetchone()


def decide(exc_id: int, status: str, note: str) -> bool:
    if status not in ("open", "accepted", "written_off"):
        raise ValueError("status is open, accepted or written_off")
    with conn() as c:
        cur = c.execute(
            "UPDATE exceptions SET status=?, note=?, decided_at=datetime('now') WHERE id=?",
            (status, note[:400], exc_id))
        return cur.rowcount > 0


def counts(period: str) -> dict:
    close = latest_close(period)
    if close is None:
        return {"open": 0, "accepted": 0, "written_off": 0, "by_kind": {}, "at_risk": 0}
    with conn() as c:
        rows = c.execute(
            "SELECT status, COUNT(*) n FROM exceptions WHERE run_id=? GROUP BY status",
            (close["id"],)).fetchall()
        kinds = c.execute(
            "SELECT kind, COUNT(*) n, SUM(ABS(amount)) v FROM exceptions"
            " WHERE run_id=? GROUP BY kind", (close["id"],)).fetchall()
        risk = c.execute(
            "SELECT COALESCE(SUM(ABS(amount)),0) FROM exceptions WHERE run_id=? AND status='open'",
            (close["id"],)).fetchone()[0]
    out = {"open": 0, "accepted": 0, "written_off": 0}
    for r in rows:
        out[r["status"]] = r["n"]
    out["by_kind"] = {r["kind"]: {"n": r["n"], "value": r["v"] or 0} for r in kinds}
    out["at_risk"] = risk
    return out


def daily_series(period: str) -> tuple[list[int], list[str]]:
    """Statement value per day, for the column chart on the Overview."""
    with conn() as c:
        rows = c.execute(
            "SELECT value_date, SUM(ABS(amount)) v FROM bank WHERE period=?"
            " GROUP BY value_date ORDER BY value_date", (period,)).fetchall()
    return [int(r["v"]) for r in rows], [r["value_date"][-2:] for r in rows]


def money(pence: int | None) -> str:
    if pence is None:
        return "—"
    sign = "-" if pence < 0 else ""
    return f"{sign}£{abs(pence) / 100:,.2f}"
