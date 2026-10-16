# Reconcile

A month-end reconciliation desk: two sides of a month, matched three ways, and
the leftovers put in front of somebody who can decide about them.

FastAPI and uvicorn, embedded SQLite, no npm build, no CDN, no chart library.
The page is a client of the same JSON API your tooling would call, and the API
tab is generated from this app's own OpenAPI document at page load — so it works
on an install with no route to the internet.

This is the example to read if you want to see **an app using the platform's own
capabilities** rather than just being hosted by it. Everything below is in
`platform_seam.py`, in about two hundred lines, and every call in it degrades:
run this on an install with none of it attached and the page still works.

## The six tabs

| Tab | What it is |
|---|---|
| **Overview** | The close in four numbers, statement value by day, and what did not match. |
| **Exceptions** | The work: sortable, filterable, and two buttons that need a role to press. |
| **Runs** | Closes that ran as background jobs, and who they were attributed to. |
| **Settings** | The tolerances, stored in this app's one JSON document, saved with a version. |
| **Platform** | What Toft told this app about itself, and what the app did with it. |
| **API** | Nine routes, read from `/openapi.json`. |

## What it demonstrates

**The app's one JSON document.** The tolerance and the date window live in
`PUT /api/v1/app/data` — 256 KiB, no keys, no queries, and it outlives a
restart, a redeploy and a rollback. It needs nothing switched on: no attachment,
no capability, no administrator. Saving sends `If-Match`, so a second person
with the page open gets a **409 naming the version that is stored** instead of
silently losing their edit.

**Background jobs.** A close is a *different entrypoint of this same
repository*: `worker.py`, declared as `[jobs.close]` in `toft.toml`, run by
Toft in a container of its own with a memory limit, a deadline and a
durable record. The page returns as soon as it is queued. On an install with no
isolated backend the platform says so by name and the app offers to run it in
the web process instead — which is exactly the thing the job system exists to
avoid, and the page says that too.

Started with `toft.viewer(headers).jobs` rather than `toft.jobs`, which is the whole
decision: the first attributes the run to the person looking at the page, the
second to the app. Two objects rather than a flag, so attribution cannot be lost
by forgetting.

**The caller's role.** `toft.caller(headers)` reads a header Toft signs per
app and per workload start with a 120-second life, and verifies it *in this
process* against a key the platform put in the environment. No round trip, no
administrator switch, and nothing on the same loopback interface can forge it.
It is what gates the two buttons that change something — and it is why an `lp_`
app key minted carrying `viewer` can read the exception list and cannot decide
one.

**Viewer identity.** A different question, and a different call: `toft.who()`
gives a name, an email and groups, needs a round trip, and needs an
administrator to have switched it on. A role names nobody, which is why the two
are not one call. Reach for the role to *refuse*; reach for identity to
*describe*.

**Integrations.** The summary goes to Slack or email if an administrator
attached either. This app supplies the message and nothing else: it never holds
the bot token, never names the channel, and cannot set the address the mail
comes from — asking for any of those is a `400` from the platform that names the
rule.

**Storage.** The register lands on the most durable path this install offers —
a named mount, else scratch. The Platform tab says which, and what it costs: on
a shared-mode install with no storage attached, a redeploy starts the month
again.

**Scheduled tasks.** `POST /tasks/nightly-close` is a path a task fires at. A
task is an HTTP request to a path the app published, never a command, which is
why it needs nothing switched on. Toft signs the firing with the same role
header a page load carries, so the endpoint verifies it exactly as every other
one does; `system` is the schedule firing on its own, and a person pressing
*Run now* arrives as themselves.

## The matching

Three passes, cheapest first, each seeing only what the one before could not
place:

1. **exact** — same reference, same amount
2. **tolerant** — same reference, amounts within the tolerance. A variance is a
   match *and* an exception: the money moved, the wrong amount of it did
3. **loose** — same amount, value date inside the window

Four kinds of leftover and no fifth: in the ledger and not on the statement, on
the statement and not in the ledger, matched with a variance, and paid twice.

## The data is seeded, and meant to be

Three months, about 460 ledger entries and 480 statement lines, from a fixed
random seed — so two people on two installs are looking at the same months and
can talk about `QUA-2606-0333` and mean the same row. Every anomaly is planted
rather than hoped for; an example whose interesting rows depend on the random
number generator is an example that is boring on somebody else's screen.

## Running it

On Toft: paste this repository's URL, or `toft deploy`. Nothing has to be
configured — every capability above is optional and the rail across the top of
the page says which of them this install actually has.

Locally:

```bash
python -m venv .venv && .venv/bin/pip install -r requirements.txt
.venv/bin/python worker.py 2026-06        # close a month
.venv/bin/python -m uvicorn main:app --port 8000
```

Off-platform there is no `TOFT_APP_TOKEN`; the SDK raises rather than
pretending, `platform_seam.py` catches it once, and the page assumes `owner` so
every screen is reachable. It says so on the rail — there is deliberately no
dry-run mode, because an SDK that quietly printed to a console instead of paging
a channel would be a trap that only springs in production.

Optional environment:

| Variable | What it does |
|---|---|
| `RECONCILE_DATA_DIR` | Put the register somewhere specific, ahead of every other choice. |
| `CLOSE_REPORT_TO` | Comma-separated addresses for the emailed summary. |

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit):
one stylesheet, one script, no dependencies, copied byte-identically into every
example in the gallery. `static/app.css` and `static/app.js` are this app's own
and add to it without redefining any of it.

MIT.
