package documents

import (
	"context"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/reports"
	"kamarapms/internal/tenancy"
)

// SetReports gives the documents the reports service, for the daily flash report as a PDF.
func (s *Service) SetReports(r *reports.Service) { s.reports = r }

// The columns of the flash: the row, then the seven figures of 7.1 (today, MTD, MTD last month, MTD last year, budget MTD, variance MTD, YTD). A property without a budget for the month has no budget and
// no variance column: the five that are left are wider.
var (
	flashW       = []float64{50, 18.5, 18.5, 18.5, 18.5, 18.5, 18.5, 18.5}
	flashWNoBudg = []float64{62, 23.5, 23.5, 23.5, 23.5, 23.5}
)

// flashCols are the columns of the table: the index of each in a row of eight cells and its width.
func flashCols(budget bool) ([]int, []float64) {
	if budget {
		return []int{0, 1, 2, 3, 4, 5, 6, 7}, flashW
	}
	return []int{0, 1, 2, 3, 4, 7}, flashWNoBudg
}

// flashTable prints the rows of one part of the flash under a header of the columns. A row is its label and its figures; bold rows are totals.
func (g *page) flashTable(budget bool, head []string, rows []flashRow) {
	idx, widths := flashCols(budget)
	hdr := func() {
		g.p.SetFillColor(band[0], band[1], band[2])
		g.font("B", 7)
		for i, w := range widths {
			al := "R"
			if i == 0 {
				al = "L"
			}
			g.p.CellFormat(w, 6, g.tr(g.lang.T(head[idx[i]])), "", boolInt(i == len(widths)-1), al, true, 0, "")
		}
	}
	hdr()
	for _, r := range rows {
		if g.p.GetY()+5 > footerAt-14 {
			g.p.AddPage()
			hdr()
		}
		style := ""
		if r.bold {
			style = "B"
		}
		g.font(style, 7)
		y := g.p.GetY()
		for i, w := range widths {
			al := "R"
			if i == 0 {
				al = "L"
			}
			s := r.cells[idx[i]]
			if i == 0 {
				s = g.lang.T(s)
				if r.indent {
					s = "  " + s
				}
				for len(g.split(g.tr(s), w-1)) > 1 && len(s) > 3 { // a long name is cut, never wrapped: the figures stay on one line
					s = strings.TrimSpace(s[:len(s)-2])
				}
			}
			g.p.CellFormat(w, 4.6, g.tr(s), "", boolInt(i == len(widths)-1), al, false, 0, "")
		}
		g.p.SetDrawColor(225, 228, 232)
		g.p.SetLineWidth(0.1)
		g.p.Line(margin, y+4.6, margin+bodyW, y+4.6)
	}
}

type flashRow struct {
	cells  [8]string
	bold   bool
	indent bool
}

// DailyFlashPDF is the daily flash report of a business date (report.view): the figures of the report, with the signature block of the night audit and of accounting.
func (s *Service) DailyFlashPDF(ctx context.Context, propertyID int64, date civil.Date) (Document, error) {
	rep, err := s.reports.DailyFlash(ctx, propertyID, date)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	g := newPage(dc.hotel, dc.lang, "DAILY FLASH REPORT", dc.printed)
	renderFlash(g, dc, rep, true)
	pdf, err := g.bytes()
	return Document{Filename: "daily-flash-" + rep.BusinessDate.String() + ".pdf", PDF: pdf}, err
}

// renderFlash prints the report on the page: the same body for the document and for the section of the night audit pack (which has no signature lines of its own).
func renderFlash(g *page, dc docContext, rep reports.DailyFlash, signatures bool) {
	l, dec := dc.lang, dc.decimals
	cell := func(kind, v string) string {
		if v == "" {
			return "-" // no figure: not a zero
		}
		d, err := decimal.NewFromString(v)
		if err != nil {
			return v
		}
		switch kind {
		case reports.FlashMoney:
			return l.Money(d, dec)
		case reports.FlashCount:
			return l.Money(d, 0)
		}
		return l.Money(d, 2)
	}
	opt := func(kind string, v *string) string {
		if v == nil {
			return "-"
		}
		return cell(kind, *v)
	}
	row := func(ln reports.FlashLine, bold, indent bool) flashRow {
		label := ln.Label
		for _, prefix := range []string{"method:", "refund:"} { // a payment method is a code: its name is printed
			if code, ok := strings.CutPrefix(ln.Key, prefix); ok {
				label = napMethod(code)
			}
		}
		return flashRow{bold: bold, indent: indent, cells: [8]string{
			label, cell(ln.Kind, ln.Today), cell(ln.Kind, ln.MTD), cell(ln.Kind, ln.LastMonth), cell(ln.Kind, ln.LastYear), opt(ln.Kind, ln.Budget), opt(ln.Kind, ln.Variance), cell(ln.Kind, ln.YTD),
		}}
	}
	rowsOf := func(ls []reports.FlashLine, bold, indent bool) []flashRow {
		out := make([]flashRow, 0, len(ls))
		for _, x := range ls {
			out = append(out, row(x, bold, indent))
		}
		return out
	}
	head := []string{"", "Today", "MTD", "MTD last month", "MTD last year", "Budget MTD", "Variance MTD", "YTD"}

	g.title("DAILY FLASH REPORT", l.Date(rep.BusinessDate))
	g.pairs([][2]string{
		{"Business date", l.Date(rep.BusinessDate)}, {"Currency", dc.prop.CurrencyCode},
		{"Month to date", l.Date(rep.Ranges.MTD.From) + " - " + l.Date(rep.Ranges.MTD.To)}, {"Year to date from", l.Date(rep.Ranges.YTD.From)},
	})
	if rep.Interim {
		g.stamp("INTERIM")
	}
	g.section("Room statistics")
	g.flashTable(rep.HasBudget, head, append(rowsOf(rep.Rooms, false, false), rowsOf(rep.SoldByType, false, true)...))
	g.section("Activity")
	g.flashTable(rep.HasBudget, head, rowsOf(rep.Activity, false, false))
	if len(rep.Covers) > 0 {
		g.section("Covers")
		g.flashTable(rep.HasBudget, head, rowsOf(rep.Covers, false, false))
	}
	g.section("Net revenue")
	var rev []flashRow
	for _, grp := range rep.Revenue.Groups {
		rev = append(rev, flashRow{bold: true, cells: [8]string{grp.Code + ". " + grp.Title}})
		for _, b := range grp.Blocks {
			if b.Title != "" {
				rev = append(rev, flashRow{bold: true, indent: true, cells: [8]string{b.Title}})
			}
			rev = append(rev, rowsOf(b.Rows, false, true)...)
		}
		rev = append(rev, row(grp.Total, true, false))
	}
	rev = append(rev, row(rep.Revenue.TotalNet, true, false), row(rep.Revenue.Service, false, false), row(rep.Revenue.Tax, false, false), row(rep.Revenue.GTR, true, false))
	if rep.Revenue.Journal != nil {
		rev = append(rev, row(*rep.Revenue.Journal, false, false))
	}
	g.flashTable(rep.HasBudget, head, rev)
	g.section("Revenue by source")
	g.flashTable(rep.HasBudget, head, rowsOf(rep.Revenue.BySource, false, false))
	g.section("Payments received (GTP)")
	pay := append([]flashRow{{bold: true, cells: [8]string{"By cashier"}}}, rowsOf(rep.Payments.ByCashier, false, true)...)
	pay = append(pay, flashRow{bold: true, cells: [8]string{"By payment method"}})
	pay = append(pay, rowsOf(rep.Payments.ByMethod, false, true)...)
	pay = append(pay, row(rep.Payments.GTP, true, false))
	pay = append(pay, rowsOf(rep.Payments.Refunds, false, true)...)
	pay = append(pay, row(rep.Payments.RefundsTotal, true, false), row(rep.Payments.CityLedger, false, false))
	g.flashTable(rep.HasBudget, head, pay)

	if c := rep.Control; c != nil {
		g.section("Control balance")
		cw := []col{{86, "", "L"}, {47, "Today", "R"}, {47, "MTD", "R"}}
		m := func(v string) string { return cell(reports.FlashMoney, v) }
		g.table(cw, [][]string{
			{"GTR", m(c.Today.GTR), m(c.MTD.GTR)},
			{"Payments received (GTP)", m(c.Today.GTP), m(c.MTD.GTP)},
			{"Refunds", m(c.Today.Refunds), m(c.MTD.Refunds)},
			{"Transferred to the city ledger", m(c.Today.CityLedger), m(c.MTD.CityLedger)},
			{"Expected change of the guest ledger", m(c.Today.Expected), m(c.MTD.Expected)},
			{"Change of the guest ledger", m(c.Today.ActualChange), m(c.MTD.ActualChange)},
			{"Difference", m(c.Today.Difference), m(c.MTD.Difference)},
		})
		if !c.Balanced {
			g.color(alert)
			g.note("The control balance has a difference: a row of the ledger did not follow the usual path.", "B", 9)
			g.color(ink)
		}
	}
	for _, w := range rep.Warnings {
		g.note(flashWarning(w), "I", 8.5)
	}
	if signatures && !rep.Interim {
		g.p.Ln(10)
		y := g.p.GetY() + 14
		g.p.SetXY(margin, y-8)
		g.font("", 9)
		g.p.CellFormat(80, 4, g.tr(rep.Signatures.PreparedBy), "", 0, "L", false, 0, "")
		g.signature(margin, y, 80, "Prepared by (night audit)")
		g.signature(margin+100, y, 80, "Approved by (accounting)")
	}
}

// flashWarning is the English text of a finding of the report.
func flashWarning(w reports.FlashWarning) string {
	switch w.Code {
	case "UNMAPPED_REVENUE":
		return "Some charge codes have no revenue account: their revenue is printed under Unmapped in group C. Fix the setup of the charge code."
	case "BANQUET_PAX_FALLBACK":
		return "The pax of " + w.Params["sessions"] + " banquet session(s) is the guaranteed number: no actual count was entered."
	case "BILLS_WITHOUT_COVERS":
		return w.Params["bills"] + " bill(s) of the month have no guest count: their covers are not counted."
	}
	return w.Code
}

func (h *Handler) registerFlash(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/reports/daily-flash.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		ctx := langCtx(r)
		date, err := h.svc.reports.FlashDateFrom(ctx, pid, r)
		if err != nil {
			return err
		}
		doc, err := h.svc.DailyFlashPDF(ctx, pid, date)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
}
