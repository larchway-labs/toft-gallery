// The dashboard's data. Generated deterministically so the example never
// changes shape between two people looking at it — a demo with random data
// makes every screenshot and every bug report unreproducible.
//
// Swap this file for a database query or a fetch and the rest of the app is
// unchanged: everything that renders it is a server component.

// A tiny seeded generator. Not cryptographic, not trying to be — it exists so
// the numbers look like numbers rather than a straight line.
function seeded(seed) {
  let state = seed;
  return () => {
    state = (state * 1664525 + 1013904223) % 4294967296;
    return state / 4294967296;
  };
}

const DAYS = 90;

function series({ seed, base, drift, jitter, floor = 0 }) {
  const rand = seeded(seed);
  const out = [];
  const today = new Date(Date.UTC(2026, 5, 30));
  for (let i = DAYS - 1; i >= 0; i--) {
    const day = new Date(today);
    day.setUTCDate(today.getUTCDate() - i);
    const weekend = day.getUTCDay() === 0 || day.getUTCDay() === 6;
    const value =
      base + drift * (DAYS - i) + (rand() - 0.5) * jitter - (weekend ? base * 0.35 : 0);
    out.push({ date: day.toISOString().slice(0, 10), value: Math.max(floor, Math.round(value)) });
  }
  return out;
}

export const metrics = {
  requests: {
    label: "Requests",
    unit: "",
    data: series({ seed: 7, base: 42000, drift: 260, jitter: 9000 }),
  },
  p95: {
    label: "p95 latency",
    unit: "ms",
    data: series({ seed: 19, base: 240, drift: -0.5, jitter: 70, floor: 40 }),
  },
  errors: {
    label: "Errors",
    unit: "",
    data: series({ seed: 33, base: 180, drift: -0.8, jitter: 150 }),
  },
  deploys: {
    label: "Deploys",
    unit: "",
    data: series({ seed: 51, base: 9, drift: 0.02, jitter: 8 }),
  },
};

export const RANGES = { 7: "7 days", 30: "30 days", 90: "90 days" };

export function slice(key, days) {
  const { data, ...rest } = metrics[key];
  return { ...rest, key, data: data.slice(-days) };
}

export function summarize(series) {
  const values = series.data.map((d) => d.value);
  const total = values.reduce((a, b) => a + b, 0);
  const half = Math.floor(values.length / 2);
  const first = values.slice(0, half).reduce((a, b) => a + b, 0) / (half || 1);
  const second = values.slice(half).reduce((a, b) => a + b, 0) / (values.length - half || 1);
  return {
    total,
    average: total / (values.length || 1),
    peak: Math.max(...values),
    // Change against the first half of the same window, so a 7-day view is
    // compared with the 7 days it contains rather than with a different range.
    changePct: first === 0 ? 0 : ((second - first) / first) * 100,
  };
}

export const services = [
  { name: "checkout-api", owner: "Payments", status: "healthy", release: "2026.6.28" },
  { name: "invoice-worker", owner: "Payments", status: "healthy", release: "2026.6.24" },
  { name: "search-indexer", owner: "Discovery", status: "degraded", release: "2026.6.29" },
  { name: "notify-relay", owner: "Platform", status: "healthy", release: "2026.6.12" },
  { name: "report-builder", owner: "Analytics", status: "paused", release: "2026.5.30" },
];
