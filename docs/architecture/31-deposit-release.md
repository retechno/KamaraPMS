# 31. Releasing a deposit against the charges it pays

Status: **plan APPROVED by the owner on 2026-10-14, with the decisions marked D below; built (section 9)** in its own PR, on top of PR #29 for the numbering of the migrations. Asked after the review of PR 6 (cancelling a group), in answer to the accounting question of section 1.

Words: a **deposit** is a payment the books keep in 2310 (advance deposits, a liability) because it was taken before the stay (guest or company folio) or before the first charge (master folio): the column `is_deposit` of the view `folio_item_gl` (`02-database-schema.md`, `30-group-folio.md` 5.2). A **settlement** is any other payment. **Release** is the move of a deposit from 2310 to 1210 (the guest ledger, an asset) when it stops being a liability and becomes what pays a charge.

## 1. The problem, with the numbers of the tests

`internal/accounting/groupcancel_test.go`: three reservations of a cancelled group hold deposits of 300,000 (A, kept), 200,000 (B, refunded) and 300,000 (C), and the master holds 500,000 (refunded). C is charged the fee of its cancellation, 200,000, which its deposit pays. After the day close:

| Account | Balance | What it is |
|---|---:|---|
| 1110 cash | +600,000 | 1,300,000 taken, 700,000 refunded |
| 1210 guest ledger | +200,000 | the fee of C, owed |
| 2310 advance deposits | -600,000 | A 300,000 and **C 300,000 whole** |
| 4510 fee revenue | -200,000 | the fee, once |

The deposit of C is a liability of 300,000 and, at the same time, the fee of 200,000 is a receivable from the same person: the balance sheet carries 200,000 on both sides. The reconciliation agrees because its rule says so (1210 = what the folios owe + the deposits they hold). **Today the two meet only when the folio is closed** (`DEPOSIT_RELEASE`, journaled on the day of `folios.closed_on`: debit 2310, credit 1210, the sum of the deposit lines of the folio); after the rest of C is refunded (100,000) and its folio closed, 2310 and 1210 of that folio are both zero (checked: 2310 -600,000 to -300,000 after the release of 200,000, 1210 200,000 to 0). A guest in house is booked the same way: the deposit stays whole in 2310 until the check-out. For a stay of days this is a small timing difference; for a cancelled reservation whose folio stays open for weeks (a kept deposit) it is a permanent gross-up.

## 2. The decision of the owner (2026-10-14)

A setting **per property**, "Release of deposits":

- **EVERY_DAY** (the default): at each day close the deposit is released against the charges it pays, folio by folio, on the day the charge is posted.
- **ON_CLOSE**: what the books do today (`DEPOSIT_RELEASE` when the folio closes).
- **Locked** as soon as the property has its first transaction (a folio item, a payment or a deposit): after that it can only be read, with "Locked since [date]". The choice and the lock are in the audit; the server refuses a change after the lock.
- Every property that exists (demo data only) moves to EVERY_DAY by a migration, with a one-time adjustment for the open folios that hold a deposit (section 6 explains how).
- The reconciliation of the control accounts and the day-close journal read the setting (sections 4 and 5).

## 3. The setting, its lock, its audit

- **Columns** on `properties` (the place of `refund_methods` and the other settings that are not accounting's own): `deposit_release_mode varchar(12) NOT NULL DEFAULT 'EVERY_DAY' CHECK (IN ('EVERY_DAY', 'ON_CLOSE'))` and `deposit_release_locked_on date NULL`.
- **The lock is made by the database**, not by the screen: an `AFTER INSERT` trigger on `folio_items` sets `deposit_release_locked_on` to the business date of the item when it is still null (a payment and a deposit are folio items, so every first transaction is caught, whichever path posted it, and nothing needs a second place to remember it). A `BEFORE UPDATE OF deposit_release_mode` trigger on `properties` raises `DEPOSIT_RELEASE_LOCKED` when `deposit_release_locked_on` is not null; the service checks first and answers 409 with the same code (the trigger is the last word, as for `business_day_must_be_open`). A new constraint name is mapped in `internal/platform/db/errors.go`, and the test that every name exists keeps it honest.
- **Who and where:** the accounting settings page (`accounting.manage`), `GET/PUT {P}/accounting/settings` gains `deposit_release_mode` and `deposit_release_locked_on` (read-only). Before the first transaction the person may change it, and each change writes `accounting.deposit_release_set` (old, new). The lock itself is a trigger and cannot write the audit through the writer; **(D) the entry `accounting.deposit_release_locked` (mode, date, the first item) is written by the first day close that finds `deposit_release_locked_on` set and no entry**, in the same transaction as the journal. (An entry written at the exact moment of the first posting would need the audit writer inside every posting path: not worth it; the date it records is exact.)
- **Screen:** a choice of two with a sentence for each; disabled with "Locked since 30 Sep 2026" after the lock; en and id.

## 4. The journal in EVERY_DAY

For a folio F and a business date D, from the view `folio_item_gl` as of D:

- **H(F, D)** = the deposit held = `-sum(signed_amount)` of the PAYMENT items with `is_deposit` (a refund of a deposit is a debit and reduces it).
- **U(F, D)** = what the deposit can pay = `max(0, sum(signed_amount) of the CHARGE items + sum(signed_amount) of the PAYMENT items that are not deposits)`: the charges (a fee, a night, a transfer line TRF, an adjustment, a reversal) less what a settlement already paid, never below zero.
- **Target T(F, D)** = `min(H, U)` while the folio is open and `H` when it is closed (a closed folio has a zero balance, so `H` equals `U` there).
- **So far S(F)** = the sum of the lines `DEPOSIT_APPLIED` and `DEPOSIT_RELEASE` already journaled for the folio (their debits on 2310).

At the close of D the day journal gains, for each folio with `T - S` not zero, a pair of lines **`DEPOSIT_APPLIED`, source_ref the folio number**: debit 2310 and credit 1210 for `T - S`; a negative amount is the same pair the other way (a charge of the same day was reversed, so less is applied). It is **one journal per day, as now** (the lines join the journal of the day close, no journal of its own), and the day is posted once (`gl_day_posts`), so the lines are written once. The posting is a function of the folios and of the earlier journals: nothing is stored on the folio.

- In EVERY_DAY the day close writes no `DEPOSIT_RELEASE` (the folio that closes already has `T = H`); in ON_CLOSE it writes no `DEPOSIT_APPLIED`. A folio of a property that changed mode before its first transaction has no lines of the other kind.
- A **master folio** follows the same formulas (its `is_deposit` keeps the rule of the first charge).
- The reason of the number: a deposit of 300,000 and a fee of 200,000 give `T = 200,000` on the day of the fee: 2310 is 100,000 (what can still be refunded), 1210 is 0, 4510 -200,000. The refund of the 100,000 and the close of the folio leave nothing to release. A deposit of 100,000 and a fee of 200,000 give `T = 100,000`: 2310 0, 1210 100,000 (what the person still owes). A guest in house with 300,000 and nights of 122,100 gets `T` of 122,100 after the first night, 244,200 after the second, 300,000 from the third and for ever after (the deposit is used up).

## 5. The reconciliation, for each mode

The control accounts are compared as of D with what the folios say (`ControlSources`), which does not read the journals; the formulas, with `FB` = the sum of all items of all folios to D:

| | ON_CLOSE (as it is) | EVERY_DAY |
|---|---|---|
| 2310 advance deposits | `sum over the folios open at D of H` | `sum over all folios of (H - T)` |
| 1210 guest ledger | `FB + (sum over the folios open at D of H)` | `FB + sum over all folios of (H - T)` |

Both read as `FB + the deposits still held as a liability` for 1210 and `the deposits still held as a liability` for 2310; they differ in what "still held" means (not yet closed, or not yet applied to a charge). `ControlSources` computes `T` from the folios in SQL (the same view), so the check stays independent of the lines that were journaled. The city ledger and payables controls do not change.

## 6. Migration 00078, and the properties that exist

**Next free migration: 00078.** (The change of `is_deposit` to a stored flag, promised after PR 6, is a different PR and takes the next number free at the time, 00079 if this one is built first; the two do not depend on each other: the formulas above read `is_deposit` from the view and will read the stored flag as they read the view.)

- **The lock must cost nothing after it is made (D).** The trigger on `folio_items` runs on every posting of every property, so it must not take a row lock on `properties` each time: it executes only `UPDATE properties SET deposit_release_locked_on = NEW.business_date WHERE id = NEW.property_id AND deposit_release_locked_on IS NULL`, and a row that is already locked does not match the `WHERE`, so it is not locked or written; before the lock the first posting takes the row lock once, and the second of two postings at the same moment waits for the first and finds the date set (PostgreSQL re-evaluates the `WHERE` after the wait, `READ COMMITTED`), so it does nothing. The date is the business date of the item that got there first. The property row is locked by the trigger only for that first posting, and only at the end of the transaction of the posting (after the folio and its items); whether any use case locks `properties` for writing before a folio (a deadlock with that one) is **checked when it is built** and held by the test of this item, not assumed.
- Adds the two columns and the two triggers. Every property that exists gets **EVERY_DAY**; one that already has items gets `deposit_release_locked_on` = the date of its first item (the trigger is created after the update, so the migration is not stopped by its own lock). The label of the new source type is added to the journal labels.
- **The one-time adjustment.** The owner asked for an adjusting journal for the open folios that hold a deposit. **(D) No journal is written by the migration; the first day close after it carries the adjustment.** At that close `S` is 0 for the open folios that hold a deposit, so `T - S` is their whole target and the `DEPOSIT_APPLIED` lines of that day are exactly the one-time adjustment (**D:** dated that day, with the description "includes the adjustment" on the line of each folio that was caught up, and the number of those folios in the audit entry of the close, `accounting.day_posted`, as `deposit_applied_adjusted_folios`). A folio that was closed before the migration was released by `DEPOSIT_RELEASE`, which is in `S`, so it gets nothing twice. **Business date: the date of that first close** (the open business date of the property at the time it runs). Reasons for not writing the journal in SQL at migration time: a journal dated in the past falls into a day already posted, a period already reported and perhaps a fiscal year already closed, so it would change closed numbers; a journal needs its number from the sequences, the accounts from the map of the property and the posting rules of the accounting module, all of which live in the service; and a property with accounting not set up has no journals to adjust. The numbers of every closed day stay as they are, and the reconciliation as of a closed day does not change. (If the owner prefers a journal inside the migration, it is the same lines dated the last closed day of each property, with a risk for a closed fiscal year; say so and it is written that way.)
- **Down:** it **refuses** (`RAISE EXCEPTION`) when any journal line with source `DEPOSIT_APPLIED` exists, because the old code would not know those lines and the reconciliation would break; before the first close after the migration (nothing applied yet) it drops the triggers and the columns. The rows cannot be un-posted, and that is the reason for the refusal.

## 7. Tests (both modes, real PostgreSQL, the reconciliation checked at every close)

For each of ON_CLOSE and EVERY_DAY, with the expected balances of 1210, 2310, 4xxx, cash and the reconciliation after each close:

1. **A guest in house with a deposit, then the check-out:** the deposit of 300,000, three nights, a settlement of the rest; EVERY_DAY applies it night by night (122,100, 244,200, 300,000) and has nothing left at the close of the folio, ON_CLOSE holds it whole until then.
2. **A cancelled reservation with a deposit kept and the fee of its cancellation** (case C: 300,000 and 200,000): after the day 2310 -100,000 and 1210 0 (EVERY_DAY) or 2310 -300,000 and 1210 +200,000 (ON_CLOSE); 4510 -200,000 in both.
3. **A deposit smaller than the fee** (100,000 and 200,000): 2310 0 and 1210 +100,000 (EVERY_DAY); later the person pays the rest.
4. **The refund of the rest, then the close of the folio:** 2310 and 1210 of the folio end at zero in both modes, the cash is the fee.
5. **A reversal the same day** of the charge a deposit paid (EVERY_DAY gives back the applied part, a negative `T - S` of the same day), a **transfer** of a charge to another folio, a **settlement by card** that makes `U` smaller than the deposit, and the **master folio** with its deposit before the first charge.
6. **The lock:** the setting changes before the first transaction (audit `accounting.deposit_release_set`); after the first folio item, payment or deposit the service answers `DEPOSIT_RELEASE_LOCKED`, a direct `UPDATE` is stopped by the trigger, the date is the date of the first item, and the first day close writes `accounting.deposit_release_locked` once.
7. **The migration:** a property with an open folio that holds a deposit and a closed one that was released, before and after: the first close carries the catch-up only for the open one, the reconciliation as of every closed day is unchanged, and the one after the first close agrees; the Down refuses after that close and works before it.
8. **(D) Two first postings at the same moment on a property that is not locked** (two transactions in parallel, each posting the first payment of a different folio): neither waits for ever or fails on a deadlock, `deposit_release_locked_on` is the earlier business date of the two, and the first day close writes **one** entry `accounting.deposit_release_locked`; and **after the lock a posting does not touch the property row** (a test reads `xmin` of the row before and after a thousand postings, and that two postings in parallel on a locked property take no row lock on it).
9. **A property chosen ON_CLOSE before its first transaction** behaves exactly as today (the tests that exist for `DEPOSIT_RELEASE` run unchanged in this mode).

## 8. Not in this plan

The release of a deposit by a person (a button that applies a deposit to a charge at will), a change of mode after the lock, and the stored `is_deposit` (its own PR).

## 9. As built (migration 00078)

- **Where the setting is.** There is no screen or route of accounting settings (`accounting_settings` holds only the start date and is made by the setup of a property), so the choice is one more field of the **settings of the property**: `deposit_release_mode` in `POST /properties` and `PATCH /properties/{id}` (`property.manage`), and `deposit_release_locked_on` (read-only) in the property read. The page is Settings, Property: a choice of two with a sentence for each, disabled with "Locked since [date]" after the lock. A change of the mode writes `property.deposit_release_set` (old, new); after the lock the service answers 409 `DEPOSIT_RELEASE_LOCKED` (with `context.locked_on`; the same value is not a change) and the trigger of the database is the last word (`deposit_release_mode_locked`, in `internal/platform/db/errors.go`).
- **The database.** `properties.deposit_release_mode` (default `EVERY_DAY`), `deposit_release_locked_on` and `deposit_release_adjust_pending`; the function `folio_deposit_state(property, date)` (held, payable, closed, applied, section 4, read from `folio_item_gl`, only folios not closed before the date), which the day close and the reconciliation both read; the trigger `folio_items_lock_deposit_release` (the conditional `UPDATE ... WHERE deposit_release_locked_on IS NULL`: a locked property is not locked or written by a posting, which a test holds by the `xmin` of the row) and the guard `properties_deposit_release_mode_locked`. The migration locks the properties that have items at the date of their first item and sets `deposit_release_adjust_pending` for those that have payments.
- **The day close** (`internal/accounting/journals.go`, `DayActivity`): in EVERY_DAY the lines `DEPOSIT_APPLIED` (the folio number as the reference; debit 2310 and credit 1210, or the other way for a negative amount) replace `DEPOSIT_RELEASE`; in ON_CLOSE `DEPOSIT_RELEASE` is written as before. The first close with `deposit_release_adjust_pending` marks the lines of the folios it carries with "includes the adjustment", puts their number in the entry `accounting.day_posted` (`deposit_applied_adjusted_folios`) and clears the flag. The first close that finds the lock writes `accounting.deposit_release_locked` once (`mode`, `locked_on`, the first item). The **reconciliation** (`ControlSources`) reads the same function: section 5.
- **The tests** (`internal/accounting/depositrelease_test.go`, real PostgreSQL, the reconciliation and the account of the transfers at every close): a guest in house with a deposit and the check-out, the cancelled reservation with a kept deposit and a fee (the case of 300,000 and 200,000), a deposit smaller than the fee, a charge that moves away (the applied amount given back), the master folio, each in both modes with their own expected balances; the lock (the date, the refusal of the database, no write to the row after it, one entry of the audit); two first postings at the same moment; the first close after the migration (the adjustment, the folios counted, the days already posted unchanged); the setting through the property. **The tests that were written for the old books run in ON_CLOSE** (`setupHotel` chooses it before the first transaction; `setupHotelMode` chooses either), which is what "ON_CLOSE behaves exactly as today" means in practice.
- **Not done:** the migration test of `scripts/db-test.sh` for 00078 (the migration adds columns, so it cannot be run again on a database that has it, as 00076 and 00077 are; the migration is covered by the up/down/up of the script and by the test of the first close above, which makes the same changes by SQL).
