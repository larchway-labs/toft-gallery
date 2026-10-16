"""CSV Explorer — upload a file, filter it, chart it, take it away again.

A Streamlit example for Toft that is useful on the day it is deployed:
drop in a CSV export from anywhere, and get filtering, summary statistics, a
chart and a cleaned download without writing a notebook.

It ships with a small generated dataset so the app has something to show before
anybody uploads anything — an example whose first screen is an empty file
picker teaches nothing about the framework.

Two things worth copying into your own Streamlit app:

  * Uploaded data is held in `st.session_state` and never written to disk. On a
    shared install every session runs in one process under one UID, so a file
    written to the app's directory is a file every other viewer can read.
  * Nothing here binds a port or builds a URL. Toft starts Streamlit with
    --server.baseUrlPath, and Streamlit is the one framework that keeps the
    prefix rather than having it stripped.
"""

import datetime
import io
import os
import random

import pandas as pd
import streamlit as st

import chrome

APP_TITLE = os.getenv("APP_TITLE", "CSV Explorer")
MAX_ROWS_CHARTED = 2000

st.set_page_config(page_title=APP_TITLE, page_icon="📊", layout="wide")


@st.cache_data
def sample_data() -> pd.DataFrame:
    """A deterministic sample: two years of weekly figures for four regions.

    Seeded on purpose. A demo dataset that changes on every rerun makes every
    screenshot and every bug report unreproducible.
    """
    rng = random.Random(20240612)
    regions = ["North", "South", "East", "West"]
    base = {"North": 1400, "South": 980, "East": 1220, "West": 760}
    trend = {"North": 6.0, "South": 2.5, "East": 9.0, "West": -1.5}

    start = datetime.date(2024, 1, 7)
    rows = []
    for week in range(104):
        day = start + datetime.timedelta(weeks=week)
        for region in regions:
            seasonal = 90 * (1 if day.month in (11, 12) else 0)
            noise = rng.gauss(0, 55)
            revenue = base[region] + trend[region] * week + seasonal + noise
            orders = max(1, int(revenue / rng.uniform(28, 42)))
            rows.append(
                {
                    "week": day,
                    "region": region,
                    "revenue": round(max(revenue, 0), 2),
                    "orders": orders,
                    "returns": max(0, int(orders * rng.uniform(0.01, 0.07))),
                }
            )
    return pd.DataFrame(rows)


def load_frame() -> tuple:
    """Returns (dataframe, source label). The uploaded file wins when there is
    one; otherwise the sample."""
    uploaded = st.session_state.get("uploaded")
    if uploaded is None:
        return sample_data(), "the built-in sample"
    try:
        # Held in memory only — see the module docstring.
        frame = pd.read_csv(io.BytesIO(uploaded["bytes"]))
    except Exception as exc:  # noqa: BLE001 — the message is the whole point
        st.error(f"That file could not be read as CSV: {exc}")
        return sample_data(), "the built-in sample"
    return frame, uploaded["name"]


# The masthead and the capability rail every example in the gallery opens with.
# Streamlit draws its own shell, so this is injected rather than laid out; see
# chrome.py for what that costs and what it checks.
chrome.masthead(
    APP_TITLE,
    "Drop in a CSV export from anywhere and get filtering, summary statistics, a "
    "chart and a cleaned download &mdash; without writing a notebook.",
    [
        chrome.cap("Streamlit", True,
                   "Detected from `streamlit` in requirements.txt."),
        chrome.cap("baseUrlPath", bool(os.environ.get("TOFT_APP_SLUG")),
                   "Streamlit is the one framework that keeps its /apps/{slug} prefix "
                   "rather than having it stripped."),
        chrome.cap("WebSocket through the proxy", True,
                   "Toft passes --server.enableCORS false, because Streamlit "
                   "compares Origin against the upstream Host and would refuse every "
                   "upgrade. The page would render blank with the reason only in the log."),
        chrome.cap("Session state", True,
                   "Uploads are held in st.session_state and never written to disk."),
        chrome.cap("Toft workload", bool(os.environ.get("TOFT_APP_TOKEN")),
                   "Running as a Toft workload."
                   if os.environ.get("TOFT_APP_TOKEN") else "This is a local run."),
    ],
)

agrees, drift = chrome.palette_agrees()
if not agrees:
    chrome.note(
        f"<strong>The palette has drifted.</strong> {drift}. "
        "<code>.streamlit/config.toml</code> and <code>toft-kit.css</code> "
        "both name these colours and there is no way to share them, so this page "
        "checks rather than assumes.", "tone-att")

with st.sidebar:
    st.header("Data")
    file = st.file_uploader("Upload a CSV", type=["csv"], help="Held in memory; never written to disk.")
    if file is not None:
        st.session_state["uploaded"] = {"name": file.name, "bytes": file.getvalue()}
    if st.session_state.get("uploaded") and st.button("Forget the upload", width="stretch"):
        del st.session_state["uploaded"]
        st.rerun()

df, source = load_frame()
st.caption(f"Showing **{source}** — {len(df):,} rows, {len(df.columns)} columns.")

if df.empty:
    st.warning("That file has no rows.")
    st.stop()

# --- filtering ---------------------------------------------------------------

numeric_cols = [c for c in df.columns if pd.api.types.is_numeric_dtype(df[c])]
categorical_cols = [
    c for c in df.columns if not pd.api.types.is_numeric_dtype(df[c]) and df[c].nunique(dropna=True) <= 50
]

filtered = df
with st.sidebar:
    st.header("Filter")
    if not categorical_cols:
        st.caption("No column with a small enough set of values to filter on.")
    for col in categorical_cols[:4]:
        values = sorted(str(v) for v in df[col].dropna().unique())
        chosen = st.multiselect(col, values, default=values, key=f"filter-{col}")
        if len(chosen) != len(values):
            filtered = filtered[filtered[col].astype(str).isin(chosen)]

if filtered.empty:
    st.warning("Nothing matches those filters.")
    st.stop()

# --- summary -----------------------------------------------------------------

if numeric_cols:
    cols = st.columns(min(4, len(numeric_cols)))
    for slot, name in zip(cols, numeric_cols[:4]):
        series = filtered[name]
        slot.metric(name, f"{series.sum():,.0f}", help=f"mean {series.mean():,.1f} · max {series.max():,.0f}")

# --- chart -------------------------------------------------------------------

if numeric_cols:
    st.subheader("Chart")
    left, right = st.columns([2, 3])
    with left:
        y = st.selectbox("Value", numeric_cols, index=0)
        group_options = ["(none)"] + categorical_cols
        group = st.selectbox("Split by", group_options, index=1 if categorical_cols else 0)
        x_options = [c for c in filtered.columns if c != y]
        x = st.selectbox("Across", x_options, index=0) if x_options else None

    if x:
        chart_source = filtered
        if len(chart_source) > MAX_ROWS_CHARTED:
            chart_source = chart_source.head(MAX_ROWS_CHARTED)
            right.caption(f"Charting the first {MAX_ROWS_CHARTED:,} rows.")
        if group != "(none)":
            pivot = chart_source.pivot_table(index=x, columns=group, values=y, aggfunc="sum").fillna(0)
        else:
            pivot = chart_source.groupby(x)[y].sum()
        right.line_chart(pivot, height=320)

# --- table and download ------------------------------------------------------

st.subheader("Rows")
st.dataframe(filtered, width="stretch", height=380)

st.download_button(
    "Download what you are looking at (CSV)",
    data=filtered.to_csv(index=False).encode("utf-8"),
    file_name="filtered.csv",
    mime="text/csv",
)

with st.expander("Column summary"):
    st.dataframe(
        pd.DataFrame(
            {
                "column": df.columns,
                "type": [str(df[c].dtype) for c in df.columns],
                "non-null": [int(df[c].notna().sum()) for c in df.columns],
                "distinct": [int(df[c].nunique(dropna=True)) for c in df.columns],
            }
        ),
        width="stretch",
        hide_index=True,
    )


# --- what the platform told this app -----------------------------------------
#
# Every example in the gallery carries this. On Streamlit it is an expander,
# because the page is one column and a tab strip would fight the sidebar.

with st.expander("Platform — what Toft told this app"):
    left, right = st.columns(2)
    with left:
        st.markdown("**Started with**")
        st.code(
            "streamlit run streamlit_app.py \\\n"
            "  --server.address 127.0.0.1 \\\n"
            "  --server.port $PORT \\\n"
            "  --server.baseUrlPath /apps/{slug} \\\n"
            "  --server.enableCORS false",
            language="bash",
        )
        chrome.note(
            "<strong>Streamlit keeps its prefix.</strong> It is the exception: for "
            "Flask, Go and generic Node the proxy strips <code>/apps/{slug}</code> "
            "before forwarding, and here it forwards the full path because "
            "<code>--server.baseUrlPath</code> has already told Streamlit to expect it.")
        chrome.note(
            "<strong><code>--server.enableCORS false</code> is not optional.</strong> "
            "A reverse proxy rewrites <code>Host</code> to the upstream address, so "
            "Streamlit sees a mismatch against the browser's <code>Origin</code> and "
            "refuses every WebSocket upgrade &mdash; and since Streamlit is entirely "
            "WebSocket-driven, the page renders <em>blank</em> with the reason only in "
            "the app's log. Toft applies its own origin check first, from an "
            "allowlist that knows every valid origin.", "tone-att")
    with right:
        st.markdown("**Environment**")
        st.dataframe(
            pd.DataFrame(
                [
                    ("TOFT_APP_SLUG", os.environ.get("TOFT_APP_SLUG", "—"),
                     "This app's slug."),
                    ("TOFT_EXECUTION_MODE", os.environ.get("TOFT_EXECUTION_MODE", "—"),
                     "shared or isolated."),
                    ("TOFT_SCRATCH_DIR", os.environ.get("TOFT_SCRATCH_DIR", "—"),
                     "The app's own corner of the disk, wiped on every deploy."),
                    ("TOFT_APP_TOKEN",
                     "set" if os.environ.get("TOFT_APP_TOKEN") else "—",
                     "The app calling out. This example never uses it."),
                    ("APP_TITLE", APP_TITLE, "The heading, from the app's Settings tab."),
                ],
                columns=["variable", "value", "what it is for"],
            ),
            width="stretch",
            hide_index=True,
        )
        chrome.note(
            "<strong>Nothing here writes to disk.</strong> On a shared install every "
            "session runs in one process under one UID, so a file written to the app's "
            "directory is a file every other viewer can read. The upload lives in "
            "<code>st.session_state</code> and dies with the session.")

chrome.footer("Python &middot; Streamlit &middot; pandas &middot; no chart library")
