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

// The purchase request (PR) and its approval (35-inventory.md 5.3.1, 5.8). A person with inventory.request writes it; submitting copies the chain of the property into the steps of the round; the approvers decide
// one level at a time; an approved request is turned into orders (orders.go).

// States of a request.
const (
	ReqDraft     = "DRAFT"
	ReqSubmitted = "SUBMITTED"
	ReqApproved  = "APPROVED"
	ReqReturned  = "RETURNED"
	ReqConverted = "CONVERTED"
	ReqCancelled = "CANCELLED"
)

const maxDocLines = 200

// RequestLineInput is a line of a request being written: an item in one of its units, the quantity in that unit and the price the person expects to pay for one such unit.
type RequestLineInput struct {
	ItemID       int64            `json:"item_id"`
	UnitID       int64            `json:"unit_id"`
	Qty          decimal.Decimal  `json:"qty"`
	EstUnitPrice *decimal.Decimal `json:"est_unit_price"`
	SupplierID   *int64           `json:"supplier_id"`
	Note         *string          `json:"note"`
}

// RequestInput creates (ClientUUID) or changes (Version) a request while it is a draft or has been returned.
type RequestInput struct {
	ClientUUID   string             `json:"client_uuid"`
	DepartmentID *int64             `json:"department_id"`
	WarehouseID  *int64             `json:"warehouse_id"`
	NeededOn     *civil.Date        `json:"needed_on"`
	Note         *string            `json:"note"`
	Lines        []RequestLineInput `json:"lines"`
	Version      int32              `json:"version"`
}

// RequestLine is a line of a request as it stands.
type RequestLine struct {
	LineNo        int              `json:"line_no"`
	ItemID        int64            `json:"item_id"`
	ItemCode      string           `json:"item_code"`
	ItemName      string           `json:"item_name"`
	UnitID        int64            `json:"unit_id"`
	UnitCode      string           `json:"unit_code"`
	Qty           decimal.Decimal  `json:"qty"`
	QtyStock      decimal.Decimal  `json:"qty_stock"`
	StockUnitCode string           `json:"stock_unit_code"`
	EstUnitPrice  *decimal.Decimal `json:"est_unit_price"`
	Amount        decimal.Decimal  `json:"amount"`
	SupplierID    *int64           `json:"supplier_id"`
	SupplierCode  *string          `json:"supplier_code"`
	SupplierName  *string          `json:"supplier_name"`
	Note          *string          `json:"note"`
	OnHand        decimal.Decimal  `json:"on_hand"`
	PONumber      *string          `json:"po_number"`
}

// Stage is where a request is in its chain: the levels that apply and how many are done ("2/3 Cost control").
type Stage struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Name  string `json:"name"`
}

// Decision says what the signed-in user can do with the active level of a request: decide it, or why not.
type Decision struct {
	CanDecide bool    `json:"can_decide"`
	LevelNo   int     `json:"level_no"`
	Reason    *string `json:"reason"`
}

// Request is a purchase request with its lines and its approval.
type Request struct {
	ID             int64           `json:"id"`
	Number         string          `json:"request_number"`
	Status         string          `json:"status"`
	RequesterID    int64           `json:"requester_id"`
	RequesterName  string          `json:"requester_name"`
	DepartmentID   *int64          `json:"department_id"`
	DepartmentCode *string         `json:"department_code"`
	DepartmentName *string         `json:"department_name"`
	WarehouseID    *int64          `json:"warehouse_id"`
	WarehouseName  *string         `json:"warehouse_name"`
	NeededOn       *civil.Date     `json:"needed_on"`
	EstTotal       decimal.Decimal `json:"est_total"`
	Cycle          int             `json:"cycle"`
	SubmittedAt    *time.Time      `json:"submitted_at"`
	ReturnedNote   *string         `json:"returned_note"`
	Note           *string         `json:"note"`
	Version        int32           `json:"version"`
	CreatedAt      time.Time       `json:"created_at"`
	Stage          *Stage          `json:"stage"`
	Lines          []RequestLine   `json:"lines"`
	Steps          []ApprovalStep  `json:"steps"`
	Edits          []ApprovalEdit  `json:"edits"`
	Decision       *Decision       `json:"decision"`
	Orders         []POBrief       `json:"purchase_orders"`
}

// RequestBrief is a row of the list of requests.
type RequestBrief struct {
	ID             int64           `json:"id"`
	Number         string          `json:"request_number"`
	Status         string          `json:"status"`
	RequesterID    int64           `json:"requester_id"`
	RequesterName  string          `json:"requester_name"`
	DepartmentID   *int64          `json:"department_id"`
	DepartmentName *string         `json:"department_name"`
	WarehouseName  *string         `json:"warehouse_name"`
	NeededOn       *civil.Date     `json:"needed_on"`
	EstTotal       decimal.Decimal `json:"est_total"`
	LineCount      int             `json:"line_count"`
	SubmittedAt    *time.Time      `json:"submitted_at"`
	CreatedAt      time.Time       `json:"created_at"`
	Stage          *Stage          `json:"stage"`
	WaitingForMe   bool            `json:"waiting_for_me"`
}

// RequestFilter narrows the list of requests.
type RequestFilter struct {
	Status string
	Mine   bool
	Q      string
	Limit  int
}

// requestReaders may read the requests: the permissions of the module, and the approvers of the chain.
var requestReaders = []auth.Permission{auth.PermInventoryView, auth.PermInventoryManage, auth.PermInventoryRequest, auth.PermInventoryPurchase, auth.PermInventoryReceive,
	auth.PermInventoryDeptApprove, auth.PermInventoryCostControl, auth.PermInventoryFinalApprove, auth.PermInventorySRFinalApprove}

func (s *Service) canReadRequests(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, requestReaders...)
}

func (s *Service) canRequest(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermInventoryRequest)
}

func errNotRequester() *apperr.Error {
	return apperr.Forbidden("NOT_THE_REQUESTER", "only the person who wrote the request can change, submit, recall or cancel it")
}

func errRequestState(status string, want string) *apperr.Error {
	return apperr.Conflict("REQUEST_STATE_INVALID", "the request is "+status+": this is for a request that is "+want).WithContext("status", status)
}

// ---------------------------------------------------------------------------------------------------------------- reading

// stageOf reads the stage of a request from the steps of its current round.
func stageOf(steps []ApprovalStep, cycle int, total decimal.Decimal) *Stage {
	var applying []ApprovalStep
	for _, st := range steps {
		if st.Cycle != cycle {
			continue
		}
		switch st.Status {
		case StepSkipped, StepNotReached:
			continue
		case StepWaiting:
			if !st.Applies {
				continue
			}
		}
		applying = append(applying, st)
	}
	if len(applying) == 0 {
		return nil
	}
	out := &Stage{Total: len(applying)}
	for _, st := range applying {
		if st.Status == StepApproved {
			out.Done++
		}
	}
	for _, st := range applying {
		if st.Status == StepActive || st.Status == StepReturned {
			out.Name = st.Name
			break
		}
	}
	if out.Name == "" && out.Done > 0 {
		out.Name = applying[len(applying)-1].Name
	}
	return out
}

func (s *Service) stepsOf(ctx context.Context, tenantID, propertyID int64, requestIDs []int64, totals map[int64]decimal.Decimal) (map[int64][]ApprovalStep, error) {
	rows, err := s.q(ctx).ListSteps(ctx, inventorydb.ListStepsParams{TenantID: tenantID, PropertyID: propertyID, RequestIds: requestIDs})
	if err != nil {
		return nil, err
	}
	out := map[int64][]ApprovalStep{}
	for _, r := range rows {
		out[r.RequestID] = append(out[r.RequestID], toStep(r, totals[r.RequestID]))
	}
	return out, nil
}

// ListRequests lists the requests, newest first.
func (s *Service) ListRequests(ctx context.Context, propertyID int64, f RequestFilter) ([]RequestBrief, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	arg := inventorydb.ListRequestsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: optional(&f.Status), Q: optional(&f.Q), RowLimit: limitOf(f.Limit)}
	if f.Mine {
		arg.RequesterID = &p.UserID
	}
	rows, err := q.ListRequests(ctx, arg)
	if err != nil {
		return nil, err
	}
	idList := make([]int64, 0, len(rows))
	totals := map[int64]decimal.Decimal{}
	for _, r := range rows {
		idList = append(idList, r.ID)
		totals[r.ID] = r.EstTotal
	}
	steps, err := s.stepsOf(ctx, p.TenantID, propertyID, idList, totals)
	if err != nil {
		return nil, err
	}
	waiting := map[int64]bool{}
	if inbox, err := q.ApprovalInbox(ctx, inventorydb.ApprovalInboxParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, IsAdmin: p.IsTenantAdmin}); err == nil {
		for _, it := range inbox {
			waiting[it.RequestID] = true
		}
	} else {
		return nil, err
	}
	out := make([]RequestBrief, 0, len(rows))
	for _, r := range rows {
		b := RequestBrief{ID: r.ID, Number: r.RequestNumber, Status: r.Status, RequesterID: r.RequesterID, RequesterName: r.RequesterName, DepartmentID: r.DepartmentID, DepartmentName: r.DepartmentName,
			WarehouseName: r.WarehouseName, NeededOn: r.NeededOn, EstTotal: r.EstTotal, LineCount: int(r.LineCount), SubmittedAt: r.SubmittedAt, CreatedAt: r.CreatedAt, WaitingForMe: waiting[r.ID]}
		if r.Status == ReqSubmitted || r.Status == ReqApproved || r.Status == ReqReturned || r.Status == ReqConverted {
			b.Stage = stageOf(steps[r.ID], int(r.Cycle), r.EstTotal)
		}
		out = append(out, b)
	}
	return out, nil
}

// GetRequest answers one request with its lines, the steps of every round, the edits, the orders made from it and what the signed-in user can decide.
func (s *Service) GetRequest(ctx context.Context, propertyID, id int64) (Request, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

func (s *Service) request(ctx context.Context, p auth.Principal, propertyID, id int64) (Request, error) {
	q := s.q(ctx)
	r, err := q.GetRequest(ctx, inventorydb.GetRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Request{}, orNotFound(err, notFound("REQUEST_NOT_FOUND", "request"))
	}
	out := Request{ID: r.ID, Number: r.RequestNumber, Status: r.Status, RequesterID: r.RequesterID, RequesterName: r.RequesterName, DepartmentID: r.DepartmentID, DepartmentCode: r.DepartmentCode,
		DepartmentName: r.DepartmentName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName, NeededOn: r.NeededOn, EstTotal: r.EstTotal, Cycle: int(r.Cycle), SubmittedAt: r.SubmittedAt,
		ReturnedNote: r.ReturnedNote, Note: r.Note, Version: r.Version, CreatedAt: r.CreatedAt}
	lines, err := q.ListRequestLines(ctx, inventorydb.ListRequestLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id})
	if err != nil {
		return Request{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	out.Lines = make([]RequestLine, 0, len(lines))
	for _, l := range lines {
		amount := decimal.Zero
		if l.EstUnitPrice != nil {
			amount = l.QtyPurchase.Mul(*l.EstUnitPrice).Round(prop.CurrencyDecimals)
		}
		out.Lines = append(out.Lines, RequestLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, UnitID: l.PurchaseUnitID, UnitCode: l.UnitCode, Qty: l.QtyPurchase,
			QtyStock: l.QtyStock, StockUnitCode: l.StockUnitCode, EstUnitPrice: l.EstUnitPrice, Amount: amount, SupplierID: l.SupplierID, SupplierCode: l.SupplierCode, SupplierName: l.SupplierName,
			Note: l.Note, OnHand: l.OnHand})
	}
	ordered, err := q.ListRequestLineOrders(ctx, inventorydb.ListRequestLineOrdersParams{PropertyID: propertyID, RequestID: &id})
	if err != nil {
		return Request{}, err
	}
	for _, o := range ordered {
		if o.RequestLineNo == nil {
			continue
		}
		for i := range out.Lines {
			if out.Lines[i].LineNo == int(*o.RequestLineNo) {
				number := o.PoNumber
				out.Lines[i].PONumber = &number
			}
		}
	}
	steps, err := s.stepsOf(ctx, p.TenantID, propertyID, []int64{id}, map[int64]decimal.Decimal{id: r.EstTotal})
	if err != nil {
		return Request{}, err
	}
	out.Steps = steps[id]
	if out.Steps == nil {
		out.Steps = []ApprovalStep{}
	}
	if r.Status != ReqDraft && r.Status != ReqCancelled {
		out.Stage = stageOf(out.Steps, int(r.Cycle), r.EstTotal)
	}
	edits, err := q.ListEdits(ctx, inventorydb.ListEditsParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id})
	if err != nil {
		return Request{}, err
	}
	out.Edits = make([]ApprovalEdit, 0, len(edits))
	for _, e := range edits {
		out.Edits = append(out.Edits, ApprovalEdit{ID: e.ID, StepID: e.StepID, LevelNo: int(e.LevelNo), Cycle: int(e.Cycle), LineNo: int(e.LineNo), Field: e.Field, OldValue: e.OldValue, NewValue: e.NewValue,
			EditedBy: e.EditedBy, EditedByName: e.EditedByName, EditedAt: e.EditedAt})
	}
	out.Orders, err = s.ordersOfRequest(ctx, p.TenantID, propertyID, id)
	if err != nil {
		return Request{}, err
	}
	if r.Status == ReqSubmitted {
		out.Decision, err = s.decisionFor(ctx, p, propertyID, r.RequesterID, r.DepartmentID, out.Steps, int(r.Cycle))
		if err != nil {
			return Request{}, err
		}
		for i := range out.Steps { // who is being waited for
			st := &out.Steps[i]
			if st.Status != StepActive || st.Cycle != int(r.Cycle) {
				continue
			}
			who, err := s.eligible(ctx, p.TenantID, propertyID, r.DepartmentID, r.RequesterID, st.ApproverKind, st.Permission, decidedBy(out.Steps, int(r.Cycle)))
			if err != nil {
				return Request{}, err
			}
			for _, w := range who {
				st.Approvers = append(st.Approvers, w.Name)
			}
		}
	}
	return out, nil
}

// decidedBy are the people who approved a level of the round.
func decidedBy(steps []ApprovalStep, cycle int) map[int64]bool {
	m := map[int64]bool{}
	for _, st := range steps {
		if st.Cycle == cycle && st.Status == StepApproved && st.DecidedBy != nil {
			m[*st.DecidedBy] = true
		}
	}
	return m
}

func ptrStr(s string) *string { return &s }

// decisionFor says whether the signed-in user can decide the active step of a request, and if not, why.
func (s *Service) decisionFor(ctx context.Context, p auth.Principal, propertyID, requesterID int64, departmentID *int64, steps []ApprovalStep, cycle int) (*Decision, error) {
	var active *ApprovalStep
	for i := range steps {
		if steps[i].Cycle == cycle && steps[i].Status == StepActive {
			active = &steps[i]
		}
	}
	if active == nil {
		return nil, nil
	}
	d := &Decision{LevelNo: active.LevelNo}
	switch {
	case requesterID == p.UserID:
		d.Reason = ptrStr("OWN_DOCUMENT")
		return d, nil
	case decidedBy(steps, cycle)[p.UserID]:
		d.Reason = ptrStr("SAME_PERSON")
		return d, nil
	}
	who, err := s.eligible(ctx, p.TenantID, propertyID, departmentID, requesterID, active.ApproverKind, active.Permission, nil)
	if err != nil {
		return nil, err
	}
	if slices.Contains(ids(who), p.UserID) {
		d.CanDecide = true
		return d, nil
	}
	d.Reason = ptrStr("NOT_ELIGIBLE")
	return d, nil
}

// ---------------------------------------------------------------------------------------------------------------- writing

type checkedLine struct {
	in       RequestLineInput
	item     inventorydb.GetItemRow
	stockQty decimal.Decimal
	price    *decimal.Decimal
}

// checkRequestLines validates every line before anything is written: the item and its unit, the quantity, the price, the supplier, one line per item.
func (s *Service) checkRequestLines(ctx context.Context, p auth.Principal, propertyID int64, lines []RequestLineInput, decimals int32) ([]checkedLine, error) {
	q := s.q(ctx)
	var f []apperr.FieldError
	if len(lines) == 0 || len(lines) > maxDocLines {
		f = append(f, fieldErr("lines", "OUT_OF_RANGE", "1 to "+itoa(maxDocLines)+" lines"))
		return nil, invalid("request", f...)
	}
	itemIDs := make([]int64, 0, len(lines))
	for _, l := range lines {
		itemIDs = append(itemIDs, l.ItemID)
	}
	units := map[[2]int64]inventorydb.ListItemUnitsOfRow{}
	rows, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		units[[2]int64{r.ItemID, r.UnitID}] = r
	}
	seen := map[int64]bool{}
	out := make([]checkedLine, 0, len(lines))
	for i, l := range lines {
		at := fmt.Sprintf("lines[%d]", i)
		it, err := q.GetItem(ctx, inventorydb.GetItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: l.ItemID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			f = append(f, fieldErr(at+".item_id", "NOT_FOUND", "no such item in this property"))
			continue
		case err != nil:
			return nil, err
		case !it.IsActive:
			f = append(f, fieldErr(at+".item_id", "INACTIVE", "the item is switched off"))
			continue
		case seen[l.ItemID]:
			f = append(f, fieldErr(at+".item_id", "DUPLICATE", "an item is on the document once"))
			continue
		}
		seen[l.ItemID] = true
		u, ok := units[[2]int64{l.ItemID, l.UnitID}]
		if !ok {
			f = append(f, fieldErr(at+".unit_id", "NOT_FOUND", "not a unit of the item"))
			continue
		}
		if !l.Qty.IsPositive() || l.Qty.GreaterThan(decimal.NewFromInt(1_000_000_000)) || !l.Qty.Equal(Round6(l.Qty)) {
			f = append(f, fieldErr(at+".qty", "INVALID_VALUE", "above zero, six decimals at most"))
			continue
		}
		if l.EstUnitPrice != nil && (l.EstUnitPrice.IsNegative() || l.EstUnitPrice.GreaterThan(decimal.NewFromInt(1_000_000_000_000)) || !l.EstUnitPrice.Equal(l.EstUnitPrice.Round(decimals))) {
			f = append(f, fieldErr(at+".est_unit_price", "INVALID_VALUE", fmt.Sprintf("zero or more, %d decimals at most", decimals)))
			continue
		}
		if l.Note != nil && len([]rune(trimmed(*l.Note))) > 200 {
			f = append(f, fieldErr(at+".note", "INVALID_VALUE", "200 characters at most"))
			continue
		}
		if l.SupplierID != nil {
			if _, err := q.GetSupplierLite(ctx, inventorydb.GetSupplierLiteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *l.SupplierID}); errors.Is(err, pgx.ErrNoRows) {
				f = append(f, fieldErr(at+".supplier_id", "NOT_FOUND", "no such supplier in this property"))
				continue
			} else if err != nil {
				return nil, err
			}
		}
		qty := StockQty(l.Qty, u.StockNum, u.StockDen)
		if !qty.IsPositive() {
			f = append(f, fieldErr(at+".qty", "TOO_SMALL", "less than the smallest quantity of the stock unit"))
			continue
		}
		out = append(out, checkedLine{in: l, item: it, stockQty: qty, price: l.EstUnitPrice})
	}
	if len(f) > 0 {
		return nil, invalid("request", f...)
	}
	return out, nil
}

func totalOfLines(lines []checkedLine, decimals int32) decimal.Decimal {
	t := decimal.Zero
	for _, l := range lines {
		if l.price != nil {
			t = t.Add(l.in.Qty.Mul(*l.price).Round(decimals))
		}
	}
	return t
}

func (s *Service) checkRequestHeader(ctx context.Context, p auth.Principal, propertyID int64, in RequestInput) error {
	q := s.q(ctx)
	var f []apperr.FieldError
	if in.Note != nil && len([]rune(trimmed(*in.Note))) > 300 {
		f = append(f, fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	if in.DepartmentID != nil && s.depts != nil {
		if err := s.depts.Check(ctx, p.TenantID, propertyID, *in.DepartmentID, "department_id"); err != nil {
			return err
		}
	}
	if in.WarehouseID != nil {
		w, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.WarehouseID})
		if errors.Is(err, pgx.ErrNoRows) {
			f = append(f, fieldErr("warehouse_id", "NOT_FOUND", "no such warehouse in this property"))
		} else if err != nil {
			return err
		} else if !w.IsActive {
			f = append(f, fieldErr("warehouse_id", "INACTIVE", "the warehouse is switched off"))
		}
	}
	if len(f) > 0 {
		return invalid("request", f...)
	}
	return nil
}

func (s *Service) writeRequestLines(ctx context.Context, p auth.Principal, propertyID, id int64, lines []checkedLine) error {
	q := s.q(ctx)
	if err := q.DeleteRequestLines(ctx, inventorydb.DeleteRequestLinesParams{PropertyID: propertyID, RequestID: id}); err != nil {
		return err
	}
	for i, l := range lines {
		if err := q.InsertRequestLine(ctx, inventorydb.InsertRequestLineParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id, LineNo: int32(i + 1), //nolint:gosec // G115: at most 200
			ItemID: l.item.ID, PurchaseUnitID: l.in.UnitID, QtyPurchase: l.in.Qty, QtyStock: l.stockQty, EstUnitPrice: l.price, SupplierID: l.in.SupplierID, Note: optional(l.in.Note)}); err != nil {
			return err
		}
	}
	return nil
}

// CreateRequest writes a purchase request as a draft (inventory.request). A repeated request with the same client_uuid answers the first.
func (s *Service) CreateRequest(ctx context.Context, propertyID int64, in RequestInput) (Request, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Request{}, invalid("request", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		if prev, err := q.GetRequestByClientUUID(ctx, inventorydb.GetRequestByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := s.checkRequestHeader(ctx, p, propertyID, in); err != nil {
			return err
		}
		lines, err := s.checkRequestLines(ctx, p, propertyID, in.Lines, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvPR)
		if err != nil {
			return err
		}
		id, err = q.InsertRequest(ctx, inventorydb.InsertRequestParams{TenantID: p.TenantID, PropertyID: propertyID, RequestNumber: number, ClientUuid: key, RequesterID: p.UserID, DepartmentID: in.DepartmentID,
			WarehouseID: in.WarehouseID, NeededOn: in.NeededOn, Note: optional(in.Note), EstTotal: totalOfLines(lines, prop.CurrencyDecimals), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.writeRequestLines(ctx, p, propertyID, id, lines); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_created", "inv_request", id, number, nil, map[string]any{"number": number, "lines": len(lines), "est_total": totalOfLines(lines, prop.CurrencyDecimals)}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

// UpdateRequest changes a request while it is a draft or was returned (inventory.request, the requester). The lines are replaced.
func (s *Service) UpdateRequest(ctx context.Context, propertyID, id int64, in RequestInput) (Request, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := db.LockRows(ctx, db.StockRequests, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return orNotFound(err, notFound("REQUEST_NOT_FOUND", "request"))
		}
		q := s.q(ctx)
		cur, err := q.GetRequest(ctx, inventorydb.GetRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("REQUEST_NOT_FOUND", "request"))
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != ReqDraft && cur.Status != ReqReturned {
			return errRequestState(cur.Status, "a draft or returned")
		}
		if cur.Version != in.Version {
			return versionConflict("request", cur.Version)
		}
		if err := s.checkRequestHeader(ctx, p, propertyID, in); err != nil {
			return err
		}
		lines, err := s.checkRequestLines(ctx, p, propertyID, in.Lines, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		total := totalOfLines(lines, prop.CurrencyDecimals)
		if _, err := q.UpdateRequestDraft(ctx, inventorydb.UpdateRequestDraftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, DepartmentID: in.DepartmentID, WarehouseID: in.WarehouseID,
			NeededOn: in.NeededOn, Note: optional(in.Note), EstTotal: total, Version: in.Version, ActorID: p.ActorID()}); err != nil {
			return orNotFound(err, versionConflict("request", cur.Version))
		}
		if err := s.writeRequestLines(ctx, p, propertyID, id, lines); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_updated", "inv_request", id, cur.RequestNumber, map[string]any{"est_total": cur.EstTotal}, map[string]any{"est_total": total, "lines": len(lines)}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

// loadForChange locks a request and reads it for a change; the caller is inside s.write.
func (s *Service) loadForChange(ctx context.Context, p auth.Principal, propertyID, id int64) (inventorydb.GetRequestRow, error) {
	if err := db.LockRows(ctx, db.StockRequests, db.ForUpdate, propertyID, []int64{id}); err != nil {
		return inventorydb.GetRequestRow{}, orNotFound(err, notFound("REQUEST_NOT_FOUND", "request"))
	}
	cur, err := s.q(ctx).GetRequest(ctx, inventorydb.GetRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return cur, orNotFound(err, notFound("REQUEST_NOT_FOUND", "request"))
	}
	return cur, nil
}

// SubmitRequest copies the chain of the property into the steps of a new round and sends the request to the first level (inventory.request, the requester). A request that no level applies to is approved at
// once. A level that applies and that nobody can approve refuses the submission (409 APPROVAL_CHAIN_UNSATISFIABLE).
func (s *Service) SubmitRequest(ctx context.Context, propertyID, id int64) (Request, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != ReqDraft && cur.Status != ReqReturned {
			return errRequestState(cur.Status, "a draft or returned")
		}
		if cur.DepartmentID == nil {
			return invalid("request", fieldErr("department_id", "REQUIRED", "the department whose head approves the request"))
		}
		lines, err := s.q(ctx).ListRequestLines(ctx, inventorydb.ListRequestLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id})
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			return invalid("request", fieldErr("lines", "OUT_OF_RANGE", "at least one line"))
		}
		if err := s.ensureChain(ctx, p, propertyID, DocPR); err != nil {
			return err
		}
		chain, err := s.loadChain(ctx, p.TenantID, propertyID, DocPR)
		if err != nil {
			return err
		}
		// the round: one more than the rounds there were
		old, err := s.q(ctx).ListStepsOfCycle(ctx, inventorydb.ListStepsOfCycleParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id, Cycle: cur.Cycle})
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
		// which levels apply now, and can each be given to a different person?
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
			if _, err := s.q(ctx).InsertStep(ctx, inventorydb.InsertStepParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id, Cycle: cycle, LevelNo: int16(l.LevelNo), //nolint:gosec // G115: small
				Name: l.Name, ApproverKind: l.ApproverKind, Permission: l.Permission, ConditionKind: l.ConditionKind, Threshold: l.Threshold, EditAllowed: chain.ApproverMayEdit, Status: StepWaiting}); err != nil {
				return err
			}
		}
		if err := s.q(ctx).SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ReqSubmitted, Cycle: cycle, SubmittedAt: &now, //nolint:gosec // G115: small
			ReturnedNote: nil, EstTotal: total, ActorID: p.ActorID()}); err != nil {
			return err
		}
		done, err := s.advance(ctx, p, propertyID, id, cycle, cur.DepartmentID, p.UserID, total, chain.SkipLevel1IfHead, leader)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_submitted", "inv_request", id, cur.RequestNumber, map[string]any{"status": cur.Status},
			map[string]any{"status": ReqSubmitted, "round": cycle, "est_total": total, "levels": len(chain.Levels)})); err != nil {
			return err
		}
		if done {
			return s.finishRequest(ctx, p, propertyID, bd, id, cur.RequestNumber, cycle, total, "no level of the chain applies")
		}
		return nil
	})
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

// advance gives the request to its next level: each waiting level is read in order, a conditional level the total does not reach is skipped, the first that applies becomes active and its people are told.
// It reports true when no level is left: the request is approved.
func (s *Service) advance(ctx context.Context, p auth.Principal, propertyID, requestID int64, cycle int16, departmentID *int64, requesterID int64, total decimal.Decimal, skipHead, requesterLeads bool) (bool, error) {
	q := s.q(ctx)
	steps, err := q.ListStepsOfCycle(ctx, inventorydb.ListStepsOfCycleParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: requestID, Cycle: cycle})
	if err != nil {
		return false, err
	}
	now := s.clock.Now()
	decided := map[int64]bool{}
	for _, st := range steps {
		if st.Status == StepApproved && st.DecidedBy != nil {
			decided[*st.DecidedBy] = true
		}
	}
	for _, st := range steps {
		if st.Status != StepWaiting {
			continue
		}
		skip := func(reason string) error {
			return q.SetStepState(ctx, inventorydb.SetStepStateParams{PropertyID: propertyID, ID: st.ID, Status: StepSkipped, SkipReason: &reason})
		}
		switch {
		case st.ConditionKind == CondTotalAbove && st.Threshold != nil && !total.GreaterThan(*st.Threshold):
			if err := skip("CONDITION"); err != nil {
				return false, err
			}
			continue
		case st.LevelNo == 1 && st.ApproverKind == ApproverDeptHead && skipHead && requesterLeads:
			if err := skip("REQUESTER_IS_HEAD"); err != nil {
				return false, err
			}
			continue
		}
		if err := q.SetStepState(ctx, inventorydb.SetStepStateParams{PropertyID: propertyID, ID: st.ID, Status: StepActive, ActivatedAt: &now}); err != nil {
			return false, err
		}
		who, err := s.eligible(ctx, p.TenantID, propertyID, departmentID, requesterID, st.ApproverKind, st.Permission, decided)
		if err != nil {
			return false, err
		}
		stepID, reqID := st.ID, requestID
		if len(who) == 0 { // stuck: the administrators are told
			admins, err := q.ListUsersWithPermission(ctx, inventorydb.ListUsersWithPermissionParams{TenantID: p.TenantID, PropertyID: propertyID, Permission: string(auth.PermPropertyManage)})
			if err != nil {
				return false, err
			}
			adminIDs := make([]int64, 0, len(admins))
			for _, a := range admins {
				adminIDs = append(adminIDs, a.ID)
			}
			return false, s.notify(ctx, p.TenantID, propertyID, adminIDs, NoticeStuck, &stepID, &reqID)
		}
		return false, s.notify(ctx, p.TenantID, propertyID, ids(who), NoticeWaiting, &stepID, &reqID)
	}
	return true, nil
}

// finishRequest approves a request whose chain is done and tells the requester.
func (s *Service) finishRequest(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, id int64, number string, cycle int16, total decimal.Decimal, note string) error {
	q := s.q(ctx)
	cur, err := q.GetRequest(ctx, inventorydb.GetRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return err
	}
	if err := q.SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ReqApproved, Cycle: cycle, SubmittedAt: cur.SubmittedAt, //nolint:gosec // G115: small
		ReturnedNote: nil, EstTotal: total, ActorID: p.ActorID()}); err != nil {
		return err
	}
	reqID := id
	if err := s.notify(ctx, p.TenantID, propertyID, []int64{cur.RequesterID}, NoticeApproved, nil, &reqID); err != nil {
		return err
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_approved", "inv_request", id, number, map[string]any{"status": ReqSubmitted},
		map[string]any{"status": ReqApproved, "round": cycle, "est_total": total, "note": note}))
}

// ApproveInput is the decision of an approver on the active level: an optional note, optional edits of the lines, and the level the approver saw (a decision on a level that is no longer the active one is refused).
type ApproveInput struct {
	LevelNo *int          `json:"level_no"`
	Note    *string       `json:"note"`
	Edits   []ApproveEdit `json:"edits"`
}

// ApproveEdit changes the quantity or the expected price of a line before it is approved (35-inventory.md O2).
type ApproveEdit struct {
	LineNo       int              `json:"line_no"`
	Qty          *decimal.Decimal `json:"qty"`
	EstUnitPrice *decimal.Decimal `json:"est_unit_price"`
}

// ReturnRequestInput sends a request back to its requester, with the reason.
type ReturnRequestInput struct {
	LevelNo *int   `json:"level_no"`
	Note    string `json:"note"`
}

// activeStepFor checks that the signed-in user may decide the active step of a request, in the order of the rules: R3 the level that is active, R2 not the requester, R1 not twice in a round, then eligibility.
// Replay reports that this user has decided that level already: the caller answers the request as it is.
func (s *Service) activeStepFor(ctx context.Context, p auth.Principal, propertyID int64, cur inventorydb.GetRequestRow, levelNo *int) (step inventorydb.InvApprovalStep, replay bool, err error) {
	if cur.Status != ReqSubmitted {
		return step, false, apperr.Conflict("APPROVAL_NOT_IN_PROGRESS", "the request is not waiting for an approval").WithContext("status", cur.Status)
	}
	steps, err := s.q(ctx).ListStepsOfCycle(ctx, inventorydb.ListStepsOfCycleParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: cur.ID, Cycle: cur.Cycle})
	if err != nil {
		return step, false, err
	}
	var active *inventorydb.InvApprovalStep
	decided := map[int64]bool{}
	for i := range steps {
		if steps[i].Status == StepActive {
			active = &steps[i]
		}
		if steps[i].Status == StepApproved && steps[i].DecidedBy != nil {
			decided[*steps[i].DecidedBy] = true
			if levelNo != nil && int(steps[i].LevelNo) == *levelNo && *steps[i].DecidedBy == p.UserID {
				return steps[i], true, nil
			}
		}
	}
	if active == nil || (levelNo != nil && int(active.LevelNo) != *levelNo) {
		return step, false, apperr.Conflict("APPROVAL_NOT_YOUR_TURN", "this level is not the one that waits for a decision now")
	}
	if cur.RequesterID == p.UserID {
		return step, false, apperr.Forbidden("APPROVAL_OWN_DOCUMENT", "nobody approves their own request, the administrator included")
	}
	if decided[p.UserID] {
		return step, false, apperr.Conflict("APPROVAL_SAME_PERSON", "one person decides one level of a request in a round")
	}
	who, err := s.eligible(ctx, p.TenantID, propertyID, cur.DepartmentID, cur.RequesterID, active.ApproverKind, active.Permission, decided)
	if err != nil {
		return step, false, err
	}
	if !slices.Contains(ids(who), p.UserID) {
		return step, false, apperr.Forbidden("APPROVAL_NOT_ELIGIBLE", "you are not one of the people who decide this level")
	}
	return *active, false, nil
}

// ApproveRequest decides the active level of a request in favour (the signed-in approver: no password). The approver may change a quantity or a price first when the chain says so; the total is computed again and
// the later conditional levels read the new total; a level already passed is never reopened. When no level is left the request is approved.
func (s *Service) ApproveRequest(ctx context.Context, propertyID, id int64, in ApproveInput) (Request, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Request{}, err
	}
	if err := s.canDecideAt(ctx, p, propertyID); err != nil { // the gate of the module: a person who decides holds the permission of a level
		return Request{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	if note := optional(in.Note); note != nil && len([]rune(*note)) > 500 {
		return Request{}, invalid("approval", fieldErr("note", "INVALID_VALUE", "500 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.canDecideAt(ctx, p, propertyID); err != nil {
			return err
		}
		step, replay, err := s.activeStepFor(ctx, p, propertyID, cur, in.LevelNo)
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
			total, edited, err = s.applyEdits(ctx, p, propertyID, id, step.ID, prop.CurrencyDecimals, in.Edits)
			if err != nil {
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
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.approval_step_decided", "inv_request", id, cur.RequestNumber, nil,
			map[string]any{"level": step.LevelNo, "name": step.Name, "decision": "APPROVED", "total_seen": total, "note": in.Note, "edits": edited, "approved_by": p.UserID})); err != nil {
			return err
		}
		if err := q.SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ReqSubmitted, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			ReturnedNote: nil, EstTotal: total, ActorID: p.ActorID()}); err != nil {
			return err
		}
		chain, err := s.loadChain(ctx, p.TenantID, propertyID, DocPR)
		if err != nil {
			return err
		}
		done, err := s.advance(ctx, p, propertyID, id, cur.Cycle, cur.DepartmentID, cur.RequesterID, total, chain.SkipLevel1IfHead, false)
		if err != nil {
			return err
		}
		if done {
			return s.finishRequest(ctx, p, propertyID, bd, id, cur.RequestNumber, cur.Cycle, total, "the last level approved")
		}
		return nil
	})
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

// canDecideAt is the module gate of a decision: a person who may decide holds no permission to read the module necessarily, but the request and the property must be theirs to see.
func (s *Service) canDecideAt(ctx context.Context, p auth.Principal, propertyID int64) error {
	if p.IsTenantAdmin {
		return nil
	}
	return auth.RequireAny(ctx, s.authz, propertyID, auth.PermInventoryDeptApprove, auth.PermInventoryCostControl, auth.PermInventoryFinalApprove, auth.PermInventorySRFinalApprove)
}

// applyEdits changes the lines an approver edited, writes one row for each field changed and answers the new total of the request.
func (s *Service) applyEdits(ctx context.Context, p auth.Principal, propertyID, requestID, stepID int64, decimals int32, edits []ApproveEdit) (decimal.Decimal, []map[string]any, error) {
	q := s.q(ctx)
	lines, err := q.ListRequestLines(ctx, inventorydb.ListRequestLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: requestID})
	if err != nil {
		return decimal.Zero, nil, err
	}
	byNo := map[int32]inventorydb.ListRequestLinesRow{}
	for _, l := range lines {
		byNo[l.LineNo] = l
	}
	var f []apperr.FieldError
	for i, e := range edits {
		at := fmt.Sprintf("edits[%d].", i)
		l, ok := byNo[int32(e.LineNo)] //nolint:gosec // G115: bounded by the lines
		switch {
		case !ok:
			f = append(f, fieldErr(at+"line_no", "NOT_FOUND", "no such line"))
			continue
		case e.Qty == nil && e.EstUnitPrice == nil:
			f = append(f, fieldErr(at+"qty", "INVALID_VALUE", "a quantity or a price"))
			continue
		}
		_ = l
		if e.Qty != nil && (!e.Qty.IsPositive() || e.Qty.GreaterThan(decimal.NewFromInt(1_000_000_000)) || !e.Qty.Equal(Round6(*e.Qty))) {
			f = append(f, fieldErr(at+"qty", "INVALID_VALUE", "above zero, six decimals at most"))
		}
		if e.EstUnitPrice != nil && (e.EstUnitPrice.IsNegative() || e.EstUnitPrice.GreaterThan(decimal.NewFromInt(1_000_000_000_000)) || !e.EstUnitPrice.Equal(e.EstUnitPrice.Round(decimals))) {
			f = append(f, fieldErr(at+"est_unit_price", "INVALID_VALUE", fmt.Sprintf("zero or more, %d decimals at most", decimals)))
		}
	}
	if len(f) > 0 {
		e := apperr.Invalid("an edit is invalid", f...)
		e.Code = "APPROVAL_EDIT_NOT_ALLOWED"
		return decimal.Zero, nil, e
	}
	now := s.clock.Now()
	var changed []map[string]any
	for _, e := range edits {
		l := byNo[int32(e.LineNo)] //nolint:gosec // G115: checked above
		qty, price := l.QtyPurchase, l.EstUnitPrice
		if e.Qty != nil && !e.Qty.Equal(l.QtyPurchase) {
			if err := q.InsertEdit(ctx, inventorydb.InsertEditParams{TenantID: p.TenantID, PropertyID: propertyID, StepID: stepID, LineNo: int32(e.LineNo), Field: "QTY", //nolint:gosec // G115: checked above
				OldValue: l.QtyPurchase, NewValue: *e.Qty, EditedBy: p.UserID, EditedAt: now}); err != nil {
				return decimal.Zero, nil, err
			}
			changed = append(changed, map[string]any{"line": e.LineNo, "field": "QTY", "old": l.QtyPurchase, "new": *e.Qty})
			qty = *e.Qty
		}
		if e.EstUnitPrice != nil && (price == nil || !e.EstUnitPrice.Equal(*price)) {
			old := decimal.Zero
			if price != nil {
				old = *price
			}
			if err := q.InsertEdit(ctx, inventorydb.InsertEditParams{TenantID: p.TenantID, PropertyID: propertyID, StepID: stepID, LineNo: int32(e.LineNo), Field: "EST_UNIT_PRICE", //nolint:gosec // G115: checked above
				OldValue: old, NewValue: *e.EstUnitPrice, EditedBy: p.UserID, EditedAt: now}); err != nil {
				return decimal.Zero, nil, err
			}
			changed = append(changed, map[string]any{"line": e.LineNo, "field": "EST_UNIT_PRICE", "old": old, "new": *e.EstUnitPrice})
			pv := *e.EstUnitPrice
			price = &pv
		}
		stock := StockQty(qty, l.StockNum, l.StockDen)
		if !stock.IsPositive() {
			return decimal.Zero, nil, invalid("approval", fieldErr("edits", "TOO_SMALL", "less than the smallest quantity of the stock unit"))
		}
		if err := q.UpdateRequestLineEdit(ctx, inventorydb.UpdateRequestLineEditParams{PropertyID: propertyID, RequestID: requestID, LineNo: int32(e.LineNo), QtyPurchase: qty, QtyStock: stock, EstUnitPrice: price}); err != nil { //nolint:gosec // G115: checked above
			return decimal.Zero, nil, err
		}
		byNo[int32(e.LineNo)] = inventorydb.ListRequestLinesRow{LineNo: l.LineNo, QtyPurchase: qty, EstUnitPrice: price} //nolint:gosec // G115: checked above
	}
	total := decimal.Zero
	for _, l := range lines {
		cur := byNo[l.LineNo]
		if cur.EstUnitPrice != nil {
			total = total.Add(cur.QtyPurchase.Mul(*cur.EstUnitPrice).Round(decimals))
		}
	}
	return total, changed, nil
}

// ReturnRequest sends a request back to its requester from the active level, with a note (the signed-in approver). The later levels are not reached; the requester changes the request and submits it again, which
// starts a new round of the chain from the first level.
func (s *Service) ReturnRequest(ctx context.Context, propertyID, id int64, in ReturnRequestInput) (Request, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Request{}, err
	}
	if err := s.canDecideAt(ctx, p, propertyID); err != nil {
		return Request{}, err
	}
	note := trimmed(in.Note)
	if n := len([]rune(note)); n < 1 || n > 500 {
		return Request{}, invalid("return", fieldErr("note", "REQUIRED", "say what has to change, 500 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.canDecideAt(ctx, p, propertyID); err != nil {
			return err
		}
		step, _, err := s.activeStepFor(ctx, p, propertyID, cur, in.LevelNo)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		now := s.clock.Now()
		if err := q.SetStepState(ctx, inventorydb.SetStepStateParams{PropertyID: propertyID, ID: step.ID, Status: StepReturned, ActivatedAt: step.ActivatedAt, DecidedBy: &p.UserID, DecidedAt: &now,
			TotalSeen: &cur.EstTotal, Note: &note}); err != nil {
			return err
		}
		steps, err := q.ListStepsOfCycle(ctx, inventorydb.ListStepsOfCycleParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id, Cycle: cur.Cycle})
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
		if err := q.SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ReqReturned, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			ReturnedNote: &note, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		reqID := id
		if err := s.notify(ctx, p.TenantID, propertyID, []int64{cur.RequesterID}, NoticeReturned, &step.ID, &reqID); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.approval_step_decided", "inv_request", id, cur.RequestNumber, nil,
			map[string]any{"level": step.LevelNo, "name": step.Name, "decision": "RETURNED", "note": note, "approved_by": p.UserID})); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_returned", "inv_request", id, cur.RequestNumber, map[string]any{"status": ReqSubmitted}, map[string]any{"status": ReqReturned, "note": note}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

// RecallRequest pulls a submitted request back to a draft before it is approved (inventory.request, the requester). The steps stay as the history of the round.
func (s *Service) RecallRequest(ctx context.Context, propertyID, id int64) (Request, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != ReqSubmitted {
			return errRequestState(cur.Status, "submitted")
		}
		q := s.q(ctx)
		steps, err := q.ListStepsOfCycle(ctx, inventorydb.ListStepsOfCycleParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: id, Cycle: cur.Cycle})
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
		if err := q.SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ReqDraft, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			ReturnedNote: nil, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_recalled", "inv_request", id, cur.RequestNumber, map[string]any{"status": ReqSubmitted}, map[string]any{"status": ReqDraft}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

// CancelRequest ends a draft or a returned request (inventory.request, the requester).
func (s *Service) CancelRequest(ctx context.Context, propertyID, id int64) (Request, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.RequesterID != p.UserID {
			return errNotRequester()
		}
		if cur.Status != ReqDraft && cur.Status != ReqReturned {
			return errRequestState(cur.Status, "a draft or returned")
		}
		if err := s.q(ctx).SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ReqCancelled, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			ReturnedNote: cur.ReturnedNote, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_cancelled", "inv_request", id, cur.RequestNumber, map[string]any{"status": cur.Status}, map[string]any{"status": ReqCancelled}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.request(ctx, p, propertyID, id)
}

// releaseRequest frees the lines a cancelled order took from its request, and approves the request again when no order of it stands.
func (s *Service) releaseRequest(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, poID, requestID int64) error {
	q := s.q(ctx)
	if err := q.ReleasePOLines(ctx, inventorydb.ReleasePOLinesParams{PropertyID: propertyID, PoID: poID}); err != nil {
		return err
	}
	live, err := q.CountLiveOrdersOfRequest(ctx, inventorydb.CountLiveOrdersOfRequestParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: &requestID, PoID: poID})
	if err != nil {
		return err
	}
	if live > 0 {
		return nil
	}
	cur, err := q.GetRequest(ctx, inventorydb.GetRequestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID})
	if err != nil || cur.Status != ReqConverted {
		return err
	}
	if err := q.SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID, Status: ReqApproved, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
		ReturnedNote: nil, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
		return err
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_reopened", "inv_request", requestID, cur.RequestNumber, map[string]any{"status": ReqConverted}, map[string]any{"status": ReqApproved}))
}
