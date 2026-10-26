package documents

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// registerBanquet mounts the PDF of the banquet: the proposal, the BEO and the contract of an event (docs/architecture/36-banquet.md 4.6).
func (h *Handler) registerBanquet(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/banquet"
	mux.Handle("GET "+p+"/events/{id}/documents/proposal.pdf", httpx.HandlerFunc(h.serve(h.svc.BanquetProposal)))
	mux.Handle("GET "+p+"/events/{id}/documents/contract.pdf", httpx.HandlerFunc(h.serve(h.svc.BanquetContract)))
	mux.Handle("GET "+p+"/events/{id}/documents/beo.pdf", httpx.HandlerFunc(h.beo))
	mux.Handle("GET "+p+"/documents/{id}/file.pdf", httpx.HandlerFunc(h.serve(h.svc.BanquetDocument)))
}

// beo serves the BEO of an event: ?session_id= for one session, ?revision= for the BEO as it was at a revision.
func (h *Handler) beo(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return apperr.NotFound("NOT_FOUND", "no such document")
	}
	var sid int64
	if v := r.URL.Query().Get("session_id"); v != "" {
		if sid, err = strconv.ParseInt(v, 10, 64); err != nil || sid < 1 {
			return apperr.BadRequest("INVALID_SESSION", "session_id must be a number")
		}
	}
	var rev *int
	if v := r.URL.Query().Get("revision"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return apperr.BadRequest("INVALID_REVISION", "revision must be a number")
		}
		rev = &n
	}
	doc, err := h.svc.BanquetBeo(langCtx(r), pid, id, sid, rev)
	if err != nil {
		return err
	}
	return writePDF(w, doc)
}
