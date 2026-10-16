// The shell every page in this app sits inside.
//
// Two stylesheets and the order matters: the kit is the house style the whole
// Toft gallery shares, and globals.css is this app's own layer on top of
// it. Both are imported here rather than linked, because Next.js hashes and
// inlines what it is given and a <link> to a file it does not know about would
// not survive the build.
import "./toft-kit.css";
import "./globals.css";

export const metadata = {
  title: "Service metrics",
  description: "A Next.js dashboard example for Toft.",
  icons: { icon: "/favicon.svg" },
};

// What the platform told this app. Read on the server, which is where the
// environment is — a client component would have to be handed it, and handing
// a browser the whole environment is how a secret ends up in a bundle.
function rail() {
  const basePath = process.env.BASE_PATH || "";
  const slug = process.env.TOFT_APP_SLUG || "";
  const mode = process.env.TOFT_EXECUTION_MODE || "";
  return [
    ["Next.js", "on", "Detected from next.config.* or `next` in package.json."],
    ["Server components", "on", "Everything here renders on the server except the chart."],
    ["basePath", basePath ? "on" : "off",
      basePath
        ? `Injected into a generated next.config wrapper before the build: ${basePath}`
        : "Not mounted under a prefix here."],
    ["No chart library", "on", "The line is an SVG path this app writes."],
    ["Toft workload", slug ? "on" : "off",
      slug ? `${slug}${mode ? ` · ${mode} mode` : ""}` : "This is a local run."],
  ];
}

export default function RootLayout({ children }) {
  // Next.js is the one framework here the proxy forwards the *full* path to, so
  // a URL this page emits has to carry the prefix. `basePath` is injected into a
  // generated next.config wrapper before the build and handles every asset Next
  // knows about; a plain <script> tag is not one of those, so it is spelled out.
  const base = process.env.BASE_PATH || "";
  return (
    <html lang="en">
      <body>
        <a className="lp-skip" href="#main">Skip to content</a>

        <header className="masthead">
          <div className="masthead-in">
            <div>
              <div className="wordmark">
                <span className="mark" aria-hidden="true" />
                <span className="wordmark-text">Toft example</span>
              </div>
              <h1>Service metrics</h1>
              <p className="standfirst">
                Ninety days of generated data, so the numbers are the same for everyone
                looking at this page. The range and the metric are in the URL, so a view
                is a link somebody can paste into a chat.
              </p>
            </div>
            <div className="masthead-aside">
              <span className="chip chip-lang">JavaScript · Next.js 15</span>
              <span className="chip">React 19 · App Router</span>
            </div>
          </div>
        </header>

        <div className="rail">
          <div className="rail-in">
            <span className="rail-label">Toft</span>
            {rail().map(([label, state, note]) => (
              <span key={label} className={`cap ${state}`} title={note}>
                <b>{label}</b>
              </span>
            ))}
          </div>
        </div>

        <main className="shell" id="main">{children}</main>

        {/* The gallery's shared script: sortable tables, relative times, toasts.
            Loaded as a plain tag rather than next/script because it touches the
            DOM Next has already rendered and needs no bundling. */}
        <script src={`${base}/toft-kit.js`} defer />

        <footer className="foot">
          <div className="foot-in">
            <span>An example app from the <strong>Toft</strong> gallery.</span>
            <span>Next.js · server components · one client island · no chart library</span>
          </div>
        </footer>
      </body>
    </html>
  );
}
