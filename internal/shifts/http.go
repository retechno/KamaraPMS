package shifts

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the cashier shift API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/cashier"
	mux.Handle("GET "+p+"/shifts", httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p+"/shifts", httpx.HandlerFunc(h.open))
	mux.Handle("GET "+p+"/shifts/current", httpx.HandlerFunc(h.current))
	mux.Handle("GET "+p+"/shifts/suggested-float", httpx.HandlerFunc(h.suggestedFloat))
	mux.Handle("GET "+p+"/shifts/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("POST "+p+"/shifts/{id}/movements", httpx.HandlerFunc(h.move))
	mux.Handle("POST "+p+"/shifts/{id}/close", httpx.HandlerFunc(h.close))
	mux.Handle("GET "+p+"/settings", httpx.HandlerFunc(h.settings))
	mux.Handle("PUT "+p+"/settings", httpx.HandlerFunc(h.setSettings))
}

func ids(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return propertyID, id, nil
}

type idCursor struct {
	Before int64 `json:"b"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur idCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	var f Filter
	q := r.URL.Query()
	f.Status = strings.TrimSpace(q.Get("status"))
	var fields []apperr.FieldError
	if v := strings.TrimSpace(q.Get("user_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			fields = append(fields, fieldErr("user_id", "INVALID_VALUE", "a user id"))
		} else {
			f.UserID = &id
		}
	}
	for name, dst := range map[string]**civil.Date{"from": &f.From, "to": &f.To} {
		if v := strings.TrimSpace(q.Get(name)); v != "" {
			d, err := civil.ParseDate(v)
			if err != nil {
				fields = append(fields, fieldErr(name, "INVALID_DATE", "a date as YYYY-MM-DD"))
				continue
			}
			*dst = &d
		}
	}
	if len(fields) > 0 {
		return apperr.Invalid("the query is invalid", fields...)
	}
	items, err := h.svc.List(r.Context(), pid, f, cur.Before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Shift]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{Before: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in OpenInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	sh, err := h.svc.Open(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, sh)
}

func (h *Handler) current(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	sh, err := h.svc.Current(r.Context(), pid)
	if err != nil {
		return err
	}
	if sh == nil {
		return httpx.WriteJSON(w, http.StatusOK, struct {
			Shift *Shift `json:"shift"`
		}{})
	}
	return httpx.WriteJSON(w, http.StatusOK, struct {
		Shift *Shift `json:"shift"`
	}{sh})
}

func (h *Handler) suggestedFloat(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	amount, err := h.svc.SuggestedFloat(r.Context(), pid, r.URL.Query().Get("drawer"))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, struct {
		OpeningFloat string `json:"opening_float"`
	}{amount})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	sh, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, sh)
}

func (h *Handler) move(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in MovementInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	sh, err := h.svc.Move(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, sh)
}

func (h *Handler) close(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in CloseInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	sh, err := h.svc.Close(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, sh)
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	s, err := h.svc.GetSettings(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) setSettings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in SettingsInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	s, err := h.svc.UpdateSettings(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, s)
}
