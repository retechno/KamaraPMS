# 32. Who may approve a correction: rules by kind of approval and by amount

Status: **approved (2026-10-15)**, built in a PR of its own after the release of deposits (document 31): migration 00079, as built in section 9. Asked by the owner on 2026-10-14 (review of PR 6); **changed by the owner the same day**: not one switch, but **a rule for each kind of approval and for each amount, per property**. Decisions of the owner are marked **D**.

## 1. What exists

A correction carries `approval: { email, password }`: the approver's own credentials, checked by `iam.Service.VerifyApproval` and `VerifyApprovalFor` (`internal/iam/approval.go`); the approver must hold `correction.approve` (or another permission for the approval of a rate override or of a budget) at the property; a tenant administrator holds every permission. **The actor may approve their own correction** (`06-api.md` 14.1: "A separate approver is optional"); the audit entry carries `actor` and `approved_by`, equal when self-approved. There is no setting, and no constraint of the database (`approved_by` only has to be present). Every approval passes through the one function.

## 2. The kinds of approval (D)

The use cases that call the function (25 files) are grouped into **kinds a person understands**. The kind of a use case is fixed in the code; the rule of a kind is the property's. **The amount** of a kind that has one is the **absolute value of the total of the correction in one request**, defined here once and not left to each caller:

| Kind (screen label) | Use cases that ask for it | Amount (what is added up) |
|---|---|---|
| `FOLIO_ADJUSTMENT` (adjustment of a folio) | `folios.PostAdjustment` | `abs(amount)` of the adjustment |
| `FOLIO_REVERSAL` (reversal, void) | `folios.Reverse` (a charge), `folios.Void` (a payment) | the amount of the charge reversed, or of the payment voided |
| `REFUND` (refund) | `folios.Refund` | the amount refunded |
| `FOLIO_TRANSFER` (transfer between folios) | `folios.transferApproval` (items, a code, a balance, a return), `folios.MoveToMaster` | **the sum of every item or balance moved in the request** (a transfer of many items adds them all) |
| `STAY_RATE` (rate of a stay) | `frontdesk.ChangeRates`, the corrections of a posted night (`folios/nightadjust.go`) | the sum over the nights of `abs(new price - price in force)` |
| `RATE_OVERRIDE` (override of a rate at booking) | `reservations.requireOverrideApproval` (one reservation and the bulk booking of a group) | the sum over the nights of `abs(price given - price in force)`, over every row of the request |
| `SALES_RESTRICTION` (breaking a sales restriction) | `reservations.requireSellable` | none |
| `FREE_ROOM` (a room that is free) | `reservations.requireFreeApproval` | the value of the nights given free: the price in force of those nights |
| `CITY_LEDGER_CREDIT` (credit note, write-off) | `cityledger.CreateCreditNote`, `CreateWriteOff`, `VoidAdjustment` | the amount of the note, of the write-off or of the one voided |
| `CITY_LEDGER_VOID` (void of an invoice or a receipt) | `cityledger.VoidInvoice`, `VoidReceipt` | the total of the invoice, the amount of the receipt |
| `JOURNAL` (manual journal) | `accounting.Reverse` (the reversal of a journal) | the total debit of the journal |
| `FISCAL_YEAR` (closing and reopening a fiscal year) | `accounting.ReopenFiscalYear` | none |
| `BANK` (bank reconciliation) | `bankrec.Reopen` | none |
| `BUDGET` (budget) | `budget.Activate` | none |
| `PAYABLES` (accounts payable) | `payables.VoidBill`, `VoidCreditNote`, `VoidPayment` | the total of the document voided |
| `TAX_DOCUMENT` (tax filing and tax invoices) | `taxfiling.VoidReturn`, `VoidPayment`, `SetOpeningCredit`, `VoidOpeningCredit`, `taxinvoice.VoidInvoice` | the tax amount of the document (the opening credit: its amount) |
| `TAX_SETTINGS` (tax settings) | `taxfiling.ChangeSettings` | none |
| `SHIFT_VARIANCE` (cashier shift difference) | `shifts.Close` | `abs(counted cash - expected cash)` |

The list lives in one table in `internal/iam` (the kind, the label key for en and id, whether it has an amount, the permission its approver must hold); a use case that is not in it does not compile (section 5).

## 3. The rules and where they are kept (D)

- A rule is **`FREE`** (the default: anyone who holds the permission may approve, **including the person who acts**, as today), **`DISTINCT`** (the approver must be someone other than the person who acts) or **`DISTINCT_ABOVE`** (the person who acts may approve their own correction **up to and including a limit**; above it the approver must be someone else). A kind without an amount takes only `FREE` or `DISTINCT`; the server refuses `DISTINCT_ABOVE` on it (`422`, `RULE_NOT_ALLOWED_FOR_KIND`), and refuses a `DISTINCT_ABOVE` without a limit or with a negative one.
- **Table** `property_approval_rules (tenant_id, property_id, kind varchar(30), rule varchar(14), threshold numeric(18,3), updated_at timestamptz, updated_by bigint, PRIMARY KEY (property_id, kind))`, a composite foreign key to the property, `CHECK (rule IN ('FREE', 'DISTINCT', 'DISTINCT_ABOVE'))`, `CHECK ((rule = 'DISTINCT_ABOVE') = (threshold IS NOT NULL))`, `CHECK (threshold IS NULL OR threshold >= 0)`. **No row means `FREE`**: choosing `FREE` deletes the row. The kind is validated by the application against the table of section 2 (the database only requires capital letters and underscores, so a new kind is not a migration).
- **Every change is audited** (`property.approval_rule_changed`: the kind, the old rule and limit, the new). It needs `property.manage`. Nothing about the books depends on a rule, so it may be changed at any time, in both directions.
- **API:** `GET {P}/approval-rules` answers every kind with its rule and limit (the default `FREE` for a kind without a row), its label, whether it has an amount and the permission its approver needs; `PUT {P}/approval-rules` takes `{ rules: [{ kind, rule, threshold }] }` and changes the kinds named.

## 4. The check: one place (D)

`VerifyApprovalFor` gets the kind and the amount: `iam.Service.Verify(ctx, ApprovalRequest{ PropertyID, Input, Kind, Amount })` replaces the two functions (the permission to hold comes from the table of kinds, not from the caller). After the credentials and the permission are good:

1. The rule of the kind is read (one read by the primary key; no row is `FREE`).
2. `FREE` → accepted. `DISTINCT`, or `DISTINCT_ABOVE` with `amount > threshold` (**equal to the limit is allowed**), and **the approver is the actor** → the check refuses with **403 `APPROVAL_SAME_USER`**, `context`: `kind`, `rule`, `threshold`, `amount`. It is not counted as a failed password (the credentials were right). In every other case the approval is accepted.
3. **The exception for the tenant administrator (D):** when the rule would refuse and the approver is a tenant administrator who is also the actor, the approval is **accepted** and carries the marker, which every use case writes in its audit entry as `self_approved_by_admin: true` (next to `actor` and `approved_by`). With no rule in force, or a rule that is satisfied, nothing is marked. The `Approval` value gets `AuditFields()`, the one way the fields (`approved_by`, the marker) are added to an entry; the audit list gets the filter `self_approved=true` (the page and the export) to trace them.
4. A kind with an amount and a call without one is a programming error, caught by the structural test below, never a refusal of the user.

**The callers must send the kind and the amount (D).** A structural test (as the guard of the sale paths, `salepath_guard_test.go`) reads the source with `go/parser`: every call of `Verify` outside `iam` names a kind of the table, passes an amount when the kind has one, and the use case adds `AuditFields()` to its entry; every kind of the table is used by a use case. A new use case that asks for an approval and forgets any of that fails the test.

## 5. The screens (D)

- **Settings → Property → Approvals:** a table of **kind × rule × limit** (the limit is a field that exists only for a kind with an amount and a rule `DISTINCT_ABOVE`), with the label of the kind and the permission its approver needs; saved with `PUT`. en and id. A **warning** (a notice, not a block) for each kind whose rule is `DISTINCT` or `DISTINCT_ABOVE` when **fewer than two active users other than the administrators hold the permission of its approver at the property**: "Only one person can approve this; they cannot approve their own, so it needs an administrator."
- **The dialog of approval** (`ApprovalDialog`, shared by every correction) receives the kind and, when the screen knows it, the amount, and says the rule in force for that correction: "Above 500,000: the approver must be someone other than you" (when the amount is above the limit), "Up to 500,000 you may approve it yourself" (below), "The approver must be someone other than you" (`DISTINCT`), nothing for `FREE`. **The email is not filled in with the person's own when the rule asks for someone else** (it is left empty), **except for a tenant administrator**, for whom it stays as it is and the line says that an administrator may approve their own and that it is recorded. The rules are read once for the property (`GET {P}/approval-rules`) and kept by the property store; a refusal that comes anyway (`APPROVAL_SAME_USER`) has its text in en and id and names the limit from `context`.

## 6. Migration, API, documents

**Migration: the number after the one of document 31 (00078), so 00079**, if that PR is built first; if this one comes first it takes 00078 and the other the next: the plans do not touch the same tables. It creates the table (no rows: every kind is `FREE`, nothing changes for any property). Down: drops the table. The documents `06-api.md` 14.1 and the OpenAPI say that the actor may approve their own correction **unless the rule of the property says otherwise**, and list the new routes and the code `APPROVAL_SAME_USER`.

## 7. Tests (real PostgreSQL)

For **each family of kinds** (folio: adjustment, reversal and void, refund, transfer; stay rate; rate override and sales restriction and free room; city ledger credit and void; journal; fiscal year; bank; budget; payables; tax; shift difference), through the use case that asks for the approval:

1. **`FREE`:** the actor approves their own correction; no marker.
2. **`DISTINCT`:** the actor approving their own is refused, 403 `APPROVAL_SAME_USER`, with the context, nothing written, no failed attempt counted; another user who holds the right is accepted and is the `approved_by`.
3. **`DISTINCT_ABOVE`** with a limit of 500,000: **below** the limit the actor approves their own, **at the limit** too, **above** it is refused; another user above the limit is accepted.
4. **The tenant administrator** as the actor and the approver, under `DISTINCT` and above the limit: accepted, the entry has `self_approved_by_admin: true`, the audit filter finds it; an administrator who makes the change and another user who approves: no marker; with `FREE`: no marker.
5. **The amounts:** a transfer of many items adds all of them (three items of 200,000 against a limit of 500,000 are refused although each is below it); a refund, an adjustment (negative too: `abs`), a rate override and a shift difference by their definitions in section 2.
6. **The rules:** a kind without an amount refuses `DISTINCT_ABOVE`; a limit is required and not negative; `FREE` deletes the row; a change is audited with the old and the new; `property.manage` is needed; no row means `FREE`.
7. **The structural test** of section 4.
8. **The screens** (en and id): the table and its warning, the dialog with each of the four lines, the email left empty and the exception for an administrator.

## 8. Not in this plan

Two people for one correction (a second approval on top of the first), a queue of corrections that wait for another person's decision, and a rule for each user or role: the rule says only "not the same person", by kind and by amount.

## 9. As built (PR 32, migration 00079)

What was built is the plan above; this part says what was found when the callers were read, and where the code differs from the words of the plan.

**The two mappings the plan left open (verified in the code):**

- `folios/nightadjust.go` (`AdjustChargedNights`) **does not ask for an approval itself**: it posts the adjustments of the charged nights with the `Approval` of its caller, `frontdesk.ChangeRates`, which asks for it. The kind is `STAY_RATE` as planned, and so are both approvals the change of a rate can need (the correction of a charged night, `correction.approve`; a rate that goes down, `reservation.override_rate_approve`, replaced by the permission of the request, `ApprovalRequest.Permission`). The **amount** is the sum, over the nights that change, of `abs(new price - price in force)`, the rate difference and not the gross of the adjustment (which the engine computes with tax and service charge): one amount for both approvals of the same change. A change of the rate of a night that is not charged has the same kind and amount.
- `bankrec.Reopen` is the reopening of the latest reconciled statement: it moves no money, so `BANK` **has no amount** as planned (a statement has balances, but none of them is what the correction changes).

**The kinds.** `internal/iam/approvalkinds.go` holds the 18 kinds, whether each has an amount and the permission its approver needs. The amounts as they were found:

| Kind | Amount, as built | Where it is known |
|---|---|---|
| `FOLIO_ADJUSTMENT` | `abs(amount)` | before the transaction |
| `FOLIO_REVERSAL` | debit + credit of the item reversed, or the amount of the payment voided | read before the transaction, without a lock |
| `REFUND` | the amount refunded | before the transaction |
| `FOLIO_TRANSFER` | the sum of what the request moves: the charges named or those of the code that can still move (read before the locks), the balance handed over, the transfers returned, the `expected_total` of the move to the master | before the transaction |
| `STAY_RATE` | see above | read before the transaction from the nights without a lock; the transaction asks again |
| `RATE_OVERRIDE` | the sum, over the nights of every room of the request, of `abs(price given - price in force)` (the price after the yield rules; a night with no price counts its whole amount) | while the lines are priced |
| `SALES_RESTRICTION` | none | |
| `FREE_ROOM` | the value of the nights given free: the grid price of the **reference rate plan** for the room type (the plan that values the free rooms in the report of free rooms), added over the lines of the request; zero when the property has no reference plan | while the lines are priced |
| `CITY_LEDGER_CREDIT` | the total of the credit note **with its tax**, the amount of the write-off, the amount of the adjustment voided | the credit note: the net before the transaction, the total under the locks |
| `CITY_LEDGER_VOID` | the total of the invoice, the amount of the receipt | read before the transaction |
| `JOURNAL` | the total debit of the journal reversed | read before the transaction |
| `FISCAL_YEAR`, `BANK`, `BUDGET`, `TAX_SETTINGS` | none | |
| `PAYABLES` | the total of the bill or credit note, the amount of the payment | read before the transaction |
| `TAX_DOCUMENT` | the amount of the opening credit (set or voided), the tax of the return, the amount of the tax payment, the VAT of the tax invoice | read before the transaction |
| `SHIFT_VARIANCE` | `abs(counted - expected)` | the expected cash is read before the transaction, without a lock; the transaction applies the rule again to the difference it finds |

**Where the amount is known only under the locks** (the total of a credit note, the difference of a shift), the approval is checked before the transaction with what is known (a password is never hashed under a lock) and `iam.Service.Extend` applies the rule again, inside the transaction, to the exact amount: an approval of someone else stays as it is; one of the person who acts, now above the limit, is refused (or replaced by the credentials in the request, if there are any). The same call serves a request that prices several lines (a booking of several rooms, a group): the credentials are checked once, and each line adds its amount to the total of the request (`reservations/approvals.go`).

**The check.** `iam.Service.Verify(ctx, ApprovalRequest{PropertyID, Input, Kind, Amount, Permission, Own})` replaces `VerifyApproval` and `VerifyApprovalFor`. `Permission` is empty unless a use case asks another permission of its approver than the kind does (a lower rate). `Own` is for the use cases whose person who acts approves **without a password** when they hold the permission themselves (a rate override, a free room, a sales restriction, a lower rate): under a rule that asks for someone else they get `422 APPROVAL_REQUIRED` (with the rule in the `context`), because the screen has to ask for the credentials of someone else; a tenant administrator is accepted and marked. Outside `Own`, `403 APPROVAL_SAME_USER`, as planned. `Approval.AuditFields(data)` writes `approved_by` and the marker `self_approved_by_admin: true`; `Approval.Marker(data)` only the marker, for an entry that names the approver under another key (`override_approved_by`, `rate_approved_by`, `free_room_approved_by`, the entry of a restriction override).

**The rules.** `property_approval_rules` as planned (no row is `FREE`, `FREE` deletes the row). `GET` answers every kind with its rule, its limit, whether it has an amount, its permission and **`approvers`: the number of active users, other than tenant administrators, who hold that permission at the property, for every kind** (the plan had it only for a kind with a rule; the screen needs it to warn before a rule is saved). `PUT` changes the kinds named in one transaction and audits each one that changes (`property.approval_rule_changed`, entity `approval_rule`, label the kind, the old and the new rule); the same rule again writes nothing. Any grant on the property reads the rules (the dialog of approval needs them); changing them is `property.manage`. The limit is checked at the currency decimals of the property.

**The audit.** `GET {P}/audit-logs?self_approved=true` (and the filter of the audit screen) lists the entries whose data carry `self_approved_by_admin: true`.

**The screens.** Setup, "Approval rules" (`/setup/approval-rules`): one row for each kind with its label, what its amount is, the permission its approver needs, the rule and, for the rule above a limit, the limit; on a phone each kind is a block. The warning shows when the rule asks for someone else and fewer than two people besides the administrators can approve that kind, and it follows the choice before it is saved. `ApprovalDialog` takes `kind` and, when the screen knows it, `amount`, and says the rule (the four lines of section 5, and the line for an administrator); every screen that asks for an approval passes its kind. Screens that do not know the amount (a credit note whose tax is computed by the server, a void of a document chosen from a list, a group booking) pass none, and the dialog then says the limit instead of whether this amount is above it; the server decides in every case.

**The structural test** (`internal/iam/approvalcallers_test.go`) reads the source of every module with `go/parser`: every `iam.ApprovalRequest` outside `iam` names its kind with a constant of the table, passes `Amount` when the kind has one, the package writes `AuditFields` or the marker in an audit entry (checked by package, not by use case: the entries of the reservations are written by helpers), and every kind of the table is asked for by some use case. The helper of the reservations (`approveRun`) is read as a call with its kind in the third argument. The test was checked to fail when a call forgets its amount.

**Tests.** `iam`: the rule at the limit (below, at, above; negative amounts; a kind without an amount), the exception of the administrator and its marker, the credentials of another person, `Own` and `Extend`, no failed attempt counted for `APPROVAL_SAME_USER`, validation and audit of the rules, the tenant. For each family, through the use case: folios (adjustment, reversal and void, refund, a transfer of three items of 200,000 against a limit of 500,000, the return of transfers), stay rate, rate override (two rooms of 150,000 against 200,000), free room (value of the nights), sales restriction, city ledger, journal and fiscal year, bank, budget, payables, tax, tax invoice and shift. Web: the dialog (the lines, the email left empty, the administrator, the refusal that names the limit), the screen of the rules (en and id, the warning, read-only), the filter of the audit.

**Not changed:** a property with no rule behaves as before (the table starts empty); the idempotency replay does not ask for an approval again; nothing about the books depends on a rule.
