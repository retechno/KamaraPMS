package rates

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rates/ratesdb"
)

// BedSupplement is one row of the supplement of a bed type on a rate plan and room type (docs/architecture/16-bed-variants.md).
type BedSupplement struct {
	Kind   string // AMOUNT or PERCENT
	Amount decimal.Decimal
	From   civil.Date
}

// BedSupplements lists the supplements of one bed type of a room type on a plan, oldest first. It reads committed data and takes no locks;
// callers that write snapshots hold their own locks (a plan with no row has no supplement).
func (s *Service) BedSupplements(ctx context.Context, tenantID, propertyID, ratePlanID, roomTypeID, bedTypeID int64) ([]BedSupplement, error) {
	rows, err := s.q(ctx).ListBedSupplements(ctx, ratesdb.ListBedSupplementsParams{
		TenantID: tenantID, PropertyID: propertyID, RatePlanID: ratePlanID, RoomTypeID: roomTypeID, BedTypeID: bedTypeID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]BedSupplement, len(rows))
	for i, r := range rows {
		out[i] = BedSupplement{Kind: r.AdjustKind, Amount: r.Amount, From: r.EffectiveFrom}
	}
	return out, nil
}

// SupplementOn is the supplement in force on a night: the latest row that has started (rows are oldest first). The bool is false
// when none has started, which is the same price.
func SupplementOn(rows []BedSupplement, night civil.Date) (BedSupplement, bool) {
	var found BedSupplement
	ok := false
	for _, r := range rows {
		if r.From.After(night) {
			break
		}
		found, ok = r, true
	}
	return found, ok
}

// BedAdjustmentFor is what a supplement adds to the price a night is sold at (the grid price after the yield rules): the amount,
// or the percentage of that price, rounded to the decimals of the currency (half away from zero). The price never goes below 0, so a
// discount is cut at the price itself.
func BedAdjustmentFor(sold decimal.Decimal, sup BedSupplement, decimals int32) decimal.Decimal {
	adj := sup.Amount
	if sup.Kind == BedAdjustPercent {
		adj = sold.Mul(sup.Amount).Div(decimal.NewFromInt(100))
	}
	adj = adj.Round(decimals)
	if sold.Add(adj).IsNegative() {
		adj = sold.Neg()
	}
	return adj
}
