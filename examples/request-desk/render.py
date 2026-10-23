"""Every page this app serves, as strings.

Server-rendered HTML with one inline stylesheet and no JavaScript beyond a
`confirm()` on the two destructive buttons. There is no build step, no asset to
fetch and nothing to keep in step with a framework — which is the right shape
for a demonstration app somebody is going to read before they trust it.

Every link is built from ``base``, the prefix Toft mounts the app under.
Nothing here hard-codes a path.
"""

from __future__ import annotations

import html
from typing import Iterable
from urllib.parse import urlencode

from desk import Abilities, Caller

STYLE = """
:root {
  --ink: #16181d; --muted: #6b7280; --line: #e5e7eb; --bg: #f7f8fa; --panel: #ffffff;
  --accent: #2c4bd8; --ok: #0f7b45; --no: #b3261e; --wait: #8a6100;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--ink); font: 15px/1.55
  system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
main { max-width: 980px; margin: 0 auto; padding: 28px 20px 72px; }
h1 { font-size: 22px; margin: 0 0 2px; letter-spacing: -0.01em; }
h2 { font-size: 15px; margin: 26px 0 10px; text-transform: uppercase;
     letter-spacing: 0.06em; color: var(--muted); }
h3 { font-size: 15px; margin: 0 0 6px; }
p { margin: 0 0 10px; }
a { color: var(--accent); }
.sub { color: var(--muted); margin: 0 0 18px; }
nav { display: flex; gap: 4px; flex-wrap: wrap; border-bottom: 1px solid var(--line);
      margin: 0 0 22px; padding: 0 0 10px; }
nav a { padding: 5px 11px; border-radius: 6px; text-decoration: none; color: var(--ink); font-weight: 500; }
nav a:hover { background: #eceef3; }
nav a.on { background: var(--ink); color: #fff; }
.panel { background: var(--panel); border: 1px solid var(--line); border-radius: 10px;
         padding: 16px 18px; margin: 0 0 16px; }
.panel.quiet { background: #fbfcfd; }
.who { display: flex; gap: 14px; align-items: flex-start; }
.who .addr { font-weight: 600; }
.who .meta { color: var(--muted); font-size: 13px; }
.grants { display: flex; gap: 6px; flex-wrap: wrap; margin: 10px 0 0; }
.grant { font-size: 12px; padding: 2px 9px; border-radius: 999px; border: 1px solid var(--line);
         background: #f2f4f8; color: var(--muted); }
.grant.on { background: #eaf0ff; border-color: #c7d4fb; color: var(--accent); font-weight: 600; }
.why { margin: 12px 0 0; padding: 0 0 0 18px; color: var(--muted); font-size: 13.5px; }
.why li { margin: 3px 0; }
table { width: 100%; border-collapse: collapse; font-size: 14px; background: var(--panel); }
th { text-align: left; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em;
     color: var(--muted); font-weight: 600; padding: 8px 10px; border-bottom: 1px solid var(--line); }
td { padding: 9px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
tr:last-child td { border-bottom: 0; }
.wrap { border: 1px solid var(--line); border-radius: 10px; overflow: hidden; margin: 0 0 16px; }
.tag { font-size: 12px; font-weight: 600; padding: 2px 9px; border-radius: 999px; white-space: nowrap; }
.tag.pending { background: #fdf3dc; color: var(--wait); }
.tag.approved { background: #e3f5ea; color: var(--ok); }
.tag.rejected { background: #fbe6e5; color: var(--no); }
.tag.withdrawn { background: #eef0f3; color: var(--muted); }
form.inline { display: inline; }
label { display: block; font-size: 13px; font-weight: 600; margin: 12px 0 4px; }
label.cb { display: inline-flex; align-items: center; gap: 6px; font-weight: 400; margin: 0 14px 0 0; }
input[type=text], input[type=email], textarea, select {
  width: 100%; padding: 8px 10px; border: 1px solid #cfd4dd; border-radius: 7px;
  font: inherit; background: #fff; }
textarea { min-height: 78px; resize: vertical; }
.row { display: flex; gap: 14px; flex-wrap: wrap; }
.row > * { flex: 1 1 220px; }
button { font: inherit; font-weight: 600; padding: 7px 14px; border-radius: 7px;
         border: 1px solid var(--ink); background: var(--ink); color: #fff; cursor: pointer; }
button.ghost { background: #fff; color: var(--ink); border-color: #cfd4dd; }
button.ok { background: var(--ok); border-color: var(--ok); }
button.no { background: #fff; color: var(--no); border-color: #e3b5b2; }
button.small { padding: 4px 9px; font-size: 13px; }
.actions { margin: 16px 0 0; display: flex; gap: 8px; }
.note { background: #fff8e6; border: 1px solid #f0dfae; border-radius: 8px; padding: 10px 14px;
        margin: 0 0 16px; font-size: 14px; }
.note.bad { background: #fdecea; border-color: #f2c2bd; }
.note.good { background: #e9f6ee; border-color: #bfe3ce; }
.empty { color: var(--muted); padding: 18px 10px; text-align: center; font-size: 14px; }
footer { margin: 34px 0 0; padding: 14px 0 0; border-top: 1px solid var(--line);
         color: var(--muted); font-size: 13px; }
code { background: #eef0f4; padding: 1px 5px; border-radius: 4px; font-size: 13px; }
pre { background: #16181d; color: #e6e9ef; padding: 14px; border-radius: 8px; overflow-x: auto;
      font-size: 12.5px; line-height: 1.5; }
.mini { color: var(--muted); font-size: 12.5px; }
"""

NAV = (
    ("", "My desk", None),
    ("queue", "Queue", "view_all"),
    ("people", "People", "manage"),
    ("groups", "Groups", "manage"),
    ("access", "Access", None),
)


def e(value) -> str:
    return html.escape("" if value is None else str(value))


def page(base: str, title: str, heading: str, sub: str, body: str, *,
         here: str = "", abilities: Abilities | None = None, flash: str = "",
         flash_kind: str = "", footer: str = "") -> str:
    nav = []
    for slug, label, need in NAV:
        if need and not (abilities and getattr(abilities, need)):
            continue
        href = f"{base}/{slug}" if slug else (base or "/")
        cls = ' class="on"' if slug == here else ""
        nav.append(f'<a href="{e(href)}"{cls}>{e(label)}</a>')
    banner = ""
    if flash:
        kind = f" {flash_kind}" if flash_kind else ""
        banner = f'<div class="note{kind}">{e(flash)}</div>'
    return f"""<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{e(title)}</title><style>{STYLE}</style></head>
<body><main>
<h1>{e(heading)}</h1>
<p class="sub">{sub}</p>
<nav>{''.join(nav)}</nav>
{banner}
{body}
<footer>{footer}</footer>
</main></body></html>"""


def identity_card(caller: Caller, abilities: Abilities, *, source: str) -> str:
    grants = [
        ("administers the desk", abilities.manage),
        ("submits requests", abilities.submit),
        ("sees every request", abilities.view_all),
        ("decides", abilities.decide),
    ]
    chips = "".join(
        f'<span class="grant{" on" if on else ""}">{e(label)}</span>' for label, on in grants
    )
    groups = ", ".join(abilities.groups) if abilities.groups else "none"
    toft = ", ".join(caller.lp_groups) if caller.lp_groups else "none"
    why = "".join(f"<li>{e(line)}</li>" for line in abilities.why) or "<li>No rule applied.</li>"
    return f"""<div class="panel">
  <div class="who"><div>
    <div class="addr">{e(caller.name or caller.email or "an anonymous visitor")}</div>
    <div class="meta">{e(caller.email or "no address")} · Toft role
      <strong>{e(caller.role or "none")}</strong>{" · install administrator" if caller.is_admin else ""}</div>
    <div class="meta">Toft groups: {e(toft)} · desk groups: {e(groups)}</div>
  </div></div>
  <div class="grants">{chips}</div>
  <ul class="why">{why}</ul>
  <p class="mini" style="margin:10px 0 0">{source}</p>
</div>"""


def request_rows(base: str, rows: Iterable[dict], *, viewer_email: str, can_decide: bool,
                 show_requester: bool) -> str:
    body = []
    for r in rows:
        actions = []
        if r["status"] == "pending" and can_decide:
            actions.append(
                f'<form class="inline" method="post" action="{e(base)}/requests/{r["id"]}/decide">'
                f'<input type="hidden" name="verdict" value="approved">'
                f'<button class="small ok" type="submit">Approve</button></form>'
            )
            actions.append(
                f'<form class="inline" method="post" action="{e(base)}/requests/{r["id"]}/decide">'
                f'<input type="hidden" name="verdict" value="rejected">'
                f'<button class="small no" type="submit">Reject</button></form>'
            )
        if r["status"] == "pending" and r["requester_email"] == viewer_email:
            actions.append(
                f'<form class="inline" method="post" action="{e(base)}/requests/{r["id"]}/withdraw">'
                f'<button class="small ghost" type="submit">Withdraw</button></form>'
            )
        who = ""
        if show_requester:
            who = (f'<td>{e(r["requester_name"] or r["requester_email"])}'
                   f'<div class="mini">{e(r["requester_email"])}</div></td>')
        decided = ""
        if r["decided_by"]:
            decided = (f'<div class="mini">{e(r["status"])} by {e(r["decided_by"])}'
                       f'{" — " + e(r["decision_note"]) if r["decision_note"] else ""}</div>')
        detail = f'<div class="mini">{e(r["details"][:180])}</div>' if r["details"] else ""
        amount = f' · {e(r["amount"])}' if r["amount"] else ""
        body.append(f"""<tr>
  <td>#{r["id"]}</td>
  <td><strong>{e(r["title"])}</strong>
      <div class="mini">{e(r["category"] or "uncategorised")}{amount} · {e(r["created_at"][:10])}</div>
      {detail}{decided}</td>
  {who}
  <td><span class="tag {e(r["status"])}">{e(r["status"])}</span></td>
  <td>{" ".join(actions)}</td>
</tr>""")
    if not body:
        span = 5 if show_requester else 4
        return f'<div class="wrap"><table><tr><td class="empty" colspan="{span}">Nothing here yet.</td></tr></table></div>'
    head = "<th>#</th><th>Request</th>" + ("<th>Requester</th>" if show_requester else "") + \
           "<th>Status</th><th></th>"
    return f'<div class="wrap"><table><thead><tr>{head}</tr></thead><tbody>{"".join(body)}</tbody></table></div>'


def submit_form(base: str) -> str:
    return f"""<div class="panel">
  <h3>New request</h3>
  <form method="post" action="{e(base)}/requests">
    <div class="row">
      <div><label for="title">Title</label>
        <input id="title" name="title" type="text" required maxlength="200"
               placeholder="A second monitor for the support desk"></div>
      <div><label for="category">Category</label>
        <input id="category" name="category" type="text" maxlength="80" placeholder="Equipment"></div>
      <div><label for="amount">Amount</label>
        <input id="amount" name="amount" type="text" maxlength="40" placeholder="£240"></div>
    </div>
    <label for="details">Details</label>
    <textarea id="details" name="details" maxlength="4000"
              placeholder="What it is for, and when it is needed."></textarea>
    <div class="actions"><button type="submit">Submit request</button></div>
  </form>
</div>"""


def people_page(base: str, people: list[dict], groups: list[dict]) -> str:
    rows = []
    for p in people:
        checks = []
        for g in groups:
            on = " checked" if g["name"] in p["groups"] else ""
            checks.append(
                f'<form class="inline" method="post" action="{e(base)}/people/{e(p["email"])}/groups">'
                f'<input type="hidden" name="group_id" value="{g["id"]}">'
                f'<input type="hidden" name="member" value="{"0" if on else "1"}">'
                f'<label class="cb"><input type="checkbox"{on} onchange="this.form.submit()">'
                f'{e(g["name"])}</label></form>'
            )
        state = "active" if p["active"] else "deactivated"
        toggle = "0" if p["active"] else "1"
        rows.append(f"""<tr>
  <td><strong>{e(p["name"] or "—")}</strong><div class="mini">{e(p["email"])}</div></td>
  <td>{"".join(checks) or '<span class="mini">no groups exist yet</span>'}</td>
  <td><span class="tag {"approved" if p["active"] else "withdrawn"}">{e(state)}</span>
      <div class="mini">{p["request_count"]} request(s)</div></td>
  <td>
    <form class="inline" method="post" action="{e(base)}/people/{e(p["email"])}/active">
      <input type="hidden" name="active" value="{toggle}">
      <button class="small ghost" type="submit">{"Deactivate" if p["active"] else "Reactivate"}</button>
    </form>
    <form class="inline" method="post" action="{e(base)}/people/{e(p["email"])}/delete"
          onsubmit="return confirm('Remove {e(p["email"])} from the directory? Their requests stay.')">
      <button class="small no" type="submit">Remove</button>
    </form>
  </td>
</tr>""")
    table = "".join(rows) or '<tr><td class="empty" colspan="4">Nobody in the directory yet.</td></tr>'
    return f"""<div class="panel">
  <h3>Add somebody</h3>
  <p class="mini">The address is the identity. When they open this app, Toft tells it their
     address and this row is what it matches.</p>
  <form method="post" action="{e(base)}/people">
    <div class="row">
      <div><label for="email">Email address</label>
        <input id="email" name="email" type="email" required placeholder="rosa@example.com"></div>
      <div><label for="name">Name</label>
        <input id="name" name="name" type="text" placeholder="Rosa Iqbal"></div>
    </div>
    <div class="actions"><button type="submit">Add to directory</button></div>
  </form>
</div>
<h2>Directory</h2>
<div class="wrap"><table>
<thead><tr><th>Person</th><th>Groups</th><th>State</th><th></th></tr></thead>
<tbody>{table}</tbody></table></div>"""


def groups_page(base: str, groups: list[dict]) -> str:
    rows = []
    for g in groups:
        def cb(field: str, label: str) -> str:
            on = " checked" if g[field] else ""
            return f'<label class="cb"><input type="checkbox" name="{field}" value="1"{on}> {label}</label>'
        rows.append(f"""<tr>
  <td><strong>{e(g["name"])}</strong><div class="mini">{g["member_count"]} member(s)</div></td>
  <td>
    <form method="post" action="{e(base)}/groups/{g["id"]}">
      <input type="text" name="description" value="{e(g["description"])}" placeholder="What this group is for">
      <div style="margin:8px 0">{cb("can_submit", "submit")}{cb("can_view_all", "see every request")}{cb("can_decide", "decide")}</div>
      <input type="text" name="lp_group" value="{e(g["lp_group"])}"
             placeholder="Toft group to link (optional)">
      <div class="actions"><button class="small" type="submit">Save</button></div>
    </form>
  </td>
  <td>
    <form class="inline" method="post" action="{e(base)}/groups/{g["id"]}/delete"
          onsubmit="return confirm('Delete the group {e(g["name"])}? Its members lose what it granted.')">
      <button class="small no" type="submit">Delete</button>
    </form>
  </td>
</tr>""")
    table = "".join(rows) or '<tr><td class="empty" colspan="3">No groups.</td></tr>'
    return f"""<div class="panel">
  <h3>New group</h3>
  <p class="mini">A group is three switches and an optional link. Link one to a Toft group and
     anybody the platform puts in that group lands here without being added by hand.</p>
  <form method="post" action="{e(base)}/groups">
    <div class="row">
      <div><label for="gname">Name</label><input id="gname" name="name" type="text" required></div>
      <div><label for="gdesc">Description</label><input id="gdesc" name="description" type="text"></div>
      <div><label for="glp">Linked Toft group</label>
        <input id="glp" name="lp_group" type="text" placeholder="finance"></div>
    </div>
    <div style="margin:12px 0 0">
      <label class="cb"><input type="checkbox" name="can_submit" value="1"> submit</label>
      <label class="cb"><input type="checkbox" name="can_view_all" value="1"> see every request</label>
      <label class="cb"><input type="checkbox" name="can_decide" value="1"> decide</label>
    </div>
    <div class="actions"><button type="submit">Create group</button></div>
  </form>
</div>
<h2>Groups</h2>
<div class="wrap"><table>
<thead><tr><th>Group</th><th>What it grants</th><th></th></tr></thead>
<tbody>{table}</tbody></table></div>"""


def blocked_panel(title: str, body: str) -> str:
    return f'<div class="panel"><h3>{e(title)}</h3>{body}</div>'


def redirect_target(where: str = "", msg: str = "", kind: str = "") -> str:
    """Where a mutation sends the browser afterwards — **without the prefix**.

    Every link on a page carries the prefix and this one must not, and the
    asymmetry is the platform's rather than a slip here.

    Toft starts uvicorn with ``--root-path /apps/{slug}`` and its proxy
    *strips* that prefix before forwarding, so the app is mounted at ``/`` and
    builds its own links back up from ``root_path``. But the proxy also puts
    the prefix back on any ``Location`` header coming the other way — that is
    what makes a framework's own redirects work without the framework knowing
    where it is mounted. A redirect that prefixed the path itself is therefore
    prefixed twice, and arrives as
    ``/apps/request-desk/apps/request-desk/people``.

    So: ``href`` and ``action`` get ``base``; ``Location`` never does.
    """
    target = f"/{where.lstrip('/')}" if where else "/"
    if msg:
        target += "?" + urlencode({"msg": msg, "kind": kind})
    return target
