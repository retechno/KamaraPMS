package pos

import (
	"context"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/pos/posdb"
)

// The cashier floor plan (docs/architecture/34-pos.md 5.1, 11): the plan of an outlet with the state of each table, derived and never stored, the open bills, and the holds ("dipesan").

// Hold is a table held for a party. It is not linked to a reservation of the PMS.
type Hold struct {
	ID        int64  `json:"id"`
	TableID   int64  `json:"table_id"`
	TableCode string `json:"table_code"`
	Date      string `json:"business_date"`
	From      string `json:"from_time"`
	PartySize *int   `json:"party_size"`
	Name      string `json:"name"`
	Phone     string `json:"phone,omitempty"`
	Note      string `json:"note,omitempty"`
	Status    string `json:"status"`
}

// HoldInput holds a table for the business day open now.
type HoldInput struct {
	TableID   int64  `json:"table_id"`
	FromTime  string `json:"from_time"` // HH:MM
	PartySize *int   `json:"party_size"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Note      string `json:"note"`
}

// FloorTable is a table of the plan with its state and what sits at it.
type FloorTable struct {
	Table
	Status string        `json:"status"`
	Bills  []BillSummary `json:"bills"`
	Holds  []Hold        `json:"holds"`
}

// FloorArea is an area with its tables in their state.
type FloorArea struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	GridW         int          `json:"grid_w"`
	GridH         int          `json:"grid_h"`
	LayoutVersion int32        `json:"layout_version"`
	Tables        []FloorTable `json:"tables"`
	Marks         []Mark       `json:"marks"`
}

// FloorState is what the cashier floor plan shows.
type FloorState struct {
	BusinessDate civil.Date    `json:"business_date"`
	Outlet       Outlet        `json:"outlet"`
	Areas        []FloorArea   `json:"areas"`
	OpenBills    []BillSummary `json:"open_bills"`
	Holds        []Hold        `json:"holds"`
	Now          time.Time     `json:"now"`
}

func toHold(r posdb.ListHoldsRow) Hold {
	return Hold{ID: r.ID, TableID: r.TableID, TableCode: r.TableCode, Date: r.BusinessDate.String(), From: r.FromTime.String(), PartySize: optInt(r.PartySize), Name: r.Name,
		Phone: deref(r.Phone), Note: deref(r.Note), Status: r.Status}
}

// Floor answers the plan of an outlet in its state: a table is OCCUPIED when an open bill sits at it, AWAITING_PAYMENT when one of them asked for the bill, HELD when it has a hold today, FREE
// otherwise. With it come the open bills of the outlet (with the minutes and the total of each) and the holds of the day.
func (s *Service) Floor(ctx context.Context, propertyID, outletID int64) (FloorState, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return FloorState{}, err
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, outletID)
	if err != nil {
		return FloorState{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return FloorState{}, err
	}
	plan, err := s.areas(ctx, p.TenantID, propertyID, &outletID, nil)
	if err != nil {
		return FloorState{}, err
	}
	bills, err := s.ListBills(ctx, propertyID, outletID, BillFilter{Status: BillOpen, Limit: maxPageOfBill})
	if err != nil {
		return FloorState{}, err
	}
	holdRows, err := s.q(ctx).ListHolds(ctx, posdb.ListHoldsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, BusinessDate: day.BusinessDate})
	if err != nil {
		return FloorState{}, err
	}
	holds := make([]Hold, len(holdRows))
	for i, h := range holdRows {
		holds[i] = toHold(h)
	}
	out := FloorState{BusinessDate: day.BusinessDate, Outlet: o, OpenBills: bills, Holds: holds, Areas: []FloorArea{}, Now: s.clock.Now()}
	for _, a := range plan {
		if !a.IsActive {
			continue
		}
		fa := FloorArea{ID: a.ID, Name: a.Name, GridW: a.GridW, GridH: a.GridH, LayoutVersion: a.LayoutVersion, Marks: a.Marks, Tables: []FloorTable{}}
		for _, t := range a.Tables {
			if !t.IsActive {
				continue
			}
			ft := FloorTable{Table: t, Status: TableFree, Bills: []BillSummary{}, Holds: []Hold{}}
			for _, b := range bills {
				if b.TableID != nil && *b.TableID == t.ID {
					ft.Bills = append(ft.Bills, b)
					if b.RequestedAt != nil {
						ft.Status = TableAwaitingPayment
					} else if ft.Status != TableAwaitingPayment {
						ft.Status = TableOccupied
					}
				}
			}
			for _, h := range holds {
				if h.TableID == t.ID {
					ft.Holds = append(ft.Holds, h)
					if ft.Status == TableFree {
						ft.Status = TableHeld
					}
				}
			}
			fa.Tables = append(fa.Tables, ft)
		}
		out.Areas = append(out.Areas, fa)
	}
	return out, nil
}

// CreateHold holds a table for a party on the business day open now ("dipesan").
func (s *Service) CreateHold(ctx context.Context, propertyID, outletID int64, in HoldInput) (Hold, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Hold{}, err
	}
	var fields []apperr.FieldError
	at, perr := civil.ParseTimeOfDay(strings.TrimSpace(in.FromTime))
	if perr != nil {
		fields = append(fields, fieldErr("from_time", "INVALID_VALUE", "HH:MM"))
	}
	name := strings.TrimSpace(in.Name)
	if n := len([]rune(name)); n < 1 || n > 100 {
		fields = append(fields, fieldErr("name", "REQUIRED", "the name of the party, up to 100 characters"))
	}
	if in.PartySize != nil && (*in.PartySize < 1 || *in.PartySize > 999) {
		fields = append(fields, fieldErr("party_size", "INVALID_VALUE", "1 to 999"))
	}
	if len(fields) > 0 {
		return Hold{}, invalid("hold", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		t, err := s.q(ctx).GetTableOutlet(ctx, posdb.GetTableOutletParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.TableID})
		if err != nil || t.OutletID != outletID || !t.IsActive {
			return invalid("hold", fieldErr("table_id", "TABLE_NOT_FOUND", "the table does not exist in this outlet"))
		}
		id, err = s.q(ctx).InsertHold(ctx, posdb.InsertHoldParams{TenantID: p.TenantID, PropertyID: propertyID, TableID: in.TableID, BusinessDate: day.BusinessDate, FromTime: at,
			PartySize: int2(in.PartySize), Name: name, Phone: optStr(in.Phone), Note: optStr(in.Note), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.table_held", "pos_table", in.TableID, t.Code, nil, map[string]any{"name": name, "from": in.FromTime, "party_size": in.PartySize}))
	})
	if err != nil {
		return Hold{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Hold{}, err
	}
	rows, err := s.q(ctx).ListHolds(ctx, posdb.ListHoldsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, BusinessDate: day.BusinessDate})
	if err != nil {
		return Hold{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return toHold(r), nil
		}
	}
	return Hold{ID: id}, nil
}

// ReleaseHold ends a hold: the party came and sat (SEATED) or did not come (CANCELLED).
func (s *Service) ReleaseHold(ctx context.Context, propertyID, holdID int64, seated bool) error {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return err
	}
	status := "CANCELLED"
	if seated {
		status = "SEATED"
	}
	return s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		n, err := s.q(ctx).SetHoldStatus(ctx, posdb.SetHoldStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: holdID, Status: status, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if n != 1 {
			return notFound("HOLD_NOT_FOUND", "hold")
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "pos.table_hold_ended", "pos_table_hold", holdID, "", nil, map[string]any{"status": status}))
	})
}

// -------------------------------------------------------------------------------------------------- the night audit

// NightAuditIssue is a finding of the night audit about the bills.
type NightAuditIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Ref     string `json:"ref,omitempty"`
}

// NightAuditChecks answers what the bills say about closing the day (34-pos.md 13.1): open bills with sold lines are a warning, or a blocker when the outlet says so; draft lines never sent are a
// warning. It writes nothing and takes no lock.
func (s *Service) NightAuditChecks(ctx context.Context, tenantID, propertyID int64) (blockers, warnings []NightAuditIssue, err error) {
	rows, err := s.q(ctx).NightAuditOpenBills(ctx, posdb.NightAuditOpenBillsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		if r.WithSoldLines > 0 {
			is := NightAuditIssue{Code: "POS_OPEN_BILLS", Ref: r.OutletCode, Message: itoaN(int(r.WithSoldLines)) + " open bill(s) of " + r.OutletName + " with items sold: the sale is revenue of the day, the payment is not taken yet"}
			if r.Blocks {
				blockers = append(blockers, is)
			} else {
				warnings = append(warnings, is)
			}
		}
		if r.DraftsOnly > 0 {
			warnings = append(warnings, NightAuditIssue{Code: "POS_DRAFT_LINES", Ref: r.OutletCode, Message: itoaN(int(r.DraftsOnly)) + " open bill(s) of " + r.OutletName + " have lines that were never sent: they would post on tomorrow's date"})
		}
	}
	return blockers, warnings, nil
}

func itoaN(n int) string { return decimal.NewFromInt(int64(n)).String() }

// -------------------------------------------------------------------------------------------------- the reports

// ReportDay is the sales of one business day.
type ReportDay struct {
	Date     civil.Date      `json:"business_date"`
	Bills    int             `json:"bills"`
	Net      decimal.Decimal `json:"net"`
	Service  decimal.Decimal `json:"service"`
	Tax      decimal.Decimal `json:"tax"`
	Rounding decimal.Decimal `json:"rounding"`
	Total    decimal.Decimal `json:"total"`
}

// ReportTotals are the sums of a range.
type ReportTotals struct {
	Bills    int             `json:"bills"`
	Net      decimal.Decimal `json:"net"`
	Service  decimal.Decimal `json:"service"`
	Tax      decimal.Decimal `json:"tax"`
	Rounding decimal.Decimal `json:"rounding"`
	Total    decimal.Decimal `json:"total"`
}

// ReportProduct is a product sold in the range.
type ReportProduct struct {
	ProductID int64           `json:"product_id"`
	Name      string          `json:"name"`
	Category  string          `json:"category"`
	Quantity  decimal.Decimal `json:"quantity"`
	Base      decimal.Decimal `json:"base"`
	Discount  decimal.Decimal `json:"discount"`
	Net       decimal.Decimal `json:"net"`
	Service   decimal.Decimal `json:"service"`
	Tax       decimal.Decimal `json:"tax"`
	Total     decimal.Decimal `json:"total"`
}

// ReportMethod is what was taken by a method.
type ReportMethod struct {
	Method   string          `json:"method"`
	Payments int             `json:"payments"`
	Amount   decimal.Decimal `json:"amount"`
}

// ReportEvent is a void, a discount or a complimentary: who, who approved, why, how much.
type ReportEvent struct {
	Date       civil.Date      `json:"business_date"`
	Bill       string          `json:"bill_number"`
	What       string          `json:"what"`
	Amount     decimal.Decimal `json:"amount"`
	Reason     string          `json:"reason,omitempty"`
	By         string          `json:"by,omitempty"`
	ApprovedBy string          `json:"approved_by,omitempty"`
	Kind       string          `json:"kind,omitempty"`
}

// ReportPerson is the figures of a cashier or a waiter.
type ReportPerson struct {
	Name     string          `json:"name"`
	Bills    int             `json:"bills,omitempty"`
	Payments int             `json:"payments,omitempty"`
	Cash     decimal.Decimal `json:"cash"`
	Other    decimal.Decimal `json:"other"`
	Total    decimal.Decimal `json:"total"`
}

// OutletReport is the report of an outlet for a range of business dates.
type OutletReport struct {
	OutletID   int64           `json:"outlet_id"`
	OutletName string          `json:"outlet_name"`
	From       civil.Date      `json:"from"`
	To         civil.Date      `json:"to"`
	Days       []ReportDay     `json:"days"`
	Totals     ReportTotals    `json:"totals"`
	Products   []ReportProduct `json:"products"`
	Methods    []ReportMethod  `json:"methods"`
	Voids      []ReportEvent   `json:"voids"`
	Discounts  []ReportEvent   `json:"discounts"`
	Comps      []ReportEvent   `json:"comps"`
	Cashiers   []ReportPerson  `json:"cashiers"`
	Waiters    []ReportPerson  `json:"waiters"`
	// DrawerOpens are the times the drawer was opened without a sale in the range, with the person and the reason (docs/architecture/34-pos.md 20).
	DrawerOpens []DrawerOpen `json:"drawer_opens"`
	RoomCharges struct {
		Count    int             `json:"count"`
		Amount   decimal.Decimal `json:"amount"`
		Returned int             `json:"returned"`
	} `json:"room_charges"`
	OpenBills struct {
		Count   int             `json:"count"`
		Balance decimal.Decimal `json:"balance"`
	} `json:"open_bills"`
}

// Report answers the report of an outlet for a range of business dates (pos.report), all read from the ledger of the folios of the bills.
func (s *Service) Report(ctx context.Context, propertyID, outletID int64, from, to civil.Date) (OutletReport, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return OutletReport{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPOSReport); err != nil {
		return OutletReport{}, err
	}
	if to.Before(from) || from.DaysUntil(to) > 366 {
		return OutletReport{}, apperr.Invalid("the range is invalid", fieldErr("to", "INVALID_VALUE", "not before the first day, at most a year"))
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, outletID)
	if err != nil {
		return OutletReport{}, err
	}
	q := s.q(ctx)
	out := OutletReport{OutletID: outletID, OutletName: o.Name, From: from, To: to, Days: []ReportDay{}, Products: []ReportProduct{}, Methods: []ReportMethod{}, Voids: []ReportEvent{},
		Discounts: []ReportEvent{}, Comps: []ReportEvent{}, Cashiers: []ReportPerson{}, Waiters: []ReportPerson{}, DrawerOpens: []DrawerOpen{}}
	days, err := q.ReportDays(ctx, posdb.ReportDaysParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: &outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, d := range days {
		rd := ReportDay{Date: d.BusinessDate, Bills: int(d.Bills), Net: d.Net, Service: d.Service, Tax: d.Tax, Rounding: d.Rounding, Total: d.Total}
		out.Days = append(out.Days, rd)
		out.Totals.Bills += rd.Bills
		out.Totals.Net, out.Totals.Service, out.Totals.Tax = out.Totals.Net.Add(rd.Net), out.Totals.Service.Add(rd.Service), out.Totals.Tax.Add(rd.Tax)
		out.Totals.Rounding, out.Totals.Total = out.Totals.Rounding.Add(rd.Rounding), out.Totals.Total.Add(rd.Total)
	}
	prods, err := q.ReportProducts(ctx, posdb.ReportProductsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, r := range prods {
		out.Products = append(out.Products, ReportProduct{ProductID: r.ProductID, Name: r.ProductName, Category: r.Category, Quantity: r.Quantity, Base: r.Base, Discount: r.Discount, Net: r.Net, Service: r.Service, Tax: r.Tax, Total: r.Total})
	}
	methods, err := q.ReportMethods(ctx, posdb.ReportMethodsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: &outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, r := range methods {
		out.Methods = append(out.Methods, ReportMethod{Method: r.Method, Payments: int(r.Payments), Amount: r.Amount})
	}
	voids, err := q.ReportVoids(ctx, posdb.ReportVoidsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, r := range voids {
		out.Voids = append(out.Voids, ReportEvent{Date: r.BusinessDate, Bill: r.BillNumber, What: r.ProductName, Amount: r.Amount, Reason: deref(r.VoidReason), By: r.VoidedBy, ApprovedBy: r.ApprovedBy})
	}
	disc, err := q.ReportDiscounts(ctx, posdb.ReportDiscountsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: &outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, r := range disc {
		out.Discounts = append(out.Discounts, ReportEvent{Date: r.BusinessDate, Bill: r.BillNumber, What: r.Description, Amount: r.Amount.Abs(), Reason: r.Reason, By: r.GivenBy, ApprovedBy: r.ApprovedBy, Kind: r.Kind})
	}
	comps, err := q.ReportComps(ctx, posdb.ReportCompsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: &outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, r := range comps {
		out.Comps = append(out.Comps, ReportEvent{Date: r.BusinessDate, Bill: r.BillNumber, Amount: r.Amount, Reason: r.Remarks, By: r.ApprovedBy, Kind: r.Treatment})
	}
	cashiers, err := q.ReportCashiers(ctx, posdb.ReportCashiersParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: &outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, r := range cashiers {
		out.Cashiers = append(out.Cashiers, ReportPerson{Name: r.Cashier, Payments: int(r.Payments), Cash: r.Cash, Other: r.Other, Total: r.Cash.Add(r.Other)})
	}
	waiters, err := q.ReportWaiters(ctx, posdb.ReportWaitersParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	for _, r := range waiters {
		out.Waiters = append(out.Waiters, ReportPerson{Name: r.Waiter, Bills: int(r.Bills), Total: r.Total})
	}
	rc, err := q.ReportRoomCharges(ctx, posdb.ReportRoomChargesParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, FromDate: from, ToDate: to})
	if err != nil {
		return OutletReport{}, err
	}
	out.RoomCharges.Count, out.RoomCharges.Amount, out.RoomCharges.Returned = int(rc.Charges), rc.Amount, int(rc.Returned)
	ob, err := q.ReportOpenBills(ctx, posdb.ReportOpenBillsParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID})
	if err != nil {
		return OutletReport{}, err
	}
	out.OpenBills.Count, out.OpenBills.Balance = int(ob.Bills), ob.Balance
	if out.DrawerOpens, err = s.drawerOpens(ctx, p.TenantID, propertyID, outletID, from, to); err != nil {
		return OutletReport{}, err
	}
	return out, nil
}
