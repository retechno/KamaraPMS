# Credit notes, write-offs and overdue follow-up (proposal, not approved)

Status: **approved 2026-10-03. Built so far: credit notes (against an invoice or a transfer), write-offs, balances, statement, aging, books, PDF, screens (migration 00045). The tax of a credit note in the return, the books check, the liability report and the tax invoice is built too. The overdue list, the payment reminders (levels 1 to 3, with a PDF letter) and the late fee shown as interest are built too (migration 00046): phase 1 is complete.** Item 2 of the finance order in `08-backlog.md`. Customer side first (the city ledger); the supplier
side follows in a second phase (see "Phase 2" below).

## What exists today (read from the code, 2026-10-03)

- **Receivable of a company** = what folios transferred to it (payments of method CITY_LEDGER) less the receipts it paid; derived, never
  stored. A city ledger invoice is a document that groups transfers; what it has been paid is derived from the allocations of posted
  receipts; an invoice goes `ISSUED -> VOIDED` only. The aging puts the transfers in age buckets, receipts settling the oldest first.
- **Books.** The day close journals the transfers and the receipts against the control account CITY_LEDGER, which only the day close (and no
  other module) posts to; the reconciliation compares its balance with `transferred - received`. The chart has 1240 Allowance for
  doubtful accounts, 6140 Bad debt expense and allowance accounts of revenue (4160, 4290, 4390).
- **Corrections.** A folio item is only reversed or adjusted (with an approval) while the folio is open; a closed folio, and an invoice,
  cannot be changed. So a discount or a dispute settled after the invoice has no home today, nor does a debt that will not be paid.
- **Modules post journals** through `accounting.Poster` (payables, bank, tax), which refuses the control accounts except ACCOUNTS_PAYABLE.
- **The VAT return** reads the output tax from the tax components of the folio items; the tax invoice (step 4 of the PKP work) reads the
  VAT of the folios behind a city ledger invoice.

## Proposal (phase 1: the customer side)

### 1. Credit note (nota kredit) to a company

- Against an `ISSUED` city ledger invoice, or against a transfer not yet on a live invoice (see "Correcting a charge after the folio is closed"). It reduces what the invoice and the account owe. Lines: description, the revenue account
  of the allowance (4160, 4290, ...), the net amount, and optionally a tax of the property with its amount (`net x rate`, rounded to
  the decimals of the currency). The total (net plus tax) is at most what the invoice still owes.
- Posted when it is made, on the business date: Dr the allowance accounts (net), Dr the tax payable account of each tax, Cr CITY_LEDGER (total).
  Journal type new `RECEIVABLES`. It needs an approval and the permission `cityledger.credit_note`; number from a new sequence `CREDIT_NOTE`
  (`CN000001`); audited. A credit note is voided with an approval: its journal is reversed on the business date and the invoice owes
  again.
- **Tax.** A credit note with tax lowers the output tax of the return of its month: the worksheet gets a negative line "credit notes"
  per tax (base and tax), so the lines still add up to the return, and the check against the books counts the debit of the credit note
  journal on the tax payable account. The liability report counts them too. A month whose credit notes exceed what was collected is
  blocked as today ("the tax of the month is negative").
- **Tax invoice.** A credit note with a VAT-kind tax is refused while the invoice has a live tax invoice (`TAX_INVOICE_EXISTS`...): void the
  tax invoice first; the replacement tax invoice then subtracts the credit notes of the invoice (base and VAT of the same rate), so
  the faktur the buyer gets is the reduced one.

### 2. Write-off (penghapusan piutang)

- Of the outstanding of an `ISSUED` invoice, all of it or a part, with a reason, an approval and the permission `cityledger.write_off`.
- Journal: Dr Bad debt expense (6140 by default; any expense account, or 1240 Allowance for doubtful accounts when the hotel provisions
  by a manual journal), Cr CITY_LEDGER. **No tax effect** (relief of the VAT on bad debt is not modeled); a note says so.
- Voided with an approval (reversal journal, the invoice owes again). A receipt that arrives after a write-off is taken on account as
  any receipt; reclassifying it as a recovery is a manual journal.

### 3. Balances, statement, aging, books

- Company balance = transferred - received - credit notes - write-offs. Invoice outstanding = total - receipts allocated - credit notes -
  write-offs, all of the notes and write-offs not voided. An invoice with a live credit note or write-off cannot be voided (as one
  with a receipt cannot today).
- The statement of a company gets the lines CREDIT_NOTE and WRITE_OFF (credit side); the aging treats them like receipts (they settle
  the oldest transfers first); the control account CITY_LEDGER reconciliation subtracts them (new term in the source of the check).
- The Poster gets one narrow exception: a journal may name one control key it is allowed to post to (here CITY_LEDGER), nothing wider.

### 4. Overdue invoices, reminders and interest

- **Overdue list** (`GET city-ledger/overdue`): invoices with an outstanding above zero and a due date before the business date, by
  company: days overdue, bucket, outstanding, last reminder (date and level).
- **Reminder** (surat tagihan): recording that a reminder of level 1, 2 or 3 was sent for a company, with the invoices and what they
  owed that day frozen (`city_ledger_reminders`, append-only), and a PDF letter listing them. No effect on the books.
- **Interest / late fee:** a property setting (percent per month, grace days, off by default) that the overdue list uses to *show* the
  interest due on each invoice. Nothing is posted; issuing a late fee as a charge is left out.

### Schema (one migration)

`city_ledger_credit_notes` and `_lines`, `city_ledger_write_offs`, `city_ledger_reminders` and `_items`; sequences `CREDIT_NOTE` (`CN`) and
`WRITE_OFF` (`WO`); journal type `RECEIVABLES`; the tax worksheet line type for credit notes. Notes and write-offs: composite FKs to
the company and the invoice, amounts above zero, `ISSUED/POSTED -> VOIDED` only (guard trigger), never deleted; a deferred check that
the lines of a note add up to its total; the amount against an invoice cannot pass what it owes (checked under the lock of the company, as a
receipt is). Constraint names go into `internal/platform/db/errors.go`.

### Locks and rules

Business day -> company (the same lock as a receipt) -> accounting settings -> tax. Dated the business date (no backdating), so no closed
period is touched; the period of the tax return is the month of the credit note. Idempotency keys on the creation of notes and
write-offs. Races: two notes against one invoice cannot exceed what it owes; a note and a receipt cannot both settle the same amount.

### API and screens

`POST city-ledger/invoices/{id}/credit-notes`, `GET/POST .../credit-notes/{id}`, `.../void`, `POST .../invoices/{id}/write-offs` (+ void),
`GET city-ledger/overdue`, `POST city-ledger/companies/{id}/reminders` (+ list, PDF), `GET/PUT city-ledger/settings/late-fee`. On the city
ledger account page the invoice rows get "Credit note..." and "Write off..."; a page "Overdue invoices" under City ledger; PDF of the
credit note and of the reminder; the tax worksheet shows the credit note lines.

### Tests and edge cases

Credit note with and without tax (journal, balance, statement, aging, control account), more than is owed, on a voided invoice, a
note and a receipt racing, void and re-credit; a note with tax lowers the return of its month and the tax invoice rule; write-off whole
and part, with the expense and the allowance account, void; an invoice with a note cannot be voided; the overdue list by days and the
interest shown; reminders frozen with their invoices; permissions, approval, tenant isolation, audit.

## Correcting a charge after the folio is closed (added after a question of the owner)

Case: a charge of 500,000 is corrected to 400,000 some days after check-out; the folio is closed and its balance was transferred to the
company (city ledger).

**What the code allows today:** nothing. Every correction path of a folio (charge, adjustment, reversal, void or refund of a payment,
transfer) requires the folio to be OPEN, there is no reopening, and a transfer is not refundable. The books of the closed days are
journaled. So the transfer of 500,000 stays as it is, and an invoice cannot be edited.

**The way proposed:** the ledger is never rewritten; the correction is a **credit note of 100,000** (net and tax) that lowers what the
company owes and is journaled on the date it is made (Dr allowance of revenue and tax payable, Cr CITY_LEDGER). The folio keeps showing
500,000; the statement of the company shows the transfer and the credit note. What changes by the state of the invoice:

1. **Invoice not issued yet.** The credit note is made against the *transfer* (not the invoice). When the invoice is made from that
   transfer, the credit note is attached to it (a link, released if the invoice is voided): the invoice shows the transfer of 500,000, the
   credit note of -100,000 and a total of **400,000**, so the company receives one invoice of the right amount. The tax invoice
   (faktur pajak) of that invoice covers 400,000 of the 500,000 of the folio (its share, as it already does for a part transfer).
   A credit note may be up to the transfer; one for the whole transfer leaves nothing to invoice.
2. **Invoice issued, not paid.** The credit note is made against the invoice and lowers what it owes to 400,000. If the invoice has a
   live tax invoice it is voided first and the replacement covers 400,000 (the decision of the last question).
3. **Invoice issued and paid in full (500,000 received).** The company has overpaid by 100,000: the credit note leaves a **credit on
   account** that is refunded or used against another invoice. The refund of a credit balance does not exist yet, so in phase 1 a credit
   note may not exceed what the invoice still owes, and an overpaid invoice is handled by a manual journal until the refund is built.

The invoice is never voided and re-made for a correction (voiding releases the transfers but cannot change their amount).

## Phase 2: the supplier side (not part of the above)

A supplier credit note lowers a bill (or stands on account): Dr ACCOUNTS_PAYABLE, Cr the expense lines and the input VAT per the treatment
frozen on the bill. The input VAT of a creditable credit note is taken back in the VAT return as a negative claim (the claims of step 3
reference a bill line today; they would reference a credit note line too). A write-off on the payables side does not exist.

## Questions for the owner

1. **Order.** Customer side now (credit notes, write-offs, overdue list and reminders) and the supplier credit notes next?
2. **Tax on a credit note.** Does it lower the output tax of the return of its month, as proposed, or is a credit note an allowance with
   no tax effect?
3. **Interest.** Only shown on the overdue list (proposed), posted as a late fee charge, or not at all?
4. **Write-off account.** Bad debt expense by default with the option of the allowance account (proposed), or the expense only?
5. **Credit note and tax invoice.** Refused while the tax invoice is live, and the replacement tax invoice subtracts the credit notes
   (proposed), or should the credit note simply be allowed and the tax invoice left as issued?
