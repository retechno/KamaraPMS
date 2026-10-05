package taxfiling

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling/taxfilingdb"
	"kamarapms/internal/tenancy"
)

func toPayment(r taxfilingdb.ListTaxPaymentsRow) Payment {
	return Payment{
		ID: r.ID, Number: r.PaymentNumber, ReturnID: r.ReturnID, ReturnNumber: r.ReturnNumber, TaxCode: r.TaxCode, TaxName: r.TaxName, PeriodStart: r.PeriodStart, PaymentDate: r.PaymentDate,
		Amount: r.Amount, Penalty: r.Penalty, Total: r.Amount.Add(r.Penalty), PaymentMethod: r.PaymentMethod, ReferenceNumber: deref(r.ReferenceNumber), Remarks: deref(r.Remarks),
		Status: r.Status, JournalID: r.JournalID, JournalNumber: r.JournalNumber, VoidJournalID: r.VoidJournalID, VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason), CreatedAt: r.CreatedAt,
	}
}

// Payments lists the payments to the tax authority, newest first (tax.view).
func (s *Service) Payments(ctx context.Context, propertyID int64, f PaymentFilter) ([]Payment, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return nil, err
	}
	return s.listPayments(ctx, p.TenantID, propertyID, nil, f.ReturnID, f.Status, f.Limit)
}

func (s *Service) listPayments(ctx context.Context, tenantID, propertyID int64, id, returnID *int64, status string, limit int) ([]Payment, error) {
	if limit < 1 || limit > maxPage {
		limit = maxPage
	}
	rows, err := s.q(ctx).ListTaxPayments(ctx, taxfilingdb.ListTaxPaymentsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, ReturnID: returnID, Status: nullable(status), RowLimit: int32(limit)}) //nolint:gosec // G115: at most 200
	if err != nil {
		return nil, err
	}
	out := make([]Payment, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPayment(r))
	}
	return out, nil
}

func (s *Service) loadPayment(ctx context.Context, tenantID, propertyID, id int64) (Payment, error) {
	list, err := s.listPayments(ctx, tenantID, propertyID, &id, nil, "", 1)
	if err != nil {
		return Payment{}, err
	}
	if len(list) == 0 {
		return Payment{}, errPaymentNotFound()
	}
	return list[0], nil
}

// GetPayment is one payment to the tax authority (tax.view).
func (s *Service) GetPayment(ctx context.Context, propertyID, id int64) (Payment, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return Payment{}, err
	}
	return s.loadPayment(ctx, p.TenantID, propertyID, id)
}

// payableAccount is the account of the books the tax is owed on: the account of the tax when it is an active liability
// account that takes postings, else the TAX_PAYABLE system account, which is where the day close put it.
func (s *Service) payableAccount(ctx context.Context, tenantID, propertyID int64, prof Profile) (int64, string, error) {
	q := s.q(ctx)
	if prof.GLAccountCode != "" {
		a, err := q.AccountByCode(ctx, taxfilingdb.AccountByCodeParams{TenantID: tenantID, PropertyID: propertyID, Code: prof.GLAccountCode})
		if err == nil && a.IsActive && a.IsPostable && a.AccountType == accounting.TypeLiability {
			return a.ID, prof.GLAccountCode, nil
		}
		if err != nil && !isNoRows(err) {
			return 0, "", err
		}
	}
	code, err := q.MapAccountCode(ctx, taxfilingdb.MapAccountCodeParams{TenantID: tenantID, PropertyID: propertyID, MapKey: accounting.KeyTaxPayable})
	if err != nil {
		return 0, "", err
	}
	a, err := q.AccountByCode(ctx, taxfilingdb.AccountByCodeParams{TenantID: tenantID, PropertyID: propertyID, Code: code})
	return a.ID, code, err
}

func validatePayment(in PayInput, decimals int32) []apperr.FieldError {
	var fields []apperr.FieldError
	if in.PaymentDate.IsZero() {
		fields = append(fields, fieldErr("payment_date", "REQUIRED", "the date of the payment"))
	}
	if _, ok := methodAccount[in.PaymentMethod]; !ok {
		fields = append(fields, fieldErr("payment_method", "INVALID_METHOD", "CASH, BANK_TRANSFER or OTHER"))
	}
	switch {
	case !in.Amount.IsPositive():
		fields = append(fields, fieldErr("amount", "NOT_POSITIVE", "an amount above zero"))
	case !in.Amount.Equal(in.Amount.Round(decimals)):
		fields = append(fields, fieldErr("amount", "TOO_PRECISE", fmt.Sprintf("at most %d decimals", decimals)))
	}
	switch {
	case in.Penalty.IsNegative():
		fields = append(fields, fieldErr("penalty", "NEGATIVE", "the penalty is not negative"))
	case !in.Penalty.Equal(in.Penalty.Round(decimals)):
		fields = append(fields, fieldErr("penalty", "TOO_PRECISE", fmt.Sprintf("at most %d decimals", decimals)))
	case in.Penalty.IsPositive() && in.PenaltyAccountID < 1:
		fields = append(fields, fieldErr("penalty_account_id", "REQUIRED", "the expense account of the penalty"))
	}
	if len([]rune(in.ReferenceNumber)) > 100 {
		fields = append(fields, fieldErr("reference_number", "TOO_LONG", "at most 100 characters"))
	}
	if len([]rune(in.Remarks)) > 500 {
		fields = append(fields, fieldErr("remarks", "TOO_LONG", "at most 500 characters"))
	}
	return fields
}

// PayReturn pays the tax authority against a return (tax.file): up to what is still owed on it. The journal debits the
// tax payable account (and the penalty account) against the cash, bank or other-payment account of the method. The
// Idempotency-Key makes a retry return the first payment.
func (s *Service) PayReturn(ctx context.Context, propertyID, returnID int64, in PayInput, key string) (Payment, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxFile)
	if err != nil {
		return Payment{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Payment{}, err
	}
	if fields := validatePayment(in, prop.CurrencyDecimals); len(fields) > 0 {
		return Payment{}, apperr.Invalid("the payment is invalid", fields...)
	}
	var id int64
	err = retryOnDuplicate(key, func() error { return s.payReturn(ctx, p, propertyID, returnID, in, key, &id) })
	if err != nil {
		return Payment{}, err
	}
	return s.loadPayment(ctx, p.TenantID, propertyID, id)
}

func (s *Service) payReturn(ctx context.Context, p auth.Principal, propertyID, returnID int64, in PayInput, key string, out *int64) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID) // accounting settings (46) before the tax profile (49)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		profID, err := q.ProfileOfReturn(ctx, taxfilingdb.ProfileOfReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: returnID})
		if isNoRows(err) {
			return errReturnNotFound()
		}
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxProfiles, db.ForUpdate, propertyID, []int64{profID}); err != nil {
			return errProfileNotFound()
		}
		if key != "" {
			if prev, err := q.FindTaxPaymentByKey(ctx, taxfilingdb.FindTaxPaymentByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
				*out = prev
				return nil
			} else if !isNoRows(err) {
				return err
			}
		}
		ret, err := s.loadReturn(ctx, p.TenantID, propertyID, returnID, day.BusinessDate)
		if err != nil {
			return err
		}
		if ret.Status != ReturnFiled {
			return apperr.Conflict("TAX_RETURN_VOIDED", "the return is voided")
		}
		if in.Amount.GreaterThan(ret.Outstanding) {
			return apperr.Invalid("the payment is invalid", fieldErr("amount", "EXCEEDS_OUTSTANDING", "more than the "+ret.Outstanding.String()+" still owed on the return")).
				WithContext("outstanding", ret.Outstanding.String())
		}
		prof, err := s.loadProfile(ctx, p.TenantID, propertyID, profID)
		if err != nil {
			return err
		}
		if err := po.CheckDate(ctx, in.PaymentDate, "payment_date"); err != nil {
			return err
		}
		payable, payableCode, err := s.payableAccount(ctx, p.TenantID, propertyID, prof)
		if err != nil {
			return err
		}
		from, err := po.SystemAccount(ctx, methodAccount[in.PaymentMethod])
		if err != nil {
			return err
		}
		if in.Penalty.IsPositive() {
			if in.PenaltyAccountID == payable || in.PenaltyAccountID == from {
				return apperr.Invalid("the payment is invalid", fieldErr("penalty_account_id", "SAME_ACCOUNT", "the penalty is an expense, not the tax payable or the paying account"))
			}
			if err := po.CheckAccount(ctx, in.PenaltyAccountID, "penalty_account_id"); err != nil {
				return err
			}
			if err := po.CheckDepartment(ctx, in.DepartmentID, "department_id"); err != nil {
				return err
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqTaxPayment)
		if err != nil {
			return err
		}
		desc := "Tax payment " + number + " " + ret.TaxCode + " " + monthLabel(ret.PeriodStart)
		lines := []accounting.SystemLine{{AccountID: payable, Debit: in.Amount, Description: desc, SourceType: "TAX_PAYMENT", SourceRef: number}}
		total := in.Amount
		if in.Penalty.IsPositive() {
			lines = append(lines, accounting.SystemLine{AccountID: in.PenaltyAccountID, DepartmentID: in.DepartmentID, Debit: in.Penalty, Description: "Penalty: " + desc, SourceType: "TAX_PAYMENT", SourceRef: number})
			total = total.Add(in.Penalty)
		}
		lines = append(lines, accounting.SystemLine{AccountID: from, Credit: total, Description: desc, SourceType: "TAX_PAYMENT", SourceRef: number})
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalTax, Date: in.PaymentDate, Description: desc, Reference: number, Lines: lines})
		if err != nil {
			return err
		}
		id, err := q.InsertTaxPayment(ctx, taxfilingdb.InsertTaxPaymentParams{
			TenantID: p.TenantID, PropertyID: propertyID, PaymentNumber: number, ReturnID: returnID, PaymentDate: in.PaymentDate, Amount: in.Amount, Penalty: in.Penalty,
			PaymentMethod: in.PaymentMethod, ReferenceNumber: nullable(in.ReferenceNumber), Remarks: nullable(in.Remarks), JournalID: jid, IdempotencyKey: nullable(key), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		*out = id
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.payment_posted", "tax_payment", id, nil,
			map[string]any{"payment_number": number, "return": ret.Number, "tax": ret.TaxCode, "amount": in.Amount.String(), "penalty": in.Penalty.String(), "method": in.PaymentMethod, "account": payableCode, "journal": jnum}))
	})
}

// VoidPayment voids a payment to the tax authority (tax.file plus an approval): its journal is reversed on the current
// business date and the amount is owed on the return again.
func (s *Service) VoidPayment(ctx context.Context, propertyID, id int64, in VoidInput) (Payment, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxFile)
	if err != nil {
		return Payment{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Payment{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Payment{}, err
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
		q := s.q(ctx)
		returnID, err := q.ReturnOfPayment(ctx, taxfilingdb.ReturnOfPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if isNoRows(err) {
			return errPaymentNotFound()
		}
		if err != nil {
			return err
		}
		profID, err := q.ProfileOfReturn(ctx, taxfilingdb.ProfileOfReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: returnID})
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxProfiles, db.ForUpdate, propertyID, []int64{profID}); err != nil {
			return errProfileNotFound()
		}
		pay, err := s.loadPayment(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if pay.Status != "POSTED" {
			return apperr.Conflict("TAX_PAYMENT_ALREADY_VOIDED", "the payment is voided already")
		}
		by := approval.UserID()
		rj, err := po.Reverse(ctx, pay.JournalID, day.BusinessDate, reason, by)
		if err != nil {
			return err
		}
		if err := q.VoidTaxPayment(ctx, taxfilingdb.VoidTaxPaymentParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, VoidJournalID: &rj, ApprovedBy: &by,
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.payment_voided", "tax_payment", id,
			map[string]any{"status": "POSTED"}, map[string]any{"status": "VOIDED", "reason": reason, "approved_by": by, "payment_number": pay.Number}))
	})
	if err != nil {
		return Payment{}, err
	}
	return s.loadPayment(ctx, p.TenantID, propertyID, id)
}

// Liability is what is owed to the tax authority as of a business date (tax.view): per tax, the tax collected on folios,
// what has been filed and paid, what is not on a return yet, and what is overdue; and per tax payable account of the
// books, the balance against what the taxes using it say is owed. A past date reproduces what was owed then.
func (s *Service) Liability(ctx context.Context, propertyID int64, asOf *civil.Date) (Liability, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return Liability{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Liability{}, err
	}
	at := day.BusinessDate
	if asOf != nil {
		if asOf.After(day.BusinessDate) {
			return Liability{}, apperr.Invalid("the date is invalid", fieldErr("as_of", "IN_THE_FUTURE", "not after the current business date"))
		}
		at = *asOf
	}
	profiles, err := s.listProfiles(ctx, p.TenantID, propertyID, nil, nil)
	if err != nil {
		return Liability{}, err
	}
	q := s.q(ctx)
	collectedRows, err := q.CollectedToDate(ctx, taxfilingdb.CollectedToDateParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return Liability{}, err
	}
	collected := map[int64]decimal.Decimal{}
	for _, r := range collectedRows {
		if r.TaxID != nil {
			collected[*r.TaxID] = r.Tax
		}
	}
	creditRows, err := q.CreditNoteTaxToDate(ctx, taxfilingdb.CreditNoteTaxToDateParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return Liability{}, err
	}
	for _, r := range creditRows {
		if r.TaxID != nil {
			collected[*r.TaxID] = collected[*r.TaxID].Sub(r.Tax) // the credit notes took it off
		}
	}
	filedRows, err := q.FiledAndPaidToDate(ctx, taxfilingdb.FiledAndPaidToDateParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return Liability{}, err
	}
	type fp struct {
		filed, offset, paid decimal.Decimal
		n                   int
	}
	filed := map[int64]fp{}
	for _, r := range filedRows {
		filed[r.TaxID] = fp{r.Filed, r.Offsets, r.Paid, int(r.Returns)}
	}
	paidBy, err := q.PaidByReturnToDate(ctx, taxfilingdb.PaidByReturnToDateParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return Liability{}, err
	}
	paidOf := map[int64]decimal.Decimal{}
	for _, r := range paidBy {
		paidOf[r.ReturnID] = r.Paid
	}
	returns, err := s.listReturns(ctx, p.TenantID, propertyID, nil, nil, ReturnFiled, 0, at)
	if err != nil {
		return Liability{}, err
	}
	start, startErr := q.AccountingStart(ctx, taxfilingdb.AccountingStartParams{TenantID: p.TenantID, PropertyID: propertyID})
	out := Liability{AsOf: at, Taxes: []LiabilityLine{}, Accounts: []LiabilityAccount{}}
	byAccount := map[string]decimal.Decimal{}
	var accountOrder []string
	for _, prof := range profiles {
		_, code, err := s.payableAccount(ctx, p.TenantID, propertyID, prof)
		if err != nil {
			return Liability{}, err
		}
		f := filed[prof.TaxID]
		line := LiabilityLine{
			TaxID: prof.TaxID, TaxCode: prof.TaxCode, TaxName: prof.TaxName, Authority: prof.Authority, AccountCode: code, Collected: collected[prof.TaxID], Filed: f.filed,
			Paid: f.paid, ReturnsFiled: f.n, RegistrationNum: prof.RegistrationNumber,
		}
		line.Offset = f.offset
		line.Unfiled = line.Collected.Sub(line.Filed)
		line.Owed = line.Collected.Sub(line.Offset).Sub(line.Paid)
		line.CreditAvailable = prof.OpeningCredit
		var latest civil.Date
		have := map[civil.Date]bool{}
		for _, r := range returns {
			if r.TaxID != prof.TaxID || r.FiledOn.After(at) {
				continue
			}
			have[r.PeriodStart] = true
			if r.PeriodStart.After(latest) {
				latest, line.CreditAvailable = r.PeriodStart, r.CreditCarriedForward
			}
			if out := r.Payable.Sub(paidOf[r.ID]); r.DueDate.Before(at) && out.IsPositive() {
				line.OverdueUnpaid = line.OverdueUnpaid.Add(out)
			}
		}
		if startErr == nil {
			for m := periodStart(start); periodEnd(m).Before(at); m = periodEnd(m).AddDays(1) {
				if dueDate(m, prof.DueDay).Before(at) && !have[m] {
					line.OverdueUnfiled++
				}
			}
		}
		out.Taxes = append(out.Taxes, line)
		out.Owed = out.Owed.Add(line.Owed)
		if _, ok := byAccount[code]; !ok {
			accountOrder = append(accountOrder, code)
		}
		byAccount[code] = byAccount[code].Add(line.Owed)
	}
	for _, code := range accountOrder {
		books, err := q.AccountCreditBalance(ctx, taxfilingdb.AccountCreditBalanceParams{TenantID: p.TenantID, PropertyID: propertyID, Code: code, AsOf: at})
		if err != nil {
			return Liability{}, err
		}
		out.Accounts = append(out.Accounts, LiabilityAccount{AccountCode: code, Books: books, Owed: byAccount[code], Difference: books.Sub(byAccount[code])})
	}
	return out, nil
}
