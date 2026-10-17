package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The automatic amenities of the day close (35-inventory.md 5.9, I16, D24). At each close the system makes one issue (AMN-20260903) of the amenities for every room that was slept in that business day: the plan
// gives a base package for every room type and extras for a room type, each per room or per guest. The document is an inv_issues row (origin AMENITY) and its run is kept in inv_amenity_runs, one for each
// business day (the idempotency key). A shortage never stops the close (the issue passes AllowShortfall); a warehouse that a count freezes holds its lines (status HELD) until the count ends.

// Bases of a plan line.
const (
	BasisRoom  = "PER_ROOM"
	BasisGuest = "PER_GUEST"
)

// Occupancy kinds of a room of the night.
const (
	OccPaid          = "PAID"
	OccComplimentary = "COMPLIMENTARY"
	OccHouseUse      = "HOUSE_USE"
)

// Codes of the findings of the night audit about the amenities.
const (
	FindAmenityPlanInvalid = "AMENITY_PLAN_INVALID"
	FindAmenityHeld        = "AMENITY_HELD"
	FindAmenityShort       = "AMENITY_STOCK_SHORT"
	FindCountOpen          = "INVENTORY_COUNT_OPEN"
)

// NightRoom is a room slept in on a business date (the front desk's answer, through Occupancy).
type NightRoom struct {
	RoomID     int64
	RoomNumber string
	RoomTypeID int64
	StayID     int64
	Guests     int
	Kind       string
}

// Occupancy gives the rooms slept in on a business date. internal/frontdesk implements it (next to the room-night rule the room charge uses); internal/app wires it, so that inventory does not import the front
// desk.
type Occupancy interface {
	NightOccupancy(ctx context.Context, tenantID, propertyID int64, date civil.Date) ([]NightRoom, error)
}

// SetOccupancy wires the front desk; without it the amenities of a close make nothing.
func (s *Service) SetOccupancy(o Occupancy) { s.occupancy = o }

// AmenityPlanLineInput is a line of the plan: an item in one of its units, the quantity, per room or per guest, for a room type or for every type.
type AmenityPlanLineInput struct {
	RoomTypeID *int64          `json:"room_type_id"`
	ItemID     int64           `json:"item_id"`
	UnitID     int64           `json:"unit_id"`
	Qty        decimal.Decimal `json:"qty"`
	Basis      string          `json:"basis"`
}

// AmenityPlanInput saves the plan as a new version. A plan that is switched off keeps its lines.
type AmenityPlanInput struct {
	IsEnabled        bool                   `json:"is_enabled"`
	WarehouseID      *int64                 `json:"warehouse_id"`
	DepartmentID     *int64                 `json:"department_id"`
	ExpenseAccountID *int64                 `json:"expense_account_id"`
	Lines            []AmenityPlanLineInput `json:"lines"`
}

// AmenityPlanLine is a line of the plan as it stands.
type AmenityPlanLine struct {
	LineNo        int             `json:"line_no"`
	RoomTypeID    *int64          `json:"room_type_id"`
	RoomTypeName  *string         `json:"room_type_name"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitID        int64           `json:"unit_id"`
	UnitCode      string          `json:"unit_code"`
	StockUnitCode string          `json:"stock_unit_code"`
	Qty           decimal.Decimal `json:"qty"`
	Basis         string          `json:"basis"`
}

// RoomTypeCost is what the plan costs for a room with a number of guests.
type RoomTypeCost struct {
	RoomTypeID   int64           `json:"room_type_id"`
	RoomTypeName string          `json:"room_type_name"`
	Guests       int             `json:"guests"`
	Cost         decimal.Decimal `json:"cost"`
}

// AmenityPlan is a version of the plan with its lines and what a room costs by it.
type AmenityPlan struct {
	ID              int64             `json:"id"`
	VersionNo       int               `json:"version_no"`
	EffectiveFrom   *civil.Date       `json:"effective_from"`
	IsEnabled       bool              `json:"is_enabled"`
	WarehouseID     *int64            `json:"warehouse_id"`
	WarehouseName   *string           `json:"warehouse_name"`
	DepartmentID    *int64            `json:"department_id"`
	DepartmentName  *string           `json:"department_name"`
	AccountID       *int64            `json:"expense_account_id"`
	AccountCode     *string           `json:"expense_account_code"`
	AccountName     *string           `json:"expense_account_name"`
	CreatedAt       *time.Time        `json:"created_at"`
	Lines           []AmenityPlanLine `json:"lines"`
	CostPerRoom     []RoomTypeCost    `json:"cost_per_room"`
	Versions        []PlanVersion     `json:"versions"`
	AppliesFromNext bool              `json:"applies_from_next_close"`
}

// PlanVersion is a row of the history of the plan.
type PlanVersion struct {
	ID            int64      `json:"id"`
	VersionNo     int        `json:"version_no"`
	EffectiveFrom civil.Date `json:"effective_from"`
	IsEnabled     bool       `json:"is_enabled"`
	LineCount     int        `json:"line_count"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedByName *string    `json:"created_by_name"`
}

func (s *Service) planOf(ctx context.Context, p auth.Principal, propertyID int64, date civil.Date) (*inventorydb.GetAmenityPlanInForceRow, error) {
	r, err := s.q(ctx).GetAmenityPlanInForce(ctx, inventorydb.GetAmenityPlanInForceParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: date})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// itemCosts reads the cost of each item in a warehouse as a unit cost now: the average of the balance, else the last unit cost, else the reference cost (what the screen shows as the cost of a room).
func (s *Service) itemCosts(ctx context.Context, p auth.Principal, propertyID, warehouseID int64, itemIDs []int64) (map[int64]decimal.Decimal, error) {
	rows, err := s.q(ctx).ItemAverageCosts(ctx, inventorydb.ItemAverageCostsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	out := map[int64]decimal.Decimal{}
	for _, r := range rows {
		switch {
		case r.Qty.IsPositive():
			out[r.ItemID] = r.Value.Div(r.Qty)
		case r.LastUnitCost != nil:
			out[r.ItemID] = *r.LastUnitCost
		case r.ReferenceCost != nil:
			out[r.ItemID] = *r.ReferenceCost
		}
	}
	return out, nil
}

// GetAmenityPlan answers the plan in force and its history (inventory.view or inventory.manage). A property that has none gets an empty plan that is switched off.
func (s *Service) GetAmenityPlan(ctx context.Context, propertyID int64) (AmenityPlan, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return AmenityPlan{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return AmenityPlan{}, err
	}
	return s.amenityPlan(ctx, p, propertyID, day.BusinessDate)
}

func (s *Service) amenityPlan(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) (AmenityPlan, error) {
	q := s.q(ctx)
	out := AmenityPlan{Lines: []AmenityPlanLine{}, CostPerRoom: []RoomTypeCost{}, Versions: []PlanVersion{}, AppliesFromNext: true}
	vs, err := q.ListAmenityPlans(ctx, inventorydb.ListAmenityPlansParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	for _, v := range vs {
		out.Versions = append(out.Versions, PlanVersion{ID: v.ID, VersionNo: int(v.VersionNo), EffectiveFrom: v.EffectiveFrom, IsEnabled: v.IsEnabled, LineCount: int(v.LineCount), CreatedAt: v.CreatedAt, CreatedByName: v.CreatedByName})
	}
	r, err := s.planOf(ctx, p, propertyID, bd)
	if err != nil || r == nil {
		return out, err
	}
	out.ID, out.VersionNo, out.IsEnabled = r.ID, int(r.VersionNo), r.IsEnabled
	ef, ca := r.EffectiveFrom, r.CreatedAt
	out.EffectiveFrom, out.CreatedAt = &ef, &ca
	out.WarehouseID, out.WarehouseName, out.DepartmentID, out.DepartmentName = r.WarehouseID, r.WarehouseName, r.DepartmentID, r.DepartmentName
	out.AccountID, out.AccountCode, out.AccountName = r.ExpenseAccountID, r.AccountCode, r.AccountName
	lines, err := q.ListAmenityPlanLines(ctx, inventorydb.ListAmenityPlanLinesParams{TenantID: p.TenantID, PropertyID: propertyID, PlanID: r.ID})
	if err != nil {
		return out, err
	}
	var items []int64
	for _, l := range lines {
		out.Lines = append(out.Lines, AmenityPlanLine{LineNo: int(l.LineNo), RoomTypeID: l.RoomTypeID, RoomTypeName: l.RoomTypeName, ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName,
			UnitID: l.UnitID, UnitCode: l.UnitCode, StockUnitCode: l.StockUnitCode, Qty: l.Qty, Basis: l.Basis})
		items = append(items, l.ItemID)
	}
	if r.WarehouseID != nil && len(lines) > 0 {
		costs, err := s.itemCosts(ctx, p, propertyID, *r.WarehouseID, items)
		if err != nil {
			return out, err
		}
		types, err := q.ListRoomTypesLite(ctx, inventorydb.ListRoomTypesLiteParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return out, err
		}
		for _, t := range types {
			guests := 2
			total := decimal.Zero
			for _, l := range lines {
				if l.RoomTypeID != nil && *l.RoomTypeID != t.ID {
					continue
				}
				mult := int64(1)
				if l.Basis == BasisGuest {
					mult = int64(guests)
				}
				stock := StockQty(l.Qty.Mul(decimal.NewFromInt(mult)), l.StockNum, l.StockDen)
				total = total.Add(stock.Mul(costs[l.ItemID]))
			}
			out.CostPerRoom = append(out.CostPerRoom, RoomTypeCost{RoomTypeID: t.ID, RoomTypeName: t.Name, Guests: guests, Cost: total.Round(2)})
		}
	}
	return out, nil
}

// SaveAmenityPlan saves the plan as a new version (inventory.manage). A version is never edited; the one saved on the business day D is used by the next close that runs, normally the close of D, and a run
// that was made keeps the version it used. A plan that is switched on is validated: the warehouse is active, the account is an active postable expense account whose department rule the plan meets, the items
// are active and the units are theirs (422 AMENITY_PLAN_INVALID).
func (s *Service) SaveAmenityPlan(ctx context.Context, propertyID int64, in AmenityPlanInput) (AmenityPlan, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return AmenityPlan{}, err
	}
	if len(in.Lines) > 100 {
		return AmenityPlan{}, planInvalid(fieldErr("lines", "OUT_OF_RANGE", "100 lines at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		if err := s.checkAmenityPlan(ctx, p, propertyID, in); err != nil {
			return err
		}
		prev, err := q.MaxAmenityPlanVersion(ctx, propertyID)
		if err != nil {
			return err
		}
		id, err := q.InsertAmenityPlan(ctx, inventorydb.InsertAmenityPlanParams{TenantID: p.TenantID, PropertyID: propertyID, VersionNo: prev + 1, EffectiveFrom: bd, IsEnabled: in.IsEnabled,
			WarehouseID: in.WarehouseID, DepartmentID: in.DepartmentID, ExpenseAccountID: in.ExpenseAccountID, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		for i, l := range in.Lines {
			if err := q.InsertAmenityPlanLine(ctx, inventorydb.InsertAmenityPlanLineParams{TenantID: p.TenantID, PropertyID: propertyID, PlanID: id, LineNo: int32(i + 1), RoomTypeID: l.RoomTypeID, //nolint:gosec // G115: at most 100
				ItemID: l.ItemID, UnitID: l.UnitID, Qty: l.Qty, Basis: l.Basis}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.amenity_plan_saved", "inv_amenity_plan", id, fmt.Sprintf("version %d", prev+1), nil,
			map[string]any{"version": prev + 1, "enabled": in.IsEnabled, "lines": len(in.Lines), "warehouse_id": in.WarehouseID}))
	})
	if err != nil {
		return AmenityPlan{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return AmenityPlan{}, err
	}
	return s.amenityPlan(ctx, p, propertyID, day.BusinessDate)
}

func planInvalid(f ...apperr.FieldError) *apperr.Error {
	e := apperr.Invalid("the amenities plan is invalid", f...)
	e.Code = FindAmenityPlanInvalid
	return e
}

// checkAmenityPlan validates a plan before it is saved. A plan that is off is checked for the shape of its lines only.
func (s *Service) checkAmenityPlan(ctx context.Context, p auth.Principal, propertyID int64, in AmenityPlanInput) error {
	q := s.q(ctx)
	var f []apperr.FieldError
	ids := make([]int64, 0, len(in.Lines))
	for _, l := range in.Lines {
		ids = append(ids, l.ItemID)
	}
	units := map[[2]int64]bool{}
	us, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return err
	}
	for _, u := range us {
		units[[2]int64{u.ItemID, u.UnitID}] = true
	}
	infos := map[int64]inventorydb.ItemCategoryAccountsRow{}
	rows, err := q.ItemCategoryAccounts(ctx, inventorydb.ItemCategoryAccountsParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return err
	}
	for _, r := range rows {
		infos[r.ItemID] = r
	}
	types := map[int64]bool{}
	ts, err := q.ListRoomTypesLite(ctx, inventorydb.ListRoomTypesLiteParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return err
	}
	for _, t := range ts {
		types[t.ID] = true
	}
	for i, l := range in.Lines {
		at := lineAt(i, "")
		it, ok := infos[l.ItemID]
		switch {
		case !ok:
			f = append(f, fieldErr(at+"item_id", "NOT_FOUND", "no such item in this property"))
		case in.IsEnabled && !it.IsActive:
			f = append(f, fieldErr(at+"item_id", "INACTIVE", "the item is switched off"))
		case !units[[2]int64{l.ItemID, l.UnitID}]:
			f = append(f, fieldErr(at+"unit_id", "NOT_FOUND", "not a unit of the item"))
		}
		if !l.Qty.IsPositive() || l.Qty.GreaterThan(decimal.NewFromInt(1_000_000)) || !l.Qty.Equal(Round6(l.Qty)) {
			f = append(f, fieldErr(at+"qty", "INVALID_VALUE", "above zero, six decimals at most"))
		}
		if l.Basis != BasisRoom && l.Basis != BasisGuest {
			f = append(f, fieldErr(at+"basis", "INVALID_VALUE", "PER_ROOM or PER_GUEST"))
		}
		if l.RoomTypeID != nil && !types[*l.RoomTypeID] {
			f = append(f, fieldErr(at+"room_type_id", "NOT_FOUND", "no such room type in this property"))
		}
	}
	if in.IsEnabled {
		if len(in.Lines) == 0 {
			f = append(f, fieldErr("lines", "REQUIRED", "a plan that is on has at least one line"))
		}
		if in.WarehouseID == nil {
			f = append(f, fieldErr("warehouse_id", "REQUIRED", "the warehouse the amenities come from"))
		} else if w, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.WarehouseID}); err != nil {
			f = append(f, fieldErr("warehouse_id", "NOT_FOUND", "no such warehouse in this property"))
		} else if !w.IsActive {
			f = append(f, fieldErr("warehouse_id", "INACTIVE", "the warehouse is switched off"))
		}
		if in.ExpenseAccountID == nil {
			f = append(f, fieldErr("expense_account_id", "REQUIRED", "the expense account of the amenities"))
		} else if _, derr := s.resolveDepartment(ctx, p, propertyID, *in.ExpenseAccountID, in.DepartmentID, false, "expense_account_id"); derr != nil {
			var ae *apperr.Error
			if errors.As(derr, &ae) && len(ae.Fields) > 0 {
				f = append(f, ae.Fields...)
			} else {
				return derr
			}
		}
	}
	if err := s.checkDepartmentExists(ctx, p, propertyID, in.DepartmentID, "department_id"); err != nil {
		return err
	}
	if len(f) > 0 {
		return planInvalid(f...)
	}
	return nil
}

// ---------------------------------------------------------------------------------------------- what the plan makes of a night

// need is what a plan line asks of one night: how many times its quantity.
type need struct {
	planLine   inventorydb.ListAmenityPlanLinesRow
	multiplier int
	qtyStock   decimal.Decimal
}

// nightTotals are the rooms of a night as the amenities count them.
type nightTotals struct {
	rooms, guests, comp, house int
	byType                     map[int64][2]int // room type -> rooms, guests
}

func totalsOf(rooms []NightRoom) nightTotals {
	t := nightTotals{byType: map[int64][2]int{}}
	for _, r := range rooms {
		t.rooms++
		t.guests += r.Guests
		switch r.Kind {
		case OccComplimentary:
			t.comp++
		case OccHouseUse:
			t.house++
		}
		c := t.byType[r.RoomTypeID]
		t.byType[r.RoomTypeID] = [2]int{c[0] + 1, c[1] + r.Guests}
	}
	return t
}

// needsOf works out the quantities of a night: each line of the plan times the rooms of its type (or all rooms), or the guests of those rooms. Lines that ask for nothing are left out.
func needsOf(lines []inventorydb.ListAmenityPlanLinesRow, t nightTotals) []need {
	var out []need
	for _, l := range lines {
		rooms, guests := t.rooms, t.guests
		if l.RoomTypeID != nil {
			c := t.byType[*l.RoomTypeID]
			rooms, guests = c[0], c[1]
		}
		mult := rooms
		if l.Basis == BasisGuest {
			mult = guests
		}
		if mult == 0 {
			continue
		}
		out = append(out, need{planLine: l, multiplier: mult, qtyStock: StockQty(l.Qty.Mul(decimal.NewFromInt(int64(mult))), l.StockNum, l.StockDen)})
	}
	return out
}

// issueLinesOf adds the needs up by item: one line of the document for each item, in the order of the plan.
func issueLinesOf(needs []need) (items []int64, qty map[int64]decimal.Decimal) {
	qty = map[int64]decimal.Decimal{}
	for _, n := range needs {
		if _, ok := qty[n.planLine.ItemID]; !ok {
			items = append(items, n.planLine.ItemID)
		}
		qty[n.planLine.ItemID] = qty[n.planLine.ItemID].Add(n.qtyStock)
	}
	return items, qty
}

func amenityNumber(bd civil.Date) string {
	return fmt.Sprintf("AMN-%04d%02d%02d", bd.Year(), int(bd.Month()), bd.Day())
}

// planRunnable says whether the plan in force can run tonight (5.7): its warehouse is active, its account and department rule hold, its items are active. A warehouse frozen by a count is not a cause (D24).
// It answers the reasons, empty when it can.
func (s *Service) planRunnable(ctx context.Context, p auth.Principal, propertyID int64, plan *inventorydb.GetAmenityPlanInForceRow, lines []inventorydb.ListAmenityPlanLinesRow) ([]string, error) {
	var why []string
	if plan.WarehouseID == nil || plan.ExpenseAccountID == nil {
		return []string{"the plan has no warehouse or no account"}, nil
	}
	w, err := s.q(ctx).GetWarehouseLite(ctx, inventorydb.GetWarehouseLiteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *plan.WarehouseID})
	if err != nil || !w.IsActive {
		why = append(why, "the warehouse of the plan is switched off")
	}
	if _, err := s.resolveDepartment(ctx, p, propertyID, *plan.ExpenseAccountID, plan.DepartmentID, false, "expense_account_id"); err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			why = append(why, ae.Message)
		} else {
			return nil, err
		}
	}
	for _, l := range lines {
		if !l.ItemActive {
			why = append(why, "the item "+l.ItemCode+" is switched off")
		}
	}
	return why, nil
}

// ---------------------------------------------------------------------------------------------- the preview

// AmenityShort is an item that tonight's need is more than the shelf holds.
type AmenityShort struct {
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	StockUnitCode string          `json:"stock_unit_code"`
	Need          decimal.Decimal `json:"need"`
	OnHand        decimal.Decimal `json:"on_hand"`
	Short         decimal.Decimal `json:"short"`
}

// AmenityPreview is tonight's amenities as the close would make them now.
type AmenityPreview struct {
	Date       civil.Date       `json:"business_date"`
	Enabled    bool             `json:"enabled"`
	Rooms      int              `json:"rooms"`
	Guests     int              `json:"guests"`
	CompRooms  int              `json:"comp_rooms"`
	HouseRooms int              `json:"house_use_rooms"`
	Value      decimal.Decimal  `json:"value"`
	Short      []AmenityShort   `json:"short"`
	Held       *HeldDocument    `json:"held"`
	HeldCount  *string          `json:"held_by_count"`
	Invalid    []string         `json:"invalid"`
	Types      []AmenityRunType `json:"types"`
	Lines      []AmenityRunLine `json:"lines"`
}

// AmenityPreview works out what the close would make tonight (inventory.view): the rooms, the guests, the value at the cost of now and the items that will be short. It reads and writes nothing.
func (s *Service) AmenityPreview(ctx context.Context, propertyID int64) (AmenityPreview, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return AmenityPreview{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return AmenityPreview{}, err
	}
	return s.amenityPreview(ctx, p, propertyID, day.BusinessDate)
}

func (s *Service) amenityPreview(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) (AmenityPreview, error) {
	out := AmenityPreview{Date: bd, Short: []AmenityShort{}, Invalid: []string{}, Types: []AmenityRunType{}, Lines: []AmenityRunLine{}, Value: decimal.Zero}
	plan, err := s.planOf(ctx, p, propertyID, bd)
	if err != nil || plan == nil || !plan.IsEnabled {
		return out, err
	}
	out.Enabled = true
	lines, err := s.q(ctx).ListAmenityPlanLines(ctx, inventorydb.ListAmenityPlanLinesParams{TenantID: p.TenantID, PropertyID: propertyID, PlanID: plan.ID})
	if err != nil {
		return out, err
	}
	if out.Invalid, err = s.planRunnable(ctx, p, propertyID, plan, lines); err != nil {
		return out, err
	}
	if out.Invalid == nil {
		out.Invalid = []string{} // a list in the JSON, never null
	}
	if s.occupancy == nil || plan.WarehouseID == nil {
		return out, nil
	}
	rooms, err := s.occupancy.NightOccupancy(ctx, p.TenantID, propertyID, bd)
	if err != nil {
		return out, err
	}
	t := totalsOf(rooms)
	out.Rooms, out.Guests, out.CompRooms, out.HouseRooms = t.rooms, t.guests, t.comp, t.house
	types, err := s.runTypes(ctx, p, propertyID, rooms)
	if err != nil {
		return out, err
	}
	out.Types = types
	needs := needsOf(lines, t)
	items, qty := issueLinesOf(needs)
	if len(items) == 0 {
		return out, nil
	}
	costs, err := s.itemCosts(ctx, p, propertyID, *plan.WarehouseID, items)
	if err != nil {
		return out, err
	}
	bal, err := s.q(ctx).ItemAverageCosts(ctx, inventorydb.ItemAverageCostsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: *plan.WarehouseID, ItemIds: items})
	if err != nil {
		return out, err
	}
	onHand := map[int64]decimal.Decimal{}
	for _, b := range bal {
		onHand[b.ItemID] = b.Qty
	}
	meta := map[int64]inventorydb.ListAmenityPlanLinesRow{}
	for _, l := range lines {
		meta[l.ItemID] = l
	}
	for i, n := range needs {
		l := n.planLine
		val := n.qtyStock.Mul(costs[l.ItemID]).Round(2)
		out.Value = out.Value.Add(val)
		out.Lines = append(out.Lines, AmenityRunLine{LineNo: i + 1, ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, StockUnitCode: l.StockUnitCode, RoomTypeID: l.RoomTypeID, RoomTypeName: l.RoomTypeName,
			Basis: l.Basis, QtyPer: l.Qty, Multiplier: n.multiplier, QtyStock: n.qtyStock, Value: &val, Status: "PREVIEW"})
	}
	for _, id := range items {
		have := decimal.Max(onHand[id], decimal.Zero)
		if qty[id].GreaterThan(have) {
			m := meta[id]
			out.Short = append(out.Short, AmenityShort{ItemID: id, ItemCode: m.ItemCode, ItemName: m.ItemName, StockUnitCode: m.StockUnitCode, Need: qty[id], OnHand: onHand[id], Short: qty[id].Sub(have)})
		}
	}
	if w, err := s.q(ctx).GetWarehouseLite(ctx, inventorydb.GetWarehouseLiteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *plan.WarehouseID}); err == nil && w.CountID != nil {
		out.HeldCount = w.CntNumber
	}
	return out, nil
}

// runTypes counts the rooms and the guests of a night by room type.
func (s *Service) runTypes(ctx context.Context, p auth.Principal, propertyID int64, rooms []NightRoom) ([]AmenityRunType, error) {
	types, err := s.q(ctx).ListRoomTypesLite(ctx, inventorydb.ListRoomTypesLiteParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	t := totalsOf(rooms)
	out := []AmenityRunType{}
	for _, rt := range types {
		if c, ok := t.byType[rt.ID]; ok {
			out = append(out, AmenityRunType{RoomTypeID: rt.ID, RoomTypeName: rt.Name, Rooms: c[0], Guests: c[1]})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------- the run of the close

// RunAmenities makes the amenities of a business day, inside the transaction of the close, after the room charges and before the journal (5.7 step 2). It is idempotent (one run for each business day) and a
// shortage does not stop it. A plan that cannot run at all answers 422 AMENITY_PLAN_INVALID (the night audit has said so as a blocker before). It takes the stock level and, in that order, the warehouse
// and the balances of the items.
func (s *Service) RunAmenities(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	if err := db.EnterLockLevel(ctx, db.LevelStock); err != nil {
		return err
	}
	q := s.q(ctx)
	plan, err := s.planOf(ctx, p, propertyID, bd)
	if err != nil || plan == nil || !plan.IsEnabled || s.occupancy == nil {
		return err
	}
	if _, err := q.GetAmenityRunByDate(ctx, inventorydb.GetAmenityRunByDateParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd}); err == nil {
		return nil // a second attempt of the same day finds the run and does nothing
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rooms, err := s.occupancy.NightOccupancy(ctx, p.TenantID, propertyID, bd)
	if err != nil || len(rooms) == 0 {
		return err // no occupied room, no document
	}
	lines, err := q.ListAmenityPlanLines(ctx, inventorydb.ListAmenityPlanLinesParams{TenantID: p.TenantID, PropertyID: propertyID, PlanID: plan.ID})
	if err != nil {
		return err
	}
	if why, err := s.planRunnable(ctx, p, propertyID, plan, lines); err != nil {
		return err
	} else if len(why) > 0 {
		return planInvalid(fieldErr("plan", "INVALID_VALUE", why[0]))
	}
	t := totalsOf(rooms)
	needs := needsOf(lines, t)
	items, qty := issueLinesOf(needs)
	if len(items) == 0 {
		return nil
	}
	wh := *plan.WarehouseID
	w, err := q.GetWarehouseLite(ctx, inventorydb.GetWarehouseLiteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: wh})
	if err != nil {
		return err
	}
	frozen := w.CountID != nil // D24: the close is not blocked, the lines wait for the count
	account := *plan.ExpenseAccountID
	dept, err := s.resolveDepartment(ctx, p, propertyID, account, plan.DepartmentID, false, "expense_account_id")
	if err != nil {
		return err
	}
	var stock *Stock
	if !frozen {
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, len(items))
		for _, id := range items {
			pairs = append(pairs, Pair{wh, id})
		}
		if stock, err = s.ledger.Lock(ctx, q, book, pairs, LockOptions{}); err != nil {
			return err
		}
	}
	var key pgtype.UUID
	if err := key.Scan(newUUID()); err != nil {
		return err
	}
	now := s.clock.Now()
	note := "Amenities otomatis " + bd.String()
	srcDate := bd
	issueID, err := q.InsertIssue(ctx, inventorydb.InsertIssueParams{TenantID: p.TenantID, PropertyID: propertyID, IssNumber: amenityNumber(bd), ClientUuid: key, WarehouseID: wh, Purpose: PurposeUse,
		Origin: OriginAmenity, DepartmentID: plan.DepartmentID, BusinessDate: bd, SourceDate: &srcDate, Note: &note, CreatedAt: now, ActorID: p.ActorID()})
	if err != nil {
		return err
	}
	before := map[int64]decimal.Decimal{}
	total := decimal.Zero
	lineNoOf := map[int64]int{}
	for i, id := range items {
		lineNoOf[id] = i + 1
		if frozen {
			if err := q.InsertIssueLine(ctx, inventorydb.InsertIssueLineParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: issueID, LineNo: int32(i + 1), ItemID: id, //nolint:gosec // G115: at most 100
				QtyStock: qty[id], ExpenseAccountID: &account, DepartmentID: dept, Status: LineHeld}); err != nil {
				return err
			}
			continue
		}
		b, _, _ := stock.Balance(Pair{wh, id})
		before[id] = b
		posted, err := stock.Out(ctx, AllowShortfall(OutArgs{Pair: Pair{wh, id}, Kind: KindIssue, Qty: qty[id], Ref: Ref{Type: "INV_ISSUE", ID: issueID, Line: i + 1},
			Counter: &Counter{AccountID: account, DepartmentID: dept}}, "AMENITY"))
		if err != nil {
			return err
		}
		value, mid := posted.Value.Neg(), posted.MovementID
		if err := q.InsertIssueLine(ctx, inventorydb.InsertIssueLineParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: issueID, LineNo: int32(i + 1), ItemID: id, //nolint:gosec // G115: at most 100
			QtyStock: qty[id], Value: &value, ExpenseAccountID: &account, DepartmentID: dept, MovementID: &mid, Status: LineApplied, AppliedOn: &bd, AppliedAt: &now}); err != nil {
			return err
		}
		total = total.Add(value)
	}
	runID, err := q.InsertAmenityRun(ctx, inventorydb.InsertAmenityRunParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd, PlanID: plan.ID, IssueID: issueID, Rooms: int32(t.rooms), //nolint:gosec // G115: rooms
		Guests: int32(t.guests), CompRooms: int32(t.comp), HouseUseRooms: int32(t.house), TotalValue: total, CreatedAt: now}) //nolint:gosec // G115: small counts
	if err != nil {
		return err
	}
	for _, r := range rooms {
		if err := q.InsertAmenityRunRoom(ctx, inventorydb.InsertAmenityRunRoomParams{TenantID: p.TenantID, PropertyID: propertyID, RunID: runID, RoomID: r.RoomID, RoomTypeID: r.RoomTypeID, StayID: r.StayID,
			Guests: int16(r.Guests), OccupancyKind: r.Kind}); err != nil { //nolint:gosec // G115: guests of a room
			return err
		}
	}
	for i, n := range needs {
		var sb *decimal.Decimal
		if b, ok := before[n.planLine.ItemID]; ok {
			sb = &b
		}
		if err := q.InsertAmenityRunLine(ctx, inventorydb.InsertAmenityRunLineParams{TenantID: p.TenantID, PropertyID: propertyID, RunID: runID, LineNo: int32(i + 1), PlanID: plan.ID, //nolint:gosec // G115: at most 100
			PlanLineNo: n.planLine.LineNo, ItemID: n.planLine.ItemID, RoomTypeID: n.planLine.RoomTypeID, Basis: n.planLine.Basis, QtyPer: n.planLine.Qty, Multiplier: int32(n.multiplier), //nolint:gosec // G115: rooms
			QtyStock: n.qtyStock, StockBefore: sb, IssueLineNo: int32(lineNoOf[n.planLine.ItemID])}); err != nil { //nolint:gosec // G115: at most 100
			return err
		}
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.amenity_run_posted", "inv_amenity_run", runID, amenityNumber(bd), nil,
		map[string]any{"rooms": t.rooms, "guests": t.guests, "value": total, "held": frozen, "plan_version": plan.VersionNo}))
}

// applyHeldIssueLine applies a line of an amenities document that a count held: an ISSUE with the right to take stock below zero, dated the open day with the night of the amenities as source_date, at the cost
// of this moment. The line becomes APPLIED and the run keeps the total of what has been applied.
func (s *Service) applyHeldIssueLine(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, stock *Stock, r heldRow) error {
	q := s.q(ctx)
	issue, err := q.GetIssue(ctx, inventorydb.GetIssueParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.IssueID})
	if err != nil {
		return err
	}
	pair := Pair{issue.WarehouseID, r.ItemID}
	posted, err := stock.Out(ctx, AllowShortfall(OutArgs{Pair: pair, Kind: KindIssue, Qty: r.Qty, Ref: Ref{Type: "INV_ISSUE", ID: r.IssueID, Line: int(r.LineNo), SourceDate: r.SourceDate},
		Counter: &Counter{AccountID: r.AccountID, DepartmentID: r.DepartmentID}}, "AMENITY"))
	if err != nil {
		return err
	}
	if err := q.SetIssueLineApplied(ctx, inventorydb.SetIssueLineAppliedParams{PropertyID: propertyID, IssueID: r.IssueID, LineNo: r.LineNo, MovementID: &posted.MovementID, Value: ptrDec(posted.Value.Neg()), AppliedOn: &bd, AppliedAt: ptrTime(s.clock.Now())}); err != nil {
		return err
	}
	if run, err := q.GetAmenityRunByIssue(ctx, inventorydb.GetAmenityRunByIssueParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: r.IssueID}); err == nil {
		sum, err := q.SumIssueLineValues(ctx, inventorydb.SumIssueLineValuesParams{PropertyID: propertyID, IssueID: r.IssueID})
		if err != nil {
			return err
		}
		return q.SetAmenityRunTotal(ctx, inventorydb.SetAmenityRunTotalParams{TenantID: p.TenantID, PropertyID: propertyID, ID: run.ID, TotalValue: sum})
	}
	return nil
}

func ptrTime(t time.Time) *time.Time { return &t }

func ptrDec(d decimal.Decimal) *decimal.Decimal { return &d }

// ---------------------------------------------------------------------------------------------- the runs

// AmenityRunType is the rooms and the guests of a room type in a night.
type AmenityRunType struct {
	RoomTypeID   int64  `json:"room_type_id"`
	RoomTypeName string `json:"room_type_name"`
	Rooms        int    `json:"rooms"`
	Guests       int    `json:"guests"`
}

// AmenityRunLine is a line of a run: the rule ("1 x 78 rooms"), the quantity and, once applied, the value.
type AmenityRunLine struct {
	LineNo        int              `json:"line_no"`
	ItemID        int64            `json:"item_id"`
	ItemCode      string           `json:"item_code"`
	ItemName      string           `json:"item_name"`
	StockUnitCode string           `json:"stock_unit_code"`
	RoomTypeID    *int64           `json:"room_type_id"`
	RoomTypeName  *string          `json:"room_type_name"`
	Basis         string           `json:"basis"`
	QtyPer        decimal.Decimal  `json:"qty_per"`
	Multiplier    int              `json:"multiplier"`
	QtyStock      decimal.Decimal  `json:"qty_stock"`
	StockBefore   *decimal.Decimal `json:"stock_before"`
	Value         *decimal.Decimal `json:"value"`
	Status        string           `json:"status"`
	AppliedOn     *civil.Date      `json:"applied_on"`
}

// AmenityRunRoom is a room of a night.
type AmenityRunRoom struct {
	RoomID       int64  `json:"room_id"`
	RoomNumber   string `json:"room_number"`
	RoomTypeName string `json:"room_type_name"`
	Guests       int    `json:"guests"`
	Kind         string `json:"occupancy_kind"`
}

// AmenityJournalLine is a line of the journal the document makes.
type AmenityJournalLine struct {
	DebitCode      string          `json:"debit_code"`
	DebitName      string          `json:"debit_name"`
	CreditCode     string          `json:"credit_code"`
	CreditName     string          `json:"credit_name"`
	DepartmentName *string         `json:"department_name"`
	Amount         decimal.Decimal `json:"amount"`
}

// AmenityRunBrief is a row of the list of runs.
type AmenityRunBrief struct {
	BusinessDate  civil.Date      `json:"business_date"`
	Number        string          `json:"iss_number"`
	Rooms         int             `json:"rooms"`
	Guests        int             `json:"guests"`
	CompRooms     int             `json:"comp_rooms"`
	HouseUseRooms int             `json:"house_use_rooms"`
	TotalValue    decimal.Decimal `json:"total_value"`
	Status        string          `json:"status"`
	PlanVersion   int             `json:"plan_version"`
	HeldLines     int             `json:"held_lines"`
}

// AmenityRun is the amenities document of one night.
type AmenityRun struct {
	AmenityRunBrief
	IssueID        int64                `json:"issue_id"`
	CreatedAt      time.Time            `json:"created_at"`
	ReversalNumber *string              `json:"reversal_number"`
	CostPerRoom    decimal.Decimal      `json:"cost_per_room"`
	Reversible     bool                 `json:"reversible"`
	HeldByCount    *string              `json:"held_by_count"`
	Types          []AmenityRunType     `json:"types"`
	Lines          []AmenityRunLine     `json:"lines"`
	RoomList       []AmenityRunRoom     `json:"room_list"`
	Short          []AmenityRunLine     `json:"short"`
	Journal        []AmenityJournalLine `json:"journal"`
}

// ListAmenityRuns lists the runs of a period, newest first (inventory.view).
func (s *Service) ListAmenityRuns(ctx context.Context, propertyID int64, from, to *civil.Date, limit int) ([]AmenityRunBrief, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListAmenityRuns(ctx, inventorydb.ListAmenityRunsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to, RowLimit: limitOf(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]AmenityRunBrief, 0, len(rows))
	for _, r := range rows {
		out = append(out, AmenityRunBrief{BusinessDate: r.BusinessDate, Number: r.IssNumber, Rooms: int(r.Rooms), Guests: int(r.Guests), CompRooms: int(r.CompRooms), HouseUseRooms: int(r.HouseUseRooms),
			TotalValue: r.TotalValue, Status: r.Status, PlanVersion: int(r.PlanVersion), HeldLines: int(r.HeldLines)})
	}
	return out, nil
}

// GetAmenityRun answers the document of one night (inventory.view): the rooms, the rules and quantities, what the shelf held, the journal.
func (s *Service) GetAmenityRun(ctx context.Context, propertyID int64, date civil.Date) (AmenityRun, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return AmenityRun{}, err
	}
	q := s.q(ctx)
	r, err := q.GetAmenityRunByDate(ctx, inventorydb.GetAmenityRunByDateParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: date})
	if err != nil {
		return AmenityRun{}, orNotFound(err, notFound("AMENITY_RUN_NOT_FOUND", "amenities document"))
	}
	held, err := q.CountHeldLinesOfIssue(ctx, inventorydb.CountHeldLinesOfIssueParams{PropertyID: propertyID, IssueID: r.IssueID})
	if err != nil {
		return AmenityRun{}, err
	}
	out := AmenityRun{AmenityRunBrief: AmenityRunBrief{BusinessDate: r.BusinessDate, Number: r.IssNumber, Rooms: int(r.Rooms), Guests: int(r.Guests), CompRooms: int(r.CompRooms), HouseUseRooms: int(r.HouseUseRooms),
		TotalValue: r.TotalValue, Status: r.Status, PlanVersion: int(r.PlanVersion), HeldLines: int(held)}, IssueID: r.IssueID, CreatedAt: r.CreatedAt, ReversalNumber: r.ReversalNumber,
		Types: []AmenityRunType{}, Lines: []AmenityRunLine{}, RoomList: []AmenityRunRoom{}, Short: []AmenityRunLine{}, Journal: []AmenityJournalLine{}}
	out.Reversible = r.Status == IssuePosted && held == 0
	if r.Rooms > 0 {
		out.CostPerRoom = r.TotalValue.Div(decimal.NewFromInt32(r.Rooms)).Round(2)
	}
	rooms, err := q.ListAmenityRunRooms(ctx, inventorydb.ListAmenityRunRoomsParams{TenantID: p.TenantID, PropertyID: propertyID, RunID: r.ID})
	if err != nil {
		return AmenityRun{}, err
	}
	typeAt := map[string]int{}
	for _, rm := range rooms {
		out.RoomList = append(out.RoomList, AmenityRunRoom{RoomID: rm.RoomID, RoomNumber: rm.RoomNumber, RoomTypeName: rm.RoomTypeName, Guests: int(rm.Guests), Kind: rm.OccupancyKind})
		i, ok := typeAt[rm.RoomTypeName]
		if !ok {
			out.Types = append(out.Types, AmenityRunType{RoomTypeID: rm.RoomTypeID, RoomTypeName: rm.RoomTypeName})
			i = len(out.Types) - 1
			typeAt[rm.RoomTypeName] = i
		}
		out.Types[i].Rooms++
		out.Types[i].Guests += int(rm.Guests)
	}
	lines, err := q.ListAmenityRunLines(ctx, inventorydb.ListAmenityRunLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RunID: r.ID})
	if err != nil {
		return AmenityRun{}, err
	}
	for _, l := range lines {
		rl := AmenityRunLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, StockUnitCode: l.StockUnitCode, RoomTypeID: l.RoomTypeID, RoomTypeName: l.RoomTypeName,
			Basis: l.Basis, QtyPer: l.QtyPer, Multiplier: int(l.Multiplier), QtyStock: l.QtyStock, StockBefore: l.StockBefore, Value: l.Value, Status: l.Status, AppliedOn: l.AppliedOn}
		out.Lines = append(out.Lines, rl)
		if l.StockBefore != nil && l.StockBefore.LessThan(l.QtyStock) {
			out.Short = append(out.Short, rl)
		}
	}
	jl, err := q.IssueJournal(ctx, inventorydb.IssueJournalParams{TenantID: p.TenantID, PropertyID: propertyID, IssueID: r.IssueID})
	if err != nil {
		return AmenityRun{}, err
	}
	for _, j := range jl {
		out.Journal = append(out.Journal, AmenityJournalLine{DebitCode: j.DebitCode, DebitName: j.DebitName, CreditCode: j.CreditCode, CreditName: j.CreditName, DepartmentName: j.DepartmentName, Amount: j.Amount})
	}
	if docs, err := q.ListHeldAmenityDocs(ctx, inventorydb.ListHeldAmenityDocsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: s.issueWarehouse(ctx, p, propertyID, r.IssueID)}); err == nil && held > 0 && len(docs) > 0 {
		if w, err := q.GetWarehouseLite(ctx, inventorydb.GetWarehouseLiteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: s.issueWarehouse(ctx, p, propertyID, r.IssueID)}); err == nil {
			out.HeldByCount = w.CntNumber
		}
	}
	return out, nil
}

func (s *Service) issueWarehouse(ctx context.Context, p auth.Principal, propertyID, issueID int64) int64 {
	if i, err := s.q(ctx).GetIssue(ctx, inventorydb.GetIssueParams{TenantID: p.TenantID, PropertyID: propertyID, ID: issueID}); err == nil {
		return i.WarehouseID
	}
	return 0
}

// ReverseAmenityRun reverses the whole document of a night with a reversing document dated the open day (inventory.manage): the goods come back at the cost they were taken, an open shortfall of the night is
// lowered first, and a layer is restored under FIFO. It is whole and once (409 AMENITY_RUN_NOT_REVERSIBLE) and is refused while lines are held by a count (409 AMENITY_RUN_HELD). A partial correction is a
// manual issue.
func (s *Service) ReverseAmenityRun(ctx context.Context, propertyID int64, date civil.Date, reason string) (AmenityRun, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return AmenityRun{}, err
	}
	reason = trimmed(reason)
	if n := len([]rune(reason)); n < 1 || n > 300 {
		return AmenityRun{}, invalid("reversal", fieldErr("reason", "REQUIRED", "say why, 300 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		r, err := q.GetAmenityRunByDate(ctx, inventorydb.GetAmenityRunByDateParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: date})
		if err != nil {
			return orNotFound(err, notFound("AMENITY_RUN_NOT_FOUND", "amenities document"))
		}
		if err := db.LockRows(ctx, db.StockAmenityRuns, db.ForUpdate, propertyID, []int64{r.ID}); err != nil {
			return err
		}
		if r, err = q.GetAmenityRunByDate(ctx, inventorydb.GetAmenityRunByDateParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: date}); err != nil {
			return err
		}
		if r.Status != IssuePosted {
			return apperr.Conflict("AMENITY_RUN_NOT_REVERSIBLE", "the document was reversed already: a reversal is whole and once; correct a part with an issue").WithContext("status", r.Status)
		}
		rid, err := s.reverseIssueDoc(ctx, p, propertyID, bd, r.IssueID, reason, true)
		if err != nil {
			return err
		}
		if err := q.SetAmenityRunReversed(ctx, inventorydb.SetAmenityRunReversedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.ID, ReversalIssueID: &rid}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.amenity_run_reversed", "inv_amenity_run", r.ID, r.IssNumber, map[string]any{"status": IssuePosted},
			map[string]any{"status": IssueReversed, "reason": reason, "reversing_issue_id": rid}))
	})
	if err != nil {
		return AmenityRun{}, err
	}
	return s.GetAmenityRun(ctx, propertyID, date)
}

// ---------------------------------------------------------------------------------------------- the checks of the night audit

// amenityFindings are the findings of the night audit about the amenities and the counts (5.7): the plan that cannot run (a blocker), the amenities that a count holds (a warning), an item that tonight will
// not cover (a warning, never a blocker), a count that is still open (a blocker or a warning by the setting, D6).
func (s *Service) amenityFindings(ctx context.Context, tenantID, propertyID int64, bd civil.Date) (blockers, warnings []Finding, err error) {
	p := auth.Principal{TenantID: tenantID}
	cfg, err := s.settingsOrDefault(ctx, p, propertyID)
	if err != nil {
		return nil, nil, err
	}
	counts, err := s.q(ctx).ListOpenCounts(ctx, inventorydb.ListOpenCountsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, nil, err
	}
	for _, c := range counts {
		f := Finding{Code: FindCountOpen, Ref: c.CntNumber, Message: fmt.Sprintf("the count %s of %s is still open: it freezes the warehouse", c.CntNumber, c.WarehouseName)}
		if cfg.CountBlocksNightAudit {
			blockers = append(blockers, f)
		} else {
			f.Message += "; what the day makes for this warehouse waits until the count ends"
			warnings = append(warnings, f)
		}
	}
	pv, err := s.amenityPreview(ctx, p, propertyID, bd)
	if err != nil || !pv.Enabled {
		return blockers, warnings, err
	}
	for _, why := range pv.Invalid {
		blockers = append(blockers, Finding{Code: FindAmenityPlanInvalid, Message: "the amenities plan cannot run tonight: " + why})
	}
	if pv.HeldCount != nil && pv.Rooms > 0 {
		warnings = append(warnings, Finding{Code: FindAmenityHeld, Ref: *pv.HeldCount, Message: fmt.Sprintf("the amenities warehouse is frozen by the count %s: tonight's amenities are written and applied when the count ends", *pv.HeldCount)})
	}
	sort.Slice(pv.Short, func(i, j int) bool { return pv.Short[i].ItemCode < pv.Short[j].ItemCode })
	for _, sh := range pv.Short {
		warnings = append(warnings, Finding{Code: FindAmenityShort, Ref: sh.ItemCode, Message: fmt.Sprintf("%s: %s %s needed tonight, %s on the shelf: the close goes on and the balance goes below zero", sh.ItemName, sh.Need, sh.StockUnitCode, sh.OnHand)})
	}
	return blockers, warnings, nil
}

var _ = slices.Contains[[]string]
