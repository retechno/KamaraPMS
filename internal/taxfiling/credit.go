package taxfiling

import (
	"context"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling/taxfilingdb"
)

// The input side of the monthly VAT return and the credit carried forward (design: docs/architecture/09-pkp-input-vat.md, step 3).

// openingEquityCode is the account the opening credit is the other side of (opening balance equity in the standard chart).
const openingEquityCode = "3900"

// VATOffset is what a return of the VAT tax offsets against the VAT collected: the input VAT claimed and the credit brought
// forward are added up, the offset is as much of it as the tax collected, the rest is carried to the next month.
type VATOffset struct {
	InputClaimed         decimal.Decimal `json:"input_claimed"`
	CreditBroughtForward decimal.Decimal `json:"credit_brought_forward"`
	Offset               decimal.Decimal `json:"offset"`
	Payable              decimal.Decimal `json:"payable"`
	CreditCarriedForward decimal.Decimal `json:"credit_carried_forward"`
}

// offsetOf is the arithmetic of a return: available = brought forward + input claimed; offset = least(tax, available);
// payable = tax - offset; carried = available - offset. The tax of a month with no input VAT is paid whole.
func offsetOf(tax, input, broughtForward decimal.Decimal) VATOffset {
	available := broughtForward.Add(input)
	off := decimal.Min(tax, available)
	return VATOffset{InputClaimed: input, CreditBroughtForward: broughtForward, Offset: off, Payable: tax.Sub(off), CreditCarriedForward: available.Sub(off)}
}

// InputClaim is the VAT of a bill line claimed on a return, or (Reversal) the claim of a bill voided after it was claimed,
// taken back with the sign changed.
type InputClaim struct {
	BillID                int64           `json:"bill_id"`
	LineNo                int             `json:"line_no"`
	BillNumber            string          `json:"bill_number"`
	SupplierInvoiceNumber string          `json:"supplier_invoice_number"`
	SupplierName          string          `json:"supplier_name"`
	BillDate              civil.Date      `json:"bill_date"`
	Amount                decimal.Decimal `json:"amount"`
	Reversal              bool            `json:"reversal"`
	reversesID            int64
}

// creditBroughtForward is the credit the month starts with: what the live return before it carried, else the opening credit.
func (s *Service) creditBroughtForward(ctx context.Context, tenantID, propertyID, taxID int64, start civil.Date) (decimal.Decimal, error) {
	q := s.q(ctx)
	prev, err := q.PreviousLiveReturn(ctx, taxfilingdb.PreviousLiveReturnParams{TenantID: tenantID, PropertyID: propertyID, TaxID: taxID, PeriodStart: start})
	if err == nil {
		return prev.CreditCarriedForward, nil
	}
	if !isNoRows(err) {
		return decimal.Zero, err
	}
	oc, err := q.OpeningCreditOfTax(ctx, taxfilingdb.OpeningCreditOfTaxParams{TenantID: tenantID, PropertyID: propertyID, TaxID: taxID})
	if isNoRows(err) {
		return decimal.Zero, nil
	}
	return oc.Amount, err
}

// inputClaims is what a return of the month would claim: the creditable VAT of the bills dated up to the end of the month that no
// live return claims yet (so a late bill goes to the first open month), and the reversal of the claims of bills voided by then.
// Only the filing profile that claims the input VAT has any.
func (s *Service) inputClaims(ctx context.Context, tenantID, propertyID int64, prof Profile, end civil.Date) ([]InputClaim, error) {
	out := []InputClaim{}
	if !prof.ClaimsInputVAT {
		return out, nil
	}
	q := s.q(ctx)
	lines, err := q.ClaimableBillLines(ctx, taxfilingdb.ClaimableBillLinesParams{TenantID: tenantID, PropertyID: propertyID, ToDate: end})
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		out = append(out, InputClaim{BillID: l.BillID, LineNo: int(l.LineNo), BillNumber: l.BillNumber, SupplierInvoiceNumber: l.SupplierInvoiceNumber, SupplierName: l.SupplierName, BillDate: l.BillDate, Amount: l.VatAmount})
	}
	revs, err := q.ReversibleClaims(ctx, taxfilingdb.ReversibleClaimsParams{TenantID: tenantID, PropertyID: propertyID, TaxID: prof.TaxID, ToDate: end})
	if err != nil {
		return nil, err
	}
	for _, r := range revs {
		out = append(out, InputClaim{BillID: r.BillID, LineNo: int(r.LineNo), BillNumber: r.BillNumber, SupplierInvoiceNumber: r.SupplierInvoiceNumber, SupplierName: r.SupplierName, BillDate: r.BillDate,
			Amount: r.Amount.Neg(), Reversal: true, reversesID: r.ID})
	}
	return out, nil
}

func (s *Service) loadClaims(ctx context.Context, tenantID, propertyID, returnID int64) ([]InputClaim, error) {
	rows, err := s.q(ctx).ListReturnClaims(ctx, taxfilingdb.ListReturnClaimsParams{TenantID: tenantID, PropertyID: propertyID, ReturnID: returnID})
	if err != nil {
		return nil, err
	}
	out := make([]InputClaim, 0, len(rows))
	for _, r := range rows {
		out = append(out, InputClaim{BillID: r.BillID, LineNo: int(r.LineNo), BillNumber: r.BillNumber, SupplierInvoiceNumber: r.SupplierInvoiceNumber, SupplierName: r.SupplierName, BillDate: r.BillDate,
			Amount: r.Amount, Reversal: r.ReversesClaimID != nil})
	}
	return out, nil
}

// postOffset journals the offset of a return: the VAT collected is settled against the input VAT (Dr tax payable, Cr input VAT; the
// sides are swapped when the offset is negative, i.e. more input VAT was reversed than there is to claim).
func (s *Service) postOffset(ctx context.Context, po *accounting.Poster, tenantID, propertyID int64, prof Profile, w Worksheet, number string, filedOn civil.Date) (int64, error) {
	if err := po.CheckDate(ctx, filedOn, "filed_on"); err != nil {
		return 0, err
	}
	payable, _, err := s.payableAccount(ctx, tenantID, propertyID, prof)
	if err != nil {
		return 0, err
	}
	input, err := po.SystemAccount(ctx, accounting.KeyInputVAT)
	if err != nil {
		return 0, err
	}
	desc := "VAT offset " + number + " " + prof.TaxCode + " " + monthLabel(w.PeriodStart)
	dr, cr := payable, input
	amount := w.Offset
	if amount.IsNegative() {
		dr, cr, amount = input, payable, amount.Neg()
	}
	jid, _, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalTax, Date: filedOn, Description: desc, Reference: number, Lines: []accounting.SystemLine{
		{AccountID: dr, Debit: amount, Description: desc, SourceType: "TAX_OFFSET", SourceRef: number},
		{AccountID: cr, Credit: amount, Description: desc, SourceType: "TAX_OFFSET", SourceRef: number},
	}})
	return jid, err
}

// ---------------------------------------------------------------------------------------------------------------
// The opening credit

// OpeningCreditInput sets the VAT credit a hotel starts with, as of a date. It needs an approval.
type OpeningCreditInput struct {
	AsOf     civil.Date         `json:"as_of"`
	Amount   decimal.Decimal    `json:"amount"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// SetOpeningCredit records the credit of the VAT tax at the start (tax.manage plus an approval): journal Dr input VAT, Cr opening
// balance equity. Only while the tax has no live return, and once.
func (s *Service) SetOpeningCredit(ctx context.Context, propertyID, profileID int64, in OpeningCreditInput) (Profile, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxManage)
	if err != nil {
		return Profile{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Profile{}, err
	}
	var fields []apperr.FieldError
	switch {
	case !in.Amount.IsPositive():
		fields = append(fields, fieldErr("amount", "NOT_POSITIVE", "an amount above zero"))
	case !in.Amount.Equal(in.Amount.Round(prop.CurrencyDecimals)):
		fields = append(fields, fieldErr("amount", "TOO_PRECISE", "at most "+strconv.Itoa(int(prop.CurrencyDecimals))+" decimals"))
	}
	if in.AsOf.IsZero() {
		fields = append(fields, fieldErr("as_of", "REQUIRED", "the date the credit is as of"))
	}
	if len(fields) > 0 {
		return Profile{}, apperr.Invalid("the opening credit is invalid", fields...)
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Profile{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxProfiles, db.ForUpdate, propertyID, []int64{profileID}); err != nil {
			return errProfileNotFound()
		}
		prof, err := s.loadProfile(ctx, p.TenantID, propertyID, profileID)
		if err != nil {
			return err
		}
		if prof.TaxKind != "VAT" {
			return apperr.Invalid("the opening credit is invalid", fieldErr("tax_id", "NOT_VAT", "only a VAT tax has a credit"))
		}
		if in.AsOf.After(day.BusinessDate) {
			return apperr.Invalid("the opening credit is invalid", fieldErr("as_of", "IN_THE_FUTURE", "not after the current business date"))
		}
		q := s.q(ctx)
		if n, err := q.CountLiveReturnsOfTax(ctx, taxfilingdb.CountLiveReturnsOfTaxParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: prof.TaxID}); err != nil {
			return err
		} else if n > 0 {
			return apperr.Conflict("TAX_OPENING_CREDIT_LOCKED", "the opening credit is set before the first return is filed").WithContext("returns", n)
		}
		if _, err := q.OpeningCreditOfTax(ctx, taxfilingdb.OpeningCreditOfTaxParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: prof.TaxID}); err == nil {
			return apperr.Conflict("TAX_OPENING_CREDIT_EXISTS", "the tax has an opening credit already: void it first")
		} else if !isNoRows(err) {
			return err
		}
		if err := po.CheckDate(ctx, in.AsOf, "as_of"); err != nil {
			return err
		}
		input, err := po.SystemAccount(ctx, accounting.KeyInputVAT)
		if err != nil {
			return err
		}
		eq, err := q.AccountByCode(ctx, taxfilingdb.AccountByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: openingEquityCode})
		if err != nil || !eq.IsActive || !eq.IsPostable {
			return apperr.Conflict("OPENING_EQUITY_ACCOUNT_MISSING", "the opening balance equity account "+openingEquityCode+" is missing, inactive or does not take postings")
		}
		desc := "Opening VAT credit " + prof.TaxCode
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalTax, Date: in.AsOf, Description: desc, Reference: prof.TaxCode, Lines: []accounting.SystemLine{
			{AccountID: input, Debit: in.Amount, Description: desc, SourceType: "TAX_OPENING_CREDIT", SourceRef: prof.TaxCode},
			{AccountID: eq.ID, Credit: in.Amount, Description: desc, SourceType: "TAX_OPENING_CREDIT", SourceRef: prof.TaxCode},
		}})
		if err != nil {
			return err
		}
		by := approval.UserID()
		id, err := q.InsertOpeningCredit(ctx, taxfilingdb.InsertOpeningCreditParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: prof.TaxID, AsOf: in.AsOf, Amount: in.Amount, JournalID: jid, ApprovedBy: &by, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.opening_credit_set", "tax_opening_credit", id, nil,
			map[string]any{"tax": prof.TaxCode, "amount": in.Amount.String(), "as_of": in.AsOf.String(), "approved_by": by, "journal": jnum}))
	})
	if err != nil {
		return Profile{}, err
	}
	return s.loadProfile(ctx, p.TenantID, propertyID, profileID)
}

// VoidOpeningCredit voids the opening credit of a tax (tax.manage plus an approval), while the tax has no live return: its
// journal is reversed on the current business date.
func (s *Service) VoidOpeningCredit(ctx context.Context, propertyID, profileID int64, in VoidInput) (Profile, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxManage)
	if err != nil {
		return Profile{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Profile{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Profile{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxProfiles, db.ForUpdate, propertyID, []int64{profileID}); err != nil {
			return errProfileNotFound()
		}
		prof, err := s.loadProfile(ctx, p.TenantID, propertyID, profileID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		oc, err := q.OpeningCreditOfTax(ctx, taxfilingdb.OpeningCreditOfTaxParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: prof.TaxID})
		if isNoRows(err) {
			return apperr.NotFound("TAX_OPENING_CREDIT_NOT_FOUND", "the tax has no opening credit")
		}
		if err != nil {
			return err
		}
		if n, err := q.CountLiveReturnsOfTax(ctx, taxfilingdb.CountLiveReturnsOfTaxParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: prof.TaxID}); err != nil {
			return err
		} else if n > 0 {
			return apperr.Conflict("TAX_OPENING_CREDIT_LOCKED", "the opening credit is set before the first return is filed").WithContext("returns", n)
		}
		by := approval.UserID()
		rj, err := po.Reverse(ctx, oc.JournalID, day.BusinessDate, reason, by)
		if err != nil {
			return err
		}
		if err := q.VoidOpeningCredit(ctx, taxfilingdb.VoidOpeningCreditParams{TenantID: p.TenantID, PropertyID: propertyID, ID: oc.ID, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, VoidJournalID: &rj, ApprovedBy: &by}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.opening_credit_voided", "tax_opening_credit", oc.ID,
			map[string]any{"amount": oc.Amount.String()}, map[string]any{"status": "VOIDED", "reason": reason, "approved_by": by}))
	})
	if err != nil {
		return Profile{}, err
	}
	return s.loadProfile(ctx, p.TenantID, propertyID, profileID)
}
