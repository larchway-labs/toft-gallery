package main

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// A report is built once and then rendered three ways: as a PDF to download, as
// a PDF attached to Slack or email, and as the plain-text summary that travels
// with it. One assembly step, three renderers — because a Slack message quoting
// a different number from the PDF beside it is the failure that makes people
// stop trusting both.

type ReportKind struct {
	Key   string
	Title string
	Blurb string
}

var ReportKinds = []ReportKind{
	{"inventory", "Full register", "Every active line, grouped by aisle, with counts and value."},
	{"low_stock", "At or below reorder point", "The short list: what to order, and how far under it is."},
	{"movements", "Movements, last 30 days", "Every receipt, issue, adjustment and return, most recent first."},
}

func reportKind(key string) ReportKind {
	for _, k := range ReportKinds {
		if k.Key == key {
			return k
		}
	}
	return ReportKinds[0]
}

// Report is the assembled thing: the numbers, the rows, and the words.
type Report struct {
	Kind      ReportKind
	At        time.Time
	AppName   string
	Stats     Stats
	Items     []Item
	Movements []Movement
	// Summary is the plain-text body — the Slack comment and the email's text
	// half. It stands on its own: someone reading it on a phone without opening
	// the attachment still learns the thing the report was sent to tell them.
	Summary  string
	Filename string
}

func (s *Store) BuildReport(kind, appName string) (*Report, error) {
	k := reportKind(kind)
	stats, err := s.Stats()
	if err != nil {
		return nil, err
	}
	r := &Report{Kind: k, At: time.Now().UTC(), AppName: appName, Stats: stats}

	switch k.Key {
	case "low_stock":
		if r.Items, err = s.ListItems(ItemQuery{Sort: "qty"}); err != nil {
			return nil, err
		}
		short := r.Items[:0]
		for _, it := range r.Items {
			if st := it.State(); st == "low" || st == "out" {
				short = append(short, it)
			}
		}
		r.Items = short
	case "movements":
		if r.Movements, err = s.ListMovements(time.Now().UTC().AddDate(0, 0, -30), 0, "", 500); err != nil {
			return nil, err
		}
	default:
		// Grouped in the fixed aisle order, not alphabetically: the report is
		// read beside the band above it, and two different orderings of the same
		// five aisles on one page is a page somebody has to re-read.
		for _, category := range Categories {
			items, err := s.ListItems(ItemQuery{Category: category})
			if err != nil {
				return nil, err
			}
			for _, it := range items {
				if it.Status != "retired" {
					r.Items = append(r.Items, it)
				}
			}
		}
	}

	r.Summary = r.buildSummary()
	r.Filename = fmt.Sprintf("stockroom-%s-%s.pdf", k.Key, r.At.Format("2006-01-02"))
	return r, nil
}

func (r *Report) buildSummary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%s* — %s\n", r.Kind.Title, r.At.Format("2 Jan 2006, 15:04 MST"))
	fmt.Fprintf(&b, "%d lines on the shelf · %s at cost · %d at or below reorder point",
		r.Stats.Items, money(r.Stats.ValueCents), r.Stats.Low)
	if r.Stats.Out > 0 {
		fmt.Fprintf(&b, " · %d out of stock", r.Stats.Out)
	}
	b.WriteString("\n")

	switch r.Kind.Key {
	case "low_stock":
		if len(r.Items) == 0 {
			b.WriteString("\nNothing is at or below its reorder point. No order needed this week.")
			break
		}
		b.WriteString("\nTo order:\n")
		for i, it := range r.Items {
			if i == 12 {
				fmt.Fprintf(&b, "…and %d more, in the attached PDF.\n", len(r.Items)-12)
				break
			}
			fmt.Fprintf(&b, "• %s — %s: %d left, reorder at %d (%s)\n",
				it.SKU, it.Name, it.Quantity, it.ReorderPoint, it.Bin)
		}
	case "movements":
		receipts, issues := 0, 0
		for _, m := range r.Movements {
			if m.Qty > 0 {
				receipts += m.Qty
			} else {
				issues -= m.Qty
			}
		}
		fmt.Fprintf(&b, "\n%d movements in 30 days: %d units in, %d units out.\n",
			len(r.Movements), receipts, issues)
	default:
		b.WriteString("\nBy aisle:\n")
		for _, c := range r.Stats.Categories {
			fmt.Fprintf(&b, "• %s — %d lines, %d units, %s\n",
				c.Category, c.Items, c.Units, money(c.ValueCents))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------- the PDF ----------

// The page carries the app's own identity rather than a generic table dump: the
// steel header band, the aisle band, and codes set in a monospaced face are the
// same three devices the screen uses, so a PDF forwarded to somebody who has
// never opened the app still looks like it came from this stockroom.

type rgb struct{ r, g, b int }

var (
	pdfInk    = rgb{21, 24, 26}
	pdfMuted  = rgb{107, 112, 118}
	pdfRule   = rgb{221, 220, 212}
	pdfSteel  = rgb{46, 75, 90}
	pdfZebra  = rgb{247, 247, 243}
	pdfFlag   = rgb{168, 62, 39}
	pdfPaper  = rgb{255, 255, 255}
	pdfCatHex = map[string]rgb{
		"Electronics": {14, 147, 168},
		"Consumables": {194, 112, 28},
		"Tools":       {29, 111, 191},
		"Safety":      {47, 143, 91},
		"Spares":      {154, 78, 174},
	}
)

func (r *Report) PDF() ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(14, 14, 14)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AliasNbPages("{np}")

	generated := r.At.Format("2 January 2006, 15:04 MST")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-14)
		pdf.SetFont("Helvetica", "", 7.5)
		setText(pdf, pdfMuted)
		pdf.CellFormat(0, 4, tr(r.AppName+" · generated "+generated+" · sent through Toft"),
			"", 0, "L", false, 0, "")
		pdf.CellFormat(0, 4, tr(fmt.Sprintf("Page %d of {np}", pdf.PageNo())), "", 0, "R", false, 0, "")
	})

	pdf.AddPage()
	r.pdfHeader(pdf)
	r.pdfSummaryRow(pdf)
	r.pdfAisleBand(pdf)

	switch r.Kind.Key {
	case "movements":
		r.pdfMovements(pdf)
	case "low_stock":
		r.pdfLowStock(pdf)
	default:
		r.pdfInventory(pdf)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (r *Report) pdfHeader(pdf *fpdf.Fpdf) {
	w, _ := pdf.GetPageSize()
	setFill(pdf, pdfSteel)
	pdf.Rect(0, 0, w, 26, "F")

	pdf.SetXY(14, 7)
	pdf.SetFont("Courier", "B", 15)
	setText(pdf, pdfPaper)
	pdf.CellFormat(100, 7, tr(strings.ToUpper(r.AppName)), "", 0, "L", false, 0, "")

	pdf.SetXY(14, 15)
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(120, 5, tr(r.Kind.Title), "", 0, "L", false, 0, "")

	pdf.SetXY(w-104, 15)
	pdf.SetFont("Helvetica", "", 8)
	pdf.CellFormat(90, 5, tr(r.At.Format("2 January 2006, 15:04 MST")), "", 0, "R", false, 0, "")

	pdf.SetY(34)
}

func (r *Report) pdfSummaryRow(pdf *fpdf.Fpdf) {
	tiles := []struct {
		value, label string
	}{
		{fmt.Sprintf("%d", r.Stats.Items), "lines on the shelf"},
		{fmt.Sprintf("%d", r.Stats.Units), "units counted"},
		{money(r.Stats.ValueCents), "value at cost"},
		{fmt.Sprintf("%d", r.Stats.Low+r.Stats.Out), "need ordering"},
	}
	w, _ := pdf.GetPageSize()
	tileW := (w - 28) / 4
	y := pdf.GetY()

	for i, t := range tiles {
		x := 14 + float64(i)*tileW
		pdf.SetXY(x, y)
		pdf.SetFont("Courier", "B", 17)
		if i == 3 && r.Stats.Low+r.Stats.Out > 0 {
			setText(pdf, pdfFlag)
		} else {
			setText(pdf, pdfInk)
		}
		pdf.CellFormat(tileW, 8, tr(t.value), "", 0, "L", false, 0, "")

		pdf.SetXY(x, y+8)
		pdf.SetFont("Helvetica", "", 8)
		setText(pdf, pdfMuted)
		pdf.CellFormat(tileW, 4, tr(t.label), "", 0, "L", false, 0, "")
	}
	pdf.SetY(y + 16)
}

// pdfAisleBand is the report's copy of the screen's signature element: the whole
// stockroom as one bar, each aisle sized by what is standing in it.
func (r *Report) pdfAisleBand(pdf *fpdf.Fpdf) {
	total := 0
	for _, c := range r.Stats.Categories {
		total += c.ValueCents
	}
	if total == 0 {
		return
	}
	w, _ := pdf.GetPageSize()
	full := w - 28
	y := pdf.GetY()
	x := 14.0

	for _, c := range r.Stats.Categories {
		if c.ValueCents == 0 {
			continue
		}
		seg := full * float64(c.ValueCents) / float64(total)
		setFill(pdf, pdfCatHex[c.Category])
		// A 0.7mm gap between segments, not a stroke: adjacent fills that touch
		// read as one shape, and a border would add a colour the palette never
		// agreed to.
		pdf.Rect(x, y, maxf(seg-0.7, 0.4), 5, "F")
		x += seg
	}

	// The key is an even row of five, not five labels parked under their own
	// segments: a narrow aisle would put its name on top of its neighbour's, and
	// two overlapping labels are worse than a key that has to be read across.
	y += 6.5
	col := full / float64(len(r.Stats.Categories))
	pdf.SetFont("Helvetica", "", 7.5)
	for i, c := range r.Stats.Categories {
		x := 14 + float64(i)*col
		setFill(pdf, pdfCatHex[c.Category])
		pdf.Rect(x, y+1.3, 2, 2, "F")
		setText(pdf, pdfMuted)
		pdf.SetXY(x+3.4, y)
		pdf.CellFormat(col-3.4, 4,
			tr(fmt.Sprintf("%s  %s", c.Category, money(c.ValueCents))), "", 0, "L", false, 0, "")
	}
	pdf.SetY(y + 9)
}

// tableHead draws the column strip. Widths are given in mm and have to sum to
// the printable width (182mm on A4 with 14mm margins).
func tableHead(pdf *fpdf.Fpdf, cols []string, widths []float64, aligns []string) {
	pdf.SetFont("Helvetica", "B", 7.5)
	setText(pdf, pdfMuted)
	setDraw(pdf, pdfRule)
	pdf.SetLineWidth(0.3)
	y := pdf.GetY()
	for i, c := range cols {
		pdf.CellFormat(widths[i], 6, tr(strings.ToUpper(c)), "", 0, aligns[i], false, 0, "")
	}
	pdf.Ln(6)
	pdf.Line(14, y+6, 196, y+6)
	pdf.SetY(y + 7)
}

func (r *Report) pdfInventory(pdf *fpdf.Fpdf) {
	// The widths sum to 182mm, the printable width on A4 at 14mm margins. The
	// money columns are wide enough for a four-figure value, because a figure
	// that overruns its cell prints on top of the one beside it.
	cols := []string{"SKU", "Item", "Bin", "Supplier", "Qty", "Reorder", "Unit", "Value"}
	widths := []float64{18, 52, 16, 30, 12, 16, 17, 21}
	aligns := []string{"L", "L", "L", "L", "R", "R", "R", "R"}

	current := ""
	for _, it := range r.Items {
		if it.Category != current {
			current = it.Category
			if pdf.GetY() > 240 {
				pdf.AddPage()
			}
			pdf.Ln(3)
			setFill(pdf, pdfCatHex[current])
			pdf.Rect(14, pdf.GetY()+1.2, 2.2, 3.6, "F")
			pdf.SetX(18)
			pdf.SetFont("Helvetica", "B", 9.5)
			setText(pdf, pdfInk)
			pdf.CellFormat(100, 6, tr(current), "", 1, "L", false, 0, "")
			tableHead(pdf, cols, widths, aligns)
		}
		if pdf.GetY() > 268 {
			pdf.AddPage()
			tableHead(pdf, cols, widths, aligns)
		}
		r.pdfItemRow(pdf, it, widths, aligns)
	}
}

func (r *Report) pdfLowStock(pdf *fpdf.Fpdf) {
	if len(r.Items) == 0 {
		pdf.SetFont("Helvetica", "", 10)
		setText(pdf, pdfInk)
		pdf.MultiCell(0, 5, tr("Nothing is at or below its reorder point. "+
			"No order needs raising from this report."), "", "L", false)
		return
	}
	cols := []string{"SKU", "Item", "Aisle", "Supplier", "Qty", "Reorder", "Short by", "To order"}
	widths := []float64{18, 56, 16, 32, 12, 16, 16, 16}
	aligns := []string{"L", "L", "L", "L", "R", "R", "R", "R"}
	tableHead(pdf, cols, widths, aligns)

	zebra := false
	for _, it := range r.Items {
		if pdf.GetY() > 268 {
			pdf.AddPage()
			tableHead(pdf, cols, widths, aligns)
		}
		if zebra {
			setFill(pdf, pdfZebra)
			pdf.Rect(14, pdf.GetY()-0.5, 182, 6, "F")
		}
		zebra = !zebra

		short := it.ReorderPoint - it.Quantity
		order := maxi(short*2, it.ReorderPoint)
		pdf.SetFont("Courier", "", 8)
		setText(pdf, pdfInk)
		pdf.CellFormat(widths[0], 5, tr(it.SKU), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.CellFormat(widths[1], 5, tr(clip(it.Name, 46)), "", 0, "L", false, 0, "")
		pdf.SetFont("Courier", "", 8)
		pdf.CellFormat(widths[2], 5, tr(it.Bin), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.CellFormat(widths[3], 5, tr(clip(it.Supplier, 26)), "", 0, "L", false, 0, "")
		pdf.SetFont("Courier", "B", 8)
		setText(pdf, pdfFlag)
		pdf.CellFormat(widths[4], 5, fmt.Sprintf("%d", it.Quantity), "", 0, "R", false, 0, "")
		setText(pdf, pdfInk)
		pdf.SetFont("Courier", "", 8)
		pdf.CellFormat(widths[5], 5, fmt.Sprintf("%d", it.ReorderPoint), "", 0, "R", false, 0, "")
		pdf.CellFormat(widths[6], 5, fmt.Sprintf("%d", short), "", 0, "R", false, 0, "")
		pdf.SetFont("Courier", "B", 8)
		pdf.CellFormat(widths[7], 5, fmt.Sprintf("%d", order), "", 1, "R", false, 0, "")
	}

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "", 8)
	setText(pdf, pdfMuted)
	pdf.MultiCell(0, 4, tr("\"To order\" is a suggestion, not a purchase order: "+
		"twice the shortfall, or the reorder point, whichever is larger."), "", "L", false)
}

func (r *Report) pdfItemRow(pdf *fpdf.Fpdf, it Item, widths []float64, aligns []string) {
	low := it.State() == "low" || it.State() == "out"
	pdf.SetFont("Courier", "", 8)
	setText(pdf, pdfInk)
	pdf.CellFormat(widths[0], 5, tr(it.SKU), "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.CellFormat(widths[1], 5, tr(clip(it.Name, 48)), "", 0, "L", false, 0, "")
	pdf.SetFont("Courier", "", 8)
	pdf.CellFormat(widths[2], 5, tr(it.Bin), "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.CellFormat(widths[3], 5, tr(clip(it.Supplier, 26)), "", 0, "L", false, 0, "")
	pdf.SetFont("Courier", "", 8)
	if low {
		setText(pdf, pdfFlag)
		pdf.SetFont("Courier", "B", 8)
	}
	pdf.CellFormat(widths[4], 5, fmt.Sprintf("%d", it.Quantity), "", 0, "R", false, 0, "")
	setText(pdf, pdfInk)
	pdf.SetFont("Courier", "", 8)
	pdf.CellFormat(widths[5], 5, fmt.Sprintf("%d", it.ReorderPoint), "", 0, "R", false, 0, "")
	pdf.CellFormat(widths[6], 5, tr(money(it.UnitCostCents)), "", 0, "R", false, 0, "")
	pdf.CellFormat(widths[7], 5, tr(money(it.ValueCents())), "", 1, "R", false, 0, "")
}

func (r *Report) pdfMovements(pdf *fpdf.Fpdf) {
	cols := []string{"When", "SKU", "Item", "Movement", "Change", "Note", "By"}
	widths := []float64{24, 18, 48, 20, 14, 34, 24}
	aligns := []string{"L", "L", "L", "L", "R", "L", "L"}
	tableHead(pdf, cols, widths, aligns)

	if len(r.Movements) == 0 {
		pdf.SetFont("Helvetica", "", 10)
		setText(pdf, pdfInk)
		pdf.MultiCell(0, 5, tr("Nothing moved in the last 30 days."), "", "L", false)
		return
	}

	zebra := false
	for _, m := range r.Movements {
		if pdf.GetY() > 268 {
			pdf.AddPage()
			tableHead(pdf, cols, widths, aligns)
		}
		if zebra {
			setFill(pdf, pdfZebra)
			pdf.Rect(14, pdf.GetY()-0.5, 182, 6, "F")
		}
		zebra = !zebra

		pdf.SetFont("Courier", "", 7.5)
		setText(pdf, pdfMuted)
		pdf.CellFormat(widths[0], 5, tr(m.At.Format("02 Jan 15:04")), "", 0, "L", false, 0, "")
		setText(pdf, pdfInk)
		pdf.CellFormat(widths[1], 5, tr(m.SKU), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.CellFormat(widths[2], 5, tr(clip(m.ItemName, 40)), "", 0, "L", false, 0, "")
		pdf.CellFormat(widths[3], 5, tr(m.Kind), "", 0, "L", false, 0, "")
		pdf.SetFont("Courier", "B", 8)
		if m.Qty < 0 {
			setText(pdf, pdfFlag)
		} else {
			setText(pdf, pdfSteel)
		}
		pdf.CellFormat(widths[4], 5, fmt.Sprintf("%+d", m.Qty), "", 0, "R", false, 0, "")
		setText(pdf, pdfMuted)
		pdf.SetFont("Helvetica", "", 8)
		pdf.CellFormat(widths[5], 5, tr(clip(m.Note, 28)), "", 0, "L", false, 0, "")
		pdf.CellFormat(widths[6], 5, tr(clip(m.Actor, 18)), "", 1, "L", false, 0, "")
	}
}

func setText(pdf *fpdf.Fpdf, c rgb) { pdf.SetTextColor(c.r, c.g, c.b) }
func setFill(pdf *fpdf.Fpdf, c rgb) { pdf.SetFillColor(c.r, c.g, c.b) }
func setDraw(pdf *fpdf.Fpdf, c rgb) { pdf.SetDrawColor(c.r, c.g, c.b) }

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clip(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// tr folds text into the Latin-1 range the PDF core fonts can set.
//
// Written out rather than pulled from a font translator: the alternative loads a
// code-page map from disk at runtime, and a report that fails to render because
// a data file is missing from the release tree is a bad trade for the six
// characters this app actually uses.
var pdfFold = strings.NewReplacer(
	"×", "x", "–", "-", "—", "-", "·", "-", "…", "...",
	"'", "'", "'", "'", "“", "\"", "”", "\"", "≤", "<=", "≥", ">=",
	"→", "->", "•", "-",
)

func tr(s string) string {
	s = pdfFold.Replace(s)
	var b strings.Builder
	for _, r := range s {
		if r < 128 {
			b.WriteRune(r)
		} else if r < 256 {
			b.WriteRune(r) // Latin-1 supplement sets correctly in the core fonts.
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}
