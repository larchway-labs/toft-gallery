# Service Catalog

A directory of the services a team runs: who owns each one, what it calls, and
what calls it. One page, one dataset, no build step.

This is the **static hosting** example on Toft. The repository is HTML, CSS,
JavaScript and a JSON file, and that is the whole deployment — nothing is
installed, nothing is compiled, and the release you push is byte for byte the
site that gets served.

```
index.html            the page
404.html              what an unmatched path gets
assets/styles.css     one stylesheet, no imports, no web fonts
assets/catalog.js     search, filters, keyboard, and the wiring diagram
assets/favicon.svg
data/services.json    24 services, 7 teams — the only source of content
```

## What it does

- **Search** across name, summary, tags, team, language and on-call rotation.
  Every word has to match, so `go payments` narrows rather than widens.
- **Filter** by tier and team; **sort** by tier, name, how many services depend
  on a service, or how long it has been since anyone reviewed the entry.
- **A wiring diagram** for the selected service: what it calls on the left, what
  calls it on the right, both read off the same `depends_on` arrays. Nothing in
  the file records the reverse direction — the page derives it, so the two
  halves cannot disagree.
- **A link per service.** `#billing-api` opens the catalog with that service
  selected, which is what you paste into a ticket.
- **Keyboard.** `/` searches, up and down move between services, `Esc` clears
  the filters.

## Why it is worth reading

Static hosting has one rule that surprises people, and this repo is an example
of following it:

**Relative URLs work; root-absolute ones do not.** The app is served from
`/apps/{slug}/`, so `assets/styles.css` and `data/services.json` resolve under
that prefix. `/assets/styles.css` asks *Toft* for a file at the root of the
platform and gets a 404. Every URL here — the stylesheet, the script, the icon,
and the `fetch` in `catalog.js` — is relative for that reason. Toft warns
about root-absolute URLs in the build log and never rewrites your files.

Two smaller ones:

- `404.html` is served for any path this release does not contain. It carries
  its own styles inline, because an unmatched path can sit at any depth and a
  relative stylesheet link would not resolve from there.
- There is no `[static]` block in a `toft.toml` here, and no
  `toft.toml` at all. The document root is the repository, the entry
  document is `index.html`, and both are the defaults. A repo that keeps its
  site in `docs/` or commits a build directory needs one line:

  ```toml
  [static]
  root = "dist"
  ```

  A `package.json`, `requirements.txt`, `pyproject.toml`, `Pipfile` or `go.mod`
  in the tree disqualifies a repository from static detection — that is what
  keeps an unbuilt Vite app from deploying successfully and serving a blank
  page. A `[static]` table overrides that.

This app is client-side only and uses no client-side routing, so it does not
need `fallback = true`. An app that routes in the browser does.

## Editing the catalog

`data/services.json` is the content. A service is:

```json
{
  "id": "billing-api",
  "name": "billing-api",
  "team": "payments",
  "tier": 1,
  "summary": "One sentence. What breaks when this stops.",
  "language": "Go",
  "repo": "git.internal/payments/billing-api",
  "runbook": "wiki.internal/runbooks/billing-api",
  "endpoint": "https://billing.internal",
  "rotation": "payments-primary",
  "slo": { "availability": "99.95%", "latency_p95_ms": 150 },
  "last_review": "2026-08-08",
  "tags": ["ledger", "postgres", "money"],
  "depends_on": ["tax-rates", "event-bus", "auth-broker"]
}
```

`team` is a team `id` from the `teams` array, `depends_on` holds service `id`s,
and an id that does not exist is ignored rather than drawn. Bump `revision` and
`reviewed` when you change the file — the plate at the top of the page shows
both, which is how a reader knows whether to trust it.

Redeploy after an edit: the JSON ships with the release, so the page shows what
the release contains and nothing is fetched from anywhere else.

## Running it locally

Any static file server. The page fetches JSON, so `file://` will not do:

```bash
python3 -m http.server 8000     # then http://127.0.0.1:8000
```

## What it does not do

No backend, no database, no writes. Nobody's live status is in here — this is
the record of what exists and who owns it, not a monitor. The data is invented
for the example.

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
`assets/toft-kit.css` and `assets/toft-kit.js`, byte-identical in every
example in the gallery. `assets/styles.css` is this app's own layer: the service
list, the detail panel, the dependency diagram and the two blocks under it. It
never redefines a kit token.

`404.html` is the one page that copies the palette instead of linking the
stylesheet, because an unmatched path can sit at any depth and a relative link
would not resolve from there — and a 404 page that is itself broken is a poor way
to explain that something is missing.
