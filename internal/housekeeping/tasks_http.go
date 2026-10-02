package housekeeping

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// RegisterTasks mounts the routes of flags, the cleaning list and its staff.
func (h *Handler) RegisterTasks(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("PUT "+p+"/rooms/{roomId}/housekeeping/flags", httpx.HandlerFunc(h.setFlags))
	mux.Handle("GET "+p+"/housekeeping/staff", httpx.HandlerFunc(h.staff))
	mux.Handle("GET "+p+"/housekeeping/tasks", httpx.HandlerFunc(h.tasks))
	mux.Handle("POST "+p+"/housekeeping/tasks", httpx.HandlerFunc(h.createTask))
	mux.Handle("POST "+p+"/housekeeping/tasks/generate", httpx.HandlerFunc(h.generate))
	mux.Handle("POST "+p+"/housekeeping/tasks/assign", httpx.HandlerFunc(h.assign))
	mux.Handle("POST "+p+"/housekeeping/tasks/{taskId}/start", httpx.HandlerFunc(h.startTask))
	mux.Handle("POST "+p+"/housekeeping/tasks/{taskId}/complete", httpx.HandlerFunc(h.completeTask))
	mux.Handle("POST "+p+"/housekeeping/tasks/{taskId}/skip", httpx.HandlerFunc(h.skipTask))
}

func taskID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("taskId"), 10, 64)
	if err != nil || id < 1 {
		return 0, taskNotFound()
	}
	return id, nil
}

func (h *Handler) setFlags(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := roomID(r)
	if err != nil {
		return err
	}
	var in FlagsInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f, err := h.svc.SetFlags(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, f)
}

func (h *Handler) staff(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Staff(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Staff]{Data: list})
}

func (h *Handler) tasks(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	var f TaskFilter
	var fields []apperr.FieldError
	if v := q.Get("date"); v != "" {
		d, err := civil.ParseDate(v)
		if err != nil {
			fields = append(fields, apperr.FieldError{Field: "date", Code: "INVALID_DATE", Message: "a date as YYYY-MM-DD"})
		}
		f.Date = &d
	}
	f.Status = q.Get("status")
	if v := q.Get("assigned_to"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			fields = append(fields, apperr.FieldError{Field: "assigned_to", Code: "INVALID_VALUE", Message: "a user id"})
		}
		f.AssignedTo = &id
	}
	if v := q.Get("unassigned"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			fields = append(fields, apperr.FieldError{Field: "unassigned", Code: "INVALID_VALUE", Message: "true or false"})
		}
		f.Unassigned = b
	}
	if v := strings.TrimSpace(q.Get("floor")); v != "" {
		f.Floor = &v
	}
	if len(fields) > 0 {
		return apperr.Invalid("the filter is invalid", fields...)
	}
	list, err := h.svc.Tasks(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in TaskInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	t, err := h.svc.CreateTask(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) generate(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	res, err := h.svc.GenerateTasks(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

type assignRequest struct {
	TaskIDs []int64 `json:"task_ids"`
	UserID  *int64  `json:"user_id"`
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req assignRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	n, err := h.svc.AssignTasks(r.Context(), pid, req.TaskIDs, req.UserID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]int{"assigned": n})
}

func (h *Handler) startTask(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := taskID(r)
	if err != nil {
		return err
	}
	t, err := h.svc.StartTask(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

type noteRequest struct {
	Notes  string `json:"notes"`
	Reason string `json:"reason"`
}

func (h *Handler) completeTask(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := taskID(r)
	if err != nil {
		return err
	}
	var req noteRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			return err
		}
	}
	t, err := h.svc.CompleteTask(r.Context(), pid, id, req.Notes)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) skipTask(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := taskID(r)
	if err != nil {
		return err
	}
	var req noteRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	t, err := h.svc.SkipTask(r.Context(), pid, id, req.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}
