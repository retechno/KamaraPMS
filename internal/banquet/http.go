package banquet

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the banquet API (docs/architecture/36-banquet.md 8). Every route is of the BANQUET module of the plan.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/banquet"
	mux.Handle("GET "+p+"/settings", httpx.HandlerFunc(h.getSettings))
	mux.Handle("PUT "+p+"/settings", httpx.HandlerFunc(h.putSettings))

	mux.Handle("GET "+p+"/rooms", httpx.HandlerFunc(h.listRooms))
	mux.Handle("POST "+p+"/rooms", httpx.HandlerFunc(h.createRoom))
	mux.Handle("GET "+p+"/rooms/{id}", httpx.HandlerFunc(h.getRoom))
	mux.Handle("PATCH "+p+"/rooms/{id}", httpx.HandlerFunc(h.updateRoom))
	mux.Handle("PUT "+p+"/rooms/{id}/parts", httpx.HandlerFunc(h.setParts))
	mux.Handle("PUT "+p+"/rooms/{id}/setups", httpx.HandlerFunc(h.setSetups))

	mux.Handle("GET "+p+"/event-types", httpx.HandlerFunc(h.listEventTypes))
	mux.Handle("POST "+p+"/event-types", httpx.HandlerFunc(h.createEventType))
	mux.Handle("PATCH "+p+"/event-types/{id}", httpx.HandlerFunc(h.updateEventType))
	mux.Handle("GET "+p+"/reasons", httpx.HandlerFunc(h.listReasons))
	mux.Handle("POST "+p+"/reasons", httpx.HandlerFunc(h.createReason))
	mux.Handle("PATCH "+p+"/reasons/{id}", httpx.HandlerFunc(h.updateReason))
	mux.Handle("GET "+p+"/items", httpx.HandlerFunc(h.listItems))
	mux.Handle("POST "+p+"/items", httpx.HandlerFunc(h.createItem))
	mux.Handle("PATCH "+p+"/items/{id}", httpx.HandlerFunc(h.updateItem))
	mux.Handle("GET "+p+"/packages", httpx.HandlerFunc(h.listPackages))
	mux.Handle("POST "+p+"/packages", httpx.HandlerFunc(h.createPackage))
	mux.Handle("GET "+p+"/packages/{id}", httpx.HandlerFunc(h.getPackage))
	mux.Handle("PATCH "+p+"/packages/{id}", httpx.HandlerFunc(h.updatePackage))

	mux.Handle("GET "+p+"/calendar", httpx.HandlerFunc(h.calendar))
	mux.Handle("GET "+p+"/today", httpx.HandlerFunc(h.today))

	mux.Handle("GET "+p+"/events", httpx.HandlerFunc(h.listEvents))
	mux.Handle("POST "+p+"/events", httpx.HandlerFunc(h.createEvent))
	mux.Handle("GET "+p+"/events/{id}", httpx.HandlerFunc(h.getEvent))
	mux.Handle("PATCH "+p+"/events/{id}", httpx.HandlerFunc(h.updateEvent))
	mux.Handle("POST "+p+"/events/{id}/sessions", httpx.HandlerFunc(h.addSession))
	mux.Handle("PATCH "+p+"/events/{id}/sessions/{sid}", httpx.HandlerFunc(h.updateSession))
	mux.Handle("DELETE "+p+"/events/{id}/sessions/{sid}", httpx.HandlerFunc(h.cancelSession))
	mux.Handle("POST "+p+"/events/{id}/availability", httpx.HandlerFunc(h.availability))
	mux.Handle("POST "+p+"/events/{id}/tentative", httpx.HandlerFunc(h.tentative))
	mux.Handle("POST "+p+"/events/{id}/confirm", httpx.HandlerFunc(h.confirm))
	mux.Handle("POST "+p+"/events/{id}/lost", httpx.HandlerFunc(h.lost))
	mux.Handle("POST "+p+"/events/{id}/cancel", httpx.HandlerFunc(h.cancel))

	mux.Handle("GET "+p+"/notices", httpx.HandlerFunc(h.listNotices))
	mux.Handle("POST "+p+"/notices/seen", httpx.HandlerFunc(h.noticesSeen))

	h.registerMoney(mux)
	h.registerDocs(mux)
}

func pathInt(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return id, nil
}

// idsOf is the property of the path and the resource id.
func idsOf(r *http.Request) (int64, int64, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, 0, err
	}
	id, err := pathInt(r, "id")
	return pid, id, err
}

func queryBool(r *http.Request, name string) (*bool, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, apperr.Invalid("the query is invalid", fieldErr(name, "INVALID_VALUE", "true or false"))
	}
	return &b, nil
}

func queryDate(r *http.Request, name string) (*civil.Date, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	d, err := civil.ParseDate(v)
	if err != nil {
		return nil, apperr.Invalid("the query is invalid", fieldErr(name, "INVALID_FORMAT", "a date, YYYY-MM-DD"))
	}
	return &d, nil
}

func queryID(r *http.Request, name string) (*int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return nil, apperr.Invalid("the query is invalid", fieldErr(name, "INVALID_VALUE", "an id"))
	}
	return &n, nil
}

func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var in T
	err := httpx.DecodeJSON(w, r, &in)
	return in, err
}

func page[T any](w http.ResponseWriter, list []T, err error) error {
	if err != nil {
		return err
	}
	if list == nil {
		list = []T{}
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[T]{Data: list})
}

func ok(w http.ResponseWriter, v any, err error) error {
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func created(w http.ResponseWriter, v any, err error) error {
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

// ---------------------------------------------------------------- settings and masters

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetSettings(r.Context(), pid)
	return ok(w, v, err)
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[SettingsInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.PutSettings(r.Context(), pid, in)
	return ok(w, v, err)
}

func (h *Handler) listRooms(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	list, err := h.svc.ListRooms(r.Context(), pid, active)
	return page(w, list, err)
}

func (h *Handler) createRoom(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[RoomInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateRoom(r.Context(), pid, in)
	return created(w, v, err)
}

func (h *Handler) getRoom(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetRoom(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) updateRoom(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[RoomInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateRoom(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) setParts(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[PartsInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.SetRoomParts(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) setSetups(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[SetupsInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.SetRoomSetups(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) listEventTypes(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	list, err := h.svc.ListEventTypes(r.Context(), pid, active)
	return page(w, list, err)
}

func (h *Handler) createEventType(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[EventTypeInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateEventType(r.Context(), pid, in)
	return created(w, v, err)
}

func (h *Handler) updateEventType(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[EventTypeInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateEventType(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) listReasons(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	var kind *string
	if k := r.URL.Query().Get("kind"); k != "" {
		kind = &k
	}
	list, err := h.svc.ListReasons(r.Context(), pid, kind, active)
	return page(w, list, err)
}

func (h *Handler) createReason(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[ReasonInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateReason(r.Context(), pid, in)
	return created(w, v, err)
}

func (h *Handler) updateReason(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[ReasonInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateReason(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	list, err := h.svc.ListItems(r.Context(), pid, active)
	return page(w, list, err)
}

func (h *Handler) createItem(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[ItemInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateItem(r.Context(), pid, in)
	return created(w, v, err)
}

func (h *Handler) updateItem(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[ItemInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateItem(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) listPackages(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	list, err := h.svc.ListPackages(r.Context(), pid, active)
	return page(w, list, err)
}

func (h *Handler) getPackage(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetPackage(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) createPackage(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[PackageInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreatePackage(r.Context(), pid, in)
	return created(w, v, err)
}

func (h *Handler) updatePackage(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[PackageInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdatePackage(r.Context(), pid, id, in)
	return ok(w, v, err)
}

// ---------------------------------------------------------------- calendar and events

func (h *Handler) calendar(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, err := queryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := queryDate(r, "to")
	if err != nil {
		return err
	}
	v, err := h.svc.Calendar(r.Context(), pid, from, to)
	return ok(w, v, err)
}

func (h *Handler) today(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Today(r.Context(), pid)
	return ok(w, v, err)
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := EventFilter{Query: strings.TrimSpace(q.Get("q"))}
	if s := q.Get("status"); s != "" {
		f.Statuses = strings.Split(s, ",")
	}
	if f.SalesUserID, err = queryID(r, "sales_user_id"); err != nil {
		return err
	}
	if f.EventTypeID, err = queryID(r, "event_type_id"); err != nil {
		return err
	}
	if f.From, err = queryDate(r, "from"); err != nil {
		return err
	}
	if f.To, err = queryDate(r, "to"); err != nil {
		return err
	}
	if b, err := queryBool(r, "option_overdue"); err != nil {
		return err
	} else if b != nil {
		f.OptionOverdue = *b
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			return apperr.Invalid("the query is invalid", fieldErr("limit", "INVALID_VALUE", "a positive number"))
		}
		f.Limit = n
	}
	list, err := h.svc.ListEvents(r.Context(), pid, f)
	return page(w, list, err)
}

func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[EventInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateEvent(r.Context(), pid, in)
	return created(w, v, err)
}

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetEvent(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) updateEvent(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[EventInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateEvent(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) addSession(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[SessionInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.AddSession(r.Context(), pid, id, in)
	return created(w, v, err)
}

func (h *Handler) updateSession(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	sid, err := pathInt(r, "sid")
	if err != nil {
		return err
	}
	in, err := decode[SessionInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateSession(r.Context(), pid, id, sid, in)
	return ok(w, v, err)
}

func (h *Handler) cancelSession(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	sid, err := pathInt(r, "sid")
	if err != nil {
		return err
	}
	v, err := h.svc.CancelSession(r.Context(), pid, id, sid)
	return ok(w, v, err)
}

func (h *Handler) availability(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[AvailabilityInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CheckAvailability(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) tentative(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[TentativeInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.MakeTentative(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decodeOptional[ConfirmInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.Confirm(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) lost(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[CloseInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.MarkLost(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[CloseInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CancelEvent(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) listNotices(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	unseen, err := queryBool(r, "unseen")
	if err != nil {
		return err
	}
	list, err := h.svc.ListNotices(r.Context(), pid, unseen != nil && *unseen)
	return page(w, list, err)
}

func (h *Handler) noticesSeen(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[NoticesSeenInput](w, r)
	if err != nil {
		return err
	}
	n, err := h.svc.MarkNoticesSeen(r.Context(), pid, in.IDs)
	return ok(w, map[string]int64{"marked": n}, err)
}
