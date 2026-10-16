"""Runway — a reactive plan, on marimo.

A marimo notebook is a **server framework**, not a document. Toft runs
`marimo run` and executes this file once per viewer session, which is why it is
the only notebook-shaped thing in the gallery that has a slider in it: move one
and every cell that depends on it re-runs, in dependency order, with no hidden
state and no "run all above".

That property is the reason this example is a plan rather than a report. A
twenty-four month model has about nine assumptions in it, everybody who looks at
it wants to change one, and the value of the thing is entirely in what happens
when they do.

Nothing outside the standard library and marimo. The charts are inline SVG this
file writes; there is no chart library anywhere in the Toft gallery.
"""

import marimo

__generated_with = "0.24.0"
app = marimo.App(width="full", app_title="Runway")


@app.cell(hide_code=True)
def _():
    import marimo as mo
    import os
    from pathlib import Path

    HERE = Path(__file__).parent

    # The house style, inlined. Every example in the gallery carries a
    # byte-identical copy of this stylesheet: no CDN, no web fonts, and nothing
    # fetched at render time, because an internal install may have no route off
    # the network.
    KIT = (HERE / "static" / "toft-kit.css").read_text()
    return HERE, KIT, mo, os


@app.cell(hide_code=True)
def _(KIT, mo):
    mo.Html(f"""
    <style>
    {KIT}
    /* marimo draws its own shell around this notebook, so the kit's page-level
       rules are scoped to the blocks below rather than to <body>. Two overrides
       and no more: the container marimo gives a cell, and the fact that the
       masthead has to be able to reach the full width of it. */
    .lp-block {{ font: 400 15px/1.6 var(--lp-sans); color: var(--lp-ink); }}
    .lp-block *, .lp-block *::before, .lp-block *::after {{ box-sizing: border-box; }}
    .lp-block .masthead, .lp-block .rail {{ border-radius: var(--lp-r) var(--lp-r) 0 0; }}
    .lp-block .rail {{ border-radius: 0 0 var(--lp-r) var(--lp-r); }}
    .lp-full {{ margin: 0 -1rem; }}
    </style>
    """)
    return


@app.cell(hide_code=True)
def _(mo, os):
    # What the platform told this app. Every example in the gallery has this
    # panel; on marimo it is one cell because a notebook has no tabs.
    slug = os.environ.get("TOFT_APP_SLUG", "")
    base = os.environ.get("BASE_PATH", "")
    on_platform = bool(os.environ.get("TOFT_APP_TOKEN"))

    def cap(label, on, note=""):
        return (f'<span class="cap {"on" if on else "off"}" title="{note}">'
                f"<b>{label}</b></span>")

    rail = "".join([
        cap("marimo", True, "A server framework: executed per viewer session."),
        cap("Reactive", True, "Move a slider and every dependent cell re-runs."),
        cap("Per-session state", True, "One process, one session, no shared globals."),
        cap("Base path", bool(base), f"marimo run --base-url {base}" if base
            else "Not mounted under a base path here."),
        cap("Toft workload", on_platform,
            "Running as a Toft workload." if on_platform
            else "No TOFT_APP_TOKEN: this is a local run."),
    ])

    masthead = mo.Html(f"""
    <div class="lp-block lp-full">
      <header class="masthead">
        <div class="masthead-in">
          <div>
            <div class="wordmark">
              <span class="mark" aria-hidden="true"></span>
              <span class="wordmark-text">Toft example</span>
            </div>
            <h1>Runway</h1>
            <p class="standfirst">
              Twenty-four months of a product plan, and the nine assumptions everybody
              who looks at it wants to change. Move one and the whole thing re-runs.
            </p>
          </div>
          <div class="masthead-aside">
            <span class="chip chip-lang">Python · marimo</span>
            {'<span class="chip">' + slug + "</span>" if slug else ""}
          </div>
        </div>
      </header>
      <div class="rail"><div class="rail-in">
        <span class="rail-label">Toft</span>{rail}
      </div></div>
    </div>
    """)
    masthead
    return base, on_platform, slug


@app.cell(hide_code=True)
def _(mo):
    # The assumptions. Every one of these is an input to the model below, and
    # marimo works out that dependency from the code rather than being told —
    # which is why there is no "recalculate" button anywhere on this page.
    new_customers = mo.ui.slider(
        0, 300, value=120, step=5, label="New customers a month", show_value=True)
    growth = mo.ui.slider(
        -10, 25, value=6, step=1, label="…growing (% a month)", show_value=True)
    churn = mo.ui.slider(
        0.0, 8.0, value=2.0, step=0.1, label="Monthly churn (%)", show_value=True)
    arpu = mo.ui.slider(
        20, 600, value=210, step=5, label="Revenue per customer (£/month)", show_value=True)
    margin = mo.ui.slider(
        30, 95, value=78, step=1, label="Gross margin (%)", show_value=True)
    cash = mo.ui.slider(
        200, 6000, value=2400, step=50, label="Cash in the bank (£k)", show_value=True)
    fixed = mo.ui.slider(
        20, 600, value=160, step=5, label="Fixed costs (£k/month)", show_value=True)
    hires = mo.ui.slider(
        0, 8, value=2, step=1, label="Hires a month", show_value=True)
    cost_per_head = mo.ui.slider(
        4, 20, value=9, step=1, label="Cost of a hire (£k/month)", show_value=True)
    return arpu, cash, churn, cost_per_head, fixed, growth, hires, margin, new_customers


@app.cell(hide_code=True)
def _(arpu, cash, churn, cost_per_head, fixed, growth, hires, margin, mo, new_customers):
    controls = mo.Html('<div class="lp-block"><div class="card"><div class="card-hd">'
                       "<h2>Assumptions</h2>"
                       '<span class="muted small">Nine numbers. Everything below is derived '
                       "from them and nothing is stored.</span></div></div></div>")
    mo.vstack([
        controls,
        mo.hstack([
            mo.vstack([new_customers, growth, churn]),
            mo.vstack([arpu, margin, fixed]),
            mo.vstack([cash, hires, cost_per_head]),
        ], gap=2, widths="equal"),
    ])
    return


@app.cell
def _(arpu, cash, churn, cost_per_head, fixed, growth, hires, margin, new_customers):
    # The model. Twenty-four months, one loop, no library.
    #
    # This is the only cell in the notebook that computes anything, and it is
    # deliberately readable end to end: an assumption you cannot follow is an
    # assumption nobody will argue with, and a plan nobody argues with is not
    # doing its job.
    MONTHS = 24

    def project():
        customers = 1500.0
        headcount = 16.0
        bank = cash.value * 1000.0
        acquisitions = float(new_customers.value)
        rows = []
        for m in range(MONTHS):
            lost = customers * (churn.value / 100.0)
            customers = customers + acquisitions - lost
            headcount += hires.value
            acquisitions *= 1 + growth.value / 100.0

            mrr = customers * arpu.value
            gross = mrr * (margin.value / 100.0)
            costs = fixed.value * 1000.0 + headcount * cost_per_head.value * 1000.0
            net = gross - costs
            bank += net

            rows.append({
                "month": m + 1,
                "customers": customers,
                "lost": lost,
                "mrr": mrr,
                "gross": gross,
                "costs": costs,
                "net": net,
                "bank": bank,
                "headcount": headcount,
            })
        return rows

    plan = project()

    # Three questions a plan is actually asked, answered from the same rows the
    # table below prints. One derivation, so the tiles and the table cannot
    # disagree.
    breakeven = next((r["month"] for r in plan if r["net"] >= 0), None)
    out_of_cash = next((r["month"] for r in plan if r["bank"] <= 0), None)
    peak_burn = min((r["net"] for r in plan), default=0.0)
    end = plan[-1]
    return breakeven, end, MONTHS, out_of_cash, peak_burn, plan


@app.cell(hide_code=True)
def _(breakeven, end, mo, out_of_cash, peak_burn):
    def money(v, unit="£"):
        a = abs(v)
        if a >= 1_000_000:
            return f"{'-' if v < 0 else ''}{unit}{a / 1_000_000:,.2f}m"
        if a >= 1_000:
            return f"{'-' if v < 0 else ''}{unit}{a / 1_000:,.0f}k"
        return f"{'-' if v < 0 else ''}{unit}{a:,.0f}"

    def tile(k, v, note, tone=""):
        return (f'<div class="tile {tone}"><div class="tile-k">{k}</div>'
                f'<div class="tile-v">{v}</div><div class="tile-n">{note}</div></div>')

    tiles = "".join([
        tile("Break-even",
             f"month {breakeven}" if breakeven else "not in 24",
             "First month the plan is cash-positive",
             "tone-ok" if breakeven and breakeven <= 12 else
             "tone-att" if breakeven else "tone-dan"),
        tile("Runway",
             f"month {out_of_cash}" if out_of_cash else "24+ months",
             "When the bank account reaches zero",
             "tone-dan" if out_of_cash and out_of_cash <= 12 else
             "tone-att" if out_of_cash else "tone-ok"),
        tile("Peak burn", money(peak_burn), "Worst month, net", "tone-neu"),
        tile("Customers at 24", f"{end['customers']:,.0f}",
             f"{end['headcount']:,.0f} people · {money(end['mrr'])} MRR", "tone-info"),
    ])

    mo.Html(f'<div class="lp-block"><div class="tiles">{tiles}</div></div>')
    return (money,)


@app.cell(hide_code=True)
def _(MONTHS, mo, money, plan):
    # Two charts, drawn as SVG paths this cell writes. The kit's `.chart`
    # classes colour them, so they match every other example in the gallery
    # without a library being involved.
    def line_chart(series, labels, height=220, digits=0, fmt=None):
        w, h = 760, height
        m = {"t": 14, "r": 14, "b": 28, "l": 66}
        iw, ih = w - m["l"] - m["r"], h - m["t"] - m["b"]
        allv = [v for s in series for v in s["values"]]
        lo, hi = min(allv + [0.0]), max(allv + [1.0])
        span = (hi - lo) or 1.0
        fmt = fmt or (lambda v: f"{v:,.{digits}f}")

        def X(i):
            return m["l"] + (i / (len(labels) - 1)) * iw

        def Y(v):
            return m["t"] + ih - ((v - lo) / span) * ih

        parts = [f'<svg class="chart" viewBox="0 0 {w} {h}" role="img">']
        for g in range(5):
            gy = m["t"] + g / 4 * ih
            parts.append(f'<line class="grid" x1="{m["l"]}" y1="{gy:.1f}" '
                         f'x2="{w - m["r"]}" y2="{gy:.1f}"/>')
            parts.append(f'<text class="lbl" x="{m["l"] - 8}" y="{gy + 3.5:.1f}" '
                         f'text-anchor="end">{fmt(hi - g / 4 * span)}</text>')
        # Zero, when the axis crosses it, is the one line worth drawing darker.
        if lo < 0 < hi:
            parts.append(f'<line class="axis" x1="{m["l"]}" y1="{Y(0):.1f}" '
                         f'x2="{w - m["r"]}" y2="{Y(0):.1f}"/>')
        for si, s in enumerate(series):
            d = " ".join(("M" if i == 0 else "L") + f"{X(i):.1f} {Y(v):.1f}"
                         for i, v in enumerate(s["values"]))
            parts.append(f'<path class="s{si % 3 + 1}" d="{d}"/>')
        for i, lab in enumerate(labels):
            if lab:
                parts.append(f'<text class="lbl" x="{X(i):.1f}" y="{h - 8}" '
                             f'text-anchor="middle">{lab}</text>')
        parts.append("</svg>")
        return "".join(parts)

    labels = [str(r["month"]) if r["month"] % 3 == 0 else "" for r in plan]

    cash_chart = line_chart(
        [{"values": [r["bank"] for r in plan]}], labels, fmt=money)
    pl_chart = line_chart(
        [{"values": [r["gross"] for r in plan]},
         {"values": [r["costs"] for r in plan]}], labels, fmt=money)

    def legend(items):
        return ('<div class="legend">' + "".join(
            f'<span><i style="background:var(--lp-c{c})"></i>{t}</span>'
            for t, c in items) + "</div>")

    mo.Html(f"""
    <div class="lp-block">
      <div class="cards">
        <div class="card">
          <div class="card-hd"><h2>Cash</h2>
            <span class="muted small">{MONTHS} months</span></div>
          <div class="card-bd">{cash_chart}
            <p class="small muted">Where this crosses zero is the answer to the only
               question anybody outside the room is asking.</p></div>
        </div>
        <div class="card">
          <div class="card-hd"><h2>Gross profit against cost</h2></div>
          <div class="card-bd">{pl_chart}
            {legend([("Gross profit", 1), ("Costs", 2)])}
            <p class="small muted">Break-even is where they cross. Everything before it is
               being paid for out of the bank balance above.</p></div>
        </div>
      </div>
    </div>
    """)
    return


@app.cell(hide_code=True)
def _(mo, money, plan):
    rows = "".join(
        f"<tr>"
        f'<td class="lead mono">{r["month"]:02d}</td>'
        f'<td class="num">{r["customers"]:,.0f}</td>'
        f'<td class="num muted">{r["lost"]:,.1f}</td>'
        f'<td class="num">{r["headcount"]:,.0f}</td>'
        f'<td class="num mono">{money(r["mrr"])}</td>'
        f'<td class="num mono">{money(r["costs"])}</td>'
        f'<td class="num mono">'
        f'<span class="badge {"tone-ok" if r["net"] >= 0 else "tone-att"}">'
        f'{money(r["net"])}</span></td>'
        f'<td class="num mono">'
        f'<span class="badge {"tone-dan" if r["bank"] <= 0 else ""}">'
        f'{money(r["bank"])}</span></td>'
        f"</tr>"
        for r in plan)

    mo.Html(f"""
    <div class="lp-block">
      <div class="card">
        <div class="card-hd"><h2>The plan, month by month</h2>
          <span class="muted small">Every number here is the model's, not a rounding of a chart</span></div>
        <div class="card-bd" style="padding:0">
          <div class="tbl-wrap" style="max-height:26rem;overflow:auto">
            <table class="tbl">
              <thead><tr>
                <th>Month</th><th class="num">Customers</th><th class="num">Churned</th>
                <th class="num">People</th><th class="num">MRR</th><th class="num">Costs</th>
                <th class="num">Net</th><th class="num">Bank</th>
              </tr></thead>
              <tbody>{rows}</tbody>
            </table>
          </div>
        </div>
        <div class="card-ft">
          Twenty-four rows, recomputed on every slider move. marimo re-runs the cells that
          depend on what changed and nothing else — there is no hidden state to go stale,
          and no “run all above”.
        </div>
      </div>
    </div>
    """)
    return


@app.cell(hide_code=True)
def _(base, mo, on_platform, os, slug):
    def kv(k, v):
        return f"<dt>{k}</dt><dd>{v}</dd>"

    env_rows = "".join(kv(k, f'<span class="mono small">{os.environ.get(k) or "—"}</span>')
                       for k in ("TOFT_APP_SLUG", "BASE_PATH", "PORT",
                                 "TOFT_SCRATCH_DIR", "TOFT_EXECUTION_MODE"))

    mo.Html(f"""
    <div class="lp-block">
      <div class="cards">
        <div class="card">
          <div class="card-hd"><h2>Platform</h2></div>
          <div class="card-bd">
            <dl class="kv">
              {kv("Framework", '<span class="badge tone-brand">marimo</span>')}
              {kv("Detected by", "a <code>.py</code> at the root that imports marimo and "
                                 "constructs <code>marimo.App</code>")}
              {kv("Started with", '<span class="mono small">marimo run --base-url ' +
                                  (base or "/") + "</span>")}
              {kv("Workload", '<span class="badge tone-ok">yes</span>' if on_platform
                              else '<span class="badge">not on Toft</span>')}
              {env_rows}
            </dl>
            <div class="note tone-brand">
              <strong>marimo in <code>requirements.txt</code> is deliberately not the
              trigger.</strong> It is a library people import into ordinary programs, and a
              Flask app that depends on it stays a Flask app. What makes this a marimo app
              is that a file at the root builds a <code>marimo.App</code>.
            </div>
          </div>
        </div>
        <div class="card">
          <div class="card-hd"><h2>What this is not</h2></div>
          <div class="card-bd">
            <div class="note">
              <strong>Not a document.</strong> The gallery has two of those — a Jupyter
              notebook and an R Markdown report — and both are executed
              <em>once, at build time</em>, with the page you get being the output that
              fell out. Nothing in them moves.
            </div>
            <div class="note">
              <strong>This one is a server.</strong> It is executed per viewer session, so
              a slider is possible at all. One process serves the session, which is also
              the reason a heavy model here is a queue for whoever is sharing it —
              the same trade Shiny makes, for the same reason.
            </div>
            <p class="small muted">Nine assumptions, twenty-four months, no dependencies
               beyond marimo and the standard library, and no chart library.</p>
          </div>
        </div>
      </div>
      <footer class="foot" style="margin-top:1.5rem;border-radius:var(--lp-r)">
        <div class="foot-in">
          <span>An example app from the <strong>Toft</strong> gallery.</span>
          <span>Python · marimo · inline SVG · no chart library</span>
        </div>
      </footer>
    </div>
    """)
    return


if __name__ == "__main__":
    app.run()
