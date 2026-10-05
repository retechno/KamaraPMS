package payables

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/payables/payablesdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/tenancy"
)

// Credit notes of suppliers (design: docs/architecture/14-supplier-credit-notes.md). A supplier gives back part of a bill after the fact. The credit note is made against the bill:
// each of its lines credits a line of the bill and takes that line's account, department and VAT treatment, so what is credited is booked where it was booked and the VAT comes back the way it
// went in. The journal is the mirror of the bill (Dr payables; Cr the expense, Cr input VAT). It takes what it credits off the bill as far as the bill still owes; the rest is a credit of the
// supplier that applications later take off other bills. No money moves: a refund from the supplier is not part of this.

func toCreditNote(r payablesdb.ListCreditNotesRow) CreditNote {
	c := CreditNote{
		ID: r.ID, Number: r.CreditNumber, SupplierID: r.SupplierID, SupplierCode: r.SupplierCode, SupplierName: r.SupplierName, BillID: r.BillID, BillNumber: r.BillNumber,
		SupplierInvoice: r.SupplierInvoiceNumber, SupplierCreditNumber: r.SupplierCreditNumber, CreditDate: r.CreditDate, Reason: r.Reason, Total: r.Total, Applied: r.Applied,
		Status: r.Status, JournalID: r.JournalID, JournalNumber: r.JournalNumber, VoidJournalID: r.VoidJournalID, VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason), CreatedAt: r.CreatedAt,
	}
	if r.Status == CreditPosted {
		c.Unapplied = r.Total.Sub(r.Applied)
	} else {
		c.Applied = decimal.Zero
	}
	return c
}

func errCreditNotFound() *apperr.Error {
	return apperr.NotFound("CREDIT_NOTE_NOT_FOUND", "the credit note does not exist in this property")
}

// CreditNotes lists the credit notes of suppliers, newest first (payables.view).
func (s *Service) CreditNotes(ctx context.Context, propertyID int64, f CreditFilter) ([]CreditNote, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return nil, err
	}
	return s.listCredits(ctx, p.TenantID, propertyID, nil, f)
}

func (s *Service) listCredits(ctx context.Context, tenantID, propertyID int64, id *int64, f CreditFilter) ([]CreditNote, error) {
	rows, err := s.q(ctx).ListCreditNotes(ctx, payablesdb.ListCreditNotesParams{
		TenantID: tenantID, PropertyID: propertyID, ID: id, SupplierID: f.SupplierID, BillID: f.BillID, Status: nullable(f.Status), FromDate: f.From, ToDate: f.To,
		UnappliedOnly: f.UnappliedOnly, Q: nullable(f.Q), RowLimit: limitOf(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]CreditNote, 0, len(rows))
	for _, r := range rows {
		out = append(out, toCreditNote(r))
	}
	return out, nil
}

// GetCreditNote is one credit note with its lines and where it was applied (payables.view).
func (s *Service) GetCreditNote(ctx context.Context, propertyID, id int64) (CreditNote, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return CreditNote{}, err
	}
	return s.loadCredit(ctx, p.TenantID, propertyID, id)
}

func (s *Service) loadCredit(ctx context.Context, tenantID, propertyID, id int64) (CreditNote, error) {
	list, err := s.listCredits(ctx, tenantID, propertyID, &id, CreditFilter{Limit: 1})
	if err != nil {
		return CreditNote{}, err
	}
	if len(list) == 0 {
		return CreditNote{}, errCreditNotFound()
	}
	c := list[0]
	q := s.q(ctx)
	lines, err := q.ListCreditLines(ctx, payablesdb.ListCreditLinesParams{TenantID: tenantID, PropertyID: propertyID, CreditID: id})
	if err != nil {
		return CreditNote{}, err
	}
	c.Lines = make([]CreditLine, 0, len(lines))
	for _, l := range lines {
		c.Lines = append(c.Lines, CreditLine{LineNo: l.LineNo, BillLineNo: l.BillLineNo, AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Description: deref(l.Description),
			Amount: l.Amount, VATAmount: l.VatAmount, VATTreatment: deref(l.VatTreatment), DepartmentID: l.DepartmentID, DepartmentCode: deref(l.DepartmentCode), DepartmentName: deref(l.DepartmentName)})
	}
	allocs, err := q.ListCreditAllocations(ctx, payablesdb.ListCreditAllocationsParams{TenantID: tenantID, PropertyID: propertyID, CreditID: id})
	if err != nil {
		return CreditNote{}, err
	}
	c.Allocations = make([]CreditAllocation, 0, len(allocs))
	for _, a := range allocs {
		c.Allocations = append(c.Allocations, CreditAllocation{BillID: a.BillID, BillNumber: a.BillNumber, SupplierInvoiceNumber: a.SupplierInvoiceNumber, Amount: a.Amount, AppliedOn: a.AppliedOn})
	}
	return c, nil
}

func validateCredit(in CreditNoteInput, decimals int32) (decimal.Decimal, []apperr.FieldError) {
	var fields []apperr.FieldError
	num := strings.TrimSpace(in.SupplierCreditNumber)
	switch {
	case num == "":
		fields = append(fields, fieldErr("supplier_credit_number", "REQUIRED", "the number on the supplier's credit note"))
	case len([]rune(num)) > 60:
		fields = append(fields, fieldErr("supplier_credit_number", "TOO_LONG", "at most 60 characters"))
	}
	if r := strings.TrimSpace(in.Reason); r == "" || len([]rune(r)) > 500 {
		fields = append(fields, fieldErr("reason", "REQUIRED", "why the supplier gave the credit, up to 500 characters"))
	}
	if in.BillID < 1 {
		fields = append(fields, fieldErr("bill_id", "REQUIRED", "choose the bill it credits"))
	}
	if in.CreditDate.IsZero() {
		fields = append(fields, fieldErr("credit_date", "REQUIRED", "the date of the credit note"))
	}
	if len(in.Lines) < 1 || len(in.Lines) > maxBillLines {
		fields = append(fields, fieldErr("lines", "INVALID_COUNT", "between 1 and 100 lines"))
	}
	total := decimal.Zero
	seen := map[int32]bool{}
	for i, l := range in.Lines {
		at := func(f string) string { return fmt.Sprintf("lines[%d].%s", i, f) }
		switch {
		case l.BillLineNo < 1:
			fields = append(fields, fieldErr(at("bill_line_no"), "REQUIRED", "the line of the bill it credits"))
		case seen[l.BillLineNo]:
			fields = append(fields, fieldErr(at("bill_line_no"), "DUPLICATE", "a line of the bill is credited once in a credit note"))
		}
		seen[l.BillLineNo] = true
		switch {
		case l.Amount.IsNegative():
			fields = append(fields, fieldErr(at("amount"), "NEGATIVE", "zero or more"))
		case !l.Amount.Equal(l.Amount.Round(decimals)):
			fields = append(fields, fieldErr(at("amount"), "TOO_PRECISE", fmt.Sprintf("at most %d decimals", decimals)))
		}
		switch {
		case l.VATAmount.IsNegative():
			fields = append(fields, fieldErr(at("vat_amount"), "NEGATIVE", "zero or more"))
		case !l.VATAmount.Equal(l.VATAmount.Round(decimals)):
			fields = append(fields, fieldErr(at("vat_amount"), "TOO_PRECISE", fmt.Sprintf("at most %d decimals", decimals)))
		}
		if !l.Amount.Add(l.VATAmount).IsPositive() && !l.Amount.IsNegative() && !l.VATAmount.IsNegative() {
			fields = append(fields, fieldErr(at("amount"), "NOT_POSITIVE", "an amount or a VAT above zero"))
		}
		if len([]rune(l.Description)) > 300 {
			fields = append(fields, fieldErr(at("description"), "TOO_LONG", "at most 300 characters"))
		}
		total = total.Add(l.Amount).Add(l.VATAmount)
	}
	return total, fields
}

// PostCreditNote enters the credit note a supplier gave on a bill (payables.post): its journal credits the accounts of the bill lines it credits (and input VAT) against ACCOUNTS PAYABLE on the
// credit date. The Idempotency-Key makes a retry return the first credit note.
func (s *Service) PostCreditNote(ctx context.Context, propertyID int64, in CreditNoteInput, key string) (CreditNote, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesPost)
	if err != nil {
		return CreditNote{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return CreditNote{}, err
	}
	total, fields := validateCredit(in, prop.CurrencyDecimals)
	if len(fields) > 0 {
		return CreditNote{}, apperr.Invalid("the credit note is invalid", fields...)
	}
	var id int64
	err = retryOnDuplicate(key, func() error { return s.postCredit(ctx, p, propertyID, in, total, key, &id) })
	if err != nil {
		return CreditNote{}, err
	}
	return s.loadCredit(ctx, p.TenantID, propertyID, id)
}

func (s *Service) postCredit(ctx context.Context, p auth.Principal, propertyID int64, in CreditNoteInput, total decimal.Decimal, key string, out *int64) error {
	num := strings.TrimSpace(in.SupplierCreditNumber)
	reason := strings.TrimSpace(in.Reason)
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
		supplierID, err := q.BillSupplier(ctx, payablesdb.BillSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.BillID})
		if isNoRows(err) {
			return apperr.Invalid("the credit note is invalid", fieldErr("bill_id", "NOT_FOUND", "no such bill in this property"))
		}
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Suppliers, db.ForUpdate, propertyID, []int64{supplierID}); err != nil {
			return err
		}
		if key != "" {
			if prev, err := q.FindCreditByKey(ctx, payablesdb.FindCreditByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
				*out = prev
				return nil
			} else if !isNoRows(err) {
				return err
			}
		}
		b, err := s.loadBill(ctx, p.TenantID, propertyID, in.BillID)
		if err != nil {
			return err
		}
		if b.Status != "POSTED" {
			return apperr.Conflict("BILL_ALREADY_VOIDED", "a voided bill takes no credit note")
		}
		sup, err := s.loadSupplier(ctx, p.TenantID, propertyID, supplierID)
		if err != nil {
			return err
		}
		if in.CreditDate.Before(b.BillDate) {
			return apperr.Invalid("the credit note is invalid", fieldErr("credit_date", "BEFORE_BILL_DATE", "not before the bill date"))
		}
		if err := po.CheckDate(ctx, in.CreditDate, "credit_date"); err != nil {
			return err
		}
		// what the credit notes that stand have credited of each line of the bill already
		done, err := q.CreditedOfBillLines(ctx, payablesdb.CreditedOfBillLinesParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: in.BillID})
		if err != nil {
			return err
		}
		doneAmount, doneVAT := map[int32]decimal.Decimal{}, map[int32]decimal.Decimal{}
		for _, d := range done {
			doneAmount[d.BillLineNo], doneVAT[d.BillLineNo] = d.Amount, d.Vat
		}
		billLine := map[int32]BillLine{}
		for _, l := range b.Lines {
			billLine[l.LineNo] = l
		}
		payable, err := po.SystemAccount(ctx, accounting.KeyAccountsPayable)
		if err != nil {
			return err
		}
		var lineErrs []apperr.FieldError
		var inputVAT int64
		depts := make([]*int64, len(in.Lines))
		for i, l := range in.Lines {
			at := func(f string) string { return fmt.Sprintf("lines[%d].%s", i, f) }
			bl, ok := billLine[l.BillLineNo]
			if !ok {
				lineErrs = append(lineErrs, fieldErr(at("bill_line_no"), "NOT_FOUND", "no such line on the bill"))
				continue
			}
			if left := bl.Amount.Sub(doneAmount[l.BillLineNo]); l.Amount.GreaterThan(left) {
				lineErrs = append(lineErrs, fieldErr(at("amount"), "EXCEEDS_BILL_LINE", "more than the "+left.String()+" the bill line has left to credit"))
			}
			if left := bl.VATAmount.Sub(doneVAT[l.BillLineNo]); l.VATAmount.GreaterThan(left) {
				lineErrs = append(lineErrs, fieldErr(at("vat_amount"), "EXCEEDS_BILL_LINE", "more than the "+left.String()+" of VAT the bill line has left to credit"))
			}
			if err := po.CheckAccount(ctx, bl.AccountID, at("bill_line_no")); err != nil {
				var ae *apperr.Error
				if asApp(err, &ae) && len(ae.Fields) > 0 {
					lineErrs = append(lineErrs, ae.Fields...)
					continue
				}
				return err
			}
			d, err := po.ResolveDepartment(ctx, bl.AccountID, bl.DepartmentID, at("department_id"))
			if err != nil {
				var ae *apperr.Error
				if asApp(err, &ae) && len(ae.Fields) > 0 {
					lineErrs = append(lineErrs, ae.Fields...)
					continue
				}
				return err
			}
			depts[i] = d
			if l.VATAmount.IsPositive() && bl.VATTreatment != taxfiling.InputVATExpense && inputVAT == 0 {
				if inputVAT, err = po.SystemAccount(ctx, accounting.KeyInputVAT); err != nil {
					return err
				}
			}
		}
		if len(lineErrs) > 0 {
			return apperr.Invalid("the credit note is invalid", lineErrs...)
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqSupplierCredit)
		if err != nil {
			return err
		}
		jl := make([]accounting.SystemLine, 0, len(in.Lines)+1)
		for i, l := range in.Lines {
			bl := billLine[l.BillLineNo]
			d := strings.TrimSpace(l.Description)
			if d == "" {
				d = "Credit note " + num + " " + sup.Name
			}
			credit := l.Amount
			if bl.VATTreatment == taxfiling.InputVATExpense {
				credit = credit.Add(l.VATAmount) // the VAT was part of what the purchase cost
			}
			if credit.IsPositive() {
				jl = append(jl, accounting.SystemLine{AccountID: bl.AccountID, Credit: credit, Description: d, SourceType: "AP_CREDIT", SourceRef: number, DepartmentID: depts[i]})
			}
			if l.VATAmount.IsPositive() && bl.VATTreatment != taxfiling.InputVATExpense {
				jl = append(jl, accounting.SystemLine{AccountID: inputVAT, Credit: l.VATAmount, Description: "VAT " + d, SourceType: "AP_CREDIT", SourceRef: number})
			}
		}
		jl = append(jl, accounting.SystemLine{AccountID: payable, Debit: total, Description: "Credit note " + num + " " + sup.Name, SourceType: "AP_CREDIT", SourceRef: number})
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Date: in.CreditDate, Description: "Credit note " + number + " " + sup.Code + " " + num, Reference: number, Lines: jl})
		if err != nil {
			return err
		}
		id, err := q.InsertCreditNote(ctx, payablesdb.InsertCreditNoteParams{
			TenantID: p.TenantID, PropertyID: propertyID, CreditNumber: number, SupplierID: supplierID, BillID: in.BillID, SupplierCreditNumber: num, CreditDate: in.CreditDate,
			Reason: reason, Total: total, JournalID: jid, IdempotencyKey: nullable(key), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for i, l := range in.Lines {
			bl := billLine[l.BillLineNo]
			var treatment *string
			if l.VATAmount.IsPositive() {
				treatment = &bl.VATTreatment
			}
			if err := q.InsertCreditLine(ctx, payablesdb.InsertCreditLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, CreditID: id, LineNo: int32(i + 1), BillID: in.BillID, BillLineNo: l.BillLineNo, AccountID: bl.AccountID,
				Description: nullable(l.Description), Amount: l.Amount, VatAmount: l.VATAmount, VatTreatment: treatment, DepartmentID: depts[i],
			}); err != nil {
				return err
			}
		}
		// it takes off the bill as much as the bill still owes; the rest stays a credit of the supplier
		applied := decimal.Min(total, b.Outstanding)
		if applied.IsPositive() {
			if err := q.InsertCreditAllocation(ctx, payablesdb.InsertCreditAllocationParams{
				TenantID: p.TenantID, PropertyID: propertyID, SupplierID: supplierID, CreditID: id, BillID: in.BillID, Amount: applied, AppliedOn: in.CreditDate, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
		}
		*out = id
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.credit_note_posted", "supplier_credit_note", id, nil,
			map[string]any{"credit_number": number, "supplier": sup.Code, "bill": b.Number, "supplier_credit_number": num, "total": total.String(), "applied": applied.String(), "journal": jnum}))
	})
}

// ApplyCredit takes what is left of a credit note off open bills of the same supplier (payables.post). It posts no journal: the credit note already reduced what is owed to the supplier.
func (s *Service) ApplyCredit(ctx context.Context, propertyID, id int64, in ApplyCreditInput) (CreditNote, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesPost)
	if err != nil {
		return CreditNote{}, err
	}
	if len(in.Allocations) < 1 || len(in.Allocations) > maxAllocations {
		return CreditNote{}, apperr.Invalid("the application is invalid", fieldErr("allocations", "INVALID_COUNT", "between 1 and 100 bills"))
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		supplierID, err := q.CreditSupplier(ctx, payablesdb.CreditSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if isNoRows(err) {
			return errCreditNotFound()
		}
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Suppliers, db.ForUpdate, propertyID, []int64{supplierID}); err != nil {
			return err
		}
		c, err := s.loadCredit(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if c.Status != CreditPosted {
			return apperr.Conflict("CREDIT_NOTE_VOIDED", "a voided credit note takes nothing off a bill")
		}
		open, err := q.OpenBillsOfSupplier(ctx, payablesdb.OpenBillsOfSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, SupplierID: supplierID})
		if err != nil {
			return err
		}
		left := map[int64]decimal.Decimal{}
		for _, b := range open {
			left[b.ID] = b.Outstanding
		}
		sum := decimal.Zero
		var errs []apperr.FieldError
		for i, a := range in.Allocations {
			at := func(f string) string { return fmt.Sprintf("allocations[%d].%s", i, f) }
			o, ok := left[a.BillID]
			switch {
			case !a.Amount.IsPositive():
				errs = append(errs, fieldErr(at("amount"), "NOT_POSITIVE", "an amount above zero"))
			case !ok:
				errs = append(errs, fieldErr(at("bill_id"), "NOT_PAYABLE", "not an open bill of this supplier"))
			case a.Amount.GreaterThan(o):
				errs = append(errs, fieldErr(at("amount"), "EXCEEDS_OUTSTANDING", "more than the "+o.String()+" still owed on the bill"))
			default:
				left[a.BillID] = o.Sub(a.Amount) // a bill named twice is checked against what is left of it
			}
			sum = sum.Add(a.Amount)
		}
		if len(errs) > 0 {
			e := apperr.Conflict("ALLOCATION_EXCEEDS_OUTSTANDING", "the credit settles more than is owed")
			e.Fields = errs
			return e
		}
		if sum.GreaterThan(c.Unapplied) {
			return apperr.Conflict("CREDIT_EXCEEDS_UNAPPLIED", "more than the credit note has left to apply").WithContext("unapplied", c.Unapplied.String())
		}
		for _, a := range in.Allocations {
			if err := q.InsertCreditAllocation(ctx, payablesdb.InsertCreditAllocationParams{
				TenantID: p.TenantID, PropertyID: propertyID, SupplierID: supplierID, CreditID: id, BillID: a.BillID, Amount: a.Amount, AppliedOn: day.BusinessDate, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.credit_note_applied", "supplier_credit_note", id, nil,
			map[string]any{"credit_number": c.Number, "applied": sum.String(), "bills": len(in.Allocations)}))
	})
	if err != nil {
		return CreditNote{}, err
	}
	return s.loadCredit(ctx, p.TenantID, propertyID, id)
}

// VoidCreditNote voids a credit note (payables.post plus an approval): its journal is reversed on the current business date, the bills it was taken off owe that much again, and the VAT it took back
// off a return that had claimed it is given back on the next return. A credit note can always be voided: it only makes more owed, never less.
func (s *Service) VoidCreditNote(ctx context.Context, propertyID, id int64, in VoidInput) (CreditNote, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesPost)
	if err != nil {
		return CreditNote{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return CreditNote{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return CreditNote{}, err
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
		supplierID, err := q.CreditSupplier(ctx, payablesdb.CreditSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if isNoRows(err) {
			return errCreditNotFound()
		}
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Suppliers, db.ForUpdate, propertyID, []int64{supplierID}); err != nil {
			return err
		}
		c, err := s.loadCredit(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if c.Status != CreditPosted {
			return apperr.Conflict("CREDIT_NOTE_ALREADY_VOIDED", "the credit note is voided already")
		}
		by := approval.UserID()
		rj, err := po.Reverse(ctx, c.JournalID, day.BusinessDate, reason, by)
		if err != nil {
			return err
		}
		if err := q.VoidCreditNote(ctx, payablesdb.VoidCreditNoteParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, VoidJournalID: &rj, ApprovedBy: &by,
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.credit_note_voided", "supplier_credit_note", id,
			map[string]any{"status": "POSTED"}, map[string]any{"status": "VOIDED", "reason": reason, "approved_by": by, "credit_number": c.Number}))
	})
	if err != nil {
		return CreditNote{}, err
	}
	return s.loadCredit(ctx, p.TenantID, propertyID, id)
}

// agingCredits adds the credit of each supplier that no bill has taken off as of the date of the aging.
func (s *Service) agingCredits(ctx context.Context, tenantID, propertyID int64, asOf civil.Date, out *Aging) error {
	rows, err := s.q(ctx).UnappliedCreditRows(ctx, payablesdb.UnappliedCreditRowsParams{TenantID: tenantID, PropertyID: propertyID, AsOf: asOf})
	if err != nil {
		return err
	}
	idx := map[int64]int{}
	for i, sp := range out.Suppliers {
		idx[sp.SupplierID] = i
	}
	for _, r := range rows {
		if !r.Unapplied.IsPositive() {
			continue
		}
		i, ok := idx[r.SupplierID]
		if !ok {
			i = len(out.Suppliers)
			idx[r.SupplierID] = i
			zero := map[string]decimal.Decimal{}
			for _, b := range agingBuckets {
				zero[b] = decimal.Zero
			}
			out.Suppliers = append(out.Suppliers, AgingSupplier{SupplierID: r.SupplierID, SupplierCode: r.SupplierCode, SupplierName: r.SupplierName, Buckets: zero, Bills: []AgingBill{}, Credits: []AgingCredit{}})
		}
		sup := &out.Suppliers[i]
		sup.UnappliedCredit = sup.UnappliedCredit.Add(r.Unapplied)
		sup.Credits = append(sup.Credits, AgingCredit{CreditID: r.ID, CreditNumber: r.CreditNumber, SupplierCreditNumber: r.SupplierCreditNumber, CreditDate: r.CreditDate, Unapplied: r.Unapplied})
		out.UnappliedCredit = out.UnappliedCredit.Add(r.Unapplied)
	}
	for i := range out.Suppliers {
		out.Suppliers[i].Net = out.Suppliers[i].Total.Sub(out.Suppliers[i].UnappliedCredit)
	}
	out.Net = out.Total.Sub(out.UnappliedCredit)
	return nil
}
