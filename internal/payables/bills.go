package payables

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/payables/payablesdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/tenancy"
)

func toBill(r payablesdb.ListBillsRow) Bill {
	b := Bill{
		ID: r.ID, Number: r.BillNumber, SupplierID: r.SupplierID, SupplierCode: r.SupplierCode, SupplierName: r.SupplierName, SupplierInvoiceNumber: r.SupplierInvoiceNumber,
		BillDate: r.BillDate, DueDate: r.DueDate, Description: deref(r.Description), Total: r.Total, Paid: r.Paid, Credited: r.Credited, Status: r.Status, JournalID: r.JournalID,
		JournalNumber: r.JournalNumber, VoidJournalID: r.VoidJournalID, VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason), CreatedAt: r.CreatedAt,
	}
	switch r.Status {
	case "VOIDED":
		b.PaymentStatus, b.Paid, b.Credited = BillVoided, decimal.Zero, decimal.Zero
	default:
		b.Outstanding = r.Total.Sub(r.Paid).Sub(r.Credited)
		switch {
		case b.Outstanding.IsZero():
			b.PaymentStatus = BillPaid
		case r.Paid.IsPositive() || r.Credited.IsPositive():
			b.PaymentStatus = BillPartial
		default:
			b.PaymentStatus = BillUnpaid
		}
	}
	return b
}

// Bills lists supplier bills, newest first (payables.view).
func (s *Service) Bills(ctx context.Context, propertyID int64, f BillFilter) ([]Bill, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return nil, err
	}
	return s.listBills(ctx, p.TenantID, propertyID, nil, f)
}

func (s *Service) listBills(ctx context.Context, tenantID, propertyID int64, id *int64, f BillFilter) ([]Bill, error) {
	rows, err := s.q(ctx).ListBills(ctx, payablesdb.ListBillsParams{
		TenantID: tenantID, PropertyID: propertyID, ID: id, SupplierID: f.SupplierID, Status: nullable(f.Status), FromDate: f.From, ToDate: f.To,
		Q: nullable(f.Q), OpenOnly: f.OpenOnly, RowLimit: limitOf(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Bill, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBill(r))
	}
	return out, nil
}

// GetBill is one bill with its lines (payables.view).
func (s *Service) GetBill(ctx context.Context, propertyID, id int64) (Bill, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p.TenantID, propertyID, id)
}

func (s *Service) loadBill(ctx context.Context, tenantID, propertyID, id int64) (Bill, error) {
	list, err := s.listBills(ctx, tenantID, propertyID, &id, BillFilter{Limit: 1})
	if err != nil {
		return Bill{}, err
	}
	if len(list) == 0 {
		return Bill{}, errBillNotFound()
	}
	b := list[0]
	lines, err := s.q(ctx).ListBillLines(ctx, payablesdb.ListBillLinesParams{TenantID: tenantID, PropertyID: propertyID, BillID: id})
	if err != nil {
		return Bill{}, err
	}
	b.Lines = make([]BillLine, 0, len(lines))
	for _, l := range lines {
		b.Lines = append(b.Lines, BillLine{LineNo: l.LineNo, AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Description: deref(l.Description), Amount: l.Amount, VATAmount: l.VatAmount, VATTreatment: deref(l.VatTreatment),
			DepartmentID: l.DepartmentID, DepartmentCode: deref(l.DepartmentCode), DepartmentName: deref(l.DepartmentName)})
	}
	return b, nil
}

func validateBill(in BillInput, decimals int32) (decimal.Decimal, []apperr.FieldError) {
	var fields []apperr.FieldError
	inv := strings.TrimSpace(in.SupplierInvoiceNumber)
	switch {
	case inv == "":
		fields = append(fields, fieldErr("supplier_invoice_number", "REQUIRED", "the number on the supplier's invoice"))
	case len([]rune(inv)) > 60:
		fields = append(fields, fieldErr("supplier_invoice_number", "TOO_LONG", "at most 60 characters"))
	}
	if len([]rune(in.Description)) > 300 {
		fields = append(fields, fieldErr("description", "TOO_LONG", "at most 300 characters"))
	}
	if in.SupplierID < 1 {
		fields = append(fields, fieldErr("supplier_id", "REQUIRED", "choose a supplier"))
	}
	if in.BillDate.IsZero() {
		fields = append(fields, fieldErr("bill_date", "REQUIRED", "the date of the bill"))
	} else if in.DueDate != nil && in.DueDate.Before(in.BillDate) {
		fields = append(fields, fieldErr("due_date", "BEFORE_BILL_DATE", "not before the bill date"))
	}
	if len(in.Lines) < 1 || len(in.Lines) > maxBillLines {
		fields = append(fields, fieldErr("lines", "INVALID_COUNT", "between 1 and 100 lines"))
	}
	total := decimal.Zero
	for i, l := range in.Lines {
		at := func(f string) string { return fmt.Sprintf("lines[%d].%s", i, f) }
		switch {
		case !l.Amount.IsPositive():
			fields = append(fields, fieldErr(at("amount"), "NOT_POSITIVE", "an amount above zero"))
		case !l.Amount.Equal(l.Amount.Round(decimals)):
			fields = append(fields, fieldErr(at("amount"), "TOO_PRECISE", fmt.Sprintf("at most %d decimals", decimals)))
		}
		switch {
		case l.VATAmount.IsNegative():
			fields = append(fields, fieldErr(at("vat_amount"), "NEGATIVE", "zero or more"))
		case !l.VATAmount.Equal(l.VATAmount.Round(decimals)):
			fields = append(fields, fieldErr(at("vat_amount"), "TOO_PRECISE", fmt.Sprintf("at most %d decimals", decimals)))
		}
		if l.AccountID < 1 {
			fields = append(fields, fieldErr(at("account_id"), "REQUIRED", "choose the account it is charged to"))
		}
		if len([]rune(l.Description)) > 300 {
			fields = append(fields, fieldErr(at("description"), "TOO_LONG", "at most 300 characters"))
		}
		total = total.Add(l.Amount).Add(l.VATAmount)
	}
	return total, fields
}

// PostBill enters a supplier bill (payables.post): its journal debits each line's account against ACCOUNTS PAYABLE
// on the bill date. The Idempotency-Key makes a retry return the first bill.
func (s *Service) PostBill(ctx context.Context, propertyID int64, in BillInput, key string) (Bill, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesPost)
	if err != nil {
		return Bill{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	total, fields := validateBill(in, prop.CurrencyDecimals)
	if len(fields) > 0 {
		return Bill{}, apperr.Invalid("the bill is invalid", fields...)
	}
	var id int64
	err = retryOnDuplicate(key, func() error { return s.postBill(ctx, p, propertyID, in, total, key, &id) })
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p.TenantID, propertyID, id)
}

func (s *Service) postBill(ctx context.Context, p auth.Principal, propertyID int64, in BillInput, total decimal.Decimal, key string, out *int64) error {
	inv := strings.TrimSpace(in.SupplierInvoiceNumber)
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID) // accounting settings (46) before the supplier (47)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.SupplierActive(ctx, payablesdb.SupplierActiveParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.SupplierID}); isNoRows(err) {
			return apperr.Invalid("the bill is invalid", fieldErr("supplier_id", "NOT_FOUND", "no such supplier in this property"))
		} else if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Suppliers, db.ForUpdate, propertyID, []int64{in.SupplierID}); err != nil {
			return err
		}
		if key != "" {
			if prev, err := q.FindBillByKey(ctx, payablesdb.FindBillByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
				*out = prev
				return nil
			} else if !isNoRows(err) {
				return err
			}
		}
		sup, err := s.loadSupplier(ctx, p.TenantID, propertyID, in.SupplierID)
		if err != nil {
			return err
		}
		if !sup.IsActive {
			return apperr.Conflict("SUPPLIER_INACTIVE", "the supplier is inactive")
		}
		due := in.BillDate.AddDays(sup.PaymentTermsDays)
		if in.DueDate != nil {
			due = *in.DueDate
		}
		if err := po.CheckDate(ctx, in.BillDate, "bill_date"); err != nil {
			return err
		}
		// How the VAT of the bill is treated is the PKP status of the property on the bill date, frozen on each line.
		status, err := s.tax.SettingsOnDate(ctx, p.TenantID, propertyID, in.BillDate)
		if err != nil {
			return err
		}
		treatment := status.InputVATTreatment
		var inputVAT int64
		for _, l := range in.Lines {
			if l.VATAmount.IsPositive() && treatment != taxfiling.InputVATExpense {
				if inputVAT, err = po.SystemAccount(ctx, accounting.KeyInputVAT); err != nil {
					return err
				}
				break
			}
		}
		payable, err := po.SystemAccount(ctx, accounting.KeyAccountsPayable)
		if err != nil {
			return err
		}
		var lineErrs []apperr.FieldError
		for i, l := range in.Lines {
			at := fmt.Sprintf("lines[%d].account_id", i)
			if l.AccountID == payable {
				lineErrs = append(lineErrs, fieldErr(at, "CONTROL_ACCOUNT", "the payables account is posted by the bill itself"))
				continue
			}
			if err := po.CheckAccount(ctx, l.AccountID, at); err != nil {
				var ae *apperr.Error
				if asApp(err, &ae) && len(ae.Fields) > 0 {
					lineErrs = append(lineErrs, ae.Fields...)
					continue
				}
				return err
			}
		}
		for i, l := range in.Lines {
			dept, err := po.ResolveDepartment(ctx, l.AccountID, l.DepartmentID, fmt.Sprintf("lines[%d].department_id", i)) // the rule of the account: its default, or a required department
			if err != nil {
				var ae *apperr.Error
				if asApp(err, &ae) && len(ae.Fields) > 0 {
					lineErrs = append(lineErrs, ae.Fields...)
					continue
				}
				return err
			}
			in.Lines[i].DepartmentID = dept
		}
		if len(lineErrs) > 0 {
			return apperr.Invalid("the bill is invalid", lineErrs...)
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqSupplierBill)
		if err != nil {
			return err
		}
		desc := strings.TrimSpace(in.Description)
		jl := make([]accounting.SystemLine, 0, len(in.Lines)+1)
		for _, l := range in.Lines {
			d := strings.TrimSpace(l.Description)
			if d == "" {
				d = "Invoice " + inv + " " + sup.Name
			}
			cost := l.Amount
			if treatment == taxfiling.InputVATExpense {
				cost = cost.Add(l.VATAmount) // the VAT is part of what the purchase cost
			}
			jl = append(jl, accounting.SystemLine{AccountID: l.AccountID, Debit: cost, Description: d, SourceType: "AP_BILL", SourceRef: number, DepartmentID: l.DepartmentID})
			if l.VATAmount.IsPositive() && treatment != taxfiling.InputVATExpense {
				jl = append(jl, accounting.SystemLine{AccountID: inputVAT, Debit: l.VATAmount, Description: "VAT " + d, SourceType: "AP_BILL", SourceRef: number})
			}
		}
		jl = append(jl, accounting.SystemLine{AccountID: payable, Credit: total, Description: "Invoice " + inv + " " + sup.Name, SourceType: "AP_BILL", SourceRef: number})
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Date: in.BillDate, Description: "Bill " + number + " " + sup.Code + " " + inv, Reference: number, Lines: jl})
		if err != nil {
			return err
		}
		id, err := q.InsertBill(ctx, payablesdb.InsertBillParams{
			TenantID: p.TenantID, PropertyID: propertyID, BillNumber: number, SupplierID: in.SupplierID, SupplierInvoiceNumber: inv, BillDate: in.BillDate, DueDate: due,
			Description: nullable(desc), Total: total, JournalID: jid, IdempotencyKey: nullable(key), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for i, l := range in.Lines {
			var lineTreatment *string
			if l.VATAmount.IsPositive() {
				lineTreatment = &treatment
			}
			if err := q.InsertBillLine(ctx, payablesdb.InsertBillLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, BillID: id, LineNo: int32(i + 1), AccountID: l.AccountID, Description: nullable(l.Description), Amount: l.Amount,
				VatAmount: l.VATAmount, VatTreatment: lineTreatment, DepartmentID: l.DepartmentID,
			}); err != nil {
				return err
			}
		}
		*out = id
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.bill_posted", "supplier_bill", id, auditlabel.SupplierBill(ctx, propertyID, id), nil,
			map[string]any{"bill_number": number, "supplier": sup.Code, "invoice": inv, "total": total.String(), "vat": vatOf(in.Lines).String(), "vat_treatment": treatment, "journal": jnum}))
	})
}

// VoidBill voids a bill that has no payments (payables.post plus an approval): its journal is reversed on the current
// business date.
func (s *Service) VoidBill(ctx context.Context, propertyID, id int64, in VoidInput) (Bill, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesPost)
	if err != nil {
		return Bill{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Bill{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Bill{}, err
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
		supplierID, err := q.BillSupplier(ctx, payablesdb.BillSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if isNoRows(err) {
			return errBillNotFound()
		}
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Suppliers, db.ForUpdate, propertyID, []int64{supplierID}); err != nil {
			return err
		}
		b, err := s.loadBill(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if b.Status != "POSTED" {
			return apperr.Conflict("BILL_ALREADY_VOIDED", "the bill is voided already")
		}
		n, err := q.CountLiveAllocations(ctx, payablesdb.CountLiveAllocationsParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: id})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("BILL_HAS_PAYMENTS", "a bill with payments cannot be voided: void its payments first").WithContext("payments", n)
		}
		credits, err := q.CountLiveCreditsOfBill(ctx, payablesdb.CountLiveCreditsOfBillParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: id})
		if err != nil {
			return err
		}
		if credits > 0 {
			return apperr.Conflict("BILL_HAS_CREDIT_NOTES", "a bill with credit notes cannot be voided: void its credit notes first").WithContext("credit_notes", credits)
		}
		by := approval.UserID()
		rj, err := po.Reverse(ctx, b.JournalID, day.BusinessDate, reason, by)
		if err != nil {
			return err
		}
		if err := q.VoidBill(ctx, payablesdb.VoidBillParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, VoidJournalID: &rj, ApprovedBy: &by,
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.bill_voided", "supplier_bill", id, auditlabel.SupplierBill(ctx, propertyID, id),
			map[string]any{"status": "POSTED"}, map[string]any{"status": "VOIDED", "reason": reason, "approved_by": by, "bill_number": b.Number}))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p.TenantID, propertyID, id)
}

// OpenBills lists what is still owed on the bills of a supplier, oldest due date first, for choosing what to pay
// (payables.view).
func (s *Service) OpenBills(ctx context.Context, propertyID, supplierID int64) ([]OpenBill, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return nil, err
	}
	if _, err := s.loadSupplier(ctx, p.TenantID, propertyID, supplierID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).OpenBillsOfSupplier(ctx, payablesdb.OpenBillsOfSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, SupplierID: supplierID})
	if err != nil {
		return nil, err
	}
	out := make([]OpenBill, 0, len(rows))
	for _, r := range rows {
		if r.Outstanding.IsPositive() {
			out = append(out, OpenBill{BillID: r.ID, BillNumber: r.BillNumber, SupplierInvoiceNumber: r.SupplierInvoiceNumber, BillDate: r.BillDate, DueDate: r.DueDate, Total: r.Total, Outstanding: r.Outstanding})
		}
	}
	return out, nil
}

// vatOf is the VAT paid on the lines of a bill.
func vatOf(lines []BillLineInput) decimal.Decimal {
	v := decimal.Zero
	for _, l := range lines {
		v = v.Add(l.VATAmount)
	}
	return v
}
