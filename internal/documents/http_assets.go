package documents

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/assets"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// registerAssets mounts the PDF of the QR labels of the register (docs/architecture/37-fixed-assets.md 4.7). The literal path wins over assets/{id}.
func (h *Handler) registerAssets(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/assets/labels.pdf", httpx.HandlerFunc(h.assetLabels))
}

// assetLabels serves the labels: ?kind=asset (the default) or place, ?ids=1,2,3, ?location_id=, ?category_id= and ?layout=A4_24|ROLL_50x30.
func (h *Handler) assetLabels(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := assets.LabelFilter{Places: strings.EqualFold(q.Get("kind"), "place")}
	for _, v := range strings.Split(q.Get("ids"), ",") {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			return apperr.BadRequest("INVALID_IDS", "ids must be numbers separated by commas")
		}
		f.IDs = append(f.IDs, id)
	}
	for name, dst := range map[string]**int64{"location_id": &f.LocationID, "category_id": &f.CategoryID} {
		if v := q.Get(name); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil || id < 1 {
				return apperr.BadRequest("INVALID_FILTER", name+" must be a number")
			}
			*dst = &id
		}
	}
	doc, err := h.svc.AssetLabels(langCtx(r), pid, f, q.Get("layout"))
	if err != nil {
		return err
	}
	return writePDF(w, doc)
}
