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
lines of `platform.go`.

## The six tabs

| Tab | What it is |
|---|---|
| **The room** | The whole stockroom as one band, sized by what is standing in each aisle; the order list; what moved. |
| **Inventory** | The CRUD surface — search, filter by aisle and state, sort, add, edit, retire, delete. |
| **Movements** | The ledger. Receipts, issues, returns and counts, each one written in the same transaction as the count it changes. |
| **Reports** | Three reports, three channels out, and what has already gone. |
| **Activity** | What changed here, beside the scheduled runs Toft reports back. |
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

Create a scheduled task on the app's **Automation** tab:

| | |
|---|---|
| Path | `/tasks/report` |
| Method | `POST` |
| Parameters | `{"report": "low_stock", "channel": "slack", "to": "ops@example.com"}` |

`report` is `inventory`, `low_stock` or `movements`; `channel` is `slack`,
`email` or `both`. Two schedules on the same path with different parameters is
the point — one report, weekly and monthly, without a second endpoint.

Three things the handler does that are worth copying:

- **The work finishes inside the request.** Returning 202 and carrying on in a
  goroutine records success for work that has not happened.
- **It fails when the send failed.** A task that answers 200 after failing to
  post is a schedule that looks healthy for months while nobody receives
  anything.
- **It pushes its own log lines.** The platform captures nothing here — it made
  an HTTP request, and a task's prints land in the app's process log interleaved
  with every other request. Pushed lines are what survives a run that is killed
  at the timeout, which is the run somebody actually needs a log for.

## The contract it keeps

| Variable | What the app does with it |
|---|---|
| `PORT` | Listens on it, bound to `127.0.0.1` — an app on `0.0.0.0` bypasses the platform's authorization entirely. |
| `BASE_PATH` | Writes it back onto every link, form action and asset. The proxy strips the prefix, so routes are matched at `/`. |
| `TOFT_API_URL` / `TOFT_APP_TOKEN` | The app API and this app's outbound identity. Absent means Slack and email are **absent**, not broken. |
| `TOFT_SCRATCH_DIR` / `TOFT_STORAGE` | Where the database may live. |
| `X-Toft-Session` | Labels who made a change. An opaque id — enough to tell two people's edits apart, never enough to name either, and the app never dresses it up as a person. |
| `X-Toft-Cron` / `X-Toft-Run` | This request is a scheduled firing, and the run to push log lines against. |

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
| `/reports` | Build and send. |
| `/activity`, `/platform` | What happened, and what this app is. |
| `/tasks/report` | The scheduled task's endpoint. `GET` and `POST`. |
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
