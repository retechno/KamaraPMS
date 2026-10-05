package documents

import (
	"context"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/shifts"
)

// SetShifts gives the documents the cashier shift service, for the Z report.
func (s *Service) SetShifts(sh *shifts.Service) { s.shifts = sh }

var shiftCols = []col{{82, "", "L"}, {56, "", "L"}, {42, "", "R"}}

// ShiftReportPDF is the report of a cashier shift: the Z report of a closed shift (final, never changes) or the X report of an open one (the figures so far).
// It has the cash reconciliation, the count by denomination, the other tenders the cashier took, the cash payments and the movements of the drawer.
func (s *Service) ShiftReportPDF(ctx context.Context, propertyID, id int64) (Document, error) {
	rep, err := s.shifts.Report(ctx, propertyID, id)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	l, dec := dc.lang, dc.decimals
	money := func(v string) string {
		d, err := decimal.NewFromString(v)
		if err != nil {
			return v
		}
		return l.Money(d, dec)
	}
	var rows [][]string
	bold := map[int]bool{}
	heading := func(t string) {
		bold[len(rows)] = true
		rows = append(rows, []string{l.T(t), "", ""})
	}
	add := func(label, detail, amount string) { rows = append(rows, []string{label, detail, amount}) }
	total := func(label, amount string) {
		bold[len(rows)] = true
		rows = append(rows, []string{label, "", amount})
	}
	sh, c := rep.Shift, rep.Shift.Cash
	if c != nil {
		heading("Cash reconciliation")
		add(l.T("Opening float"), "", money(c.OpeningFloat))
		add(l.T("Cash payments"), "", money(c.Payments))
		add(l.T("Cash refunds"), "", "-"+money(c.Refunds))
		add(l.T("Receipts of the city ledger"), "", money(c.Receipts))
		add(l.T("Voided from earlier shifts"), "", "-"+money(c.VoidedAfterClose))
		add(l.T("Pay-ins"), "", money(c.PayIns))
		add(l.T("Pay-outs"), "", "-"+money(c.PayOuts))
		add(l.T("Drops to the safe"), "", "-"+money(c.Drops))
		total(l.T("Expected cash"), money(c.Expected))
		if sh.CountedCash != nil {
			total(l.T("Counted cash"), money(*sh.CountedCash))
		}
		if sh.OverShort != nil {
			over, _ := decimal.NewFromString(*sh.OverShort)
			label := l.T("Over")
			if over.IsNegative() {
				label = l.T("Short")
			}
			if over.IsZero() {
				label = l.T("Over / short")
			}
			total(label, money(*sh.OverShort))
			if sh.VarianceReason != "" {
				add(l.T("Reason"), sh.VarianceReason, "")
			}
		}
	}
	if len(sh.Counts) > 0 {
		heading("Count by denomination")
		for _, k := range sh.Counts {
			d, _ := decimal.NewFromString(k.Denomination)
			add(money(k.Denomination), "x "+decimal.NewFromInt(int64(k.Quantity)).String(), l.Money(d.Mul(decimal.NewFromInt(int64(k.Quantity))), dec))
		}
	}
	if len(rep.OtherTenders) > 0 {
		heading("Other tenders taken by the cashier (not drawer cash)")
		for _, t := range rep.OtherTenders {
			sign := ""
			if t.Kind == "REFUND" {
				sign = "-"
			}
			add(l.T(methodLabel(t.Method)), l.T(strings.ToLower(t.Kind))+" x "+decimal.NewFromInt(int64(t.Count)).String(), sign+money(t.Amount))
		}
	}
	if len(rep.CashPayments) > 0 {
		heading("Cash payments and receipts of the shift")
		for _, p := range rep.CashPayments {
			detail := l.T(strings.ToLower(p.Kind)) + " · " + l.Time(p.PaidAt, dc.prop.Location())
			if p.Reference != "" {
				detail += " · " + p.Reference
			}
			if p.Status == "VOIDED" {
				detail += " · " + l.T("voided")
			}
			add(p.Number, detail, money(p.Amount))
		}
	}
	if len(sh.Movements) > 0 {
		heading("Movements of the drawer")
		for _, m := range sh.Movements {
			what := m.Reason
			if m.AccountCode != "" {
				what = m.AccountCode + " " + m.AccountName + " · " + m.Reason
			}
			add(l.T(movementLabel(m.Kind))+" · "+l.Date(m.BusinessDate), what, money(m.Amount))
		}
	}

	title := "Z REPORT"
	note := "A Z report is final: it is made from the shift as it was closed and does not change."
	if rep.Kind == "X" {
		title = "X REPORT"
		note = "An X report is a reading of an open shift: it closes nothing, and the figures change until the shift is closed."
	}
	pairs := [][2]string{
		{l.T("Shift"), sh.Number}, {l.T("Cashier"), sh.UserName}, {l.T("Drawer"), sh.Drawer},
		{l.T("Opened"), l.Time(sh.OpenedAt, dc.prop.Location()) + " (" + l.Date(sh.BusinessDateOpened) + ")"},
	}
	if sh.ClosedAt != nil && sh.BusinessDateClosed != nil {
		pairs = append(pairs, [2]string{l.T("Closed"), l.Time(*sh.ClosedAt, dc.prop.Location()) + " (" + l.Date(*sh.BusinessDateClosed) + ")"})
	}
	if rep.ClosedByName != "" {
		pairs = append(pairs, [2]string{l.T("Closed by"), rep.ClosedByName})
	}
	if rep.ApprovedByName != "" {
		pairs = append(pairs, [2]string{l.T("Variance approved by"), rep.ApprovedByName})
	}
	if rep.HandedOverToName != "" {
		pairs = append(pairs, [2]string{l.T("Drawer handed over to"), rep.HandedOverToName})
	}
	pairs = append(pairs, [2]string{l.T("Currency"), dc.prop.CurrencyCode})
	d := FinancialDoc{Hotel: dc.hotel, Lang: l, Printed: dc.printed, Title: l.T(title), Subtitle: l.T("Cashier shift") + " " + sh.Number, Pairs: pairs, Cols: shiftCols, Rows: rows, Bold: bold, Notes: []string{l.T(note)}}
	pdf, err := RenderFinancial(d)
	return Document{Filename: strings.ToLower(rep.Kind) + "-report-" + sh.Number + ".pdf", PDF: pdf}, err
}

func movementLabel(k string) string {
	switch k {
	case shifts.KindDrop:
		return "Drop"
	case shifts.KindPayIn:
		return "Pay-in"
	case shifts.KindPayOut:
		return "Pay-out"
	}
	return k
}

func (h *Handler) registerShifts(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/cashier/shifts/{id}/report.pdf", httpx.HandlerFunc(h.serve(h.svc.ShiftReportPDF)))
}
