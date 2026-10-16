/* Service Catalog — everything the page does, in one file.
 *
 * The one platform-specific line is the fetch below. This app is served from
 * /apps/{slug}/, so "data/services.json" resolves under that prefix and
 * "/data/services.json" would ask Toft itself and 404. Same rule for the
 * stylesheet, the script tag and the icon: relative, always.
 */
(function () {
  "use strict";

  var DATA_URL = "data/services.json";
  var STALE_DAYS = 180;

  var el = {
    plate: document.getElementById("plate"),
    controls: document.getElementById("controls"),
    q: document.getElementById("q"),
    tier: document.getElementById("tier"),
    team: document.getElementById("team"),
    sort: document.getElementById("sort"),
    count: document.getElementById("count"),
    list: document.getElementById("list"),
    empty: document.getElementById("empty"),
    detail: document.getElementById("detail")
  };


  var data = null;          // the parsed file
  var byId = {};            // id -> service
  var teamName = {};        // team id -> display name
  var dependents = {};      // id -> [ids that depend on it]
  var view = [];            // what the list is currently showing
  var selected = null;      // service id

  var state = { q: "", tier: "all", team: "all", sort: "tier" };

  /* ── load ─────────────────────────────────────────────────────────── */

  fetch(DATA_URL, { headers: { accept: "application/json" } })
    .then(function (r) {
      if (!r.ok) throw new Error(r.status + " " + r.statusText);
      return r.json();
    })
    .then(start)
    .catch(function (err) {
      el.detail.innerHTML = "";
      el.list.innerHTML = "";
      el.empty.hidden = false;
      el.empty.textContent =
        "The catalog data did not load (" + err.message + "). " +
        "It is served from " + DATA_URL + ", next to this page.";
    });

  function start(json) {
    data = json;

    data.teams.forEach(function (t) { teamName[t.id] = t.name; });
    data.services.forEach(function (s) {
      byId[s.id] = s;
      dependents[s.id] = dependents[s.id] || [];
    });
    data.services.forEach(function (s) {
      (s.depends_on || []).forEach(function (dep) {
        if (dependents[dep]) dependents[dep].push(s.id);
      });
    });

    fillPlate();
    fillTeams();
    wire();

    var fromUrl = location.hash.replace(/^#\/?/, "");
    render();
    select(byId[fromUrl] ? fromUrl : (view[0] && view[0].id) || null, false);
  }

  function fillPlate() {
    var t1 = data.services.filter(function (s) { return s.tier === 1; }).length;
    var set = {
      services: data.services.length,
      teams: data.teams.length,
      tier1: t1,
      revision: "r" + data.revision,
      reviewed: shortDate(data.reviewed)
    };
    Object.keys(set).forEach(function (k) {
      var node = el.plate.querySelector('[data-plate="' + k + '"]');
      if (node) node.textContent = set[k];
    });
  }

  function fillTeams() {
    data.teams.slice().sort(byName).forEach(function (t) {
      var o = document.createElement("option");
      o.value = t.id;
      o.textContent = t.name;
      el.team.appendChild(o);
    });
  }

  /* ── controls ─────────────────────────────────────────────────────── */

  function wire() {
    el.controls.addEventListener("submit", function (e) { e.preventDefault(); });

    el.q.addEventListener("input", function () {
      state.q = el.q.value.trim().toLowerCase();
      render();
    });

    el.tier.addEventListener("click", function (e) {
      var b = e.target.closest("button[data-tier]");
      if (!b) return;
      state.tier = b.dataset.tier;
      Array.prototype.forEach.call(el.tier.children, function (c) {
        c.setAttribute("aria-pressed", String(c === b));
      });
      render();
    });

    el.team.addEventListener("change", function () { state.team = el.team.value; render(); });
    el.sort.addEventListener("change", function () { state.sort = el.sort.value; render(); });

    el.list.addEventListener("click", function (e) {
      var row = e.target.closest(".svc");
      if (row) select(row.dataset.id, true);
    });

    document.addEventListener("keydown", function (e) {
      var typing = /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName);

      if (e.key === "/" && !typing) { e.preventDefault(); el.q.focus(); el.q.select(); return; }

      if (e.key === "Escape") {
        if (state.q || state.tier !== "all" || state.team !== "all") { reset(); }
        else if (typing) { document.activeElement.blur(); }
        return;
      }

      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        if (typing && document.activeElement !== el.q) return;
        if (!view.length) return;
        e.preventDefault();
        var i = view.findIndex(function (s) { return s.id === selected; });
        var next = e.key === "ArrowDown" ? Math.min(view.length - 1, i + 1) : Math.max(0, i - 1);
        if (i === -1) next = 0;
        select(view[next].id, true);
      }
    });
  }

  function reset() {
    state.q = ""; state.tier = "all"; state.team = "all";
    el.q.value = ""; el.team.value = "all";
    Array.prototype.forEach.call(el.tier.children, function (c) {
      c.setAttribute("aria-pressed", String(c.dataset.tier === "all"));
    });
    render();
  }

  /* ── list ─────────────────────────────────────────────────────────── */

  function matches(s) {
    if (state.tier !== "all" && String(s.tier) !== state.tier) return false;
    if (state.team !== "all" && s.team !== state.team) return false;
    if (!state.q) return true;
    var hay = [s.name, s.summary, s.language, s.rotation, teamName[s.team]]
      .concat(s.tags || []).join(" ").toLowerCase();
    return state.q.split(/\s+/).every(function (word) { return hay.indexOf(word) !== -1; });
  }

  function render() {
    view = data.services.filter(matches).sort(sorter(state.sort));

    el.list.innerHTML = "";
    view.forEach(function (s) { el.list.appendChild(row(s)); });

    var n = view.length;
    var total = data.services.length;
    el.count.textContent = n === total
      ? total + " services"
      : n + " of " + total + " services";

    el.empty.hidden = n > 0;
    if (!n) {
      el.empty.textContent = state.q
        ? "Nothing matches “" + el.q.value.trim() + "”. Try a team name, a language, or a tag like inventory."
        : "No service is in this tier for this team.";
    }

    if (selected) markCurrent();
  }

  function sorter(mode) {
    if (mode === "name") return byName;
    if (mode === "dependents") {
      return function (a, b) {
        return dependents[b.id].length - dependents[a.id].length || byName(a, b);
      };
    }
    if (mode === "review") {
      return function (a, b) { return a.last_review < b.last_review ? -1 : a.last_review > b.last_review ? 1 : byName(a, b); };
    }
    return function (a, b) { return a.tier - b.tier || byName(a, b); };
  }

  function byName(a, b) { return a.name < b.name ? -1 : a.name > b.name ? 1 : 0; }

  function row(s) {
    var li = document.createElement("li");
    var b = document.createElement("button");
    b.type = "button";
    b.className = "svc";
    b.dataset.id = s.id;
    b.dataset.tier = s.tier;

    var fan = dependents[s.id].length;
    b.innerHTML =
      '<span class="svc-tier">T' + s.tier + '</span>' +
      '<span class="svc-name">' + mark(s.name) + '</span>' +
      '<span class="svc-summary">' + mark(s.summary) + '</span>' +
      '<span class="svc-meta">' +
        '<span class="svc-team">' + esc(teamName[s.team]) + '</span>' +
        '<span>' + esc(s.language) + '</span>' +
        '<span class="svc-fan" title="Services that depend on this one">' +
          (fan ? fan + (fan === 1 ? " caller" : " callers") : "no callers") +
        '</span>' +
      '</span>';

    li.appendChild(b);
    return li;
  }

  function markCurrent() {
    Array.prototype.forEach.call(el.list.querySelectorAll(".svc"), function (r) {
      if (r.dataset.id === selected) r.setAttribute("aria-current", "true");
      else r.removeAttribute("aria-current");
    });
  }

  /* ── detail ───────────────────────────────────────────────────────── */

  function select(id, scroll) {
    selected = id;
    markCurrent();
    history.replaceState(null, "", id ? "#" + id : location.pathname);
    detail(id ? byId[id] : null);

    if (scroll) {
      var row = el.list.querySelector('.svc[aria-current="true"]');
      if (row) row.scrollIntoView({ block: "nearest" });
    }
  }

  function detail(s) {
    if (!s) {
      el.detail.removeAttribute("data-tier");
      el.detail.innerHTML = '<p class="detail-empty">Pick a service to see who owns it, what it calls, and what calls it.</p>';
      return;
    }

    el.detail.dataset.tier = s.tier;
    var team = data.teams.filter(function (t) { return t.id === s.team; })[0] || {};
    var age = daysBetween(s.last_review, data.reviewed);

    el.detail.innerHTML =
      '<div class="detail-in">' +
        '<div class="detail-hd">' +
          '<h2 class="detail-name">' + esc(s.name) + '</h2>' +
          '<span class="detail-tier">Tier ' + s.tier + '</span>' +
        '</div>' +
        '<p class="detail-summary">' + esc(s.summary) + '</p>' +
        '<section class="patch"><h3>Wiring</h3>' + patch(s) + '</section>' +
        '<section class="facts"><h3>Record</h3><dl>' +
          fact("Team", teamName[s.team] + (team.channel ? " · " + team.channel : "")) +
          fact("On call", s.rotation) +
          fact("Language", s.language) +
          fact("Availability", s.slo.availability + " · p95 " + s.slo.latency_p95_ms + " ms") +
          fact("Endpoint", s.endpoint) +
          fact("Repository", s.repo) +
          fact("Runbook", s.runbook) +
          fact("Last review", shortDate(s.last_review) + " · " + ago(age), age > STALE_DAYS) +
        '</dl></section>' +
        '<section class="tags"><h3>Tags</h3><div class="tag-list">' +
          (s.tags || []).map(function (t) {
            return '<button type="button" data-tag="' + esc(t) + '">' + esc(t) + '</button>';
          }).join("") +
        '</div></section>' +
      '</div>';

    el.detail.querySelectorAll(".patch .node").forEach(function (g) {
      g.addEventListener("click", function () { select(g.dataset.id, true); });
    });
    el.detail.querySelectorAll(".tag-list button").forEach(function (b) {
      b.addEventListener("click", function () {
        el.q.value = b.dataset.tag;
        state.q = b.dataset.tag.toLowerCase();
        render();
      });
    });
    // Draw the cables in: dash the whole length, then release it on the next
    // frame. Held in JS because the length is only knowable once it is in the
    // document, and skipped entirely when the reader asked for less motion.
    var still = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.detail.querySelectorAll(".patch .cable").forEach(function (p) {
      if (still) return;
      var len = Math.ceil(p.getTotalLength());
      p.style.strokeDasharray = len;
      p.style.strokeDashoffset = len;
      requestAnimationFrame(function () {
        p.style.transition = "stroke-dashoffset .45s ease";
        p.style.strokeDashoffset = 0;
      });
    });
  }

  function fact(k, v, stale) {
    return "<dt>" + esc(k) + "</dt><dd" + (stale ? ' class="stale"' : "") + ">" + esc(v) + "</dd>";
  }

  /* ── the wiring diagram ───────────────────────────────────────────── */
  /* Left lane: what this service calls. Right lane: what calls it.
     Both are read straight off depends_on — nothing is stored twice. */

  function patch(s) {
    var ins = (s.depends_on || []).slice().sort();
    var outs = dependents[s.id].slice().sort();

    var W = 320, STEP = 22, TOP = 30, PAD = 12;
    var rows = Math.max(ins.length, outs.length, 1);
    var H = TOP + rows * STEP + PAD;
    var hubY = TOP + (rows * STEP) / 2;

    var svg = '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" ' +
      'aria-label="' + esc(s.name + " calls " + (ins.length || "no") + " services and is called by " + (outs.length || "no") + " services") + '">';

    svg += '<text class="lane" x="0" y="10">Calls</text>';
    svg += '<text class="lane" x="' + W + '" y="10" text-anchor="end">Called by</text>';
    svg += '<text class="hub-label" x="160" y="11" text-anchor="middle">' + esc(s.name) + '</text>';

    svg += lane(ins, "in", TOP, STEP, hubY);
    svg += lane(outs, "out", TOP, STEP, hubY);

    svg += '<rect class="hub" x="154" y="' + (hubY - 14) + '" width="12" height="28" rx="2"/>';
    svg += "</svg>";
    return svg;
  }

  function lane(ids, dir, top, step, hubY) {
    var out = "";
    var portX = dir === "in" ? 104 : 216;
    var textX = dir === "in" ? 96 : 224;
    var anchor = dir === "in" ? "end" : "start";
    var hubX = dir === "in" ? 154 : 166;

    if (!ids.length) {
      return '<text class="none" x="' + textX + '" y="' + (hubY + 3) + '" text-anchor="' + anchor + '">nothing</text>';
    }

    ids.forEach(function (id, i) {
      var y = top + i * step + step / 2;
      var c1 = dir === "in" ? portX + 24 : portX - 24;
      var c2 = dir === "in" ? hubX - 24 : hubX + 24;
      out += '<path class="cable cable-' + dir + '" d="M' + portX + ',' + y +
             ' C' + c1 + ',' + y + ' ' + c2 + ',' + hubY + ' ' + hubX + ',' + hubY + '"/>';
      out += '<g class="node" data-id="' + esc(id) + '" tabindex="0" role="button">' +
               '<circle class="port port-' + dir + '" cx="' + portX + '" cy="' + y + '" r="3.5"/>' +
               '<text x="' + textX + '" y="' + (y + 3) + '" text-anchor="' + anchor + '">' +
                 esc(label(id)) +
               '</text>' +
             '</g>';
    });
    return out;
  }

  function label(id) {
    var s = byId[id];
    var name = s ? s.name : id;
    return name.length > 18 ? name.slice(0, 17) + "…" : name;
  }

  /* ── small helpers ────────────────────────────────────────────────── */

  function esc(v) {
    return String(v === undefined || v === null ? "" : v)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function mark(text) {
    var safe = esc(text);
    if (!state.q) return safe;
    state.q.split(/\s+/).filter(Boolean).forEach(function (word) {
      var re = new RegExp("(" + word.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + ")", "ig");
      safe = safe.replace(re, "<mark>$1</mark>");
    });
    return safe;
  }

  function shortDate(iso) {
    var p = String(iso).split("-");
    var months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    return p[2].replace(/^0/, "") + " " + months[Number(p[1]) - 1] + " " + p[0];
  }

  // "1 days ago" is the sort of thing a reader stops trusting a page over.
  function ago(days) {
    if (days <= 0) return "today";
    if (days === 1) return "yesterday";
    return days + " days ago";
  }

  function daysBetween(a, b) {
    return Math.round((Date.parse(b) - Date.parse(a)) / 86400000);
  }

  window.addEventListener("hashchange", function () {
    var id = location.hash.replace(/^#\/?/, "");
    if (byId[id] && id !== selected) select(id, true);
  });
})();
