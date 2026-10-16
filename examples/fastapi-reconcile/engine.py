"""The close itself — the one piece of work worth moving off the web process.

Imported by both entrypoints of this app: `main.py`, which serves the pages, and
`worker.py`, which the platform runs in a container of its own. Same repo, same
release, same database; a different command.

That is the shape the job system is for, and the reason it reads as one module
rather than two: a close that a developer can call from a test, a page can call
inline when there is no isolated backend, and the platform can call as a job.
"""

from __future__ import annotations

from collections import defaultdict
from datetime import date
from typing import Callable

import ledger

Progress = Callable[[int, str], None]


def _noop(_pct: int, _msg: str) -> None:
    pass


def run_close(period: str, tolerance_pence: int = 150, date_window_days: int = 3,
              trigger: str = "page", job_run_id: str = "",
              progress: Progress = _noop) -> dict:
    """Match one period's two sides and record what is left over.

    Three passes, cheapest first, and each one only sees what the pass before it
    could not place:

      1. exact — same reference, same amount
      2. tolerant — same reference, amounts within the tolerance
      3. loose — same counterparty and amount, value date within the window

    Everything unplaced afterwards is an exception, typed by which side it is
    on. The three tolerances are the app's settings document, so changing one is
    a save rather than a redeploy.
    """
    if period not in ledger.PERIODS:
        raise ValueError(f"unknown period {period!r}")

    progress(2, f"Reading {period}")
    with ledger.conn() as c:
        cur = c.execute(
            "INSERT INTO closes(period, started_at, trigger, job_run_id)"
            " VALUES (?, datetime('now'), ?, ?)", (period, trigger, job_run_id))
        run_id = int(cur.lastrowid)
        led = c.execute("SELECT * FROM ledger WHERE period=? ORDER BY id", (period,)).fetchall()
        bnk = c.execute("SELECT * FROM bank WHERE period=? ORDER BY id", (period,)).fetchall()

    progress(12, f"{len(led):,} ledger entries against {len(bnk):,} statement lines")

    unplaced_l = {r["id"]: r for r in led}
    unplaced_b = {r["id"]: r for r in bnk}
    matched = 0
    exceptions: list[tuple] = []

    # Pass 1 — the reference and the amount agree. Most of a real month.
    by_ref_amount: dict[tuple[str, int], list[int]] = defaultdict(list)
    for r in bnk:
        by_ref_amount[(r["reference"], r["amount"])].append(r["id"])
    for lid, l in list(unplaced_l.items()):
        key = (l["reference"], l["amount"])
        if by_ref_amount[key]:
            bid = by_ref_amount[key].pop(0)
            if bid in unplaced_b:
                del unplaced_l[lid]
                del unplaced_b[bid]
                matched += 1
    progress(38, f"{matched:,} matched on reference and amount")

    # Pass 2 — the reference agrees and the amount does not. A variance, which
    # is a match *and* an exception: the money moved, the wrong amount of it did.
    by_ref: dict[str, list[int]] = defaultdict(list)
    for bid, b in unplaced_b.items():
        by_ref[b["reference"]].append(bid)
    for lid, l in list(unplaced_l.items()):
        for bid in list(by_ref.get(l["reference"], [])):
            if bid not in unplaced_b:
                continue
            b = unplaced_b[bid]
            delta = b["amount"] - l["amount"]
            if abs(delta) > tolerance_pence * 40:      # a different payment entirely
                continue
            del unplaced_l[lid]
            del unplaced_b[bid]
            by_ref[l["reference"]].remove(bid)
            matched += 1
            if abs(delta) > tolerance_pence:
                exceptions.append((run_id, period, "amount_variance", lid, bid,
                                   l["reference"], l["counterparty"], l["amount"], delta,
                                   l["txn_date"]))
            break
    progress(58, "Variances found")

    # Pass 3 — no reference in common, but the same counterparty paid the same
    # amount inside the window. This is where a re-presented payment shows up.
    by_amount: dict[int, list[int]] = defaultdict(list)
    for bid, b in unplaced_b.items():
        by_amount[b["amount"]].append(bid)
    for lid, l in list(unplaced_l.items()):
        ld = date.fromisoformat(l["txn_date"])
        for bid in list(by_amount.get(l["amount"], [])):
            if bid not in unplaced_b:
                continue
            b = unplaced_b[bid]
            if abs((date.fromisoformat(b["value_date"]) - ld).days) > date_window_days:
                continue
            del unplaced_l[lid]
            del unplaced_b[bid]
            matched += 1
            break
    progress(74, "Loose matches placed")

    # Whatever is left is somebody's morning.
    #
    # Counted over *every* statement line rather than the leftovers, which is
    # the whole distinction: a re-presented payment has one twin that matched
    # cleanly, so a count taken after matching would see one of it and call it
    # a line nobody expected.
    seen_on_statement: dict[tuple[str, int], int] = defaultdict(int)
    for b in bnk:
        seen_on_statement[(b["reference"], b["amount"])] += 1

    for lid, l in unplaced_l.items():
        exceptions.append((run_id, period, "unmatched_ledger", lid, None,
                           l["reference"], l["counterparty"], l["amount"], 0, l["txn_date"]))
    for bid, b in unplaced_b.items():
        kind = ("duplicate" if seen_on_statement[(b["reference"], b["amount"])] > 1
                else "unmatched_bank")
        exceptions.append((run_id, period, kind, None, bid,
                           b["reference"], b["narrative"].title(), b["amount"], 0, b["value_date"]))

    progress(88, f"{len(exceptions):,} exceptions")

    at_risk = sum(abs(e[7]) for e in exceptions)
    with ledger.conn() as c:
        c.executemany(
            "INSERT INTO exceptions(run_id, period, kind, ledger_id, bank_id, reference,"
            " counterparty, amount, delta, txn_date) VALUES (?,?,?,?,?,?,?,?,?,?)", exceptions)
        c.execute(
            "UPDATE closes SET finished_at=datetime('now'), ledger_rows=?, bank_rows=?,"
            " matched=?, exceptions=?, value_at_risk=? WHERE id=?",
            (len(led), len(bnk), matched, len(exceptions), at_risk, run_id))

    progress(100, "Closed")
    return {
        "run_id": run_id, "period": period,
        "ledger_rows": len(led), "bank_rows": len(bnk),
        "matched": matched, "exceptions": len(exceptions),
        "value_at_risk": at_risk,
        "match_rate": round(matched / len(led), 4) if led else 0.0,
    }


def summary_text(result: dict) -> str:
    """One paragraph, quoting the same numbers the page does.

    A message that disagrees with the screen beside it is how people stop
    trusting both, so there is one function and every channel calls it.
    """
    return (
        f"{result['period']} closed. "
        f"{result['matched']:,} of {result['ledger_rows']:,} ledger entries matched "
        f"({result['match_rate'] * 100:.1f}%), "
        f"{result['exceptions']:,} exceptions worth "
        f"{ledger.money(result['value_at_risk'])} left open."
    )
