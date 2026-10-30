package reports

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
)

// The cancellations and no-shows report (docs/architecture/38-reports-night-audit-pack.md, 7.2): one row for each event of the reservation module, by the business date of the event.

// The kinds of a row and the filter of the request.
const (
	CancelKindAll           = "ALL"
	CancelKindCancelled     = "CANCELLED"
	CancelKindRoomCancelled = "ROOM_CANCELLED"
	CancelKindNoShow        = "NO_SHOW"
)

const (
	actionCancelled     = "reservation.cancelled"
	actionRoomCancelled = "reservation.room_cancelled"
	actionNoShow        = "reservation.no_show"
)

// CancellationParams are the parameters of the report.
type CancellationParams struct {
	From, To  civil.Date
	Kind      string
	Source    string
	CompanyID *int64
}

// CancellationEvent is one cancellation, one cancelled room or one no-show. A cell that does not apply is null: "no fee posted" is a null fee, not a zero, so a missing fee is visible.
type CancellationEvent struct {
	BusinessDate       civil.Date  `json:"business_date"`
	EventAt            time.Time   `json:"event_at"`
	UserName           string      `json:"user_name"`
	Kind               string      `json:"kind"`
	ReservationID      int64       `json:"reservation_id"`
	ConfirmationNumber string      `json:"confirmation_number"`
	ReservationRoomID  *int64      `json:"reservation_room_id"`
	Guest              string      `json:"guest"`
	Company            string      `json:"company"`
	Source             string      `json:"source"`
	Market             string      `json:"market"`
	RoomType           string      `json:"room_type"`
	RatePlan           string      `json:"rate_plan"`
	Rooms              int         `json:"rooms"`
	Arrival            *civil.Date `json:"arrival"`
	Departure          *civil.Date `json:"departure"`
	Nights             *int        `json:"nights"`
	LeadDays           *int        `json:"lead_days"`
	ValueLost          *string     `json:"value_lost"`
	Reason             string      `json:"reason"`
	FeeCode            string      `json:"fee_code,omitempty"`
	FeeNet             *string     `json:"fee_net"`
	FeeGross           *string     `json:"fee_gross"`
	FeeDate            *civil.Date `json:"fee_date"`
	FeeReversed        bool        `json:"fee_reversed"`
	DepositAtEvent     string      `json:"deposit_at_event"`
	DepositNow         string      `json:"deposit_now"`
	Reinstated         bool        `json:"reinstated"`
}

// CancellationGroup is a total of the events under one key (a kind, a reason, a source, a company).
type CancellationGroup struct {
	Key    string `json:"key"`
	Events int    `json:"events"`
	Rooms  int    `json:"rooms"`
	Nights int    `json:"nights"`
	Value  string `json:"value"`
}

// CancellationReport is the report.
type CancellationReport struct {
	From         civil.Date          `json:"from"`
	To           civil.Date          `json:"to"`
	Kind         string              `json:"kind"`
	Events       []CancellationEvent `json:"events"`
	ByKind       []CancellationGroup `json:"by_kind"`
	ByReason     []CancellationGroup `json:"by_reason"`
	BySource     []CancellationGroup `json:"by_source"`
	ByCompany    []CancellationGroup `json:"by_company"`
	FeesPosted   string              `json:"fees_posted"`
	FeeCount     int                 `json:"fee_count"`
	DepositsHeld string              `json:"deposits_held"`
}

// CSV is the list of the events.
func (r CancellationReport) CSV() ([]string, [][]string) {
	h := []string{"business_date", "event_at", "user", "kind", "confirmation_number", "guest", "company", "source", "market", "room_type", "rate_plan", "rooms", "arrival", "departure", "nights", "lead_days",
		"value_lost", "reason", "fee_code", "fee_net", "fee_gross", "fee_date", "fee_reversed", "deposit_at_event", "deposit_now", "reinstated"}
	rows := make([][]string, 0, len(r.Events))
	for _, e := range r.Events {
		rows = append(rows, []string{e.BusinessDate.String(), e.EventAt.UTC().Format(time.RFC3339), e.UserName, e.Kind, e.ConfirmationNumber, e.Guest, e.Company, e.Source, e.Market, e.RoomType, e.RatePlan,
			itoa(e.Rooms), optDate(e.Arrival), optDate(e.Departure), optInt(e.Nights), optInt(e.LeadDays), optStr(e.ValueLost), e.Reason, e.FeeCode, optStr(e.FeeNet), optStr(e.FeeGross), optDate(e.FeeDate),
			boolStr(e.FeeReversed), e.DepositAtEvent, e.DepositNow, boolStr(e.Reinstated)})
	}
	return h, rows
}

func optDate(d *civil.Date) string {
	if d == nil {
		return ""
	}
	return d.String()
}

func optInt(n *int) string {
	if n == nil {
		return ""
	}
	return itoa(*n)
}

func optStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// CancellationParamsFrom checks report.view and then reads the query: the permission is answered before the query is looked at.
func (s *Service) CancellationParamsFrom(ctx context.Context, propertyID int64, q func(string) string) (CancellationParams, error) {
	if _, _, err := s.access(ctx, propertyID); err != nil {
		return CancellationParams{}, err
	}
	var errs []apperr.FieldError
	out := CancellationParams{Kind: CancelKindAll, Source: q("source")}
	out.From, out.To = queryDate(q, "from", &errs), queryDate(q, "to", &errs)
	switch k := q("kind"); k {
	case "", CancelKindAll:
	case CancelKindCancelled, CancelKindNoShow:
		out.Kind = k
	default:
		errs = append(errs, apperr.FieldError{Field: "kind", Code: "INVALID_VALUE", Message: "ALL, CANCELLED or NO_SHOW"})
	}
	if v := q("company_id"); v != "" {
		id, err := parseID(v)
		if err != nil {
			errs = append(errs, apperr.FieldError{Field: "company_id", Code: "INVALID_VALUE", Message: "a number"})
		} else {
			out.CompanyID = &id
		}
	}
	if len(errs) > 0 {
		return out, apperr.Invalid("the query is invalid", errs...)
	}
	return out, nil
}

// Cancellations answers the report (report.view). The rows are the audit entries of the events dated in the range; the rooms, the value of the nights and the fee are read from the reservation and
// the ledger as they are now.
func (s *Service) Cancellations(ctx context.Context, propertyID int64, in CancellationParams) (CancellationReport, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return CancellationReport{}, err
	}
	if err := checkRange(in.From, in.To); err != nil {
		return CancellationReport{}, err
	}
	actions := []string{actionCancelled, actionRoomCancelled, actionNoShow}
	switch in.Kind {
	case CancelKindCancelled:
		actions = []string{actionCancelled, actionRoomCancelled}
	case CancelKindNoShow:
		actions = []string{actionNoShow}
	}
	q := s.q(ctx)
	var src *string
	if in.Source != "" {
		src = &in.Source
	}
	evs, err := q.CancellationEvents(ctx, reportsdb.CancellationEventsParams{TenantID: p.TenantID, PropertyID: &propertyID, FromDate: in.From, ToDate: in.To, Actions: actions, Source: src, CompanyID: in.CompanyID})
	if err != nil {
		return CancellationReport{}, err
	}
	out := CancellationReport{From: in.From, To: in.To, Kind: in.Kind, Events: []CancellationEvent{}, ByKind: []CancellationGroup{}, ByReason: []CancellationGroup{}, BySource: []CancellationGroup{}, ByCompany: []CancellationGroup{},
		FeesPosted: fx(decimal.Zero, decimals), DepositsHeld: fx(decimal.Zero, decimals)}
	if len(evs) == 0 {
		return out, nil
	}
	resIDs, resRefs, lineRefs := []int64{}, []string{}, []string{}
	seenRes := map[int64]bool{}
	for _, e := range evs {
		if !seenRes[e.ReservationID] {
			seenRes[e.ReservationID] = true
			resIDs = append(resIDs, e.ReservationID)
			resRefs = append(resRefs, itoa64(e.ReservationID))
		}
		if e.LineID != 0 {
			lineRefs = append(lineRefs, itoa64(e.LineID))
		}
	}
	lines, err := q.CancellationLines(ctx, reportsdb.CancellationLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationIds: resIDs})
	if err != nil {
		return CancellationReport{}, err
	}
	fees, err := q.CancellationFees(ctx, reportsdb.CancellationFeesParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationRefs: resRefs, LineRefs: lineRefs})
	if err != nil {
		return CancellationReport{}, err
	}
	deps, err := q.CancellationDeposits(ctx, reportsdb.CancellationDepositsParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationIds: resIDs})
	if err != nil {
		return CancellationReport{}, err
	}
	byRes := map[int64][]reportsdb.CancellationLinesRow{}
	byLine := map[int64]reportsdb.CancellationLinesRow{}
	for _, l := range lines {
		byRes[l.ReservationID] = append(byRes[l.ReservationID], l)
		byLine[l.ID] = l
	}
	// The rooms of a whole cancellation: the cancelled rooms of the reservation, the latest cancelled first; each event takes as many as it cancelled (newest event first), so a reservation that
	// was reinstated and cancelled again gives its rooms to the last event and the first one shows its count only.
	cancelled := map[int64][]reportsdb.CancellationLinesRow{}
	for id, ls := range byRes {
		for _, l := range ls {
			if l.Status == "CANCELLED" && l.CancelledAt != nil {
				cancelled[id] = append(cancelled[id], l)
			}
		}
		c := cancelled[id]
		sort.SliceStable(c, func(i, j int) bool { return c[i].CancelledAt.After(*c[j].CancelledAt) })
	}
	taken := map[int64]int{}
	wholeLines := map[int64][]reportsdb.CancellationLinesRow{} // by audit id
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if e.Action != actionCancelled {
			continue
		}
		c := cancelled[e.ReservationID]
		n := int(e.RoomsCancelled)
		from := taken[e.ReservationID]
		to := from + n
		if from > len(c) {
			from = len(c)
		}
		if to > len(c) {
			to = len(c)
		}
		wholeLines[e.AuditID] = c[from:to]
		taken[e.ReservationID] = to
	}
	feeUsed := map[int64]bool{}
	pickFee := func(refType, ref, code string, on civil.Date) *reportsdb.CancellationFeesRow {
		for i := range fees {
			f := &fees[i]
			if feeUsed[f.ID] || f.ChargeCode != code || f.ReferenceType == nil || *f.ReferenceType != refType || f.ReferenceID != ref || f.BusinessDate.Before(on) {
				continue
			}
			feeUsed[f.ID] = true
			return f
		}
		return nil
	}
	depositOn := func(resID int64, on *civil.Date) decimal.Decimal {
		t := decimal.Zero
		for _, d := range deps {
			if d.ReservationID == nil || *d.ReservationID != resID {
				continue
			}
			if on == nil || !d.BusinessDate.After(*on) {
				t = t.Add(d.Amount)
			}
		}
		return t
	}
	nightsOf := func(l reportsdb.CancellationLinesRow) int { return l.ArrivalDate.DaysUntil(l.DepartureDate) }
	for _, e := range evs {
		bd := civil.Date{}
		if e.EventDate != nil {
			bd = *e.EventDate
		}
		row := CancellationEvent{BusinessDate: bd, EventAt: e.EventAt, UserName: e.UserName, ReservationID: e.ReservationID, ConfirmationNumber: e.ConfirmationNumber,
			Guest: e.GuestName, Company: e.CompanyName, Source: e.Source, Market: deref(e.Market), Reason: e.EventReason}
		var ls []reportsdb.CancellationLinesRow
		switch e.Action {
		case actionCancelled:
			row.Kind = CancelKindCancelled
			ls = wholeLines[e.AuditID]
			row.Rooms = int(e.RoomsCancelled)
			if row.Reason == "" {
				row.Reason = e.ReservationReason
			}
			row.Reinstated = e.ReservationStatus != "CANCELLED"
			if f := pickFee("RESERVATION", itoa64(e.ReservationID), "CANCEL_FEE", bd); f != nil {
				setFee(&row, f, decimals)
			}
		case actionRoomCancelled:
			row.Kind = CancelKindRoomCancelled
			id := e.LineID
			row.ReservationRoomID = &id
			if l, ok := byLine[id]; ok {
				ls = []reportsdb.CancellationLinesRow{l}
				row.Reinstated = l.Status != "CANCELLED"
				if row.Reason == "" {
					row.Reason = l.CancellationReason
				}
			}
			row.Rooms = 1
		case actionNoShow:
			row.Kind = CancelKindNoShow
			id := e.LineID
			row.ReservationRoomID = &id
			if l, ok := byLine[id]; ok {
				ls = []reportsdb.CancellationLinesRow{l}
				row.Reinstated = l.Status != "NO_SHOW"
			}
			row.Rooms = 1
			if f := pickFee("RESERVATION_ROOM", itoa64(id), "NO_SHOW_FEE", bd); f != nil {
				setFee(&row, f, decimals)
			}
		}
		if len(ls) > 0 {
			arr, dep := ls[0].ArrivalDate, ls[0].DepartureDate
			nights, value := 0, decimal.Zero
			types, plans := []string{}, []string{}
			for _, l := range ls {
				if l.ArrivalDate.Before(arr) {
					arr = l.ArrivalDate
				}
				if l.DepartureDate.After(dep) {
					dep = l.DepartureDate
				}
				nights += nightsOf(l)
				value = value.Add(l.NightValue)
				types, plans = addDistinct(types, l.RoomType), addDistinct(plans, l.RatePlan)
			}
			lead := bd.DaysUntil(arr)
			v := fx(value, decimals)
			row.Arrival, row.Departure, row.Nights, row.LeadDays, row.ValueLost = &arr, &dep, &nights, &lead, &v
			row.RoomType, row.RatePlan = strings.Join(types, ", "), strings.Join(plans, ", ")
		}
		row.DepositAtEvent = fx(depositOn(e.ReservationID, &bd), decimals)
		row.DepositNow = fx(depositOn(e.ReservationID, nil), decimals)
		out.Events = append(out.Events, row)
	}
	s.cancellationTotals(&out, decimals)
	heldFor := map[int64]bool{}
	held := decimal.Zero
	for _, e := range out.Events {
		if heldFor[e.ReservationID] {
			continue
		}
		heldFor[e.ReservationID] = true
		d, _ := decimal.NewFromString(e.DepositNow)
		if d.IsPositive() {
			held = held.Add(d)
		}
	}
	out.DepositsHeld = fx(held, decimals)
	return out, nil
}

func setFee(row *CancellationEvent, f *reportsdb.CancellationFeesRow, decimals int32) {
	net, gross, d := fx(f.NetAmount, decimals), fx(f.Gross, decimals), f.BusinessDate
	row.FeeCode, row.FeeNet, row.FeeGross, row.FeeDate, row.FeeReversed = f.ChargeCode, &net, &gross, &d, f.Reversed
}

func addDistinct(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// cancellationTotals fills the totals and the groups: events, rooms, nights and the value of the nights lost by kind, by reason (the free text folded by case and spacing), by source and by company.
func (s *Service) cancellationTotals(out *CancellationReport, decimals int32) {
	type acc struct {
		key                   string
		events, rooms, nights int
		value                 decimal.Decimal
	}
	groupBy := func(keyOf func(CancellationEvent) (fold, label string)) []CancellationGroup {
		idx := map[string]*acc{}
		var order []string
		for _, e := range out.Events {
			fold, label := keyOf(e)
			a := idx[fold]
			if a == nil {
				a = &acc{key: label}
				idx[fold] = a
				order = append(order, fold)
			}
			a.events++
			a.rooms += e.Rooms
			if e.Nights != nil {
				a.nights += *e.Nights
			}
			if e.ValueLost != nil {
				v, _ := decimal.NewFromString(*e.ValueLost)
				a.value = a.value.Add(v)
			}
		}
		sort.SliceStable(order, func(i, j int) bool { return idx[order[i]].events > idx[order[j]].events })
		res := make([]CancellationGroup, 0, len(order))
		for _, k := range order {
			a := idx[k]
			res = append(res, CancellationGroup{Key: a.key, Events: a.events, Rooms: a.rooms, Nights: a.nights, Value: fx(a.value, decimals)})
		}
		return res
	}
	out.ByKind = groupBy(func(e CancellationEvent) (string, string) { return e.Kind, e.Kind })
	out.ByReason = groupBy(func(e CancellationEvent) (string, string) {
		f := foldText(e.Reason)
		if f == "" {
			return "", ""
		}
		return f, strings.Join(strings.Fields(e.Reason), " ")
	})
	out.BySource = groupBy(func(e CancellationEvent) (string, string) { return e.Source, e.Source })
	out.ByCompany = groupBy(func(e CancellationEvent) (string, string) { return e.Company, e.Company })
	fees, n := decimal.Zero, 0
	for _, e := range out.Events {
		if e.FeeNet != nil && !e.FeeReversed {
			v, _ := decimal.NewFromString(*e.FeeNet)
			fees = fees.Add(v)
			n++
		}
	}
	out.FeesPosted, out.FeeCount = fx(fees, decimals), n
}

// foldText folds case and spacing, so "Guest ill" and "guest  ILL" are one reason.
func foldText(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
