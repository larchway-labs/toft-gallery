# Status Board

An internal service status page for Toft. It polls a list of HTTP
endpoints from inside your network and shows the result as a board — up, slow,
or down, with the last error text when there is one.

Go, standard library only. No database, no external assets, nothing persisted:
the board is what this process has seen since it started.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `SERVICES` | yes | — | Comma-separated `name=url` pairs. A malformed entry is skipped with a log line rather than failing the boot. |
| `BOARD_TITLE` | no | `Service status` | Heading on the page. |
| `CHECK_INTERVAL_SECONDS` | no | `60` | How often every endpoint is polled (minimum 5). |
| `CHECK_TIMEOUT_SECONDS` | no | `5` | Per-request timeout (minimum 1). |

Example:

```
SERVICES=Wiki=https://wiki.internal/health, Billing=https://billing.internal/healthz
```

## Routes

| Path | What |
|---|---|
| `/` | The board. |
| `/api/status` | The same data as JSON. |
| `/healthz` | Liveness, for whatever is watching this app. |

## The platform contract

Toft passes `PORT`, `HOST` and `BASE_PATH`. A Go app owns its own bind, so
this one listens on `HOST:PORT` — `127.0.0.1` in shared mode, never `0.0.0.0` —
and the proxy strips `/apps/{slug}` before forwarding, so routes are registered
at `/`. `BASE_PATH` is used only where an absolute URL is printed.

## Running it locally

```bash
PORT=8080 HOST=127.0.0.1 SERVICES="Example=https://example.com" go run .
```
