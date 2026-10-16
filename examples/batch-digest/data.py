"""The deliveries the digest reports on.

Generated rather than fetched, so the example runs on any install with nothing
to connect to: a fixed seed per day makes every day's numbers the same on every
run, which is what lets a re-run of a past day be checked against the first.
Replace ``deliveries_on`` with a query against your own system.
"""

from __future__ import annotations

import datetime as dt
import random
from dataclasses import dataclass

REGIONS = ["North", "South", "East", "West", "Central"]


@dataclass(frozen=True)
class Delivery:
    order_id: str
    region: str
    minutes_late: int


def deliveries_on(day: dt.date) -> list[Delivery]:
    rng = random.Random(day.toordinal())
    count = rng.randint(140, 260)
    out = []
    for i in range(count):
        late = rng.random() < 0.12
        out.append(Delivery(
            order_id=f"ORD-{day:%y%m%d}-{i + 1:04d}",
            region=rng.choice(REGIONS),
            minutes_late=rng.randint(5, 180) if late else 0,
        ))
    return out
