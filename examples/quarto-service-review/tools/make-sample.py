#!/usr/bin/env python3
"""Generate the two CSVs this report reads.

Committed output rather than a build step: the report has to render on an
install with no route to anywhere, and a fixed seed means two people looking at
two installs are looking at the same quarter.
"""
import csv, random
from datetime import date, datetime, time, timedelta

rng = random.Random(20260701)

REGIONS = ["North", "South", "Midlands", "Ireland", "Benelux"]
SERVICES = [
    ("Ordering",        "Tier 1", 99.9),
    ("Payments",        "Tier 1", 99.95),
    ("Warehouse",       "Tier 1", 99.5),
    ("Customer portal", "Tier 2", 99.5),
    ("Reporting",       "Tier 2", 99.0),
    ("Notifications",   "Tier 2", 99.0),
    ("Partner API",     "Tier 2", 99.5),
    ("Internal wiki",   "Tier 3", 98.0),
    ("Expenses",        "Tier 3", 98.0),
    ("Fleet tracking",  "Tier 3", 98.5),
]
CAUSES = ["Change", "Capacity", "Dependency", "Configuration", "Hardware", "Unknown"]

start = date(2026, 4, 1)
days = (date(2026, 7, 1) - start).days

with open("data/services.csv", "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["service", "tier", "target_pct", "owner", "region"])
    owners = ["Commerce", "Payments", "Logistics", "Platform", "Workplace"]
    for i, (name, tier, target) in enumerate(SERVICES):
        w.writerow([name, tier, target, owners[i % len(owners)], REGIONS[i % len(REGIONS)]])

rows = []
for _ in range(148):
    svc, tier, _t = rng.choice(SERVICES)
    when = datetime.combine(start, time()) + timedelta(
        days=rng.randrange(days), hours=rng.randrange(24), minutes=rng.randrange(60))
    sev = rng.choices(["S1", "S2", "S3"], weights=[6, 26, 68])[0]
    base = {"S1": 240, "S2": 95, "S3": 35}[sev]
    minutes = max(6, int(rng.gauss(base, base * 0.55)))
    detect = max(1, int(rng.gauss(minutes * 0.18, minutes * 0.12)))
    cause = rng.choices(CAUSES, weights=[34, 16, 18, 14, 8, 10])[0]
    rows.append([
        f"INC-{2600 + len(rows):04d}", when.strftime("%Y-%m-%d %H:%M"), svc, tier, sev,
        REGIONS[rng.randrange(len(REGIONS))], cause, minutes, detect,
        "yes" if cause == "Change" and rng.random() < 0.72 else "no",
    ])
rows.sort(key=lambda r: r[1])

with open("data/incidents.csv", "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["id", "opened", "service", "tier", "severity", "region", "cause",
                "minutes", "detect_minutes", "change_related"])
    w.writerows(rows)

print("wrote", len(rows), "incidents")
