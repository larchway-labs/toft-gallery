"use client";

// A line chart in about eighty lines of SVG, and no chart library.
//
// Deliberate: this example is about Next.js on Toft, and a dependency
// that pulls 300 kB of JavaScript into the bundle would be the largest thing
// in it. The shape below — a viewBox, a path, and one hover index — is enough
// for a dashboard, and it renders identically in every browser.

import { useState } from "react";

const W = 720;
const H = 240;
const PAD = { top: 16, right: 16, bottom: 26, left: 46 };

export default function Chart({ data, unit = "", color = "var(--lp-c1)", label = "" }) {
  const [hover, setHover] = useState(null);

  if (!data || data.length < 2) return null;

  const values = data.map((d) => d.value);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;

  const x = (i) => PAD.left + (i / (data.length - 1)) * (W - PAD.left - PAD.right);
  const y = (v) => PAD.top + (1 - (v - min) / span) * (H - PAD.top - PAD.bottom);

  const line = data.map((d, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(d.value).toFixed(1)}`).join(" ");
  const area = `${line} L${x(data.length - 1).toFixed(1)},${H - PAD.bottom} L${x(0).toFixed(1)},${H - PAD.bottom} Z`;

  const ticks = [min, min + span / 2, max];
  const labelEvery = Math.ceil(data.length / 6);

  const onMove = (e) => {
    const box = e.currentTarget.getBoundingClientRect();
    const px = ((e.clientX - box.left) / box.width) * W;
    const i = Math.round(((px - PAD.left) / (W - PAD.left - PAD.right)) * (data.length - 1));
    setHover(Math.max(0, Math.min(data.length - 1, i)));
  };

  return (
    <div className="chart-wrap">
      {/* The readout is above the plot rather than a tooltip that follows the
          pointer, because a tooltip that follows the pointer covers the thing
          you are pointing at. */}
      <div className="chart-head">
        <span>
          {hover === null ? (
            <span className="muted">Hover the chart for a day.</span>
          ) : (
            <>
              <b>{format(data[hover].value)}{unit && ` ${unit}`}</b>
              <span className="muted"> on {data[hover].date}</span>
            </>
          )}
        </span>
        {label && <span className="muted">{label}</span>}
      </div>
      <svg
        className="chart"
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label={`Line chart, ${data.length} points, from ${data[0].date} to ${data[data.length - 1].date}`}
        onMouseMove={onMove}
        onMouseLeave={() => setHover(null)}
      >
        <defs>
          <linearGradient id="fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.18" />
            <stop offset="100%" stopColor={color} stopOpacity="0" />
          </linearGradient>
        </defs>

        {ticks.map((t) => (
          <g key={t}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(t)} y2={y(t)} className="grid" />
            <text x={PAD.left - 8} y={y(t) + 4} textAnchor="end" className="lbl">
              {format(t)}
            </text>
          </g>
        ))}

        <path d={area} fill="url(#fill)" />
        <path d={line} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" />

        {data.map((d, i) =>
          i % labelEvery === 0 ? (
            <text key={d.date} x={x(i)} y={H - 8} textAnchor="middle" className="lbl">
              {d.date.slice(5)}
            </text>
          ) : null
        )}

        {hover !== null && (
          <g>
            <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={H - PAD.bottom} className="chart-cursor" />
            <circle className="chart-pt" cx={x(hover)} cy={y(data[hover].value)} r="4" stroke={color} />
          </g>
        )}
      </svg>
    </div>
  );
}

function format(n) {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return `${Math.round(n)}`;
}
