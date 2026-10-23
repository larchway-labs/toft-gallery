/* Reconcile — the page's own wiring.
 *
 * Everything shared with the rest of the gallery is in toft-kit.js. What is
 * here is only what this app does: decide an exception, start and follow a
 * close, save the settings document, and render the OpenAPI document into the
 * house style.
 */

(function () {
  "use strict";

  // Where this app is mounted. The template writes it onto <html> because a
  // relative URL resolves differently depending on the trailing slash, and an
  // app that is right at /apps/reconcile/ and wrong at /apps/reconcile is worse
  // than one that is simply explicit.
  var BASE = document.documentElement.getAttribute("data-base") || "";
  var api = function (p) { return BASE + p; };

  /* ── deciding an exception ───────────────────────────────────────────── */

  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-decide]");
    if (!btn) return;
    var row = btn.closest("tr");
    var id = row.getAttribute("data-id");
    var status = btn.getAttribute("data-decide");
    var note = "";
    if (status === "written_off") {
      note = prompt("Why is this being written off?\n\nIt stays on the row. A write-off " +
                    "with no reason is a question somebody asks in six months.");
      if (note === null) return;
      if (!note.trim()) { LP.toast("A write-off needs a reason.", "att"); return; }
    }
    btn.disabled = true;
    LP.json(api("/api/exceptions/" + id + "/decide"), {
      method: "POST", body: { status: status, note: note },
    }).then(function () {
      LP.toast(status === "accepted" ? "Accepted." : "Written off.", "ok");
      // Repaint the one cell rather than reloading: the sort and the filter the
      // person set are theirs, and a reload would throw both away.
      var cell = row.cells[6];
      cell.innerHTML = status === "accepted"
        ? '<span class="badge tone-ok">accepted</span>'
        : '<span class="badge">written off</span>';
      if (note) cell.innerHTML += '<div class="tiny muted">' + LP.esc(note) + "</div>";
      row.style.opacity = ".6";
    }).catch(function (err) {
      LP.toast(err.message, "dan", 8000);
    }).finally(function () { btn.disabled = false; });
  });

  /* ── the kind filter ─────────────────────────────────────────────────── */

  var seg = document.querySelector("[data-kind]");
  if (seg) {
    seg.closest(".seg").addEventListener("click", function (e) {
      var b = e.target.closest("[data-kind]");
      if (!b) return;
      var kind = b.getAttribute("data-kind");
      this.querySelectorAll("button").forEach(function (o) { o.classList.toggle("is-on", o === b); });
      var shown = 0;
      document.querySelectorAll("#exceptions tbody tr").forEach(function (r) {
        var hit = !kind || r.getAttribute("data-kind") === kind;
        r.hidden = !hit;
        if (hit) shown++;
      });
      var n = document.getElementById("shown");
      if (n) n.textContent = shown;
      var q = document.getElementById("q");
      if (q) q.value = "";
    });
  }

  /* ── the runs ────────────────────────────────────────────────────────────
     The tab is drawn from one document — /api/runs, embedded in the page for
     the first paint — and redrawn from it every few seconds while any close in
     it is still open. A card is kept across repaints, so the one somebody
     opened stays open with its log still scrolling. */

  // A run ends in exactly one of six states and never changes again.
  var ENDED = { succeeded: 1, failed: 1, timed_out: 1, out_of_memory: 1, cancelled: 1, skipped: 1 };
  var TONE = {
    succeeded: "ok", failed: "dan", timed_out: "dan", out_of_memory: "dan",
    cancelled: "neu", skipped: "att", running: "info", queued: "neu",
  };
  var WORD = {
    succeeded: "succeeded", failed: "failed", timed_out: "timed out",
    out_of_memory: "out of memory", cancelled: "cancelled", skipped: "skipped",
    running: "running", queued: "queued",
  };
  // `started_by` is five words, not two: a close from the CLI is not the app.
  var BY = { viewer: "a person", app: "the app", schedule: "the schedule", cli: "the CLI", ui: "Toft's UI" };

  var docEl = document.getElementById("runs-doc");
  var R = docEl ? JSON.parse(docEl.textContent) : null;
  var open = {};      // run id → true while its card is expanded
  var watching = {};  // runs this page started, to say something when they end
  var timer = null;

  function q(scope) { return "?scope=" + encodeURIComponent(scope || (R && R.scope) || "mine"); }
  function id8(id) { return String(id || "").slice(0, 8); }
  function ordinal(n) {
    var s = ["th", "st", "nd", "rd"], v = n % 100;
    return n + (s[(v - 20) % 10] || s[v] || s[0]);
  }
  function dur(a, b) {
    var t0 = a ? new Date(a) : null, t1 = b ? new Date(b) : new Date();
    if (!t0 || isNaN(t0)) return "";
    var s = Math.max(0, Math.round((t1 - t0) / 1000));
    if (s < 60) return s + "s";
    if (s < 3600) return Math.floor(s / 60) + "m " + (s % 60) + "s";
    return Math.floor(s / 3600) + "h " + Math.floor((s % 3600) / 60) + "m";
  }
  function when(t, zone) {
    if (!t) return "—";
    var d = new Date(t);
    if (isNaN(d)) return "—";
    try {
      return d.toLocaleString(undefined, {
        day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit",
        timeZone: zone || undefined, timeZoneName: zone ? "short" : undefined,
      });
    } catch (e) { return d.toLocaleString(); }
  }
  function until(t) {
    var s = Math.round((new Date(t) - Date.now()) / 1000);
    if (isNaN(s)) return "";
    if (s < 3600) return "in " + Math.max(1, Math.round(s / 60)) + " min";
    if (s < 86400 * 2) return "in " + Math.round(s / 3600) + " hours";
    return "in " + Math.round(s / 86400) + " days";
  }
  function forWhat(r) {
    if (r.params && r.params.period) return r.params.period;
    // A schedule names an instant, not a month. The worker turns it into one —
    // the month before, if the register holds it — and its log says which.
    if (r.scheduled_for) return "tick " + String(r.scheduled_for).slice(0, 10);
    return "settings period";
  }

  /* the four tiles: when it fires, whether it is healthy, what it may use */
  function paintTiles() {
    var el = document.getElementById("runs-tiles");
    if (!el || !R) return;
    var d = R.declaration || {}, runs = R.runs || [];
    var ended = runs.filter(function (r) { return ENDED[r.status]; });
    var good = ended.filter(function (r) { return r.status === "succeeded"; }).length;
    var latest = ended[0];
    var stale = R.last_success && (Date.now() - new Date(R.last_success)) > 31 * 86400e3;

    var tiles = [
      d.next_at
        ? { tone: "info", k: "Next close", v: LP.esc(when(d.next_at, d.timezone).split(",")[0]),
            n: LP.esc(until(d.next_at) + " · " + d.schedule + " · " + d.timezone) }
        : { tone: "neu", k: "Next close", v: "On demand", n: "No schedule in toft.toml" },
      R.last_success
        ? { tone: stale ? "att" : "ok", k: "Last success", v: LP.esc(LP.ago(R.last_success)),
            n: stale ? "Longer than a month — the schedule may be paused" : "By anyone, as the app sees it" }
        : { tone: "neu", k: "Last success", v: "Never", n: "No close has succeeded as a job yet" },
      { tone: latest && TONE[latest.status] === "dan" ? "dan" : "neu",
        k: R.scope === "everyone" ? "Everyone's closes" : "Your closes",
        v: ended.length ? good + "<small> of " + ended.length + " succeeded</small>" : "—",
        n: latest ? "Latest " + LP.esc(WORD[latest.status]) + " " + LP.esc(LP.ago(latest.finished_at || latest.queued_at))
                  : "Nothing has finished yet" },
      { tone: "neu", k: "Limits", v: LP.esc(d.timeout || "—") + "<small> · " + LP.esc(d.memory_mb ? d.memory_mb + " MiB" : "—") + "</small>",
        n: "Overlap " + LP.esc(d.overlap || "queue") + " · notify on " + LP.esc(d.notify || "none") },
    ];
    el.innerHTML = tiles.map(function (t) {
      return '<div class="tile tone-' + t.tone + '"><div class="tile-k">' + t.k + '</div><div class="tile-v">' +
        t.v + '</div><div class="tile-n">' + t.n + "</div></div>";
    }).join("");
  }

  /* the strip: the last twenty, oldest first, one square each */
  function paintStrip() {
    var el = document.getElementById("run-strip");
    if (!el || !R) return;
    var runs = (R.runs || []).slice(0, 20).reverse();
    el.setAttribute("aria-label", runs.length + " most recent closes, oldest first");
    el.innerHTML = runs.map(function (r) {
      return '<button type="button" class="run-pip tone-' + TONE[r.status] + '" data-pip="' + LP.esc(r.id) +
        '" title="' + LP.esc(forWhat(r) + " · " + WORD[r.status] + " · " + LP.ago(r.queued_at)) + '"></button>';
    }).join("");
  }

  function head(r) {
    var tone = TONE[r.status] || "neu";
    var live = !ENDED[r.status];
    var line = r.status === "queued"
      ? (r.queue_position ? ordinal(r.queue_position) + " in line" : "Waiting for a slot")
      : r.status === "succeeded" ? (r.summary || "Closed.")
      : r.status === "running" ? (r.summary || "Starting…")
      : (r.failure_reason || WORD[r.status]);
    var time = r.status === "queued" ? "waiting " + dur(r.queued_at)
      : dur(r.started_at, r.finished_at);
    return '<span class="run-dot tone-' + tone + '" aria-hidden="true"></span>' +
      '<span class="run-main">' +
        '<span class="run-title"><b class="mono">' + LP.esc(forWhat(r)) + "</b>" +
          '<span class="badge tone-' + tone + '">' + LP.esc(WORD[r.status] || r.status) +
          (r.status === "running" ? " " + r.progress + "%" : "") + "</span>" +
          '<span class="badge' + (r.started_by === "viewer" ? " tone-brand" : "") + '">' +
          LP.esc(BY[r.started_by] || r.started_by || "—") + "</span>" +
          (r.rerun_of ? '<span class="badge tone-info">re-run of ' + LP.esc(id8(r.rerun_of)) + "</span>" : "") +
        "</span>" +
        '<span class="run-sub">' + LP.esc(line) + "</span>" +
        (live ? '<span class="bar-track run-bar"><span class="bar-fill' + (r.status === "queued" ? " tone-att" : "") +
          '" style="width:' + (r.status === "queued" ? 4 : Math.max(4, r.progress || 0)) + '%"></span></span>' : "") +
      "</span>" +
      '<span class="run-when"><span>' + LP.esc(LP.ago(r.queued_at)) + "</span>" +
        '<span class="mono">' + LP.esc(time) + "</span></span>" +
      '<span class="run-chev" aria-hidden="true"></span>';
  }

  function card(r) {
    var el = document.createElement("article");
    el.className = "run";
    el.setAttribute("data-run", r.id);
    el.innerHTML = '<button type="button" class="run-hd" aria-expanded="false"></button>' +
      '<div class="run-bd" hidden></div>';
    return el;
  }

  function paintList() {
    var list = document.getElementById("runs-list");
    if (!list || !R) return;
    var title = document.getElementById("runs-title");
    if (title) title.textContent = R.scope === "everyone" ? "Everyone's closes" : "Your closes";
    var runs = R.runs || [];
    if (!runs.length) {
      list.innerHTML = '<div class="empty"><h3>No closes yet</h3><p>' +
        LP.esc(R.note || "Start one with “Run the close”, or wait for the schedule.") + "</p></div>";
      return;
    }
    var keep = {};
    Array.prototype.forEach.call(list.querySelectorAll("[data-run]"), function (el) {
      keep[el.getAttribute("data-run")] = el;
    });
    list.querySelectorAll(".empty").forEach(function (e) { e.remove(); });
    runs.forEach(function (r) {
      var el = keep[r.id] || card(r);
      delete keep[r.id];
      el.className = "run tone-" + (TONE[r.status] || "neu") + (open[r.id] ? " is-open" : "");
      el.querySelector(".run-hd").innerHTML = head(r);
      el.querySelector(".run-hd").setAttribute("aria-expanded", open[r.id] ? "true" : "false");
      el._run = r;
      list.appendChild(el);                       // in order: moving a node keeps it
      if (open[r.id]) paintDetail(el, r);
    });
    Object.keys(keep).forEach(function (k) { keep[k].remove(); });
  }

  /* one card, opened: the record, the output, the log and what can be done */
  function paintDetail(el, r) {
    var bd = el.querySelector(".run-bd");
    var d = (R && R.declaration) || {};
    if (!bd.firstChild) {
      bd.innerHTML =
        '<div class="run-grid">' +
          '<dl class="kv run-kv"></dl>' +
          '<div class="run-out"></div>' +
        "</div>" +
        '<div class="run-log-hd"><h4>Log</h4><span class="tiny muted run-log-note"></span></div>' +
        '<pre class="term run-log"><span class="dim">Reading the log…</span></pre>' +
        '<div class="btns run-actions"></div>';
      bd._seq = 0;
      bd._lines = [];
      loadDetail(el, r);
    }
    var mem = r.peak_memory_mb && d.memory_mb
      ? '<span class="bar run-mem"><span class="bar-track"><span class="bar-fill' +
          (r.status === "out_of_memory" || r.peak_memory_mb >= d.memory_mb ? " tone-dan"
            : r.peak_memory_mb / d.memory_mb > 0.85 ? " tone-att" : " tone-ok") + '" style="width:' +
          Math.min(100, (r.peak_memory_mb / d.memory_mb) * 100).toFixed(1) + '%"></span></span>' +
          '<span class="bar-v">' + r.peak_memory_mb + " / " + d.memory_mb + " MiB</span></span>"
      : "—";
    var rows = [
      ["Run", '<span class="mono" title="' + LP.esc(r.id) + '">' + LP.esc(id8(r.id)) + "</span>"],
      ["Month", r.params && r.params.period ? LP.esc(r.params.period)
        : '<span class="muted">Not named — the worker chose it, and its first log line says which</span>'],
      r.scheduled_for ? ["Scheduled for", LP.esc(when(r.scheduled_for, d.timezone))] : null,
      ["Queued", LP.esc(when(r.queued_at))],
      r.started_at ? ["Started", LP.esc(when(r.started_at))] : null,
      r.finished_at ? ["Finished", LP.esc(when(r.finished_at)) + ' <span class="muted small">after ' + LP.esc(dur(r.started_at, r.finished_at)) + "</span>"]
        : (r.deadline ? ["Stopped at", LP.esc(when(r.deadline)) + ' <span class="muted small">if still going</span>'] : null),
      ["Peak memory", mem],
      r.exit_code !== null && r.exit_code !== undefined ? ["Exit code", '<span class="mono">' + r.exit_code + "</span>"] : null,
    ].filter(Boolean);
    bd.querySelector(".run-kv").innerHTML = rows.map(function (x) {
      return "<dt>" + x[0] + "</dt><dd>" + x[1] + "</dd>";
    }).join("");

    var acts = [];
    if (R && R.may_edit) {
      if (!ENDED[r.status]) acts.push('<button class="btn btn-danger btn-sm" type="button" data-cancel>Cancel</button>');
      else acts.push('<button class="btn btn-sm" type="button" data-again>Run again</button>');
    }
    if (r.status_url) acts.push('<a class="btn btn-ghost btn-sm" href="' + LP.esc(r.status_url) + '" target="_blank" rel="noopener">Status page ↗</a>');
    acts.push('<span class="tiny muted">or <code>toft job rerun ' + LP.esc(id8(r.id)) +
      "</code> — a re-run keeps this run's month and its scheduled time</span>");
    bd.querySelector(".run-actions").innerHTML = acts.join("");
    if (!ENDED[r.status] || !bd._logDone) loadLog(el, r);
  }

  function loadDetail(el, r) {
    var out = el.querySelector(".run-out");
    if (r.status !== "succeeded") {
      out.innerHTML = r.failure_reason
        ? '<div class="note tone-dan"><strong>' + LP.esc(WORD[r.status]) + ".</strong> " + LP.esc(r.failure_reason) + "</div>"
        : '<p class="small muted">The six numbers appear here when the close succeeds.</p>';
      return;
    }
    out.innerHTML = '<p class="small muted">Reading the output…</p>';
    LP.json(api("/api/runs/" + encodeURIComponent(r.id) + q())).then(function (x) {
      var o = x.output;
      if (!o) {
        out.innerHTML = '<p class="small muted">' + LP.esc(x.output_note || "This run wrote no output.") + "</p>";
        return;
      }
      out.innerHTML = '<div class="tiles run-tiles">' + [
        ["Match rate", LP.pct(o.match_rate), LP.num(o.matched) + " of " + LP.num(o.ledger_rows), o.match_rate > 0.97 ? "ok" : "att"],
        ["Exceptions", LP.num(o.exceptions), LP.num(o.bank_rows) + " statement lines", o.exceptions ? "att" : "ok"],
        ["Value at risk", "£" + LP.num((o.value_at_risk || 0) / 100, 2), "Left open", "neu"],
      ].map(function (t) {
        return '<div class="tile tone-' + t[3] + '"><div class="tile-k">' + t[0] + '</div><div class="tile-v">' +
          LP.esc(t[1]) + '</div><div class="tile-n">' + LP.esc(t[2]) + "</div></div>";
      }).join("") + "</div>" +
      '<p class="tiny muted">The run\'s output: what <code>write_output_json</code> stored, read back through Toft.</p>';
    }).catch(function (err) {
      out.innerHTML = '<p class="small muted">' + LP.esc(err.message) + "</p>";
    });
  }

  function loadLog(el, r) {
    var bd = el.querySelector(".run-bd");
    if (bd._loading) return;
    bd._loading = true;
    LP.json(api("/api/runs/" + encodeURIComponent(r.id) + "/logs" + q() + "&after=" + (bd._seq || 0)))
      .then(function (x) {
        (x.lines || []).forEach(function (l) {
          bd._seq = Math.max(bd._seq, l.seq || 0);
          bd._lines.push(l);
        });
        var pre = bd.querySelector(".run-log");
        var stick = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 8;
        pre.innerHTML = bd._lines.length ? bd._lines.map(function (l) {
          var cls = l.stream === "stderr" ? "dan" : /\[100%\]|closed\./i.test(l.line) ? "ok" : "";
          return '<span class="dim">' + LP.esc(String(l.seq).padStart(3, " ")) + "  </span>" +
            (cls ? '<span class="' + cls + '">' : "<span>") + LP.esc(l.line) + "</span>";
        }).join("\n") : '<span class="dim">Nothing written yet.</span>';
        if (stick) pre.scrollTop = pre.scrollHeight;
        bd.querySelector(".run-log-note").textContent = bd._lines.length + " lines" +
          (ENDED[x.status] ? "" : " · following");
        if (ENDED[x.status]) bd._logDone = true;
      })
      .catch(function (err) {
        bd.querySelector(".run-log").innerHTML = '<span class="dim">' + LP.esc(err.message) + "</span>";
        bd._logDone = true;
      })
      .finally(function () { bd._loading = false; });
  }

  function paint() { paintTiles(); paintStrip(); paintList(); }

  function refresh() {
    clearTimeout(timer);
    return LP.json(api("/api/runs" + q())).then(function (doc) {
      var before = {};
      (R.runs || []).forEach(function (r) { before[r.id] = r.status; });
      R = doc;
      R.runs.forEach(function (r) {
        if (!watching[r.id] || !ENDED[r.status] || ENDED[before[r.id]]) return;
        delete watching[r.id];
        if (r.status === "succeeded") {
          LP.toast("Close finished. " + (r.summary || ""), "ok", 8000);
          var fresh = document.getElementById("runs-fresh");
          if (fresh) fresh.hidden = false;
        } else {
          LP.toast("The close " + WORD[r.status] + ". " + (r.failure_reason || ""), "dan", 10000);
        }
      });
      paint();
      schedule();
    }).catch(function () { schedule(8000); });
  }

  function schedule(ms) {
    clearTimeout(timer);
    var live = (R.runs || []).some(function (r) { return !ENDED[r.status]; });
    if (live || Object.keys(watching).length) timer = setTimeout(refresh, ms || 2500);
  }

  if (R) { paint(); schedule(); }

  document.addEventListener("click", function (e) {
    var hd = e.target.closest(".run-hd");
    var pip = e.target.closest("[data-pip]");
    if (pip) {
      var target = document.querySelector('[data-run="' + pip.getAttribute("data-pip") + '"]');
      if (target) {
        if (!open[target.getAttribute("data-run")]) target.querySelector(".run-hd").click();
        target.scrollIntoView({ behavior: "smooth", block: "center" });
      }
      return;
    }
    if (hd) {
      var el = hd.closest(".run"), id = el.getAttribute("data-run");
      open[id] = !open[id];
      el.classList.toggle("is-open", !!open[id]);
      hd.setAttribute("aria-expanded", open[id] ? "true" : "false");
      el.querySelector(".run-bd").hidden = !open[id];
      if (open[id]) paintDetail(el, el._run);
      return;
    }
    var scopeBtn = e.target.closest("[data-scope]");
    if (scopeBtn) {
      var scope = scopeBtn.getAttribute("data-scope");
      document.querySelectorAll("[data-scope]").forEach(function (b) { b.classList.toggle("is-on", b === scopeBtn); });
      LP.json(api("/api/runs" + q(scope))).then(function (doc) {
        R = doc; open = {};
        document.getElementById("runs-list").innerHTML = "";
        paint(); schedule();
      }).catch(function (err) { LP.toast(err.message, "dan", 8000); });
      return;
    }
    var run = e.target.closest(".run");
    if (!run) return;
    var rid = run.getAttribute("data-run");
    if (e.target.closest("[data-cancel]")) {
      var b = e.target.closest("[data-cancel]");
      b.disabled = true;
      LP.json(api("/api/runs/" + encodeURIComponent(rid) + "/cancel"), { method: "POST" })
        .then(function () { LP.toast("Cancelled. The run ends as cancelled, with its log kept.", "ok"); refresh(); })
        .catch(function (err) { LP.toast((err.body && err.body.reason) || err.message, "dan", 9000); b.disabled = false; });
    } else if (e.target.closest("[data-again]")) {
      var a = e.target.closest("[data-again]");
      a.disabled = true;
      LP.json(api("/api/runs/" + encodeURIComponent(rid) + "/again" + q()), { method: "POST" })
        .then(function (r) { started(r); })
        .catch(function (err) { LP.toast((err.body && err.body.reason) || err.message, "dan", 9000); })
        .finally(function () { a.disabled = false; });
    }
  });

  function started(r) {
    if (r.deduplicated) {
      LP.toast("That close is already running — this is the same run.", "att", 6000);
    } else {
      LP.toast("Queued as a job" + (r.attributed ? ", in your name" : ", as the app") +
        (r.queue_position ? " — " + ordinal(r.queue_position) + " in line." : "."), "ok");
    }
    watching[r.run_id] = true;
    open[r.run_id] = true;
    // Its card appears on the next read; the Runs tab is where to watch it.
    var tab = document.querySelector('[data-tab="runs"]');
    if (tab && document.querySelector('[data-panel="runs"]').hidden) tab.click();
    refresh();
  }

  /* starting a close, from any of the three buttons */

  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-close]");
    if (!btn) return;
    var pick = document.getElementById("close-period");
    var period = btn.getAttribute("data-close") || (pick && pick.value) || "";
    btn.disabled = true;
    var was = btn.textContent;
    btn.textContent = "Starting…";

    LP.json(api("/api/close"), { method: "POST", body: { period: period, background: true } })
      .then(started)
      .catch(function (err) {
        // 409 means this install has nowhere to run a job, which is a real
        // answer rather than a failure: offer the thing that does work and
        // say what it costs.
        if (err.status === 409) {
          if (!confirm(err.body.reason + "\n\nRun it in the web process instead? " +
                       "That is exactly what the job system exists to avoid.")) return;
          return LP.json(api("/api/close"), { method: "POST", body: { period: period, background: false } })
            .then(function (r) { LP.toast(r.summary, "ok", 8000); setTimeout(reload, 1200); });
        }
        LP.toast(err.message, "dan", 8000);
      })
      .finally(function () { btn.disabled = false; btn.textContent = was; });
  });

  document.addEventListener("click", function (e) {
    if (e.target.closest("[data-reload]")) reload();
  });

  function reload() {
    // Keep the tab the person is on.
    location.href = BASE + "/?period=" +
      encodeURIComponent(new URLSearchParams(location.search).get("period") || "") +
      (location.hash || "");
  }

  /* ── the settings document ───────────────────────────────────────────── */

  var form = document.getElementById("settings-form");
  if (form) {
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var badge = form.closest(".card").querySelector(".card-hd .badge");
      var version = parseInt((badge.textContent.match(/\d+/) || [0])[0], 10);
      var body = {
        tolerance_pence: parseInt(form.tolerance_pence.value, 10),
        date_window_days: parseInt(form.date_window_days.value, 10),
        period: form.period.value,
        announce: form.announce.checked,
      };
      LP.json(api("/api/settings"), {
        method: "PUT", body: body, headers: { "X-Settings-Version": String(version) },
      }).then(function (r) {
        badge.textContent = "version " + r.version;
        LP.toast("Saved as version " + r.version + ".", "ok");
      }).catch(function (err) {
        LP.toast(err.body && err.body.reason ? err.body.reason : err.message, "dan", 9000);
      });
    });
  }

  /* ── the summary ─────────────────────────────────────────────────────── */

  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-announce]");
    if (!btn) return;
    btn.disabled = true;
    LP.json(api("/api/announce"), { method: "POST", body: { period: btn.getAttribute("data-announce") } })
      .then(function (r) { LP.toast(r.message, "ok", 9000); })
      .catch(function (err) { LP.toast(err.message, "dan", 9000); })
      .finally(function () { btn.disabled = false; });
  });

  /* ── the OpenAPI document, in the house style ────────────────────────── */

  var apiList = document.getElementById("api-list");
  if (apiList) {
    LP.json(api("/openapi.json")).then(function (doc) {
      var groups = {};
      Object.keys(doc.paths || {}).forEach(function (p) {
        Object.keys(doc.paths[p]).forEach(function (m) {
          var op = doc.paths[p][m];
          var tag = (op.tags && op.tags[0]) || "Other";
          (groups[tag] = groups[tag] || []).push({ path: p, method: m.toUpperCase(), op: op });
        });
      });
      var n = 0;
      var html = Object.keys(groups).sort().map(function (tag) {
        return "<h4 style='margin-top:1.25rem'>" + LP.esc(tag) + "</h4>" +
          "<div class='tbl-wrap'><table class='tbl'><tbody>" +
          groups[tag].map(function (r) {
            n++;
            var tone = r.method === "GET" ? "tone-info" : "tone-brand";
            // The docstring's first line, with markdown emphasis honoured —
            // escaped first, so the ** is the only thing that becomes markup.
            var doc1 = LP.esc((r.op.description || r.op.summary || "").split("\n")[0])
              .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
              .replace(/`([^`]+)`/g, "<code>$1</code>");
            return "<tr><td style='width:5.5rem'><span class='badge " + tone + "'>" + r.method +
              "</span></td><td class='mono small' style='width:20rem'>" + LP.esc(r.path) +
              "</td><td class='small muted'>" + doc1 + "</td></tr>";
          }).join("") + "</tbody></table></div>";
      }).join("");
      apiList.innerHTML = html || "<p class='muted'>No routes.</p>";
      document.getElementById("api-count").textContent = n + " operations";
    }).catch(function (err) {
      apiList.innerHTML = "<div class='note tone-dan'>Could not read the OpenAPI document: " +
        LP.esc(err.message) + "</div>";
    });
  }
})();
