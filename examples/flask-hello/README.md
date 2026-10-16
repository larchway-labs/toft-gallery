# Flask Hello

The smallest complete Flask app for Toft — and a look at the request from
inside it.

One dependency. It is still a hello page, and it still does the four things a
real Flask app on this platform has to do; what it adds is the other half of a
first deploy: **every header the platform put on this request, the caller's
signed role, who is looking, and the environment the process was handed** — each
with a sentence saying what it is for.

That is the page somebody actually wants on the day they are working out why a
link points at the wrong place.

## The four things, checked rather than claimed

| | |
|---|---|
| **A module-level `app`** | Toft starts it with `flask --app app run --host <host> --port <port>`. A factory nobody can find is the most common reason a green build serves nothing. |
| **`ProxyFix(..., x_prefix=1)`** | So `url_for()` emits links that include `/apps/{slug}`. |
| **Configuration from the environment** | With working defaults, so it runs on a laptop too. |
| **A health check that does not lie** | `/healthz` reports this process's real uptime. One that returns 200 unconditionally has never once told anybody anything. |

The Getting-started tab runs all five as live checks against the running
process, because an example that asserts it is set up correctly is worth much
less than one that looks.

## The one that catches everybody

The proxy **strips** `/apps/{slug}` before forwarding, so this process is
mounted at `/`.

- **A link has to carry the prefix back.** `ProxyFix` reading
  `X-Forwarded-Prefix` is what makes `url_for()` do that.
- **A `Location:` header must not.** The proxy re-adds it, so a redirect that
  carries it lands on `/apps/slug/apps/slug`.

Links carry the prefix; redirects do not. That asymmetry is the single most
common way a working app breaks on a platform.

## Who is asking — two questions, two calls

This is the example that shows both, side by side, because reaching for the
wrong one is a design mistake that is hard to see afterwards.

**`toft.caller(headers)` — what may this caller do.** A word, signed by the
platform per app and per workload start, verified in this process with `hmac`.
No round trip, no administrator switch, present on every forwarded request. Six
words — `anonymous`, `viewer`, `editor`, `owner`, `system`, `app` — and no
seventh. **Reach for it to refuse.**

**`toft.who(headers)` — who is this.** A name, an email and groups. One round trip,
and an administrator has to have switched viewer identity on for this app.
**Reach for it to describe.**

A role names nobody, which is exactly why the two are not one call. An app that
gates on identity when it means authority has built a slower, more fragile
version of the first one — and has asked an administrator for a setting it did
not need.

The page keeps **204, 403 and a person** apart, deliberately: *no viewer*, *this
app may not be told*, and *here they are* are three different answers, and
collapsing any two of them is how an app ends up telling somebody the wrong
story.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `GREETING` | no | `Hello from Toft` | The heading, and the greeting the API returns. |

Everything else on the Environment tab is set by the platform.

## Routes

| Path | What |
|---|---|
| `/` | The page. |
| `/api/greet?name=` | JSON, and it hands the caller's role back so you can see it travels on an XHR too. |
| `/healthz` | Liveness. |

## Running it

On Toft: paste this repository's URL. Nothing has to be configured.

Locally:

```bash
python -m venv .venv && .venv/bin/pip install -r requirements.txt
.venv/bin/python app.py          # http://127.0.0.1:5000
```

Off-platform there is no role header and no SDK; the page says so on the rail
rather than pretending, and every tab still works.

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
one stylesheet, one script, no CDN and no web fonts — served from `static/` and
linked with `url_for('static', ...)`, which is itself the point being made.

MIT.
