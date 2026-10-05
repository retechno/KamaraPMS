# 15. VAT on the card commission (MDR)

Status: **design approved by the owner on 2026-10-05. Steps 1 (schema and the rename), 2 (rules, snapshots, expected report and proposal) 3 (preview, settle, the settlements list) and 4 (the claims on the VAT return) are built; step 5 is not.** Where this file says "built" below it means step 1. It extends part C of `11-cashier-budget-cashflow-card.md` (card fee rules, the snapshots on the payments, the card settlement).

Decisions of the owner (2026-10-05):

1. A card settlement that has been posted is **final and immutable**. It is not voidable in this scope. If a correction or reversal is needed later, it is a separate feature (settlement reversal/correction).
2. A settlement of several payments with different VAT rates gets a **proportional allocation** of the actual deduction by the expected MDR and the expected VAT as the **proposed** split. It only proposes: the user can change the final VAT.
3. A payment that has no `mdr_vat_rate` (taken before this feature): expected VAT is 0, it is marked **"without rate"** (`tanpa tarif`), and no historical rate is guessed or applied.
4. The VAT treatment is **frozen on the card settlement**. A settlement already saved never changes because of a change of the PKP status, of the MDR rule, of the VAT rate or of the account mapping.
5. The settlement keeps a **snapshot** of `mdr_rate` and `vat_rate` so that the history can be audited: at least `expected_mdr`, `expected_vat`, `mdr_amount`, `vat_amount`, `mdr_rate`, `vat_rate`, `vat_treatment`, `fee`.
6. `fee = mdr_amount + vat_amount`, and the actual deduction is `mdr_amount + vat_amount`.
7. The design and the schema are written here first; no coding before they are final.

## The words

- **Deduction** (actual): what the bank kept: `gross - net`, gross = the payment lines settled, net = the statement line.
- **MDR**: the commission of the acquirer without VAT. **VAT**: the tax the acquirer charges on the MDR.
- `fee` in `card_settlements` always meant the whole deduction booked as commission; it keeps that meaning and now equals `mdr_amount + vat_amount`.

## 1. Schema (migration 00056, with Down)

`card_fee_rules` (append-only; a new rate is a new rule):

- `vat_rate numeric(7,4) NOT NULL DEFAULT 0`, CHECK between 0 and 100 (percent, 4 decimals). Existing rules get 0.

`payments` and `city_ledger_receipts` (the snapshot taken when a card or e-wallet payment is taken, next to `mdr_rate`, `mdr_fee`, `expected_settlement_date`):

- `mdr_vat_rate numeric(7,4)` and `mdr_vat numeric(18,3)`: the VAT rate of the rule in force and the expected VAT = the (rounded) MDR fee of the payment times that rate.
- CHECK: both null or both set; set only when `mdr_rate` is set; rate between 0 and 100; `mdr_vat >= 0`.
- A payment taken before this feature keeps both null: that is "without rate". A new payment under a rule with a VAT rate of 0 keeps rate 0 and VAT 0, which is not "without rate".

`card_settlements` (the settlement, frozen at the moment it is posted):

| column | meaning |
|---|---|
| `fee` (exists) | the whole deduction: `gross - net` |
| `mdr_amount numeric(18,3)` **generated** (`fee - vat_amount`, stored) | the MDR as finally booked (without VAT) |
| `vat_amount numeric(18,3) NOT NULL DEFAULT 0` | the VAT as finally booked |
| `vat_treatment varchar(10)` | CREDITABLE, EXPENSE or DEFERRED; null exactly when `vat_amount = 0` |
| `expected_mdr numeric(18,3)` | the MDR the payments expected (the existing `expected_fee`, **renamed**; null when a payment kept no snapshot) |
| `expected_vat numeric(18,3)` | the VAT the payments expected; payments without `mdr_vat_rate` count 0; null when `expected_mdr` is null |
| `proposed_vat numeric(18,3) NOT NULL DEFAULT 0` | the VAT the system proposed, kept so that an override can be seen |
| `mdr_rate numeric(7,4)` | the MDR rate of the payments settled when they all share one rate; null when mixed or unknown (the exact rates are on the payments of the settlement) |
| `vat_rate numeric(7,4)` | the same for the VAT rate |
| `payments_without_vat_rate int NOT NULL DEFAULT 0` | how many payments of the settlement were "without rate" |

CHECKs: `vat_amount >= 0` and `vat_amount <= fee` (so `mdr_amount >= 0`); `fee = mdr_amount + vat_amount` holds **by construction** because `mdr_amount` is a stored generated column; `(vat_amount = 0) = (vat_treatment IS NULL)`; the treatment is one of the three; the rates between 0 and 100; `payments_without_vat_rate >= 0`. The settlement rows are append-only (the existing guard); nothing changes after posting. Data of the settlements made before this: `mdr_amount = fee`, `vat_amount = 0`, `proposed_vat = 0`, no treatment, `expected_mdr = expected_fee`, `expected_vat` null.
As a generated column `mdr_amount` is filled for the settlements made before this too (VAT 0, so the MDR is the whole fee), and no UPDATE of the append-only table is needed. The rows are written with `fee` and `vat_amount`; the service never writes `mdr_amount`. `card_settlements` already has `UNIQUE (property_id, id)` and `UNIQUE (journal_id)`.

`tax_return_input_claims` (the input side of the VAT return, see `14-supplier-credit-notes.md` for the pattern):

- `settlement_id bigint` with a foreign key to `card_settlements (property_id, id)`. A claim now names a bill line, or a credit note line, or a settlement, exactly one of them (CHECK).
- A settlement claim is always **positive** (the VAT of a CREDITABLE settlement) and has no reversal: a settlement is final. One live claim per settlement (unique index on `settlement_id` where not released and not a reversal). A claim is released when its return is voided, like any claim.
- `card_settlements` needs `UNIQUE (property_id, id)` for the foreign key if it has none.

Constraint names are mapped to error codes in `internal/platform/db/errors.go`; new codes need their texts in `locales/en.ts` and `locales/id.ts`. `schema_test.sql` gets the checks above.

## 2. The journal of a settlement

Unchanged in shape (journal type BANK, dated the date of the statement line). With `D = gross - net`, `VAT` the final VAT and `MDR = D - VAT`:

| line | CREDITABLE and DEFERRED | EXPENSE | no VAT (`VAT = 0`) |
|---|---|---|---|
| Dr bank account | net | net | net |
| Dr commission account (the one the user picks) | MDR | MDR + VAT | MDR (= D) |
| Dr Input VAT (1425, `INPUT_VAT`) | VAT | none | none |
| Cr clearing account of the method | gross | gross | gross |

It always balances: `net + MDR + VAT = gross`. The commission line carries the department like today (the department rule of the account applies, `ResolveDepartment`); the input VAT line has none unless the account's rule or default gives one. A settlement with `D = 0` posts no commission and no VAT, as today.

**The VAT treatment is that of the property on the date of the statement line**, from the same function the supplier bills use (`SettingsOnDate`): a PKP property takes CREDITABLE or DEFERRED as it is set for that date, a property that is not PKP takes EXPENSE. It is written to `vat_treatment` and never recomputed. The account behind `INPUT_VAT` at the time is what the journal line carries; the line is the record, so a later change of the mapping moves nothing.

## 3. Expected against actual

**Expected, per payment** (taken when the payment is taken, never recomputed): `mdr_fee = round(amount x mdr_rate)`, `mdr_vat = round(mdr_fee x vat_rate)`. The expected deduction is `mdr_fee + mdr_vat`, the expected net `amount - mdr_fee - mdr_vat`. The expected settlement report and the proposal of the payment lines that make up a statement line both use this expected net (so a line is matched within the tolerance whether or not the acquirer charges VAT). A payment "without rate" counts expected VAT 0 and the report counts them (`without_vat_rate`), apart from those without any MDR rate (`without_rate`, existing).

**Actual, at the settlement:** gross is the sum of the payment lines chosen, net is the statement line, `D = gross - net`; these are the final figures. The system proposes a split of `D`:

- the payments all carry a VAT rate `v` and share it: `proposed_vat = round(D x v / (100 + v))`;
- otherwise (different rates, or some payments without a VAT rate): **proportional** by the expected figures of the payments, `proposed_vat = round(D x expected_vat / (expected_mdr + expected_vat))`, and 0 when both are 0.

The user may change the final VAT (the acquirer's slip rounds differently, or the invoice differs). `MDR = D - VAT`, so `D = MDR + VAT` exactly. The settlement keeps `expected_mdr`, `expected_vat`, `proposed_vat`, `mdr_amount`, `vat_amount`; the variances are shown, never enforced: **MDR variance** `mdr_amount - expected_mdr` and **VAT variance** `vat_amount - expected_vat` (and the total deduction against the expected deduction).

## 4. Rounding

Everything is rounded to the decimals of the currency of the property, half away from zero (`money.Percent`). The expected figures round per payment, the MDR first and the VAT from the rounded MDR, so the sum of the snapshots can differ from the bank's figure by a few units of the currency: that goes into the variance and is never forced. The proposed VAT is rounded once; the final MDR is `D - VAT`, so there is no rounding residue and the journal needs no balancing line. The final VAT must satisfy `0 <= VAT <= D` (422 `VAT_EXCEEDS_DEDUCTION` on `vat_amount`; `INVALID_AMOUNT` for a negative or too precise one).

## 5. Reversal

There is none, on purpose. A posted settlement is final and immutable: its journal, its snapshot and its VAT are not changed or voided, and the VAT claim of a settlement is never reversed. `Unclear` still only removes the matching of the bank line (the journal and the settlement stay, and the payment lines cannot be settled again), as today. A wrong settlement is corrected by a manual journal; a **settlement reversal/correction feature** (void with its journal, the payment lines released, a reversing claim) is a separate piece of work. The claims table already has the pattern of a reversing claim, so that feature can plug in later.

## 6. The VAT return

A settlement whose `vat_treatment` is CREDITABLE is **claimed** on the VAT return like a bill: a positive claim of `vat_amount` (source `SETTLEMENT`), in the return of the month of the date of the statement line, or in the first return that is not filed after it (like a late bill). Only the filing profile that claims input VAT claims it. EXPENSE has no VAT account and no claim; DEFERRED sits in 1425 and is not claimed (like a DEFERRED bill). The worksheet and the filed return list the claim with its source, the number of the bank statement line or settlement reference and its date. Voiding the return releases the claim through the existing mechanism, and the settlement is claimed again by the next return. The worksheet's `input_claimed`, the offset and the credit carried forward use the settlement claims like the others.

## 7. API

- `GET/POST {P}/bank/card-fee-rules`: the rule has `vat_rate` (percent as a string, default 0; 422 `INVALID_RATE`).
- `GET {P}/bank/card-settlements/expected`: each line has `vat_rate`, `expected_vat`, `expected_deduction`, `expected_net` (with the VAT), and a flag `without_vat_rate`; the totals add `expected_vat`, `expected_deduction`, `without_vat_rate`.
- `GET {P}/bank/statements/{id}/lines/{line}/settlement-proposal`: lines and totals as above.
- **New** `POST {P}/bank/statements/{id}/lines/{line}/settlement-preview` (`bank.view`): `{account_key, journal_line_ids}` answers `gross`, `net`, `deduction`, `expected_mdr`, `expected_vat`, `proposed_vat`, `proposed_mdr`, `vat_rate` (null when mixed), `without_vat_rate`, `vat_treatment` (what the settlement would freeze on the date of the line), `input_vat_account`. It writes nothing.
- `POST {P}/bank/statements/{id}/lines/{line}/settle`: `vat_amount` is optional. Absent: the proposed VAT is used. Present (also "0"): it is the final VAT. 422 `VAT_EXCEEDS_DEDUCTION`.
- `GET {P}/bank/card-settlements`: each settlement has `fee` (the deduction), `mdr_amount`, `vat_amount`, `vat_treatment`, `expected_mdr`, `expected_vat`, `proposed_vat`, `mdr_rate`, `vat_rate`, `payments_without_vat_rate`, `mdr_variance`, `vat_variance`. `expected_fee` is renamed `expected_mdr` and `fee_variance` becomes `mdr_variance` (both clients are ours); the same rename was made on the expected lines, the totals and the proposal (`expected_fee` to `expected_mdr`), where the fee of a payment is its MDR.
- Tax return worksheet and return: a claim has `source` `SETTLEMENT` and `settlement_id`.
- Permissions: `bank.view` reads, `bank.manage` writes the rules, `bank.reconcile` settles (as today); no new permission. The payment and receipt views that show the MDR snapshot add `mdr_vat_rate` and `mdr_vat`.

## 8. Screens

- Card fee rules (Bank, Card settlements): a field "VAT on the commission (%)", shown in the list of rules.
- Expected settlements: columns for the expected VAT and the expected net with the VAT, a mark on the payments without a VAT rate.
- The settle dialog (Bank, Reconcile a statement): gross, what the bank paid, the deduction; the expected MDR and VAT; the proposed split; an editable field for the final VAT (filled with the proposal); the final MDR (deduction less VAT, read only); the treatment that will be frozen (CREDITABLE, EXPENSE or DEFERRED) and the input VAT account for it; the commission account choice; a warning when payments are without a VAT rate.
- The list of settlements: the MDR and the VAT booked, the treatment, and the two variances.
- A tax return: the claims of settlements are named as such.

## 9. Tests

- **Rules and snapshots:** a rule with a VAT rate gives the snapshot (`mdr_vat_rate`, `mdr_vat`) on a card payment, an e-wallet payment and a city ledger receipt by card; a payment under a rule with VAT rate 0 has rate 0; a payment taken before the feature has none ("without rate"); a new rule from a later date does not move an earlier snapshot.
- **Expected:** MDR, VAT, deduction and net per payment, at 0 and 2 decimals, with the VAT rounded from the rounded MDR; the report counts the payments without a VAT rate and without any rate; the proposal of payment lines uses the net with the VAT.
- **Preview and proposal:** one VAT rate (the formula), different rates (proportional), some payments without a VAT rate (proportional, they count 0), all without (proposed 0), `D = 0`; the figures change nothing.
- **Settle:** with the proposed VAT; with the VAT changed (both directions); with `vat_amount` 0; the journal balances for CREDITABLE, EXPENSE and DEFERRED (the lines of the table above); the snapshot columns; `fee = mdr_amount + vat_amount` and `fee = gross - net`; `payments_without_vat_rate`; the treatment is frozen: a later PKP change, a new rule, a new VAT rate and a remapped `INPUT_VAT` change nothing of a saved settlement.
- **Validation:** VAT above the deduction, negative, too precise; no INPUT_VAT account for CREDITABLE; a settlement of a property without PKP gets EXPENSE.
- **Database:** `fee = mdr + vat`, the treatment and the zero VAT agree, the rate ranges, the settlement stays append-only, a claim names exactly one source, one live claim per settlement.
- **VAT return:** a CREDITABLE settlement is a positive claim in the month of its line date; a late one goes to the first open month; only the claiming profile claims; EXPENSE and DEFERRED do not; voiding the return releases it and the next return claims it again; `input_claimed`, the offset and the credit use it.
- **Cross-cutting:** the department rule on the commission line; tenant isolation of every new route; the OpenAPI check; the front-end tests of the rule form, the expected table and the settle dialog (the proposal, the override, the treatment label, the warning).

## Order of building (after approval)

1. Migration 00056, constraint mappings, the DB tests, the rename `expected_fee` to `expected_mdr`. **Built.**
2. Rules and snapshots (rules with `vat_rate`, payments and city ledger receipts), the expected report and the proposal. **Built.**
3. The settlement: the preview, the settle with the final VAT and the frozen figures, the settlements list. **Built** (see "Built in step 3" below).
4. The claims on the VAT return. **Built** (see "Built in step 4" below).
5. OpenAPI, the front end, the documents (README, `06-api.md`, the feature map), the full checks, one commit per step.

## Built in step 3

- `SettleInput.VATAmount` (`vat_amount`, optional) and `POST .../settlement-preview` as in section 7; `SettlementRow` has the frozen figures and the two variances. The checks of the payment lines are shared by the preview and the settle (`settlementItems`).
- **The commission account is needed only when there is a commission to book** (`commission > 0`): a deduction that is all VAT and booked to input VAT (CREDITABLE or DEFERRED) needs no commission account. For EXPENSE the commission is the whole deduction.
- `payments_without_vat_rate` and the preview's `without_vat_rate` count the payments that have an MDR snapshot and no VAT rate (like the expected report); the payments with no snapshot at all are in the preview's `without_rate`. A payment with no snapshot at all counts 0 in the expected figures, and then the expected MDR and VAT of the settlement are null.
- The VAT treatment is read with `SettingsOnDate` (a share lock on the tax settings, after the bank account in the lock order), so the preview runs in a transaction too.
- The settlement still has no void: `Unclear` removes only the matching.

## Built in step 4

- `taxfiling.Service` claims a CREDITABLE settlement (`ClaimableSettlements`): a positive claim of `vat_amount`, source `SETTLEMENT` (`ClaimSettlement`), in the return of the month of the date of the statement line (the date of the settlement journal) or the first return not filed after it; only a profile that claims input VAT claims it. EXPENSE and DEFERRED settlements are never claimed. A settlement has no reversal, so there is no reversing claim.
- A claim of a settlement reads back on the worksheet and the return with `source` SETTLEMENT and `settlement_id`; `bill_number` is the number of the settlement journal, `supplier_invoice_number` its reference, `supplier_name` the bank account and `bill_date` the date of the line. The PDF names it "Input VAT on card commission". Voiding the return releases the claim and the next worksheet offers it again, through the existing mechanism.
- The database keeps one live claim per settlement, exactly one source per claim and no reversal of a settlement claim (migration 00056).

## Not in this scope

Voiding or correcting a settlement; the VAT on the other fees of a bank (the `Adjust` of a statement line); a VAT rate per card brand (there is one rule per method); the tax invoice of the acquirer as a document.
