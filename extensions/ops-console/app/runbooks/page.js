import Link from "next/link";
import { runbooks } from "../../lib/data";

export const metadata = { title: "Runbooks · Ops Console" };

export default function Runbooks() {
  return (
    <>
      <h1>Runbooks</h1>
      <p className="sub">
        Short and ordered. A runbook that needs a paragraph of context before
        step one is a design document wearing the wrong hat.
      </p>
      <div className="grid">
        {runbooks.map((r) => (
          <Link key={r.slug} href={`/runbooks/${r.slug}`} className="runbook">
            <div className="card">
              <div className="value" style={{ fontSize: "1rem" }}>
                {r.title}
              </div>
              <div className="note">{r.summary}</div>
              <div className="note">{r.steps.length} steps</div>
            </div>
          </Link>
        ))}
      </div>
    </>
  );
}
