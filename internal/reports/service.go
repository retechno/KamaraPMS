package reports

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reports/reportsdb"
	"kamarapms/internal/tenancy"
)

// Service runs the reports (report.view). Every method is a read: no locks, no writes.
type Service struct {
	txm   *db.TxManager
	authz auth.Authorizer
	days  *tenancy.Service
	audit *nightaudit.Service
}

// NewService wires the service. audit is used only to compute the live summary of the open business day.
func NewService(txm *db.TxManager, authz auth.Authorizer, days *tenancy.Service, a *nightaudit.Service) *Service {
	return &Service{txm: txm, authz: authz, days: days, audit: a}
}

func (s *Service) q(ctx context.Context) *reportsdb.Queries { return reportsdb.New(s.txm.DB(ctx)) }

// access authorizes the caller and returns the principal and the property's currency decimals.
func (s *Service) access(ctx context.Context, propertyID int64) (auth.Principal, int32, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, 0, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermReportView); err != nil {
		return p, 0, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return p, 0, err
	}
	return p, prop.CurrencyDecimals, nil
}

func checkRange(from, to civil.Date) error {
	if to.Before(from) {
		return apperr.Invalid("the range is invalid", apperr.FieldError{Field: "to", Code: "OUT_OF_RANGE", Message: "on or after from"})
	}
	if from.DaysUntil(to)+1 > MaxRangeDays {
		return apperr.Invalid("the range is invalid", apperr.FieldError{Field: "to", Code: "OUT_OF_RANGE", Message: "at most 366 days"})
	}
	return nil
}

func fx(d decimal.Decimal, decimals int32) string { return d.StringFixed(decimals) }

// DailySummary returns the closing summary of a business date: the stored one of a closed day, or a live
// computation for the open day (404 BUSINESS_DAY_NOT_FOUND for a date that was never a business day).
func (s *Service) DailySummary(ctx context.Context, propertyID int64, bd civil.Date) (DailySummary, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return DailySummary{}, err
	}
	row, err := s.q(ctx).GetBusinessDay(ctx, reportsdb.GetBusinessDayParams{PropertyID: propertyID, Bd: bd})
	if errors.Is(err, pgx.ErrNoRows) {
		return DailySummary{}, apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "the property has no business day on that date")
	}
	if err != nil {
		return DailySummary{}, err
	}
	out := DailySummary{BusinessDate: row.BusinessDate, Status: row.Status}
	if row.Status == "OPEN" {
		sum, err := s.audit.Summarize(ctx, p, propertyID, bd, decimals, 0)
		if err != nil {
			return DailySummary{}, err
		}
		out.Live, out.Summary = true, &sum
		return out, nil
	}
	out.Summary, _ = parseSummary(row.Summary)
	return out, nil
}

// Revenue lists the revenue of a business-date range by charge code, with totals and the totals by charge type.
// Items are signed: adjustments and reversals net out.
func (s *Service) Revenue(ctx context.Context, propertyID int64, from, to civil.Date) (Revenue, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return Revenue{}, err
	}
	if err := checkRange(from, to); err != nil {
		return Revenue{}, err
	}
	rows, err := s.q(ctx).RevenueByChargeCode(ctx, reportsdb.RevenueByChargeCodeParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return Revenue{}, err
	}
	out := Revenue{From: from, To: to, ByCode: []RevenueLine{}, ByType: []TypeTotal{}}
	type acc struct {
		items                    int
		net, service, tax, total decimal.Decimal
	}
	var all acc
	byType := map[string]*acc{}
	var order []string
	for _, r := range rows {
		out.ByCode = append(out.ByCode, RevenueLine{
			ChargeCodeID: r.ChargeCodeID, ChargeCode: r.Code, Name: r.Name, ChargeType: r.ChargeType, RevenueAccountCode: r.RevenueAccountCode, Items: int(r.Items),
			Base: fx(r.Base, decimals), Discount: fx(r.Discount, decimals), Net: fx(r.Net, decimals), Service: fx(r.Service, decimals), Tax: fx(r.Tax, decimals), Total: fx(r.Total, decimals),
		})
		a := byType[r.ChargeType]
		if a == nil {
			a = &acc{}
			byType[r.ChargeType] = a
			order = append(order, r.ChargeType)
		}
		for _, t := range []*acc{a, &all} {
			t.items += int(r.Items)
			t.net, t.service, t.tax, t.total = t.net.Add(r.Net), t.service.Add(r.Service), t.tax.Add(r.Tax), t.total.Add(r.Total)
		}
	}
	mk := func(a acc) RevenueTotals {
		return RevenueTotals{Items: a.items, Net: fx(a.net, decimals), Service: fx(a.service, decimals), Tax: fx(a.tax, decimals), Total: fx(a.total, decimals)}
	}
	for _, t := range order {
		out.ByType = append(out.ByType, TypeTotal{ChargeType: t, RevenueTotals: mk(*byType[t])})
	}
	out.Totals = mk(all)
	return out, nil
}

// Tax lists taxes and service charges collected in a range from the component snapshots.
func (s *Service) Tax(ctx context.Context, propertyID int64, from, to civil.Date) (TaxReport, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return TaxReport{}, err
	}
	if err := checkRange(from, to); err != nil {
		return TaxReport{}, err
	}
	rows, err := s.q(ctx).TaxReport(ctx, reportsdb.TaxReportParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return TaxReport{}, err
	}
	out := TaxReport{From: from, To: to, Taxes: []TaxLine{}, ServiceCharges: []TaxLine{}}
	tax, svc := decimal.Zero, decimal.Zero
	for _, r := range rows {
		l := TaxLine{ComponentType: r.ComponentType, Code: r.Code, Name: r.Name, Rate: r.Rate.StringFixed(4), GLAccountCode: r.GlAccountCode, Items: int(r.Items), Base: fx(r.Base, decimals), Amount: fx(r.Amount, decimals)}
		if r.ComponentType == "TAX" {
			out.Taxes, tax = append(out.Taxes, l), tax.Add(r.Amount)
		} else {
			out.ServiceCharges, svc = append(out.ServiceCharges, l), svc.Add(r.Amount)
		}
	}
	out.TaxTotal, out.ServiceTotal = fx(tax, decimals), fx(svc, decimals)
	return out, nil
}

// Cashier lists payments by business date and method, with refunds netted and voids shown apart.
func (s *Service) Cashier(ctx context.Context, propertyID int64, from, to civil.Date) (Cashier, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return Cashier{}, err
	}
	if err := checkRange(from, to); err != nil {
		return Cashier{}, err
	}
	rows, err := s.q(ctx).CashierByMethod(ctx, reportsdb.CashierByMethodParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return Cashier{}, err
	}
	out := Cashier{From: from, To: to, Lines: []CashierLine{}, ByMethod: []MethodSum{}}
	type acc struct{ pay, ref decimal.Decimal }
	by := map[string]*acc{}
	var order []string
	net := decimal.Zero
	for _, r := range rows {
		n := r.Payments.Sub(r.Refunds)
		out.Lines = append(out.Lines, CashierLine{BusinessDate: r.BusinessDate, Method: r.PaymentMethod, Payments: fx(r.Payments, decimals), Refunds: fx(r.Refunds, decimals),
			Net: fx(n, decimals), Count: int(r.Count), Voided: fx(r.Voided, decimals), VoidedCount: int(r.VoidedCount)})
		a := by[r.PaymentMethod]
		if a == nil {
			a = &acc{}
			by[r.PaymentMethod] = a
			order = append(order, r.PaymentMethod)
		}
		a.pay, a.ref = a.pay.Add(r.Payments), a.ref.Add(r.Refunds)
		if r.PaymentMethod != methodCityLedger { // a transfer to a company is not money received
			net = net.Add(n)
		}
	}
	for _, m := range order {
		a := by[m]
		out.ByMethod = append(out.ByMethod, MethodSum{Method: m, Payments: fx(a.pay, decimals), Refunds: fx(a.ref, decimals), Net: fx(a.pay.Sub(a.ref), decimals)})
	}
	out.Net = fx(net, decimals)
	return out, nil
}

// methodCityLedger is the payment method of a transfer to a company account.
const methodCityLedger = "CITY_LEDGER"

// Statistics is the occupancy and statistics report over the closed days of a range (the stored summaries).
func (s *Service) Statistics(ctx context.Context, propertyID int64, from, to civil.Date) (Statistics, error) {
	_, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return Statistics{}, err
	}
	if err := checkRange(from, to); err != nil {
		return Statistics{}, err
	}
	rows, err := s.q(ctx).ListClosedDaySummaries(ctx, reportsdb.ListClosedDaySummariesParams{PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return Statistics{}, err
	}
	out := Statistics{From: from, To: to, Days: []StatDay{}}
	var available, occupied, sold int
	revenue := decimal.Zero
	for _, r := range rows {
		sum, ok := parseSummary(r.Summary)
		if !ok {
			continue
		}
		rev, _ := decimal.NewFromString(sum.RoomRevenue.Net)
		out.Days = append(out.Days, StatDay{
			BusinessDate: r.BusinessDate, RoomsTotal: sum.Rooms.Total, OutOfOrder: sum.Rooms.OutOfOrder, Sellable: sum.Rooms.Sellable, Occupied: sum.Rooms.Occupied,
			RoomNightsSold: sum.Rooms.Sold, Arrivals: sum.Arrivals, Departures: sum.Departures, NoShows: sum.NoShows, RoomRevenue: sum.RoomRevenue.Net,
			OccupancyPercent: sum.OccupancyPercent, ADR: sum.ADR, RevPAR: sum.RevPAR,
		})
		available += sum.Rooms.Total - sum.Rooms.OutOfOrder
		occupied += sum.Rooms.Occupied
		sold += sum.Rooms.Sold
		revenue = revenue.Add(rev)
	}
	zero := fx(decimal.Zero, decimals)
	t := StatTotals{Days: len(out.Days), AvailableNights: available, OccupiedNights: occupied, RoomNightsSold: sold, RoomRevenue: fx(revenue, decimals), OccupancyPercent: "0.00", ADR: zero, RevPAR: zero}
	if available > 0 {
		av := decimal.NewFromInt(int64(available))
		t.OccupancyPercent = decimal.NewFromInt(int64(occupied)).Mul(decimal.NewFromInt(100)).DivRound(av, 2).StringFixed(2)
		t.RevPAR = fx(revenue.DivRound(av, decimals), decimals)
	}
	if sold > 0 {
		t.ADR = fx(revenue.DivRound(decimal.NewFromInt(int64(sold)), decimals), decimals)
	}
	out.Totals = t
	return out, nil
}

// InHouse lists the stays that are in house now, with their folio balance.
func (s *Service) InHouse(ctx context.Context, propertyID int64) (StayList, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return StayList{}, err
	}
	rows, err := s.q(ctx).ReportInHouse(ctx, reportsdb.ReportInHouseParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return StayList{}, err
	}
	out := StayList{Rows: []StayRow{}}
	for _, r := range rows {
		out.Rows = append(out.Rows, StayRow{StayID: r.StayID, StayNumber: r.StayNumber, Status: "OPEN", ConfirmationNumber: r.ConfirmationNumber, Guest: name(r.GuestFirstName, r.GuestLastName),
			Room: deref(r.RoomNumber), ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, Adults: int(r.AdultCount), Children: int(r.ChildCount), Balance: fx(r.Balance, decimals)})
	}
	return out, nil
}

// Departures lists the stays that leave on a date (still open or already checked out).
func (s *Service) Departures(ctx context.Context, propertyID int64, on civil.Date) (StayList, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return StayList{}, err
	}
	rows, err := s.q(ctx).ReportDepartures(ctx, reportsdb.ReportDeparturesParams{TenantID: p.TenantID, PropertyID: propertyID, OnDate: on})
	if err != nil {
		return StayList{}, err
	}
	out := StayList{Date: &on, Rows: []StayRow{}}
	for _, r := range rows {
		out.Rows = append(out.Rows, StayRow{StayID: r.StayID, StayNumber: r.StayNumber, Status: r.Status, ConfirmationNumber: r.ConfirmationNumber, Guest: name(r.GuestFirstName, r.GuestLastName),
			Room: r.RoomNumber, ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, Balance: fx(r.Balance, decimals)})
	}
	return out, nil
}

// Arrivals lists every room that arrives on a date, whatever became of it.
func (s *Service) Arrivals(ctx context.Context, propertyID int64, on civil.Date) (ArrivalList, error) {
	p, _, err := s.access(ctx, propertyID)
	if err != nil {
		return ArrivalList{}, err
	}
	rows, err := s.q(ctx).ReportArrivals(ctx, reportsdb.ReportArrivalsParams{TenantID: p.TenantID, PropertyID: propertyID, OnDate: on})
	if err != nil {
		return ArrivalList{}, err
	}
	out := ArrivalList{Date: on, Rows: []ArrivalRow{}}
	for _, r := range rows {
		out.Rows = append(out.Rows, ArrivalRow{ReservationRoomID: r.ReservationRoomID, ConfirmationNumber: r.ConfirmationNumber, Status: r.Status, Guest: name(r.GuestFirstName, deref(r.GuestLastName)),
			RoomType: r.RoomTypeCode, Room: deref(r.RoomNumber), ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, Adults: int(r.AdultCount), Children: int(r.ChildCount)})
	}
	return out, nil
}
