"""Everything this app says to Toft, and everything it asks it.

One module, on purpose. An example is worth reading only if the platform-facing
part is somewhere a person can find it, and the rest of the app is ordinary
FastAPI that would run anywhere.

It is called ``platform_seam`` rather than ``platform`` because Python already
has a stdlib module of that name, and shadowing it from an app's root directory
is a bug that surfaces three imports deep in somebody else's library.

**Every function here degrades.** Off-platform — a laptop, a test run, ``uvicorn
main:app`` from a checkout — there is no ``TOFT_APP_TOKEN``, and the SDK
raises rather than pretending. So each call is wrapped once, here, and returns a
"not available, and here is why" value that the page can render. The app is
never in the position of discovering at 2am that a feature was never switched
on: the capability rail says so on every screen.
"""

from __future__ import annotations

import os
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

try:                                    # installed into every app by the builder
    import toft
    from toft.errors import ToftError
    SDK = True
except Exception:                       # pragma: no cover - off-platform
    toft = None                           # type: ignore[assignment]
    ToftError = Exception          # type: ignore[misc,assignment]
    SDK = False


def on_platform() -> bool:
    """Whether there is a platform to talk to at all."""
    return SDK and bool(os.environ.get("TOFT_APP_TOKEN"))


# ── who this app is ─────────────────────────────────────────────────────────

_app_cache: tuple[float, dict] | None = None


def app_info(max_age: float = 30.0) -> dict:
    """``GET /api/v1/app/self`` — id, slug, mode, policy, capabilities.

    Cached for half a minute. An administrator attaching Slack should show up on
    the next page load without a redeploy, and a page that asked on every render
    would spend a round trip per request on an answer that changes twice a year.
    """
    global _app_cache
    now = time.monotonic()
    if _app_cache and now - _app_cache[0] < max_age:
        return _app_cache[1]

    info: dict[str, Any] = {"available": False, "reason": "", "capabilities": ()}
    if not on_platform():
        info["reason"] = ("no TOFT_APP_TOKEN in the environment — this process is not "
                          "running as a Toft workload")
    else:
        try:
            a = toft.app.refresh()
            info = {
                "available": True, "reason": "",
                "id": a.id, "slug": a.slug, "name": a.name, "mode": a.mode,
                "base_path": a.base_path, "policy": a.policy,
                "capabilities": a.capabilities,
            }
        except ToftError as exc:
            info["reason"] = str(exc)
    _app_cache = (now, info)
    return info


def can(capability: str) -> bool:
    return capability in (app_info().get("capabilities") or ())


def mode() -> str:
    return app_info().get("mode") or "unknown"


# ── storage and scratch ─────────────────────────────────────────────────────

def mounts() -> list:
    if not on_platform():
        return []
    try:
        return toft.mounts()
    except ToftError:
        return []


def first_writable_mount():
    for m in mounts():
        if not m.read_only:
            return m
    return None


def scratch_dir() -> Path | None:
    if not SDK:
        return None
    try:
        return toft.scratch_dir()
    except ToftError:
        return None


# ── who is looking, and what they may do ────────────────────────────────────
#
# Two calls, and which one to reach for is the decision:
#
#   caller(headers)  what may this request do — a signed role, verified locally,
#                    no round trip, no administrator switch. Reach for it to
#                    *refuse*.
#   who(headers)     who is this — a name, an email, groups. One round trip, and
#                    an administrator has to have switched viewer identity on.
#                    Reach for it to *describe*.
#
# A role names nobody, which is exactly why the two are not one call.

@dataclass
class Seen:
    role: str = "unknown"
    may_edit: bool = False
    name: str = ""
    email: str = ""
    groups: tuple[str, ...] = ()
    is_admin: bool = False
    identity_state: str = "off"      # off | anonymous | known | refused
    identity_note: str = ""


def seen(headers: Any) -> Seen:
    """Both halves, in the order that matters: refuse first, describe second."""
    out = Seen()
    if not SDK:
        # Off-platform the app is being read rather than run. Give the developer
        # the top rung so every screen is reachable, and say so on the rail.
        return Seen(role="owner", may_edit=True, name="local developer",
                    identity_state="off",
                    identity_note="Running outside Toft: the role header is absent and "
                                  "this page assumed owner so you can see all of it.")

    c = toft.caller(headers)
    out.role = c.role
    # `editor` is the rung that may decide an exception. `system` is Toft
    # firing a schedule and is deliberately not on the ladder, so it is named.
    out.may_edit = c.at_least(toft.ROLE_EDITOR) or c.role == toft.ROLE_SYSTEM

    try:
        me = toft.who(headers)
    except ToftError as exc:
        out.identity_state = "refused"
        out.identity_note = str(exc)
        return out
    if me is None:
        out.identity_state = "anonymous"
        out.identity_note = "Nobody is signed in, or this app may not be told who is."
        return out
    out.identity_state = "known"
    out.name = me.name or me.id
    out.email = me.email or ""
    out.groups = me.group_names
    out.is_admin = me.is_admin
    return out


# ── the app's one JSON document ─────────────────────────────────────────────
#
# Settings that outlive a restart, a redeploy and a rollback, and belong to
# nobody's session. It needs nothing switched on — no attachment, no capability,
# no administrator — which is why the tolerances live here and not in an
# environment variable somebody would have to redeploy to change.

DEFAULTS = {
    "tolerance_pence": 150,
    "date_window_days": 3,
    "period": "2026-06",
    "announce": False,
}


def settings() -> tuple[dict, int, str]:
    """The document, its version, and a note when there is no platform."""
    if not on_platform():
        return dict(DEFAULTS), 0, "Off-platform: these are the built-in defaults and are not stored."
    try:
        doc = toft.data.read()
    except ToftError as exc:
        return dict(DEFAULTS), 0, f"Could not read the document: {exc}"
    merged = dict(DEFAULTS)
    merged.update(doc.data or {})
    return merged, doc.version, ""


def save_settings(values: dict, if_version: int | None) -> tuple[int, str]:
    """Write with ``If-Match`` when the page knew a version.

    A conflict is a real answer rather than an error: two people had the tab
    open, and the second one should be told rather than silently winning.
    """
    if not on_platform():
        return 0, "Off-platform: nothing was stored."
    merged = dict(DEFAULTS)
    merged.update(values)
    try:
        doc = toft.data.set(merged, if_version=if_version)
    except toft.VersionConflict as exc:
        return exc.version, ("Somebody else saved these settings while this page was open. "
                             "Reload and try again.")
    except ToftError as exc:
        return if_version or 0, str(exc)
    return doc.version, ""


# ── background work ─────────────────────────────────────────────────────────
#
# A close over three months of two ledgers is seconds of work here and minutes
# on a real one, which is the whole argument for the job system: the run gets a
# container of its own, a memory limit and a deadline, and the page that started
# it returns immediately.

JOB_NAME = "close"


def jobs_available() -> tuple[bool, str]:
    if not on_platform():
        return False, "Not running on Toft."
    policy = app_info().get("policy") or {}
    jobs = policy.get("jobs") if isinstance(policy, dict) else None
    if isinstance(jobs, dict) and jobs.get("available") is False:
        return False, str(jobs.get("reason") or "Jobs are not available on this install.")
    return True, ""


def start_close(headers: Any, period: str | None, as_viewer: bool = True):
    """Start the close, attributed to whoever is looking at the page.

    ``toft.viewer(headers).jobs`` rather than ``toft.jobs`` is the whole decision:
    the first attributes the run to the person, the second to the app. It is two
    objects rather than a flag so that attribution cannot be lost by omission.
    """
    params = {"period": period} if period else {}
    if as_viewer and SDK:
        try:
            return toft.viewer(headers).jobs.start(JOB_NAME, params=params), ""
        except ToftError as exc:
            # No viewer to act for — a machine client, or a stranger. Fall back
            # to the app's own authority and say which happened.
            if getattr(exc, "code", "") in ("viewer_anonymous", "act_for_missing"):
                pass
            else:
                return None, str(exc)
    try:
        return toft.jobs.start(JOB_NAME, params=params), ""
    except ToftError as exc:
        return None, str(exc)


def runs(headers: Any, everyone: bool = False, limit: int = 20) -> tuple[list, str]:
    """The closes this page may show, newest first.

    ``everyone`` is every run of the job, read as the app — the caller decides
    who may ask for it, because the platform's own rule is that a run started
    in a person's name is theirs and its app's editors'. Otherwise this viewer's
    runs where there is a viewer, the app's where there is not.
    """
    if not on_platform():
        return [], "Not running on Toft."
    if not everyone:
        try:
            return toft.viewer(headers).jobs.mine(JOB_NAME, limit=limit), ""
        except ToftError:
            pass
    try:
        return toft.jobs.list(JOB_NAME, limit=limit), ""
    except ToftError as exc:
        return [], str(exc)


def run_of(headers: Any, run_id: str, everyone: bool = False):
    """One run, through the same door :func:`runs` used to list it.

    Read through the viewer, the platform answers 404 for somebody else's run;
    read as the app it answers for any. Which door is the caller's decision,
    made on the role — the same one that decides who sees everyone's closes.
    """
    if not on_platform():
        return None, "Not running on Toft."
    jobs = toft.jobs if everyone else toft.viewer(headers).jobs
    try:
        return jobs.get(run_id), ""
    except ToftError as exc:
        if not everyone and getattr(exc, "code", "") in ("viewer_anonymous", "act_for_missing"):
            return run_of(headers, run_id, everyone=True)
        return None, str(exc)


def cancel(headers: Any, run_id: str) -> str:
    """Stop a run, as whoever may.

    A viewer may cancel the runs they started; the app acting as itself, the
    runs with no person behind them. Try the person first — it is the narrower
    authority — and the app second, and report the platform's own refusal when
    neither may.
    """
    if not on_platform():
        return "Not running on Toft."
    last = ""
    for jobs in (toft.viewer(headers).jobs, toft.jobs):
        try:
            jobs.get(run_id).cancel()
            return ""
        except ToftError as exc:
            last = str(exc)
    return last


def last_success() -> str | None:
    """When the close last succeeded, by anybody — a time and nothing else.

    Read as the app, because "is month-end being closed" is the app's state
    rather than any one person's, and a timestamp names nobody. Off-platform,
    the register's own record of its last job close.
    """
    if on_platform():
        try:
            done = toft.jobs.list(JOB_NAME, status="succeeded", limit=1)
            return done[0].finished_at if done else None
        except ToftError:
            return None
    import ledger                 # here, not at the top: ledger imports this module
    for row in ledger.closes(25):
        if row["trigger"] in ("job", "schedule") and row["finished_at"]:
            return row["finished_at"]
    return None


def output_of(run) -> tuple[Any, str]:
    """The six numbers the worker wrote, or why there are none."""
    if not run.has_output:
        return None, ""
    try:
        return run.output_json(), ""
    except ToftError as exc:
        return None, str(exc)


# ── saying something ────────────────────────────────────────────────────────

def announce(text: str, title: str) -> str:
    """Post the close summary wherever an administrator attached this app.

    The app supplies the message. It does not name the channel, does not hold
    the bot token, and cannot turn either integration on — all three are the
    platform's, and asking for any of them is a 400 that says so.
    """
    if not on_platform():
        return "Not running on Toft."
    sent = []
    if can("slack.post"):
        try:
            # A Slack message has no title field; the heading is the first line.
            toft.notify.slack(f"*{title}*\n{text}")
            sent.append("Slack")
        except ToftError as exc:
            return f"Slack refused: {exc}"
    if can("email.send"):
        owner = os.environ.get("CLOSE_REPORT_TO", "").strip()
        if owner:
            try:
                toft.notify.email(to=[a.strip() for a in owner.split(",") if a.strip()],
                                subject=title, text=text)
                sent.append("email")
            except ToftError as exc:
                return f"Email refused: {exc}"
    if not sent:
        return ("Nothing is attached. An administrator attaches Slack or email on this "
                "app's Integrations tab; this app cannot switch either on.")
    return "Handed to " + " and ".join(sent) + ". Delivery happens after."


# ── the capability rail ─────────────────────────────────────────────────────

@dataclass
class Cap:
    label: str
    state: str            # on | off | warn
    note: str = ""


def rail(seen_: Seen | None = None) -> list[Cap]:
    """What this example demonstrates, and whether it is really here.

    Rendered on every page. The point of the gallery is not that these features
    exist — it is that an app can ask, and can say something useful when the
    answer is no.
    """
    info = app_info()
    caps: list[Cap] = []

    if not info.get("available"):
        # Off-platform, the rail still names everything this example is *for*.
        # A rail that collapsed to one grey chip would hide the list of things
        # worth looking at from the one reader most likely to be looking: a
        # person reading the repository rather than running it.
        why = info.get("reason", "")
        return [Cap(label, "off", why) for label in (
            "App data", "Background jobs", "Viewer identity", "Caller role",
            "Slack", "Email", "Storage")]

    caps.append(Cap(f"{info['mode'].title()} mode", "on", "GET /api/v1/app/self"))
    caps.append(Cap("App data", "on", "The settings below are the app's one JSON document."))

    ok, why = jobs_available()
    caps.append(Cap("Background jobs", "on" if ok else "off",
                    "A close runs in its own container." if ok else why))

    if seen_ is not None:
        state = {"known": "on", "anonymous": "warn", "refused": "off", "off": "off"}[seen_.identity_state]
        caps.append(Cap("Viewer identity", state, seen_.identity_note or "GET /api/v1/app/viewer"))
        caps.append(Cap(f"Role: {seen_.role}", "on" if seen_.may_edit else "warn",
                        "Signed by the platform, verified in this process, 120-second life."))

    caps.append(Cap("Slack", "on" if can("slack.post") else "off",
                    "Attached." if can("slack.post") else "An administrator has not attached it."))
    caps.append(Cap("Email", "on" if can("email.send") else "off",
                    "Attached." if can("email.send") else "An administrator has not attached it."))

    m = first_writable_mount()
    caps.append(Cap("Storage", "on" if m else "off",
                    f"Writable mount '{m.name}'." if m else
                    "No writable mount, so the register is on scratch."))
    return caps
