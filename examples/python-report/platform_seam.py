"""What this app knows about Toft, using nothing but the standard library.

The rest of the gallery imports `toft`, the SDK the platform installs into
every app. This one deliberately does not — the whole point of the example is
that the platform's contract is **environment variables and HTTP headers**, and
that you can honour all of it with `os`, `hmac`, `hashlib` and `base64`.

Two things are worth reading here:

* :func:`facts` — the environment an app is handed, and what each variable is
  for. Get `PORT` or `HOST` wrong and you have a green build nobody can reach,
  or an app on `0.0.0.0` that has stepped around the platform's authorization
  entirely.

* :func:`role` — the signed role header, verified in about fifteen lines. This
  is the one platform mechanism that looks like it needs a library and does not.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import os
import time

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
         "The app calling out. This example never uses it — that is the point."),
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
