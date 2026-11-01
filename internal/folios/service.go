package folios

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/tenancy"
)

// Service is the folio application service. It holds FolioPostingService (posting.go: the only writer of
// folio_items) and PaymentService (payments.go). Locks follow docs/architecture/05-transactions-locking.md:
// the business day (share), then the reservation, the folio, the payment, and sequences last.
type Service struct {
	txm     *db.TxManager
	clock   clock.Clock
	audit   *audit.Writer
	authz   auth.Authorizer
	days    *tenancy.Service
	billing *billingconfig.Service
	iam     *iam.Service
	gate    CompanyGate
	shifts  ShiftGate
}

// CompanyGate guards a company's city ledger account. Implementations lock the company row (lock level 44) and
// are called inside the caller's transaction.
type CompanyGate interface {
	// LockForTransfer refuses an unknown, inactive or over-limit company: owed + amount must stay within its credit limit.
	LockForTransfer(ctx context.Context, propertyID, companyID int64, amount decimal.Decimal) error
	// LockForVoid refuses taking amount back when receipts would then exceed what the company is owed.
	LockForVoid(ctx context.Context, propertyID, companyID int64, amount decimal.Decimal) error
}

// ShiftGate answers the cashier shift a cash payment or refund goes through (lock level 43, taken in the caller's transaction before the document number).
type ShiftGate interface {
	// CashShift is the open shift of the caller, or nil when the property does not need one; 409 NO_OPEN_SHIFT when it does and there is none.
	CashShift(ctx context.Context, propertyID int64) (*int64, error)
	// CashShiftOfDrawer is the open shift of the caller on that drawer (the cash of an outlet), under the same rules.
	CashShiftOfDrawer(ctx context.Context, propertyID int64, drawer string) (*int64, error)
}

// SetShiftGate wires the cashier shifts; without it cash is taken on no shift.
func (s *Service) SetShiftGate(g ShiftGate) { s.shifts = g }

// SetCompanyGate wires the city ledger; without it transfers are refused.
func (s *Service) SetCompanyGate(g CompanyGate) { s.gate = g }

// NewService wires the folio service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, b *billingconfig.Service, i *iam.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, billing: b, iam: i}
}

func (s *Service) q(ctx context.Context) *foliosdb.Queries { return foliosdb.New(s.txm.DB(ctx)) }

func errFolioNotFound() *apperr.Error {
	return apperr.NotFound("FOLIO_NOT_FOUND", "the folio does not exist in this property")
}

func errItemNotFound() *apperr.Error {
	return apperr.NotFound("FOLIO_ITEM_NOT_FOUND", "the folio item does not exist in this property")
}

func errPaymentNotFound() *apperr.Error {
	return apperr.NotFound("PAYMENT_NOT_FOUND", "the payment does not exist in this property")
}

func orNotFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

func mapNotFound(err error, nf *apperr.Error) error {
	if apperr.IsCode(err, "NOT_FOUND") {
		return nf
	}
	return err
}

func (s *Service) actor(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
		Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated,
	}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}

func (s *Service) decimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

// lockFolio takes the folio's row lock (L4) and returns it. The business day must already be share-locked.
func (s *Service) lockFolio(ctx context.Context, tenantID, propertyID, folioID int64) (foliosdb.Folio, error) {
	if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, []int64{folioID}); err != nil {
		return foliosdb.Folio{}, mapNotFound(err, errFolioNotFound())
	}
	f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: folioID})
	return f, orNotFound(err, errFolioNotFound())
}

func requireOpen(f foliosdb.Folio) error {
	if f.Status != "OPEN" {
		return apperr.Conflict("FOLIO_CLOSED", "the folio is closed").WithContext("folio_number", f.FolioNumber).WithContext("folio_id", f.ID)
	}
	return nil
}

func (s *Service) balanceOf(ctx context.Context, propertyID, folioID int64) (decimal.Decimal, foliosdb.FolioTotalsRow, error) {
	t, err := s.q(ctx).FolioTotals(ctx, foliosdb.FolioTotalsParams{PropertyID: propertyID, FolioID: folioID})
	return t.Debit.Sub(t.Credit), t, err
}

func fixed(d decimal.Decimal, decimals int32) string { return d.StringFixed(decimals) }

// ---------------------------------------------------------------------------
// Views

func (s *Service) itemViews(ctx context.Context, tenantID, propertyID int64, rows []foliosdb.ListFolioItemsRow, decimals int32) ([]Item, error) {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	comps, err := s.q(ctx).ListItemComponents(ctx, foliosdb.ListItemComponentsParams{TenantID: tenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	byItem := map[int64][]Component{}
	for _, c := range comps {
		byItem[c.FolioItemID] = append(byItem[c.FolioItemID], Component{
			ComponentType: c.ComponentType, Code: c.Code, Name: c.Name, Rate: c.Rate.StringFixed(4),
			BaseAmount: fixed(c.BaseAmount, decimals), Amount: fixed(c.Amount, decimals), Sequence: int(c.Sequence), GLAccountCode: c.GlAccountCode,
		})
	}
	marks, err := s.itemTransfers(ctx, tenantID, propertyID, ids)
	if err != nil {
		return nil, err
	}
	bills := map[int64]*ItemPosBill{}
	if br, err := s.q(ctx).ListPosBillsOfItems(ctx, foliosdb.ListPosBillsOfItemsParams{TenantID: tenantID, PropertyID: propertyID, Ids: ids}); err != nil {
		return nil, err
	} else {
		for _, b := range br {
			bills[b.ItemID] = &ItemPosBill{ID: b.BillID, Number: b.BillNumber, OutletName: b.OutletName}
		}
	}
	out := make([]Item, len(rows))
	for i, r := range rows {
		cs := byItem[r.ID]
		if cs == nil {
			cs = []Component{}
		}
		out[i] = Item{
			ID: r.ID, FolioID: r.FolioID, TransactionType: r.TransactionType, BusinessDate: r.BusinessDate, ServiceDate: r.ServiceDate,
			TransactionAt: r.TransactionAt, Description: r.Description, ChargeCode: deref(r.ChargeCode), ChargeCodeID: r.ChargeCodeID, RevenueAccountCode: r.RevenueAccountCode,
			Quantity: r.Quantity.String(), UnitPrice: fixed(r.UnitPrice, decimals), PriceMode: r.PriceMode,
			BaseAmount: fixed(r.BaseAmount, decimals), DiscountAmount: fixed(r.DiscountAmount, decimals), NetAmount: fixed(r.NetAmount, decimals),
			RoundingAdjustment: fixed(r.RoundingAdjustment, decimals), ServiceChargeTotal: fixed(r.ServiceChargeTotal, decimals),
			TaxTotal: fixed(r.TaxTotal, decimals), Debit: fixed(r.Debit, decimals), Credit: fixed(r.Credit, decimals), Components: cs,
			PaymentID: r.PaymentID, ReversesItemID: r.ReversesItemID, ReversedByItemID: r.ReversedByItemID, GroupCode: r.GroupCode, Reason: deref(r.Reason),
			RoomNumber: r.RoomNumber, StayID: stayShown(r.StayID, r.GuestName), GuestName: r.GuestName, CreatedBy: r.CreatedBy, ApprovedBy: r.ApprovedBy, Transfer: marks[r.ID], PosBill: bills[r.ID],
		}
	}
	return out, nil
}

// loadFolio builds the detail view of a folio row.
func (s *Service) loadFolio(ctx context.Context, tenantID, propertyID int64, f foliosdb.Folio) (Folio, error) {
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Folio{}, err
	}
	rows, err := s.q(ctx).ListFolioItems(ctx, foliosdb.ListFolioItemsParams{TenantID: tenantID, PropertyID: propertyID, FolioID: f.ID})
	if err != nil {
		return Folio{}, err
	}
	items, err := s.itemViews(ctx, tenantID, propertyID, rows, decimals)
	if err != nil {
		return Folio{}, err
	}
	balance, totals, err := s.balanceOf(ctx, propertyID, f.ID)
	if err != nil {
		return Folio{}, err
	}
	company := ""
	if f.BillToCompanyID != nil {
		if company, err = s.q(ctx).GetCompanyName(ctx, foliosdb.GetCompanyNameParams{TenantID: tenantID, PropertyID: propertyID, ID: *f.BillToCompanyID}); err != nil {
			return Folio{}, err
		}
	}
	out := Folio{
		ID: f.ID, FolioNumber: f.FolioNumber, FolioType: f.FolioType, BillToCompanyID: f.BillToCompanyID, BillToCompanyName: company, Status: f.Status, ReservationID: f.ReservationID, StayID: f.StayID,
		BookingGroupID: f.BookingGroupID, OutletID: f.OutletID, BanquetEventID: f.BanquetEventID, OpenedAt: f.OpenedAt, ClosedAt: f.ClosedAt, Version: f.Version, Balance: fixed(balance, decimals),
		Totals: Totals{Debit: fixed(totals.Debit, decimals), Credit: fixed(totals.Credit, decimals)}, Items: items,
	}
	if f.BookingGroupID != nil {
		g, err := s.q(ctx).GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: tenantID, PropertyID: propertyID, ID: *f.BookingGroupID})
		if err != nil {
			return Folio{}, err
		}
		out.BookingGroupCode, out.BookingGroupName = g.Code, g.Name
		out.BookingGroupContactName, out.BookingGroupContactEmail, out.BookingGroupContactPhone = deref(g.ContactName), deref(g.ContactEmail), deref(g.ContactPhone)
	}
	if f.OutletID != nil {
		o, err := s.q(ctx).GetOutletBrief(ctx, foliosdb.GetOutletBriefParams{PropertyID: propertyID, ID: *f.OutletID})
		if err != nil {
			return Folio{}, err
		}
		out.OutletCode, out.OutletName = o.Code, o.Name
	}
	return out, nil
}

// itemView loads one item (by id) with its components.
func (s *Service) itemView(ctx context.Context, tenantID, propertyID, folioID, itemID int64, decimals int32) (Item, error) {
	rows, err := s.q(ctx).ListFolioItems(ctx, foliosdb.ListFolioItemsParams{TenantID: tenantID, PropertyID: propertyID, FolioID: folioID})
	if err != nil {
		return Item{}, err
	}
	for _, r := range rows {
		if r.ID == itemID {
			items, err := s.itemViews(ctx, tenantID, propertyID, []foliosdb.ListFolioItemsRow{r}, decimals)
			return items[0], err
		}
	}
	return Item{}, errItemNotFound()
}

// GetFolio returns a folio with its items (folio.read).
func (s *Service) GetFolio(ctx context.Context, propertyID, id int64) (Folio, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return Folio{}, err
	}
	f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Folio{}, orNotFound(err, errFolioNotFound())
	}
	return s.loadFolio(ctx, p.TenantID, propertyID, f)
}

// likeEscaper makes the words of a search literal in a LIKE pattern: the pattern characters % and _ and the escape character \ itself stand for themselves.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likeEscape(s string) string { return likeEscaper.Replace(s) }

// FolioSortSpec is what the folio list can be sorted by. The default order is the open folios first, by room among them, then the closed ones.
var FolioSortSpec = listsort.Spec{Columns: map[string]listsort.Kind{"folio_number": listsort.Text, "balance": listsort.Num, "status": listsort.Text}, DefaultKind: listsort.Text}

// FolioList is a page of folios and where it ends, when there are more.
type FolioList struct {
	Data []FolioSummary
	Next *listsort.Position
}

// ListFolios lists the folios of a property (folio.read) in the order asked for, a page of limit. pos is where the page before ended (nil for the first page).
func (s *Service) ListFolios(ctx context.Context, propertyID int64, f FolioFilter, order listsort.Sort, pos *listsort.Position, limit int) (FolioList, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return FolioList{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return FolioList{}, err
	}
	after, err := listsort.AfterOf(pos, FolioSortSpec.Kind(order))
	if err != nil {
		return FolioList{}, err
	}
	rows, err := s.q(ctx).ListFoliosOrdered(ctx, foliosdb.ListFoliosOrderedParams{
		SortKey: order.Key, SortDesc: order.Desc, TenantID: p.TenantID, PropertyID: propertyID, ReservationID: f.ReservationID, StayID: f.StayID, BookingGroupID: f.BookingGroupID, FolioType: nullable(f.FolioType), OutletID: f.OutletID, Status: nullable(f.Status), Q: nullable(likeEscape(strings.TrimSpace(f.Search))), CancelledWithBalance: f.CancelledWithBalance,
		AfterID: after.ID, AfterText: after.Text, AfterNum: after.Num, RowLimit: rowLimit(limit + 1),
	})
	if err != nil {
		return FolioList{}, err
	}
	out := FolioList{}
	if len(rows) > limit {
		last := rows[limit-1]
		pos := listsort.Position{ID: last.ID}
		switch order.Key {
		case "folio_number":
			pos.Value = &last.FolioNumber
		case "status":
			pos.Value = &last.Status
		case "balance":
			pos.Value = listsort.NumValue(&last.Balance)
		default: // the default order: open first, then by room, as one text (see the query)
			pos.Value = &last.SortText
		}
		out.Next = &pos
		rows = rows[:limit]
	}
	out.Data = make([]FolioSummary, len(rows))
	for i, r := range rows {
		out.Data[i] = FolioSummary{ID: r.ID, FolioNumber: r.FolioNumber, FolioType: r.FolioType, BillToCompanyID: r.BillToCompanyID, Status: r.Status, ReservationID: r.ReservationID, BookingGroupID: r.BookingGroupID, BookingGroupCode: r.GroupCode, BookingGroupName: r.GroupName, OutletID: r.OutletID, OutletCode: r.OutletCode, OutletName: r.OutletName, BanquetEventID: r.BanquetEventID, ConfirmationNumber: r.ConfirmationNumber, RoomNumber: r.RoomNumber, GuestName: r.GuestName, StayID: r.StayID,
			OpenedAt: r.OpenedAt, Version: r.Version, Balance: fixed(r.Balance, decimals)}
	}
	return out, nil
}

// CloseFolio closes a folio whose balance is zero (folio.post_charge). It is for folios that never got a stay
// (cancelled or no-show reservations): a folio linked to an OPEN stay closes with the check-out.
func (s *Service) CloseFolio(ctx context.Context, propertyID, id int64, version int32) (Folio, error) {
	perm := auth.PermFolioPostCharge
	if pre, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantOf(ctx), PropertyID: propertyID, ID: id}); err == nil && pre.FolioType == FolioTypeMaster {
		perm = auth.PermFolioMasterClose // the bill of a whole group is not closed by every cashier
	}
	p, err := s.actor(ctx, propertyID, perm)
	if err != nil {
		return Folio{}, err
	}
	if version < 1 {
		return Folio{}, apperr.Invalid("the request is invalid", fieldErr("version", "REQUIRED", "the version you loaded"))
	}
	var out Folio
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		f, err := s.lockFolio(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if f.Version != version {
			return apperr.Conflict("VERSION_CONFLICT", "the folio was changed by someone else").WithContext("current_version", f.Version)
		}
		if err := requireOpen(f); err != nil {
			return err
		}
		if f.FolioType == FolioTypeMaster {
			if err := s.requireMasterCanClose(ctx, p.TenantID, propertyID, f); err != nil {
				return err
			}
		}
		if f.StayID != nil {
			status, err := s.q(ctx).GetStayStatus(ctx, foliosdb.GetStayStatusParams{PropertyID: propertyID, ID: *f.StayID})
			if err != nil {
				return err
			}
			if status == "OPEN" {
				return apperr.Conflict("FOLIO_LINKED_TO_OPEN_STAY", "the folio belongs to an in-house stay: it closes with the check-out")
			}
		}
		decimals, err := s.decimals(ctx, propertyID)
		if err != nil {
			return err
		}
		balance, _, err := s.balanceOf(ctx, propertyID, id)
		if err != nil {
			return err
		}
		if !balance.IsZero() {
			return apperr.Conflict("FOLIO_BALANCE_NOT_ZERO", "the folio balance must be zero to close it").WithContext("balance", fixed(balance, decimals))
		}
		closed, err := s.q(ctx).CloseFolio(ctx, foliosdb.CloseFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: s.clock.Now(), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.closed", "folio", id, auditlabel.Folio(ctx, propertyID, id),
			map[string]any{"status": f.Status}, map[string]any{"status": closed.Status})); err != nil {
			return err
		}
		if err := s.lockOnClose(ctx, p, propertyID, []int64{id}, LockReasonFolioClosed); err != nil {
			return err
		}
		out, err = s.loadFolio(ctx, p.TenantID, propertyID, closed)
		return err
	})
	return out, err
}

// tenantOf is the tenant of the caller, 0 when there is none (a read that then finds nothing).
func tenantOf(ctx context.Context) int64 {
	p, err := auth.Require(ctx)
	if err != nil {
		return 0
	}
	return p.TenantID
}

// stayShown is the stay of a line as the view gives it: only on the master folio of a group (where the query names the guest), because on the folio of a stay every line has the stay of the folio.
func stayShown(stay *int64, guest string) *int64 {
	if guest == "" {
		return nil
	}
	return stay
}
