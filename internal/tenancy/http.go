package tenancy

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
)

// Handler exposes the tenancy API (docs/architecture/06-api.md §3).
type Handler struct {
	svc *Service
}

// NewHandler returns the tenancy HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties", httpx.HandlerFunc(h.list))
	mux.Handle("POST /api/v1/properties", httpx.HandlerFunc(h.create))
	mux.Handle("GET /api/v1/properties/{propertyId}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH /api/v1/properties/{propertyId}", httpx.HandlerFunc(h.update))
	mux.Handle("GET /api/v1/properties/{propertyId}/business-date", httpx.HandlerFunc(h.businessDate))
	mux.Handle("GET /api/v1/properties/{propertyId}/business-days", httpx.HandlerFunc(h.businessDays))
}

// PropertyID parses the {propertyId} path segment. Malformed ids are reported
// as not found, like ids of other tenants.
func PropertyID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("propertyId"), 10, 64)
	if err != nil || id < 1 {
		return 0, clone(errPropertyNotFound)
	}
	return id, nil
}

// fieldParser collects per-field parse errors for string-typed inputs.
type fieldParser struct{ errs []apperr.FieldError }

func (f *fieldParser) timeOfDay(field string, s *string, dst *civil.TimeOfDay, required bool) {
	if s == nil {
		if required {
			f.errs = append(f.errs, apperr.FieldError{Field: field, Code: "REQUIRED", Message: "HH:MM"})
		}
		return
	}
	t, err := civil.ParseTimeOfDay(*s)
	if err != nil {
		f.errs = append(f.errs, apperr.FieldError{Field: field, Code: "INVALID_FORMAT", Message: "24-hour HH:MM, e.g. 14:00"})
		return
	}
	*dst = t
}

func (f *fieldParser) date(field string, s *string, dst *civil.Date) {
	if s == nil {
		f.errs = append(f.errs, apperr.FieldError{Field: field, Code: "REQUIRED", Message: "YYYY-MM-DD"})
		return
	}
	d, err := civil.ParseDate(*s)
	if err != nil {
		f.errs = append(f.errs, apperr.FieldError{Field: field, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
		return
	}
	*dst = d
}

func (f *fieldParser) err() error {
	if len(f.errs) == 0 {
		return nil
	}
	return apperr.Invalid("the request is invalid", f.errs...)
}

type createPropertyRequest struct {
	Code                            string  `json:"code"`
	Name                            string  `json:"name"`
	Address                         string  `json:"address"`
	City                            string  `json:"city"`
	CountryCode                     string  `json:"country_code"`
	Phone                           string  `json:"phone"`
	Email                           string  `json:"email"`
	TaxID                           string  `json:"tax_id"`
	DocumentFooter                  string  `json:"document_footer"`
	Timezone                        string  `json:"timezone"`
	CurrencyCode                    string  `json:"currency_code"`
	CurrencyDecimals                *int32  `json:"currency_decimals"`
	CheckInTime                     *string `json:"check_in_time"`
	CheckOutTime                    *string `json:"check_out_time"`
	RequireRoomInspectionForCheckin bool    `json:"require_room_inspection_for_checkin"`
	NightAuditMarksOccupiedDirty    *bool   `json:"night_audit_marks_occupied_dirty"`
	NightAuditEarliestTime          *string `json:"night_audit_earliest_time"`
	OpeningBusinessDate             *string `json:"opening_business_date"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req createPropertyRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in := CreatePropertyInput{
		Code: req.Code,
		Settings: PropertySettings{
			Name: req.Name, Address: req.Address, City: req.City, CountryCode: req.CountryCode,
			Phone: req.Phone, Email: req.Email, TaxID: req.TaxID, DocumentFooter: req.DocumentFooter,
			Timezone: req.Timezone, CurrencyCode: req.CurrencyCode,
			RequireRoomInspectionForCheckin: req.RequireRoomInspectionForCheckin,
			NightAuditMarksOccupiedDirty:    true,
			NightAuditEarliestTime:          civil.MustParseTimeOfDay("20:00"),
		},
	}
	var fp fieldParser
	if req.CurrencyDecimals == nil {
		fp.errs = append(fp.errs, apperr.FieldError{Field: "currency_decimals", Code: "REQUIRED", Message: "0-3, e.g. 0 for IDR, 2 for USD"})
	} else {
		in.Settings.CurrencyDecimals = *req.CurrencyDecimals
	}
	if req.NightAuditMarksOccupiedDirty != nil {
		in.Settings.NightAuditMarksOccupiedDirty = *req.NightAuditMarksOccupiedDirty
	}
	fp.timeOfDay("check_in_time", req.CheckInTime, &in.Settings.CheckInTime, true)
	fp.timeOfDay("check_out_time", req.CheckOutTime, &in.Settings.CheckOutTime, true)
	fp.timeOfDay("night_audit_earliest_time", req.NightAuditEarliestTime, &in.Settings.NightAuditEarliestTime, false)
	fp.date("opening_business_date", req.OpeningBusinessDate, &in.OpeningBusinessDate)
	if err := fp.err(); err != nil {
		return err
	}

	p, err := h.svc.CreateProperty(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, p)
}

type propertyCursor struct {
	AfterID int64 `json:"a"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur propertyCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	props, err := h.svc.ListProperties(r.Context(), cur.AfterID, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Property]{Data: props}
	if len(props) > page.Limit {
		out.Data = props[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(propertyCursor{AfterID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := PropertyID(r)
	if err != nil {
		return err
	}
	p, err := h.svc.GetPropertyWithDay(r.Context(), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, p)
}

type patchPropertyRequest struct {
	Name                            *string `json:"name"`
	Address                         *string `json:"address"`
	City                            *string `json:"city"`
	CountryCode                     *string `json:"country_code"`
	Phone                           *string `json:"phone"`
	Email                           *string `json:"email"`
	TaxID                           *string `json:"tax_id"`
	DocumentFooter                  *string `json:"document_footer"`
	Timezone                        *string `json:"timezone"`
	CurrencyCode                    *string `json:"currency_code"`
	CurrencyDecimals                *int32  `json:"currency_decimals"`
	CheckInTime                     *string `json:"check_in_time"`
	CheckOutTime                    *string `json:"check_out_time"`
	RequireRoomInspectionForCheckin *bool   `json:"require_room_inspection_for_checkin"`
	NightAuditMarksOccupiedDirty    *bool   `json:"night_audit_marks_occupied_dirty"`
	NightAuditEarliestTime          *string `json:"night_audit_earliest_time"`
	Status                          *string `json:"status"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := PropertyID(r)
	if err != nil {
		return err
	}
	var req patchPropertyRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	patch := PropertyPatch{
		Name: req.Name, Address: req.Address, City: req.City, CountryCode: req.CountryCode,
		Phone: req.Phone, Email: req.Email, TaxID: req.TaxID, DocumentFooter: req.DocumentFooter,
		Timezone: req.Timezone, CurrencyCode: req.CurrencyCode, CurrencyDecimals: req.CurrencyDecimals,
		RequireRoomInspectionForCheckin: req.RequireRoomInspectionForCheckin,
		NightAuditMarksOccupiedDirty:    req.NightAuditMarksOccupiedDirty,
		Status:                          req.Status,
	}
	var fp fieldParser
	for _, t := range []struct {
		field string
		in    *string
		out   **civil.TimeOfDay
	}{
		{"check_in_time", req.CheckInTime, &patch.CheckInTime},
		{"check_out_time", req.CheckOutTime, &patch.CheckOutTime},
		{"night_audit_earliest_time", req.NightAuditEarliestTime, &patch.NightAuditEarliestTime},
	} {
		if t.in != nil {
			var v civil.TimeOfDay
			fp.timeOfDay(t.field, t.in, &v, false)
			*t.out = &v
		}
	}
	if err := fp.err(); err != nil {
		return err
	}

	if _, err := h.svc.UpdateProperty(r.Context(), id, patch); err != nil {
		return err
	}
	p, err := h.svc.GetPropertyWithDay(r.Context(), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) businessDate(w http.ResponseWriter, r *http.Request) error {
	id, err := PropertyID(r)
	if err != nil {
		return err
	}
	p, err := h.svc.GetProperty(r.Context(), id)
	if err != nil {
		return err
	}
	c, err := h.svc.DayClock(r.Context(), p)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

type dayCursor struct {
	Before civil.Date `json:"b"`
}

func (h *Handler) businessDays(w http.ResponseWriter, r *http.Request) error {
	id, err := PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.GetProperty(r.Context(), id); err != nil {
		return err
	}
	var before *civil.Date
	if page.Cursor != "" {
		var cur dayCursor
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
		before = &cur.Before
	}
	days, err := h.svc.BusinessDayHistory(r.Context(), id, before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[BusinessDay]{Data: days}
	if len(days) > page.Limit {
		out.Data = days[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(dayCursor{Before: out.Data[page.Limit-1].BusinessDate}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
