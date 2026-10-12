package subscription

import "strings"

// propertyPrefix is the prefix of every property-scoped route.
const propertyPrefix = "/api/v1/properties/{propertyId}/"

// moduleRules classify the property-scoped routes by the path that follows propertyPrefix. The longest
// matching prefix wins; a prefix matches a whole segment ("tax" matches "tax" and "tax/…", not "taxes").
var moduleRules = []struct {
	prefix string
	module Module
}{
	// Housekeeping beyond the status of one room: the board and a single change stay CORE (check-in needs a clean room).
	{"housekeeping/bulk", Housekeeping},
	{"housekeeping/tasks", Housekeeping},
	{"maintenance-requests", Housekeeping},
	{"maintenance-staff", Housekeeping},
	{"lost-found", Housekeeping},
	{"reports/housekeeping-productivity", Housekeeping},
	{"reports/housekeeping-dirty-rooms", Housekeeping},
	{"reports/maintenance", Housekeeping},

	// Back office.
	{"accounting", Accounting},
	{"payables", Accounting},
	{"bank", Accounting},
	{"tax", Accounting},
	{"budgets", Accounting},
	{"departments", Accounting},

	// Front office, billing, setup, end of day and reports: CORE.
	{"housekeeping", Core},
	{"rooms", Core},
	{"room-types", Core},
	{"bed-types", Core},
	{"room-blocks", Core},
	{"business-date", Core},
	{"business-days", Core},
	{"audit-logs", Core},
	{"charge-codes", Core},
	{"charge-calculations", Core},
	{"taxes", Core},
	{"service-charges", Core},
	{"rate-plans", Core},
	{"rates", Core},
	{"rate-quotes", Core},
	{"rate-restrictions", Core},
	{"yield-rules", Core},
	{"availability", Core},
	{"free-night-quotas", Core},
	{"tape-chart", Core},
	{"reservations", Core},
	{"arrivals", Core},
	{"walk-ins", Core},
	{"stays", Core},
	{"groups", Core},
	{"folios", Core},
	{"folio-items", Core},
	{"payments", Core},
	{"cashier", Core},
	{"night-audit", Core},
	{"companies", Core},
	{"city-ledger", Core},
	{"reports", Core},
	{"dashboard", Core},
}

// tenantCore are the tenant-level routes, all CORE.
var tenantCore = []string{
	"/api/v1/auth/", "/api/v1/users", "/api/v1/roles", "/api/v1/permissions", "/api/v1/properties",
	"/api/v1/guests", "/api/v1/audit-logs", "/api/v1/subscription",
}

// ModuleOf returns the module of a route pattern as the mux reports it ("GET /api/v1/…" or a path),
// and false when the route is not classified. TestEveryRouteHasAModule keeps every route classified.
func ModuleOf(pattern string) (Module, bool) {
	path := pattern
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		path = pattern[i+1:]
	}
	if rest, ok := strings.CutPrefix(path, propertyPrefix); ok {
		best, found := "", Module("")
		for _, r := range moduleRules {
			if (rest == r.prefix || strings.HasPrefix(rest, r.prefix+"/")) && len(r.prefix) > len(best) {
				best, found = r.prefix, r.module
			}
		}
		return found, found != ""
	}
	if path == "/api/v1/properties/{propertyId}" {
		return Core, true
	}
	for _, p := range tenantCore {
		if path == strings.TrimSuffix(p, "/") || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
			return Core, true
		}
	}
	return "", false
}
