package shifts

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/shifts/shiftsdb"
	"kamarapms/internal/tenancy"
)

// Service is the cashier shift application service.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	iam   *iam.Service
	acct  *accounting.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, i *iam.Service, acct *accounting.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, iam: i, acct: acct}
}

func (s *Service) q(ctx context.Context) *shiftsdb.Queries { return shiftsdb.New(s.txm.DB(ctx)) }

func (s *Service) actor(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

// can says whether the caller holds a permission at the property (the property itself is checked by the caller).
func (s *Service) can(ctx context.Context, propertyID int64, perm auth.Permission) bool {
	return s.authz.Require(ctx, propertyID, perm) == nil
}

func (s *Service) decimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

func errShiftNotFound() *apperr.Error {
	return apperr.NotFound("SHIFT_NOT_FOUND", "the shift does not exist in this property")
}

func errNoOpenShift() *apperr.Error {
	return apperr.Conflict("NO_OPEN_SHIFT", "open a cashier shift before taking cash")
}

func errShiftNotOpen() *apperr.Error {
	return apperr.Conflict("SHIFT_NOT_OPEN", "the shift is closed")
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "cashier_shift", EntityID: id, Old: old, New: updated}
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

func ptr[T any](v T) *T { return &v }

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func fixed(d decimal.Decimal, decimals int32) string { return d.StringFixed(decimals) }

// ---------------------------------------------------------------------------------------------------------------
// Settings

func toSettings(r shiftsdb.PropertyCashierSetting, decimals int32) Settings {
	return Settings{RequireShiftForCash: r.RequireShiftForCash, MaxVariance: fixed(r.MaxVariance, decimals), BlockNightAudit: r.BlockNightAudit}
}

func (s *Service) settingsRow(ctx context.Context, tenantID, propertyID int64) (shiftsdb.PropertyCashierSetting, error) {
	row, err := s.q(ctx).GetSettings(ctx, shiftsdb.GetSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	if isNoRows(err) { // every property has a row; the defaults stand if one is missing
		return shiftsdb.PropertyCashierSetting{RequireShiftForCash: true, BlockNightAudit: true}, nil
	}
	return row, err
}

// GetSettings answers the cashier settings (any cashier permission).
func (s *Service) GetSettings(ctx context.Context, propertyID int64) (Settings, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Settings{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return Settings{}, err
	}
	if !s.can(ctx, propertyID, auth.PermCashierShift) && !s.can(ctx, propertyID, auth.PermCashierShiftManage) && !s.can(ctx, propertyID, auth.PermCashierSettings) {
		return Settings{}, s.authz.Require(ctx, propertyID, auth.PermCashierShift)
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Settings{}, err
	}
	row, err := s.settingsRow(ctx, p.TenantID, propertyID)
	if err != nil {
		return Settings{}, err
	}
	return toSettings(row, decimals), nil
}

// UpdateSettings changes the cashier settings (cashier.settings).
func (s *Service) UpdateSettings(ctx context.Context, propertyID int64, in SettingsInput) (Settings, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCashierSettings)
	if err != nil {
		return Settings{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Settings{}, err
	}
	limit, fe := parseAmount("max_variance", in.MaxVariance, decimals, true)
	if fe != nil {
		return Settings{}, apperr.Invalid("the settings are invalid", *fe)
	}
	var out Settings
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		old, err := s.settingsRow(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).UpdateSettings(ctx, shiftsdb.UpdateSettingsParams{
			TenantID: p.TenantID, PropertyID: propertyID, RequireShiftForCash: in.RequireShiftForCash, MaxVariance: limit, BlockNightAudit: in.BlockNightAudit, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toSettings(row, decimals)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cashier.settings_changed", row.ID, toSettings(old, decimals), out))
	})
	return out, err
}

// ---------------------------------------------------------------------------------------------------------------
// The gate for cash

// CashShift is what a cash payment, a cash refund or a cash receipt asks: the open shift of the caller to put it on, locked in share mode so it cannot be
// closed meanwhile. A property that requires a shift for cash answers 409 NO_OPEN_SHIFT without one; otherwise no shift is nil. It runs in the caller's
// transaction, before the document number is taken (lock order: the shift is at the level of the payments, the sequences are last).
func (s *Service) CashShift(ctx context.Context, propertyID int64) (*int64, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := s.settingsRow(ctx, p.TenantID, propertyID)
	if err != nil {
		return nil, err
	}
	none := func() (*int64, error) {
		if cfg.RequireShiftForCash {
			return nil, errNoOpenShift()
		}
		return nil, nil
	}
	id, err := s.q(ctx).OpenShiftOfUser(ctx, shiftsdb.OpenShiftOfUserParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID})
	if isNoRows(err) {
		return none()
	}
	if err != nil {
		return nil, err
	}
	if err := db.LockRows(ctx, db.CashierShifts, db.ForShare, propertyID, []int64{id}); err != nil {
		return nil, err
	}
	sh, err := s.q(ctx).GetShift(ctx, shiftsdb.GetShiftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return nil, err
	}
	if sh.Status != StatusOpen { // closed while this request waited for the lock
		return none()
	}
	return &id, nil
}

// OpenShifts lists the shifts that are open now (the night audit refuses to run while one is).
func (s *Service) OpenShifts(ctx context.Context, tenantID, propertyID int64) ([]OpenShift, bool, error) {
	cfg, err := s.settingsRow(ctx, tenantID, propertyID)
	if err != nil {
		return nil, false, err
	}
	rows, err := s.q(ctx).ListOpenShifts(ctx, shiftsdb.ListOpenShiftsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, false, err
	}
	out := make([]OpenShift, 0, len(rows))
	for _, r := range rows {
		out = append(out, OpenShift{ID: r.ID, Number: r.ShiftNumber, Drawer: r.Drawer, UserID: r.UserID, UserName: r.UserName, OpenedAt: r.OpenedAt})
	}
	return out, cfg.BlockNightAudit, nil
}

// ---------------------------------------------------------------------------------------------------------------
// Reading

func toShift(r shiftsdb.GetShiftRow, decimals int32) Shift {
	sh := Shift{
		ID: r.ID, Number: r.ShiftNumber, UserID: r.UserID, UserName: r.UserName, Drawer: r.Drawer, Status: r.Status, OpenedAt: r.OpenedAt,
		BusinessDateOpened: r.BusinessDateOpened, OpeningFloat: fixed(r.OpeningFloat, decimals), ClosedAt: r.ClosedAt, BusinessDateClosed: r.BusinessDateClosed,
		VarianceReason: deref(r.VarianceReason), JournalID: r.JournalID, ApprovedBy: r.ApprovedBy, HandedOverTo: r.HandedOverTo,
	}
	for _, f := range []struct {
		d   *decimal.Decimal
		out **string
	}{{r.ExpectedCash, &sh.ExpectedCash}, {r.CountedCash, &sh.CountedCash}, {r.OverShort, &sh.OverShort}} {
		if f.d != nil {
			*f.out = ptr(fixed(*f.d, decimals))
		}
	}
	return sh
}

func toListed(r shiftsdb.ListShiftsRow, decimals int32) Shift {
	return toShift(shiftsdb.GetShiftRow(r), decimals)
}

// cashOf computes what went through the drawer of a shift and the cash expected in it. A closed shift is read up to its closing, so it never moves.
func (s *Service) cashOf(ctx context.Context, tenantID, propertyID int64, sh shiftsdb.GetShiftRow, decimals int32) (Cash, decimal.Decimal, error) {
	until := s.clock.Now().Add(time.Minute)
	if sh.ClosedAt != nil {
		until = *sh.ClosedAt
	}
	c, err := s.q(ctx).ShiftCash(ctx, shiftsdb.ShiftCashParams{PropertyID: propertyID, ShiftID: sh.ID, UserID: sh.UserID, OpenedAt: sh.OpenedAt, Until: until})
	if err != nil {
		return Cash{}, decimal.Zero, err
	}
	exp := Expected(sh.OpeningFloat, c.PaymentsIn, c.RefundsOut, c.ReceiptsIn, c.VoidedAfterClose, c.PayIns, c.PayOuts, c.Drops)
	return Cash{
		OpeningFloat: fixed(sh.OpeningFloat, decimals), Payments: fixed(c.PaymentsIn, decimals), Refunds: fixed(c.RefundsOut, decimals), Receipts: fixed(c.ReceiptsIn, decimals),
		VoidedAfterClose: fixed(c.VoidedAfterClose, decimals), PayIns: fixed(c.PayIns, decimals), PayOuts: fixed(c.PayOuts, decimals), Drops: fixed(c.Drops, decimals),
		Expected: fixed(exp, decimals),
	}, exp, nil
}

func (s *Service) detail(ctx context.Context, tenantID, propertyID, id int64, decimals int32) (Shift, error) {
	row, err := s.q(ctx).GetShift(ctx, shiftsdb.GetShiftParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if isNoRows(err) {
		return Shift{}, errShiftNotFound()
	}
	if err != nil {
		return Shift{}, err
	}
	sh := toShift(row, decimals)
	cash, _, err := s.cashOf(ctx, tenantID, propertyID, row, decimals)
	if err != nil {
		return Shift{}, err
	}
	sh.Cash = &cash
	moves, err := s.q(ctx).ListMovements(ctx, shiftsdb.ListMovementsParams{TenantID: tenantID, PropertyID: propertyID, ShiftID: id})
	if err != nil {
		return Shift{}, err
	}
	sh.Movements = make([]Movement, 0, len(moves))
	for _, m := range moves {
		sh.Movements = append(sh.Movements, Movement{
			ID: m.ID, Kind: m.Kind, Amount: fixed(m.Amount, decimals), AccountID: m.AccountID, AccountCode: m.AccountCode, AccountName: m.AccountName, Reason: m.Reason,
			BusinessDate: m.BusinessDate, JournalID: m.JournalID, JournalNumber: m.JournalNumber, CreatedAt: m.CreatedAt,
		})
	}
	counts, err := s.q(ctx).ListCounts(ctx, shiftsdb.ListCountsParams{TenantID: tenantID, PropertyID: propertyID, ShiftID: id})
	if err != nil {
		return Shift{}, err
	}
	sh.Counts = make([]Count, 0, len(counts))
	for _, c := range counts {
		sh.Counts = append(sh.Counts, Count{Denomination: fixed(c.Denomination, decimals), Quantity: int(c.Quantity)})
	}
	return sh, nil
}

// canSee says whether the caller may see the shift: the owner with cashier.shift, anyone else with cashier.shift_manage.
func (s *Service) canSee(ctx context.Context, p auth.Principal, propertyID, owner int64) error {
	if s.can(ctx, propertyID, auth.PermCashierShiftManage) {
		return nil
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermCashierShift); err != nil {
		return err
	}
	if owner != p.UserID {
		return s.authz.Require(ctx, propertyID, auth.PermCashierShiftManage)
	}
	return nil
}

// Get answers a shift with its cash, its movements and its count (the owner, or cashier.shift_manage).
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Shift, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Shift{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return Shift{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Shift{}, err
	}
	sh, err := s.detail(ctx, p.TenantID, propertyID, id, decimals)
	if err != nil {
		return Shift{}, err
	}
	return sh, s.canSee(ctx, p, propertyID, sh.UserID)
}

// Current answers the open shift of the caller with its cash, or nil.
func (s *Service) Current(ctx context.Context, propertyID int64) (*Shift, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCashierShift)
	if err != nil {
		return nil, err
	}
	id, err := s.q(ctx).OpenShiftOfUser(ctx, shiftsdb.OpenShiftOfUserParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	sh, err := s.detail(ctx, p.TenantID, propertyID, id, decimals)
	return &sh, err
}

// List answers shifts, newest first: all of them with cashier.shift_manage, otherwise the caller's own.
func (s *Service) List(ctx context.Context, propertyID int64, f Filter, before int64, limit int) ([]Shift, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	if !s.can(ctx, propertyID, auth.PermCashierShiftManage) {
		if err := s.authz.Require(ctx, propertyID, auth.PermCashierShift); err != nil {
			return nil, err
		}
		f.UserID = &p.UserID
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if before <= 0 {
		before = 1 << 62
	}
	if f.Status != "" && f.Status != StatusOpen && f.Status != StatusClosed {
		return nil, apperr.Invalid("the query is invalid", fieldErr("status", "INVALID_VALUE", "OPEN or CLOSED"))
	}
	rows, err := s.q(ctx).ListShifts(ctx, shiftsdb.ListShiftsParams{
		TenantID: p.TenantID, PropertyID: propertyID, BeforeID: before, UserID: f.UserID, Status: nullable(f.Status), FromDate: f.From, ToDate: f.To, RowLimit: int32(limit), //nolint:gosec // G115: the page limit is small
	})
	if err != nil {
		return nil, err
	}
	out := make([]Shift, 0, len(rows))
	for _, r := range rows {
		out = append(out, toListed(r, decimals))
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------
// Opening

// Open opens a shift for the caller (cashier.shift): a float, in a drawer. One shift is open per cashier and per drawer.
func (s *Service) Open(ctx context.Context, propertyID int64, in OpenInput) (Shift, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCashierShift)
	if err != nil {
		return Shift{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Shift{}, err
	}
	drawer, fe := in.parseDrawer()
	var fields []apperr.FieldError
	if fe != nil {
		fields = append(fields, *fe)
	}
	var opening *decimal.Decimal
	if in.OpeningFloat != nil {
		d, ferr := parseAmount("opening_float", *in.OpeningFloat, decimals, true)
		if ferr != nil {
			fields = append(fields, *ferr)
		}
		opening = &d
	}
	if len(fields) > 0 {
		return Shift{}, apperr.Invalid("the shift is invalid", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.OpenShiftOfUser(ctx, shiftsdb.OpenShiftOfUserParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID}); err == nil {
			return apperr.Conflict("SHIFT_ALREADY_OPEN", "you have a shift open already")
		} else if !isNoRows(err) {
			return err
		}
		float := decimal.Zero
		if opening != nil {
			float = *opening
		} else if left, err := q.LastClosedOfDrawer(ctx, shiftsdb.LastClosedOfDrawerParams{TenantID: p.TenantID, PropertyID: propertyID, Drawer: drawer}); err == nil {
			float = left
		} else if !isNoRows(err) {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqShift)
		if err != nil {
			return err
		}
		id, err = q.InsertShift(ctx, shiftsdb.InsertShiftParams{
			TenantID: p.TenantID, PropertyID: propertyID, ShiftNumber: number, UserID: p.UserID, Drawer: drawer, OpenedAt: s.clock.Now(), BusinessDateOpened: day.BusinessDate, OpeningFloat: float,
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cashier.shift_opened", id, nil, map[string]any{
			"shift_number": number, "drawer": drawer, "opening_float": float.String(),
		}))
	})
	if err != nil {
		return Shift{}, err
	}
	return s.detail(ctx, p.TenantID, propertyID, id, decimals)
}

// SuggestedFloat is what the last shift of a drawer left in it (what was counted; the drops were taken out before): the float a new shift of that drawer starts with.
func (s *Service) SuggestedFloat(ctx context.Context, propertyID int64, drawer string) (string, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCashierShift)
	if err != nil {
		return "", err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return "", err
	}
	d := strings.ToUpper(strings.TrimSpace(drawer))
	if d == "" {
		d = defaultDrawer
	}
	left, err := s.q(ctx).LastClosedOfDrawer(ctx, shiftsdb.LastClosedOfDrawerParams{TenantID: p.TenantID, PropertyID: propertyID, Drawer: d})
	if isNoRows(err) {
		return fixed(decimal.Zero, decimals), nil
	}
	return fixed(left, decimals), err
}

// ---------------------------------------------------------------------------------------------------------------
// Movements

// Move records a drop to the safe, a pay-in or a pay-out on an open shift (the owner, or cashier.shift_manage). A pay-in and a pay-out are journaled against the
// account chosen; a drop is not: the cash stays in the books. key is the request's Idempotency-Key.
func (s *Service) Move(ctx context.Context, propertyID, shiftID int64, key string, in MovementInput) (Shift, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Shift{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return Shift{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Shift{}, err
	}
	amount, fields := in.parse(decimals)
	if key == "" || len(key) > 100 {
		fields = append(fields, fieldErr("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return Shift{}, apperr.Invalid("the movement is invalid", fields...)
	}
	reason := strings.TrimSpace(in.Reason)
	if m, err := s.q(ctx).GetMovementByKey(ctx, shiftsdb.GetMovementByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
		if m.ShiftID != shiftID || m.Kind != in.Kind || !m.Amount.Equal(amount) {
			return Shift{}, apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used for another request")
		}
		return s.Get(ctx, propertyID, shiftID)
	} else if !isNoRows(err) {
		return Shift{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetShift(ctx, shiftsdb.GetShiftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: shiftID}); err != nil {
			return orNotFound(err)
		}
		if err := db.LockRows(ctx, db.CashierShifts, db.ForShare, propertyID, []int64{shiftID}); err != nil {
			return err
		}
		sh, err := q.GetShift(ctx, shiftsdb.GetShiftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: shiftID})
		if err != nil {
			return orNotFound(err)
		}
		if err := s.canSee(ctx, p, propertyID, sh.UserID); err != nil {
			return err
		}
		if sh.Status != StatusOpen {
			return errShiftNotOpen()
		}
		var accountID, journalID *int64
		if in.Kind != KindDrop {
			jid, err := s.postMovement(ctx, p, propertyID, day.BusinessDate, sh.ShiftNumber, in, amount, reason)
			if err != nil {
				return err
			}
			accountID, journalID = ptr(in.AccountID), &jid
		}
		id, err := q.InsertMovement(ctx, shiftsdb.InsertMovementParams{
			TenantID: p.TenantID, PropertyID: propertyID, ShiftID: shiftID, Kind: in.Kind, Amount: amount, AccountID: accountID, Reason: reason,
			BusinessDate: day.BusinessDate, JournalID: journalID, IdempotencyKey: &key, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cashier.cash_moved", shiftID, nil, map[string]any{
			"movement_id": id, "kind": in.Kind, "amount": amount.String(), "reason": reason,
		}))
	})
	if err != nil {
		return Shift{}, err
	}
	return s.detail(ctx, p.TenantID, propertyID, shiftID, decimals)
}

func orNotFound(err error) error {
	if isNoRows(err) {
		return errShiftNotFound()
	}
	return err
}

// postMovement journals a pay-in (Dr cash, Cr the account) or a pay-out (Dr the account, Cr cash) and answers the journal id.
func (s *Service) postMovement(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, shiftNumber string, in MovementInput, amount decimal.Decimal, reason string) (int64, error) {
	po, err := s.acct.BeginPosting(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	cash, err := po.SystemAccount(ctx, accounting.KeyCash)
	if err != nil {
		return 0, err
	}
	if in.AccountID == cash {
		return 0, apperr.Invalid("the movement is invalid", fieldErr("account_id", "INVALID_VALUE", "not the cash account itself"))
	}
	if err := po.CheckAccount(ctx, in.AccountID, "account_id"); err != nil {
		return 0, err
	}
	other := accounting.SystemLine{AccountID: in.AccountID, SourceType: "SHIFT", SourceRef: shiftNumber, Description: reason}
	till := accounting.SystemLine{AccountID: cash, SourceType: "SHIFT", SourceRef: shiftNumber, Description: reason}
	label := "Pay-in"
	if in.Kind == KindPayIn {
		till.Debit, other.Credit = amount, amount
	} else {
		other.Debit, till.Credit = amount, amount
		label = "Pay-out"
	}
	id, _, err := po.Post(ctx, accounting.SystemJournal{
		Type: accounting.JournalCashier, Date: bd, Description: label + " " + shiftNumber + ": " + reason, Reference: shiftNumber, Lines: []accounting.SystemLine{till, other},
	})
	return id, err
}

// ---------------------------------------------------------------------------------------------------------------
// Closing

func (in CloseInput) parse(decimals int32) (counted decimal.Decimal, counts []Count, fields []apperr.FieldError) {
	counted, fe := parseAmount("counted_cash", in.CountedCash, decimals, true)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if len(in.Reason) > maxReason {
		fields = append(fields, fieldErr("reason", "INVALID_VALUE", "at most 500 characters"))
	}
	if len(in.Counts) > maxCountRows {
		fields = append(fields, fieldErr("counts", "TOO_MANY", "at most 50 denominations"))
		return counted, nil, fields
	}
	total, seen := decimal.Zero, map[string]bool{}
	for i, c := range in.Counts {
		d, err := money.Parse(strings.TrimSpace(c.Denomination))
		if err != nil || !d.IsPositive() || !d.Equal(d.Round(decimals)) {
			fields = append(fields, fieldErr("counts["+itoa(i)+"].denomination", "INVALID_AMOUNT", "an amount above zero"))
			continue
		}
		if c.Quantity < 0 {
			fields = append(fields, fieldErr("counts["+itoa(i)+"].quantity", "INVALID_VALUE", "not negative"))
			continue
		}
		if seen[d.String()] {
			fields = append(fields, fieldErr("counts["+itoa(i)+"].denomination", "DUPLICATE_VALUE", "a denomination is counted once"))
			continue
		}
		seen[d.String()] = true
		total = total.Add(d.Mul(decimal.NewFromInt(int64(c.Quantity))))
		counts = append(counts, Count{Denomination: d.String(), Quantity: c.Quantity})
	}
	if len(in.Counts) > 0 && len(fields) == 0 && !total.Equal(counted) {
		fields = append(fields, fieldErr("counted_cash", "COUNT_MISMATCH", "the denominations add up to "+total.StringFixed(decimals)))
	}
	return counted, counts, fields
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }

// Close closes a shift with the cash counted (the owner, or cashier.shift_manage). The difference to the cash expected is the over or short; any difference
// beyond the limit of the property (0 by default: every difference) needs a reason and the approval of a user with cashier.shift_approve, and is journaled
// on the business date against the cash over and short account. A closed shift never changes.
func (s *Service) Close(ctx context.Context, propertyID, shiftID int64, in CloseInput) (Shift, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Shift{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return Shift{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Shift{}, err
	}
	counted, counts, fields := in.parse(decimals)
	if len(fields) > 0 {
		return Shift{}, apperr.Invalid("the close is invalid", fields...)
	}
	// An approval that is given is checked now, so a wrong password is refused whether or not the difference needs it.
	var approval iam.Approval
	if in.Approval != nil {
		if approval, err = s.iam.VerifyApprovalFor(ctx, propertyID, in.Approval, auth.PermCashierShiftApprove); err != nil {
			return Shift{}, err
		}
	}
	reason := strings.TrimSpace(in.Reason)
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetShift(ctx, shiftsdb.GetShiftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: shiftID}); err != nil {
			return orNotFound(err)
		}
		if err := db.LockRows(ctx, db.CashierShifts, db.ForUpdate, propertyID, []int64{shiftID}); err != nil {
			return err
		}
		row, err := q.GetShift(ctx, shiftsdb.GetShiftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: shiftID})
		if err != nil {
			return orNotFound(err)
		}
		if err := s.canSee(ctx, p, propertyID, row.UserID); err != nil {
			return err
		}
		if row.Status != StatusOpen {
			return errShiftNotOpen()
		}
		if in.HandOverTo != nil {
			n, err := q.UserInProperty(ctx, shiftsdb.UserInPropertyParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: *in.HandOverTo})
			if err != nil {
				return err
			}
			if n == 0 || *in.HandOverTo == row.UserID {
				return apperr.Invalid("the close is invalid", fieldErr("hand_over_to", "INVALID_VALUE", "another user of this property"))
			}
		}
		_, expected, err := s.cashOf(ctx, p.TenantID, propertyID, row, decimals)
		if err != nil {
			return err
		}
		overShort := counted.Sub(expected)
		cfg, err := s.settingsRow(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		var journalID *int64
		var approvedBy *int64
		if !overShort.IsZero() {
			if Exceeds(overShort, cfg.MaxVariance) {
				if reason == "" {
					return apperr.Invalid("the close is invalid", fieldErr("reason", "REQUIRED", "say why the cash is over or short"))
				}
				if approval.IsZero() {
					return apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "this difference needs an approval: the approver's email and password").
						WithContext("permission", string(auth.PermCashierShiftApprove)).WithContext("over_short", overShort.StringFixed(decimals))
				}
				approvedBy = ptr(approval.UserID())
			} else if !approval.IsZero() {
				approvedBy = ptr(approval.UserID())
			}
			if jid, err := s.postOverShort(ctx, propertyID, day.BusinessDate, row.ShiftNumber, overShort); err != nil {
				if !apperr.IsCode(err, "ACCOUNTING_NOT_SET_UP") { // a property without books has nothing to journal
					return err
				}
			} else {
				journalID = &jid
			}
		}
		if _, err := q.CloseShift(ctx, shiftsdb.CloseShiftParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: shiftID, ClosedAt: s.clock.Now(), BusinessDateClosed: day.BusinessDate, ExpectedCash: expected, CountedCash: counted,
			OverShort: overShort, VarianceReason: nullable(reason), JournalID: journalID, ClosedBy: p.UserID, ApprovedBy: approvedBy, HandedOverTo: in.HandOverTo,
		}); err != nil {
			return err
		}
		for _, c := range counts {
			if err := q.InsertCount(ctx, shiftsdb.InsertCountParams{
				TenantID: p.TenantID, PropertyID: propertyID, ShiftID: shiftID, Denomination: decimal.RequireFromString(c.Denomination), Quantity: int32(c.Quantity), //nolint:gosec // G115: a count is small
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cashier.shift_closed", shiftID, nil, map[string]any{
			"shift_number": row.ShiftNumber, "expected_cash": expected.String(), "counted_cash": counted.String(), "over_short": overShort.String(), "reason": reason,
			"approved_by": approvedBy, "journal_id": journalID,
		}))
	})
	if err != nil {
		return Shift{}, err
	}
	return s.detail(ctx, p.TenantID, propertyID, shiftID, decimals)
}

// postOverShort journals the difference of a shift: short (the count is less than expected) debits the cash over and short account and credits cash, over does the opposite.
func (s *Service) postOverShort(ctx context.Context, propertyID int64, bd civil.Date, shiftNumber string, overShort decimal.Decimal) (int64, error) {
	po, err := s.acct.BeginPosting(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	cash, err := po.SystemAccount(ctx, accounting.KeyCash)
	if err != nil {
		return 0, err
	}
	ov, err := po.SystemAccount(ctx, accounting.KeyCashOverShort)
	if err != nil {
		return 0, err
	}
	amount := overShort.Abs()
	till := accounting.SystemLine{AccountID: cash, SourceType: "SHIFT", SourceRef: shiftNumber}
	diff := accounting.SystemLine{AccountID: ov, SourceType: "SHIFT", SourceRef: shiftNumber}
	label := "Cash over, shift "
	if overShort.IsNegative() {
		diff.Debit, till.Credit = amount, amount
		label = "Cash short, shift "
	} else {
		till.Debit, diff.Credit = amount, amount
	}
	till.Description, diff.Description = label+shiftNumber, label+shiftNumber
	id, _, err := po.Post(ctx, accounting.SystemJournal{
		Type: accounting.JournalCashier, Date: bd, Description: label + shiftNumber, Reference: shiftNumber, Lines: []accounting.SystemLine{till, diff},
	})
	return id, err
}
