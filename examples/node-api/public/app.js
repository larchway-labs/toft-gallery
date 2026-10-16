/* Task list — the page's own wiring.
 *
 * Everything shared with the rest of the gallery is in toft-kit.js. What is
 * here is only what this app does.
 */

(function () {
  "use strict";

  // The app is served under /apps/{slug}. A URL written as "/api/tasks" would
  // leave that prefix and hit the platform instead, so every request is built
  // relative to the document — which is what `new URL(path, base)` does when the
  // base ends in a slash. This is the browser half of the same rule the server
  // half states with BASE_PATH.
  var base = new URL(".", window.location.href);
  var api = function (p) { return new URL(String(p).replace(/^\//, ""), base).toString(); };

  var state = { tasks: [], filter: "all", q: "", mayEdit: false, role: "unknown" };

  /* ── the board ───────────────────────────────────────────────────────── */

  function load() {
    return LP.json(api("api/tasks")).then(function (d) {
      state.tasks = d.tasks;
      state.mayEdit = d.mayEdit;
      state.role = d.callerRole;
      fillSelect("owner", d.owners);
      fillSelect("lane", d.lanes);
      paintTiles(d);
      paintGate();
      paintRows();
    }).catch(function (err) { LP.toast("Could not load: " + err.message, "dan", 8000); });
  }

  function fillSelect(id, values) {
    var el = document.getElementById(id);
    if (!el || el.options.length) return;
    values.forEach(function (v) {
      var o = document.createElement("option");
      o.value = v; o.textContent = v;
      el.appendChild(o);
    });
  }

  function paintTiles(d) {
    var open = state.tasks.filter(function (t) { return !t.done; }).length;
    var byLane = {};
    state.tasks.forEach(function (t) { byLane[t.lane] = (byLane[t.lane] || 0) + 1; });
    var busiest = Object.keys(byLane).sort(function (a, b) { return byLane[b] - byLane[a]; })[0] || "—";
    var pct = state.tasks.length ? (state.tasks.length - open) / state.tasks.length : 0;

    document.getElementById("tiles").innerHTML = [
      tile("Open", open, open + " of " + state.tasks.length + " still to do",
           open === 0 ? "tone-ok" : "tone-att"),
      tile("Done", state.tasks.length - open, LP.pct(pct, 0) + " of the list", "tone-ok"),
      tile("Busiest lane", busiest, (byLane[busiest] || 0) + " tasks", "tone-info"),
      tile("Your role", d.callerRole,
           d.mayEdit ? "may add, close and delete" : "read-only",
           d.mayEdit ? "" : "tone-neu"),
    ].join("");
  }

  function tile(k, v, note, tone) {
    return '<div class="tile ' + (tone || "") + '"><div class="tile-k">' + LP.esc(k) +
      '</div><div class="tile-v">' + LP.esc(v) + '</div><div class="tile-n">' +
      LP.esc(note) + "</div></div>";
  }

  function paintGate() {
    var el = document.getElementById("gate-note");
    if (state.mayEdit) { el.innerHTML = ""; return; }
    // The controls stay visible and refuse with the platform's own sentence.
    // An action you cannot see is an action you cannot ask about.
    el.innerHTML =
      '<div class="note tone-att"><strong>Read-only.</strong> Toft signed this ' +
      'request as <code>' + LP.esc(state.role) + '</code>, and changing the list needs ' +
      '<code>editor</code> or <code>owner</code>. The controls below still work — they ' +
      'refuse with the API\'s own sentence rather than disappearing.</div>';
  }

  function paintRows() {
    var q = state.q.toLowerCase();
    var rows = state.tasks.filter(function (t) {
      if (state.filter === "open" && t.done) return false;
      if (state.filter === "done" && !t.done) return false;
      return !q || (t.title + " " + t.owner + " " + t.lane).toLowerCase().indexOf(q) !== -1;
    });
    document.getElementById("shown").textContent = rows.length;
    document.getElementById("empty").hidden = rows.length !== 0;
    document.getElementById("rows").innerHTML = rows.map(function (t) {
      return '<tr data-id="' + t.id + '"' + (t.done ? ' style="opacity:.62"' : "") + ">" +
        '<td><input type="checkbox" data-toggle ' + (t.done ? "checked" : "") +
          ' aria-label="Done"></td>' +
        '<td class="lead"' + (t.done ? ' style="text-decoration:line-through"' : "") + ">" +
          LP.esc(t.title) + "</td>" +
        '<td><span class="badge">' + LP.esc(t.lane) + "</span></td>" +
        "<td>" + LP.esc(t.owner) + "</td>" +
        '<td class="mono small" data-v="' + t.createdAt + '"><span data-ago="' +
          t.createdAt + '"></span></td>' +
        '<td class="right"><button class="btn btn-sm btn-danger" data-del type="button">Delete</button></td>' +
        "</tr>";
    }).join("");
    LP.enhance(document.getElementById("rows"));
  }

  /* ── the three routes that change something ──────────────────────────── */

  function mutate(promise, ok) {
    return promise.then(function (r) { LP.toast(ok, "ok"); load(); return r; })
      .catch(function (err) {
        // The API's refusal names the role it saw and the one it wanted, so it
        // is shown verbatim rather than replaced with something vaguer.
        LP.toast(err.message, err.status === 403 ? "att" : "dan", 9000);
      });
  }

  document.getElementById("add").addEventListener("submit", function (e) {
    e.preventDefault();
    var title = document.getElementById("title");
    mutate(LP.json(api("api/tasks"), {
      method: "POST",
      body: {
        title: title.value,
        owner: document.getElementById("owner").value,
        lane: document.getElementById("lane").value,
      },
    }), "Added.").then(function () { title.value = ""; });
  });

  document.addEventListener("change", function (e) {
    var box = e.target.closest("[data-toggle]");
    if (!box) return;
    var id = box.closest("tr").getAttribute("data-id");
    mutate(LP.json(api("api/tasks/" + id), { method: "PATCH", body: { done: box.checked } }),
           box.checked ? "Closed." : "Reopened.");
  });

  document.addEventListener("click", function (e) {
    var del = e.target.closest("[data-del]");
    if (del) {
      var id = del.closest("tr").getAttribute("data-id");
      return mutate(LP.json(api("api/tasks/" + id), { method: "DELETE" }), "Deleted.");
    }
    var f = e.target.closest("[data-filter]");
    if (f) {
      state.filter = f.getAttribute("data-filter");
      f.parentNode.querySelectorAll("button").forEach(function (b) {
        b.classList.toggle("is-on", b === f);
      });
      paintRows();
    }
  });

  document.getElementById("q").addEventListener("input", function () {
    state.q = this.value.trim();
    paintRows();
  });

  /* ── activity ────────────────────────────────────────────────────────── */

  function loadActivity() {
    LP.json(api("api/activity")).then(function (d) {
      var body = document.getElementById("activity-rows");
      if (!d.activity.length) {
        body.innerHTML = '<tr><td colspan="3" class="muted">' +
          "Nothing yet. Add or close something on the Board.</td></tr>";
        return;
      }
      body.innerHTML = d.activity.map(function (a) {
        return "<tr><td class='mono small'><span data-ago='" + a.at + "'></span></td>" +
          "<td>" + LP.esc(a.what) + "</td>" +
          "<td><span class='badge tone-brand'>" + LP.esc(a.who) + "</span></td></tr>";
      }).join("");
      LP.enhance(body);
    });
  }

  /* ── platform ────────────────────────────────────────────────────────── */

  function loadPlatform() {
    LP.json(api("api/platform")).then(function (d) {
      document.getElementById("rail").innerHTML = d.rail.map(function (c) {
        return '<span class="cap ' + c.state + '" title="' + LP.esc(c.note) + '"><b>' +
          LP.esc(c.label) + "</b></span>";
      }).join("");

      document.getElementById("role-badge").textContent = d.role;
      document.getElementById("ladder").innerHTML = d.roles.map(function (w) {
        return '<span class="badge ' + (w === d.role ? "tone-brand" : "") + '">' + w + "</span>";
      }).join("");

      document.getElementById("hdrs").innerHTML = d.platformHeaders.map(function (h) {
        return '<span class="badge ' + (h.present ? "tone-ok" : "") + '">' +
          LP.esc(h.name) + (h.present ? "" : " · absent") + "</span>";
      }).join("");

      var kv = [];
      if (d.self.available) {
        kv.push(["Name", d.self.name], ["Slug", d.self.slug],
                ["Mode", '<span class="badge tone-brand">' + LP.esc(d.self.mode) + "</span>"],
                ["Base path", "<code>" + LP.esc(d.self.basePath || "/") + "</code>"],
                ["Capabilities", (d.self.capabilities || []).map(function (c) {
                  return '<span class="badge" style="margin:0 .25rem .25rem 0">' + LP.esc(c) + "</span>";
                }).join("") || '<span class="muted">none</span>']);
        var chip = document.getElementById("mode-chip");
        if (chip) chip.textContent = d.self.mode + " mode";
        document.getElementById("slug-1").textContent = d.self.slug;
      } else {
        kv.push(["Not a workload", '<span class="badge tone-att">' + LP.esc(d.self.reason) + "</span>"]);
      }
      kv.push(["Node", "<code>" + LP.esc(d.node) + "</code>"],
              ["Started", '<span data-ago="' + d.startedAt + '"></span>'],
              ["State", "in memory — a restart brings the seed list back"]);
      document.getElementById("self-kv").innerHTML = kv.map(function (r) {
        return "<dt>" + LP.esc(r[0]) + "</dt><dd>" + r[1] + "</dd>";
      }).join("");

      document.getElementById("env-rows").innerHTML = d.env.map(function (e) {
        return "<tr><td class='mono small nowrap'>" + LP.esc(e[0]) + "</td>" +
          "<td class='mono small'>" + LP.esc(e[1]) + "</td>" +
          "<td class='muted small'>" + LP.esc(e[2]) + "</td></tr>";
      }).join("");

      LP.enhance(document.getElementById("self-kv"));
    }).catch(function (err) {
      document.getElementById("rail").innerHTML =
        '<span class="cap off"><b>' + LP.esc(err.message) + "</b></span>";
    });
  }

  load();
  loadPlatform();
  loadActivity();
  // The activity feed is the only thing that changes without this page asking,
  // and only because another client might be talking to the same API.
  setInterval(loadActivity, 15000);
})();
