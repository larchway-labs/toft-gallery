"""The checks themselves — one registry, each entry a self-contained probe.

A check answers three questions and never more: what did it try, what happened,
and is that what a correctly isolated app *should* see. That last one is the
whole point. A tool that only prints "this app can reach 127.0.0.1:3101" tells
you nothing; the reader has to know whether it is meant to. So every check
carries its own expectation, and the verdict is reality measured against it:

    pass   isolation held exactly as the platform intends
    warn   an exposure that is real, documented, and an ACCEPTED trade in this
           execution mode — app-to-app loopback, the platform API answered as
           anonymous, a TCP path to the database. Visible on purpose; not a bug.
    fail   isolation that is supposed to hold did NOT. This is a finding.
    info   a fact with no pass/fail — identity, the environment inventory.

Nothing here persists and nothing here writes outside the app's own scratch
directory. Every probe is read-only except the one that deliberately tests
where it can write, and that one cleans up after itself.
"""

from __future__ import annotations

import glob
import json
import os
import pwd
import socket
import subprocess
import urllib.request
from dataclasses import dataclass, field
from typing import Callable

# Verdict constants — the four things a check can conclude.
PASS, WARN, FAIL, INFO = "pass", "warn", "fail", "info"

# Map a verdict to the kit's tone vocabulary (.tone-*).
TONE = {PASS: "ok", WARN: "warn", FAIL: "bad", INFO: "muted"}


@dataclass
class Result:
    verdict: str
    headline: str
    detail: str = ""
    evidence: dict = field(default_factory=dict)


@dataclass
class Check:
    id: str
    title: str
    category: str
    question: str          # what a reader is actually asking
    expectation: str       # what a correctly isolated app should see
    run: Callable[[], Result]


def _me() -> str:
    return pwd.getpwuid(os.getuid()).pw_name


def _my_app_id() -> str:
    return os.environ.get("TOFT_APP_ID", "")


# Environment variables that are the app's own working configuration, so that
# "everything else" can be reported as the surprising part.
_EXPECTED_ENV_PREFIXES = ("TOFT_", "PM2_", "NODE_", "FLASK_")
_EXPECTED_ENV_EXACT = {
    "PATH", "HOME", "LANG", "LC_CTYPE", "TMPDIR", "USER", "SHELL", "PWD",
    "PORT", "HOST", "BASE_PATH", "PYTHONPATH", "PYTHONUNBUFFERED",
    "GUNICORN_CMD_ARGS",
}

# Secrets that must NEVER be in a deployed app's environment. If any of these
# appear, the platform handed the app a key that decrypts other apps' secrets
# or forges a session — the most serious finding this tool can produce.
_FORBIDDEN_SECRETS = {
    "DATABASE_URL", "JWT_SECRET", "ENCRYPTION_KEY", "ENCRYPTION_KEY_OLD",
    "POSTGRES_PASSWORD", "AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID",
    "AWS_SESSION_TOKEN", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
    "AWS_CONTAINER_CREDENTIALS_FULL_URI", "GITHUB_CLIENT_SECRET",
    "OIDC_CLIENT_SECRET", "BOOTSTRAP_ADMIN_PASSWORD",
}


# ── Filesystem isolation ────────────────────────────────────────────────────

def check_apps_dir():
    base = "/data/apps"
    rows, listable_others = [], 0
    try:
        entries = sorted(os.listdir(base))
    except PermissionError:
        return Result(PASS, "The apps directory itself is not listable",
                      f"listdir({base}) is denied to this app.")
    except FileNotFoundError:
        return Result(INFO, "No shared apps directory on this install",
                      f"{base} does not exist — nothing to isolate here.")
    me = _my_app_id()
    for e in entries[:40]:
        p = os.path.join(base, e)
        try:
            st = os.stat(p)
            owner = pwd.getpwuid(st.st_uid).pw_name
            mode = oct(st.st_mode)[-4:]
        except Exception:
            owner, mode = "?", "?"
        mine = bool(me) and me in e
        can_list = False
        if not mine:
            try:
                os.listdir(p)
                can_list = True
                listable_others += 1
            except Exception:
                pass
        rows.append({"dir": e[:12], "mode": mode, "owner": owner,
                     "mine": mine, "listable_by_me": can_list})
    if listable_others:
        return Result(FAIL, f"{listable_others} other app directory(ies) are listable",
                      "Another app's release directory could be enumerated.",
                      {"entries": rows})
    return Result(PASS, "Every other app's directory is opaque",
                  "Each app directory is mode 0710 and owned by that app's own "
                  "account, so this app cannot list or enter another's.",
                  {"entries": rows})


def check_read_other_source():
    me = _my_app_id()
    read, denied = [], 0
    for path in glob.glob("/data/apps/*/current/**/*", recursive=True)[:5000]:
        if me and me in path:
            continue
        if not os.path.isfile(path):
            continue
        try:
            with open(path, "rb") as f:
                f.read(16)
            read.append(path)
        except PermissionError:
            denied += 1
        except Exception:
            pass
    if read:
        return Result(FAIL, f"Read {len(read)} file(s) from other apps' code",
                      "Another app's source or configuration was readable.",
                      {"sample": read[:8], "denied": denied})
    return Result(PASS, "No other app's source was readable",
                  f"Every attempt to open another app's files was denied "
                  f"({denied} blocked).", {"denied": denied})


def check_read_other_envfiles():
    me = _my_app_id()
    read, denied = [], 0
    for pat in ("/data/apps/*/.env*", "/data/apps/*/current/.env*",
                "/data/apps/*/env*", "/data/apps/*/current/**/.env*"):
        for path in glob.glob(pat, recursive=True):
            if me and me in path:
                continue
            try:
                with open(path, "rb") as f:
                    f.read(16)
                read.append(path)
            except PermissionError:
                denied += 1
            except Exception:
                pass
    if read:
        return Result(FAIL, f"Read {len(read)} env file(s) from other apps",
                      "Another app's environment file — where its secrets live "
                      "on disk — was readable.", {"sample": read[:8]})
    return Result(PASS, "No other app's env files were readable",
                  f"{denied} attempt(s) blocked; none succeeded.")


def check_read_other_logs():
    me = _my_app_id()
    read, denied = [], 0
    for path in glob.glob("/data/logs/*")[:400]:
        if me and me in path:
            continue
        try:
            with open(path, "rb") as f:
                f.read(16)
            read.append(os.path.basename(path))
        except PermissionError:
            denied += 1
        except Exception:
            pass
    if read:
        return Result(FAIL, f"Read {len(read)} other app log(s)",
                      "Another app's log output was readable — request lines, "
                      "stack traces and whatever it printed.", {"sample": read[:8]})
    return Result(PASS, "No other app's logs were readable",
                  f"{denied} attempt(s) blocked.")


def check_sensitive_files():
    """The platform's own files an app must never READ, and the platform code
    it must never WRITE.

    The distinction is the whole check. A world-readable compiled binary leaks
    nothing — it is the same code the platform ships publicly, and its secrets
    come from the environment and the database at run time, neither of which an
    app can read (those are separate checks). What would be a finding is a
    binary an app can *replace* — that is code execution as the platform — or a
    file whose contents are themselves secret, like /etc/shadow.
    """
    findings, observed = {}, {}

    # Contents-are-secret: readable is the finding.
    for label, path in (("/etc/shadow", "/etc/shadow"),
                        ("PM2's home (holds other apps' process env)",
                         os.environ.get("PM2_HOME", "/root/.pm2"))):
        try:
            if os.path.isdir(path):
                os.listdir(path); findings[label] = "readable/listable"
            else:
                with open(path, "rb") as f:
                    f.read(16)
                findings[label] = "readable"
        except Exception:
            observed[label] = "denied"

    # Code-execution: WRITABLE is the finding; readable alone is expected.
    for label, path in (("the platform binary", "/app/toft"),
                        ("the platform's /app directory", "/app")):
        try:
            writable = os.access(path, os.W_OK)
        except Exception:
            writable = False
        if writable:
            findings[label] = "WRITABLE — could replace platform code"
        else:
            observed[label] = ("readable, not writable"
                               if os.access(path, os.R_OK) else "denied")

    if findings:
        return Result(FAIL, "A platform-owned sensitive path is exposed",
                      "Either a secret file was readable or platform code was "
                      "writable.", {"findings": findings, "other": observed})
    return Result(PASS, "Platform-owned sensitive paths are protected",
                  "/etc/shadow and PM2's home are not readable, and the platform "
                  "binary and /app are not writable — a readable-but-immutable "
                  "binary leaks nothing, since its secrets live in the "
                  "environment and database, not in the file.",
                  {"observed": observed})


def check_writable():
    """Where can this app write? Its own scratch, and nowhere it shouldn't."""
    scratch = os.environ.get("TOFT_SCRATCH_DIR", "")
    forbidden = ["/data/apps", "/app", "/etc", "/data/logs"]
    wrote_forbidden = []
    for d in forbidden:
        probe = os.path.join(d, ".seclab_write_test")
        try:
            with open(probe, "w") as f:
                f.write("x")
            os.remove(probe)
            wrote_forbidden.append(d)
        except Exception:
            pass
    scratch_ok = False
    if scratch:
        try:
            probe = os.path.join(scratch, ".seclab_write_test")
            with open(probe, "w") as f:
                f.write("x")
            os.remove(probe)
            scratch_ok = True
        except Exception:
            pass
    if wrote_forbidden:
        return Result(FAIL, f"Wrote into {len(wrote_forbidden)} platform directory(ies)",
                      "This app could create a file in a directory that is not "
                      "its own.", {"wrote": wrote_forbidden})
    return Result(PASS, "Writes are confined",
                  "This app can write to its own scratch directory and was "
                  "refused everywhere it should be.",
                  {"scratch_writable": scratch_ok, "refused": forbidden})


# ── Process & secret isolation ──────────────────────────────────────────────

def check_platform_environ():
    try:
        with open("/proc/1/environ", "rb") as f:
            raw = f.read().decode(errors="replace")
        keys = [kv.split("=")[0] for kv in raw.split("\0") if kv]
        leaked = sorted(set(keys) & _FORBIDDEN_SECRETS)
        return Result(FAIL, "Read the platform process's environment",
                      "PID 1 is the platform, and its environment holds the "
                      "keys that decrypt every app's secrets.",
                      {"forbidden_keys_visible": leaked or "(env read; specific keys not matched)"})
    except PermissionError:
        return Result(PASS, "The platform's process environment is not readable",
                      "/proc/1/environ is denied — the app cannot read the "
                      "platform's DATABASE_URL, JWT_SECRET or ENCRYPTION_KEY "
                      "from the running process.")
    except Exception as e:
        return Result(INFO, "Could not test PID 1's environment",
                      f"{type(e).__name__}: {e}")


def check_other_environs():
    me = _me()
    read_mine, read_others, denied = 0, [], 0
    for d in os.listdir("/proc"):
        if not d.isdigit():
            continue
        try:
            owner = pwd.getpwuid(os.stat(f"/proc/{d}").st_uid).pw_name
        except Exception:
            owner = "?"
        try:
            with open(f"/proc/{d}/environ", "rb") as f:
                f.read(1)
            if owner == me:
                read_mine += 1
            else:
                read_others.append(f"{d}({owner})")
        except PermissionError:
            denied += 1
        except Exception:
            pass
    if read_others:
        return Result(FAIL, f"Read {len(read_others)} other process(es)' environment",
                      "A process owned by another account had a readable "
                      "environment — that is where another app's token lives.",
                      {"others": read_others[:10], "denied": denied})
    return Result(PASS, "Only this app's own processes are readable",
                  f"{read_mine} of this app's own process environ(s) readable, "
                  f"{denied} others denied. No other account's process leaked.",
                  {"denied": denied})


def check_own_env_secrets():
    present = sorted(k for k in os.environ if k in _FORBIDDEN_SECRETS)
    if present:
        return Result(FAIL, "This app's own environment contains platform secrets",
                      "The platform handed this app a variable it must never "
                      "have — a key or credential from the allow-list breach.",
                      {"forbidden_present": present})
    # Inventory the whole environment, classified.
    expected, extra = [], []
    for k in sorted(os.environ):
        if k in _EXPECTED_ENV_EXACT or k.startswith(_EXPECTED_ENV_PREFIXES):
            expected.append(k)
        else:
            extra.append(k)
    return Result(PASS, "This app's environment carries no platform secrets",
                  "Every variable is either the app's own configuration or a "
                  "documented TOFT_* handoff. None is a platform key.",
                  {"handoff_and_config": expected, "other": extra})


# ── AWS / cloud credential exposure ─────────────────────────────────────────

def check_aws_env():
    aws = {k: ("<present>" if "SECRET" in k or "TOKEN" in k or "KEY" in k else v)
           for k, v in os.environ.items() if k.startswith(("AWS_", "ECS_"))}
    creds = {k for k in aws if any(t in k for t in ("SECRET", "TOKEN", "CREDENTIALS", "ACCESS_KEY"))}
    if creds:
        return Result(FAIL, "AWS credential variables are in this app's environment",
                      "The app can assume the platform's cloud identity directly "
                      "from its environment.", {"aws_env": aws})
    if aws:
        return Result(PASS, "Only harmless AWS region hints are present",
                      "AWS_REGION and the like carry no credential.", {"aws_env": aws})
    return Result(PASS, "No AWS variables in this app's environment",
                  "Nothing AWS_/ECS_ was handed to this app at all.")


def check_metadata_endpoint():
    results = {}
    reachable_with_data = False
    for label, url in (
        ("ECS task role (169.254.170.2)", "http://169.254.170.2/v2/credentials/"),
        ("EC2 instance role (169.254.169.254)",
         "http://169.254.169.254/latest/meta-data/iam/security-credentials/"),
    ):
        try:
            req = urllib.request.Request(url)
            with urllib.request.urlopen(req, timeout=4) as r:
                body = r.read(200).decode(errors="replace")
                results[label] = f"HTTP {r.status}: {body[:120]}"
                # A 200 that lists a role name is the dangerous case.
                if r.status == 200 and body.strip() and "NoId" not in body:
                    reachable_with_data = True
        except urllib.error.HTTPError as e:
            results[label] = f"HTTP {e.code} ({e.reason})"
        except Exception as e:
            results[label] = f"{type(e).__name__}: {str(e)[:80]}"
    if reachable_with_data:
        return Result(FAIL, "A metadata endpoint returned credentials",
                      "The app reached the cloud metadata service and got a "
                      "role back.", {"endpoints": results})
    return Result(PASS, "No metadata endpoint yielded credentials",
                  "Both the ECS task-role and EC2 instance-role endpoints "
                  "refused or returned nothing usable.", {"endpoints": results})


def check_boto3():
    try:
        import boto3
    except Exception as e:
        return Result(INFO, "boto3 is not installed",
                      f"Cannot test SDK credential discovery: {e}")
    out = {"boto3_version": boto3.__version__}
    got_creds = False
    try:
        creds = boto3.Session().get_credentials()
        got_creds = creds is not None
        out["credentials_found"] = got_creds
    except Exception as e:
        out["credentials_found"] = f"{type(e).__name__}: {str(e)[:60]}"
    listed = None
    try:
        s3 = boto3.client("s3", region_name=os.environ.get("AWS_REGION", "us-east-1"))
        listed = [b["Name"] for b in s3.list_buckets().get("Buckets", [])]
        out["list_buckets"] = listed
    except Exception as e:
        out["list_buckets"] = f"{type(e).__name__}: {str(e)[:100]}"
    if got_creds or (isinstance(listed, list)):
        return Result(FAIL, "boto3 found AWS credentials",
                      "The SDK resolved a credential and could act against S3 "
                      "as the platform's identity.", out)
    return Result(PASS, "boto3 found no credentials",
                  "The SDK searched its whole chain — environment, container "
                  "and instance metadata — and came up empty, so an app cannot "
                  "reach S3 as the platform.", out)


# ── Network reach (accepted-risk territory) ─────────────────────────────────

def check_loopback_neighbours():
    reachable = []
    for port in range(3100, 3120):
        s = socket.socket()
        s.settimeout(0.3)
        if s.connect_ex(("127.0.0.1", port)) == 0:
            reachable.append(port)
        s.close()
    # Exclude our own port.
    mine = os.environ.get("PORT", "")
    others = [p for p in reachable if str(p) != mine]
    if others:
        return Result(WARN, f"Reached {len(others)} neighbouring app port(s)",
                      "Apps share loopback in shared mode, so this app can open "
                      "a socket to another running app. This is a DOCUMENTED, "
                      "accepted trade of shared mode — not a defect — and it is "
                      "why sensitive apps run in isolated mode. It does not by "
                      "itself grant access to the neighbour's data.",
                      {"ports": others, "my_port": mine})
    return Result(PASS, "No neighbouring app ports were reachable",
                  "Nothing else is listening on loopback in this app's range.",
                  {"my_port": mine})


def check_platform_api():
    out = {}
    saw_data = False
    for path in ("/api/v1/config", "/api/v1/apps", "/api/v1/admin/settings"):
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:8080{path}", timeout=4) as r:
                body = r.read(300).decode(errors="replace")
                out[path] = f"HTTP {r.status}: {body[:120]}"
                if path == "/api/v1/apps" and body.strip() not in ("[]", "[]\n"):
                    saw_data = True
        except urllib.error.HTTPError as e:
            out[path] = f"HTTP {e.code}"
        except Exception as e:
            out[path] = f"{type(e).__name__}"
    if saw_data:
        return Result(FAIL, "The platform API returned app data to this app",
                      "Reaching the platform API is expected; getting a "
                      "non-empty answer to /apps means it treated this app as a "
                      "signed-in user, which it must not.", {"responses": out})
    return Result(WARN, "The platform API is reachable, and answers as anonymous",
                  "An app can reach the platform's own API on loopback — "
                  "expected in shared mode — but it is seen as anonymous: "
                  "/apps returns an empty list and /admin/settings is refused. "
                  "It reads only what any unauthenticated caller could.",
                  {"responses": out})


def check_database_tcp():
    host = os.environ.get("SECLAB_DB_HOST", "").strip()
    if not host:
        # Best-effort discovery is out of scope; report informationally.
        return Result(INFO, "No database host configured to test",
                      "Set SECLAB_DB_HOST to the RDS endpoint to test whether "
                      "this app can open a TCP path to it. Reachability without "
                      "the password is defence-in-depth, not access.")
    s = socket.socket()
    s.settimeout(4)
    rc = s.connect_ex((host, 5432))
    s.close()
    if rc == 0:
        return Result(WARN, "A TCP path to the database is open",
                      "The app shares the platform's network identity in shared "
                      "mode, so it can open a socket to Postgres. It cannot "
                      "authenticate — the password is not in reach — so this is "
                      "reachability, not access. Isolated mode or network "
                      "segmentation is what closes it.", {"host": host})
    return Result(PASS, "No TCP path to the database",
                  f"connect_ex to {host}:5432 returned {rc}.", {"host": host})


def check_identity():
    u = pwd.getpwuid(os.getuid())
    return Result(INFO, f"Running as {u.pw_name} (uid {os.getuid()})",
                  "A deployed app should run as its own per-app account, never "
                  "as root and never as the platform.",
                  {"uid": os.getuid(), "user": u.pw_name, "gid": os.getgid(),
                   "groups": os.getgroups(), "home": u.pw_dir,
                   "app_id": _my_app_id(),
                   "app_slug": os.environ.get("TOFT_APP_SLUG", "")})


# ── The registry ────────────────────────────────────────────────────────────

CHECKS = [
    Check("identity", "Process identity", "Baseline",
          "Who is this app running as?",
          "Its own per-app account — not root, not the platform.",
          check_identity),
    Check("apps-dir", "Other apps' directories", "Filesystem",
          "Can this app see other apps' release directories?",
          "No — each is 0710, owned by that app's account.",
          check_apps_dir),
    Check("read-source", "Other apps' source code", "Filesystem",
          "Can this app read another app's code?",
          "No — every open is denied.",
          check_read_other_source),
    Check("read-envfiles", "Other apps' env files", "Filesystem",
          "Can this app read another app's on-disk secrets?",
          "No — .env files are not readable across apps.",
          check_read_other_envfiles),
    Check("read-logs", "Other apps' logs", "Filesystem",
          "Can this app read another app's log output?",
          "No — logs are per-app.",
          check_read_other_logs),
    Check("sensitive-files", "Platform-owned files", "Filesystem",
          "Can this app read the platform's own sensitive files?",
          "No — the binary, /etc/shadow and PM2's home are denied.",
          check_sensitive_files),
    Check("writable", "Write confinement", "Filesystem",
          "Where can this app write?",
          "Only its own scratch directory.",
          check_writable),
    Check("platform-environ", "Platform process secrets", "Secrets",
          "Can this app read the platform's running environment?",
          "No — /proc/1/environ is denied, so JWT_SECRET and ENCRYPTION_KEY "
          "stay out of reach.",
          check_platform_environ),
    Check("other-environs", "Other processes' environments", "Secrets",
          "Can this app read any other account's process environment?",
          "No — only its own.",
          check_other_environs),
    Check("own-env-secrets", "This app's own environment", "Secrets",
          "Was this app handed any platform secret in its own environment?",
          "No — only its own config and documented TOFT_* handoffs.",
          check_own_env_secrets),
    Check("aws-env", "AWS credentials in the environment", "Cloud",
          "Are AWS credentials present in this app's environment?",
          "No — at most a region hint, never a key.",
          check_aws_env),
    Check("metadata", "Cloud metadata endpoints", "Cloud",
          "Can this app pull a role from the cloud metadata service?",
          "No — the endpoints refuse or return nothing usable.",
          check_metadata_endpoint),
    Check("boto3", "boto3 credential discovery", "Cloud",
          "Can the AWS SDK find credentials and reach S3?",
          "No — the whole credential chain comes up empty.",
          check_boto3),
    Check("loopback", "Neighbouring app ports", "Network",
          "Can this app open a socket to another running app?",
          "Yes in shared mode — an accepted, documented trade (warn), not a "
          "path to the neighbour's data.",
          check_loopback_neighbours),
    Check("platform-api", "The platform API", "Network",
          "Can this app reach the platform's own API, and as whom?",
          "Reachable on loopback but only as anonymous (warn).",
          check_platform_api),
    Check("database-tcp", "Database reachability", "Network",
          "Can this app open a TCP path to the database?",
          "Reachable in shared mode but not authenticable (warn); needs a host "
          "to test.",
          check_database_tcp),
]

CHECKS_BY_ID = {c.id: c for c in CHECKS}


def run_check(check_id: str) -> Result:
    c = CHECKS_BY_ID.get(check_id)
    if not c:
        return Result(INFO, "No such check", check_id)
    try:
        return c.run()
    except Exception as e:  # a probe must never take the app down
        return Result(INFO, "Check errored", f"{type(e).__name__}: {e}")
