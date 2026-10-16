package main

import (
	"html/template"
	"net/http"
)

// The page.
//
// One template, CSS and script inline, no external asset of any kind. A tool
// for looking at a filesystem should not need the network to work beyond the
// request that fetched it — and an install where the platform is reachable but
// a CDN is not is exactly the install this app tends to run in.
//
// Everything below the shell is drawn from /api/volumes and /api/list, and
// every name that reaches the DOM goes through textContent rather than a
// string of HTML. A file on a shared EFS volume was put there by something
// other than this app; a filename is data, and it is treated as data.
var page = template.Must(template.New("browser").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root {
    color-scheme: light;
    --ink: #1c2024; --muted: #6b7280; --faint: #98a2b3;
    --line: #e5e7eb; --hair: #f0f1f3; --paper: #fff; --ground: #f7f8fa;
    --accent: #175cd3; --accent-soft: #eff4ff;
    --danger: #b42318; --danger-soft: #fef3f2;
    --ok: #067647; --ok-soft: #ecfdf3;
    --warn: #b54708; --warn-soft: #fffaeb;
  }
  * { box-sizing: border-box; }
  body { margin: 0; font: 15px/1.5 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
         color: var(--ink); background: var(--ground); }
  header.top { background: var(--paper); border-bottom: 1px solid var(--line); padding: .9rem 1.5rem;
               display: flex; align-items: baseline; gap: .75rem; }
  header.top h1 { font-size: 1.05rem; margin: 0; letter-spacing: -0.01em; }
  header.top .sub { color: var(--muted); font-size: .8rem; }
  .layout { display: flex; align-items: flex-start; gap: 1.25rem; padding: 1.25rem 1.5rem 3rem; max-width: 76rem; margin: 0 auto; }
  aside { width: 17rem; flex: none; }
  aside h2, main h2 { font-size: .72rem; text-transform: uppercase; letter-spacing: .06em; color: var(--muted);
                      margin: 0 0 .5rem; font-weight: 600; }
  .vol { display: block; width: 100%; text-align: left; background: var(--paper); border: 1px solid var(--line);
         border-radius: 10px; padding: .6rem .75rem; margin-bottom: .5rem; cursor: pointer; font: inherit; color: inherit; }
  .vol:hover { border-color: #cdd3dc; }
  .vol[aria-current="true"] { border-color: var(--accent); box-shadow: 0 0 0 1px var(--accent); background: var(--accent-soft); }
  .vol[disabled] { cursor: not-allowed; opacity: .85; }
  .vol .name { font-weight: 600; font-size: .9rem; word-break: break-all; }
  .vol .path { color: var(--muted); font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
               font-size: .72rem; word-break: break-all; margin-top: .15rem; }
  .vol .why { color: var(--warn); font-size: .75rem; margin-top: .35rem; }
  main { flex: 1; min-width: 0; }
  .card { background: var(--paper); border: 1px solid var(--line); border-radius: 10px; overflow: hidden; }
  .bar { display: flex; align-items: center; gap: .5rem; flex-wrap: wrap; padding: .6rem .75rem; border-bottom: 1px solid var(--hair); }
  .crumbs { flex: 1; min-width: 12rem; font-size: .85rem; color: var(--muted); word-break: break-all; }
  .crumbs button { background: none; border: 0; padding: 0 .1rem; font: inherit; color: var(--accent); cursor: pointer; }
  .crumbs span.sep { color: var(--faint); padding: 0 .15rem; }
  .crumbs .here { color: var(--ink); font-weight: 600; }
  button.act { background: var(--paper); border: 1px solid var(--line); border-radius: 8px; padding: .35rem .7rem;
               font: inherit; font-size: .82rem; cursor: pointer; color: var(--ink); }
  button.act:hover:not([disabled]) { border-color: #cdd3dc; background: #fbfbfc; }
  button.act[disabled] { opacity: .5; cursor: not-allowed; }
  button.act.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  button.act.primary:hover:not([disabled]) { background: #1349ad; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: .55rem .9rem; border-bottom: 1px solid var(--hair); font-size: .875rem; vertical-align: middle; }
  th { font-size: .7rem; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); background: #fbfbfc; }
  tr:last-child td { border-bottom: none; }
  td.size, td.when { color: var(--muted); font-size: .8rem; white-space: nowrap; }
  .icon { vertical-align: -3px; margin-right: .45rem; flex: none; }
  .icon.folder { color: var(--accent); }
  .icon.file { color: var(--faint); }
  td.name button.open { background: none; border: 0; padding: 0; font: inherit; color: var(--accent); cursor: pointer; text-align: left; word-break: break-all; }
  td.name .plain { word-break: break-all; }
  td.tools { text-align: right; white-space: nowrap; }
  td.tools button { background: none; border: 0; font: inherit; font-size: .8rem; color: var(--muted); cursor: pointer; padding: .1rem .3rem; }
  td.tools button:hover { color: var(--ink); text-decoration: underline; }
  td.tools button.danger:hover { color: var(--danger); }
  .tag { display: inline-block; padding: .05rem .4rem; border-radius: 999px; font-size: .68rem; font-weight: 600;
         letter-spacing: .02em; margin-left: .35rem; vertical-align: 1px; }
  .tag.ro { color: var(--warn); background: var(--warn-soft); }
  .tag.rw { color: var(--ok); background: var(--ok-soft); }
  .tag.link { color: #475467; background: #f2f4f7; }
  .empty { padding: 2.5rem 1.5rem; text-align: center; color: var(--muted); font-size: .9rem; }
  .empty strong { display: block; color: var(--ink); font-size: .95rem; margin-bottom: .35rem; }
  .empty code { background: #f2f4f7; padding: .1rem .35rem; border-radius: 4px; font-size: .82rem; }
  .drop { outline: 2px dashed var(--accent); outline-offset: -6px; background: var(--accent-soft); }
  dialog { border: 1px solid var(--line); border-radius: 12px; padding: 0; max-width: 26rem; width: calc(100% - 2rem);
           box-shadow: 0 12px 32px rgba(16,24,40,.16); }
  dialog::backdrop { background: rgba(16,24,40,.35); }
  dialog form { padding: 1.1rem 1.2rem; margin: 0; }
  dialog h3 { margin: 0 0 .35rem; font-size: 1rem; }
  dialog p { margin: 0 0 .8rem; color: var(--muted); font-size: .85rem; word-break: break-all; }
  dialog input[type=text] { width: 100%; padding: .45rem .6rem; border: 1px solid var(--line); border-radius: 8px;
                            font: inherit; font-size: .9rem; }
  dialog input[type=text]:focus { outline: 2px solid var(--accent); outline-offset: -1px; border-color: var(--accent); }
  dialog .row { display: flex; justify-content: flex-end; gap: .5rem; margin-top: 1rem; }
  dialog label.check { display: flex; gap: .5rem; align-items: flex-start; font-size: .85rem; color: var(--ink); margin-top: .6rem; }
  #toasts { position: fixed; right: 1rem; bottom: 1rem; display: flex; flex-direction: column; gap: .5rem; z-index: 10; }
  .toast { background: var(--paper); border: 1px solid var(--line); border-left: 3px solid var(--ok);
           border-radius: 8px; padding: .55rem .8rem; font-size: .84rem; box-shadow: 0 6px 18px rgba(16,24,40,.12);
           max-width: 26rem; word-break: break-word; }
  .toast.bad { border-left-color: var(--danger); }
  .busy { color: var(--muted); font-size: .8rem; }
  @media (max-width: 800px) { .layout { flex-direction: column; } aside { width: 100%; } }
</style>
</head>
<body>
<header class="top">
  <h1>{{.Title}}</h1>
  <span class="sub">Attached storage, browsed from inside the app.</span>
</header>

<div class="layout">
  <aside>
    <h2>Volumes</h2>
    <div id="volumes"></div>
    <p class="busy" id="mountnote"></p>
  </aside>
  <main>
    <div class="card" id="pane">
      <div class="bar">
        <div class="crumbs" id="crumbs"></div>
        <button class="act" id="refresh">Refresh</button>
        <button class="act" id="mkdir">New folder</button>
        <button class="act primary" id="upload">Upload</button>
      </div>
      <div id="body"></div>
    </div>
    <p class="busy" id="hint"></p>
  </main>
</div>

<input type="file" id="picker" multiple hidden>
<div id="toasts"></div>

<dialog id="ask">
  <form method="dialog">
    <h3 id="ask-title"></h3>
    <p id="ask-note"></p>
    <input type="text" id="ask-input" autocomplete="off" spellcheck="false">
    <div class="row">
      <button class="act" value="cancel">Cancel</button>
      <button class="act primary" id="ask-ok" value="ok">Save</button>
    </div>
  </form>
</dialog>

<dialog id="confirm">
  <form method="dialog">
    <h3 id="confirm-title"></h3>
    <p id="confirm-note"></p>
    <label class="check" id="confirm-check" hidden>
      <input type="checkbox" id="confirm-box">
      <span id="confirm-check-text"></span>
    </label>
    <div class="row">
      <button class="act" value="cancel">Cancel</button>
      <button class="act primary" id="confirm-ok" value="ok" style="background:#b42318;border-color:#b42318">Delete</button>
    </div>
  </form>
</dialog>

<script>
"use strict";
var state = { volumes: [], vol: null, path: "", writable: false, maxText: "" };

function el(tag, cls, text) {
  var n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined && text !== null) n.textContent = text;
  return n;
}
// Icons are drawn, not typed. An emoji here depends on a font the client may
// not have — and a file browser whose folders are empty boxes is the kind of
// broken that only shows up on someone else's machine.
function icon(kind) {
  var ns = "http://www.w3.org/2000/svg";
  var svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("width", "15");
  svg.setAttribute("height", "15");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", "icon " + kind);
  var p = document.createElementNS(ns, "path");
  p.setAttribute("d", kind === "folder"
    ? "M1.75 4.25c0-.55.45-1 1-1h3.1l1.3 1.6h6.1c.55 0 1 .45 1 1v6.4c0 .55-.45 1-1 1h-10.5c-.55 0-1-.45-1-1z"
    : "M3.75 1.75h5l3.5 3.5v9c0 .28-.22.5-.5.5h-8a.5.5 0 0 1-.5-.5v-12c0-.28.22-.5.5-.5zM8.75 1.75v3.5h3.5");
  p.setAttribute("fill", "none");
  p.setAttribute("stroke", "currentColor");
  p.setAttribute("stroke-width", "1.3");
  p.setAttribute("stroke-linejoin", "round");
  svg.appendChild(p);
  return svg;
}
function toast(message, bad) {
  var t = el("div", bad ? "toast bad" : "toast", message);
  document.getElementById("toasts").appendChild(t);
  setTimeout(function () { t.remove(); }, bad ? 9000 : 4000);
}
function bytes(n) {
  if (n < 1024) return n + " B";
  var u = ["KiB", "MiB", "GiB", "TiB", "PiB"], i = -1;
  do { n = n / 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(1) + " " + u[i];
}
function when(iso) {
  if (!iso || iso.startsWith("0001-01-01")) return "";
  var d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleString();
}
function api(path, opts) {
  return fetch(path, opts || {}).then(function (r) {
    return r.json().catch(function () { return {}; }).then(function (body) {
      if (!r.ok) throw new Error(body.error || ("request failed with " + r.status));
      return body;
    });
  });
}
function join(dir, name) { return dir ? dir + "/" + name : name; }

// --- dialogs. Native <dialog>, so there is nothing to style into existence and
// nothing to get wrong about focus. ---
function askFor(title, note, value, okLabel) {
  return new Promise(function (resolve) {
    var d = document.getElementById("ask");
    document.getElementById("ask-title").textContent = title;
    document.getElementById("ask-note").textContent = note || "";
    document.getElementById("ask-ok").textContent = okLabel || "Save";
    var input = document.getElementById("ask-input");
    input.value = value || "";
    d.onclose = function () { resolve(d.returnValue === "ok" ? input.value.trim() : null); };
    d.showModal();
    input.focus();
    input.select();
  });
}
function confirmFor(title, note, checkText) {
  return new Promise(function (resolve) {
    var d = document.getElementById("confirm");
    document.getElementById("confirm-title").textContent = title;
    document.getElementById("confirm-note").textContent = note || "";
    var wrap = document.getElementById("confirm-check");
    var box = document.getElementById("confirm-box");
    box.checked = false;
    wrap.hidden = !checkText;
    if (checkText) document.getElementById("confirm-check-text").textContent = checkText;
    d.onclose = function () { resolve(d.returnValue === "ok" ? { ok: true, checked: box.checked } : { ok: false }); };
    d.showModal();
  });
}

// --- rendering ---
function renderVolumes() {
  var box = document.getElementById("volumes");
  box.textContent = "";
  if (!state.volumes.length) {
    box.appendChild(el("p", "busy", "None attached."));
    return;
  }
  state.volumes.forEach(function (v) {
    var b = el("button", "vol");
    b.type = "button";
    if (!v.browsable) b.disabled = true;
    if (state.vol && v.name === state.vol.name) b.setAttribute("aria-current", "true");
    var name = el("div", "name", v.name);
    var tag = el("span", "tag " + (v.access === "read" ? "ro" : "rw"), v.access === "read" ? "read-only" : "read-write");
    if (v.browsable) name.appendChild(tag);
    b.appendChild(name);
    b.appendChild(el("div", "path", v.path || "(no path)"));
    if (v.problem) b.appendChild(el("div", "why", v.problem));
    b.onclick = function () { open(v, ""); };
    box.appendChild(b);
  });
}

function renderCrumbs() {
  var c = document.getElementById("crumbs");
  c.textContent = "";
  if (!state.vol) return;
  var root = el("button", null, state.vol.name);
  root.type = "button";
  root.onclick = function () { open(state.vol, ""); };
  if (!state.path) { c.appendChild(el("span", "here", state.vol.name)); }
  else { c.appendChild(root); }
  var parts = state.path ? state.path.split("/") : [];
  var acc = "";
  parts.forEach(function (part, i) {
    c.appendChild(el("span", "sep", "/"));
    acc = join(acc, part);
    if (i === parts.length - 1) { c.appendChild(el("span", "here", part)); return; }
    var here = acc;
    var b = el("button", null, part);
    b.type = "button";
    b.onclick = function () { open(state.vol, here); };
    c.appendChild(b);
  });
}

function renderEntries(entries) {
  var body = document.getElementById("body");
  body.textContent = "";
  if (!entries.length) {
    var e = el("div", "empty");
    e.appendChild(el("strong", null, "This folder is empty."));
    e.appendChild(document.createTextNode(state.writable ? "Drop files here, or use Upload." : "The volume is mounted read-only."));
    body.appendChild(e);
    return;
  }
  var table = el("table");
  var head = el("tr");
  ["Name", "Size", "Modified", ""].forEach(function (h) { head.appendChild(el("th", null, h)); });
  table.appendChild(head);

  entries.forEach(function (en) {
    var tr = el("tr");
    var name = el("td", "name");
    if (en.dir || (!en.other)) {
      var b = el("button", "open");
      b.type = "button";
      b.appendChild(icon(en.dir ? "folder" : "file"));
      b.appendChild(el("span", null, en.name));
      b.onclick = function () {
        if (en.dir) { open(state.vol, join(state.path, en.name)); }
        else { window.location.href = "api/file?vol=" + encodeURIComponent(state.vol.name) + "&path=" + encodeURIComponent(join(state.path, en.name)); }
      };
      name.appendChild(b);
    } else {
      var plain = el("span", "plain");
      plain.appendChild(icon("file"));
      plain.appendChild(el("span", null, en.name));
      name.appendChild(plain);
    }
    if (en.symlink) name.appendChild(el("span", "tag link", "link"));
    if (en.other) name.appendChild(el("span", "tag link", "special"));
    tr.appendChild(name);
    tr.appendChild(el("td", "size", en.dir ? "" : bytes(en.size)));
    tr.appendChild(el("td", "when", when(en.modified)));

    var tools = el("td", "tools");
    if (!en.dir && !en.other) {
      var dl = el("button", null, "Download");
      dl.type = "button";
      dl.onclick = function () {
        window.location.href = "api/file?vol=" + encodeURIComponent(state.vol.name) + "&path=" + encodeURIComponent(join(state.path, en.name));
      };
      tools.appendChild(dl);
    }
    if (state.writable) {
      var rn = el("button", null, "Rename");
      rn.type = "button";
      rn.onclick = function () { doRename(en); };
      tools.appendChild(rn);
      var rm = el("button", "danger", "Delete");
      rm.type = "button";
      rm.onclick = function () { doDelete(en); };
      tools.appendChild(rm);
    }
    tr.appendChild(tools);
    table.appendChild(tr);
  });
  body.appendChild(table);
}

function setBusy(text) { document.getElementById("hint").textContent = text || ""; }

function toolsEnabled() {
  document.getElementById("mkdir").disabled = !state.writable;
  document.getElementById("upload").disabled = !state.writable;
  document.getElementById("refresh").disabled = !state.vol;
}

// --- navigation ---
function open(v, path) {
  state.vol = v;
  state.path = path || "";
  renderVolumes();
  renderCrumbs();
  if (!v || !v.browsable) {
    state.writable = false;
    toolsEnabled();
    var body = document.getElementById("body");
    body.textContent = "";
    var e = el("div", "empty");
    e.appendChild(el("strong", null, "Not browsable"));
    e.appendChild(document.createTextNode(v ? v.problem : ""));
    body.appendChild(e);
    return;
  }
  history.replaceState(null, "", "#" + encodeURIComponent(v.name) + (state.path ? "/" + state.path : ""));
  setBusy("Reading...");
  api("api/list?vol=" + encodeURIComponent(v.name) + "&path=" + encodeURIComponent(state.path))
    .then(function (data) {
      state.writable = data.writable;
      toolsEnabled();
      renderEntries(data.entries || []);
      setBusy(state.writable ? "Drag files onto the list to upload. Limit " + state.maxText + " per file." : "This volume is mounted read-only, so nothing here can be changed.");
    })
    .catch(function (err) {
      setBusy("");
      toast(err.message, true);
      var body = document.getElementById("body");
      body.textContent = "";
      var e = el("div", "empty");
      e.appendChild(el("strong", null, "Could not read that folder"));
      e.appendChild(document.createTextNode(err.message));
      body.appendChild(e);
    });
}
function reload() { if (state.vol) open(state.vol, state.path); }

// --- operations ---
function doMkdir() {
  askFor("New folder", "Created in " + (state.path || "the volume root") + ".", "", "Create").then(function (name) {
    if (!name) return;
    api("api/mkdir", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ vol: state.vol.name, path: state.path, name: name })
    }).then(function () { toast("Created " + name + "."); reload(); })
      .catch(function (e) { toast(e.message, true); });
  });
}

function doRename(en) {
  askFor("Rename", "In " + (state.path || "the volume root") + ".", en.name, "Rename").then(function (name) {
    if (!name || name === en.name) return;
    api("api/rename", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ vol: state.vol.name, path: state.path, from: join(state.path, en.name), name: name })
    }).then(function () { toast("Renamed to " + name + "."); reload(); })
      .catch(function (e) { toast(e.message, true); });
  });
}

function doDelete(en) {
  var note = en.dir
    ? "Deleting the folder " + en.name + " on volume " + state.vol.name + ". This is the filesystem, not a trash can — there is nothing to undo it with."
    : "Deleting " + en.name + " on volume " + state.vol.name + ". There is no undo.";
  confirmFor("Delete " + (en.dir ? "folder" : "file"), note, en.dir ? "Also delete everything inside it" : null)
    .then(function (answer) {
      if (!answer.ok) return;
      api("api/delete", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ vol: state.vol.name, path: join(state.path, en.name), recursive: !!answer.checked })
      }).then(function () { toast("Deleted " + en.name + "."); reload(); })
        .catch(function (e) { toast(e.message, true); });
    });
}

function sendFiles(files) {
  if (!files || !files.length) return;
  if (!state.writable) { toast("This volume is mounted read-only.", true); return; }
  var form = new FormData();
  for (var i = 0; i < files.length; i++) form.append("file", files[i]);
  setBusy("Uploading " + files.length + " file" + (files.length === 1 ? "" : "s") + "...");
  api("api/upload?vol=" + encodeURIComponent(state.vol.name) + "&path=" + encodeURIComponent(state.path), { method: "POST", body: form })
    .then(function (data) {
      var saved = data.saved || [];
      var failed = saved.filter(function (s) { return s.error; });
      var ok = saved.length - failed.length;
      if (ok) toast(ok + " file" + (ok === 1 ? "" : "s") + " uploaded.");
      failed.forEach(function (f) { toast(f.name + ": " + f.error, true); });
      reload();
    })
    .catch(function (e) { setBusy(""); toast(e.message, true); });
}

// --- wiring ---
document.getElementById("refresh").onclick = reload;
document.getElementById("mkdir").onclick = doMkdir;
document.getElementById("upload").onclick = function () { document.getElementById("picker").click(); };
document.getElementById("picker").onchange = function (e) { sendFiles(e.target.files); e.target.value = ""; };

var pane = document.getElementById("pane");
["dragenter", "dragover"].forEach(function (t) {
  pane.addEventListener(t, function (e) { e.preventDefault(); if (state.writable) pane.classList.add("drop"); });
});
["dragleave", "drop"].forEach(function (t) {
  pane.addEventListener(t, function (e) { e.preventDefault(); pane.classList.remove("drop"); });
});
pane.addEventListener("drop", function (e) { sendFiles(e.dataTransfer.files); });

function fromHash() {
  var h = decodeURIComponent((location.hash || "").replace(/^#/, ""));
  if (!h) return null;
  var slash = h.indexOf("/");
  return slash < 0 ? { name: h, path: "" } : { name: h.slice(0, slash), path: h.slice(slash + 1) };
}

function boot() {
  api("api/volumes").then(function (data) {
    state.volumes = data.volumes || [];
    state.maxText = data.max_upload_text || "";
    renderVolumes();
    var note = document.getElementById("mountnote");
    if (data.read_only) note.textContent = "READ_ONLY is set, so every volume is shown read-only here.";
    var wanted = fromHash();
    var first = null;
    state.volumes.forEach(function (v) {
      if (wanted && v.name === wanted.name) first = v;
      if (!first && !wanted && v.browsable) first = v;
    });
    if (first) { open(first, wanted && first.name === wanted.name ? wanted.path : ""); return; }
    toolsEnabled();
    var body = document.getElementById("body");
    body.textContent = "";
    var e = el("div", "empty");
    if (!state.volumes.length) {
      e.appendChild(el("strong", null, "No storage is attached to this app."));
      e.appendChild(document.createTextNode("Attach an EFS volume to this app in Toft and redeploy it — a storage mapping takes effect on the next deployment. Isolated mode only."));
    } else {
      e.appendChild(el("strong", null, "Nothing here can be browsed."));
      e.appendChild(document.createTextNode("Every attached resource is either an object store or a volume that is not on the filesystem. The reason for each one is beside it."));
    }
    body.appendChild(e);
  }).catch(function (e) { toast(e.message, true); });
}
boot();
</script>
</body>
</html>
`))

func renderPage(w http.ResponseWriter, cfg config, vols []volume) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = page.Execute(w, map[string]any{
		"Title":   cfg.Title,
		"Volumes": vols,
	})
}
