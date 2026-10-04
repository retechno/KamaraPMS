package folios

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
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

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, old, updated any) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
		Action: action, EntityType: entity, EntityID: id, Old: old, New: updated,
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
		return apperr.Conflict("FOLIO_CLOSED", "the folio is closed").WithContext("folio_number", f.FolioNumber)
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
			PaymentID: r.PaymentID, ReversesItemID: r.ReversesItemID, ReversedByItemID: r.ReversedByItemID, Reason: deref(r.Reason),
			RoomNumber: deref(r.RoomNumber), CreatedBy: r.CreatedBy, ApprovedBy: r.ApprovedBy,
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
	return Folio{
		ID: f.ID, FolioNumber: f.FolioNumber, FolioType: f.FolioType, Status: f.Status, ReservationID: f.ReservationID, StayID: f.StayID,
		OpenedAt: f.OpenedAt, ClosedAt: f.ClosedAt, Version: f.Version, Balance: fixed(balance, decimals),
		Totals: Totals{Debit: fixed(totals.Debit, decimals), Credit: fixed(totals.Credit, decimals)}, Items: items,
	}, nil
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

// ListFolios lists folios with their balances (folio.read).
func (s *Service) ListFolios(ctx context.Context, propertyID int64, f FolioFilter, afterID int64, limit int) ([]FolioSummary, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListFolios(ctx, foliosdb.ListFoliosParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, ReservationID: f.ReservationID, StayID: f.StayID,
		Status: nullable(f.Status), RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]FolioSummary, len(rows))
	for i, r := range rows {
		out[i] = FolioSummary{ID: r.ID, FolioNumber: r.FolioNumber, Status: r.Status, ReservationID: r.ReservationID, StayID: r.StayID,
			OpenedAt: r.OpenedAt, Version: r.Version, Balance: fixed(r.Debit.Sub(r.Credit), decimals)}
	}
	return out, nil
}

// CloseFolio closes a folio whose balance is zero (folio.post_charge). It is for folios that never got a stay
// (cancelled or no-show reservations): a folio linked to an OPEN stay closes with the check-out.
func (s *Service) CloseFolio(ctx context.Context, propertyID, id int64, version int32) (Folio, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioPostCharge)
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
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.closed", "folio", id,
			map[string]any{"status": f.Status}, map[string]any{"status": closed.Status})); err != nil {
			return err
		}
		out, err = s.loadFolio(ctx, p.TenantID, propertyID, closed)
		return err
	})
	return out, err
}
