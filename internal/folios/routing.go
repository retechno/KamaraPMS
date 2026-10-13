package folios

import (
	"context"

	"kamarapms/internal/expected"
	"kamarapms/internal/folios/foliosdb"
)

// The target folio of a charge (docs/architecture/18-architecture-decisions.md, decision 1). A stay may have several folios, one for each payer, and this file is the only place that
// decides which of them a charge goes to. Anything that posts to a stay without being told a folio (the room nights of the night audit today, a point of sale one day) asks it.
//
// The rule, in order: a billing instruction for that charge code; else the room instruction if the charge is a room night; else the "all" instruction; else the guest folio. An
// instruction names a company, and its answer is the OPEN folio of the stay billed to that company. When that folio is closed or missing the answer says so (FolioID 0, Routed true):
// it is a blocker, and the charge is never sent to the guest folio instead, which would bill the guest for what the company agreed to pay.

// Route says what is being posted: the charge code, and whether it is the room night (a charge code of type ROOM, which a billing instruction can name as "the room").
type Route struct {
	ChargeCodeID int64
	IsRoom       bool
}

// RoomRoute is the route of a room night whose charge code is not known (the room instruction and the "all" instruction apply).
var RoomRoute = Route{IsRoom: true}

// Target is the folio a charge goes to. FolioID is 0 when there is no open folio to take it; Routed says a billing instruction chose it.
type Target = expected.Target

// ResolveTarget is the folio a charge of a stay goes to. It does not lock the folio: the caller that posts locks it (level 4) after the stays.
func (s *Service) ResolveTarget(ctx context.Context, tenantID, propertyID, stayID int64, route Route) (Target, error) {
	m, err := s.resolve(ctx, tenantID, propertyID, []int64{stayID})
	if err != nil {
		return Target{}, err
	}
	return m[stayID].For(route.ChargeCodeID, route.IsRoom), nil
}

// ResolveRoomTargets is the answer for the room nights of many stays at once (the evaluator of the expected charges asks it for a whole property): what each stay's nights do by
// default, and for a charge code an instruction names.
func (s *Service) ResolveRoomTargets(ctx context.Context, tenantID, propertyID int64, stayIDs []int64) (map[int64]expected.StayTargets, error) {
	m, err := s.resolve(ctx, tenantID, propertyID, stayIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]expected.StayTargets, len(m))
	for id, r := range m {
		out[id] = expected.StayTargets{Default: r.room(), ByCode: r.codes()}
	}
	return out, nil
}

// routing is what the instructions of a stay say, resolved to folios: the instructions of its line (to company folios of the stay) and, behind them, the instructions of its group (to the
// master folio of the group), unless the line is excluded from the routing of the group.
type routing struct {
	guest  Target
	all    *Target
	roomT  *Target
	byCode map[int64]Target
	// the group stage: ignored for an excluded line
	excluded bool
	gAll     *Target
	gRoom    *Target
	gByCode  map[int64]Target
}

// groupStage is the group's instruction for a charge, when the line takes part in the routing of its group.
func (r routing) groupStage(chargeCodeID int64, isRoom bool) (Target, bool) {
	if r.excluded {
		return Target{}, false
	}
	if t, ok := r.gByCode[chargeCodeID]; ok && chargeCodeID != 0 {
		return t, true
	}
	if isRoom && r.gRoom != nil {
		return *r.gRoom, true
	}
	if r.gAll != nil {
		return *r.gAll, true
	}
	return Target{}, false
}

// room is what a room night does when no charge code names it: the room instruction of the line, else its "all", else the group's, else the guest folio.
func (r routing) room() Target {
	if r.roomT != nil {
		return *r.roomT
	}
	if r.all != nil {
		return *r.all
	}
	if t, ok := r.groupStage(0, true); ok {
		return t
	}
	return r.guest
}

// codes is every charge code that has an instruction of its own, the instruction of the line winning over that of the group.
func (r routing) codes() map[int64]Target {
	if len(r.byCode) == 0 && (r.excluded || len(r.gByCode) == 0) {
		return r.byCode
	}
	out := map[int64]Target{}
	if !r.excluded {
		for k, v := range r.gByCode {
			out[k] = v
		}
	}
	for k, v := range r.byCode {
		out[k] = v
	}
	return out
}

// For is the one precedence rule: the instructions of the line first (a charge code, the room, all), then those of the group (the same order) unless the line is excluded from them, then the
// guest folio.
func (r routing) For(chargeCodeID int64, isRoom bool) Target {
	if t, ok := r.byCode[chargeCodeID]; ok && chargeCodeID != 0 {
		return t
	}
	if isRoom && r.roomT != nil {
		return *r.roomT
	}
	if r.all != nil {
		return *r.all
	}
	if t, ok := r.groupStage(chargeCodeID, isRoom); ok {
		return t
	}
	return r.guest
}

// resolve is the one decision, for many stays: the open guest folios, the instructions of the stays' lines and the open company folios they point at.
func (s *Service) resolve(ctx context.Context, tenantID, propertyID int64, stayIDs []int64) (map[int64]routing, error) {
	out := make(map[int64]routing, len(stayIDs))
	if len(stayIDs) == 0 {
		return out, nil
	}
	q := s.q(ctx)
	guests, err := q.ListStayGuestFolios(ctx, foliosdb.ListStayGuestFoliosParams{TenantID: tenantID, PropertyID: propertyID, StayIds: stayIDs})
	if err != nil {
		return nil, err
	}
	for _, g := range guests {
		if g.StayID != nil {
			out[*g.StayID] = routing{guest: Target{FolioID: g.ID}}
		}
	}
	rules, err := q.ListStayInstructions(ctx, foliosdb.ListStayInstructionsParams{TenantID: tenantID, PropertyID: propertyID, StayIds: stayIDs})
	if err != nil {
		return nil, err
	}
	companyFolios, err := q.ListStayOpenCompanyFolios(ctx, foliosdb.ListStayOpenCompanyFoliosParams{TenantID: tenantID, PropertyID: propertyID, StayIds: stayIDs})
	if err != nil {
		return nil, err
	}
	type key struct{ stay, company int64 }
	open := map[key]int64{}
	for _, f := range companyFolios {
		if f.StayID != nil && f.BillToCompanyID != nil {
			open[key{*f.StayID, *f.BillToCompanyID}] = f.ID
		}
	}
	for _, id := range stayIDs {
		if _, ok := out[id]; !ok {
			out[id] = routing{} // no open guest folio: FolioID 0, not routed
		}
	}
	groupRules, err := q.ListStayGroupRules(ctx, foliosdb.ListStayGroupRulesParams{TenantID: tenantID, PropertyID: propertyID, StayIds: stayIDs})
	if err != nil {
		return nil, err
	}
	for _, rule := range groupRules {
		r := out[rule.StayID]
		r.excluded = rule.Excluded
		// the master of the group, when it is open: a rule whose master is closed or missing answers FolioID 0 and Routed, which is ROUTING_TARGET_CLOSED, never the guest folio
		t := Target{Routed: true}
		if rule.MasterStatus == "OPEN" {
			t.FolioID = rule.MasterID
		}
		switch rule.Scope {
		case "ALL":
			r.gAll = &t
		case "ROOM":
			r.gRoom = &t
		case "CHARGE_CODE":
			if r.gByCode == nil {
				r.gByCode = map[int64]Target{}
			}
			r.gByCode[*rule.ChargeCodeID] = t
		}
		out[rule.StayID] = r
	}
	for _, rule := range rules {
		r := out[rule.StayID]
		t := Target{FolioID: open[key{rule.StayID, rule.CompanyID}], Routed: true}
		switch rule.Scope {
		case "ALL":
			r.all = &t
		case "ROOM":
			r.roomT = &t
		case "CHARGE_CODE":
			if r.byCode == nil {
				r.byCode = map[int64]Target{}
			}
			r.byCode[*rule.ChargeCodeID] = t
		}
		out[rule.StayID] = r
	}
	return out, nil
}
