package shifts

import (
	"context"
	"time"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/shifts/shiftsdb"
)

// The handover of a drawer and the shift report (design: docs/architecture/11-cashier-budget-cashflow-card.md, part A, and "Handover and the Z report" there).
// A shift closed with `hand_over_to` names who gets the drawer next. The next shift of that drawer starts with the float the closed one left; the person it was handed to
// sees the drawer waiting for them and opens it with that float. Nothing is opened by itself and nothing stops another user from opening the drawer: two people never
// share one open shift, which the database keeps.

// Cashier is a user a drawer can be handed to.
type Cashier struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Handover is a drawer that was handed to the caller and has not been opened since.
type Handover struct {
	ShiftID      int64     `json:"shift_id"`
	ShiftNumber  string    `json:"shift_number"`
	Drawer       string    `json:"drawer"`
	FromUserID   int64     `json:"from_user_id"`
	FromUserName string    `json:"from_user_name"`
	ClosedAt     time.Time `json:"closed_at"`
	// LeftCash is what the closing cashier counted in the drawer: the float the next shift is suggested to open with.
	LeftCash string `json:"left_cash"`
}

// ReportPayment is a cash payment, refund or receipt of the shift on the report.
type ReportPayment struct {
	Number    string    `json:"number"`
	Kind      string    `json:"kind"` // PAYMENT, REFUND or RECEIPT
	Amount    string    `json:"amount"`
	Status    string    `json:"status"`
	PaidAt    time.Time `json:"paid_at"`
	Reference string    `json:"reference,omitempty"`
}

// Tender is what the cashier took by a method other than cash while the shift was open.
type Tender struct {
	Method string `json:"method"`
	Kind   string `json:"kind"` // PAYMENT or REFUND
	Count  int    `json:"count"`
	Amount string `json:"amount"`
}

// Report is the report of a shift: the Z report of a closed one, the X report (a reading that closes nothing) of an open one.
type Report struct {
	Kind             string          `json:"kind"` // Z or X
	Shift            Shift           `json:"shift"`
	ClosedByName     string          `json:"closed_by_name,omitempty"`
	ApprovedByName   string          `json:"approved_by_name,omitempty"`
	HandedOverToName string          `json:"handed_over_to_name,omitempty"`
	CashPayments     []ReportPayment `json:"cash_payments"`
	OtherTenders     []Tender        `json:"other_tenders"`
}

// Cashiers lists the users the drawer can be handed to (cashier.shift): the people of the property who can run a shift, except the caller.
func (s *Service) Cashiers(ctx context.Context, propertyID int64) ([]Cashier, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCashierShift)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListCashiers(ctx, shiftsdb.ListCashiersParams{TenantID: p.TenantID, PropertyID: propertyID, ExceptUserID: p.UserID})
	if err != nil {
		return nil, err
	}
	out := make([]Cashier, len(rows))
	for i, r := range rows {
		out[i] = Cashier{ID: r.ID, Name: r.FullName}
	}
	return out, nil
}

// Handovers lists the drawers handed to the caller that nobody has opened since (cashier.shift).
func (s *Service) Handovers(ctx context.Context, propertyID int64) ([]Handover, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCashierShift)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).HandoversTo(ctx, shiftsdb.HandoversToParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: &p.UserID})
	if err != nil {
		return nil, err
	}
	out := make([]Handover, len(rows))
	for i, r := range rows {
		out[i] = Handover{ShiftID: r.ID, ShiftNumber: r.ShiftNumber, Drawer: r.Drawer, FromUserID: r.UserID, FromUserName: r.FromName, ClosedAt: r.ClosedAt, LeftCash: fixed(r.LeftCash, decimals)}
	}
	return out, nil
}

// Report answers the report of a shift (the owner, or cashier.shift_manage): the cash reconciliation, the cash payments, the other tenders, the count and the movements.
// A closed shift gives the Z report, which never changes; an open one gives the X report, the same figures so far.
func (s *Service) Report(ctx context.Context, propertyID, id int64) (Report, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Report{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return Report{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Report{}, err
	}
	sh, err := s.detail(ctx, p.TenantID, propertyID, id, decimals)
	if err != nil {
		return Report{}, err
	}
	if err := s.canSee(ctx, p, propertyID, sh.UserID); err != nil {
		return Report{}, err
	}
	q := s.q(ctx)
	rep := Report{Kind: "X", Shift: sh, CashPayments: []ReportPayment{}, OtherTenders: []Tender{}}
	until := s.clock.Now()
	if sh.Status == StatusClosed {
		rep.Kind = "Z"
		if sh.ClosedAt != nil {
			until = *sh.ClosedAt
		}
	}
	row, err := q.GetShift(ctx, shiftsdb.GetShiftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Report{}, err
	}
	ids := []int64{}
	for _, u := range []*int64{row.ClosedBy, row.ApprovedBy, row.HandedOverTo} {
		if u != nil {
			ids = append(ids, *u)
		}
	}
	names := map[int64]string{}
	if len(ids) > 0 {
		list, err := q.UserNames(ctx, shiftsdb.UserNamesParams{TenantID: p.TenantID, Ids: ids})
		if err != nil {
			return Report{}, err
		}
		for _, u := range list {
			names[u.ID] = u.FullName
		}
	}
	name := func(u *int64) string {
		if u == nil {
			return ""
		}
		return names[*u]
	}
	rep.ClosedByName, rep.ApprovedByName, rep.HandedOverToName = name(row.ClosedBy), name(row.ApprovedBy), name(row.HandedOverTo)
	pays, err := q.ListShiftPayments(ctx, shiftsdb.ListShiftPaymentsParams{TenantID: p.TenantID, PropertyID: propertyID, ShiftID: id})
	if err != nil {
		return Report{}, err
	}
	for _, r := range pays {
		kind := r.PaymentType
		rep.CashPayments = append(rep.CashPayments, ReportPayment{Number: r.PaymentNumber, Kind: kind, Amount: fixed(r.Amount, decimals), Status: r.Status, PaidAt: r.PaidAt, Reference: deref(r.ReferenceNumber)})
	}
	tenders, err := q.ShiftOtherTenders(ctx, shiftsdb.ShiftOtherTendersParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: &sh.UserID, OpenedAt: sh.OpenedAt, Until: until})
	if err != nil {
		return Report{}, err
	}
	for _, t := range tenders {
		rep.OtherTenders = append(rep.OtherTenders, Tender{Method: t.Method, Kind: t.PaymentType, Count: int(t.N), Amount: fixed(t.Amount, decimals)})
	}
	return rep, nil
}
