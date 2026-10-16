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

// The opening stock (35-inventory.md 5.2, F9, D11): the quantity and the cost of what a warehouse holds the day the module starts. One opening per item and warehouse; every line is an
// OPENING movement booked Dr the inventory account of the category / Cr 3900 "Opening balance equity".

// OpeningLineInput is one line of an opening: an item, a unit of the item, the quantity in that unit and the cost of one such unit.
type OpeningLineInput struct {
	ItemID   int64           `json:"item_id"`
	UnitID   int64           `json:"unit_id"`
	Qty      decimal.Decimal `json:"qty"`
	UnitCost decimal.Decimal `json:"unit_cost"`
}

// OpeningInput posts the opening stock of a warehouse. ClientUUID makes the request safe to repeat: the second answers the first.
type OpeningInput struct {
	ClientUUID  string             `json:"client_uuid"`
	WarehouseID int64              `json:"warehouse_id"`
	Note        *string            `json:"note"`
	Lines       []OpeningLineInput `json:"lines"`
}

// OpeningLine is a line of a posted opening, in stock units.
type OpeningLine struct {
	LineNo   int             `json:"line_no"`
	ItemID   int64           `json:"item_id"`
	ItemCode string          `json:"item_code"`
	ItemName string          `json:"item_name"`
	UnitCode string          `json:"unit_code"`
	Qty      decimal.Decimal `json:"qty"`
	UnitCost decimal.Decimal `json:"unit_cost"`
	Value    decimal.Decimal `json:"value"`
}

// Opening is a posted opening stock.
type Opening struct {
	ID            int64           `json:"id"`
	Number        string          `json:"number"`
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseCode string          `json:"warehouse_code"`
	WarehouseName string          `json:"warehouse_name"`
	BusinessDate  civil.Date      `json:"business_date"`
	TotalValue    decimal.Decimal `json:"total_value"`
	LineCount     int             `json:"line_count"`
	Note          *string         `json:"note"`
	CreatedAt     time.Time       `json:"created_at"`
	Lines         []OpeningLine   `json:"lines,omitempty"`
}

const maxOpeningLines = 500

// ListOpenings lists the openings posted, newest first (inventory.view).
func (s *Service) ListOpenings(ctx context.Context, propertyID int64) ([]Opening, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListOpenings(ctx, inventorydb.ListOpeningsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Opening, 0, len(rows))
	for _, r := range rows {
		out = append(out, Opening{ID: r.ID, Number: r.OpeningNumber, WarehouseID: r.WarehouseID, WarehouseCode: r.WarehouseCode, WarehouseName: r.WarehouseName, BusinessDate: r.BusinessDate,
			TotalValue: r.TotalValue, LineCount: int(r.LineCount), Note: r.Note, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// GetOpening answers one opening with its lines (inventory.view).
func (s *Service) GetOpening(ctx context.Context, propertyID, id int64) (Opening, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return Opening{}, err
	}
	return s.opening(ctx, p, propertyID, id)
}

func (s *Service) opening(ctx context.Context, p auth.Principal, propertyID, id int64) (Opening, error) {
	q := s.q(ctx)
	r, err := q.GetOpening(ctx, inventorydb.GetOpeningParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Opening{}, orNotFound(err, notFound("OPENING_NOT_FOUND", "opening"))
	}
	lines, err := q.ListOpeningLines(ctx, inventorydb.ListOpeningLinesParams{TenantID: p.TenantID, PropertyID: propertyID, OpeningID: id})
	if err != nil {
		return Opening{}, err
	}
	o := Opening{ID: r.ID, Number: r.OpeningNumber, WarehouseID: r.WarehouseID, WarehouseCode: r.WarehouseCode, WarehouseName: r.WarehouseName, BusinessDate: r.BusinessDate,
		TotalValue: r.TotalValue, LineCount: len(lines), Note: r.Note, CreatedAt: r.CreatedAt, Lines: make([]OpeningLine, 0, len(lines))}
	for _, l := range lines {
		o.Lines = append(o.Lines, OpeningLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, UnitCode: l.UnitCode, Qty: l.QtyStock, UnitCost: l.UnitCost, Value: l.Value})
	}
	return o, nil
}

// PostOpening posts the opening stock of a warehouse (inventory.opening). All or nothing: the lines are checked first and nothing is written when one is wrong. The first opening of a
// property is refused (409 INVENTORY_GL_NOT_EMPTY) while its inventory accounts hold a balance in the general ledger: the accountant clears them first (35-inventory.md F9).
func (s *Service) PostOpening(ctx context.Context, propertyID int64, in OpeningInput) (Opening, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Opening{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermInventoryOpening); err != nil {
		return Opening{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Opening{}, invalid("opening", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	if len(in.Lines) == 0 || len(in.Lines) > maxOpeningLines {
		return Opening{}, invalid("opening", fieldErr("lines", "OUT_OF_RANGE", "1 to "+itoa(maxOpeningLines)+" lines"))
	}
	in.Note = optional(in.Note)
	if in.Note != nil && len([]rune(*in.Note)) > 300 {
		return Opening{}, invalid("opening", fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		// a repeated request gets the first answer
		if prev, err := q.GetOpeningByClientUUID(ctx, inventorydb.GetOpeningByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev.ID
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		wh, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.WarehouseID})
		if err != nil {
			return orNotFound(err, invalid("opening", fieldErr("warehouse_id", "NOT_FOUND", "no such warehouse in this property")))
		}
		if !wh.IsActive {
			return invalid("opening", fieldErr("warehouse_id", "INACTIVE", "the warehouse is switched off"))
		}
		lines, err := s.checkOpeningLines(ctx, p, propertyID, in)
		if err != nil {
			return err
		}
		book, err := s.book(ctx, p, propertyID, day.BusinessDate)
		if err != nil {
			return err
		}
		if has, err := q.HasMovements(ctx, propertyID); err != nil {
			return err
		} else if !has {
			bal, err := q.InventoryAccountBalances(ctx, inventorydb.InventoryAccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID})
			if err != nil {
				return err
			}
			if len(bal) > 0 {
				msg := ""
				for i, b := range bal {
					if i > 0 {
						msg += ", "
					}
					msg += fmt.Sprintf("%s %s: %s", b.Code, b.Name, b.Balance.String())
				}
				return apperr.Conflict("INVENTORY_GL_NOT_EMPTY", "the inventory accounts of the general ledger already hold a balance ("+msg+"): the accountant clears them by a manual journal before the opening stock is posted").
					WithContext("accounts", msg)
			}
		}
		equity, err := q.GetAccountByCode(ctx, inventorydb.GetAccountByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: "3900"})
		if err != nil || equity.AccountType != "EQUITY" || !equity.IsActive || !equity.IsPostable {
			return apperr.Conflict("OPENING_ACCOUNT_MISSING", "the opening stock is booked against the account 3900 \"Opening balance equity\", which is missing or not usable in this property")
		}
		pairs := make([]Pair, 0, len(lines))
		for _, l := range lines {
			pairs = append(pairs, Pair{in.WarehouseID, l.item.ID})
		}
		// the order of 5.1: stock first (the warehouse, then the balance rows), the document number last
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvOpening)
		if err != nil {
			return err
		}
		total := decimal.Zero
		for _, l := range lines {
			total = total.Add(book.roundMoney(l.value))
		}
		id, err = q.InsertOpening(ctx, inventorydb.InsertOpeningParams{TenantID: p.TenantID, PropertyID: propertyID, OpeningNumber: number, ClientUuid: key, WarehouseID: in.WarehouseID,
			BusinessDate: day.BusinessDate, TotalValue: total, Note: in.Note, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		counter := &Counter{AccountID: equity.ID}
		for i, l := range lines {
			posted, err := stock.In(ctx, InArgs{Pair: Pair{in.WarehouseID, l.item.ID}, Kind: KindOpening, Qty: l.qty, Value: l.value, Ref: Ref{Type: "INV_OPENING", ID: id, Line: i + 1}, Counter: counter})
			if err != nil {
				return err
			}
			if err := q.InsertOpeningLine(ctx, inventorydb.InsertOpeningLineParams{TenantID: p.TenantID, PropertyID: propertyID, OpeningID: id, LineNo: int32(i + 1), //nolint:gosec // G115: at most 500
				WarehouseID: in.WarehouseID, ItemID: l.item.ID, QtyStock: posted.Qty, UnitCost: posted.UnitCost, Value: posted.Value, MovementID: posted.MovementID}); err != nil {
				return err
			}
		}
		o, err := s.opening(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "inventory.opening_posted", "inv_opening", id, number, nil,
			map[string]any{"number": number, "warehouse": wh.Code, "lines": len(lines), "total_value": o.TotalValue}))
	})
	if err != nil {
		return Opening{}, err
	}
	return s.opening(ctx, p, propertyID, id)
}

type openingLine struct {
	item  inventorydb.GetItemRow
	qty   decimal.Decimal // stock units
	value decimal.Decimal // money of the whole quantity, from the unit cost
}

func (b Book) roundMoney(d decimal.Decimal) decimal.Decimal { return d.Round(b.Decimals) }

// checkOpeningLines validates every line before anything is written: the item and its unit, the quantity and the cost, one line per item, and no opening of that item in the warehouse yet.
func (s *Service) checkOpeningLines(ctx context.Context, p auth.Principal, propertyID int64, in OpeningInput) ([]openingLine, error) {
	q := s.q(ctx)
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var f []apperr.FieldError
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(in.Lines))
	for _, l := range in.Lines {
		ids = append(ids, l.ItemID)
	}
	units := map[[2]int64]inventorydb.ListItemUnitsOfRow{}
	if rows, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids}); err != nil {
		return nil, err
	} else {
		for _, r := range rows {
			units[[2]int64{r.ItemID, r.UnitID}] = r
		}
	}
	opened, err := q.OpenedItems(ctx, inventorydb.OpenedItemsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: in.WarehouseID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	out := make([]openingLine, 0, len(in.Lines))
	for i, l := range in.Lines {
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
			f = append(f, fieldErr(at+".item_id", "DUPLICATE", "one line per item"))
			continue
		case slices.Contains(opened, l.ItemID):
			f = append(f, fieldErr(at+".item_id", "OPENING_EXISTS", "the item has its opening stock in this warehouse already"))
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
		if l.UnitCost.IsNegative() || l.UnitCost.GreaterThan(decimal.NewFromInt(1_000_000_000_000)) || !l.UnitCost.Equal(l.UnitCost.Round(prop.CurrencyDecimals)) {
			f = append(f, fieldErr(at+".unit_cost", "INVALID_VALUE", fmt.Sprintf("zero or more, %d decimals at most", prop.CurrencyDecimals)))
			continue
		}
		qty := StockQty(l.Qty, u.StockNum, u.StockDen)
		if !qty.IsPositive() {
			f = append(f, fieldErr(at+".qty", "TOO_SMALL", "less than the smallest quantity of the stock unit"))
			continue
		}
		// a purchase is valued from the unit it was counted in, so a conversion never makes a difference of money (4.1)
		out = append(out, openingLine{item: it, qty: qty, value: l.Qty.Mul(l.UnitCost).Round(prop.CurrencyDecimals)})
	}
	if len(f) > 0 {
		return nil, invalid("opening", f...)
	}
	return out, nil
}
