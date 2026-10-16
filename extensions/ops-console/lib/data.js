// The console's content. Checklists and runbooks are code — edit this file,
// commit, and the gallery's update path carries it to every install running
// this extension. Only the things that differ per install (team name, who is
// on call) come from the environment.

export function team() {
  return {
    name: process.env.TEAM_NAME || "Platform",
    primary: process.env.ONCALL_PRIMARY || "unassigned",
    secondary: process.env.ONCALL_SECONDARY || "unassigned",
    escalation: process.env.ESCALATION_CHANNEL || "#platform-oncall",
  };
}

export const checklist = [
  {
    cadence: "daily",
    title: "Read the overnight failure events",
    detail: "Anything that failed while nobody was watching, before the queue fills with the day's work.",
    owner: "primary",
  },
  {
    cadence: "daily",
    title: "Check the deploy queue is draining",
    detail: "A build stuck in 'building' for more than an hour is a stranded worker, not a slow build.",
    owner: "primary",
  },
  {
    cadence: "weekly",
    title: "Review apps with auto-deploy off",
    detail: "Auto-deploy switched off during an incident and never switched back is how code drifts from main.",
    owner: "secondary",
  },
  {
    cadence: "weekly",
    title: "Reconcile the app owner list",
    detail: "Every app has an owner who still works here and still recognises the app.",
    owner: "secondary",
  },
  {
    cadence: "monthly",
    title: "Rotate the credentials that are due",
    detail: "Signing keys, SMTP passwords, bot tokens. Rotation that is never rehearsed is rotation that fails.",
    owner: "primary",
  },
  {
    cadence: "monthly",
    title: "Prune releases and check disk headroom",
    detail: "Version retention is a policy, not a cleanup script somebody remembers to run.",
    owner: "secondary",
  },
];

export const runbooks = [
  {
    slug: "app-will-not-start",
    title: "An app builds but will not start",
    summary: "The build is green, the process is not there. Where to look, in order.",
    steps: [
      "Read the app's own log first. A process that exits immediately almost always says why on its last line.",
      "Check the app binds the port it was given. Toft passes PORT and HOST; an app with a hardcoded port starts and is never reachable.",
      "Check the bind address. In shared mode an app must listen on 127.0.0.1 — a process on 0.0.0.0 is a finding, not a fix.",
      "Look for a missing environment variable. An app that reads config at import time fails before it can log anything useful.",
      "Redeploy the previous release. Rolling back is the cheapest way to separate 'this code' from 'this platform'.",
    ],
  },
  {
    slug: "deploy-stuck-building",
    title: "A deploy is stuck in building",
    summary: "What a stranded build looks like and what is safe to do about it.",
    steps: [
      "Confirm the build is stranded rather than slow: a first Node build on a cold cache is minutes, not seconds.",
      "Read the build log to the end. A clone that is waiting on credentials produces no output at all.",
      "Check the queue depth. One long build blocking the queue is capacity, not a fault.",
      "Cancel and redeploy. A cancelled build leaves the previous release serving — nothing is taken down by trying again.",
      "If several builds strand at once, suspect the box: disk, memory, or a package registry that is refusing connections.",
    ],
  },
  {
    slug: "rotate-a-credential",
    title: "Rotate a credential without an outage",
    summary: "The order that keeps both the old and the new key valid for the overlap.",
    steps: [
      "Issue the new credential first and confirm it works somewhere harmless.",
      "Add it alongside the old one where the consumer accepts two. Never replace in one step what you cannot roll back in one step.",
      "Redeploy the consumers and watch a real request succeed — not a health check, which frequently does not use the credential at all.",
      "Revoke the old credential only after the overlap window, and write down the date it was revoked.",
      "Search the logs for the old credential's identifier. Something always still holds it.",
    ],
  },
  {
    slug: "onboard-an-app",
    title: "Onboard a new internal app",
    summary: "What has to be true before an app is somebody's dependency.",
    steps: [
      "It has a named owner who is not the person who deployed it.",
      "Its environment variables are documented in its README, including the ones with defaults.",
      "It has a health endpoint that touches the things it actually depends on.",
      "Its failure notifications reach a channel somebody reads, not an inbox rule.",
      "Somebody other than the author has deployed it once, from scratch, following only the README.",
    ],
  },
];

export function runbook(slug) {
  return runbooks.find((r) => r.slug === slug);
}
