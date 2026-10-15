package pos

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the POS master data API (docs/architecture/34-pos.md). Every route is of the POS module of the plan.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/pos"
	mux.Handle("GET "+p+"/outlets", httpx.HandlerFunc(h.listOutlets))
	mux.Handle("POST "+p+"/outlets", httpx.HandlerFunc(h.createOutlet))
	mux.Handle("GET "+p+"/outlets/{id}", httpx.HandlerFunc(h.getOutlet))
	mux.Handle("PUT "+p+"/outlets/{id}", httpx.HandlerFunc(h.updateOutlet))
	mux.Handle("GET "+p+"/outlets/{id}/floor-plan", httpx.HandlerFunc(h.floorPlan))
	mux.Handle("POST "+p+"/outlets/{id}/areas", httpx.HandlerFunc(h.createArea))
	mux.Handle("PUT "+p+"/areas/{id}/layout", httpx.HandlerFunc(h.saveLayout))
	mux.Handle("GET "+p+"/outlets/{id}/sold-out", httpx.HandlerFunc(h.listSoldOut))
	mux.Handle("PUT "+p+"/outlets/{id}/sold-out/{productId}", httpx.HandlerFunc(h.markSoldOut))
	mux.Handle("DELETE "+p+"/outlets/{id}/sold-out/{productId}", httpx.HandlerFunc(h.clearSoldOut))

	mux.Handle("GET "+p+"/printers", httpx.HandlerFunc(h.listPrinters))
	mux.Handle("POST "+p+"/printers", httpx.HandlerFunc(h.createPrinter))
	mux.Handle("PUT "+p+"/printers/{id}", httpx.HandlerFunc(h.updatePrinter))
	mux.Handle("GET "+p+"/stations", httpx.HandlerFunc(h.listStations))
	mux.Handle("POST "+p+"/stations", httpx.HandlerFunc(h.createStation))
	mux.Handle("PUT "+p+"/stations/{id}", httpx.HandlerFunc(h.updateStation))
	mux.Handle("GET "+p+"/rule-profiles", httpx.HandlerFunc(h.listProfiles))
	mux.Handle("POST "+p+"/rule-profiles", httpx.HandlerFunc(h.createProfile))
	mux.Handle("PUT "+p+"/rule-profiles/{id}", httpx.HandlerFunc(h.updateProfile))
	mux.Handle("GET "+p+"/price-lists", httpx.HandlerFunc(h.listPriceLists))
	mux.Handle("POST "+p+"/price-lists", httpx.HandlerFunc(h.createPriceList))
	mux.Handle("PUT "+p+"/price-lists/{id}", httpx.HandlerFunc(h.updatePriceList))
	mux.Handle("GET "+p+"/price-lists/{id}/items", httpx.HandlerFunc(h.listPriceItems))
	mux.Handle("PUT "+p+"/price-lists/{id}/items", httpx.HandlerFunc(h.setPriceItems))
	mux.Handle("GET "+p+"/categories", httpx.HandlerFunc(h.listCategories))
	mux.Handle("POST "+p+"/categories", httpx.HandlerFunc(h.createCategory))
	mux.Handle("PUT "+p+"/categories/{id}", httpx.HandlerFunc(h.updateCategory))
	mux.Handle("GET "+p+"/products", httpx.HandlerFunc(h.listProducts))
	mux.Handle("POST "+p+"/products", httpx.HandlerFunc(h.createProduct))
	mux.Handle("POST "+p+"/products/import", httpx.HandlerFunc(h.importProducts))
	mux.Handle("GET "+p+"/products/{id}", httpx.HandlerFunc(h.getProduct))
	mux.Handle("PUT "+p+"/products/{id}", httpx.HandlerFunc(h.updateProduct))
	mux.Handle("GET "+p+"/setup-check", httpx.HandlerFunc(h.setupCheck))
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

// do reads the property and the body, calls fn and writes its answer (200, or 201 for a creation).
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

func (h *Handler) listOutlets(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	a, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	list, err := h.svc.ListOutlets(r.Context(), pid, a)
	return page(w, list, err)
}

func (h *Handler) createOutlet(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[OutletInput](w, r)
	if err != nil {
		return err
	}
	o, err := h.svc.CreateOutlet(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, o)
}

func (h *Handler) getOutlet(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	o, err := h.svc.GetOutlet(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, o)
}

func (h *Handler) updateOutlet(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[OutletInput](w, r)
	if err != nil {
		return err
	}
	o, err := h.svc.UpdateOutlet(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, o)
}

func (h *Handler) floorPlan(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	fp, err := h.svc.GetFloorPlan(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, fp)
}

func (h *Handler) createArea(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[AreaInput](w, r)
	if err != nil {
		return err
	}
	a, err := h.svc.CreateArea(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) saveLayout(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[LayoutInput](w, r)
	if err != nil {
		return err
	}
	a, err := h.svc.SaveLayout(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) listSoldOut(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	ids, err := h.svc.ListSoldOut(r.Context(), pid, id)
	return page(w, ids, err)
}

func (h *Handler) soldOut(w http.ResponseWriter, r *http.Request, v bool) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	productID, err := pathInt(r, "productId")
	if err != nil {
		return err
	}
	if err := h.svc.SetSoldOut(r.Context(), pid, id, productID, v); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) markSoldOut(w http.ResponseWriter, r *http.Request) error {
	return h.soldOut(w, r, true)
}
func (h *Handler) clearSoldOut(w http.ResponseWriter, r *http.Request) error {
	return h.soldOut(w, r, false)
}

func (h *Handler) listPrinters(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListPrinters(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) createPrinter(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[PrinterInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreatePrinter(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updatePrinter(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[PrinterInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdatePrinter(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listStations(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListStations(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) createStation(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[StationInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateStation(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updateStation(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[StationInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateStation(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listProfiles(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListRuleProfiles(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) createProfile(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[RuleProfileInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateRuleProfile(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[RuleProfileInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateRuleProfile(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listPriceLists(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListPriceLists(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) createPriceList(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[PriceListInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreatePriceList(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updatePriceList(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[PriceListInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdatePriceList(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listPriceItems(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListPriceItems(r.Context(), pid, id)
	return page(w, list, err)
}

func (h *Handler) setPriceItems(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[struct {
		Items []PriceItemInput `json:"items"`
	}](w, r)
	if err != nil {
		return err
	}
	list, err := h.svc.SetPriceItems(r.Context(), pid, id, in.Items)
	return page(w, list, err)
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

func (h *Handler) listProducts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	a, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	var outlet *int64
	if v := strings.TrimSpace(r.URL.Query().Get("outlet_id")); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || id < 1 {
			return apperr.Invalid("the filter is invalid", fieldErr("outlet_id", "INVALID_VALUE", "an outlet id"))
		}
		outlet = &id
	}
	list, err := h.svc.ListProducts(r.Context(), pid, outlet, a)
	return page(w, list, err)
}

func (h *Handler) getProduct(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetProduct(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) createProduct(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[ProductInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateProduct(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) updateProduct(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[ProductInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateProduct(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

// importProducts takes the CSV as the body (text/csv) or as {"csv": "..."}; ?dry_run=true only checks it.
func (h *Handler) importProducts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	dry, err := queryBool(r, "dry_run")
	if err != nil {
		return err
	}
	var text string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/csv") {
		b, rerr := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if rerr != nil {
			return apperr.Invalid("the CSV cannot be read", fieldErr("csv", "INVALID_FORMAT", "the body is too large or cannot be read"))
		}
		text = string(b)
	} else {
		in, derr := decode[struct {
			CSV string `json:"csv"`
		}](w, r)
		if derr != nil {
			return derr
		}
		text = in.CSV
	}
	res, err := h.svc.ImportProducts(r.Context(), pid, text, dry != nil && *dry)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) setupCheck(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.SetupCheck(r.Context(), pid)
	return page(w, list, err)
}
