package inventory

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// CardLine is one movement of the stock card with the balance after it.
type CardLine struct {
	MovementID     int64           `json:"movement_id"`
	BusinessDate   civil.Date      `json:"business_date"`
	SourceDate     *civil.Date     `json:"source_date"`
	Kind           string          `json:"kind"`
	SourceType     string          `json:"source_type"`
	DocumentNumber string          `json:"document_number,omitempty"`
	Qty            decimal.Decimal `json:"qty"`
	Value          decimal.Decimal `json:"value"`
	UnitCost       decimal.Decimal `json:"unit_cost"`
	Estimated      bool            `json:"cost_estimated"`
	BalanceQty     decimal.Decimal `json:"balance_qty"`
	BalanceValue   decimal.Decimal `json:"balance_value"`
	AverageCost    decimal.Decimal `json:"average_cost"`
	ReversesID     *int64          `json:"reverses_id"`
}

// Layer is the part of a receipt that is still on hand (FIFO).
type Layer struct {
	ID             int64           `json:"id"`
	ReceivedOn     civil.Date      `json:"received_on"`
	QtyIn          decimal.Decimal `json:"qty_in"`
	QtyRemaining   decimal.Decimal `json:"qty_remaining"`
	UnitCost       decimal.Decimal `json:"unit_cost"`
	ValueRemaining decimal.Decimal `json:"value_remaining"`
}

// Shortfall is stock sold below zero that nobody has settled yet (35-inventory.md 4.4).
type Shortfall struct {
	ID           int64           `json:"id"`
	QtyOpen      decimal.Decimal `json:"qty_open"`
	EstUnitCost  decimal.Decimal `json:"est_unit_cost"`
	EstValueOpen decimal.Decimal `json:"est_value_open"`
	BusinessDate civil.Date      `json:"business_date"`
	SourceType   string          `json:"source_type"`
}

// Card is the stock card of an item in a warehouse: the movements of a period with the running balance, and under FIFO the layers that are left.
type Card struct {
	Item         Item            `json:"item"`
	WarehouseID  int64           `json:"warehouse_id"`
	Warehouse    string          `json:"warehouse"`
	Method       string          `json:"costing_method"`
	From         *civil.Date     `json:"from"`
	To           *civil.Date     `json:"to"`
	OpeningQty   decimal.Decimal `json:"opening_qty"`
	OpeningValue decimal.Decimal `json:"opening_value"`
	Lines        []CardLine      `json:"lines"`
	ClosingQty   decimal.Decimal `json:"closing_qty"`
	ClosingValue decimal.Decimal `json:"closing_value"`
	Layers       []Layer         `json:"layers"`
	Shortfalls   []Shortfall     `json:"shortfalls"`
	Truncated    bool            `json:"truncated"`
}

const maxCardRows = 1000

// StockCard answers the stock card of an item in a warehouse for a period (inventory.view). Without a period it is the whole history.
func (s *Service) StockCard(ctx context.Context, propertyID, warehouseID, itemID int64, from, to *civil.Date) (Card, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return Card{}, err
	}
	if warehouseID < 1 || itemID < 1 {
		return Card{}, apperr.Invalid("the filter is invalid", fieldErr("warehouse", "REQUIRED", "a warehouse and an item"))
	}
	q := s.q(ctx)
	wh, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: warehouseID})
	if err != nil {
		return Card{}, orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
	}
	it, err := s.item(ctx, p, propertyID, itemID)
	if err != nil {
		return Card{}, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return Card{}, apperr.Invalid("the period is invalid", fieldErr("to", "BEFORE_FROM", "not before the start of the period"))
	}
	card := Card{Item: it, WarehouseID: wh.ID, Warehouse: wh.Code, From: from, To: to, Lines: []CardLine{}, Layers: []Layer{}, Shortfalls: []Shortfall{}, Method: MethodAverage}
	if cfg, err := q.GetSettings(ctx, inventorydb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID}); err == nil {
		card.Method = cfg.CostingMethod
	}
	if from != nil {
		o, err := q.StockCardOpening(ctx, inventorydb.StockCardOpeningParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID, ItemID: itemID, FromDate: *from})
		if err != nil {
			return Card{}, err
		}
		card.OpeningQty, card.OpeningValue = o.Qty, o.Value
	}
	rows, err := q.StockCard(ctx, inventorydb.StockCardParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID, ItemID: itemID, FromDate: from, ToDate: to, RowLimit: maxCardRows + 1})
	if err != nil {
		return Card{}, err
	}
	if len(rows) > maxCardRows {
		rows, card.Truncated = rows[:maxCardRows], true
	}
	qty, value := card.OpeningQty, card.OpeningValue
	for _, r := range rows {
		qty, value = qty.Add(r.QtyDelta), value.Add(r.ValueDelta)
		avg := decimal.Zero
		if qty.IsPositive() {
			avg = value.Div(qty).Round(2)
		}
		card.Lines = append(card.Lines, CardLine{MovementID: r.ID, BusinessDate: r.BusinessDate, SourceDate: r.SourceDate, Kind: r.Kind, SourceType: r.SourceType, DocumentNumber: r.DocumentNumber,
			Qty: r.QtyDelta, Value: r.ValueDelta, UnitCost: r.UnitCost, Estimated: r.CostEstimated, BalanceQty: qty, BalanceValue: value, AverageCost: avg, ReversesID: r.ReversesID})
	}
	card.ClosingQty, card.ClosingValue = qty, value
	layers, err := q.ListItemLayers(ctx, inventorydb.ListItemLayersParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID, ItemID: itemID})
	if err != nil {
		return Card{}, err
	}
	for _, l := range layers {
		card.Layers = append(card.Layers, Layer{ID: l.ID, ReceivedOn: l.ReceivedOn, QtyIn: l.QtyIn, QtyRemaining: l.QtyRemaining, UnitCost: UnitCost(l.ValueRemaining, l.QtyRemaining), ValueRemaining: l.ValueRemaining})
	}
	short, err := q.ListOpenShortfallsOf(ctx, inventorydb.ListOpenShortfallsOfParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID, ItemID: itemID})
	if err != nil {
		return Card{}, err
	}
	for _, sf := range short {
		card.Shortfalls = append(card.Shortfalls, Shortfall{ID: sf.ID, QtyOpen: sf.QtyOpen, EstUnitCost: sf.EstUnitCost, EstValueOpen: sf.EstValueOpen, BusinessDate: sf.BusinessDate, SourceType: sf.SourceType})
	}
	return card, nil
}
