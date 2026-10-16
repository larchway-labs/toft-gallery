# Ops Console

An internal operations console for the team that runs the platform: who is on
call, the daily/weekly/monthly checklist, and four short runbooks.

Next.js (App Router), React 19, no dependencies beyond the framework and no
external assets.

## It reads no platform API

This is a content app. Checklists and runbooks live in `lib/data.js` — edit,
commit, tag, and the gallery's update path carries the change to every install
running it. Only what genuinely differs per install comes from the environment.

That is deliberate rather than incidental. On a single-origin Toft install
an app's JavaScript runs on the platform's own origin, so an app that called
`/api/v1` would be doing so as whoever is looking at it. A console that
displays configuration cannot make that mistake.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `TEAM_NAME` | no | `Platform` | Heading on the overview. |
| `ONCALL_PRIMARY` | no | `unassigned` | Who is holding the pager. |
| `ONCALL_SECONDARY` | no | `unassigned` | Who takes the weekly work. |
| `ESCALATION_CHANNEL` | no | `#platform-oncall` | Where to escalate. |

The overview is `force-dynamic`, so changing one of these and restarting the
app is enough — no rebuild.

## Routes

| Path | What |
|---|---|
| `/` | On-call, and the checklist by cadence. |
| `/runbooks` | The runbook index. |
| `/runbooks/{slug}` | One runbook, prerendered. |

## What a Next.js app on Toft has to know

- **Do not set `basePath`.** Toft injects it at build time; an
  app-supplied value is overridden either way. This repo ships no
  `next.config.js` at all.
- **`next/link` and `next/image` apply the prefix themselves.** A hand-built
  URL does not — use `NEXT_PUBLIC_BASE_PATH`, which is exported during the
  build, if you add one.
- **A `build` script is required**, and install scripts do not run, so anything
  that depends on `postinstall` belongs in `build`.
- **Never set `NODE_ENV=production` as an app variable.** npm reads it as
  `--omit=dev` and the build then fails on a missing devDependency.

## Local development

```bash
npm install && npm run dev
```
