package assets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The disposal of an asset (docs/architecture/37-fixed-assets.md 4.7 and 4.8): a sale, a write-off, or a part of a batch of operating equipment. One journal of type ASSET, dated the disposal date, takes
// the cost and the accumulated depreciation off the books and puts the gain or loss on the account of the key ASSET_DISPOSAL; the ledger gets a DISPOSE row (ledger.go).
//
// The approval of the kind ASSET_DISPOSAL (document 32) is asked for every disposal. When the person who disposes may approve their own under the rule of the property, or brings the credentials of
// the approver, the disposal is posted at once. Otherwise it becomes a request that waits (fa_disposal_requests): nothing is posted until an approver, signed in, decides it (the mode Decision of
// iam.Verify, as the count of the stock). An asset with a request waiting is left out of the depreciation, cannot be moved, changed or disposed of again.

// Kinds and reasons of a disposal.
const (
	DisposalSale     = "SALE"
	DisposalWriteOff = "WRITE_OFF"

	ReasonSold     = "SOLD"
	ReasonDamaged  = "DAMAGED"
	ReasonLost     = "LOST"
	ReasonObsolete = "OBSOLETE"
	ReasonDonated  = "DONATED"
	ReasonOther    = "OTHER"

	DisposalPosted   = "POSTED"
	DisposalReversed = "REVERSED"

	RequestPending  = "PENDING"
	RequestApproved = "APPROVED"
	RequestRejected = "REJECTED"

	statusDisposed = "DISPOSED"
	maxNote        = 500
)

// Approver verifies the approval of a use case (iam.Service).
type Approver interface {
	Verify(ctx context.Context, req iam.ApprovalRequest) (iam.Approval, error)
}

// SetApprover wires the approver of the disposals (internal/app).
func (s *Service) SetApprover(a Approver) { s.approver = a }

// DisposeInput is what is asked of a disposal. The date is the open business day unless it is given; the units are those of a batch (default 1).
type DisposeInput struct {
	Kind              string             `json:"kind"`
	DisposalDate      *civil.Date        `json:"disposal_date"`
	Units             *int32             `json:"units"`
	Proceeds          *decimal.Decimal   `json:"proceeds"`
	ProceedsAccountID *int64             `json:"proceeds_account_id"`
	ReasonCode        string             `json:"reason_code"`
	Note              string             `json:"note"`
	Approval          *iam.ApprovalInput `json:"approval"`
	IdempotencyKey    string             `json:"-"`
}

// DisposalLine is a line of the journal of a disposal.
type DisposalLine struct {
	AccountID    int64           `json:"account_id"`
	AccountCode  string          `json:"account_code"`
	AccountName  string          `json:"account_name"`
	Debit        decimal.Decimal `json:"debit"`
	Credit       decimal.Decimal `json:"credit"`
	DepartmentID *int64          `json:"department_id"`
}

// DisposalPreview is the disposal worked out and not made: the amounts, the journal and the amount of the approval.
type DisposalPreview struct {
	AssetID         int64           `json:"asset_id"`
	AssetNumber     string          `json:"asset_number"`
	AssetName       string          `json:"asset_name"`
	Kind            string          `json:"kind"`
	ReasonCode      string          `json:"reason_code"`
	DisposalDate    civil.Date      `json:"disposal_date"`
	Units           int32           `json:"units"`
	UnitCount       int32           `json:"unit_count"`
	CostRemoved     decimal.Decimal `json:"cost_removed"`
	AccumRemoved    decimal.Decimal `json:"accum_removed"`
	CarryingRemoved decimal.Decimal `json:"carrying_removed"`
	Proceeds        decimal.Decimal `json:"proceeds"`
	GainLoss        decimal.Decimal `json:"gain_loss"`
	ApprovalAmount  decimal.Decimal `json:"approval_amount"`
	PKPOnDate       bool            `json:"pkp_on_date"`
	Lines           []DisposalLine  `json:"lines"`
}

// Disposal is a posted disposal.
type Disposal struct {
	ID                    int64           `json:"id"`
	Number                string          `json:"number"`
	AssetID               int64           `json:"asset_id"`
	AssetNumber           string          `json:"asset_number"`
	AssetName             string          `json:"asset_name"`
	Kind                  string          `json:"kind"`
	ReasonCode            string          `json:"reason_code"`
	DisposalDate          civil.Date      `json:"disposal_date"`
	Units                 int32           `json:"units"`
	CostRemoved           decimal.Decimal `json:"cost_removed"`
	AccumRemoved          decimal.Decimal `json:"accum_removed"`
	CarryingRemoved       decimal.Decimal `json:"carrying_removed"`
	Proceeds              decimal.Decimal `json:"proceeds"`
	ProceedsAccountID     *int64          `json:"proceeds_account_id"`
	ProceedsAccountCode   *string         `json:"proceeds_account_code"`
	ProceedsAccountName   *string         `json:"proceeds_account_name"`
	GainLoss              decimal.Decimal `json:"gain_loss"`
	Note                  *string         `json:"note"`
	JournalID             int64           `json:"journal_id"`
	JournalNumber         string          `json:"journal_number"`
	Status                string          `json:"status"`
	ReversalJournalID     *int64          `json:"reversal_journal_id"`
	ReversalJournalNumber *string         `json:"reversal_journal_number"`
	ReversalReason        *string         `json:"reversal_reason"`
	ReversedByName        *string         `json:"reversed_by_name"`
	ApprovedByName        *string         `json:"approved_by_name"`
	CreatedByName         *string         `json:"created_by_name"`
	CreatedAt             string          `json:"created_at"`
}

// DisposalRequest is a disposal that waits for an approver, or that was decided.
type DisposalRequest struct {
	ID                  int64           `json:"id"`
	AssetID             int64           `json:"asset_id"`
	AssetNumber         string          `json:"asset_number"`
	AssetName           string          `json:"asset_name"`
	Kind                string          `json:"kind"`
	ReasonCode          string          `json:"reason_code"`
	DisposalDate        civil.Date      `json:"disposal_date"`
	Units               int32           `json:"units"`
	Proceeds            decimal.Decimal `json:"proceeds"`
	ProceedsAccountID   *int64          `json:"proceeds_account_id"`
	ProceedsAccountCode *string         `json:"proceeds_account_code"`
	ProceedsAccountName *string         `json:"proceeds_account_name"`
	Note                *string         `json:"note"`
	ApprovalAmount      decimal.Decimal `json:"approval_amount"`
	Status              string          `json:"status"`
	RequestedBy         *int64          `json:"requested_by"`
	RequestedByName     *string         `json:"requested_by_name"`
	RequestedAt         string          `json:"requested_at"`
	DecidedByName       *string         `json:"decided_by_name"`
	DecidedAt           *string         `json:"decided_at"`
	DecisionNote        *string         `json:"decision_note"`
	DisposalID          *int64          `json:"disposal_id"`
	DisposalNumber      *string         `json:"disposal_number"`
}

// DisposeResult is the answer of a disposal: posted at once (Disposal) or waiting for an approver (Request).
type DisposeResult struct {
	Status   string           `json:"status"` // POSTED or PENDING
	Disposal *Disposal        `json:"disposal"`
	Request  *DisposalRequest `json:"request"`
	Replayed bool             `json:"replayed"`
}

// disposalPlan is a disposal worked out for an asset that is locked.
type disposalPlan struct {
	asset          assetsdb.GetAssetRow
	cat            assetsdb.FaCategory
	kind           string
	reason         string
	date           civil.Date
	units          int32
	note           *string
	proceeds       decimal.Decimal
	proceedsAcc    *int64
	cost, accum    decimal.Decimal
	carrying, gain decimal.Decimal
	approvalAmount decimal.Decimal
	lines          []accounting.SystemLine
	preview        []DisposalLine
	deptID         *int64
}

func validReason(kind, reason string) bool {
	if kind == DisposalSale {
		return reason == ReasonSold
	}
	switch reason {
	case ReasonDamaged, ReasonLost, ReasonObsolete, ReasonDonated, ReasonOther:
		return true
	}
	return false
}

// proceedsAccountOK says whether an account may receive the proceeds of a sale (Q10): a cash or bank account of the property (the statement group CASH) or 1230 Other receivables for a sale on credit.
func (s *Service) proceedsAccountOK(ctx context.Context, p auth.Principal, propertyID, id int64) (bool, error) {
	r, err := s.q(ctx).GetProceedsAccount(ctx, assetsdb.GetProceedsAccountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return r.AccountType == accounting.TypeAsset && (deref(r.StatementGroup) == "CASH" || r.Code == "1230"), nil
}

// disposalPending is the refusal of a change to an asset whose disposal waits for an approver.
func disposalPending(number string) *apperr.Error {
	return apperr.Conflict("DISPOSAL_PENDING", "this asset has a disposal that waits for an approver").WithContext("asset", number)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// planDisposal works out a disposal of the locked asset: the checks of 4.7 (the months before the disposal are run, the month of it is not, the date is in an open month), the amounts (proportional for a
// part of a batch, 4.8), the journal and the amount of the approval (7.2).
func (s *Service) planDisposal(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, cur assetsdb.GetAssetRow, cfg assetsdb.FaSetting, po *accounting.Poster, decimals int32, in DisposeInput) (*disposalPlan, error) {
	if cur.Status != statusActive {
		return nil, apperr.Conflict("ASSET_STATUS_INVALID", "only an active asset is disposed of").WithContext("asset", cur.Number).WithContext("status", cur.Status)
	}
	pl := &disposalPlan{asset: cur, kind: in.Kind, reason: in.ReasonCode, date: day, units: 1}
	var f []apperr.FieldError
	if in.Kind != DisposalSale && in.Kind != DisposalWriteOff {
		f = append(f, fieldErr("kind", "INVALID_VALUE", "SALE or WRITE_OFF"))
	} else if !validReason(in.Kind, in.ReasonCode) {
		f = append(f, fieldErr("reason_code", "INVALID_VALUE", "SOLD for a sale; DAMAGED, LOST, OBSOLETE, DONATED or OTHER for a write-off"))
	}
	if n := trimmed(in.Note); n != "" {
		if len([]rune(n)) > maxNote {
			f = append(f, fieldErr("note", "INVALID_VALUE", fmt.Sprintf("at most %d characters", maxNote)))
		}
		pl.note = &n
	} else if in.ReasonCode == ReasonOther {
		f = append(f, fieldErr("note", "REQUIRED", "say what happened to the asset"))
	}
	if in.DisposalDate != nil {
		pl.date = *in.DisposalDate
	}
	if in.Units != nil {
		pl.units = *in.Units
	}
	if pl.units < 1 || pl.units > cur.UnitCount {
		f = append(f, fieldErr("units", "UNITS_INVALID", fmt.Sprintf("1 to %d", cur.UnitCount)))
	}
	pl.proceeds = decimal.Zero
	if in.Kind == DisposalSale {
		if in.Proceeds == nil || !in.Proceeds.IsPositive() || !moneyOK(*in.Proceeds, decimals) {
			f = append(f, fieldErr("proceeds", "INVALID_VALUE", "the price received, above zero"))
		} else {
			pl.proceeds = *in.Proceeds
		}
		if in.ProceedsAccountID == nil {
			f = append(f, fieldErr("proceeds_account_id", "REQUIRED", "the cash, bank or receivable account the price goes to"))
		} else {
			ok, err := s.proceedsAccountOK(ctx, p, propertyID, *in.ProceedsAccountID)
			if err != nil {
				return nil, err
			}
			if !ok {
				f = append(f, fieldErr("proceeds_account_id", "PROCEEDS_ACCOUNT_INVALID", "a cash or bank account, or 1230 Other receivables"))
			}
			pl.proceedsAcc = in.ProceedsAccountID
		}
	} else if (in.Proceeds != nil && !in.Proceeds.IsZero()) || in.ProceedsAccountID != nil {
		f = append(f, fieldErr("proceeds", "INVALID_VALUE", "a write-off brings in nothing"))
	}
	if len(f) > 0 {
		return nil, invalid("disposal", f...)
	}
	if pl.date.After(day) {
		return nil, invalid("disposal", fieldErr("disposal_date", "FUTURE_DATE", "the disposal date is not after the open business day"))
	}
	if cfg.RegisterFrom != nil && pl.date.Before(*cfg.RegisterFrom) {
		return nil, invalid("disposal", fieldErr("disposal_date", "BEFORE_REGISTER", "the register starts later than this date"))
	}
	if err := po.CheckDate(ctx, pl.date, "disposal_date"); err != nil {
		return nil, err
	}
	// 4.7: through the month before the disposal, and the month of it is not depreciated (Q5)
	if err := requireRunThrough(cur, cfg, pl.date, decimals); err != nil {
		return nil, err
	}
	if cur.LastDepMonth != nil && !cur.LastDepMonth.Before(MonthStart(pl.date)) {
		return nil, apperr.Conflict("DISPOSAL_MONTH_DEPRECIATED", "the month of the disposal has been depreciated already: reverse its run or date the disposal later").
			WithContext("asset", cur.Number).WithContext("month", monthLabel(*cur.LastDepMonth))
	}
	cat, err := s.q(ctx).GetCategory(ctx, assetsdb.GetCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: cur.CategoryID})
	if err != nil {
		return nil, err
	}
	pl.cat = cat
	if pl.units == cur.UnitCount {
		pl.cost, pl.accum = cur.Cost, cur.AccumDepr
	} else { // a part of a batch (4.8): in proportion, rounded to the currency
		n := decimal.NewFromInt32(cur.UnitCount)
		k := decimal.NewFromInt32(pl.units)
		pl.cost = cur.Cost.Mul(k).Div(n).Round(decimals)
		pl.accum = cur.AccumDepr.Mul(k).Div(n).Round(decimals)
	}
	if !pl.cost.IsPositive() {
		return nil, invalid("disposal", fieldErr("units", "UNITS_INVALID", "the pieces have no cost"))
	}
	pl.carrying = pl.cost.Sub(pl.accum)
	pl.gain = pl.proceeds.Sub(pl.carrying)
	pl.approvalAmount = decimal.Max(pl.carrying.Abs(), pl.proceeds)
	if pl.accum.IsPositive() && (cat.AccumulatedAccountID == nil) {
		return nil, apperr.Conflict("CATEGORY_NOT_DEPRECIABLE", "the category has no accumulated depreciation account").WithContext("category", cat.Code)
	}
	gainAcc, err := po.SystemAccount(ctx, accounting.KeyAssetDisposal)
	if err != nil {
		return nil, apperr.Conflict("ASSET_DISPOSAL_ACCOUNT_MISSING", "the account of the gain or loss on disposal is not set: point ASSET_DISPOSAL at an account in the account map")
	}
	moves, err := s.movesByAsset(ctx, p.TenantID, propertyID, []int64{cur.ID})
	if err != nil {
		return nil, err
	}
	dept := departmentAt(cur.DepartmentID, moves[cur.ID], pl.date)
	if dept == nil {
		dept = cat.DepartmentID
	}
	if !pl.gain.IsZero() {
		if dept, err = po.ResolveDepartment(ctx, gainAcc, dept, "department_id"); err != nil {
			return nil, err
		}
	}
	pl.deptID = dept
	desc := "Disposal of " + cur.Number
	line := func(acc int64, debit, credit decimal.Decimal, d *int64) {
		pl.lines = append(pl.lines, accounting.SystemLine{AccountID: acc, Debit: debit, Credit: credit, DepartmentID: d, Description: desc, SourceType: journalSourceAsset, SourceRef: cur.Number})
	}
	if pl.proceeds.IsPositive() {
		line(*pl.proceedsAcc, pl.proceeds, decimal.Zero, nil)
	}
	if pl.accum.IsPositive() {
		line(*cat.AccumulatedAccountID, pl.accum, decimal.Zero, nil)
	}
	if pl.gain.IsNegative() {
		line(gainAcc, pl.gain.Neg(), decimal.Zero, dept)
	}
	line(cat.CostAccountID, decimal.Zero, pl.cost, nil)
	if pl.gain.IsPositive() {
		line(gainAcc, decimal.Zero, pl.gain, dept)
	}
	for _, l := range pl.lines {
		code, name, _, _, err := po.AccountInfo(ctx, l.AccountID)
		if err != nil {
			return nil, err
		}
		pl.preview = append(pl.preview, DisposalLine{AccountID: l.AccountID, AccountCode: code, AccountName: name, Debit: l.Debit, Credit: l.Credit, DepartmentID: l.DepartmentID})
	}
	return pl, nil
}

func (s *Service) previewOf(ctx context.Context, propertyID int64, pl *disposalPlan) (DisposalPreview, error) {
	pkp, err := s.q(ctx).PKPOnDate(ctx, assetsdb.PKPOnDateParams{PropertyID: propertyID, OnDate: pl.date})
	if err != nil {
		return DisposalPreview{}, err
	}
	return DisposalPreview{AssetID: pl.asset.ID, AssetNumber: pl.asset.Number, AssetName: pl.asset.Name, Kind: pl.kind, ReasonCode: pl.reason, DisposalDate: pl.date, Units: pl.units,
		UnitCount: pl.asset.UnitCount, CostRemoved: pl.cost, AccumRemoved: pl.accum, CarryingRemoved: pl.carrying, Proceeds: pl.proceeds, GainLoss: pl.gain, ApprovalAmount: pl.approvalAmount,
		PKPOnDate: pkp, Lines: pl.preview}, nil
}

// beginDisposal opens what every use case of the disposals takes before the asset: the accounting settings (46) and the settings of the register (47, FOR SHARE: a depreciation run takes them FOR UPDATE and
// so waits for us, or we for it), then the asset.
func (s *Service) beginDisposal(ctx context.Context, p auth.Principal, propertyID, assetID int64) (*accounting.Poster, assetsdb.FaSetting, error) {
	po, err := s.begin(ctx, propertyID)
	if err != nil {
		return nil, assetsdb.FaSetting{}, err
	}
	if po == nil {
		return nil, assetsdb.FaSetting{}, apperr.NotFound("ACCOUNTING_NOT_SET_UP", "accounting is not set up for this property")
	}
	cfg, err := s.lockSettings(ctx, p, propertyID, db.ForShare)
	if err != nil {
		return nil, cfg, err
	}
	if err := lockAssets(ctx, propertyID, []int64{assetID}); err != nil {
		return nil, cfg, err
	}
	return po, cfg, nil
}

// PreviewDisposal works out a disposal and writes nothing (asset.dispose).
func (s *Service) PreviewDisposal(ctx context.Context, propertyID, id int64, in DisposeInput) (DisposalPreview, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDispose)
	if err != nil {
		return DisposalPreview{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return DisposalPreview{}, err
	}
	var out DisposalPreview
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		po, cfg, err := s.beginDisposal(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		cur, err := s.q(ctx).GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		pl, err := s.planDisposal(ctx, p, propertyID, day, cur, cfg, po, prop.CurrencyDecimals, in)
		if err != nil {
			return err
		}
		out, err = s.previewOf(ctx, propertyID, pl)
		return err
	})
	return out, err
}

// Dispose disposes of an asset (asset.dispose, an Idempotency-Key, the approval of 7.2). Posted at once when the approval is given or the actor may approve their own; else a request that waits.
func (s *Service) Dispose(ctx context.Context, propertyID, id int64, in DisposeInput) (DisposeResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDispose)
	if err != nil {
		return DisposeResult{}, err
	}
	key, err := idempotencyKey(in.IdempotencyKey)
	if err != nil {
		return DisposeResult{}, err
	}
	if s.approver == nil {
		return DisposeResult{}, apperr.Internal(errors.New("assets: the approver is not wired"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return DisposeResult{}, err
	}
	var res DisposeResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		res = DisposeResult{}
		q := s.q(ctx)
		po, cfg, err := s.beginDisposal(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if prev, err := q.GetDisposalByKey(ctx, assetsdb.GetDisposalByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
			if prev.AssetID != id {
				return apperr.Conflict("DUPLICATE_REQUEST", "the Idempotency-Key was used for another asset")
			}
			d, err := s.disposalByID(ctx, p, propertyID, prev.ID)
			res = DisposeResult{Status: DisposalPosted, Disposal: &d, Replayed: true}
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if prev, err := q.GetDisposalRequestByKey(ctx, assetsdb.GetDisposalRequestByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
			if prev.AssetID != id {
				return apperr.Conflict("DUPLICATE_REQUEST", "the Idempotency-Key was used for another asset")
			}
			r, err := s.requestByID(ctx, p, propertyID, prev.ID)
			res = DisposeResult{Status: DisposalRequestStatus(r), Request: &r, Replayed: true}
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		if cur.DisposalPending {
			return disposalPending(cur.Number)
		}
		pl, err := s.planDisposal(ctx, p, propertyID, day, cur, cfg, po, prop.CurrencyDecimals, in)
		if err != nil {
			return err
		}
		ap, err := s.approver.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindAssetDisposal, Amount: pl.approvalAmount, Own: true})
		if err != nil {
			ae, ok := apperr.As(err)
			if !ok || ae.Code != "APPROVAL_REQUIRED" {
				return err
			}
			rid, err := q.InsertDisposalRequest(ctx, assetsdb.InsertDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id, Kind: pl.kind, DisposalDate: pl.date, Units: pl.units,
				Proceeds: pl.proceeds, ProceedsAccountID: pl.proceedsAcc, ReasonCode: pl.reason, Note: pl.note, ApprovalAmount: pl.approvalAmount, ActorID: p.ActorID(), IdempotencyKey: &key})
			if err != nil {
				return err
			}
			if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.disposal_requested", "fa_disposal_request", rid, cur.Number, nil,
				map[string]any{"kind": pl.kind, "reason_code": pl.reason, "disposal_date": pl.date, "units": pl.units, "proceeds": pl.proceeds, "approval_amount": pl.approvalAmount})); err != nil {
				return err
			}
			r, err := s.requestByID(ctx, p, propertyID, rid)
			res = DisposeResult{Status: RequestPending, Request: &r}
			return err
		}
		did, err := s.postDisposal(ctx, p, propertyID, day, po, pl, ap, &key, "asset.disposed", nil)
		if err != nil {
			return err
		}
		d, err := s.disposalByID(ctx, p, propertyID, did)
		res = DisposeResult{Status: DisposalPosted, Disposal: &d}
		return err
	})
	return res, err
}

// DisposalRequestStatus is the status a result shows for a request: PENDING while it waits, else POSTED when it ended in a disposal and REJECTED when it did not.
func DisposalRequestStatus(r DisposalRequest) string {
	switch r.Status {
	case RequestApproved:
		return DisposalPosted
	default:
		return r.Status
	}
}

// postDisposal writes the journal, the disposal, the ledger row and the state of the asset, and the audit entry. The asset is locked and the plan is made.
func (s *Service) postDisposal(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, po *accounting.Poster, pl *disposalPlan, ap iam.Approval, key *string, action string, requestID *int64) (int64, error) {
	number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFADisposal)
	if err != nil {
		return 0, err
	}
	cur := pl.asset
	jid, _, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalAsset, Date: pl.date, Description: fmt.Sprintf("Disposal of %s (%s)", cur.Number, number), Reference: number, Lines: pl.lines})
	if err != nil {
		return 0, err
	}
	var approver *int64
	if !ap.IsZero() {
		approver = ptr(ap.UserID())
	}
	did, err := s.q(ctx).InsertDisposal(ctx, assetsdb.InsertDisposalParams{TenantID: p.TenantID, PropertyID: propertyID, Number: number, AssetID: cur.ID, Kind: pl.kind, DisposalDate: pl.date, Units: pl.units,
		CostRemoved: pl.cost, AccumRemoved: pl.accum, CarryingRemoved: pl.carrying, Proceeds: pl.proceeds, ProceedsAccountID: pl.proceedsAcc, GainLoss: pl.gain, ReasonCode: pl.reason, Note: pl.note,
		JournalID: jid, ApprovedBy: approver, IdempotencyKey: key, ActorID: p.ActorID()})
	if err != nil {
		return 0, err
	}
	if err := s.disposeAsset(ctx, p, propertyID, cur, did, jid, pl.date, pl.cost, pl.accum, pl.units); err != nil {
		return 0, err
	}
	pkp, err := s.q(ctx).PKPOnDate(ctx, assetsdb.PKPOnDateParams{PropertyID: propertyID, OnDate: pl.date})
	if err != nil {
		return 0, err
	}
	fields := ap.AuditFields(map[string]any{"number": number, "kind": pl.kind, "reason_code": pl.reason, "disposal_date": pl.date, "units": pl.units, "cost_removed": pl.cost, "accum_removed": pl.accum,
		"carrying_removed": pl.carrying, "proceeds": pl.proceeds, "gain_loss": pl.gain, "journal_id": jid, "approval_amount": pl.approvalAmount, "pkp_on_date": pkp})
	if requestID != nil {
		fields["request_id"] = *requestID
	}
	return did, s.audit.Write(ctx, s.entry(p, propertyID, day, action, "fa_disposal", did, number, nil, fields))
}

// ApproveDisposal approves a disposal that waits (asset.dispose; the kind ASSET_DISPOSAL, a decision of the signed-in approver: the rule of the property compares them with the person who asked). The
// amounts are worked out again with the asset locked, and the disposal is posted.
func (s *Service) ApproveDisposal(ctx context.Context, propertyID, requestID int64) (DisposeResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDispose)
	if err != nil {
		return DisposeResult{}, err
	}
	if s.approver == nil {
		return DisposeResult{}, apperr.Internal(errors.New("assets: the approver is not wired"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return DisposeResult{}, err
	}
	var res DisposeResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		res = DisposeResult{}
		q := s.q(ctx)
		head, err := q.GetDisposalRequest(ctx, assetsdb.GetDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID})
		if err != nil {
			return orNotFound(err, notFound("DISPOSAL_REQUEST_NOT_FOUND", "disposal request"))
		}
		po, cfg, err := s.beginDisposal(ctx, p, propertyID, head.AssetID)
		if err != nil {
			return err
		}
		req, err := q.GetDisposalRequest(ctx, assetsdb.GetDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID})
		if err != nil {
			return err
		}
		if req.Status != RequestPending {
			return apperr.Conflict("DISPOSAL_REQUEST_DECIDED", "this request was decided already").WithContext("status", req.Status)
		}
		cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: req.AssetID})
		if err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		units, proceeds, date := req.Units, req.Proceeds, req.DisposalDate
		in := DisposeInput{Kind: req.Kind, DisposalDate: &date, Units: &units, Proceeds: &proceeds, ProceedsAccountID: req.ProceedsAccountID, ReasonCode: req.ReasonCode}
		if req.Note != nil {
			in.Note = *req.Note
		}
		pl, err := s.planDisposal(ctx, p, propertyID, day, cur, cfg, po, prop.CurrencyDecimals, in)
		if err != nil {
			return err
		}
		subject := int64(0)
		if req.RequestedBy != nil {
			subject = *req.RequestedBy
		}
		ap, err := s.approver.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Kind: iam.KindAssetDisposal, Amount: pl.approvalAmount, Decision: true, Subject: subject})
		if err != nil {
			return err
		}
		did, err := s.postDisposal(ctx, p, propertyID, day, po, pl, ap, nil, "asset.disposed", &requestID)
		if err != nil {
			return err
		}
		if err := q.DecideDisposalRequest(ctx, assetsdb.DecideDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID, Status: RequestApproved, ActorID: p.ActorID(),
			DecidedAt: ptr(s.clock.Now()), DisposalID: &did}); err != nil {
			return err
		}
		d, err := s.disposalByID(ctx, p, propertyID, did)
		if err != nil {
			return err
		}
		r, err := s.requestByID(ctx, p, propertyID, requestID)
		res = DisposeResult{Status: DisposalPosted, Disposal: &d, Request: &r}
		return err
	})
	return res, err
}

// RejectDisposal ends a request that waits without a disposal (asset.dispose; the person who asked may withdraw it, an approver may refuse it), with the reason.
func (s *Service) RejectDisposal(ctx context.Context, propertyID, requestID int64, note string) (DisposalRequest, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDispose)
	if err != nil {
		return DisposalRequest{}, err
	}
	note, err = reasonOf(note, "decision")
	if err != nil {
		return DisposalRequest{}, err
	}
	var out DisposalRequest
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		q := s.q(ctx)
		head, err := q.GetDisposalRequest(ctx, assetsdb.GetDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID})
		if err != nil {
			return orNotFound(err, notFound("DISPOSAL_REQUEST_NOT_FOUND", "disposal request"))
		}
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		if err := lockAssets(ctx, propertyID, []int64{head.AssetID}); err != nil {
			return err
		}
		req, err := q.GetDisposalRequest(ctx, assetsdb.GetDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID})
		if err != nil {
			return err
		}
		if req.Status != RequestPending {
			return apperr.Conflict("DISPOSAL_REQUEST_DECIDED", "this request was decided already").WithContext("status", req.Status)
		}
		if err := q.DecideDisposalRequest(ctx, assetsdb.DecideDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID, Status: RequestRejected, ActorID: p.ActorID(),
			DecidedAt: ptr(s.clock.Now()), DecisionNote: &note}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.disposal_rejected", "fa_disposal_request", requestID, req.AssetNumber, nil,
			map[string]any{"reason": note, "requested_by": req.RequestedBy})); err != nil {
			return err
		}
		out, err = s.requestByID(ctx, p, propertyID, requestID)
		return err
	})
	return out, err
}

// ReverseDisposalInput is the reason of a reversal and, when the rule asks for someone else, the approver's credentials.
type ReverseDisposalInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// ReverseDisposal reverses a posted disposal while the book month of its date is open (asset.dispose, the same approval kind): the journal is reversed, the asset is active again with its state, and
// the months it was not depreciated while it was gone are caught up by the next run.
func (s *Service) ReverseDisposal(ctx context.Context, propertyID, disposalID int64, in ReverseDisposalInput) (Disposal, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetDispose)
	if err != nil {
		return Disposal{}, err
	}
	reason, err := reasonOf(in.Reason, "reversal")
	if err != nil {
		return Disposal{}, err
	}
	if s.approver == nil {
		return Disposal{}, apperr.Internal(errors.New("assets: the approver is not wired"))
	}
	var out Disposal
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		q := s.q(ctx)
		head, err := q.GetDisposal(ctx, assetsdb.GetDisposalParams{TenantID: p.TenantID, PropertyID: propertyID, ID: disposalID})
		if err != nil {
			return orNotFound(err, notFound("DISPOSAL_NOT_FOUND", "disposal"))
		}
		po, _, err := s.beginDisposal(ctx, p, propertyID, head.AssetID)
		if err != nil {
			return err
		}
		d, err := q.GetDisposal(ctx, assetsdb.GetDisposalParams{TenantID: p.TenantID, PropertyID: propertyID, ID: disposalID})
		if err != nil {
			return err
		}
		if d.Status != DisposalPosted {
			return apperr.Conflict("DISPOSAL_ALREADY_REVERSED", "this disposal was reversed already").WithContext("number", d.Number)
		}
		cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: d.AssetID})
		if err != nil {
			return err
		}
		if cur.DisposalPending {
			return disposalPending(cur.Number)
		}
		if cur.Status == statusActive { // a part of a batch (a whole asset is DISPOSED): nothing may have been depreciated since
			if cur.LastDepMonth != nil && !cur.LastDepMonth.Before(MonthStart(d.DisposalDate)) {
				return apperr.Conflict("DISPOSAL_NOT_REVERSIBLE", "the asset was depreciated after this disposal: reverse the depreciation of that month first").WithContext("number", d.Number)
			}
		}
		later, err := q.LaterEntriesOfAsset(ctx, assetsdb.LaterEntriesOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: d.AssetID, DisposalID: d.ID})
		if err != nil {
			return err
		}
		if later > 0 {
			return apperr.Conflict("DISPOSAL_NOT_REVERSIBLE", "the asset was changed or disposed of again after this disposal").WithContext("number", d.Number)
		}
		amount := decimal.Max(d.CarryingRemoved.Abs(), d.Proceeds)
		ap, err := s.approver.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindAssetDisposal, Amount: amount, Own: true})
		if err != nil {
			return err
		}
		rid, err := po.Reverse(ctx, d.JournalID, d.DisposalDate, reason, firstID(ap.UserID(), p.UserID))
		if err != nil {
			return err
		}
		if err := s.reinstateDisposal(ctx, p, propertyID, cur, d.ID, d.Units, rid); err != nil {
			return err
		}
		if err := q.MarkDisposalReversed(ctx, assetsdb.MarkDisposalReversedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: d.ID, ReversalJournalID: &rid, ActorID: p.ActorID(),
			ReversedAt: ptr(s.clock.Now()), ReversalReason: &reason}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.disposal_reversed", "fa_disposal", d.ID, d.Number, nil,
			ap.AuditFields(map[string]any{"number": d.Number, "reason": reason, "reversal_journal_id": rid, "approval_amount": amount}))); err != nil {
			return err
		}
		out, err = s.disposalByID(ctx, p, propertyID, d.ID)
		return err
	})
	return out, err
}

// ---------------------------------------------------------------------------------------------------------------------
// Reading

func (s *Service) disposalByID(ctx context.Context, p auth.Principal, propertyID, id int64) (Disposal, error) {
	r, err := s.q(ctx).GetDisposal(ctx, assetsdb.GetDisposalParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Disposal{}, orNotFound(err, notFound("DISPOSAL_NOT_FOUND", "disposal"))
	}
	return disposalFrom(r), nil
}

func disposalFrom(r assetsdb.GetDisposalRow) Disposal {
	return Disposal{ID: r.ID, Number: r.Number, AssetID: r.AssetID, AssetNumber: r.AssetNumber, AssetName: r.AssetName, Kind: r.Kind, ReasonCode: r.ReasonCode, DisposalDate: r.DisposalDate,
		Units: r.Units, CostRemoved: r.CostRemoved, AccumRemoved: r.AccumRemoved, CarryingRemoved: r.CarryingRemoved, Proceeds: r.Proceeds, ProceedsAccountID: r.ProceedsAccountID,
		ProceedsAccountCode: r.ProceedsAccountCode, ProceedsAccountName: r.ProceedsAccountName, GainLoss: r.GainLoss, Note: r.Note, JournalID: r.JournalID, JournalNumber: r.JournalNumber,
		Status: r.Status, ReversalJournalID: r.ReversalJournalID, ReversalJournalNumber: r.ReversalJournalNumber, ReversalReason: r.ReversalReason, ReversedByName: r.ReversedByName,
		ApprovedByName: r.ApprovedByName, CreatedByName: r.CreatedByName, CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")}
}

func (s *Service) requestByID(ctx context.Context, p auth.Principal, propertyID, id int64) (DisposalRequest, error) {
	r, err := s.q(ctx).GetDisposalRequest(ctx, assetsdb.GetDisposalRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return DisposalRequest{}, orNotFound(err, notFound("DISPOSAL_REQUEST_NOT_FOUND", "disposal request"))
	}
	return requestFrom(r), nil
}

func requestFrom(r assetsdb.GetDisposalRequestRow) DisposalRequest {
	out := DisposalRequest{ID: r.ID, AssetID: r.AssetID, AssetNumber: r.AssetNumber, AssetName: r.AssetName, Kind: r.Kind, ReasonCode: r.ReasonCode, DisposalDate: r.DisposalDate, Units: r.Units,
		Proceeds: r.Proceeds, ProceedsAccountID: r.ProceedsAccountID, ProceedsAccountCode: r.ProceedsAccountCode, ProceedsAccountName: r.ProceedsAccountName, Note: r.Note,
		ApprovalAmount: r.ApprovalAmount, Status: r.Status, RequestedBy: r.RequestedBy, RequestedByName: r.RequestedByName, RequestedAt: r.RequestedAt.UTC().Format("2006-01-02T15:04:05Z"),
		DecidedByName: r.DecidedByName, DecisionNote: r.DecisionNote, DisposalID: r.DisposalID, DisposalNumber: r.DisposalNumber}
	if r.DecidedAt != nil {
		t := r.DecidedAt.UTC().Format("2006-01-02T15:04:05Z")
		out.DecidedAt = &t
	}
	return out
}

// ListDisposals lists the disposals of the property, or of one asset (any asset permission).
func (s *Service) ListDisposals(ctx context.Context, propertyID int64, assetID *int64) ([]Disposal, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListDisposals(ctx, assetsdb.ListDisposalsParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID})
	if err != nil {
		return nil, err
	}
	out := make([]Disposal, 0, len(rows))
	for _, r := range rows {
		out = append(out, disposalFrom(assetsdb.GetDisposalRow(r)))
	}
	return out, nil
}

// ListDisposalRequests lists the requests of the property, the ones that wait or all (any asset permission).
func (s *Service) ListDisposalRequests(ctx context.Context, propertyID int64, status string) ([]DisposalRequest, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	var st *string
	if t := trimmed(status); t != "" {
		switch t {
		case RequestPending, RequestApproved, RequestRejected:
		default:
			return nil, invalid("filter", fieldErr("status", "INVALID_VALUE", "PENDING, APPROVED or REJECTED"))
		}
		st = &t
	}
	rows, err := s.q(ctx).ListDisposalRequests(ctx, assetsdb.ListDisposalRequestsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: st})
	if err != nil {
		return nil, err
	}
	out := make([]DisposalRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, requestFrom(assetsdb.GetDisposalRequestRow(r)))
	}
	return out, nil
}

// firstID is the approver when there is one (the rule asked for one), otherwise the person who acted.
func firstID(approver, actor int64) int64 {
	if approver != 0 {
		return approver
	}
	return actor
}
