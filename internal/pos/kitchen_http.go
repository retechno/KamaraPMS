package pos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// registerKitchen mounts the stream of events, the board of a station and the queue of print jobs (docs/architecture/06-api.md 24.2).
func (h *Handler) registerKitchen(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/pos"
	mux.Handle("GET "+p+"/events", httpx.HandlerFunc(h.events))
	mux.Handle("GET "+p+"/stations/{id}/board", httpx.HandlerFunc(h.stationBoard))
	mux.Handle("POST "+p+"/tickets/{id}/status", httpx.HandlerFunc(h.ticketStatus))
	mux.Handle("GET "+p+"/print-jobs", httpx.HandlerFunc(h.printJobs))
}

func (h *Handler) stationBoard(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.StationBoard(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) ticketStatus(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.AdvanceTicket(r.Context(), pid, id, strings.ToUpper(strings.TrimSpace(in.Action)))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) printJobs(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var printer *int64
	if v := strings.TrimSpace(r.URL.Query().Get("printer_id")); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 1 {
			return apperr.Invalid("the filter is invalid", fieldErr("printer_id", "INVALID_VALUE", "a printer id"))
		}
		printer = &n
	}
	limit := 0
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 {
			return apperr.Invalid("the filter is invalid", fieldErr("limit", "INVALID_VALUE", "1 to 200"))
		}
		limit = n
	}
	v, err := h.svc.ListPrintJobs(r.Context(), pid, printer, strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status"))), limit)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

// events is the stream: Server-Sent Events read by the client with fetch (the header of authorisation is the one of every route; the
// browser's EventSource cannot send it). The id of an event is its row in the outbox: the client sends the last one it saw as Last-Event-ID
// and receives what came after, once.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := h.svc.RequireStreamReader(ctx, pid); err != nil {
		return err
	}
	channels, err := ParseChannels(r.URL.Query().Get("channels"))
	if err != nil {
		return err
	}
	p, err := h.svc.AuthoriseChannels(ctx, pid, channels)
	if err != nil {
		return err
	}
	var last int64
	if v := strings.TrimSpace(r.Header.Get("Last-Event-ID")); v != "" {
		if last, err = strconv.ParseInt(v, 10, 64); err != nil || last < 0 {
			return apperr.Invalid("the Last-Event-ID is invalid", fieldErr("Last-Event-ID", "INVALID_VALUE", "the id of the last event received"))
		}
	}
	cur, err := h.svc.EventsStart(ctx, pid, last)
	if err != nil {
		return err
	}
	closeStream, err := h.svc.Hub().Open(p.UserID)
	if err != nil {
		return err
	}
	defer closeStream()
	h.svc.maybePurge(ctx)

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the server's write timeout is for answers, not for a stream
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-cache")
	hd.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 3000\n: stream open\n\n"); err != nil {
		return nil
	}
	if rc.Flush() != nil {
		return nil
	}

	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	beat := time.NewTicker(20 * time.Second)
	defer beat.Stop()
	end := time.NewTimer(StreamLifetime)
	defer end.Stop()
	for {
		wake := h.svc.Hub().Wait() // taken before the read: a commit during the read wakes the next turn
		for {
			evs, next, err := h.svc.EventsSince(ctx, p.TenantID, pid, channels, cur)
			if err != nil {
				return nil
			}
			for _, e := range evs {
				b, _ := json.Marshal(e)
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, b); err != nil {
					return nil
				}
			}
			cur = next
			if len(evs) > 0 && rc.Flush() != nil {
				return nil
			}
			if len(evs) < EventBatch {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-end.C:
			_, _ = fmt.Fprint(w, "event: reconnect\ndata: {}\n\n")
			_ = rc.Flush()
			return nil
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil || rc.Flush() != nil {
				return nil
			}
		case <-wake:
		case <-timer.C:
		}
	}
}
