package payables

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/payables/payablesdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// Aging is what the hotel owes its suppliers as of a business date, by supplier and by how late each bill is (days past
// the due date). A past date reproduces what was owed then: payments and voids made after it are ignored (payables.view).
func (s *Service) Aging(ctx context.Context, propertyID int64, asOf *civil.Date) (Aging, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return Aging{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Aging{}, err
	}
	at := day.BusinessDate
	if asOf != nil {
		if asOf.After(day.BusinessDate) {
			return Aging{}, apperr.Invalid("the date is invalid", fieldErr("as_of", "IN_THE_FUTURE", "not after the current business date"))
		}
		at = *asOf
	}
	rows, err := s.q(ctx).AgingRows(ctx, payablesdb.AgingRowsParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return Aging{}, err
	}
	zero := func() map[string]decimal.Decimal {
		m := map[string]decimal.Decimal{}
		for _, b := range agingBuckets {
			m[b] = decimal.Zero
		}
		return m
	}
	out := Aging{AsOf: at, Suppliers: []AgingSupplier{}, Buckets: zero()}
	idx := map[int64]int{}
	for _, r := range rows {
		if !r.Outstanding.IsPositive() {
			continue
		}
		i, ok := idx[r.SupplierID]
		if !ok {
			i = len(out.Suppliers)
			idx[r.SupplierID] = i
			out.Suppliers = append(out.Suppliers, AgingSupplier{SupplierID: r.SupplierID, SupplierCode: r.SupplierCode, SupplierName: r.SupplierName, Buckets: zero(), Bills: []AgingBill{}, Credits: []AgingCredit{}})
		}
		over := r.DueDate.DaysUntil(at)
		b := bucketOf(over)
		sup := &out.Suppliers[i]
		sup.Buckets[b] = sup.Buckets[b].Add(r.Outstanding)
		sup.Total = sup.Total.Add(r.Outstanding)
		out.Buckets[b] = out.Buckets[b].Add(r.Outstanding)
		out.Total = out.Total.Add(r.Outstanding)
		if over < 0 {
			over = 0
		}
		sup.Bills = append(sup.Bills, AgingBill{BillID: r.ID, BillNumber: r.BillNumber, SupplierInvoiceNumber: r.SupplierInvoiceNumber, BillDate: r.BillDate, DueDate: r.DueDate, DaysOverdue: over, Outstanding: r.Outstanding})
	}
	if err := s.agingCredits(ctx, p.TenantID, propertyID, at, &out); err != nil {
		return Aging{}, err
	}
	return out, nil
}
