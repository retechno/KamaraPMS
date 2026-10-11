// Package auditlabel looks up the identifier an audit entry is about when the use case that writes the entry has only the id of the thing (a room block knows its room by id, a
// folio item knows its item, a stay knows its stay by id). The use case that already holds the row passes its number to the writer itself and does not come here.
//
// Every lookup is a plain SELECT in the ambient transaction: no FOR UPDATE and no lock, so the global lock order is not touched. A thing that is not found, or a lookup that fails,
// gives an empty label (the entry is then written without one); a failed statement in a transaction fails the use case at its next statement anyway, so nothing is hidden by it.
package auditlabel

import (
	"context"
	"fmt"

	"kamarapms/internal/auditlabel/auditlabeldb"
	"kamarapms/internal/platform/db"
)

func queries(ctx context.Context) (*auditlabeldb.Queries, bool) {
	tx, err := db.Tx(ctx)
	if err != nil {
		return nil, false
	}
	return auditlabeldb.New(tx), true
}

// Room is the number of a room ("305").
func Room(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.RoomNumber(ctx, auditlabeldb.RoomNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// Reservation is the confirmation number of a reservation ("RES000012").
func Reservation(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.ReservationNumber(ctx, auditlabeldb.ReservationNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// ReservationOfLine is the confirmation number of the reservation a reservation room (a line) belongs to.
func ReservationOfLine(ctx context.Context, propertyID, lineID int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.ReservationNumberOfLine(ctx, auditlabeldb.ReservationNumberOfLineParams{PropertyID: propertyID, ID: lineID})
	if err != nil {
		return ""
	}
	return v
}

// Stay is the number of a stay ("STY000035").
func Stay(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.StayNumber(ctx, auditlabeldb.StayNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// Folio is the number of a folio ("FOL000026").
func Folio(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.FolioNumber(ctx, auditlabeldb.FolioNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// FolioOfItem is the number of the folio a folio line is on: the line has no number of its own.
func FolioOfItem(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.FolioNumberOfItem(ctx, auditlabeldb.FolioNumberOfItemParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// TaskRoom is the number of the room a housekeeping task is for (a task has no number of its own).
func TaskRoom(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.TaskRoomNumber(ctx, auditlabeldb.TaskRoomNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// Property is the code of a property ("BALI"), for an entry about the property as a whole.
func Property(ctx context.Context, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.PropertyCode(ctx, id)
	if err != nil {
		return ""
	}
	return v
}

// RatePlan is the code of a rate plan.
func RatePlan(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.RatePlanCode(ctx, auditlabeldb.RatePlanCodeParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// BankStatement names a bank statement, which has no number, by its account and period: "BCA 01 Oct 2026 - 31 Oct 2026" is language-neutral as "BCA 2026-10-01..2026-10-31".
func BankStatement(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.BankStatementPeriod(ctx, auditlabeldb.BankStatementPeriodParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s %s..%s", v.AccountName, v.PeriodFrom, v.PeriodTo)
}

// Budget is the name of a budget.
func Budget(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.BudgetName(ctx, auditlabeldb.BudgetNameParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// Payment is the number of a payment ("PAY000032").
func Payment(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.PaymentNumber(ctx, auditlabeldb.PaymentNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// Shift is the number of a cashier shift.
func Shift(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.ShiftNumber(ctx, auditlabeldb.ShiftNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// GLAccount is the code of an account of the chart.
func GLAccount(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.GLAccountCode(ctx, auditlabeldb.GLAccountCodeParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// Journal is the number of a journal.
func Journal(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.JournalNumber(ctx, auditlabeldb.JournalNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// SupplierBill is the number of a supplier bill.
func SupplierBill(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.SupplierBillNumber(ctx, auditlabeldb.SupplierBillNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// SupplierCreditNote is the number of a supplier credit note.
func SupplierCreditNote(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.SupplierCreditNumber(ctx, auditlabeldb.SupplierCreditNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// SupplierPayment is the number of a payment to a supplier.
func SupplierPayment(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.SupplierPaymentNumber(ctx, auditlabeldb.SupplierPaymentNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// CityLedgerInvoice is the number of a city ledger invoice.
func CityLedgerInvoice(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.CityLedgerInvoiceNumber(ctx, auditlabeldb.CityLedgerInvoiceNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// CityLedgerReceipt is the number of a city ledger receipt.
func CityLedgerReceipt(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.CityLedgerReceiptNumber(ctx, auditlabeldb.CityLedgerReceiptNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// CityLedgerAdjustment is the number of a city ledger credit note or write-off.
func CityLedgerAdjustment(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.CityLedgerAdjustmentNumber(ctx, auditlabeldb.CityLedgerAdjustmentNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// TaxInvoice is the reference of a tax invoice.
func TaxInvoice(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.TaxInvoiceRef(ctx, auditlabeldb.TaxInvoiceRefParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// TaxReturn is the number of a tax return.
func TaxReturn(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.TaxReturnNumber(ctx, auditlabeldb.TaxReturnNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// TaxPayment is the number of a tax payment.
func TaxPayment(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.TaxPaymentNumber(ctx, auditlabeldb.TaxPaymentNumberParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// TaxOfProfile is the code of the tax a filing profile is for.
func TaxOfProfile(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.TaxCodeOfProfile(ctx, auditlabeldb.TaxCodeOfProfileParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// TaxOfOpeningCredit is the code of the tax an opening credit is for.
func TaxOfOpeningCredit(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.TaxCodeOfOpeningCredit(ctx, auditlabeldb.TaxCodeOfOpeningCreditParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// BankAccount is the name of a bank account.
func BankAccount(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.BankAccountName(ctx, auditlabeldb.BankAccountNameParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}

// Supplier is the code of a supplier.
func Supplier(ctx context.Context, propertyID, id int64) string {
	q, ok := queries(ctx)
	if !ok {
		return ""
	}
	v, err := q.SupplierCode(ctx, auditlabeldb.SupplierCodeParams{PropertyID: propertyID, ID: id})
	if err != nil {
		return ""
	}
	return v
}
