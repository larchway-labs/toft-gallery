"""What `toft.toml` says about the close, read by the app that declared it.

The platform reconciles the job set to that file on every deploy, so the file in
this release *is* the declaration — the app needs no API to learn its own
schedule, limits or parameters, and the Runs tab works off-platform too.

The one thing computed here is the next firing. It is a small cron evaluator
for the five plain fields — numbers, ranges, lists, steps and ``*`` — in the
declared zone, which is all a declaration this app wrote needs. It is the page's
estimate; the platform's scheduler is the authority, and a paused schedule is
the one thing this file cannot know about.
"""

from __future__ import annotations

import datetime
import os
import tomllib
from dataclasses import dataclass, field
from zoneinfo import ZoneInfo

HERE = os.path.dirname(os.path.abspath(__file__))
JOB = "close"


@dataclass
class Declaration:
    schedule: str = ""
    timezone: str = "UTC"
    overlap: str = "queue"
    timeout: str = ""
    memory_mb: int = 0
    notify: str = "none"
    choices: list[str] = field(default_factory=list)
    next_at: datetime.datetime | None = None


def read(now: datetime.datetime | None = None) -> Declaration:
    try:
        with open(os.path.join(HERE, "toft.toml"), "rb") as fh:
            job = tomllib.load(fh).get("jobs", {}).get(JOB, {})
    except (OSError, tomllib.TOMLDecodeError):
        return Declaration()
    timeout = job.get("timeout", "")
    d = Declaration(
        schedule=job.get("schedule", ""),
        timezone=job.get("timezone", "UTC"),
        overlap=job.get("overlap", "queue"),
        timeout=f"{timeout // 60}m" if isinstance(timeout, int) else str(timeout),
        memory_mb=int(job.get("memory", 0) or 0),
        notify=job.get("notify", "none"),
        choices=list(job.get("params", {}).get("period", {}).get("choices", [])),
    )
    if d.schedule:
        try:
            d.next_at = next_tick(d.schedule, ZoneInfo(d.timezone), now)
        except (ValueError, KeyError):
            d.next_at = None
    return d


def _field(spec: str, lo: int, hi: int) -> set[int]:
    out: set[int] = set()
    for part in spec.split(","):
        rng, _, step = part.partition("/")
        if rng == "*":
            a, b = lo, hi
        elif "-" in rng:
            a, b = (int(x) for x in rng.split("-", 1))
        else:
            a = b = int(rng)
        out.update(range(a, b + 1, int(step) if step else 1))
    if not out or min(out) < lo or max(out) > hi:
        raise ValueError(spec)
    return out


def next_tick(expr: str, zone: ZoneInfo, now: datetime.datetime | None = None) -> datetime.datetime | None:
    """The first wall-clock time after ``now`` that ``expr`` names, in ``zone``.

    Walks days, then hours, then minutes, so a monthly schedule costs a few
    dozen steps rather than forty thousand. Day-of-month and day-of-week
    combine the way cron does: either matches when both are restricted.
    """
    mi, hr, dom, mon, dow = expr.split()
    minutes, hours = _field(mi, 0, 59), _field(hr, 0, 23)
    days, months = _field(dom, 1, 31), _field(mon, 1, 12)
    weekdays = {d % 7 for d in _field(dow, 0, 7)}
    both = dom != "*" and dow != "*"

    now = (now or datetime.datetime.now(datetime.timezone.utc)).astimezone(zone)
    day = now.date()
    for _ in range(366 * 5):
        if day.month in months:
            hit_dom = day.day in days
            hit_dow = (day.isoweekday() % 7) in weekdays
            if (hit_dom or hit_dow) if both else (hit_dom and hit_dow):
                for h in sorted(hours):
                    for m in sorted(minutes):
                        at = datetime.datetime(day.year, day.month, day.day, h, m, tzinfo=zone)
                        if at > now:
                            return at
        day += datetime.timedelta(days=1)
    return None
