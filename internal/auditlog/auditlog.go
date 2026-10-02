// Package auditlog reads the audit trail (the writer is package audit).
package auditlog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"kamarapms/internal/audit/auditdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// User is the user an entry is attributed to.
type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Record is an audit entry as the viewer shows it.
type Record struct {
	ID           int64           `json:"id"`
	CreatedAt    time.Time       `json:"created_at"`
	BusinessDate *civil.Date     `json:"business_date"`
	User         *User           `json:"user"`
	Action       string          `json:"action"`
	EntityType   string          `json:"entity_type"`
	EntityID     int64           `json:"entity_id"`
	OldData      json.RawMessage `json:"old_data"`
	NewData      json.RawMessage `json:"new_data"`
	RequestID    string          `json:"request_id,omitempty"`
	IPAddress    *netip.Addr     `json:"ip_address,omitempty"`
}

// Filter narrows a search. A nil PropertyID searches the tenant-level entries (users, roles, guests).
type Filter struct {
	PropertyID *int64
	EntityType string
	EntityID   *int64
	UserID     *int64
	Action     string
	From, To   *civil.Date
}

// Reader reads the audit trail (audit.read). Reading is the only thing it does.
type Reader struct {
	txm   *db.TxManager
	authz auth.Authorizer
}

// NewReader returns a Reader.
func NewReader(txm *db.TxManager, authz auth.Authorizer) *Reader {
	return &Reader{txm: txm, authz: authz}
}

// Search lists entries newest first, beforeID being the keyset position (0 = from the newest). A property search
// needs audit.read at the property (404 for a property the caller cannot see); a tenant-level search is for tenant
// administrators.
func (r *Reader) Search(ctx context.Context, f Filter, beforeID int64, limit int) ([]Record, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if f.PropertyID != nil {
		if err := r.authz.Require(ctx, *f.PropertyID, auth.PermAuditRead); err != nil {
			return nil, err
		}
	} else if !p.IsTenantAdmin {
		return nil, apperr.New(apperr.KindForbidden, "PERMISSION_DENIED", "the tenant-level audit trail is for tenant administrators")
	}
	q := auditdb.SearchAuditLogsParams{TenantID: p.TenantID, PropertyID: f.PropertyID, EntityID: f.EntityID, UserID: f.UserID, FromDate: f.From, ToDate: f.To, RowLimit: int32(limit)} //nolint:gosec // G115: the page size is bounded by ParsePage
	if f.EntityType != "" {
		q.EntityType = &f.EntityType
	}
	if f.Action != "" {
		q.Action = &f.Action
	}
	if beforeID > 0 {
		q.BeforeID = &beforeID
	}
	rows, err := auditdb.New(r.txm.DB(ctx)).SearchAuditLogs(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Record, len(rows))
	for i, a := range rows {
		rec := Record{ID: a.ID, CreatedAt: a.CreatedAt, BusinessDate: a.BusinessDate, Action: a.Action, EntityType: a.EntityType, EntityID: a.EntityID,
			OldData: redact(a.OldData), NewData: redact(a.NewData), IPAddress: a.IpAddress}
		if a.RequestID != nil {
			rec.RequestID = *a.RequestID
		}
		if a.UserID != nil {
			rec.User = &User{ID: *a.UserID}
			if a.UserName != nil {
				rec.User.Name = *a.UserName
			}
		}
		out[i] = rec
	}
	return out, nil
}

// redact is a belt for the braces: entries are written from types that never carry secrets, but the viewer
// still blanks any field that looks like one.
func redact(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return json.RawMessage("null")
	}
	b, err := json.Marshal(scrub(v))
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func scrub(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "password") || strings.Contains(lk, "secret") || strings.Contains(lk, "token") {
				t[k] = "[redacted]"
				continue
			}
			t[k] = scrub(x)
		}
	case []any:
		for i := range t {
			t[i] = scrub(t[i])
		}
	}
	return v
}

// Handler exposes the audit trail (docs/architecture/06-api.md §16).
type Handler struct{ r *Reader }

// NewHandler returns the HTTP handler.
func NewHandler(r *Reader) *Handler { return &Handler{r: r} }

// Register mounts the routes: the property trail, and the tenant-level trail for tenant administrators.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/audit-logs", httpx.HandlerFunc(h.property))
	mux.Handle("GET /api/v1/audit-logs", httpx.HandlerFunc(h.tenant))
}

type cursor struct {
	Before int64 `json:"b"`
}

func (h *Handler) property(w http.ResponseWriter, req *http.Request) error {
	pid, err := tenancy.PropertyID(req)
	if err != nil {
		return err
	}
	return h.list(w, req, &pid)
}

func (h *Handler) tenant(w http.ResponseWriter, req *http.Request) error { return h.list(w, req, nil) }

func (h *Handler) list(w http.ResponseWriter, req *http.Request, property *int64) error {
	q := req.URL.Query()
	var errs []apperr.FieldError
	f := Filter{PropertyID: property, EntityType: q.Get("entity_type"), Action: q.Get("action")}
	num := func(name string) *int64 {
		v := q.Get(name)
		if v == "" {
			return nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			errs = append(errs, apperr.FieldError{Field: name, Code: "INVALID_VALUE", Message: "a positive id"})
			return nil
		}
		return &n
	}
	date := func(name string) *civil.Date {
		v := q.Get(name)
		if v == "" {
			return nil
		}
		d, err := civil.ParseDate(v)
		if err != nil {
			errs = append(errs, apperr.FieldError{Field: name, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
			return nil
		}
		return &d
	}
	f.EntityID, f.UserID, f.From, f.To = num("entity_id"), num("user_id"), date("from"), date("to")
	if len(f.EntityType) > 50 || len(f.Action) > 100 {
		errs = append(errs, apperr.FieldError{Field: "entity_type", Code: "TOO_LONG", Message: "too long"})
	}
	if len(errs) > 0 {
		return apperr.Invalid("the query is invalid", errs...)
	}
	page, err := httpx.ParsePage(req)
	if err != nil {
		return err
	}
	var cur cursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	items, err := h.r.Search(req.Context(), f, cur.Before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Record]{Data: items}
	if out.Data == nil {
		out.Data = []Record{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(cursor{Before: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
