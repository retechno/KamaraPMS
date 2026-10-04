# 11. Cashier shifts, budget and cash flow, card settlement

Status: **approved 2026-10-04 with these decisions (they change nothing without a new approval): (1) a cashier shift is required for cash by default
(`require_shift_for_cash` on for every new property; properties that existed before the migration keep it off until they turn it on); (2) over/short always needs
an approval, `max_variance` default 0; (3) the safe is only the DROP movement, no safe account; (4) the night audit is blocked while a shift is open; (5) card: one
MDR rate per payment method with effective-date history, **and the payment keeps a snapshot of the rate and of the fee computed from it**; (6) budget is money only,
per account per month; (7) cash flow is the indirect method from the existing journals, reconciled to the change of the cash accounts; (8) order A shifts, C card, B budget and cash flow.**
Built so far: see the README. Items 3, 4 and 5 of the finance order in
`08-backlog.md`. They are independent; the order proposed is A (shifts), C (card, the smallest), B (budget and cash flow).
Each is built the usual way (DB, backend, API, frontend, tests together) and committed on its own.

## What exists today (read from the code)

- `payments` (append-only except POSTED to VOIDED) has the method (CASH, CARD, BANK_TRANSFER, OTHER), `created_by`, `paid_at`
  and `business_date`, but **no shift and no drawer**. The cashier report sums payments by date and method; it does not know
  who held the cash.
- City ledger receipts in CASH also go into the drawer (they are in `city_ledger_receipts`, with `created_by`).
- The day close journal (`DAY_CLOSE`) debits the account of each method, one line per payment. No journal knows about a drawer count.
- Card: card payments sit in the clearing account CARD (OTHER for e-wallets) until the payout. **Settlement is already built**
  (`internal/bankrec/settlement.go`, `card_settlements`, `card_settlement_items`, migration 00033): from a bank statement line the
  payment lines are picked by hand, the commission account is chosen, and a BANK journal debits the bank (net) and the commission
  (difference) and credits the clearing account (gross). Each payment line is settled once.
- Accounts have `statement_group` (CASH, RECEIVABLES, ... FIXED_ASSETS, EQUITY, REV_*, EXP_*, UND_*, DEPRECIATION, ...). The USALI
  income statement and the balance sheet add them up; there is no budget and no cash flow statement.

---

## A. Cashier shifts and the cash drawer

### Model

- `cashier_shifts`: `shift_number` (new sequence `SHIFT`, `SHF000001`), `user_id` (the cashier), `drawer` (varchar, a label such as
  FRONT or NIGHT; one open shift per drawer), `business_date_opened`, `opened_at`, `opening_float`, `status` OPEN or CLOSED,
  `closed_at`, `business_date_closed`, `expected_cash`, `counted_cash`, `over_short` (counted minus expected), `variance_reason`,
  `journal_id` (when over/short is posted), `closed_by`. Header columns of a closed shift never change (guard trigger).
- `cashier_shift_movements` (append-only): `shift_id`, `kind` DROP (cash taken to the safe), PAY_IN (cash added that is not a guest
  payment), PAY_OUT (petty cash paid), `amount`, `account_id` (PAY_IN and PAY_OUT: the account on the other side), `reason`,
  `business_date`, `journal_id` (PAY_IN and PAY_OUT only), `approved_by`.
- `cashier_shift_counts`: the count by denomination at close (`denomination`, `quantity`); optional, the total must equal
  `counted_cash` when given.
- `payments.shift_id` and `city_ledger_receipts.shift_id`: nullable, set at insert for method CASH (a composite FK ties the shift to
  the property). Not changed afterwards, so the append-only rule of the tables holds.
- `property_cashier_settings` (one row per property): `require_shift_for_cash` (default **off**, so existing hotels keep working),
  `max_variance` (over/short above it needs an approval to close, default 0 = always needs one), `block_night_audit_with_open_shift`
  (default on).
- New system account key `CASH_OVER_SHORT` (default: an expense account of undistributed expenses; USALI chart gets it by a migration like 00042
  did for INPUT_VAT).

### Rules

1. **Expected cash** = opening float + CASH payments of the shift (PAYMENT) - CASH refunds + CASH city ledger receipts + PAY_IN - PAY_OUT
   - DROP. Voided payments count as nothing (a void after close is a correction of the day, see 5).
2. **Opening**: one open shift per cashier and per drawer (partial unique indexes). The suggested float is what the previous shift of the
   drawer left (its counted cash less its drops); the cashier may change it, the difference is on the shift report.
3. **Posting cash** with `require_shift_for_cash` on needs the caller's open shift (`NO_OPEN_SHIFT`, 409); the payment gets its id. With the
   setting off, cash without a shift still works and is shown as "not on a shift" in the shift report of the day.
4. **Closing**: the cashier counts, the server computes expected and over/short. Over/short beyond `max_variance` needs a reason and an
   approval of another user with `cashier.shift_approve` (the existing `iam.VerifyApproval`). Over or short is journaled on the business date:
   short = Dr CASH_OVER_SHORT, Cr CASH; over = Dr CASH, Cr CASH_OVER_SHORT (journal type `CASHIER`, `source_type SHIFT`). A zero difference posts nothing.
   A shift is closed once; there is no reopening (a mistake is a new movement or a correction journal).
5. **Voiding a cash payment of a closed shift**: the void keeps working as today (it needs approval), but the shift is already closed, so the
   void is counted in the **shift that is open when it happens** (as a negative line "voided from shift X"); the closed shift never changes.
6. **Handover**: closing with `hand_over_to` (a user) opens nothing by itself; it records who gets the drawer and the next shift of that drawer
   suggests the float. Two people never share one open shift.
7. **Drops**: move cash to the safe and are not journaled (the cash stays in CASH). A drop to the bank is out of scope; the bank deposit of cash is a bank
   statement line matched with the CASH account as today.
8. **Pay-in and pay-out** are journaled on the business date (Dr/Cr CASH against the chosen account; a pay-out to an expense account, a pay-in to
   an income or liability account). Both need `cashier.movement`; a pay-out above `max_variance` rule is not applied (no limit in v1).
9. **Night audit**: with `block_night_audit_with_open_shift` the run is refused (`SHIFTS_OPEN`, listing the shifts) so that the day's cash is counted
   before the day closes. A shift may be open across midnight only after the setting is turned off.
10. Every action writes an audit entry in the same transaction. All business-dated writes call `RequireOpenBusinessDay` first.

### Locks and tests

New lock level **cashier shifts** between payments and companies in the order (folios -> payments -> **shifts** -> companies ...): posting a
cash payment takes the shift `FOR SHARE` after the payment locks; closing takes it `FOR UPDATE`, so a payment cannot slip in while the count is made.
Tests: expected cash arithmetic (payments, refunds, receipts, movements), one open shift per cashier and drawer (race), closing with variance
(approval, journal, balanced), payments after close refused, void of a closed-shift payment lands in the open shift, night audit refused, tenant
isolation, schema tests for the checks (amount > 0, kind, closed shift immutable).

### API and screens

`GET/POST {P}/cashier/shifts` (list with `status`, `user`, `from`, `to`; open), `GET {P}/cashier/shifts/{id}` (with movements, payments and the
expected cash so far), `POST .../{id}/movements`, `POST .../{id}/close` (`counted_cash`, `counts?`, `reason?`, `approval?`, `hand_over_to?`),
`GET .../{id}/report.pdf` (the Z report), `GET/PUT {P}/cashier/settings`. Permissions: `cashier.shift` (open and close one's own shift, record movements),
`cashier.shift_manage` (see every shift, close another's), `cashier.shift_approve` (approve a variance), `cashier.settings`. Frontend: a shift bar on the
cashier screen (open shift, float, drop, pay-in, pay-out, close with the count and the variance), a shifts list, the Z report, the settings card.

---

## C. Card settlement (what is left after the part that exists)

What is missing is **expectation**: today the commission is only discovered when the payout is posted, and the payment lines are chosen by hand.

- `card_fee_rules` (per property): `payment_method` (CARD or OTHER), `card_brand` nullable (VISA, MASTERCARD, DEBIT, QRIS, ...; nothing today records the brand, so
  v1 is one rate per method, with the brand column ready), `mdr_rate` (percent, 0-100, 4 decimals), `settlement_days` (usually 1-3), `effective_from` (history, a rule
  never edited). Used only to **expect** the fee and the payout date.
- **Expected settlement** report: card payment lines not yet settled, with expected fee (gross x rate, rounded) and expected payout date (payment date + days), so the
  cashier sees what the bank should pay and when; the **late** ones (past the expected date and not settled) are listed.
- **Propose a settlement** from a statement line: the unsettled payment lines are ordered by date and the oldest ones whose expected net sums to the line (within a
  tolerance) are pre-selected; the user confirms. The existing settle call and journal are used unchanged, so nothing new is posted by a proposal.
- **Fee variance**: the settlement shows expected fee against the fee taken (difference per settlement); a difference above a tolerance is flagged, nothing blocks it.
- **Tax on the fee** (optional, `fee_input_vat` flag on the rule): if the acquirer's invoice carries VAT, the fee line is split into the commission account and INPUT_VAT
  (only for a PKP property that claims input VAT, same switch as supplier bills); off by default.
- No change to the settle journal, to `card_settlements` or to the rule that each payment line is settled once.
- API: `GET/POST {P}/bank/card-fee-rules`, `GET {P}/bank/card-settlements/expected`, `GET {P}/bank/statements/{id}/lines/{line}/settlement-proposal`. Permission `bank.view` to read,
  `bank.manage` to write. Tests: rule history by date, expected fee rounding at the currency decimals, proposal picks the oldest lines, late list, tenant isolation.

---

## B. Budget and cash flow

### Budget

- `budgets`: `fiscal_year_id` (the existing fiscal years), `name`, `version` (a year may have several versions, one **ACTIVE**), `status` DRAFT, ACTIVE, ARCHIVED.
  `budget_lines`: `budget_id`, `account_id` (a postable account of revenue or expense type), `month` (1-12 of the fiscal year, so a year that does not start in
  January works), `amount` (numeric(18,3), the normal side of the account, so a revenue budget is positive revenue and an expense budget positive expense).
  An ACTIVE budget cannot be edited (a revision is a new version copied from it); an ARCHIVED one is read only.
- Entry: a grid (account x month) in the screen, **CSV import and export** (same shape as the chart import: validated all or nothing, the rows that are wrong
  listed), **copy from last year's actuals** with a percent change, and spread of a yearly figure over months (equal or by last year's pattern).
- Budget of the **statistics** (rooms available, occupied, ADR) is not part of v1: the budget is money only. Occupancy based drivers are later.
- **Budget against actual** report (`from`, `to` in months of one fiscal year): per account and per **USALI department** (the account's `statement_group`:
  REV_ROOMS, REV_FB, EXP_ROOMS, UND_AG, ...), actual (journals of the period, the same source as the income statement), budget, variance (amount and percent, favourable
  or not depending on revenue or expense), and the totals at the USALI subtotals (department profit, GOP, EBITDA, net income) so it reads like the income statement. Month and
  year-to-date columns. CSV and PDF like the other books.
- Permissions: `budget.view`, `budget.manage`, `budget.approve` (making a version ACTIVE needs an approval).

### Built (budget)

Part B's budget is built (README, "Budget"). What differs from the text above:

- `budgets.year_start` (the first day of the fiscal year) takes the place of `fiscal_year_id`: `gl_fiscal_years` holds a row only for a year that was closed, so an open year has no id.
- Beyond the plan: **spread** of a yearly figure (equally, or after last year's months), a **PDF** of the report, English and Indonesian texts, and a `budget.approve` permission used as the approver's.
- The report takes a **period** (whole months) and shows the **year to date** beside it, instead of a month column and a year column picked separately.
- A budget may be made for the fiscal years from the year the books start in to two years after the current one.
- The budget of the **statistics** was built afterwards (README, "Budget statistics"): rooms available, rooms sold and ADR per month, kept beside the money. Not built, as decided: a budget of the balance sheet. The cash flow statement is built too (README, "Cash flow statement"): the range may be any range up to five years, not only one inside a fiscal year, and SUSPENSE is treated as operating.

### Cash flow statement (indirect method)

- Computed, nothing stored. Period = a range of dates inside a fiscal year. **Starts from net income** of the period (the income statement of the same range), adds back non-cash
  items (group DEPRECIATION), then the change of the balance sheet accounts by their `statement_group`:
  - Operating: RECEIVABLES, INVENTORIES, PREPAID, OTHER_ASSETS (increase = cash out), PAYABLES, ACCRUED, DEPOSITS, TAXES_PAYABLE, OTHER_CURRENT_LIABILITIES (increase = cash in).
  - Investing: FIXED_ASSETS (gross and accumulated depreciation net of the add-back).
  - Financing: LONG_TERM_DEBT and EQUITY other than current-year earnings (capital, drawings).
  - The result must equal the **change of the CASH group** between the opening and closing balance; the report states the check and shows the difference if it does not (for
    example an account without a group, shown as unclassified).
- The **direct method** was built afterwards (README, "Cash flow statement"): the journals are classified by the statement group of the accounts that stand against the cash, so no new data was needed.
- API: `GET {P}/accounting/cash-flow?from&to` (+ `.pdf`, `.csv`), permission `accounting.view`. Tests: a small set of journals whose cash flow is worked out by hand (sale on credit, payment of a
  bill, purchase of equipment, loan received), the reconciliation to the cash change, a contra account, period with no activity.

### Frontend

Accounting -> Budget (versions list, grid, import, copy from actuals, activate), Budget against actual (month/YTD, department grouping, favourable/unfavourable colours in text and not
colour alone), Cash flow statement. Texts in English and Indonesian.

---

## Questions for the owner

1. **Shifts:** is `require_shift_for_cash` off by default acceptable (existing hotels keep working, a property turns it on when its cashiers are trained)?
2. **Shifts:** over/short needs an approval above `max_variance` by default 0 (every difference). Or a default such as Rp 50.000 so a small difference closes without a second person?
3. **Shifts:** one cash account (CASH) for the drawer and the safe, with drops only recorded, is fine for v1? Or do you want a separate safe account (CASH_SAFE) so a drop is a journal?
4. **Shifts:** should the night audit **block** when a shift is open (proposed, the day's cash is counted first) or only warn?
5. **Card:** does the property know the brand of a card payment? Nothing records it today; adding a brand to payments changes the payment form. Proposal: one rate per method now, the brand later.
6. **Budget:** the budget by month in money, revenue and expense accounts only (no balance sheet budget), per fiscal year with versions; and the department view from the `statement_group` of each
   account. Is that enough, or do you also need a rooms-statistics budget (occupancy, ADR) now?
7. **Cash flow:** the indirect method only (from the existing journals), financing and investing from the account groups. Right for v1?
8. **Order:** A (shifts), then C (card), then B (budget and cash flow). Or another order?
