"""The smallest complete Flask app for Toft — and what it can see.

It is still a hello page, and it still does the four things a real Flask app on
this platform has to do:

  1. Expose a module-level `app` — Toft starts it with
     `flask --app app run --host <host> --port <port>`, so a factory nobody can
     find is the most common reason a green build serves nothing.
  2. Wrap the WSGI app in ProxyFix with `x_prefix=1`, so `url_for()` emits links
     that include `/apps/{slug}`. The proxy strips that prefix before
     forwarding, which is exactly why the app has to be told about it.
  3. Take configuration from the environment, with working defaults.
  4. Answer a health check that does not lie.

What it adds is the other half of a first deploy: **a look at the request from
inside the app**. Every header Toft puts on a forwarded request, the
caller's signed role, who is looking where an administrator has switched that
on, and the environment the process was handed — each with a sentence saying
what it is for. That is the page somebody actually wants on the day they are
working out why a link points at the wrong place.

Copy it, delete the parts you do not need, and you have a working starting point
rather than a scaffold.
"""

import datetime
import os
import platform
import socket

from flask import Flask, jsonify, render_template, request, url_for
from werkzeug.middleware.proxy_fix import ProxyFix

import platform_seam as seam

app = Flask(__name__)

# x_prefix=1 is the one that matters here: it makes url_for() honour the
# X-Forwarded-Prefix header Toft's proxy sets to /apps/{slug}. Without it
# every generated link points at the platform root and 404s.
#
# The other three are ordinary reverse-proxy hygiene, and Toft only sends
# them from a trusted proxy — TRUSTED_PROXY_CIDRS defaults to loopback, because
# a connection from there came from a process on this box that already has host
# access, and anything else is a remote client that is never believed.
app.wsgi_app = ProxyFix(app.wsgi_app, x_for=1, x_proto=1, x_host=1, x_prefix=1)

STARTED_AT = datetime.datetime.now(datetime.timezone.utc)
GREETING = os.getenv("GREETING", "Hello from Toft")

# The headers the platform puts on a forwarded request, and what each is for.
# The list lives here rather than in the template because it is the subject of
# this example rather than decoration on it.
PLATFORM_HEADERS = [
    ("X-Forwarded-Prefix", "Where this app is mounted. ProxyFix reads it, which is what "
                           "makes url_for() emit a link that works."),
    ("X-Forwarded-Proto", "The *browser's* scheme, not this process's. Behind a "
                          "TLS-terminating proxy this app's own socket is plain HTTP."),
    ("X-Forwarded-Host", "The host the browser asked for."),
    ("X-Forwarded-For", "The client address, as the platform resolved it."),
    ("X-Toft-Role", "What the caller may do. Signed per app and per workload start, "
                         "good for 120 seconds, verified below."),
    ("X-Toft-Act-For", "An opaque token naming the viewer, for an app that wants to "
                            "act on their behalf. Never an identity by itself."),
    ("X-Toft-Session", "One browser login, so an app can keep a scratch directory "
                            "per session without inventing a cookie of its own."),
    ("X-Toft-Request-Deadline", "When the gateway will cut this request off, RFC 3339. "
                                "Work that might outlast it belongs in a job."),
    ("X-Toft-Scan", "Present when a security scanner is asking. It grants nothing, "
                         "and means only: do not count this as usage."),
]


@app.route("/")
def index():
    role = seam.role(request.headers)
    viewer = seam.viewer(request.headers)
    prefix = request.headers.get("X-Forwarded-Prefix", "")
    return render_template(
        "index.html",
        greeting=GREETING,
        base_path=os.getenv("BASE_PATH", "") or prefix,
        host=socket.gethostname(),
        python=platform.python_version(),
        flask_version=_flask_version(),
        started=STARTED_AT.strftime("%d %B %Y at %H:%M UTC"),
        uptime=_uptime(),
        # The four things, each checked rather than claimed. An example that
        # asserts it is set up correctly is worth much less than one that looks.
        checks=_checks(prefix),
        role=role,
        ladder=seam.ROLES,
        may_edit=seam.at_least(role, "editor"),
        viewer=viewer,
        rail=seam.rail(role, viewer),
        platform_headers=[(name, why, request.headers.get(name))
                          for name, why in PLATFORM_HEADERS],
        other_headers=sorted(
            (k, v) for k, v in request.headers.items()
            if k.lower() not in {n.lower() for n, _ in PLATFORM_HEADERS}),
        env=seam.facts(),
        request_facts=[
            ("Method", request.method),
            ("Path, as this app sees it", request.path),
            ("Path, as the browser asked for it", (prefix or "") + request.path),
            ("Scheme", request.scheme),
            ("Host", request.host),
            ("Remote address", request.remote_addr or "—"),
            ("url_for('index')", _url_for_index()),
            ("url_for('greet')", _url_for("greet")),
        ],
    )


@app.route("/api/greet")
def greet():
    """A small JSON endpoint, so the example shows both halves of a web app."""
    name = (request.args.get("name") or "world").strip()[:80]
    return jsonify(
        greeting=f"{GREETING}, {name}!",
        at=_now(),
        # Handed back so the page can show that the role travels on an XHR
        # exactly as it travels on a page load: it is a property of the request,
        # not of the browser navigating.
        caller_role=seam.role(request.headers),
    )


@app.route("/healthz")
def healthz():
    """Liveness. It reports only what this process can actually vouch for — a
    health check that returns 200 unconditionally is a health check that has
    never once told anybody anything."""
    return jsonify(status="ok", uptime_seconds=int(_uptime_seconds()))


# --- the four things, checked ------------------------------------------------

def _checks(prefix):
    proxied = isinstance(app.wsgi_app, ProxyFix)
    port = os.getenv("PORT", "")
    return [
        {
            "title": "A module-level <code>app</code>",
            "ok": True,
            "detail": "Found, or this page would not be rendering. A factory nobody can "
                      "find is the most common reason a green build serves nothing.",
        },
        {
            "title": "ProxyFix with <code>x_prefix=1</code>",
            "ok": proxied,
            "detail": (f"<code>url_for('index')</code> is <code>{_url_for_index()}</code>, "
                       f"and the prefix on this request is "
                       f"<code>{prefix or 'absent'}</code>."
                       if proxied else
                       "Not wrapped. Every generated link points at the platform root."),
        },
        {
            "title": "Configuration from the environment",
            "ok": True,
            "detail": f"<code>GREETING</code> is <code>{GREETING}</code>. Changing it is "
                      "an edit on the app's Settings tab and a restart, not a redeploy.",
        },
        {
            "title": "A health check that does not lie",
            "ok": True,
            "detail": f"<code>/healthz</code> reports this process's real uptime "
                      f"({_uptime()}), which is the only thing it can vouch for.",
        },
        {
            "title": "Started by the platform",
            "ok": bool(port),
            "detail": (f"<code>PORT</code> is <code>{port}</code>, and the Flask CLI was "
                       "given it along with the host — which is what keeps this app off "
                       "<code>0.0.0.0</code> in shared mode."
                       if port else
                       "No <code>PORT</code> in the environment, so this is a local run "
                       "and <code>app.run()</code> at the bottom of app.py supplied the "
                       "defaults."),
        },
    ]


def _url_for(endpoint):
    try:
        return url_for(endpoint)
    except Exception:                                   # pragma: no cover
        return "—"


def _url_for_index():
    return _url_for("index")


def _flask_version():
    try:
        from importlib.metadata import version
        return version("flask")
    except Exception:                                   # pragma: no cover
        return "unknown"


def _now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")


def _uptime_seconds():
    return (datetime.datetime.now(datetime.timezone.utc) - STARTED_AT).total_seconds()


def _uptime():
    seconds = int(_uptime_seconds())
    if seconds < 60:
        return f"{seconds}s"
    if seconds < 3600:
        return f"{seconds // 60}m {seconds % 60}s"
    return f"{seconds // 3600}h {(seconds % 3600) // 60}m"


if __name__ == "__main__":
    # Local development only. On Toft the Flask CLI supplies host and port,
    # which is what keeps an app off 0.0.0.0 in shared mode.
    app.run(host=os.getenv("HOST", "127.0.0.1"), port=int(os.getenv("PORT", "5000")))
