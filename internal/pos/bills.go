package pos

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/pos/posdb"
)

// The bills of an outlet: opening, reading and the free edits of a draft (docs/architecture/34-pos.md 5). A bill is the folio of an outlet (type OUTLET) with a row that says what the screens need
// (table, guests, waiter) and a row for each line; the money is in the ledger, written by the one folio posting service, and this package never computes tax or service.

// Ledger is what a bill needs of the folio service. *folios.Service is the one implementation; the interface keeps the dependency to what is used.
type Ledger = *folios.Service

// SetBilling wires the ledger, the approvals and the charge calculation. Without them the bills are refused.
func (s *Service) SetBilling(l Ledger, i *iam.Service, b *billingconfig.Service) {
	s.ledger, s.iam, s.billing = l, i, b
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// parseUUID reads the id a client made. UUID v7 is what the clients make; any uuid is taken.
func parseUUID(field, v string) (pgtype.UUID, *apperr.FieldError) {
	if !uuidPattern.MatchString(v) {
		return pgtype.UUID{}, &apperr.FieldError{Field: field, Code: "INVALID_UUID", Message: "a uuid made by the client"}
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(v, "-", ""))
	if err != nil || len(raw) != 16 {
		return pgtype.UUID{}, &apperr.FieldError{Field: field, Code: "INVALID_UUID", Message: "a uuid made by the client"}
	}
	var u pgtype.UUID
	copy(u.Bytes[:], raw)
	u.Valid = true
	return u, nil
}

func uuidString(u pgtype.UUID) string {
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Service) needBilling() error {
	if s.ledger == nil || s.iam == nil || s.billing == nil {
		return apperr.Conflict("POS_UNAVAILABLE", "the point of sale is not available")
	}
	return nil
}

// orderers may take orders: the waiter and the cashier.
var orderers = []auth.Permission{auth.PermPOSOrder, auth.PermPOSCashier}

func (s *Service) orderer(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	if err := s.needBilling(); err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, orderers...)
}

func (s *Service) cashier(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	if err := s.needBilling(); err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermPOSCashier)
}

// outletOf reads an outlet of the property.
func (s *Service) outletOf(ctx context.Context, tenantID, propertyID, outletID int64) (Outlet, error) {
	o, err := s.outlets(ctx, tenantID, propertyID, &outletID, nil)
	if err != nil {
		return Outlet{}, err
	}
	if len(o) == 0 {
		return Outlet{}, notFound("OUTLET_NOT_FOUND", "outlet")
	}
	return o[0], nil
}

func (s *Service) billRow(ctx context.Context, tenantID, propertyID, id int64) (posdb.GetBillRow, error) {
	r, err := s.q(ctx).GetBill(ctx, posdb.GetBillParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return r, orNotFound(err, errBillNotFound())
	}
	return r, nil
}

func optInt(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int16)
	return &n
}

func int2(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true} //nolint:gosec // G115: validated to 1..999
}

func optStr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

// -------------------------------------------------------------------------------------------------- opening

func validateOpen(in OpenBillInput, o Outlet) (pgtype.UUID, []apperr.FieldError) {
	var fields []apperr.FieldError
	u, fe := parseUUID("client_uuid", in.ClientUUID)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if !slices.Contains(orderTypes, in.OrderType) {
		fields = append(fields, fieldErr("order_type", "INVALID_VALUE", "DINE_IN, TAKEAWAY, ROOM_SERVICE or DIRECT"))
	}
	if in.GuestCount != nil && (*in.GuestCount < 1 || *in.GuestCount > 999) {
		fields = append(fields, fieldErr("guest_count", "INVALID_VALUE", "1 to 999"))
	}
	if o.GuestCountRequired && in.OrderType == OrderDineIn && in.GuestCount == nil {
		fields = append(fields, fieldErr("guest_count", "REQUIRED", "this outlet asks for the number of guests"))
	}
	if len([]rune(in.CustomerName)) > 100 {
		fields = append(fields, fieldErr("customer_name", "INVALID_VALUE", "at most 100 characters"))
	}
	if len([]rune(in.Note)) > maxNoteLen {
		fields = append(fields, fieldErr("note", "INVALID_VALUE", "at most 200 characters"))
	}
	if in.OrderType != OrderDineIn && in.TableID != nil {
		fields = append(fields, fieldErr("table_id", "INVALID_VALUE", "only a bill to eat in has a table"))
	}
	if in.OrderType == OrderDineIn && o.UsesTables && in.TableID == nil {
		fields = append(fields, fieldErr("table_id", "REQUIRED", "choose a table"))
	}
	return u, fields
}

// OpenBill opens a bill: its folio and its number {outlet code}-{n}. The same ClientUUID again returns the bill that exists (a retry, or a device that synced twice).
func (s *Service) OpenBill(ctx context.Context, propertyID, outletID int64, in OpenBillInput) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, outletID)
	if err != nil {
		return Bill{}, err
	}
	u, fields := validateOpen(in, o)
	if len(fields) > 0 {
		return Bill{}, invalid("bill", fields...)
	}
	if existing, err := s.q(ctx).GetBillByUUID(ctx, posdb.GetBillByUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: u}); err == nil {
		return s.loadBill(ctx, p, propertyID, existing)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Bill{}, err
	}
	if !o.IsActive {
		return Bill{}, apperr.Conflict("OUTLET_INACTIVE", "the outlet is not active")
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if in.TableID != nil {
			t, err := q.GetTableOutlet(ctx, posdb.GetTableOutletParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.TableID})
			if err != nil || t.OutletID != outletID || !t.IsActive {
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				return invalid("bill", fieldErr("table_id", "TABLE_NOT_FOUND", "the table does not exist in this outlet"))
			}
		}
		if in.TargetStayID != nil {
			if in.OrderType != OrderRoomService {
				return invalid("bill", fieldErr("target_stay_id", "INVALID_VALUE", "only room service is for a room"))
			}
			if err := s.ledger.LockStayShared(ctx, propertyID, *in.TargetStayID); err != nil {
				return err
			}
		}
		// the sequence of the outlet is the last lock of the transaction (level 50)
		if err := db.EnterLockLevel(ctx, db.LevelSequences); err != nil {
			return err
		}
		n, err := q.NextBillNumber(ctx, posdb.NextBillNumberParams{TenantID: p.TenantID, PropertyID: propertyID, ID: outletID})
		if err != nil {
			return err
		}
		number := fmt.Sprintf("%s-%04d", n.Code, n.Number)
		f, err := s.ledger.OpenOutletFolio(ctx, p, propertyID, outletID, number)
		if err != nil {
			return err
		}
		id, err = q.InsertBill(ctx, posdb.InsertBillParams{
			TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, ClientUuid: u, FolioID: &f.ID, BillNumber: &number, OrderType: in.OrderType, TableID: in.TableID,
			GuestCount: int2(in.GuestCount), TargetStayID: in.TargetStayID, CustomerName: optStr(in.CustomerName), ActorID: p.ActorID(), CompTreatment: o.CompTreatment,
			BusinessDateOpened: day.BusinessDate, OpenedAt: s.clock.Now(), Note: optStr(in.Note),
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.bill_opened", "pos_bill", id, number, nil, map[string]any{
			"outlet_id": outletID, "order_type": in.OrderType, "table_id": in.TableID, "guest_count": in.GuestCount, "folio_id": f.ID, "comp_treatment": o.CompTreatment,
		}))
	})
	if err != nil {
		if apperr.IsCode(err, "DUPLICATE_REQUEST") { // two requests with the same uuid at once: the other one made the bill
			if existing, e2 := s.q(ctx).GetBillByUUID(ctx, posdb.GetBillByUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: u}); e2 == nil {
				return s.loadBill(ctx, p, propertyID, existing)
			}
		}
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, id)
}

// -------------------------------------------------------------------------------------------------- reading

func decodeOptions(raw []byte) []LineOption {
	out := []LineOption{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = []LineOption{}
	}
	return out
}

// effectiveProfile is the profile in force for a line: the product's, else the outlet's, snapshotted when the line was added.
func (s *Service) quote(ctx context.Context, propertyID int64, chargeCodeID int64, qty, unitPrice, discount decimal.Decimal, profile *int64) (Quote, error) {
	b, err := s.billing.Calculate(ctx, billingconfig.ChargeRequest{PropertyID: propertyID, ChargeCodeID: chargeCodeID, Quantity: qty, UnitPrice: unitPrice, Discount: discount, RuleProfileID: profile})
	if err != nil {
		return Quote{}, err
	}
	return Quote{Base: b.BaseAmount, Discount: b.Discount, Net: b.NetAmount, Service: b.ServiceTotal, Tax: b.TaxTotal, Total: b.TotalAmount}, nil
}

// Quote is what the one charge engine says a line comes to.
type Quote struct {
	Base, Discount, Net, Service, Tax, Total decimal.Decimal
}

// loadBill reads a bill with its lines, payments, room charges and totals. A draft line carries the quote of the charge engine: it is what the line will post, never a figure of the client.
func (s *Service) loadBill(ctx context.Context, p auth.Principal, propertyID, id int64) (Bill, error) {
	r, err := s.billRow(ctx, p.TenantID, propertyID, id)
	if err != nil {
		return Bill{}, err
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, r.OutletID)
	if err != nil {
		return Bill{}, err
	}
	b := Bill{
		ID: r.ID, ClientUUID: uuidString(r.ClientUuid), OutletID: r.OutletID, OutletCode: o.Code, OutletName: o.Name, FolioID: r.FolioID, Number: deref(r.BillNumber), Status: r.Status,
		OrderType: r.OrderType, TableID: r.TableID, TableCode: r.TableCode, GuestCount: optInt(r.GuestCount), TargetStayID: r.TargetStayID, TargetRoom: r.TargetRoom,
		CustomerName: deref(r.CustomerName), WaiterID: r.WaiterID, WaiterName: r.WaiterName, CompTreatment: r.CompTreatment, BusinessDateOpened: r.BusinessDateOpened,
		OpenedAt: r.OpenedAt, BillRequestedAt: r.BillRequestedAt, ClosedAt: r.ClosedAt, ClosedBusinessDate: r.ClosedBusinessDate, MergedIntoBillID: r.MergedIntoBillID,
		Note: deref(r.Note), Version: r.Version, Lines: []BillLine{}, Payments: []BillPayment{}, RoomCharges: []BillRoomCharge{},
	}
	lines, err := s.q(ctx).ListBillLines(ctx, posdb.ListBillLinesParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: id})
	if err != nil {
		return Bill{}, err
	}
	t := BillTotals{}
	for _, l := range lines {
		bl := BillLine{
			ID: l.ID, ClientUUID: uuidString(l.ClientUuid), ProductID: l.ProductID, ProductName: l.ProductName, Options: decodeOptions(l.Options), Quantity: l.Quantity, UnitPrice: l.UnitPrice,
			DiscountPct: l.DiscountPct, DiscountAmount: l.DiscountAmount, DiscountReason: deref(l.DiscountReason), IsComp: l.IsComp, Note: deref(l.Note), Status: l.Status,
			Round: int(l.RoundNo), TicketStatus: l.TicketStatus, FolioItemID: l.FolioItemID, CreatedByName: l.CreatedByName, CreatedAt: l.CreatedAt, SentAt: l.SentAt,
			VoidReason: deref(l.VoidReason), VoidedByName: l.VoidedByName, ApprovedByName: l.ApprovedByName, VoidedAt: l.VoidedAt,
		}
		switch l.Status {
		case LineDraft:
			if r.Status == BillOpen {
				q, err := s.quote(ctx, propertyID, l.ChargeCodeID, l.Quantity, l.UnitPrice, l.DiscountAmount, l.RuleProfileID)
				if err != nil {
					return Bill{}, err
				}
				bl.Base, bl.Net, bl.Service, bl.Tax, bl.Total = q.Base, q.Net, q.Service, q.Tax, q.Total
				t.Drafts = t.Drafts.Add(q.Total)
			}
		case LineSent:
			bl.Base, bl.Net, bl.Service, bl.Tax, bl.Total = l.ItemBase, l.ItemNet, l.ItemService, l.ItemTax, l.ItemTotal
			bl.DiscountAmount = l.ItemDiscount
		default: // VOID: the line stays on the bill, struck through, with its original amounts
			bl.Base, bl.Net, bl.Service, bl.Tax, bl.Total = l.ItemBase, l.ItemNet, l.ItemService, l.ItemTax, l.ItemTotal
		}
		b.Lines = append(b.Lines, bl)
	}
	if r.FolioID != nil {
		sums, err := s.q(ctx).BillLedgerSums(ctx, posdb.BillLedgerSumsParams{PropertyID: propertyID, FolioID: *r.FolioID})
		if err != nil {
			return Bill{}, err
		}
		t.Sold, t.Rounding, t.Paid, t.Transferred = sums.Sold, sums.Rounding, sums.Paid, sums.Transferred
		t.Base, t.Net, t.Service, t.Tax = sums.Base, sums.Net, sums.Service, sums.Tax
		t.Balance = sums.Sold.Add(sums.Rounding).Sub(sums.Paid).Sub(sums.Transferred)
		for _, l := range b.Lines { // the drafts are in the bill, not yet in the ledger
			if l.Status == LineDraft && r.Status == BillOpen {
				t.Base, t.Net, t.Service, t.Tax = t.Base.Add(l.Base), t.Net.Add(l.Net), t.Service.Add(l.Service), t.Tax.Add(l.Tax)
			}
		}
		t.Discount = t.Base.Sub(t.Net)
		t.Payable = t.Balance.Add(t.Drafts)
		pays, err := s.ledger.ListBillPayments(ctx, p, propertyID, *r.FolioID)
		if err != nil {
			return Bill{}, err
		}
		for _, py := range pays {
			b.Payments = append(b.Payments, BillPayment{ID: py.PaymentID, Number: py.PaymentNumber, Method: py.Method, Amount: py.Amount, Reference: py.Reference, Label: py.Remarks, PaidAt: py.PaidAt, Status: py.Status, CompanyID: py.CompanyID})
		}
	}
	t.CashPayable = RoundToUnit(t.Payable, o.RoundingUnit)
	if r.Status != BillOpen {
		t.Payable, t.CashPayable = decimal.Zero, decimal.Zero
	}
	b.Totals = t
	rcs, err := s.q(ctx).ListBillRoomCharges(ctx, posdb.ListBillRoomChargesParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: &id})
	if err != nil {
		return Bill{}, err
	}
	for _, c := range rcs {
		b.RoomCharges = append(b.RoomCharges, BillRoomCharge{ID: c.ID, Amount: c.Amount, ToFolioID: c.ToFolioID, StayID: c.StayID, Room: c.RoomNumber, ReturnedAt: c.ReturnedAt})
	}
	return b, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// GetBill reads a bill (pos.order or pos.cashier).
func (s *Service) GetBill(ctx context.Context, propertyID, id int64) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, id)
}

// BillFilter narrows a list of bills.
type BillFilter struct {
	Status  string
	TableID *int64
	Day     *civil.Date
	Query   string
	Limit   int
}

// ListBills lists the bills of an outlet, newest first.
func (s *Service) ListBills(ctx context.Context, propertyID, outletID int64, f BillFilter) ([]BillSummary, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if _, err := s.outletOf(ctx, p.TenantID, propertyID, outletID); err != nil {
		return nil, err
	}
	if f.Status != "" && !slices.Contains([]string{BillOpen, BillClosed, BillVoid, BillMerged}, f.Status) {
		return nil, apperr.Invalid("the filter is invalid", fieldErr("status", "INVALID_VALUE", "OPEN, CLOSED, VOID or MERGED"))
	}
	limit := f.Limit
	if limit < 1 || limit > maxPageOfBill {
		limit = 100
	}
	rows, err := s.q(ctx).ListBills(ctx, posdb.ListBillsParams{
		TenantID: p.TenantID, PropertyID: propertyID, OutletID: outletID, Status: optStr(f.Status), TableID: f.TableID, Day: f.Day, Q: optStr(f.Query), RowLimit: int32(limit), //nolint:gosec // G115: bounded above
	})
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := make([]BillSummary, 0, len(rows))
	for _, r := range rows {
		total := r.Balance
		if r.Status != BillOpen {
			total = r.SoldDebit
		}
		bs := BillSummary{
			ID: r.ID, Number: deref(r.BillNumber), Status: r.Status, OrderType: r.OrderType, TableID: r.TableID, TableCode: r.TableCode, GuestCount: optInt(r.GuestCount),
			TargetStayID: r.TargetStayID, TargetRoom: r.TargetRoom, CustomerName: deref(r.CustomerName), WaiterName: r.WaiterName, OpenedAt: r.OpenedAt,
			RequestedAt: r.BillRequestedAt, ClosedAt: r.ClosedAt, Total: total, DraftLines: int(r.DraftLines), SentLines: int(r.SentLines), Version: r.Version,
		}
		end := now
		if r.ClosedAt != nil {
			end = *r.ClosedAt
		}
		if m := int(end.Sub(r.OpenedAt) / time.Minute); m > 0 {
			bs.Minutes = m
		}
		out = append(out, bs)
	}
	return out, nil
}

// -------------------------------------------------------------------------------------------------- the free edits

// lockBillShared takes the folio of the bill FOR SHARE (a draft is edited while the bill is open, so a close cannot slip in) and reads the bill again under it.
func (s *Service) lockBill(ctx context.Context, p auth.Principal, propertyID, billID int64, mode db.LockMode) (posdb.GetBillRow, error) {
	pre, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return pre, err
	}
	if pre.FolioID == nil {
		return pre, apperr.Conflict("BILL_NOT_SYNCED", "the bill has no folio yet")
	}
	if err := s.ledger.LockFolios(ctx, propertyID, mode, *pre.FolioID); err != nil {
		return pre, err
	}
	r, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return r, err
	}
	if r.Status != BillOpen {
		return r, errBillNotOpen().WithContext("status", r.Status)
	}
	return r, nil
}

// priceLine reads the product as the outlet sells it and the options asked for, and answers what the line is: its name, its price with the options in it, its snapshots.
func (s *Service) priceLine(ctx context.Context, p auth.Principal, propertyID int64, o Outlet, bd civil.Date, productID int64, optIn []LineOptionInput, decimals int32) (name string, unit decimal.Decimal, codeID int64, profile *int64, station *int64, opts []LineOption, err error) {
	sp, err := s.q(ctx).GetSellableProduct(ctx, posdb.GetSellableProductParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: o.ID, BusinessDate: bd, ProductID: productID})
	if err != nil {
		return "", decimal.Zero, 0, nil, nil, nil, orNotFound(err, invalid("line", fieldErr("product_id", "PRODUCT_NOT_FOUND", "the product does not exist in this property")))
	}
	switch {
	case !sp.IsActive || !sp.IsSold || sp.Price == nil:
		return "", decimal.Zero, 0, nil, nil, nil, apperr.Conflict("PRODUCT_NOT_SOLD", "the outlet does not sell this product").WithContext("product_id", productID)
	case sp.SoldOut:
		return "", decimal.Zero, 0, nil, nil, nil, apperr.Conflict("PRODUCT_SOLD_OUT", "this product is sold out today").WithContext("product_id", productID)
	}
	groups, err := s.q(ctx).ListOptionGroups(ctx, posdb.ListOptionGroupsParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: &productID})
	if err != nil {
		return "", decimal.Zero, 0, nil, nil, nil, err
	}
	options, err := s.q(ctx).ListOptions(ctx, posdb.ListOptionsParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: &productID})
	if err != nil {
		return "", decimal.Zero, 0, nil, nil, nil, err
	}
	byID := map[int64]posdb.ListOptionsRow{}
	for _, op := range options {
		byID[op.ID] = op
	}
	count := map[int64]int{}   // per group: how many were chosen (with quantity)
	chosen := map[int64]bool{} // an option once
	unit = *sp.Price
	for _, in := range optIn {
		op, ok := byID[in.OptionID]
		if !ok || !op.IsActive {
			return "", decimal.Zero, 0, nil, nil, nil, invalid("line", fieldErr("options", "OPTION_NOT_FOUND", "the option does not belong to this product"))
		}
		qty := in.Qty
		if qty == 0 {
			qty = 1
		}
		if chosen[op.ID] || qty < 1 || qty > int(op.MaxQty) {
			return "", decimal.Zero, 0, nil, nil, nil, invalid("line", fieldErr("options", "INVALID_VALUE", "an option once, at most its limit"))
		}
		chosen[op.ID] = true
		count[op.GroupID] += qty
		unit = unit.Add(op.PriceDelta.Mul(decimal.NewFromInt(int64(qty))))
		opts = append(opts, LineOption{OptionID: op.ID, GroupID: op.GroupID, Name: op.Name, PriceDelta: op.PriceDelta, Qty: qty})
	}
	for _, g := range groups {
		n := count[g.ID]
		if n < int(g.MinSelect) || n > int(g.MaxSelect) {
			return "", decimal.Zero, 0, nil, nil, nil, invalid("line", fieldErr("options", "OPTION_GROUP_LIMIT", fmt.Sprintf("%s: choose from %d to %d", g.Name, g.MinSelect, g.MaxSelect)))
		}
	}
	if unit.IsNegative() {
		unit = decimal.Zero
	}
	unit = money.Round(unit, decimals)
	prof := sp.RuleProfileID
	if prof == nil {
		pr := o.RuleProfileID
		prof = &pr
	}
	if opts == nil {
		opts = []LineOption{}
	}
	st := sp.ListStationID
	if st == nil {
		st = sp.ProductStationID
	}
	return sp.Name, unit, sp.ChargeCodeID, prof, st, opts, nil
}

func validateQty(q decimal.Decimal, note string) []apperr.FieldError {
	var fields []apperr.FieldError
	if !q.IsPositive() || q.GreaterThan(decimal.NewFromInt(9999)) || q.Exponent() < -3 {
		fields = append(fields, fieldErr("quantity", "INVALID_VALUE", "above zero, at most 3 decimals"))
	}
	if len([]rune(note)) > maxNoteLen {
		fields = append(fields, fieldErr("note", "INVALID_VALUE", "at most 200 characters"))
	}
	return fields
}

// AddLine adds a draft line to an open bill. The price is the one of the price list of the outlet at this moment, snapshotted on the line; the client never sends it. Two waiters adding at once
// both succeed: they are different rows, and the folio is only share-locked.
func (s *Service) AddLine(ctx context.Context, propertyID, billID int64, in AddLineInput) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	u, fe := parseUUID("client_uuid", in.ClientUUID)
	fields := validateQty(in.Quantity, in.Note)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if len(fields) > 0 {
		return Bill{}, invalid("line", fields...)
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForShare)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetBillLineByUUID(ctx, posdb.GetBillLineByUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: billID, ClientUuid: u}); err == nil {
			return nil // the same line again: it is there
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		name, unit, code, prof, station, opts, err := s.priceLine(ctx, p, propertyID, o, day.BusinessDate, in.ProductID, in.Options, decimals)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(opts)
		lines, err := q.ListBillLines(ctx, posdb.ListBillLinesParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: billID})
		if err != nil {
			return err
		}
		if len(lines) >= maxBillLines {
			return apperr.Conflict("BILL_TOO_LARGE", "a bill has at most 300 lines")
		}
		_, err = q.InsertBillLine(ctx, posdb.InsertBillLineParams{
			TenantID: p.TenantID, PropertyID: propertyID, BillID: billID, ClientUuid: u, ProductID: in.ProductID, ProductName: name, ChargeCodeID: code, RuleProfileID: prof,
			Options: raw, Quantity: in.Quantity, UnitPrice: unit, DiscountAmount: decimal.Zero, Note: optStr(in.Note), StationID: station, ActorID: p.ActorID(),
		})
		return err
	})
	if err != nil {
		if apperr.IsCode(err, "DUPLICATE_REQUEST") { // the same uuid at once: the other request added it
			return s.loadBill(ctx, p, propertyID, billID)
		}
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// UpdateLine changes the quantity, the options or the note of a draft line.
func (s *Service) UpdateLine(ctx context.Context, propertyID, billID, lineID int64, in UpdateLineInput) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if fields := validateQty(in.Quantity, in.Note); len(fields) > 0 {
		return Bill{}, invalid("line", fields...)
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForShare)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		l, err := q.GetBillLine(ctx, posdb.GetBillLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
		if err != nil || l.BillID != billID {
			return orNotFound(err, notFound("BILL_LINE_NOT_FOUND", "bill line"))
		}
		if l.Status != LineDraft {
			return apperr.Conflict("BILL_LINE_SENT", "a sent line is not changed: void it")
		}
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		_, unit, _, _, _, opts, err := s.priceLine(ctx, p, propertyID, o, day.BusinessDate, l.ProductID, in.Options, decimals)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(opts)
		n, err := q.UpdateDraftLine(ctx, posdb.UpdateDraftLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID, BillID: billID, Quantity: in.Quantity, Options: raw, UnitPrice: unit, Note: optStr(in.Note)})
		if err != nil {
			return err
		}
		if n != 1 {
			return apperr.Conflict("BILL_LINE_SENT", "a sent line is not changed: void it")
		}
		if l.DiscountPct != nil { // a percentage follows the new base
			amt := money.Round(unit.Mul(in.Quantity).Mul(*l.DiscountPct).Div(decimal.NewFromInt(100)), decimals)
			_, err = q.SetLineDiscount(ctx, posdb.SetLineDiscountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID, BillID: billID, DiscountPct: l.DiscountPct, DiscountAmount: amt,
				DiscountReason: l.DiscountReason, ActorID: p.ActorID(), IsComp: l.IsComp})
			return err
		}
		return nil
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// DeleteLine removes a draft line (a sent line is voided, with an approval).
func (s *Service) DeleteLine(ctx context.Context, propertyID, billID, lineID int64) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil); err != nil {
			return err
		}
		if _, err := s.lockBill(ctx, p, propertyID, billID, db.ForShare); err != nil {
			return err
		}
		l, err := s.q(ctx).GetBillLine(ctx, posdb.GetBillLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
		if err != nil || l.BillID != billID {
			return orNotFound(err, notFound("BILL_LINE_NOT_FOUND", "bill line"))
		}
		if l.Status != LineDraft {
			return apperr.Conflict("BILL_LINE_NOT_DRAFT", "only a draft line is removed from a bill: a sent line is voided")
		}
		_, err = s.q(ctx).DeleteDraftLine(ctx, posdb.DeleteDraftLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID, BillID: billID})
		return err
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// UpdateBill changes what the bill says: the table (a move: no ledger), the guests, the name, the note. Version is the one the client read.
func (s *Service) UpdateBill(ctx context.Context, propertyID, billID int64, in UpdateBillInput) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForShare)
		if err != nil {
			return err
		}
		if in.Version != 0 && in.Version != b.Version {
			return apperr.Conflict("VERSION_CONFLICT", "the bill changed: read it again").WithContext("current", b.Version)
		}
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		ot := in.OrderType
		if ot == "" {
			ot = b.OrderType
		}
		_, fields := validateOpen(OpenBillInput{ClientUUID: uuidString(b.ClientUuid), OrderType: ot, TableID: in.TableID, GuestCount: in.GuestCount, CustomerName: in.CustomerName, Note: in.Note}, o)
		if len(fields) > 0 {
			return invalid("bill", fields...)
		}
		if in.TableID != nil {
			t, err := s.q(ctx).GetTableOutlet(ctx, posdb.GetTableOutletParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.TableID})
			if err != nil || t.OutletID != b.OutletID || !t.IsActive {
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				return invalid("bill", fieldErr("table_id", "TABLE_NOT_FOUND", "the table does not exist in this outlet"))
			}
		}
		_, err = s.q(ctx).UpdateBillMeta(ctx, posdb.UpdateBillMetaParams{TenantID: p.TenantID, PropertyID: propertyID, ID: billID, TableID: in.TableID, OrderType: ot,
			GuestCount: int2(in.GuestCount), CustomerName: optStr(in.CustomerName), Note: optStr(in.Note), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.bill_changed", "pos_bill", billID, deref(b.BillNumber),
			map[string]any{"table_id": b.TableID, "order_type": b.OrderType, "guest_count": optInt(b.GuestCount)},
			map[string]any{"table_id": in.TableID, "order_type": ot, "guest_count": in.GuestCount}))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// RequestBill marks that the guest asked for the bill: the table turns to "awaiting payment". It posts nothing.
func (s *Service) RequestBill(ctx context.Context, propertyID, billID int64) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForShare)
		if err != nil {
			return err
		}
		if err := s.q(ctx).SetBillRequested(ctx, posdb.SetBillRequestedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: billID, At: s.clock.Now(), ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(), Action: "pos.bill_requested",
			EntityType: "pos_bill", EntityID: billID, EntityLabel: deref(b.BillNumber)})
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}
