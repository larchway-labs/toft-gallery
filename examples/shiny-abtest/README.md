# A/B Test

A Shiny example for Toft that is useful the day it is deployed: type in the
visitors and conversions from two variants and get the difference, a confidence
interval, a p-value and a plot — plus a second tab that answers the question
people actually arrive with, which is *how many do I need before this is worth
looking at*.

No data files, no database, nothing stored. Every number on the page comes from
the four inputs, so the app is the same on every install.

## Three things worth copying

**Nothing binds a port and nothing builds a URL.** Toft starts a Shiny app
by evaluating `shiny::runApp(appDir, host, port)` — R has no `--port` flag — and
serves it under `/apps/{slug}/`. Shiny emits relative asset URLs and derives its
WebSocket URL from the page's own path, so the prefix takes care of itself. This
is why `app.R` never mentions either.

**One R process serves everybody.** Shiny is single-threaded, so a reactive that
takes four seconds is a four-second queue for every viewer at once, not just the
one who moved the slider. That is the reason the statistics here are `prop.test`
and `power.prop.test` rather than a simulation: everything on the page is
microseconds. If you need something slow, start a Toft job and let the page
poll for the result — do not put it in a reactive.

**Configuration is read once, at start.** `APP_TITLE` and `SIGNIFICANCE_LEVEL`
are read into ordinary variables at the top of `app.R`, not inside a reactive.
An environment variable is configuration; changing it is a restart, and pretending
otherwise gives you a value that is re-read on every keystroke for no reason.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `APP_TITLE` | no | `A/B Test` | Page title and heading. |
| `SIGNIFICANCE_LEVEL` | no | `0.05` | The threshold, as a proportion. Anything outside 0 to 0.5 falls back to 0.05. |

## Dependencies

`renv.lock` is committed, which is what makes this deployable. Without one
Toft installs nothing, says so in the build log, and the app fails at its
first `library(shiny)` call.

Only `shiny` is declared. Base R already has `prop.test`, `binom.test` and
`power.prop.test`, and the plots are base graphics — a smaller lockfile restores
faster on every single deploy, and there is nothing here a tidyverse dependency
would have made clearer.

To add a package: install it locally, then `renv::snapshot()`, then commit the
lockfile.

## Local development

```bash
Rscript -e 'renv::restore()'
Rscript -e 'shiny::runApp(".", port = 8000)'
```

Then open <http://127.0.0.1:8000/>.

## Reading the output

The **Compare** tab runs a two-proportion test with continuity correction. The
verdict line is deliberately not the whole answer — the interval underneath it is
what tells you whether "not conclusive" means *there is no effect* or *this test
was too small to see one*. When the sample is small enough that the chi-squared
approximation is shaky, R says so and the app shows you what it said rather than
hiding the warning.

The **Plan** tab is `power.prop.test` over a range of relative lifts. The counts
are per arm; double them for the test as a whole.

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
`www/toft-kit.css`, byte-identical in every example in the gallery, linked
rather than pasted because `www/` is Shiny's static directory and Shiny serves it
under the app's own prefix without this file knowing what that prefix is.

`app_layer` in `app.R` is the rest: the six places Bootstrap and the kit
disagree, plus the verdict box. One of them is worth naming — **`.row` is a flex
utility in the kit and a grid row in Bootstrap**, and `sidebarLayout` is built
from one, so left alone the sidebar and the panel stop being side by side.
Bootstrap wins inside its own container.

The two plots use `--lp-c1` and the kit's neutral, so a base-R barplot matches
every other chart in the gallery.
