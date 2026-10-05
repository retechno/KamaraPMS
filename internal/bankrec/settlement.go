package bankrec

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/bankrec/bankrecdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling"
)

// Settlement account keys: the clearing accounts that guest payments by card and by e-wallet are posted to until the
// acquirer pays them out, net of its commission.
const (
	KeyCard         = "CARD"
	KeyOtherPayment = "OTHER_PAYMENT"
)

func validKey(k string) bool { return k == KeyCard || k == KeyOtherPayment }

// SettlementLines lists the lines of the card or e-wallet clearing account, up to the end of the statement, that no
// settlement has settled yet: the payments the acquirer has not paid out (bank.view). `key` is CARD or OTHER_PAYMENT.
func (s *Service) SettlementLines(ctx context.Context, propertyID, statementID int64, key string) ([]UnclearedLine, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return nil, err
	}
	if !validKey(key) {
		return nil, apperr.Invalid("the request is invalid", fieldErr("account_key", "INVALID", "CARD or OTHER_PAYMENT"))
	}
	st, err := s.statementRow(ctx, p.TenantID, propertyID, statementID)
	if err != nil {
		return nil, err
	}
	accountID, err := s.q(ctx).MapAccount(ctx, bankrecdb.MapAccountParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: key})
	if isNoRows(err) {
		return nil, apperr.Conflict("ACCOUNT_MAP_INCOMPLETE", "the system account "+key+" is not mapped")
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).SettlementCandidates(ctx, bankrecdb.SettlementCandidatesParams{TenantID: p.TenantID, PropertyID: propertyID, AccountID: accountID, ToDate: st.PeriodTo, RowLimit: maxUncleared})
	if err != nil {
		return nil, err
	}
	out := make([]UnclearedLine, 0, len(rows))
	for _, r := range rows {
		if r.Amount.IsZero() {
			continue
		}
		desc := deref(r.Description)
		if desc == "" {
			desc = r.JournalDescription
		}
		out = append(out, UnclearedLine{
			JournalLineID: r.ID, Date: r.JournalDate, JournalID: r.JournalID, JournalNumber: r.JournalNumber, JournalType: r.JournalType, Description: desc, Reference: deref(r.SourceRef),
			Amount: r.Amount, Remaining: r.Amount,
		})
	}
	return out, nil
}

// Settle posts a card or e-wallet settlement from a statement line (bank.reconcile): the acquirer paid the amount of the
// line for the payment lines chosen and kept the difference. A BANK journal dated the day of the line debits the bank
// account with what was paid and the fee account with the commission, and credits the clearing account with the
// payments; the lines are recorded as settled (each once) and the statement line is matched with the bank side.
func (s *Service) Settle(ctx context.Context, propertyID, statementID, lineID int64, in SettleInput) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return StatementDetail{}, err
	}
	var fields []apperr.FieldError
	if !validKey(in.AccountKey) {
		fields = append(fields, fieldErr("account_key", "INVALID", "CARD or OTHER_PAYMENT"))
	}
	if n := len(in.JournalLineIDs); n < 1 || n > maxClearBatch {
		fields = append(fields, fieldErr("journal_line_ids", "INVALID_COUNT", "between 1 and 200 payment lines"))
	}
	if len([]rune(in.Description)) > 300 {
		fields = append(fields, fieldErr("description", "TOO_LONG", "at most 300 characters"))
	}
	if len(fields) > 0 {
		return StatementDetail{}, apperr.Invalid("the settlement is invalid", fields...)
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID) // accounting settings (46) before the bank account (48)
		if err != nil {
			return err
		}
		st, ba, err := s.openForWork(ctx, p.TenantID, propertyID, statementID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		line, err := q.GetStatementLine(ctx, bankrecdb.GetStatementLineParams{TenantID: p.TenantID, PropertyID: propertyID, StatementID: statementID, ID: lineID})
		if isNoRows(err) {
			return apperr.NotFound("STATEMENT_LINE_NOT_FOUND", "the line does not exist in this statement")
		}
		if err != nil {
			return err
		}
		done, err := q.LineCleared(ctx, bankrecdb.LineClearedParams{TenantID: p.TenantID, PropertyID: propertyID, StatementLineID: &lineID})
		if err != nil {
			return err
		}
		if !done.IsZero() {
			return apperr.Conflict("LINE_ALREADY_MATCHED", "the line has something matched already: unmatch it first")
		}
		if !line.Amount.IsPositive() {
			return apperr.Invalid("the settlement is invalid", fieldErr("statement_line_id", "NOT_MONEY_IN", "a settlement is money paid into the bank"))
		}
		clearing, err := po.SystemAccount(ctx, in.AccountKey)
		if err != nil {
			return err
		}
		if clearing == ba.AccountID {
			return apperr.Invalid("the settlement is invalid", fieldErr("account_key", "SAME_ACCOUNT", "the bank account is the clearing account itself"))
		}
		items, gross, err := s.settlementItems(ctx, q, p.TenantID, propertyID, st.PeriodTo, clearing, in.AccountKey, in.JournalLineIDs)
		if err != nil {
			return err
		}
		net := line.Amount
		fee := gross.Sub(net)
		switch {
		case !gross.IsPositive():
			return apperr.Invalid("the settlement is invalid", fieldErr("journal_line_ids", "NOT_POSITIVE", "the payments chosen add up to "+gross.String()))
		case fee.IsNegative():
			return apperr.Invalid("the settlement is invalid", fieldErr("journal_line_ids", "NET_EXCEEDS_GROSS", "the bank paid "+net.String()+", more than the "+gross.String()+" of the payments chosen"))
		}
		// What the bank kept is the MDR and the VAT on it. The payments settled say what they expected; the system proposes the split, the user may give the final VAT, and the final MDR is the rest.
		prop, err := s.days.GetProperty(ctx, propertyID)
		if err != nil {
			return err
		}
		exp, err := s.expectationOf(ctx, p.TenantID, propertyID, in.JournalLineIDs)
		if err != nil {
			return err
		}
		proposed := proposeVAT(fee, exp, prop.CurrencyDecimals)
		vat, err := finalVAT(in.VATAmount, proposed, fee, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		// The VAT is treated as the property treats it on the date of the line, and that is written to the settlement for good.
		var treatment string
		var inputVAT int64
		if vat.IsPositive() {
			if treatment, err = s.vatTreatmentOn(ctx, p.TenantID, propertyID, line.LineDate); err != nil {
				return err
			}
			if treatment != taxfiling.InputVATExpense {
				if inputVAT, err = po.SystemAccount(ctx, accounting.KeyInputVAT); err != nil {
					return err
				}
			}
		}
		mdr := fee.Sub(vat)
		commission := mdr // an EXPENSE VAT is part of the cost: it is booked with the commission
		if treatment == taxfiling.InputVATExpense {
			commission = fee
		}
		if commission.IsPositive() {
			if in.FeeAccountID == 0 {
				return apperr.Invalid("the settlement is invalid", fieldErr("fee_account_id", "REQUIRED", "choose the account for the commission of "+commission.String()))
			}
			if in.FeeAccountID == ba.AccountID || in.FeeAccountID == clearing {
				return apperr.Invalid("the settlement is invalid", fieldErr("fee_account_id", "SAME_ACCOUNT", "the commission is not the bank or the clearing account"))
			}
			if err := po.CheckAccount(ctx, in.FeeAccountID, "fee_account_id"); err != nil {
				return err
			}
			dept, err := po.ResolveDepartment(ctx, in.FeeAccountID, in.DepartmentID, "department_id")
			if err != nil {
				return err
			}
			in.DepartmentID = dept
		}
		desc := strings.TrimSpace(in.Description)
		if desc == "" {
			desc = deref(line.Description)
		}
		if desc == "" {
			desc = "Settlement of " + strings.ToLower(strings.ReplaceAll(in.AccountKey, "_", " ")) + " payments"
		}
		ref := deref(line.Reference)
		if ref == "" {
			ref = fmt.Sprintf("ST%d-%d", statementID, line.LineNo)
		}
		jlines := []accounting.SystemLine{{AccountID: ba.AccountID, Debit: net, Description: desc, SourceType: "SETTLEMENT", SourceRef: ref}}
		if commission.IsPositive() {
			jlines = append(jlines, accounting.SystemLine{AccountID: in.FeeAccountID, DepartmentID: in.DepartmentID, Debit: commission, Description: "Commission: " + desc, SourceType: "SETTLEMENT", SourceRef: ref})
		}
		if vat.IsPositive() && treatment != taxfiling.InputVATExpense {
			jlines = append(jlines, accounting.SystemLine{AccountID: inputVAT, Debit: vat, Description: "VAT on the commission: " + desc, SourceType: "SETTLEMENT", SourceRef: ref})
		}
		jlines = append(jlines, accounting.SystemLine{AccountID: clearing, Credit: gross, Description: desc, SourceType: "SETTLEMENT", SourceRef: ref})
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalBank, Date: line.LineDate, Description: "Bank: " + desc, Reference: ref, Lines: jlines})
		if err != nil {
			return err
		}
		bankLine, err := q.JournalLineOfAccount(ctx, bankrecdb.JournalLineOfAccountParams{TenantID: p.TenantID, PropertyID: propertyID, JournalID: jid, AccountID: ba.AccountID})
		if err != nil {
			return err
		}
		creditLine, err := q.JournalLineOfAccount(ctx, bankrecdb.JournalLineOfAccountParams{TenantID: p.TenantID, PropertyID: propertyID, JournalID: jid, AccountID: clearing})
		if err != nil {
			return err
		}
		var expectedMDR, expectedVAT *decimal.Decimal // what the payments expected, when every one of them kept a snapshot
		if exp.known {
			m, v := exp.mdr, exp.vat
			expectedMDR, expectedVAT = &m, &v
		}
		var frozen *string // the treatment of the VAT, written once
		if vat.IsPositive() {
			frozen = &treatment
		}
		sid, err := q.InsertSettlement(ctx, bankrecdb.InsertSettlementParams{
			TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: ba.ID, AccountKey: in.AccountKey, JournalID: jid, Gross: gross, Net: net, Fee: fee, VatAmount: vat, VatTreatment: frozen,
			ExpectedMdr: expectedMDR, ExpectedVat: expectedVAT, ProposedVat: proposed, MdrRate: exp.mdrRate, VatRate: exp.vatRate, PaymentsWithoutVatRate: int32(exp.withoutVATRate), //nolint:gosec // G115: a count of payment lines
			Reference: nullable(ref), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for _, it := range items {
			if err := q.InsertSettlementItem(ctx, bankrecdb.InsertSettlementItemParams{TenantID: p.TenantID, PropertyID: propertyID, SettlementID: sid, SettledLineID: it.ID, SettlingLineID: creditLine, Amount: it.Amount}); err != nil {
				return err
			}
		}
		if err := q.InsertClearing(ctx, bankrecdb.InsertClearingParams{
			TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: st.BankAccountID, StatementID: statementID, StatementLineID: &lineID, JournalLineID: bankLine,
			Amount: net, Now: s.clock.Now(), ActorID: p.ActorID(),
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.settled", "bank_statement", statementID, nil,
			map[string]any{"line": line.LineNo, "key": in.AccountKey, "gross": gross.String(), "net": net.String(), "fee": fee.String(), "mdr": mdr.String(), "vat": vat.String(), "vat_treatment": treatment,
				"proposed_vat": proposed.String(), "expected_mdr": expectedMDR, "expected_vat": expectedVAT, "payments": len(items), "journal": jnum}))
	})
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, statementID)
}

// settlementItems reads the payment lines a settlement settles and adds them up: each is on the clearing account of the method, not after the end of the statement,
// and not settled yet. The same checks serve the preview and the settlement.
func (s *Service) settlementItems(ctx context.Context, q *bankrecdb.Queries, tenantID, propertyID int64, periodTo civil.Date, clearing int64, accountKey string, ids []int64) ([]bankrecdb.GetJournalLineRow, decimal.Decimal, error) {
	gross := decimal.Zero
	seen := map[int64]bool{}
	items := make([]bankrecdb.GetJournalLineRow, 0, len(ids))
	for i, id := range ids {
		at := fmt.Sprintf("journal_line_ids[%d]", i)
		if seen[id] {
			return nil, gross, apperr.Invalid("the settlement is invalid", fieldErr(at, "DUPLICATE", "the payment line is listed twice"))
		}
		seen[id] = true
		jl, err := q.GetJournalLine(ctx, bankrecdb.GetJournalLineParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
		switch {
		case isNoRows(err):
			return nil, gross, apperr.Invalid("the settlement is invalid", fieldErr(at, "NOT_FOUND", "no such journal line in this property"))
		case err != nil:
			return nil, gross, err
		case jl.AccountID != clearing:
			return nil, gross, apperr.Invalid("the settlement is invalid", fieldErr(at, "NOT_CLEARING_ACCOUNT", "the journal line is not on the "+strings.ToLower(strings.ReplaceAll(accountKey, "_", " "))+" account"))
		case jl.JournalDate.After(periodTo):
			return nil, gross, apperr.Invalid("the settlement is invalid", fieldErr(at, "AFTER_STATEMENT", "the journal line is dated after the end of the statement"))
		}
		if used, err := q.IsSettledOrSettling(ctx, id); err != nil {
			return nil, gross, err
		} else if used {
			return nil, gross, apperr.Conflict("ALREADY_SETTLED", "the payment line is settled already").WithContext("journal_line_id", id)
		}
		gross = gross.Add(jl.Amount)
		items = append(items, jl)
	}
	return items, gross, nil
}
