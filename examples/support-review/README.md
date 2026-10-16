# Support review

A notebook example for Toft. One `.ipynb` at the root of a repository is
an app: Toft installs its `requirements.txt`, executes every cell once at
deploy time with the app's own environment, and serves the HTML that fell out
at `/apps/{slug}/`.

The report itself is a monthly review of a support desk — arrival volume by
team, first response against a target, and the open backlog by age — drawn from
a seeded generator so it is worth looking at before anybody has plugged in a
real data source. Replace the cell under **The data** with your query; nothing
else in the notebook changes.

## The four things worth copying

**The cells run once, at deploy — not per viewer.** There is no kernel behind
the published page. A wake, a crash restore or a repair serves the same file;
only a deploy produces a new one, and a cell that raises fails the deploy while
the release already serving stays up. The app's **Overview** tab has a
re-render schedule that turns a cron expression into that deploy.

**Configuration comes from the environment, and is clamped.** A hosted report
differs from one on a laptop in exactly this: the title, the teams, the period
and the response target are the app's settings rather than constants in a cell.
`int_env()` never raises, because a report should not be taken down by somebody
typing `twelve` into a settings box.

**Whatever a cell prints is published.** The last section prints the variable
*names* it read and deliberately not their values. On a public app, published
means the internet. This is the framework's sharpest edge and the platform
cannot gate it — it has no way to know which cell output is a secret.

**Only the render is served.** Toft publishes `toft-render/`, a
directory it owns, and never the release — so the notebook source, the
`requirements.txt` and the virtualenv are all 404 to a visitor.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `REVIEW_TITLE` | no | `Support review` | The line the report opens with. |
| `TEAMS` | no | `Billing,Identity,Platform,Mobile` | Comma-separated. The first eight are used. |
| `PERIOD_WEEKS` | no | `12` | How many weeks the review covers. Clamped to 4–52. |
| `SLA_HOURS` | no | `8` | The first-response target. Clamped to 1–168. |

There are no parameters, in either render engine: a notebook reads its app's
environment, and an app's environment is per app. Two reports that need
different settings are two apps deployed from the same repository.

## The engine

nbconvert, the default for an `.ipynb`, invoked as

```
python -m nbconvert --to html --template lab --execute
```

which is what `File → Export as HTML` produces — so the deployed page matches
the one that was checked before it was pushed, and figures arrive as data URIs
in a single self-contained document.

A repo that wants a contents sidebar, code folding, callouts and figure
captions asks for Quarto instead:

```toml
# toft.toml
[notebook]
engine = "quarto"
```

Quarto is not bundled. On an install without it, that declaration fails the
deploy naming `QUARTO_BIN` rather than quietly rendering with the other engine.

## Local development

```bash
python -m venv venv && venv/bin/pip install -r requirements.txt nbconvert ipykernel
venv/bin/python -m nbconvert --to html --template lab --execute \
    --output index.html --output-dir toft-render support_review.ipynb
```

`nbconvert` and `ipykernel` are absent from `requirements.txt` on purpose:
Toft installs the renderer itself, because the engine is the platform's
business and not the report's.

`toft-render/` is gitignored here so a local render is never committed —
a release that already contains a directory of that name fails the deploy by
name, since it is the one directory the platform writes into.

## The house style

The look is the [Toft Example Kit](https://github.com/larchway-labs/toft-gallery/tree/main/kit) —
`static/toft-kit.css`, byte-identical in every example in the gallery, with
`static/nb.css` as the layer between it and nbconvert's `lab` template. The
first cell is two lines and an import; `chrome.py` is where the detail lives.

That split is deliberate. **nbconvert's `lab` template shows every cell's
source**, and Toft runs that template on purpose: it is what *File → Export
as HTML* produces, so the deployed page matches the one the author already looked
at. A notebook app shows its working — so eighty lines of CSS at the top of the
document would be the first thing anybody read, and the right response is to make
the code pleasant rather than to fight the template.

The chart palette is the kit's five series, copied into `PALETTE`, because
matplotlib cannot read a stylesheet.
