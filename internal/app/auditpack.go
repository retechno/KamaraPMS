package app

import (
	"context"

	"kamarapms/internal/subscription"
)

// packPlan says whether the plan of a tenant has the point of sale: the section of the outlets of the night audit pack is off without it.
type packPlan struct{ subs *subscription.Service }

func (p packPlan) HasPOS(ctx context.Context, tenantID int64) (bool, error) {
	acc, err := p.subs.AccessOf(ctx, tenantID)
	if err != nil {
		return false, nil //nolint:nilerr // no subscription, no module
	}
	return acc.Has(subscription.POS), nil
}
