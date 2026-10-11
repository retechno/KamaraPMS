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
	"unicode/utf8"

	"kamarapms/internal/audit/auditdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/tenancy"
)

// User is the user an entry is attributed to.
type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// searchRows reads the page in the order that was asked for: two queries, one for each direction, so that either walks the primary key (an ORDER BY that is chosen by a CASE could not).
func (r *Reader) searchRows(ctx context.Context, f Filter, tenantID int64, entityType, action, labelLike *string, position *int64, limit int32) ([]auditdb.SearchAuditLogsRow, error) {
	q := auditdb.New(r.txm.DB(ctx))
	if !f.Oldest {
		return q.SearchAuditLogs(ctx, auditdb.SearchAuditLogsParams{TenantID: tenantID, PropertyID: f.PropertyID, EntityType: entityType, EntityID: f.EntityID, UserID: f.UserID, Action: action,
			FromDate: f.From, ToDate: f.To, LabelLike: labelLike, BeforeID: position, RowLimit: limit})
	}
	rows, err := q.SearchAuditLogsAsc(ctx, auditdb.SearchAuditLogsAscParams{TenantID: tenantID, PropertyID: f.PropertyID, EntityType: entityType, EntityID: f.EntityID, UserID: f.UserID, Action: action,
		FromDate: f.From, ToDate: f.To, LabelLike: labelLike, AfterID: position, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]auditdb.SearchAuditLogsRow, len(rows))
	for i, a := range rows {
		out[i] = auditdb.SearchAuditLogsRow(a)
	}
	return out, nil
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
	EntityLabel  *string         `json:"entity_label"` // what the entry is about as a person reads it (a number or a code); null for an entry without one
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
	// Oldest lists the oldest entries first; the default is the newest first. The order is by id (ids grow with time).
	Oldest bool
	// Q finds the entries whose label holds it, in any case (a part of a number: "0012" finds RES000012). 3 to 64 characters; the escaping for LIKE is done here.
	Q string
}

// The length of a search text: three characters so the trigram index serves it, and a number or a code is never longer than 64.
const (
	MinQuery = 3
	MaxQuery = 64
)

// likeEscaper makes a text literal in a LIKE pattern that has ESCAPE of a backslash: the backslash itself, the percent sign and the underscore.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likePattern is "%<q>%" with q escaped, so a search for 50% finds the label that holds 50% and not every label.
func likePattern(q string) string { return "%" + likeEscaper.Replace(q) + "%" }

// Reader reads the audit trail (audit.read). Reading is the only thing it does.
type Reader struct {
	txm   *db.TxManager
	authz auth.Authorizer
}

// NewReader returns a Reader.
func NewReader(txm *db.TxManager, authz auth.Authorizer) *Reader {
	return &Reader{txm: txm, authz: authz}
}

// Search lists entries newest first (oldest first with f.Oldest), position being the id of the last entry of the page before (0 = from the first). A property search
// needs audit.read at the property (404 for a property the caller cannot see); a tenant-level search is for tenant
// administrators.
func (r *Reader) Search(ctx context.Context, f Filter, position int64, limit int) ([]Record, error) {
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
	// The search text is checked here, after the permission: a person who may not read the trail is refused (403) whatever they ask.
	if n := utf8.RuneCountInString(f.Q); f.Q != "" && (n < MinQuery || n > MaxQuery) {
		return nil, apperr.Invalid("the query is invalid", apperr.FieldError{Field: "q", Code: "INVALID_LENGTH", Message: "3 to 64 characters"})
	}
	var entityType, action, labelLike *string
	if f.EntityType != "" {
		entityType = &f.EntityType
	}
	if f.Action != "" {
		action = &f.Action
	}
	if f.Q != "" {
		pattern := likePattern(f.Q)
		labelLike = &pattern
	}
	var at *int64
	if position > 0 {
		at = &position
	}
	rowLimit := int32(limit) //nolint:gosec // G115: the page size is bounded by ParsePage
	rows, err := r.searchRows(ctx, f, p.TenantID, entityType, action, labelLike, at, rowLimit)
	if err != nil {
		return nil, err
	}
	out := make([]Record, len(rows))
	for i, a := range rows {
		rec := Record{ID: a.ID, CreatedAt: a.CreatedAt, BusinessDate: a.BusinessDate, Action: a.Action, EntityType: a.EntityType, EntityID: a.EntityID,
			EntityLabel: a.EntityLabel, OldData: redact(a.OldData), NewData: redact(a.NewData), IPAddress: a.IpAddress}
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

// The trail is ordered by time, which is by id (ids grow with time): newest first unless the client asks for `sort=created_at&order=asc`.
var sortSpec = listsort.Spec{Columns: map[string]listsort.Kind{"created_at": listsort.Num}, DefaultKind: listsort.Num, DefaultDesc: true}

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
	f := Filter{PropertyID: property, EntityType: q.Get("entity_type"), Action: q.Get("action"), Q: strings.TrimSpace(q.Get("q"))}
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
	order, err := sortSpec.Parse(req)
	if err != nil {
		return err
	}
	pos, err := listsort.Decode(page.Cursor, order)
	if err != nil {
		return err
	}
	f.Oldest = !order.Desc
	var after int64
	if pos != nil {
		after = pos.ID
	}
	items, err := h.r.Search(req.Context(), f, after, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Record]{Data: items}
	if out.Data == nil {
		out.Data = []Record{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = listsort.Encode(order, listsort.Position{ID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
