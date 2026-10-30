package reports

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
	"kamarapms/internal/shifts"
)

// The Z report of the day for all drawers (docs/architecture/38-reports-night-audit-pack.md, 7.5): one block for each cashier shift that closed on the date or was open at any time of it, with the
// figures of the report of the shift itself, a consolidation by drawer, and the check against the cashier report.

// SetShifts gives the reports the shift service: the blocks are the reports of the shifts, so the two never disagree.
func (s *Service) SetShifts(sh *shifts.Service) { s.shifts = sh }

// ZBlock is the report of one shift: Z when it is closed, X (interim) while it is open.
type ZBlock struct {
	Kind               string          `json:"kind"`
	ShiftID            int64           `json:"shift_id"`
	Number             string          `json:"number"`
	Drawer             string          `json:"drawer"`
	Cashier            string          `json:"cashier"`
	OpenedAt           time.Time       `json:"opened_at"`
	ClosedAt           *time.Time      `json:"closed_at"`
	BusinessDateOpened civil.Date      `json:"business_date_opened"`
	BusinessDateClosed *civil.Date     `json:"business_date_closed"`
	OpeningFloat       string          `json:"opening_float"`
	CashPayments       string          `json:"cash_payments"`
	CashRefunds        string          `json:"cash_refunds"`
	CashReceipts       string          `json:"cash_receipts"`
	VoidedAfterClose   string          `json:"voided_after_close"`
	PayIns             string          `json:"pay_ins"`
	PayOuts            string          `json:"pay_outs"`
	Drops              string          `json:"drops"`
	ExpectedCash       string          `json:"expected_cash"`
	CountedCash        *string         `json:"counted_cash"`
	OverShort          *string         `json:"over_short"`
	VarianceReason     string          `json:"variance_reason"`
	ClosedBy           string          `json:"closed_by"`
	ApprovedBy         string          `json:"approved_by"`
	HandedOverTo       string          `json:"handed_over_to"`
	Tenders            []shifts.Tender `json:"tenders"`
	TotalTakings       string          `json:"total_takings"`
}

// ZDrawer is the consolidation of the shifts of one drawer, or of the property (Drawer empty).
type ZDrawer struct {
	Drawer           string  `json:"drawer"`
	Shifts           int     `json:"shifts"`
	OpeningFloat     string  `json:"opening_float"`
	CashPayments     string  `json:"cash_payments"`
	CashRefunds      string  `json:"cash_refunds"`
	CashReceipts     string  `json:"cash_receipts"`
	VoidedAfterClose string  `json:"voided_after_close"`
	PayIns           string  `json:"pay_ins"`
	PayOuts          string  `json:"pay_outs"`
	Drops            string  `json:"drops"`
	ExpectedCash     string  `json:"expected_cash"`
	CountedCash      *string `json:"counted_cash"`
	OverShort        *string `json:"over_short"`
	TotalTakings     string  `json:"total_takings"`
}

// ZTaking is what was taken by one method on the date, on a shift and without one, against the cashier report.
type ZTaking struct {
	Method        string `json:"method"`
	OnShift       string `json:"on_shift"`
	OffShift      string `json:"off_shift"`
	Total         string `json:"total"`
	CashierReport string `json:"cashier_report"`
	Difference    string `json:"difference"`
}

// ZReport is the report of the day.
type ZReport struct {
	Date             civil.Date `json:"date"`
	Blocks           []ZBlock   `json:"blocks"`
	Drawers          []ZDrawer  `json:"drawers"`
	Property         ZDrawer    `json:"property"`
	CashOffShift     string     `json:"cash_off_shift"`
	VoidedAfterClose string     `json:"voided_after_close"`
	Takings          []ZTaking  `json:"takings"`
	Reconciled       bool       `json:"reconciled"`
}

// CSV is one row for each shift.
func (r ZReport) CSV() ([]string, [][]string) {
	h := []string{"kind", "shift", "drawer", "cashier", "opened_at", "closed_at", "opening_float", "cash_payments", "cash_refunds", "cash_receipts", "voided_after_close", "pay_ins", "pay_outs", "drops", "expected_cash",
		"counted_cash", "over_short", "variance_reason", "approved_by", "handed_over_to", "total_takings"}
	rows := make([][]string, 0, len(r.Blocks))
	for _, b := range r.Blocks {
		closed := ""
		if b.ClosedAt != nil {
			closed = b.ClosedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{b.Kind, b.Number, b.Drawer, b.Cashier, b.OpenedAt.UTC().Format(time.RFC3339), closed, b.OpeningFloat, b.CashPayments, b.CashRefunds, b.CashReceipts, b.VoidedAfterClose,
			b.PayIns, b.PayOuts, b.Drops, b.ExpectedCash, optStr(b.CountedCash), optStr(b.OverShort), b.VarianceReason, b.ApprovedBy, b.HandedOverTo, b.TotalTakings})
	}
	return h, rows
}

// ZDateFrom checks report.view and cashier.shift_manage and then reads the date: the permissions are answered before the query is looked at. Without a date it is the last closed business day.
func (s *Service) ZDateFrom(ctx context.Context, propertyID int64, r *http.Request) (civil.Date, error) {
	if _, _, err := s.access(ctx, propertyID); err != nil {
		return civil.Date{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermCashierShiftManage); err != nil {
		return civil.Date{}, err
	}
	return s.FlashDateFrom(ctx, propertyID, r)
}

func dec(v string) decimal.Decimal {
	d, _ := decimal.NewFromString(v)
	return d
}

// ZReports answers the Z report of all drawers of a business date (report.view and cashier.shift_manage).
func (s *Service) ZReports(ctx context.Context, propertyID int64, day civil.Date) (ZReport, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return ZReport{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermCashierShiftManage); err != nil {
		return ZReport{}, err
	}
	q := s.q(ctx)
	if _, err := q.ReportBusinessDayStatus(ctx, reportsdb.ReportBusinessDayStatusParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: day}); err != nil {
		return ZReport{}, businessDayErr(err)
	}
	ids, err := q.ZShiftIDs(ctx, reportsdb.ZShiftIDsParams{TenantID: p.TenantID, PropertyID: propertyID, Day: day})
	if err != nil {
		return ZReport{}, err
	}
	out := ZReport{Date: day, Blocks: []ZBlock{}, Drawers: []ZDrawer{}, Takings: []ZTaking{}}
	type sum struct {
		d                                                                               ZDrawer
		float, pay, ref, rec, voided, payIn, payOut, drops, exp, counted, over, takings decimal.Decimal
		anyCounted                                                                      bool
	}
	byDrawer := map[string]*sum{}
	var order []string
	total := &sum{}
	for _, id := range ids {
		rep, err := s.shifts.Report(ctx, propertyID, id)
		if err != nil {
			return ZReport{}, err
		}
		sh := rep.Shift
		cash := shifts.Cash{}
		if sh.Cash != nil {
			cash = *sh.Cash
		}
		b := ZBlock{Kind: rep.Kind, ShiftID: sh.ID, Number: sh.Number, Drawer: sh.Drawer, Cashier: sh.UserName, OpenedAt: sh.OpenedAt, ClosedAt: sh.ClosedAt, BusinessDateOpened: sh.BusinessDateOpened,
			BusinessDateClosed: sh.BusinessDateClosed, OpeningFloat: cash.OpeningFloat, CashPayments: cash.Payments, CashRefunds: cash.Refunds, CashReceipts: cash.Receipts, VoidedAfterClose: cash.VoidedAfterClose,
			PayIns: cash.PayIns, PayOuts: cash.PayOuts, Drops: cash.Drops, ExpectedCash: cash.Expected, CountedCash: sh.CountedCash, OverShort: sh.OverShort, VarianceReason: sh.VarianceReason,
			ClosedBy: rep.ClosedByName, ApprovedBy: rep.ApprovedByName, HandedOverTo: rep.HandedOverToName, Tenders: rep.OtherTenders}
		if b.OpeningFloat == "" {
			b.OpeningFloat = fx(decimal.Zero, decimals)
		}
		if b.ExpectedCash == "" && sh.ExpectedCash != nil {
			b.ExpectedCash = *sh.ExpectedCash
		}
		takings := dec(cash.Payments).Sub(dec(cash.Refunds)).Add(dec(cash.Receipts))
		for _, t := range rep.OtherTenders {
			if t.Kind == "REFUND" {
				takings = takings.Sub(dec(t.Amount))
			} else {
				takings = takings.Add(dec(t.Amount))
			}
		}
		b.TotalTakings = fx(takings, decimals)
		out.Blocks = append(out.Blocks, b)
		d := byDrawer[sh.Drawer]
		if d == nil {
			d = &sum{}
			byDrawer[sh.Drawer] = d
			order = append(order, sh.Drawer)
		}
		for _, x := range []*sum{d, total} {
			x.d.Shifts++
			x.float, x.pay, x.ref, x.rec = x.float.Add(dec(b.OpeningFloat)), x.pay.Add(dec(b.CashPayments)), x.ref.Add(dec(b.CashRefunds)), x.rec.Add(dec(b.CashReceipts))
			x.voided, x.payIn, x.payOut, x.drops = x.voided.Add(dec(b.VoidedAfterClose)), x.payIn.Add(dec(b.PayIns)), x.payOut.Add(dec(b.PayOuts)), x.drops.Add(dec(b.Drops))
			x.exp, x.takings = x.exp.Add(dec(b.ExpectedCash)), x.takings.Add(takings)
			if b.CountedCash != nil {
				x.anyCounted = true
				x.counted = x.counted.Add(dec(*b.CountedCash))
				if b.OverShort != nil {
					x.over = x.over.Add(dec(*b.OverShort))
				}
			}
		}
	}
	finish := func(name string, x *sum) ZDrawer {
		z := ZDrawer{Drawer: name, Shifts: x.d.Shifts, OpeningFloat: fx(x.float, decimals), CashPayments: fx(x.pay, decimals), CashRefunds: fx(x.ref, decimals), CashReceipts: fx(x.rec, decimals),
			VoidedAfterClose: fx(x.voided, decimals), PayIns: fx(x.payIn, decimals), PayOuts: fx(x.payOut, decimals), Drops: fx(x.drops, decimals), ExpectedCash: fx(x.exp, decimals), TotalTakings: fx(x.takings, decimals)}
		if x.anyCounted {
			c, o := fx(x.counted, decimals), fx(x.over, decimals)
			z.CountedCash, z.OverShort = &c, &o
		}
		return z
	}
	sort.Strings(order)
	for _, name := range order {
		out.Drawers = append(out.Drawers, finish(name, byDrawer[name]))
	}
	out.Property = finish("", total)
	out.VoidedAfterClose = fx(total.voided, decimals)

	// The check: what was taken on the date, by method, on a shift and without one, against the cashier report of the same date.
	tk, err := q.ZTakingsByMethod(ctx, reportsdb.ZTakingsByMethodParams{TenantID: p.TenantID, PropertyID: propertyID, Day: day})
	if err != nil {
		return ZReport{}, err
	}
	cashier, err := s.Cashier(ctx, propertyID, day, day)
	if err != nil {
		return ZReport{}, err
	}
	rep := map[string]decimal.Decimal{}
	for _, m := range cashier.ByMethod {
		rep[m.Method] = dec(m.Net)
	}
	out.CashOffShift = fx(decimal.Zero, decimals)
	out.Reconciled = true
	seen := map[string]bool{}
	for _, r := range tk {
		seen[r.Method] = true
		t := r.OnShift.Add(r.OffShift)
		diff := t.Sub(rep[r.Method])
		out.Takings = append(out.Takings, ZTaking{Method: r.Method, OnShift: fx(r.OnShift, decimals), OffShift: fx(r.OffShift, decimals), Total: fx(t, decimals), CashierReport: fx(rep[r.Method], decimals), Difference: fx(diff, decimals)})
		if r.Method == "CASH" {
			out.CashOffShift = fx(r.OffShift, decimals)
		}
		if !diff.IsZero() {
			out.Reconciled = false
		}
	}
	for m, v := range rep { // a method the cashier report has and the takings do not
		if !seen[m] && !v.IsZero() {
			out.Takings = append(out.Takings, ZTaking{Method: m, OnShift: fx(decimal.Zero, decimals), OffShift: fx(decimal.Zero, decimals), Total: fx(decimal.Zero, decimals), CashierReport: fx(v, decimals), Difference: fx(v.Neg(), decimals)})
			out.Reconciled = false
		}
	}
	sort.SliceStable(out.Takings, func(i, j int) bool { return out.Takings[i].Method < out.Takings[j].Method })
	return out, nil
}
