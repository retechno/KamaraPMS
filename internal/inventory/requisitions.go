package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The store requisition, SR (35-inventory.md 5.4, 5.8): an outlet or a department asks a warehouse (the store) for goods. It goes through the approval chain of the SR (two levels by default, the engine of
// approval.go and requests.go) and, once approved, the store answers it with one or more transfers (transfers.go).

// States of a requisition.
const (
	SRDraft     = "DRAFT"
	SRSubmitted = "SUBMITTED"
	SRReturned  = "RETURNED"
	SRApproved  = "APPROVED"
	SRPartial   = "PARTIAL"
	SRFulfilled = "FULFILLED"
	SRRejected  = "REJECTED"
	SRCancelled = "CANCELLED"
)

// RequisitionLineInput is a line of a requisition being written: an item and a quantity in one of its units (the stock unit when none is named).
type RequisitionLineInput struct {
	ItemID int64           `json:"item_id"`
	UnitID *int64          `json:"unit_id"`
	Qty    decimal.Decimal `json:"qty"`
	Note   *string         `json:"note"`
}

// RequisitionInput creates (ClientUUID) or changes (Version) a requisition while it is a draft or has been returned. ToWarehouseID is the warehouse that asks, FromWarehouseID the store that supplies.
type RequisitionInput struct {
	ClientUUID      string                 `json:"client_uuid"`
	ToWarehouseID   int64                  `json:"to_warehouse_id"`
	FromWarehouseID int64                  `json:"from_warehouse_id"`
	DepartmentID    *int64                 `json:"department_id"`
	NeededOn        *civil.Date            `json:"needed_on"`
	Note            *string                `json:"note"`
	Lines           []RequisitionLineInput `json:"lines"`
	Version         int32                  `json:"version"`
}

// RequisitionLine is a line of a requisition as it stands, with what the store holds and what was sent.
type RequisitionLine struct {
	LineNo        int             `json:"line_no"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitID        int64           `json:"unit_id"`
	UnitCode      string          `json:"unit_code"`
	Qty           decimal.Decimal `json:"qty"`
	QtyStock      decimal.Decimal `json:"qty_stock"`
	StockUnitCode string          `json:"stock_unit_code"`
	QtyIssued     decimal.Decimal `json:"qty_issued_stock"`
	QtyLeft       decimal.Decimal `json:"qty_left_stock"`
	OnHand        decimal.Decimal `json:"on_hand"`
	EstValue      decimal.Decimal `json:"est_value"`
	Note          *string         `json:"note"`
}

// Requisition is a store requisition with its lines, its approval and the transfers that answered it.
type Requisition struct {
	ID                int64             `json:"id"`
	Number            string            `json:"sr_number"`
	Status            string            `json:"status"`
	RequesterID       int64             `json:"requester_id"`
	RequesterName     string            `json:"requester_name"`
	DepartmentID      *int64            `json:"department_id"`
	DepartmentName    *string           `json:"department_name"`
	ToWarehouseID     int64             `json:"to_warehouse_id"`
	ToWarehouseName   string            `json:"to_warehouse_name"`
	FromWarehouseID   int64             `json:"from_warehouse_id"`
	FromWarehouseName string            `json:"from_warehouse_name"`
	NeededOn          *civil.Date       `json:"needed_on"`
	EstTotal          decimal.Decimal   `json:"est_total"`
	Cycle             int               `json:"cycle"`
	SubmittedAt       *time.Time        `json:"submitted_at"`
	ReturnedNote      *string           `json:"returned_note"`
	RejectedNote      *string           `json:"rejected_note"`
	Note              *string           `json:"note"`
	Version           int32             `json:"version"`
	CreatedAt         time.Time         `json:"created_at"`
	Stage             *Stage            `json:"stage"`
	Lines             []RequisitionLine `json:"lines"`
	Steps             []ApprovalStep    `json:"steps"`
	Edits             []ApprovalEdit    `json:"edits"`
	Decision          *Decision         `json:"decision"`
	Transfers         []TransferBrief   `json:"transfers"`
}

// RequisitionBrief is a row of the list of requisitions.
type RequisitionBrief struct {
	ID                int64           `json:"id"`
	Number            string          `json:"sr_number"`
	Status            string          `json:"status"`
	RequesterID       int64           `json:"requester_id"`
	RequesterName     string          `json:"requester_name"`
	DepartmentName    *string         `json:"department_name"`
	ToWarehouseName   string          `json:"to_warehouse_name"`
	FromWarehouseName string          `json:"from_warehouse_name"`
	NeededOn          *civil.Date     `json:"needed_on"`
	EstTotal          decimal.Decimal `json:"est_total"`
	LineCount         int             `json:"line_count"`
	AnsweredBy        string          `json:"answered_by"`
	SubmittedAt       *time.Time      `json:"submitted_at"`
	CreatedAt         time.Time       `json:"created_at"`
	Stage             *Stage          `json:"stage"`
	WaitingForMe      bool            `json:"waiting_for_me"`
}

// RequisitionFilter narrows the list of requisitions.
type RequisitionFilter struct {
	Status string
	Mine   bool
	Q      string
	Limit  int
}

func errRequisitionState(status, want string) *apperr.Error {
	return apperr.Conflict("REQUISITION_STATE_INVALID", "the requisition is "+status+": this is for one that is "+want).WithContext("status", status)
}

func toStepSR(r inventorydb.ListSRStepsRow, total decimal.Decimal) ApprovalStep {
	st := ApprovalStep{ID: r.ID, Cycle: int(r.Cycle), LevelNo: int(r.LevelNo), Name: r.Name, ApproverKind: r.ApproverKind, Permission: r.Permission, ConditionKind: r.ConditionKind, Threshold: r.Threshold,
		EditAllowed: r.EditAllowed, Status: r.Status, SkipReason: r.SkipReason, ActivatedAt: r.ActivatedAt, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt, TotalSeen: r.TotalSeen, Note: r.Note}
	if r.DecidedByName != nil {
		st.DecidedByName = *r.DecidedByName
	}
	st.Applies = r.ConditionKind == CondAlways || (r.Threshold != nil && total.GreaterThan(*r.Threshold))
	return st
}

func (s *Service) stepsOfSR(ctx context.Context, tenantID, propertyID int64, ids []int64, totals map[int64]decimal.Decimal) (map[int64][]ApprovalStep, error) {
	rows, err := s.q(ctx).ListSRSteps(ctx, inventorydb.ListSRStepsParams{TenantID: tenantID, PropertyID: propertyID, RequisitionIds: ids})
	if err != nil {
		return nil, err
	}
	out := map[int64][]ApprovalStep{}
	for _, r := range rows {
		out[*r.RequisitionID] = append(out[*r.RequisitionID], toStepSR(r, totals[*r.RequisitionID]))
	}
	return out, nil
}

// ListRequisitions lists the requisitions, newest first.
func (s *Service) ListRequisitions(ctx context.Context, propertyID int64, f RequisitionFilter) ([]RequisitionBrief, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	arg := inventorydb.ListRequisitionsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: optional(&f.Status), Q: optional(&f.Q), RowLimit: limitOf(f.Limit)}
	if f.Mine {
		arg.RequesterID = &p.UserID
	}
	rows, err := q.ListRequisitions(ctx, arg)
	if err != nil {
		return nil, err
	}
	idList := make([]int64, 0, len(rows))
	totals := map[int64]decimal.Decimal{}
	for _, r := range rows {
		idList = append(idList, r.ID)
		totals[r.ID] = r.EstTotal
	}
	steps, err := s.stepsOfSR(ctx, p.TenantID, propertyID, idList, totals)
	if err != nil {
		return nil, err
	}
	waiting := map[int64]bool{}
	inbox, err := q.RequisitionInbox(ctx, inventorydb.RequisitionInboxParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, IsAdmin: p.IsTenantAdmin})
	if err != nil {
		return nil, err
	}
	for _, it := range inbox {
		waiting[it.RequisitionID] = true
	}
	out := make([]RequisitionBrief, 0, len(rows))
	for _, r := range rows {
		b := RequisitionBrief{ID: r.ID, Number: r.SrNumber, Status: r.Status, RequesterID: r.RequesterID, RequesterName: r.RequesterName, DepartmentName: r.DepartmentName, ToWarehouseName: r.ToWarehouseName,
			FromWarehouseName: r.FromWarehouseName, NeededOn: r.NeededOn, EstTotal: r.EstTotal, LineCount: int(r.LineCount), AnsweredBy: r.AnsweredBy, SubmittedAt: r.SubmittedAt, CreatedAt: r.CreatedAt, WaitingForMe: waiting[r.ID]}
		if r.Status != SRDraft && r.Status != SRCancelled {
			b.Stage = stageOf(steps[r.ID], int(r.Cycle), r.EstTotal)
		}
		out = append(out, b)
	}
	return out, nil
}

// GetRequisition answers one requisition with its lines, the steps of every round, the edits, the transfers that answered it and what the signed-in user can decide.
func (s *Service) GetRequisition(ctx context.Context, propertyID, id int64) (Requisition, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

func (s *Service) requisition(ctx context.Context, p auth.Principal, propertyID, id int64) (Requisition, error) {
	q := s.q(ctx)
	r, err := q.GetRequisition(ctx, inventorydb.GetRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Requisition{}, orNotFound(err, notFound("REQUISITION_NOT_FOUND", "requisition"))
	}
	out := Requisition{ID: r.ID, Number: r.SrNumber, Status: r.Status, RequesterID: r.RequesterID, RequesterName: r.RequesterName, DepartmentID: r.DepartmentID, DepartmentName: r.DepartmentName,
		ToWarehouseID: r.ToWarehouseID, ToWarehouseName: r.ToWarehouseName, FromWarehouseID: r.FromWarehouseID, FromWarehouseName: r.FromWarehouseName, NeededOn: r.NeededOn, EstTotal: r.EstTotal, Cycle: int(r.Cycle),
		SubmittedAt: r.SubmittedAt, ReturnedNote: r.ReturnedNote, RejectedNote: r.RejectedNote, Note: r.Note, Version: r.Version, CreatedAt: r.CreatedAt}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	lines, err := q.ListRequisitionLines(ctx, inventorydb.ListRequisitionLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: id})
	if err != nil {
		return Requisition{}, err
	}
	out.Lines = make([]RequisitionLine, 0, len(lines))
	for _, l := range lines {
		left := decimal.Max(l.QtyStock.Sub(l.QtyIssuedStock), decimal.Zero)
		out.Lines = append(out.Lines, RequisitionLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, UnitID: l.UnitID, UnitCode: l.UnitCode, Qty: l.QtyRequested, QtyStock: l.QtyStock,
			StockUnitCode: l.StockUnitCode, QtyIssued: l.QtyIssuedStock, QtyLeft: left, OnHand: l.OnHand, EstValue: l.QtyStock.Mul(l.AvgCost).Round(prop.CurrencyDecimals), Note: l.Note})
	}
	steps, err := s.stepsOfSR(ctx, p.TenantID, propertyID, []int64{id}, map[int64]decimal.Decimal{id: r.EstTotal})
	if err != nil {
		return Requisition{}, err
	}
	out.Steps = steps[id]
	if out.Steps == nil {
		out.Steps = []ApprovalStep{}
	}
	if r.Status != SRDraft && r.Status != SRCancelled {
		out.Stage = stageOf(out.Steps, int(r.Cycle), r.EstTotal)
	}
	edits, err := q.ListSREdits(ctx, inventorydb.ListSREditsParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: &id})
	if err != nil {
		return Requisition{}, err
	}
	out.Edits = make([]ApprovalEdit, 0, len(edits))
	for _, e := range edits {
		out.Edits = append(out.Edits, ApprovalEdit{ID: e.ID, StepID: e.StepID, LevelNo: int(e.LevelNo), Cycle: int(e.Cycle), LineNo: int(e.LineNo), Field: e.Field, OldValue: e.OldValue, NewValue: e.NewValue,
			EditedBy: e.EditedBy, EditedByName: e.EditedByName, EditedAt: e.EditedAt})
	}
	trf, err := q.ListTransfersOfRequisition(ctx, inventorydb.ListTransfersOfRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: &id})
	if err != nil {
		return Requisition{}, err
	}
	out.Transfers = make([]TransferBrief, 0, len(trf))
	for _, t := range trf {
		out.Transfers = append(out.Transfers, TransferBrief{ID: t.ID, Number: t.TrfNumber, Status: t.Status, BusinessDate: t.BusinessDate, TotalValue: t.TotalValue, RequisitionID: &id})
	}
	if r.Status == SRSubmitted {
		out.Decision, err = s.decisionFor(ctx, p, propertyID, r.RequesterID, r.DepartmentID, out.Steps, int(r.Cycle))
		if err != nil {
			return Requisition{}, err
		}
		for i := range out.Steps {
			st := &out.Steps[i]
			if st.Status != StepActive || st.Cycle != int(r.Cycle) {
				continue
			}
			who, err := s.eligible(ctx, p.TenantID, propertyID, r.DepartmentID, r.RequesterID, st.ApproverKind, st.Permission, decidedBy(out.Steps, int(r.Cycle)))
			if err != nil {
				return Requisition{}, err
			}
			for _, w := range who {
				st.Approvers = append(st.Approvers, w.Name)
			}
		}
	}
	return out, nil
}

// checkRequisitionHeader validates the warehouses and the department of a requisition and gives the department its default: the department of the asking warehouse (or of its outlet).
func (s *Service) checkRequisitionHeader(ctx context.Context, p auth.Principal, propertyID int64, in RequisitionInput) (*int64, error) {
	q := s.q(ctx)
	var f []apperr.FieldError
	if in.Note != nil && len([]rune(trimmed(*in.Note))) > 300 {
		f = append(f, fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	if in.ToWarehouseID == in.FromWarehouseID {
		f = append(f, fieldErr("from_warehouse_id", "INVALID_VALUE", "the store is another warehouse than the one that asks"))
	}
	for _, w := range []struct {
		field string
		id    int64
	}{{"to_warehouse_id", in.ToWarehouseID}, {"from_warehouse_id", in.FromWarehouseID}} {
		r, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: w.id})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			f = append(f, fieldErr(w.field, "NOT_FOUND", "no such warehouse in this property"))
		case err != nil:
			return nil, err
		case !r.IsActive:
			f = append(f, fieldErr(w.field, "INACTIVE", "the warehouse is switched off"))
		}
	}
	if len(f) > 0 {
		return nil, invalid("requisition", f...)
	}
	dept := in.DepartmentID
	if dept == nil {
		if wd, err := q.WarehouseDepartment(ctx, inventorydb.WarehouseDepartmentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.ToWarehouseID}); err == nil && wd.DepartmentID != 0 {
			d := wd.DepartmentID
			dept = &d
		}
	}
	if err := s.checkDepartmentExists(ctx, p, propertyID, dept, "department_id"); err != nil {
		return nil, err
	}
	return dept, nil
}

func (s *Service) writeRequisitionLines(ctx context.Context, p auth.Principal, propertyID, id int64, in []RequisitionLineInput) error {
	docLines := make([]docLine, 0, len(in))
	for _, l := range in {
		docLines = append(docLines, docLine{ItemID: l.ItemID, UnitID: l.UnitID, Qty: l.Qty})
	}
	lines, err := s.checkDocLines(ctx, p, propertyID, "requisition", docLines)
	if err != nil {
		return err
	}
	q := s.q(ctx)
	if err := q.DeleteRequisitionLines(ctx, inventorydb.DeleteRequisitionLinesParams{PropertyID: propertyID, RequisitionID: id}); err != nil {
		return err
	}
	stockUnits, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: itemIDsOf(lines)})
	if err != nil {
		return err
	}
	for i, l := range lines {
		unit := int64(0)
		if l.in.UnitID != nil {
			unit = *l.in.UnitID
		} else {
			it, err := q.GetItem(ctx, inventorydb.GetItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: l.item.ItemID})
			if err != nil {
				return err
			}
			unit = it.StockUnitID
		}
		_ = stockUnits
		var note *string
		if i < len(in) {
			note = optional(in[i].Note)
		}
		if err := q.InsertRequisitionLine(ctx, inventorydb.InsertRequisitionLineParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: id, LineNo: int32(i + 1), //nolint:gosec // G115: at most 200
			ItemID: l.item.ItemID, UnitID: unit, QtyRequested: l.in.Qty, QtyStock: l.stockQty, Note: note}); err != nil {
			return err
		}
	}
	return nil
}

func itemIDsOf(lines []docLineOut) []int64 {
	out := make([]int64, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.item.ItemID)
	}
	return out
}

// estimateRequisition computes the value of a requisition (the quantity at the cost of the store) and keeps it as est_total, the amount the conditions of the chain read.
func (s *Service) estimateRequisition(ctx context.Context, p auth.Principal, propertyID, id int64, decimals int32) (decimal.Decimal, error) {
	lines, err := s.q(ctx).ListRequisitionLines(ctx, inventorydb.ListRequisitionLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: id})
	if err != nil {
		return decimal.Zero, err
	}
	total := decimal.Zero
	for _, l := range lines {
		total = total.Add(l.QtyStock.Mul(l.AvgCost).Round(decimals))
	}
	return total, nil
}

// CreateRequisition writes a requisition as a draft (inventory.request). A repeated client_uuid answers the first.
func (s *Service) CreateRequisition(ctx context.Context, propertyID int64, in RequisitionInput) (Requisition, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Requisition{}, invalid("requisition", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		if prev, err := q.GetRequisitionByClientUUID(ctx, inventorydb.GetRequisitionByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		dept, err := s.checkRequisitionHeader(ctx, p, propertyID, in)
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvSR)
		if err != nil {
			return err
		}
		id, err = q.InsertRequisition(ctx, inventorydb.InsertRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, SrNumber: number, ClientUuid: key, ToWarehouseID: in.ToWarehouseID,
			FromWarehouseID: in.FromWarehouseID, RequesterID: p.UserID, DepartmentID: dept, NeededOn: in.NeededOn, Note: optional(in.Note), EstTotal: decimal.Zero, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.writeRequisitionLines(ctx, p, propertyID, id, in.Lines); err != nil {
			return err
		}
		total, err := s.estimateRequisition(ctx, p, propertyID, id, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		cur, err := q.GetRequisition(ctx, inventorydb.GetRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return err
		}
		if err := q.SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRDraft, Cycle: 1, EstTotal: total, ActorID: p.ActorID()}); err != nil {
			return err
		}
		_ = cur
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_created", "inv_requisition", id, number, nil, map[string]any{"number": number, "lines": len(in.Lines), "est_total": total}))
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

func (s *Service) loadRequisitionForChange(ctx context.Context, p auth.Principal, propertyID, id int64) (inventorydb.GetRequisitionRow, error) {
	if err := db.LockRows(ctx, db.StockRequisitions, db.ForUpdate, propertyID, []int64{id}); err != nil {
		return inventorydb.GetRequisitionRow{}, orNotFound(err, notFound("REQUISITION_NOT_FOUND", "requisition"))
	}
	cur, err := s.q(ctx).GetRequisition(ctx, inventorydb.GetRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return cur, orNotFound(err, notFound("REQUISITION_NOT_FOUND", "requisition"))
	}
	return cur, nil
}

func chainDocOfRequisition(r inventorydb.GetRequisitionRow) chainDoc {
	return chainDoc{Ref: docRef{DocSR, r.ID}, Status: r.Status, Cycle: r.Cycle, RequesterID: r.RequesterID, DepartmentID: r.DepartmentID}
}

// UpdateRequisition changes a requisition while it is a draft or was returned (inventory.request, the requester). The lines are replaced.
func (s *Service) UpdateRequisition(ctx context.Context, propertyID, id int64, in RequisitionInput) (Requisition, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadRequisitionForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != SRDraft && cur.Status != SRReturned {
			return errRequisitionState(cur.Status, "a draft or returned")
		}
		if cur.Version != in.Version {
			return versionConflict("requisition", cur.Version)
		}
		dept, err := s.checkRequisitionHeader(ctx, p, propertyID, in)
		if err != nil {
			return err
		}
		if err := s.writeRequisitionLines(ctx, p, propertyID, id, in.Lines); err != nil {
			return err
		}
		total, err := s.estimateRequisition(ctx, p, propertyID, id, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).UpdateRequisitionDraft(ctx, inventorydb.UpdateRequisitionDraftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, ToWarehouseID: in.ToWarehouseID,
			FromWarehouseID: in.FromWarehouseID, DepartmentID: dept, NeededOn: in.NeededOn, Note: optional(in.Note), EstTotal: total, Version: in.Version, ActorID: p.ActorID()}); err != nil {
			return orNotFound(err, versionConflict("requisition", cur.Version))
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_updated", "inv_requisition", id, cur.SrNumber, map[string]any{"est_total": cur.EstTotal}, map[string]any{"est_total": total, "lines": len(in.Lines)}))
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

// SubmitRequisition copies the chain of the SR into the steps of a new round and sends the requisition to the first level (inventory.request, the requester).
func (s *Service) SubmitRequisition(ctx context.Context, propertyID, id int64) (Requisition, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadRequisitionForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != SRDraft && cur.Status != SRReturned {
			return errRequisitionState(cur.Status, "a draft or returned")
		}
		if cur.DepartmentID == nil {
			return invalid("requisition", fieldErr("department_id", "REQUIRED", "the department whose head approves the requisition"))
		}
		q := s.q(ctx)
		lines, err := q.ListRequisitionLines(ctx, inventorydb.ListRequisitionLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: id})
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			return invalid("requisition", fieldErr("lines", "OUT_OF_RANGE", "at least one line"))
		}
		if err := s.ensureChain(ctx, p, propertyID, DocSR); err != nil {
			return err
		}
		chain, err := s.loadChain(ctx, p.TenantID, propertyID, DocSR)
		if err != nil {
			return err
		}
		ref := docRef{DocSR, id}
		old, err := s.stepsOfCycle(ctx, p, propertyID, ref, cur.Cycle)
		if err != nil {
			return err
		}
		cycle := cur.Cycle
		if len(old) > 0 || cur.SubmittedAt != nil {
			cycle = cur.Cycle + 1
		}
		total := cur.EstTotal
		leader, err := s.leaderOf(ctx, p.TenantID, propertyID, cur.DepartmentID, p.UserID)
		if err != nil {
			return err
		}
		var needed [][]person
		for _, l := range chain.Levels {
			if l.ConditionKind == CondTotalAbove && !total.GreaterThan(*l.Threshold) {
				continue
			}
			if l.LevelNo == 1 && l.ApproverKind == ApproverDeptHead && chain.SkipLevel1IfHead && leader {
				continue
			}
			who, err := s.eligible(ctx, p.TenantID, propertyID, cur.DepartmentID, p.UserID, l.ApproverKind, l.Permission, nil)
			if err != nil {
				return err
			}
			if len(who) == 0 {
				dept := ""
				if cur.DepartmentName != nil {
					dept = *cur.DepartmentName
				}
				return errChainUnsatisfiable(l.Name, dept)
			}
			needed = append(needed, who)
		}
		if !satisfiable(needed) {
			return errChainUnsatisfiable("one person for each level", "")
		}
		now := s.clock.Now()
		for _, l := range chain.Levels {
			if err := s.insertStep(ctx, p, propertyID, ref, cycle, l, chain.ApproverMayEdit); err != nil {
				return err
			}
		}
		if err := q.SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRSubmitted, Cycle: cycle, SubmittedAt: &now, EstTotal: total, ActorID: p.ActorID()}); err != nil {
			return err
		}
		done, err := s.advance(ctx, p, propertyID, ref, cycle, cur.DepartmentID, p.UserID, total, chain.SkipLevel1IfHead, leader)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_submitted", "inv_requisition", id, cur.SrNumber, map[string]any{"status": cur.Status},
			map[string]any{"status": SRSubmitted, "round": cycle, "est_total": total, "levels": len(chain.Levels)})); err != nil {
			return err
		}
		if done {
			return s.finishRequisition(ctx, p, propertyID, bd, id, cur.SrNumber, cycle, total, "no level of the chain applies")
		}
		return nil
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

// finishRequisition approves a requisition whose chain is done and tells the requester.
func (s *Service) finishRequisition(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, id int64, number string, cycle int16, total decimal.Decimal, note string) error {
	q := s.q(ctx)
	cur, err := q.GetRequisition(ctx, inventorydb.GetRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return err
	}
	if err := q.SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRApproved, Cycle: cycle, SubmittedAt: cur.SubmittedAt, EstTotal: total, ActorID: p.ActorID()}); err != nil {
		return err
	}
	if err := s.notifyDoc(ctx, p.TenantID, propertyID, []int64{cur.RequesterID}, NoticeApproved, nil, docRef{DocSR, id}); err != nil {
		return err
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_approved", "inv_requisition", id, number, map[string]any{"status": SRSubmitted},
		map[string]any{"status": SRApproved, "round": cycle, "est_total": total, "note": note}))
}

// ApproveRequisition decides the active level of a requisition in favour (the signed-in approver). The approver may change a quantity first when the chain says so (the value is worked out again and the later
// conditional levels read it); when no level is left the requisition is approved and the store can answer it.
func (s *Service) ApproveRequisition(ctx context.Context, propertyID, id int64, in ApproveInput) (Requisition, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Requisition{}, err
	}
	if err := s.canDecideAt(ctx, p, propertyID); err != nil {
		return Requisition{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	if note := optional(in.Note); note != nil && len([]rune(*note)) > 500 {
		return Requisition{}, invalid("approval", fieldErr("note", "INVALID_VALUE", "500 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadRequisitionForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.canDecideAt(ctx, p, propertyID); err != nil {
			return err
		}
		step, replay, err := s.activeStepFor(ctx, p, propertyID, chainDocOfRequisition(cur), in.LevelNo)
		if err != nil {
			return err
		}
		if replay {
			return nil
		}
		q := s.q(ctx)
		total := cur.EstTotal
		var edited []map[string]any
		if len(in.Edits) > 0 {
			if !step.EditAllowed {
				return apperr.New(apperr.KindInvalid, "APPROVAL_EDIT_NOT_ALLOWED", "this property does not let an approver change the lines")
			}
			edited, err = s.applyRequisitionEdits(ctx, p, propertyID, id, step.ID, in.Edits)
			if err != nil {
				return err
			}
			if total, err = s.estimateRequisition(ctx, p, propertyID, id, prop.CurrencyDecimals); err != nil {
				return err
			}
		}
		now := s.clock.Now()
		if err := q.SetStepState(ctx, inventorydb.SetStepStateParams{PropertyID: propertyID, ID: step.ID, Status: StepApproved, ActivatedAt: step.ActivatedAt, DecidedBy: &p.UserID, DecidedAt: &now,
			TotalSeen: &total, Note: optional(in.Note)}); err != nil {
			return err
		}
		if err := q.CloseStepNotices(ctx, inventorydb.CloseStepNoticesParams{PropertyID: propertyID, StepID: &step.ID, Now: &now}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.approval_step_decided", "inv_requisition", id, cur.SrNumber, nil,
			map[string]any{"level": step.LevelNo, "name": step.Name, "decision": "APPROVED", "total_seen": total, "note": in.Note, "edits": edited, "approved_by": p.UserID})); err != nil {
			return err
		}
		if err := q.SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRSubmitted, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			EstTotal: total, ActorID: p.ActorID()}); err != nil {
			return err
		}
		chain, err := s.loadChain(ctx, p.TenantID, propertyID, DocSR)
		if err != nil {
			return err
		}
		done, err := s.advance(ctx, p, propertyID, docRef{DocSR, id}, cur.Cycle, cur.DepartmentID, cur.RequesterID, total, chain.SkipLevel1IfHead, false)
		if err != nil {
			return err
		}
		if done {
			return s.finishRequisition(ctx, p, propertyID, bd, id, cur.SrNumber, cur.Cycle, total, "the last level approved")
		}
		return nil
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

// applyRequisitionEdits changes the quantity of the lines an approver edited and writes one row for each change. Only the quantity of a requisition can be edited (it has no price).
func (s *Service) applyRequisitionEdits(ctx context.Context, p auth.Principal, propertyID, id, stepID int64, edits []ApproveEdit) ([]map[string]any, error) {
	q := s.q(ctx)
	lines, err := q.ListRequisitionLines(ctx, inventorydb.ListRequisitionLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: id})
	if err != nil {
		return nil, err
	}
	byNo := map[int32]inventorydb.ListRequisitionLinesRow{}
	for _, l := range lines {
		byNo[l.LineNo] = l
	}
	var f []apperr.FieldError
	for i, e := range edits {
		at := fmt.Sprintf("edits[%d].", i)
		_, ok := byNo[int32(e.LineNo)] //nolint:gosec // G115: bounded by the lines
		switch {
		case !ok:
			f = append(f, fieldErr(at+"line_no", "NOT_FOUND", "no such line"))
		case e.Qty == nil || e.EstUnitPrice != nil:
			f = append(f, fieldErr(at+"qty", "INVALID_VALUE", "a quantity (a requisition has no price)"))
		case !e.Qty.IsPositive() || e.Qty.GreaterThan(decimal.NewFromInt(1_000_000_000)) || !e.Qty.Equal(Round6(*e.Qty)):
			f = append(f, fieldErr(at+"qty", "INVALID_VALUE", "above zero, six decimals at most"))
		}
	}
	if len(f) > 0 {
		e := apperr.Invalid("an edit is invalid", f...)
		e.Code = "APPROVAL_EDIT_NOT_ALLOWED"
		return nil, e
	}
	now := s.clock.Now()
	var changed []map[string]any
	for _, e := range edits {
		l := byNo[int32(e.LineNo)] //nolint:gosec // G115: checked above
		if e.Qty.Equal(l.QtyRequested) {
			continue
		}
		if err := q.InsertEdit(ctx, inventorydb.InsertEditParams{TenantID: p.TenantID, PropertyID: propertyID, StepID: stepID, LineNo: int32(e.LineNo), Field: "QTY", //nolint:gosec // G115: checked above
			OldValue: l.QtyRequested, NewValue: *e.Qty, EditedBy: p.UserID, EditedAt: now}); err != nil {
			return nil, err
		}
		changed = append(changed, map[string]any{"line": e.LineNo, "field": "QTY", "old": l.QtyRequested, "new": *e.Qty})
		stock := StockQty(*e.Qty, l.StockNum, l.StockDen)
		if !stock.IsPositive() {
			return nil, invalid("approval", fieldErr("edits", "TOO_SMALL", "less than the smallest quantity of the stock unit"))
		}
		if err := q.UpdateRequisitionLineEdit(ctx, inventorydb.UpdateRequisitionLineEditParams{PropertyID: propertyID, RequisitionID: id, LineNo: int32(e.LineNo), QtyRequested: *e.Qty, QtyStock: stock}); err != nil { //nolint:gosec // G115: checked above
			return nil, err
		}
	}
	return changed, nil
}

// ReturnRequisition sends a requisition back to its requester from the active level, with a note (the signed-in approver).
func (s *Service) ReturnRequisition(ctx context.Context, propertyID, id int64, in ReturnRequestInput) (Requisition, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Requisition{}, err
	}
	if err := s.canDecideAt(ctx, p, propertyID); err != nil {
		return Requisition{}, err
	}
	note := trimmed(in.Note)
	if n := len([]rune(note)); n < 1 || n > 500 {
		return Requisition{}, invalid("return", fieldErr("note", "REQUIRED", "say what has to change, 500 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadRequisitionForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.canDecideAt(ctx, p, propertyID); err != nil {
			return err
		}
		step, _, err := s.activeStepFor(ctx, p, propertyID, chainDocOfRequisition(cur), in.LevelNo)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		now := s.clock.Now()
		if err := q.SetStepState(ctx, inventorydb.SetStepStateParams{PropertyID: propertyID, ID: step.ID, Status: StepReturned, ActivatedAt: step.ActivatedAt, DecidedBy: &p.UserID, DecidedAt: &now,
			TotalSeen: &cur.EstTotal, Note: &note}); err != nil {
			return err
		}
		steps, err := s.stepsOfCycle(ctx, p, propertyID, docRef{DocSR, id}, cur.Cycle)
		if err != nil {
			return err
		}
		for _, st := range steps {
			if st.Status == StepWaiting {
				if err := q.SetStepState(ctx, inventorydb.SetStepStateParams{PropertyID: propertyID, ID: st.ID, Status: StepNotReached}); err != nil {
					return err
				}
			}
		}
		if err := q.CloseStepNotices(ctx, inventorydb.CloseStepNoticesParams{PropertyID: propertyID, StepID: &step.ID, Now: &now}); err != nil {
			return err
		}
		if err := q.SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRReturned, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			ReturnedNote: &note, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := s.notifyDoc(ctx, p.TenantID, propertyID, []int64{cur.RequesterID}, NoticeReturned, &step.ID, docRef{DocSR, id}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.approval_step_decided", "inv_requisition", id, cur.SrNumber, nil,
			map[string]any{"level": step.LevelNo, "name": step.Name, "decision": "RETURNED", "note": note, "approved_by": p.UserID})); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_returned", "inv_requisition", id, cur.SrNumber, map[string]any{"status": SRSubmitted}, map[string]any{"status": SRReturned, "note": note}))
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

// RecallRequisition pulls a submitted requisition back to a draft before it is approved (inventory.request, the requester).
func (s *Service) RecallRequisition(ctx context.Context, propertyID, id int64) (Requisition, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadRequisitionForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != SRSubmitted {
			return errRequisitionState(cur.Status, "submitted")
		}
		q := s.q(ctx)
		steps, err := s.stepsOfCycle(ctx, p, propertyID, docRef{DocSR, id}, cur.Cycle)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		for _, st := range steps {
			if st.Status == StepWaiting || st.Status == StepActive {
				if st.Status == StepActive {
					if err := q.CloseStepNotices(ctx, inventorydb.CloseStepNoticesParams{PropertyID: propertyID, StepID: &st.ID, Now: &now}); err != nil {
						return err
					}
				}
				if err := q.SetStepState(ctx, inventorydb.SetStepStateParams{PropertyID: propertyID, ID: st.ID, Status: StepNotReached}); err != nil {
					return err
				}
			}
		}
		if err := q.SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRDraft, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_recalled", "inv_requisition", id, cur.SrNumber, map[string]any{"status": SRSubmitted}, map[string]any{"status": SRDraft}))
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

// CancelRequisition ends a draft or a returned requisition (inventory.request, the requester).
func (s *Service) CancelRequisition(ctx context.Context, propertyID, id int64) (Requisition, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadRequisitionForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != SRDraft && cur.Status != SRReturned {
			return errRequisitionState(cur.Status, "a draft or returned")
		}
		if err := s.q(ctx).SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRCancelled, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			ReturnedNote: cur.ReturnedNote, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_cancelled", "inv_requisition", id, cur.SrNumber, map[string]any{"status": cur.Status}, map[string]any{"status": SRCancelled}))
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

// RejectRequisition is the store saying it cannot supply an approved requisition (inventory.move), with a reason: what was sent stays sent, the rest is not coming. It is the store's own decision, after the chain.
func (s *Service) RejectRequisition(ctx context.Context, propertyID, id int64, reason string) (Requisition, error) {
	p, err := s.canMove(ctx, propertyID)
	if err != nil {
		return Requisition{}, err
	}
	reason = trimmed(reason)
	if n := len([]rune(reason)); n < 1 || n > 300 {
		return Requisition{}, invalid("rejection", fieldErr("reason", "REQUIRED", "say why the store cannot supply, 300 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadRequisitionForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Status != SRApproved && cur.Status != SRPartial {
			return apperr.Conflict("SR_NOT_APPROVED", "the requisition is "+cur.Status+": the store rejects a requisition that is approved or partly answered").WithContext("status", cur.Status)
		}
		if err := s.q(ctx).SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: SRRejected, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			RejectedNote: &reason, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := s.notifyDoc(ctx, p.TenantID, propertyID, []int64{cur.RequesterID}, NoticeReturned, nil, docRef{DocSR, id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.sr_rejected", "inv_requisition", id, cur.SrNumber, map[string]any{"status": cur.Status}, map[string]any{"status": SRRejected, "reason": reason}))
	})
	if err != nil {
		return Requisition{}, err
	}
	return s.requisition(ctx, p, propertyID, id)
}

// refreshRequisitionStatus gives an approved requisition the state its issued quantities say: FULFILLED when every line was sent, PARTIAL when some was, APPROVED when none was. A rejected or cancelled one stays so.
func (s *Service) refreshRequisitionStatus(ctx context.Context, p auth.Principal, propertyID, id int64) error {
	q := s.q(ctx)
	cur, err := q.GetRequisition(ctx, inventorydb.GetRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return err
	}
	if !slices.Contains([]string{SRApproved, SRPartial, SRFulfilled}, cur.Status) {
		return nil
	}
	lines, err := q.ListRequisitionLines(ctx, inventorydb.ListRequisitionLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: id})
	if err != nil {
		return err
	}
	any, all := false, true
	for _, l := range lines {
		if l.QtyIssuedStock.IsPositive() {
			any = true
		}
		if l.QtyIssuedStock.LessThan(l.QtyStock) {
			all = false
		}
	}
	status := SRApproved
	switch {
	case any && all:
		status = SRFulfilled
	case any:
		status = SRPartial
	}
	if status == cur.Status {
		return nil
	}
	return q.SetRequisitionState(ctx, inventorydb.SetRequisitionStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: status, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
		EstTotal: cur.EstTotal, ActorID: p.ActorID()})
}
