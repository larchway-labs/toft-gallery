"""Nightly delivery digest — a batch job with no web server.

It answers one question each morning: how did yesterday's deliveries go? Then
it records the answer three ways — the run's log, its structured result, and an
output file — and, when REPORT_TO is set, emails it.

**Which day.** The day a run is *for*, not the day it happens to run. A
scheduled run reads its tick from ``toft.run.scheduled_for`` — the 06:00 run is
for yesterday even if it waited in a queue until 06:40 — and a re-run started
with ``toft job run digest --for 2026-10-06`` reports on the 5th. A run nobody
gave an instant to reports on the day before today.

**The email is optional, and when asked for it is not best-effort.** With
REPORT_TO unset the digest is recorded and nothing is sent. With it set, a send
the platform refuses — email not attached to this app, say — fails the run, so
the app's owners are told rather than the report silently going nowhere.
"""

from __future__ import annotations

import datetime as dt
import html
import os
import sys

import toft

from data import Delivery, deliveries_on

TITLE = os.environ.get("REPORT_TITLE", "Nightly delivery digest")


def report_day() -> dt.date:
    """The day this run reports on: the day before the instant it is for."""
    run = toft.run
    at = run.scheduled_for if run else None
    if at is None:
        at = dt.datetime.now(dt.timezone.utc)
    return (at - dt.timedelta(days=1)).date()


def summarise(day: dt.date, rows: list[Delivery]) -> dict:
    late = [r for r in rows if r.minutes_late > 0]
    by_region: dict[str, dict[str, int]] = {}
    for r in rows:
        region = by_region.setdefault(r.region, {"deliveries": 0, "late": 0})
        region["deliveries"] += 1
        region["late"] += 1 if r.minutes_late > 0 else 0
    on_time = (len(rows) - len(late)) / len(rows) if rows else 1.0
    worst = max(late, key=lambda r: r.minutes_late, default=None)
    return {
        "day": day.isoformat(),
        "deliveries": len(rows),
        "late": len(late),
        "on_time_rate": round(on_time, 4),
        "worst_delay_minutes": worst.minutes_late if worst else 0,
        "worst_delay_order": worst.order_id if worst else "",
        "regions": dict(sorted(by_region.items())),
    }


def as_text(s: dict) -> str:
    lines = [
        f"{TITLE} — {s['day']}",
        "",
        f"Deliveries   {s['deliveries']}",
        f"Late         {s['late']}",
        f"On time      {s['on_time_rate']:.1%}",
    ]
    if s["worst_delay_order"]:
        lines.append(f"Worst delay  {s['worst_delay_minutes']} min ({s['worst_delay_order']})")
    lines += ["", "By region"]
    for region, r in s["regions"].items():
        lines.append(f"  {region:<10} {r['deliveries']:>4} deliveries, {r['late']:>3} late")
    return "\n".join(lines)


def as_html(s: dict) -> str:
    rows = "".join(
        f"<tr><td>{html.escape(region)}</td><td align='right'>{r['deliveries']}</td>"
        f"<td align='right'>{r['late']}</td></tr>"
        for region, r in s["regions"].items()
    )
    worst = (
        f"<p>Worst delay: <strong>{s['worst_delay_minutes']} min</strong> "
        f"({html.escape(s['worst_delay_order'])})</p>"
        if s["worst_delay_order"]
        else ""
    )
    return (
        f"<h2>{html.escape(TITLE)} — {s['day']}</h2>"
        f"<p><strong>{s['deliveries']}</strong> deliveries, <strong>{s['late']}</strong> late — "
        f"<strong>{s['on_time_rate']:.1%}</strong> on time.</p>"
        f"{worst}"
        "<table cellpadding='4' cellspacing='0' border='1'>"
        "<tr><th align='left'>Region</th><th>Deliveries</th><th>Late</th></tr>"
        f"{rows}</table>"
    )


def recipients() -> list[str]:
    return [a.strip() for a in os.environ.get("REPORT_TO", "").split(",") if a.strip()]


def main() -> int:
    day = report_day()
    summary = summarise(day, deliveries_on(day))
    text = as_text(summary)
    print(text)

    run = toft.run
    if run is None:
        # Run by hand, off the platform: the report above is the whole output.
        return 0
    run.set_result(summary)
    if run.output_uri:
        run.write_output_json(summary)

    to = recipients()
    if not to:
        print("\nREPORT_TO is not set, so the digest was recorded and not emailed.")
        return 0
    try:
        sent = toft.notify.email(
            to=to,
            subject=f"{TITLE} — {summary['day']}",
            text=text,
            html=as_html(summary),
            # One digest per day however many times the day is re-run.
            idempotency_key=f"digest-{summary['day']}",
        )
    except toft.ToftError as err:
        print(f"\nThe digest could not be emailed: {err}", file=sys.stderr)
        return 1
    print(f"\nEmailed to {', '.join(to)} (send {sent.id}).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
