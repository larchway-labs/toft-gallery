import Link from "next/link";
import Chart from "../components/chart";
import { RANGES, metrics, services, slice, summarize } from "../lib/metrics";

// Everything on this page is a server component except the chart, which needs a
// hover handler. The range and metric are in the URL rather than in React state,
// so a view is a link somebody can paste into a chat — and so the whole page
// renders on the server with no hydration cost for the part that never changes.
export default async function Dashboard({ searchParams }) {
  const params = await searchParams;
  const days = RANGES[params?.range] ? Number(params.range) : 30;
  const key = metrics[params?.metric] ? params.metric : "requests";

  const current = slice(key, days);
  const stats = summarize(current);

  const up = services.filter((s) => s.status === "healthy").length;

  return (
    <>
      <div className="section-hd">
        <div>
          <h2>Last {RANGES[days]}</h2>
          <p>
            Pick a metric to chart it. Swap <code>lib/metrics.js</code> for a query and
            nothing else on this page changes — everything that renders it is a server
            component.
          </p>
        </div>
        <nav className="seg" aria-label="Range">
          {Object.entries(RANGES).map(([value, label]) => (
            <Link
              key={value}
              href={`/?metric=${key}&range=${value}`}
              aria-current={Number(value) === days ? "true" : undefined}
            >
              {label}
            </Link>
          ))}
        </nav>
      </div>

      <div className="tiles">
        {Object.keys(metrics).map((k) => {
          const s = slice(k, days);
          const sum = summarize(s);
          const on = k === key;
          // Lower is better for two of the four, so "good" is not the same as
          // "up". Getting this backwards is the classic dashboard lie.
          const good = k === "errors" || k === "p95" ? sum.changePct <= 0 : sum.changePct >= 0;
          return (
            <Link
              key={k}
              href={`/?metric=${k}&range=${days}`}
              className={on ? "tile on" : "tile"}
              aria-current={on ? "true" : undefined}
            >
              <div className="tile-k">{s.label}</div>
              <div className="tile-v">
                {fmt(k === "p95" ? sum.average : sum.total)}
                {s.unit && <small> {s.unit}</small>}
              </div>
              <div className={good ? "delta good" : "delta bad"}>
                {sum.changePct >= 0 ? "▲" : "▼"} {Math.abs(sum.changePct).toFixed(1)}%
                <span className="muted"> vs the first half</span>
              </div>
            </Link>
          );
        })}
      </div>

      <div className="section">
        <div className="card">
          <div className="card-hd">
            <h2>{current.label}</h2>
            <span className="muted small">
              peak {fmt(stats.peak)}{current.unit && ` ${current.unit}`} · mean{" "}
              {fmt(stats.average)}{current.unit && ` ${current.unit}`}
            </span>
          </div>
          <div className="card-bd">
            <Chart
              data={current.data}
              unit={current.unit}
              color={key === "errors" || key === "p95" ? "var(--lp-c5)" : "var(--lp-c1)"}
              label={`${current.data.length} days`}
            />
          </div>
          <div className="card-ft">
            One <code>&lt;path&gt;</code>, one hover index, and no chart library. A
            dependency that pulled 300&nbsp;kB of JavaScript into the bundle would be the
            largest thing in this example.
          </div>
        </div>
      </div>

      <div className="section">
        <div className="card">
          <div className="card-hd">
            <h2>Services</h2>
            <span className="muted small">{up} of {services.length} healthy</span>
          </div>
          <div className="card-bd" style={{ padding: 0 }}>
            <div className="tbl-wrap">
              <table className="tbl" data-sortable>
                <thead>
                  <tr>
                    <th data-sort="text">Service</th>
                    <th data-sort="text">Owner</th>
                    <th data-sort="text">Status</th>
                    <th data-sort="text">Release</th>
                  </tr>
                </thead>
                <tbody>
                  {services.map((s) => (
                    <tr key={s.name}>
                      <td className="lead mono">{s.name}</td>
                      <td>{s.owner}</td>
                      <td>
                        <span className={`badge ${tone(s.status)}`}>
                          <i className="dot" />
                          {s.status}
                        </span>
                      </td>
                      <td className="mono small muted">{s.release}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>

      <div className="section">
        <div className="cards">
          <div className="card">
            <div className="card-hd"><h2>What a Next.js app has to know</h2></div>
            <div className="card-bd">
              <div className="note tone-brand">
                <strong><code>basePath</code> is injected, not configured.</strong> Toft
                moves an existing <code>next.config.*</code> aside and writes a generated
                wrapper that merges <code>basePath</code> over your export — before the
                build, because that is when Next bakes the prefix into every asset URL.
                Nothing here parses your JavaScript.
              </div>
              <div className="note tone-att">
                <strong>Do not set <code>output</code>.</strong> <code>next start</code>
                ignores a <code>standalone</code> build, and <code>output: &apos;export&apos;</code>
                cannot build route handlers at all. The wrapper warns in the build log and
                leaves the value alone — silently rewriting your config is worse than a
                build that fails for a stated reason.
              </div>
              <dl className="kv">
                <dt>Detected by</dt><dd><code>next.config.*</code>, or <code>next</code> in <code>package.json</code></dd>
                <dt>Built with</dt><dd><code>npm ci</code> then <code>npm run build</code></dd>
                <dt>Started as</dt><dd><code>next start --port --hostname</code></dd>
                <dt>Proxy</dt><dd>forwards the <strong>full</strong> path, unlike Flask, Go and generic Node</dd>
              </dl>
            </div>
          </div>

          <div className="card">
            <div className="card-hd"><h2>Why the state is in the URL</h2></div>
            <div className="card-bd">
              <p className="small">
                Every control on this page is a <code>&lt;Link&gt;</code>. The consequences
                are worth more than the convenience of <code>useState</code>:
              </p>
              <ul className="stack-s small" style={{ marginTop: ".5rem" }}>
                <li>· a view is a link somebody can paste into a chat</li>
                <li>· the back button does what it looks like it does</li>
                <li>· the page renders on the server, so there is nothing to hydrate</li>
                <li>· the only client component in the app is the chart, and only because it needs a hover handler</li>
              </ul>
              <p className="small muted" style={{ marginTop: ".75rem" }}>
                Ninety days of data, generated deterministically. A demo with random
                numbers makes every screenshot and every bug report unreproducible.
              </p>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}

function tone(status) {
  if (status === "healthy") return "tone-ok";
  if (status === "degraded") return "tone-att";
  return "tone-dan";
}

function fmt(n) {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return `${Math.round(n)}`;
}
