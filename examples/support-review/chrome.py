"""The house style, inside a notebook.

Toft executes this notebook **once, at build time**, and serves the HTML
that fell out. There is no kernel behind the published page, so the only way to
style it from inside the notebook is to have a cell emit the stylesheet — which
is what :func:`masthead` does.

It lives in a module rather than in the notebook because nbconvert's ``lab``
template shows every code cell's source, and Toft runs that template on
purpose: it is what *File → Export as HTML* produces, so the deployed page
matches the one the author already looked at. A notebook app therefore shows its
working, and eighty lines of CSS at the top of the document would be the first
thing anybody read. Two lines and an import is the right size for the first cell.
"""

from __future__ import annotations

import os
from pathlib import Path

from IPython.display import HTML, display

HERE = Path(__file__).parent


def _css() -> str:
    return "".join((HERE / "static" / name).read_text()
                   for name in ("toft-kit.css", "nb.css"))


def _cap(label: str, on: bool, note: str = "") -> str:
    return (f'<span class="cap {"on" if on else "off"}" title="{note}">'
            f"<b>{label}</b></span>")


def _rail() -> str:
    on_platform = bool(os.environ.get("TOFT_APP_TOKEN"))
    return "".join([
        _cap("Notebook", True,
             "One .ipynb at the repository root, executed once at build time with "
             "nbconvert and the lab template, and served as the document that fell out."),
        _cap("Built at deploy", True,
             "Nothing is re-rendered by a restart. A wake, a crash restore or a repair "
             "serves this same file; only a deploy produces a new one."),
        _cap("No kernel", True,
             "There is no Python process behind this page. Nothing here is live, and "
             "nothing here should try to be."),
        _cap("The app's environment", True,
             "The cells ran with it. That is what lets this report read its own "
             "settings — and why a cell that printed a connection string would have "
             "published it to everyone who can read the page."),
        _cap("toft-render/", True,
             "The platform's directory, not the release. The notebook source, "
             "requirements.txt and venv/ are all 404."),
        _cap("Toft workload", on_platform,
             "Rendered as a Toft workload." if on_platform
             else "Rendered outside Toft."),
    ])


def masthead(title: str, standfirst: str, chip: str = "") -> None:
    """The bar and the rail every example in the Toft gallery opens with."""
    display(HTML(f"""<style>{_css()}</style>
<div class="lp-block">
<div class="masthead"><div class="masthead-in">
<div>
<div class="wordmark"><span class="mark"></span>
<span class="wordmark-text">Toft example</span></div>
<h1>{title}</h1>
<p class="standfirst">{standfirst}</p>
</div>
<div class="masthead-aside">
<span class="chip chip-lang">Python &middot; Jupyter</span>
{f'<span class="chip">{chip}</span>' if chip else ''}
</div>
</div></div>
<div class="rail"><div class="rail-in">
<span class="rail-label">Toft</span>{_rail()}
</div></div>
</div>"""))
