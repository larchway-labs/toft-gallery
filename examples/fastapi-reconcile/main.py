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

import declaration
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
        # The Runs tab is painted by app.js from this, and repainted from
        # /api/runs while anything is open — one renderer, so the first paint
        # and the hundredth cannot disagree about what a status looks like.
        "runs_doc": runs_doc(request, "mine"),
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


def run_json(r) -> dict:
    """A run as the page draws it: the platform's fields, nothing invented."""
    return {
        "id": r.id, "status": r.status, "started_by": r.started_by,
        "rerun_of": r.rerun_of, "params": r.params, "scheduled_for": r.scheduled_for,
        "queued_at": r.queued_at, "started_at": r.started_at, "finished_at": r.finished_at,
        "deadline": r.deadline, "progress": r.progress, "summary": r.progress_message,
        "queue_position": r.queue_position, "has_output": r.has_output,
        "exit_code": r.exit_code, "failure_reason": r.failure_reason,
        "peak_memory_mb": r.peak_memory_mb, "status_url": r.status_url,
    }


Scope = Literal["mine", "everyone"]


def scope_or_403(request: Request, scope: str) -> bool:
    """Whether to read as the app. **Everyone's closes need editor or owner.**

    The platform's rule for a run started in a person's name is that it is
    theirs and its app's editors' — its status page answers 404 to anybody
    else. Reading as the app would let this page widen that, so the page
    applies the same rule before it reads.
    """
    if scope != "everyone":
        return False
    who = seam.seen(request.headers)
    if not who.may_edit:
        raise HTTPException(403, (f"This caller is '{who.role}'. Everyone's closes need editor "
                                  f"or owner; your own are under scope=mine."))
    return True


def runs_doc(request: Request, scope: str) -> dict:
    everyone = scope_or_403(request, scope)
    rs, note = seam.runs(request.headers, everyone=everyone)
    decl = declaration.read()
    return {
        "scope": scope, "note": note, "runs": [run_json(r) for r in rs],
        "may_edit": seam.seen(request.headers).may_edit,
        "last_success": seam.last_success(),
        "declaration": {
            "schedule": decl.schedule, "timezone": decl.timezone, "overlap": decl.overlap,
            "timeout": decl.timeout, "memory_mb": decl.memory_mb, "notify": decl.notify,
            "choices": decl.choices,
            "next_at": decl.next_at.isoformat() if decl.next_at else None,
        },
    }


@app.get("/api/runs", tags=["Reading"])
def runs(request: Request, scope: Scope = "mine"):
    """Closes, newest first: yours, or everyone's for an editor.

    Yours are resolved by the platform from the token this app relays — the
    app never learns who you are, it hands the blob back and Toft filters on
    the person behind it. With them, the job's declaration and its next firing.
    """
    return runs_doc(request, scope)


def run_or_404(request: Request, run_id: str, scope: str):
    run, err = seam.run_of(request.headers, run_id, everyone=scope_or_403(request, scope))
    if run is None:
        raise HTTPException(404, err or "No such run.")
    return run


@app.get("/api/runs/{run_id}", tags=["Reading"])
def run_detail(run_id: str, request: Request, scope: Scope = "mine"):
    """One close, with the six numbers it wrote as its output."""
    run = run_or_404(request, run_id, scope)
    output, why = seam.output_of(run) if run.status == "succeeded" else (None, "")
    return {**run_json(run), "output": output, "output_note": why}


@app.get("/api/runs/{run_id}/logs", tags=["Reading"])
def run_logs(run_id: str, request: Request, scope: Scope = "mine", after: int = 0):
    """The close's log after line `after` — the worker's stdout, as Toft kept it."""
    run = run_or_404(request, run_id, scope)
    return {"status": run.status, "lines": run.logs(after_seq=after)}


@app.post("/api/runs/{run_id}/cancel", tags=["Deciding"])
def run_cancel(run_id: str, request: Request):
    """Stop a close that is queued or running. **Editor or owner.**"""
    require_editor(request)
    err = seam.cancel(request.headers, run_id)
    if err:
        return JSONResponse(status_code=409, content={"cancelled": False, "reason": err})
    return {"cancelled": True}


@app.post("/api/runs/{run_id}/again", tags=["Deciding"])
def run_again(run_id: str, request: Request, scope: Scope = "mine"):
    """Close the same month again, in your name. **Editor or owner.**

    A new run with the original's params rather than a re-run: the app's
    surface starts runs and does not re-run them, because `rerun_of` and the
    original's `scheduled_for` are a person's deliberate act in Toft's own UI
    or `toft job rerun`. The answer carries that command for whoever wants it.
    """
    require_editor(request)
    old = run_or_404(request, run_id, scope)
    run, err = seam.start_close(request.headers, (old.params or {}).get("period"))
    if run is None:
        return JSONResponse(status_code=502, content={"started": False, "reason": err})
    return {"started": True, "run_id": run.id, "deduplicated": run.deduplicated,
            "attributed": run.started_by_viewer, "queue_position": run.queue_position,
            "rerun_hint": f"toft job rerun {old.id[:8]}"}


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

