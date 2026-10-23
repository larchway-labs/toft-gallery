package main

import (
	"html/template"
	"net/http"
	"time"
)

// The page is one template with its CSS inline. No external stylesheet, no
// font, no CDN: a status board is the one page that has to render when the
// network it is reporting on is broken.
var page = template.Must(template.New("board").Funcs(template.FuncMap{
	"since": func(t time.Time) string {
		if t.IsZero() {
			return "not checked yet"
		}
		d := time.Since(t).Round(time.Second)
		if d < time.Second {
			return "just now"
		}
		return d.String() + " ago"
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: light; }
  body { margin: 0; padding: 2.5rem 1.5rem; font: 15px/1.5 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
         color: #1c2024; background: #f7f8fa; }
  main { max-width: 52rem; margin: 0 auto; }
  h1 { font-size: 1.4rem; margin: 0 0 .25rem; letter-spacing: -0.01em; }
  p.sub { margin: 0 0 1.75rem; color: #6b7280; font-size: .875rem; }
  table { width: 100%; border-collapse: collapse; background: #fff; border: 1px solid #e5e7eb; border-radius: 10px; overflow: hidden; }
  th, td { text-align: left; padding: .7rem .9rem; border-bottom: 1px solid #f0f1f3; font-size: .9rem; }
  th { font-size: .72rem; text-transform: uppercase; letter-spacing: .06em; color: #6b7280; background: #fbfbfc; }
  tr:last-child td { border-bottom: none; }
  td.url { color: #6b7280; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .8rem; word-break: break-all; }
  .pill { display: inline-flex; align-items: center; gap: .4rem; padding: .15rem .55rem; border-radius: 999px;
          font-size: .75rem; font-weight: 600; }
  .dot { width: .5rem; height: .5rem; border-radius: 999px; background: currentColor; }
  .up      { color: #067647; background: #ecfdf3; }
  .slow    { color: #b54708; background: #fffaeb; }
  .down    { color: #b42318; background: #fef3f2; }
  .pending { color: #475467; background: #f2f4f7; }
  .empty { background: #fff; border: 1px dashed #d0d5dd; border-radius: 10px; padding: 2rem; text-align: center; color: #6b7280; }
  code { background: #f2f4f7; padding: .1rem .35rem; border-radius: 4px; font-size: .82rem; }
  footer { margin-top: 1.25rem; color: #98a2b3; font-size: .78rem; }
</style>
</head>
<body>
<main>
  <h1>{{.Title}}</h1>
  <p class="sub">Checked every {{.Every}}s from inside the network. This page shows what this
     process has seen since it started; nothing is stored.</p>

  {{if .Results}}
  <table>
    <thead><tr><th>Service</th><th>Status</th><th>Latency</th><th>Last check</th><th>Endpoint</th></tr></thead>
    <tbody>
    {{range .Results}}
      <tr>
        <td><strong>{{.Service.Name}}</strong>{{if .Error}}<br><span style="color:#b42318;font-size:.78rem">{{.Error}}</span>{{end}}</td>
        <td><span class="pill {{.Status}}"><span class="dot"></span>{{.Status}}</span></td>
        <td>{{if .CheckedAt.IsZero}}—{{else}}{{.LatencyMS}} ms{{end}}</td>
        <td>{{since .CheckedAt}}</td>
        <td class="url">{{.Service.URL}}</td>
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <div class="empty">
    <p><strong>No services configured.</strong></p>
    <p>Set the <code>SERVICES</code> environment variable to a comma-separated list of
       <code>name=url</code> pairs, then redeploy:</p>
    <p><code>Wiki=https://wiki.internal/health, Billing=https://billing.internal/healthz</code></p>
  </div>
  {{end}}

  <footer>JSON at <code>{{.BasePath}}/api/status</code> · liveness at <code>{{.BasePath}}/healthz</code></footer>
</main>
</body>
</html>
`))

func renderBoard(w http.ResponseWriter, cfg config, results []Result) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = page.Execute(w, struct {
		Title    string
		BasePath string
		Every    int
		Results  []Result
	}{
		Title:    cfg.Title,
		BasePath: cfg.BasePath,
		Every:    int(cfg.Interval.Seconds()),
		Results:  results,
	})
}
