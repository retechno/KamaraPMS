package taxfiling

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/audit"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling/taxfilingdb"
	"kamarapms/internal/tenancy"
)

const maxPage = 200

// Service is the tax filing application service. Reading needs tax.view, the filing profiles tax.manage, and filing
// returns and paying the tax tax.file (voiding one also needs an approval).
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	acct  *accounting.Service
	iam   *iam.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, acct *accounting.Service, iamSvc *iam.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, acct: acct, iam: iamSvc}
}

func (s *Service) q(ctx context.Context) *taxfilingdb.Queries { return taxfilingdb.New(s.txm.DB(ctx)) }

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated}
}

func errProfileNotFound() *apperr.Error {
	return apperr.NotFound("TAX_PROFILE_NOT_FOUND", "how this tax is filed has not been set up in this property")
}
func errReturnNotFound() *apperr.Error {
	return apperr.NotFound("TAX_RETURN_NOT_FOUND", "the tax return does not exist in this property")
}
func errPaymentNotFound() *apperr.Error {
	return apperr.NotFound("TAX_PAYMENT_NOT_FOUND", "the tax payment does not exist in this property")
}

// retryOnDuplicate runs a use case again once when a concurrent request with the same Idempotency-Key won the insert.
func retryOnDuplicate(key string, run func() error) error {
	err := run()
	var ae *apperr.Error
	if key != "" && errors.As(err, &ae) && ae.Code == "DUPLICATE_REQUEST" {
		return run()
	}
	return err
}

// ---------------------------------------------------------------------------------------------------------------
// Profiles

func toProfile(r taxfilingdb.ListProfilesRow) Profile {
	return Profile{
		ID: r.ID, TaxID: r.TaxID, TaxCode: r.TaxCode, TaxName: r.TaxName, TaxRate: r.TaxRate, TaxKind: r.TaxKind, GLAccountCode: deref(r.GlAccountCode), Authority: r.Authority,
		RegistrationNumber: deref(r.RegistrationNumber), DueDay: int(r.DueDay), IsActive: r.IsActive, ClaimsInputVAT: r.ClaimsInputVat, OpeningCredit: r.OpeningCredit, CreatedAt: r.CreatedAt,
	}
}

// Profiles lists how each tax is filed (tax.view).
func (s *Service) Profiles(ctx context.Context, propertyID int64) ([]Profile, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return nil, err
	}
	return s.listProfiles(ctx, p.TenantID, propertyID, nil, nil)
}

func (s *Service) listProfiles(ctx context.Context, tenantID, propertyID int64, id, taxID *int64) ([]Profile, error) {
	rows, err := s.q(ctx).ListProfiles(ctx, taxfilingdb.ListProfilesParams{TenantID: tenantID, PropertyID: propertyID, ID: id, TaxID: taxID})
	if err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(rows))
	for _, r := range rows {
		out = append(out, toProfile(r))
	}
	return out, nil
}

func (s *Service) profileOfTax(ctx context.Context, tenantID, propertyID, taxID int64) (Profile, error) {
	list, err := s.listProfiles(ctx, tenantID, propertyID, nil, &taxID)
	if err != nil {
		return Profile{}, err
	}
	if len(list) == 0 {
		return Profile{}, errProfileNotFound()
	}
	return list[0], nil
}

func (s *Service) loadProfile(ctx context.Context, tenantID, propertyID, id int64) (Profile, error) {
	list, err := s.listProfiles(ctx, tenantID, propertyID, &id, nil)
	if err != nil {
		return Profile{}, err
	}
	if len(list) == 0 {
		return Profile{}, errProfileNotFound()
	}
	return list[0], nil
}

// GetProfile is one filing profile (tax.view).
func (s *Service) GetProfile(ctx context.Context, propertyID, id int64) (Profile, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return Profile{}, err
	}
	return s.loadProfile(ctx, p.TenantID, propertyID, id)
}

func validateProfile(authority, registration string, dueDay int) []apperr.FieldError {
	var fields []apperr.FieldError
	switch {
	case strings.TrimSpace(authority) == "":
		fields = append(fields, fieldErr("authority", "REQUIRED", "the tax authority the tax is filed with"))
	case len([]rune(authority)) > 150:
		fields = append(fields, fieldErr("authority", "TOO_LONG", "at most 150 characters"))
	}
	if len([]rune(registration)) > 60 {
		fields = append(fields, fieldErr("registration_number", "TOO_LONG", "at most 60 characters"))
	}
	if dueDay < 1 || dueDay > 28 {
		fields = append(fields, fieldErr("due_day", "OUT_OF_RANGE", "a day between 1 and 28"))
	}
	return fields
}

// CreateProfile sets up the filing of a tax (tax.manage).
func (s *Service) CreateProfile(ctx context.Context, propertyID int64, in ProfileInput) (Profile, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxManage)
	if err != nil {
		return Profile{}, err
	}
	dueDay := 15
	if in.DueDay != nil {
		dueDay = *in.DueDay
	}
	if fields := validateProfile(in.Authority, in.RegistrationNumber, dueDay); len(fields) > 0 {
		return Profile{}, apperr.Invalid("the filing profile is invalid", fields...)
	}
	claims := in.ClaimsInputVAT != nil && *in.ClaimsInputVAT
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		tax, err := s.q(ctx).TaxOfProperty(ctx, taxfilingdb.TaxOfPropertyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.TaxID})
		if isNoRows(err) {
			return apperr.Invalid("the filing profile is invalid", fieldErr("tax_id", "NOT_FOUND", "no such tax in this property"))
		}
		if err != nil {
			return err
		}
		if claims && tax.TaxKind != "VAT" {
			return apperr.Invalid("the filing profile is invalid", fieldErr("claims_input_vat", "NOT_VAT", "only the profile of a VAT tax claims input VAT"))
		}
		id, err = s.q(ctx).InsertProfile(ctx, taxfilingdb.InsertProfileParams{
			TenantID: p.TenantID, PropertyID: propertyID, TaxID: in.TaxID, Authority: strings.TrimSpace(in.Authority), RegistrationNumber: nullable(in.RegistrationNumber),
			DueDay: int16(dueDay), IsActive: in.IsActive == nil || *in.IsActive, ClaimsInputVat: claims, ActorID: p.ActorID(), //nolint:gosec // G115: validated to 1..28
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.profile_created", "tax_filing_profile", id, auditlabel.TaxOfProfile(ctx, propertyID, id), nil, map[string]any{"tax": tax.Code, "authority": in.Authority}))
	})
	if err != nil {
		return Profile{}, err
	}
	return s.loadProfile(ctx, p.TenantID, propertyID, id)
}

// UpdateProfile changes the filing of a tax (tax.manage).
func (s *Service) UpdateProfile(ctx context.Context, propertyID, id int64, patch ProfilePatch) (Profile, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxManage)
	if err != nil {
		return Profile{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxProfiles, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return errProfileNotFound()
		}
		cur, err := s.loadProfile(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		authority, registration, dueDay, active, claims := cur.Authority, cur.RegistrationNumber, cur.DueDay, cur.IsActive, cur.ClaimsInputVAT
		if patch.Authority != nil {
			authority = strings.TrimSpace(*patch.Authority)
		}
		if patch.RegistrationNumber != nil {
			registration = strings.TrimSpace(*patch.RegistrationNumber)
		}
		if patch.DueDay != nil {
			dueDay = *patch.DueDay
		}
		if patch.IsActive != nil {
			active = *patch.IsActive
		}
		if patch.ClaimsInputVAT != nil {
			claims = *patch.ClaimsInputVAT
		}
		if claims && !cur.ClaimsInputVAT && cur.TaxKind != "VAT" {
			return apperr.Invalid("the filing profile is invalid", fieldErr("claims_input_vat", "NOT_VAT", "only the profile of a VAT tax claims input VAT"))
		}
		if !claims && cur.ClaimsInputVAT {
			n, err := s.q(ctx).CountLiveClaimsOfTax(ctx, taxfilingdb.CountLiveClaimsOfTaxParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: cur.TaxID})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("TAX_CLAIMS_IN_USE", "the returns of this tax have claimed input VAT: void them first").WithContext("claims", n)
			}
		}
		if fields := validateProfile(authority, registration, dueDay); len(fields) > 0 {
			return apperr.Invalid("the filing profile is invalid", fields...)
		}
		if err := s.q(ctx).UpdateProfile(ctx, taxfilingdb.UpdateProfileParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Authority: authority, RegistrationNumber: nullable(registration), DueDay: int16(dueDay), IsActive: active, ClaimsInputVat: claims, ActorID: p.ActorID(), //nolint:gosec // G115: validated to 1..28
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.profile_updated", "tax_filing_profile", id, auditlabel.TaxOfProfile(ctx, propertyID, id),
			map[string]any{"authority": cur.Authority, "due_day": cur.DueDay, "active": cur.IsActive}, map[string]any{"authority": authority, "due_day": dueDay, "active": active}))
	})
	if err != nil {
		return Profile{}, err
	}
	return s.loadProfile(ctx, p.TenantID, propertyID, id)
}

// ---------------------------------------------------------------------------------------------------------------
// The worksheet of a month

func monthLabel(d civil.Date) string { return fmt.Sprintf("%04d-%02d", d.Year(), int(d.Month())) }

// worksheet computes the month of a tax: what was collected, what the books say, whether the month can be filed. It
// reads only; filing recomputes it under the profile's lock.
func (s *Service) worksheet(ctx context.Context, tenantID, propertyID int64, prof Profile, start, today civil.Date) (Worksheet, error) {
	end := periodEnd(start)
	q := s.q(ctx)
	rows, err := q.WorksheetLines(ctx, taxfilingdb.WorksheetLinesParams{TenantID: tenantID, PropertyID: propertyID, TaxID: &prof.TaxID, FromDate: start, ToDate: end})
	if err != nil {
		return Worksheet{}, err
	}
	w := Worksheet{Profile: prof, PeriodStart: start, PeriodEnd: end, DueDate: dueDate(start, prof.DueDay), Lines: []WorksheetLine{}, Input: []InputClaim{}, ClaimsInputVAT: prof.ClaimsInputVAT, Blockers: []string{}}
	for _, r := range rows {
		w.Lines = append(w.Lines, WorksheetLine{ChargeCode: r.ChargeCode, ChargeName: r.ChargeName, Rate: r.Rate, Items: int(r.Items), Base: r.Base, Tax: r.Tax})
		w.Base, w.Tax = w.Base.Add(r.Base), w.Tax.Add(r.Tax)
	}
	// the credit notes to companies take off what was collected: a negative line per rate (and a positive one for a credit note voided in the month)
	notes, err := q.CreditNoteTaxLines(ctx, taxfilingdb.CreditNoteTaxLinesParams{TenantID: tenantID, PropertyID: propertyID, TaxID: &prof.TaxID, FromDate: start, ToDate: end})
	if err != nil {
		return Worksheet{}, err
	}
	for _, r := range notes {
		w.Lines = append(w.Lines, WorksheetLine{ChargeCode: CreditNotesCode, ChargeName: "Credit notes", Rate: r.Rate, Items: int(r.Items), Base: r.Base, Tax: r.Tax})
		w.Base, w.Tax = w.Base.Add(r.Base), w.Tax.Add(r.Tax)
	}
	if w.Input, err = s.inputClaims(ctx, tenantID, propertyID, prof, end); err != nil {
		return Worksheet{}, err
	}
	input := decimal.Zero
	for _, c := range w.Input {
		input = input.Add(c.Amount)
	}
	bf, err := s.creditBroughtForward(ctx, tenantID, propertyID, prof.TaxID, start)
	if err != nil {
		return Worksheet{}, err
	}
	w.VATOffset = offsetOf(w.Tax, input, bf)
	w.GLCollected, err = q.GLCollected(ctx, taxfilingdb.GLCollectedParams{TenantID: tenantID, PropertyID: propertyID, TaxCode: &prof.TaxCode, FromDate: start, ToDate: end})
	if err != nil {
		return Worksheet{}, err
	}
	w.Difference = w.Tax.Sub(w.GLCollected)
	first := start
	if st, err := q.AccountingStart(ctx, taxfilingdb.AccountingStartParams{TenantID: tenantID, PropertyID: propertyID}); err == nil {
		if st.After(first) {
			first = st
		}
		if end.Before(st) {
			w.Blockers = append(w.Blockers, "The month is before accounting started on "+st.String()+".")
		}
	} else if isNoRows(err) {
		w.Blockers = append(w.Blockers, "Accounting is not set up for this property.")
	} else {
		return Worksheet{}, err
	}
	if !end.Before(today) {
		w.Blockers = append(w.Blockers, "The month is not over yet.")
	} else if !first.After(end) {
		w.Days = first.DaysUntil(end) + 1
		posted, err := q.CountPostedDays(ctx, taxfilingdb.CountPostedDaysParams{TenantID: tenantID, PropertyID: propertyID, FromDate: first, ToDate: end})
		if err != nil {
			return Worksheet{}, err
		}
		w.PostedDays = int(posted)
		if w.PostedDays != w.Days {
			w.Blockers = append(w.Blockers, fmt.Sprintf("Only %d of %d business days are closed with their journal.", w.PostedDays, w.Days))
		}
	}
	if w.Tax.IsNegative() {
		w.Blockers = append(w.Blockers, "The tax of the month is negative: reversals exceed what was charged.")
	}
	if !w.Difference.IsZero() && len(w.Blockers) == 0 {
		w.Blockers = append(w.Blockers, "The tax on the folios ("+w.Tax.String()+") differs from the books ("+w.GLCollected.String()+").")
	}
	w.Ready = len(w.Blockers) == 0
	return w, nil
}

// Worksheet is the worksheet of a month of a tax, with the return filed for it if there is one (tax.view).
func (s *Service) Worksheet(ctx context.Context, propertyID, taxID int64, start civil.Date) (Worksheet, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return Worksheet{}, err
	}
	if start.IsZero() || start.Day() != 1 {
		return Worksheet{}, apperr.Invalid("the period is invalid", fieldErr("period", "NOT_FIRST_OF_MONTH", "the first day of a month"))
	}
	prof, err := s.profileOfTax(ctx, p.TenantID, propertyID, taxID)
	if err != nil {
		return Worksheet{}, err
	}
	today, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Worksheet{}, err
	}
	w, err := s.worksheet(ctx, p.TenantID, propertyID, prof, start, today.BusinessDate)
	if err != nil {
		return Worksheet{}, err
	}
	if id, err := s.q(ctx).LiveReturnOfPeriod(ctx, taxfilingdb.LiveReturnOfPeriodParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: taxID, PeriodStart: start}); err == nil {
		ret, err := s.loadReturn(ctx, p.TenantID, propertyID, id, today.BusinessDate)
		if err != nil {
			return Worksheet{}, err
		}
		w.Return = &ret
		w.Ready = false
	} else if !isNoRows(err) {
		return Worksheet{}, err
	}
	return w, nil
}

// Periods lists the months of a tax from the accounting start to the current month, newest first, with what was
// collected, whether each is filed or ready, and what is paid (tax.view).
func (s *Service) Periods(ctx context.Context, propertyID, taxID int64) ([]Period, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return nil, err
	}
	prof, err := s.profileOfTax(ctx, p.TenantID, propertyID, taxID)
	if err != nil {
		return nil, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	today := day.BusinessDate
	q := s.q(ctx)
	st, err := q.AccountingStart(ctx, taxfilingdb.AccountingStartParams{TenantID: p.TenantID, PropertyID: propertyID})
	if isNoRows(err) {
		return []Period{}, nil
	}
	if err != nil {
		return nil, err
	}
	returns, err := s.listReturns(ctx, p.TenantID, propertyID, nil, &taxID, ReturnFiled, 0, today)
	if err != nil {
		return nil, err
	}
	byStart := map[civil.Date]Return{}
	for _, r := range returns {
		byStart[r.PeriodStart] = r
	}
	var out []Period
	for start := periodStart(st); !start.After(periodStart(today)) && len(out) < 240; start = periodEnd(start).AddDays(1) {
		end := periodEnd(start)
		per := Period{PeriodStart: start, PeriodEnd: end, DueDate: dueDate(start, prof.DueDay), Status: "OPEN"}
		if r, ok := byStart[start]; ok {
			id := r.ID
			per.Status, per.ReturnID, per.Tax, per.Paid, per.Outstanding, per.Overdue = "FILED", &id, r.Tax, r.Paid, r.Outstanding, r.Overdue
			per.Payable, per.CreditCarriedForward = r.Payable, r.CreditCarriedForward
		} else {
			tax, err := q.CollectedBetween(ctx, taxfilingdb.CollectedBetweenParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: &taxID, FromDate: start, ToDate: end})
			if err != nil {
				return nil, err
			}
			credits, err := q.CreditNoteTaxBetween(ctx, taxfilingdb.CreditNoteTaxBetweenParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: &taxID, FromDate: start, ToDate: end})
			if err != nil {
				return nil, err
			}
			per.Tax = tax.Add(credits)
			if end.Before(today) {
				first := start
				if st.After(first) {
					first = st
				}
				posted, err := q.CountPostedDays(ctx, taxfilingdb.CountPostedDaysParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: first, ToDate: end})
				if err != nil {
					return nil, err
				}
				if int(posted) == first.DaysUntil(end)+1 {
					per.Status = "READY"
				}
				per.Overdue = per.DueDate.Before(today)
			}
		}
		out = append(out, per)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------
// Returns

func toReturn(r taxfilingdb.ListReturnsRow, today civil.Date) Return {
	ret := Return{
		ID: r.ID, Number: r.ReturnNumber, TaxID: r.TaxID, TaxCode: r.TaxCode, TaxName: r.TaxName, PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd, DueDate: r.DueDate,
		Base: r.BaseAmount, Tax: r.TaxAmount, Status: r.Status, FiledOn: r.FiledOn, FilingReference: deref(r.FilingReference), Notes: deref(r.Notes), FiledAt: r.FiledAt,
		VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason), Paid: r.Paid, OffsetJournalID: r.OffsetJournalID,
		VATOffset: VATOffset{InputClaimed: r.InputClaimed, CreditBroughtForward: r.CreditBroughtForward, Offset: r.OffsetAmount, Payable: r.PayableAmount, CreditCarriedForward: r.CreditCarriedForward},
	}
	if r.Status == ReturnVoided {
		ret.PaymentStatus, ret.Paid = PayVoided, decimal.Zero
		return ret
	}
	ret.Outstanding = r.PayableAmount.Sub(r.Paid)
	switch {
	case ret.Outstanding.IsZero():
		ret.PaymentStatus = PayPaid
	case r.Paid.IsPositive():
		ret.PaymentStatus = PayPartial
	default:
		ret.PaymentStatus = PayUnpaid
	}
	ret.Overdue = ret.Outstanding.IsPositive() && r.DueDate.Before(today)
	return ret
}

func (s *Service) listReturns(ctx context.Context, tenantID, propertyID int64, id, taxID *int64, status string, limit int, today civil.Date) ([]Return, error) {
	if limit < 1 || limit > maxPage {
		limit = maxPage
	}
	rows, err := s.q(ctx).ListReturns(ctx, taxfilingdb.ListReturnsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, TaxID: taxID, Status: nullable(status), RowLimit: int32(limit)}) //nolint:gosec // G115: at most 200
	if err != nil {
		return nil, err
	}
	out := make([]Return, 0, len(rows))
	for _, r := range rows {
		out = append(out, toReturn(r, today))
	}
	return out, nil
}

// Returns lists the returns filed, newest period first (tax.view).
func (s *Service) Returns(ctx context.Context, propertyID int64, f ReturnFilter) ([]Return, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return nil, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	return s.listReturns(ctx, p.TenantID, propertyID, nil, f.TaxID, f.Status, f.Limit, day.BusinessDate)
}

// GetReturn is a return with its worksheet lines and payments (tax.view).
func (s *Service) GetReturn(ctx context.Context, propertyID, id int64) (Return, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return Return{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Return{}, err
	}
	return s.loadReturn(ctx, p.TenantID, propertyID, id, day.BusinessDate)
}

func (s *Service) loadReturn(ctx context.Context, tenantID, propertyID, id int64, today civil.Date) (Return, error) {
	list, err := s.listReturns(ctx, tenantID, propertyID, &id, nil, "", 1, today)
	if err != nil {
		return Return{}, err
	}
	if len(list) == 0 {
		return Return{}, errReturnNotFound()
	}
	ret := list[0]
	lines, err := s.q(ctx).ListReturnLines(ctx, taxfilingdb.ListReturnLinesParams{TenantID: tenantID, PropertyID: propertyID, ReturnID: id})
	if err != nil {
		return Return{}, err
	}
	ret.Lines = make([]WorksheetLine, 0, len(lines))
	for _, l := range lines {
		ret.Lines = append(ret.Lines, WorksheetLine{ChargeCode: l.ChargeCode, ChargeName: deref(l.ChargeName), Rate: l.Rate, Items: int(l.Items), Base: l.BaseAmount, Tax: l.TaxAmount})
	}
	if ret.Input, err = s.loadClaims(ctx, tenantID, propertyID, id); err != nil {
		return Return{}, err
	}
	ret.Payments, err = s.listPayments(ctx, tenantID, propertyID, nil, &id, "", 0)
	return ret, err
}

// FileReturn files the return of a month (tax.file): the worksheet is computed again under the lock of the tax and
// frozen. The month must be over, every business day of it closed with its journal, and the tax on the folios must equal
// what the books credited; the month before must be filed (the first month of the books excepted). The Idempotency-Key
// makes a retry return the first return.
func (s *Service) FileReturn(ctx context.Context, propertyID int64, in FileInput, key string) (Return, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxFile)
	if err != nil {
		return Return{}, err
	}
	var fields []apperr.FieldError
	if in.PeriodStart.IsZero() || in.PeriodStart.Day() != 1 {
		fields = append(fields, fieldErr("period_start", "NOT_FIRST_OF_MONTH", "the first day of the month the return covers"))
	}
	if len([]rune(in.FilingReference)) > 100 {
		fields = append(fields, fieldErr("filing_reference", "TOO_LONG", "at most 100 characters"))
	}
	if len([]rune(in.Notes)) > 500 {
		fields = append(fields, fieldErr("notes", "TOO_LONG", "at most 500 characters"))
	}
	if len(fields) > 0 {
		return Return{}, apperr.Invalid("the return is invalid", fields...)
	}
	var id int64
	err = retryOnDuplicate(key, func() error { return s.fileReturn(ctx, p, propertyID, in, key, &id) })
	if err != nil {
		return Return{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Return{}, err
	}
	return s.loadReturn(ctx, p.TenantID, propertyID, id, day.BusinessDate)
}

func (s *Service) fileReturn(ctx context.Context, p auth.Principal, propertyID int64, in FileInput, key string, out *int64) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID) // accounting settings (46) before the tax profile (49)
		if err != nil {
			return err
		}
		prof, err := s.profileOfTax(ctx, p.TenantID, propertyID, in.TaxID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxProfiles, db.ForUpdate, propertyID, []int64{prof.ID}); err != nil {
			return errProfileNotFound()
		}
		q := s.q(ctx)
		if key != "" {
			if prev, err := q.FindReturnByKey(ctx, taxfilingdb.FindReturnByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
				*out = prev
				return nil
			} else if !isNoRows(err) {
				return err
			}
		}
		if !prof.IsActive {
			return apperr.Conflict("TAX_PROFILE_INACTIVE", "this tax is not filed any more")
		}
		if _, err := q.LiveReturnOfPeriod(ctx, taxfilingdb.LiveReturnOfPeriodParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: in.TaxID, PeriodStart: in.PeriodStart}); err == nil {
			return apperr.Conflict("TAX_RETURN_EXISTS", "a return is filed for this month already").WithContext("period", monthLabel(in.PeriodStart))
		} else if !isNoRows(err) {
			return err
		}
		w, err := s.worksheet(ctx, p.TenantID, propertyID, prof, in.PeriodStart, day.BusinessDate)
		if err != nil {
			return err
		}
		if !w.Ready {
			return apperr.Conflict("TAX_MONTH_NOT_READY", "the month cannot be filed yet").WithContext("blockers", w.Blockers)
		}
		// the months go in order: the month before is filed, unless this is the first month of the books
		if st, err := q.AccountingStart(ctx, taxfilingdb.AccountingStartParams{TenantID: p.TenantID, PropertyID: propertyID}); err == nil && in.PeriodStart.After(periodStart(st)) {
			prevStart := periodStart(in.PeriodStart.AddDays(-1))
			if _, err := q.LiveReturnOfPeriod(ctx, taxfilingdb.LiveReturnOfPeriodParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: in.TaxID, PeriodStart: prevStart}); isNoRows(err) {
				return apperr.Conflict("TAX_PREVIOUS_NOT_FILED", "file the month before first").WithContext("period", monthLabel(prevStart))
			} else if err != nil {
				return err
			}
		}
		filedOn := in.FiledOn
		switch {
		case filedOn.IsZero():
			filedOn = day.BusinessDate
		case filedOn.After(day.BusinessDate):
			return apperr.Invalid("the return is invalid", fieldErr("filed_on", "IN_THE_FUTURE", "not after the current business date"))
		case filedOn.Before(w.PeriodEnd):
			return apperr.Invalid("the return is invalid", fieldErr("filed_on", "BEFORE_PERIOD_END", "not before the month ends"))
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqTaxReturn)
		if err != nil {
			return err
		}
		var offsetJournal *int64
		if !w.Offset.IsZero() {
			jid, err := s.postOffset(ctx, po, p.TenantID, propertyID, prof, w, number, filedOn)
			if err != nil {
				return err
			}
			offsetJournal = &jid
		}
		id, err := q.InsertReturn(ctx, taxfilingdb.InsertReturnParams{
			TenantID: p.TenantID, PropertyID: propertyID, ReturnNumber: number, TaxID: in.TaxID, PeriodStart: w.PeriodStart, PeriodEnd: w.PeriodEnd, DueDate: w.DueDate,
			BaseAmount: w.Base, TaxAmount: w.Tax, FiledOn: filedOn, FilingReference: nullable(in.FilingReference), Notes: nullable(in.Notes), Now: s.clock.Now(), ActorID: p.ActorID(),
			IdempotencyKey: nullable(key), InputClaimed: w.InputClaimed, CreditBroughtForward: w.CreditBroughtForward, OffsetJournalID: offsetJournal,
		})
		if err != nil {
			return err
		}
		for i, l := range w.Lines {
			if err := q.InsertReturnLine(ctx, taxfilingdb.InsertReturnLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, ReturnID: id, LineNo: int32(i + 1), ChargeCode: l.ChargeCode, ChargeName: nullable(l.ChargeName), Rate: l.Rate, //nolint:gosec // G115: a few lines
				Items: int32(l.Items), BaseAmount: l.Base, TaxAmount: l.Tax, //nolint:gosec // G115: a count of items
			}); err != nil {
				return err
			}
		}
		for _, c := range w.Input {
			var reverses *int64
			if c.Reversal {
				reverses = ptr(c.reversesID)
			}
			claim := taxfilingdb.InsertClaimParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: id, Amount: c.Amount, ReversesClaimID: reverses}
			switch c.Source {
			case ClaimCreditNote:
				claim.CreditID, claim.CreditLineNo = ptr(c.CreditID), ptr(int64(c.CreditLineNo))
			case ClaimSettlement:
				claim.SettlementID = ptr(c.SettlementID)
			default:
				claim.BillID, claim.LineNo = ptr(c.BillID), ptr(int64(c.LineNo))
			}
			if err := q.InsertClaim(ctx, claim); err != nil {
				return err
			}
		}
		*out = id
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.return_filed", "tax_return", id, auditlabel.TaxReturn(ctx, propertyID, id), nil,
			map[string]any{"return_number": number, "tax": prof.TaxCode, "period": monthLabel(in.PeriodStart), "base": w.Base.String(), "tax_amount": w.Tax.String(), "reference": in.FilingReference,
				"input_claimed": w.InputClaimed.String(), "credit_brought_forward": w.CreditBroughtForward.String(), "offset": w.Offset.String(), "payable": w.Payable.String(), "credit_carried_forward": w.CreditCarriedForward.String()}))
	})
}

// VoidReturn voids a return that has no payments (tax.file plus an approval), so the month can be filed again.
func (s *Service) VoidReturn(ctx context.Context, propertyID, id int64, in VoidInput) (Return, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxFile)
	if err != nil {
		return Return{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Return{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Return{}, err
	}
	var today civil.Date
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		today = day.BusinessDate
		po, err := s.acct.BeginPosting(ctx, propertyID) // accounting settings (46) before the tax profile (49)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		profID, err := q.ProfileOfReturn(ctx, taxfilingdb.ProfileOfReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: id})
		if isNoRows(err) {
			return errReturnNotFound()
		}
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxProfiles, db.ForUpdate, propertyID, []int64{profID}); err != nil {
			return errProfileNotFound()
		}
		ret, err := s.loadReturn(ctx, p.TenantID, propertyID, id, day.BusinessDate)
		if err != nil {
			return err
		}
		if ret.Status != ReturnFiled {
			return apperr.Conflict("TAX_RETURN_ALREADY_VOIDED", "the return is voided already")
		}
		n, err := q.CountLivePayments(ctx, taxfilingdb.CountLivePaymentsParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: id})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("TAX_RETURN_HAS_PAYMENTS", "a return with payments cannot be voided: void its payments first").WithContext("payments", n)
		}
		// the credit carried goes on to the next month, so only the latest return of a tax can be voided
		if n, err := q.CountLaterLiveReturns(ctx, taxfilingdb.CountLaterLiveReturnsParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: ret.TaxID, PeriodStart: ret.PeriodStart}); err != nil {
			return err
		} else if n > 0 {
			return apperr.Conflict("TAX_RETURN_NOT_LATEST", "a return of a later month is filed: void the latest return first").WithContext("returns", n)
		}
		by := approval.UserID()
		var offsetVoid *int64
		if ret.OffsetJournalID != nil {
			rj, err := po.Reverse(ctx, *ret.OffsetJournalID, day.BusinessDate, reason, by)
			if err != nil {
				return err
			}
			offsetVoid = &rj
		}
		if err := q.ReleaseClaims(ctx, taxfilingdb.ReleaseClaimsParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: id, Now: ptr(s.clock.Now())}); err != nil {
			return err
		}
		if err := q.VoidReturn(ctx, taxfilingdb.VoidReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, ApprovedBy: &by, OffsetVoidJournalID: offsetVoid}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.return_voided", "tax_return", id, auditlabel.TaxReturn(ctx, propertyID, id),
			map[string]any{"status": "FILED"}, map[string]any{"status": "VOIDED", "reason": reason, "approved_by": by, "return_number": ret.Number}))
	})
	if err != nil {
		return Return{}, err
	}
	return s.loadReturn(ctx, p.TenantID, propertyID, id, today)
}
