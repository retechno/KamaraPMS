// Package entitlement is the small contract between the subscription module and the modules whose
// records a plan limits (properties, rooms, users). Those modules import this package, never
// internal/subscription, so the dependency points one way (docs/architecture/26-subscriptions.md §5).
package entitlement

import "context"

// Resource is something a plan limits.
type Resource string

// The limited resources.
const (
	Properties Resource = "properties"
	Rooms      Resource = "rooms"
	Users      Resource = "users"
)

// Gate checks a tenant's quota inside the caller's transaction.
//
// Hold locks the tenant's subscription row (lock level db.LevelSubscription, before the business day),
// so that a count and the insert that follows it cannot race another one. Call it first in the
// transaction, before any other lock. Check counts what the tenant holds and refuses with 409
// PLAN_LIMIT_REACHED when adding would go over the limit; it takes no lock, so it may run after
// higher-level locks provided Hold ran first. Require is Hold followed by Check.
type Gate interface {
	Hold(ctx context.Context, tenantID int64) error
	Check(ctx context.Context, tenantID int64, r Resource, adding int) error
	Require(ctx context.Context, tenantID int64, r Resource, adding int) error
}

// Unlimited is the gate of a service that has none wired (unit tests, the seed tool).
type Unlimited struct{}

// Hold does nothing.
func (Unlimited) Hold(context.Context, int64) error { return nil }

// Check allows everything.
func (Unlimited) Check(context.Context, int64, Resource, int) error { return nil }

// Require allows everything.
func (Unlimited) Require(context.Context, int64, Resource, int) error { return nil }

// OrUnlimited returns g, or Unlimited when g is nil.
func OrUnlimited(g Gate) Gate {
	if g == nil {
		return Unlimited{}
	}
	return g
}
