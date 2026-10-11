package groups

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/groups/groupsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Service manages booking groups (group.manage to write; reservation.read to read).
type Service struct {
	txm   *db.TxManager
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service) *Service {
	return &Service{txm: txm, audit: a, authz: authz, days: days}
}

func (s *Service) q(ctx context.Context) *groupsdb.Queries { return groupsdb.New(s.txm.DB(ctx)) }

func errNotFound() *apperr.Error {
	return apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
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

func (s *Service) actor(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "booking_group", EntityID: id, EntityLabel: label, Old: old, New: updated}
}

func toGroup(g groupsdb.BookingGroup, company *string, reservations, rooms int32) Group {
	return Group{
		ID: g.ID, Code: g.Code, Name: g.Name, CompanyID: g.CompanyID, CompanyName: deref(company), ContactName: deref(g.ContactName),
		ContactEmail: deref(g.ContactEmail), ContactPhone: deref(g.ContactPhone), ArrivalDate: g.ArrivalDate, DepartureDate: g.DepartureDate,
		Notes: deref(g.Notes), IsActive: g.IsActive, ReservationCount: int(reservations), RoomCount: int(rooms), CreatedAt: g.CreatedAt,
	}
}

// List lists groups, newest first, before the id beforeID (0 = from the newest).
func (s *Service) List(ctx context.Context, propertyID, beforeID int64, active *bool, companyID *int64, q string, limit int) ([]Group, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	if beforeID <= 0 {
		beforeID = 1 << 62
	}
	rows, err := s.q(ctx).ListGroups(ctx, groupsdb.ListGroupsParams{
		TenantID: p.TenantID, PropertyID: propertyID, BeforeID: beforeID, Active: active, CompanyID: companyID, Q: nullable(q), RowLimit: int32(max(1, min(limit, 1000))), //nolint:gosec // G115: bounded
	})
	if err != nil {
		return nil, err
	}
	out := make([]Group, len(rows))
	for i, r := range rows {
		out[i] = toGroup(groupsdb.BookingGroup{
			ID: r.ID, Code: r.Code, Name: r.Name, CompanyID: r.CompanyID, ContactName: r.ContactName, ContactEmail: r.ContactEmail, ContactPhone: r.ContactPhone,
			ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, Notes: r.Notes, IsActive: r.IsActive, CreatedAt: r.CreatedAt,
		}, r.CompanyName, r.ReservationCount, r.RoomCount)
	}
	return out, nil
}

func (s *Service) load(ctx context.Context, tenantID, propertyID, id int64) (Group, error) {
	r, err := s.q(ctx).GetGroup(ctx, groupsdb.GetGroupParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, errNotFound()
	}
	if err != nil {
		return Group{}, err
	}
	return toGroup(groupsdb.BookingGroup{
		ID: r.ID, Code: r.Code, Name: r.Name, CompanyID: r.CompanyID, ContactName: r.ContactName, ContactEmail: r.ContactEmail, ContactPhone: r.ContactPhone,
		ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, Notes: r.Notes, IsActive: r.IsActive, CreatedAt: r.CreatedAt,
	}, r.CompanyName, r.ReservationCount, r.RoomCount), nil
}

// Get returns one group.
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Group, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return Group{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// Members lists the reservations of a group.
func (s *Service) Members(ctx context.Context, propertyID, id int64) ([]Member, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	if _, err := s.load(ctx, p.TenantID, propertyID, id); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListMembers(ctx, groupsdb.ListMembersParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &id})
	if err != nil {
		return nil, err
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		out[i] = Member{
			ReservationID: r.ID, ConfirmationNumber: r.ConfirmationNumber, Status: r.Status, GuestName: r.GuestName, CompanyID: r.CompanyID,
			ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, RoomCount: int(r.RoomCount),
		}
	}
	return out, nil
}

// Create adds a group (group.manage). The company, when given, must exist and be active.
func (s *Service) Create(ctx context.Context, propertyID int64, in Input) (Group, error) {
	p, err := s.actor(ctx, propertyID, auth.PermGroupManage)
	if err != nil {
		return Group{}, err
	}
	in.Normalize()
	if fields := in.Validate(true); len(fields) > 0 {
		return Group{}, apperr.Invalid("the group is invalid", fields...)
	}
	var out Group
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.requireCompany(ctx, p.TenantID, propertyID, in.CompanyID); err != nil {
			return err
		}
		row, err := s.q(ctx).CreateGroup(ctx, groupsdb.CreateGroupParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, CompanyID: in.CompanyID, ContactName: nullable(in.ContactName),
			ContactEmail: nullable(in.ContactEmail), ContactPhone: nullable(in.ContactPhone), ArrivalDate: in.ArrivalDate, DepartureDate: in.DepartureDate,
			Notes: nullable(in.Notes), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		if out, err = s.load(ctx, p.TenantID, propertyID, row.ID); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "group.created", out.ID, out.Code, nil, out))
	})
	return out, err
}

func (s *Service) requireCompany(ctx context.Context, tenantID, propertyID int64, id *int64) error {
	if id == nil {
		return nil
	}
	active, err := s.q(ctx).CompanyActive(ctx, groupsdb.CompanyActiveParams{TenantID: tenantID, PropertyID: propertyID, ID: *id})
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("COMPANY_NOT_FOUND", "the company does not exist in this property")
	}
	if err != nil {
		return err
	}
	if !active {
		return apperr.Conflict("COMPANY_INACTIVE", "the company is inactive")
	}
	return nil
}

// Patch changes selected attributes; nil fields stay unchanged. CompanyID below 1 removes the company.
type Patch struct {
	Name          *string
	CompanyID     *int64
	ContactName   *string
	ContactEmail  *string
	ContactPhone  *string
	ArrivalDate   *civil.Date
	DepartureDate *civil.Date
	Notes         *string
	IsActive      *bool
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// Update edits a group (group.manage). The dates cannot shrink past a reservation's rooms and the company cannot
// change while reservations bill another one: both 409.
func (s *Service) Update(ctx context.Context, propertyID, id int64, patch Patch) (Group, error) {
	p, err := s.actor(ctx, propertyID, auth.PermGroupManage)
	if err != nil {
		return Group{}, err
	}
	var out Group
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Groups, db.ForUpdate, propertyID, []int64{id}); err != nil {
			if apperr.IsCode(err, "NOT_FOUND") {
				return errNotFound()
			}
			return err
		}
		before, err := s.load(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		in := Input{Code: before.Code, Name: before.Name, CompanyID: before.CompanyID, ContactName: before.ContactName, ContactEmail: before.ContactEmail,
			ContactPhone: before.ContactPhone, ArrivalDate: before.ArrivalDate, DepartureDate: before.DepartureDate, Notes: before.Notes, IsActive: before.IsActive}
		apply(&in.Name, patch.Name)
		apply(&in.ContactName, patch.ContactName)
		apply(&in.ContactEmail, patch.ContactEmail)
		apply(&in.ContactPhone, patch.ContactPhone)
		apply(&in.ArrivalDate, patch.ArrivalDate)
		apply(&in.DepartureDate, patch.DepartureDate)
		apply(&in.Notes, patch.Notes)
		apply(&in.IsActive, patch.IsActive)
		if patch.CompanyID != nil {
			in.CompanyID = patch.CompanyID
		}
		in.Normalize()
		if fields := in.Validate(false); len(fields) > 0 {
			return apperr.Invalid("the group is invalid", fields...)
		}
		q := s.q(ctx)
		if !in.ArrivalDate.Equal(before.ArrivalDate) || !in.DepartureDate.Equal(before.DepartureDate) {
			n, err := q.CountLinesOutside(ctx, groupsdb.CountLinesOutsideParams{PropertyID: propertyID, GroupID: &id, ArrivalDate: in.ArrivalDate, DepartureDate: in.DepartureDate})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("GROUP_HAS_ROOMS_OUTSIDE_DATES", "rooms of the group fall outside the new dates").WithContext("rooms", n)
			}
		}
		if !sameID(in.CompanyID, before.CompanyID) {
			if err := s.requireCompany(ctx, p.TenantID, propertyID, in.CompanyID); err != nil {
				return err
			}
			n, err := q.CountReservationsOfOtherCompany(ctx, groupsdb.CountReservationsOfOtherCompanyParams{PropertyID: propertyID, GroupID: &id, CompanyID: in.CompanyID})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("GROUP_HAS_RESERVATIONS", "reservations of the group bill another company").WithContext("reservations", n)
			}
		}
		if _, err := q.UpdateGroup(ctx, groupsdb.UpdateGroupParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, CompanyID: in.CompanyID, ContactName: nullable(in.ContactName),
			ContactEmail: nullable(in.ContactEmail), ContactPhone: nullable(in.ContactPhone), ArrivalDate: in.ArrivalDate, DepartureDate: in.DepartureDate,
			Notes: nullable(in.Notes), IsActive: in.IsActive, ActorID: p.ActorID(),
		}); err != nil {
			return err
		}
		if out, err = s.load(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "group.updated", id, out.Code, before, out))
	})
	return out, err
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
