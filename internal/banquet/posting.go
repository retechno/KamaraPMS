package banquet

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// Posting a session to the EVENT folio (36 4.5). The charges go through folios.PostEventCharge, the one writer of folio_items, with the price mode the line froze.

type postTarget struct{ sessionID, eventID int64 }

// postOpts says how a session is posted.
type postOpts struct {
	auto        bool
	useEstimate bool
}

// SetActual enters the number of guests that came (banquet.manage); it can be entered again until the session is posted.
func (s *Service) SetActual(ctx context.Context, propertyID, eventID, sessionID int64, in ActualInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	if in.PaxActual != nil && (*in.PaxActual < 0 || *in.PaxActual > maxPax) {
		return Event{}, invalid("session", fieldErr("pax_actual", "INVALID_VALUE", "0 up to 100000"))
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if hdr.Status != StatusDefinite && hdr.Status != StatusTentative {
			return apperr.Conflict("EVENT_STATUS_INVALID", "the guests that came are entered on a tentative or definite event").WithContext("status", hdr.Status)
		}
		old, err := s.sessionForLine(ctx, p.TenantID, propertyID, eventID, sessionID)
		if err != nil {
			return err
		}
		if n, err := s.q(ctx).SetSessionActual(ctx, banquetdb.SetSessionActualParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, ID: sessionID, PaxActual: int32Ptr(in.PaxActual)}); err != nil {
			return err
		} else if n == 0 {
			return notFound("SESSION_NOT_FOUND", "session")
		}
		if err := s.refreshEstimate(ctx, p, propertyID, bd, eventID); err != nil {
			return err
		}
		if out, err = s.event(ctx, p.TenantID, propertyID, eventID); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.session_actual_entered", "banquet_session", sessionID, out.Number+" / "+old.Name,
			map[string]any{"pax_actual": old.PaxActual}, map[string]any{"pax_actual": in.PaxActual}))
	})
	return out, err
}

// PostSession posts a session by hand (banquet.manage): any session of any day up to the open business day, in both modes. The guests that came can be entered with the request.
func (s *Service) PostSession(ctx context.Context, propertyID, eventID, sessionID int64, in PostInput) (PostResult, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return PostResult{}, err
	}
	if in.PaxActual != nil && (*in.PaxActual < 0 || *in.PaxActual > maxPax) {
		return PostResult{}, invalid("session", fieldErr("pax_actual", "INVALID_VALUE", "0 up to 100000"))
	}
	var out PostResult
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if in.PaxActual != nil {
			if n, err := s.q(ctx).SetSessionActual(ctx, banquetdb.SetSessionActualParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, ID: sessionID, PaxActual: int32Ptr(in.PaxActual)}); err != nil {
				return err
			} else if n == 0 {
				return notFound("SESSION_NOT_FOUND", "session")
			}
		}
		res, err := s.postSessions(ctx, p, propertyID, bd, []postTarget{{sessionID: sessionID, eventID: eventID}}, postOpts{useEstimate: in.UseEstimate})
		if err != nil {
			return err
		}
		out = res[0]
		return nil
	})
	return out, err
}

// PostDueSessions is the day close (36 4.5): it posts every session of a definite event, dated the business date or earlier, that is not posted and whose event is posted by the close. A session with no
// guaranteed and no actual number is left (the close warns, a person decides). It runs in the transaction of the close, which holds the business day; it takes the folios first and the events after.
func (s *Service) PostDueSessions(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) (int, error) {
	due, err := s.q(ctx).DueSessions(ctx, banquetdb.DueSessionsParams{TenantID: p.TenantID, PropertyID: propertyID, Day: bd})
	if err != nil || len(due) == 0 {
		return 0, err
	}
	targets := make([]postTarget, len(due))
	for i, d := range due {
		targets[i] = postTarget{sessionID: d.SessionID, eventID: d.EventID}
	}
	res, err := s.postSessions(ctx, p, propertyID, bd, targets, postOpts{auto: true})
	return len(res), err
}

// postSessions posts the sessions in the transaction of the caller, which holds the open business day. It reads the folios of the events without a lock, takes them all (level 42, ascending), then the
// events (level 45, ascending), and reads again what it decided on: an event whose folio changed meanwhile is a VERSION_CONFLICT. A session that the automatic close cannot bill (no guests) is left out.
func (s *Service) postSessions(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, targets []postTarget, o postOpts) ([]PostResult, error) {
	slices.SortFunc(targets, func(a, b postTarget) int {
		if a.eventID != b.eventID {
			return int(a.eventID - b.eventID)
		}
		return int(a.sessionID - b.sessionID)
	})
	folioOf := map[int64]int64{}
	var folioIDs, eventIDs []int64
	for _, t := range targets {
		if _, ok := folioOf[t.eventID]; ok {
			continue
		}
		f, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, t.eventID)
		if err != nil {
			return nil, err
		}
		folioOf[t.eventID] = 0
		eventIDs = append(eventIDs, t.eventID)
		if f != nil {
			folioOf[t.eventID] = f.ID
			folioIDs = append(folioIDs, f.ID)
		}
	}
	if len(folioIDs) > 0 {
		slices.Sort(folioIDs)
		if err := s.folios.LockFolios(ctx, propertyID, db.ForUpdate, slices.Compact(folioIDs)...); err != nil {
			return nil, err
		}
	}
	if err := db.LockRows(ctx, db.BanquetEvents, db.ForUpdate, propertyID, eventIDs); err != nil {
		return nil, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	var out []PostResult
	for _, t := range targets {
		r, ok, err := s.postOne(ctx, p, propertyID, bd, t, folioOf[t.eventID], o)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Service) postOne(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, t postTarget, folioID int64, o postOpts) (PostResult, bool, error) {
	q := s.q(ctx)
	hdr, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: t.eventID})
	if err != nil {
		return PostResult{}, false, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	if hdr.Status != StatusDefinite {
		return PostResult{}, false, apperr.Conflict("EVENT_STATUS_INVALID", "only a session of a definite event is posted").WithContext("status", hdr.Status)
	}
	// the folio that was locked is the folio of the event now; an event that has none gets it here (it is ours, nobody else can have locked it)
	f, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, t.eventID)
	if err != nil {
		return PostResult{}, false, err
	}
	switch {
	case f == nil && folioID == 0:
		made, err := s.folios.OpenEventFolio(ctx, p, propertyID, t.eventID, hdr.BookerCompanyID)
		if err != nil {
			return PostResult{}, false, err
		}
		folioID = made.ID
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.folio_opened", "banquet_event", t.eventID, hdr.Number, nil, map[string]any{"folio_id": made.ID, "folio_number": made.Number})); err != nil {
			return PostResult{}, false, err
		}
	case f == nil || f.ID != folioID:
		return PostResult{}, false, apperr.Conflict("VERSION_CONFLICT", "the folio of the event changed: ask again")
	}
	sn, err := s.sessionOf(ctx, p.TenantID, propertyID, t.eventID, t.sessionID)
	if err != nil {
		return PostResult{}, false, err
	}
	if sn.Status != "ACTIVE" {
		return PostResult{}, false, apperr.Conflict("SESSION_CANCELLED", "a cancelled session is not posted")
	}
	posted, err := q.SessionHasPosting(ctx, banquetdb.SessionHasPostingParams{PropertyID: propertyID, SessionID: t.sessionID})
	if err != nil {
		return PostResult{}, false, err
	}
	if posted {
		return PostResult{}, false, apperr.Conflict("SESSION_ALREADY_POSTED", "the session is posted already").WithContext("session_id", t.sessionID)
	}
	if !o.auto && sn.SessionDate.After(bd) {
		return PostResult{}, false, apperr.Conflict("SESSION_NOT_DUE", "a session of a day after the business date is not posted").WithContext("session_date", sn.SessionDate.String())
	}
	pax, basis, onGuarantee, ok := BilledPax(sn.PaxGuaranteed, sn.PaxActual)
	if !ok {
		if o.auto {
			return PostResult{}, false, nil
		}
		if !o.useEstimate {
			return PostResult{}, false, apperr.Conflict("NO_GUARANTEED_GUESTS", "the session has no guaranteed and no actual guests: enter a number, or post on the estimate").WithContext("pax_estimated", sn.PaxEstimated)
		}
		pax, basis, onGuarantee = sn.PaxEstimated, BasisEstimated, true
	}
	if o.auto {
		inactive, err := q.SessionInactiveCode(ctx, banquetdb.SessionInactiveCodeParams{PropertyID: propertyID, SessionID: t.sessionID})
		if err != nil {
			return PostResult{}, false, err
		}
		if inactive != "" {
			return PostResult{}, false, nil // the session stays unposted, on the report with its cause
		}
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return PostResult{}, false, err
	}
	lines, err := s.loadLines(ctx, p.TenantID, propertyID, t.eventID, nil, &t.sessionID, true)
	if err != nil {
		return PostResult{}, false, err
	}
	plans := make([]PlanLine, len(lines))
	for i, l := range lines {
		plans[i] = planLineOf(l)
	}
	posting, err := q.InsertSessionPosting(ctx, banquetdb.InsertSessionPostingParams{TenantID: p.TenantID, PropertyID: propertyID, SessionID: t.sessionID, FolioID: folioID, BusinessDate: bd,
		PaxBilled: int32(pax), PaxBasis: basis, Auto: o.auto, OnGuarantee: onGuarantee, PostedBy: p.ActorID()}) //nolint:gosec // G115: bounded by maxPax
	if err != nil {
		return PostResult{}, false, err
	}
	res := PostResult{Posting: Posting{SessionID: t.sessionID, BusinessDate: bd, PaxBilled: pax, PaxBasis: basis, Auto: o.auto, OnGuarantee: onGuarantee}, Charges: []PostedCharge{}}
	for _, c := range PlanSession(plans, pax, decimals) {
		desc := truncate(hdr.Name+" · "+sn.Name+" · "+c.Description, 200)
		ref := strconv.FormatInt(c.LineID, 10)
		if c.ComponentID != 0 {
			ref += ":" + strconv.FormatInt(c.ComponentID, 10)
		}
		line, err := s.folios.PostEventCharge(ctx, p, propertyID, bd, folioID, folios.EventCharge{ChargeCodeID: c.ChargeCodeID, Quantity: c.Quantity, UnitPrice: c.UnitPrice,
			PriceMode: chargecalc.PriceMode(c.Mode), Discount: c.Discount, Description: desc, ServiceDate: bd, Key: c.Key(t.sessionID), ReferenceType: folios.ReferenceBanquetLine, ReferenceID: ref})
		if err != nil {
			return PostResult{}, false, err
		}
		var comp *int64
		if c.ComponentID != 0 {
			comp = ptr(c.ComponentID)
		}
		if err := q.InsertPostingItem(ctx, banquetdb.InsertPostingItemParams{PropertyID: propertyID, PostingID: posting, LineID: c.LineID, ComponentItemID: comp, FolioItemID: line.ItemID}); err != nil {
			return PostResult{}, false, err
		}
		res.Charges = append(res.Charges, PostedCharge{LineID: c.LineID, ComponentID: c.ComponentID, FolioItemID: line.ItemID, Description: desc, Net: line.Net, Service: line.Service, Tax: line.Tax,
			Total: line.Total, ChargeCodeID: c.ChargeCodeID})
		res.Total = res.Total.Add(line.Total)
	}
	err = s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.session_posted", "banquet_session", t.sessionID, hdr.Number+" / "+sn.Name, nil,
		map[string]any{"folio_id": folioID, "pax_billed": pax, "pax_basis": basis, "auto": o.auto, "on_guarantee": onGuarantee, "charges": len(res.Charges), "total": res.Total.String()}))
	return res, true, err
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

// ---------------------------------------------------------------- the report and the checks of the close

// UnpostedReport answers the sessions of definite events dated the business date or earlier that are not posted (banquet.read and the others).
func (s *Service) UnpostedReport(ctx context.Context, propertyID int64) ([]Unposted, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return nil, err
	}
	return s.unposted(ctx, p.TenantID, propertyID)
}

func (s *Service) unposted(ctx context.Context, tenantID, propertyID int64) ([]Unposted, error) {
	day := s.today(ctx, propertyID)
	if day.IsZero() {
		return []Unposted{}, nil
	}
	rows, err := s.q(ctx).UnpostedSessions(ctx, banquetdb.UnpostedSessionsParams{TenantID: tenantID, PropertyID: propertyID, Day: day})
	if err != nil {
		return nil, err
	}
	out := make([]Unposted, 0, len(rows))
	for _, r := range rows {
		u := Unposted{SessionID: r.SessionID, EventID: r.EventID, EventNumber: r.EventNumber, EventName: r.EventName, SessionNo: int(r.SessionNo), SessionName: r.SessionName, SessionDate: r.SessionDate,
			RoomName: r.RoomName, PaxEstimated: int(r.PaxEstimated), PostingMode: r.PostingMode, SalesName: r.SalesName, DaysLate: r.SessionDate.DaysUntil(day), HasLines: r.HasLines}
		if r.PaxGuaranteed != nil {
			u.PaxGuaranteed = ptr(int(*r.PaxGuaranteed))
		}
		if r.PaxActual != nil {
			u.PaxActual = ptr(int(*r.PaxActual))
		}
		u.Blocked = u.PaxGuaranteed == nil && u.PaxActual == nil
		if r.InactiveCode != "" {
			u.InactiveCode = ptr(r.InactiveCode)
		}
		out = append(out, u)
	}
	return out, nil
}

// NightAuditIssue is a finding of the close about the banquet.
type NightAuditIssue struct {
	Code    string
	Message string
	Ref     string
	Params  map[string]string
}

// NightAuditChecks are the findings of the close for the banquet (36 4.5): a session of the automatic close that has no guests, one that waits for the office (manual) and one whose lines use an inactive
// charge code are warnings; none of them blocks the close, and each stays on the report "Sessions not posted" with its cause.
func (s *Service) NightAuditChecks(ctx context.Context, tenantID, propertyID int64) (blockers, warnings []NightAuditIssue, err error) {
	list, err := s.unposted(ctx, tenantID, propertyID)
	if err != nil {
		return nil, nil, err
	}
	for _, u := range list {
		ref := u.EventNumber + " / " + u.SessionName
		params := map[string]string{"event": u.EventNumber, "session": u.SessionName, "date": u.SessionDate.String()}
		switch {
		case u.PostingMode == PostingManual:
			warnings = append(warnings, NightAuditIssue{Code: "BANQUET_SESSION_NOT_POSTED", Message: "a banquet session waits to be posted by the office: " + ref, Ref: ref, Params: params})
		case u.InactiveCode != nil:
			params["charge_code"] = *u.InactiveCode
			warnings = append(warnings, NightAuditIssue{Code: "BANQUET_CHARGE_CODE_INACTIVE", Message: "a line of a banquet session uses an inactive charge code, so the close does not post it: " + ref, Ref: ref, Params: params})
		case u.Blocked:
			warnings = append(warnings, NightAuditIssue{Code: "BANQUET_SESSION_NO_GUESTS", Message: "a banquet session has no guaranteed and no actual guests, so the close does not post it: " + ref, Ref: ref, Params: params})
		}
	}
	return blockers, warnings, nil
}

// PostingModeInput overrides the property's posting mode for one event; an empty mode goes back to the property setting.
type PostingModeInput struct {
	PostingMode string `json:"posting_mode"`
}

// SetPostingMode sets or clears the posting mode of an event (banquet.manage).
func (s *Service) SetPostingMode(ctx context.Context, propertyID, eventID int64, in PostingModeInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	if in.PostingMode != "" && in.PostingMode != PostingAuto && in.PostingMode != PostingManual {
		return Event{}, invalid("event", fieldErr("posting_mode", "INVALID_VALUE", "AUTO or MANUAL"))
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if hdr.Status == StatusCancelled || hdr.Status == StatusLost || hdr.Status == StatusCompleted {
			return apperr.Conflict("EVENT_STATUS_INVALID", "the posting mode of a closed event cannot change").WithContext("status", hdr.Status)
		}
		var mode *string
		if in.PostingMode != "" {
			mode = &in.PostingMode
		}
		if err := s.q(ctx).SetEventPosting(ctx, banquetdb.SetEventPostingParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, PostingMode: mode, ActorID: &p.UserID}); err != nil {
			return err
		}
		if out, err = s.event(ctx, p.TenantID, propertyID, eventID); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.posting_mode_set", "banquet_event", eventID, out.Number,
			map[string]any{"posting_mode": hdr.PostingMode}, map[string]any{"posting_mode": mode}))
	})
	return out, err
}
