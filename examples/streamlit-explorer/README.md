# CSV Explorer

A Streamlit example for Toft that is useful the day it is deployed: drop
in a CSV export from anywhere and get filtering, summary metrics, a chart and a
cleaned download — without opening a notebook.

It ships with a small generated dataset (two years of weekly figures for four
regions, seeded so it never changes) so there is something on screen before
anybody uploads anything.

## Two things worth copying

**Uploaded data stays in memory.** It is held in `st.session_state` and never
written to disk. On a shared Toft install every session runs in one
process under one UID, so a file written into the app's directory is a file
every other viewer of that app can read. Per-session scratch isolation is a
convention, not a boundary.

**Nothing here binds a port or builds a URL.** Toft starts Streamlit with
`--server.baseUrlPath /apps/{slug}`; Streamlit is the one framework that keeps
the prefix instead of having it stripped, and it handles that itself.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `APP_TITLE` | no | `CSV Explorer` | Page title and heading. |

## Local development

```bash
pip install -r requirements.txt
streamlit run streamlit_app.py
```

## A note on WebSockets

Streamlit is entirely WebSocket-driven. Toft turns off Streamlit's own
origin check (`--server.enableCORS false`) because it cannot be satisfied
behind a proxy that rewrites `Host` — the platform applies its own origin
allowlist to every app request first, and XSRF protection stays on. Nothing for
this app to do, but it is the reason a Streamlit app that works locally can
render blank behind a naive proxy.

## The house style, inside Streamlit

Streamlit draws its own shell, so an example here cannot simply carry the
gallery's HTML skeleton. `chrome.py` does the two things it can:

- **`.streamlit/config.toml`** hands Streamlit the kit's palette, which is what
  colours the widgets.
- **`toft-kit.css`** is injected, along with the masthead and capability
  rail every other example renders.

The palette is duplicated between the two because there is no way to share it,
so `chrome.palette_agrees()` checks they have not drifted and the page says so
if they have. A design system that quietly disagrees with itself is worse than
one that never tried.

Two things learned the hard way and written down in `chrome.py`:

- **Every line of injected HTML is un-indented first.** Streamlit renders it as
  Markdown, and Markdown reads a four-space indent as a code block — which is
  how a masthead ends up on the page with a stray `</div>` in a grey box.
- **`<header>` and `<footer>` do not survive** Streamlit's sanitiser, and their
  orphaned closing tags land on the page. Plain `<div>`s with the same classes do.

MIT.
