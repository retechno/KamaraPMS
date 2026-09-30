package guests

import (
	"context"
	"fmt"
	"slices"

	"kamarapms/internal/audit"
	"kamarapms/internal/guests/guestsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Service is the guests application service.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
}

// NewService wires the guests service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days}
}

func (s *Service) q(ctx context.Context) *guestsdb.Queries { return guestsdb.New(s.txm.DB(ctx)) }

// scope is what the caller may see. Guest profiles are tenant-wide, so visibility follows the caller's
// properties: a guest is visible when the caller may search the whole tenant (guest.search_all at one of
// their properties) or when the guest is linked to one of the properties where they hold guest.read.
type scope struct {
	propertyIDs []int64
	all         bool
}

// scopeFor computes the caller's scope. With a property id the scope is that property only (the caller
// must hold guest.read there). Without one it spans every property where the caller holds guest.read, and
// fails with PERMISSION_DENIED if there is none.
func (s *Service) scopeFor(ctx context.Context, p auth.Principal, propertyID *int64) (scope, error) {
	if propertyID != nil {
		if err := s.authz.Require(ctx, *propertyID, auth.PermGuestRead); err != nil {
			return scope{}, err
		}
	}
	readable, err := s.authz.PropertiesWith(ctx, auth.PermGuestRead)
	if err != nil {
		return scope{}, err
	}
	searchAll, err := s.authz.PropertiesWith(ctx, auth.PermGuestSearchAll)
	if err != nil {
		return scope{}, err
	}
	if propertyID != nil {
		readable = []int64{*propertyID}
	}
	if len(readable) == 0 && !p.IsTenantAdmin {
		return scope{}, apperr.Forbidden("PERMISSION_DENIED", "your role does not allow viewing guests").
			WithContext("permission", string(auth.PermGuestRead))
	}
	sc := scope{propertyIDs: readable}
	for _, id := range searchAll {
		if slices.Contains(readable, id) {
			sc.all = true
		}
	}
	return sc, nil
}

// lenientScope is like scopeFor without a property but never fails: no readable property means nothing is visible.
func (s *Service) lenientScope(ctx context.Context, p auth.Principal) (scope, error) {
	sc, err := s.scopeFor(ctx, p, nil)
	if apperr.IsCode(err, "PERMISSION_DENIED") {
		return scope{propertyIDs: []int64{}}, nil
	}
	return sc, err
}

func (sc scope) ids() []int64 {
	if sc.propertyIDs == nil {
		return []int64{}
	}
	return sc.propertyIDs
}

// SearchAfter is the keyset position of the alphabetical guest list.
type SearchAfter struct {
	Last  string `json:"l"`
	First string `json:"f"`
	ID    int64  `json:"i"`
}

// SearchResult is one page of guests with the position to continue from.
type SearchResult struct {
	Guests []Guest
	Next   *SearchAfter // set when more results exist
}

// Search finds visible guests by free text (names, email, phone, ID number, code), alphabetically.
func (s *Service) Search(ctx context.Context, q string, propertyID *int64, after *SearchAfter, limit int) (SearchResult, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	sc, err := s.scopeFor(ctx, p, propertyID)
	if err != nil {
		return SearchResult{}, err
	}
	if limit < 1 || limit > maxSearchLimit {
		limit = 50
	}
	arg := guestsdb.SearchGuestsParams{
		TenantID: p.TenantID, AllAccess: sc.all || p.IsTenantAdmin, PropertyIds: sc.ids(), Tokens: SearchTokens(q), RowLimit: rowLimit(limit + 1),
	}
	if arg.Tokens == nil {
		arg.Tokens = []string{}
	}
	if after != nil {
		arg.AfterID, arg.AfterLast, arg.AfterFirst = &after.ID, &after.Last, &after.First
	}
	rows, err := s.q(ctx).SearchGuests(ctx, arg)
	if err != nil {
		return SearchResult{}, err
	}
	var res SearchResult
	for i, r := range rows {
		if i == limit {
			last := rows[limit-1]
			res.Next = &SearchAfter{Last: last.SortLast, First: last.SortFirst, ID: last.Guest.ID}
			break
		}
		res.Guests = append(res.Guests, toGuest(r.Guest))
	}
	return res, nil
}

// View is a guest with what the caller may do with it.
type View struct {
	Guest
	CanEdit bool `json:"can_edit"`
}

// Get returns a visible guest.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return View{}, err
	}
	sc, err := s.scopeFor(ctx, p, nil)
	if err != nil {
		return View{}, err
	}
	row, err := s.q(ctx).GetVisibleGuest(ctx, guestsdb.GetVisibleGuestParams{
		TenantID: p.TenantID, ID: id, AllAccess: sc.all || p.IsTenantAdmin, PropertyIds: sc.ids(),
	})
	if err != nil {
		return View{}, orNotFound(err)
	}
	canEdit, err := s.canWrite(ctx, p, id)
	if err != nil {
		return View{}, err
	}
	return View{Guest: toGuest(row), CanEdit: canEdit}, nil
}

// canWrite: guest.write at a property where the guest is linked.
func (s *Service) canWrite(ctx context.Context, p auth.Principal, id int64) (bool, error) {
	writable, err := s.authz.PropertiesWith(ctx, auth.PermGuestWrite)
	if err != nil || len(writable) == 0 {
		return false, err
	}
	linked, err := s.q(ctx).GuestLinkedTo(ctx, guestsdb.GuestLinkedToParams{TenantID: p.TenantID, ID: id, PropertyIds: writable})
	return err == nil && linked, err
}

// CreateResult is the new guest with hints about profiles that may be the same person.
type CreateResult struct {
	Guest                Guest
	PossibleDuplicates   []PossibleDuplicate
	HiddenDuplicateCount int // matches the caller may not see (they exist at other properties)
}

// Create adds a profile (guest.write at the origin property). Duplicates are a warning only: the profile
// is always created, and the result lists the visible look-alikes.
func (s *Service) Create(ctx context.Context, originPropertyID int64, in Profile) (CreateResult, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	if err := s.authz.Require(ctx, originPropertyID, auth.PermGuestWrite); err != nil {
		return CreateResult{}, err
	}
	in.Normalize()
	if fields := in.Validate(civil.DateOf(s.clock.Now())); len(fields) > 0 {
		return CreateResult{}, apperr.Invalid("the guest is invalid", fields...)
	}

	var out CreateResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, originPropertyID)
		if err != nil {
			return err
		}
		sc, err := s.lenientScope(ctx, p)
		if err != nil {
			return err
		}
		if err := db.EnterLockLevel(ctx, db.LevelSequences); err != nil {
			return err
		}
		q := s.q(ctx)
		num, err := q.NextGuestNumber(ctx, p.TenantID)
		if err != nil {
			return err
		}
		row, err := q.CreateGuest(ctx, guestsdb.CreateGuestParams{
			TenantID: p.TenantID, Code: fmt.Sprintf("%s%06d", num.Prefix, num.Number), OriginPropertyID: &originPropertyID,
			FirstName: nullable(in.FirstName), LastName: in.LastName, Email: nullable(in.Email), Phone: nullable(in.Phone),
			Nationality: nullable(in.Nationality), CountryCode: nullable(in.CountryCode), DateOfBirth: dateOrNil(in.DateOfBirth),
			Gender: nullable(in.Gender), IDType: nullable(in.IDType), IDNumber: nullable(in.IDNumber), Address: nullable(in.Address),
			City: nullable(in.City), Notes: nullable(in.Notes), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out.Guest = toGuest(row)

		cands, err := q.FindGuestDuplicates(ctx, guestsdb.FindGuestDuplicatesParams{
			TenantID: p.TenantID, ExcludeID: out.Guest.ID, AllAccess: sc.all || p.IsTenantAdmin, PropertyIds: sc.ids(),
			Email: in.Email, PhoneDigits: PhoneDigits(in.Phone), IDNumber: in.IDNumber, IDType: in.IDType,
			DateOfBirth: in.DateOfBirth, LastName: in.LastName, FirstName: in.FirstName,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if !c.Visible {
				out.HiddenDuplicateCount++
				continue
			}
			g := toGuest(c.Guest)
			out.PossibleDuplicates = append(out.PossibleDuplicates, PossibleDuplicate{Guest: g, Reasons: DuplicateReasons(in, g)})
		}
		bd := day.BusinessDate
		return s.audit.Write(ctx, audit.Entry{
			TenantID: p.TenantID, PropertyID: &originPropertyID, BusinessDate: &bd, UserID: p.ActorID(),
			Action: "guest.created", EntityType: "guest", EntityID: out.Guest.ID, New: auditView(out.Guest),
		})
	})
	return out, err
}

// Patch changes selected fields; nil leaves a field unchanged and an empty string clears it.
type Patch struct {
	FirstName, LastName, Email, Phone, Nationality, CountryCode *string
	DateOfBirth                                                 *civil.Date
	ClearDateOfBirth                                            bool
	Gender, IDType, IDNumber, Address, City, Notes              *string
}

// Update edits a visible profile. It needs guest.write at a property where the guest is linked.
func (s *Service) Update(ctx context.Context, id int64, patch Patch) (View, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return View{}, err
	}
	sc, err := s.scopeFor(ctx, p, nil)
	if err != nil {
		return View{}, err
	}

	var out View
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		if _, err := q.GetGuestForUpdate(ctx, guestsdb.GetGuestForUpdateParams{TenantID: p.TenantID, ID: id}); err != nil {
			return orNotFound(err)
		}
		// Re-read under the lock, applying the visibility rule: invisible guests do not exist for the caller.
		row, err := q.GetVisibleGuest(ctx, guestsdb.GetVisibleGuestParams{
			TenantID: p.TenantID, ID: id, AllAccess: sc.all || p.IsTenantAdmin, PropertyIds: sc.ids(),
		})
		if err != nil {
			return orNotFound(err)
		}
		ok, err := s.canWrite(ctx, p, id)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.Forbidden("PERMISSION_DENIED", "guest.write is required at a property where the guest is linked").
				WithContext("permission", string(auth.PermGuestWrite))
		}

		before := toGuest(row)
		in := toProfile(before)
		set := func(dst *string, v *string) {
			if v != nil {
				*dst = *v
			}
		}
		set(&in.FirstName, patch.FirstName)
		set(&in.LastName, patch.LastName)
		set(&in.Email, patch.Email)
		set(&in.Phone, patch.Phone)
		set(&in.Nationality, patch.Nationality)
		set(&in.CountryCode, patch.CountryCode)
		set(&in.Gender, patch.Gender)
		set(&in.IDType, patch.IDType)
		set(&in.IDNumber, patch.IDNumber)
		set(&in.Address, patch.Address)
		set(&in.City, patch.City)
		set(&in.Notes, patch.Notes)
		if patch.DateOfBirth != nil {
			in.DateOfBirth = patch.DateOfBirth
		}
		if patch.ClearDateOfBirth {
			in.DateOfBirth = nil
		}
		in.Normalize()
		if fields := in.Validate(civil.DateOf(s.clock.Now())); len(fields) > 0 {
			return apperr.Invalid("the guest is invalid", fields...)
		}

		updated, err := q.UpdateGuest(ctx, guestsdb.UpdateGuestParams{
			TenantID: p.TenantID, ID: id, FirstName: nullable(in.FirstName), LastName: in.LastName, Email: nullable(in.Email),
			Phone: nullable(in.Phone), Nationality: nullable(in.Nationality), CountryCode: nullable(in.CountryCode),
			DateOfBirth: dateOrNil(in.DateOfBirth), Gender: nullable(in.Gender), IDType: nullable(in.IDType),
			IDNumber: nullable(in.IDNumber), Address: nullable(in.Address), City: nullable(in.City), Notes: nullable(in.Notes),
			ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = View{Guest: toGuest(updated), CanEdit: true}

		entry := audit.Entry{
			TenantID: p.TenantID, UserID: p.ActorID(), Action: "guest.updated", EntityType: "guest", EntityID: id,
			Old: auditView(before), New: auditView(out.Guest),
		}
		if origin := before.OriginPropertyID; origin != nil {
			if day, err := s.days.CurrentBusinessDay(ctx, *origin); err == nil {
				bd := day.BusinessDate
				entry.PropertyID, entry.BusinessDate = origin, &bd
			}
		}
		return s.audit.Write(ctx, entry)
	})
	return out, err
}

// History is a guest's reservations and stays. Items at properties the caller may not see are not returned
// but counted in HiddenCount.
type History struct {
	Items       []HistoryItem
	HiddenCount int64
}

// History lists a visible guest's reservations and stays across properties, newest first. The caller sees
// their own properties only, unless they hold guest.history_all_properties.
func (s *Service) History(ctx context.Context, id int64, offset, limit int) (History, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return History{}, err
	}
	sc, err := s.scopeFor(ctx, p, nil)
	if err != nil {
		return History{}, err
	}
	q := s.q(ctx)
	if _, err := q.GetVisibleGuest(ctx, guestsdb.GetVisibleGuestParams{
		TenantID: p.TenantID, ID: id, AllAccess: sc.all || p.IsTenantAdmin, PropertyIds: sc.ids(),
	}); err != nil {
		return History{}, orNotFound(err)
	}
	histAll, err := s.authz.PropertiesWith(ctx, auth.PermGuestHistoryAllProperties)
	if err != nil {
		return History{}, err
	}
	all := p.IsTenantAdmin
	for _, pid := range histAll {
		all = all || slices.Contains(sc.propertyIDs, pid)
	}

	var out History
	guestID := id
	rows, err := q.ListGuestHistory(ctx, guestsdb.ListGuestHistoryParams{
		TenantID: p.TenantID, GuestID: &guestID, AllProperties: all, PropertyIds: sc.ids(),
		RowLimit: rowLimit(limit), RowOffset: int32(min(max(offset, 0), 1_000_000)), //nolint:gosec // G115: bounded above
	})
	if err != nil {
		return History{}, err
	}
	for _, r := range rows {
		out.Items = append(out.Items, HistoryItem{
			Type: r.Kind, ID: r.ID, Number: r.Number, Role: r.Role, Status: r.Status, PropertyID: r.PropertyID,
			PropertyCode: r.PropertyCode, PropertyName: r.PropertyName, ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate,
		})
	}
	if !all {
		c, err := q.CountGuestHistory(ctx, guestsdb.CountGuestHistoryParams{TenantID: p.TenantID, GuestID: &guestID, PropertyIds: sc.ids()})
		if err != nil {
			return History{}, err
		}
		out.HiddenCount = c.HiddenCount
	}
	return out, nil
}
