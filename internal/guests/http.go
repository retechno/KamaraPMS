package guests

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
)

// Handler exposes the guests API (docs/architecture/06-api.md §8).
type Handler struct{ svc *Service }

// NewHandler returns the guests HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/guests", httpx.HandlerFunc(h.search))
	mux.Handle("POST /api/v1/guests", httpx.HandlerFunc(h.create))
	mux.Handle("GET /api/v1/guests/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH /api/v1/guests/{id}", httpx.HandlerFunc(h.update))
	mux.Handle("GET /api/v1/guests/{id}/history", httpx.HandlerFunc(h.history))
}

func guestID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errGuestNotFound()
	}
	return id, nil
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var propertyID *int64
	if s := r.URL.Query().Get("property_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			return apperr.Invalid("invalid filter", apperr.FieldError{Field: "property_id", Code: "INVALID_VALUE", Message: "a positive integer"})
		}
		propertyID = &id
	}
	var after *SearchAfter
	if page.Cursor != "" {
		var c SearchAfter
		if err := httpx.DecodeCursor(page.Cursor, &c); err != nil {
			return err
		}
		after = &c
	}
	res, err := h.svc.Search(r.Context(), r.URL.Query().Get("q"), propertyID, after, page.Limit)
	if err != nil {
		return err
	}
	out := httpx.Page[Guest]{Data: res.Guests}
	if out.Data == nil {
		out.Data = []Guest{}
	}
	if res.Next != nil {
		if out.NextCursor, err = httpx.EncodeCursor(res.Next); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type profileRequest struct {
	FirstName   string  `json:"first_name"`
	LastName    string  `json:"last_name"`
	Email       string  `json:"email"`
	Phone       string  `json:"phone"`
	Nationality string  `json:"nationality"`
	CountryCode string  `json:"country_code"`
	DateOfBirth *string `json:"date_of_birth"`
	Gender      string  `json:"gender"`
	IDType      string  `json:"id_type"`
	IDNumber    string  `json:"id_number"`
	Address     string  `json:"address"`
	City        string  `json:"city"`
	Notes       string  `json:"notes"`
}

type createRequest struct {
	OriginPropertyID int64 `json:"origin_property_id"`
	profileRequest
}

func parseDate(field string, s *string) (*civil.Date, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	d, err := civil.ParseDate(*s)
	if err != nil {
		return nil, apperr.Invalid("the guest is invalid", apperr.FieldError{Field: field, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	return &d, nil
}

type createResponse struct {
	Guest
	PossibleDuplicates   []PossibleDuplicate `json:"possible_duplicates"`
	HiddenDuplicateCount int                 `json:"hidden_duplicate_count"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.OriginPropertyID < 1 {
		return apperr.Invalid("the guest is invalid", apperr.FieldError{Field: "origin_property_id", Code: "REQUIRED"})
	}
	dob, err := parseDate("date_of_birth", req.DateOfBirth)
	if err != nil {
		return err
	}
	res, err := h.svc.Create(r.Context(), req.OriginPropertyID, Profile{
		FirstName: req.FirstName, LastName: req.LastName, Email: req.Email, Phone: req.Phone, Nationality: req.Nationality,
		CountryCode: req.CountryCode, DateOfBirth: dob, Gender: req.Gender, IDType: req.IDType, IDNumber: req.IDNumber,
		Address: req.Address, City: req.City, Notes: req.Notes,
	})
	if err != nil {
		return err
	}
	dups := res.PossibleDuplicates
	if dups == nil {
		dups = []PossibleDuplicate{}
	}
	return httpx.WriteJSON(w, http.StatusCreated, createResponse{Guest: res.Guest, PossibleDuplicates: dups, HiddenDuplicateCount: res.HiddenDuplicateCount})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := guestID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

type patchRequest struct {
	FirstName   *string `json:"first_name"`
	LastName    *string `json:"last_name"`
	Email       *string `json:"email"`
	Phone       *string `json:"phone"`
	Nationality *string `json:"nationality"`
	CountryCode *string `json:"country_code"`
	DateOfBirth *string `json:"date_of_birth"` // "" clears
	Gender      *string `json:"gender"`
	IDType      *string `json:"id_type"`
	IDNumber    *string `json:"id_number"`
	Address     *string `json:"address"`
	City        *string `json:"city"`
	Notes       *string `json:"notes"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := guestID(r)
	if err != nil {
		return err
	}
	var req patchRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	patch := Patch{
		FirstName: req.FirstName, LastName: req.LastName, Email: req.Email, Phone: req.Phone, Nationality: req.Nationality,
		CountryCode: req.CountryCode, Gender: req.Gender, IDType: req.IDType, IDNumber: req.IDNumber, Address: req.Address,
		City: req.City, Notes: req.Notes,
	}
	if req.DateOfBirth != nil {
		if *req.DateOfBirth == "" {
			patch.ClearDateOfBirth = true
		} else if patch.DateOfBirth, err = parseDate("date_of_birth", req.DateOfBirth); err != nil {
			return err
		}
	}
	v, err := h.svc.Update(r.Context(), id, patch)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

type historyResponse struct {
	Data        []HistoryItem `json:"data"`
	NextCursor  string        `json:"next_cursor,omitempty"`
	HiddenCount int64         `json:"hidden_count"`
}

type offsetCursor struct {
	Offset int `json:"o"`
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) error {
	id, err := guestID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur offsetCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	hist, err := h.svc.History(r.Context(), id, cur.Offset, page.Limit+1)
	if err != nil {
		return err
	}
	out := historyResponse{Data: hist.Items, HiddenCount: hist.HiddenCount}
	if out.Data == nil {
		out.Data = []HistoryItem{}
	}
	if len(out.Data) > page.Limit {
		out.Data = out.Data[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(offsetCursor{Offset: cur.Offset + page.Limit}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
