import Link from "next/link";
import { notFound } from "next/navigation";
import { runbook, runbooks } from "../../../lib/data";

// The set of runbooks is known at build time, so the pages are prerendered.
export function generateStaticParams() {
  return runbooks.map((r) => ({ slug: r.slug }));
}

export async function generateMetadata({ params }) {
  const { slug } = await params;
  const r = runbook(slug);
  return { title: r ? `${r.title} · Ops Console` : "Not found · Ops Console" };
}

export default async function RunbookPage({ params }) {
  const { slug } = await params;
  const r = runbook(slug);
  if (!r) notFound();

  return (
    <>
      <Link href="/runbooks" className="back">
        ← All runbooks
      </Link>
      <h1>{r.title}</h1>
      <p className="sub">{r.summary}</p>
      {r.steps.map((step, i) => (
        <div className="step" key={i}>
          <div className="n">STEP {i + 1}</div>
          {step}
        </div>
      ))}
    </>
  );
}
