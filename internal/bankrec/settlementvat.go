package bankrec

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/bankrec/bankrecdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/taxfiling"
)

// The VAT in the deduction of a card settlement (design: docs/architecture/15-card-fee-vat.md). What the bank kept (gross - net) is the commission (MDR) and the VAT on it. The payments settled
// say what each was expected to cost; from them the system proposes a split, the user may change the VAT, and the final MDR is what is left: MDR = deduction - VAT, so the two always add up to
// the deduction. The settlement keeps the final figures, the expectation, the proposal and the VAT treatment of the day, and none of them is computed again afterwards.

// expectation is what the payments of a settlement expected to cost, from their snapshots.
type expectation struct {
	mdr, vat       decimal.Decimal
	known          bool             // every payment kept an MDR snapshot
	withoutVATRate int              // payments with an MDR snapshot but no VAT rate: they expect no VAT, nothing is guessed
	withoutRate    int              // payments with no snapshot at all
	mdrRate        *decimal.Decimal // the MDR rate when every payment shares one, else nil
	vatRate        *decimal.Decimal // the VAT rate when every payment has one and shares it, else nil
}

// expectationOf adds up the MDR and the VAT that the payments of some journal lines expected.
func (s *Service) expectationOf(ctx context.Context, tenantID, propertyID int64, lineIDs []int64) (expectation, error) {
	e := expectation{mdr: decimal.Zero, vat: decimal.Zero}
	refs, err := s.q(ctx).JournalLineRefs(ctx, bankrecdb.JournalLineRefsParams{TenantID: tenantID, PropertyID: propertyID, Ids: lineIDs})
	if err != nil {
		return e, err
	}
	numbers := make([]string, 0, len(refs))
	for _, r := range refs {
		numbers = append(numbers, r.SourceRef)
	}
	snaps, err := s.snapshots(ctx, tenantID, propertyID, numbers)
	if err != nil {
		return e, err
	}
	e.known = len(refs) > 0
	sameMDR, sameVAT, firstMDR, firstVAT := true, true, true, true
	for _, r := range refs {
		sn, ok := snaps[r.SourceRef]
		if !ok {
			e.known, e.withoutRate = false, e.withoutRate+1
			sameMDR, sameVAT = false, false
			continue
		}
		e.mdr = e.mdr.Add(sn.fee)
		if e.mdrRate == nil {
			rate := sn.rate
			e.mdrRate = &rate
		} else if !e.mdrRate.Equal(sn.rate) {
			sameMDR = false
		}
		firstMDR = false
		if sn.vatRate == nil || sn.vat == nil {
			e.withoutVATRate++
			sameVAT = false
			continue
		}
		e.vat = e.vat.Add(*sn.vat)
		if e.vatRate == nil {
			rate := *sn.vatRate
			e.vatRate = &rate
		} else if !e.vatRate.Equal(*sn.vatRate) {
			sameVAT = false
		}
		firstVAT = false
	}
	if !sameMDR || firstMDR {
		e.mdrRate = nil
	}
	if !sameVAT || firstVAT {
		e.vatRate = nil
	}
	return e, nil
}

// proposeVAT is the VAT the system proposes out of the deduction: when every payment has the same VAT rate v, the deduction is MDR plus v percent of it, so VAT = D x v / (100 + v);
// with different rates, or payments without a rate, the deduction is split in proportion to the expected VAT and the expected MDR; with nothing expected it is 0. Rounded once to
// the decimals of the currency, half away from zero, and never above the deduction.
func proposeVAT(deduction decimal.Decimal, e expectation, decimals int32) decimal.Decimal {
	if !deduction.IsPositive() {
		return decimal.Zero
	}
	var vat decimal.Decimal
	switch {
	case e.vatRate != nil && e.withoutVATRate == 0 && e.withoutRate == 0:
		vat = deduction.Mul(*e.vatRate).DivRound(decimal.NewFromInt(100).Add(*e.vatRate), decimals)
	case e.mdr.Add(e.vat).IsPositive():
		vat = deduction.Mul(e.vat).DivRound(e.mdr.Add(e.vat), decimals)
	default:
		return decimal.Zero
	}
	return decimal.Min(vat, deduction)
}

// vatTreatmentOn is how the input VAT of the property is treated on a date: the status of the property on that day, as for a supplier bill.
func (s *Service) vatTreatmentOn(ctx context.Context, tenantID, propertyID int64, d civil.Date) (string, error) {
	status, err := s.tax.SettingsOnDate(ctx, tenantID, propertyID, d)
	if err != nil {
		return "", err
	}
	return status.InputVATTreatment, nil
}

// finalVAT reads the VAT the user gave, or takes the proposal; the VAT is between 0 and the deduction.
func finalVAT(in *string, proposed, deduction decimal.Decimal, decimals int32) (decimal.Decimal, error) {
	if in == nil {
		return proposed, nil
	}
	v, err := money.Parse(*in)
	switch {
	case err != nil:
		return decimal.Zero, apperr.Invalid("the settlement is invalid", fieldErr("vat_amount", "INVALID_AMOUNT", "an amount"))
	case v.IsNegative():
		return decimal.Zero, apperr.Invalid("the settlement is invalid", fieldErr("vat_amount", "NEGATIVE", "zero or more"))
	case !v.Equal(v.Round(decimals)):
		return decimal.Zero, apperr.Invalid("the settlement is invalid", fieldErr("vat_amount", "TOO_PRECISE", "at most "+itoa(int(decimals))+" decimals"))
	case v.GreaterThan(deduction):
		return decimal.Zero, apperr.Invalid("the settlement is invalid", fieldErr("vat_amount", "VAT_EXCEEDS_DEDUCTION", "the VAT is part of the "+deduction.String()+" the bank kept"))
	}
	return v, nil
}

// PreviewAccount is the account the input VAT of a settlement would be booked to.
type PreviewAccount struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// SettlementPreview is the split of the deduction of a settlement the system would propose; it writes nothing.
type SettlementPreview struct {
	StatementLineID int64            `json:"statement_line_id"`
	AccountKey      string           `json:"account_key"`
	Gross           decimal.Decimal  `json:"gross"`
	Net             decimal.Decimal  `json:"net"`
	Deduction       decimal.Decimal  `json:"deduction"`
	ExpectedMDR     *decimal.Decimal `json:"expected_mdr"`
	ExpectedVAT     *decimal.Decimal `json:"expected_vat"`
	ProposedVAT     decimal.Decimal  `json:"proposed_vat"`
	ProposedMDR     decimal.Decimal  `json:"proposed_mdr"`
	MDRRate         *string          `json:"mdr_rate"`
	VATRate         *string          `json:"vat_rate"`
	WithoutVATRate  int              `json:"without_vat_rate"`
	WithoutRate     int              `json:"without_rate"`
	// VATTreatment is what a settlement with VAT would freeze on the date of the line; InputVATAccount is the account of the VAT then (none for EXPENSE).
	VATTreatment    string          `json:"vat_treatment"`
	InputVATAccount *PreviewAccount `json:"input_vat_account"`
}

// SettlementPreview proposes the split of the deduction for the payment lines chosen against a statement line (bank.view): the deduction, what the payments expected, the VAT and the
// MDR the system proposes, and the treatment the VAT would get. Nothing is written; the settlement itself is the usual call.
func (s *Service) SettlementPreview(ctx context.Context, propertyID, statementID, lineID int64, in SettleInput) (SettlementPreview, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return SettlementPreview{}, err
	}
	if !validKey(in.AccountKey) {
		return SettlementPreview{}, apperr.Invalid("the request is invalid", fieldErr("account_key", "INVALID", "CARD or OTHER_PAYMENT"))
	}
	if n := len(in.JournalLineIDs); n < 1 || n > maxClearBatch {
		return SettlementPreview{}, apperr.Invalid("the request is invalid", fieldErr("journal_line_ids", "INVALID_COUNT", "between 1 and 200 payment lines"))
	}
	var out SettlementPreview
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error { // the VAT treatment reads the tax settings under a share lock
		st, err := s.statementRow(ctx, p.TenantID, propertyID, statementID)
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
		if !line.Amount.IsPositive() {
			return apperr.Invalid("the request is invalid", fieldErr("statement_line_id", "NOT_MONEY_IN", "a settlement is money paid into the bank"))
		}
		clearing, err := s.clearingAccount(ctx, p.TenantID, propertyID, in.AccountKey)
		if err != nil {
			return err
		}
		_, gross, err := s.settlementItems(ctx, q, p.TenantID, propertyID, st.PeriodTo, clearing, in.AccountKey, in.JournalLineIDs)
		if err != nil {
			return err
		}
		deduction := gross.Sub(line.Amount)
		switch {
		case !gross.IsPositive():
			return apperr.Invalid("the request is invalid", fieldErr("journal_line_ids", "NOT_POSITIVE", "the payments chosen add up to "+gross.String()))
		case deduction.IsNegative():
			return apperr.Invalid("the request is invalid", fieldErr("journal_line_ids", "NET_EXCEEDS_GROSS", "the bank paid "+line.Amount.String()+", more than the "+gross.String()+" of the payments chosen"))
		}
		prop, err := s.days.GetProperty(ctx, propertyID)
		if err != nil {
			return err
		}
		exp, err := s.expectationOf(ctx, p.TenantID, propertyID, in.JournalLineIDs)
		if err != nil {
			return err
		}
		proposed := proposeVAT(deduction, exp, prop.CurrencyDecimals)
		out = SettlementPreview{
			StatementLineID: lineID, AccountKey: in.AccountKey, Gross: gross, Net: line.Amount, Deduction: deduction, ProposedVAT: proposed, ProposedMDR: deduction.Sub(proposed),
			WithoutVATRate: exp.withoutVATRate, WithoutRate: exp.withoutRate,
		}
		if exp.known {
			mdr, vat := exp.mdr, exp.vat
			out.ExpectedMDR, out.ExpectedVAT = &mdr, &vat
		}
		if exp.mdrRate != nil {
			r := exp.mdrRate.String()
			out.MDRRate = &r
		}
		if exp.vatRate != nil {
			r := exp.vatRate.String()
			out.VATRate = &r
		}
		if out.VATTreatment, err = s.vatTreatmentOn(ctx, p.TenantID, propertyID, line.LineDate); err != nil {
			return err
		}
		if out.VATTreatment != taxfiling.InputVATExpense {
			id, err := q.MapAccount(ctx, bankrecdb.MapAccountParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: accounting.KeyInputVAT})
			if err != nil && !isNoRows(err) {
				return err
			}
			if err == nil {
				a, err := q.AccountLabel(ctx, bankrecdb.AccountLabelParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
				if err != nil {
					return err
				}
				out.InputVATAccount = &PreviewAccount{ID: id, Code: a.Code, Name: a.Name}
			}
		}
		return nil
	})
	return out, err
}
