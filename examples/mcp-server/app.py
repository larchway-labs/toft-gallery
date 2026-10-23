"""MCP Server — an assistant-facing tool server, hosted on Toft.

The Model Context Protocol has two transports. `stdio` is a subprocess on
somebody's laptop; `streamable-http` is an HTTP server, and an HTTP server is
exactly what this platform hosts. So a remote MCP server needs nothing special
here — it is an ordinary app, and this file is the whole of it.

Three things are worth reading the code for.

**It contains no authentication, deliberately.** The gateway in front of it
already refuses anything without a credential: a personal key (`toftu_`) of an
account granted on this app admits the request and is stripped before it arrives, so this process never sees a secret
and never decides who may call it. An MCP server that reimplemented that would
be a second answer to a question the platform has already answered, and the
second answer is the one that goes stale.

**Streamable HTTP is the only transport offered.** The older `sse` transport
tells the client, in-band, which URL to post replies to — a path from this
process's point of view, which is not the path the client reached us on, because
the proxy mounts the app under /apps/<slug> and strips the prefix before
forwarding. Streamable HTTP has no such handshake and needs no rewriting.

**The DNS-rebinding middleware is off unless configured.** See ALLOWED_HOSTS
below; the reasoning is worth more than the switch.

Configuration:

    PORT             the port to listen on. Supplied by the platform.
    HOST             the address to bind. Supplied by the platform: 127.0.0.1
                     in shared mode, where a port on 0.0.0.0 would bypass
                     authorization entirely.
    BASE_PATH        the prefix the app is mounted under. The proxy strips it
                     before forwarding, so routes are matched at "/" — it is
                     used here only to print a URL that leaves this process.
    NOTES_DIR        the folder of Markdown the tools read. Default ./notes.
    SERVER_NAME      what the server calls itself to a client.
    ALLOWED_HOSTS    optional, comma-separated; see below.
    ALLOWED_ORIGINS  optional, comma-separated; see below.
"""

import html
import os
from datetime import datetime, timezone
from pathlib import Path

from mcp.server import MCPServer
from mcp.server.transport_security import TransportSecuritySettings
from starlette.requests import Request
from starlette.responses import HTMLResponse, JSONResponse, Response

BASE_PATH = os.getenv("BASE_PATH", "")
NOTES_DIR = Path(os.getenv("NOTES_DIR", "notes")).resolve()
SERVER_NAME = os.getenv("SERVER_NAME", "Runbooks")

# What a tool may return in one call. A tool that hands back a megabyte does not
# fail — it quietly spends somebody's context window on a file they did not ask
# to read, which is worse, because nothing about the answer looks wrong.
MAX_NOTE_BYTES = 64_000
MAX_MATCHES = 20
SNIPPET_CHARS = 240

mcp = MCPServer(
    name=SERVER_NAME,
    version="1.0.0",
    instructions=(
        f"{SERVER_NAME} holds this team's operational notes as Markdown files. "
        "Use search_notes to find the relevant one and read_note to read it. "
        "Quote the note's path when you answer, so the reader can check you."
    ),
)


# ---------------------------------------------------------------------------
# Reading the notes
#
# Every path a client supplies is resolved and then checked to be inside
# NOTES_DIR. `..` is the obvious case; a symlink pointing out of the folder is
# the one that is usually forgotten, and resolve() is what catches both.
# ---------------------------------------------------------------------------


def _resolve(path: str) -> Path:
    candidate = (NOTES_DIR / path).resolve()
    if candidate != NOTES_DIR and NOTES_DIR not in candidate.parents:
        raise ValueError(f"{path!r} is outside the notes folder")
    if not candidate.is_file():
        raise ValueError(f"there is no note at {path!r}")
    return candidate


def _notes() -> list[Path]:
    if not NOTES_DIR.is_dir():
        return []
    return sorted(p for p in NOTES_DIR.rglob("*.md") if p.is_file())


def _title(text: str, fallback: str) -> str:
    for line in text.splitlines():
        if line.startswith("#"):
            return line.lstrip("#").strip() or fallback
    return fallback


def _describe(path: Path) -> dict:
    text = path.read_text(encoding="utf-8", errors="replace")
    stat = path.stat()
    return {
        "path": str(path.relative_to(NOTES_DIR)),
        "title": _title(text, path.stem),
        "bytes": stat.st_size,
        "modified": datetime.fromtimestamp(stat.st_mtime, timezone.utc).isoformat(),
    }


# ---------------------------------------------------------------------------
# The tools
#
# Three of them, with flat parameters and plain return types. A small, strictly
# typed tool surface is not a simplification for the example's sake: it is what
# a local model can call correctly, and an install that brings its own endpoint
# is often running one.
# ---------------------------------------------------------------------------


@mcp.tool(
    title="List notes",
    description="List every note, with its title, size and last-modified time.",
)
def list_notes() -> list[dict]:
    return [_describe(p) for p in _notes()]


@mcp.tool(
    title="Search notes",
    description=(
        "Find notes containing a phrase. Returns the matching lines with the "
        "path and line number of each, in file order."
    ),
)
def search_notes(query: str, limit: int = 10) -> dict:
    """Case-insensitive substring search across every note."""
    if not query.strip():
        raise ValueError("query is empty")
    limit = max(1, min(limit, MAX_MATCHES))

    needle = query.lower()
    matches: list[dict] = []
    truncated = False

    for path in _notes():
        text = path.read_text(encoding="utf-8", errors="replace")
        for number, line in enumerate(text.splitlines(), start=1):
            if needle not in line.lower():
                continue
            if len(matches) >= limit:
                truncated = True
                break
            snippet = line.strip()
            if len(snippet) > SNIPPET_CHARS:
                snippet = snippet[:SNIPPET_CHARS] + "…"
            matches.append(
                {
                    "path": str(path.relative_to(NOTES_DIR)),
                    "line": number,
                    "text": snippet,
                }
            )
        if truncated:
            break

    return {
        "query": query,
        "matches": matches,
        # Said out loud rather than inferred from len(matches) == limit. A
        # caller that cannot tell a complete answer from a clipped one will
        # eventually report the clipped one as complete.
        "truncated": truncated,
    }


@mcp.tool(
    title="Read a note",
    description="Read one note in full, by the path search_notes reported.",
)
def read_note(path: str) -> str:
    note = _resolve(path)
    text = note.read_text(encoding="utf-8", errors="replace")
    if len(text.encode("utf-8")) > MAX_NOTE_BYTES:
        text = text[:MAX_NOTE_BYTES]
        text += f"\n\n[truncated at {MAX_NOTE_BYTES} bytes — read the file directly for the rest]"
    return text


@mcp.resource(
    "notes://{path}",
    title="Note",
    description="A note, addressable as a resource for clients that prefer one.",
    mime_type="text/markdown",
)
def note_resource(path: str) -> str:
    return _resolve(path).read_text(encoding="utf-8", errors="replace")


# ---------------------------------------------------------------------------
# The two human-facing routes
#
# An MCP endpoint answers POST and 404s a browser, so opening the app from the
# Toft app list would otherwise show a person nothing at all. This page is
# what they get instead: the exact URL to paste into a client, worked out from
# the headers the proxy set rather than from a value somebody has to keep in
# step with the app's slug.
# ---------------------------------------------------------------------------


def _public_url(request: Request) -> str:
    proto = request.headers.get("x-forwarded-proto", request.url.scheme)
    host = request.headers.get("x-forwarded-host", request.headers.get("host", ""))
    prefix = request.headers.get("x-forwarded-prefix", BASE_PATH)
    return f"{proto}://{host}{prefix}/mcp"


@mcp.custom_route("/static/{name}", ["GET"])
async def static_file(request: Request) -> Response:
    """The three files of the house style, and nothing else.

    A name allowlist rather than a path join: this app has no framework to hand
    a static directory to, and building a filesystem path out of a URL segment
    is a directory traversal waiting to happen. Three names is the whole set.
    """
    served = {
        "toft-kit.css": "text/css; charset=utf-8",
        "favicon.svg": "image/svg+xml",
    }
    name = request.path_params["name"]
    kind = served.get(name)
    if kind is None:
        return Response(status_code=404)
    body = (Path(__file__).parent / "static" / name).read_bytes()
    return Response(body, media_type=kind,
                    headers={"Cache-Control": "public, max-age=300"})


@mcp.custom_route("/", ["GET"])
async def index(request: Request) -> HTMLResponse:
    url = _public_url(request)
    prefix = request.headers.get("x-forwarded-prefix", BASE_PATH)
    tools = await mcp.list_tools()
    notes = _notes()
    handle = SERVER_NAME.lower().replace(" ", "-")

    def _params(tool) -> str:
        """The parameter names, from whichever spelling this SDK version uses.

        `inputSchema` became `input_schema` between SDK majors, and a landing
        page that 500s because of a rename is worse than one that says nothing.
        """
        schema = getattr(tool, "input_schema", None) or getattr(tool, "inputSchema", None)
        names = list((schema or {}).get("properties", {}))
        return ", ".join(names) if names else "\u2014"

    def cap(label, on, note=""):
        return (f'<span class="cap {"on" if on else "off"}" title="{html.escape(note)}">'
                f"<b>{html.escape(label)}</b></span>")

    rail = "".join([
        cap("Generic Python", True,
            "No framework the platform recognises, so this app owns its own server, "
            "bind address and port \u2014 which is what an MCP server wants anyway."),
        cap("Streamable HTTP", True,
            "One endpoint, POST only. A browser gets a 404 from it, which is why this "
            "page exists."),
        cap("Stateless", True,
            "Every request stands alone. A session-bound server would pin a client to a "
            "process that a deploy, a crash restart or an idle sleep is allowed to "
            "replace underneath it \u2014 and here all three are ordinary."),
        cap("Personal keys", True,
            "The platform authenticates the connection. The client sends the toftu_ key of "
            "an account granted on this app, and this process never sees a credential of "
            "its own."),
        cap("Base path", bool(prefix),
            prefix or "Not mounted under a prefix here."),
        cap("Toft workload", bool(os.getenv("TOFT_APP_SLUG")),
            "Running as a Toft workload." if os.getenv("TOFT_APP_SLUG")
            else "This is a local run."),
    ])

    tool_rows = "".join(
        f"<tr><td class='lead mono small nowrap'>{html.escape(t.name)}</td>"
        f"<td class='muted small'>{html.escape((t.description or '').strip().splitlines()[0] if t.description else '')}</td>"
        f"<td class='mono small muted'>"
        f"{html.escape(_params(t))}"
        f"</td></tr>"
        for t in tools)

    note_rows = "".join(
        f"<tr><td class='lead'>{html.escape(_describe(n)['title'])}</td>"
        f"<td class='mono small'>{html.escape(n.name)}</td>"
        f"<td class='num small muted'>{_describe(n)['bytes']:,} bytes</td></tr>"
        for n in notes)

    return HTMLResponse(
        f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(SERVER_NAME)} &mdash; MCP server</title>
<meta name="description" content="A Model Context Protocol server, hosted like any other app.">
<link rel="icon" href="{html.escape(prefix)}/static/favicon.svg" type="image/svg+xml">
<link rel="stylesheet" href="{html.escape(prefix)}/static/toft-kit.css">
</head>
<body>
<a class="lp-skip" href="#main">Skip to content</a>

<header class="masthead"><div class="masthead-in">
<div>
<div class="wordmark"><span class="mark" aria-hidden="true"></span>
<span class="wordmark-text">Toft example</span></div>
<h1>{html.escape(SERVER_NAME)}</h1>
<p class="standfirst">A Model Context Protocol server, over streamable HTTP, hosted
like any other app. <strong>There is no web interface</strong> &mdash; this page exists
so the endpoint can be found, because an MCP endpoint answers POST and 404s a browser.</p>
</div>
<div class="masthead-aside">
<span class="chip chip-lang">Python &middot; MCP SDK</span>
<span class="chip">{len(tools)} tools &middot; {len(notes)} notes</span>
</div>
</div></header>

<div class="rail"><div class="rail-in">
<span class="rail-label">Toft</span>{rail}
</div></div>

<main class="shell" id="main">

<div class="card">
  <div class="card-hd"><h2>The endpoint</h2>
    <span class="muted small">Worked out from the headers the proxy set</span></div>
  <div class="card-bd">
    <pre class="term">{html.escape(url)}</pre>
    <div class="note tone-brand">
      <strong>Not a value anybody has to keep in step with the app's slug.</strong>
      It is built from <code>X-Forwarded-Proto</code>, <code>X-Forwarded-Host</code> and
      <code>X-Forwarded-Prefix</code> &mdash; the three headers the platform sets on
      every request it forwards. Rename the app and this page is still right.
    </div>
  </div>
</div>

<div class="section">
  <div class="card">
    <div class="card-hd"><h2>Connecting a client</h2></div>
    <div class="card-bd">
      <p class="small">The platform authenticates the connection. Give the client the
         personal key of an account granted on this app &mdash; for a shared assistant,
         an account of its own &mdash; as a bearer token:</p>
      <pre class="term">claude mcp add --transport http {html.escape(handle)} {html.escape(url)} \\
  --header "Authorization: Bearer toftu_your_key_here"</pre>
      <div class="note">
        This process never sees a credential of its own. The <code>toftu_</code> key is
        checked at the gateway before the request reaches here, which is the whole reason
        an MCP server is worth hosting rather than running on somebody's laptop: the
        authorization, the audit trail and the ability to revoke are the platform's.
      </div>
    </div>
  </div>
</div>

<div class="section">
  <div class="cards">
    <div class="card">
      <div class="card-hd"><h2>Tools</h2>
        <span class="muted small">What an assistant can call</span></div>
      <div class="card-bd" style="padding:0">
        <div class="tbl-wrap"><table class="tbl">
          <thead><tr><th>Name</th><th>What it does</th><th>Takes</th></tr></thead>
          <tbody>{tool_rows}</tbody>
        </table></div>
      </div>
      <div class="card-ft">
        Each one is a decorated Python function in <code>app.py</code>. The docstring is
        the description an assistant reads, which is why they are written for a reader
        rather than for a linter.
      </div>
    </div>

    <div class="card">
      <div class="card-hd"><h2>What it is serving</h2>
        <span class="muted small">Markdown in <code>notes/</code></span></div>
      <div class="card-bd" style="padding:0">
        <div class="tbl-wrap"><table class="tbl">
          <thead><tr><th>Title</th><th>File</th><th class="num">Size</th></tr></thead>
          <tbody>{note_rows}</tbody>
        </table></div>
      </div>
      <div class="card-ft">
        Four runbooks, committed. Point <code>NOTES_DIR</code> at a storage mount and the
        same three tools serve whatever is on it &mdash; which is the version worth
        deploying.
      </div>
    </div>
  </div>
</div>

<div class="section">
  <div class="cards">
    <div class="card">
      <div class="card-hd"><h2>Why generic Python</h2></div>
      <div class="card-bd">
        <p class="small">There is no flask, fastapi or streamlit in
           <code>requirements.txt</code>, and that absence is what puts this app on
           Toft's <strong>generic Python</strong> path: started as
           <code>python app.py</code>, with the app owning its own server, bind address
           and port.</p>
        <dl class="kv">
          <dt><code>PORT</code></dt><dd>listened on. A hardcoded port is a green build and an app nobody can reach</dd>
          <dt><code>HOST</code></dt><dd>bound. Loopback in shared mode &mdash; <code>0.0.0.0</code> steps around the platform's authorization entirely</dd>
          <dt><code>BASE_PATH</code></dt><dd>used for URLs that leave the process, and nothing else</dd>
        </dl>
      </div>
    </div>
    <div class="card">
      <div class="card-hd"><h2>Two decisions worth copying</h2></div>
      <div class="card-bd">
        <div class="note tone-brand">
          <strong><code>stateless_http=True</code>.</strong> Every request stands alone. A
          session-bound server would pin a client to a process that a deploy, a crash
          restart or an idle sleep is allowed to replace underneath it &mdash; and on this
          platform all three are ordinary.
        </div>
        <div class="note tone-att">
          <strong>DNS-rebinding protection is off unless an operator names the hosts.</strong>
          The SDK's middleware defends a server a browser can reach directly on loopback.
          This one is behind the gateway, which rewrites <code>Host</code> to the address it
          dialled &mdash; an address this process cannot predict in isolated mode, where the
          workload is a pod rather than a port on a machine. Enforcing an allowlist here
          would be a 421 with no way to write the correct list. The gateway's own origin
          allowlist and its authorization still run in front of every request.
        </div>
      </div>
    </div>
  </div>
</div>

</main>

<footer class="foot"><div class="foot-in">
<span>An example app from the <strong>Toft</strong> gallery.</span>
<span>Python &middot; MCP SDK &middot; streamable HTTP &middot; one dependency</span>
</div></footer>
</body>
</html>"""
    )


@mcp.custom_route("/healthz", ["GET"])
async def healthz(_: Request) -> JSONResponse:
    return JSONResponse({"status": "ok", "notes": len(_notes())})


def _csv_env(name: str) -> list[str]:
    return [v.strip() for v in os.getenv(name, "").split(",") if v.strip()]


if __name__ == "__main__":
    # The SDK's DNS-rebinding middleware defends a server that a browser can
    # reach directly on loopback. This one is behind the platform gateway, which
    # rewrites Host to the address it dialled — an address this process cannot
    # predict in isolated mode, where the workload is a pod rather than a port on
    # this machine. Enforcing a Host allowlist here would therefore be a 421 with
    # no way to write the correct list, so it is off unless an operator supplies
    # one; the gateway's own Origin allowlist and its authorization still run in
    # front of every request either way.
    hosts, origins = _csv_env("ALLOWED_HOSTS"), _csv_env("ALLOWED_ORIGINS")
    security = TransportSecuritySettings(
        enable_dns_rebinding_protection=bool(hosts or origins),
        allowed_hosts=hosts,
        allowed_origins=origins,
    )

    mcp.run(
        transport="streamable-http",
        host=os.getenv("HOST", "127.0.0.1"),
        port=int(os.getenv("PORT", "8000")),
        # Every request stands alone. A session-bound server would pin a client
        # to a process that a deploy, a crash-restart or an idle sleep is allowed
        # to replace underneath it — and on this platform all three are ordinary.
        stateless_http=True,
        transport_security=security,
    )
