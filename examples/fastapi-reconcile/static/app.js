/* Reconcile — the page's own wiring.
 *
 * Everything shared with the rest of the gallery is in toft-kit.js. What is
 * here is only what this app does: decide an exception, start a close, save the
 * settings document, and render the OpenAPI document into the house style.
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

  /* ── running the close ───────────────────────────────────────────────── */

  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-close]");
    if (!btn) return;
    var period = btn.getAttribute("data-close");
    btn.disabled = true;
    var was = btn.textContent;
    btn.textContent = "Starting…";

    LP.json(api("/api/close"), { method: "POST", body: { period: period, background: true } })
      .then(function (r) {
        if (r.deduplicated) {
          LP.toast("That close is already running — this is the same run.", "att", 6000);
        } else {
          LP.toast("Queued as a job" + (r.attributed ? ", in your name." : ", as the app."), "ok");
        }
        poll(r.run_id, 0);
      })
      .catch(function (err) {
        // 409 means the install has no isolated backend, which is a real answer
        // rather than a failure: offer the thing that does work and say what it
        // costs.
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

  // A run ends in one of six states and never changes again. Polling until
  // only two of them is a page that spins for three minutes on a timeout.
  var ENDED = { succeeded: 1, failed: 1, timed_out: 1, out_of_memory: 1, cancelled: 1, skipped: 1 };

  // `started_by` is five words, not two: a close from the CLI is not the app.
  var BY = { viewer: "a person", app: "the app", schedule: "the schedule", cli: "the CLI", ui: "Toft's UI" };

  function poll(runId, tries) {
    if (!runId || tries > 90) return;
    setTimeout(function () {
      LP.json(api("/api/runs")).then(function (r) {
        var run = (r.runs || []).filter(function (x) { return x.id === runId; })[0];
        if (!run) return poll(runId, tries + 1);
        paintRuns(r.runs);
        if (run.status === "succeeded") {
          LP.toast("Close finished. " + (run.summary || ""), "ok", 8000);
          setTimeout(reload, 1500);
        } else if (ENDED[run.status]) {
          LP.toast("The close " + run.status + ". " + (run.failure_reason || ""), "dan", 10000);
        } else {
          poll(runId, tries + 1);
        }
      }).catch(function () { poll(runId, tries + 1); });
    }, 2000);
  }

  function paintRuns(runs) {
    var body = document.getElementById("runs-body");
    if (!body || !runs) return;
    body.innerHTML = runs.map(function (r) {
      var badge = r.status === "succeeded" ? '<span class="badge tone-ok">succeeded</span>'
        : ENDED[r.status]
          ? '<span class="badge tone-dan">' + LP.esc(r.status) + "</span>"
          : r.status === "running"
            ? '<span class="badge tone-info">running ' + r.progress + "%</span>"
            : '<span class="badge">' + LP.esc(r.status) + "</span>";
      var result = (r.status === "succeeded" && r.summary) ? r.summary
        : (r.failure_reason || "—");
      return "<tr><td class='mono small'>" + LP.esc(r.id.slice(0, 12)) + "</td><td>" + badge +
        "</td><td>" +
        '<span class="badge' + (r.started_by === "viewer" ? " tone-brand" : "") + '">' +
        LP.esc(BY[r.started_by] || r.started_by || "—") + "</span>" +
        "</td><td class='small muted'>" + LP.esc(result) + "</td></tr>";
    }).join("");
  }

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
