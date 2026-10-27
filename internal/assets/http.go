package assets

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the fixed asset API (docs/architecture/37-fixed-assets.md 8). Every route is of the INVENTORY module of the plan (A17). The PDF of the labels is served by the documents.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/assets"
	mux.Handle("GET "+p+"/settings", httpx.HandlerFunc(h.getSettings))
	mux.Handle("PUT "+p+"/settings", httpx.HandlerFunc(h.putSettings))
	mux.Handle("GET "+p+"/fiscal-groups", httpx.HandlerFunc(h.listFiscalGroups))
	mux.Handle("PUT "+p+"/fiscal-groups/{code}", httpx.HandlerFunc(h.putFiscalGroup))
	mux.Handle("GET "+p+"/categories", httpx.HandlerFunc(h.listCategories))
	mux.Handle("POST "+p+"/categories", httpx.HandlerFunc(h.createCategory))
	mux.Handle("PATCH "+p+"/categories/{id}", httpx.HandlerFunc(h.updateCategory))
	mux.Handle("GET "+p+"/locations", httpx.HandlerFunc(h.listLocations))
	mux.Handle("POST "+p+"/locations", httpx.HandlerFunc(h.createLocation))
	mux.Handle("POST "+p+"/locations/ensure", httpx.HandlerFunc(h.ensureLocation))
	mux.Handle("PATCH "+p+"/locations/{id}", httpx.HandlerFunc(h.updateLocation))
	mux.Handle("GET "+p+"/lookup", httpx.HandlerFunc(h.lookup))
	mux.Handle("GET "+p+"/accounts", httpx.HandlerFunc(h.listAccounts))
	mux.Handle("GET "+p+"/setup-check", httpx.HandlerFunc(h.setupCheck))
	mux.Handle("POST "+p+"/rebuild-state", httpx.HandlerFunc(h.rebuildState))
	mux.Handle("GET "+p+"/opening/imports", httpx.HandlerFunc(h.listImports))
	mux.Handle("POST "+p+"/opening/import", httpx.HandlerFunc(h.importOpening))
	mux.Handle("POST "+p+"/opening/single", httpx.HandlerFunc(h.importSingle))
	mux.Handle("GET "+p+"/waiting", httpx.HandlerFunc(h.listWaiting))
	mux.Handle("POST "+p+"/waiting/dismiss", httpx.HandlerFunc(h.dismissLine))
	mux.Handle("POST "+p+"/waiting/restore", httpx.HandlerFunc(h.restoreLine))
	mux.Handle("POST "+p+"/from-line", httpx.HandlerFunc(h.makeAsset))
	mux.Handle("GET "+p+"/depreciation", httpx.HandlerFunc(h.listDepreciation))
	mux.Handle("GET "+p+"/depreciation/{month}/preview", httpx.HandlerFunc(h.previewDepreciation))
	mux.Handle("POST "+p+"/depreciation/{month}/run", httpx.HandlerFunc(h.runDepreciation))
	mux.Handle("POST "+p+"/depreciation/{month}/reverse", httpx.HandlerFunc(h.reverseDepreciation))
	mux.Handle("GET "+p, httpx.HandlerFunc(h.listAssets))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.getAsset))
	mux.Handle("PATCH "+p+"/{id}", httpx.HandlerFunc(h.updateAsset))
	mux.Handle("POST "+p+"/{id}/cancel", httpx.HandlerFunc(h.cancelAsset))
	mux.Handle("GET "+p+"/{id}/schedule", httpx.HandlerFunc(h.assetSchedule))
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return id, nil
}

func idsOf(r *http.Request) (int64, int64, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, 0, err
	}
	id, err := pathID(r)
	return pid, id, err
}

func queryInt(r *http.Request, name string) (*int64, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return nil, apperr.Invalid("the filter is invalid", fieldErr(name, "INVALID_VALUE", "a whole number"))
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

func list[T any](w http.ResponseWriter, items []T, err error) error {
	if err != nil {
		return err
	}
	if items == nil {
		items = []T{}
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[T]{Data: items})
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
	var in SettingsInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.UpdateSettings(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listFiscalGroups(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.ListFiscalGroups(r.Context(), pid)
	return list(w, v, err)
}

func (h *Handler) putFiscalGroup(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in FiscalGroupInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.UpdateFiscalGroup(r.Context(), pid, r.PathValue("code"), in)
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
	v, err := h.svc.ListCategories(r.Context(), pid)
	return list(w, v, err)
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in CategoryInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
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
	var in CategoryInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.UpdateCategory(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listLocations(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	v, err := h.svc.ListLocations(r.Context(), pid, strings.ToUpper(r.URL.Query().Get("kind")), active)
	return list(w, v, err)
}

func (h *Handler) createLocation(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in LocationInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.CreateLocation(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) ensureLocation(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in EnsureLocationInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.EnsureLocation(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) updateLocation(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var in LocationPatch
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.UpdateLocation(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.LookupAssets(r.Context(), pid, r.URL.Query().Get("q"))
	return list(w, v, err)
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.ListAccounts(r.Context(), pid, strings.ToUpper(r.URL.Query().Get("type")))
	return list(w, v, err)
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

func (h *Handler) rebuildState(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.RebuildState(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listImports(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.ListOpeningImports(r.Context(), pid)
	return list(w, v, err)
}

// openingBody is the request of an import: the file as text (or rows), the month end and the mode. dry_run may also come as the query parameter.
func (h *Handler) openingInput(w http.ResponseWriter, r *http.Request) (int64, OpeningInput, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, OpeningInput{}, err
	}
	var in OpeningInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return 0, in, err
	}
	if b, err := queryBool(r, "dry_run"); err != nil {
		return 0, in, err
	} else if b != nil && *b {
		in.DryRun = true
	}
	in.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	return pid, in, nil
}

func (h *Handler) importOpening(w http.ResponseWriter, r *http.Request) error {
	pid, in, err := h.openingInput(w, r)
	if err != nil {
		return err
	}
	res, err := h.svc.ImportOpening(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, openingStatus(res), res)
}

// importSingle is the form "add an existing asset": one row, the same service as the file.
func (h *Handler) importSingle(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var body struct {
		AsOf        civil.Date  `json:"as_of"`
		JournalDate *civil.Date `json:"journal_date"`
		PostJournal *bool       `json:"post_journal"`
		DryRun      bool        `json:"dry_run"`
		Row         OpeningRow  `json:"row"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		return err
	}
	in := OpeningInput{AsOf: body.AsOf, JournalDate: body.JournalDate, PostJournal: body.PostJournal, DryRun: body.DryRun, Rows: []OpeningRow{body.Row}, IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key"))}
	if b, err := queryBool(r, "dry_run"); err != nil {
		return err
	} else if b != nil && *b {
		in.DryRun = true
	}
	res, err := h.svc.ImportOpening(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, openingStatus(res), res)
}

func openingStatus(res OpeningResult) int {
	if res.DryRun || res.Replayed {
		return http.StatusOK
	}
	return http.StatusCreated
}

func (h *Handler) listAssets(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := AssetFilter{Status: strings.ToUpper(q.Get("status")), Q: q.Get("q")}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	if f.LocationID, err = queryInt(r, "location_id"); err != nil {
		return err
	}
	if f.DepartmentID, err = queryInt(r, "department_id"); err != nil {
		return err
	}
	if f.WarrantyBefore, err = queryDate(r, "warranty_before"); err != nil {
		return err
	}
	if f.FullyDepreciated, err = queryBool(r, "fully_depreciated"); err != nil {
		return err
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
	if q.Get("format") == "csv" {
		rows, err := h.svc.ExportAssets(r.Context(), pid, f)
		if err != nil {
			return err
		}
		return writeRegisterCSV(w, r, rows)
	}
	v, err := h.svc.ListAssets(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func dateStr(d *civil.Date) string {
	if d == nil {
		return ""
	}
	return d.String()
}

func writeRegisterCSV(w http.ResponseWriter, r *http.Request, rows []Asset) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="asset-register.csv"`)
	cw := csv.NewWriter(w)
	head := csvlang.Header(r, []string{"number", "name", "category", "location", "department", "supplier", "acquisition_date", "in_service_date", "cost", "residual_value", "accumulated_depreciation",
		"carrying_value", "life_months", "months_done", "method", "status", "serial_no", "warranty_until"})
	if err := cw.Write(head); err != nil {
		return err
	}
	for _, a := range rows {
		life := ""
		if a.LifeMonths != nil {
			life = strconv.Itoa(int(*a.LifeMonths))
		}
		if err := cw.Write([]string{a.Number, a.Name, a.CategoryCode, str(a.LocationCode), str(a.DepartmentCode), str(a.SupplierName), a.AcquisitionDate.String(), a.InServiceDate.String(),
			a.Cost.String(), a.ResidualValue.String(), a.AccumDepr.String(), a.CarryingValue.String(), life, strconv.Itoa(int(a.MonthsDone)), str(a.Method), a.Status, str(a.SerialNo),
			dateStr(a.WarrantyUntil)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func (h *Handler) getAsset(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetAsset(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) updateAsset(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var in AssetPatch
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.UpdateAsset(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) cancelAsset(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var in CancelInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.CancelAsset(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listWaiting(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := WaitingFilter{Source: strings.ToUpper(strings.TrimSpace(q.Get("source"))), Q: q.Get("q")}
	if f.CategoryID, err = queryInt(r, "category_id"); err != nil {
		return err
	}
	if f.DateFrom, err = queryDate(r, "date_from"); err != nil {
		return err
	}
	if f.DateTo, err = queryDate(r, "date_to"); err != nil {
		return err
	}
	if b, err := queryBool(r, "dismissed"); err != nil {
		return err
	} else if b != nil {
		f.Dismissed = *b
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
	v, err := h.svc.ListWaiting(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) makeAsset(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in MakeAssetInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	in.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	res, err := h.svc.MakeAssetFromLine(r.Context(), pid, in)
	if err != nil {
		return err
	}
	if res.Replayed {
		return httpx.WriteJSON(w, http.StatusOK, res)
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) dismissLine(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in DismissInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := h.svc.DismissLine(r.Context(), pid, in); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) restoreLine(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in struct {
		JournalLineID int64 `json:"journal_line_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := h.svc.RestoreLine(r.Context(), pid, in.JournalLineID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) listDepreciation(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.ListDepreciation(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func monthOf(r *http.Request) (int64, civil.Date, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, civil.Date{}, err
	}
	m, err := ParseMonth(r.PathValue("month"))
	return pid, m, err
}

func (h *Handler) previewDepreciation(w http.ResponseWriter, r *http.Request) error {
	pid, m, err := monthOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.PreviewDepreciation(r.Context(), pid, m)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) runDepreciation(w http.ResponseWriter, r *http.Request) error {
	pid, m, err := monthOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.RunDepreciation(r.Context(), pid, m, strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		return err
	}
	if v.Replayed {
		return httpx.WriteJSON(w, http.StatusOK, v)
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) reverseDepreciation(w http.ResponseWriter, r *http.Request) error {
	pid, m, err := monthOf(r)
	if err != nil {
		return err
	}
	var in ReverseRunInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.ReverseDepreciation(r.Context(), pid, m, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) assetSchedule(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.AssetSchedule(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}
