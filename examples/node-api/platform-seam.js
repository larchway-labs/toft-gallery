// Everything this app says to Toft, and everything it asks it.
//
// One module, on purpose: the rest of the app is ordinary Express that would
// run anywhere, and the platform-facing part is somewhere a person can find it.
//
// The SDK is installed into every app's node_modules by the builder, so the
// import below needs nothing in package.json. Off-platform it is simply not
// there, which is why the import is dynamic and every function here has an
// answer for "no platform".

let toft = null;
let sdkError = "";

try {
  toft = await import("@larchway/toft");
} catch (err) {
  sdkError = "@larchway/toft is not installed — the platform's builder puts it in " +
    "node_modules, so this is a local run.";
}

export const ROLES = ["anonymous", "viewer", "editor", "owner", "system", "app"];
export const LADDER = { anonymous: 0, viewer: 1, editor: 2, owner: 3 };

export function onPlatform() {
  return Boolean(toft && process.env.TOFT_APP_TOKEN);
}

// --- who is asking ----------------------------------------------------------
//
// `caller` answers *what may this request do*: a word the platform signs per
// app and per workload start, verified in this process with no round trip and
// no administrator switch. It is what gates the three routes below that change
// something.
//
// It is the honest question an app can ask. Nothing here can tell you a human
// is in front of you — an app key may be minted carrying `viewer` or `editor`
// by an owner who chose to describe their machine client that way, and such a
// key satisfies the ladder exactly as a person with the same word does. That is
// the point of the word: the caller may do what it says.

export function role(headers) {
  if (!toft) return "unknown";
  try {
    return toft.caller(headers).role;
  } catch {
    return "unknown";
  }
}

export function atLeast(word, want) {
  const mine = LADDER[word];
  const target = LADDER[want];
  return mine !== undefined && target !== undefined && mine >= target;
}

// --- what this app is -------------------------------------------------------

let cached = null;
let cachedAt = 0;

export async function self() {
  if (cached && Date.now() - cachedAt < 30_000) return cached;
  if (!onPlatform()) {
    cached = { available: false, reason: sdkError || "No TOFT_APP_TOKEN in the environment." };
  } else {
    try {
      const a = await toft.app.refresh();
      cached = {
        available: true,
        id: a.id, slug: a.slug, name: a.name, mode: a.mode,
        basePath: a.basePath, capabilities: a.capabilities,
      };
    } catch (err) {
      cached = { available: false, reason: String(err.message || err) };
    }
  }
  cachedAt = Date.now();
  return cached;
}

// --- the environment, and what each variable is for -------------------------

export function facts() {
  const v = (n) => process.env[n] || "";
  return [
    ["PORT", v("PORT"), "The port to listen on. A hardcoded port is a green build and an app nobody can reach."],
    ["HOST", v("HOST") || "127.0.0.1 (this app's default)",
      "The address to bind. Loopback in shared mode — an app on 0.0.0.0 has stepped around the platform's authorization entirely."],
    ["BASE_PATH", v("BASE_PATH") || "(none)",
      "Where this app is mounted. The proxy strips it before forwarding, so every route here is declared at the root."],
    ["TOFT_APP_SLUG", v("TOFT_APP_SLUG") || "—", "This app's slug."],
    ["TOFT_EXECUTION_MODE", v("TOFT_EXECUTION_MODE") || "—",
      "shared or isolated. A property of the deployment, never a backend name."],
    ["TOFT_SCRATCH_DIR", v("TOFT_SCRATCH_DIR") || "—",
      "This app's own corner of the disk, wiped on every deploy and restart."],
    ["TOFT_ROLE_KEY", v("TOFT_ROLE_KEY") ? "set" : "—",
      "Per app, per workload start, derived and never stored. What the role header is verified against."],
    ["TOFT_APP_TOKEN", v("TOFT_APP_TOKEN") ? "set" : "—",
      "The app calling out. Minted per deployment, never shown to a person, and a deploy is how it rotates."],
    ["NODE_VERSION", process.versions.node, "Resolved from [runtime] node in toft.toml, or the platform's default."],
  ];
}

export async function rail(roleWord) {
  const info = await self();
  const cap = (label, state, note) => ({ label, state, note });
  return [
    cap("Generic Node", "on", "Any package.json with a start script. Toft runs `npm run start`."),
    cap("BASE_PATH", process.env.BASE_PATH ? "on" : "off",
      process.env.BASE_PATH || "Not mounted under a prefix here."),
    cap(`Role: ${roleWord}`, roleWord === "unknown" ? "off" : "on",
      "X-Toft-Role, verified in this process. It gates the three routes that change something."),
    cap("App keys", "on",
      "An lp_ key reaches these routes the same way a browser does — and carries a role of its own."),
    cap("Toft workload", info.available ? "on" : "off",
      info.available ? `${info.mode} mode` : info.reason),
  ];
}
