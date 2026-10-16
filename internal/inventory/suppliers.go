package inventory

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
)

// The suppliers as the purchasing sees them: a short list to choose from, and what each supplier sells at what price. The suppliers are the payables' (suppliers are made and edited there); the purchasing reads
// them with its own permissions (35-inventory.md 9.1, document 33: the permission of a screen that uses the data).

// SupplierRef is a supplier to choose.
type SupplierRef struct {
	ID               int64  `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	PaymentTermsDays int    `json:"payment_terms_days"`
	IsActive         bool   `json:"is_active"`
}

// ListSuppliers lists the suppliers of the property (a permission of the purchasing, or of the module).
func (s *Service) ListSuppliers(ctx context.Context, propertyID int64, active *bool) ([]SupplierRef, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListSuppliersLite(ctx, inventorydb.ListSuppliersLiteParams{TenantID: p.TenantID, PropertyID: propertyID, Active: active})
	if err != nil {
		return nil, err
	}
	out := make([]SupplierRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, SupplierRef{ID: r.ID, Code: r.Code, Name: r.Name, PaymentTermsDays: int(r.PaymentTermsDays), IsActive: r.IsActive})
	}
	return out, nil
}

// SupplierItem is what a supplier sells: the last price paid for one purchase unit.
type SupplierItem struct {
	SupplierID     int64           `json:"supplier_id"`
	ItemID         int64           `json:"item_id"`
	ItemCode       string          `json:"item_code"`
	ItemName       string          `json:"item_name"`
	PurchaseUnitID int64           `json:"purchase_unit_id"`
	UnitCode       string          `json:"unit_code"`
	LastPrice      decimal.Decimal `json:"last_price"`
	SupplierSKU    *string         `json:"supplier_sku"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// ListSupplierItems lists what suppliers sell, optionally of one supplier or of one item.
func (s *Service) ListSupplierItems(ctx context.Context, propertyID int64, supplierID, itemID *int64) ([]SupplierItem, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListSupplierItems(ctx, inventorydb.ListSupplierItemsParams{TenantID: p.TenantID, PropertyID: propertyID, SupplierID: supplierID, ItemID: itemID})
	if err != nil {
		return nil, err
	}
	out := make([]SupplierItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, SupplierItem{SupplierID: r.SupplierID, ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, PurchaseUnitID: r.PurchaseUnitID, UnitCode: r.UnitCode, LastPrice: r.LastPrice,
			SupplierSKU: r.SupplierSku, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}
