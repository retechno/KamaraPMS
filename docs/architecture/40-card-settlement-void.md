# 40. Voiding a card settlement (KD-05)

Status: **PLAN, awaiting the owner's approval (2026-11-26).** Nothing is built; this document has no code and no migration.
It answers KD-05 of the user guide report (`docs/user-guide/_laporan.md`): a wrong card, e-wallet or QRIS settlement cannot be
undone, and unmatching its statement line (`internal/bankrec/matching.go:40-70`, `Unclear`) leaves the journal behind. It
replaces the rule "a settlement is final" of `15-card-fee-vat.md` §5, which named this feature as separate work and kept room
for it (the reversing claim of the VAT return). The questions for the owner are in §11. **R** marks a recommendation that
waits for an answer.

## 1. What exists, and what goes wrong

- **Settle** (`settlement.go`, `bank.reconcile`) posts one `BANK` journal **dated the statement line**:
  - Dr the bank account for the net;
  - Dr the commission account for the MDR (with the VAT when the treatment is `EXPENSE`);
  - Dr input VAT 1425 for `CREDITABLE` and `DEFERRED`;
  - Cr the clearing account (`CARD`, `OTHER_PAYMENT`, `QRIS`) for the gross.

  It writes `card_settlements` (the amounts, the frozen VAT treatment and the snapshot of 15 §1), one `card_settlement_items`
  row per payment line settled (`settled_line_id`, unique, and `settling_line_id`, the credit of the clearing account), and
  one `bank_clearings` row that matches the bank debit with the statement line. Both settlement tables are append-only
  (`forbid_modification`, migration 00033).
- **Unclear** deletes the `bank_clearings` row and nothing else. Afterwards:
  - the settlement, its journal and its items stay;
  - the bank debit is uncleared again;
  - the statement line has nothing matched;
  - the payment lines are still "settled", so `Settle` refuses them (`ALREADY_SETTLED`, unique `card_settlement_items_settled_uk`).

  A wrong settlement is therefore stuck in the ledger. The only way out today is a manual journal, and the payment lines can
  never be settled again.
- **The VAT return** claims the VAT of a `CREDITABLE` settlement (`ClaimableSettlements`, `tax_return_input_claims.settlement_id`).
  Settlement claims cannot be reversing claims (`tax_return_input_claims_settlement_ck`: `reverses_claim_id IS NULL AND amount > 0`).
  Credit notes already have the reversing-claim pattern (`ReversibleCreditClaims`).

## 2. The use case in one sentence

**Void settlement** (`POST {P}/bank/card-settlements/{id}/void`, `bank.reconcile` plus an approval, with a reason) does five
things in one transaction:
1. posts the mirror image of the settlement's journal;
2. marks the settlement `VOIDED`;
3. releases its payment lines, so they can be settled again;
4. removes the matching of its bank line, so the statement line can be matched or settled again;
5. clears the original bank line and its mirror against each other.

Its VAT claim, if any, is taken back by the next VAT return.

## 3. The journal

**One full reversal, no partial void.** A wrong settlement is voided whole and settled again correctly. There is no edit
of a settlement.

The journal is a `REVERSAL` with `reverses_journal_id` pointing at the settlement journal. It swaps every debit and credit,
with the same accounts, departments and source references. It is written by `accounting.Poster.Reverse`, whose list of
reversible types gains `BANK` **only for the journal of a settlement**. The caller passes the settlement, so a plain BANK
journal (an `Adjust`) still cannot be reversed through it. That journal is out of scope, see §10.

So for gross 1,000,000, net 977,800, MDR 20,000 and VAT 2,200 (CREDITABLE):

| Account | Settlement (existing) | Void (new) |
|---|---|---|
| Bank | Dr 977,800 | Cr 977,800 |
| Commission (MDR) | Dr 20,000 | Cr 20,000 |
| Input VAT 1425 | Dr 2,200 | Cr 2,200 |
| Card clearing | Cr 1,000,000 | Dr 1,000,000 |

**The date (R, question V1): the date of the settlement journal, which is the date of the statement line.**
- The month of the line must be open. `Poster.CheckDate` gives `PERIOD_CLOSED` with the period otherwise, and nothing is written.
- Why the original date, not the business date:
  - the corrected settlement that follows is dated the same statement line, because `Settle` always dates the line, and needs
    that month open anyway;
  - a reversal in a later month would leave the month of the statement with the wrong settlement **and** the right one;
  - that month's bank book would then not agree with the statement it is reconciled against. Its reversal would be dated after
    the end of the statement and could not be cleared in it (`AFTER_STATEMENT`).
- **When the month is closed**, the user has two ways:
  - reopen it with the existing `accounting.ReopenPeriod` (`accounting.close`; note that it also reverses the depreciation
    runs of that month and later, 37 §4.3);
  - or leave the settlement and post a manual journal in an open month, as today.

  The void does not reopen a month by itself.
- The alternative the owner may prefer is in V1: reverse on the business date, and let `Settle` take a date too.

The posting itself follows the rules of every module journal:
- `RequireOpenBusinessDay(ForShare)` first;
- `accounting.BeginPosting` (the accounting settings, level 46) before the bank account (level 48);
- the journal number from the sequence (level 50, last);
- `approved_by` on the reversal journal;
- the audit row `Poster.Reverse` already writes for every module journal it reverses (its name is not changed here);
- plus `bank.settlement_voided` on the statement (§8).

## 4. The settlement, its payment lines, the statement lines

### 4.1 The settlement

`card_settlements` gains these columns:
- `status` (`POSTED` | `VOIDED`, default `POSTED`);
- `voided_at`, `voided_by`, `void_reason`, `void_approved_by`;
- `void_journal_id` (FK to `gl_journals`, unique).

The append-only trigger is replaced by a narrow one, as `tax_returns` has (migration 00034):
- the only update allowed is `POSTED` → `VOIDED` together with the void columns;
- every other column stays as it was;
- a delete is still refused.

A voided settlement is never posted again: settling again makes a **new** settlement row.

### 4.2 The payment lines

`card_settlement_items` gains `released_at`. The unique constraint `card_settlement_items_settled_uk (settled_line_id)` becomes
the partial unique index `card_settlement_items_live_uk (settled_line_id) WHERE released_at IS NULL`. A payment line is then
settled at most once **at a time**, and the database still guarantees it, race included.

A trigger on the items allows one update only, `released_at` from NULL to a time, and only when the parent settlement is
`VOIDED` in the same transaction.

The lists that offer payment lines leave out these lines:
- lines of a live item (`released_at IS NULL`), as today;
- **every line of a settlement journal or of its void journal**.

Without the second rule:
- the old credit line of the clearing account would come back as a negative "payment";
- the mirror's debit of the clearing account would come back as a positive one.

The queries concerned are `SettlementCandidates`, `IsSettledOrSettling` and `expectedLines` (the expected settlements of
`cardfee.go`, 11 part C). After the void the released payments are listed again with their own expected fee, as before
they were settled.

### 4.3 The statement lines

The void deletes **every** `bank_clearings` row of the settlement's bank line. Normally that is the one row that matched it
with its statement line.
- Each such row must be in an **open** statement of that bank account. A reconciled one gives `STATEMENT_RECONCILED`, with the
  statement in the context: reopen it first with the existing `Reopen` (latest statement only, approval `BANK`).
- The statement line then has nothing matched, and can be:
  - settled again with the right payment lines;
  - matched with something else;
  - adjusted.

Then the original bank debit (+net) and the mirror's bank credit (−net) are **cleared on their own** (`statement_line_id` NULL,
as `Clear` already does) in the statement whose matching was removed. They add up to zero, so:
- they leave the uncleared list;
- the book balance of the reconciliation is unchanged;
- the deferred check `bank_clearings_within_line` holds.

A settlement whose matching had **already been removed** by `Unclear` before this change (the trap of KD-05) has no clearing.
Its two bank lines then stay uncleared, side by side on the same date, and the user clears them together with the existing
**Clear**. The guide says so.

### 4.4 Unclear of a settlement

**Unclear of the clearing of a settlement is refused** (R, question V4): 409 `CLEARING_IS_SETTLEMENT`, "void the settlement
instead". This closes the second half of KD-05: the matching of a settlement and its journal can no longer drift apart. Unclear
of any other clearing is unchanged.

## 5. The VAT

The VAT treatment frozen on the settlement decides what happens:

| Treatment | Effect of the void |
|---|---|
| `EXPENSE` | The commission line carries the VAT; the mirror takes it back. No claim. |
| `DEFERRED` | The 1425 line is mirrored. It was never claimed. |
| `CREDITABLE`, not claimed yet | The mirror takes 1425 back. `ClaimableSettlements` adds `status = 'POSTED'`, so the voided settlement is never claimed. |
| `CREDITABLE`, claimed by a filed return (R, question V3) | The void goes ahead. **The next return takes the claim back**: a new query `ReversibleSettlementClaims` makes a negative claim, as `ReversibleCreditClaims` does for a voided credit note. See below. |

For the last row:
- **Which claims it finds:** the live claims of settlements that are `VOIDED`, whose void journal is dated up to the end of
  the month, and that no live return reverses yet.
- **Migration:** the check `tax_return_input_claims_settlement_ck` is redefined to allow a reversing settlement claim
  (`reverses_claim_id NOT NULL AND amount < 0`). The live unique index stays as it is.
- **The correct settlement made afterwards** is claimed as a new one.
- **No refusal is needed and no tax lock is taken.** A void that races a filing either:
  - lands before it: the filing does not see the settlement;
  - or lands after it: the next return reverses the claim.

  The order of locks therefore does not grow.

## 6. Permissions and approval

- **Permission:** `bank.reconcile`, as `Settle` and `Unclear`. No new permission.
- **Approval:** a new kind **`BANK_SETTLEMENT_VOID`** (R, question V2) with an amount: the **gross** of the settlement. The
  approver needs `correction.approve`. A new kind is not a migration (32 §3: kinds are validated by the application). The
  rule of the property applies like any other kind (anyone, someone else, someone else above an amount).
- **Why not the existing `BANK`:** `BANK` has no amount (32 §2: reopening a statement moves no money), while a void reverses a
  journal of real money.
- **The reason** is required, up to 500 characters. It is written on the settlement and on the reversal journal.

## 7. Locks, migration, errors

### 7.1 Order of locks

The order follows CLAUDE.md, the same as `Settle`:
1. the business day (`ForShare`, level 10);
2. the accounting settings (`BeginPosting`, 46);
3. the bank account of the settlement (`FOR UPDATE`, 48). This is `card_settlements.bank_account_id`, the account of its
   statement.
4. the sequences (50), for the journal number.

The settlement row is read **after** the bank account lock and its status checked again. Two voids of the same settlement are
therefore serialised by the bank account: the second sees `VOIDED` and answers 409. The database guards the same thing twice
over, if a future path forgets the lock:
- the transition trigger refuses `VOIDED` → `VOIDED`;
- `void_journal_id` is unique;
- `Poster.Reverse` refuses a journal already reversed (`JOURNAL_ALREADY_REVERSED`).

A re-settle of the released lines on **another** bank account's statement takes a different bank lock. The partial unique
index of §4.2 decides that race: one live item per payment line, and the loser gets `ALREADY_SETTLED`.

### 7.2 Migration `00101_card_settlement_void.sql` (with Down)

- **`card_settlements`:**
  - the columns of §4.1;
  - checks `card_settlements_status_ck`, `card_settlements_voided_ck` (`(status = 'VOIDED') = (voided_at IS NOT NULL)`, the
    same for `void_journal_id`, `void_reason`, `voided_by`);
  - `card_settlements_void_journal_fk` and `card_settlements_void_journal_uk`;
  - the trigger `card_settlements_void_only` replaces `card_settlements_append_only`. It raises with that constraint name.
- **`card_settlement_items`:**
  - `released_at`;
  - drop `card_settlement_items_settled_uk`, create the partial unique `card_settlement_items_live_uk`;
  - the trigger `card_settlement_items_release_only` replaces `card_settlement_items_append_only`.
- **`tax_return_input_claims`:** redefine `tax_return_input_claims_settlement_ck` as in §5.
- **No business-day trigger.** `business_day_must_be_open` (00065) covers ledger and cash rows, not journals or bank
  documents, as for `Settle`.
- **Down:**
  - refuses to run while a settlement is `VOIDED` or an item released, because the old unique could not hold them;
  - otherwise it restores the old constraint and triggers.

### 7.3 Errors

`internal/platform/db/errors.go` gets these mappings for the new constraint names; the test of the mapping checks that every
name exists:

| Constraint | Code |
|---|---|
| `card_settlement_items_live_uk` | 409 `ALREADY_SETTLED` (replaces `card_settlement_items_settled_uk`) |
| `card_settlements_void_journal_uk`, `card_settlements_void_only` | 409 `SETTLEMENT_ALREADY_VOIDED` |
| `card_settlement_items_release_only` | 409 `SETTLEMENT_NOT_VOIDED` (an internal guard) |
| `card_settlements_status_ck`, `card_settlements_voided_ck` | 422 `INVALID_STATE` (internal) |

New codes of the use case:

| Code | When |
|---|---|
| 404 `SETTLEMENT_NOT_FOUND` | Unknown, or another property or tenant |
| 409 `SETTLEMENT_ALREADY_VOIDED` | The settlement is voided already |
| 409 `CLEARING_IS_SETTLEMENT` | Unclear of a settlement's clearing |

Existing codes that also apply:
- `STATEMENT_RECONCILED`;
- `PERIOD_CLOSED`;
- `BUSINESS_DAY_*`;
- `PERMISSION_DENIED`;
- the approval errors of `iam.Verify`.

## 8. API, audit, screens, guide

**API** (`06-api.md`, bank section; `api/openapi.yaml`; `npm run gen:api`):
- `POST {P}/bank/card-settlements/{id}/void` takes `{reason, approval}` and answers the settlement row.
- `GET {P}/bank/card-settlements` gains these fields:
  - `status`, `voided_at`, `void_reason`;
  - `void_journal_number`;
  - `statement_id` / `statement_line_no` of its matching, when it has one, so the screen can open it.
- `DELETE …/clearings/{clearingId}` documents 409 `CLEARING_IS_SETTLEMENT`.
- **No Idempotency-Key:** a second void is a 409, never a second journal. Other voids work the same way, for example a credit note.

**Audit** (in the same transaction): `bank.settlement_voided` on the statement, with:
- the settlement, gross, net, fee, VAT and its treatment;
- the original and reversal journal numbers;
- the reason, `approved_by`, and the statement lines unmatched.

**Screens** (Tailwind and `components/ui`, i18n EN and ID):
- **Settlements made** (`Settlement yang dibuat`):
  - a status column (`Dibatalkan` with the void journal);
  - a **Void settlement** / **Batalkan settlement** action for `bank.reconcile`, with a dialog for the reason and the approval.
- **The statement page:** a line matched by a settlement shows **Void settlement** instead of the unmatch button.
- **Error messages:** each error code of §7.3 has its text.

**Guide:**
- `08-keuangan-back-office/settlement-kartu.md`:
  - remove the KD-05 block;
  - a section "Membatalkan settlement yang salah": void, then settle again, and what to do when the month is closed or the
    statement is reconciled;
  - new screenshots.
- `_laporan.md`: KD-05 marked fixed.
- `15-card-fee-vat.md` §5 and "Not in this scope": point to this document.

## 9. Tests

Everything runs on real PostgreSQL (`dbtest`).

**Use case and books** (`internal/bankrec`):
1. **Void a CREDITABLE settlement in an open month:**
   - the mirror journal, dated the statement line, with `reverses_journal_id` and `approved_by`;
   - the trial balance of the month is back to its state before the settlement;
   - the settlement is `VOIDED` with the void fields;
   - its items are released;
   - its clearing is gone, and the two bank lines are cleared on their own;
   - the statement still reconciles to the same book balance;
   - audit rows.
2. **Settle again after a void,** with other payment lines and with the same ones:
   - it succeeds;
   - the candidates and the expected settlements list the released payments;
   - they do not list the old credit line or the mirror's debit (§4.2).
3. **EXPENSE, DEFERRED and no-VAT settlements, and a settlement without commission:** the mirror of each journal shape.
4. **Month closed:** `PERIOD_CLOSED`, nothing written; after `ReopenPeriod` the void succeeds.
5. **Statement reconciled:** `STATEMENT_RECONCILED`; after `Reopen` it succeeds.
6. **A settlement already uncleared** before the change (made by inserting the old state): the void succeeds; its two bank lines
   stay uncleared and `Clear` clears them together.
7. **Unclear of a settlement's clearing** gives `CLEARING_IS_SETTLEMENT`; Unclear of an ordinary clearing is unchanged.
8. **Business day not open:** the gate refuses.
9. **Permissions:**
   - no `bank.reconcile` gives 403;
   - another tenant's settlement or property gives 404;
   - approval missing or wrong;
   - the `BANK_SETTLEMENT_VOID` rule with `DISTINCT` above an amount, compared with the gross.
10. **The second void** gives `SETTLEMENT_ALREADY_VOIDED`.

**VAT** (`internal/taxfiling`):

11. **Voided before any return:** never claimed.
12. **Claimed by a filed return, then voided:**
    - the next return has the reversing claim −VAT;
    - voiding that return releases it, and the following return reverses it again;
    - the correct settlement is claimed as a new one.

**Schema** (`scripts/db-test.sh` and the module tests):

13. **The transition trigger refuses:**
    - a change of any other column;
    - `VOIDED` → `POSTED`;
    - a delete.
14. **An item** is released only when its settlement is voided. Two live items of one payment line are refused (`ALREADY_SETTLED`);
    after a release, a new live item is accepted.
15. **Mappings:** every new constraint name is mapped (the existing test).
16. **Migration** up/down/up, with the Down refusing while a void exists.

**Races:**

17. **Two voids of the same settlement at the same time:**
    - exactly one succeeds;
    - the other gets `SETTLEMENT_ALREADY_VOIDED`;
    - one reversal journal, one audit row, the items released once.
18. **A void and a re-settle** of the same payment lines on another bank account's statement: never two live items, and the
    loser gets a clean error.
19. **A void and a VAT filing** of the same month at the same time: the claim either does not exist or is reversed by the next
    return. Never a live claim of a voided settlement that no return will reverse.

**Web and end to end:**

20. **Vitest:**
    - the action and the dialog: shown only with `bank.reconcile`;
    - a fresh `vi.fn()` per test;
    - the request body;
    - the error texts;
    - the status column.
21. **E2E (`08` bank spec), with the docs screenshots taken from the same flow:**
    - settle a statement line with the wrong payments;
    - void the settlement with an approval;
    - settle it again correctly;
    - the statement reconciles.

## 10. Not in this plan

- A partial void or an edit of a settlement.
- The reversal of an `Adjust` journal of a statement line. Its Unclear leaves the journal behind in the same way; it is recorded
  as a separate finding and is not changed here.
- Voiding in a closed month without reopening it.
- Undoing a void.

## 11. Questions for the owner

| # | Question | Recommendation |
|---|---|---|
| V1 | The date of the reversal: the date of the settlement (its month must be open), or the business date? | **The date of the settlement** (§3). The corrected settlement is dated the statement line anyway, and the month of the statement stays right. The alternative, the business date, would also need a date on `Settle` and leaves the bank book of the statement's month wrong. |
| V2 | The approval: a new kind `BANK_SETTLEMENT_VOID` with the gross as its amount, or the existing `BANK` without an amount? | **The new kind** (§6). |
| V3 | A VAT claim already filed: reverse it in the next return, or refuse the void while it is claimed? | **Reverse it in the next return** (§5). A refusal would block every settlement whose return was filed and paid (a paid return cannot be voided), so most voids would be impossible. |
| V4 | Unclear of a settlement's matching: refuse it (void instead), or keep it allowed? | **Refuse it** (§4.4). It is the source of the KD-05 trap. |
