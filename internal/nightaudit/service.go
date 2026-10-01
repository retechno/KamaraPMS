package nightaudit

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/expected"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/nightaudit/nightauditdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations"
	"kamarapms/internal/roomcharge"
	"kamarapms/internal/tenancy"
)

// Service is the night audit orchestrator.
type Service struct {
	txm     *db.TxManager
	clock   clock.Clock
	audit   *audit.Writer
	authz   auth.Authorizer
	days    *tenancy.Service
	charges *roomcharge.Service
	res     *reservations.Service
	hk      *housekeeping.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service,
	charges *roomcharge.Service, res *reservations.Service, hk *housekeeping.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, charges: charges, res: res, hk: hk}
}

func (s *Service) q(ctx context.Context) *nightauditdb.Queries {
	return nightauditdb.New(s.txm.DB(ctx))
}

func (s *Service) actor(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func fixed(d decimal.Decimal, decimals int32) string { return d.StringFixed(decimals) }

// analysis is what the checks found for a business date.
type analysis struct {
	blockers Blockers
	missing  MissingCharges
	tonight  TonightCharges
	warnings Warnings
}

// analyze runs the checks 2 to 6 of §12.1 as plain reads. Check 1 (the day and the time guard) is the caller's.
func (s *Service) analyze(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, decimals int32, withWarnings bool) (analysis, error) {
	q := s.q(ctx)
	a := analysis{
		blockers: Blockers{UnresolvedArrivals: []Arrival{}, UnresolvedDepartures: []Departure{}, ChargeErrors: []roomcharge.Result{}, InvalidCharges: []expected.Invalid{}},
		missing:  MissingCharges{Items: []roomcharge.Result{}},
		warnings: Warnings{StaleDrafts: []StaleDraft{}, OpenFolios: []DeadFolio{}, BlocksEnding: []EndingBlock{}},
	}
	arrivals, err := q.ListUnresolvedArrivals(ctx, nightauditdb.ListUnresolvedArrivalsParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return a, err
	}
	for _, r := range arrivals {
		a.blockers.UnresolvedArrivals = append(a.blockers.UnresolvedArrivals, Arrival{
			ReservationRoomID: r.ReservationRoomID, ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber,
			Guest: name(r.GuestFirstName, deref(r.GuestLastName)), RoomType: r.RoomTypeCode, Room: deref(r.RoomNumber), ArrivalDate: r.ArrivalDate,
		})
	}
	departures, err := q.ListUnresolvedDepartures(ctx, nightauditdb.ListUnresolvedDeparturesParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return a, err
	}
	for _, r := range departures {
		a.blockers.UnresolvedDepartures = append(a.blockers.UnresolvedDepartures, Departure{
			StayID: r.StayID, StayNumber: r.StayNumber, Room: deref(r.RoomNumber), Guest: name(r.GuestFirstName, r.GuestLastName), DepartureDate: r.DepartureDate,
		})
	}
	cmd := roomcharge.PostCmd{BusinessDate: bd, Trigger: roomcharge.TriggerNightAudit}
	dry := cmd
	dry.DryRun = true
	rep, err := s.charges.Post(ctx, p, propertyID, dry)
	if err != nil {
		return a, err
	}
	total := decimal.Zero
	for _, r := range rep.Items {
		switch r.Status {
		case expected.StatusError:
			a.blockers.ChargeErrors = append(a.blockers.ChargeErrors, r)
		case expected.StatusReady:
			if r.ServiceDate.Before(bd) {
				a.missing.Count++
				a.missing.Items = append(a.missing.Items, r)
			} else {
				a.tonight.Count++
				t, _ := decimal.NewFromString(r.Total)
				total = total.Add(t)
			}
		}
	}
	a.tonight.Total = fixed(total, decimals)
	rev, err := s.charges.Revalidate(ctx, p, propertyID, cmd)
	if err != nil {
		return a, err
	}
	a.blockers.InvalidCharges = append(a.blockers.InvalidCharges, rev.Invalid...)

	if !withWarnings {
		return a, nil
	}
	drafts, err := q.ListStaleDrafts(ctx, nightauditdb.ListStaleDraftsParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return a, err
	}
	for _, d := range drafts {
		a.warnings.StaleDrafts = append(a.warnings.StaleDrafts, StaleDraft{ReservationRoomID: d.ReservationRoomID, ReservationID: d.ReservationID, ConfirmationNumber: d.ConfirmationNumber, ArrivalDate: d.ArrivalDate})
	}
	folios, err := q.ListOpenFoliosOfDeadReservations(ctx, nightauditdb.ListOpenFoliosOfDeadReservationsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return a, err
	}
	for _, f := range folios {
		a.warnings.OpenFolios = append(a.warnings.OpenFolios, DeadFolio{FolioID: f.FolioID, FolioNumber: f.FolioNumber, ConfirmationNumber: f.ConfirmationNumber, Balance: fixed(f.Balance, decimals)})
	}
	blocks, err := q.ListBlocksEndingAt(ctx, nightauditdb.ListBlocksEndingAtParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return a, err
	}
	for _, b := range blocks {
		a.warnings.BlocksEnding = append(a.warnings.BlocksEnding, EndingBlock{BlockID: b.BlockID, BlockType: b.BlockType, Room: b.RoomNumber, EndDate: b.EndDate})
	}
	return a, nil
}

// Preview runs every check and a dry run of the room charges (nightaudit.run). It writes nothing.
func (s *Service) Preview(ctx context.Context, propertyID int64) (Preview, error) {
	p, err := s.actor(ctx, propertyID, auth.PermNightAuditRun)
	if err != nil {
		return Preview{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Preview{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Preview{}, err
	}
	a, err := s.analyze(ctx, p, propertyID, day.BusinessDate, prop.CurrencyDecimals, true)
	if err != nil {
		return Preview{}, err
	}
	clk := tenancy.EvaluateDay(day.BusinessDate, s.clock.Now(), prop.Location(), prop.NightAuditEarliestTime)
	return Preview{
		BusinessDate: day.BusinessDate, PropertyLocalTime: clk.PropertyLocalTime, TimeGuardOK: clk.NightAuditAllowed, AllowedFrom: clk.NightAuditAllowedFrom,
		CanRun: clk.NightAuditAllowed && !a.blockers.Any(), Blockers: a.blockers, MissingCharges: a.missing, TonightCharges: a.tonight, Warnings: a.warnings,
	}, nil
}

// NoShows marks exactly the given unresolved arrivals as no-show (nightaudit.no_show) and answers with what still
// blocks the run. The request must carry confirm = true; the server never expands the set (409 NO_SHOW_SET_CHANGED).
func (s *Service) NoShows(ctx context.Context, propertyID int64, bd civil.Date, lineIDs []int64, confirm bool, reason string) (NoShowResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermNightAuditNoShow)
	if err != nil {
		return NoShowResult{}, err
	}
	if !confirm {
		return NoShowResult{}, apperr.Invalid("the no-show request is invalid", apperr.FieldError{Field: "confirm", Code: "REQUIRED", Message: "must be true"})
	}
	marked, err := s.res.BulkNoShow(ctx, propertyID, bd, lineIDs, reason)
	if err != nil {
		return NoShowResult{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return NoShowResult{}, err
	}
	a, err := s.analyze(ctx, p, propertyID, bd, prop.CurrencyDecimals, false)
	if err != nil {
		return NoShowResult{}, err
	}
	return NoShowResult{Marked: marked, RemainingBlockers: a.blockers}, nil
}

func blocked(b Blockers) *apperr.Error {
	return apperr.Conflict("NIGHT_AUDIT_BLOCKED", "the night audit is blocked: resolve the findings first").WithContext("blockers", b)
}

// Run is one transaction (§12.5): advisory lock, the open day FOR UPDATE, the checks, housekeeping, the room
// charges, revalidation, the closing summary, and the move of the business date. Any blocker rolls back everything,
// including the charges posted. The lock order is L0 advisory, L1 day, L3 rooms, L4 stays and folios.
func (s *Service) Run(ctx context.Context, propertyID int64, bd civil.Date) (RunResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermNightAuditRun)
	if err != nil {
		return RunResult{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return RunResult{}, err
	}
	var out RunResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		got, err := db.TryAdvisoryXactLock(ctx, "night_audit", propertyID) // L0
		if err != nil {
			return err
		}
		if !got {
			return apperr.Conflict("NIGHT_AUDIT_IN_PROGRESS", "a night audit of this property is already running")
		}
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForUpdate, &bd) // L1; BUSINESS_DATE_MISMATCH
		if err != nil {
			return err
		}
		if c := tenancy.EvaluateDay(day.BusinessDate, s.clock.Now(), prop.Location(), prop.NightAuditEarliestTime); !c.NightAuditAllowed {
			return apperr.Conflict("NIGHT_AUDIT_TOO_EARLY", "the business date cannot be closed before its end of day").
				WithContext("business_date", day.BusinessDate.String()).WithContext("allowed_from", c.NightAuditAllowedFrom)
		}
		a, err := s.analyze(ctx, p, propertyID, bd, prop.CurrencyDecimals, false)
		if err != nil {
			return err
		}
		if a.blockers.Any() {
			return blocked(a.blockers)
		}
		if prop.NightAuditMarksOccupiedDirty { // housekeeping only: L3 rooms come before the L4 stays of the posting
			ids, err := s.q(ctx).ListOccupiedRoomIDs(ctx, nightauditdb.ListOccupiedRoomIDsParams{TenantID: p.TenantID, PropertyID: propertyID})
			if err != nil {
				return err
			}
			if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, ids); err != nil {
				return err
			}
			for _, id := range ids {
				if err := s.hk.MarkDirtyLocked(ctx, p.TenantID, propertyID, id, bd, housekeeping.SourceNightAudit, "", p.ActorID()); err != nil {
					return err
				}
			}
		}
		rep, err := s.charges.PostLocked(ctx, p, propertyID, day, roomcharge.PostCmd{BusinessDate: bd, Trigger: roomcharge.TriggerNightAudit})
		if err != nil {
			return err
		}
		if r := rep.Revalidation; r.Ready > 0 || len(r.Errors) > 0 || len(r.Invalid) > 0 {
			b := a.blockers
			b.ChargeErrors, b.InvalidCharges = append(b.ChargeErrors, r.Errors...), append(b.InvalidCharges, r.Invalid...)
			if !b.Any() {
				return apperr.Conflict("NIGHT_AUDIT_BLOCKED", "room charges are still due after posting").WithContext("ready", r.Ready)
			}
			return blocked(b)
		}
		summary, err := s.Summarize(ctx, p, propertyID, bd, prop.CurrencyDecimals, rep.Posted)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		closed, opened, err := s.days.CloseAndOpenNextLocked(ctx, prop, day, p.ActorID(), raw)
		if err != nil {
			return err
		}
		out = RunResult{ClosedBusinessDate: closed.BusinessDate, NewBusinessDate: opened.BusinessDate, RoomChargesPosted: rep.Posted, Summary: summary}
		return s.audit.Write(ctx, audit.Entry{
			TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
			Action: "night_audit.completed", EntityType: "business_day", EntityID: closed.ID,
			New: map[string]any{"closed": closed.BusinessDate, "opened": opened.BusinessDate, "room_charges_posted": rep.Posted},
		})
	})
	return out, err
}

// Summarize computes the daily closing summary (§12.5 step 9) from rows of the transaction. Occupancy is occupied
// rooms over rooms that are not out of order; ADR is room revenue per room night charged; RevPAR is room revenue
// per room that is not out of order. Money uses the property's decimals.
func (s *Service) Summarize(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, decimals int32, posted int) (Summary, error) {
	q := s.q(ctx)
	sum := Summary{BusinessDate: bd, RoomChargesPosted: posted, RevenueByChargeType: []TypeRevenue{}, PaymentsByMethod: []MethodTotal{}}
	rooms, err := q.SummaryRooms(ctx, nightauditdb.SummaryRoomsParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return sum, err
	}
	sold, err := q.SummaryRoomNights(ctx, nightauditdb.SummaryRoomNightsParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return sum, err
	}
	sum.Rooms = RoomCounts{
		Total: int(rooms.Total), OutOfOrder: int(rooms.OutOfOrder), OutOfService: int(rooms.OutOfService),
		Sellable: int(rooms.Total - rooms.OutOfOrder - rooms.OutOfService), Occupied: int(rooms.Occupied), Sold: int(sold),
	}
	if sum.Arrivals, err = s.count(q.SummaryArrivals(ctx, nightauditdb.SummaryArrivalsParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})); err != nil {
		return sum, err
	}
	if sum.Departures, err = s.count(q.SummaryDepartures(ctx, nightauditdb.SummaryDeparturesParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})); err != nil {
		return sum, err
	}
	if sum.NoShows, err = s.count(q.SummaryNoShows(ctx, nightauditdb.SummaryNoShowsParams{TenantID: p.TenantID, PropertyID: &propertyID, Bd: bd})); err != nil {
		return sum, err
	}
	byType, err := q.SummaryRevenueByType(ctx, nightauditdb.SummaryRevenueByTypeParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return sum, err
	}
	room := Money{Net: fixed(decimal.Zero, decimals), Service: fixed(decimal.Zero, decimals), Tax: fixed(decimal.Zero, decimals)}
	roomNet := decimal.Zero
	for _, r := range byType {
		m := Money{Net: fixed(r.Net, decimals), Service: fixed(r.Service, decimals), Tax: fixed(r.Tax, decimals)}
		sum.RevenueByChargeType = append(sum.RevenueByChargeType, TypeRevenue{ChargeType: r.ChargeType, Money: m})
		if r.ChargeType == "ROOM" {
			room, roomNet = m, r.Net
		}
	}
	sum.RoomRevenue = room
	pays, err := q.SummaryPaymentsByMethod(ctx, nightauditdb.SummaryPaymentsByMethodParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: bd})
	if err != nil {
		return sum, err
	}
	for _, m := range pays {
		sum.PaymentsByMethod = append(sum.PaymentsByMethod, MethodTotal{
			Method: m.PaymentMethod, Payments: fixed(m.Payments, decimals), Refunds: fixed(m.Refunds, decimals), Net: fixed(m.Payments.Sub(m.Refunds), decimals),
		})
	}
	sort.Slice(sum.PaymentsByMethod, func(i, j int) bool { return sum.PaymentsByMethod[i].Method < sum.PaymentsByMethod[j].Method })
	available := decimal.NewFromInt(int64(rooms.Total - rooms.OutOfOrder))
	zero := fixed(decimal.Zero, decimals)
	sum.OccupancyPercent, sum.ADR, sum.RevPAR = "0.00", zero, zero
	if available.IsPositive() {
		sum.OccupancyPercent = decimal.NewFromInt(int64(rooms.Occupied)).Mul(decimal.NewFromInt(100)).DivRound(available, 2).StringFixed(2)
		sum.RevPAR = fixed(roomNet.DivRound(available, decimals), decimals)
	}
	if sold > 0 {
		sum.ADR = fixed(roomNet.DivRound(decimal.NewFromInt(int64(sold)), decimals), decimals)
	}
	return sum, nil
}

func (s *Service) count(n int32, err error) (int, error) { return int(n), err }
