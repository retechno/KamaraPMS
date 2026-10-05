// Package app is the composition root: it wires modules into one HTTP handler.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"kamarapms/internal/accounting"
	"kamarapms/internal/audit"
	"kamarapms/internal/auditlog"
	"kamarapms/internal/availability"
	"kamarapms/internal/bankrec"
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
	"kamarapms/internal/lostfound"
	"kamarapms/internal/maintenance"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/notifications"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/health"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/rates"
	"kamarapms/internal/reports"
	"kamarapms/internal/reservations"
	"kamarapms/internal/roomcharge"
	"kamarapms/internal/rooms"
	"kamarapms/internal/shifts"
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
}

// App is the assembled application: the API handler and the background work that goes with it.
type App struct {
	Handler  http.Handler
	notifier *notifications.Service
}

// Background runs the e-mail worker until ctx ends. It returns at once when e-mail is off.
func (a *App) Background(ctx context.Context) { a.notifier.Run(ctx, 15*time.Second) }

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

	foliosSvc := folios.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, billingSvc, iamSvc)
	reservationsSvc := reservations.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, availSvc, ratesSvc, billingSvc, guestsSvc)
	reservationsSvc.SetApprover(iamSvc) // a rate override is approved with the credentials of someone who may

	roomChargeSvc := roomcharge.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, expected.NewLoader(d.TxManager), billingSvc, foliosSvc.RoomPoster())
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
	lostFoundSvc := lostfound.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	groupsSvc := groups.NewService(d.TxManager, auditWriter, authz, tenancySvc)
	shiftsSvc := shifts.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, iamSvc, accountingSvc)
	foliosSvc.SetShiftGate(shiftsSvc) // cash goes through the shift of the cashier
	cityLedgerSvc.SetShiftGate(shiftsSvc)
	nightAuditSvc.SetShiftChecker(shiftsSvc) // and an open shift stops the night audit
	budgetSvc := budget.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, iamSvc)
	documentsSvc.SetBudget(budgetSvc)
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
	auditlog.NewHandler(auditlog.NewReader(d.TxManager, authz)).Register(api)
	api.Handle("/api/", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return apperr.NotFound("ROUTE_NOT_FOUND", "no such endpoint: "+r.Method+" "+r.URL.Path)
	}))

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Live())
	mux.Handle("GET /readyz", health.Ready(d.DB))
	iamHTTP.RegisterPublic(mux) // login, refresh, logout: no access token required
	mux.Handle("/api/", iamHTTP.Middleware(auth.RequireAuthenticated(api)))

	return &App{notifier: notifierSvc, Handler: httpx.Chain(mux,
		httpx.RequestID(d.Logger),
		httpx.AccessLog,
		httpx.Recover,
		httpx.RateLimit(d.RateLimitPerMinute, d.Clock.Now),
	)}
}
