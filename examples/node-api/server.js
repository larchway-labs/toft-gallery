// A task list with a REST API and the page that uses it — the generic Node path
// on Toft.
//
// "Generic Node" means any package.json with a `start` script: Toft runs
// `npm run start` and tells the app where it is mounted with BASE_PATH. The
// proxy strips that prefix before forwarding, so every route here is declared at
// the root, and BASE_PATH is used for exactly one thing — building URLs that
// leave the server.
//
// Four things this example is careful about, and every one of them is the
// difference between working locally and working on the platform:
//
//   * It binds HOST, not 0.0.0.0. In shared mode an app on 0.0.0.0 is reachable
//     without passing through the platform's authorization at all.
//   * It listens on PORT. A hardcoded port produces a green build and an app
//     nobody can reach.
//   * The page's fetch() URLs are relative to the document, not to "/".
//   * The three routes that change something are gated on the caller's role,
//     which the platform signs onto every forwarded request — so an `lp_` app
//     key minted carrying `viewer` can read this list and cannot edit it.

import express from "express";
import path from "node:path";
import { fileURLToPath } from "node:url";

import * as seam from "./platform-seam.js";

const HERE = path.dirname(fileURLToPath(import.meta.url));

const app = express();
const PORT = Number(process.env.PORT || 3000);
const HOST = process.env.HOST || "127.0.0.1";
const BASE_PATH = process.env.BASE_PATH || "";
const STARTED_AT = new Date();

app.use(express.json({ limit: "64kb" }));

// --- the data ---------------------------------------------------------------
// In memory, and honest about it: this is an example, and a task list that
// survives a restart is a database decision rather than a framework one. The
// Platform tab says so on screen instead of leaving it to be discovered.

let nextId = 1;
const tasks = [];
const activity = [];

const OWNERS = ["ana", "raj", "mira", "tom"];
const LANES = ["Deploys", "Docs", "On-call", "Security"];

function seed(title, done, owner, lane, days) {
  const at = new Date(Date.now() - days * 86_400_000);
  tasks.push({ id: nextId++, title, done, owner, lane, createdAt: at.toISOString() });
}

seed("Read the deploy log to the end", true, "ana", "Deploys", 9);
seed("Give the app an owner who is not you", false, "ana", "Security", 8);
seed("Document every environment variable, including the defaults", false, "raj", "Docs", 7);
seed("Point the health check at something that can actually fail", false, "raj", "Deploys", 6);
seed("Write down who is on call at the weekend", true, "mira", "On-call", 5);
seed("Rotate the deploy key nobody remembers minting", false, "mira", "Security", 4);
seed("Move the nightly report off the web process", false, "tom", "Deploys", 3);
seed("Decide what happens when the export does not land", false, "tom", "On-call", 2);
seed("Delete the runbook that describes the old cluster", true, "ana", "Docs", 1);

function note(what, who) {
  activity.unshift({ at: new Date().toISOString(), what, who });
  activity.length = Math.min(activity.length, 40);
}

// --- the gate ---------------------------------------------------------------
//
// One middleware, on the three routes that change something. The refusal names
// the role it saw and the one it wanted, because "forbidden" is the least
// useful sentence in software.

function needsEditor(req, res, next) {
  const role = seam.role(req.headers);
  if (seam.atLeast(role, "editor") || role === "system") {
    req.callerRole = role;
    return next();
  }
  return res.status(403).json({
    error: `this caller is '${role}', and changing the list needs editor or owner`,
    role,
    hint: role === "unknown"
      ? "No verifiable X-Toft-Role on this request. Running outside Toft, or the header was stripped."
      : "Ask the app's owner for access on its Sharing tab, or mint a key that carries a role.",
  });
}

// --- API --------------------------------------------------------------------

app.get("/api/tasks", (req, res) => {
  const { done, owner, lane } = req.query;
  let out = tasks;
  if (done === "true" || done === "false") out = out.filter((t) => t.done === (done === "true"));
  if (owner) out = out.filter((t) => t.owner === owner);
  if (lane) out = out.filter((t) => t.lane === lane);
  res.json({
    count: out.length,
    total: tasks.length,
    callerRole: seam.role(req.headers),
    mayEdit: seam.atLeast(seam.role(req.headers), "editor"),
    owners: OWNERS,
    lanes: LANES,
    tasks: out,
  });
});

app.post("/api/tasks", needsEditor, (req, res) => {
  const title = String(req.body?.title ?? "").trim();
  if (!title) return res.status(400).json({ error: "title is required" });
  if (title.length > 200) return res.status(400).json({ error: "title is longer than 200 characters" });

  const task = {
    id: nextId++,
    title,
    done: false,
    owner: String(req.body?.owner ?? "unassigned").slice(0, 40),
    lane: LANES.includes(req.body?.lane) ? req.body.lane : "Deploys",
    createdAt: new Date().toISOString(),
  };
  tasks.push(task);
  note(`added “${task.title}”`, req.callerRole);
  res.status(201).json(task);
});

app.patch("/api/tasks/:id", needsEditor, (req, res) => {
  const task = tasks.find((t) => t.id === Number(req.params.id));
  if (!task) return res.status(404).json({ error: "no such task" });
  if (typeof req.body?.done === "boolean") {
    task.done = req.body.done;
    note(`${task.done ? "closed" : "reopened"} “${task.title}”`, req.callerRole);
  }
  if (typeof req.body?.title === "string" && req.body.title.trim()) {
    task.title = req.body.title.trim().slice(0, 200);
  }
  res.json(task);
});

app.delete("/api/tasks/:id", needsEditor, (req, res) => {
  const i = tasks.findIndex((t) => t.id === Number(req.params.id));
  if (i < 0) return res.status(404).json({ error: "no such task" });
  const [removed] = tasks.splice(i, 1);
  note(`deleted “${removed.title}”`, req.callerRole);
  res.json(removed);
});

app.get("/api/activity", (_req, res) => res.json({ activity }));

app.get("/api/platform", async (req, res) => {
  const role = seam.role(req.headers);
  res.json({
    self: await seam.self(),
    role,
    roles: seam.ROLES,
    mayEdit: seam.atLeast(role, "editor"),
    rail: await seam.rail(role),
    env: seam.facts(),
    basePath: BASE_PATH,
    node: process.versions.node,
    startedAt: STARTED_AT.toISOString(),
    // The header names, so the page can say which of them arrived. The values
    // are deliberately not echoed: the role blob is a credential for 120
    // seconds, and a page that prints one has published it.
    platformHeaders: [
      "x-forwarded-prefix", "x-forwarded-proto", "x-forwarded-host",
      "x-toft-role", "x-toft-act-for", "x-toft-session",
      "x-toft-cron", "x-toft-scan",
    ].map((h) => ({ name: h, present: Boolean(req.headers[h]) })),
  });
});

app.get("/healthz", (_req, res) => {
  res.json({
    status: "ok",
    uptimeSeconds: Math.round((Date.now() - STARTED_AT.getTime()) / 1000),
    tasks: tasks.length,
  });
});

// --- the page ---------------------------------------------------------------

app.get("/", (_req, res) => res.sendFile(path.join(HERE, "public", "index.html")));
app.use(express.static(path.join(HERE, "public")));

app.use((req, res) => res.status(404).json({ error: `no route for ${req.method} ${req.path}` }));

app.listen(PORT, HOST, () => {
  console.log(`node-api listening on http://${HOST}:${PORT} (base path ${BASE_PATH || "/"})`);
});
