package cityledger

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/cityledger/cityledgerdb"
	"kamarapms/internal/companies"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/tenancy"
)

// Service is the city ledger application service (cityledger.read / cityledger.receive).
type Service struct {
	txm       *db.TxManager
	clock     clock.Clock
	audit     *audit.Writer
	authz     auth.Authorizer
	days      *tenancy.Service
	iam       *iam.Service
	companies *companies.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, i *iam.Service, co *companies.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, iam: i, companies: co}
}

func (s *Service) q(ctx context.Context) *cityledgerdb.Queries {
	return cityledgerdb.New(s.txm.DB(ctx))
}

func errNotFound() *apperr.Error {
	return apperr.NotFound("COMPANY_NOT_FOUND", "the company does not exist in this property")
}

func errReceiptNotFound() *apperr.Error {
	return apperr.NotFound("RECEIPT_NOT_FOUND", "the receipt does not exist in this property")
}

func errKeyReused() *apperr.Error {
	return apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used for another request")
}

func field(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Service) actor(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func (s *Service) decimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "city_ledger_receipt", EntityID: id, Old: old, New: updated}
}

func account(id int64, code, name string, active bool, limit *decimal.Decimal, terms int16, transferred, received decimal.Decimal, decimals int32) Account {
	balance := transferred.Sub(received)
	a := Account{
		CompanyID: id, Code: code, Name: name, IsActive: active, PaymentTermsDays: int(terms),
		Transferred: transferred.StringFixed(decimals), Received: received.StringFixed(decimals), Balance: balance.StringFixed(decimals),
	}
	if limit != nil {
		l := limit.StringFixed(decimals)
		a.CreditLimit = &l
		avail := decimal.Max(limit.Sub(balance), decimal.Zero).StringFixed(decimals)
		a.Available = &avail
	}
	return a
}

// Accounts lists companies with their balances after afterID. owing keeps only those that owe.
func (s *Service) Accounts(ctx context.Context, propertyID, afterID int64, owing bool, q string, limit int) ([]Account, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListAccounts(ctx, cityledgerdb.ListAccountsParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, Q: nullable(strings.TrimSpace(q)), Owing: owing, RowLimit: int32(limit), //nolint:gosec // G115: page limit is bounded by ParsePage
	})
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, account(r.ID, r.Code, r.Name, r.IsActive, r.CreditLimit, r.PaymentTermsDays, r.Transferred, r.Received, decimals))
	}
	return out, nil
}

func (s *Service) loadAccount(ctx context.Context, tenantID, propertyID, companyID int64, decimals int32) (Account, error) {
	r, err := s.q(ctx).GetAccount(ctx, cityledgerdb.GetAccountParams{TenantID: tenantID, PropertyID: propertyID, ID: companyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, errNotFound()
	}
	if err != nil {
		return Account{}, err
	}
	return account(r.ID, r.Code, r.Name, r.IsActive, r.CreditLimit, r.PaymentTermsDays, r.Transferred, r.Received, decimals), nil
}

// GetAccount is one company's account.
func (s *Service) GetAccount(ctx context.Context, propertyID, companyID int64) (Account, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return Account{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Account{}, err
	}
	return s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals)
}

func receiptView(r cityledgerdb.CityLedgerReceipt, decimals int32) Receipt {
	return Receipt{
		ID: r.ID, ReceiptNumber: r.ReceiptNumber, CompanyID: r.CompanyID, Amount: r.Amount.StringFixed(decimals), PaymentMethod: r.PaymentMethod,
		ReferenceNumber: deref(r.ReferenceNumber), Remarks: deref(r.Remarks), BusinessDate: r.BusinessDate, PaidAt: r.PaidAt, Status: r.Status,
		VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason), CreatedBy: r.CreatedBy, ApprovedBy: r.ApprovedBy,
	}
}

// Receipts lists a company's receipts, oldest first.
func (s *Service) Receipts(ctx context.Context, propertyID, companyID int64) ([]Receipt, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if _, err := s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListReceipts(ctx, cityledgerdb.ListReceiptsParams{TenantID: p.TenantID, PropertyID: propertyID, CompanyID: companyID})
	if err != nil {
		return nil, err
	}
	out := make([]Receipt, 0, len(rows))
	for _, r := range rows {
		out = append(out, receiptView(r, decimals))
	}
	return out, nil
}

// Statement lists a company's movements; from and to are optional business dates (inclusive).
func (s *Service) Statement(ctx context.Context, propertyID, companyID int64, from, to *civil.Date) (Statement, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return Statement{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Statement{}, err
	}
	acc, err := s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals)
	if err != nil {
		return Statement{}, err
	}
	q := s.q(ctx)
	transfers, err := q.ListTransfers(ctx, cityledgerdb.ListTransfersParams{TenantID: p.TenantID, PropertyID: propertyID, CompanyID: &companyID})
	if err != nil {
		return Statement{}, err
	}
	receipts, err := q.ListReceipts(ctx, cityledgerdb.ListReceiptsParams{TenantID: p.TenantID, PropertyID: propertyID, CompanyID: companyID})
	if err != nil {
		return Statement{}, err
	}
	type entry struct {
		date  civil.Date
		order int
		id    int64
		line  StatementLine
		debit decimal.Decimal
		cred  decimal.Decimal
	}
	var all []entry
	for _, t := range transfers {
		e := entry{date: t.BusinessDate, order: 0, id: t.ID, line: StatementLine{
			Date: t.BusinessDate, Kind: "TRANSFER", Number: t.PaymentNumber, Description: "Folio " + t.FolioNumber, Reference: deref(t.ReferenceNumber), Status: t.Status,
			FolioNumber: t.FolioNumber, ConfirmationNumber: t.ConfirmationNumber, GuestName: t.GuestName,
		}}
		if t.Status == "POSTED" {
			e.debit = t.Amount
		}
		e.line.Debit = t.Amount.StringFixed(decimals)
		e.line.Credit = decimal.Zero.StringFixed(decimals)
		all = append(all, e)
	}
	for _, r := range receipts {
		e := entry{date: r.BusinessDate, order: 1, id: r.ID, line: StatementLine{
			Date: r.BusinessDate, Kind: "RECEIPT", Number: r.ReceiptNumber, Description: "Receipt (" + r.PaymentMethod + ")", Reference: deref(r.ReferenceNumber), Status: r.Status,
		}}
		if r.Status == "POSTED" {
			e.cred = r.Amount
		}
		e.line.Debit = decimal.Zero.StringFixed(decimals)
		e.line.Credit = r.Amount.StringFixed(decimals)
		all = append(all, e)
	}
	// Date, then transfers before receipts, then id: a stable order the running balance follows.
	sort.SliceStable(all, func(i, j int) bool {
		return less(all[i].date, all[i].order, all[i].id, all[j].date, all[j].order, all[j].id)
	})
	st := Statement{Company: acc, From: from, To: to, Lines: []StatementLine{}}
	opening, running, totalD, totalC := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
	for _, e := range all {
		if from != nil && e.date.Before(*from) {
			opening = opening.Add(e.debit).Sub(e.cred)
			running = opening
			continue
		}
		if to != nil && e.date.After(*to) {
			continue
		}
		running = running.Add(e.debit).Sub(e.cred)
		totalD, totalC = totalD.Add(e.debit), totalC.Add(e.cred)
		e.line.Balance = running.StringFixed(decimals)
		st.Lines = append(st.Lines, e.line)
	}
	st.OpeningBalance = opening.StringFixed(decimals)
	st.TotalDebit, st.TotalCredit = totalD.StringFixed(decimals), totalC.StringFixed(decimals)
	st.ClosingBalance = running.StringFixed(decimals)
	return st, nil
}

func less(d1 civil.Date, o1 int, i1 int64, d2 civil.Date, o2 int, i2 int64) bool {
	if c := d1.Compare(d2); c != 0 {
		return c < 0
	}
	if o1 != o2 {
		return o1 < o2
	}
	return i1 < i2
}

// Aging is what a company owes by the age of the transfers, as of the current business date.
func (s *Service) Aging(ctx context.Context, propertyID, companyID int64) (Aging, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return Aging{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Aging{}, err
	}
	acc, err := s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals)
	if err != nil {
		return Aging{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Aging{}, err
	}
	rows, err := s.q(ctx).ListTransfers(ctx, cityledgerdb.ListTransfersParams{TenantID: p.TenantID, PropertyID: propertyID, CompanyID: &companyID})
	if err != nil {
		return Aging{}, err
	}
	var ts []transfer
	for _, t := range rows {
		if t.Status == "POSTED" {
			ts = append(ts, transfer{t.BusinessDate, t.Amount})
		}
	}
	received, err := decimal.NewFromString(acc.Received)
	if err != nil {
		return Aging{}, err
	}
	buckets := ageTransfers(ts, received, day.BusinessDate)
	out := Aging{AsOf: day.BusinessDate, Total: acc.Balance}
	for i, label := range agingLabels {
		out.Buckets = append(out.Buckets, AgingBucket{Label: label, Amount: buckets[i].StringFixed(decimals)})
	}
	return out, nil
}

func (in ReceiptInput) parse(decimals int32) (decimal.Decimal, string, []apperr.FieldError) {
	var fields []apperr.FieldError
	amount, err := money.Parse(strings.TrimSpace(in.Amount))
	if err != nil || !amount.IsPositive() {
		fields = append(fields, field("amount", "INVALID_AMOUNT", "a positive amount"))
	} else if !amount.Equal(amount.Round(decimals)) {
		fields = append(fields, field("amount", "INVALID_AMOUNT", "at most the currency's decimals"))
	}
	method := strings.ToUpper(strings.TrimSpace(in.PaymentMethod))
	valid := false
	for _, m := range receiptMethods {
		valid = valid || m == method
	}
	if !valid {
		fields = append(fields, field("payment_method", "INVALID_VALUE", "one of "+strings.Join(receiptMethods, ", ")))
	}
	if len([]rune(in.ReferenceNumber)) > maxReferenceLen {
		fields = append(fields, field("reference_number", "TOO_LONG", "too long"))
	}
	if len([]rune(in.Remarks)) > maxRemarksLen {
		fields = append(fields, field("remarks", "TOO_LONG", "too long"))
	}
	return amount, method, fields
}

func receiptReplay[T any](key string, lookup func() (T, bool, error), run func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; attempt < 2; attempt++ {
		if v, ok, err := lookup(); err != nil || ok {
			return v, err
		}
		v, err := run()
		if apperr.IsCode(err, "DUPLICATE_REQUEST") {
			continue
		}
		return v, err
	}
	return zero, apperr.Busy("REQUEST_IN_PROGRESS", "the same request is still being processed")
}

// Receive records money a company paid against its account (cityledger.receive). The company row is locked
// (level 44) so the receipt cannot exceed what is owed, however many are taken at once.
func (s *Service) Receive(ctx context.Context, propertyID, companyID int64, key string, in ReceiptInput) (ReceiptResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerReceive)
	if err != nil {
		return ReceiptResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ReceiptResult{}, err
	}
	amount, method, fields := in.parse(decimals)
	if key == "" || len(key) > 100 {
		fields = append(fields, field("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return ReceiptResult{}, apperr.Invalid("the receipt is invalid", fields...)
	}
	return receiptReplay(key,
		func() (ReceiptResult, bool, error) {
			r, err := s.q(ctx).GetReceiptByKey(ctx, cityledgerdb.GetReceiptByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key})
			if errors.Is(err, pgx.ErrNoRows) {
				return ReceiptResult{}, false, nil
			}
			if err != nil {
				return ReceiptResult{}, false, err
			}
			if r.CompanyID != companyID {
				return ReceiptResult{}, false, errKeyReused()
			}
			acc, err := s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals)
			return ReceiptResult{Receipt: receiptView(r, decimals), Balance: acc.Balance}, true, err
		},
		func() (ReceiptResult, error) {
			var out ReceiptResult
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				owes, err := s.companies.LockForReceipt(ctx, propertyID, companyID)
				if err != nil {
					return err
				}
				if amount.GreaterThan(owes) {
					return apperr.Conflict("RECEIPT_EXCEEDS_BALANCE", "the receipt is more than the company owes").WithContext("balance", owes.StringFixed(decimals))
				}
				number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqCityLedgerReceipt)
				if err != nil {
					return err
				}
				r, err := s.q(ctx).InsertReceipt(ctx, cityledgerdb.InsertReceiptParams{
					TenantID: p.TenantID, PropertyID: propertyID, ReceiptNumber: number, CompanyID: companyID, Amount: amount, PaymentMethod: method,
					ReferenceNumber: nullable(strings.TrimSpace(in.ReferenceNumber)), Remarks: nullable(strings.TrimSpace(in.Remarks)),
					BusinessDate: day.BusinessDate, PaidAt: s.clock.Now(), IdempotencyKey: &key, ActorID: p.ActorID(),
				})
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cityledger.receipt_posted", r.ID, nil, map[string]any{
					"receipt_number": number, "company_id": companyID, "amount": amount.String(), "method": method,
				})); err != nil {
					return err
				}
				out = ReceiptResult{Receipt: receiptView(r, decimals), Balance: owes.Sub(amount).StringFixed(decimals)}
				return nil
			})
			return out, err
		})
}

// VoidReceipt voids a receipt taken on the current business date (cityledger.receive plus an approval).
func (s *Service) VoidReceipt(ctx context.Context, propertyID, receiptID int64, in VoidInput) (ReceiptResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerReceive)
	if err != nil {
		return ReceiptResult{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return ReceiptResult{}, apperr.Invalid("the void is invalid", field("reason", "REQUIRED", "a reason is required"))
	case len([]rune(reason)) > maxReasonLen:
		return ReceiptResult{}, apperr.Invalid("the void is invalid", field("reason", "TOO_LONG", "too long"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return ReceiptResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ReceiptResult{}, err
	}
	var out ReceiptResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		pre, err := s.q(ctx).GetReceipt(ctx, cityledgerdb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: receiptID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errReceiptNotFound()
		}
		if err != nil {
			return err
		}
		owes, err := s.companies.LockForReceipt(ctx, propertyID, pre.CompanyID)
		if err != nil {
			return err
		}
		r, err := s.q(ctx).GetReceipt(ctx, cityledgerdb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: receiptID})
		if err != nil {
			return err
		}
		switch {
		case r.Status != ReceiptPosted:
			return apperr.Conflict("RECEIPT_ALREADY_VOIDED", "the receipt is already voided")
		case !r.BusinessDate.Equal(day.BusinessDate):
			return apperr.Conflict("CORRECTION_REQUIRES_ADJUSTMENT", "only a receipt taken on the current business date can be voided").WithContext("business_date", r.BusinessDate)
		}
		by := approval.UserID()
		v, err := s.q(ctx).VoidReceipt(ctx, cityledgerdb.VoidReceiptParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: receiptID, Now: s.clock.Now(), ActorID: p.ActorID(), Reason: &reason, ApprovedBy: &by,
		})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cityledger.receipt_voided", receiptID,
			map[string]any{"status": r.Status}, map[string]any{"status": v.Status, "reason": reason, "actor": p.ActorID(), "approved_by": by})); err != nil {
			return err
		}
		out = ReceiptResult{Receipt: receiptView(v, decimals), Balance: owes.Add(r.Amount).StringFixed(decimals)}
		return nil
	})
	return out, err
}
