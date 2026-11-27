# 42. Returning a POS charge to the wrong room after check-out (KD-26)

Status: **approved by the owner on 2026-11-27, with conditions; not built yet** (§11). The build is a separate PR, after the
PR of KD-27 (doc 41) is merged. It answers KD-26 of the user guide report (`docs/user-guide/_laporan.md`, priority 2, money): an
outlet bill charged to the wrong room and found after that guest checked out cannot be returned from any screen, so the guest
stays charged for someone else's meal and the difference is settled outside the system. **Case b (the city ledger) is deferred**
(R3): the use case refuses it, and its design is kept in §10.1 for later (KD-29). **R** marks what was a recommendation. A choice
that departs from an existing rule is marked **⚠ deviation**.

Read with: `34-pos.md` (§6.3 charge to room, §8 corrections, §9.5 the return), `30-group-folio.md` (§4 transfers and the return,
§5 the master), `03-financial-engines.md` (one posting path), `10-credit-notes-writeoffs.md` ("Correcting a charge after the folio
is closed"), `31-deposit-release.md`, `05-transactions-locking.md`.

## 1. What exists, and what goes wrong

- **Charge to room** (`folios/outletbill.go` `ChargeOutletBillToRoom`) moves the bill's balance to the target folio as a `TRF` pair
  "{outlet} · {bill}" (a `folio_transfers` row, batch scope `OUTLET_CHARGE`) and writes `pos_room_charges` (what counts against the
  folio's limit). The bill is then paid to zero and **closes** (34 §4: `OPEN ──pay to zero──▶ CLOSED`).
- **Return** (`pos.ReturnRoomCharge` → `folios.ReturnOutletRoomCharge`, `folio.reverse` + approval `FOLIO_TRANSFER`) is the return of
  30 §4.5: the opposite `TRF` pair from the holder back to the bill, a `folio_transfers` row with `returns_transfer_id` (partial
  unique `folio_transfers_returns_uk`: a transfer is returned once), and `pos_room_charges.returned_by_transfer_id`. **Both folios
  must be OPEN** (`requireOpen`, `outletbill.go:598-601`), and the bill must be `OPEN` (so a closed bill is first reopened,
  `POS_REOPEN` + `folio.reopen`, 34 §8 table). **Move** (`MoveRoomCharge`) is a return and a new charge in one transaction.
- **After check-out** the guest folio is `CLOSED` at a zero balance (`CloseStayFolios`: `FOLIO_NOT_BALANCED` otherwise). It was
  brought to zero by the guest's payments (cash, card, QRIS, transfer, deposit), by a `CITY_LEDGER` transfer to a company, or by a
  balance `TRF` to a master folio. Nothing can be written to it any more:
  - a charge, an adjustment, a reversal, a transfer or a return: `FOLIO_CLOSED` (30 §4 item 7: "a closed folio can never be the
    source or the target");
  - a refund (`folios/payments.go` `Refund`): `requireOpen` too, and a `CITY_LEDGER` transfer is never refunded ("the company's
    receipt settles it");
  - **a guest folio is never reopened.** `folio.reopen` exists, but only `ReopenBillFolio` uses it, and only for `OUTLET` folios
    (`outletbill.go:422-438`). 10 says it plainly: "there is no reopening".
- **A credit note to the company** (10, "Correcting a charge after the folio is closed") is the existing answer for a wrong charge on
  a folio sent to the city ledger. It lowers what the company owes with Dr the allowance of revenue / Cr `CITY_LEDGER`.

### 1.1 The real cases after check-out

| | Case | What is wrong, and what the right end is |
|---|---|---|
| a | The guest **paid** the folio (cash, card, QRIS, bank transfer). | The guest paid X too much. The guest is owed X back. The bill must owe X again and be charged to whoever ate. |
| b | The folio was **transferred to a company** (city ledger). b1: the transfer is not on a live invoice yet; b2: it is on an issued invoice that still owes at least X; b3: the invoice is paid (or owes less than X). | The company owes X too much. Its invoice (if any) must owe X less. The bill must owe X again. |
| c | The charge went to a **master folio** of a group (routing), or to the guest folio whose balance then went to the master by a balance `TRF`. c1: the master is still open; c2: the master is closed, settled by payments or the city ledger. | The same as a or b, on the folio that settled the charge. |

In every case the **revenue is right**: the outlet sold the meal and posted it once, on its own folio. Only who pays is wrong. This
is why a credit note (Dr allowance of revenue) is the wrong tool here (§10).

## 2. The use case in one sentence

**Return after check-out** (`POST {P}/pos/room-charges/{id}/return-closed`, R) does, in one transaction:
1. reopens the bill if it is closed (the `POS_REOPEN` step of 34 §8, inside the same transaction);
2. posts the return pair of 30 §4.5 between the **closed** holder and the bill (**⚠ deviation** from 30 §4 item 7, question R1). The
   holder gets a credit of X and the bill owes X;
3. **settles the holder's credit at once, by the way the holder was settled** (§3), so the holder ends at zero again. It **stays
   CLOSED**, and `closed_at` and `closed_on` do not change;
4. marks `pos_room_charges` returned (the usage of the limit goes down, as today).

The bill is then OPEN with a balance of X. The cashier settles it with the existing flows: charge it to the right room, to a company,
or take the payment.

## 3. Per case

All the rows below are dated **the current business date** and journaled by **today's** day close, through the existing
`folio_item_gl` path. No closed day is touched, no journal is back-dated, and no manual journal is needed.

### 3.a The guest paid: a refund to the guest (question R2)

| Folio | Row | Amount |
|---|---|---|
| Holder (closed guest folio) | ADJUSTMENT `TRF` (return) | −X |
| Bill (reopened) | CHARGE `TRF` (return) | +X |
| Holder | **REFUND** of one of its payments (`refund_of_payment_id`), method from the property's refund methods | +X |

- The holder: −X + X = 0, and it stays closed.
- **Journal** (today's day close):
  - the `TRF` lines net to zero on `FOLIO_TRANSFER` 1215 (the readiness check of 1215 stays green);
  - 1210 guest ledger: the holder −X and the bill +X, then the refund +X (Dr 1210);
  - the refund: Cr the method's account (cash in the drawer, the card or QRIS clearing, the bank);
  - **net:** Dr 1210 X (the bill owes) / Cr cash or bank X (the money that leaves).
  - Revenue does not move.
- **The money:**
  - a **CASH** refund goes through the cashier's open shift (`CashShift`, as any refund), which counts it as cash out. The night
    audit already blocks on open shifts.
  - a **CARD**, **QRIS** or **BANK_TRANSFER** refund is recorded when the hotel makes it at the terminal or the bank, with its
    reference.
  - The guest has usually left: the refund is recorded when the money actually goes out, and not before (R2). Until then the case
    stays as it is.
  - **The limitation of this (R2, the owner's condition):** until the refund is recorded, the bill is not open again either. The
    guest who should pay for the meal can check out in the meantime, and then they can no longer be charged to their room. The
    guide therefore asks the supervisor to do this correction as soon as possible. When the refund can be recorded the same day,
    the supervisor should do it before the right guest checks out, and charge that guest at once.
- **Which payment** is refunded: the screen proposes the folio's last payment that is not a deposit and has enough refundable left
  (amount − refunds, checked under the payment lock, `REFUND_EXCEEDS_PAYMENT`). The clerk may pick another.
- **A payment that was a deposit** (`folio_item_gl.is_deposit`: paid before check-in) is **refused** in this plan
  (`REFUND_OF_DEPOSIT_AFTER_CLOSE`). The deposit was released to 1210 at the close (31, `ON_CLOSE`), but a refund linked to it would
  be booked on 2310 by `folio_item_gl`, which has nothing left. Whether the view can book it on 1210 instead is checked in the build,
  with a test. If it can, the refusal goes; if it cannot, the clerk refunds against another payment of the folio.
- **Permission:** `payment.refund`, as any refund. It is covered by the one approval of §6.

### 3.b The company: refused for now (question R3, deferred)

A holder settled by a `CITY_LEDGER` transfer to a company (case b, and c2 when the master went to the city ledger) is **refused**
with **`RETURN_AFTER_CHECKOUT_CITY_LEDGER`** (409), before anything is written. The use case finds this from the holder's (or the
master's) payments: a live `CITY_LEDGER` PAYMENT on it. Its message, in English and Indonesian, sends the user to the accountant.
The preview (§8) says so before the approval is asked, and the screen shows the message instead of the form.

**The way round today** is manual. The guide writes it down after the build checks it against the code: the owner's condition is
that it is taken from the code, not guessed. Its likely shape: a credit note to the company's invoice or transfer (10), then a
charge of the meal to the folio of whoever ate. The guide says that the credit note lowers revenue, so the accountant may need
a reclassifying journal. The design that does it properly (a reduction of the company's transfer) is in §10.1, and the case is
recorded as **KD-29** in `_laporan.md` (priority 2, money).

### 3.c The master folio (question R4)

- **The charge was routed straight to the master** (the holder is the master):
  - **c1, the master is open:** the existing return works as soon as the bill is reopened. Nothing new.
  - **c2, the master is closed:** the same as a, with the master as the holder; settled by the city ledger, refused as 3.b.
- **The charge was on the guest folio, whose balance then went to the master by a balance `TRF`** (the guest closed at zero): the
  "settling folio" is the master. The return pair goes between the guest folio and the bill (the guest −X). Then a balance pair of
  X moves the credit to the master (guest +X, master −X), so the guest is back at zero.
  - c1, the master is open: the credit stays on it. The group owes X less, and its own later settlement takes it into account.
    Nothing else.
  - c2, the master is closed: the master's credit is settled as in a (a refund); settled by the city ledger, refused as 3.b.
- **Only this one hop** (guest → master) is followed. A longer chain (a balance moved twice) is refused with
  `RETURN_CHAIN_UNSUPPORTED`, and the guide sends it to the supervisor. It is the shape of 30 (routing to a master); nothing else
  makes such chains today.

### 3.d The guest folio is still open, the bill is closed

This is not KD-26, and it already works: reopen the bill, then return. The new screen does not offer itself in this case (the
existing Return is the way).

## 4. Rules that hold in every case

- **Nothing is rewritten.** Every row is new and appended (CLAUDE.md: folio items append-only, corrections are reversals and
  adjustments). Only `FolioPostingService` writes the folio items (`moveBalance` and `createPayment` are its paths), with
  `RequireOpenBusinessDay` first and the business-day trigger behind.
- **The holder never stays off zero.** The use case refuses unless its own rows bring every closed folio it touches back to zero in
  the same transaction (`CLOSED_FOLIO_UNBALANCED`, a programming guard). A new deferred check at commit is added: a CLOSED folio's
  items sum to zero (`folios_closed_balanced`). Today that rule holds only in the code (`CloseStayFolios`). The trigger makes it a
  database rule for every path, old and new. The build checks first that no existing closed folio breaks it (a query in the
  migration that raises with the folio numbers).
- **Before this trigger is written** (the owner's condition on R1), the build finds with `grep` **every path that inserts into
  `folio_items`**. That includes the posting service, the night audit, the deposit release `ON_CLOSE`, transfers, groups, POS and
  check-out. The list goes here, in this section, with the reason each one never writes to a CLOSED folio or always writes a
  balanced set. Test 17 (§9) runs check-out → night audit → deposit release `ON_CLOSE` on a folio already closed, with the trigger
  on.
- **The folio stays CLOSED.** `closed_at` and `closed_on` are not changed, so:
  - the deposit release `ON_CLOSE` (it reads `closed_on`) does not run twice;
  - the outlet lock of the folio stays LOCKED;
  - the night audit sees no new open folio of a departed stay.
- **The bill** is reopened in the same transaction when it is closed (`ReopenBillFolio`), and left OPEN with a balance of X. The
  night audit treats it like any open bill with sold items (34 §13.1: a warning, or a blocker when the outlet says so). The
  cashier settles it with the existing pay flows (question R6).
- **Audit:** one entry `pos.room_charge_returned_after_checkout`, entity the bill, with:
  - the charge, the holder (and the master), the case (a / b / c), the refunded payment or the company and invoice;
  - the amounts, the reason and the approver.

  Folio entries are written on the holder and the master (visible from the folios, as 30 §4.5). Their ID labels exist in both
  languages.

## 5. Locks

The order of CLAUDE.md, the same as the existing return and refund:
1. business day (`ForShare`, 10);
2. the folios of the request, ascending, in one call (42): the holder, the bill and the master when there is one;
3. the payment refunded (43, `FOR UPDATE`), and the cashier shift for cash (43);
4. sequences (50): the payment number of the refund.

No company lock: case b is refused (3.b), and its lock is part of §10.1.

The approval is verified before the transaction, as every approved use case. Under the locks:
- the charge is read again (`returned_at` NULL, else `ALREADY_RETURNED`);
- the bill status;
- the holder's settlement;
- the refundable amount or the invoice's outstanding.

The partial unique `folio_transfers_returns_uk` stays the database's last guard against a double return.

## 6. Permissions and approval (question R5)

- **One new approval kind, `ROOM_CHARGE_RETURN_CLOSED`** (R), with X as its amount, approved with `correction.approve`, and a reason
  required. It covers the reopen of the bill, the return and the refund or the reduction of the transfer: one decision by one
  supervisor, for one act. A new kind is not a migration (32 §3).
- **Permissions of the actor:**
  - all cases: `folio.reverse` (the return, as today);
  - when the bill is closed: `folio.reopen` (as the reopen of a bill);
  - case a: `payment.refund`;
  - case b is refused (3.b); its permission, when it is built, is in §10.1.

  A clerk without one of them gets 403 `PERMISSION_DENIED`, before the approval is asked.
- **Why not the three existing kinds** (`POS_REOPEN`, `FOLIO_TRANSFER` and `REFUND`, one after the other): a supervisor would
  approve three steps of one correction in three dialogs, and a failure between them would leave a reopened bill and a half-done
  correction. A single transaction needs a single approval.

## 7. Migration `00103_closed_folio_return.sql` (with Down)

KD-27 takes 00102; this one takes the next free number when it is built. Nothing for the city ledger (case b is deferred, §10.1).
- **`folios`:** the deferred constraint trigger `folios_closed_balanced` (§4). It runs on INSERT into `folio_items` for a CLOSED
  folio and on the status change to CLOSED.
- **`pos_room_charges`:** no change. `returned_by_transfer_id` is the return's transfer, as today.
- The new constraint names are mapped in `internal/platform/db/errors.go`.
- **Down** drops the trigger.

## 8. API, screens and the guide

- **API:** `POST {P}/pos/room-charges/{id}/return-closed`, body:
  - `reason`, `approval`;
  - `settle`: `{ kind: "REFUND", payment_id, payment_method?, reference_number? }` or `{ kind: "COMPANY" }`. The server finds the
    company's transfer from the holder.

  Answer: the bill, the holder (and master) numbers and balances, and the refund or the company's new balance. A
  **`GET {P}/pos/room-charges/{id}/return-closed`** preview says which case applies, what will be refunded or reduced, and what
  would refuse it, so the screen can show it before the approval. `openapi.yaml`, `gen:api` and `06-api.md` get both.
- **Screen.** On the bill's room charges (where **Kembalikan beban kamar** is today, `04-pos/mengembalikan-beban-kamar.md`), a charge
  whose holder is closed shows **Kembalikan setelah check-out** instead of the plain return. It opens a form:
  - the case read from the preview ("Tamu sudah membayar: dana dikembalikan ke tamu" / "Ditagihkan ke PT …, invoice INV-…");
  - for a refund: the payment and the method, with the reference;
  - the reason, then `ApprovalDialog` (kind `ROOM_CHARGE_RETURN_CLOSED`, the amount X).

  After success, the bill shows its balance and the existing pay buttons. All texts are in `en.ts` and `id.ts`, with the new
  error codes and their actions.
- **Guide** (same commit):
  - `04-pos/mengembalikan-beban-kamar.md`: the 🐞 KD-26 line goes, and a section "Setelah tamu check-out" covers each case, what
    happens to the bill, the refund and the cashier shift, and the company invoice;
  - `troubleshooting.md:282` loses the POS example;
  - `faq.md` gets a question;
  - the manual way of case b (3.b), checked against the code, on the same page and in `troubleshooting.md`;
  - `_laporan.md`: KD-26 marked fixed, and **KD-29** (case b) added in priority 2 (money), pointing at §10.1.

  There are screenshots if the docs world has a checked-out guest with an outlet charge; otherwise a `SCREENSHOT` marker and a
  line in §6.

## 9. Tests

All on real PostgreSQL (`dbtest`).

**Cases** (`internal/pos`, `internal/folios`, `internal/cityledger`):
1. **Case a, card:** the bill is closed and reopened; the return pair and the refund are dated today. The holder stays CLOSED at zero
   with `closed_on` unchanged. `pos_room_charges` is returned and the limit usage goes down. Today's day close journal is Dr 1210 /
   Cr card clearing, 1215 is zero and the readiness is green; revenue and the closed days' journals are unchanged; the audit rows
   are written.
2. **Case a, cash:** the refund is in the cashier's shift (expected cash goes down). Without an open shift: the existing
   shift error, and nothing is written.
3. **Case a, a refundable amount too small** on the chosen payment: `REFUND_EXCEEDS_PAYMENT`. A deposit payment:
   `REFUND_OF_DEPOSIT_AFTER_CLOSE` (or, if the build shows `folio_item_gl` books it right, the refund on 1210 and 2310 unchanged).
4. **Case b refused:** a holder transferred to a company, invoiced or not: `RETURN_AFTER_CHECKOUT_CITY_LEDGER`, nothing written,
   the bill stays closed; the preview says the same.
5. **Case c2 to the city ledger:** a closed master settled by a company: the same refusal.
6. *(The tests of the reduction of the transfer are in §10.1, for when it is built.)*
7. **Case c:** straight to a closed master paid by the group (a refund); through the guest folio to an open master (the credit stays
   on it); to a closed master paid by the group; a two-hop chain: `RETURN_CHAIN_UNSUPPORTED`.
8. **Refusals:** a charge already returned (`ALREADY_RETURNED`); a holder that is open (the existing return, not this one); the
   permissions one by one (403 before the approval); the approval rule of the property (someone else, above an amount); tenant
   isolation (404).
9. **The schema:**
   - `folios_closed_balanced`: an item on a closed folio that leaves it off zero is refused at commit, a balanced pair is accepted;
   - the company refund checks: a refund with `company_id` but without `refund_of_payment_id`, of another company, or above the
     transfer, is refused;
   - the migration's check of the existing closed folios;
   - Down.
10. **The bill afterwards:** it can be charged to the right room (the gate of 34 §6 applies) or paid. While it is open with a
    balance, the night audit warns, or blocks per the outlet setting.

**Races** (two transactions, `pg_stat_activity` to wait for the lock):
11. **The night audit and the return at once:** the return holds the business day `FOR SHARE`, the audit wants it `FOR UPDATE`.
    Either the return commits first and its rows are in the day being closed (journaled by that close, 1215 zero), or the audit
    commits first and the return is dated the next day. Never half of each.
12. **Two returns of the same charge:** one commits, the other answers `ALREADY_RETURNED` (the folio locks, then the unique index).
13. **The return and a refund of the same payment by another path:** the payment lock serialises them, and together they never pass
    the refundable amount.
14. **The return and a payment of the reopened bill at once:** the bill folio lock. The payment sees the bill's balance after the
    return, or waits.
15. **Two returns of two charges of the same closed holder at once,** refunded from the same payment: the folio and payment locks;
    together they never pass the refundable amount, and the holder ends at zero.

**The closed-folio rule** (the owner's condition on R1):
16. Every path of the list in §4 that writes to a folio, run against a closed folio with the trigger on, refuses as before (its own
    `FOLIO_CLOSED`) and never reaches the trigger; the balanced pair of this use case is accepted.
17. **Check-out → night audit → deposit release `ON_CLOSE`**, for a folio with a deposit that closed at check-out, with the trigger
    on: the night audit closes the day, the deposit is released once (Dr 2310 / Cr 1210), and the trigger raises nothing.

**Web** (Vitest): the button only for a charge with a closed holder and the permissions; the case and the amounts from the preview;
the approval dialog with `ROOM_CHARGE_RETURN_CLOSED`; the error texts. **E2E** (`web/e2e/final`, after the night audit): a guest who
paid by card checks out with a bill of the restaurant on their folio; the next day the charge is returned with a refund, and the
bill is charged to the right room.

## 10. Deferred and rejected

### 10.1 Deferred: case b, the transfer to the company is reduced (KD-29)

| Folio | Row | Amount |
|---|---|---|
| Holder (closed) | ADJUSTMENT `TRF` (return) | −X |
| Bill (reopened) | CHARGE `TRF` (return) | +X |
| Holder | **REFUND** of the `CITY_LEDGER` transfer payment (`refund_of_payment_id` = the transfer, `company_id` = its company) | +X |

- **Journal** (today's day close): the `TRF` lines net on 1215; the refund of a `CITY_LEDGER` payment is read by the day close like
  the transfer with the opposite sign: Dr 1210 X / Cr `CITY_LEDGER` X. **Net:** Dr 1210 (the bill owes) / Cr `CITY_LEDGER` (the
  company owes less). Revenue does not move. The control account reconciliation reads `folio_item_gl`
  (`accounting/queries.sql:325-327`), so it follows by itself.
- **The company side (the large part of this case).** Today every reader of the transfers sums `payments.amount` for
  `company_id`, and none expects a refund among them. Each must count a refund **negatively**, or ignore it where it lists
  transfers and subtract it from its transfer:
  - `companies` `CompanyOwes` (the balance and the credit limit);
  - `cityledger` `ListAccounts`, `GetAccount` (transferred), `ListTransfers`, `GetTransfer`, `ListInvoiceCandidates` (the net of a
    transfer: amount − credit notes − **returned**), the statement (a new credit line **RETURN** "Retur beban POS {bill}");
  - the aging (the transfer ages at its net);
  - `nightaudit` `SummaryCityLedger`;
  - `taxinvoice` `CityLedgerTransfers` (the share of a folio);
  - and every other query found by `grep` in the build, each with a test.

  The refund is the one new shape. A `payment_type = 'REFUND'` row with `company_id` is refused today by the code only, not by the
  database. The build adds a check that such a refund has `refund_of_payment_id` pointing at a `CITY_LEDGER` payment of the same
  company.
- **The invoice**, by state:
  - **b1, not on a live invoice:** the transfer's net goes down. An invoice made later uses the net (as for a credit note against a
    transfer, 10 case 1).
  - **b2, on an issued invoice that still owes at least X:** the invoice gets **`returned`** beside `credited` and `written_off`.
    Its outstanding = total − paid − credited − written off − returned, and the PDF shows the line. The invoice is not voided or
    remade. **A live tax invoice** (faktur) of that invoice refuses the return (`TAX_INVOICE_EXISTS`, as a credit note with VAT):
    the faktur is voided first, and the replacement takes the smaller share.
  - **b3, the invoice owes less than X** (paid, in full or in part): **refused** (`RETURN_EXCEEDS_INVOICE`, with `outstanding`).
    The refund of a credit balance to a company does not exist (10 case 3). The guide gives the way round: release the receipt's
    allocation (doc 41) if the payment was matched wrongly, or a manual journal.
- **The company's balance** may not go below zero: the company lock (level 44, `LockForVoid`'s check, `COMPANY_BALANCE_SETTLED`).
- **Permission:** `cityledger.credit_note` (R5): what it does to the company is what a credit note does, without touching revenue.

What else it needs when it is built:
- **Migration:**
  - the check `payments_refund_company_ck` (a REFUND with `company_id` has `refund_of_payment_id`);
  - a deferred trigger `payments_company_refund_guard` (the refunded payment is a `CITY_LEDGER` PAYMENT of the same company, and
    the refunds of a transfer are at most its amount).
  - `returned` of an invoice is derived from the refunds of its lines' payments, like `paid`; nothing is stored.
  - Down refuses while a company refund exists.
- **Tests:**
  - b1: the transfer's net, the candidates and a new invoice, the balance, statement, aging, credit limit and night audit summary;
    the `CITY_LEDGER` reconciliation stays green;
  - b2: the invoice's `returned`, the PDF line, `TAX_INVOICE_EXISTS` with a live faktur, the replacement's smaller share;
  - b3: `RETURN_EXCEEDS_INVOICE`;
  - races with a receipt of the company (`RECEIPT_EXCEEDS_BALANCE`) and with a credit note to the same invoice (the company lock).

### 10.2 Alternatives rejected

| Alternative | Why not |
|---|---|
| **Reopen the guest folio**, return, refund, close it again | It changes `closed_on`, so the deposit release `ON_CLOSE` would run a second time. It reopens a folio of a departed stay to every other posting while it is open. Closing it again needs every check of the check-out. 10 states "there is no reopening", and no code path reopens a guest folio. Writing to the closed folio within one balanced transaction (§4) gets the same rows without any of this. |
| **A credit note to the company** (10's tool) for case b | It debits an allowance of **revenue**, while the outlet's revenue is right. It leaves the bill closed, so whoever ate is never charged. It does nothing for case a. |
| **A city ledger adjustment that also posts the bill's line** (no write on the guest folio) | The bill's line would be a lone `TRF` (no pair), which breaks the readiness rule that `TRF` lines and 1215 are zero at each close. Otherwise it needs a new code and journal with an exception for two control accounts. The guest folio would still show the wrong charge. |
| **Leave a credit on the closed folio**, refund later | The closed folio would hold a balance for days, which no screen or report expects. The refund is recorded when the money leaves, so there is nothing to wait for in the system (R2). |
| **A cashier pay-out** to the guest, outside the folio | It loses the link to the guest's payment and folio. The pay-out account would have to be the guest ledger, which only the day close posts. |
| **Three approvals** (reopen, return, refund) | §6. |

⚠ **The one deviation** is from 30 §4 item 7 ("a closed folio can never be the source or the target of a transfer or of a return"),
in this one use case only, with the conditions of §4. Everything else keeps CLAUDE.md:
- folio items are append-only;
- corrections are reversals and adjustments;
- nothing is written into a closed day;
- only the posting service writes folio items;
- every row is dated the open business date.

## 11. Decisions of the owner

Decided **by the owner on 2026-11-27** (*disetujui pemilik*): R1, R2, R4, R5 and R6 as recommended, with conditions; **R3 deferred**.

| # | Question | Decision |
|---|---|---|
| R1 | How is the closed guest folio corrected: rows written to it while it stays CLOSED (⚠ deviation from 30 §4 item 7), or reopening it? | **Rows written to it while it stays CLOSED**, in one balanced transaction, guarded by the database rule that a closed folio is balanced (§4). Conditions: before the global trigger `folios_closed_balanced` is written, every path that inserts into `folio_items` is found with `grep` and listed in §4, each with why it never writes to a CLOSED folio or always writes a balanced set; test 17 runs check-out → night audit → deposit release `ON_CLOSE` on a closed folio with the trigger on. |
| R2 | Case a: refund the guest in the same transaction, or leave a credit to refund later? | **In the same transaction, when the money actually goes out.** Condition: §3.a and the guide state the limitation: until the refund is recorded, the bill is not open again, so the guest who should pay can check out meanwhile. The guide asks the supervisor to do the correction as soon as possible, and, when the refund can be recorded the same day, to charge the right guest before they check out. |
| R3 | Case b (city ledger): build it now, or leave it for later? | **Deferred.** The use case refuses it (and c2 to the city ledger) with `RETURN_AFTER_CHECKOUT_CITY_LEDGER`, whose message (EN/ID) sends the user to the accountant (§3.b). The guide gives the manual way that works today, checked against the code; for example a credit note to the company's invoice, then a charge to the folio of the right payer, with the note that the credit note lowers revenue, so the accountant may need a reclassifying journal. Case b is recorded as KD-29 in `_laporan.md` (priority 2, money), pointing at §10.1, which keeps the design. |
| R4 | Case c: follow the one hop guest → master, and refuse longer chains? | **Yes, one hop only.** A master settled by payments follows case a; one settled by the city ledger is refused as R3. |
| R5 | Approval and permissions? | **One new kind `ROOM_CHARGE_RETURN_CLOSED`** (the amount X, `correction.approve`) for the whole act. The actor needs the permission of each step: `folio.reverse`, plus `folio.reopen` when the bill is closed, plus `payment.refund`. |
| R6 | The bill after the return? | **Left OPEN** for the cashier to settle with the existing flows. |

**A refund of a deposit payment** stays refused (`REFUND_OF_DEPOSIT_AFTER_CLOSE`, §3.a), unless a test proves that the journal view
books it right.
