"""Security Probe — a deployed app that measures its own isolation.

This is a diagnostic you run *from inside* Toft: deploy it like any other
app and it reports, from its own per-app account, exactly what it can and
cannot reach — other apps' files, the platform's secrets, cloud credentials,
the network. Each probe carries its own expectation, so a result is not "this
app can reach X" but "this app can reach X, and here is whether it should."

It writes nothing, persists nothing and needs no configuration. Point a
neighbour's RDS endpoint at SECLAB_DB_HOST to also test database reachability.

The Toft contract it honors: Flask started by the platform CLI with PORT
and HOST supplied, ProxyFix(x_prefix=1) so links carry /apps/{slug}, and a
health check that reports real uptime.
"""

import datetime
import json
import os
import platform

from flask import Flask, jsonify, render_template, request
from werkzeug.middleware.proxy_fix import ProxyFix

import checks as C

app = Flask(__name__)
app.wsgi_app = ProxyFix(app.wsgi_app, x_for=1, x_proto=1, x_host=1, x_prefix=1)

STARTED_AT = datetime.datetime.now(datetime.timezone.utc)


def _base():
    return request.headers.get("X-Forwarded-Prefix", "") or os.getenv("BASE_PATH", "")


@app.get("/")
def index():
    return render_template(
        "index.html",
        base_path=_base(),
        checks=C.CHECKS,
        categories=_categories(),
        tone=C.TONE,
        host=platform.node(),
        python=platform.python_version(),
        identity=C.check_identity().evidence,
    )


@app.post("/api/run/<check_id>")
def run_one(check_id):
    r = C.run_check(check_id)
    return jsonify(_wire(check_id, r))


@app.get("/api/run-all")
@app.post("/api/run-all")
def run_all():
    return jsonify([_wire(c.id, C.run_check(c.id)) for c in C.CHECKS])


@app.get("/api/checks")
def list_checks():
    return jsonify([
        {"id": c.id, "title": c.title, "category": c.category,
         "question": c.question, "expectation": c.expectation}
        for c in C.CHECKS
    ])


@app.get("/healthz")
def healthz():
    return jsonify(status="ok",
                   uptime_seconds=int((datetime.datetime.now(datetime.timezone.utc)
                                       - STARTED_AT).total_seconds()),
                   checks=len(C.CHECKS))


def _wire(check_id, r):
    return {"id": check_id, "verdict": r.verdict, "tone": C.TONE.get(r.verdict, "muted"),
            "headline": r.headline, "detail": r.detail, "evidence": r.evidence}


def _categories():
    seen = []
    for c in C.CHECKS:
        if c.category not in seen:
            seen.append(c.category)
    return seen


if __name__ == "__main__":
    app.run(host=os.getenv("HOST", "127.0.0.1"), port=int(os.getenv("PORT", "5000")))
