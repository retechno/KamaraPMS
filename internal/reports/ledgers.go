package reports

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
)

// The guest ledger and the deposit ledger as of a closed business date (docs/architecture/38-reports-night-audit-pack.md, 7.3). Every sum is of the items dated up to the date, so the figures of a past
// day do not move when later days are posted.

// noDate is the date the queries use for "none" (a folio that has no arrival).
var noDate = civil.MustParseDate("1900-01-01")

// LedgerCheck is a comparison the report prints: the figure of the report against the same figure from another source, with the difference (zero for a closed day).
type LedgerCheck struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	Account    string `json:"account,omitempty"`
	Report     string `json:"report"`
	Other      string `json:"other"`
	Difference string `json:"difference"`
}

// GuestLedgerRow is a folio with a balance, or holding a deposit, as of the date.
type GuestLedgerRow struct {
	FolioID            int64       `json:"folio_id"`
	FolioNumber        string      `json:"folio_number"`
	FolioType          string      `json:"folio_type"`
	Status             string      `json:"status"`
	ReservationID      *int64      `json:"reservation_id"`
	ConfirmationNumber string      `json:"confirmation_number"`
	Room               string      `json:"room"`
	Owner              string      `json:"owner"`
	Arrival            *civil.Date `json:"arrival"`
	Departure          *civil.Date `json:"departure"`
	Charges            string      `json:"charges"`
	Settlements        string      `json:"settlements"`
	Deposits           string      `json:"deposits"`
	Balance            string      `json:"balance"`
	DepositHeld        string      `json:"deposit_held"`
	AgeDays            *int        `json:"age_days"`
	AgeBucket          string      `json:"age_bucket"`
}

// AgeTotal is the balance of the folios that owe, by the age of the oldest charge.
type AgeTotal struct {
	Bucket  string `json:"bucket"`
	Folios  int    `json:"folios"`
	Balance string `json:"balance"`
}

// GuestLedgerReport is the guest ledger.
type GuestLedgerReport struct {
	AsOf        civil.Date       `json:"as_of"`
	ReleaseMode string           `json:"release_mode"`
	Rows        []GuestLedgerRow `json:"rows"`
	Charges     string           `json:"charges"`
	Settlements string           `json:"settlements"`
	Deposits    string           `json:"deposits"`
	Balance     string           `json:"balance"`
	DepositHeld string           `json:"deposit_held"`
	Aging       []AgeTotal       `json:"aging"`
	Checks      []LedgerCheck    `json:"checks"`
	Reconciled  bool             `json:"reconciled"`
}

// CSV is the list of the folios.
func (r GuestLedgerReport) CSV() ([]string, [][]string) {
	h := []string{"folio_number", "folio_type", "status", "confirmation_number", "room", "owner", "arrival", "departure", "charges", "settlements", "deposits_received", "balance", "deposit_held", "age_days", "age_bucket"}
	rows := make([][]string, 0, len(r.Rows))
	for _, x := range r.Rows {
		rows = append(rows, []string{x.FolioNumber, x.FolioType, x.Status, x.ConfirmationNumber, x.Room, x.Owner, optDate(x.Arrival), optDate(x.Departure), x.Charges, x.Settlements, x.Deposits, x.Balance, x.DepositHeld, optInt(x.AgeDays), x.AgeBucket})
	}
	return h, rows
}

// DepositLedgerRow is a folio that held a deposit, or moved one, on the date. The identity Opening + Received - Applied - Refunded = Closing is asserted for each row (Balanced).
type DepositLedgerRow struct {
	FolioID            int64       `json:"folio_id"`
	FolioNumber        string      `json:"folio_number"`
	FolioType          string      `json:"folio_type"`
	ReservationID      *int64      `json:"reservation_id"`
	ConfirmationNumber string      `json:"confirmation_number"`
	Owner              string      `json:"owner"`
	Arrival            *civil.Date `json:"arrival"`
	Opening            string      `json:"opening"`
	Received           string      `json:"received"`
	Applied            string      `json:"applied"`
	Refunded           string      `json:"refunded"`
	Closing            string      `json:"closing"`
	Balanced           bool        `json:"balanced"`
	ExpectedUse        *civil.Date `json:"expected_use"`
	Overdue            bool        `json:"overdue"`
}

// DepositLedgerReport is the deposit ledger.
type DepositLedgerReport struct {
	AsOf        civil.Date         `json:"as_of"`
	ReleaseMode string             `json:"release_mode"`
	Rows        []DepositLedgerRow `json:"rows"`
	Opening     string             `json:"opening"`
	Received    string             `json:"received"`
	Applied     string             `json:"applied"`
	Refunded    string             `json:"refunded"`
	Closing     string             `json:"closing"`
	Checks      []LedgerCheck      `json:"checks"`
	Reconciled  bool               `json:"reconciled"`
}

// CSV is the list of the folios.
func (r DepositLedgerReport) CSV() ([]string, [][]string) {
	h := []string{"folio_number", "folio_type", "confirmation_number", "owner", "arrival", "opening", "received", "applied", "refunded", "closing", "expected_use", "overdue"}
	rows := make([][]string, 0, len(r.Rows))
	for _, x := range r.Rows {
		rows = append(rows, []string{x.FolioNumber, x.FolioType, x.ConfirmationNumber, x.Owner, optDate(x.Arrival), x.Opening, x.Received, x.Applied, x.Refunded, x.Closing, optDate(x.ExpectedUse), boolStr(x.Overdue)})
	}
	return h, rows
}

// AsOfFrom checks report.view and then reads as_of: the permission is answered before the query is looked at. Without a date it is the last closed business day.
func (s *Service) AsOfFrom(ctx context.Context, propertyID int64, q func(string) string) (*civil.Date, error) {
	if _, _, err := s.access(ctx, propertyID); err != nil {
		return nil, err
	}
	v := q("as_of")
	if v == "" {
		return nil, nil
	}
	d, err := civil.ParseDate(v)
	if err != nil {
		return nil, apperr.Invalid("the query is invalid", apperr.FieldError{Field: "as_of", Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	return &d, nil
}

// closedDate resolves as_of: the last closed day when it is absent; a date that is not a closed business day is refused (the books of the open day are not complete).
func (s *Service) closedDate(ctx context.Context, tenantID, propertyID int64, asOf *civil.Date) (civil.Date, error) {
	q := s.q(ctx)
	if asOf == nil {
		d, err := q.FlashLastClosedDay(ctx, reportsdb.FlashLastClosedDayParams{TenantID: tenantID, PropertyID: propertyID})
		if errors.Is(err, pgx.ErrNoRows) {
			return civil.Date{}, apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "the property has no closed business day")
		}
		return d, err
	}
	status, err := q.ReportBusinessDayStatus(ctx, reportsdb.ReportBusinessDayStatusParams{TenantID: tenantID, PropertyID: propertyID, Bd: *asOf})
	if errors.Is(err, pgx.ErrNoRows) {
		if open, e := q.FlashOpenDay(ctx, reportsdb.FlashOpenDayParams{TenantID: tenantID, PropertyID: propertyID}); e == nil && asOf.After(open) {
			return civil.Date{}, apperr.Conflict("DATE_NOT_CLOSED", "the business day is not closed yet, so its books are not complete").WithContext("as_of", asOf.String())
		}
		return civil.Date{}, apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "the property has no business day on that date")
	}
	if err != nil {
		return civil.Date{}, err
	}
	if status != "CLOSED" {
		return civil.Date{}, apperr.Conflict("DATE_NOT_CLOSED", "the business day is not closed yet, so its books are not complete").WithContext("as_of", asOf.String())
	}
	return *asOf, nil
}

// ledgerAccounts reads the control accounts of the guests as of the date; it is empty when the accounting map is not set up.
func (s *Service) ledgerAccounts(ctx context.Context, tenantID, propertyID int64, at civil.Date) (map[string]reportsdb.LedgerControlAccountsRow, error) {
	rows, err := s.q(ctx).LedgerControlAccounts(ctx, reportsdb.LedgerControlAccountsParams{TenantID: tenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return nil, err
	}
	m := map[string]reportsdb.LedgerControlAccountsRow{}
	for _, r := range rows {
		m[r.MapKey] = r
	}
	return m, nil
}

func ownerOf(folioType, guest, company, group, outlet, event string) string {
	pick := func(vs ...string) string {
		for _, v := range vs {
			if v != "" {
				return v
			}
		}
		return ""
	}
	switch folioType {
	case "COMPANY":
		return pick(company, guest)
	case "MASTER":
		return pick(group, company, guest)
	case "OUTLET":
		return pick(outlet, guest)
	case "EVENT":
		return pick(event, company, guest)
	}
	return pick(guest, company, group, event, outlet)
}

func optDay(d civil.Date) *civil.Date {
	if d.IsZero() || d.Equal(noDate) {
		return nil
	}
	return &d
}

func ageBucket(days int) string {
	switch {
	case days <= 1:
		return "0-1"
	case days <= 7:
		return "2-7"
	case days <= 30:
		return "8-30"
	}
	return "31+"
}

var ageOrder = []string{"0-1", "2-7", "8-30", "31+"}

func check(key, title, account string, report, other decimal.Decimal, decimals int32) LedgerCheck {
	return LedgerCheck{Key: key, Title: title, Account: account, Report: fx(report, decimals), Other: fx(other, decimals), Difference: fx(report.Sub(other), decimals)}
}

// GuestLedger answers the guest ledger as of a closed business day (report.view): the folios with a balance, the deposits they hold, and the comparison with the control sources and with account 1210.
// A folio that holds a deposit but owes nothing is listed too, so the deposit column adds up to the deposits held.
func (s *Service) GuestLedger(ctx context.Context, propertyID int64, asOf *civil.Date) (GuestLedgerReport, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return GuestLedgerReport{}, err
	}
	at, err := s.closedDate(ctx, p.TenantID, propertyID, asOf)
	if err != nil {
		return GuestLedgerReport{}, err
	}
	q := s.q(ctx)
	src, err := q.LedgerControlSources(ctx, reportsdb.LedgerControlSourcesParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return GuestLedgerReport{}, err
	}
	rows, err := q.GuestLedgerFolios(ctx, reportsdb.GuestLedgerFoliosParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at, EveryDay: src.ReleaseMode == "EVERY_DAY"})
	if err != nil {
		return GuestLedgerReport{}, err
	}
	out := GuestLedgerReport{AsOf: at, ReleaseMode: src.ReleaseMode, Rows: []GuestLedgerRow{}, Aging: []AgeTotal{}, Checks: []LedgerCheck{}}
	var charges, settlements, deposits, balance, held decimal.Decimal
	type bucket struct {
		n   int
		sum decimal.Decimal
	}
	buckets := map[string]*bucket{}
	for _, r := range rows {
		status := "OPEN"
		if r.ClosedOn != nil && !r.ClosedOn.After(at) {
			status = "CLOSED"
		}
		row := GuestLedgerRow{FolioID: r.FolioID, FolioNumber: r.FolioNumber, FolioType: r.FolioType, Status: status, ConfirmationNumber: r.ConfirmationNumber, Room: r.RoomNumber,
			Owner: ownerOf(r.FolioType, r.GuestName, r.CompanyName, r.GroupName, r.OutletName, r.EventName), Arrival: optDay(r.ArrivalDate), Departure: optDay(r.DepartureDate),
			Charges: fx(r.Charges, decimals), Settlements: fx(r.Settlements, decimals), Deposits: fx(r.Deposits, decimals), Balance: fx(r.Balance, decimals), DepositHeld: fx(r.Liability, decimals)}
		if r.ReservationID != 0 {
			id := r.ReservationID
			row.ReservationID = &id
		}
		if r.Balance.IsPositive() && !r.OldestCharge.Equal(noDate) {
			age := r.OldestCharge.DaysUntil(at)
			b := ageBucket(age)
			row.AgeDays, row.AgeBucket = &age, b
			if buckets[b] == nil {
				buckets[b] = &bucket{}
			}
			buckets[b].n++
			buckets[b].sum = buckets[b].sum.Add(r.Balance)
		}
		charges, settlements, deposits = charges.Add(r.Charges), settlements.Add(r.Settlements), deposits.Add(r.Deposits)
		balance, held = balance.Add(r.Balance), held.Add(r.Liability)
		out.Rows = append(out.Rows, row)
	}
	out.Charges, out.Settlements, out.Deposits, out.Balance, out.DepositHeld = fx(charges, decimals), fx(settlements, decimals), fx(deposits, decimals), fx(balance, decimals), fx(held, decimals)
	for _, b := range ageOrder {
		if x := buckets[b]; x != nil {
			out.Aging = append(out.Aging, AgeTotal{Bucket: b, Folios: x.n, Balance: fx(x.sum, decimals)})
		}
	}
	out.Checks = append(out.Checks,
		check("FOLIO_BALANCE", "Balance of the folios", "", balance, src.FolioBalance, decimals),
		check("DEPOSITS_HELD", "Deposits held", "", held, src.DepositsHeld, decimals))
	accts, err := s.ledgerAccounts(ctx, p.TenantID, propertyID, at)
	if err != nil {
		return GuestLedgerReport{}, err
	}
	if a, ok := accts["GUEST_LEDGER"]; ok {
		out.Checks = append(out.Checks, check("ACCOUNT_GUEST_LEDGER", "Guest ledger account", a.AccountCode+" "+a.AccountName, balance.Add(held), a.Balance, decimals))
	}
	out.Reconciled = allZero(out.Checks)
	return out, nil
}

func allZero(cs []LedgerCheck) bool {
	for _, c := range cs {
		if d, err := decimal.NewFromString(c.Difference); err != nil || !d.IsZero() {
			return false
		}
	}
	return true
}

// DepositLedger answers the deposit ledger of a closed business day (report.view): the liability of every folio that held or moved a deposit, the movement of the day, and the comparison with the
// deposits held of the control sources and with account 2310.
func (s *Service) DepositLedger(ctx context.Context, propertyID int64, asOf *civil.Date) (DepositLedgerReport, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return DepositLedgerReport{}, err
	}
	at, err := s.closedDate(ctx, p.TenantID, propertyID, asOf)
	if err != nil {
		return DepositLedgerReport{}, err
	}
	q := s.q(ctx)
	src, err := q.LedgerControlSources(ctx, reportsdb.LedgerControlSourcesParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return DepositLedgerReport{}, err
	}
	rows, err := q.DepositLedgerFolios(ctx, reportsdb.DepositLedgerFoliosParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at, PrevDate: at.AddDays(-1), EveryDay: src.ReleaseMode == "EVERY_DAY"})
	if err != nil {
		return DepositLedgerReport{}, err
	}
	out := DepositLedgerReport{AsOf: at, ReleaseMode: src.ReleaseMode, Rows: []DepositLedgerRow{}, Checks: []LedgerCheck{}}
	var opening, received, applied, refunded, closing decimal.Decimal
	for _, r := range rows {
		balanced := r.Opening.Add(r.Received).Sub(r.Applied).Sub(r.Refunded).Equal(r.Closing)
		row := DepositLedgerRow{FolioID: r.FolioID, FolioNumber: r.FolioNumber, FolioType: r.FolioType, ConfirmationNumber: r.ConfirmationNumber,
			Owner: ownerOf(r.FolioType, r.GuestName, r.CompanyName, r.GroupName, "", r.EventName), Arrival: optDay(r.ArrivalDate),
			Opening: fx(r.Opening, decimals), Received: fx(r.Received, decimals), Applied: fx(r.Applied, decimals), Refunded: fx(r.Refunded, decimals), Closing: fx(r.Closing, decimals), Balanced: balanced}
		if r.ReservationID != 0 {
			id := r.ReservationID
			row.ReservationID = &id
		}
		if row.Arrival != nil {
			row.ExpectedUse = row.Arrival
			row.Overdue = !r.HasStay && r.Closing.IsPositive() && row.Arrival.Before(at)
		}
		opening, received, applied, refunded, closing = opening.Add(r.Opening), received.Add(r.Received), applied.Add(r.Applied), refunded.Add(r.Refunded), closing.Add(r.Closing)
		out.Rows = append(out.Rows, row)
	}
	out.Opening, out.Received, out.Applied, out.Refunded, out.Closing = fx(opening, decimals), fx(received, decimals), fx(applied, decimals), fx(refunded, decimals), fx(closing, decimals)
	out.Checks = append(out.Checks,
		check("MOVEMENT", "Opening + received - applied - refunded", "", opening.Add(received).Sub(applied).Sub(refunded), closing, decimals),
		check("DEPOSITS_HELD", "Deposits held", "", closing, src.DepositsHeld, decimals))
	accts, err := s.ledgerAccounts(ctx, p.TenantID, propertyID, at)
	if err != nil {
		return DepositLedgerReport{}, err
	}
	if a, ok := accts["ADVANCE_DEPOSITS"]; ok {
		out.Checks = append(out.Checks, check("ACCOUNT_ADVANCE_DEPOSITS", "Advance deposits account", a.AccountCode+" "+a.AccountName, closing, a.Balance.Neg(), decimals))
	}
	out.Reconciled = allZero(out.Checks)
	return out, nil
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func parseID(v string) (int64, error) { return strconv.ParseInt(v, 10, 64) }

// queryDate reads a required date of the query.
func queryDate(q func(string) string, name string, errs *[]apperr.FieldError) civil.Date {
	v := q(name)
	if v == "" {
		*errs = append(*errs, apperr.FieldError{Field: name, Code: "REQUIRED", Message: "YYYY-MM-DD"})
		return civil.Date{}
	}
	d, err := civil.ParseDate(v)
	if err != nil {
		*errs = append(*errs, apperr.FieldError{Field: name, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	return d
}
