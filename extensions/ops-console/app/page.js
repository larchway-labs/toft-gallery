import Link from "next/link";
import { checklist, runbooks, team } from "../lib/data";

// Rendered per request rather than at build time: the on-call names come from
// the environment, and an environment variable an administrator sets after the
// deploy has to be visible without another build.
export const dynamic = "force-dynamic";

export default function Overview() {
  const t = team();
  const byCadence = { daily: [], weekly: [], monthly: [] };
  for (const item of checklist) byCadence[item.cadence].push(item);

  return (
    <>
      <h1>{t.name} operations</h1>
      <p className="sub">
        Who is holding the pager, what gets checked and when, and the four
        runbooks worth having open before you need them.
      </p>

      <div className="grid">
        <div className="card">
          <div className="label">Primary on-call</div>
          <div className="value">{t.primary}</div>
          <div className="note">First responder for anything paging.</div>
        </div>
        <div className="card">
          <div className="label">Secondary</div>
          <div className="value">{t.secondary}</div>
          <div className="note">Takes the weekly work off the primary.</div>
        </div>
        <div className="card">
          <div className="label">Escalation</div>
          <div className="value">{t.escalation}</div>
          <div className="note">Where to shout when the primary is unreachable.</div>
        </div>
      </div>

      {["daily", "weekly", "monthly"].map((cadence) => (
        <section key={cadence}>
          <h2>{cadence}</h2>
          <ul className="checklist">
            {byCadence[cadence].map((item) => (
              <li key={item.title}>
                <span className={`pill ${cadence}`}>{cadence}</span>
                <span>
                  <strong>{item.title}</strong>
                  <br />
                  <span style={{ color: "var(--muted)", fontSize: "0.84rem" }}>{item.detail}</span>
                </span>
                <span className="owner">{item.owner === "primary" ? t.primary : t.secondary}</span>
              </li>
            ))}
          </ul>
        </section>
      ))}

      <h2>Runbooks</h2>
      <div className="grid">
        {runbooks.map((r) => (
          <Link key={r.slug} href={`/runbooks/${r.slug}`} className="runbook">
            <div className="card">
              <div className="value" style={{ fontSize: "1rem" }}>
                {r.title}
              </div>
              <div className="note">{r.summary}</div>
            </div>
          </Link>
        ))}
      </div>
    </>
  );
}
