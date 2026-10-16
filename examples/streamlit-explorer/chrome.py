"""The house style, inside Streamlit.

Streamlit draws its own shell, so an example here cannot simply carry the
gallery's HTML skeleton. What it can do is two things, and this module is both:

  * hand Streamlit the kit's palette through `.streamlit/config.toml`, which is
    what colours the widgets, and
  * inject `toft-kit.css` plus the masthead and capability rail every other
    example in the gallery renders.

The palette is duplicated between the TOML file and the stylesheet because there
is no way to share it. :func:`palette_agrees` checks the two have not drifted and
the page says so if they have — a design system that quietly disagrees with
itself is worse than one that never tried.
"""

from __future__ import annotations

import os
import re
from pathlib import Path

import streamlit as st

HERE = Path(__file__).parent
KIT = HERE / "static" / "toft-kit.css"
CONFIG = HERE / ".streamlit" / "config.toml"


@st.cache_data
def _kit_css() -> str:
    return KIT.read_text() if KIT.exists() else ""


def palette_agrees() -> tuple[bool, str]:
    """Whether config.toml still names the same colours the stylesheet defines."""
    if not (KIT.exists() and CONFIG.exists()):
        return True, ""
    css, toml = _kit_css(), CONFIG.read_text()
    pairs = {
        "primaryColor": "--lp-primary",
        "backgroundColor": "--lp-paper",
        "secondaryBackgroundColor": "--lp-card",
        "textColor": "--lp-ink",
        "borderColor": "--lp-rule",
    }
    for key, token in pairs.items():
        want = re.search(rf"{token}:\s*(#[0-9A-Fa-f]{{6}})", css)
        got = re.search(rf'{key}\s*=\s*"(#[0-9A-Fa-f]{{6}})"', toml)
        if want and got and want.group(1).upper() != got.group(1).upper():
            return False, f"{key} is {got.group(1)} and {token} is {want.group(1)}"
    return True, ""


def cap(label: str, on: bool | str, note: str = "") -> str:
    state = on if isinstance(on, str) else ("on" if on else "off")
    return f'<span class="cap {state}" title="{note}"><b>{label}</b></span>'


def masthead(title: str, standfirst: str, caps: list[str]) -> None:
    """The bar and the rail every example in the gallery opens with."""
    slug = os.environ.get("TOFT_APP_SLUG", "")
    _html(
        f"""<style>
{_kit_css()}
/* Streamlit's own container is the page, so the kit's page-level rules are
   scoped to the block below. Three overrides and no more. */
.lp-block {{ font: 400 15px/1.6 var(--lp-sans); color: var(--lp-ink); }}
.lp-block *, .lp-block *::before, .lp-block *::after {{ box-sizing: border-box; }}
.lp-block .masthead {{ border-radius: var(--lp-r) var(--lp-r) 0 0; }}
.lp-block .rail {{ border-radius: 0 0 var(--lp-r) var(--lp-r); }}
/* Streamlit's metric, brought closer to the kit's tile without fighting it. */
[data-testid="stMetric"] {{
  background: var(--lp-card); border: 1px solid var(--lp-rule);
  border-radius: var(--lp-r); padding: .875rem 1rem 1rem; position: relative; overflow: hidden;
}}
[data-testid="stMetric"]::before {{
  content: ""; position: absolute; inset: 0 auto 0 0; width: 3px; background: var(--lp-primary);
}}
[data-testid="stMetricLabel"] {{ color: var(--lp-ink-3); }}
[data-testid="stSidebar"] {{ border-right: 1px solid var(--lp-rule); }}
</style>
<div class="lp-block">
<div class="masthead"><div class="masthead-in">
    <div>
      <div class="wordmark">
        <span class="mark"></span>
        <span class="wordmark-text">Toft example</span>
      </div>
      <h1>{title}</h1>
      <p class="standfirst">{standfirst}</p>
    </div>
    <div class="masthead-aside">
      <span class="chip chip-lang">Python &middot; Streamlit</span>
      {f'<span class="chip">{slug}</span>' if slug else ''}
    </div>
</div></div>
<div class="rail"><div class="rail-in">
    <span class="rail-label">Toft</span>{''.join(caps)}
  </div></div>
</div>""")


def footer(right: str) -> None:
    _html(f"""<div class="lp-block">
<div class="foot" style="border-radius:var(--lp-r);margin-top:1.5rem">
<div class="foot-in">
<span>An example app from the <strong>Toft</strong> gallery.</span>
<span>{right}</span>
</div>
</div>
</div>""")


def _html(markup: str) -> None:
    """Render raw HTML through Streamlit's markdown pipe.

    Every line is un-indented first. Streamlit renders this as Markdown, and
    Markdown reads a line indented four spaces as a code block — which is how a
    masthead ends up on the page with a stray ``</div>`` in a grey box.
    """
    lines = [line.lstrip() for line in markup.splitlines()]
    st.markdown("\n".join(lines), unsafe_allow_html=True)


def note(text: str, tone: str = "") -> None:
    _html(f'<div class="lp-block"><div class="note {tone}">{text}</div></div>')
