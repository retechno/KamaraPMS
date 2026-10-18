package app

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/inventory"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos"
	"kamarapms/internal/subscription"
)

// stockBooks is what the accounting asks of the inventory: the lines of the day close, the stock value by account and the accounts only the inventory posts to (so that internal/inventory
// does not import internal/accounting).
type stockBooks struct{ inv *inventory.Service }

func (b stockBooks) StockDayLines(ctx context.Context, tenantID, propertyID int64, bd civil.Date) ([]accounting.StockDayLine, error) {
	lines, err := b.inv.StockDayLines(ctx, tenantID, propertyID, bd)
	out := make([]accounting.StockDayLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, accounting.StockDayLine{AccountID: l.AccountID, DepartmentID: l.DepartmentID, SourceType: l.SourceType, SourceRef: l.SourceRef, Amount: l.Amount})
	}
	return out, err
}

func (b stockBooks) StockValueByAccount(ctx context.Context, tenantID, propertyID int64, asOf civil.Date) (map[int64]decimal.Decimal, error) {
	return b.inv.StockValueByAccount(ctx, tenantID, propertyID, asOf)
}

func (b stockBooks) ControlAccountIDs(ctx context.Context, tenantID, propertyID int64) ([]int64, error) {
	return b.inv.ControlAccountIDs(ctx, tenantID, propertyID)
}

// stockClose is what the night audit asks of the inventory: its findings and its step of the close.
type stockClose struct{ inv *inventory.Service }

func issues(list []inventory.Finding) []nightaudit.OutletIssue {
	var out []nightaudit.OutletIssue
	for _, f := range list {
		out = append(out, nightaudit.OutletIssue{Code: f.Code, Message: f.Message, Ref: f.Ref, Params: f.Params})
	}
	return out
}

func (c stockClose) NightAuditStock(ctx context.Context, tenantID, propertyID int64, bd civil.Date) (blockers, warnings []nightaudit.OutletIssue, err error) {
	b, w, err := c.inv.NightAuditStock(ctx, tenantID, propertyID, bd)
	return issues(b), issues(w), err
}

func (c stockClose) PrepareClose(ctx context.Context, p auth.Principal, propertyID int64) error {
	return c.inv.PrepareClose(ctx, p, propertyID)
}

func (c stockClose) CloseStock(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	return c.inv.CloseStock(ctx, p, propertyID, bd)
}

// nightOccupancy is what the amenities of the close ask of the front desk: the rooms slept in on a business date, by the rule of the room night (so that inventory does not import the front desk).
type nightOccupancy struct{ fd *frontdesk.Service }

func (n nightOccupancy) NightOccupancy(ctx context.Context, tenantID, propertyID int64, date civil.Date) ([]inventory.NightRoom, error) {
	rooms, err := n.fd.NightOccupancy(ctx, tenantID, propertyID, date)
	out := make([]inventory.NightRoom, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, inventory.NightRoom{RoomID: r.RoomID, RoomNumber: r.RoomNumber, RoomTypeID: r.RoomTypeID, StayID: r.StayID, Guests: r.Guests, Kind: r.Kind})
	}
	return out, err
}

// posStock is what the POS asks of the stock (internal/pos does not import internal/inventory): the hook of the deduction of a sale, gated by the plan (POS and INVENTORY, I12).
type posStock struct {
	inv  *inventory.Service
	subs *subscription.Service
}

func (h posStock) on(ctx context.Context, tenantID int64) (bool, error) {
	acc, err := h.subs.AccessOf(ctx, tenantID)
	if err != nil {
		return false, nil //nolint:nilerr // a tenant with no subscription has no stock module
	}
	return acc.Has(subscription.POS) && acc.Has(subscription.Inventory), nil
}

func (h posStock) QueueSale(ctx context.Context, tenantID, propertyID int64, sale pos.StockSale) error {
	if on, err := h.on(ctx, tenantID); err != nil || !on {
		return err
	}
	in := inventory.PosSale{BillLineID: sale.BillLineID, OutletID: sale.OutletID, ProductID: sale.ProductID, Quantity: sale.Quantity, BusinessDate: sale.BusinessDate}
	for _, o := range sale.Options {
		in.Options = append(in.Options, inventory.PosOption{OptionID: o.OptionID, Qty: o.Qty})
	}
	return h.inv.QueueSale(ctx, tenantID, propertyID, in)
}

func (h posStock) QueueVoid(ctx context.Context, tenantID, propertyID int64, v pos.StockVoid) error {
	if on, err := h.on(ctx, tenantID); err != nil || !on {
		return err
	}
	return h.inv.QueueVoid(ctx, tenantID, propertyID, inventory.PosVoid{BillLineID: v.BillLineID, Waste: v.Waste, BusinessDate: v.BusinessDate})
}

// posPricer answers the recipe's question of the net price of a product at an outlet.
type posPricer struct{ pos *pos.Service }

func (p posPricer) NetPrice(ctx context.Context, tenantID, propertyID, outletID, productID int64) (decimal.Decimal, bool, error) {
	return p.pos.NetPrice(ctx, tenantID, propertyID, outletID, productID)
}

// stockPlan says whether the plan of a tenant has the stock module (the recipe is edited only then).
type stockPlan struct{ subs *subscription.Service }

func (p stockPlan) HasInventory(ctx context.Context, tenantID int64) (bool, error) {
	acc, err := p.subs.AccessOf(ctx, tenantID)
	if err != nil {
		return false, nil //nolint:nilerr // no subscription, no module
	}
	return acc.Has(subscription.Inventory), nil
}
