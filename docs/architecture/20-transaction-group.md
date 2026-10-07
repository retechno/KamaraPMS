# 20. Transaction Group / Split Bill

Status: **built** (migration 00063, `internal/folios/groups.go`, `internal/documents`, the folio screen). Design settled here; the planned concept and its open questions are in `19-post-implementation-audit.md` section 16.

> **Transaction Group is a presentation/operational grouping dimension within one folio. It is not a financial folio, does not create separate receivables, and does not affect GL ownership or folio balance.**

A folio can show its lines under up to four headings, A to D: room and breakfast under A, the extras the guest pays in cash under B. The groups organise the folio screen and the printed bill. Nothing else reads them.

## 1. What it is not

- Not a sub-folio, not a folio, not an invoice: no `sub_folio_id`, no table of financial folios.
- Not a balance. The folio balance is `sum(debit) - sum(credit)` of the whole folio (`FolioTotals`), as before. Group A may hold 500,000 of charges and group B a payment of 500,000: the folio balance is 0, and there is no balance, settlement or receivable per group anywhere in the money layer.
- Not a general ledger dimension. The day journal reads `folio_item_gl`, which does not know groups.
- Not a transfer. A charge moves between **folios** by the transfer of Architecture 18 (`POST /folio-items/{id}/transfer`: a reversal and a copy, a reason and an approval). Moving a line between **groups** is not a correction: no reversal, no posting, no journal. A payment never leaves its folio.
- Not the booking group of the groups module. The name `group_code` exists twice, on purpose (decided by the owner, not renamed): **`reservations.group_code` is the booking/group identifier** (the code of a block of rooms of one event, from `booking_groups`), and **`folio_item_groups.group_code` is the billing/folio transaction group** (a heading A to D of one folio's bill). They are different tables, different schemas in the API (`Reservation` and `FolioItem`/`Payment`) and have nothing in common.

## 2. Data model

`folio_item_groups` (migration 00063): `(property_id, folio_item_id)` primary key, `tenant_id`, `group_code text CHECK (group_code ~ '^[A-Z]$')`, `updated_at`, `updated_by`, with composite foreign keys to `properties (tenant_id, id)` and `folio_items (property_id, id)`.

- **A line with no row is in group A.** Every line that existed before the migration, and every line posted afterwards, is therefore in group A, without a data migration and without a write at posting time. A row exists only for a line that was moved (and stays once moved, even back to A).
- **Why a table of its own and not a column of `folio_items` and `payments`.** `folio_items` is append-only (the trigger `folio_items_append_only` refuses every UPDATE, which the project rules list as non-negotiable) and a payment may only change POSTED to VOIDED. A mutable `group_code` on them would need the guarantee loosened for the whole ledger. The side table leaves the ledger tables untouched, so the database still refuses any change to a financial field. This is the "small side table" option of the audit (section 16, point 1); the history is the audit trail (`folio_item.group_changed`) and not a second table.
- **A payment's group is the group of its ledger line.** A payment is a ledger line (`folio_items.payment_id`, unique), so `payments` has no group column: `Payment.group_code` in the API is the group of its line.
- **The limit of four** is in the application (`folios.GroupCodes`, the screens), not in the schema: the table accepts one capital letter, so a fifth group is a change of a list and a screen, not a migration.
- **Names** (`Company`, `Guest`, `Travel agent` instead of A, B, C) are not built: groups are A to D with a default label. A master table of group names is a later enhancement.

## 3. Which lines can be moved

The rules follow the ledger rules; none is new.

| Line | Movable | Why |
|---|---|---|
| CHARGE (manual, room night, package or service line, transferred copy, deposit charge) | yes | the move changes no financial field |
| ADJUSTMENT | yes | same |
| PAYMENT, REFUND (through the item or through the payment) | yes | the payment stays on its folio; only the heading changes |
| REVERSAL (a correction, a void) | **no, by itself** (409 `FOLIO_ITEM_GROUP_NOT_ALLOWED`) | it is shown in the group of the line it reverses, so that the pair always nets to zero in one group. Moving the original moves the reversal with it |
| a line of a CLOSED folio | **no** (409 `FOLIO_CLOSED`) | a closed folio is a final document and its print must not change |
| a line already reversed | yes | its reversal follows |

Lines reached by an invoice: a folio is invoiced when it is closed, so an invoiced line is a line of a closed folio. A transfer to the city ledger is a payment line of the folio and is movable while the folio is open.

## 4. API

- `PATCH /properties/{propertyId}/folio-items/{id}/group` with `{"group_code": "B"}`, and `PATCH /properties/{propertyId}/payments/{id}/group` (the same move on the line of a payment). Answer `{item_id, folio_id, payment_id, group_code, previous_group_code, changed}`; moving to the group the line is in answers 200 with `changed: false` and writes nothing.
- `group_code` is on every `FolioItem` and every `Payment` the API returns (`A` by default).
- Errors: 404 `FOLIO_ITEM_NOT_FOUND` / `PAYMENT_NOT_FOUND` (another tenant or property gets the same 404), 403 `PERMISSION_DENIED`, 409 `FOLIO_CLOSED`, 409 `FOLIO_ITEM_GROUP_NOT_ALLOWED`, 422 for a group that is not A to D (the code is normalised to capitals first).
- `GET /folios/{id}/invoice.pdf?group=ALL|A|B|C|D` (default `ALL`).

## 5. Permission

The existing `folio.post_charge`. Arranging a bill is part of posting to it, the same permission closes a folio, and the move touches no money, so it does not need the correction permission (`folio.reverse`, which asks for an approval). No permission was added.

## 6. Concurrency and the audit

One transaction: the open business day is share-locked (L1), the folio row is locked `FOR UPDATE` (L4), then the line is read and the row of the group is written with `INSERT ... ON CONFLICT DO UPDATE`. Two moves of one bill, or a move and a posting, take turns on the folio lock, so no move is lost halfway and the last writer wins with a consistent row. The audit entry `folio_item.group_changed` is written in the same transaction with the old group, the new group, the folio, the payment (if any), the user and the time (the audit writer's own).

## 7. Printing

The folio screen has a filter (All, A, B, C, D) and a choice of the group to print. The bill of a group (`?group=B`) lists the lines of that group. Its figures are said apart: **"Charges in this group"** and **"Payments in this group"** are sub-totals of the lines shown (with the net, service charge and tax of those lines above them), and **"Total charges (whole folio)"**, **"Payments received (whole folio)"** and **"Balance due (whole folio)"** are those of the entire folio, the same as on the page of the whole folio. A note says that a group has no balance of its own. A group with no line prints as an empty page with the folio figures. The file name is `bill-<folio>-B.pdf` (or `invoice-...` once the folio is closed). Printing writes nothing.

What was not touched: the tax invoice (faktur) is per folio, as before; whether a group can have its own buyer is a legal question and is not decided here. The guest folio, the company folio, the city ledger invoice and the receipts are unchanged.

## 8. Defaults that follow from "every new line is in group A"

- A **refund** is a new line and starts in group A; it does not inherit the group of the payment it refunds (a user moves it).
- The **copy made by a transfer** to another folio starts in group A.
- The **room night** and every posting start in group A.
These are consistent with the rule and cost nothing to change later (a write at posting time). **Known follow-ups, not blockers for this step (decided by the owner):** (1) a refund and the copy made by a transfer start in group A; (2) the tax invoice stays per folio, and no buyer or tax ownership is derived from a group.

## 9. Evidence

- Tests: `internal/folios/groups_test.go` (defaults, A to B to C to D to A for a charge and a payment, every part of the ledger, the payments and the general ledger byte-identical before and after, balance unchanged, audit entries, no-op, invalid codes, missing line or payment, reversal and void follow, closed folio, other tenant, other property, permission, concurrency), `internal/accounting/groups_test.go` (the journal, the day posts and the reconciliation do not change; the next night audit runs as before and the groups survive it), `internal/documents/documents_test.go` (All, A to D, the figures, invalid group, nothing written), `internal/app/groups_api_test.go` (HTTP), `db/tests/schema_test.sql` (the table's constraints and that the ledger is still append-only), `FolioView.test.ts`.
- No SQL that computes money reads `folio_item_groups` or `group_code`: the only reader besides the group queries is `ListFolioItems`, which joins the table on its primary key (so a line cannot be multiplied) to show the group. `FolioTotals`, the folio list balance, `DayActivity`, the reports and the tax queries are unchanged.
