package bankrec

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/bankrec/bankrecdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
)

// Card fee rules and the settlements that were expected (design: docs/architecture/11-cashier-budget-cashflow-card.md, part C). A rule says the percent (MDR) the
// acquirer keeps from the payments of a method and how many days it takes to pay out, from a date; a payment keeps a snapshot of the rule that applied on its day.
// Nothing here is booked: it tells the cashier what the bank should pay, and when, and helps to choose the payment lines of a settlement.

// FeeRule is a rate from a date.
type FeeRule struct {
	ID             int64      `json:"id"`
	PaymentMethod  string     `json:"payment_method"`
	MDRRate        string     `json:"mdr_rate"`
	VATRate        string     `json:"vat_rate"` // the VAT the acquirer charges on the MDR
	SettlementDays int        `json:"settlement_days"`
	EffectiveFrom  civil.Date `json:"effective_from"`
	CreatedAt      time.Time  `json:"created_at"`
}

// FeeRuleInput adds a rule. A rule is never changed: a new rate is a new rule from a later date.
type FeeRuleInput struct {
	PaymentMethod  string     `json:"payment_method"`
	MDRRate        string     `json:"mdr_rate"`
	VATRate        string     `json:"vat_rate"` // optional, a percentage; empty is 0
	SettlementDays int        `json:"settlement_days"`
	EffectiveFrom  civil.Date `json:"effective_from"`
}

// ExpectedLine is a payment line of a clearing account that no settlement has settled, with the fee and the payout date its payment expected.
type ExpectedLine struct {
	JournalLineID int64           `json:"journal_line_id"`
	Date          civil.Date      `json:"journal_date"`
	JournalNumber string          `json:"journal_number"`
	Description   string          `json:"description,omitempty"`
	Reference     string          `json:"reference,omitempty"`
	Amount        decimal.Decimal `json:"amount"`
	MDRRate       *string         `json:"mdr_rate"`
	ExpectedMDR   decimal.Decimal `json:"expected_mdr"`
	// The VAT rate and the VAT expected on the MDR; the rate is nil, and the VAT 0, for a payment taken before the VAT was kept (WithoutVATRate: it has an MDR but no VAT rate).
	VATRate           *string         `json:"vat_rate"`
	ExpectedVAT       decimal.Decimal `json:"expected_vat"`
	ExpectedDeduction decimal.Decimal `json:"expected_deduction"`
	WithoutVATRate    bool            `json:"without_vat_rate"`
	ExpectedNet       decimal.Decimal `json:"expected_net"`
	ExpectedDate      *civil.Date     `json:"expected_date"`
	Late              bool            `json:"late"`
}

// Expected is what the acquirer should still pay for the payments of a clearing account.
type Expected struct {
	AsOf              civil.Date      `json:"as_of"`
	AccountKey        string          `json:"account_key"`
	Gross             decimal.Decimal `json:"gross"`
	ExpectedMDR       decimal.Decimal `json:"expected_mdr"`
	ExpectedVAT       decimal.Decimal `json:"expected_vat"`
	ExpectedDeduction decimal.Decimal `json:"expected_deduction"`
	ExpectedNet       decimal.Decimal `json:"expected_net"`
	LateCount         int             `json:"late_count"`
	LateGross         decimal.Decimal `json:"late_gross"`
	NoRate            int             `json:"without_rate"`
	WithoutVATRate    int             `json:"without_vat_rate"` // payments with an MDR but no VAT rate
	Lines             []ExpectedLine  `json:"lines"`
}

// Proposal is the payment lines that most likely make up a line of the bank statement.
type Proposal struct {
	StatementLineID   int64           `json:"statement_line_id"`
	AccountKey        string          `json:"account_key"`
	Amount            decimal.Decimal `json:"amount"`
	Tolerance         decimal.Decimal `json:"tolerance"`
	Matched           bool            `json:"matched"`
	Difference        decimal.Decimal `json:"difference"`
	Gross             decimal.Decimal `json:"gross"`
	ExpectedMDR       decimal.Decimal `json:"expected_mdr"`
	ExpectedVAT       decimal.Decimal `json:"expected_vat"`
	ExpectedDeduction decimal.Decimal `json:"expected_deduction"`
	WithoutVATRate    int             `json:"without_vat_rate"`
	ExpectedNet       decimal.Decimal `json:"expected_net"`
	JournalLineIDs    []int64         `json:"journal_line_ids"`
	Lines             []ExpectedLine  `json:"lines"`
}

// SettlementRow is a settlement made, with the fee it was expected to cost.
type SettlementRow struct {
	ID            int64           `json:"id"`
	BankAccountID int64           `json:"bank_account_id"`
	AccountKey    string          `json:"account_key"`
	Date          civil.Date      `json:"journal_date"`
	JournalNumber string          `json:"journal_number"`
	Gross         decimal.Decimal `json:"gross"`
	Net           decimal.Decimal `json:"net"`
	Fee           decimal.Decimal `json:"fee"`
	// The deduction of the bank is Fee: the MDR and the VAT as they were booked (frozen with the settlement), the VAT treatment of the day, what the payments expected, what the system
	// proposed, the rates when the payments shared one, and the variances (MDR and VAT booked less expected; null when a payment had no snapshot).
	MDRAmount              decimal.Decimal  `json:"mdr_amount"`
	VATAmount              decimal.Decimal  `json:"vat_amount"`
	VATTreatment           *string          `json:"vat_treatment"`
	ExpectedMDR            *decimal.Decimal `json:"expected_mdr"`
	ExpectedVAT            *decimal.Decimal `json:"expected_vat"`
	ProposedVAT            decimal.Decimal  `json:"proposed_vat"`
	MDRRate                *string          `json:"mdr_rate"`
	VATRate                *string          `json:"vat_rate"`
	PaymentsWithoutVATRate int              `json:"payments_without_vat_rate"`
	MDRVariance            *decimal.Decimal `json:"mdr_variance"`
	VATVariance            *decimal.Decimal `json:"vat_variance"`
	Payments               int              `json:"payments"`
	Reference              string           `json:"reference,omitempty"`
	CreatedAt              time.Time        `json:"created_at"`
}

func toFeeRule(r bankrecdb.CardFeeRule) FeeRule {
	return FeeRule{ID: r.ID, PaymentMethod: r.PaymentMethod, MDRRate: r.MdrRate.String(), VATRate: r.VatRate.String(), SettlementDays: int(r.SettlementDays), EffectiveFrom: r.EffectiveFrom, CreatedAt: r.CreatedAt}
}

// CardFeeRules lists the rules, by method and then the newest first (bank.view).
func (s *Service) CardFeeRules(ctx context.Context, propertyID int64) ([]FeeRule, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListCardFeeRules(ctx, bankrecdb.ListCardFeeRulesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]FeeRule, 0, len(rows))
	for _, r := range rows {
		out = append(out, toFeeRule(r))
	}
	return out, nil
}

// CreateCardFeeRule adds a rule (bank.manage). It applies to the payments of its method from its date on, as they are taken: the payments already taken keep the
// rate they were taken under.
func (s *Service) CreateCardFeeRule(ctx context.Context, propertyID int64, in FeeRuleInput) (FeeRule, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankManage)
	if err != nil {
		return FeeRule{}, err
	}
	var fields []apperr.FieldError
	if in.PaymentMethod != "CARD" && in.PaymentMethod != "OTHER" {
		fields = append(fields, fieldErr("payment_method", "INVALID_VALUE", "CARD or OTHER"))
	}
	vatRate := decimal.Zero
	if v := strings.TrimSpace(in.VATRate); v != "" {
		var verr error
		switch vatRate, verr = money.Parse(v); {
		case verr != nil || vatRate.IsNegative() || vatRate.GreaterThan(decimal.NewFromInt(100)):
			fields = append(fields, fieldErr("vat_rate", "INVALID_RATE", "a percentage from 0 to 100"))
		case !vatRate.Equal(vatRate.Round(4)):
			fields = append(fields, fieldErr("vat_rate", "INVALID_RATE", "at most 4 decimals"))
		}
	}
	rate, perr := money.Parse(strings.TrimSpace(in.MDRRate))
	switch {
	case perr != nil || rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(100)):
		fields = append(fields, fieldErr("mdr_rate", "INVALID_RATE", "a percentage from 0 to 100"))
	case !rate.Equal(rate.Round(4)):
		fields = append(fields, fieldErr("mdr_rate", "INVALID_RATE", "at most 4 decimals"))
	}
	if in.SettlementDays < 0 || in.SettlementDays > 60 {
		fields = append(fields, fieldErr("settlement_days", "OUT_OF_RANGE", "between 0 and 60 days"))
	}
	if in.EffectiveFrom.IsZero() {
		fields = append(fields, fieldErr("effective_from", "REQUIRED", "the date the rate starts"))
	}
	if len(fields) > 0 {
		return FeeRule{}, apperr.Invalid("the fee rule is invalid", fields...)
	}
	var out FeeRule
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).InsertCardFeeRule(ctx, bankrecdb.InsertCardFeeRuleParams{
			TenantID: p.TenantID, PropertyID: propertyID, PaymentMethod: in.PaymentMethod, MdrRate: rate, VatRate: vatRate, SettlementDays: int16(in.SettlementDays), EffectiveFrom: in.EffectiveFrom, ActorID: p.ActorID(), //nolint:gosec // G115: checked above
		})
		if err != nil {
			return err
		}
		out = toFeeRule(row)
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.card_fee_rule_added", "card_fee_rule", row.ID, nil,
			map[string]any{"payment_method": in.PaymentMethod, "mdr_rate": rate.String(), "vat_rate": vatRate.String(), "settlement_days": in.SettlementDays, "effective_from": in.EffectiveFrom}))
	})
	return out, err
}

type snapshot struct {
	rate    decimal.Decimal
	fee     decimal.Decimal
	on      civil.Date
	vatRate *decimal.Decimal // nil: the payment was taken before the VAT was kept ("without rate")
	vat     *decimal.Decimal
}

// snapshots reads what the payments and the receipts of some numbers expected.
func (s *Service) snapshots(ctx context.Context, tenantID, propertyID int64, numbers []string) (map[string]snapshot, error) {
	out := map[string]snapshot{}
	if len(numbers) == 0 {
		return out, nil
	}
	q := s.q(ctx)
	pays, err := q.PaymentFeeSnapshots(ctx, bankrecdb.PaymentFeeSnapshotsParams{TenantID: tenantID, PropertyID: propertyID, Numbers: numbers})
	if err != nil {
		return nil, err
	}
	for _, r := range pays {
		out[r.DocNumber] = snapshot{r.MdrRate, r.MdrFee, r.ExpectedDate, r.VatRate, r.Vat}
	}
	recs, err := q.ReceiptFeeSnapshots(ctx, bankrecdb.ReceiptFeeSnapshotsParams{TenantID: tenantID, PropertyID: propertyID, Numbers: numbers})
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		out[r.DocNumber] = snapshot{r.MdrRate, r.MdrFee, r.ExpectedDate, r.VatRate, r.Vat}
	}
	return out, nil
}

// expectedLines turns unsettled payment lines into lines with the fee and the date their payments expected. A line without a snapshot (taken before there was a
// rule) expects no fee and no date.
func (s *Service) expectedLines(ctx context.Context, tenantID, propertyID, accountID int64, to civil.Date, today civil.Date) ([]ExpectedLine, error) {
	rows, err := s.q(ctx).SettlementCandidates(ctx, bankrecdb.SettlementCandidatesParams{TenantID: tenantID, PropertyID: propertyID, AccountID: accountID, ToDate: to, RowLimit: maxUncleared})
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.SourceRef != nil {
			refs = append(refs, *r.SourceRef)
		}
	}
	snaps, err := s.snapshots(ctx, tenantID, propertyID, refs)
	if err != nil {
		return nil, err
	}
	out := make([]ExpectedLine, 0, len(rows))
	for _, r := range rows {
		if r.Amount.IsZero() {
			continue
		}
		desc := deref(r.Description)
		if desc == "" {
			desc = r.JournalDescription
		}
		l := ExpectedLine{JournalLineID: r.ID, Date: r.JournalDate, JournalNumber: r.JournalNumber, Description: desc, Reference: deref(r.SourceRef), Amount: r.Amount, ExpectedNet: r.Amount, ExpectedMDR: decimal.Zero, ExpectedVAT: decimal.Zero, ExpectedDeduction: decimal.Zero}
		if sn, ok := snaps[l.Reference]; ok && r.Amount.IsPositive() {
			rate := sn.rate.String()
			on := sn.on
			l.MDRRate, l.ExpectedMDR, l.ExpectedDate = &rate, sn.fee, &on
			if sn.vatRate != nil && sn.vat != nil {
				vr := sn.vatRate.String()
				l.VATRate, l.ExpectedVAT = &vr, *sn.vat
			} else {
				l.WithoutVATRate = true // nothing is guessed: its VAT expects 0
			}
			l.ExpectedDeduction = l.ExpectedMDR.Add(l.ExpectedVAT)
			l.ExpectedNet = r.Amount.Sub(l.ExpectedDeduction)
			l.Late = on.Before(today)
		}
		out = append(out, l)
	}
	return out, nil
}

func (s *Service) clearingAccount(ctx context.Context, tenantID, propertyID int64, key string) (int64, error) {
	if !validKey(key) {
		return 0, apperr.Invalid("the request is invalid", fieldErr("account_key", "INVALID", "CARD or OTHER_PAYMENT"))
	}
	id, err := s.q(ctx).MapAccount(ctx, bankrecdb.MapAccountParams{TenantID: tenantID, PropertyID: propertyID, MapKey: key})
	if isNoRows(err) {
		return 0, apperr.Conflict("ACCOUNT_MAP_INCOMPLETE", "the system account "+key+" is not mapped")
	}
	return id, err
}

// ExpectedSettlements answers what the acquirer should still pay for the payments of a clearing account (bank.view), with the fee each payment expected and the
// day it should have paid out. The payments past that day are the late ones.
func (s *Service) ExpectedSettlements(ctx context.Context, propertyID int64, key string) (Expected, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return Expected{}, err
	}
	account, err := s.clearingAccount(ctx, p.TenantID, propertyID, key)
	if err != nil {
		return Expected{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Expected{}, err
	}
	lines, err := s.expectedLines(ctx, p.TenantID, propertyID, account, day.BusinessDate, day.BusinessDate)
	if err != nil {
		return Expected{}, err
	}
	out := Expected{AsOf: day.BusinessDate, AccountKey: key, Lines: lines, Gross: decimal.Zero, ExpectedMDR: decimal.Zero, ExpectedVAT: decimal.Zero, ExpectedDeduction: decimal.Zero, ExpectedNet: decimal.Zero, LateGross: decimal.Zero}
	if out.Lines == nil {
		out.Lines = []ExpectedLine{}
	}
	for _, l := range lines {
		out.Gross, out.ExpectedMDR, out.ExpectedNet = out.Gross.Add(l.Amount), out.ExpectedMDR.Add(l.ExpectedMDR), out.ExpectedNet.Add(l.ExpectedNet)
		out.ExpectedVAT, out.ExpectedDeduction = out.ExpectedVAT.Add(l.ExpectedVAT), out.ExpectedDeduction.Add(l.ExpectedDeduction)
		if l.MDRRate == nil {
			out.NoRate++
		}
		if l.WithoutVATRate {
			out.WithoutVATRate++
		}
		if l.Late {
			out.LateCount++
			out.LateGross = out.LateGross.Add(l.Amount)
		}
	}
	return out, nil
}

// pick chooses the oldest payments whose expected net adds up closest to what the bank paid: the acquirer pays in the order the payments come due, so the
// payments are taken in that order, and the number taken is the one that leaves the least difference (the fewer when two are equally close).
func pick(lines []ExpectedLine, target decimal.Decimal) (count int, sum decimal.Decimal) {
	best, bestSum, run := decimal.Zero, decimal.Zero, decimal.Zero
	first := true
	for i, l := range lines {
		run = run.Add(l.ExpectedNet)
		diff := run.Sub(target).Abs()
		if first || diff.LessThan(best) {
			best, bestSum, count, first = diff, run, i+1, false
		}
	}
	return count, bestSum
}

// dueOrder sorts payments by the day they should be paid out (the day they were made when there is no snapshot), then by line.
func dueOrder(lines []ExpectedLine) {
	due := func(l ExpectedLine) civil.Date {
		if l.ExpectedDate != nil {
			return *l.ExpectedDate
		}
		return l.Date
	}
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := due(lines[i]), due(lines[j])
		if !a.Equal(b) {
			return a.Before(b)
		}
		return lines[i].JournalLineID < lines[j].JournalLineID
	})
}

// SettlementProposal suggests the payment lines of a settlement for a line of money in on a statement (bank.view): the unsettled payments made by the day of the
// line, in the order they come due, up to the number whose expected net is closest to the line. `matched` says the difference is within the tolerance (a
// currency unit or half a percent of the line); the proposal is only a choice of lines, and settling them is the usual call.
func (s *Service) SettlementProposal(ctx context.Context, propertyID, statementID, lineID int64, key string) (Proposal, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return Proposal{}, err
	}
	account, err := s.clearingAccount(ctx, p.TenantID, propertyID, key)
	if err != nil {
		return Proposal{}, err
	}
	st, err := s.statementRow(ctx, p.TenantID, propertyID, statementID)
	if err != nil {
		return Proposal{}, err
	}
	line, err := s.q(ctx).GetStatementLine(ctx, bankrecdb.GetStatementLineParams{TenantID: p.TenantID, PropertyID: propertyID, StatementID: statementID, ID: lineID})
	if isNoRows(err) {
		return Proposal{}, apperr.NotFound("STATEMENT_LINE_NOT_FOUND", "the line does not exist in this statement")
	}
	if err != nil {
		return Proposal{}, err
	}
	if !line.Amount.IsPositive() {
		return Proposal{}, apperr.Invalid("the request is invalid", fieldErr("statement_line_id", "NOT_MONEY_IN", "a settlement is money paid into the bank"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Proposal{}, err
	}
	all, err := s.expectedLines(ctx, p.TenantID, propertyID, account, st.PeriodTo, st.PeriodTo)
	if err != nil {
		return Proposal{}, err
	}
	var lines []ExpectedLine
	for _, l := range all {
		if !l.Date.After(line.LineDate) && l.Amount.IsPositive() {
			lines = append(lines, l)
		}
	}
	dueOrder(lines)
	tol := decimal.New(1, -prop.CurrencyDecimals)
	if half := money.Percent(line.Amount, decimal.RequireFromString("0.5"), prop.CurrencyDecimals); half.GreaterThan(tol) {
		tol = half
	}
	out := Proposal{StatementLineID: lineID, AccountKey: key, Amount: line.Amount, Tolerance: tol, JournalLineIDs: []int64{}, Lines: []ExpectedLine{}, Gross: decimal.Zero, ExpectedMDR: decimal.Zero, ExpectedVAT: decimal.Zero, ExpectedDeduction: decimal.Zero, ExpectedNet: decimal.Zero}
	if len(lines) == 0 {
		out.Difference = line.Amount
		return out, nil
	}
	n, sum := pick(lines, line.Amount)
	out.Lines = lines[:n]
	for _, l := range out.Lines {
		out.JournalLineIDs = append(out.JournalLineIDs, l.JournalLineID)
		out.Gross, out.ExpectedMDR = out.Gross.Add(l.Amount), out.ExpectedMDR.Add(l.ExpectedMDR)
		out.ExpectedVAT, out.ExpectedDeduction = out.ExpectedVAT.Add(l.ExpectedVAT), out.ExpectedDeduction.Add(l.ExpectedDeduction)
		if l.WithoutVATRate {
			out.WithoutVATRate++
		}
	}
	out.ExpectedNet = sum
	out.Difference = line.Amount.Sub(sum)
	out.Matched = out.Difference.Abs().LessThanOrEqual(tol)
	return out, nil
}

// Settlements lists the settlements made, newest first, with the fee each was expected to cost and the difference (bank.view).
func (s *Service) Settlements(ctx context.Context, propertyID, before int64, limit int) ([]SettlementRow, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return nil, err
	}
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.q(ctx).ListSettlements(ctx, bankrecdb.ListSettlementsParams{TenantID: p.TenantID, PropertyID: propertyID, BeforeID: before, RowLimit: int32(limit)}) //nolint:gosec // G115: the page limit is small
	if err != nil {
		return nil, err
	}
	out := make([]SettlementRow, 0, len(rows))
	for _, r := range rows {
		row := SettlementRow{
			ID: r.ID, BankAccountID: r.BankAccountID, AccountKey: r.AccountKey, Date: r.JournalDate, JournalNumber: r.JournalNumber, Gross: r.Gross, Net: r.Net, Fee: r.Fee,
			MDRAmount: r.MdrAmount, VATAmount: r.VatAmount, VATTreatment: r.VatTreatment, ExpectedMDR: r.ExpectedMdr, ExpectedVAT: r.ExpectedVat, ProposedVAT: r.ProposedVat,
			PaymentsWithoutVATRate: int(r.PaymentsWithoutVatRate), Payments: int(r.Payments), Reference: deref(r.Reference), CreatedAt: r.CreatedAt,
		}
		if r.MdrRate != nil {
			v := r.MdrRate.String()
			row.MDRRate = &v
		}
		if r.VatRate != nil {
			v := r.VatRate.String()
			row.VATRate = &v
		}
		if r.ExpectedMdr != nil {
			v := r.MdrAmount.Sub(*r.ExpectedMdr)
			row.MDRVariance = &v
		}
		if r.ExpectedVat != nil {
			v := r.VatAmount.Sub(*r.ExpectedVat)
			row.VATVariance = &v
		}
		out = append(out, row)
	}
	return out, nil
}
