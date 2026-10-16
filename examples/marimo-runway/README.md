# Runway

Twenty-four months of a product plan, and the nine assumptions everybody who
looks at it wants to change. Move one and the whole thing re-runs.

A [marimo](https://marimo.io) notebook, which on Toft is a **server
framework** rather than a document — and that distinction is the reason this
example exists.

## Why a plan, and not a report

The gallery has two documents in it: a Jupyter notebook and an R Markdown
report. Both are executed **once, at build time**, and what you get is the
output that fell out. Nothing in them moves.

marimo is the other thing. Toft runs `marimo run` and executes this file
**per viewer session**, so a slider is possible at all — and marimo re-runs the
cells that depend on what changed, in dependency order, with no hidden state and
no "run all above". That property is worth an example of its own, and a
twenty-four month model is the honest case for it: nine assumptions, everybody
who reads it wants to change one, and the entire value of the thing is what
happens when they do.

## What is in it

| | |
|---|---|
| **Assumptions** | Nine sliders. Acquisition and its growth rate, churn, price, margin, fixed cost, headcount plan, cost of a head, and the bank balance. |
| **Four numbers** | Break-even month, runway, peak burn, and where the business is at month 24. |
| **Two charts** | Cash against zero, and gross profit against cost. Inline SVG this file writes — there is no chart library anywhere in the Toft gallery. |
| **The plan** | All twenty-four rows, recomputed on every move, because a chart is a rounding of the model and somebody always wants the number. |
| **Platform** | What Toft told this app, and what makes it a marimo app rather than a Python one. |

Nothing outside marimo and the standard library.

## How Toft knows this is marimo

A `.py` file at the repository root that **imports marimo and constructs a
`marimo.App`**. Both halves are required, and it is a line scan rather than a
parse.

`marimo` in `requirements.txt` is deliberately *not* the trigger: it is a
library people import into ordinary programs, and a Flask app that depends on it
stays a Flask app.

There may be exactly one such file at the root. Two is a deploy that fails
naming both, because guessing which one you meant would silently deploy the
other.

## The trade it makes

One process serves a session, so a heavy model is a queue for whoever is sharing
it — the same trade Shiny makes, for the same reason. Keep the work in one cell,
keep it in the standard library, and it stays instant.

## Running it

On Toft: paste this repository's URL. Toft starts it with
`marimo run --base-url /apps/{slug}`, so it works under its mount point without
anything in this file knowing about it.

Locally:

```bash
python -m venv .venv && .venv/bin/pip install -r requirements.txt
.venv/bin/marimo run runway.py        # as a viewer sees it
.venv/bin/marimo edit runway.py       # with the code
```

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
one stylesheet, no CDN, no web fonts — inlined into the page by the second cell.
Every example in the gallery carries a byte-identical copy of it.

MIT.
