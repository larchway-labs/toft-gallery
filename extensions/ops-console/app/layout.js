import Link from "next/link";
import "./globals.css";

export const metadata = {
  title: "Ops Console",
  description: "On-call, service checklist and runbooks for the platform team.",
};

// next/link applies the platform's basePath itself, so every link here is
// written as if the app were mounted at the root. A hand-built URL would not
// be — see the README.
export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body>
        <header className="topbar">
          <div className="wrap">
            <Link href="/" className="brand">
              Ops Console
            </Link>
            <nav>
              <Link href="/">Overview</Link>
              <Link href="/runbooks">Runbooks</Link>
            </nav>
          </div>
        </header>
        <main className="wrap">{children}</main>
        <footer className="wrap foot">
          Deployed from the Toft gallery. Everything on this page is
          configuration — this app reads no platform API.
        </footer>
      </body>
    </html>
  );
}
