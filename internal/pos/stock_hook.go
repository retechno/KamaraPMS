package pos

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos/posdb"
)

// The POS side of the deduction from stock (docs/architecture/35-inventory.md 5.6). When a line becomes a sale (it is posted to the ledger: sendDrafts, which every send, every payment of a bill that was not sent
// and every reopening goes through) the POS tells the stock in the same transaction; when a sent line is voided it tells the stock how. The stock writes one row of its queue and takes no stock lock, so the
// cashier never waits for it and a sale never fails for a stock reason. The hook is called only for an outlet that has a warehouse; the stock decides whether the plan has the module.

// StockOption is a choice on a line that the recipe may depend on.
type StockOption struct {
	OptionID int64
	Qty      int
}

// StockSale is a line posted as a sale.
type StockSale struct {
	BillLineID   int64
	OutletID     int64
	ProductID    int64
	Quantity     decimal.Decimal
	Options      []StockOption
	BusinessDate civil.Date
}

// StockVoid is a sent line voided: Waste when the kitchen has made it.
type StockVoid struct {
	BillLineID   int64
	Waste        bool
	BusinessDate civil.Date
}

// StockHook is what the POS asks of the stock; internal/app wires it (SetStock).
type StockHook interface {
	QueueSale(ctx context.Context, tenantID, propertyID int64, sale StockSale) error
	QueueVoid(ctx context.Context, tenantID, propertyID int64, v StockVoid) error
}

// SetStock wires the stock. Without it the POS deducts nothing.
func (s *Service) SetStock(h StockHook) { s.stock = h }

// queueSale tells the stock about a line that was just posted.
func (s *Service) queueSale(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, o Outlet, l posdb.ListBillLinesRow) error {
	if s.stock == nil || o.WarehouseID == nil {
		return nil
	}
	opts := decodeOptions(l.Options)
	sale := StockSale{BillLineID: l.ID, OutletID: o.ID, ProductID: l.ProductID, Quantity: l.Quantity, BusinessDate: bd, Options: make([]StockOption, 0, len(opts))}
	for _, op := range opts {
		sale.Options = append(sale.Options, StockOption{OptionID: op.OptionID, Qty: op.Qty})
	}
	return s.stock.QueueSale(ctx, p.TenantID, propertyID, sale)
}

// voidIsWaste says how a void is made good (35-inventory.md 5.6, D5): the food is waste when the ticket of the line is COOKING, READY or SERVED; an outlet that makes no ticket (DIRECT) returns the goods;
// an outlet that prints its tickets never moves them, so its setting "a void after N minutes is waste" decides (off by default).
func (s *Service) voidIsWaste(ctx context.Context, p auth.Principal, propertyID int64, o Outlet, lineID int64, sentAt *time.Time) (bool, error) {
	if o.OrderMode == "DIRECT" {
		return false, nil
	}
	status, err := s.q(ctx).LineTicketStatus(ctx, posdb.LineTicketStatusParams{TenantID: p.TenantID, PropertyID: propertyID, BillLineID: lineID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	switch status {
	case TicketCooking, TicketReady, TicketServed:
		return true, nil
	}
	if o.OrderMode == "PRINT" && o.VoidWasteAfterMinutes > 0 && sentAt != nil {
		return s.clock.Now().Sub(*sentAt) >= time.Duration(o.VoidWasteAfterMinutes)*time.Minute, nil
	}
	return false, nil
}

// queueVoid tells the stock that a sent line was voided.
func (s *Service) queueVoid(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, o Outlet, lineID int64, sentAt *time.Time) error {
	if s.stock == nil || o.WarehouseID == nil {
		return nil
	}
	waste, err := s.voidIsWaste(ctx, p, propertyID, o, lineID, sentAt)
	if err != nil {
		return err
	}
	return s.stock.QueueVoid(ctx, p.TenantID, propertyID, StockVoid{BillLineID: lineID, Waste: waste, BusinessDate: bd})
}

// NetPrice is the net price (before service and tax) of one portion of a product at an outlet, from the price list of the outlet and the one charge engine: the stock reads it for the margin of a
// recipe. It answers false when the outlet does not sell the product. It takes no permission of its own: the stock asks after its own.
func (s *Service) NetPrice(ctx context.Context, tenantID, propertyID, outletID, productID int64) (decimal.Decimal, bool, error) {
	o, err := s.outletOf(ctx, tenantID, propertyID, outletID)
	if err != nil {
		return decimal.Zero, false, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return decimal.Zero, false, err
	}
	sp, err := s.q(ctx).GetSellableProduct(ctx, posdb.GetSellableProductParams{TenantID: tenantID, PropertyID: propertyID, OutletID: o.ID, BusinessDate: day.BusinessDate, ProductID: productID})
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, false, nil
	}
	if err != nil {
		return decimal.Zero, false, err
	}
	if !sp.IsActive || !sp.IsSold || sp.Price == nil {
		return decimal.Zero, false, nil
	}
	prof := sp.RuleProfileID
	if prof == nil {
		pr := o.RuleProfileID
		prof = &pr
	}
	q, err := s.quote(ctx, propertyID, sp.ChargeCodeID, decimal.NewFromInt(1), *sp.Price, decimal.Zero, prof)
	if err != nil {
		return decimal.Zero, false, err
	}
	return q.Net, true, nil
}
