# 32. Who may approve a correction: rules by kind of approval and by amount (PLAN: nothing is built; waiting for the owner)

Status: **plan, APPROVED by the owner on 2026-10-15** (code starts after PR 31 is merged; the two mappings to verify, `folios/nightadjust.go` and `bankrec.Reopen`, are written up in an "as built" part when the PR is done), a PR of its own, after the release of deposits (document 31). Asked by the owner on 2026-10-14 (review of PR 6); **changed by the owner the same day**: not one switch, but **a rule for each kind of approval and for each amount, per property**. Decisions of the owner are marked **D**.

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
