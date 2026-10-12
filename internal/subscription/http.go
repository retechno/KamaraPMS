package subscription

import (
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/httpx"
)

// Handler exposes the tenant's read-only view of its subscription.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/subscription", httpx.HandlerFunc(h.summary))
	mux.Handle("GET /api/v1/subscription/invoices", httpx.HandlerFunc(h.invoices))
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) error {
	s, err := h.svc.Summary(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) invoices(w http.ResponseWriter, r *http.Request) error {
	inv, err := h.svc.Invoices(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": inv})
}

// AccessHeader carries the access state on every authenticated response, so the UI can show the grace
// and read-only banners without asking.
const AccessHeader = "X-Subscription-Access"

// Gate wraps the authenticated API mux with the module and read-only checks (§3, §4). It resolves the
// route the mux would serve, so the module comes from the pattern, not from the URL.
func (s *Service) Gate(api *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.PrincipalFrom(r.Context())
		if !ok {
			api.ServeHTTP(w, r)
			return
		}
		_, pattern := api.Handler(r)
		module, known := ModuleOf(pattern)
		if !known { // the not-found route, a method mismatch: let the mux answer
			api.ServeHTTP(w, r)
			return
		}
		acc, err := s.AccessOf(r.Context(), p.TenantID)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		w.Header().Set(AccessHeader, string(acc.State))
		if !acc.Has(module) {
			httpx.WriteError(w, r, apperr.Forbidden("MODULE_NOT_IN_PLAN", "the "+strings.ToLower(string(module))+" module is not part of the subscription plan").
				WithContext("module", string(module)))
			return
		}
		if acc.State == AccessReadOnly && !readOnlySafe(r) {
			httpx.WriteError(w, r, apperr.Forbidden("SUBSCRIPTION_READ_ONLY", "the subscription has ended: the data can be read but not changed").
				WithContext("access", string(acc.State)))
			return
		}
		api.ServeHTTP(w, r)
	})
}

// readOnlySafe: reads, and the user's own session and password.
func readOnlySafe(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/auth/")
}
