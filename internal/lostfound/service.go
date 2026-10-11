// Package lostfound keeps what guests left behind: an item is recorded when it is found, kept in a storage place and
// later handed back to its owner or disposed of. Both endings are final and record who, when and why. For an item
// found in a room the service can name the guests who had the room around that day.
package lostfound

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/lostfound/lostfounddb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Statuses and the largest page.
const (
	StatusStored   = "STORED"
	StatusReturned = "RETURNED"
	StatusDisposed = "DISPOSED"

	maxPage     = 200
	ownerWindow = 3 // days before the find that a stay can have ended and still be suggested
)

var categories = []string{"ELECTRONICS", "CLOTHING", "DOCUMENTS", "JEWELRY", "BAGS", "OTHER"}

// Item is something found.
type Item struct {
	ID              int64       `json:"id"`
	ItemNumber      string      `json:"item_number"`
	Description     string      `json:"description"`
	Category        string      `json:"category"`
	RoomID          *int64      `json:"room_id"`
	RoomNumber      string      `json:"room_number,omitempty"`
	Location        string      `json:"location,omitempty"`
	FoundOn         civil.Date  `json:"found_on"`
	FoundAt         time.Time   `json:"found_at"`
	FoundBy         *int64      `json:"found_by"`
	FinderName      string      `json:"finder_name,omitempty"`
	StorageLocation string      `json:"storage_location,omitempty"`
	PossibleOwner   string      `json:"possible_owner,omitempty"`
	Notes           string      `json:"notes,omitempty"`
	Status          string      `json:"status"`
	ClosedOn        *civil.Date `json:"closed_on"`
	ClosedAt        *time.Time  `json:"closed_at"`
	CloserName      string      `json:"closer_name,omitempty"`
	ClaimantName    string      `json:"claimant_name,omitempty"`
	ClaimantProof   string      `json:"claimant_proof,omitempty"`
	CloseNote       string      `json:"close_note,omitempty"`
}

// Owner is a guest who had the room around the day an item was found.
type Owner struct {
	StayID        int64      `json:"stay_id"`
	StayNumber    string     `json:"stay_number"`
	GuestName     string     `json:"guest_name"`
	Phone         string     `json:"phone,omitempty"`
	Email         string     `json:"email,omitempty"`
	ArrivalDate   civil.Date `json:"arrival_date"`
	DepartureDate civil.Date `json:"departure_date"`
	StayStatus    string     `json:"stay_status"`
}

// CreateInput records an item that was found.
type CreateInput struct {
	Description     string `json:"description"`
	Category        string `json:"category"`
	RoomID          *int64 `json:"room_id"`
	Location        string `json:"location"`
	StorageLocation string `json:"storage_location"`
	PossibleOwner   string `json:"possible_owner"`
	Notes           string `json:"notes"`
}

// Patch changes a stored item; nil fields stay as they are.
type Patch struct {
	Description     *string `json:"description"`
	Category        *string `json:"category"`
	Location        *string `json:"location"`
	StorageLocation *string `json:"storage_location"`
	PossibleOwner   *string `json:"possible_owner"`
	Notes           *string `json:"notes"`
}

// ReturnInput hands an item back: to whom and what was checked.
type ReturnInput struct {
	ClaimantName  string `json:"claimant_name"`
	ClaimantProof string `json:"claimant_proof"`
	Note          string `json:"note"`
}

// DisposeInput gives away or throws away an item that nobody claimed.
type DisposeInput struct {
	Reason string `json:"reason"`
}

// Filter narrows the list.
type Filter struct {
	Status    string
	Category  string
	RoomID    *int64
	FoundFrom *civil.Date
	FoundTo   *civil.Date
	Query     string
}

// Service is the lost and found application service: recording and reading need lostfound.report, everything that
// changes or closes an item needs lostfound.manage.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days}
}

func (s *Service) q(ctx context.Context) *lostfounddb.Queries { return lostfounddb.New(s.txm.DB(ctx)) }

func errNotFound() *apperr.Error {
	return apperr.NotFound("LOST_ITEM_NOT_FOUND", "the item does not exist in this property")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func entry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "lost_found_item", EntityID: id, EntityLabel: label, Old: old, New: updated}
}

func view(r lostfounddb.ListItemsRow) Item {
	return Item{
		ID: r.ID, ItemNumber: r.ItemNumber, Description: r.Description, Category: r.Category, RoomID: r.RoomID, RoomNumber: deref(r.RoomNumber), Location: deref(r.Location),
		FoundOn: r.FoundOn, FoundAt: r.FoundAt, FoundBy: r.FoundBy, FinderName: deref(r.FinderName), StorageLocation: deref(r.StorageLocation),
		PossibleOwner: deref(r.PossibleOwner), Notes: deref(r.Notes), Status: r.Status, ClosedOn: r.ClosedOn, ClosedAt: r.ClosedAt, CloserName: deref(r.CloserName),
		ClaimantName: deref(r.ClaimantName), ClaimantProof: deref(r.ClaimantProof), CloseNote: deref(r.CloseNote),
	}
}

func validate(description, category, location, storage, owner, notes string, hasRoom bool) []apperr.FieldError {
	var fields []apperr.FieldError
	if description == "" || len([]rune(description)) > 500 {
		fields = append(fields, fieldErr("description", "REQUIRED", "1-500 characters"))
	}
	if !slices.Contains(categories, category) {
		fields = append(fields, fieldErr("category", "INVALID_VALUE", strings.Join(categories, ", ")))
	}
	if len([]rune(location)) > 150 {
		fields = append(fields, fieldErr("location", "TOO_LONG", "at most 150 characters"))
	}
	if len([]rune(storage)) > 100 {
		fields = append(fields, fieldErr("storage_location", "TOO_LONG", "at most 100 characters"))
	}
	if len([]rune(owner)) > 150 {
		fields = append(fields, fieldErr("possible_owner", "TOO_LONG", "at most 150 characters"))
	}
	if len([]rune(notes)) > 500 {
		fields = append(fields, fieldErr("notes", "TOO_LONG", "at most 500 characters"))
	}
	if !hasRoom && location == "" {
		fields = append(fields, fieldErr("location", "REQUIRED", "a room or the place it was found"))
	}
	return fields
}

// Create records an item that was found (lostfound.report), in a room or at a place such as "Pool", dated with the
// current business date.
func (s *Service) Create(ctx context.Context, propertyID int64, in CreateInput) (Item, error) {
	p, err := s.need(ctx, propertyID, auth.PermLostFoundReport)
	if err != nil {
		return Item{}, err
	}
	in.Description = strings.TrimSpace(in.Description)
	in.Category = strings.ToUpper(strings.TrimSpace(in.Category))
	in.Location = strings.TrimSpace(in.Location)
	in.StorageLocation = strings.TrimSpace(in.StorageLocation)
	in.PossibleOwner = strings.TrimSpace(in.PossibleOwner)
	in.Notes = strings.TrimSpace(in.Notes)
	if in.RoomID != nil && *in.RoomID < 1 {
		in.RoomID = nil
	}
	if fields := validate(in.Description, in.Category, in.Location, in.StorageLocation, in.PossibleOwner, in.Notes, in.RoomID != nil); len(fields) > 0 {
		return Item{}, apperr.Invalid("the item is invalid", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if in.RoomID != nil {
			ok, err := q.RoomExists(ctx, lostfounddb.RoomExistsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.RoomID})
			if err != nil {
				return err
			}
			if !ok {
				return apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqLostFound) // last in the lock order
		if err != nil {
			return err
		}
		id, err = q.InsertItem(ctx, lostfounddb.InsertItemParams{
			TenantID: p.TenantID, PropertyID: propertyID, ItemNumber: number, Description: in.Description, Category: in.Category, RoomID: in.RoomID,
			Location: nullable(in.Location), FoundOn: day.BusinessDate, Now: s.clock.Now(), ActorID: p.ActorID(), StorageLocation: nullable(in.StorageLocation),
			PossibleOwner: nullable(in.PossibleOwner), Notes: nullable(in.Notes),
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "lostfound.recorded", id, number, nil, map[string]any{
			"item_number": number, "description": in.Description, "category": in.Category, "room_id": in.RoomID, "location": in.Location,
		}))
	})
	if err != nil {
		return Item{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// List lists items, newest first, before the id beforeID (lostfound.report).
func (s *Service) List(ctx context.Context, propertyID int64, f Filter, beforeID *int64, limit int) ([]Item, error) {
	p, err := s.need(ctx, propertyID, auth.PermLostFoundReport)
	if err != nil {
		return nil, err
	}
	var fields []apperr.FieldError
	if f.Status != "" && !slices.Contains([]string{StatusStored, StatusReturned, StatusDisposed}, f.Status) {
		fields = append(fields, fieldErr("status", "INVALID_VALUE", "STORED, RETURNED or DISPOSED"))
	}
	if f.Category != "" && !slices.Contains(categories, f.Category) {
		fields = append(fields, fieldErr("category", "INVALID_VALUE", strings.Join(categories, ", ")))
	}
	if f.FoundFrom != nil && f.FoundTo != nil && f.FoundTo.Before(*f.FoundFrom) {
		fields = append(fields, fieldErr("found_to", "INVALID_RANGE", "not before found_from"))
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid("the filter is invalid", fields...)
	}
	return s.list(ctx, p.TenantID, propertyID, f, nil, beforeID, limit)
}

func (s *Service) list(ctx context.Context, tenantID, propertyID int64, f Filter, id, beforeID *int64, limit int) ([]Item, error) {
	arg := lostfounddb.ListItemsParams{
		TenantID: tenantID, PropertyID: propertyID, ID: id, BeforeID: beforeID, RoomID: f.RoomID, FoundFrom: f.FoundFrom, FoundTo: f.FoundTo,
		RowLimit: int32(max(1, min(limit, maxPage+1))), //nolint:gosec // G115: bounded
	}
	if f.Status != "" {
		arg.Status = &f.Status
	}
	if f.Category != "" {
		arg.Category = &f.Category
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		arg.Q = &q
	}
	rows, err := s.q(ctx).ListItems(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]Item, len(rows))
	for i, r := range rows {
		out[i] = view(r)
	}
	return out, nil
}

func (s *Service) load(ctx context.Context, tenantID, propertyID, id int64) (Item, error) {
	rows, err := s.list(ctx, tenantID, propertyID, Filter{}, &id, nil, 1)
	if err != nil {
		return Item{}, err
	}
	if len(rows) == 0 {
		return Item{}, errNotFound()
	}
	return rows[0], nil
}

// Get returns one item (lostfound.report).
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Item, error) {
	p, err := s.need(ctx, propertyID, auth.PermLostFoundReport)
	if err != nil {
		return Item{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// PossibleOwners names the guests who had the item's room on the day it was found or in the few days before
// (lostfound.report and reservation.read, because it shows guests of stays). It is a hint, not a match.
func (s *Service) PossibleOwners(ctx context.Context, propertyID, id int64) ([]Owner, error) {
	p, err := s.need(ctx, propertyID, auth.PermLostFoundReport)
	if err != nil {
		return nil, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermReservationRead); err != nil {
		return nil, err
	}
	item, err := s.load(ctx, p.TenantID, propertyID, id)
	if err != nil {
		return nil, err
	}
	if item.RoomID == nil {
		return []Owner{}, nil
	}
	rows, err := s.q(ctx).PossibleOwners(ctx, lostfounddb.PossibleOwnersParams{
		TenantID: p.TenantID, PropertyID: propertyID, RoomID: *item.RoomID, FoundOn: item.FoundOn, Since: item.FoundOn.AddDays(-ownerWindow),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Owner, len(rows))
	for i, r := range rows {
		out[i] = Owner{StayID: r.StayID, StayNumber: r.StayNumber, GuestName: r.GuestName, Phone: deref(r.Phone), Email: deref(r.Email), ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, StayStatus: r.StayStatus}
	}
	return out, nil
}

// change runs a use case on one item: business day (share), then the item row. The item must still be STORED.
func (s *Service) change(ctx context.Context, propertyID, id int64, f func(ctx context.Context, p auth.Principal, bd civil.Date, cur lostfounddb.LostFoundItem) (string, any, error)) (Item, error) {
	p, err := s.need(ctx, propertyID, auth.PermLostFoundManage)
	if err != nil {
		return Item{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cur, err := s.q(ctx).GetItemForUpdate(ctx, lostfounddb.GetItemForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound()
		}
		if err != nil {
			return err
		}
		if cur.Status != StatusStored {
			return apperr.Conflict("ITEM_NOT_STORED", "the item has already been handed back or disposed of").WithContext("status", cur.Status)
		}
		action, detail, err := f(ctx, p, day.BusinessDate, cur)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, action, id, cur.ItemNumber, map[string]any{"status": cur.Status}, detail))
	})
	if err != nil {
		return Item{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// Update changes the details of a stored item (lostfound.manage).
func (s *Service) Update(ctx context.Context, propertyID, id int64, patch Patch) (Item, error) {
	return s.change(ctx, propertyID, id, func(ctx context.Context, p auth.Principal, bd civil.Date, cur lostfounddb.LostFoundItem) (string, any, error) {
		description, category, location := cur.Description, cur.Category, deref(cur.Location)
		storage, owner, notes := deref(cur.StorageLocation), deref(cur.PossibleOwner), deref(cur.Notes)
		set := func(dst *string, v *string, upper bool) {
			if v != nil {
				*dst = strings.TrimSpace(*v)
				if upper {
					*dst = strings.ToUpper(*dst)
				}
			}
		}
		set(&description, patch.Description, false)
		set(&category, patch.Category, true)
		set(&location, patch.Location, false)
		set(&storage, patch.StorageLocation, false)
		set(&owner, patch.PossibleOwner, false)
		set(&notes, patch.Notes, false)
		if fields := validate(description, category, location, storage, owner, notes, cur.RoomID != nil); len(fields) > 0 {
			return "", nil, apperr.Invalid("the item is invalid", fields...)
		}
		if err := s.q(ctx).UpdateItemDetails(ctx, lostfounddb.UpdateItemDetailsParams{
			TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, Description: description, Category: category, Location: nullable(location),
			StorageLocation: nullable(storage), PossibleOwner: nullable(owner), Notes: nullable(notes),
		}); err != nil {
			return "", nil, err
		}
		return "lostfound.updated", map[string]any{"description": description, "category": category, "storage_location": storage}, nil
	})
}

// Return hands a stored item back to its owner (lostfound.manage): who took it and what was checked (an ID card,
// a description of the contents, a booking number). Final.
func (s *Service) Return(ctx context.Context, propertyID, id int64, in ReturnInput) (Item, error) {
	in.ClaimantName = strings.TrimSpace(in.ClaimantName)
	in.ClaimantProof = strings.TrimSpace(in.ClaimantProof)
	in.Note = strings.TrimSpace(in.Note)
	var fields []apperr.FieldError
	if in.ClaimantName == "" || len([]rune(in.ClaimantName)) > 150 {
		fields = append(fields, fieldErr("claimant_name", "REQUIRED", "who takes the item (1-150 characters)"))
	}
	if len([]rune(in.ClaimantProof)) > 150 {
		fields = append(fields, fieldErr("claimant_proof", "TOO_LONG", "at most 150 characters"))
	}
	if len([]rune(in.Note)) > 500 {
		fields = append(fields, fieldErr("note", "TOO_LONG", "at most 500 characters"))
	}
	if len(fields) > 0 {
		return Item{}, apperr.Invalid("the hand-over is invalid", fields...)
	}
	return s.change(ctx, propertyID, id, func(ctx context.Context, p auth.Principal, bd civil.Date, cur lostfounddb.LostFoundItem) (string, any, error) {
		if err := s.q(ctx).CloseItem(ctx, lostfounddb.CloseItemParams{
			TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, Status: StatusReturned, ClosedOn: &bd, Now: s.clock.Now(), ActorID: p.ActorID(),
			ClaimantName: nullable(in.ClaimantName), ClaimantProof: nullable(in.ClaimantProof), CloseNote: nullable(in.Note),
		}); err != nil {
			return "", nil, err
		}
		return "lostfound.returned", map[string]any{"status": StatusReturned, "claimant": in.ClaimantName, "proof": in.ClaimantProof}, nil
	})
}

// Dispose closes a stored item that nobody claimed (lostfound.manage); a reason is required. Final.
func (s *Service) Dispose(ctx context.Context, propertyID, id int64, in DisposeInput) (Item, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case in.Reason == "":
		return Item{}, apperr.Invalid("a reason is required", fieldErr("reason", "REQUIRED", "what happened to the item"))
	case len([]rune(in.Reason)) > 500:
		return Item{}, apperr.Invalid("the reason is too long", fieldErr("reason", "TOO_LONG", "at most 500 characters"))
	}
	return s.change(ctx, propertyID, id, func(ctx context.Context, p auth.Principal, bd civil.Date, cur lostfounddb.LostFoundItem) (string, any, error) {
		if err := s.q(ctx).CloseItem(ctx, lostfounddb.CloseItemParams{
			TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, Status: StatusDisposed, ClosedOn: &bd, Now: s.clock.Now(), ActorID: p.ActorID(), CloseNote: &in.Reason,
		}); err != nil {
			return "", nil, err
		}
		return "lostfound.disposed", map[string]any{"status": StatusDisposed, "reason": in.Reason}, nil
	})
}
