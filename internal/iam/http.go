package iam

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/logging"
)

// RefreshCookie is the httpOnly cookie that carries the refresh token. It is
// scoped to the auth endpoints and never readable by JavaScript.
const RefreshCookie = "pms_refresh"

const refreshCookiePath = "/api/v1/auth"

// Handler exposes the identity and access API.
type Handler struct {
	svc          *Service
	cookieSecure bool
}

// NewHandler returns the iam HTTP handler.
func NewHandler(svc *Service, cookieSecure bool) *Handler {
	return &Handler{svc: svc, cookieSecure: cookieSecure}
}

// Middleware authenticates "Authorization: Bearer <access token>". Requests
// without the header continue unauthenticated (auth.RequireAuthenticated rejects
// them on protected routes); a present but invalid token is rejected here.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			httpx.WriteError(w, r, apperr.Unauthorized("TOKEN_INVALID", "expected Authorization: Bearer <token>"))
			return
		}
		p, err := h.svc.Authenticate(r.Context(), token)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		ctx := auth.WithPrincipal(r.Context(), p)
		ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With("tenant_id", p.TenantID, "user_id", p.UserID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RegisterPublic mounts the endpoints that work without an access token.
func (h *Handler) RegisterPublic(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/auth/login", httpx.HandlerFunc(h.login))
	mux.Handle("POST /api/v1/auth/refresh", httpx.HandlerFunc(h.refresh))
	mux.Handle("POST /api/v1/auth/logout", httpx.HandlerFunc(h.logout))
}

// Register mounts the authenticated endpoints.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/auth/me", httpx.HandlerFunc(h.me))
	mux.Handle("POST /api/v1/auth/password", httpx.HandlerFunc(h.changePassword))
	mux.Handle("GET /api/v1/permissions", httpx.HandlerFunc(h.permissions))

	mux.Handle("GET /api/v1/users", httpx.HandlerFunc(h.listUsers))
	mux.Handle("POST /api/v1/users", httpx.HandlerFunc(h.createUser))
	mux.Handle("GET /api/v1/users/{userId}", httpx.HandlerFunc(h.getUser))
	mux.Handle("PATCH /api/v1/users/{userId}", httpx.HandlerFunc(h.updateUser))
	mux.Handle("PUT /api/v1/users/{userId}/properties", httpx.HandlerFunc(h.replaceGrants))
	mux.Handle("POST /api/v1/users/{userId}/password", httpx.HandlerFunc(h.resetPassword))

	mux.Handle("GET /api/v1/roles", httpx.HandlerFunc(h.listRoles))
	mux.Handle("POST /api/v1/roles", httpx.HandlerFunc(h.createRole))
	mux.Handle("GET /api/v1/roles/{roleId}", httpx.HandlerFunc(h.getRole))
	mux.Handle("PATCH /api/v1/roles/{roleId}", httpx.HandlerFunc(h.updateRole))
}

// ---------------------------------------------------------------------------
// Session endpoints

func (h *Handler) setRefreshCookie(w http.ResponseWriter, s Session) {
	// Secure comes from PMS_COOKIE_SECURE: false only for http://localhost development;
	// config.Load refuses to start production without it.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see above
		Name:     RefreshCookie,
		Value:    s.RefreshToken,
		Path:     refreshCookiePath,
		Expires:  s.RefreshExpiresAt,
		MaxAge:   int(time.Until(s.RefreshExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

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

type loginRequest struct {
	TenantCode string `json:"tenant_code"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	s, err := h.svc.Login(r.Context(), LoginInput{TenantCode: req.TenantCode, Email: req.Email, Password: req.Password, UserAgent: r.UserAgent()})
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

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) error {
	if err := h.svc.Logout(r.Context(), refreshTokenFrom(r)); err != nil {
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

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) error {
	var req changePasswordRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := h.svc.ChangePassword(r.Context(), req.CurrentPassword, req.NewPassword); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) permissions(w http.ResponseWriter, r *http.Request) error {
	if _, err := auth.Require(r.Context()); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": auth.Catalogue})
}

// ---------------------------------------------------------------------------
// Users

func pathID(r *http.Request, name string, notFound func() error) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, notFound()
	}
	return id, nil
}

type userCursor struct {
	AfterID int64 `json:"a"`
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur userCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	users, err := h.svc.ListUsers(r.Context(), cur.AfterID, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[User]{Data: users}
	if len(users) > page.Limit {
		out.Data = users[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(userCursor{AfterID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "userId", userNotFound)
	if err != nil {
		return err
	}
	u, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, u)
}

type createUserRequest struct {
	Email         string       `json:"email"`
	FullName      string       `json:"full_name"`
	Password      string       `json:"password"`
	IsTenantAdmin bool         `json:"is_tenant_admin"`
	Grants        []GrantInput `json:"grants"`
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) error {
	var req createUserRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := h.svc.CreateUser(r.Context(), CreateUserInput(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, u)
}

type updateUserRequest struct {
	FullName      *string `json:"full_name"`
	IsActive      *bool   `json:"is_active"`
	IsTenantAdmin *bool   `json:"is_tenant_admin"`
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "userId", userNotFound)
	if err != nil {
		return err
	}
	var req updateUserRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := h.svc.UpdateUser(r.Context(), id, UserPatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, u)
}

type grantsRequest struct {
	Grants []GrantInput `json:"grants"`
}

func (h *Handler) replaceGrants(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "userId", userNotFound)
	if err != nil {
		return err
	}
	var req grantsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := h.svc.ReplaceGrants(r.Context(), id, req.Grants)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, u)
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "userId", userNotFound)
	if err != nil {
		return err
	}
	var req resetPasswordRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := h.svc.ResetPassword(r.Context(), id, req.Password); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------------------
// Roles

func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) error {
	roles, err := h.svc.ListRoles(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Role]{Data: roles})
}

func (h *Handler) getRole(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "roleId", roleNotFound)
	if err != nil {
		return err
	}
	role, err := h.svc.GetRole(r.Context(), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, role)
}

type roleRequest struct {
	Name        *string            `json:"name"`
	Description *string            `json:"description"`
	Permissions *[]auth.Permission `json:"permissions"`
}

func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) error {
	var req roleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	role, err := h.svc.CreateRole(r.Context(), RoleInput(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, role)
}

func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "roleId", roleNotFound)
	if err != nil {
		return err
	}
	var req roleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	role, err := h.svc.UpdateRole(r.Context(), id, RoleInput(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, role)
}
