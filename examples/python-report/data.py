"""The report's data.

Deterministic and hand-written: a report example whose numbers move on every
refresh is one nobody can check. Replace these three functions with a database
query — the shape they return is the whole interface `app.py` depends on.
"""

import os

REPORT_TITLE = os.getenv("REPORT_TITLE", "Weekly delivery report")

_WEEKS = [
    {"week": "2026-05-04", "deploys": 41, "failed_builds": 7, "median_build_minutes": 4.2},
    {"week": "2026-05-11", "deploys": 55, "failed_builds": 6, "median_build_minutes": 3.9},
    {"week": "2026-05-18", "deploys": 48, "failed_builds": 9, "median_build_minutes": 4.6},
    {"week": "2026-05-25", "deploys": 33, "failed_builds": 4, "median_build_minutes": 4.1},
    {"week": "2026-06-01", "deploys": 62, "failed_builds": 5, "median_build_minutes": 3.4},
    {"week": "2026-06-08", "deploys": 58, "failed_builds": 3, "median_build_minutes": 3.1},
    {"week": "2026-06-15", "deploys": 71, "failed_builds": 4, "median_build_minutes": 3.3},
    {"week": "2026-06-22", "deploys": 66, "failed_builds": 2, "median_build_minutes": 2.9},
]

_INCIDENTS = [
    {"date": "2026-05-19", "title": "Search indexer stopped consuming its queue", "severity": "high", "minutes": 96},
    {"date": "2026-05-27", "title": "Certificate renewal missed on the staging domain", "severity": "low", "minutes": 40},
    {"date": "2026-06-03", "title": "Build queue stranded behind one long Node build", "severity": "medium", "minutes": 63},
    {"date": "2026-06-19", "title": "Notification relay refused connections for 12 minutes", "severity": "medium", "minutes": 12},
]


def weeks():
    return [dict(row) for row in _WEEKS]


def incidents():
    return [dict(row) for row in _INCIDENTS]


def summary():
    """The tiles at the top: this half of the period against the previous half.

    Compared within the same window on purpose. A "vs last month" figure on a
    report that only holds eight weeks is a number the reader cannot verify
    from anything on the page.
    """
    rows = _WEEKS
    half = len(rows) // 2
    earlier, later = rows[:half], rows[half:]

    def total(section, key):
        return sum(r[key] for r in section)

    def mean(section, key):
        return sum(r[key] for r in section) / (len(section) or 1)

    deploys_now, deploys_before = total(later, "deploys"), total(earlier, "deploys")
    failed_now, failed_before = total(later, "failed_builds"), total(earlier, "failed_builds")
    build_now, build_before = mean(later, "median_build_minutes"), mean(earlier, "median_build_minutes")
    recovery = mean(_INCIDENTS, "minutes") if _INCIDENTS else 0

    return [
        {
            "label": "Deploys",
            "value": str(deploys_now),
            "note": _delta(deploys_now, deploys_before) + " vs the previous four weeks",
            "direction": "up" if deploys_now >= deploys_before else "down",
        },
        {
            "label": "Failed builds",
            "value": str(failed_now),
            "note": _delta(failed_now, failed_before) + " vs the previous four weeks",
            "direction": "up" if failed_now <= failed_before else "down",
        },
        {
            "label": "Median build",
            "value": "%.1f min" % build_now,
            "note": _delta(build_now, build_before) + " vs the previous four weeks",
            "direction": "up" if build_now <= build_before else "down",
        },
        {
            "label": "Mean recovery",
            "value": "%d min" % round(recovery),
            "note": "%d incidents in the period" % len(_INCIDENTS),
            "direction": "up",
        },
    ]


def _delta(now, before):
    if not before:
        return "no prior figure"
    pct = (now - before) / before * 100
    return "%+.0f%%" % pct
