# Nightly Digest

A **batch app**: a Python program with no web server, run by Toft on a
schedule, that can email its report.

Every morning at 06:00 London time it works out how yesterday's deliveries
went, then records the answer three ways — the run's log, its structured
result, and an output file — and, if you ask it to, emails it.

There is no page to open. The app's work is its one job, `digest`, and
everything about it is on the app's **Automation** tab.

## What it shows off

| | |
|---|---|
| **A batch app** | `[batch]` in `toft.toml`. Toft installs the dependencies, starts nothing, and the app is *Ready*. Restart and Stop do not apply — there is nothing running between runs. |
| **A zoned schedule** | `schedule = "0 6 * * *"` with `timezone = "Europe/London"` runs at 06:00 on the wall clock, through the clock changes. |
| **The day a run is for** | `toft.run.scheduled_for` is the schedule's tick, so the 06:00 run reports on yesterday even if it started late. Re-run a past day with `toft job run digest --for 2026-10-06`. |
| **An alert when it goes quiet** | `expect_within = "26h"`: the app's owners are told if a day goes by without a successful digest. |
| **Email from a job** | `toft.notify.email(...)` works inside a job, through the email integration an administrator attaches to the app. Every send is in the app's send log. |

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `REPORT_TO` | no | — | Comma-separated addresses to email the digest to. Unset, nothing is sent. |
| `REPORT_TITLE` | no | `Nightly delivery digest` | The report's heading and the email's subject. |

**The email is optional, and when asked for it is not best-effort.** With
`REPORT_TO` set, a send the platform refuses — most often because email is not
attached to the app — fails the run, so the app's owners hear about it rather
than the report quietly going nowhere. To send:

1. An administrator attaches **Email** on the app's **Integrations** tab.
2. Set `REPORT_TO` on the app's **Settings** tab.

## Running it

- **On a schedule** — nothing to do; it is declared in `toft.toml`.
- **Now** — *Run* on the Automation tab, or `toft job run digest`.
- **For a past day** — *Run for a time* on the Automation tab, or
  `toft job run digest --for 2026-10-06`.
- **On your laptop** — `python digest.py` prints the report and stops; with no
  platform around it records and sends nothing.

## Making it yours

The data is generated (`data.py`) so the example runs anywhere with nothing to
connect to, and a fixed seed per day keeps a re-run of a past day identical to
the first. Replace `deliveries_on` with a query against your own system, and
the rest — the schedule, the day it is for, the result, the email — stays as
it is.

Requires Toft 0.6.4 or later: batch apps and email from a job are new
there.
