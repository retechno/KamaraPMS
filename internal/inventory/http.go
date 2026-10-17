package inventory

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the inventory API (docs/architecture/35-inventory.md 10). Every route is of the INVENTORY module of the plan.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/inventory"
	mux.Handle("GET "+p+"/settings", httpx.HandlerFunc(h.getSettings))
	mux.Handle("PUT "+p+"/settings", httpx.HandlerFunc(h.putSettings))
	mux.Handle("GET "+p+"/setup-check", httpx.HandlerFunc(h.setupCheck))
	mux.Handle("GET "+p+"/accounts", httpx.HandlerFunc(h.listAccounts))

	mux.Handle("GET "+p+"/units", httpx.HandlerFunc(h.listUnits))
	mux.Handle("POST "+p+"/units", httpx.HandlerFunc(h.createUnit))
	mux.Handle("PATCH "+p+"/units/{id}", httpx.HandlerFunc(h.updateUnit))
	mux.Handle("GET "+p+"/categories", httpx.HandlerFunc(h.listCategories))
	mux.Handle("POST "+p+"/categories", httpx.HandlerFunc(h.createCategory))
	mux.Handle("PATCH "+p+"/categories/{id}", httpx.HandlerFunc(h.updateCategory))
	mux.Handle("GET "+p+"/items", httpx.HandlerFunc(h.listItems))
	mux.Handle("POST "+p+"/items", httpx.HandlerFunc(h.createItem))
	mux.Handle("GET "+p+"/items/{id}", httpx.HandlerFunc(h.getItem))
	mux.Handle("PATCH "+p+"/items/{id}", httpx.HandlerFunc(h.updateItem))
	mux.Handle("PUT "+p+"/items/{id}/units", httpx.HandlerFunc(h.setItemUnits))
	mux.Handle("GET "+p+"/warehouses", httpx.HandlerFunc(h.listWarehouses))
	mux.Handle("POST "+p+"/warehouses", httpx.HandlerFunc(h.createWarehouse))
	mux.Handle("PATCH "+p+"/warehouses/{id}", httpx.HandlerFunc(h.updateWarehouse))
	mux.Handle("GET "+p+"/warehouses/{id}/levels", httpx.HandlerFunc(h.getLevels))
	mux.Handle("PUT "+p+"/warehouses/{id}/levels", httpx.HandlerFunc(h.setLevels))

	mux.Handle("GET "+p+"/openings", httpx.HandlerFunc(h.listOpenings))
	mux.Handle("POST "+p+"/openings", httpx.HandlerFunc(h.postOpening))
	mux.Handle("GET "+p+"/openings/{id}", httpx.HandlerFunc(h.getOpening))
	mux.Handle("GET "+p+"/stock-card", httpx.HandlerFunc(h.stockCard))
	h.registerPurchasing(mux)
	h.registerMovements(mux)
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
	id, err := pathID(r)
	return pid, id, err
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

func queryInt(r *http.Request, name string) (*int64, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return nil, apperr.Invalid("the filter is invalid", fieldErr(name, "INVALID_VALUE", "a whole number above zero"))
	}
	return &n, nil
}

func queryBool(r *http.Request, name string) (*bool, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, apperr.Invalid("the filter is invalid", fieldErr(name, "INVALID_VALUE", "true or false"))
	}
	return &b, nil
}

func queryDate(r *http.Request, name string) (*civil.Date, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	d, err := civil.ParseDate(v)
	if err != nil {
		return nil, apperr.Invalid("the filter is invalid", fieldErr(name, "INVALID_FORMAT", "YYYY-MM-DD"))
	}
	return &d, nil
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetSettings(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
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
	v, err := h.svc.UpdateSettings(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) setupCheck(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.SetupCheck(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListInventoryAccounts(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) listUnits(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListUnits(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) createUnit(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[UnitInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateUnit(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updateUnit(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[UnitInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateUnit(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListCategories(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[CategoryInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateCategory(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updateCategory(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[CategoryInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateCategory(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	f := ItemFilter{Q: r.URL.Query().Get("q")}
	if f.CategoryID, err = queryInt(r, "category"); err != nil {
		return err
	}
	if f.Warehouse, err = queryInt(r, "warehouse"); err != nil {
		return err
	}
	if f.Active, err = queryBool(r, "active"); err != nil {
		return err
	}
	if b, err := queryBool(r, "below_min"); err != nil {
		return err
	} else if b != nil {
		f.BelowMin = *b
	}
	if n, err := queryInt(r, "limit"); err != nil {
		return err
	} else if n != nil {
		f.Limit = int(*n)
	}
	if n, err := queryInt(r, "offset"); err != nil {
		return err
	} else if n != nil {
		f.Offset = int(*n)
	}
	v, err := h.svc.ListItems(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
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
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) getItem(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetItem(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
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
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) setItemUnits(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[struct {
		Units []ItemUnit `json:"units"`
	}](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.SetItemUnits(r.Context(), pid, id, in.Units)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listWarehouses(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListWarehouses(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) createWarehouse(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[WarehouseInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateWarehouse(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updateWarehouse(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[WarehouseInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateWarehouse(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) getLevels(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	list, err := h.svc.GetLevels(r.Context(), pid, id)
	return page(w, list, err)
}

func (h *Handler) setLevels(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[struct {
		Levels []Level `json:"levels"`
	}](w, r)
	if err != nil {
		return err
	}
	list, err := h.svc.SetLevels(r.Context(), pid, id, in.Levels)
	return page(w, list, err)
}

func (h *Handler) listOpenings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListOpenings(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) postOpening(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[OpeningInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.PostOpening(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) getOpening(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetOpening(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) stockCard(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	wh, err := queryInt(r, "warehouse")
	if err != nil {
		return err
	}
	it, err := queryInt(r, "item")
	if err != nil {
		return err
	}
	var wid, i int64 // the service answers a missing one after it has asked for the permission
	if wh != nil {
		wid = *wh
	}
	if it != nil {
		i = *it
	}
	from, err := queryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := queryDate(r, "to")
	if err != nil {
		return err
	}
	v, err := h.svc.StockCard(r.Context(), pid, wid, i, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}
