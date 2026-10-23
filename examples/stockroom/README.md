# Stockroom

An inventory register for an internal operations team: five aisles, forty lines,
every receipt and issue on a ledger — and a PDF of any of it, downloaded,
posted to Slack, or emailed, by hand or on a schedule.

A Go app, `net/http` and `html/template`, with **SQLite embedded in the binary**
(`modernc.org/sqlite` — pure Go, because Toft builds Go apps with CGO off)
and a PDF writer (`github.com/go-pdf/fpdf`). No npm build, no CDN, no external
assets: the CSS and the one small script are `go:embed`ed with the templates.

This is the example to read if you want to see **an app talking back to
Toft** — `/api/v1/app/*` spoken directly, with no SDK, in about two hundred
lines of `platform.go` — and a Go binary that is two entrypoints, the web app
and a scheduled job.

## The six tabs

| Tab | What it is |
|---|---|
| **The room** | The whole stockroom as one band, sized by what is standing in each aisle; the order list; what moved. |
| **Inventory** | The CRUD surface — search, filter by aisle and state, sort, add, edit, retire, delete. |
| **Movements** | The ledger. Receipts, issues, returns and counts, each one written in the same transaction as the count it changes. |
| **Reports** | Three reports, three channels out, and what has already gone. |
| **Activity** | What changed here, beside the report job's runs as Toft reports them. |
| **Platform** | What the platform told this app about itself, and what the app did with it. |

## The data is meant to be lost

The register lives wherever it can, most durable first:

1. `STOCKROOM_DATA_DIR`, if you set it
2. the first **writable storage mount** in `TOFT_STORAGE` — survives redeploys
3. `TOFT_SCRATCH_DIR` — **wiped on every deploy and restart**
4. `./data`, running outside Toft

On an ordinary shared-mode install it lands on scratch, so a redeploy starts
over. That is why the app **seeds itself on an empty database**: forty lines and
about seven hundred movements over the last ten weeks, from a fixed random seed,
so two people looking at two installs are looking at the same stockroom. The
Platform tab says out loud which of the four it picked and what that means.

## Getting a report out

One report is built once and rendered three ways — the PDF, the Slack comment
and the email body all quote the same numbers, because a message disagreeing
with the attachment beside it is how people stop trusting both.

| Channel | Needs | If it is not there |
|---|---|---|
| Download | nothing | — |
| Slack | `slack.post`, and `slack.file` for the PDF | posts the summary without the attachment, and says so |
| Email | `email.send`, and `email.attach` for the PDF | sends without the attachment, and says so |

Attaching Slack or email to an app is an **administrator's** decision, made on
the app's Integrations tab. This app cannot turn either on, cannot name a Slack
channel, and cannot set `from` or `reply_to` — all three are a 400 from the
platform that names the rule. Email **recipients** are the one destination it
chooses, which is why every address goes to the install's send log.

The Reports tab reads `/api/v1/app/self` and says exactly which of those it may
do, rather than offering a button that cannot work.

## On a schedule

The schedule is a **job**, declared in `toft.toml` — the only place a job is
defined, so it versions with the code and a rollback takes it back:

```toml
[jobs.report]
args     = ["report"]          # this binary, run as `toft-app report`
schedule = "0 7 * * 1"         # Monday 07:00
timezone = "Europe/London"     # on the wall clock, through the clock changes
overlap  = "skip"
timeout  = "5m"
memory   = 256
notify   = "failures"
```

with three typed parameters: `report` (`inventory`, `low_stock`, `movements`),
`channel` (`slack`, `email`, `both`) and `to`. A value outside a declared choice
is refused when the run is started, never an hour into it. Run it now from the
app's **Automation** tab, with `toft job run report --param channel=both`, or
from the Reports tab, which starts it in the clicker's name by relaying
`X-Toft-Act-For`. Locally, `go run . report` runs it once.

Three things the job does that are worth copying:

- **Its exit code is its outcome.** A send that did not happen exits 1, and the
  last line on stderr is the reason Toft shows. A report job that exits 0 after
  failing to post is a schedule that looks healthy for months while nobody
  receives anything — and partial delivery on `both` fails too.
- **Its log is its stdout and stderr.** The runner ships both, so there is
  nothing to push and nothing lost when a run is killed at its deadline.
  Progress goes to the run's status page through `/api/v1/runner/progress`.
- **It holds a run token, not the app's.** `TOFT_JOB_TOKEN` is accepted on the
  send routes while the run is in flight and nowhere else, so the job never asks
  `/app/self` what it may do. It tries the richer send first and lets the send
  route's own refusal decide.

**The job does not share the web process's scratch space.** A run gets a
scratch directory of its own, so without a writable storage mount it opens an
empty register and seeds it: the report it sends is the demo stockroom, not the
edits made in the app, and the deliveries it records stay in its own run rather
than the Reports tab's table. Map a writable mount and both processes open the
same file. The job says which on the first lines of every run's log.

## The contract it keeps

| Variable | What the app does with it |
|---|---|
| `PORT` | Listens on it, bound to `127.0.0.1` — an app on `0.0.0.0` bypasses the platform's authorization entirely. |
| `BASE_PATH` | Writes it back onto every link, form action and asset. The proxy strips the prefix, so routes are matched at `/`. |
| `TOFT_API_URL` / `TOFT_APP_TOKEN` | The app API and this app's outbound identity. Absent means Slack and email are **absent**, not broken. |
| `TOFT_JOB_TOKEN` / `TOFT_PARAMS` / `TOFT_RUN_ID` / `TOFT_TRIGGER` / `TOFT_SCHEDULED_FOR` | Inside the report job only: the run's credential, its checked params, its id, how it started and the tick it is for. |
| `TOFT_SCRATCH_DIR` / `TOFT_STORAGE` | Where the database may live. |
| `X-Toft-Session` | Labels who made a change. An opaque id — enough to tell two people's edits apart, never enough to name either, and the app never dresses it up as a person. |
| `X-Toft-Act-For` | Relayed untouched when the Reports tab starts the job, so the run is attributed to the person. |

`SIGTERM` and `SIGINT` are handled, because PM2 sends both when a deploy
replaces the process.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `APP_NAME` | no | `Stockroom` | The wordmark, the PDF header and the email's sender line. |
| `STOCKROOM_DATA_DIR` | no | — | Overrides where the SQLite file goes. |

## Routes

| Path | What |
|---|---|
| `/` | The room. |
| `/inventory`, `/inventory/{id}` | The register and one line. |
| `/movements` | The ledger. |
| `/reports` | Build and send; `POST /reports/run` starts the report job now. |
| `/activity`, `/platform` | What happened, and what this app is. |
| `/healthz` | Liveness. |

## Running it locally

```bash
go run .            # http://127.0.0.1:8080, database in ./data
```

Slack and email are off outside Toft, and every surface offering them says
so instead of failing when pressed. Everything else — the CRUD, the ledger, the
reports, the PDF — works exactly as it does deployed.

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
`static/app.css` is the kit followed by this app's own layer, and the layer is
two things:

- **aliases.** These templates were written before the kit existed and call a
  card header `.card-head` where the kit calls it `.card-hd`. Renaming forty of
  those across eight templates would be a large diff that changed nothing
  anybody can see, so the old names are given the kit's rules instead. New
  markup uses the kit's spelling.
- **the parts the kit has no opinion about** — the aisle band, the movement
  feed, the run list, the report picker, the quantity stepper and the bin code.
  Those are what this app *is*, and they are drawn with kit tokens so they
  belong to the same page.

The five aisles are the kit's five chart series, copied into a Go map because a
map cannot read a stylesheet. `TestAisleColoursMatchTheKit` is what keeps the two
from drifting: change `--lp-c3` and the test fails, rather than an aisle quietly
becoming a colour no other example in the gallery uses.

The capability rail across the top is read from `GET /api/v1/app/self` on every
render rather than written down, so an administrator attaching Slack shows up on
the next page load.
