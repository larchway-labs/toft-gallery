# Node API

A task list with a REST API, the page that uses it, and **the caller's role on
the three routes that change something**. Express, one dependency, no build
step, everything in memory.

This is the **generic Node** path on Toft: any `package.json` with a
`start` script. Toft runs `npm run start` and tells the app where it is
mounted with `BASE_PATH`; the proxy strips that prefix before forwarding, so
routes are declared at the root.

## Three things that separate "works locally" from "works on the platform"

1. **Bind `HOST`, not `0.0.0.0`.** In shared mode an app on `0.0.0.0` is
   reachable without passing through the platform's authorization at all.
2. **Listen on `PORT`.** A hardcoded port produces a green build and an app
   nobody can reach.
3. **Build client URLs relative to the document.** `public/index.html` uses
   `new URL(path, base)` where `base` is `new URL(".", location.href)`. A
   `fetch("/api/tasks")` would leave the `/apps/{slug}` prefix and hit the
   platform instead — the single most common bug in an app that was written
   against `localhost:3000`.

## API

| Route | What |
|---|---|
| `GET /api/tasks?done=&owner=` | List, with optional filters. |
| `POST /api/tasks` | `{ "title": "…", "owner": "…" }` |
| `PATCH /api/tasks/{id}` | `{ "done": true }` or `{ "title": "…" }` |
| `DELETE /api/tasks/{id}` | Remove one. |
| `GET /healthz` | Uptime and the task count. |

## Configuration

None. `PORT`, `HOST` and `BASE_PATH` come from the platform.

## Local development

```bash
npm install && npm start        # http://127.0.0.1:3000
```

## The role, and why it is the interesting part

Every request Toft forwards carries `X-Toft-Role` — a word the
platform signs per app and per workload start, good for 120 seconds. The SDK
verifies it in this process with no round trip and no administrator switch, and
one middleware puts it in front of `POST`, `PATCH` and `DELETE`.

```js
function needsEditor(req, res, next) {
  const role = seam.role(req.headers);
  if (seam.atLeast(role, "editor") || role === "system") return next();
  return res.status(403).json({ error: `this caller is '${role}', and changing the list needs editor or owner`, ... });
}
```

Six words — `anonymous`, `viewer`, `editor`, `owner`, `system`, `app` — and no
seventh. Every verification failure is `unknown`, which is never privileged: the
one deployment where the header is missing is the one nobody configured.

**A rung is not a human.** An owner or editor of this app may mint an `lp_` key
that carries `viewer` or `editor`, and such a key satisfies the gate exactly as a
person with the same word does. That is the point of the word — the caller may do
what it says — and it is why nothing here pretends to tell you a human is in
front of it.

The page keeps the controls visible when the role refuses, and shows the API's
own sentence. An action you cannot see is an action you cannot ask about.

## The tabs

| Tab | What |
|---|---|
| **Board** | Four numbers, a filter, a search and the list. Every control is one of the six routes. |
| **Activity** | What changed and which role changed it. |
| **API** | The six routes, what each needs, and how a machine reaches them. |
| **Platform** | `GET /api/v1/app/self`, the role ladder, which platform headers arrived, and the environment. |

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
one stylesheet, one script, no CDN — served out of `public/` with the rest of the
static files. `platform-seam.js` is the only file that talks to the platform;
everything else is ordinary Express that would run anywhere.

MIT.
