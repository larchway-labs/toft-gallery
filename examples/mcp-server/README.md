# MCP Server

A **Model Context Protocol** server, hosted as an ordinary Toft app. It
exposes a folder of Markdown runbooks to an assistant as three tools —
`list_notes`, `search_notes`, `read_note` — and the notes shipped here are
samples you are meant to delete.

The point of the example is not the notes. It is that a remote MCP server needs
nothing special from this platform: MCP's `streamable-http` transport *is* an
HTTP server, and an HTTP server is what Toft hosts.

## What the platform supplies, so the app does not

| | Who does it |
|---|---|
| Authentication | **The gateway.** No credential, no request — the app never sees one. |
| Authorization | **The gateway**, against the app's visibility and the caller. |
| TLS and the public name | The install's front door. |
| Streaming | The proxy forwards without buffering, which is what SSE responses need. |
| Staying awake | An open stream counts as activity, so nothing is reaped mid-call. |

There is no `auth` block in `app.py` and no token in the environment. That is
the design, not an omission: the platform already answers "who may call this",
and a second answer inside the app is the one that goes stale when somebody is
locked out.

## Connecting a client

Deploy the app, then create a key on its **Settings → API keys** tab. The
endpoint is the app's URL with `/mcp` on the end — open the app in a browser and
the page tells you the exact URL, worked out from the request rather than from
anything you have to keep in step with the slug.

```bash
claude mcp add --transport http runbooks \
  https://toft.example.com/apps/<slug>/mcp \
  --header "Authorization: Bearer lp_your_key_here"
```

Any client that can send a static header works the same way. A client that
insists on **OAuth** does not: Toft authenticates with keys, and MCP's
OAuth flow wants an authorization server this platform does not run.

Two kinds of key behave differently, and the difference is worth knowing before
you hand one out:

- **App key** (`toft_…`) — possession *is* the authorization. It reaches this app
  and nothing else, and it outlives whoever created it. This is the one for a
  shared assistant.
- **Personal key** (`toftu_…`) — *is* a person. It authenticates its owner, who is
  then authorized normally, and it dies with their account.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `NOTES_DIR` | no | `notes` | The folder read by the tools. |
| `SERVER_NAME` | no | `Runbooks` | What the server calls itself to a client. |
| `ALLOWED_HOSTS` | no | *(empty)* | Comma-separated `Host` allowlist. See below. |
| `ALLOWED_ORIGINS` | no | *(empty)* | Comma-separated `Origin` allowlist. See below. |

`PORT`, `HOST` and `BASE_PATH` come from the platform. This is Toft's
*generic Python* path — a `requirements.txt` naming no framework the platform
recognises, started as `python app.py` — so the app binds what it is told to
bind. An MCP server wants to own its server anyway.

### A word about the Python version

The MCP SDK needs Python 3.10 or newer, which is younger than the system Python
on a good many servers. The `.python-version` file in this repo is what handles
that: `uv` reads it when the platform creates the virtual environment and fetches
the interpreter if the box does not have it. Delete the file only if you know the
build machine's Python is new enough — the failure otherwise arrives as a
dependency-resolution error in the build log, which is a puzzling way to be told
your Python is old.

### About those two allowlists

The SDK ships DNS-rebinding protection: a `Host` and `Origin` check meant for a
server a browser can reach directly on loopback. Behind this platform it is off
unless you set one of the variables, because the proxy rewrites `Host` to the
address it dialled — an address the app cannot predict in isolated mode, where
the workload is a pod rather than a port on the same machine. Enforcing a list
it cannot compute would produce a `421` with no correct value to write.

Nothing is given up by default: the gateway runs its own `Origin` allowlist and
its own authorization in front of every request. Set the variables if you want
the app's own belt as well as the platform's braces.

## Making it yours

`app.py` reads Markdown from a folder. Replace the three tool bodies with
queries against whatever your team actually has — a ticket system, a warehouse,
an internal API — and the transport, the auth story and the hosting are
unchanged. Keep the shape of what is here:

- **Flat parameters and plain return types.** A small, strictly typed tool
  surface is what a modest local model can call correctly, and an air-gapped
  install is often running one.
- **Say when an answer was cut short.** `search_notes` returns `truncated`
  rather than letting a caller infer completeness from a count that happens to
  equal the limit.
- **Resolve every path and check it is inside the folder.** `..` is the obvious
  case; a symlink pointing out is the one that gets forgotten.

## Local development

```bash
uv venv venv && uv pip install -r requirements.txt --python venv/bin/python
PORT=8000 venv/bin/python app.py
```

Then open `http://127.0.0.1:8000/` for the endpoint page, or point a client
straight at `http://127.0.0.1:8000/mcp`. No key is needed locally — the
credential belongs to the gateway, and there is no gateway in front of a process
you started yourself.

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
`static/`, served by a `custom_route` with a **name allowlist** rather than a
path join. This app has no framework to hand a static directory to, and building
a filesystem path out of a URL segment is a directory traversal waiting to
happen. Two names is the whole set.

The landing page is the one human-facing thing here, and it is worth the room:
an MCP endpoint answers POST and 404s a browser, so opening this app from the
Toft app list would otherwise show a person nothing at all. The endpoint on
it is built from `X-Forwarded-Proto`, `X-Forwarded-Host` and
`X-Forwarded-Prefix` — so rename the app and the page is still right.
