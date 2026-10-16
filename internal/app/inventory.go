package app

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/inventory"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
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
		out = append(out, nightaudit.OutletIssue{Code: f.Code, Message: f.Message, Ref: f.Ref})
	}
	return out
}

func (c stockClose) NightAuditStock(ctx context.Context, tenantID, propertyID int64, bd civil.Date) (blockers, warnings []nightaudit.OutletIssue, err error) {
	b, w, err := c.inv.NightAuditStock(ctx, tenantID, propertyID, bd)
	return issues(b), issues(w), err
}

func (c stockClose) CloseStock(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	return c.inv.CloseStock(ctx, p, propertyID, bd)
}
