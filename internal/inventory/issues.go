package inventory

import (
	"context"
	"errors"
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

// The issue of goods out of a warehouse (35-inventory.md 5.4): to a department (use), waste, a staff meal, entertainment. Each is a document with lines; each line is a movement of the kind of its purpose, booked
// to an expense account with the department of the document. The department rule of the account is applied when the document is posted, so a required department that is missing is refused there and never at
// the day close. A manual issue never takes the stock below zero. The automatic amenities of a close are a document of the same table (amenities.go).

// Purposes of an issue.
const (
	PurposeUse           = "USE"
	PurposeWaste         = "WASTE"
	PurposeStaffMeal     = "STAFF_MEAL"
	PurposeEntertainment = "ENTERTAINMENT"
)

// Reasons of a waste.
var wasteReasons = map[string]bool{"SPOILAGE": true, "EXPIRED": true, "BREAKAGE": true, "TASTING": true, "OTHER": true}

// Origins of an issue.
const (
	OriginManual  = "MANUAL"
	OriginAmenity = "AMENITY"
)

// States of an issue and of its lines.
const (
	IssuePosted   = "POSTED"
	IssueReversed = "REVERSED"
	LineApplied   = "APPLIED"
	LineHeld      = "HELD"
)

// kindOfPurpose is the kind of movement of an issue of each purpose.
var kindOfPurpose = map[string]string{PurposeUse: KindIssue, PurposeWaste: KindWaste, PurposeStaffMeal: KindStaffMeal, PurposeEntertainment: KindEntertainment}

// IssueLineInput is a line of an issue being posted: an item and a quantity in one of its units, and for a use the expense account when it is not the one of the category.
type IssueLineInput struct {
	ItemID           int64           `json:"item_id"`
	UnitID           *int64          `json:"unit_id"`
	Qty              decimal.Decimal `json:"qty"`
	ExpenseAccountID *int64          `json:"expense_account_id"`
}

// IssueInput posts an issue. The department is required for a use, a staff meal and an entertainment; a waste takes the department of the warehouse's outlet or department.
type IssueInput struct {
	ClientUUID   string           `json:"client_uuid"`
	WarehouseID  int64            `json:"warehouse_id"`
	Purpose      string           `json:"purpose"`
	DepartmentID *int64           `json:"department_id"`
	ReasonCode   *string          `json:"reason_code"`
	Covers       *int             `json:"covers"`
	Beneficiary  *string          `json:"beneficiary"`
	Note         *string          `json:"note"`
	Lines        []IssueLineInput `json:"lines"`
}

// IssueLine is a line of a posted issue. A line of an amenities document that a count holds has no value yet.
type IssueLine struct {
	LineNo             int              `json:"line_no"`
	ItemID             int64            `json:"item_id"`
	ItemCode           string           `json:"item_code"`
	ItemName           string           `json:"item_name"`
	StockUnitCode      string           `json:"stock_unit_code"`
	Qty                decimal.Decimal  `json:"qty_stock"`
	Value              *decimal.Decimal `json:"value"`
	ExpenseAccountID   *int64           `json:"expense_account_id"`
	ExpenseAccountCode *string          `json:"expense_account_code"`
	ExpenseAccountName *string          `json:"expense_account_name"`
	DepartmentID       *int64           `json:"department_id"`
	DepartmentName     *string          `json:"department_name"`
	Status             string           `json:"status"`
	AppliedOn          *civil.Date      `json:"applied_on"`
}

// Issue is a posted issue with its lines.
type Issue struct {
	ID               int64            `json:"id"`
	Number           string           `json:"iss_number"`
	Purpose          string           `json:"purpose"`
	Origin           string           `json:"origin"`
	Status           string           `json:"status"`
	WarehouseID      int64            `json:"warehouse_id"`
	WarehouseName    string           `json:"warehouse_name"`
	DepartmentID     *int64           `json:"department_id"`
	DepartmentName   *string          `json:"department_name"`
	ReasonCode       *string          `json:"reason_code"`
	Covers           *int32           `json:"covers"`
	Beneficiary      *string          `json:"beneficiary"`
	BusinessDate     civil.Date       `json:"business_date"`
	SourceDate       *civil.Date      `json:"source_date"`
	Note             *string          `json:"note"`
	ReverseReason    *string          `json:"reverse_reason"`
	ReversesNumber   *string          `json:"reverses_number"`
	ReversedByNumber *string          `json:"reversed_by_number"`
	CreatedByName    *string          `json:"created_by_name"`
	CreatedAt        time.Time        `json:"created_at"`
	TotalValue       decimal.Decimal  `json:"total_value"`
	HeldLines        int              `json:"held_lines"`
	Reversible       bool             `json:"reversible"`
	CostPerCover     *decimal.Decimal `json:"cost_per_cover"`
	Lines            []IssueLine      `json:"lines"`
}

// IssueBrief is a row of the list of issues.
type IssueBrief struct {
	ID             int64           `json:"id"`
	Number         string          `json:"iss_number"`
	Purpose        string          `json:"purpose"`
	Origin         string          `json:"origin"`
	Status         string          `json:"status"`
	WarehouseName  string          `json:"warehouse_name"`
	DepartmentName *string         `json:"department_name"`
	ReasonCode     *string         `json:"reason_code"`
	Covers         *int32          `json:"covers"`
	Beneficiary    *string         `json:"beneficiary"`
	BusinessDate   civil.Date      `json:"business_date"`
	SourceDate     *civil.Date     `json:"source_date"`
	LineCount      int             `json:"line_count"`
	HeldLines      int             `json:"held_lines"`
	TotalValue     decimal.Decimal `json:"total_value"`
	IsReversal     bool            `json:"is_reversal"`
	CreatedAt      time.Time       `json:"created_at"`
}

// IssueFilter narrows the list of issues.
type IssueFilter struct {
	Purpose      string
	Origin       string
	WarehouseID  int64
	DepartmentID int64
	From, To     *civil.Date
	Q            string
	Limit        int
}

// CostByDepartment is a row of the card "cost per department" of the list.
type CostByDepartment struct {
	DepartmentName string          `json:"department_name"`
	Purpose        string          `json:"purpose"`
	Value          decimal.Decimal `json:"value"`
}

// IssueList is the list of issues with the cost of the period by department.
type IssueList struct {
	Data []IssueBrief       `json:"data"`
	Cost []CostByDepartment `json:"cost_by_department"`
}

// ListIssues lists the issues, newest first, with the cost by department and purpose of the same period (inventory.view or a permission of the movements).
func (s *Service) ListIssues(ctx context.Context, propertyID int64, f IssueFilter) (IssueList, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return IssueList{}, err
	}
	arg := inventorydb.ListIssuesParams{TenantID: p.TenantID, PropertyID: propertyID, Purpose: optional(&f.Purpose), Origin: optional(&f.Origin), Q: optional(&f.Q), FromDate: f.From, ToDate: f.To, RowLimit: limitOf(f.Limit)}
	if f.WarehouseID > 0 {
		arg.WarehouseID = &f.WarehouseID
	}
	if f.DepartmentID > 0 {
		arg.DepartmentID = &f.DepartmentID
	}
	rows, err := s.q(ctx).ListIssues(ctx, arg)
	if err != nil {
		return IssueList{}, err
	}
	out := IssueList{Data: make([]IssueBrief, 0, len(rows)), Cost: []CostByDepartment{}}
	for _, r := range rows {
		out.Data = append(out.Data, IssueBrief{ID: r.ID, Number: r.IssNumber, Purpose: r.Purpose, Origin: r.Origin, Status: r.Status, WarehouseName: r.WarehouseName, DepartmentName: r.DepartmentName,
			ReasonCode: r.ReasonCode, Covers: r.Covers, Beneficiary: r.Beneficiary, BusinessDate: r.BusinessDate, SourceDate: r.SourceDate, LineCount: int(r.LineCount), HeldLines: int(r.HeldLines),
			TotalValue: r.TotalValue, IsReversal: r.ReversesIssueID != nil, CreatedAt: r.CreatedAt})
	}
	from, to := f.From, f.To
	if from == nil || to == nil {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err == nil {
			first := civil.NewDate(day.BusinessDate.Year(), day.BusinessDate.Month(), 1)
			if from == nil {
				from = &first
			}
			if to == nil {
				last := day.BusinessDate
				to = &last
			}
		}
	}
	if from != nil && to != nil {
		cost, err := s.q(ctx).IssueCostByDepartment(ctx, inventorydb.IssueCostByDepartmentParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: *from, ToDate: *to})
		if err != nil {
			return IssueList{}, err
		}
		for _, c := range cost {
			out.Cost = append(out.Cost, CostByDepartment{DepartmentName: c.DepartmentName, Purpose: c.Purpose, Value: c.Value})
		}
	}
	return out, nil
}

// GetIssue answers one issue with its lines.
func (s *Service) GetIssue(ctx context.Context, propertyID, id int64) (Issue, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return Issue{}, err
	}
	return s.issue(ctx, p, propertyID, id)
}

func (s *Service) issue(ctx context.Context, p auth.Principal, propertyID, id int64) (Issue, error) {
	q := s.q(ctx)
	r, err := q.GetIssue(ctx, inventorydb.GetIssueParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Issue{}, orNotFound(err, notFound("ISSUE_NOT_FOUND", "issue"))
	}
	out := Issue{ID: r.ID, Number: r.IssNumber, Purpose: r.Purpose, Origin: r.Origin, Status: r.Status, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName, DepartmentID: r.DepartmentID,
		DepartmentName: r.DepartmentName, ReasonCode: r.ReasonCode, Covers: r.Covers, Beneficiary: r.Beneficiary, BusinessDate: r.BusinessDate, SourceDate: r.SourceDate, Note: r.Note,
		ReverseReason: r.ReverseReason, ReversesNumber: r.ReversesNumber, CreatedByName: r.CreatedByName, CreatedAt: r.CreatedAt, TotalValue: r.TotalValue,
		HeldLines: int(r.HeldLines), Lines: []IssueLine{}}
	if r.ReversedByNumber != "" {
		n := r.ReversedByNumber
		out.ReversedByNumber = &n
	}
	if r.Covers != nil && *r.Covers > 0 && r.TotalValue.IsPositive() {
		per := r.TotalValue.Div(decimal.NewFromInt32(*r.Covers)).Round(2)
		out.CostPerCover = &per
	}
	if day, err := s.days.CurrentBusinessDay(ctx, propertyID); err == nil {
		out.Reversible = r.Status == IssuePosted && r.ReversesIssueID == nil && r.HeldLines == 0 && (r.Origin == OriginAmenity || r.BusinessDate.Equal(day.BusinessDate))
	}
	lines, err := q.ListIssueLines(ctx, inventorydb.ListIssueLinesParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: id})
	if err != nil {
		return Issue{}, err
	}
	for _, l := range lines {
		out.Lines = append(out.Lines, IssueLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, StockUnitCode: l.StockUnitCode, Qty: l.QtyStock, Value: l.Value,
			ExpenseAccountID: l.ExpenseAccountID, ExpenseAccountCode: l.ExpenseAccountCode, ExpenseAccountName: l.ExpenseAccountName, DepartmentID: l.DepartmentID, DepartmentName: l.DepartmentName,
			Status: l.Status, AppliedOn: l.AppliedOn})
	}
	return out, nil
}

// issueAccount gives the expense account of a line by the purpose of the issue and the category of its item (6.1): a use takes the account on the line, else the issue account of the category; a waste the waste
// account of the category, else the key INV_WASTE; a staff meal and an entertainment their keys.
func (s *Service) issueAccount(ctx context.Context, p auth.Principal, propertyID int64, purpose string, l docLineOut, keys map[string]int64) (int64, error) {
	switch purpose {
	case PurposeUse:
		switch {
		case l.in.ExpenseAccountID != nil:
			return *l.in.ExpenseAccountID, nil
		case l.item.IssueAccountID != nil:
			return *l.item.IssueAccountID, nil
		}
		return 0, apperr.Invalid("the issue is invalid", fieldErr("lines", "CATEGORY_ACCOUNT_MISSING", "the category of "+l.item.ItemCode+" has no issue account: name the account of the line"))
	case PurposeWaste:
		if l.item.WasteAccountID != nil {
			return *l.item.WasteAccountID, nil
		}
		return s.cachedKey(ctx, p, propertyID, "INV_WASTE", keys)
	case PurposeStaffMeal:
		return s.cachedKey(ctx, p, propertyID, "INV_STAFF_MEAL", keys)
	default:
		return s.cachedKey(ctx, p, propertyID, "INV_ENTERTAINMENT", keys)
	}
}

func (s *Service) cachedKey(ctx context.Context, p auth.Principal, propertyID int64, key string, cache map[string]int64) (int64, error) {
	if id, ok := cache[key]; ok {
		return id, nil
	}
	id, err := s.keyAccount(ctx, p, propertyID, key)
	if err != nil {
		return 0, err
	}
	cache[key] = id
	return id, nil
}

// checkIssueHeader validates what an issue says about itself by its purpose.
func (s *Service) checkIssueHeader(ctx context.Context, p auth.Principal, propertyID int64, in IssueInput) error {
	var f []apperr.FieldError
	if _, ok := kindOfPurpose[in.Purpose]; !ok {
		f = append(f, fieldErr("purpose", "INVALID_VALUE", "USE, WASTE, STAFF_MEAL or ENTERTAINMENT"))
	}
	if in.Note != nil && len([]rune(trimmed(*in.Note))) > 300 {
		f = append(f, fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	switch in.Purpose {
	case PurposeWaste:
		if in.ReasonCode == nil || !wasteReasons[*in.ReasonCode] {
			f = append(f, fieldErr("reason_code", "REQUIRED", "SPOILAGE, EXPIRED, BREAKAGE, TASTING or OTHER"))
		}
	default:
		if in.ReasonCode != nil {
			f = append(f, fieldErr("reason_code", "INVALID_VALUE", "only a waste has a reason"))
		}
		if in.DepartmentID == nil {
			f = append(f, fieldErr("department_id", "REQUIRED", "the department that bears the cost"))
		}
	}
	switch in.Purpose {
	case PurposeStaffMeal, PurposeEntertainment:
		if in.Covers == nil || *in.Covers < 1 || *in.Covers > 100000 {
			f = append(f, fieldErr("covers", "REQUIRED", "how many meals or guests"))
		}
	default:
		if in.Covers != nil {
			f = append(f, fieldErr("covers", "INVALID_VALUE", "only a staff meal or an entertainment has covers"))
		}
	}
	if in.Purpose == PurposeEntertainment {
		if in.Beneficiary == nil || len([]rune(trimmed(*in.Beneficiary))) < 1 || len([]rune(trimmed(*in.Beneficiary))) > 100 {
			f = append(f, fieldErr("beneficiary", "REQUIRED", "the host or the guest or event, 100 characters at most"))
		}
	} else if in.Beneficiary != nil {
		f = append(f, fieldErr("beneficiary", "INVALID_VALUE", "only an entertainment has a beneficiary"))
	}
	if len(f) > 0 {
		return invalid("issue", f...)
	}
	return s.checkDepartmentExists(ctx, p, propertyID, in.DepartmentID, "department_id")
}

// PostIssue takes goods out of a warehouse (inventory.move): to a department, as waste, as a staff meal or as entertainment. The stock may not go below zero (409 NEGATIVE_STOCK_NOT_ALLOWED), a frozen
// warehouse refuses (409 WAREHOUSE_FROZEN), and the department rule of every account is applied here (422 DEPARTMENT_REQUIRED). A repeated client_uuid answers the first issue.
func (s *Service) PostIssue(ctx context.Context, propertyID int64, in IssueInput) (Issue, error) {
	p, err := s.canMove(ctx, propertyID)
	if err != nil {
		return Issue{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Issue{}, invalid("issue", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	if err := s.checkIssueHeader(ctx, p, propertyID, in); err != nil {
		return Issue{}, err
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		if prev, err := q.GetIssueByClientUUID(ctx, inventorydb.GetIssueByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		wd, err := q.WarehouseDepartment(ctx, inventorydb.WarehouseDepartmentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.WarehouseID})
		if err != nil {
			return orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
		}
		docLines := make([]docLine, 0, len(in.Lines))
		for _, l := range in.Lines {
			docLines = append(docLines, docLine{ItemID: l.ItemID, UnitID: l.UnitID, Qty: l.Qty, ExpenseAccountID: l.ExpenseAccountID})
		}
		lines, err := s.checkDocLines(ctx, p, propertyID, "issue", docLines)
		if err != nil {
			return err
		}
		// the department of the document: named, or for a waste the warehouse's own (derived: an account that takes none drops it)
		dept, derived := in.DepartmentID, false
		if dept == nil && wd.DepartmentID != 0 {
			d := wd.DepartmentID
			dept, derived = &d, true
		}
		keys := map[string]int64{}
		type plan struct {
			account int64
			dept    *int64
		}
		plans := make([]plan, 0, len(lines))
		for i, l := range lines {
			acc, err := s.issueAccount(ctx, p, propertyID, in.Purpose, l, keys)
			if err != nil {
				return err
			}
			d, err := s.resolveDepartment(ctx, p, propertyID, acc, dept, derived, lineAt(i, "expense_account_id"))
			if err != nil {
				return err
			}
			plans = append(plans, plan{acc, d})
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, len(lines))
		for _, l := range lines {
			pairs = append(pairs, Pair{in.WarehouseID, l.item.ItemID})
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvISS)
		if err != nil {
			return err
		}
		var outletID *int64 = wd.OutletID
		id, err = q.InsertIssue(ctx, inventorydb.InsertIssueParams{TenantID: p.TenantID, PropertyID: propertyID, IssNumber: number, ClientUuid: key, WarehouseID: in.WarehouseID, Purpose: in.Purpose,
			Origin: OriginManual, DepartmentID: dept, OutletID: outletID, ReasonCode: in.ReasonCode, Covers: coversOf(in.Covers), Beneficiary: optional(in.Beneficiary), BusinessDate: bd,
			Note: optional(in.Note), CreatedAt: s.clock.Now(), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		total := decimal.Zero
		now := s.clock.Now()
		for i, l := range lines {
			posted, err := stock.Out(ctx, OutArgs{Pair: Pair{in.WarehouseID, l.item.ItemID}, Kind: kindOfPurpose[in.Purpose], Qty: l.stockQty, Ref: Ref{Type: "INV_ISSUE", ID: id, Line: i + 1},
				Counter: &Counter{AccountID: plans[i].account, DepartmentID: plans[i].dept}})
			if err != nil {
				return err
			}
			value := posted.Value.Neg()
			mid := posted.MovementID
			if err := q.InsertIssueLine(ctx, inventorydb.InsertIssueLineParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: id, LineNo: int32(i + 1), ItemID: l.item.ItemID, //nolint:gosec // G115: at most 200
				QtyStock: l.stockQty, Value: &value, ExpenseAccountID: &plans[i].account, DepartmentID: plans[i].dept, MovementID: &mid, Status: LineApplied, AppliedOn: &bd, AppliedAt: &now}); err != nil {
				return err
			}
			total = total.Add(value)
		}
		action := map[string]string{PurposeUse: "inventory.issue_posted", PurposeWaste: "inventory.waste_posted", PurposeStaffMeal: "inventory.staff_meal_posted", PurposeEntertainment: "inventory.entertainment_posted"}[in.Purpose]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, action, "inv_issue", id, number, nil,
			map[string]any{"warehouse_id": in.WarehouseID, "purpose": in.Purpose, "department_id": dept, "lines": len(lines), "value": total, "covers": in.Covers}))
	})
	if err != nil {
		return Issue{}, err
	}
	return s.issue(ctx, p, propertyID, id)
}

func coversOf(c *int) *int32 {
	if c == nil {
		return nil
	}
	v := int32(*c) //nolint:gosec // G115: checked at most 100000
	return &v
}

// ReverseIssue undoes an issue, a waste, a staff meal or an entertainment on the day it was posted (inventory.move): a reversing document puts the goods back at the cost they were taken, and the journal of the
// day follows. A later day is a new issue the other way. The amenities of a close are reversed by ReverseAmenityRun.
func (s *Service) ReverseIssue(ctx context.Context, propertyID, id int64, reason string) (Issue, error) {
	p, err := s.canMove(ctx, propertyID)
	if err != nil {
		return Issue{}, err
	}
	reason = trimmed(reason)
	if n := len([]rune(reason)); n < 1 || n > 300 {
		return Issue{}, invalid("reversal", fieldErr("reason", "REQUIRED", "say why, 300 characters at most"))
	}
	var rid int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.q(ctx).GetIssue(ctx, inventorydb.GetIssueParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("ISSUE_NOT_FOUND", "issue"))
		}
		if cur.Origin == OriginAmenity {
			return apperr.Conflict("ISSUE_NOT_REVERSIBLE", "the amenities of a close are reversed from their own page (the reversing document)").WithContext("origin", cur.Origin)
		}
		rid, err = s.reverseIssueDoc(ctx, p, propertyID, bd, id, reason, false)
		return err
	})
	if err != nil {
		return Issue{}, err
	}
	return s.issue(ctx, p, propertyID, rid)
}

// reverseIssueDoc writes the reversing document of an issue inside the transaction of the caller (which holds the business day): it locks the issue, then the warehouse and the balances, reverses every line
// (the goods come back at the cost they were taken, layers restored under FIFO) and marks the issue reversed. anyDay allows a reversal after the day of the issue (the amenities of a close).
func (s *Service) reverseIssueDoc(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, id int64, reason string, anyDay bool) (int64, error) {
	q := s.q(ctx)
	if err := db.LockRows(ctx, db.StockIssues, db.ForUpdate, propertyID, []int64{id}); err != nil {
		return 0, orNotFound(err, notFound("ISSUE_NOT_FOUND", "issue"))
	}
	cur, err := q.GetIssue(ctx, inventorydb.GetIssueParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return 0, orNotFound(err, notFound("ISSUE_NOT_FOUND", "issue"))
	}
	switch {
	case cur.Status != IssuePosted || cur.ReversesIssueID != nil:
		return 0, errDocState("ISSUE", cur.Status, "posted and not itself a reversal")
	case cur.HeldLines > 0:
		return 0, apperr.Conflict("AMENITY_RUN_HELD", "lines of the document are held by a count: they are applied when the count ends, and then the document can be reversed")
	case !anyDay && !cur.BusinessDate.Equal(bd):
		return 0, apperr.Conflict("ISSUE_NOT_REVERSIBLE", "an issue is reversed on the day it was posted: make an issue the other way").WithContext("business_date", cur.BusinessDate.String())
	}
	lines, err := q.ListAppliedIssueLinesForReverse(ctx, inventorydb.ListAppliedIssueLinesForReverseParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: id})
	if err != nil {
		return 0, err
	}
	book, err := s.book(ctx, p, propertyID, bd)
	if err != nil {
		return 0, err
	}
	pairs := make([]Pair, 0, len(lines))
	for _, l := range lines {
		pairs = append(pairs, Pair{cur.WarehouseID, l.ItemID})
	}
	stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
	if err != nil {
		return 0, err
	}
	number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvISS)
	if err != nil {
		return 0, err
	}
	origLines, err := q.ListIssueLines(ctx, inventorydb.ListIssueLinesParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: id})
	if err != nil {
		return 0, err
	}
	var key pgtype.UUID
	if err := key.Scan(newUUID()); err != nil {
		return 0, err
	}
	rid, err := q.InsertIssue(ctx, inventorydb.InsertIssueParams{TenantID: p.TenantID, PropertyID: propertyID, IssNumber: number, ClientUuid: key, WarehouseID: cur.WarehouseID, Purpose: cur.Purpose,
		Origin: OriginManual, DepartmentID: cur.DepartmentID, OutletID: cur.OutletID, ReasonCode: cur.ReasonCode, Covers: cur.Covers, Beneficiary: cur.Beneficiary, ReversesIssueID: &id, BusinessDate: bd,
		Note: nil, ReverseReason: &reason, CreatedAt: s.clock.Now(), ActorID: p.ActorID()})
	if err != nil {
		return 0, err
	}
	now := s.clock.Now()
	total := decimal.Zero
	for i, l := range lines {
		posted, err := stock.Reverse(ctx, ReverseArgs{MovementID: *movementOf(origLines, l.LineNo), Kind: KindIssueReversal, Ref: Ref{Type: "INV_ISSUE", ID: rid, Line: i + 1}})
		if err != nil {
			return 0, err
		}
		value := posted.Value
		mid := posted.MovementID
		var acc, dept *int64
		for _, ol := range origLines {
			if ol.LineNo == l.LineNo {
				acc, dept = ol.ExpenseAccountID, ol.DepartmentID
			}
		}
		if err := q.InsertIssueLine(ctx, inventorydb.InsertIssueLineParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: rid, LineNo: int32(i + 1), ItemID: l.ItemID, //nolint:gosec // G115: at most 200
			QtyStock: l.QtyStock, Value: &value, ExpenseAccountID: acc, DepartmentID: dept, MovementID: &mid, Status: LineApplied, AppliedOn: &bd, AppliedAt: &now}); err != nil {
			return 0, err
		}
		total = total.Add(value)
	}
	if err := q.SetIssueReversed(ctx, inventorydb.SetIssueReversedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Reason: &reason}); err != nil {
		return 0, err
	}
	if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.issue_reversed", "inv_issue", id, cur.IssNumber, map[string]any{"status": IssuePosted},
		map[string]any{"status": IssueReversed, "reason": reason, "reversing": number, "value": total})); err != nil {
		return 0, err
	}
	return rid, nil
}

func movementOf(lines []inventorydb.ListIssueLinesRow, lineNo int32) *int64 {
	for _, l := range lines {
		if l.LineNo == lineNo {
			return l.MovementID
		}
	}
	return nil
}
