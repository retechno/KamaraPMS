// Package app is the composition root: it wires modules into one HTTP handler.
package app

import (
	"log/slog"
	"net/http"

	"kamarapms/internal/audit"
	"kamarapms/internal/availability"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/expected"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/guests"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/nightaudit"
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
	"kamarapms/internal/tenancy"
)

// Deps are the process-wide dependencies shared by all modules.
type Deps struct {
	Logger    *slog.Logger
	DB        health.Pinger // the pool, for readiness
	TxManager *db.TxManager
	Clock     clock.Clock
	Tokens    iam.TokenConfig
}

// NewHandler builds the API handler with the standard middleware chain:
// request id (outermost, so every log line is correlated) -> access log -> panic recovery.
func NewHandler(d Deps) http.Handler {
	auditWriter := audit.NewWriter(d.Clock)
	authz := iam.NewAuthorizer(d.TxManager)
	iamSvc := iam.NewService(d.TxManager, d.Clock, auditWriter, d.Tokens)
	iamHTTP := iam.NewHandler(iamSvc, d.Tokens.CookieSecure)
	tenancySvc := tenancy.NewService(d.TxManager, d.Clock, auditWriter, authz)
	hkSvc := housekeeping.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	billingSvc := billingconfig.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	tenancySvc.OnPropertyCreated(billingSvc.SeedProperty) // standard charge codes for every new property
	ratesSvc := rates.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	guestsSvc := guests.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc)
	availSvc := availability.NewService(d.TxManager)
	roomsSvc := rooms.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, hkSvc, availSvc)

	foliosSvc := folios.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, billingSvc, iamSvc)
	reservationsSvc := reservations.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, availSvc, ratesSvc, billingSvc, guestsSvc)

	roomChargeSvc := roomcharge.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, expected.NewLoader(d.TxManager), billingSvc, foliosSvc.RoomPoster())
	nightAuditSvc := nightaudit.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, roomChargeSvc, reservationsSvc, hkSvc)
	reportsSvc := reports.NewService(d.TxManager, authz, tenancySvc, nightAuditSvc)
	frontdeskSvc := frontdesk.NewService(d.TxManager, d.Clock, auditWriter, authz, tenancySvc, availSvc, guestsSvc, hkSvc, reservationsSvc, foliosSvc, roomChargeSvc)

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
	reports.NewHandler(reportsSvc).Register(api)
	api.Handle("/api/", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return apperr.NotFound("ROUTE_NOT_FOUND", "no such endpoint: "+r.Method+" "+r.URL.Path)
	}))

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Live())
	mux.Handle("GET /readyz", health.Ready(d.DB))
	iamHTTP.RegisterPublic(mux) // login, refresh, logout: no access token required
	mux.Handle("/api/", iamHTTP.Middleware(auth.RequireAuthenticated(api)))

	return httpx.Chain(mux,
		httpx.RequestID(d.Logger),
		httpx.AccessLog,
		httpx.Recover,
	)
}
