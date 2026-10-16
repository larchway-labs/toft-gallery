# Next.js Dashboard

A metrics dashboard: four stat tiles, a range switcher, a line chart and a
service table. Next.js App Router, React 19, **no chart library and no other
dependency**.

The chart is about eighty lines of SVG in `components/chart.js`. That is the
point of the example — a dependency that pulls 300 kB of JavaScript into the
bundle would be the largest thing in this app, and a `<path>` with a `viewBox`
is enough for a dashboard.

## What it demonstrates

- **Server components by default, one client island.** Everything renders on
  the server except the chart, which needs a hover handler.
- **State in the URL, not in React.** The range and the selected metric are
  query parameters, so a view is a link somebody can paste into a chat. The
  tiles and range buttons are `next/link`, which applies the platform's
  `basePath` for you.
- **Deterministic data.** `lib/metrics.js` generates ninety days from a seed,
  so two people see the same numbers. Swap that file for a database query and
  nothing else changes.

## What a Next.js app on Toft has to know

- **Do not set `basePath`** — Toft injects it at build time. This repo
  ships no `next.config.js` at all.
- **`next/link` applies the prefix; a hand-built URL does not.**
  `NEXT_PUBLIC_BASE_PATH` is exported during the build for that case.
- **A `build` script is required.**
- **Install scripts do not run** unless the operator allows them, so anything
  that depends on `postinstall` belongs in `build`.
- **Never set `NODE_ENV=production` as an app variable** — npm reads it as
  `--omit=dev` and the build fails on a missing devDependency.

## Configuration

None. It is an example; everything it draws is in the repository.

## Local development

```bash
npm install && npm run dev
```

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
`app/toft-kit.css`, byte-identical in every example in the gallery, imported
first in `app/layout.js`. `app/globals.css` is this app's own layer: a tile that
is also a link, and the chart's hover readout. It never redefines a kit token.

The masthead, the capability rail and the footer are in the layout, and the rail
reads the environment on the **server** — a client component would have to be
handed it, and handing a browser the whole environment is how a secret ends up
in a bundle.

`public/toft-kit.js` is loaded with a plain `<script>` tag whose `src`
carries `BASE_PATH`. Next.js is the one framework here the proxy forwards the
full path to, and `basePath` covers every asset Next knows about — a hand-written
script tag is not one of those.
