"""Webhook Inspector — see what a third party is actually sending you.

Point any webhook at this app's /hook path and every request is captured with
its method, headers, query string and body. The page shows the most recent
first; the same data is available as JSON.

Nothing is persisted. The buffer lives in this process and is lost on restart,
which is the right trade for a debugging tool: captured webhooks routinely
carry signatures, customer identifiers and one-time tokens, and a debugging aid
that quietly accumulates them on disk is a liability nobody remembers creating.

The Toft contract this app honors: uvicorn is started with --root-path set
to /apps/{slug} and the proxy forwards the un-prefixed path, so routes are
declared at "/" and every link in the HTML is built from request.scope's
root_path.
"""

import html
import json
import os
from collections import deque
from datetime import datetime, timezone

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import HTMLResponse, JSONResponse, PlainTextResponse

MAX_REQUESTS = max(1, min(int(os.getenv("MAX_REQUESTS", "50") or 50), 500))
MAX_BODY_BYTES = 16 * 1024
WEBHOOK_TOKEN = (os.getenv("WEBHOOK_TOKEN") or "").strip()

# Headers that are the credential, not the payload. They are recorded as
# present-and-redacted rather than dropped: "did they send an Authorization
# header at all" is usually the actual question, and answering it does not
# require keeping the value.
SENSITIVE_HEADERS = {"authorization", "proxy-authorization", "cookie", "x-api-key", "x-auth-token"}

app = FastAPI(title="Webhook Inspector", docs_url=None, redoc_url=None)

captured = deque(maxlen=MAX_REQUESTS)
_counter = {"n": 0}


def _base(request: Request) -> str:
    """The prefix this app is mounted under, for building absolute links."""
    return request.scope.get("root_path", "") or ""


def _check_token(request: Request) -> None:
    """The optional shared secret. Absent means the hook is open to anything
    that can already reach this app — which, behind Toft, is everyone the
    app is shared with, not the internet."""
    if not WEBHOOK_TOKEN:
        return
    supplied = request.headers.get("x-webhook-token") or request.query_params.get("token") or ""
    if supplied != WEBHOOK_TOKEN:
        raise HTTPException(status_code=401, detail="bad or missing webhook token")


async def _capture(request: Request, path: str) -> dict:
    raw = await request.body()
    truncated = len(raw) > MAX_BODY_BYTES
    body = raw[:MAX_BODY_BYTES]

    try:
        text = body.decode("utf-8")
    except UnicodeDecodeError:
        text = "<{} bytes of binary>".format(len(body))

    pretty = None
    if text.strip().startswith(("{", "[")):
        try:
            pretty = json.dumps(json.loads(text), indent=2, sort_keys=True)
        except ValueError:
            pretty = None

    headers = {}
    for key, value in request.headers.items():
        headers[key] = "<redacted>" if key.lower() in SENSITIVE_HEADERS else value

    _counter["n"] += 1
    return {
        "id": _counter["n"],
        "received_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "method": request.method,
        "path": "/" + path if path else "/",
        "query": str(request.url.query or ""),
        "client": request.client.host if request.client else "",
        "headers": headers,
        "body": pretty or text,
        "body_bytes": len(raw),
        "truncated": truncated,
    }


HOOK_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE"]


@app.api_route("/hook", methods=HOOK_METHODS)
@app.api_route("/hook/{path:path}", methods=HOOK_METHODS)
async def hook(request: Request, path: str = ""):
    _check_token(request)
    record = await _capture(request, path)
    captured.appendleft(record)
    return JSONResponse({"received": True, "id": record["id"]}, status_code=202)


@app.get("/api/requests")
async def list_requests():
    return {"count": len(captured), "capacity": MAX_REQUESTS, "requests": list(captured)}


@app.delete("/api/requests")
async def clear_requests():
    captured.clear()
    return {"cleared": True}


@app.get("/healthz", response_class=PlainTextResponse)
async def healthz():
    return "ok"


@app.get("/", response_class=HTMLResponse)
async def index(request: Request):
    return HTMLResponse(render(_base(request), list(captured)))


# --- rendering ---------------------------------------------------------------
# One page, inline CSS, no external assets. A debugging tool that pulls a
# stylesheet off the internet is one that renders wrong on the closed network
# where it is most needed.

STYLE = """
:root { color-scheme: light; }
body { margin:0; padding:2.5rem 1.5rem; background:#f7f8fa; color:#1c2024;
       font:15px/1.55 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width:56rem; margin:0 auto; }
h1 { font-size:1.4rem; margin:0 0 .25rem; letter-spacing:-0.01em; }
p.sub { margin:0 0 1.5rem; color:#6b7280; font-size:.875rem; }
code, pre { font-family:ui-monospace, SFMono-Regular, Menlo, monospace; }
code { background:#f2f4f7; padding:.1rem .35rem; border-radius:4px; font-size:.82rem; }
.card { background:#fff; border:1px solid #e5e7eb; border-radius:10px; margin-bottom:.9rem; overflow:hidden; }
.card header { display:flex; gap:.6rem; align-items:center; padding:.65rem .9rem; background:#fbfbfc;
               border-bottom:1px solid #f0f1f3; font-size:.85rem; }
.method { font-weight:700; font-size:.72rem; letter-spacing:.04em; padding:.15rem .45rem; border-radius:5px;
          background:#eef2ff; color:#3538cd; }
.path { font-family:ui-monospace, Menlo, monospace; font-size:.82rem; }
.when { margin-left:auto; color:#98a2b3; font-size:.78rem; }
pre { margin:0; padding:.85rem .9rem; font-size:.8rem; overflow-x:auto; background:#fff; }
details { border-top:1px solid #f0f1f3; }
summary { padding:.5rem .9rem; cursor:pointer; font-size:.8rem; color:#475467; }
.empty { background:#fff; border:1px dashed #d0d5dd; border-radius:10px; padding:2.5rem 2rem; text-align:center; color:#6b7280; }
footer { margin-top:1.25rem; color:#98a2b3; font-size:.78rem; }
"""


def render(base: str, records: list) -> str:
    hook_url = html.escape(base + "/hook")
    if records:
        body = "\n".join(_card(r) for r in records)
    else:
        body = (
            '<div class="empty"><p><strong>Nothing captured yet.</strong></p>'
            "<p>Send anything to <code>{hook}</code> and it appears here:</p>"
            "<p><code>curl -X POST {hook} -d '{{\"hello\":\"world\"}}'</code></p></div>"
        ).format(hook=hook_url)

    return """<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Webhook Inspector</title><style>{style}</style></head>
<body><main>
  <h1>Webhook Inspector</h1>
  <p class="sub">Point a webhook at <code>{hook}</code>. The last {cap} requests are kept in memory —
     newest first, nothing written to disk, and everything lost on restart.{token}</p>
  {body}
  <footer>JSON at <code>{base}/api/requests</code> · clear with
     <code>curl -X DELETE {base}/api/requests</code></footer>
</main></body></html>""".format(
        style=STYLE,
        hook=hook_url,
        cap=MAX_REQUESTS,
        token=" A shared secret is required." if WEBHOOK_TOKEN else "",
        body=body,
        base=html.escape(base),
    )


def _card(r: dict) -> str:
    headers = "\n".join(
        "{}: {}".format(html.escape(k), html.escape(str(v))) for k, v in sorted(r["headers"].items())
    )
    query = "?" + r["query"] if r["query"] else ""
    note = "  (truncated at {} bytes)".format(MAX_BODY_BYTES) if r["truncated"] else ""
    return """<article class="card">
  <header>
    <span class="method">{method}</span>
    <span class="path">{path}{query}</span>
    <span class="when">{when} · {size} bytes{note}</span>
  </header>
  <pre>{body}</pre>
  <details><summary>{count} headers</summary><pre>{headers}</pre></details>
</article>""".format(
        method=html.escape(r["method"]),
        path=html.escape(r["path"]),
        query=html.escape(query),
        when=html.escape(r["received_at"]),
        size=r["body_bytes"],
        note=note,
        body=html.escape(r["body"]) or "<em>empty body</em>",
        count=len(r["headers"]),
        headers=headers,
    )


# Local development: `python main.py` serves the same app uvicorn would.
if __name__ == "__main__":  # pragma: no cover
    import uvicorn

    uvicorn.run(
        app,
        host=os.getenv("HOST", "127.0.0.1"),
        port=int(os.getenv("PORT", "8000")),
        root_path=os.getenv("BASE_PATH", ""),
    )
