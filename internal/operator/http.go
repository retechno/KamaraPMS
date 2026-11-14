package operator

import (
	"net/http"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/logging"
)

// RefreshCookie is the httpOnly cookie that carries the operator's refresh token (decision P8). Its path is the operator's auth routes: the hotel's cookie (pms_refresh, path /api/v1/auth)
// and this one never see each other.
const RefreshCookie = "pms_platform_refresh"

const refreshCookiePath = "/api/v1/platform/auth"

// Prefix is where the console API lives.
const Prefix = "/api/v1/platform/"

// Handler exposes the console API: sign-in, enrolment and sessions (doc 39 section 5), and the tenants, plans and invoices (section 7).
type Handler struct {
	svc          *Service
	console      *Console
	cookieSecure bool
}

// NewHandler returns the handler.
func NewHandler(svc *Service, console *Console, cookieSecure bool) *Handler {
	return &Handler{svc: svc, console: console, cookieSecure: cookieSecure}
}

// Handler is the whole console under /api/v1/platform/: the six public routes of sign-in and, behind the operator's authentication, everything else. It is mounted on the outer mux so none of the hotel's
// middleware (iam, the subscription gate) runs on it, and every answer carries Cache-Control: no-store.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	h.registerPublic(mux)
	protected := http.NewServeMux()
	h.registerProtected(protected)
	protected.Handle(Prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, apperr.NotFound("ROUTE_NOT_FOUND", "no such endpoint: "+r.Method+" "+r.URL.Path))
	}))
	mux.Handle(Prefix, h.Middleware(protected))
	return noStore(mux)
}

// Disabled is what answers under /api/v1/platform/ when the console is off (PMS_OPERATOR_TOTP_KEY is not set, decision P1): not a route, whatever the method and the token.
func Disabled() http.Handler {
	return noStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, apperr.NotFound("ROUTE_NOT_FOUND", "no such endpoint: "+r.Method+" "+r.URL.Path))
	}))
}

// noStore says that nothing the console answers may be kept by a cache or by the browser.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Middleware authenticates "Authorization: Bearer <access token>" of an operator. A request without a good token never reaches the console: a missing or a bad token, and the token of a hotel
// user, are 401 PLATFORM_TOKEN_INVALID; an expired one TOKEN_EXPIRED; a revoked session SESSION_REVOKED.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			httpx.WriteError(w, r, errPlatformToken())
			return
		}
		p, err := h.svc.Authenticate(r.Context(), token)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		ctx := auth.WithOperator(r.Context(), p)
		ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With("operator_id", p.OperatorID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// registerPublic mounts the six routes that work without an access token.
func (h *Handler) registerPublic(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/platform/auth/login", httpx.HandlerFunc(h.login))
	mux.Handle("POST /api/v1/platform/auth/totp", httpx.HandlerFunc(h.totp))
	mux.Handle("POST /api/v1/platform/auth/enroll/start", httpx.HandlerFunc(h.enrollStart))
	mux.Handle("POST /api/v1/platform/auth/enroll/confirm", httpx.HandlerFunc(h.enrollConfirm))
	mux.Handle("POST /api/v1/platform/auth/refresh", httpx.HandlerFunc(h.refresh))
	mux.Handle("POST /api/v1/platform/auth/logout", httpx.HandlerFunc(h.logout))
}

// registerProtected mounts what needs an operator.
func (h *Handler) registerProtected(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/platform/auth/me", httpx.HandlerFunc(h.me))
	if h.console != nil {
		h.console.Register(mux)
	}
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, s Session) {
	// Secure comes from PMS_COOKIE_SECURE: false only for http://localhost development; config.Load refuses to start production without it.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see above
		Name:     RefreshCookie,
		Value:    s.RefreshToken,
		Path:     refreshCookiePath,
		Expires:  s.RefreshExpiresAt,
		MaxAge:   refreshCookieMaxAge(s.RefreshExpiresAt, h.svc.clock.Now()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// refreshCookieMaxAge is the seconds left from now (the injected clock) until the refresh token expires.
func refreshCookieMaxAge(expires, now time.Time) int { return int(expires.Sub(now).Seconds()) }

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: RefreshCookie, Value: "", Path: refreshCookiePath, MaxAge: -1, //nolint:gosec // G124: Secure is configuration-driven, see setRefreshCookie
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode})
}

func refreshTokenFrom(r *http.Request) string {
	if c, err := r.Cookie(RefreshCookie); err == nil {
		return c.Value
	}
	return ""
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ch, err := h.svc.Login(r.Context(), LoginInput{Email: req.Email, Password: req.Password})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, ch)
}

func (h *Handler) totp(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Challenge    string `json:"challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	s, err := h.svc.VerifyTOTP(r.Context(), TOTPInput{Challenge: req.Challenge, Code: req.Code, RecoveryCode: req.RecoveryCode, UserAgent: r.UserAgent()})
	if err != nil {
		return err
	}
	h.setRefreshCookie(w, s)
	return httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) enrollStart(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Challenge string `json:"challenge"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := h.svc.EnrollStart(r.Context(), req.Challenge)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) enrollConfirm(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	s, err := h.svc.EnrollConfirm(r.Context(), EnrollConfirmInput{Challenge: req.Challenge, Code: req.Code, UserAgent: r.UserAgent()})
	if err != nil {
		return err
	}
	h.setRefreshCookie(w, s)
	return httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) error {
	s, err := h.svc.Refresh(r.Context(), refreshTokenFrom(r), r.UserAgent())
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Kind == apperr.KindUnauthorized {
			h.clearRefreshCookie(w) // the cookie is dead; stop the browser from sending it
		}
		return err
	}
	h.setRefreshCookie(w, s)
	return httpx.WriteJSON(w, http.StatusOK, s)
}

// logout works with the cookie, or with the bearer token when the browser has lost the cookie: the middleware is not on this route, so the token is read here.
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && token != "" {
		if p, err := h.svc.Authenticate(ctx, token); err == nil {
			ctx = auth.WithOperator(ctx, p)
		}
	}
	if err := h.svc.Logout(ctx, refreshTokenFrom(r)); err != nil {
		return err
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) error {
	me, err := h.svc.Me(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, me)
}
