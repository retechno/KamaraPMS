# 30. Groups: bulk rooms, folio transfers, the master folio and group check-in (APPROVED; part A is being built, the rest is design only)

Status: **approved by the owner** (the decisions are in section 9; the owner's changes to part B are in 4.3 and 4.5). The decisions listed in the request of the owner are taken as agreed. Where one of them meets a platform rule or the design in force, section 1 says so with the reason; nothing was changed silently. Read first: `18-architecture-decisions.md` section 3 (folio model, rule 2 the resolver, rule 4 when a rule applies, rule 6 the transfer, rule 7 split billing, 3.9 deposits, 3.13 the master folio kept for later), `28-check-in-panel.md`, `08-backlog.md`, and the groups paragraph of `README.md`.

Words: a **booking group** is `booking_groups` (a conference, a tour). A **transaction group** is `folio_item_groups` (the A/B/C/D heading of a bill, `20-transaction-group.md`): not meant here, and nothing below reads or writes it. The letters **A, B, C, E** of the request name the parts of this document (there is no D); each part is one PR.

| Part | What | Migration | PR |
|---|---|---|---|
| A | Add many rooms from a group, rooming list, rate override with one approval | **00073** | 1 |
| B | Transfer by item and by charge code, balance transfer, wider targets | **00074** | 2 |
| C | Master folio, group routing, role-based `is_deposit`, closing, invoices | **00075** | 3 |
| E | Group check-in with partial success, group payment section, deposits | none (uses C) | 4 |

## 1. Findings and conflicts with what exists (read this first)

1. **The migration numbers move.** The request assumes 00073 for the balance transfer. Part A comes first and needs a small migration (finding 3), so **A = 00073, B = 00074, C = 00075**. Nothing else is affected.
2. **A loop of `create` in one transaction would fail at the second reservation.** `db.EnterLockLevel` refuses a lower level after a higher one inside one transaction (`ErrLockOrder`). `Service.create` takes the group share lock (level 45, `resolveLinks`) and then a document number (level 50, the sequence). The second reservation of a batch would take the group lock (45) after the sequence (50) of the first. So the batch cannot just call the engine in a loop. It must take **every lock once, up front** (business day 10, the room types of all rows ascending 20, the group 45, share), and `create` must know that the group lock is already held. This is a change **inside the one booking engine** (a "locks held" marker next to the existing `held *civil.Date` that `CreateHeld` uses for walk-ins), not a second path. Guests created for the batch also take a sequence (50), so they are created **after** the group lock. Any hook run per reservation (`onConfirmed`, see finding 4) must not take a lower level; it only enqueues, and a test proves it.
3. **A confirmed reservation needs a booker guest (`BOOKER_REQUIRED`), and the group contact is only text** (`contact_name`, `contact_email`, `contact_phone`; no guest row). "Booker = the contact of the group" therefore needs a guest row. The same holds for "temporary guest name 'group name · n'": check-in, the folio, the invoice and the stay all key on a guest id, so the temporary name must be a **guest row** too. Proposal (migration 00073): `booking_groups.contact_guest_id` (the booker, created once from the contact) and `guests.is_placeholder boolean NOT NULL DEFAULT false` (a guest made by a group booking and not yet named). Placeholder guests are left out of guest search and the duplicate lists; naming one (the rooming list, check-in) clears the flag. The alternative, a text field on the reservation, was rejected: nothing downstream could use it. Guests are never deleted by this feature.
4. **A confirmation e-mail would be queued for every reservation.** `ReservationConfirmed` enqueues one to the booker (the group contact) for each of up to 100 reservations. Recommendation: the batch **suppresses** them (a context flag read by the hook) and the group screen offers one "send the confirmation of the group" later; until then nothing is sent. This is decision 2 in section 9.
5. **The request ties "Item lama dipindah dengan B" to the transfer part.** Fine: when a routing rule is saved, the group screen offers to move the nights already posted with the transfer of part B (charge code of the room, target = master). It is a use of B, not a second mechanism.
6. **The master folio replaces a statement of doc 18 and the README.** Doc 18 3.5 said a group master bill is "a city ledger invoice over the transfers of the company folios of its members", which "removes the main reason for a master folio", and the README lists "a master folio shared by a group" under "Not done, on purpose". The owner has decided otherwise; this document is the design that supersedes those two statements, and both are updated when part C is built. The reserved shape of doc 18 3.13 (`folio_type = 'MASTER'`, `reservation_id` nullable, a role-based `is_deposit` first) is followed.
7. **"Who closes the master: the cashier or correction permission."** Today `CloseFolio` needs `folio.post_charge`, which every cashier has, and `correction.approve` is the supervisor approval, not a right to act. I could not map the sentence to one existing permission without letting an ordinary cashier close a bill for many guests, so I propose one new permission, **`folio.master_close`**, and ask the owner to confirm (decision 7).
8. **The faktur pajak of the balance transfer.** The request asks how the transfer looks on the invoice and the tax invoice "of the target folio". The honest answer (5.5) is that **the tax invoice stays with the source folio**, because VAT is read from the components of the charges on a folio and a balance transfer moves no charge. If the payer must receive the faktur in its name, the charges must move with the transfer **by item**, not by balance. This is a business fact the owner should know before choosing part B's balance transfer.
9. **Allotment is out, as decided; one consequence.** A bulk booking takes real inventory with no release date, and the README says a whole group cannot be cancelled at once (it would chain reservation locks against the global order). Cancelling 100 rooms one by one is painful. A **cancel batch** is possible without breaking the order (lock the reservations of the group in **one** ascending call at level 40), but it is not in this request; it is decision 5.
10. **The deposit rule on the check-in screen is a screen rule** (doc 28: the check-in endpoint has no payment rule). The group rule of request item 19 extends that screen rule; no endpoint starts to refuse a check-in for a missing deposit.
11. **Check-in with one transaction per row** (request item 17) is not a deviation from the lock rules: it is what they require (section 9, E).
12. **A typo of the transfer notice.** `web/src/i18n/locales/id.ts` `noticeTransfer: 'Dipindahkan ke akun piutang perusahaan perusahaan.'` becomes `'Dipindahkan ke akun piutang perusahaan.'`. It is a one-line change in the first PR (the English text reads correctly).

## 2. What the code does today (facts this design relies on)

- **Booking engine** (`reservations/create.go`): `Create(propertyID, key, in)` validates, replays by `Idempotency-Key` (the key and a hash of the body are stored on the reservation: `GetReservationByKey`; another body is `IDEMPOTENCY_KEY_REUSED`), then `create` inside `WithinTx`: business day (share), room types and rooms (update) when confirming, sales restrictions, pricing, holds (`checkHolds`), the document number, the insert, `ConfirmReservation`, the audit entry, and the confirmation hook. `CreateHeld` is the same for a caller that holds the locks (walk-in). A reservation takes its group at share level and inherits the company of the group (`resolveLinks`, `OUTSIDE_GROUP_DATES`, `GROUP_INACTIVE`).
- **Rate override** (`reservations/override.go`): per-night `nightly_overrides` on a line, needs `reservation.override_rate`, a reason, and an approval (the caller's own when they hold `reservation.override_rate_approve`, else an approver's credentials via `VerifyApprovalFor`). It is verified **once per request**: the context carries an `overrideAuth` with `done`, so however many lines (or reservations sharing that context) price an override, one reason and one approval cover them. The approval is never stored; the audit entry gets the approver and the reason.
- **Groups** (`internal/groups`): list, get, create, update, members; permission `group.manage` (read through `reservation.read`). Lock level 45.
- **Check-in** (`frontdesk.CheckIn`): `frontdesk.checkin`; one transaction: `res.Lock` (business day, room type, room, reservation: 10, 20, 30, 40), `LockUnlinkedFolio` (42), `checkInCore` (stay number from the sequence: 50). Idempotent by key (`GetStayByKey`). Readiness (`READY`, `BLOCKED` with blockers, `NONE`) is read by the arrivals list with the rules the check-in itself applies. The arrivals list has no group filter.
- **Auto-assign** is client side (`AutoAssignDialog.vue`): rooms are proposed from the free rooms and saved one request at a time, a failure marks its row and does not stop the others.
- **Folios, transfer, routing, deposits, journal, reconciliation:** as in the previous section of doc 18 and in sections 5 to 8 below. The facts that matter: `TransferItem` is one charge, `folio.reverse` + approval, same reservation; `ResolveTarget` is the only router and a closed target is `ROUTING_TARGET_CLOSED`; `folio_item_gl.is_deposit` is "paid before the check-in of the folio's stay, or the folio has no stay"; `DayActivity` reads `folio_item_gl.kind IN ('CHARGE','PAYMENT')` and the view gives kind `CHARGE` to anything that is not a payment; `ControlSources` sums every `folio_items` row; reports select charges by `transaction_type IN ('CHARGE','ADJUSTMENT','REVERSAL')`; the tax invoice reads VAT from the components of the charges on its own folio; permissions are a Go catalogue (a new one needs no migration); `VerifyApprovalFor(ctx, property, approval, perm)` exists.
- **Lock levels:** business day 10, room types 20, rooms 30, reservations 40, stays 41, folios 42, payments 43, companies 44, groups 45, accounting 46, … sequences 50. Re-entering a level is allowed, going down is not.

---

## 3. Part A: add many rooms from a group (PR 1, migration 00073)

### 3.1 The form (Group detail, "Add rooms")

A table form; each **row** is: room type, number of rooms, arrival, departure, rate plan, rate per night, adults, children. A row defaults to the dates of the group and must lie inside them (the same `OUTSIDE_GROUP_DATES` rule, shown on the row). The rate per night defaults to the rate in force (the grid price, with a yield rule or restriction applied, read by the preview) and can be changed. A row can be added, copied, removed. Rows of the same room type may overlap in time; the preview adds them up.

**Preview before saving** (read-only, no lock, no write): `POST /booking-groups/{id}/room-batches/preview` with the same rows. It answers, per row: the number of rooms, nights, the grid price of each night, the estimate of one room and of the row (the figures of the existing charge/price engine, the client adds nothing it was not given), the sales-restriction verdict of the row (closed to arrival and so on, with the reason), and **the rooms left per room type per night after the whole batch** (a matrix, rows of one type cumulated). A row that does not fit is marked with the first night it is short and by how many. The preview is a snapshot; the save asks again under the locks, so a preview that was true a minute ago promises nothing.

### 3.2 The save

`POST /booking-groups/{id}/room-batches` with an `Idempotency-Key` header (`reservation.create` and `group.manage` are both needed; `guest.write` too, for the guests it makes).

Body: `rows[]` (as above, with the rate per night as an optional `rate_per_night`), `confirm` (default **true**: a block that holds nothing is not a block; `false` makes drafts), `booker_guest_id` (optional: else the contact guest of the group), `rate_override_reason` and `rate_override_approval`, and the pass-through approvals the booking engine already knows (`occupancy_approval`, `exceed_free_quota`, `restriction_override`, each **once** for the request).

**One reservation per room, all in one transaction, all or nothing.** The result is `reservations[]` (id, confirmation number, row, running number, guest id, line id) and the total. If anything fails, nothing is written and the error names the **row and the room within it** (`rows[2].count`, "room 7 of 12": the first room that did not fit).

**Through the existing engine, no second path.** The batch is a caller of `create`, as the walk-in is:

1. `RequireOpenBusinessDay` (share, level 10).
2. `LockRows(RoomTypes, ForUpdate)` once, **all distinct room types of all rows, ascending** (level 20). No room is chosen (rooms are assigned later), so there is no level 30 lock.
3. `LockRows(Groups, ForShare)` once (level 45); the contact guest and the placeholder guests are made now (sequence, 50); then `create` per reservation with "locks held" so `resolveLinks` does not lock the group again (finding 2).
4. Each reservation's holds see the rows inserted by the ones before it in the same transaction, so "the rooms ran out in the middle" is found at the room where it happens, and the whole transaction rolls back.

**Idempotency.** The engine stores a key and a hash on each reservation. The batch derives them: reservation *i* gets the key `K#i` (the header key is limited so that the longest suffix stays within 100 characters) and **the hash of the whole request**. A replay finds `K#1`; if its hash is not the hash of this request it is `IDEMPOTENCY_KEY_REUSED`; otherwise the *n* reservations `K#1..K#n` are read back and returned (all-or-nothing guarantees they all exist). Two requests with the same key at once: the loser gets `DUPLICATE_REQUEST` from the unique key and replays the winner, as `Create` does. No batch table is needed.

**Limit.** **100 rooms and 20 rows per request** (`TOO_MANY_ROOMS`, `TOO_MANY_ROWS`). A reservation costs a few dozen statements (restrictions, pricing, availability, inserts, audit); 100 of them in one transaction that holds the room-type locks is a few seconds, which is what a front-desk request can afford. A bigger group is several requests with different keys, each atomic. The number is a constant to be confirmed by measuring during implementation (decision 1).

**Audit.** The engine's own `reservation.created` for each reservation (with the override approver and reason), and **one** `group.rooms_added` on the group (rows, rooms, nights, who approved, the key).

### 3.3 Names, booker, company

- Booker: the group contact guest (`booking_groups.contact_guest_id`, created at the first batch from `contact_name`, `contact_email`, `contact_phone`; `GROUP_CONTACT_REQUIRED` when the group has no contact name and the request names no booker). The reservation header `guest_id` = booker.
- Each **line** gets a placeholder guest named **"group name · n"** (last name, cut to the column; `n` is the running number in the group, continuing after the highest number already used), `is_placeholder = true`.
- The company of the group follows by the existing rule (`resolveLinks`).
- The confirmation e-mail is not queued (finding 4).

### 3.4 The rooming list

In Group detail: a table with one row per reservation line (room, type, dates, confirmation number, status, **name, e-mail, phone**), editable in place. Saving edits the guest row of the line (`PATCH /booking-groups/{id}/rooming-list`, `guest.write` and `reservation.update`, all rows or none). Naming a placeholder clears `is_placeholder`. Linking a line to an **existing** guest (a returning guest) is a row with `guest_id`; it changes the line, so it needs the reservation `version` and locks the reservations of the changed rows in **one ascending call** (level 40).

**CSV import** (`POST /booking-groups/{id}/rooming-list/import?dry_run=true|false`): columns `confirmation_number` (or `n`, the running number), `first_name`, `last_name`, `email`, `phone`. All or nothing; `dry_run=true` reads and checks everything and writes nothing; **every wrong row is reported** (row number, column, code), not the first only (the same pattern as the budget CSV import). The dry run returns what would change (counts, the rows). Unknown confirmation numbers, rows of another group, duplicates in the file and malformed e-mails are row errors.

### 3.5 Rate that differs from the price: one reason, one approval

The existing rule needs `reservation.override_rate`, a reason and an approval, verified once per request through the context. The batch sets **one** `WithOverrideApproval(ctx, approval, reason)` at the top and calls `create` for every reservation with that context, so the first line that needs it verifies it and the rest reuse it (`overrideAuth.done`). A row whose rate per night differs from the grid price becomes `nightly_overrides` (one per night, the price of the night as the grid would charge it replaced by the agreed amount, in the price mode of the plan, as the booking form does it today). A row at the grid price needs no override. The request therefore carries **one** reason and **one** approval for any number of rooms; the approver and the reason go on every reservation's audit entry and on the group audit entry. Without the permission the request fails before anything is written (403), and with the permission but no approval it is `APPROVAL_REQUIRED` with the field.

### 3.6 Not in this PR

Allotment (rooms held with a release date) and a cancel of the whole group. Rooms are **not assigned** by the batch (the lines have no room); assignment is the existing one, or "assign automatically" of part E.

### 3.7 API, errors, permissions, migration

- OpenAPI: `POST /booking-groups/{id}/room-batches/preview`, `POST /booking-groups/{id}/room-batches`, `GET/PATCH /booking-groups/{id}/rooming-list`, `POST /booking-groups/{id}/rooming-list/import`; `Group` gains `contact_guest_id`; `Guest` gains `is_placeholder`. Run `npm run gen:api`.
- Permissions: no new one (`reservation.create`, `group.manage`, `guest.write`, `reservation.update`; the approvals existing).
- New codes: `TOO_MANY_ROOMS`, `TOO_MANY_ROWS`, `GROUP_CONTACT_REQUIRED`, `ROOMING_IMPORT_INVALID` (with the list of rows). Reused: `OUTSIDE_GROUP_DATES`, `GROUP_INACTIVE`, `IDEMPOTENCY_KEY_REUSED`, `APPROVAL_REQUIRED`, the availability error of the engine (with the row).
- Migration **00073**: `booking_groups.contact_guest_id` (FK `(tenant_id, id)` of guests), `guests.is_placeholder`, an index for the guest search that leaves placeholders out; Down drops them (refused if a placeholder guest exists and is still the guest of a line, since the column is data, not a setting).
- The guest search, the duplicate list and the guest merge leave placeholders out unless asked.

---

## 4. Part B: transfers between folios (PR 2, migration 00074)

One trail and one return serve every kind of transfer (by item, by charge code, balance): 4.5. A **closed folio can never be the source or the target** of a transfer or of a return (`FOLIO_CLOSED`, with the folio in the context): there is no shortcut; the way round is the existing `folio.reopen`, done by someone who holds it.

### 4.1 Transfer by item and by charge code (request item 6)

`POST /folios/{id}/transfer-items` with `{ folio_id, item_ids?: [], charge_code_id?: n, reason, approval }`; exactly one of `item_ids` and `charge_code_id`. `{id}` is the **source**, so one request has one source and one target: the lock set is two folios, ascending id, level 42, as `TransferItem` does today.

- **By code:** the server resolves the items **after the locks**: every CHARGE of that code on the source that is not reversed (a reversal carries the code of its original, so reversed pairs drop out). The screen shows what it believes it selected; the answer says what moved.
- **By items:** all ids must be movable items of the source or the **whole request** fails (`FOLIO_TRANSFER_INVALID`, the ids in the context) and nothing is written.
- At most 200 items (`TOO_MANY_ITEMS`); empty selection `TRANSFER_SELECTION_EMPTY`.
- **Mechanism:** the body of `TransferItem` after its locks becomes `transferOne`; `TransferItem` calls it once, the bulk endpoint calls it per item **inside one `WithinTx`**: the same `reverseAs` and snapshot copy (amounts, components, revenue account, department, service date), the register slot of a room night kept. One reason, **one approval verified once** (`folio.reverse` + the approval of today), one DB transaction, one audit entry per item (same batch id, same approver) and one summary entry `folio.items_transferred`. Net zero per account and department by construction. Payments, refunds, a REVERSAL by itself and an already reversed charge do not move (today's rule).

### 4.2 Where a charge may go (request item 8)

All targets OPEN, same property, with a stay **or** the MASTER folio of part C (the unlinked deposit folio is still refused). The server **classifies** the pair after the locks and answers with the scope; a caller cannot ask for more than the pair is.

| Scope | Rule | Permission |
|---|---|---|
| `RESERVATION` | same `reservation_id` (today's rule) | `folio.reverse` + approval |
| `GROUP_MEMBER` | both reservations have the same non-null `booking_group_id` | `folio.reverse` + approval |
| `GROUP_MASTER` | the target is the master folio of the group of the source's reservation (or the source is the master and the target a member's folio) | `folio.reverse` + approval |
| `ANY` | any other open folio of the property | **new `folio.transfer_any`** on the actor **and** on the approver (`VerifyApprovalFor`) |

A request for an unrelated target without the permission is `403 PERMISSION_DENIED`. The audit entry records the scope, both folios and both guests. No default role holds `folio.transfer_any`.

`GET /folios/transfer-targets?from_folio_id=&q=&limit=` finds the target for the screen: up to 25 open folios, each with scope, folio number, room, guest, balance, related scopes first; `q` matches the folio number, the room number or the guest name; `ANY` rows only for a caller who holds the permission. The `Combobox` gets a debounced `search` event that lets the page replace its options (it already keeps the typed text apart from the label since PR #22).

### 4.3 Balance transfer (request item 7, changed by the owner)

"Folio F1 hands X of what it owes to F2." F1's balance goes down by X, F2's up by X. No charge is split; revenue and tax stay on F1.

**It is an ordinary ledger transaction with ONE system charge code, `TRF` "Transfer saldo", not a new row type.** The first draft added a `TRANSFER` transaction type; that is dropped. No CHECK of `transaction_type` changes, no new kind of row reaches code that was not written for it, and the posting path is the one that exists.

- **The code.** A row of `charge_codes` with `is_system = true` and a new `charge_type = 'TRANSFER'` (`charge_codes_type_ck` learns the value; the other codes keep theirs). It is created for **existing properties by the migration** (00074) and for **new properties by `seed_charge_codes`**, which gets the row, with the Indonesian name where the property is Indonesian (D3). It cannot be edited, deactivated or deleted (the way the other system codes are protected; a test).
- **Out and in.** On F1 a signed **ADJUSTMENT** of -X (a credit; the engine already runs on a signed base, and `folio_items_charge_side_ck` allows a credit only on an adjustment), description "Transfer ke FOLxxx"; on F2 a **CHARGE** of +X, description "Transfer dari FOLxxx". The pair is tied by the transfer log (4.5, `folio_transfers`): its id is the `reference_id` of both rows (`reference_type = 'FOLIO_BALANCE_TRANSFER'`), so each points at the other through it. Both rows have the same business date and the reason of the batch.
- **No tax and no service, with no second path.** `ChargeCalculationEngine` gets the rules of a code from `billingconfig.ResolveRules`: the active tax and service rules mapped to it. **`TRF` has none, and the configuration refuses to map one to it**, exactly as `ROOM_EXEMPT` is "no tax or service" because nothing is mapped to it. So the same engine, through the same function, returns base = net = the amount and **zero components**; nothing is special-cased in the calculation. A test asserts the zero components and that mapping a tax or a service charge to `TRF` is refused.
- **Not postable by hand.** `postCharge` refuses a code of type `ROOM` unless it comes from the room posting (`ROOM_CHARGE_REQUIRES_ROOM_POSTING`); the same guard refuses type `TRANSFER` (`TRANSFER_CODE_NOT_MANUAL`) in the Charges tab and the adjustment endpoint, and the code is not offered in the charge-code list of the cashier. Only the transfer function (and the return of a balance transfer) posts it, passing the code itself.
- **The ledger account is a map key, like QRIS 1155.** A new system key **`FOLIO_TRANSFER`** in `gl_account_map` (`gl_account_map_key_ck` learns it; `mapKeys` shows it in Account mapping as an ASSET key). The default is a new **intermediary account 1215 "Transfer between folios"** (id: "Transfer antar folio"), an ASSET under 1200: for a **new property** from `usali_chart()` and `usali_chart_names('id')`, for a **property that exists** from migration 00074 with the rules of 00071. A property that already has the key is left as it is. With no account 1215 it is created and mapped. With an account 1215 already (the code is the user's) it is neither used nor overwritten and the next free code from 1216 to 1299 is used, the NOTICE of the migration counting the replacements. Down is refused while a `TRF` row exists; otherwise the key goes, and the account with it when it is still what the migration made and nothing uses it. The account is found through the key, never through its code. At posting the item **snapshots the account in force** into `revenue_account_code` (the posting passes it, as the copy of a transfer takes the account of its original), so a later remapping never rewrites what was posted. A transfer is refused with `TRANSFER_ACCOUNT_NOT_MAPPED` when the key does not point at an active account that takes postings.
- **Not revenue.** The account is an asset, so no income statement line moves. The reports that read revenue from the ledger by charge code and revenue account (`reports`, the revenue summary of the night audit, the tax invoice's "what was charged") leave `charge_type = 'TRANSFER'` out explicitly; a test adds a transfer and compares each of them.

**The day close (journal).** For kind `CHARGE`, `DayActivity` debits GUEST_LEDGER by charge code and credits REVENUE by the revenue account (and the components). The +X on F2 gives Dr guest ledger X and **Cr 1215 X**; the -X on F1 gives Cr guest ledger X and **Dr 1215 X**. Each is grouped by account and charge code and summed, so both net to zero, and `DayActivity` drops a line whose amount is 0: **a day with only transfers has no journal line from them.** The **balance of 1215 is zero at every day close**, by construction (a pair has one business date) and by a check: the accounting readiness of the night audit gets the blocker `TRANSFER_ACCOUNT_NOT_ZERO` when the net of the day on the mapped account is not zero (it can only be a manual journal or a damaged pair, which is what the check is for). A test posts transfers on several days, in both directions, with returns, and asserts the journals and the balance of 1215.

**Reconciliation of the control accounts stays equal.** `ControlSources.folio_balance` sums debit minus credit of every row: the pair adds 0. The guest ledger control (1210) gets Dr X and Cr X. Advance deposits (payments only) and the city ledger are untouched. The difference is unchanged with and without deposits on the folios, before and after the close and the release of a deposit (test).

**`is_deposit`.** The view marks a deposit only for a row that has a payment (`p.id IS NOT NULL`). A `TRF` row has none, so it is not a deposit, creates none and moves none. A deposit payment stays on the folio that took it and is classified as today; it is released to the guest ledger when that folio closes. For a MASTER folio a `TRF` row received counts as a charge for the role rule of part C (a payment after it is a settlement).

**Invoice of the target folio.** F2's invoice lists "Transfer dari FOLxxx · room N" (a charge of the code `TRF`, not taxable; the folio number and the room of F1 come from the log) and counts it in the total due; F1's invoice shows "Transfer ke FOLxxx" as a credit, so its balance due reads 0. `documents` shows the `TRF` lines in their own section, "Transfers between folios", and they add nothing to a VAT line.

**The tax invoice (faktur pajak) stays with the source folio.** VAT is read from the components of the charges on a folio (`FolioVATLines`); a balance transfer moves no charge, so F1's faktur is unchanged and F2's gets no VAT from the transfer. **The dialog of the balance transfer shows this warning above its button: "Butuh faktur pajak atas nama pembayar? Gunakan transfer per transaksi." / "Need a tax invoice in the payer's name? Use transfer by item."**

Rules: X > 0; X <= the balance of F1 **read after the lock** (a transfer cannot create a credit, i.e. a fake deposit, and two concurrent transfers cannot take the same balance); F1 differs from F2; both OPEN; the target scope of 4.2; **an approval always** (`folio.reverse` + approval; `folio.transfer_any` for `ANY`); an `Idempotency-Key` as payments have. Moving a credit balance (a deposit on the wrong folio) is not supported: void or refund it and post it again.

**Migration 00074** (one file, Up and Down): `charge_codes_type_ck` and the row `TRF` for every property (and `seed_charge_codes`); the key `FOLIO_TRANSFER` in `gl_account_map_key_ck` and the account 1215 (new properties: `usali_chart()`, `usali_chart_names('id')`; existing ones: the data step with the replacement rule); `folio_transfer_batches` and `folio_transfers` (4.5) with the business-day trigger on the batch (a posting-like table, as the project rule asks) and their indexes. **No change to a `folio_items` column or constraint and none to the view `folio_item_gl`** (a `TRF` row is a plain charge or adjustment with an account of its own). Down is refused while a `TRF` row or a log row exists.

### 4.4 API, errors, permissions

OpenAPI: `POST /folios/{id}/transfer-items`, `POST /folios/{id}/transfer-balance` (`Idempotency-Key`), `POST /folios/{id}/transfers/return`, `GET /folios/{id}/transfers`, `GET /folios/transfer-targets`; the folio item gains `transferred_from`; the system map gains the key `FOLIO_TRANSFER`. Permissions: **`folio.transfer_any`** (new, **granted to no built-in role**: the catalogue lists it and a tenant gives it to the roles it chooses). New codes: `TOO_MANY_ITEMS`, `TRANSFER_SELECTION_EMPTY`, `TRANSFER_SCOPE_NOT_ALLOWED`, `TRANSFER_CODE_NOT_MANUAL`, `TRANSFER_ACCOUNT_NOT_MAPPED`, `TRANSFER_ACCOUNT_NOT_ZERO` (a readiness blocker), `ALREADY_RETURNED`; reused: `FOLIO_TRANSFER_INVALID`, `ALREADY_REVERSED`, `TRANSFER_EXCEEDS_BALANCE`, `FOLIO_CLOSED`. New constraint names get their mapping in `internal/platform/db/errors.go`.

---

### 4.5 The trail, the list of what was transferred, and the return (the owner's additions 3 to 7)

**The trail** lives in two **append-only** tables (insert only; a trigger refuses update and delete, as for the ledger), so `folio_items` itself does not change:

- `folio_transfer_batches`: id, tenant, property, `kind` (ITEM or BALANCE), from folio, to folio, reason, business date, `approved_by`, scope, `idempotency_key` (unique per property), created_at and created_by. One per request: one reason, one approval, one DB transaction.
- `folio_transfers`: id, batch id, `from_folio_id`, `to_folio_id`, `out_item_id`, `in_item_id`, `original_item_id` (item kind: the charge that was moved), `amount`, **`origin_folio_id`**, **`previous_folio_id`**, `returns_transfer_id` (nullable). One row per moved charge, or per balance pair.

**First origin and previous folio.** Every row stores the **first folio** the charge came from and the folio **just before** the one it goes to. A charge moved from A to B: `origin = A`, `previous = A`. Moved again, from B to C: the log row that brought it to B is found through `in_item_id` (a unique index), `origin` is copied from it (**A, not B**) and `previous = B`. A chain A, B, C always names A as the origin. These are columns of the log, written once at the insert, so the ledger stays append-only.

**A limit, reported and not hidden: the balance.** A balance is fungible. After F2 received X from F1 it may have been paid down, and which part of what F2 hands on to F3 "came from F1" has no answer. So for a `TRF` pair the log records `origin = previous = the folio it left` (the source of that pair). Its return goes back to **that** folio; "to the first origin" has no other meaning for a balance, and the screen offers it for charges only.

**What left a folio (item 4).** On the source folio, **"Show what was transferred"** (a filter of the folio screen, `GET /folios/{id}/transfers?direction=out`) lists the transactions that went out: the transaction, the amount, **to which folio** (a link, with room and guest), **where it is now** (the end of the chain: it may have moved on), by whom and when, and the button **Return**. The normal folio view **hides the reversal pairs a transfer makes** (the original and its reversal, which sum to zero) behind this filter, with a toggle "show the corrections"; every balance is still read from every row and stays right. The lines of a **balance** transfer are not hidden: they are the settlement the invoice shows.

**What came in (item 5).** Each incoming line shows "dari FOLxxx · kamar N" (when the first origin is another folio: "dari FOLaaa via FOLbbb"), the folio number a link to that folio. It is read from the log (`GET /folios/{id}` returns `transferred_from` on the item), not parsed from the description.

**Return (item 6).** `POST /folios/{id}/transfers/return` with `{ transfer_ids[], to: "PREVIOUS" | "ORIGIN", reason, approval }`; `{id}` is the folio that holds the charges now. **One or many at once**, one transaction, **always with an approval** (`folio.reverse`, and `folio.transfer_any` when the way back is an `ANY` pair) and a reason. The mechanism is the transfer's own: for a charge, a reversal on the holder and a copy on the target (`transferOne`); for a balance, the opposite pair of `TRF` rows. Each return is a new `folio_transfers` row with `returns_transfer_id` set to the transfer it undoes, and a **partial unique index on `returns_transfer_id`** lets a transfer be returned once, whatever the race (`ALREADY_RETURNED`). A charge that has moved on is returned from where it is now, to the previous folio or straight to the first origin, never to an arbitrary folio. The audit entry (`folio.transfer_returned`) names both folios and the transfer and is **visible from both folios**: the lists show incoming and outgoing rows, and a returned row says "returned" with who and when. The scope rules of 4.2 are not asked again for a return (it goes where it came from), but both folios must be OPEN.

**Closed folios (item 7).** The source and the target of a transfer, and the holder and the target of a return, must be OPEN. Otherwise the answer is `FOLIO_CLOSED` (the existing code) with `folio_id` and `folio_number` in the context, and the return screen says which folio is closed and that the previous folio or the origin may still be chosen. Nothing moves "through" a closed folio and no flag skips the check; `folio.reopen` is the existing, separate right.

## 5. Part C: the master folio of a group (PR 3, migration 00075)

### 5.1 The folio (request item 9)

A folio of type **`MASTER`**, one per booking group, without a stay and without a reservation.
- `folios.reservation_id` becomes nullable; new `folios.booking_group_id` (FK `(property_id, booking_group_id)`); checks: `(folio_type = 'MASTER') = (booking_group_id IS NOT NULL)`, a MASTER has no stay and no reservation, every other type keeps `reservation_id NOT NULL`; `folios_payer_ck` is relaxed so a MASTER may carry the group's company (copied when it is opened; the buyer of its invoices); `UNIQUE (booking_group_id) WHERE folio_type = 'MASTER'` (a mistaken close is repaired with `folio.reopen`, not a second folio).
- It is opened by hand (`POST /booking-groups/{id}/master-folio`) or by the first routing rule, **never inside a posting run** (it takes a document number, the last lock).
- **Ripple:** `reservation_id` nullable changes the generated Go type to a pointer everywhere a folio is read (the compiler lists the sites). The inner joins to `reservations` (for example the night audit's list of open folios of dead reservations) already leave a MASTER out, which is wanted; the folio list, the folio view and `documents.Invoice` get a MASTER branch.

### 5.2 `is_deposit` becomes role-based (the prerequisite; doc 18 3.9)

Today: paid **before the check-in of the folio's stay, or the folio has no stay**. A MASTER has no stay, so every payment on it would be an advance deposit until it closes, and the guest ledger would carry its charges while advance deposits carried its settlements.

New rule in the view (00075):
- folio type GUEST or COMPANY: **the old expression, unchanged** (including its dependence on the check-in time, which is what turns a payment back into a deposit when a check-in is reversed);
- folio type MASTER: a payment that is not `CITY_LEDGER` is a deposit when **no CHARGE row of the folio (a `TRF` charge included) has a lower item id** than the payment's own row, i.e. it was taken before the folio's first charge. After the first charge a payment is a settlement.
- The deposit is released when the folio closes (`closed_on`) as for any folio.

**Impact on old data: none.** No MASTER exists before 00075 and the GUEST/COMPANY branch is the old expression. A test classifies a fixture of every existing case (a reversed check-in, a refunded deposit, a city ledger transfer, a payment on the unlinked folio) with the old view and the new one and compares row by row.

### 5.3 Auto transfer is routing (request item 10)

The charge goes to the master **when it is posted**: nothing is posted to the guest and moved, so the member's folio never carries it, the journal has one entry, and the tax sits on the master from the start (its tax invoice, addressed to the group's company, is complete).

- **Instructions of a group:** `group_billing_instructions (booking_group_id, scope ROOM|CHARGE_CODE|ALL, charge_code_id, …)`, `UNIQUE (booking_group_id, scope, COALESCE(charge_code_id, 0))`. They all point at the group's master; there is no target column. **Exclusion of a member:** `folio_group_routing_exclusions (reservation_room_id PRIMARY KEY …)`: the line stays out of the group routing (all scopes). Small on purpose; an exclusion per scope can be added as columns.
- **Resolver** (`routing.go`, `resolve`, still the only place): the **line instructions** (company folios, doc 18) win first, in the same order (code, ROOM, ALL); if none matches, an excluded line goes to its guest folio; otherwise the group instructions are tried in the same order and answer the **open master folio**; otherwise the guest folio. A group rule whose master is closed or missing answers `FolioID 0, Routed true` = **`ROUTING_TARGET_CLOSED`**, the blocker that exists, never a fallback to the guest. `ResolveRoomTargets` (the expected-charge loader) takes the same data, so the preview and the posting agree.
- **When it applies:** from the next night **not yet posted** (rule 4). A night already posted stays; moving it is part B (charge code of the room, scope `GROUP_MASTER`), offered by the group screen right after a rule is saved. Only what posts without being told a folio (room nights, later a POS) is routed; a manual charge names its folio.
- **Locks and the race with a posting run.** A run holds the stays (41, `FOR NO KEY UPDATE`), reads the instructions after that, then locks the folios (42). Changing a group instruction takes, in order: business day (10) → the **open stays of the group's members**, ascending (41, `FOR UPDATE`, which conflicts with the run's lock) → the master folio (42) → the group row (45) → the sequence when a folio is opened (50). So a change and a run take turns, as a line instruction and a run do today. The set is bounded by the in-house members of one group. No path takes a group lock before a folio lock.

### 5.4 Check-out of a member, payment, closing (request items 11, 12, 13)

- **Member check-out:** unchanged. `CloseStayFolios` lists `stay_id = …`, so the master is never in it; the member's folio(s) must be zero (`FOLIO_NOT_BALANCED`), the master stays open. The last member's check-out result carries an info `master_folio` (number, balance) so the desk is told the group still has an open bill; it does not block.
- **Paying the master:** (a) a **payment** on the master through the normal cashier flow (it works on a folio without a stay; the role-based rule makes it a settlement after the first charge, a deposit before); (b) a **city ledger transfer** to the company of the group (`folios.Transfer`: a master that carries a company obeys `TRANSFER_COMPANY_MISMATCH`; a master without a company may choose one, with the credit limit gate). A credit balance is refunded with the existing refund flow.
- **Closing the master** (`POST /folios/{id}/close`, MASTER branch; **`folio.master_close`**, new; finding 7): refused unless the balance is exactly zero (`FOLIO_BALANCE_NOT_ZERO`), **every member line is over** (COMPLETED, CANCELLED or NO_SHOW: no member in house, none still to arrive; `MASTER_FOLIO_GROUP_ACTIVE`, with the lines in the context), and no night is left to post to it. The rows are re-read after the lock (as `SetBillingInstructions` does since F-09). Closing sets `closed_on`.
- **Night audit:** no new blocker for an open master (a hotel may bill a group days later). A **warning** in the style of the open folios of dead reservations: "master folio of a group that has left is still open, with its balance". The blocker that matters stays `ROUTING_TARGET_CLOSED`; because the master cannot close while a member can still post, it can only appear after a repair (a reopened master closed again by mistake), and then it stops the audit before anything is posted.
- **Invoice and faktur of the master:** the party is the **company of the group** (`bill_to_company_id`), else the **contact of the group** (name, address), printed as "Bill to"; `documents.Invoice` gets a MASTER branch (booking line = group code and name, the members listed). The tax invoice takes its buyer from `bill_to_company_id` as for a COMPANY folio; for a master without a company it takes the contact on the path a guest folio without a tax number takes today (**to be confirmed with the owner's tax consultant (konsultan pajak) before the faktur of a master without a company is built or used; the design does not invent a rule**).

### 5.5 Migration 00075

The `folios` changes of 5.1, `group_billing_instructions`, `folio_group_routing_exclusions` (settings: no business-day trigger), the replaced `folio_item_gl` view (5.2, on top of the 00074 form), indexes by group. Down: refused when a MASTER exists.

---

## 6. Part E: group check-in (PR 4, after C)

### 6.1 The screen (request items 15, 16, 20)

**Group detail, "Check in group":** the members that **arrive today** (the business date), one row per line: a checkbox, room (or "no room"), guest name (a placeholder is shown as such, with a link to the rooming list), type, party, and the **readiness** of the existing arrivals read (`READY`, or `BLOCKED` with the blockers: no room, room not ready, occupied, blocked, guest missing). The list is the arrivals read with a new `booking_group_id` filter (no new rule), plus per row an **identity warning** (a foreign guest without a passport number: the APOA warning of doc 28) shown as a badge, never blocking. The staff tick the rows to check in; "select all that are ready" is one button. **"Assign automatically"** (the existing `AutoAssignDialog`) is on the same screen and gives rooms to the rows that have none, one request at a time as today.

### 6.2 Partial success, and why one transaction per row (items 17)

`POST /booking-groups/{id}/check-ins` with `rows[]` (`line_id`, `version`, optional `room_id`, `guest_id`, adults, children, the not-ready override with its reason, and an optional per-room deposit) and a header `Idempotency-Key`. The server takes the rows in ascending line id and runs the **existing `frontdesk.CheckIn`**, **each in its own transaction**, and answers per row `CHECKED_IN` (with the stay), `ALREADY_CHECKED_IN`, or `FAILED` with the code and message of the engine (`ROOM_NOT_READY`, `ROOM_OCCUPIED`, `GUEST_MISSING`, `ARRIVAL_DATE_MISMATCH`, a version conflict, …). The HTTP answer is 200 for a request that was understood, whatever the rows did; the row is the unit of success.

Why a transaction per row: that is what the lock rules require, not an exception to them. A check-in locks the room type (20), the room (30), the reservation (40), the folio (42) and takes a stay number (50); a second check-in in the same transaction would lock its room type (20) after the first took its stay number (50), which `EnterLockLevel` refuses. Taking every room type, room and reservation of the run up front would hold dozens of rooms for the whole run, and one room that is not ready would roll back the others, which is exactly what the owner does not want.

**Idempotency per row.** Row *i* uses the key `K#line_id`; `frontdesk` already stores the key on the stay and replays it (`GetStayByKey`), so **repeating the request returns the same stay for the rows that went through and tries again only the rows that failed**: no double check-in. A line checked in meanwhile by someone else is reported `ALREADY_CHECKED_IN`, not an error. Two staff running the group screen together check each room in once (the version and the status are checked under the locks).

### 6.3 The group payment section (item 18)

On the same screen, **Group payment**: *no master* (each member's folio carries its own charges), or *use the master* with **what is routed**: the room, chosen charge codes (a multi-select), or everything. It is saved **on the group** (`PUT /booking-groups/{id}/billing-instructions`, which opens the master folio when there is none; **`folio.master_manage`**, new: opening the master and editing the routing, held by the same people as `folio.master_close` by default advice) and nothing is stored per member, so **a member who checks in later follows the rule**: routing is resolved when a night is posted. The same editor is on the group page and can change at any time (it applies to the nights not yet posted; a button moves the posted ones with part B). A line can be excluded from the group routing from the rooming list.

### 6.4 Deposits (item 19)

- **Per-room incidental deposit** (`properties.checkin_incidental_deposit`) is proposed **only for a row whose charges stay outside the master**: a row is exempt when the group routing includes everything (`ALL`) and the line is not excluded. When every charge goes to the master the group screen asks for no deposit per room (the column is not shown). A row that does need one gets the amount of the setting, editable; it is recorded as a normal deposit on the reservation (`POST /reservations/{id}/deposits`, key `K#line_id#dep`, `payment.post`) **before** its check-in, as in the check-in panel (doc 28), each in its own transaction. If the deposit goes through and the check-in fails the row says so and a repeat only retries the check-in; if the deposit fails the row is not checked in. This remains a rule of the screen: the check-in endpoint has no payment rule.
- **Group deposit or guarantee:** a payment on the **master folio** (`POST /folios/{id}/payments`; a cash payment needs an open cashier shift as always). Before the master's first charge it is an **advance deposit** (the role-based rule, 5.2), after it a settlement. The group payment section has a "Record a group deposit" button and shows the master's balance.

### 6.5 No new migration, one read

E uses the tables of C. API: the `booking_group_id` filter and the warning on the arrivals read, `POST /booking-groups/{id}/check-ins`. New codes: `GROUP_CHECKIN_EMPTY`, `TOO_MANY_ROWS` (at most 100 rows). Permissions: `frontdesk.checkin` (existing), `payment.post` for a deposit, `frontdesk.checkin_unready_room` for an override, `folio.master_manage` for the group payment section.

---

## 7. The screens, in one place (UI, en and id)

- **Folio:** a checkbox on every movable CHARGE row (not on a reversed pair, a payment or a TRANSFER row), "select all" for the visible rows, a **filter by charge code** (choose a code, select all = transfer by account), a bar with the count and total and **Transfer…**. The dialog: **Charges** (the selection, or all charges of code X) or **Balance** (amount, default the balance), the **target** in a `Combobox` with server search (folio number, room or guest; the scope as a badge, the balance; closed folios absent), the reason, the approval fields, and the one-line hint about the faktur. Result summary, the folio reloads. (PR 2)
- **Group:** *Add rooms* (the table form and the preview), *Rooming list* (editable table, CSV import with the dry run), *Master folio* (number, balance, open or closed, open, close, link), *Routing* (rows, the exclusion, "move the nights already posted"), *Check in group* (6.1 to 6.4). (PR 1, PR 3 and PR 4)
- **Reservation and guest folio:** a notice "Room charges go to the master folio of group X" (or to the company folio), from a new read of the **effective** routing with its source (`LINE`, `GROUP`): `GET /reservations/{id}/rooms/{lineId}/billing-instructions`. The folio of a member shows the same line. (PR 3)
- i18n English and Indonesian for every string, key parity tested; `data-testid`, input names and headings that tests read are kept.

## 8. Lock order (all of it follows the global order)

| Operation | Order |
|---|---|
| A batch | business day (10) → all room types, ascending (20) → group, share (45) → sequences (50: the guests, the reservations) |
| A rooming list edit (link an existing guest) | reservations of the changed rows, one ascending call (40) |
| B transfers | business day (10) → both folios, ascending (42); no payment, company or group lock |
| C routing change | business day (10) → open member stays, ascending (41) → master folio (42) → group (45) → sequence (50) |
| C close of the master | business day (10) → the master (42); member and night state re-read after the lock |
| E group check-in | per row, its own transaction: business day (10) → room type (20) → room (30) → reservation (40) → unlinked folio (42) → stay number (50) |
| Posting run with a master | as today: stays (41) → folios ascending (42), the master being one of them |

No path takes a group lock before a folio lock, or a lower level after a sequence.

## 9. Decisions (taken by the owner)

All the proposals of this document were approved; the owner's changes are marked.

1. Migrations: **A = 00073, B = 00074, C = 00075**; E has none.
2. Batch limit: **100 rooms and 20 rows per request** (a constant, measured during implementation).
3. Confirmation e-mails of a batch are **suppressed**; one e-mail for the group follows later (not in these PRs).
4. `guests.is_placeholder` and `booking_groups.contact_guest_id` (00073). Placeholders are **left out of guest search**, shown **muted with a badge**, the name **cut to 100 characters**.
5. A bulk booking is **confirmed by default**; a draft can be chosen.
6. **Cancelling a whole group is a separate, small PR later**, not now.
7. `folio.transfer_any` is **built and given to no built-in role**. `folio.master_manage` (open the master, set the routing) and `folio.master_close` (close it) are new permissions.
8. A master **without a company is allowed**, the invoice goes to the group contact; **the route of its faktur pajak is to be confirmed with the tax consultant** (marked in 5.4).
9. A master still open after every member has checked out is a **warning** in the night audit, not a blocker.
10. **PRs: (1) A, (2) B, (3) C, (4) E**, each on its own branch from `main`.
11. Rooming list CSV columns: **name, e-mail, phone** (identity number later).
12. Locks taken at the start of a batch plus a "locks held" marker (the `CreateHeld` pattern); group check-in is **one transaction per row**.
13. **Changed by the owner (part B):** the balance transfer is an ordinary transaction with the system charge code `TRF` (4.3), not a new row type; the faktur pajak stays with the source folio and the dialog warns; every transfer keeps its first origin and its previous folio; the source folio lists what left it, with Return; the target says where each line came from; Return is always approved, per line or in bulk, to the previous folio or the first origin; a closed folio is never a source or a target (4.5).

Noted, not blocking: the "first origin" has no meaning for a fungible balance (4.5).

## 10. Tests

**PR 1 (A)**
- The batch creates exactly one reservation per room, with the booker, the placeholder guest, the company and the group; each audit entry exists, plus one `group.rooms_added`.
- **All or nothing when the rooms run out in the middle:** a three-row batch where row 2 is short by one room on one night: the request fails with the row and the room, and **no** reservation, no guest, no document number consumed beyond the rollback, no audit entry, no confirmation e-mail exist afterwards (counts before and after). The same with a failure in the last room and in a row that violates `OUTSIDE_GROUP_DATES`.
- **Race:** two batches that together want more rooms than exist (one wins, one fails whole); a batch against a single walk-in on the last room; two requests with the same key (one creates, the other replays the same ids).
- **Idempotency:** same key and body returns the same reservations and creates none; same key with another body is `IDEMPOTENCY_KEY_REUSED`; a lost answer repeated.
- **Locks:** a test that runs a batch of ten reservations through the lock-order check (the loop must not raise `ErrLockOrder`), with the `onConfirmed` hook installed; the batch never takes a level lower than the level it is at.
- **Override:** a row at a different rate needs one reason and one approval for the whole request; without the permission 403; without the approval `APPROVAL_REQUIRED`; one approver on every audit entry; a row at the grid price needs none.
- Limits (`TOO_MANY_ROOMS`, `TOO_MANY_ROWS`), the preview (matrix cumulative per type and night, the short row marked, no row written, no lock).
- Rooming list: edit in place, linking an existing guest with a stale version, the CSV dry run writes nothing and reports **every** wrong row, the import is all or nothing, a row of another group is an error.
- Placeholders are out of guest search; naming one clears the flag. Migration up/down/up; tenant isolation of the new routes; the contract test; permissions.
- Web: the form (defaults from the group dates, validation, the preview, the short row), the rooming list, the import dialog, the typo fix; i18n parity.

**PR 2 (B)**
- **`TRF`:** a transfer posts an adjustment of -X and a charge of +X of the code `TRF` with **zero tax and service components** (the same engine; mapping a tax or a service charge to `TRF` is refused); the code cannot be posted by hand, edited, deactivated or deleted; the item snapshots the account of the key `FOLIO_TRANSFER`; an unmapped key is `TRANSFER_ACCOUNT_NOT_MAPPED`.
- **Account 1215:** the migration on a chart with no 1215, with a taken 1215 and 1216, with no chart, run twice, and Down refused with a `TRF` row (the pattern of the 00071 tests); a property created after the migration gets the account, the key and the code from the seed, in Indonesian; the balance of 1215 is zero after every day close, over transfers in both directions on several days with returns; `TRANSFER_ACCOUNT_NOT_ZERO` appears when a manual journal breaks it.
- **Not revenue:** a transfer changes no revenue report, no revenue summary of the night audit and no tax invoice figure.
- **Trail and return:** a chain A to B to C keeps `origin = A` and `previous = B`; a balance pair records its own source; the source folio lists what left it with the folio it is at now; the incoming line names its origin; the normal view hides the transfer reversal pairs and the balance stays right; return per line and in bulk, to the previous folio and to the first origin, always with an approval, once (two concurrent returns of one transfer: one succeeds, the other `ALREADY_RETURNED`); the audit entry shows from both folios; **a closed folio as source, target, holder or return target is `FOLIO_CLOSED` with the folio named**, and nothing moves.
- Bulk transfer moves exactly the selected items; by code moves every movable charge of the code and skips reversed pairs; an empty or foreign selection fails whole and writes nothing.
- **Net zero per account and per department:** `DayActivity` before and after has the same totals per account and department, for EXCLUSIVE and INCLUSIVE items, with components equal to the cent; room nights keep their register slot (a night audit afterwards posts nothing twice).
- **Race:** two bulk transfers of overlapping items (one wins, the other `ALREADY_REVERSED`, nothing half written); A→B and B→A at once (no deadlock: ascending locks); a bulk transfer against a posting run; two balance transfers of 600 from a balance of 1000 (one succeeds, one `TRANSFER_EXCEEDS_BALANCE`); a repeated `Idempotency-Key`.
- **Control accounts:** the reconciliation difference is unchanged by a balance transfer, with and without deposits on the folios, before and after the day close and `DEPOSIT_RELEASE`.
- The invoices of source and target ("Transfer ke / dari" lines, balance due), the tax invoice of the source unchanged, the dialog warning about the faktur.
- The scope matrix (`RESERVATION`, `GROUP_MEMBER`, `ANY` with and without the permission on actor and on approver, closed folio, other property, the unlinked folio); the approval verified once and recorded on every entry; `folio.transfer_any` is held by no built-in role.
- Migration up/down/up (Down refuses with data), the business-day trigger on the batch, the append-only triggers on the log, the constraint mappings.
- Web: selection, the filter by code, both dialog tabs, the scope badge, the approval, the remote `Combobox`.

**PR 3 (C)**
- **Role-based `is_deposit`:** old view and new view agree row by row on the fixture of existing cases; a master payment before the first charge is ADVANCE_DEPOSITS, after it GUEST_LEDGER; journal and reconciliation agree through the close of the master and `DEPOSIT_RELEASE`.
- **Night audit with routing to the master:** rooms of three members post to the master, their folios stay empty, the register is keyed by the stay so a second run posts nothing, the journal has one set of entries; an excluded member posts to its own folio; a line (company) instruction wins over the group rule; the master closed gives `ROUTING_TARGET_CLOSED` and **nothing is posted** (the whole run rolls back).
- **Race:** a routing change against a posting run (they take turns; nights posted before the change stay where they were); two closes of the master; a payment on the master against its close.
- **Check-out of a member with the master open:** the guest folio must be zero, the master stays open, the result carries the master info; the last member out does not close it; closing is refused with a member in house, a member still to arrive, a balance, or a night to post, and allowed after payment, after a city ledger transfer to the group's company (mismatch and credit limit gate) or after a refund.

**PR 4 (E)**
- **Group check-in, partial and repeated:** with five rows, one room not ready, one occupied, one without a guest: three check in and two are reported with their code, nothing is rolled back; repeating the request returns the same stays for the three (no new stay, no new stay number consumed) and tries the two again; a room made ready meanwhile then checks in; the not-ready override with and without the permission; two staff at once check each room in once.
- Deposits: no per-room deposit when the group routes everything to the master, and one for an excluded member; the deposit goes through and the check-in fails (the repeat retries the check-in only); a group deposit on the master and its classification.
- Invoice and faktur of the master (the company, else the contact); the VAT lines are the nights routed to it; migration up/down/up (Down refuses with a master); tenant isolation; the contract test; permissions.
- Web: the master panel, the routing editor, the check-in screen (readiness badges, identity warning, auto-assign on the same screen, partial result rows, the group payment section), the reservation and folio notice; phone width; i18n parity.

Every PR finishes with: build, vet, lint (0 issues), `go test ./...`, `scripts/db-test.sh` (PRs with a migration), `scripts/sqlc.sh diff`, `npm run gen:api` when the OpenAPI changed, `npm test`, type-check, build, and the README status and this document updated (`18` 3.13, 3.5 and `08-backlog.md` point here when part C is built).

## 11. Not in this design

Allotment; a cancel of the whole group (decision 5); a percentage split of a charge; a payment moved between folios; routing by date range; an exclusion per scope; several masters per group; moving a credit balance; a master for a reservation that is in no group; sending the group confirmation e-mail (decision 2, later); the guest merge of placeholders.
