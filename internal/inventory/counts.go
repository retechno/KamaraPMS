package inventory

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The stock count and the freeze (35-inventory.md 4.7, 5.5, I9, D23). Starting a count freezes the warehouse for every manual movement and takes the book quantity of each item in scope; the counters type what
// they find, and the server stamps counted_at on each line when a quantity is entered or changed; the count is submitted and approved by someone else (the kind INV_COUNT, a decision of the signed-in
// approver); approving applies the held rows that fall before each item's counted_at, adjusts each item to the counted quantity (a gain at the last cost, a loss by the method), ends the freeze and applies the
// rest of the held rows. Cancelling ends the freeze and applies all the held rows. The held rows are the lines of an amenities document of a close inside the count (held.go); the POS queue joins in PR 4.

// States of a count.
const (
	CountOpen      = "OPEN"
	CountSubmitted = "SUBMITTED"
	CountApproved  = "APPROVED"
	CountCancelled = "CANCELLED"
)

// Scopes of a count.
const (
	ScopeAll      = "ALL"
	ScopeCategory = "CATEGORY"
	ScopeList     = "LIST"
)

// CountInput starts a count of a warehouse.
type CountInput struct {
	ClientUUID  string  `json:"client_uuid"`
	WarehouseID int64   `json:"warehouse_id"`
	Scope       string  `json:"scope"`
	CategoryID  *int64  `json:"category_id"`
	ItemIDs     []int64 `json:"item_ids"`
	Note        *string `json:"note"`
}

// CountLineInput is the quantity a counter found for one item, in one of its units (the stock unit when none is named). A nil quantity clears the line.
type CountLineInput struct {
	ItemID int64            `json:"item_id"`
	UnitID *int64           `json:"unit_id"`
	Qty    *decimal.Decimal `json:"qty"`
	Note   *string          `json:"note"`
}

// CountLine is a line of a count. The book quantity is hidden on a blind sheet while the count is open; the expected quantity, the held quantity and the variance are worked out at approval, and before it
// as a preview (the same arithmetic) for the people who review the count.
type CountLine struct {
	ItemID        int64            `json:"item_id"`
	ItemCode      string           `json:"item_code"`
	ItemName      string           `json:"item_name"`
	CategoryName  string           `json:"category_name"`
	StockUnitCode string           `json:"stock_unit_code"`
	BookQty       *decimal.Decimal `json:"book_qty"`
	CountedQty    *decimal.Decimal `json:"counted_qty"`
	CountedAt     *time.Time       `json:"counted_at"`
	EnteredUnitID *int64           `json:"entered_unit_id"`
	EnteredQty    *decimal.Decimal `json:"entered_qty"`
	ExpectedQty   *decimal.Decimal `json:"expected_qty"`
	HeldBeforeQty *decimal.Decimal `json:"held_before_qty"`
	VarianceQty   *decimal.Decimal `json:"variance_qty"`
	VarianceValue *decimal.Decimal `json:"variance_value"`
	Note          *string          `json:"note"`
}

// Count is a stock count with its sheet.
type Count struct {
	ID                  int64            `json:"id"`
	Number              string           `json:"cnt_number"`
	Status              string           `json:"status"`
	WarehouseID         int64            `json:"warehouse_id"`
	WarehouseName       string           `json:"warehouse_name"`
	Scope               string           `json:"scope"`
	CategoryName        *string          `json:"category_name"`
	StartedAt           time.Time        `json:"started_at"`
	StartedByName       *string          `json:"started_by_name"`
	BusinessDateStarted civil.Date       `json:"business_date_started"`
	SubmittedAt         *time.Time       `json:"submitted_at"`
	SubmittedByName     *string          `json:"submitted_by_name"`
	DecidedAt           *time.Time       `json:"decided_at"`
	DecidedByName       *string          `json:"decided_by_name"`
	TotalVarianceValue  *decimal.Decimal `json:"total_variance_value"`
	Note                *string          `json:"note"`
	Version             int32            `json:"version"`
	Blind               bool             `json:"blind"`
	Preview             bool             `json:"preview"`
	HeldDocuments       []HeldDocument   `json:"held_documents"`
	Lines               []CountLine      `json:"lines"`
}

// HeldDocument is an amenities document whose lines wait for the end of the count.
type HeldDocument struct {
	IssueID    int64       `json:"issue_id"`
	Number     string      `json:"iss_number"`
	SourceDate *civil.Date `json:"source_date"`
	HeldLines  int         `json:"held_lines"`
}

// CountBrief is a row of the list of counts.
type CountBrief struct {
	ID                 int64            `json:"id"`
	Number             string           `json:"cnt_number"`
	Status             string           `json:"status"`
	WarehouseID        int64            `json:"warehouse_id"`
	WarehouseName      string           `json:"warehouse_name"`
	Scope              string           `json:"scope"`
	StartedAt          time.Time        `json:"started_at"`
	StartedByName      *string          `json:"started_by_name"`
	SubmittedAt        *time.Time       `json:"submitted_at"`
	DecidedAt          *time.Time       `json:"decided_at"`
	TotalVarianceValue *decimal.Decimal `json:"total_variance_value"`
	LineCount          int              `json:"line_count"`
	CountedCount       int              `json:"counted_count"`
}

// CountFilter narrows the list of counts.
type CountFilter struct {
	Status      string
	WarehouseID int64
	Limit       int
}

// ListCounts lists the counts, newest first.
func (s *Service) ListCounts(ctx context.Context, propertyID int64, f CountFilter) ([]CountBrief, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	arg := inventorydb.ListCountsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: optional(&f.Status), RowLimit: limitOf(f.Limit)}
	if f.WarehouseID > 0 {
		arg.WarehouseID = &f.WarehouseID
	}
	rows, err := s.q(ctx).ListCounts(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]CountBrief, 0, len(rows))
	for _, r := range rows {
		out = append(out, CountBrief{ID: r.ID, Number: r.CntNumber, Status: r.Status, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName, Scope: r.Scope, StartedAt: r.StartedAt,
			StartedByName: r.StartedByName, SubmittedAt: r.SubmittedAt, DecidedAt: r.DecidedAt, TotalVarianceValue: r.TotalVarianceValue, LineCount: int(r.LineCount), CountedCount: int(r.CountedCount)})
	}
	return out, nil
}

// GetCount answers one count with its sheet.
func (s *Service) GetCount(ctx context.Context, propertyID, id int64) (Count, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return Count{}, err
	}
	return s.count(ctx, p, propertyID, id)
}

func (s *Service) count(ctx context.Context, p auth.Principal, propertyID, id int64) (Count, error) {
	q := s.q(ctx)
	r, err := q.GetCount(ctx, inventorydb.GetCountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Count{}, orNotFound(err, notFound("COUNT_NOT_FOUND", "count"))
	}
	cfg, err := s.settingsOrDefault(ctx, p, propertyID)
	if err != nil {
		return Count{}, err
	}
	out := Count{ID: r.ID, Number: r.CntNumber, Status: r.Status, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName, Scope: r.Scope, CategoryName: r.CategoryName, StartedAt: r.StartedAt,
		StartedByName: r.StartedByName, BusinessDateStarted: r.BusinessDateStarted, SubmittedAt: r.SubmittedAt, SubmittedByName: r.SubmittedByName, DecidedAt: r.DecidedAt, DecidedByName: r.DecidedByName,
		TotalVarianceValue: r.TotalVarianceValue, Note: r.Note, Version: r.Version, Blind: cfg.CountBlind, HeldDocuments: []HeldDocument{}, Lines: []CountLine{}}
	rows, err := q.ListCountLines(ctx, inventorydb.ListCountLinesParams{TenantID: p.TenantID, PropertyID: propertyID, CountID: id})
	if err != nil {
		return Count{}, err
	}
	// the preview of the arithmetic of approval, for a count that has been submitted and has not been decided
	var held []heldRow
	costs := map[int64]inventorydb.ItemAverageCostsRow{}
	if r.Status == CountSubmitted {
		out.Preview = true
		if held, err = s.heldRows(ctx, p, propertyID, r.WarehouseID); err != nil {
			return Count{}, err
		}
		ids := make([]int64, 0, len(rows))
		for _, l := range rows {
			ids = append(ids, l.ItemID)
		}
		cs, err := q.ItemAverageCosts(ctx, inventorydb.ItemAverageCostsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: r.WarehouseID, ItemIds: ids})
		if err != nil {
			return Count{}, err
		}
		for _, c := range cs {
			costs[c.ItemID] = c
		}
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Count{}, err
	}
	for _, l := range rows {
		cl := CountLine{ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, CategoryName: l.CategoryName, StockUnitCode: l.StockUnitCode, CountedQty: l.CountedQty, CountedAt: l.CountedAt,
			EnteredUnitID: l.EnteredUnitID, EnteredQty: l.EnteredQty, ExpectedQty: l.ExpectedQty, HeldBeforeQty: l.HeldBeforeQty, VarianceQty: l.VarianceQty, VarianceValue: l.VarianceValue, Note: l.Note}
		book := l.BookQty
		if !cfg.CountBlind || r.Status != CountOpen {
			cl.BookQty = &book
		}
		if out.Preview && l.CountedQty != nil && l.CountedAt != nil {
			before := heldBefore(held, l.ItemID, *l.CountedAt)
			expected := l.BookQty.Sub(before)
			variance := l.CountedQty.Sub(expected)
			c := costs[l.ItemID]
			cost := decimal.Zero
			switch {
			case variance.IsNegative() && c.Qty.IsPositive():
				cost = c.Value.Div(c.Qty)
			case c.LastUnitCost != nil:
				cost = *c.LastUnitCost
			case c.ReferenceCost != nil:
				cost = *c.ReferenceCost
			}
			value := variance.Mul(cost).Round(prop.CurrencyDecimals)
			neg := before.Neg()
			cl.ExpectedQty, cl.HeldBeforeQty, cl.VarianceQty, cl.VarianceValue = &expected, &neg, &variance, &value
		}
		out.Lines = append(out.Lines, cl)
	}
	if r.Status == CountOpen || r.Status == CountSubmitted {
		docs, err := q.ListHeldAmenityDocs(ctx, inventorydb.ListHeldAmenityDocsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: r.WarehouseID})
		if err != nil {
			return Count{}, err
		}
		for _, d := range docs {
			out.HeldDocuments = append(out.HeldDocuments, HeldDocument{IssueID: d.ID, Number: d.IssNumber, SourceDate: d.SourceDate, HeldLines: int(d.HeldLines)})
		}
	}
	return out, nil
}

// StartCount starts a count of a warehouse and freezes it for the manual movements (inventory.count). The book quantity of each item in scope is taken under the locks of the balances. A warehouse that is
// counted already, or frozen, answers 409 COUNT_IN_PROGRESS. A repeated client_uuid answers the first count.
func (s *Service) StartCount(ctx context.Context, propertyID int64, in CountInput) (Count, error) {
	p, err := s.canCount(ctx, propertyID)
	if err != nil {
		return Count{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Count{}, invalid("count", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	var f []apperr.FieldError
	switch in.Scope {
	case ScopeAll:
		if in.CategoryID != nil || len(in.ItemIDs) > 0 {
			f = append(f, fieldErr("scope", "INVALID_VALUE", "the whole warehouse names no category and no items"))
		}
	case ScopeCategory:
		if in.CategoryID == nil {
			f = append(f, fieldErr("category_id", "REQUIRED", "the category to count"))
		}
	case ScopeList:
		if len(in.ItemIDs) == 0 || len(in.ItemIDs) > maxDocLines*5 {
			f = append(f, fieldErr("item_ids", "OUT_OF_RANGE", "the items to count"))
		}
	default:
		f = append(f, fieldErr("scope", "INVALID_VALUE", "ALL, CATEGORY or LIST"))
	}
	if in.Note != nil && len([]rune(trimmed(*in.Note))) > 300 {
		f = append(f, fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	if len(f) > 0 {
		return Count{}, invalid("count", f...)
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		if prev, err := q.GetCountByClientUUID(ctx, inventorydb.GetCountByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if in.CategoryID != nil {
			if _, err := q.GetCategory(ctx, inventorydb.GetCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.CategoryID}); err != nil {
				return invalid("count", fieldErr("category_id", "NOT_FOUND", "no such category in this property"))
			}
		}
		// the warehouse FOR UPDATE: the mutations that are in flight hold it FOR SHARE and finish first (5.5 item 1)
		if err := db.LockRows(ctx, db.Warehouses, db.ForUpdate, propertyID, []int64{in.WarehouseID}); err != nil {
			return orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
		}
		w, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.WarehouseID})
		if err != nil {
			return orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
		}
		if !w.IsActive {
			return invalid("count", fieldErr("warehouse_id", "INACTIVE", "the warehouse is switched off"))
		}
		if w.CountID != nil {
			return apperr.Conflict("COUNT_IN_PROGRESS", "a count is running in this warehouse").WithContext("count_id", *w.CountID)
		}
		arg := inventorydb.ListCountScopeParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: in.WarehouseID, CategoryID: in.CategoryID}
		if in.Scope == ScopeList {
			arg.ItemIds = in.ItemIDs
		}
		scope, err := q.ListCountScope(ctx, arg)
		if err != nil {
			return err
		}
		if len(scope) == 0 {
			return invalid("count", fieldErr("scope", "EMPTY", "no item in this warehouse falls in the scope"))
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, len(scope))
		for _, it := range scope {
			pairs = append(pairs, Pair{in.WarehouseID, it.ItemID})
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{}) // the book quantity is read under the balance locks
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvCNT)
		if err != nil {
			return err
		}
		id, err = q.InsertCount(ctx, inventorydb.InsertCountParams{TenantID: p.TenantID, PropertyID: propertyID, CntNumber: number, ClientUuid: key, WarehouseID: in.WarehouseID, Scope: in.Scope,
			CategoryID: in.CategoryID, StartedAt: s.clock.Now(), StartedBy: p.UserID, BusinessDateStarted: bd, Note: optional(in.Note)})
		if err != nil {
			return err
		}
		for _, it := range scope {
			qty, _, _ := stock.Balance(Pair{in.WarehouseID, it.ItemID})
			if err := q.InsertCountLine(ctx, inventorydb.InsertCountLineParams{TenantID: p.TenantID, PropertyID: propertyID, CountID: id, ItemID: it.ItemID, BookQty: qty}); err != nil {
				return err
			}
		}
		if err := q.SetWarehouseCount(ctx, inventorydb.SetWarehouseCountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.WarehouseID, CountID: &id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.count_started", "inv_count", id, number, nil, map[string]any{"warehouse_id": in.WarehouseID, "scope": in.Scope, "items": len(scope)}))
	})
	if err != nil {
		return Count{}, err
	}
	return s.count(ctx, p, propertyID, id)
}

func (s *Service) lockCount(ctx context.Context, p auth.Principal, propertyID, id int64) (inventorydb.GetCountRow, error) {
	if err := db.LockRows(ctx, db.StockCounts, db.ForUpdate, propertyID, []int64{id}); err != nil {
		return inventorydb.GetCountRow{}, orNotFound(err, notFound("COUNT_NOT_FOUND", "count"))
	}
	cur, err := s.q(ctx).GetCount(ctx, inventorydb.GetCountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return cur, orNotFound(err, notFound("COUNT_NOT_FOUND", "count"))
	}
	return cur, nil
}

func errCountState(status, want string) *apperr.Error {
	return apperr.Conflict("COUNT_STATE_INVALID", "the count is "+status+": this is for a count that is "+want).WithContext("status", status)
}

// EnterCountLines records what the counters found (inventory.count): each quantity gets counted_at from the clock of the server, the moment it is entered or changed (never the client's clock); a quantity that
// is entered again unchanged keeps its time. The count must be open.
func (s *Service) EnterCountLines(ctx context.Context, propertyID, id int64, lines []CountLineInput) (Count, error) {
	p, err := s.canCount(ctx, propertyID)
	if err != nil {
		return Count{}, err
	}
	if len(lines) == 0 || len(lines) > maxDocLines*5 {
		return Count{}, invalid("count", fieldErr("lines", "OUT_OF_RANGE", "the quantities found"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.lockCount(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Status != CountOpen {
			return errCountState(cur.Status, "open")
		}
		q := s.q(ctx)
		existing, err := q.ListCountLines(ctx, inventorydb.ListCountLinesParams{TenantID: p.TenantID, PropertyID: propertyID, CountID: id})
		if err != nil {
			return err
		}
		byItem := map[int64]inventorydb.ListCountLinesRow{}
		for _, l := range existing {
			byItem[l.ItemID] = l
		}
		ids := make([]int64, 0, len(lines))
		for _, l := range lines {
			ids = append(ids, l.ItemID)
		}
		units := map[[2]int64]inventorydb.ListItemUnitsOfRow{}
		us, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
		if err != nil {
			return err
		}
		for _, u := range us {
			units[[2]int64{u.ItemID, u.UnitID}] = u
		}
		now := s.clock.Now()
		var f []apperr.FieldError
		changed := 0
		for i, l := range lines {
			at := lineAt(i, "")
			ex, ok := byItem[l.ItemID]
			if !ok {
				f = append(f, fieldErr(at+"item_id", "NOT_FOUND", "the item is not on the sheet of this count"))
				continue
			}
			if l.Note != nil && len([]rune(trimmed(*l.Note))) > 200 {
				f = append(f, fieldErr(at+"note", "INVALID_VALUE", "200 characters at most"))
				continue
			}
			if l.Qty == nil { // cleared
				if ex.CountedQty != nil {
					changed++
				}
				if err := q.SetCountLineCounted(ctx, inventorydb.SetCountLineCountedParams{PropertyID: propertyID, CountID: id, ItemID: l.ItemID, Note: optional(l.Note)}); err != nil {
					return err
				}
				continue
			}
			if l.Qty.IsNegative() || l.Qty.GreaterThan(decimal.NewFromInt(1_000_000_000)) || !l.Qty.Equal(Round6(*l.Qty)) {
				f = append(f, fieldErr(at+"qty", "INVALID_VALUE", "zero or more, six decimals at most"))
				continue
			}
			stockQty := Round6(*l.Qty)
			var enteredUnit *int64
			var enteredQty *decimal.Decimal
			if l.UnitID != nil {
				u, ok := units[[2]int64{l.ItemID, *l.UnitID}]
				if !ok {
					f = append(f, fieldErr(at+"unit_id", "NOT_FOUND", "not a unit of the item"))
					continue
				}
				stockQty = StockQty(*l.Qty, u.StockNum, u.StockDen)
				enteredUnit, enteredQty = l.UnitID, l.Qty
			}
			counted, stamp := &stockQty, &now
			if ex.CountedQty != nil && ex.CountedQty.Equal(stockQty) && ex.CountedAt != nil {
				stamp = ex.CountedAt // entered again unchanged: the time of the first look stays
			} else {
				changed++
			}
			if err := q.SetCountLineCounted(ctx, inventorydb.SetCountLineCountedParams{PropertyID: propertyID, CountID: id, ItemID: l.ItemID, CountedQty: counted, CountedAt: stamp,
				EnteredUnitID: enteredUnit, EnteredQty: enteredQty, CountedBy: &p.UserID, Note: optional(l.Note)}); err != nil {
				return err
			}
		}
		if len(f) > 0 {
			return invalid("count", f...)
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.count_entered", "inv_count", id, cur.CntNumber, nil, map[string]any{"lines": len(lines), "changed": changed}))
	})
	if err != nil {
		return Count{}, err
	}
	return s.count(ctx, p, propertyID, id)
}

// SubmitCount locks the sheet and sends it for approval (inventory.count). Items that were not counted stay as they are.
func (s *Service) SubmitCount(ctx context.Context, propertyID, id int64) (Count, error) {
	p, err := s.canCount(ctx, propertyID)
	if err != nil {
		return Count{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.lockCount(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Status != CountOpen {
			return errCountState(cur.Status, "open")
		}
		lines, err := s.q(ctx).ListCountLines(ctx, inventorydb.ListCountLinesParams{TenantID: p.TenantID, PropertyID: propertyID, CountID: id})
		if err != nil {
			return err
		}
		counted := 0
		for _, l := range lines {
			if l.CountedQty != nil {
				counted++
			}
		}
		if counted == 0 {
			return invalid("count", fieldErr("lines", "REQUIRED", "count at least one item before it is submitted"))
		}
		now := s.clock.Now()
		if err := s.q(ctx).SetCountState(ctx, inventorydb.SetCountStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: CountSubmitted, SubmittedAt: &now, SubmittedBy: &p.UserID}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.count_submitted", "inv_count", id, cur.CntNumber, map[string]any{"status": CountOpen}, map[string]any{"status": CountSubmitted, "counted": counted}))
	})
	if err != nil {
		return Count{}, err
	}
	return s.count(ctx, p, propertyID, id)
}

// CancelCount ends the freeze and posts nothing (inventory.count): the held rows are applied, all of them, dated the open day.
func (s *Service) CancelCount(ctx context.Context, propertyID, id int64) (Count, error) {
	p, err := s.canCount(ctx, propertyID)
	if err != nil {
		return Count{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.lockCount(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Status != CountOpen && cur.Status != CountSubmitted {
			return errCountState(cur.Status, "open or submitted")
		}
		if err := s.endFreeze(ctx, p, propertyID, bd, cur, nil, nil); err != nil {
			return err
		}
		now := s.clock.Now()
		if err := s.q(ctx).SetCountState(ctx, inventorydb.SetCountStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: CountCancelled, SubmittedAt: cur.SubmittedAt, SubmittedBy: cur.SubmittedBy,
			DecidedBy: &p.UserID, DecidedAt: &now}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.count_cancelled", "inv_count", id, cur.CntNumber, map[string]any{"status": cur.Status}, map[string]any{"status": CountCancelled}))
	})
	if err != nil {
		return Count{}, err
	}
	return s.count(ctx, p, propertyID, id)
}

// endFreeze does what ends a count: it locks the documents that hold rows, the warehouse and the balances of the pairs it will touch, applies the held rows that fall before each counted item's counted_at, calls
// adjust (the count's own movements; nil for a cancel), ends the freeze and applies the rest of the held rows. The count row is already locked by the caller.
func (s *Service) endFreeze(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, cur inventorydb.GetCountRow, counted map[int64]countedAt, adjust func(stock *Stock) error) error {
	q := s.q(ctx)
	held, err := s.heldRows(ctx, p, propertyID, cur.WarehouseID)
	if err != nil {
		return err
	}
	if err := db.LockRows(ctx, db.StockIssues, db.ForUpdate, propertyID, issueIDsOf(held)); err != nil { // the documents, then the warehouse and the balances (5.1)
		return err
	}
	if err := db.LockRows(ctx, db.Warehouses, db.ForUpdate, propertyID, []int64{cur.WarehouseID}); err != nil {
		return err
	}
	book, err := s.book(ctx, p, propertyID, bd)
	if err != nil {
		return err
	}
	pairSet := map[Pair]bool{}
	for item := range counted {
		pairSet[Pair{cur.WarehouseID, item}] = true
	}
	for _, h := range held {
		pairSet[Pair{cur.WarehouseID, h.ItemID}] = true
	}
	pairs := make([]Pair, 0, len(pairSet))
	for pr := range pairSet {
		pairs = append(pairs, pr)
	}
	stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{AllowFrozen: true})
	if err != nil {
		return err
	}
	var before, after []heldRow
	for _, h := range held {
		if c, ok := counted[h.ItemID]; ok && h.CreatedAt.Before(c.At) {
			before = append(before, h)
		} else {
			after = append(after, h)
		}
	}
	if err := s.applyHeld(ctx, p, propertyID, bd, stock, before); err != nil {
		return err
	}
	if adjust != nil {
		if err := adjust(stock); err != nil {
			return err
		}
	}
	if err := q.SetWarehouseCount(ctx, inventorydb.SetWarehouseCountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: cur.WarehouseID}); err != nil {
		return err
	}
	return s.applyHeld(ctx, p, propertyID, bd, stock, after)
}

// countedAt is when an item was counted.
type countedAt struct{ At time.Time }

// ApproveCount approves a submitted count (inventory.approve, the kind INV_COUNT: a decision of the signed-in approver, the rule of the property compares them with the person who submitted). In one transaction
// (count -> the documents that hold rows -> warehouse FOR UPDATE -> balances) it applies the held rows before each item's counted_at, adjusts each counted item to the quantity found (4.7: variance = counted
// minus what was expected), ends the freeze and applies the rest of the held rows. A shortfall of an item is closed by its count (4.4).
func (s *Service) ApproveCount(ctx context.Context, propertyID, id int64) (Count, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Count{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermInventoryApprove); err != nil {
		return Count{}, err
	}
	if s.approver == nil {
		return Count{}, apperr.Internal(errors.New("inventory: the approver is not wired"))
	}
	prev, err := s.count(ctx, p, propertyID, id)
	if err != nil {
		return Count{}, err
	}
	if prev.Status == CountApproved {
		return prev, nil // a replay
	}
	if prev.Status != CountSubmitted {
		return Count{}, errCountState(prev.Status, "submitted")
	}
	submitter := int64(0)
	if r, err := s.q(ctx).GetCount(ctx, inventorydb.GetCountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err == nil && r.SubmittedBy != nil {
		submitter = *r.SubmittedBy
	}
	amount := decimal.Zero
	for _, l := range prev.Lines {
		if l.VarianceValue != nil {
			amount = amount.Add(*l.VarianceValue)
		}
	}
	approval, err := s.approver.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Kind: iam.KindInvCount, Amount: amount.Abs(), Decision: true, Subject: submitter})
	if err != nil {
		return Count{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.lockCount(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Status == CountApproved {
			return nil
		}
		if cur.Status != CountSubmitted {
			return errCountState(cur.Status, "submitted")
		}
		q := s.q(ctx)
		lines, err := q.ListCountLines(ctx, inventorydb.ListCountLinesParams{TenantID: p.TenantID, PropertyID: propertyID, CountID: id})
		if err != nil {
			return err
		}
		counted := map[int64]countedAt{}
		for _, l := range lines {
			if l.CountedQty != nil && l.CountedAt != nil {
				counted[l.ItemID] = countedAt{At: *l.CountedAt}
			}
		}
		accounts, err := s.q(ctx).ItemCategoryAccounts(ctx, inventorydb.ItemCategoryAccountsParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: keysOf(counted)})
		if err != nil {
			return err
		}
		catOf := map[int64]inventorydb.ItemCategoryAccountsRow{}
		for _, a := range accounts {
			catOf[a.ItemID] = a
		}
		wd, err := q.WarehouseDepartment(ctx, inventorydb.WarehouseDepartmentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: cur.WarehouseID})
		if err != nil {
			return err
		}
		held, err := s.heldRows(ctx, p, propertyID, cur.WarehouseID)
		if err != nil {
			return err
		}
		keyCache := map[string]int64{}
		total := decimal.Zero
		adjust := func(stock *Stock) error {
			for i, l := range lines {
				if l.CountedQty == nil || l.CountedAt == nil {
					continue
				}
				pair := Pair{cur.WarehouseID, l.ItemID}
				before := heldBefore(held, l.ItemID, *l.CountedAt)
				expected := stock.ExpectedQty(pair) // the balance after the held rows before counted_at were applied: the book at the freeze less them
				variance := l.CountedQty.Sub(expected)
				var acc int64
				if v := catOf[l.ItemID].VarianceAccountID; v != nil {
					acc = *v
				} else if acc, err = s.cachedKey(ctx, p, propertyID, "INV_COUNT_VARIANCE", keyCache); err != nil {
					return err
				}
				var dept *int64
				if wd.DepartmentID != 0 {
					d := wd.DepartmentID
					dept = &d
				}
				if dept, err = s.resolveDepartment(ctx, p, propertyID, acc, dept, true, lineAt(i, "variance_account_id")); err != nil {
					return err
				}
				value := decimal.Zero
				ref := Ref{Type: "INV_COUNT", ID: id, Line: i + 1}
				switch {
				case variance.IsPositive():
					posted, err := stock.Gain(ctx, pair, variance, ref, &Counter{AccountID: acc, DepartmentID: dept})
					if err != nil {
						return err
					}
					value = posted.Value
				case variance.IsNegative():
					posted, err := stock.Out(ctx, OutArgs{Pair: pair, Kind: KindCountLoss, Qty: variance.Neg(), Ref: ref, Counter: &Counter{AccountID: acc, DepartmentID: dept}})
					if err != nil {
						return err
					}
					value = posted.Value // negative
				}
				neg := before.Neg()
				bookExpected := l.BookQty.Sub(before)
				if err := q.SetCountLineVariance(ctx, inventorydb.SetCountLineVarianceParams{PropertyID: propertyID, CountID: id, ItemID: l.ItemID, ExpectedQty: &bookExpected, HeldBeforeQty: &neg,
					VarianceQty: &variance, VarianceValue: &value}); err != nil {
					return err
				}
				total = total.Add(value)
			}
			return nil
		}
		if err := s.endFreeze(ctx, p, propertyID, bd, cur, counted, adjust); err != nil {
			return err
		}
		now := s.clock.Now()
		if err := q.SetCountState(ctx, inventorydb.SetCountStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: CountApproved, SubmittedAt: cur.SubmittedAt, SubmittedBy: cur.SubmittedBy,
			DecidedBy: &p.UserID, DecidedAt: &now, TotalVarianceValue: &total}); err != nil {
			return err
		}
		data := map[string]any{"status": CountApproved, "total_variance_value": total, "submitted_by": cur.SubmittedBy}
		data = approval.AuditFields(data)
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.count_approved", "inv_count", id, cur.CntNumber, map[string]any{"status": CountSubmitted}, data))
	})
	if err != nil {
		return Count{}, err
	}
	return s.count(ctx, p, propertyID, id)
}

func keysOf(m map[int64]countedAt) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
