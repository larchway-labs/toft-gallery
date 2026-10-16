# Toft Example Kit

The house style for the example apps in the [Toft](https://github.com/gopanair)
gallery. Three files, copied byte-identically into every example:

| File | What it is |
|---|---|
| `toft-kit.css` | the whole design system — palette, type, and about thirty classes |
| `toft-kit.js` | tabs, sortable tables, filter boxes, three chart shapes, toasts, one fetch helper |
| `favicon.svg` | the mark |
| `vend.sh` | copies all three into every example, and rebuilds the one stylesheet that is the kit plus a layer |

No imports, no CDN, no web fonts, no build step and no dependencies. An
internal Toft install may have no route off the network, and a page whose
type silently falls back is worse than one that chose its fallback on purpose.

Copies rather than a package, deliberately: an example has to be a repository a
person can read top to bottom and deploy in one click. A shared dependency
would make sixteen apps un-runnable the day this one moved.

The cost of that choice is `vend.sh` and the check under it:

```bash
./vend.sh                                    # copy into all sixteen
sha256sum toft-kit.css                  # then compare every copy
```

## Why every example looks the same

Sixteen apps, six languages, five runtimes. The thing they are demonstrating is
**Toft**, not each author's taste — so the reader should be able to move
from the Go one to the R one to the notebook and recognise the page, and spend
their attention on the twenty lines that differ.

Each example is still allowed one idea of its own. The rule is that it is
*additive*: an app-specific stylesheet may add classes, and may not redefine the
kit's tokens.

## The page shell

Every example renders this skeleton, in this order.

```html
<a class="lp-skip" href="#main">Skip to content</a>

<header class="masthead">
  <div class="masthead-in">
    <div>
      <div class="wordmark">
        <span class="mark" aria-hidden="true"></span>
        <span class="wordmark-text">Toft example</span>
      </div>
      <h1>Stockroom</h1>
      <p class="standfirst">One sentence saying what this app is for.</p>
    </div>
    <div class="masthead-aside">
      <span class="chip chip-lang">Go · net/http</span>
      <span class="chip">Shared mode</span>
    </div>
  </div>
</header>

<!-- What this example demonstrates, and whether it is actually on here. -->
<div class="rail">
  <div class="rail-in">
    <span class="rail-label">Toft</span>
    <span class="cap on"><b>Slack</b> attached</span>
    <span class="cap off"><b>Email</b> not attached</span>
    <span class="cap on"><b>Scheduled tasks</b></span>
  </div>
</div>

<nav class="tabs" data-tabs>
  <div class="tabs-in">
    <button class="tab" data-tab="overview" type="button">Overview</button>
    <button class="tab" data-tab="platform" type="button">Platform</button>
  </div>
</nav>

<main class="shell" id="main">
  <section data-panel="overview">…</section>
  <section data-panel="platform" hidden>…</section>
</main>

<footer class="foot">
  <div class="foot-in">
    <span>An example app from the <strong>Toft</strong> gallery.</span>
    <span>Go · net/http · html/template</span>
  </div>
</footer>
```

A server-rendered app that gives each tab its own URL uses `<a class="tab"
aria-current="page">` and drops `data-tabs` entirely; the styling is the same.

## The capability rail

The one device every example shares, and the reason the gallery is worth
reading: a strip that names the Toft capabilities the app is using and
says, **live**, whether each is really there on the install it is running on.

```
.cap.on     the platform confirmed it — green
.cap.warn   configured but degraded — amber
.cap.off    not attached, and the app says what it does instead — grey
```

It is rendered server-side from `GET /api/v1/app/self`, because the app token is
the app's and never the browser's. A static example has no server, so its rail
is written by hand and says so.

## The classes

```
masthead wordmark mark standfirst chip            the bar
rail rail-in rail-label cap                        the rail
tabs tabs-in tab tab-count                         navigation
shell shell-narrow section section-hd              layout
tiles tile tile-k tile-v tile-n                    the stat row
card card-hd card-bd card-ft cards card-flush      the container
tbl tbl-wrap th-sort                               tables
badge tone-ok|att|dan|info|brand  dot              state
btn btn-primary btn-danger btn-sm btn-ghost seg    actions
field input select textarea controls check         forms
note tone-* empty kv                               prose
bar bar-track bar-fill  cols  spark  chart  donut  heat   charts
term  toasts toast                                 logs and feedback
foot foot-in                                       the footer
mono num muted faint small tiny row stack grow     utilities
```

Five tones and no sixth: `ok`, `att` (attention — *look at this*, never
*broken*), `dan`, `info`, `neutral`. Five chart series and no sixth.

## The JavaScript, if you want it

```html
<script src="toft-kit.js"></script>
```

Wired by attributes, so a Go template or a Python f-string can use all of it
without writing any script:

```html
<table class="tbl" data-sortable>
  <thead><tr><th data-sort="text">Name</th><th data-sort="num" class="num">Count</th></tr></thead>
</table>

<input class="input" data-filter="#rows" data-filter-count="#n" placeholder="Search">

<div data-spark="4,9,7,12,11,18,22"></div>
<div data-cols="3,7,4,9" data-col-labels="Mon,Tue,Wed,Thu"></div>
<div class="donut" data-donut="0.62"></div>
<span data-ago="2026-08-27T10:00:00Z"></span>
```

And a handful of calls: `LP.toast(text, tone)`, `LP.json(url, {method, body})`
— which turns a refusal into an `Error` carrying Toft's own sentence,
because the platform's messages always name the remedy — `LP.submit(form)`,
`LP.num`, `LP.bytes`, `LP.pct`, `LP.ago`, `LP.sparkSVG`, `LP.lineChartSVG`,
`LP.enhance(root)` after you swap a fragment in.

## License

MIT. Copy it into your own app; that is what it is for.
