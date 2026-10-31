package auditpack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/auditpack/auditpackdb"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos"
	"kamarapms/internal/reports"
)

// job is one build: who asked, which day, and the context the reports are read with.
type job struct {
	ctx        context.Context // the reports are read as the tenant (see newJob)
	p          auth.Principal
	propertyID int64
	date       civil.Date
	decimals   int32
	late       bool
	notes      *nightaudit.PackNotes
}

// newJob prepares a build. The pack is the record of the hotel, not of the person who closed the day: the reports are read with the rights of the tenant administrator of that tenant (they need
// report.view, cashier.shift_manage and pos.report), and nothing is written with that context.
func (s *Service) newJob(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, late bool, notes *nightaudit.PackNotes) (*job, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rctx := auth.WithPrincipal(ctx, auth.Principal{TenantID: p.TenantID, UserID: p.UserID, SessionID: p.SessionID, IsTenantAdmin: true})
	return &job{ctx: rctx, p: p, propertyID: propertyID, date: bd, decimals: prop.CurrencyDecimals, late: late, notes: notes}, nil
}

// errOff is returned by a builder whose section does not apply to the property.
var errOff = apperr.Conflict("SECTION_OFF", "the section does not apply to this property")

// run builds the JSON of a section. off is true when the section does not apply.
func (s *Service) run(j *job, code string) (data []byte, off bool, err error) {
	var v any
	if f, ok := s.override[code]; ok {
		v, err = f(j.ctx)
	} else {
		v, err = s.build(j, code)
	}
	if errors.Is(err, errOff) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	data, err = json.Marshal(v)
	return data, false, err
}

func (s *Service) build(j *job, code string) (any, error) {
	ctx, pid, d := j.ctx, j.propertyID, j.date
	switch code {
	case SecSummary:
		return s.summary(j)
	case SecFlash:
		f, err := s.reports.DailyFlash(ctx, pid, d)
		if err != nil {
			return nil, err
		}
		if j.late {
			blankTomorrow(&f)
		}
		return f, nil
	case SecRevenue:
		return s.reports.Revenue(ctx, pid, d, d)
	case SecTax:
		return s.reports.Tax(ctx, pid, d, d)
	case SecPayments:
		return s.reports.Cashier(ctx, pid, d, d)
	case SecZReports:
		has, err := s.q(ctx).PropertyHasShifts(ctx, auditpackdb.PropertyHasShiftsParams{TenantID: j.p.TenantID, PropertyID: pid})
		if err != nil {
			return nil, err
		}
		if !has {
			return nil, errOff
		}
		return s.reports.ZReports(ctx, pid, d)
	case SecGuestLedger:
		return s.reports.GuestLedger(ctx, pid, &d)
	case SecDepositLedger:
		return s.reports.DepositLedger(ctx, pid, &d)
	case SecCorrections:
		return s.reports.Corrections(ctx, pid, reports.CorrectionParams{From: d, To: d})
	case SecCancellations:
		return s.reports.Cancellations(ctx, pid, reports.CancellationParams{From: d, To: d})
	case SecOutlets:
		return s.outletsSection(j)
	case SecOutOfOrder:
		return s.outOfOrder(j)
	case SecInHouse:
		return s.reports.InHouse(ctx, pid)
	case SecNextDay:
		arr, err := s.reports.Arrivals(ctx, pid, d.AddDays(1))
		if err != nil {
			return nil, err
		}
		dep, err := s.reports.Departures(ctx, pid, d.AddDays(1))
		if err != nil {
			return nil, err
		}
		return map[string]any{"date": d.AddDays(1), "arrivals": arr, "departures": dep}, nil
	case SecAuditNotes:
		n := nightaudit.PackNotes{}
		if j.notes != nil {
			n = *j.notes
		}
		return n, nil
	}
	return nil, fmt.Errorf("auditpack: unknown section %q", code)
}

// blankTomorrow empties the two rows about tomorrow: the books of the day after are not what they were at the close (38 A8), and a pack that is made later cannot tell.
func blankTomorrow(f *reports.DailyFlash) {
	for i := range f.Activity {
		if k := f.Activity[i].Key; k == "arrivals_tomorrow" || k == "departures_tomorrow" {
			f.Activity[i].Today = ""
		}
	}
}

// DaySummary is the section SUMMARY: the summary the close stored, who closed the day and when.
type DaySummary struct {
	BusinessDate civil.Date      `json:"business_date"`
	ClosedAt     *time.Time      `json:"closed_at"`
	ClosedBy     string          `json:"closed_by"`
	Summary      json.RawMessage `json:"summary"`
}

func (s *Service) summary(j *job) (any, error) {
	row, err := s.q(j.ctx).GetDay(j.ctx, auditpackdb.GetDayParams{TenantID: j.p.TenantID, PropertyID: j.propertyID, Bd: j.date})
	if err != nil {
		return nil, err
	}
	if row.Status != "CLOSED" || len(row.Summary) == 0 {
		return nil, fmt.Errorf("the business day %s has no closing summary", j.date)
	}
	return DaySummary{BusinessDate: j.date, ClosedAt: row.ClosedAt, ClosedBy: row.ClosedByName, Summary: row.Summary}, nil
}

// OutOfOrderRoom is a room that was out of order or out of service on the night: the block, half open [from, until).
type OutOfOrderRoom struct {
	Room     string     `json:"room"`
	RoomType string     `json:"room_type"`
	Kind     string     `json:"kind"` // OOO or OOS
	From     civil.Date `json:"from"`
	Until    civil.Date `json:"until"`
	Reason   string     `json:"reason"`
}

// OutOfOrderData is the section OUT_OF_ORDER.
type OutOfOrderData struct {
	Date         civil.Date       `json:"date"`
	OutOfOrder   int              `json:"out_of_order"`
	OutOfService int              `json:"out_of_service"`
	Rooms        []OutOfOrderRoom `json:"rooms"`
}

func (s *Service) outOfOrder(j *job) (any, error) {
	rows, err := s.q(j.ctx).ListRoomBlocksOn(j.ctx, auditpackdb.ListRoomBlocksOnParams{TenantID: j.p.TenantID, PropertyID: j.propertyID, Bd: j.date})
	if err != nil {
		return nil, err
	}
	out := OutOfOrderData{Date: j.date, Rooms: []OutOfOrderRoom{}}
	for _, r := range rows {
		if r.BlockType == "OOO" {
			out.OutOfOrder++
		} else {
			out.OutOfService++
		}
		out.Rooms = append(out.Rooms, OutOfOrderRoom{Room: r.RoomNumber, RoomType: r.RoomTypeCode, Kind: r.BlockType, From: r.StartDate, Until: r.EndDate, Reason: r.Reason})
	}
	return out, nil
}

// Count is a number of events and what they came to.
type Count struct {
	Count  int    `json:"count"`
	Amount string `json:"amount"`
}

// OutletRow is the day of one outlet.
type OutletRow struct {
	OutletID           int64  `json:"outlet_id"`
	Code               string `json:"code"`
	Name               string `json:"name"`
	Bills              int    `json:"bills"`
	Net                string `json:"net"`
	Service            string `json:"service"`
	Tax                string `json:"tax"`
	Total              string `json:"total"`
	Covers             int    `json:"covers"`
	BillsWithoutCovers int    `json:"bills_without_covers"`
	Voids              Count  `json:"voids"`
	Comps              Count  `json:"comps"`
	Discounts          Count  `json:"discounts"`
}

// OutletsData is the section OUTLETS.
type OutletsData struct {
	Date    civil.Date  `json:"date"`
	Outlets []OutletRow `json:"outlets"`
	Total   OutletRow   `json:"total"`
}

func (s *Service) outletsSection(j *job) (any, error) {
	if s.outlets == nil || s.modules == nil {
		return nil, errOff
	}
	has, err := s.modules.HasPOS(j.ctx, j.p.TenantID)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, errOff
	}
	list, err := s.outlets.ListOutlets(j.ctx, j.propertyID, nil)
	if err != nil {
		return nil, err
	}
	covers, err := s.q(j.ctx).OutletCoversOn(j.ctx, auditpackdb.OutletCoversOnParams{TenantID: j.p.TenantID, PropertyID: j.propertyID, Bd: j.date})
	if err != nil {
		return nil, err
	}
	byOutlet := map[int64]auditpackdb.OutletCoversOnRow{}
	for _, c := range covers {
		byOutlet[c.OutletID] = c
	}
	fx := func(d decimal.Decimal) string { return d.StringFixed(j.decimals) }
	count := func(es []pos.ReportEvent) (Count, decimal.Decimal) {
		t := decimal.Zero
		for _, e := range es {
			t = t.Add(e.Amount)
		}
		return Count{Count: len(es), Amount: fx(t)}, t
	}
	out := OutletsData{Date: j.date, Outlets: []OutletRow{}}
	var tot struct {
		bills, covers, noCovers    int
		net, service, taxes, total decimal.Decimal
		voids, comps, discounts    struct {
			n int
			a decimal.Decimal
		}
	}
	for _, o := range list {
		rep, err := s.outlets.Report(j.ctx, j.propertyID, o.ID, j.date, j.date)
		if err != nil {
			return nil, err
		}
		c := byOutlet[o.ID]
		if !o.IsActive && rep.Totals.Bills == 0 && len(rep.Voids) == 0 && len(rep.Comps) == 0 && len(rep.Discounts) == 0 {
			continue
		}
		v, va := count(rep.Voids)
		cp, ca := count(rep.Comps)
		dc, da := count(rep.Discounts)
		row := OutletRow{OutletID: o.ID, Code: o.Code, Name: o.Name, Bills: rep.Totals.Bills, Net: fx(rep.Totals.Net), Service: fx(rep.Totals.Service), Tax: fx(rep.Totals.Tax),
			Total: fx(rep.Totals.Total), Covers: int(c.Covers), BillsWithoutCovers: int(c.BillsWithoutCovers), Voids: v, Comps: cp, Discounts: dc}
		out.Outlets = append(out.Outlets, row)
		tot.bills += row.Bills
		tot.covers += row.Covers
		tot.noCovers += row.BillsWithoutCovers
		tot.net, tot.service, tot.taxes, tot.total = tot.net.Add(rep.Totals.Net), tot.service.Add(rep.Totals.Service), tot.taxes.Add(rep.Totals.Tax), tot.total.Add(rep.Totals.Total)
		tot.voids.n, tot.voids.a = tot.voids.n+v.Count, tot.voids.a.Add(va)
		tot.comps.n, tot.comps.a = tot.comps.n+cp.Count, tot.comps.a.Add(ca)
		tot.discounts.n, tot.discounts.a = tot.discounts.n+dc.Count, tot.discounts.a.Add(da)
	}
	out.Total = OutletRow{Name: "Total", Bills: tot.bills, Net: fx(tot.net), Service: fx(tot.service), Tax: fx(tot.taxes), Total: fx(tot.total), Covers: tot.covers, BillsWithoutCovers: tot.noCovers,
		Voids: Count{tot.voids.n, fx(tot.voids.a)}, Comps: Count{tot.comps.n, fx(tot.comps.a)}, Discounts: Count{tot.discounts.n, fx(tot.discounts.a)}}
	return out, nil
}
