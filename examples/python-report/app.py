"""Weekly Report — a Python app with no web framework at all.

This is Toft's *generic Python* path: a `requirements.txt` with nothing
the platform recognises, started as `python app.py`. Nothing supplies a server,
so the app supplies its own — and with that comes the whole contract:

    PORT       the port to listen on. A hardcoded port is a green build and an
               app nobody can reach.
    HOST       the address to bind. 127.0.0.1 in shared mode; an app on
               0.0.0.0 bypasses the platform's authorization entirely.
    BASE_PATH  the prefix the app is mounted under. The proxy strips it before
               forwarding, so routes are matched at "/" — BASE_PATH is used
               only when a URL has to leave this process.

It renders a weekly operations report from the dataset in `data.py`, and offers
the same numbers as JSON and CSV. Replace `data.py` with a database query and
the rest of the app is unchanged.

Standard library only: http.server, csv, json, html, hmac, hashlib. That is not
a limitation being worked around — it is the example. `platform_seam.py` verifies
the platform's *signed role header* in about fifteen lines of `hmac`, which is
the one Toft mechanism that looks like it needs a library and does not.

It serves its own static files too, because nothing else is going to.
"""

import csv
import html
import io
import json
import os
import signal
import sys
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

import platform_seam as seam
from data import REPORT_TITLE, incidents, summary, weeks

BASE_PATH = os.getenv("BASE_PATH", "")
STARTED_AT = datetime.now(timezone.utc)
HERE = os.path.dirname(os.path.abspath(__file__))

# The house style, read once at startup rather than per request. Three files,
# byte-identical in every example in the Toft gallery.
STATIC = {}
for _name, _type in (("toft-kit.css", "text/css; charset=utf-8"),
                     ("toft-kit.js", "text/javascript; charset=utf-8"),
                     ("favicon.svg", "image/svg+xml")):
    try:
        with open(os.path.join(HERE, "static", _name), "rb") as _f:
            STATIC["/static/" + _name] = (_f.read(), _type)
    except OSError:
        pass


class Handler(BaseHTTPRequestHandler):
    server_version = "weekly-report/1.0"

    # Routing is a dict rather than a chain of ifs, so an unknown path is a
    # 404 with a list of what does exist — an example that answers "what else
    # is there" without making anybody read the source.
    def do_GET(self):  # noqa: N802 — http.server's spelling
        path = urlparse(self.path).path.rstrip("/") or "/"

        # Static first, and out of a dict built at startup: reading a file per
        # request would be a filesystem call on the hot path, and building the
        # path from the URL would be a directory traversal waiting to happen.
        asset = STATIC.get(path)
        if asset is not None:
            return self.send_bytes(200, asset[0], asset[1],
                                   extra={"Cache-Control": "public, max-age=300"})

        routes = {
            "/": self.page,
            "/report.json": self.report_json,
            "/report.csv": self.report_csv,
            "/healthz": self.healthz,
        }
        handler = routes.get(path)
        if handler is None:
            return self.send_json(
                404, {"error": "no such path",
                      "paths": sorted(list(routes) + list(STATIC))}
            )
        handler()

    # --- routes --------------------------------------------------------------

    def page(self):
        self.send_html(render(seam.role(self.headers)))

    def report_json(self):
        self.send_json(
            200,
            {
                "title": REPORT_TITLE,
                "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
                "summary": summary(),
                "weeks": weeks(),
                "incidents": incidents(),
            },
        )

    def report_csv(self):
        buffer = io.StringIO()
        writer = csv.DictWriter(buffer, fieldnames=list(weeks()[0]))
        writer.writeheader()
        writer.writerows(weeks())
        body = buffer.getvalue().encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/csv; charset=utf-8")
        self.send_header("Content-Disposition", 'attachment; filename="weekly-report.csv"')
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def healthz(self):
        uptime = (datetime.now(timezone.utc) - STARTED_AT).total_seconds()
        self.send_json(200, {"status": "ok", "uptime_seconds": int(uptime), "weeks": len(weeks())})

    # --- plumbing ------------------------------------------------------------

    def send_bytes(self, status, body, content_type, extra=None):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        for key, value in (extra or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def send_json(self, status, payload):
        self.send_bytes(status, json.dumps(payload, indent=2).encode("utf-8"),
                        "application/json")

    def send_html(self, markup):
        self.send_bytes(200, markup.encode("utf-8"), "text/html; charset=utf-8",
                        extra={"Cache-Control": "no-store"})

    def log_message(self, fmt, *args):
        # One line per request on stdout, which is where Toft collects it.
        sys.stdout.write("%s %s\n" % (self.address_string(), fmt % args))
        sys.stdout.flush()


# --- rendering ---------------------------------------------------------------
#
# One f-string and no template engine, which is the whole point of this example:
# `html.escape` and `str.format` are the standard library's answer to rendering,
# and for a page this size they are a complete one.
#
# The look is the Toft Example Kit — one stylesheet, one script, carried
# byte-identically by every example in the gallery and served from /static above.


def esc(value):
    return html.escape(str(value), quote=True)


def cap(label, on, note=""):
    return '<span class="cap {state}" title="{note}"><b>{label}</b></span>'.format(
        state="on" if on else "off", note=esc(note), label=esc(label))


def render(role):
    s = summary()
    rows = weeks()
    incs = incidents()
    peak = max(r["deploys"] for r in rows) or 1
    on_platform = seam.on_platform()

    tiles = "".join(
        '''<div class="tile {tone}">
             <div class="tile-k">{label}</div>
             <div class="tile-v">{value}</div>
             <div class="tile-n">{note}</div>
           </div>'''.format(
            tone="tone-ok" if t["direction"] == "up" else "tone-att",
            label=esc(t["label"]), value=esc(t["value"]), note=esc(t["note"]))
        for t in s)

    week_rows = "".join(
        '''<tr>
             <td class="lead mono">{week}</td>
             <td class="num">{deploys}</td>
             <td class="num">{failed}</td>
             <td class="num">{rate}</td>
             <td class="num mono">{mins}</td>
             <td style="width:11rem">
               <span class="bar"><span class="bar-track">
                 <span class="bar-fill" style="width:{pct}%"></span>
               </span></span>
             </td>
           </tr>'''.format(
            week=esc(r["week"]), deploys=r["deploys"], failed=r["failed_builds"],
            rate="%.0f%%" % (100 * r["failed_builds"] / r["deploys"]) if r["deploys"] else "—",
            mins="%.1f" % r["median_build_minutes"],
            pct=round(100 * r["deploys"] / peak))
        for r in rows)

    tones = {"high": "tone-dan", "medium": "tone-att", "low": "tone-neu"}
    incident_rows = "".join(
        '''<tr>
             <td class="mono small">{date}</td>
             <td class="lead">{title}</td>
             <td><span class="badge {tone}">{sev}</span></td>
             <td class="num mono">{mins} min</td>
           </tr>'''.format(
            date=esc(i["date"]), title=esc(i["title"]), sev=esc(i["severity"]),
            tone=tones.get(i["severity"], ""), mins=i["minutes"])
        for i in incs)

    env_rows = "".join(
        '''<tr>
             <td class="mono small nowrap">{name}</td>
             <td class="mono small">{value}</td>
             <td class="small muted">{why}</td>
           </tr>'''.format(name=esc(name), value=esc(value), why=esc(why))
        for name, value, why in seam.facts())

    rail = "".join([
        cap("Standard library only", True,
            "http.server, csv, json, html, hmac. No framework and no SDK."),
        cap("PORT / HOST", bool(os.getenv("PORT")),
            "The two variables that decide whether anybody can reach this app."),
        cap("BASE_PATH", bool(BASE_PATH),
            BASE_PATH or "Not mounted under a prefix here."),
        cap("Role: " + role, role != "unknown",
            "X-Toft-Role, verified in this process with hmac and hashlib."),
        cap("Toft workload", on_platform,
            "Running as a Toft workload." if on_platform
            else "No TOFT_* variables: this is a local run."),
    ])

    ladder = "".join(
        '''<span class="badge {tone}">{word}</span>'''.format(
            word=word, tone="tone-brand" if word == role else "")
        for word in seam.ROLES)

    return TEMPLATE.format(
        title=esc(REPORT_TITLE),
        base=esc(BASE_PATH),
        now=datetime.now(timezone.utc).strftime("%d %B %Y at %H:%M UTC"),
        rail=rail, tiles=tiles, week_rows=week_rows, incident_rows=incident_rows,
        env_rows=env_rows, role=esc(role), ladder=ladder,
        may_export="yes" if seam.at_least(role, "viewer") else "no",
        may_export_tone="tone-ok" if seam.at_least(role, "viewer") else "tone-att",
        weeks_n=len(rows), incidents_n=len(incs),
        deploys_n=sum(r["deploys"] for r in rows),
    )


TEMPLATE = """<!doctype html>
<html lang="en" data-base="{base}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title}</title>
<meta name="description" content="A weekly delivery report from a Python app with no web framework.">
<link rel="icon" href="{base}/static/favicon.svg" type="image/svg+xml">
<link rel="stylesheet" href="{base}/static/toft-kit.css">
</head>
<body>
<a class="lp-skip" href="#main">Skip to content</a>

<header class="masthead">
  <div class="masthead-in">
    <div>
      <div class="wordmark">
        <span class="mark" aria-hidden="true"></span>
        <span class="wordmark-text">Toft example</span>
      </div>
      <h1>{title}</h1>
      <p class="standfirst">
        {weeks_n} weeks, {deploys_n} deploys and {incidents_n} incidents, served by a Python
        app with no web framework at all &mdash; <code>http.server</code>, and nothing else.
      </p>
    </div>
    <div class="masthead-aside">
      <span class="chip chip-lang">Python &middot; standard library</span>
      <span class="chip">Generated {now}</span>
    </div>
  </div>
</header>

<div class="rail"><div class="rail-in">
  <span class="rail-label">Toft</span>{rail}
</div></div>

<nav class="tabs" data-tabs aria-label="Sections">
  <div class="tabs-in">
    <button class="tab" type="button" data-tab="report">Report</button>
    <button class="tab" type="button" data-tab="incidents">Incidents
      <span class="tab-count">{incidents_n}</span></button>
    <button class="tab" type="button" data-tab="platform">Platform</button>
  </div>
</nav>

<main class="shell" id="main">

<section data-panel="report">
  <div class="tiles">{tiles}</div>

  <div class="section">
    <div class="card">
      <div class="card-hd">
        <h2>By week</h2>
        <span class="muted small">Deploys, failures, and how long a build took</span>
      </div>
      <div class="card-bd" style="padding:0">
        <div class="tbl-wrap">
          <table class="tbl" data-sortable>
            <thead><tr>
              <th data-sort="text">Week</th>
              <th data-sort="num" class="num">Deploys</th>
              <th data-sort="num" class="num">Failed</th>
              <th data-sort="num" class="num">Failure rate</th>
              <th data-sort="num" class="num">Median build (min)</th>
              <th></th>
            </tr></thead>
            <tbody>{week_rows}</tbody>
          </table>
        </div>
      </div>
      <div class="card-ft">
        The same numbers as <a href="{base}/report.json">JSON</a> and
        <a href="{base}/report.csv">CSV</a>. Those two links are the only place
        <code>BASE_PATH</code> is needed: they leave this process, and the browser
        resolves them against the platform's origin.
      </div>
    </div>
  </div>
</section>

<section data-panel="incidents" hidden>
  <div class="section-hd">
    <div>
      <h2>Incidents</h2>
      <p>Four in the period. Mean recovery is on the tile above; the row is what
         actually happened.</p>
    </div>
  </div>
  <div class="card">
    <div class="card-bd" style="padding:0">
      <div class="tbl-wrap">
        <table class="tbl" data-sortable>
          <thead><tr>
            <th data-sort="date">Date</th>
            <th data-sort="text">What happened</th>
            <th data-sort="text">Severity</th>
            <th data-sort="num" class="num">Time to recover</th>
          </tr></thead>
          <tbody>{incident_rows}</tbody>
        </table>
      </div>
    </div>
  </div>
</section>

<section data-panel="platform" hidden>
  <div class="section-hd">
    <div>
      <h2>Platform</h2>
      <p>What Toft told this app, and what it did with it &mdash; using nothing
         but the standard library. Every example in the gallery has this tab.</p>
    </div>
  </div>

  <div class="cards">
    <div class="card">
      <div class="card-hd"><h2>The caller's role</h2>
        <span class="badge tone-brand">{role}</span></div>
      <div class="card-bd">
        <p class="small">Six words and no seventh:</p>
        <div class="row">{ladder}</div>
        <dl class="kv" style="margin-top:.75rem">
          <dt>Verified</dt>
          <dd><code>hmac.new(key, b"toft-role\x00" + payload, sha256)</code></dd>
          <dt>Key</dt>
          <dd>per app, per workload start, derived and never stored</dd>
          <dt>Life</dt>
          <dd>120 seconds, compiled into the platform &mdash; an operator does not get to raise it</dd>
          <dt>May take the CSV</dt>
          <dd><span class="badge {may_export_tone}">{may_export}</span>
              <span class="tiny muted">this page does not enforce it; the example is that it could</span></dd>
        </dl>
        <div class="note tone-brand">
          <strong>It is signed rather than spelled</strong> because stripping an inbound
          header only answers the visitor. In shared mode apps bind loopback and
          app-to-app isolation is an accepted risk, so a neighbour on this box can reach
          the port without the gateway. The key is per app, so a blob minted for another
          app fails the check here &mdash; which is why this code never needs to know its
          own app id.
        </div>
      </div>
      <div class="card-ft">
        <code>platform_seam.py</code>, about fifteen lines, no dependencies. The SDK does
        exactly this; the example is that you can see all of it.
      </div>
    </div>

    <div class="card">
      <div class="card-hd"><h2>The environment</h2></div>
      <div class="card-bd" style="padding:0">
        <div class="tbl-wrap">
          <table class="tbl">
            <thead><tr><th>Variable</th><th>Value</th><th>What it is for</th></tr></thead>
            <tbody>{env_rows}</tbody>
          </table>
        </div>
      </div>
      <div class="card-ft">
        <code>PORT</code> and <code>HOST</code> are the two that decide whether this app
        works at all. A hardcoded port is a green build nobody can reach; binding
        <code>0.0.0.0</code> in shared mode steps around the platform's authorization
        entirely.
      </div>
    </div>
  </div>
</section>

</main>

<footer class="foot">
  <div class="foot-in">
    <span>An example app from the <strong>Toft</strong> gallery.</span>
    <span>Python &middot; http.server &middot; no framework &middot; no SDK &middot; no dependencies</span>
  </div>
</footer>

<script src="{base}/static/toft-kit.js"></script>
</body>
</html>"""


def main():
    host = os.getenv("HOST", "127.0.0.1")
    port = int(os.getenv("PORT", "8000"))

    server = ThreadingHTTPServer((host, port), Handler)
    server.daemon_threads = True

    # PM2 stops a process with SIGINT and then SIGTERM. Handling both means a
    # deploy replaces this app cleanly instead of having it killed mid-response.
    def shutdown(_signum, _frame):
        print("shutting down", flush=True)
        server.shutdown()

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)

    print("weekly report listening on http://%s:%d (base path %r)" % (host, port, BASE_PATH), flush=True)
    server.serve_forever()
    server.server_close()


if __name__ == "__main__":
    main()
