# 14. Supplier credit notes

Status: **built** (migration 00055, `internal/payables/credits.go`). The owner decided on 2026-10-05: (1) a credit note is made against a bill, and what the bill cannot take stays as a credit of the supplier that is applied to
other bills; (2) the VAT of a credit note follows the treatment of the bill line it credits; (3) a refund of money from the supplier is not part of this (the credit stays with the supplier).

## What it is

A supplier gives back part of a bill after the fact: returned goods, a discount after the invoice. The hotel owes less, the expense is lower, and the input VAT that was paid on it comes back. It is the mirror of a bill.

- **Against a bill.** `supplier_credit_notes.bill_id` names the bill. Each line (`supplier_credit_note_lines`) names a line of that bill (`bill_line_no`) and an amount and the VAT on it. The account, the department and the VAT
  treatment (CREDITABLE, EXPENSE, DEFERRED) are those of the bill line, frozen on the bill date, so what is credited is booked where it was booked and the VAT comes back the way it went in. A line cannot credit more than
  the bill line has left (amount and VAT apart; 422 `EXCEEDS_BILL_LINE`), counting the credit notes that stand.
- **The journal** (type PAYABLES, dated the credit date; not before the bill date, not after the business date, in an open month) is the mirror of the bill: Dr `ACCOUNTS_PAYABLE` for the total; Cr the account of each line
  (with the VAT when the treatment was EXPENSE); Cr input VAT (1425) for CREDITABLE and DEFERRED VAT. The department rule of the accounts applies like for any posting (`DEPARTMENT_REQUIRED`).
- **What it takes off.** When the credit note is made, an allocation (`supplier_credit_allocations`) takes off the bill as much as the bill still owes. What is left (`unapplied`) is a credit of the supplier:
  `POST credit-notes/{id}/apply` takes it off other open bills of the same supplier, any time, with no journal (the payables account already went down). An allocation records the business date it was made on, so the aging of a past date reproduces what was owed then.
- **Where it shows.** A bill has `credited` beside `paid` (`outstanding` = total - paid - credited). A supplier's `outstanding` is the bills less the payments and the credit notes (negative when the supplier owes a credit) and
  `unapplied_credit` is the part no bill has taken off. The payables aging lists the open bills as before and adds the credits of each supplier (`unapplied_credit`, `credits`, and `net` = total less the credit), as of the date asked.
- **Entering it once.** The same supplier credit note number of a supplier is entered once (409 `DUPLICATE_CREDIT_NOTE`; a voided one may be entered again). The `Idempotency-Key` header makes a retry return the first.
- **Voiding.** `POST credit-notes/{id}/void` (payables.post and an approval): the journal is reversed on the current business date, the allocations stop counting (the bills owe that much again) and the VAT it took back off a return is given back (below).
  A credit note can always be voided: it only makes more owed. A bill with credit notes (or credit applied to it) cannot be voided before they are (409 `BILL_HAS_CREDIT_NOTES`).
- **Permissions.** Reading is `payables.view`, entering, applying and voiding `payables.post` (a void also needs an approval); no new permission.
- **Locking.** The supplier row (lock level of the suppliers) is taken FOR UPDATE, after the accounting settings, as for bills and payments.

## The VAT return

A credit note of a supplier takes input VAT back like a bill claims it, with the sign changed (`tax_return_input_claims`, extended in 00055 with `credit_id` and `credit_line_no`; a claim names a bill line or a credit note line, never both):

- a line whose VAT was **CREDITABLE** is claimed as a **negative** amount on the return of the month of the credit date (or the first open month after it, like a late bill), so the VAT claimed for the month goes down; if the month has only credit notes, the input VAT claimed is negative and the offset is reduced (as for a bill voided after it was claimed);
- a credit note **voided after a return claimed it** is given back as a **positive** claim that reverses it, on the next return that is not filed; voided before any return claimed it, it is never claimed;
- EXPENSE lines have no VAT account and no claim; DEFERRED VAT comes back to account 1425 and is not claimed (it was not claimed when the bill was entered either).
- A worksheet and a return show each claim with its `source` (`BILL`, `CREDIT_NOTE` or, for the VAT on a card commission, `SETTLEMENT`, see `15-card-fee-vat.md`); for a credit note claim `bill_number`, `supplier_invoice_number` and `bill_date` are the number of the credit note, the number on the supplier's credit note and its date.

## Schema (00055)

`supplier_credit_notes` (header, total, status, journal, void columns, `supplier_credit_number` unique per supplier while POSTED), `supplier_credit_note_lines` (composite foreign key to the bill line), `supplier_credit_allocations`
(append-only, `applied_on`). Triggers: a credit note changes only from POSTED to VOIDED, lines and allocations never change, nothing is deleted or truncated. Deferred checks at commit: the lines add up to the total, and no more than the total is allocated.
New document sequence `SUPPLIER_CREDIT` (`SCN000001`). Constraint names are mapped to error codes in `internal/platform/db/errors.go`.

## API

`GET/POST {P}/payables/credit-notes` (filters `supplier_id`, `bill_id`, `status`, `from`, `to`, `q`, `unapplied_only`), `GET {P}/payables/credit-notes/{id}`, `POST .../{id}/apply`, `POST .../{id}/void`. Codes: 409 `DUPLICATE_CREDIT_NOTE`,
`BILL_ALREADY_VOIDED`, `ALLOCATION_EXCEEDS_OUTSTANDING`, `CREDIT_EXCEEDS_UNAPPLIED`, `CREDIT_NOTE_VOIDED`, `CREDIT_NOTE_ALREADY_VOIDED`, `BILL_HAS_CREDIT_NOTES`; 404 `CREDIT_NOTE_NOT_FOUND`; 422 `EXCEEDS_BILL_LINE`.

## Frontend

Finance, Payables, Supplier credit notes: the list (filters, the lines and applications of a credit note on demand, apply and void), and the form to enter one against a bill (opened from the bill with "Credit note…" or from the page: the lines of the bill with an amount and a VAT to credit).
The bill detail shows what was credited; the supplier list and the aging show the credit no bill has taken off; a tax return names the credit note claims.

## Not built

A refund received from the supplier (cash or bank, with a journal and bank matching); a credit note without a bill (a rebate on account); a PDF of the credit note; the allocation of one credit note across the bills of several suppliers (never).
