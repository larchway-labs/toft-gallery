"""Reconcile — a month-end desk, on FastAPI.

The framework's own shape is the reason it is here: typed request models, a
generated OpenAPI document, and a JSON API that the page in front of it is just
another client of. Everything the browser does on the Exceptions tab is a call
you could make from `curl`, and the API tab is generated from the same document
your tooling would read.

What it demonstrates about Toft, in the order the tabs run:

  * **the app's one JSON document** — the tolerances, saved with a version, so a
    second person with the page open is told rather than silently overwritten
  * **background jobs** — the close runs in a container of its own, attributed
    to whoever pressed the button, and the page never blocks on it
  * **the caller's role** — a signed header, verified in this process with no
    round trip, which is what decides who may write off an exception
  * **viewer identity** — a name and groups, which is a different question and a
    different call, and needs an administrator to have switched it on
  * **integrations** — the summary handed to Slack or email without this app
    holding a token or naming a channel
  * **storage** — where the register landed, and what that costs

Every one of those degrades. Run this on an install with none of them attached
and the page still works; the capability rail across the top says what is
missing and what the app did instead.
"""

from __future__ import annotations

import os
from typing import Annotated, Literal

from fastapi import Body, FastAPI, Header, HTTPException, Query, Request
from fastapi.responses import HTMLResponse, JSONResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from pydantic import BaseModel, Field

import engine
import ledger
import platform_seam as seam

app = FastAPI(
    title="Reconcile",
    version="1.0.0",
    summary="A month-end reconciliation desk, and a Toft example.",
    description=(
        "Every screen in this app is a client of these routes. The two that "
        "change something are gated on the caller's role, which Toft signs "
        "into every forwarded request — so an `lp_` app key minted with `viewer` "
        "can read the exception list and cannot decide one."
    ),
    docs_url="/openapi-docs",     # FastAPI's own, which needs a CDN; the API tab does not
    redoc_url=None,
)

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
app.mount("/static", StaticFiles(directory=os.path.join(BASE_DIR, "static")), name="static")
templates = Jinja2Templates(directory=os.path.join(BASE_DIR, "templates"))

ledger.init()


# ── helpers ─────────────────────────────────────────────────────────────────

def base_of(request: Request) -> str:
    """Where this app is mounted, as the browser sees it.

    uvicorn is started with `--root-path /apps/{slug}` and the proxy strips the
    prefix before forwarding, so this process is mounted at `/` and every link
    and asset URL it *emits* has to carry the prefix back. A `Location:` header
    must not — the proxy re-adds it, and a redirect that carries it lands on
    `/apps/slug/apps/slug`. That asymmetry is the one thing to get right.
    """
    return (request.scope.get("root_path") or "").rstrip("/")


def require_editor(request: Request) -> seam.Seen:
    who = seam.seen(request.headers)
    if not who.may_edit:
        raise HTTPException(
            status_code=403,
            detail=(f"This caller is '{who.role}'. Deciding an exception needs editor or "
                    f"owner on this app — ask its owner for access on the Sharing tab."),
        )
    return who


def period_or_400(period: str) -> str:
    if period not in ledger.PERIODS:
        raise HTTPException(400, f"Unknown period {period!r}. Try one of {', '.join(ledger.PERIODS)}.")
    return period


# ── the page ────────────────────────────────────────────────────────────────

@app.get("/", response_class=HTMLResponse, include_in_schema=False)
def page(request: Request, period: str = Query("")):
    cfg, version, note = seam.settings()
    period = period if period in ledger.PERIODS else cfg["period"]
    who = seam.seen(request.headers)

    close = ledger.latest_close(period)
    counts = ledger.counts(period)
    values, labels = ledger.daily_series(period)
    excs = ledger.exceptions(period)
    jobs_ok, jobs_why = seam.jobs_available()
    runs, runs_note = seam.runs(request.headers)

    # Only every third day gets a label, or the axis is a smear.
    axis = [l if i % 3 == 0 else "" for i, l in enumerate(labels)]

    return templates.TemplateResponse(request, "page.html", {
        "base": base_of(request),
        "period": period,
        "periods": ledger.PERIODS,
        "close": close,
        "counts": counts,
        "exceptions": excs,
        "kind_labels": ledger.KIND_LABELS,
        "kind_tones": ledger.KIND_TONES,
        "series": ",".join(str(v) for v in values),
        "axis": ",".join(axis),
        "cfg": cfg,
        "cfg_version": version,
        "cfg_note": note,
        "who": who,
        "rail": seam.rail(who),
        "app_info": seam.app_info(),
        "mounts": seam.mounts(),
        "where": ledger.where(),
        "jobs_ok": jobs_ok,
        "jobs_why": jobs_why,
        "runs": runs,
        "runs_note": runs_note,
        "money": ledger.money,
        "closes": ledger.closes(12),
    })


@app.get("/healthz", include_in_schema=False)
def healthz():
    return {"ok": True, "periods": list(ledger.PERIODS)}


# ── the API the page is a client of ─────────────────────────────────────────

class Summary(BaseModel):
    period: str
    ledger_rows: int = 0
    bank_rows: int = 0
    matched: int = 0
    exceptions: int = 0
    open: int = 0
    accepted: int = 0
    written_off: int = 0
    value_at_risk_pence: int = 0
    match_rate: float = 0.0
    closed_at: str | None = None


class Exception_(BaseModel):
    id: int
    kind: Literal["unmatched_ledger", "unmatched_bank", "amount_variance", "duplicate"]
    reference: str
    counterparty: str
    amount_pence: int
    delta_pence: int
    txn_date: str
    status: Literal["open", "accepted", "written_off"]
    note: str = ""


class Decision(BaseModel):
    status: Literal["open", "accepted", "written_off"]
    note: str = Field("", max_length=400,
                      description="Why. Kept on the row; a write-off with no reason is a "
                                  "question somebody asks in six months.")


class CloseRequest(BaseModel):
    period: str = Field(..., examples=["2026-06"])
    background: bool = Field(True, description=(
        "Run it as a Toft job — its own container, memory limit and deadline. "
        "False runs it in this web process, which is what an install with no isolated "
        "backend has to do and is exactly the thing the job system exists to avoid."))


class Settings(BaseModel):
    tolerance_pence: int = Field(150, ge=0, le=100_000)
    date_window_days: int = Field(3, ge=0, le=31)
    period: str = "2026-06"
    announce: bool = False


@app.get("/api/summary", response_model=Summary, tags=["Reading"])
def summary(period: str = Query("2026-06")):
    """The six numbers every other surface quotes."""
    period_or_400(period)
    close = ledger.latest_close(period)
    counts = ledger.counts(period)
    if close is None:
        return Summary(period=period, open=0)
    return Summary(
        period=period,
        ledger_rows=close["ledger_rows"], bank_rows=close["bank_rows"],
        matched=close["matched"], exceptions=close["exceptions"],
        open=counts["open"], accepted=counts["accepted"], written_off=counts["written_off"],
        value_at_risk_pence=counts["at_risk"],
        match_rate=round(close["matched"] / close["ledger_rows"], 4) if close["ledger_rows"] else 0.0,
        closed_at=close["finished_at"],
    )


@app.get("/api/exceptions", response_model=list[Exception_], tags=["Reading"])
def list_exceptions(period: str = Query("2026-06"), kind: str = "", status: str = ""):
    """What the close could not place, biggest first."""
    period_or_400(period)
    return [
        Exception_(id=r["id"], kind=r["kind"], reference=r["reference"],
                   counterparty=r["counterparty"], amount_pence=r["amount"],
                   delta_pence=r["delta"], txn_date=r["txn_date"],
                   status=r["status"], note=r["note"])
        for r in ledger.exceptions(period, kind=kind, status=status)
    ]


@app.post("/api/exceptions/{exc_id}/decide", tags=["Deciding"])
def decide(exc_id: int, decision: Decision, request: Request):
    """Accept an exception or write it off. **Editor or owner.**

    The gate is `toft.caller(headers)`, which reads a header Toft signs per
    app and per workload start with a two-minute life. It is verified in this
    process against a key the platform put in the environment — no round trip,
    no administrator switch, and nothing an app on the same loopback interface
    can forge.
    """
    require_editor(request)
    if ledger.exception(exc_id) is None:
        raise HTTPException(404, "No such exception on the current close.")
    ledger.decide(exc_id, decision.status, decision.note)
    return {"id": exc_id, "status": decision.status}


@app.post("/api/close", tags=["Deciding"])
def close(req: CloseRequest, request: Request):
    """Run the close. **Editor or owner.**

    Started as the person looking at the page rather than as the app, which is
    two different objects in the SDK rather than a flag — so a run cannot slide
    out of somebody's name by omission.
    """
    require_editor(request)
    period_or_400(req.period)
    cfg, _v, _n = seam.settings()

    if req.background:
        ok, why = seam.jobs_available()
        if not ok:
            return JSONResponse(status_code=409, content={
                "started": False, "reason": why,
                "hint": "Send background=false to run it in this process instead.",
            })
        run, err = seam.start_close(request.headers, req.period)
        if run is None:
            return JSONResponse(status_code=502, content={"started": False, "reason": err})
        return {"started": True, "run_id": run.id, "status": run.status,
                "deduplicated": run.deduplicated,
                "attributed": run.started_by_viewer,
                "queue_position": run.queue_position}

    result = engine.run_close(
        req.period,
        tolerance_pence=int(cfg["tolerance_pence"]),
        date_window_days=int(cfg["date_window_days"]),
        trigger="page")
    return {"started": True, "inline": True, "result": result,
            "summary": engine.summary_text(result)}


@app.get("/api/runs", tags=["Reading"])
def runs(request: Request):
    """This viewer's closes, resolved by the platform from the relayed token.

    The app never learns who they are. It hands back the blob it was given and
    Toft filters on the person behind it.
    """
    rs, note = seam.runs(request.headers)
    return {"note": note, "runs": [
        {"id": r.id, "status": r.status, "progress": r.progress,
         "started_by_viewer": r.started_by_viewer, "summary": r.progress_message,
         "failure_reason": r.failure_reason}
        for r in rs
    ]}


@app.get("/api/settings", response_model=Settings, tags=["Reading"])
def get_settings():
    """The app's one JSON document, merged over the built-in defaults."""
    cfg, _version, _note = seam.settings()
    return Settings(**cfg)


@app.put("/api/settings", tags=["Deciding"])
def put_settings(values: Settings, request: Request,
                 if_version: Annotated[int | None, Header(alias="X-Settings-Version")] = None):
    """Save the tolerances. **Editor or owner.**

    `X-Settings-Version` becomes an `If-Match` on the platform's document, so
    two people with the page open produce a refusal rather than a silent loss.
    """
    require_editor(request)
    version, err = seam.save_settings(values.model_dump(), if_version)
    if err:
        return JSONResponse(status_code=409, content={"saved": False, "version": version, "reason": err})
    return {"saved": True, "version": version}


@app.post("/api/announce", tags=["Deciding"])
def announce(request: Request, period: str = Body(embed=True, default="2026-06")):
    """Hand the close summary to whatever an administrator attached."""
    require_editor(request)
    period_or_400(period)
    close = ledger.latest_close(period)
    if close is None:
        raise HTTPException(409, "Nothing has been closed for that period yet.")
    counts = ledger.counts(period)
    text = engine.summary_text({
        "period": period, "matched": close["matched"], "ledger_rows": close["ledger_rows"],
        "exceptions": counts["open"], "value_at_risk": counts["at_risk"],
        "match_rate": close["matched"] / close["ledger_rows"] if close["ledger_rows"] else 0,
    })
    return {"message": seam.announce(text, f"Reconciliation — {period}"), "text": text}


# ── the scheduled task ──────────────────────────────────────────────────────

@app.post("/tasks/nightly-close", tags=["Deciding"])
def nightly_close(request: Request):
    """A path a scheduled task fires at.

    A task is an **HTTP request to a path this app published** — never a command
    — which is why it needs nothing switched on and why its ceiling is the
    install's request timeout rather than a job's deadline. Toft signs the
    firing with the same role header a page load carries, so this endpoint
    verifies it exactly as every other one does; `system` is the schedule firing
    on its own, and a person pressing *Run now* arrives as themselves.
    """
    who = require_editor(request)
    cfg, _v, _n = seam.settings()
    result = engine.run_close(cfg["period"],
                              tolerance_pence=int(cfg["tolerance_pence"]),
                              date_window_days=int(cfg["date_window_days"]),
                              trigger="schedule")
    line = engine.summary_text(result)
    out = {"fired_by": who.role, "result": result, "summary": line}
    if cfg.get("announce"):
        out["announced"] = seam.announce(line, f"Reconciliation — {result['period']}")
    return out
