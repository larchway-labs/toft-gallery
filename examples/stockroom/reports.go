package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Getting a report out of the app.
//
// Three channels — download, Slack, email — over one built report, and one
// function that does the sending. The tab and the scheduled task both call
// `deliver`, so a report posted at 07:00 by the schedule and one posted by hand
// at 07:01 are the same bytes produced by the same code.

type reportsData struct {
	Kinds      []ReportKind
	Selected   string
	Deliveries []Delivery
	Self       *Self
	SelfErr    string
	OnPlat     bool
	Stats      Stats
	DefaultTo  string
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	deliveries, err := s.store.ListDeliveries(20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stats, err := s.store.Stats()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	selected := r.URL.Query().Get("report")
	if selected == "" {
		selected = ReportKinds[0].Key
	}
	data := reportsData{
		Kinds: ReportKinds, Selected: selected, Deliveries: deliveries,
		Stats: stats, OnPlat: s.plat.Available(),
	}
	if s.plat.Available() {
		self, err := s.self(r.Context())
		if err != nil {
			data.SelfErr = err.Error()
		}
		data.Self = self
	}
	s.render(w, r, "reports", "reports", "Reports", data)
}

func (s *Server) handleReportDeliver(w http.ResponseWriter, r *http.Request) {
	kind := reportKind(r.FormValue("report"))
	channel := r.FormValue("channel")
	to := strings.TrimSpace(r.FormValue("to"))
	back := s.cfg.url("/reports?report=" + kind.Key)

	report, err := s.store.BuildReport(kind.Key, s.cfg.AppName)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	if channel == "download" {
		pdf, err := report.PDF()
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.store.RecordDelivery(Delivery{
			Report: kind.Key, Channel: "download", Trigger: "manual", Status: "ok",
			Detail: fmt.Sprintf("%d KB", len(pdf)/1024), Target: actorFrom(r),
		})
		s.store.Log(actorFrom(r), "downloaded a report", kind.Title, "")
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+report.Filename+`"`)
		_, _ = w.Write(pdf)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	// One key per report, channel, target and minute: a double-click sends once.
	// The platform answers the replay with the original send id and sends
	// nothing, which is the behaviour a button needs.
	key := fmt.Sprintf("stockroom:%s:%s:%s:%d", kind.Key, channel, to, time.Now().Unix()/60)

	result := s.deliver(ctx, report, channel, to, "manual", "", key)
	setFlash(w, result.tone, result.message)
	s.redirect(w, r, back)
}

// "accepted", never "sent". The platform answers a send with 202 and a send id:
// the message is queued, gated again immediately before the transport, and
// delivered afterwards by a worker. An app that renders that as "posted" is
// claiming an outcome it has not been told — and it will be wrong exactly when
// it matters, as it was for a workspace whose bot lacked the files:write scope,
// where every upload was accepted here and refused by Slack sixteen seconds
// later. The record is what a workflow may rely on; this is only the receipt.

// deliverResult is what both callers need: a tone and a sentence for a person,
// and the log lines the scheduled task pushes into its run.
type deliverResult struct {
	tone    string // ok | warn | bad
	message string
	logs    []string
	ok      bool
}

// deliver builds nothing — it takes a built report and sends it.
//
// Where a capability is missing it degrades and says so, rather than refusing:
// an app that cannot attach a file can still post the summary, and a stockroom
// manager who wanted the numbers on Monday morning would much rather have the
// numbers without the PDF than an error. What it never does is claim to have
// sent something it did not.
func (s *Server) deliver(ctx context.Context, report *Report, channel, to, trigger, runID, key string) deliverResult {
	if !s.plat.Available() {
		return deliverResult{tone: "warn", ok: false,
			message: "This app is not running on Toft, so there is nothing to send through. " +
				"Download the PDF instead.",
			logs: []string{"! no platform API in the environment; nothing was sent"}}
	}

	self, selfErr := s.self(ctx)
	pdf, err := report.PDF()
	if err != nil {
		s.record(report, channel, trigger, to, "failed", "building the PDF: "+err.Error(), "", runID)
		return deliverResult{tone: "bad", message: "The PDF would not build: " + err.Error(),
			logs: []string{"! building the PDF failed: " + err.Error()}}
	}
	logs := []string{fmt.Sprintf("built %s — %d KB, %d rows",
		report.Filename, len(pdf)/1024, len(report.Items)+len(report.Movements))}

	switch channel {
	case "slack":
		// Ask for the file first; fall back to the text post when the app has
		// Slack but not files. The capability list is advisory here — the
		// platform re-checks at send time either way — so a stale cache costs a
		// refusal message, never a wrong send.
		if self.Can("slack.file") || selfErr != nil {
			res, err := s.plat.PostSlackFile(ctx, report.Filename, pdf,
				report.Kind.Title, report.Summary, key)
			if err == nil {
				s.record(report, "slack", trigger, "the app's channel", "accepted", "PDF handed over", res.SendID, runID)
				logs = append(logs, "Slack accepted the PDF, send "+res.SendID)
				return deliverResult{tone: "ok", ok: true, logs: logs,
					message: "Handed " + report.Kind.Title + " to Slack as a PDF. Delivery finishes in " +
						"the background — if the workspace refuses it, the reason lands in the " +
						"install's Integration log, not here."}
			}
			if !capabilityRefusal(err) {
				return s.sendFailed(report, "slack", trigger, "the app's channel", runID, err, logs)
			}
			invalidateSelf()
			logs = append(logs, "! file upload refused, falling back to the summary text")
		}

		res, err := s.plat.PostSlack(ctx, report.Summary, key+":text")
		if err != nil {
			return s.sendFailed(report, "slack", trigger, "the app's channel", runID, err, logs)
		}
		s.record(report, "slack", trigger, "the app's channel", "accepted", "summary without the PDF", res.SendID, runID)
		logs = append(logs, "posted the summary to Slack, send "+res.SendID)
		return deliverResult{tone: "ok", ok: true, logs: logs,
			message: "Handed the summary to Slack. The PDF was not attached — ask an administrator " +
				"to allow file uploads for this app if you want the attachment."}

	case "email":
		recipients := splitRecipients(to)
		if len(recipients) == 0 {
			return deliverResult{tone: "bad", logs: append(logs, "! no recipients given"),
				message: "Enter at least one address. Recipients are the one destination an app " +
					"chooses for itself — everything else about the message is the platform's."}
		}
		req := EmailRequest{
			To:      recipients,
			Subject: fmt.Sprintf("%s — %s", report.Kind.Title, report.At.Format("2 Jan 2006")),
			Text:    plainSummary(report),
			HTML:    emailHTML(report),
		}
		attach := pdf
		note := ""
		if selfErr == nil && !self.Can("email.attach") {
			attach, note = nil, " The PDF was not attached — this app may send email but not attachments."
			logs = append(logs, "! attachments are not enabled for this app; sending the summary only")
		}
		res, err := s.plat.SendEmail(ctx, req, report.Filename, attach, key)
		if err != nil && capabilityRefusal(err) && len(attach) > 0 {
			invalidateSelf()
			logs = append(logs, "! attachment refused, retrying without it")
			res, err = s.plat.SendEmail(ctx, req, "", nil, key+":noattach")
			note = " The PDF was not attached — this app may send email but not attachments."
		}
		if err != nil {
			return s.sendFailed(report, "email", trigger, strings.Join(recipients, ", "), runID, err, logs)
		}
		s.record(report, "email", trigger, strings.Join(recipients, ", "), "accepted",
			fmt.Sprintf("%d recipient(s)", len(recipients)), res.SendID, runID)
		logs = append(logs, fmt.Sprintf("accepted for %d recipient(s), send %s", len(recipients), res.SendID))
		return deliverResult{tone: "ok", ok: true, logs: logs,
			message: fmt.Sprintf("Handed %s to the platform for %s.%s Delivery finishes in the background.",
				report.Kind.Title, strings.Join(recipients, ", "), note)}
	}

	return deliverResult{tone: "bad", message: "Pick a channel: Slack or email.",
		logs: []string{"! unknown channel " + channel}}
}

func (s *Server) sendFailed(report *Report, channel, trigger, target, runID string,
	err error, logs []string) deliverResult {
	s.record(report, channel, trigger, target, "failed", err.Error(), "", runID)
	return deliverResult{tone: "bad", logs: append(logs, "! "+err.Error()),
		// The platform's own refusal text, verbatim. These messages name the
		// remedy in the administrator's words — "ask an administrator to attach
		// this app to Slack" — and rewriting them into "send failed" throws away
		// the only part somebody could act on.
		message: err.Error()}
}

func (s *Server) record(report *Report, channel, trigger, target, status, detail, sendID, runID string) {
	s.store.RecordDelivery(Delivery{
		Report: report.Kind.Key, Channel: channel, Trigger: trigger, Target: target,
		Status: status, Detail: detail, SendID: sendID, RunID: runID,
	})
}

// capabilityRefusal reports whether the platform said no because of a gate
// rather than because something broke. Only a gate is worth retrying differently.
func capabilityRefusal(err error) bool {
	var api *APIError
	if !asAPIError(err, &api) {
		return false
	}
	return api.Status == http.StatusForbidden || api.Status == http.StatusBadRequest
}

func asAPIError(err error, target **APIError) bool {
	if e, ok := err.(*APIError); ok {
		*target = e
		return true
	}
	return false
}

func splitRecipients(to string) []string {
	out := []string{}
	for _, part := range strings.FieldsFunc(to, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' '
	}) {
		if p := strings.TrimSpace(part); strings.Contains(p, "@") {
			out = append(out, p)
		}
	}
	return out
}

// plainSummary is the text half of the email.
//
// It says where the answer lives in the app rather than promising anything about
// mail: either of the platform's notification switches may be off, so copy that
// reads "you will get this every Monday" can be false through no fault of the
// app.
func plainSummary(r *Report) string {
	body := strings.ReplaceAll(r.Summary, "*", "")
	return body + "\n\n" +
		"The full report is attached as a PDF when this app is allowed to send attachments.\n" +
		"Every delivery is also listed on the Reports tab of the app itself, whether or not " +
		"this message reached you."
}

func emailHTML(r *Report) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#15181B;line-height:1.5">`)
	fmt.Fprintf(&b, `<p style="font:600 15px/1.3 ui-monospace,Menlo,Consolas,monospace;margin:0 0 2px">%s</p>`,
		escapeHTML(r.Kind.Title))
	fmt.Fprintf(&b, `<p style="color:#6B7076;font-size:13px;margin:0 0 16px">%s</p>`,
		escapeHTML(r.At.Format("2 January 2006, 15:04 MST")))

	b.WriteString(`<table cellpadding="0" cellspacing="0" style="margin:0 0 18px"><tr>`)
	for _, tile := range [][2]string{
		{fmt.Sprintf("%d", r.Stats.Items), "lines"},
		{fmt.Sprintf("%d", r.Stats.Units), "units"},
		{money(r.Stats.ValueCents), "at cost"},
		{fmt.Sprintf("%d", r.Stats.Low+r.Stats.Out), "need ordering"},
	} {
		fmt.Fprintf(&b, `<td style="padding-right:26px"><div style="font:600 18px ui-monospace,Menlo,monospace">%s</div>`+
			`<div style="color:#6B7076;font-size:11px">%s</div></td>`, escapeHTML(tile[0]), escapeHTML(tile[1]))
	}
	b.WriteString(`</tr></table>`)

	// The platform reduces an app's HTML to an allow-list before it goes out, so
	// this stays to simple tables, paragraphs and inline colour on purpose.
	// Anything cleverer would be stripped, and the message would arrive looking
	// broken rather than looking plain.
	b.WriteString(`<table cellpadding="6" cellspacing="0" style="border-collapse:collapse;font-size:13px">`)
	switch r.Kind.Key {
	case "movements":
		b.WriteString(`<tr><th align="left">When</th><th align="left">Item</th><th align="right">Change</th></tr>`)
		for i, m := range r.Movements {
			if i == 20 {
				break
			}
			fmt.Fprintf(&b, `<tr><td style="color:#6B7076">%s</td><td>%s</td><td align="right">%+d</td></tr>`,
				escapeHTML(m.At.Format("02 Jan 15:04")), escapeHTML(m.ItemName), m.Qty)
		}
	default:
		b.WriteString(`<tr><th align="left">SKU</th><th align="left">Item</th><th align="right">Qty</th><th align="right">Reorder</th></tr>`)
		for i, it := range r.Items {
			if i == 25 {
				break
			}
			fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td align="right">%d</td><td align="right">%d</td></tr>`,
				escapeHTML(it.SKU), escapeHTML(it.Name), it.Quantity, it.ReorderPoint)
		}
	}
	b.WriteString(`</table>`)
	b.WriteString(`<p style="color:#6B7076;font-size:12px;margin-top:18px">` +
		`The complete list is in the attached PDF. Every delivery is recorded on the app's Reports tab.</p></div>`)
	return b.String()
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
