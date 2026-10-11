-- Read-only lookups of the identifier an audit entry is about, for a use case that has only the id of the thing (see package auditlabel). Plain SELECTs: no FOR UPDATE, no lock.

-- name: RoomNumber :one
SELECT room_number FROM rooms WHERE property_id = @property_id AND id = @id;

-- name: ReservationNumber :one
SELECT confirmation_number FROM reservations WHERE property_id = @property_id AND id = @id;

-- name: ReservationNumberOfLine :one
SELECT r.confirmation_number FROM reservation_rooms l JOIN reservations r ON r.id = l.reservation_id
WHERE l.property_id = @property_id AND l.id = @id;

-- name: StayNumber :one
SELECT stay_number FROM stays WHERE property_id = @property_id AND id = @id;

-- name: FolioNumber :one
SELECT folio_number FROM folios WHERE property_id = @property_id AND id = @id;

-- A folio line has no number of its own: it is named by the folio it is on.
-- name: FolioNumberOfItem :one
SELECT f.folio_number FROM folio_items i JOIN folios f ON f.id = i.folio_id
WHERE i.property_id = @property_id AND i.id = @id;

-- name: TaskRoomNumber :one
SELECT r.room_number FROM housekeeping_tasks t JOIN rooms r ON r.id = t.room_id
WHERE t.property_id = @property_id AND t.id = @id;

-- name: PropertyCode :one
SELECT code FROM properties WHERE id = @id;

-- name: RatePlanCode :one
SELECT code FROM rate_plans WHERE property_id = @property_id AND id = @id;

-- name: BankStatementPeriod :one
SELECT a.name AS account_name, s.period_from, s.period_to
FROM bank_statements s JOIN bank_accounts a ON a.id = s.bank_account_id
WHERE s.property_id = @property_id AND s.id = @id;

-- name: BudgetName :one
SELECT name FROM budgets WHERE property_id = @property_id AND id = @id;

-- name: PaymentNumber :one
SELECT payment_number FROM payments WHERE property_id = @property_id AND id = @id;

-- name: ShiftNumber :one
SELECT shift_number FROM cashier_shifts WHERE property_id = @property_id AND id = @id;

-- name: GLAccountCode :one
SELECT code FROM gl_accounts WHERE property_id = @property_id AND id = @id;

-- name: JournalNumber :one
SELECT journal_number FROM gl_journals WHERE property_id = @property_id AND id = @id;

-- name: SupplierBillNumber :one
SELECT bill_number FROM supplier_bills WHERE property_id = @property_id AND id = @id;

-- name: SupplierCreditNumber :one
SELECT credit_number FROM supplier_credit_notes WHERE property_id = @property_id AND id = @id;

-- name: SupplierPaymentNumber :one
SELECT payment_number FROM supplier_payments WHERE property_id = @property_id AND id = @id;

-- name: CityLedgerInvoiceNumber :one
SELECT invoice_number FROM city_ledger_invoices WHERE property_id = @property_id AND id = @id;

-- name: CityLedgerReceiptNumber :one
SELECT receipt_number FROM city_ledger_receipts WHERE property_id = @property_id AND id = @id;

-- name: CityLedgerAdjustmentNumber :one
SELECT adjustment_number FROM city_ledger_adjustments WHERE property_id = @property_id AND id = @id;

-- name: TaxInvoiceRef :one
SELECT invoice_ref FROM tax_invoices WHERE property_id = @property_id AND id = @id;

-- name: TaxReturnNumber :one
SELECT return_number FROM tax_returns WHERE property_id = @property_id AND id = @id;

-- name: TaxPaymentNumber :one
SELECT payment_number FROM tax_payments WHERE property_id = @property_id AND id = @id;

-- name: TaxCodeOfProfile :one
SELECT t.code FROM tax_filing_profiles p JOIN taxes t ON t.property_id = p.property_id AND t.id = p.tax_id WHERE p.property_id = @property_id AND p.id = @id;

-- name: TaxCodeOfOpeningCredit :one
SELECT t.code FROM tax_opening_credits c JOIN taxes t ON t.property_id = c.property_id AND t.id = c.tax_id WHERE c.property_id = @property_id AND c.id = @id;

-- name: BankAccountName :one
SELECT name FROM bank_accounts WHERE property_id = @property_id AND id = @id;

-- name: SupplierCode :one
SELECT code FROM suppliers WHERE property_id = @property_id AND id = @id;

-- A user of the tenant is named by the e-mail (staff, not a guest).
-- name: UserEmail :one
SELECT email FROM users WHERE tenant_id = @tenant_id AND id = @id;
