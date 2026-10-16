"""Request Desk — an app that decides what you may do from who Toft says you are.

The whole point of this app is the seam. Toft answers one question —
*who is looking at this page* — and answers it with an identity the app cannot
forge: the browser's request arrives carrying a blob the platform signed, the
app relays it back, and the platform resolves it against the real principal.
Everything after that is the app's own business, and here that business is an
embedded SQLite database holding a directory of people, two groups, and the
requests those people file.

    Toft                          this app
    ─────────                          ────────
    email, name, role, groups   ──▶    directory lookup on the address
                                       ──▶ desk group membership
                                           ──▶ submit / see all / decide

**Three sources of authority, deliberately not one.**

*The platform's role on the app* — owner, editor, or an administrator of the
install — is used for exactly one thing: administering the desk. Somebody has
to be able to add the first person to an empty directory, and that somebody is
whoever Toft already trusts with the deployment. It is never used for
approving a request, because "may redeploy this app" is not "may approve an
expense" and an app that conflates them has invented an authority nobody
granted.

*A desk group an editor put you in* is the ordinary path, and the one the
directory page is for.

*A desk group linked to a Toft group* is the same thing with the platform
doing the bookkeeping — put the finance team in the `finance` group once, and
every desk that links to it follows.

**The address is the identity.** Toft withholds the email address from an
app whose visibility is `public` — a public app's visitors did not consent to
being on a list by visiting — so this app must not be public. It says so on the
page rather than looking broken, which is the honest failure and the one worth
demonstrating.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from fastapi import FastAPI, Form, Request
from fastapi.responses import HTMLResponse, JSONResponse, PlainTextResponse, RedirectResponse

import render
from desk import Caller, Store

TITLE = os.getenv("DESK_TITLE", "Request Desk")

# The release this tree is, matching the tag the gallery catalog names. It is
# in the footer of every page because an extension is updated in place: "which
# version is actually serving" is otherwise a question you answer by reading a
# deploy log, and after a redeploy it is the first thing anybody asks.
VERSION = "1.0.2"

app = FastAPI(title=TITLE, docs_url=None, redoc_url=None)


# --------------------------------------------------------------------------
# The Toft SDK, and life without it.
#
# The SDK is installed into every Python app by the platform's build, so it is
# never a dependency this app declares. Off-platform — a local `python main.py`,
# a test run — the import fails, and that is a state to render rather than a
# crash: the page says what is missing and DEV_IDENTITY lets a developer stand
# in for a viewer while working on the pages themselves.
# --------------------------------------------------------------------------

try:
    import toft
    from toft.errors import ToftError
except ImportError:  # pragma: no cover - the platform always provides it
    toft = None

    class ToftError(RuntimeError):
        status = None
        code = ""


ON_PLATFORM = bool(toft and os.getenv("TOFT_APP_TOKEN"))


def _database_path() -> tuple[str, str]:
    """Where the SQLite file lives, and one sentence about whether it survives.

    A volume is the right home and the platform will name one if an
    administrator mapped it. Without one the file still works — it is just on
    the workload's own filesystem, which a restart replaces. Saying so on the
    page is better than a demo that quietly loses a week of requests.
    """
    configured = (os.getenv("DB_PATH") or "").strip()
    if configured:
        return configured, f"SQLite at {configured}, from DB_PATH."
    if ON_PLATFORM:
        try:
            for mount in toft.mounts():
                if mount.kind == "volume" and not mount.read_only:
                    return str(Path(mount.path) / "request-desk.sqlite"), (
                        f"SQLite on the volume {mount.name!r}, which is durable across restarts."
                    )
        except Exception:  # a storage read is never worth a 500 on every page
            pass
    return "request-desk.sqlite", (
        "SQLite beside the app on the workload's own filesystem — <strong>this is lost on "
        "restart</strong>. Map a volume to the app and it will be used instead."
    )


DB_PATH, DB_NOTE = _database_path()
store = Store(DB_PATH)
store.initialize()


class Context:
    """Who is looking, what they may do, and why the app knows (or does not)."""

    def __init__(self, state: str, caller: Caller, message: str = "") -> None:
        self.state = state          # ok | anonymous | disabled | no_email | offplatform
        self.caller = caller
        self.message = message
        self.abilities = store.abilities(caller)

    @property
    def ok(self) -> bool:
        return self.state == "ok"


def _dev_caller() -> Caller | None:
    """A stand-in viewer for local development, honoured only off-platform.

    `DEV_IDENTITY="rosa@example.com|Rosa Iqbal|editor|admin|finance,ops"`. It is
    read only when there is no app token, so setting it on a deployed app does
    nothing: an app that could be told who its viewer is by an environment
    variable would have no identity model at all, only a habit.
    """
    if ON_PLATFORM:
        return None
    raw = (os.getenv("DEV_IDENTITY") or "").strip()
    if not raw:
        return None
    parts = (raw.split("|") + ["", "", "", "", ""])[:5]
    return Caller(
        email=parts[0],
        name=parts[1],
        role=parts[2],
        is_admin=parts[3].strip().lower() in ("1", "admin", "true", "yes"),
        lp_groups=tuple(g.strip() for g in parts[4].split(",") if g.strip()),
    )


def context(request: Request) -> Context:
    """Resolve the viewer, or the reason there is not one.

    ``toft.who`` returns ``None`` for an anonymous visitor — an answer, not a
    failure — and raises for the one thing that is neither: an app an
    administrator has not switched viewer identity on for. Those two are kept
    apart here for the same reason the platform keeps 204 and 403 apart.
    """
    dev = _dev_caller()
    if dev is not None:
        return Context("ok", dev, "Identity from DEV_IDENTITY — this app is not running on Toft.")
    if not ON_PLATFORM:
        return Context(
            "offplatform", Caller(),
            "This app is not running on Toft, so there is nobody to name. Deploy it, or set "
            "DEV_IDENTITY to work on the pages locally.",
        )

    try:
        me = toft.who(request.headers)
    except ToftError as exc:
        if getattr(exc, "status", None) == 403:
            return Context(
                "disabled", Caller(),
                "Toft knows who you are and will not tell this app. An administrator turns on "
                "viewer identity for the app — it is off by default, and until it is on this desk "
                "cannot recognise anybody.",
            )
        return Context("offplatform", Caller(), f"Toft could not be asked who you are: {exc}")

    if me is None:
        return Context(
            "anonymous", Caller(),
            "Nobody is signed in on this request. That is an anonymous visitor, an API key acting "
            "for no person, or somebody whose access was revoked while the page stayed open.",
        )

    caller = Caller(
        email=me.email or "",
        name=me.name or "",
        role=me.role or "",
        is_admin=bool(me.is_admin),
        lp_groups=tuple(me.group_names),
    )
    if not caller.email:
        return Context(
            "no_email", caller,
            "Toft gave a name but no email address, which it withholds from any app whose "
            "visibility is public. This desk identifies people by address, so it must not be "
            "public — change the app's visibility and everything below starts working.",
        )
    if caller.name:
        store.update_name(caller.email, caller.name)
    return Context("ok", caller, "")


def base(request: Request) -> str:
    return request.scope.get("root_path", "") or ""


def flash(request: Request) -> tuple[str, str]:
    return (request.query_params.get("msg", ""), request.query_params.get("kind", ""))


def back(request: Request, where: str = "", msg: str = "", kind: str = "") -> RedirectResponse:
    """Every mutation ends in a redirect carrying its own sentence.

    The target is deliberately un-prefixed — see
    :func:`render.redirect_target`, which is where the reason is written down.
    """
    return RedirectResponse(render.redirect_target(where, msg, kind), status_code=303)


def source_line(ctx: Context) -> str:
    if ctx.state == "ok" and ctx.message:
        return render.e(ctx.message)
    return ("Everything above came from Toft's <code>/api/v1/app/viewer</code>, resolved from a "
            "blob this app relays and cannot forge. Everything it grants came from the desk's own "
            "SQLite database.")


def footer(request: Request) -> str:
    return (f'Version <code>{render.e(VERSION)}</code> · {DB_NOTE} · '
            f'identity at <code>{render.e(base(request))}/access.json</code> · '
            f'health at <code>{render.e(base(request))}/healthz</code>')


def blocked(request: Request, ctx: Context, heading: str) -> HTMLResponse:
    """The page somebody sees when the platform did not name them.

    Deliberately not an error page: none of these states is the visitor's
    mistake, and three of the four are a setting somebody else has to change.
    """
    body = render.blocked_panel(heading, f"<p>{render.e(ctx.message)}</p>")
    return HTMLResponse(render.page(
        base(request), TITLE, TITLE,
        "Who may do what here is decided from the identity Toft supplies.",
        body, abilities=ctx.abilities, footer=footer(request),
    ))


def require(request: Request, ctx: Context, ability: str) -> HTMLResponse | None:
    """Server-side enforcement, which is the only enforcement.

    The navigation hides what you may not do; this refuses it. A page that only
    hid it would be a demonstration of how not to do this.
    """
    if not ctx.ok:
        return blocked(request, ctx, "There is nobody to recognise")
    if not getattr(ctx.abilities, ability):
        body = render.blocked_panel(
            "Not yours to do",
            "<p>Your identity does not carry that here. What it does carry is below.</p>",
        ) + render.identity_card(ctx.caller, ctx.abilities, source=source_line(ctx))
        return HTMLResponse(render.page(
            base(request), TITLE, TITLE, "", body, abilities=ctx.abilities,
            footer=footer(request)), status_code=403)
    return None


# --------------------------------------------------------------------------
# Pages
# --------------------------------------------------------------------------


@app.get("/", response_class=HTMLResponse)
def home(request: Request):
    ctx = context(request)
    if not ctx.ok:
        return blocked(request, ctx, {
            "anonymous": "Nobody is signed in",
            "disabled": "This app has not been told who you are",
            "no_email": "No address, and the address is the identity",
            "offplatform": "Not running on Toft",
        }.get(ctx.state, "Nobody to recognise"))

    msg, kind = flash(request)
    body = render.identity_card(ctx.caller, ctx.abilities, source=source_line(ctx))
    if ctx.abilities.submit:
        body += render.submit_form(base(request))
    mine = store.requests(requester=ctx.caller.email)
    body += "<h2>My requests</h2>" + render.request_rows(
        base(request), mine, viewer_email=ctx.caller.email,
        can_decide=False, show_requester=False,
    )
    return HTMLResponse(render.page(
        base(request), TITLE, TITLE,
        "What you may do here was decided from who Toft says you are.",
        body, here="", abilities=ctx.abilities, flash=msg, flash_kind=kind,
        footer=footer(request),
    ))


@app.get("/queue", response_class=HTMLResponse)
def queue(request: Request, status: str = ""):
    ctx = context(request)
    refusal = require(request, ctx, "view_all")
    if refusal:
        return refusal
    msg, kind = flash(request)
    counts = store.counts()
    rows = store.requests(status=status)
    tabs = " · ".join(
        f'<a href="{render.e(base(request))}/queue{"?status=" + s if s else ""}">{s or "everything"}</a>'
        f' ({counts[s] if s else sum(counts.values())})'
        for s in ("", "pending", "approved", "rejected", "withdrawn")
    )
    body = f'<p class="mini">{tabs}</p>' + render.request_rows(
        base(request), rows, viewer_email=ctx.caller.email,
        can_decide=ctx.abilities.decide, show_requester=True,
    )
    return HTMLResponse(render.page(
        base(request), TITLE, "Queue",
        "Every request on this desk. You see it because your identity carries "
        + ("the decision" if ctx.abilities.decide else "read access") + ".",
        body, here="queue", abilities=ctx.abilities, flash=msg, flash_kind=kind,
        footer=footer(request),
    ))


@app.get("/people", response_class=HTMLResponse)
def people(request: Request):
    ctx = context(request)
    refusal = require(request, ctx, "manage")
    if refusal:
        return refusal
    msg, kind = flash(request)
    body = render.people_page(base(request), store.people(), store.groups())
    return HTMLResponse(render.page(
        base(request), TITLE, "People",
        "The directory. An address here is a person this desk recognises when Toft names them.",
        body, here="people", abilities=ctx.abilities, flash=msg, flash_kind=kind,
        footer=footer(request),
    ))


@app.get("/groups", response_class=HTMLResponse)
def groups(request: Request):
    ctx = context(request)
    refusal = require(request, ctx, "manage")
    if refusal:
        return refusal
    msg, kind = flash(request)
    body = render.groups_page(base(request), store.groups())
    return HTMLResponse(render.page(
        base(request), TITLE, "Groups",
        "A group is what an address is worth here. Two exist to begin with; they are ordinary rows.",
        body, here="groups", abilities=ctx.abilities, flash=msg, flash_kind=kind,
        footer=footer(request),
    ))


@app.get("/access", response_class=HTMLResponse)
def access(request: Request):
    """The demonstration page: the identity, and the decision it produced."""
    ctx = context(request)
    payload = _access_payload(ctx)
    body = render.identity_card(ctx.caller, ctx.abilities, source=source_line(ctx))
    if not ctx.ok:
        body = render.blocked_panel("No identity on this request", f"<p>{render.e(ctx.message)}</p>") + body
    body += ("<h2>What the app was told, and what it decided</h2>"
             f"<pre>{render.e(json.dumps(payload, indent=2, ensure_ascii=False))}</pre>")
    return HTMLResponse(render.page(
        base(request), TITLE, "Access",
        "The same thing the pages use, written out. This is the whole seam.",
        body, here="access", abilities=ctx.abilities, footer=footer(request),
    ))


@app.get("/access.json")
def access_json(request: Request):
    return JSONResponse(_access_payload(context(request)))


def _access_payload(ctx: Context) -> dict:
    a = ctx.abilities
    return {
        "state": ctx.state,
        "note": ctx.message,
        "toft_said": {
            "email": ctx.caller.email or None,
            "name": ctx.caller.name or None,
            "role": ctx.caller.role or None,
            "is_admin": ctx.caller.is_admin,
            "groups": list(ctx.caller.lp_groups),
        },
        "desk_decided": {
            "in_directory": a.known,
            "active": a.active,
            "desk_groups": list(a.groups),
            "manage": a.manage,
            "submit": a.submit,
            "view_all": a.view_all,
            "decide": a.decide,
        },
        "because": list(a.why),
    }


@app.get("/healthz", response_class=PlainTextResponse)
def healthz():
    """No identity, no database read beyond the one that proves it opens."""
    with store.connect() as conn:
        conn.execute("SELECT 1").fetchone()
    return "ok"


# --------------------------------------------------------------------------
# Mutations. Every one of them re-resolves the identity and checks it.
# --------------------------------------------------------------------------


@app.post("/requests")
def create_request(request: Request, title: str = Form(""), category: str = Form(""),
                   details: str = Form(""), amount: str = Form("")):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.submit:
        return back(request, "", "Your identity does not carry submitting here.", "bad")
    try:
        rid = store.submit(ctx.caller, title, category, details, amount)
    except ValueError as exc:
        return back(request, "", str(exc), "bad")
    return back(request, "", f"Request #{rid} submitted.", "good")


@app.post("/requests/{request_id}/decide")
def decide_request(request: Request, request_id: int, verdict: str = Form(""), note: str = Form("")):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.decide:
        return back(request, "queue", "Your identity does not carry deciding here.", "bad")
    try:
        msg = store.decide(ctx.caller, request_id, verdict, note)
    except ValueError as exc:
        return back(request, "queue", str(exc), "bad")
    return back(request, "queue", msg, "good")


@app.post("/requests/{request_id}/withdraw")
def withdraw_request(request: Request, request_id: int):
    ctx = context(request)
    if not ctx.ok:
        return back(request, "", "There is nobody to withdraw as.", "bad")
    try:
        msg = store.withdraw(ctx.caller, request_id)
    except ValueError as exc:
        return back(request, "", str(exc), "bad")
    return back(request, "", msg, "good")


@app.post("/people")
def add_person(request: Request, email: str = Form(""), name: str = Form("")):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.manage:
        return back(request, "", "Only somebody who administers this desk adds people.", "bad")
    try:
        msg = store.add_person(email, name, ctx.caller.email)
    except ValueError as exc:
        return back(request, "people", str(exc), "bad")
    return back(request, "people", msg, "good")


@app.post("/people/{email}/active")
def set_active(request: Request, email: str, active: str = Form("1")):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.manage:
        return back(request, "", "Only somebody who administers this desk changes the directory.", "bad")
    on = active == "1"
    store.set_person_active(email, on)
    return back(request, "people", f"{email} {'reactivated' if on else 'deactivated'}.", "good")


@app.post("/people/{email}/delete")
def delete_person(request: Request, email: str):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.manage:
        return back(request, "", "Only somebody who administers this desk changes the directory.", "bad")
    store.remove_person(email)
    return back(request, "people", f"{email} removed. Their requests stay on the queue.", "good")


@app.post("/people/{email}/groups")
def set_membership(request: Request, email: str, group_id: int = Form(...), member: str = Form("1")):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.manage:
        return back(request, "", "Only somebody who administers this desk changes groups.", "bad")
    store.set_membership(email, group_id, member == "1")
    return back(request, "people", f"Group membership updated for {email}.", "good")


@app.post("/groups")
def create_group(request: Request, name: str = Form(""), description: str = Form(""),
                 lp_group: str = Form(""), can_submit: str = Form(""),
                 can_view_all: str = Form(""), can_decide: str = Form("")):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.manage:
        return back(request, "", "Only somebody who administers this desk creates groups.", "bad")
    try:
        msg = store.create_group(name, description, bool(can_submit), bool(can_view_all),
                                 bool(can_decide), lp_group)
    except ValueError as exc:
        return back(request, "groups", str(exc), "bad")
    return back(request, "groups", msg, "good")


@app.post("/groups/{group_id}")
def update_group(request: Request, group_id: int, description: str = Form(""),
                 lp_group: str = Form(""), can_submit: str = Form(""),
                 can_view_all: str = Form(""), can_decide: str = Form("")):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.manage:
        return back(request, "", "Only somebody who administers this desk changes groups.", "bad")
    store.update_group(group_id, description, bool(can_submit), bool(can_view_all),
                       bool(can_decide), lp_group)
    return back(request, "groups", "Group saved.", "good")


@app.post("/groups/{group_id}/delete")
def delete_group(request: Request, group_id: int):
    ctx = context(request)
    if not ctx.ok or not ctx.abilities.manage:
        return back(request, "", "Only somebody who administers this desk changes groups.", "bad")
    store.delete_group(group_id)
    return back(request, "groups", "Group deleted.", "good")


# Local development: `python main.py` serves the same app uvicorn would.
if __name__ == "__main__":  # pragma: no cover
    import uvicorn

    uvicorn.run(
        app,
        host=os.getenv("HOST", "127.0.0.1"),
        port=int(os.getenv("PORT", "8000")),
        root_path=os.getenv("BASE_PATH", ""),
    )
