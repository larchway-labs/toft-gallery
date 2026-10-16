"""What this app knows about Toft.

Two questions, and which one to reach for is the decision this module exists to
make obvious:

* :func:`role` — **what may this caller do.** A word, signed by the platform per
  app and per workload start, verified in this process with `hmac`. No round
  trip, no administrator switch, and it is present on every forwarded request.
  Reach for it to *refuse*.

* :func:`viewer` — **who is this.** A name, an email and groups, from
  ``GET /api/v1/app/viewer``. One round trip, and an administrator has to have
  switched viewer identity on for this app. Reach for it to *describe*.

A role names nobody, which is exactly why the two are not one call. An app that
gates on identity when it means authority has built a slower, more fragile
version of the first one — and has asked an administrator for a setting it did
not need.

Everything here degrades. Off-platform the SDK raises rather than pretending,
this module catches it once, and the page says so on the capability rail.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import os
import time
from dataclasses import dataclass

try:                                    # installed into every app by the builder
    import toft
    from toft.errors import ToftError
    SDK = True
except Exception:                       # pragma: no cover - off-platform
    toft = None                           # type: ignore[assignment]
    ToftError = Exception          # type: ignore[misc,assignment]
    SDK = False

# The six words the platform will ever put on the wire, and there is no
# seventh. `unknown` is this end's word for a failure — absent header, absent
# key, bad MAC, expired blob — and it is never privileged.
ROLES = ("anonymous", "viewer", "editor", "owner", "system", "app")
UNKNOWN = "unknown"
LADDER = {"anonymous": 0, "viewer": 1, "editor": 2, "owner": 3}


def role(headers) -> str:
    """The caller's role, verified locally against this workload's key.

    Wire format::

        X-Toft-Role: v1.<role>.<base64url app id>.<expiry unix>.<mac>

    The MAC is HMAC-SHA256 over ``b"toft-role\\x00"`` plus the payload,
    keyed with `TOFT_ROLE_KEY`, base64url with the padding stripped.

    **Why it is signed rather than just a word.** Stripping an inbound header
    stops a visitor forging one; it does nothing about the case that matters. In
    shared mode apps bind loopback and app-to-app isolation is an accepted risk,
    so a neighbour on the same box can open a socket to this port and send
    whatever headers it likes without the gateway ever seeing the request. The
    key is derived per app and per workload start, so a blob minted for another
    app fails the MAC here — which is why this function does not need to know
    its own app id.

    Every failure returns ``unknown``, deliberately. Four different failures with
    one safe answer: an author debugging why their editor check refuses does not
    need four branches, and a verification that distinguished them would be a
    verification whose failure modes are an oracle.
    """
    key = os.environ.get("TOFT_ROLE_KEY", "")
    blob = _header(headers, "X-Toft-Role")
    if not key or not blob:
        return UNKNOWN

    payload, _, sig = blob.rpartition(".")
    if not payload:
        return UNKNOWN

    mac = hmac.new(key.encode(), b"toft-role\x00" + payload.encode(),
                   hashlib.sha256).digest()
    want = base64.urlsafe_b64encode(mac).decode().rstrip("=")
    # Constant time, and before anything is parsed: structure decoded ahead of
    # authentication is how a verifier grows a second entry point.
    if not hmac.compare_digest(sig, want):
        return UNKNOWN

    fields = payload.split(".")
    if len(fields) != 4 or fields[0] != "v1":
        return UNKNOWN
    try:
        if int(fields[3]) <= time.time():
            return UNKNOWN
    except ValueError:
        return UNKNOWN

    # A word this app does not know is `unknown` rather than passed through:
    # this app holds the key and could sign anything with it, and code that
    # switches on six values must not be handed a seventh.
    return fields[1] if fields[1] in ROLES else UNKNOWN


def at_least(word: str, want: str) -> bool:
    """Whether a role is at least ``want`` on the person ladder.

    `system` and `app` are not on the ladder and are False for every rung —
    they are not more or less authorized than a person, they are a different
    kind of caller, and ranking the platform's scheduler above an owner would be
    a guess this app has no business making.
    """
    mine, target = LADDER.get(word), LADDER.get(want)
    return mine is not None and target is not None and mine >= target


@dataclass
class Viewer:
    """Who is looking, or the reason there is no answer."""

    state: str = "off"          # off | anonymous | known | refused | no-sdk
    name: str = ""
    email: str = ""
    role: str = ""
    is_admin: bool = False
    groups: tuple[str, ...] = ()
    note: str = ""


def viewer(headers) -> Viewer:
    """``GET /api/v1/app/viewer``, and the three answers it can give.

    **204, 403 and a person are three different things**, and collapsing any two
    of them is how an app ends up telling somebody the wrong story:

    * a person — an administrator switched viewer identity on and somebody is
      signed in;
    * ``anonymous`` — nobody is signed in, or the session went away while the
      page stayed open. Show nothing personal;
    * ``refused`` — there *is* a viewer and this app may not be told. That is a
      configuration gap to close, not a runtime state to branch on.

    ``email`` is absent when the app's visibility is public: a public app's
    visitors did not consent to a mailing list by visiting.
    """
    if not SDK:
        return Viewer(state="no-sdk",
                      note="The Toft SDK is not importable, so this is a local run.")
    if not os.environ.get("TOFT_APP_TOKEN"):
        return Viewer(state="no-sdk",
                      note="No TOFT_APP_TOKEN: this process is not a Toft workload.")
    try:
        me = toft.who(headers)
    except ToftError as exc:
        return Viewer(state="refused", note=str(exc))
    if me is None:
        return Viewer(state="anonymous",
                      note="Nobody is signed in, or this app may not be told who is.")
    return Viewer(state="known", name=me.name or me.id, email=me.email or "",
                  role=me.role, is_admin=me.is_admin, groups=me.group_names)


def rail(role_word: str, who: Viewer) -> list[tuple[str, str, str]]:
    """The capability rail every example in the gallery carries."""
    prefix = os.environ.get("BASE_PATH", "")
    identity_state = {"known": "on", "anonymous": "warn",
                      "refused": "off", "no-sdk": "off", "off": "off"}[who.state]
    return [
        ("Flask", "on", "Detected from `flask` in requirements.txt."),
        ("ProxyFix", "on", "x_prefix=1, so url_for() emits a link that works."),
        ("Base path", "on" if prefix else "off", prefix or "Not mounted under a prefix here."),
        ("Role: " + role_word, "on" if role_word != UNKNOWN else "off",
         "X-Toft-Role, verified in this process."),
        ("Viewer identity", identity_state, who.note or "GET /api/v1/app/viewer"),
        ("Toft workload", "on" if on_platform() else "off",
         "Running as a Toft workload." if on_platform() else "This is a local run."),
    ]


def facts() -> list[tuple[str, str, str]]:
    """The environment, and what each variable is actually for."""
    def v(name):
        return os.environ.get(name, "")

    return [
        ("PORT", v("PORT"),
         "The port to listen on. A hardcoded port is a green build and an app "
         "nobody can reach."),
        ("HOST", v("HOST") or "127.0.0.1 (this app's default)",
         "The address to bind. Loopback in shared mode — an app on 0.0.0.0 has "
         "stepped around the platform's authorization entirely."),
        ("BASE_PATH", v("BASE_PATH") or "(none)",
         "Where this app is mounted. The proxy strips it before forwarding, so "
         "routes match at '/' and this is needed only for URLs that leave the "
         "process."),
        ("TOFT_APP_SLUG", v("TOFT_APP_SLUG") or "—", "This app's slug."),
        ("TOFT_EXECUTION_MODE", v("TOFT_EXECUTION_MODE") or "—",
         "shared or isolated. A property of the deployment, never a backend name."),
        ("TOFT_SCRATCH_DIR", v("TOFT_SCRATCH_DIR") or "—",
         "This app's own corner of the disk, wiped on every deploy and restart."),
        ("TOFT_ROLE_KEY", "set" if v("TOFT_ROLE_KEY") else "—",
         "Per app, per workload start, derived and never stored. What the role "
         "header above is verified against."),
        ("TOFT_APP_TOKEN", "set" if v("TOFT_APP_TOKEN") else "—",
         "The app calling out: what `toft.who()` above authenticates with. Minted per "
         "deployment, never shown to a person and never re-mintable — a deploy is how "
         "it rotates."),
    ]


def on_platform() -> bool:
    return bool(os.environ.get("TOFT_APP_SLUG") or os.environ.get("TOFT_ROLE_KEY"))


def _header(headers, name: str) -> str:
    """Case-insensitive lookup that works on http.server's message object."""
    if headers is None:
        return ""
    getter = getattr(headers, "get", None)
    if getter is not None:
        value = getter(name) or getter(name.lower()) or ""
        return value if isinstance(value, str) else str(value)
    return ""
