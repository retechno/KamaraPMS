package documents

import (
	"context"
	"net/http"
	"strconv"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler serves the documents as PDF (docs/architecture/06-api.md, documents).
type Handler struct{ svc *Service }

// NewHandler returns the documents HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/folios/{id}/invoice.pdf", httpx.HandlerFunc(h.serve(h.svc.Invoice)))
	mux.Handle("GET "+p+"/stays/{id}/registration-card.pdf", httpx.HandlerFunc(h.serve(h.svc.RegistrationCard)))
	mux.Handle("GET "+p+"/payments/{id}/receipt.pdf", httpx.HandlerFunc(h.serve(h.svc.Receipt)))
	mux.Handle("GET "+p+"/reservations/{id}/confirmation.pdf", httpx.HandlerFunc(h.serve(h.svc.Confirmation)))
	mux.Handle("GET "+p+"/companies/{id}/statement.pdf", httpx.HandlerFunc(h.statement))
	mux.Handle("GET "+p+"/city-ledger/invoices/{id}/invoice.pdf", httpx.HandlerFunc(h.serve(h.svc.CompanyInvoice)))
	h.registerAccounting(mux)
}

func (h *Handler) serve(render func(ctx context.Context, propertyID, id int64) (Document, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			return apperr.NotFound("NOT_FOUND", "no such document")
		}
		doc, err := render(r.Context(), pid, id)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}
}

func (h *Handler) statement(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return apperr.NotFound("NOT_FOUND", "no such document")
	}
	from, to, err := cityledger.DateRange(r)
	if err != nil {
		return err
	}
	doc, err := h.svc.CompanyStatement(r.Context(), pid, id, from, to)
	if err != nil {
		return err
	}
	return writePDF(w, doc)
}

func writePDF(w http.ResponseWriter, doc Document) error {
	{
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="`+doc.Filename+`"`)
		w.Header().Set("Cache-Control", "no-store") // financial documents are never cached
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(doc.PDF)
		return err
	}
}
