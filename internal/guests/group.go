package guests

import (
	"context"
	"fmt"

	"kamarapms/internal/guests/guestsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// GroupGuests are the guests a bulk booking of a group needs (docs/architecture/30-group-folio.md, part A): the booker, made from the contact of the group when the group has none yet,
// and one placeholder for each room, which is named later (the rooming list, the check-in). They are made inside the transaction of the booking, so a booking that fails leaves none of them,
// and with no duplicate hints: the profiles are not people the desk typed in.
type GroupGuests struct {
	Contact      *Guest  // the new booker, nil when the group had one
	Placeholders []Guest // one for each name asked for, in order
}

// CreateForGroup makes the guests of a bulk booking (guest.write at the property). contact is the profile of the group's booker, nil when the group has one already. names are the last names of the
// placeholders ("group name · 7"). It takes the guest numbers from the sequence (the last lock level), so the caller must have taken its other locks before.
func (s *Service) CreateForGroup(ctx context.Context, originPropertyID int64, contact *Profile, names []string) (GroupGuests, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return GroupGuests{}, err
	}
	if err := s.authz.Require(ctx, originPropertyID, auth.PermGuestWrite); err != nil {
		return GroupGuests{}, err
	}
	var out GroupGuests
	if contact != nil {
		contact.Normalize()
		if fields := contact.Validate(civil.DateOf(s.clock.Now())); len(fields) > 0 {
			return GroupGuests{}, apperr.Invalid("the contact of the group is not a valid guest", fields...)
		}
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if err := db.EnterLockLevel(ctx, db.LevelSequences); err != nil {
			return err
		}
		q := s.q(ctx)
		next := func() (string, error) {
			num, err := q.NextGuestNumber(ctx, p.TenantID)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s%06d", num.Prefix, num.Number), nil
		}
		if contact != nil {
			code, err := next()
			if err != nil {
				return err
			}
			row, err := q.CreateGuest(ctx, guestsdb.CreateGuestParams{
				TenantID: p.TenantID, Code: code, OriginPropertyID: &originPropertyID, FirstName: nullable(contact.FirstName), LastName: contact.LastName,
				Email: nullable(contact.Email), Phone: nullable(contact.Phone), ActorID: p.ActorID(),
			})
			if err != nil {
				return err
			}
			g := toGuest(row)
			out.Contact = &g
		}
		for _, name := range names {
			code, err := next()
			if err != nil {
				return err
			}
			row, err := q.CreatePlaceholderGuest(ctx, guestsdb.CreatePlaceholderGuestParams{TenantID: p.TenantID, Code: code, OriginPropertyID: &originPropertyID, LastName: name, ActorID: p.ActorID()})
			if err != nil {
				return err
			}
			out.Placeholders = append(out.Placeholders, toGuest(row))
		}
		return nil
	})
	return out, err
}
