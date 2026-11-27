# 41. Allocating a company receipt after it is recorded (KD-27)

Status: **plan, awaiting the owner's decisions** (§11). It answers KD-27 of the user guide report (`docs/user-guide/_laporan.md`,
priority 1, money): a receipt of a company that was recorded without allocations cannot be allocated to its invoices later, so the
invoices stay **Belum dibayar** and overdue, and the company can be asked to pay twice. Nothing in this document is built yet. **R**
marks a recommendation; each one waits for a decision in §11.

Read with: `10-credit-notes-writeoffs.md` (what an invoice owes), `14-supplier-credit-notes.md` (`ApplyCredit`, the pattern this
follows), `05-transactions-locking.md` (lock order), `32-distinct-approver.md` (approval kinds).

## 1. What exists, and what goes wrong

- **Receive** (`internal/cityledger/service.go` `Receive`, `cityledger.receive`, `POST {P}/city-ledger/accounts/{id}/receipts`) records
  a receipt and, in the same transaction, its allocations (`allocate`, `service.go:636`). Since KD-04 (commit 7a2cf30) the form splits
  the amount over the open invoices from the oldest, but the clerk may leave lines empty, and receipts recorded before KD-04 have
  no allocations at all.
- **The allocations are a table of their own** (`city_ledger_receipt_allocations`, migration 00024): `receipt_id`, `invoice_id`,
  `company_id` (both foreign keys go through `company_id`, so a receipt can only pay an invoice of its own company), `amount > 0`.
  It is append-only (`clra_immutable`) and has a unique pair `clra_pair_uk (receipt_id, invoice_id)` ("a receipt pays an invoice
  once"). It has no date and no author. Nothing stops a later INSERT: the table can take allocations after the receipt. Only the
  code has no path for it (`allocate` is called by `Receive` only).
- **What an invoice has been paid** is derived: the allocations of `POSTED` receipts (`InvoicePaid`; `ListOverdueInvoices` for the
  overdue list). Outstanding = total - paid - credit notes - write-offs (`10` §3). The part of a receipt that no invoice takes is
  "on account" (`model.go:59`); it is not shown anywhere as a number. The receipt form shows **Belum dialokasikan** while typing only.
- **A receipt is voided** only on its own business date (`VoidReceipt`: `CORRECTION_REQUIRES_ADJUSTMENT`). After the night audit,
  there is no way out: the receipt cannot be voided and recorded again with its allocations.
- **An invoice with allocations cannot be voided** (`INVOICE_HAS_PAYMENTS`, "void them first"), and a receipt of an earlier day
  cannot be voided, so a wrong allocation also blocks the void of its invoice for good.

## 2. Does an allocation post a journal? No

Read from the code, not assumed:
- The **day close** journals a receipt as Dr the method's account / Cr `CITY_LEDGER` for its whole amount
  (`internal/accounting/queries.sql:138-146`, the `RECEIPT` lines of the day's sources). It reads `city_ledger_receipts` only. It never
  reads the allocations.
- The control account `CITY_LEDGER` has no invoice dimension. Its reconciliation compares the account with
  `transferred - received - adjusted` (`queries.sql:325-333`), all company-level sums. The allocations are not part of it.
- Outside `internal/cityledger`, **no query reads `city_ledger_receipt_allocations`** (`grep` over every `queries.sql`: the four
  readers are `InsertAllocation`, `InvoicePaid`, `ListCompanyAllocations` and `ListOverdueInvoices`, all in `internal/cityledger`).

So an allocation is a **matching inside the sub-ledger**: it says which invoice the money paid. The receivable already went down
when the receipt was recorded. Allocating later moves no money and posts **no journal**, exactly as `ApplyCredit` of the supplier
credit notes (`14`: "with no journal (the payables account already went down)").

## 3. The use case in one sentence

**Allocate** (`POST {P}/city-ledger/receipts/{id}/allocations`, `cityledger.receive`, `Idempotency-Key`) takes what is left of a
`POSTED` receipt and spreads it over one or more `ISSUED` invoices of the same company. The receipt can be of any earlier day. The
allocation is dated the current business date. **Release** (`POST {P}/city-ledger/receipt-allocations/{id}/release`, with a reason
and an approval, question A3) ends one allocation, so its amount goes back to the receipt's unallocated part and the invoice owes it
again.

## 4. Model

### 4.1 The allocation gets a date, an author and a release

Migration `00102_receipt_allocation_later.sql` (with Down) changes `city_ledger_receipt_allocations`:

| Column | Type | Meaning |
|---|---|---|
| `allocated_on` | `date NOT NULL` | the business date the allocation was made on. Existing rows are back-filled with the receipt's `business_date` (they were all made with their receipt). FK to `business_days`. |
| `created_by` | `bigint NULL` → `users` | who allocated. NULL for the existing rows. |
| `idempotency_key` | `varchar(100) NULL` | the key of the request that made it (NULL for allocations made with their receipt: the receipt's own key covers them). |
| `released_at`, `released_on`, `released_by`, `release_reason`, `release_approved_by` | NULL | the release (§6). `released_on` is the business date. |

- **`clra_pair_uk` is dropped** (question A4). A receipt that paid 300 of an invoice with its receipt may pay 200 more of the same
  invoice later, as a second row. Without the drop, the second allocation would need a release of the first and a new row of 500.
  The mapping `clra_pair_uk → ALLOCATION_DUPLICATE` is removed from `errors.go`. The request itself still names each invoice once
  (`DUPLICATE_INVOICE`, as today).
- **`clra_idempotency_uk`**: unique `(property_id, idempotency_key, invoice_id)` where the key is not null.
- **The guard trigger** `city_ledger_receipt_allocations_guard` changes from "never" to "release only": an UPDATE may only set the
  five release columns of a live row (`released_at` NULL → not NULL), and everything else must stay equal
  (`to_jsonb(NEW) - release columns = to_jsonb(OLD) - release columns`, as `card_settlements_void_only` in 00101). DELETE and
  TRUNCATE stay forbidden. It raises `clra_release_only`.
- **Check** `clra_released_ck`: the five release columns are all NULL or all set. A release always has an approval (§6), and
  `release_approved_by` is the user who gave it.
- **Deferred constraint trigger** `clra_receipt_total_check` (at COMMIT, as `supplier_credit_total_check` in 00055): the live
  allocations of a receipt are at most its amount (`clra_exceeds_receipt`). Today this is only checked by `parseAllocations`, which
  sees the request and not the rows already there. The invoice side (at most what the invoice owes) stays in the application, under
  the company lock, as today: it needs the credit notes and write-offs of another table.
- **The business-day trigger** `business_day_must_be_open` (00065) is added on INSERT of the allocations
  (`city_ledger_receipt_allocations_business_day_open`, on `allocated_on`). An allocation moves no money, but it is dated by the
  business day, and the trigger is the database's guard that nothing is dated a closed day.
- **Down** refuses to run while an allocation is released or a receipt has two live allocations of the same invoice (the old
  unique could not hold them). Otherwise it restores the old trigger and unique, and drops the columns.

### 4.2 "Unallocated" per receipt

- `Receipt` gets **`allocated`** and **`unallocated`** (strings, currency decimals): `allocated` = the sum of its live allocations,
  `unallocated` = amount - allocated for a `POSTED` receipt, `0` for a `VOIDED` one.
- `ReceiptAllocation` gets `id`, `allocated_on`, `released_at`, `released_on`, `release_reason`. Released ones are still listed
  (struck through on the screen), so the trail stays visible.
- `Account` gets **`unallocated`**: the sum over the company's `POSTED` receipts. The account page shows it beside the balance
  ("Uang muka belum dialokasikan").
- `InvoicePaid` and `ListOverdueInvoices` count **live** allocations only (`released_at IS NULL`), still of `POSTED` receipts.

## 5. Allocate

`POST {P}/city-ledger/receipts/{id}/allocations`, body `{ allocations: [{ invoice_id, amount }] }` (1 to 200, the same shape as the
receipt's), header `Idempotency-Key`. Answer: the receipt (with its allocations and `unallocated`) and the company's balance.

**Order of the transaction** (CLAUDE.md lock order, the same as `Receive`):
1. `RequireOpenBusinessDay` (`ForShare`, level 10).
2. Read the receipt (404 `RECEIPT_NOT_FOUND`, another tenant's or property's included).
3. `companies.Lock` on the receipt's company (`FOR UPDATE`, level 44). This is the lock that `Receive`, `VoidReceipt`,
   `CreateInvoice`, `VoidInvoice`, credit notes, write-offs and their voids take, so all of them are serialised with the
   allocation.
4. Under the lock: the idempotency replay (§5.2), then read the receipt again, and the checks below.
5. Insert the rows, write the audit, answer.

No accounting lock, no sequence: there is no journal and no number.

### 5.1 Checks (after the lock)

| Check | Answer |
|---|---|
| The receipt is `POSTED` | 409 `RECEIPT_ALREADY_VOIDED` |
| Each invoice exists, is of the same company | 404 `INVOICE_NOT_FOUND` with `invoice_id` (as `allocate`); the FK `clra_invoice_fk` is the database's guard (`INVOICE_COMPANY_MISMATCH`) |
| Each invoice is `ISSUED` | 409 `INVOICE_NOT_PAYABLE` |
| Each amount is positive, within the currency's decimals, each invoice once | 422 (`INVALID_AMOUNT`, `DUPLICATE_INVOICE`), before the transaction |
| Each amount ≤ what the invoice still owes (total - live allocations - credit notes - write-offs) | 409 `ALLOCATION_EXCEEDS_INVOICE` with `invoice_id` and `outstanding` (the existing code of `allocate`) |
| The sum ≤ the receipt's unallocated part | 409 **`ALLOCATION_EXCEEDS_UNALLOCATED`** with `unallocated` (new; the deferred trigger `clra_exceeds_receipt` maps to the same code) |

The codes follow the existing city ledger ones (`ALLOCATION_EXCEEDS_INVOICE`), not the payables ones. The payables pair is
`ALLOCATION_EXCEEDS_OUTSTANDING` / `CREDIT_EXCEEDS_UNAPPLIED`; their meaning is the same, and the UI texts say the same thing.
`allocate` is shared by `Receive` and `Allocate`, so both get the same checks.

### 5.2 Idempotency

The key is required (1 to 100 characters). Under the company lock, the rows with that key are looked up first:
- found, same receipt → the same answer again, nothing written;
- found, another receipt → 422 `IDEMPOTENCY_KEY_REUSED` (the existing `errKeyReused`).

Two identical requests at once are serialised by the company lock, so the second always finds the first's rows. The unique
`clra_idempotency_uk` is the database's guard (`DUPLICATE_REQUEST`, retried once as `receiptReplay` does).

### 5.3 Audit

`cityledger.receipt_allocated`, entity the receipt (label: its number), in the same transaction: the invoices (number and amount),
the total, the unallocated part before and after, the actor. Its texts in English and Indonesian ("Penerimaan dialokasikan"); the
`auditCoverage` test lists the new actions.

## 6. Release

`POST {P}/city-ledger/receipt-allocations/{id}/release`, body `{ reason, approval }`.

- **When** (question A3, R): any day, while the receipt is `POSTED` and the invoice `ISSUED`. A released allocation cannot be
  released again (409 `ALLOCATION_ALREADY_RELEASED`). The same lock and order as Allocate (business day, company).
- **Approval** (R): the existing kind **`CITY_LEDGER_VOID`**, with the allocation's amount (the kind of the void of a receipt or an
  invoice, approved with `correction.approve`). No new kind: a release undoes a matching, like a void undoes a receipt.
  Permission `cityledger.receive`. A reason is required (up to 500).
- **Effect:** the five release columns are set. The invoice owes the amount again (and is back on the overdue list if it is past
  due); the receipt's unallocated part grows by it. No journal (§2).
- **Audit** `cityledger.allocation_released` (with the approver, as every approved action), entity the receipt. Its ID label says
  "dilepas".
- **The invoice void** keeps refusing while live allocations remain. Its message changes from "void them first" to "release the
  allocations first" (EN/ID), which is now possible.
- **Payables** have no release: an application of a supplier credit note is append-only, and only the void of the credit note
  ends it (`14`). The city ledger needs one because a receipt of an earlier day cannot be voided, while a supplier credit note
  can always be voided.

## 7. Dates, closed days and periods, aging, reminders

- **The date** of an allocation is the business date it is made on (question A2, R), not the receipt's date. Nothing is written
  into a closed day (the business-day trigger, §4.1). There is no journal, so an accounting period that is closed is not touched,
  whatever the receipt's month.
- **The aging of a company** (`Aging`) puts the transfers in buckets and lets receipts, credit notes and write-offs settle the
  oldest first. It is computed at the company level and **does not read the allocations**, so allocating later changes nothing
  in it. This is the existing design (10 §3) and stays.
- **The overdue list and the reminders** read the live allocations (`ListOverdueInvoices`). After an allocation, the invoice leaves
  the overdue list (or shows the smaller outstanding and interest) from that moment. Reminders already made keep the invoices and
  amounts they were made with (they are frozen, `10` §4). Nothing recomputes an overdue list of a past date: no report reads the
  allocations by date today. `allocated_on` and `released_on` make such a report possible later; it is not part of this plan.
- **The interest** shown on the overdue list is computed on the outstanding of today; after the allocation it is computed on the
  smaller outstanding. It is shown, never posted (`10` §4).

## 8. Screens and the guide

**The account page** (`web/src/views/accounts/CityLedgerAccountView.vue`):
- **A component** is extracted from the KD-04 allocation table in the receipt form: `components/cityledger/AllocationTable.vue`
  (props: the amount to split, the open invoices, the model of amounts; it fills from the oldest, keeps what the clerk typed,
  shows **Belum dialokasikan** and the problems, in thousandths, never floats). The receipt form and the new allocation form both
  use it. Its Vitest tests move with it, and the receipt form's tests stay green unchanged (the testids are kept).
- **The statement** row of a `RECEIPT` with an unallocated part shows it ("sisa 300.000") and a button **Alokasikan**
  (`allocate-{receipt_number}`, for `cityledger.receive`). It opens a form (`allocate-form`) with the component, the amount being
  the receipt's unallocated part, and the submit `allocate-submit`. A success notice names the receipt and the invoices.
- **The receipt's allocations** are listed under it (invoice, amount, date) with **Lepas** (`release-{allocation id}`) for
  `cityledger.receive`: a reason, then the approval dialog (`ApprovalDialog`, kind `CITY_LEDGER_VOID`, the allocation's amount).
  A released one is shown struck through with its date and reason.
- **The account header** shows **Uang muka belum dialokasikan** (the account's `unallocated`), when above zero.
- **Errors:** `ALLOCATION_EXCEEDS_UNALLOCATED`, `ALLOCATION_ALREADY_RELEASED` and the changed `INVOICE_HAS_PAYMENTS` get texts and
  actions in `en.ts` and `id.ts` (the `errorCodes` test). `ALLOCATION_DUPLICATE` goes (no backend code returns it any more).

**The guide** (same commit):
- `06-corporate-group-city-ledger/invoice-dan-penerimaan.md`: the 🐞 KD-27 block goes. A new section "Mengalokasikan penerimaan yang
  sudah tercatat" (when, the button, the split from the oldest, the date being today) and "Melepas alokasi yang salah" (reason,
  approval, what happens to the invoice). Screenshots if the docs world has a receipt with an unallocated part; otherwise a
  `SCREENSHOT` marker and a line in `_laporan.md` §6.
- `faq.md` (the question at line 269, "invoice sudah dibayar tapi masih Belum dibayar"): the answer becomes "Alokasikan" on the
  receipt, instead of "report it".
- `troubleshooting.md:149`: the same.
- `panduan-cepat/manajer.md:44`: one sentence that a receipt can be allocated later.
- `_laporan.md`: KD-27 marked fixed with the commit, in the priority table and §2a.

**API** (`api/openapi.yaml`, then `npm run gen:api`): the two paths, `Receipt.allocated/unallocated`, the allocation fields,
`Account.unallocated`; `06-api.md` gets the two rows.

## 9. Tests

All on real PostgreSQL (`dbtest`), in `internal/cityledger` (`allocate_later_test.go`), and the schema tests.

**The use case:**
1. A receipt recorded without allocations, on day 1; the night audit closes day 1; on day 2 it is allocated to two invoices: both
   are paid (`paid`, `outstanding`, status shown as paid), `allocated_on` = day 2, the receipt's `unallocated` is the rest,
   `Account.unallocated` the same, the audit row lists the invoices.
2. **The books do not move:** the trial balance and the `CITY_LEDGER` reconciliation are the same before and after the allocation
   and the release; no journal is written; the aging of the company is the same.
3. A receipt that already paid 300 of invoice A allocates 200 more of A later: two rows, A paid 500 (A4).
4. More than the unallocated part: `ALLOCATION_EXCEEDS_UNALLOCATED` with `unallocated`; nothing written.
5. More than the invoice owes, counting a credit note and a write-off of it: `ALLOCATION_EXCEEDS_INVOICE` with `outstanding`.
6. A voided receipt: `RECEIPT_ALREADY_VOIDED`. A voided invoice: `INVOICE_NOT_PAYABLE`. An invoice of another company: 404.
7. Idempotency: the same key twice returns the same answer and writes once; the key on another receipt: `IDEMPOTENCY_KEY_REUSED`.
8. The overdue list: the invoice leaves it after the allocation; a reminder made before keeps its invoices and amounts.
9. Release: the invoice owes again and is back on the overdue list; the receipt's unallocated part grows; the audit with the
   approver; a second release: `ALLOCATION_ALREADY_RELEASED`; without an approval or a reason: refused; the rule of approval of
   the property applies (someone else, above an amount).
10. After a release, the invoice can be voided (with no other allocation left), and it answers the new `INVOICE_HAS_PAYMENTS` text
    while one is left.
11. Permissions (`cityledger.receive` for both, `cityledger.read` sees `unallocated`), tenant isolation (another tenant's receipt
    or allocation: 404), a closed business day (the business-day trigger refuses an allocation dated a closed day).

**The schema** (`scripts/db-test.sh` and the schema test file): the guard allows only the release of a live row and nothing else;
DELETE and TRUNCATE refused; `clra_released_ck`; the deferred `clra_exceeds_receipt` at commit (two inserts in one transaction that
together pass the receipt); the business-day trigger; the back-fill of `allocated_on`; Down refused while a release exists.

**Races** (two transactions, `pg_stat_activity` to wait for the lock, as `void_race_test.go`):
12. **Two allocations of the same receipt at once**, each within the unallocated part but together above it: one commits, the other
    waits on the company lock, then sees the new unallocated part and answers `ALLOCATION_EXCEEDS_UNALLOCATED`. The receipt is never
    allocated above its amount.
13. **An allocation and a new receipt with allocations to the same invoice at once**, together above what the invoice owes: one
    commits; the other waits on the company lock, then answers `ALLOCATION_EXCEEDS_INVOICE`. The invoice is never paid above its
    total.
14. An allocation and a release of another allocation of the same receipt at once: both commit, in either order, and the
    unallocated part is right.
15. An allocation and a credit note to the same invoice at once (the credit note's own lock is the company too): the invoice never
    goes below zero outstanding.

**Web** (Vitest): the extracted component (from the oldest, a typed value kept, 0.1 + 0.2 in thousandths, too much for one invoice,
too much in total); the Alokasikan button only for a receipt with an unallocated part and the permission; the form sends the
allocations with an `Idempotency-Key`; the release asks a reason then the approval dialog with `CITY_LEDGER_VOID` and the amount;
the error texts. **E2E** (`web/e2e`): a receipt recorded without allocations, the night audit, then Alokasikan on the account page;
the invoice shows paid.

## 10. Not in this plan

- A refund of the money on account to the company.
- Allocating one receipt to the invoices of another company (never: the foreign keys forbid it).
- A report of the overdue invoices or the aging by invoice as of a past date (made possible by `allocated_on`, not built).
- Allocating credit notes or write-offs (they are made against an invoice or a transfer already).

## 11. Decisions of the owner

| # | Question | Recommendation and why |
|---|---|---|
| A1 | Permission and approval for allocating later: `cityledger.receive` without an approval, or with one? | **`cityledger.receive`, no approval** (R). An allocation moves no money and posts no journal (§2); allocating with the receipt needs no approval today; the supplier side (`ApplyCredit`) needs none either. The undo (A3) is the act that needs a second person. |
| A2 | The date of an allocation: the current business date, or the receipt's date? | **The current business date** (R). Nothing is written into a closed day; the overdue list and reminders already made stay as they were; `allocated_on` records when the matching was made, and a report of a past date can use it later. The receipt's date would rewrite what the overdue list of a past day said. |
| A3 | Releasing an allocation: allowed, until when, with which approval? | **Allowed any day while the receipt and the invoice are live, with a reason and the approval `CITY_LEDGER_VOID` (the allocation's amount, `correction.approve`)** (R). A receipt of an earlier day cannot be voided, so without a release a wrong allocation is stuck and also blocks the void of its invoice. The existing kind is that of the void of a receipt; a new kind adds nothing the rule of the property cannot do. Payables have no release, because a supplier credit note can always be voided. |
| A4 | Can a receipt pay the same invoice in two allocations (drop `clra_pair_uk`)? | **Yes, drop it** (R). Paying the rest of an invoice later from the same receipt is the normal case (part with the receipt, part later). Keeping the unique would force a release and a new row for it, with an approval for what is not a correction. The request still names an invoice once. |
