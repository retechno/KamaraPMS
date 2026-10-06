package folios

import (
	"context"

	"kamarapms/internal/folios/foliosdb"
)

// The target folio of a charge (docs/architecture/18-architecture-decisions.md, decision 1). A stay may have several folios, one for each payer, and this file is the only place that
// decides which of them a charge goes to. Anything that posts to a stay without being told a folio (the room nights of the night audit today, a point of sale one day) asks it.
//
// In this step the routing is the default one: a charge goes to the OPEN guest folio of the stay. The question already carries what the routing of the next step needs, the charge code, so
// that billing instructions (the room to a company, one charge code to a company) can be added behind it without a caller changing.

// Route says what is being posted: the charge code, and whether it is the room night (a charge code of type ROOM, which a billing instruction can name as "the room").
type Route struct {
	ChargeCodeID int64
	IsRoom       bool
}

// RoomRoute is the route of a room night.
var RoomRoute = Route{IsRoom: true}

// ResolveTarget is the folio a charge of a stay goes to, and whether the stay has one (an open folio of the stay that can take it). It does not lock the folio: the caller that posts
// locks it (level 4) after the stays.
func (s *Service) ResolveTarget(ctx context.Context, tenantID, propertyID, stayID int64, route Route) (folioID int64, ok bool, err error) {
	m, err := s.resolve(ctx, tenantID, propertyID, []int64{stayID}, route)
	if err != nil {
		return 0, false, err
	}
	folioID, ok = m[stayID]
	return folioID, ok, nil
}

// ResolveRoomTargets is ResolveTarget for the room nights of many stays at once (the evaluator of the expected charges asks it for a whole property): stay id to folio id. A stay with no
// open folio is not in the map.
func (s *Service) ResolveRoomTargets(ctx context.Context, tenantID, propertyID int64, stayIDs []int64) (map[int64]int64, error) {
	return s.resolve(ctx, tenantID, propertyID, stayIDs, RoomRoute)
}

// resolve is the one decision. Default routing: the open guest folio of the stay, whatever the route.
func (s *Service) resolve(ctx context.Context, tenantID, propertyID int64, stayIDs []int64, _ Route) (map[int64]int64, error) {
	out := make(map[int64]int64, len(stayIDs))
	if len(stayIDs) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).ListStayGuestFolios(ctx, foliosdb.ListStayGuestFoliosParams{TenantID: tenantID, PropertyID: propertyID, StayIds: stayIDs})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.StayID != nil {
			out[*r.StayID] = r.ID
		}
	}
	return out, nil
}
