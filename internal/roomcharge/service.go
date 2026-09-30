// Package roomcharge is RoomChargePostingService (docs/architecture/03-financial-engines.md step 9): it posts
// the room nights the Expected Charge Engine says are due, through the folio posting service, and records each
// one in the posting register. Night audit, "post missing charges", check-out and recovery all call the same
// method.
package roomcharge

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/expected"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/roomcharge/roomchargedb"
	"kamarapms/internal/tenancy"
)

// Triggers recorded in the register.
const (
	TriggerNightAudit = "NIGHT_AUDIT"
	TriggerManual     = "MANUAL"
	TriggerCheckOut   = "CHECK_OUT"
	TriggerRecovery   = "RECOVERY"
)

// Result statuses: the evaluator's plus POSTED.
const StatusPosted = "POSTED"

// Service is the room charge posting service.
type Service struct {
	txm     *db.TxManager
	clock   clock.Clock
	audit   *audit.Writer
	authz   auth.Authorizer
	days    *tenancy.Service
	loader  *expected.Loader
	billing *billingconfig.Service
	poster  *folios.RoomPoster
}

// NewService wires the service. poster is the room posting capability of the folio service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, l *expected.Loader,
	b *billingconfig.Service, poster *folios.RoomPoster) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, loader: l, billing: b, poster: poster}
}

// PostCmd is a posting run. BusinessDate must equal the open business date (it guards against stale screens).
// StayIDs empty means every OPEN stay. DryRun evaluates and calculates but writes nothing.
type PostCmd struct {
	BusinessDate civil.Date
	StayIDs      []int64
	Trigger      string
	DryRun       bool
}

// Result is one night of a run.
type Result struct {
	StayID             int64      `json:"stay_id"`
	StayNumber         string     `json:"stay_number"`
	GuestName          string     `json:"guest"`
	RoomNumber         string     `json:"room_number,omitempty"`
	FolioID            *int64     `json:"folio_id"`
	ServiceDate        civil.Date `json:"service_date"`
	ChargeCode         string     `json:"charge_code,omitempty"`
	RoomRate           string     `json:"room_rate"`
	PriceMode          string     `json:"price_mode,omitempty"`
	ServiceCharge      string     `json:"service_charge"`
	Tax                string     `json:"tax"`
	RoundingAdjustment string     `json:"rounding_adjustment"`
	Total              string     `json:"total"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason,omitempty"`
	FolioItemID        *int64     `json:"folio_item_id"`
}

// Revalidation is the state of the scope after a run: what is still READY, what is an error and what is invalid.
type Revalidation struct {
	Ready   int                `json:"ready"`
	Errors  []Result           `json:"errors"`
	Invalid []expected.Invalid `json:"invalid"`
}

// Report is the outcome of a run.
type Report struct {
	Items        []Result
	Revalidation Revalidation
	Posted       int
}

func ptr[T any](v T) *T { return &v }

func describe(room string, n civil.Date) string {
	return "Room " + room + " - " + itoa(n.Day()) + " " + n.Month().String()[:3] + " " + itoa(n.Year())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func fixed(d decimal.Decimal, decimals int32) string { return d.StringFixed(decimals) }

func resultOf(c expected.Charge, decimals int32) Result {
	return Result{
		StayID: c.StayID, StayNumber: c.StayNumber, GuestName: c.GuestName, RoomNumber: c.RoomNumber, FolioID: c.FolioID, ServiceDate: c.ServiceDate,
		ChargeCode: c.ChargeCode, RoomRate: fixed(c.UnitPrice, decimals), PriceMode: c.PriceMode, ServiceCharge: fixed(decimal.Zero, decimals),
		Tax: fixed(decimal.Zero, decimals), RoundingAdjustment: fixed(decimal.Zero, decimals), Total: fixed(decimal.Zero, decimals),
		Status: c.Status, Reason: c.Reason, FolioItemID: c.PostedItemID,
	}
}

// Post evaluates the expected room charges of the scope and posts the READY ones (or only calculates them in a
// dry run). A real run needs a transaction of its own or the caller's: it takes the business day share lock,
// locks the stays FOR UPDATE in id order, evaluates after the locks, locks the folios, and posts in order of
// stay and night. ERROR items are reported, never posted.
func (s *Service) Post(ctx context.Context, p auth.Principal, propertyID int64, cmd PostCmd) (Report, error) {
	if cmd.DryRun {
		return s.dryRun(ctx, p, propertyID, cmd)
	}
	var rep Report
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		bd := cmd.BusinessDate
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, &bd) // L1
		if err != nil {
			return err
		}
		decimals, err := s.decimals(ctx, propertyID)
		if err != nil {
			return err
		}
		ids, err := s.scopeIDs(ctx, p.TenantID, propertyID, cmd.StayIDs)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Stays, db.ForUpdate, propertyID, ids); err != nil { // L4 stays, ascending
			if apperr.IsCode(err, "NOT_FOUND") {
				return apperr.NotFound("STAY_NOT_FOUND", "a stay does not exist in this property")
			}
			return err
		}
		snap, err := s.loader.Load(ctx, p.TenantID, propertyID, ids) // after the locks: the current state
		if err != nil {
			return err
		}
		charges := expected.Evaluate(snap, expected.Scope{UpToDate: day.BusinessDate})
		var folioIDs []int64
		seen := map[int64]bool{}
		for _, c := range charges {
			if c.Status == expected.StatusReady && c.FolioID != nil && !seen[*c.FolioID] {
				seen[*c.FolioID] = true
				folioIDs = append(folioIDs, *c.FolioID)
			}
		}
		sort.Slice(folioIDs, func(i, j int) bool { return folioIDs[i] < folioIDs[j] })
		if err := s.poster.LockFolios(ctx, propertyID, folioIDs); err != nil { // L4 folios, ascending
			return err
		}
		q := roomchargedb.New(s.txm.DB(ctx))
		for _, c := range charges {
			r := resultOf(c, decimals)
			if c.Status == expected.StatusReady {
				night, err := s.poster.Post(ctx, p, propertyID, day.BusinessDate, folios.RoomNightCmd{
					FolioID: *c.FolioID, ChargeCodeID: c.ChargeCodeID, StayRoomID: c.StayRoomID, UnitPrice: c.UnitPrice,
					PriceMode: chargecalc.PriceMode(c.PriceMode), ServiceDate: c.ServiceDate, Description: describe(c.RoomNumber, c.ServiceDate),
				})
				if err != nil {
					return err
				}
				if _, err := q.InsertPosting(ctx, roomchargedb.InsertPostingParams{
					TenantID: p.TenantID, PropertyID: propertyID, StayID: c.StayID, StayRoomID: c.StayRoomID, ServiceDate: c.ServiceDate,
					ChargeCodeID: c.ChargeCodeID, FolioItemID: night.ItemID, BusinessDate: day.BusinessDate, PostingTrigger: cmd.Trigger, ActorID: p.ActorID(),
				}); err != nil {
					return err
				}
				r.Status, r.FolioItemID = StatusPosted, ptr(night.ItemID)
				r.ServiceCharge, r.Tax, r.RoundingAdjustment, r.Total = fixed(night.Service, decimals), fixed(night.Tax, decimals), fixed(night.Rounding, decimals), fixed(night.Total, decimals)
				rep.Posted++
			}
			rep.Items = append(rep.Items, r)
		}
		counts := map[string]int{}
		for _, r := range rep.Items {
			counts[r.Status]++
		}
		return s.audit.Write(ctx, audit.Entry{
			TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(),
			Action: "room_charges.posted", EntityType: "property", EntityID: propertyID,
			New: map[string]any{"trigger": cmd.Trigger, "stays": len(ids), "posted": counts[StatusPosted], "already_posted": counts[expected.StatusAlreadyPosted], "errors": counts[expected.StatusError]},
		})
	})
	if err != nil {
		return Report{}, err
	}
	rev, err := s.Revalidate(ctx, p, propertyID, cmd)
	rep.Revalidation = rev
	return rep, err
}

// Revalidate re-reads the scope after a run: how many nights are still READY, which are ERRORs and which posted
// nights are invalid. It takes no locks.
func (s *Service) Revalidate(ctx context.Context, p auth.Principal, propertyID int64, cmd PostCmd) (Revalidation, error) {
	rev := Revalidation{Errors: []Result{}, Invalid: []expected.Invalid{}}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return rev, err
	}
	ids, err := s.scopeIDs(ctx, p.TenantID, propertyID, cmd.StayIDs)
	if err != nil {
		return rev, err
	}
	snap, err := s.loader.Load(ctx, p.TenantID, propertyID, ids)
	if err != nil {
		return rev, err
	}
	for _, c := range expected.Evaluate(snap, expected.Scope{UpToDate: cmd.BusinessDate}) {
		switch c.Status {
		case expected.StatusReady:
			rev.Ready++
		case expected.StatusError:
			rev.Errors = append(rev.Errors, resultOf(c, decimals))
		}
	}
	rev.Invalid = append(rev.Invalid, expected.FindInvalid(snap, expected.Scope{})...)
	return rev, nil
}

func (s *Service) scopeIDs(ctx context.Context, tenantID, propertyID int64, given []int64) ([]int64, error) {
	if len(given) > 0 {
		out := append([]int64(nil), given...)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out, nil
	}
	return s.loader.OpenStayIDs(ctx, tenantID, propertyID)
}

func (s *Service) decimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

// dryRun is the preview: plain reads, the charge calculation service for the amounts, nothing written.
func (s *Service) dryRun(ctx context.Context, p auth.Principal, propertyID int64, cmd PostCmd) (Report, error) {
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Report{}, err
	}
	if !day.BusinessDate.Equal(cmd.BusinessDate) {
		return Report{}, apperr.Conflict("BUSINESS_DATE_MISMATCH", "the business date has changed; refresh and try again").
			WithContext("business_date", day.BusinessDate.String()).WithContext("requested_business_date", cmd.BusinessDate.String())
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Report{}, err
	}
	ids, err := s.scopeIDs(ctx, p.TenantID, propertyID, cmd.StayIDs)
	if err != nil {
		return Report{}, err
	}
	snap, err := s.loader.Load(ctx, p.TenantID, propertyID, ids)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	for _, c := range expected.Evaluate(snap, expected.Scope{UpToDate: day.BusinessDate}) {
		r := resultOf(c, decimals)
		if c.Status == expected.StatusReady {
			mode := chargecalc.PriceMode(c.PriceMode)
			b, err := s.billing.Calculate(ctx, billingconfig.ChargeRequest{PropertyID: propertyID, ChargeCodeID: c.ChargeCodeID, Quantity: decimal.NewFromInt(1), UnitPrice: c.UnitPrice, PriceMode: &mode})
			if err != nil {
				return Report{}, err
			}
			r.ServiceCharge, r.Tax, r.RoundingAdjustment, r.Total = fixed(b.ServiceTotal, decimals), fixed(b.TaxTotal, decimals), fixed(b.RoundingAdjustment, decimals), fixed(b.TotalAmount, decimals)
		}
		rep.Items = append(rep.Items, r)
	}
	return rep, nil
}
