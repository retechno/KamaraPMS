package banquet

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the documents of an event: the BEO and its revisions, the recipients of its sections, the contract and the completion of an event (36 8, PR 3). The PDFs and the e-mails are served by the
// packages that render and send them (internal/documents, internal/notifications), under the same prefix.
func (h *Handler) registerDocs(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/banquet"
	mux.Handle("GET "+p+"/events/{id}/beo", httpx.HandlerFunc(h.beo))
	mux.Handle("PUT "+p+"/events/{id}/sessions/{sid}/beo", httpx.HandlerFunc(h.saveBeo))
	mux.Handle("POST "+p+"/events/{id}/beo/ack", httpx.HandlerFunc(h.ackBeo))
	mux.Handle("GET "+p+"/events/{id}/beo/revisions/{no}", httpx.HandlerFunc(h.revision))
	mux.Handle("GET "+p+"/beo-recipients", httpx.HandlerFunc(h.listRecipients))
	mux.Handle("PUT "+p+"/beo-recipients", httpx.HandlerFunc(h.putRecipients))
	mux.Handle("GET "+p+"/events/{id}/documents", httpx.HandlerFunc(h.listDocuments))
	mux.Handle("POST "+p+"/events/{id}/documents/contract", httpx.HandlerFunc(h.issueContract))
	mux.Handle("POST "+p+"/events/{id}/complete", httpx.HandlerFunc(h.complete))
}

func (h *Handler) beo(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Beo(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) saveBeo(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	sid, err := pathInt(r, "sid")
	if err != nil {
		return err
	}
	in, err := decode[BeoInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.SaveBeo(r.Context(), pid, id, sid, in)
	return ok(w, v, err)
}

func (h *Handler) ackBeo(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[AckInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.AckBeo(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) revision(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil || no < 1 {
		return apperr.NotFound("REVISION_NOT_FOUND", "the revision does not exist")
	}
	v, err := h.svc.RevisionOf(r.Context(), pid, id, no)
	return ok(w, v, err)
}

func (h *Handler) listRecipients(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListRecipients(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) putRecipients(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[RecipientsInput](w, r)
	if err != nil {
		return err
	}
	list, err := h.svc.PutRecipients(r.Context(), pid, in)
	return page(w, list, err)
}

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	list, err := h.svc.ListDocuments(r.Context(), pid, id)
	return page(w, list, err)
}

func (h *Handler) issueContract(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.IssueContract(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.CompleteEvent(r.Context(), pid, id)
	return ok(w, v, err)
}
