package expected

import (
	"context"

	"kamarapms/internal/expected/expecteddb"
	"kamarapms/internal/platform/db"
)

// Loader reads the snapshot the evaluator works on. It only reads, so it runs with or without locks: the
// posting service calls it after locking the stays, a preview calls it with plain reads.
type Loader struct{ txm *db.TxManager }

// NewLoader returns the snapshot loader.
func NewLoader(txm *db.TxManager) *Loader { return &Loader{txm: txm} }

// OpenStayIDs lists the OPEN stays of a property, ascending (the scope of "all eligible stays").
func (l *Loader) OpenStayIDs(ctx context.Context, tenantID, propertyID int64) ([]int64, error) {
	return expecteddb.New(l.txm.DB(ctx)).ListOpenStayIDs(ctx, expecteddb.ListOpenStayIDsParams{TenantID: tenantID, PropertyID: propertyID})
}

// Load returns the snapshot of the given stays (every OPEN stay when stayIDs is empty).
func (l *Loader) Load(ctx context.Context, tenantID, propertyID int64, stayIDs []int64) (Snapshot, error) {
	q := expecteddb.New(l.txm.DB(ctx))
	if stayIDs == nil {
		stayIDs = []int64{}
	}
	stays, err := q.ListScopeStays(ctx, expecteddb.ListScopeStaysParams{TenantID: tenantID, PropertyID: propertyID, StayIds: stayIDs})
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Folios: map[int64]int64{}}
	ids := make([]int64, 0, len(stays))
	lineIDs := make([]int64, 0, len(stays))
	for _, s := range stays {
		name := s.GuestLastName
		if s.GuestFirstName != nil && *s.GuestFirstName != "" {
			name = *s.GuestFirstName + " " + s.GuestLastName
		}
		snap.Stays = append(snap.Stays, Stay{ID: s.ID, Number: s.StayNumber, Status: s.Status, LineStatus: s.LineStatus, ReservationRoomID: s.ReservationRoomID,
			Arrival: s.ArrivalDate, Departure: s.DepartureDate, GuestName: name})
		ids = append(ids, s.ID)
		lineIDs = append(lineIDs, s.ReservationRoomID)
	}
	if len(ids) == 0 {
		return snap, nil
	}
	segs, err := q.ListScopeSegments(ctx, expecteddb.ListScopeSegmentsParams{TenantID: tenantID, PropertyID: propertyID, StayIds: ids})
	if err != nil {
		return Snapshot{}, err
	}
	for _, s := range segs {
		snap.Segments = append(snap.Segments, Segment{ID: s.ID, StayID: s.StayID, RoomID: s.RoomID, RoomNumber: s.RoomNumber, Start: s.StartBusinessDate, End: s.EndBusinessDate})
	}
	nights, err := q.ListScopeNights(ctx, expecteddb.ListScopeNightsParams{TenantID: tenantID, PropertyID: propertyID, LineIds: lineIDs})
	if err != nil {
		return Snapshot{}, err
	}
	for _, n := range nights {
		snap.Nights = append(snap.Nights, Night{ReservationRoomID: n.ReservationRoomID, Date: n.StayDate, RatePlanID: n.RatePlanID, ChargeCodeID: n.ChargeCodeID,
			ChargeCode: n.ChargeCode, ChargeCodeActive: n.IsActive, ChargeCodeIsRoom: n.IsRoom, PriceMode: n.PriceMode, Amount: n.Amount})
	}
	posts, err := q.ListScopePostings(ctx, expecteddb.ListScopePostingsParams{TenantID: tenantID, PropertyID: propertyID, StayIds: ids})
	if err != nil {
		return Snapshot{}, err
	}
	for _, p := range posts {
		snap.Postings = append(snap.Postings, Posting{StayID: p.StayID, ServiceDate: p.ServiceDate, FolioItemID: p.FolioItemID, StayRoomID: p.StayRoomID})
	}
	folios, err := q.ListScopeFolios(ctx, expecteddb.ListScopeFoliosParams{TenantID: tenantID, PropertyID: propertyID, StayIds: ids})
	if err != nil {
		return Snapshot{}, err
	}
	for _, f := range folios {
		if f.StayID != nil {
			snap.Folios[*f.StayID] = f.ID
		}
	}
	return snap, nil
}
