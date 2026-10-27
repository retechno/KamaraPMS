// Package app is the composition root: it wires modules into one HTTP handler.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets"
	"kamarapms/internal/audit"
	"kamarapms/internal/auditlog"
	"kamarapms/internal/availability"
	"kamarapms/internal/bankrec"
	"kamarapms/internal/banquet"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/budget"
	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/departments"
	"kamarapms/internal/documents"
	"kamarapms/internal/expected"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/groups"
	"kamarapms/internal/guests"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/inventory"
	"kamarapms/internal/lostfound"
	"kamarapms/internal/maintenance"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/notifications"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/health"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/pos"
	"kamarapms/internal/rates"
	"kamarapms/internal/reports"
	"kamarapms/internal/reservations"
	"kamarapms/internal/roomcharge"
	"kamarapms/internal/roompanel"
	"kamarapms/internal/rooms"
	"kamarapms/internal/shifts"
	"kamarapms/internal/subscription"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/taxinvoice"
	"kamarapms/internal/tenancy"
)

// Deps are the process-wide dependencies shared by all modules.
type Deps struct {
	Logger    *slog.Logger
	DB        health.Pinger // the pool, for readiness
	TxManager *db.TxManager
	Clock     clock.Clock
	Tokens    iam.TokenConfig
	// Mail delivers e-mail; nil turns e-mail off (nothing is queued).
	Mail notifications.Sender
	// RateLimitPerMinute limits requests per client address (0 = off).
	RateLimitPerMinute int
	// TrustedProxies are the reverse proxies whose X-Forwarded-For is believed when the client address is resolved; none means the peer of the connection is the client.
	TrustedProxies []netip.Prefix
	// ReadyChecks are what /readyz asks besides the database (for example that the schema has the migrations of this release).
	ReadyChecks []health.Check
}

// App is the assembled application: the API handler and the background work that goes with it.
type App struct {
	Handler   http.Handler
	notifier  *notifications.Service
	inventory *inventory.Service
	logger    *slog.Logger
}

// Background runs the workers until ctx ends: the deduction of POS sales from stock (a round every few seconds, 35-inventory.md 5.6) next to the e-mail worker, which returns at once when e-mail is off.
func (a *App) Background(ctx context.Context) {
	go a.stockWorker(ctx, 3*time.Second)
	a.notifier.Run(ctx, 15*time.Second)
}

// stockWorker applies the rows of the POS queue: each round takes what is pending. A round that fails is logged and tried again at the next one.
func (a *App) stockWorker(ctx context.Context, every time.Duration) {
	if a.inventory == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := a.inventory.RunQueue(ctx); err != nil && ctx.Err() == nil && a.logger != nil {
				a.logger.Warn("stock queue round failed", "error", err)
			}
		}
	}
}

// NewHandler builds only the API handler (tests that need no background work).
func NewHandler(d Deps) http.Handler { return New(d).Handler }

// New builds the API handler with the standard middleware chain:
// request id (outermost, so every log line is correlated) -> access log -> panic recovery.
func New(d Deps) *App {
	auditWriter := audit.NewWriter(d.Clock)
	authz := iam.NewAuthorizer(d.TxManager)
	iamSvc := iam.NewService(d.TxManager, d.Clock, auditWriter, d.Tokens)
	iamHTTP := iam.NewHandler(iamSvc, d.Tokens.CookieSecure)
	tenancySvc := tenancy.NewService(d.TxManager, d.Clock, auditWriter, authz)
	subscriptionSvc := subscription.NewService(d.TxManager, d.Clock, auditWriter)
	tenancySvc.OnTenantCreated(subscriptionSvc.StartTenant) // a new tenant starts on the default plan
	tenancySvc.SetQuotaGate(subscriptionSvc)                // the plan limits active properties,
	iamSvc.SetQuotaGate(subscriptionSvc)                    // active users,
	hkSvc := housekeeping.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	billingSvc := billingconfig.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	tenancySvc.OnPropertyCreated(billingSvc.SeedProperty) // standard charge codes for every new property
	accountingSvc := accounting.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, iamSvc)
	tenancySvc.OnPropertyCreated(accountingSvc.SeedProperty) // the standard chart of accounts, after the charge codes it maps
	departmentsSvc := departments.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	tenancySvc.OnPropertyCreated(departmentsSvc.SeedProperty) // the standard departments, and the default department of the charge codes
	accountingSvc.SetDepartments(departmentsSvc)              // a journal line or a bill line names its department
	billingSvc.SetDepartments(departmentsSvc)                 // and a charge code its default department
	availSvc := availability.NewService(d.TxManager)
	ratesSvc := rates.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, availSvc)
	guestsSvc := guests.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	roomsSvc := rooms.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, hkSvc, availSvc)
	tenancySvc.OnPropertyCreated(roomsSvc.SeedProperty) // the standard bed types of every new property
	roomsSvc.SetQuotaGate(subscriptionSvc)              // and active rooms

	foliosSvc := folios.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, billingSvc, iamSvc)
	reservationsSvc := reservations.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, availSvc, ratesSvc, billingSvc, guestsSvc)
	reservationsSvc.SetApprover(iamSvc) // a rate override is approved with the credentials of someone who may

	roomChargeSvc := roomcharge.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, expected.NewLoader(d.TxManager, foliosSvc), billingSvc, foliosSvc.RoomPoster())
	nightAuditSvc := nightaudit.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, roomChargeSvc, reservationsSvc, hkSvc)
	nightAuditSvc.SetJournaler(accountingSvc) // the journal of the day is made before the day closes
	reportsSvc := reports.NewService(d.TxManager, authz, tenancySvc, nightAuditSvc)
	frontdeskSvc := frontdesk.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, availSvc, guestsSvc, hkSvc, reservationsSvc, foliosSvc, roomChargeSvc)
	companiesSvc := companies.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	foliosSvc.SetCompanyGate(companiesSvc)
	cityLedgerSvc := cityledger.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, iamSvc, companiesSvc, accountingSvc)
	taxSvc := taxfiling.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, accountingSvc, iamSvc)
	taxInvoiceSvc := taxinvoice.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, iamSvc, taxSvc)
	documentsSvc := documents.NewService(d.Clock, tenancySvc, foliosSvc, frontdeskSvc, reservationsSvc, guestsSvc, cityLedgerSvc, companiesSvc, accountingSvc, taxSvc, taxInvoiceSvc)
	notifierSvc := notifications.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, documentsSvc, reservationsSvc, d.Mail)
	reservationsSvc.SetConfirmedHook(notifierSvc)
	maintenanceSvc := maintenance.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, roomsSvc)
	payablesSvc := payables.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, accountingSvc, iamSvc, taxSvc)
	bankrecSvc := bankrec.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, accountingSvc, iamSvc)
	bankrecSvc.SetTax(taxSvc)
	lostFoundSvc := lostfound.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	groupsSvc := groups.NewService(d.TxManager, auditWriter, authz, tenancySvc)
	shiftsSvc := shifts.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, iamSvc, accountingSvc)
	foliosSvc.SetShiftGate(shiftsSvc) // cash goes through the shift of the cashier
	cityLedgerSvc.SetShiftGate(shiftsSvc)
	nightAuditSvc.SetShiftChecker(shiftsSvc) // and an open shift stops the night audit
	posSvc := pos.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, departmentsSvc)
	posSvc.SetBilling(foliosSvc, iamSvc, billingSvc) // the bills post through the one folio service, with the approvals of iam
	if pool, ok := d.DB.(*pgxpool.Pool); ok {
		posSvc.SetEventPool(pool) // the streams of events wake on NOTIFY (they also read on a timer)
	}
	banquetSvc := banquet.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, foliosSvc, billingSvc, iamSvc)
	documentsSvc.SetBanquet(banquetSvc) // the PDF of the banquet are drawn from it, and the e-mails send them
	notifierSvc.SetBanquet(banquetSvc)
	nightAuditSvc.SetOutletChecker(outletClose{pos: posSvc, folios: foliosSvc, banquet: banquetSvc})
	inventorySvc := inventory.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, departmentsSvc)
	assetsSvc := assets.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, accountingSvc, departmentsSvc)
	accountingSvc.SetAssetSource(assetsSvc)  // the accumulated depreciation accounts of the categories are the register's own (37-fixed-assets.md F5)
	accountingSvc.SetJournalGuard(assetsSvc) // a journal with a line made an asset is not reversed (37-fixed-assets.md F8)
	payablesSvc.SetAssetGuard(assetsSvc)     // nor a bill or a credit note on a line made an asset
	documentsSvc.SetAssets(assetsSvc)
	accountingSvc.SetStockSource(stockBooks{inv: inventorySvc})                 // the journal of the day reads the stock ledger, and the stock accounts are the inventory's own
	nightAuditSvc.SetStockChecker(stockClose{inv: inventorySvc})                // and the close asks the stock whether its books agree
	inventorySvc.SetPayables(payablesSvc)                                       // the bills of the purchasing are posted by the payables, on goods received not invoiced
	inventorySvc.SetApprover(iamSvc)                                            // and a price difference above the tolerance is approved with credentials
	payablesSvc.SetVoidGuard(inventorySvc)                                      // a bill matched to goods received is voided only by unmatching it
	inventorySvc.SetOccupancy(nightOccupancy{fd: frontdeskSvc})                 // the amenities of the close need the rooms that were slept in
	inventorySvc.SetPricer(posPricer{pos: posSvc})                              // the margin of a recipe reads the net price of the POS
	inventorySvc.SetEntitlement(stockPlan{subs: subscriptionSvc})               // and a recipe is edited only when the plan has the module
	posSvc.SetStock(posStock{inv: inventorySvc, subs: subscriptionSvc})         // a POS line that is sold is queued for the deduction from stock, in the same transaction (5.6)
	banquetSvc.SetStock(banquetStock{inv: inventorySvc, subs: subscriptionSvc}) // a posted session queues the deduction of its goods, in the same transaction (36 4.9)
	budgetSvc := budget.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, iamSvc)
	documentsSvc.SetBudget(budgetSvc)
	documentsSvc.SetShifts(shiftsSvc)
	budgetSvc.SetDepartments(departmentsSvc) // and a row of the budget its department

	// Business API: every route requires an authenticated principal.
	api := http.NewServeMux()
	iamHTTP.Register(api)
	tenancy.NewHandler(tenancySvc).Register(api)
	rooms.NewHandler(roomsSvc).Register(api)
	housekeeping.NewHandler(hkSvc).Register(api)
	guests.NewHandler(guestsSvc).Register(api)
	billingconfig.NewHandler(billingSvc).Register(api)
	rates.NewHandler(ratesSvc).Register(api)
	reservations.NewHandler(reservationsSvc).Register(api)
	folios.NewHandler(foliosSvc).Register(api)
	frontdesk.NewHandler(frontdeskSvc).Register(api)
	roomcharge.NewHandler(roomChargeSvc).Register(api)
	nightaudit.NewHandler(nightAuditSvc).Register(api)
	shifts.NewHandler(shiftsSvc).Register(api)
	budget.NewHandler(budgetSvc).Register(api)
	departments.NewHandler(departmentsSvc).Register(api)
	pos.NewHandler(posSvc).Register(api)
	inventory.NewHandler(inventorySvc).Register(api)
	assets.NewHandler(assetsSvc).Register(api)
	banquet.NewHandler(banquetSvc).Register(api)
	reports.NewHandler(reportsSvc).Register(api)
	documents.NewHandler(documentsSvc).Register(api)
	notifications.NewHandler(notifierSvc).Register(api)
	companies.NewHandler(companiesSvc).Register(api)
	cityledger.NewHandler(cityLedgerSvc).Register(api)
	groups.NewHandler(groupsSvc).Register(api)
	maintenance.NewHandler(maintenanceSvc).Register(api)
	accountingHTTP := accounting.NewHandler(accountingSvc)
	accountingHTTP.Register(api)
	accountingHTTP.RegisterJournals(api)
	accountingHTTP.RegisterReports(api)
	payables.NewHandler(payablesSvc).Register(api)
	bankrec.NewHandler(bankrecSvc).Register(api)
	taxfiling.NewHandler(taxSvc).Register(api)
	taxinvoice.NewHandler(taxInvoiceSvc).Register(api)
	lostfound.NewHandler(lostFoundSvc).Register(api)
	auditReader := auditlog.NewReader(d.TxManager, authz)
	auditlog.NewHandler(auditReader).Register(api)
	roompanel.NewHandler(roompanel.NewService(d.TxManager, tenancySvc, hkSvc, frontdeskSvc, reservationsSvc, foliosSvc, auditReader)).Register(api)
	iam.NewRules(iamSvc, authz).RegisterRules(api, tenancy.PropertyID, func(ctx context.Context, propertyID int64) (int32, error) {
		prop, err := tenancySvc.GetProperty(ctx, propertyID)
		return prop.CurrencyDecimals, err
	})
	subscription.NewHandler(subscriptionSvc).Register(api)
	api.Handle("/api/", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return apperr.NotFound("ROUTE_NOT_FOUND", "no such endpoint: "+r.Method+" "+r.URL.Path)
	}))

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Live())
	mux.Handle("GET /readyz", health.Ready(d.DB, d.ReadyChecks...))
	iamHTTP.RegisterPublic(mux) // login, refresh, logout: no access token required
	// The print agents: a token of a device, not of a user. The routes are POS for the module gate and the read-only state (docs/architecture/34-pos.md 10.3).
	mux.Handle("/api/v1/device/", pos.NewDeviceHandler(posSvc, posGate{subscriptionSvc}, d.Clock.Now).Handler())
	// The subscription gate runs after authentication: the module of the route and the read-only state (docs/architecture/26-subscriptions.md).
	mux.Handle("/api/", iamHTTP.Middleware(auth.RequireAuthenticated(subscriptionSvc.Gate(api))))

	return &App{notifier: notifierSvc, inventory: inventorySvc, logger: d.Logger, Handler: httpx.Chain(mux,
		httpx.RequestID(d.Logger, d.TrustedProxies...),
		httpx.SecurityHeaders,
		httpx.AccessLog,
		httpx.Recover,
		httpx.RateLimit(d.RateLimitPerMinute, d.Clock.Now),
	)}
}

// posGate is the subscription gate of the device routes: the POS module must be in the plan, and a read-only subscription refuses what changes something.
type posGate struct{ subs *subscription.Service }

func (g posGate) CheckPOS(ctx context.Context, tenantID int64, write bool) error {
	acc, err := g.subs.AccessOf(ctx, tenantID)
	if err != nil {
		return err
	}
	if !acc.Has(subscription.POS) {
		return apperr.Forbidden("MODULE_NOT_IN_PLAN", "the pos module is not part of the subscription plan").WithContext("module", string(subscription.POS))
	}
	if write && acc.State == subscription.AccessReadOnly {
		return apperr.Forbidden("SUBSCRIPTION_READ_ONLY", "the subscription has ended: the data can be read but not changed").WithContext("access", string(acc.State))
	}
	return nil
}

// outletClose is what the night audit asks of the point of sale: the findings of the open bills and the end of the locks of a day.
type outletClose struct {
	pos     *pos.Service
	folios  *folios.Service
	banquet *banquet.Service
}

func (o outletClose) NightAuditOutlets(ctx context.Context, tenantID, propertyID int64) (blockers, warnings []nightaudit.OutletIssue, err error) {
	b, w, err := o.pos.NightAuditChecks(ctx, tenantID, propertyID)
	for _, i := range b {
		blockers = append(blockers, nightaudit.OutletIssue{Code: i.Code, Message: i.Message, Ref: i.Ref, Params: i.Params})
	}
	for _, i := range w {
		warnings = append(warnings, nightaudit.OutletIssue{Code: i.Code, Message: i.Message, Ref: i.Ref, Params: i.Params})
	}
	if err != nil {
		return blockers, warnings, err
	}
	// the sessions of the banquet the close would not post, or could not
	bb, bw, err := o.banquet.NightAuditChecks(ctx, tenantID, propertyID)
	for _, i := range bb {
		blockers = append(blockers, nightaudit.OutletIssue{Code: i.Code, Message: i.Message, Ref: i.Ref, Params: i.Params})
	}
	for _, i := range bw {
		warnings = append(warnings, nightaudit.OutletIssue{Code: i.Code, Message: i.Message, Ref: i.Ref, Params: i.Params})
	}
	return blockers, warnings, err
}

func (o outletClose) ExpireOutletLocks(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	// the sessions of the banquet are posted after the room charges and before the locks of the outlets end (36 4.5): the folios of the events, then the events
	if _, err := o.banquet.PostDueSessions(ctx, p, propertyID, bd); err != nil {
		return err
	}
	if _, err := o.folios.ExpireOutletLocks(ctx, p, propertyID, bd); err != nil {
		return err
	}
	// the close of the day also removes the old documents of the print jobs of the property (a property with no agent never claims one)
	if _, err := o.pos.PurgePrintPayloadsOf(ctx, propertyID); err != nil {
		return err
	}
	// and the reminders of the options whose date has come (36-banquet.md 4.3), in the transaction of the close
	_, err := o.banquet.WriteDueNotices(ctx, p.TenantID, propertyID, bd)
	return err
}
