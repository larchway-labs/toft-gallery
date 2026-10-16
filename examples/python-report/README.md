# Weekly Report

A Python app with **no web framework at all** — `http.server`, `csv`, `json`,
`html`, `hmac`, and nothing else. It renders a weekly delivery report, offers
the same numbers as JSON and CSV, serves its own static files, and **verifies
the platform's signed role header in about fifteen lines**.

No framework, no SDK, no dependencies: `requirements.txt` is empty on purpose.

This is Toft's *generic Python* path: a `requirements.txt` that names none
of the frameworks the platform recognises, started as `python app.py`.

## Which means the app owns the whole contract

Nothing supplies a server, so nothing supplies the defaults either:

| Variable | What the app must do with it |
|---|---|
| `PORT` | Listen on it. A hardcoded port is a green build and an app nobody can reach. |
| `HOST` | Bind it. `127.0.0.1` in shared mode — an app on `0.0.0.0` bypasses the platform's authorization entirely. |
| `BASE_PATH` | Use it only for URLs that leave the process. The proxy strips the prefix, so routes are matched at `/`. |

It also handles `SIGTERM` and `SIGINT`, because PM2 sends both when a deploy
replaces the process, and an app that ignores them is killed mid-response.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `REPORT_TITLE` | no | `Weekly delivery report` | Heading and `<title>`. |

## The one mechanism worth reading

`platform_seam.py` verifies `X-Toft-Role` with `hmac` and `hashlib`. The
rest of the gallery imports the SDK Toft installs into every app; this one
deliberately does not, because the platform's contract is **environment
variables and HTTP headers** and the whole of it can be honoured from the
standard library.

```
X-Toft-Role: v1.<role>.<base64url app id>.<expiry unix>.<mac>
```

The MAC is HMAC-SHA256 over `b"toft-role\x00"` plus the payload, keyed with
`TOFT_ROLE_KEY`, base64url with the padding stripped. Six words —
`anonymous`, `viewer`, `editor`, `owner`, `system`, `app` — and no seventh; every
failure is `unknown`, which is never privileged.

It is signed rather than merely spelled because stripping an inbound header only
answers the visitor. In shared mode apps bind loopback and app-to-app isolation
is an accepted risk, so a neighbour on the same box can reach this port without
the gateway ever seeing the request. The key is derived per app and per workload
start, which is why the check never needs to know its own app id.

The Platform tab shows the verified word, the ladder, and every `TOFT_*`
variable this process was handed with a sentence saying what each one is for.

## Routes

| Path | What |
|---|---|
| `/` | The report. |
| `/static/*` | The three files of the house style, read once at startup into a dict. Building a path from the URL would be a directory traversal waiting to happen. |
| `/report.json` | Summary, weeks and incidents. |
| `/report.csv` | The weekly table, as a download. |
| `/healthz` | Liveness. |

An unknown path returns 404 **with the list of paths that do exist** — which is
how an example answers "what else is in here" without anybody reading the
source.

## Making it yours

`data.py` holds the numbers and nothing else. Replace `weeks()`, `incidents()`
and `summary()` with database queries and `app.py` is unchanged.

## Local development

```bash
PORT=8000 python app.py
```

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
one stylesheet, one script, no CDN and no web fonts — carried byte-identically by
every example in the gallery and served from `/static` by the twelve lines above.

MIT.
