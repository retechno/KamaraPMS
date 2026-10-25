# 36. Banquet: meeting rooms, events and sessions, packages, the event folio, the BEO and the deduction of stock

Status: **APPROVED by the owner (2026-10-24), the whole document: the decisions B1 to B16 (section 13) and the answers to the questions Q1 to Q15 (section 14).** Q2 and Q6 were decided differently from the recommendation (the folio of a Tentative, 4.3, 4.4, 4.7; the base of the cancellation percent, 4.7), and 4.3 gained the rule for moving a Tentative session. **Nothing is built.** The findings of section 1 and their proposals are approved as section 14 says; a **(Q)** in the text names the question that decided it.

Read with: `03-financial-engines.md` (one engine, one posting path), `05-transactions-locking.md` (lock order), `12-departments.md`, `18-architecture-decisions.md` 3 (folio model), `21-cancellation-and-no-show-fee.md` (fees posted by hand), `26-subscriptions.md` (module gate), `30-group-folio.md` (master folio, `TRF`, transfer scopes, routing), `31-deposit-release.md` (2310 and its release), `32-distinct-approver.md` (kinds of approval), `33-setup-read-permissions.md`, `34-pos.md` (outlet folio, rule profiles, the queue hook), `35-inventory.md` (the stock ledger, the queue, 12 the F&B reports) and `internal/chargecalc`, `internal/billingconfig`.

## 0. The idea in one page

- **An event is not a reservation.** It has its own header, sessions and lines (`banquet_*`), and its own folio. Rooms of the hotel (`rooms`, level 30) are not touched: the meeting rooms are a master of their own (`banquet_rooms`). The hotel can run Banquet without POS and without Inventory (B5), as the owner asked.
- **A session is the unit of time, space and billing**: a date, a start and an end (`civil.Date`, `civil.TimeOfDay`), a meeting room, a set-up and the number of guests. An event has one or more sessions, on one or more days (B2).
- **The calendar says what is held, the database guards it** (B3): an Inquiry holds nothing, a Tentative holds with a queue of options, a Definite holds exclusively. The exclusion is a constraint over **hold rows** that cover each physical room and the set-up and tear-down time, so a combined room (Ballroom = A + B) is excluded by the same constraint as its parts (section 4.2).
- **All the money goes through the paths that exist** (B8): one folio of the new type `EVENT` for each event, charges by `FolioPostingService` with the tax and the service charge of `ChargeCalculationEngine` only, payments by `PaymentService`, the deposit in 2310 and its release (31), the transfer to a company (city ledger) and the invoice PDF. No second ledger and no parallel posting path.
- **A price has a mode and keeps it** (B6): each line is NETT (INCLUSIVE) or ++ (EXCLUSIVE) and is frozen on the line when it is added. A package per guest is split into its components, each with the revenue account of its charge code, and the rounding difference goes to the largest component, so the total is exactly the price of the package times the guests.
- **Billing is by session, once** (B9): the night audit posts the sessions of the day (setting AUTO), or the banquet office presses "Post session" (setting MANUAL, per property and per event). A session can be posted only once (a unique key of the database); a correction is an adjustment.
- **The BEO is a document with revisions** (B11): after Definite every change makes a revision, and the departments see "changed since revision X" and are told.
- **Stock follows the posting** (B14): a banquet line linked to a POS product with a recipe writes a row of the same queue as a POS sale (billed guests x recipe). F&B reports of document 35 12 are widened to read it (section 4.9).
- Four PRs, each with DB, backend, API, frontend, i18n and tests together: 1 masters and schedule (no money), 2 money, 3 documents, 4 stock and reports (section 11).

## 1. Findings and conflicts (read this first)

Each finding is a conflict between a decision of the owner and the code or a document in force, with what the design does about it. All are decided by the answers of section 14.

| # | Finding | Proposal |
|---|---|---|
| F1 | **B16 asks for a new lock level "after groups"; there is no free number.** `db/locks.go`: groups 45, **stock 45 (shared with groups)**, accounting 46, payables 47, bank 48, tax and budget 49, sequences 50. A level between 45 and 46 does not exist as an integer. | **(Q1, approved):** the events join level 45 with an **inner order**, as the stock tables already do (`stockRank`): **groups, then the meeting rooms (`banquet_rooms`), then the events, ascending id**. It is "after groups" in the order that matters and needs no renumbering, and no other document changes. Alternative: renumber accounting to sequences by +1 (`LevelEvents = 46`); it touches `db/locks.go`, the tests of the order and every document that quotes a number (05, 30, 31, 35). Section 6 states the order of every use case; the structural test of `35-inventory.md` 5.1 learns that **no use case may reach both a stock lock and an event lock** (banquet only inserts a queue row, as POS does, so it never does). |
| F2 | **`folios` cannot hold an event bill.** `folio_type` is GUEST, COMPANY, MASTER, OUTLET (`folios_type_ck`, 00081), `reservation_id` is NOT NULL except for MASTER and OUTLET (`folios_reservation_ck`). | Migration of PR 2 adds `EVENT` with `banquet_event_id` (FK), as 00075 did for MASTER and 00081 for OUTLET: an EVENT has no stay, no reservation, no group and no outlet; it may carry the company that pays (`bill_to_company_id`, as a COMPANY folio does); at most one OPEN folio for an event. |
| F3 | **`is_deposit` would misclassify the payments of an EVENT folio** (a folio with no stay: every payment a deposit until it closes). It is the problem 30 5.2 solved for MASTER and 34 F5 for OUTLET. | The view `folio_item_gl` gets the branch of MASTER and OUTLET for EVENT: a payment is a deposit when it was taken before the first live CHARGE row of the folio, a settlement after that. The row-by-row comparison test against the old view (as in 00075 and 00081) is part of PR 2. With the release of 31 in EVERY_DAY, the deposit of an event stays in 2310 until a session is posted, which is the wanted behaviour: `T = min(H, U)`. |
| F4 | **`folio_items.source`** has a closed set (POS was added in 00081) and **the structural guard of the sale paths** (`salepath_guard_test.go`) lists the paths that may post. | A new `source = 'BANQUET'`; the guard learns the banquet path, which calls `PostCharge` and `PostAdjustment` and nothing else. `ChargeRequest.PriceMode` already exists (`billingconfig/calculation.go`), so a line passes its frozen mode and the engine needs **no change**. |
| F5 | **Document 21: fees are manual, "no cancellation-policy engine".** B10 asks for a fee by the number of days before the event. | Document 21 is about the fee of a **reservation** and stays as it is. For an event the policy is a table of tiers snapshotted on the event, and the fee is **computed and shown to the person, who confirms it**; it is posted through the same path (`CANCEL_FEE`, account 4510) by a banquet use case. Nothing is posted by the system on its own (4.7). A waiver is the reversal that exists, with its approval. |
| F6 | **Account 4520 "Forfeited deposits" has no code that uses it** (chart only). | A forfeiture is a **charge** with its own charge code (`BQT_FORFEIT`, account 4520) that the deposit pays by the release of 31, exactly as a fee is netted today (21 "The folio"). No new mechanism. |
| F7 | **The chart has 2330 "Unearned revenue (groups and events)"**, and B8 says the deposit is in 2310. | **(Q12)** B8 is followed: 2310. 2330 stays unused (it is not mapped to any key). If the owner wants events apart from guest deposits in the balance sheet, it is a new map key and one line in the journal of 31, not a different mechanism. |
| F8 | **No notification of a user exists.** `email_outbox` names a reservation (`reservation_id NOT NULL`, `kind CHECK IN ('RESERVATION_CONFIRMATION')`, 00021); the only table of notices is `inv_notices` (inventory, with its own columns); `35-inventory.md` F19 and D27 reached the same wall. B3 (the sales are told that an option moved up) and B11 (the departments are told of a revision) need it. | **(Q4)** PR 1 adds `banquet_notices` (the shape of `inv_notices`: user, kind, event, session, created, seen; read in the bell and the inbox, refreshed on focus and every 60 seconds). PR 3 frees `email_outbox` from its reservation (a nullable `reservation_id`, a `banquet_event_id`, the kinds `BANQUET_PROPOSAL` and `BANQUET_CONTRACT`) for B12, with its CHECK widened by a migration, and nothing else changes in the e-mail path. A generic notice table for every module is a later decision and not needed here. |
| F9 | **There is no scheduler.** "A Tentative past its option date is marked and reminded, not cancelled" (B3) and the reminders of the deposit (B10) need something that runs by date. | They are **derived on read** (the list and the calendar mark the event: `option_until < business date`) and a **notice is written by the day close**, once for each event and date (a unique key), by the business date, as everything else of the hotel. Nothing runs by the wall clock. |
| F10 | **Time.** B2 asks for `civil.Date` and `civil.TimeOfDay`; an exclusion constraint needs a range. | The range is a `tsrange` (a timestamp **without** a zone) of the **local** date and time of the property, never an instant: the hotel's dates are the hotel's. A session that ends after midnight (a wedding to 01:00) is allowed: **(Q7)** `ends_next_day boolean`, with `start_time` different from `end_time`; the range adds a day. A multi-day event is several sessions (B2). |
| F11 | **Recipes belong to POS.** `pos_recipe_lines` hangs on a POS product; the recipe editor is a POS route (module `POS`). B5 asks that a banquet F&B item **may** be linked to a POS product to use its recipe, and that Banquet runs without POS or Inventory. | **(Q11)** The link is optional and the recipe is **read** through it. A tenant with Banquet and Inventory but without POS has no recipe to link (no way to edit one), so its banquet lines deduct nothing; this is a limit of the plan and is stated on the item screen. A second recipe table for banquet is not proposed (it would be a second place for the same fact). |
| F12 | **The stock queue is tied to a POS line.** `inv_consumption_queue.pos_bill_line_id NOT NULL`, `outlet_id NOT NULL` (00087), the sale class and the warehouse come from the outlet. | PR 4 widens the queue: `source` (`POS`, `BANQUET`), `pos_bill_line_id` and `outlet_id` nullable, a `banquet_posting_line_id`, a CHECK that exactly one source is set, a unique key `(banquet_posting_line_id, kind)` for the idempotency of the hook. The warehouse is a **setting** of Banquet (`banquet_settings.warehouse_id`). Section 4.9. |
| F13 | **Document 35 12.3: "Only POS revenue is read. Banquet or room-service revenue posted straight to a folio is outside these reports until POS covers it."** B14 asks to explain the effect. | Section 4.9: the revenue of a banquet F&B line is added to `inv_fb_cost_days` under the **outlet "Banquet"** (a source, not a POS outlet), by the class of the item, so the F&B cost % of a month in which a hotel serves a wedding is no longer cost without revenue. This changes 12.1, 12.2 and 12.4 of document 35 and their tests; it is written in PR 4, not before. |
| F14 | **Document 32: an approval is always asked by the use case; the rules say only who may approve.** B7 wants a discount above a limit to need the approval, and one below it to need none. | The same shape as POS 7.3: a setting **`max_discount_pct_no_approval`** (default 0: always an approval) and a new kind **`BANQUET_DISCOUNT`** (amount: the base discounted, `abs`, added over the lines of the request; approver's permission `banquet.confirm`), which takes `FREE`, `DISTINCT` and `DISTINCT_ABOVE` like any other kind. The structural test of 32 4 is extended by the package. |
| F15 | **B13: charges stay on their folios and move by the transfer that exists.** The scopes of 30 4.2 know nothing of an event. | A new scope **`EVENT_GROUP`** of `folio_transfer_batches`: one folio is the EVENT folio of an event linked to a group, the other is the MASTER of that group or the folio of a member's reservation. Permission `folio.reverse` and the approval of today, **not** `folio.transfer_any`. Without a link the pair is `ANY`. |
| F16 | **The comment of `internal/subscription/model.go` is wrong.** `Banquet Module = "BANQUET" // the outlets (docs/architecture/34-pos.md)` says "the outlets", which are the module `POS`. | Written as asked in B16, **in the code of PR 1** (this PR has no code): "meeting rooms, events and banquets (docs/architecture/36-banquet.md)". The table of 26 section 3 gets the routes of Banquet in the same PR. |
| F17 | **The night audit.** B9 (AUTO) posts the sessions of the day at the close; the stock queue is drained and the journal made in the same transaction (`35-inventory.md` 5.7). | The banquet step goes **after the room charges and before `ExpireOutletLocks`**, so that the queue rows it writes are drained by the stock steps and the lines are in the journal of the same day. A MANUAL property gets a **warning**, not a blocker, for the sessions of the day that are not posted (B9). Locks in 6. |
| F18 | **Tax on the cancellation fee and the faktur pajak of an event.** The fee of 21 has the rules mapped to its charge code (none by default); the invoice of an event bill to a company needs a buyer. | **(Q5)** As in 30 5.4 and 34 F14: the design does not invent a rule. The event invoice is the invoice of the folio (`documents.Invoice` gets an EVENT branch, the party is the company or the booker); the tax invoice of an event to a person without a tax number waits for the tax consultant (konsultan pajak). |

## 2. Vocabulary

| Term | Meaning |
|---|---|
| Meeting room | A space that can be booked: `banquet_rooms` (not a hotel room). A **combined room** (Ballroom) is made of **base rooms** (A, B); a base room has its own row. |
| Set-up | The arrangement of a room: theater, classroom, U-shape, round table, cocktail, boardroom. Each room has a capacity for each set-up it offers. |
| Event | The booking of a customer: a header (booker, contact, sales, type), its sessions, its lines, its folio. Number `BQ-000123`. |
| Session | One date, start, end, room, set-up and guests of an event. The unit of the calendar, of the BEO and of billing. |
| Hold | What a session takes from the calendar: one row for each base room it covers and the time from the start of the preparation to the end of the tear-down. `OPTION` (Tentative) or `DEFINITE`. |
| Option | A Tentative that holds, in line: the first option of a room and time is option 1, the next ones 2, 3, ... |
| Banquet item | A thing that is sold in an event (a menu per guest, a drink package, the rental of a room, equipment, decoration, other). Has a charge code. |
| Package | A price per guest for a list of items. When it is sold it is **split** into its components. |
| Price mode | **NETT**: the price contains service and tax (engine mode `INCLUSIVE`). **++**: the price is before them (`EXCLUSIVE`). |
| Guests: estimated, guaranteed, actual | The three numbers of a session. **Billed guests = max(guaranteed, actual)**; when the actual is not yet entered, the guaranteed. |
| BEO | Banquet Event Order: the order of a session for the departments (set-up, run of the day, menu, guests, equipment, notes by department). Has revisions. |
| Event folio | The folio of type `EVENT`, one for each event. |

## 3. Data model

Migrations start at **00089** (the next free number; they move if another PR takes one first). Money columns are `numeric(18,3)`; times of day are `time` columns read as `civil.TimeOfDay`; every table is scoped by `tenant_id`/`property_id` with composite foreign keys, and each uses the usual `(property_id, id)` unique key for its children. Rows are never deleted after they are used: a state or an active flag says what happened.

### 3.1 Changes to existing tables

| Table | Change | PR |
|---|---|---|
| `folios` | `folio_type` accepts `EVENT`; `banquet_event_id`; `folios_event_ck` ((type = 'EVENT') = (banquet_event_id IS NOT NULL)); an EVENT has no stay, reservation, group or outlet; `reservation_id` may be null for EVENT too; partial unique index: one OPEN folio per event | 2 |
| `folio_item_gl` (view) | `is_deposit` by role for EVENT as for MASTER and OUTLET (F3) | 2 |
| `folio_items` | `source` accepts `BANQUET` | 2 |
| `folio_transfer_batches` | `scope` accepts `EVENT_GROUP` (30 4.2, F15) | 2 |
| `email_outbox` | `reservation_id` nullable, `banquet_event_id`, `document_id`, the new kinds (F8) | 3 |
| `inv_consumption_queue` | `source`, nullable POS columns, `banquet_posting_line_id`, the CHECK of one source (F12) | 4 |
| `inv_fb_cost_days` and the cost tables | a `source` of revenue and cost: `POS` or `BANQUET` (4.9) | 4 |
| charge codes, accounts | **data, not schema**: the seeded charge codes `BQT_FB`, `BQT_ROOM`, `BQT_EQUIPMENT`, `BQT_DECOR`, `BQT_OTHER`, `BQT_FORFEIT` (accounts 4240, 4410, 4410, 4590, 4590, 4520 as the property can change them) are made by the seed of charge codes for new properties and by the migration for existing ones, as `TRF` was (30 4.3) | 2 |

No existing constraint is edited in place: each is replaced by a new one in the migration that needs it. No rejected decision of `CLAUDE.md` comes back (`rooms.status`, `taxes.is_inclusive`, `payments.currency_code`, `properties.business_date`, reservation-header dates).

### 3.2 PR 1 (migration 00089): masters and schedule, no money

```
banquet_settings          (property_id PK, tentative_conflict_mode CHECK IN (REJECT, QUEUE) DEFAULT REJECT, operating_from time, operating_to time,
                           updated_at, updated_by)               -- no row = the defaults; every change is audited (B3: any time, for new bookings)
banquet_rooms             (id, tenant_id, property_id, code UK, name, area_m2 numeric(8,1) NULL, setup_minutes int ≥ 0, teardown_minutes int ≥ 0,
                           is_combo bool, is_active, sort_order, version)                     -- B1; UK (property_id, id)
banquet_room_parts        (property_id, combo_room_id, part_room_id, PK both)                 -- the combo is_combo, the part is not (a trigger: one level only), combo ≠ part
banquet_room_setups       (property_id, room_id, setup_style CHECK IN (THEATER, CLASSROOM, U_SHAPE, ROUND_TABLE, COCKTAIL, BOARDROOM), capacity int > 0, PK (room_id, setup_style))
banquet_event_types       (id, property_id, code UK, name, is_active)                         -- wedding, meeting, gala dinner...
banquet_reasons           (id, property_id, kind CHECK IN (LOST, CANCEL), code UK, name, is_active)   -- the reason of a Lost or a Cancelled is required (B3)
banquet_items             (id, property_id, code UK, name, kind CHECK IN (MENU, DRINK_PACKAGE, ROOM_RENTAL, EQUIPMENT, DECOR, OTHER),
                           unit_basis CHECK IN (PER_PAX, PER_SESSION, PER_HOUR, PER_UNIT), charge_code_id, list_price numeric(18,3) ≥ 0,
                           fb_class NULL CHECK IN (FOOD, BEVERAGE, OTHER), pos_product_id NULL, description, is_active)   -- B5, F11
banquet_packages          (id, property_id, code UK, name, price_per_pax numeric(18,3) ≥ 0, description, is_active)       -- B6
banquet_package_components(package_id, item_id, qty_per_pax numeric(10,3) > 0, sort_order, PK (package_id, item_id))      -- components are items per guest
banquet_events            (id, tenant_id, property_id, number UK, status CHECK IN (INQUIRY, TENTATIVE, DEFINITE, COMPLETED, CANCELLED, LOST), name,
                           event_type_id, booker_guest_id NULL, booker_company_id NULL (exactly one), contact_name, contact_phone, contact_email,
                           sales_user_id, option_until date NULL, reason_id NULL, reason_note, booking_group_id NULL, notes,
                           inquiry_at, definite_at, closed_at, version, created_by, created_at)
banquet_sessions          (id, tenant_id, property_id, event_id, session_no, name, session_date date, start_time time, end_time time, ends_next_day bool,
                           room_id, setup_style, pax_estimated int > 0, pax_guaranteed int NULL, pax_actual int NULL, status CHECK IN (ACTIVE, CANCELLED), notes)
banquet_holds             (id, property_id, session_id, event_id, atom_room_id, during tsrange NOT NULL, hold_kind CHECK IN (OPTION, DEFINITE), created_at,
                           EXCLUDE USING gist (atom_room_id WITH =, during WITH &&) WHERE (hold_kind = 'DEFINITE'))        -- B3, 4.2
banquet_notices           (id, property_id, user_id, kind CHECK IN (OPTION_PROMOTED, OPTION_DUE, OPTION_LOST), event_id, session_id NULL, created_at, seen_at NULL)
                          -- unique (event_id, kind, user_id, local day) so the day close writes a reminder once (F9); the bell is the user's own
```

- **CHECKs that tie a state to its columns:** `TENTATIVE` has `option_until`; `CANCELLED` and `LOST` have `reason_id`; `DEFINITE` has `definite_at`; exactly one of the two bookers is set; an `ends_next_day` session has `start_time <> end_time`; a session with `ends_next_day = false` has `end_time > start_time`; `pax_guaranteed` and `pax_actual` are null or positive; the capacity of a session is **checked by the service** against `banquet_room_setups` and answered as a warning the person confirms (a wedding of 300 in a room for 250 is a business decision, not an error); the set-up must be one the room offers.
- **The guard behind the service.** A deferred constraint trigger refuses a **DEFINITE event with an active session that has no hold rows for every base room it covers** (`BANQUET_HOLDS_MISSING`), so a bug that forgets to write the holds cannot leave a Definite session unprotected; the exclusion does the rest. Holds exist only for TENTATIVE (kind `OPTION`) and DEFINITE events; an Inquiry holds nothing. They are deleted when a session is cancelled or the event is Cancelled or Lost, and kept when it is Completed.
- **Calendar, derived data.** The option rank and "promoted" are **not stored**: they are computed from the holds in the order of creation (4.3).
- **Event and folio numbers.** The event number uses a `document_sequences` row `BANQUET_EVENT` of the property (level 50, the last lock); the folio number is the usual one.
- Constraint names are mapped in `internal/platform/db/errors.go` (`banquet_holds_no_overlap_ex` -> 409 `BANQUET_ROOM_NOT_AVAILABLE`, `banquet_holds_missing` -> 500 programming error, the others to their codes) and a test verifies that every name exists.

### 3.3 PR 2 (migration 00090): money

```
banquet_settings   + posting_mode CHECK IN (AUTO, MANUAL) DEFAULT AUTO, default_price_mode CHECK IN (EXCLUSIVE, INCLUSIVE) DEFAULT EXCLUSIVE,
                     max_discount_pct_no_approval numeric(5,2) DEFAULT 0                  -- B6, B7, B9
banquet_events     + posting_mode NULL (the override of the property, B9), policy_base CHECK IN (NET, GROSS) NULL (copied from the template at Definite), contract_value_estimate numeric(18,3) (derived and kept for the list),
                     cancelled_fee_item_id NULL
banquet_event_lines(id, tenant_id, property_id, event_id, session_id, line_no, item_id NULL, package_id NULL (exactly one), description,
                    basis, quantity numeric(10,3) > 0, unit_price numeric(18,3), price_mode CHECK IN (EXCLUSIVE, INCLUSIVE),   -- frozen when added (B6)
                    discount_pct NULL, discount_amount NULL, charge_code_id (snapshot), fb_class, pos_product_id NULL (snapshot), is_active)
banquet_line_components(line_id, item_id, charge_code_id, qty_per_pax, list_price, sort_order)   -- the snapshot of a package, so a later edit of the master changes nothing
banquet_session_postings(id, tenant_id, property_id, session_id UNIQUE, folio_id, business_date, pax_billed, pax_basis CHECK IN (ACTUAL, GUARANTEED),
                    auto bool, on_guarantee bool, posted_by, posted_at)                   -- B9: a session is posted once, by the database
banquet_posting_items(posting_id, line_id, component_id NULL, folio_item_id UNIQUE)       -- what each folio item came from
banquet_deposit_schedule(event_id, seq, label, due_date, percent numeric(5,2) NULL, amount numeric(18,3), PK (event_id, seq))      -- B10
banquet_policy_templates(id, property_id, code UK, name, is_default, base CHECK IN (NET, GROSS) DEFAULT NET)   -- a starting point, copied to the event with its tiers at Definite (B10 is per event; Q6)
banquet_policy_tiers(template_id NULL, event_id NULL (exactly one), days_from int, days_to int NULL, percent numeric(5,2), action CHECK IN (FEE, FORFEIT))
```

- **The trigger of the business day.** `banquet_session_postings` is a table of posting rows dated by the business day, so it takes `business_day_must_be_open` like the other posting tables (CLAUDE.md, 00065); the folio items it makes have it already.
- **Idempotency.** The folio items of a session carry the key `banquet:session:<session_id>:<line>:<component>`, so `folio_items_idempotency_uk` is a second guard (21 "Charged once").
- **Folio of the event.** Opened **automatically by the transition to DEFINITE** (4.4) when the event has none, and **earlier, on a Tentative, by the action "Accept deposit"** (4.3, **Q2**), always with a number from the sequence in a transaction of its own (6); it takes the `bill_to_company_id` of the event when the booker is a company. An event has at most one folio, whenever it was opened.

### 3.4 PR 3 (migration 00091): documents

```
banquet_beo_sections   (session_id, section CHECK IN (KITCHEN, FB_SERVICE, HOUSEKEEPING, ENGINEERING, SECURITY, FRONT_OFFICE, GENERAL), body text, updated_at, updated_by, PK both)
banquet_run_of_show    (id, session_id, seq, at_time time, text)                          -- the order of the programme
banquet_beo_revisions  (id, event_id, revision_no, reason, snapshot jsonb, created_by, created_at, UNIQUE (event_id, revision_no))   -- B11
banquet_beo_ack        (event_id, section, revision_no, user_id, at)                      -- who saw what; "changed since revision X"
banquet_beo_recipients (property_id, section, department_id)                              -- the department whose head and deputy are told (Q8)
banquet_documents      (id, event_id, kind CHECK IN (PROPOSAL, CONTRACT, BEO), number UK, revision_no NULL, language, created_by, created_at, sent_at NULL, email_id NULL)
banquet_notices        kinds widened: + BEO_REVISED, DEPOSIT_DUE, FEE_PROPOSED
email_outbox           the change of F8
```

The PDF is rendered on request by `internal/documents` from the data of the moment; `banquet_documents` keeps the number, the revision and the e-mail that sent it (the document of a revision can be rendered again from its snapshot, 4.6).

### 3.5 PR 4 (migration 00092): stock and reports

`banquet_settings.warehouse_id` (FK `inv_warehouses`), the change of `inv_consumption_queue` (F12), the `source` of the cost tables (F13), and the indexes of the reports. No snapshot table is added: the reports of 4.10 read the sessions, the postings and the holds live.

## 4. Flows

### 4.1 The meeting rooms and combined rooms (B1)

A combined room is a room whose `is_combo` is true with two or more **base** rooms (`banquet_room_parts`), one level only (the trigger refuses a combo that is a part). A base room may be in more than one combo (A + B = Ballroom, B + C = Garden). The **buffers** (`setup_minutes`, `teardown_minutes`) are the ones of the room that is booked: a session in the Ballroom uses the Ballroom's buffers on both A and B. A room may be deactivated, never deleted once a session used it.

### 4.2 The time of a session and its hold (B2, B3, F10)

- The range of a session is `[session_date + start_time - setup_minutes, session_date + end_time (+ 1 day if ends_next_day) + teardown_minutes)` as a local `tsrange`. One **hold row** for each **base** room covered: a booking of a base room has one, of the Ballroom has two (A and B).
- The constraint `EXCLUDE USING gist (atom_room_id WITH =, during WITH &&) WHERE (hold_kind = 'DEFINITE')` therefore refuses, in one rule, a second Definite on the same room, a Definite on a room whose combo is already Definite, and a Definite on a combo when a part is, **with the preparation and tear-down time included**. `23P01` is mapped to 409 `BANQUET_ROOM_NOT_AVAILABLE` (as `ROOM_NOT_AVAILABLE` is for the rooms).
- Two sessions of the **same event** may not overlap in a room either (the same constraint; an event that needs two rooms at the same time has two sessions).
- Option holds (`OPTION`) are not under the constraint (4.3).

### 4.3 The statuses and the queue of options (B3)

```
INQUIRY ──> TENTATIVE (option_until) ──> DEFINITE ──> COMPLETED
   │              │                         │
   └──────────────┴────────> LOST / CANCELLED (a reason is required)
```

- **Inquiry** holds nothing and may have no room yet. **Tentative** needs `option_until` and a room, a date and a time for every active session, and writes `OPTION` holds. **Definite** writes `DEFINITE` holds (the same rows change kind), uses the folio of the event or opens it (PR 2, 4.4), and is protected by the constraint. **Completed** is set by hand ("Complete the event", 4.5), **Lost** and **Cancelled** delete the holds.
- **The setting** `tentative_conflict_mode` is read when a Tentative is created **or moved**:
  - **REJECT**: a session whose hold overlaps another live hold (`OPTION` or `DEFINITE`) of any base room is refused, 409 `BANQUET_ROOM_NOT_AVAILABLE`, with the event in the context.
  - **QUEUE**: it is accepted as **option 2, 3, ...** of those holds. The **rank** of a session is derived: the number of live holds of earlier creation (by `banquet_holds.id`) that overlap its holds on at least one base room, plus one. It is shown in the calendar and on the event.
  - The setting may be changed at any time by `banquet.setup`; it applies to **new** bookings and moves, never to what exists, and each change writes `banquet.settings_changed` (old, new).
- **An option cannot become Definite while an earlier option still holds** the room and time (`BANQUET_OPTION_NOT_FIRST`, with the holder in the context); the service checks it under the locks of 6, and the exclusion constraint refuses the overlap with a Definite anyway.
- **When option 1 is cancelled or lost** (its holds are deleted), the sessions that now have rank 1 are **promoted**, and the `sales_user_id` of each event gets a notice `OPTION_PROMOTED` in the same transaction. The events that lose their room to a Definite of someone else get `OPTION_LOST` (their holds stay, the event is flagged "room taken", they are not cancelled).
- **Moving a Tentative session (a new time or a new room) makes new holds**: the old holds are deleted and new ones are written in the new slot, so the session goes to the **back of the queue** of that slot (its rank is by the id of the new hold) and loses the rank it had. The screen **warns the sales before saving**: the availability check (`POST .../availability`) answers with the rank the session will have in the new slot and the rank it gives up, and the dialog says "You will be option N in the new slot; option M in the old slot is released" and needs a confirmation. The old slot is released like any (the options behind it are promoted and their sales told, above). In mode REJECT the move is refused when the new slot is held. A Definite session that moves keeps no queue: it takes the exclusive hold or is refused (4.2).
- **A Tentative may take a deposit** (Q2): the action **"Accept deposit"** (`banquet.manage`, and the payment permission that exists, `payment.post`) opens the EVENT folio of the Tentative when it has none and goes to the payment dialog of that folio, so that a deposit can lock the date in the contract. It is **two transactions** (the folio, then the payment of the cashier flow), because the payment locks the folio and the payments (42, 43) which come before the event (45) in the order (6); a folio that was opened and never paid is empty and harmless. The payment is a deposit (2310, F3). A Tentative with a folio that then becomes **Lost or Cancelled** is settled by 4.7: the deposit is **refunded** (approval of kind `REFUND`) or **forfeited** (`BQT_FORFEIT`, 4520).
- **An option date in the past** is shown (list and calendar) and a notice `OPTION_DUE` is written by the day close; the event is not cancelled (F9).

### 4.4 Confirming an event (Tentative to Definite)

One transaction (`banquet.confirm`): business day (share) -> **the folio of the event, when it exists** (42) -> the base rooms of every session, ascending -> the event -> the sequence **only when the folio must be made**. It checks that the rank of every session is 1, changes the holds to `DEFINITE` (the constraint decides the race), sets `definite_at`, **uses the folio that exists (a Tentative that took a deposit has one, with the deposit already on it, 4.3) or creates it** (PR 2, **Q2**), copies the cancellation base and tiers (4.7) and the deposit schedule if the template has one, and writes `banquet.event_confirmed`. A Definite event can no longer lose its room.

### 4.5 Billing: posting a session (B9)

- **Who posts.** AUTO: the night audit, at the close of the day D, posts every session of a DEFINITE event dated D (and any earlier day that is still not posted) that is not posted. MANUAL: the banquet office presses **"Post session"** (`banquet.manage`) after entering the actual guests; any session, of any day not later than the open business day, can be posted early in both modes ("posting manual lebih awal boleh"). The setting is the property's, **overridden per event** (`posting_mode` of the event).
- **Which guests.** `pax_billed = max(pax_guaranteed, pax_actual)`. AUTO follows the owner's rule: the actual when it was entered; when it was not, the **guaranteed**, and the posting is **flagged** (`on_guarantee`, shown on the session and in the report of the not posted). The actual can still be entered afterwards; a lower number after the posting is an adjustment by a person (10.5), never a second posting. **(Q9)** A session that has **neither** a guaranteed nor an actual number is not posted by AUTO: it is listed as blocked ("no guaranteed guests") and the close warns; a person may post it on the estimated guests, the design never bills a number nobody gave.
- **Early posting.** In both modes the banquet office may post a session before the close; the session must be dated **not later than the open business day** (**Q15**: a session of a future day is not posted).
- **Business date.** Posting uses `RequireOpenBusinessDay` and the business date of the open day (the trigger behind it); a session of an earlier date that is posted late is **dated the day it is posted**, and shows in the report "not posted" (B9). The service date on the lines is that day.
- **What is posted.** For each active line of the session and each of its components, one `PostCharge` (source `BANQUET`) with `PriceMode` of the line, the charge code of the line or the component, the discount, and a description "Event name · session · item". Tax and service charge come only from `ChargeCalculationEngine` (5.2). The posting row, the posting items, the folio items, the audit entry (`banquet.session_posted`) and, when the plan has Inventory and the item is linked, the queue rows (4.9) are written in **one transaction**.
- **Once.** `banquet_session_postings.session_id` is unique. A second request for the same session finds the first: 409 `SESSION_ALREADY_POSTED` (the loser of two parallel requests gets it from the unique key), and the retry replays by the idempotency key of the items.
- **Complete.** "Complete the event" requires every active session posted (or cancelled) and the event not cancelled; it sets COMPLETED. It does not close the folio: that is the existing close, with the balance at zero.

### 4.6 Changes after Definite, the BEO and its revisions (B11)

- Any change to a Definite event (date, time, room, set-up, guests, a line, a text of the BEO) is saved as **one revision** of the BEO: `revision_no + 1` with a snapshot (`jsonb`) of the BEO as it is after the save. **A save is one revision**, however many fields it changes. Before Definite there are no revisions: the BEO is a draft.
- Each department section has an **acknowledgement**: the section shows "changed since revision X" when its last acknowledged revision is older and its text or the shared data (date, time, room, guests) changed after it. The people told are the **head and the deputy** of the department that the property mapped to the section (`banquet_beo_recipients`, Q8), by a notice `BEO_REVISED`; coalesced for a person who has not seen the last one.
- A change that moves money after the posting of a session (guests, price) is an **adjustment** on the folio (with the approval kinds of 32 that exist: `FOLIO_ADJUSTMENT`), not an edit of the posted lines. A session that is posted cannot be moved or cancelled: the correction is the adjustment.
- The proposal and the contract (B12) are rendered from the lines of the moment and sent by `email_outbox`; the contract is numbered and is **final for the revision** it carries (it can be printed again from its snapshot).

### 4.7 Deposits and cancellation (B10)

- **Schedule.** Rows of (label, due date, percent or amount): for example 30% at signature, 50% at H-30 (30 days before the first session), the rest at H-7. The base of a percent is the **estimated total** of the event (lines, with service and tax, by the guaranteed or estimated guests); a row with a percent is recalculated when the estimate changes (until it is paid, audited). A row is **paid** when the **deposits received** on the folio (`is_deposit` payments, F3) reach the **cumulative** due: no row is linked to a payment. Overdue and due soon are derived; the day close writes `DEPOSIT_DUE` to the sales.
- **Payment.** An ordinary payment on the EVENT folio (`PaymentService`, the cashier flow): before the first charge it is a deposit (2310), after it a settlement. It can be a transfer to the city ledger of the company that pays (30 5.4; the company's credit limit gate applies). On a **Tentative** the payment is taken by "Accept deposit" (4.3).
- **Policy per event.** The tiers (days before the first session -> a percent) and the **base** of the percent are chosen **by policy template** (**Q6**): **NET** (the default) is the **estimated net revenue** of the event, before service and tax; **GROSS** is the **contract total with service and tax**. The template, with its base and its tiers, is **copied to the event at Definite** (`policy_base` and the tiers of the event) and a later change of the template changes no event. The person who holds `banquet.confirm` can edit the tiers of the event; each change is audited. `action FEE` posts the charge code `CANCEL_FEE` (account 4510, as 21), `action FORFEIT` posts `BQT_FORFEIT` (account 4520) for the amount, **limited to the deposits received**. With **NET** the fee is posted in the mode of its charge code (a net amount, as 21); with **GROSS** it is posted **INCLUSIVE**, so that the customer pays exactly the amount the tier gives (when the charge code has no service or tax rule, the default, the two are the same number).
- **Cancelling a Definite event** (`banquet.confirm`): the screen shows the tier in force, the fee, the deposits held, what would be applied and what would be refunded or still owed; the person **confirms or changes the amount** (a lower amount than the policy needs the reason, and is one of the reversals of 32 if it is posted and then waived). The fee is posted through the path of 21 (a banquet use case calling `PostCharge` on the EVENT folio, once, with the key `banquet:cancel:<event>`, the second request gets 409 `FEE_ALREADY_POSTED`). The deposit pays it by the release of 31; the rest is refunded by the existing refund (an approval of kind `REFUND`) or stays on the folio. Sessions that were posted stay posted. The holds are deleted.
- **Lost or cancelled while still Tentative, with a deposit on the folio** (Q2): there are no tiers (they are copied at Definite), so the dialog shows the deposits held and the person chooses, for the whole or a part: **refund** (the existing refund, approval `REFUND`) or **forfeit** (a `BQT_FORFEIT` charge, 4520, up to the deposits received; the deposit pays it by the release of 31). The same use case and the same confirmation as above; a reason is required as for any Lost or Cancelled; the folio is then closed by the existing close when its balance is zero.
- **Tax on the fee** follows the charge code (none by default, F18).

### 4.8 The event and the room group (B13)

An event may be linked to **one booking group** (`booking_group_id`; setting it takes the group row **share**, level 45 first, 6). Nothing is posted to another folio by the link. The charges of the event stay on the EVENT folio; moving them to the MASTER folio of the group (or to a member's folio) is the **transfer that exists** with the new scope `EVENT_GROUP` (F15), item or balance, with its trail and its return (30 4.5). An event with no group link can still be moved by the scope `ANY`, which needs `folio.transfer_any` as today.

**Transfer to the master folio (the owner, 2026-10-24, with the mockups).** The bill of an event **can be transferred to the master folio of a group**, item or balance. The event page shows "Transfer to the group's master folio" / "Transfer ke folio master grup" (`docs/design/39-bq-acara-sesi.dc.html`). When the event is linked, the target is that group's MASTER folio (scope `EVENT_GROUP`). When it is not linked, the dialog lets the person choose a group of the property that has a MASTER folio; confirming **links the event to that group first and then transfers**, in one use case (business day, share -> the EVENT folio and the MASTER folio, ascending, 42 -> the group, share, then the event, 45, by the inner order of 6; the audit has both `banquet.group_linked` and the transfer), so a transfer to a master folio is always `EVENT_GROUP` and never needs `folio.transfer_any`. An event already linked to another group cannot be moved to a third group's master: unlink first.

### 4.9 Stock and the F&B reports (B14, F11 to F13)

- **When.** At the posting of a session (4.5), for each line (or component) that is linked to a POS product (`pos_product_id`) with recipe lines, the code writes **one queue row** (`source = BANQUET`), if the plan has Inventory **and** the property has a banquet warehouse (`banquet_settings.warehouse_id`); otherwise nothing, and the **gap report** (`35-inventory.md` 12.5) lists the posted banquet lines with a recipe and no queue row, as it does for POS.
- **What.** The recipe is expanded as for POS (`35-inventory.md` 5.6): the lines of the product, each quantity converted to the stock unit and multiplied by **billed guests x the quantity per guest of the line** (a menu counts once per guest; a package component counts `qty_per_pax` per guest). The components are snapshotted in the row. The **sale class** is the `fb_class` of the banquet item (the category of the product as a default). The worker is the one of POS: the same `FOR UPDATE SKIP LOCKED`, the same `POS_SALE` movements (the ledger kind is the consumption of a sale by recipe; its `outlet_id` is null and the new `source` says `BANQUET`), the same drain by the night audit, the same `FAILED` state that blocks the close.
- **Corrections.** An adjustment of a posted session **does not move stock**: the food was prepared, and the difference is found by the count. A cancelled event with sessions posted keeps its consumption. (Q3)
- **The effect on `35-inventory.md` 12 (F13).** Today the three cost reports read POS only: the revenue of the F&B % is `inv_fb_cost_days.revenue_net` from POS lines (12.3), and a banquet in the books would bring cost of sales (from the queue) with **no revenue** and distort the percentage of the month. PR 4 therefore: (a) writes the **net revenue of the F&B lines of a posted session** into `inv_fb_cost_days` under the source `BANQUET` and the class of the item (voided and adjusted lines follow the adjustments); (b) shows **"Banquet" as an outlet** in 12.4, the cost per outlet, and in the daily cost "by department" under the department of its charge codes; (c) adds the key of the cost targets (`inv_cost_targets`) for Banquet; (d) adds the "of which banquet" line to 12.1 and 12.2, so the owner can read the month with and without it; (e) extends the setup check `FB_CLASS_REVENUE_MISMATCH` to the item's class and the account of its charge code. The ledger itself needs no new kind. These are changes to document 35 and to its tests, **made in PR 4**, and listed there; PR 1 to PR 3 touch nothing of inventory.
- **Without Inventory or POS** the Banquet module works as described in all of sections 4.1 to 4.8; only this section is off.

### 4.10 Reports (B15)

All have CSV and a print view, under `banquet.read`, with the period and the filters of the other reports (this month, last month); decimals are computed in SQL or Go and money is `decimal`. The definitions:

| Report | What |
|---|---|
| **Revenue forecast** | By month of the session date: the estimated net revenue of **Definite** and of **Tentative** events (two lines, not added; Inquiry is not forecast), by event type and sales; the estimate is the lines of the active sessions (net, before service and tax) at the guaranteed or estimated guests; a toggle shows it with service and tax |
| **Revenue and occupancy by room** | Revenue **posted** by room (the sessions posted in the period) and **occupancy** = hours held by Definite sessions (the session time, not the buffers) over the hours of operation (`banquet_settings.operating_from/to`, default 07:00 to 23:00) x the days of the period |
| **Guaranteed against actual guests** | By session with both numbers: guaranteed, actual, difference and percentage; the total of the period; sessions with no actual yet |
| **Production by sales** | By `sales_user_id`: events created, Definite, Lost, the value of each, the conversion (Definite over Definite + Lost) |
| **Lost reasons** | Lost and cancelled events by reason, with count and estimated value |
| **Daily schedule** | The sessions of a date by time: event, room, set-up, guests, the contact, the status of the BEO; printable for the front office and the departments (also "Events today") |
| **Sessions not posted** | Sessions of Definite events whose date has come and are not posted, with the age in days and the reason (no guaranteed guests, manual mode) |

## 5. Money, prices and the journal

### 5.1 Price mode and the lines (B6)

Each line carries its price mode, **frozen when the line is added**; the default of a new line is `banquet_settings.default_price_mode`. An event may mix modes. The proposal and the contract print **"nett"** or **"++"** beside each price. The mode is passed to the engine in `ChargeRequest.PriceMode` and the rules (service, tax) are the ones mapped to the charge code (`ResolveRules`); the engine is not changed.

### 5.2 A package per guest is split (B6)

Let the package price per guest be `P` (in the line's mode), the guests `N`, and the components `c_i` with list weight `w_i = list_price_i x qty_per_pax_i`. The total to bill is `P x N`.

1. Each component gets `g_i = round(P x N x w_i / sum(w), decimals)` (half away from zero, at the currency decimals of the property).
2. The difference `D = P x N - sum(g_i)` is added to the **largest** component (the largest `g_i`; on a tie the first by sort order). So `sum(g_i) = P x N` exactly.
3. Each component is posted as **one charge, quantity 1, unit price `g_i`**, in the line's mode, with its own charge code; the engine computes the tax and service of its rules, and under NETT (INCLUSIVE) it guarantees that **the total of the item equals `g_i`** (`Breakdown.TotalAmount` equals the quoted gross, the rounding left over goes to the net revenue of the component and is recorded in `RoundingAdjustment`). Under ++ the `g_i` are net and the tax and service come on top.

The component list prices only decide the **shares**; they are not billed. The description says "Paket ... (100 pax) · Lunch".

### 5.3 The folio, the deposit and the journal

- One folio of type EVENT per event (F2). Charges, adjustments, reversals, payments, refunds, transfers and the invoice are the existing ones; `FolioPostingService` is the only writer of `folio_items` and `PaymentService` of `payments`.
- The journal is the day-close journal of the platform (03): debit 1210 (the guest ledger) for the charges, credit the revenue account of each charge code, 2430 (service) and 2410 (tax); a deposit is Dr cash / Cr 2310 and its release (31, EVERY_DAY) is Dr 2310 / Cr 1210 as the sessions are posted. **Nothing in Banquet writes a journal line.**
- Account by charge code: meeting room rental 4410, F&B 4240 ("Banquet and meeting F&B revenue", already in the chart), the rest by the charge code. The department of a line is the charge code's (12), so the departmental report (and the budget) show Banquet beside the other departments.

### 5.4 Discounts (B7)

A line has a discount in percent or an amount (never both). A discount up to `max_discount_pct_no_approval` of the base of the **request** needs `banquet.manage` only; above it the approval of the kind `BANQUET_DISCOUNT` (F14) is asked, and the approver is the one the rule of the property demands (32). The percent is of the base of the discounted lines of the request, and the **who gave and who approved** are on the line and in the audit entry. A discount after a session is posted is a negative adjustment with the approval of `FOLIO_ADJUSTMENT`.

## 6. The order of locks

Document 05 gets **one addition** (F1): `LevelEvents = 45`, **shared** with the booking groups and the stock, with an **inner order**: groups -> meeting rooms (`banquet_rooms`, base rooms ascending) -> events (ascending). The structural test of `35-inventory.md` 5.1 is extended: no use case reaches a lock of the stock (`StockDocuments`, `Warehouses`, `StockBalances`) and an event lock in its call tree (banquet writes only the queue row).

| Use case | Order |
|---|---|
| Create or edit an Inquiry (no hold) | business day (10, share) -> the event (45) |
| Tentative, move a session | business day (10) -> group (45, share, when linked) -> **the base rooms of every session, ascending** (45) -> the events involved, ascending (45) -> the sequence when a number is made (50) |
| Confirm (Definite) | business day (10) -> **the folio of the event when it exists (42)** -> base rooms of every session, ascending (45) -> the event (45) -> the sequence for the folio number (50), only when the folio does not exist yet |
| **Accept deposit** (opens the folio of a Tentative) | **transaction 1:** business day (10) -> the event (45) -> the sequence (50), which makes the folio; **transaction 2** is the payment of the cashier flow: business day (10) -> the folio (42) -> payments (43). Two transactions, because the payment's locks (42, 43) come before the event's (45); one transaction would descend |
| Post a session (manual) | business day (10, share) -> **the EVENT folio (42)** -> the event (45) -> the sequence is not needed (the folio exists) |
| Night audit, posting of the day | day `FOR UPDATE` (10) -> the stays (41) -> the folios, ascending (42, the EVENT folios among them) -> the events (45) -> the stock steps (queue rows are only inserted by the posting; the drain locks stock after, 45) -> accounting settings (46) |
| Payment, refund, transfer on an EVENT folio | as every folio: business day (10) -> folios, ascending (42) -> payments (43) -> companies (44) |
| Cancel a Definite event, or lose or cancel a Tentative with a folio | business day (10) -> the folio (42) -> the event (45) -> sequences if a number is made (50); the refund and the close of the folio are the existing use cases, each in its own transaction |
| BEO edit and revision | business day not needed (not dated); the event (45) |

A posting takes the **folio before the event** (42 before 45), as a group's reservation takes its reservation before the group: the use case reads the event and its folio id **without a lock**, locks the folio, then locks the event and re-reads it. `db.LockRows` and `db.EnterLockLevel` enforce the order at run time; `dbtest.LockTrace` records the order of every banquet use case and the test fails on a descent. A race has a test: two parallel postings of a session (one succeeds, one gets `SESSION_ALREADY_POSTED`), two Definite on a combined room and its part (the exclusion, `BANQUET_ROOM_NOT_AVAILABLE`), the promotion of an option while another is created.

## 7. Permissions, approvals and the module

### 7.1 Permissions (B16; new; granted to **no role** by the migration, the tenant administrator holds all)

| Permission | Who | Allows |
|---|---|---|
| `banquet.read` | sales, F&B, the departments | the calendar, events, sessions, the BEO, the reports, the documents |
| `banquet.manage` | banquet office, sales | create and edit events (Inquiry, Tentative), sessions, lines, the BEO text, post a session, complete an event, open the folio of a Tentative ("Accept deposit", with `payment.post` for the payment itself), the proposal and the e-mail |
| `banquet.confirm` | banquet manager | Definite, cancel a Definite, the tiers of an event, the approver of `BANQUET_DISCOUNT`, the contract |
| `banquet.setup` | banquet manager | meeting rooms and combinations, set-ups, items, packages, event types, reasons, templates, the settings (B3, B9, B6, B7), the BEO recipients |

The reads of the masters follow document 33: the list of rooms, items and packages is readable by `banquet.setup` **or** `banquet.manage` **or** `banquet.read`; the change needs the one that manages it. The payments and the transfers on the event folio need the **folio permissions that exist** (`payment.post`, `folio.reverse`, `cityledger.transfer`), as for a POS bill (34 8.1); the banquet permissions never replace them. Posting a session by the banquet office (a charge) is checked by `banquet.manage`, as POS checks `pos.order`: **(to be checked when it is built)** that `PostCharge` called with the source `BANQUET` does not also ask `folio.post_charge`.

### 7.2 Approval kinds (document 32 2)

| Kind (label) | Use cases | Amount | Approver's permission |
|---|---|---|---|
| `BANQUET_DISCOUNT` (discount of an event) | the discount of a line above the limit, before or after the posting | the base discounted, `abs`, added over the lines of the request | `banquet.confirm` |

It takes `FREE`, `DISTINCT` and `DISTINCT_ABOVE` per property like any kind. **No other kind is added**: a fee waived is the reversal that exists (`FOLIO_REVERSAL`), a refund is `REFUND`, an adjustment `FOLIO_ADJUSTMENT`, a transfer `FOLIO_TRANSFER`. The structural test of 32 4 is extended by the package.

### 7.3 The module

Routes of Banquet are classified **`BANQUET`** in `subscription.ModuleOf` (`{P}/banquet/*`, its PDFs and reports); `TestEveryRouteHasAModule` fails when one is not. A tenant without the module gets 403 `MODULE_NOT_IN_PLAN` on those routes only. The **services stay ungated**: the night audit posts the sessions that exist whether or not the module is on, and the data stays when the module is switched off (26 3). The folio routes, the payments and the invoice of an EVENT folio belong to `CORE`, as the folio of a POS bill does. Stock (4.9) needs `INVENTORY` and `POS` in the plan as well (F11). The comment of `model.go` is corrected in PR 1 (F16).

## 8. API

`{P}` = `/api/v1/properties/{propertyId}`. Every POST that creates or posts takes an `Idempotency-Key`; mutations are in OpenAPI first and the TS types are generated; errors are `*apperr.Error` with the stable codes below. Reads in PR 1 unless said.

| Route | Permission | What |
|---|---|---|
| `GET/POST/PATCH {P}/banquet/rooms[/{id}]`, `PUT .../rooms/{id}/parts`, `PUT .../rooms/{id}/setups` | read / setup | meeting rooms, combinations, set-ups and capacities, buffers |
| `GET/POST/PATCH {P}/banquet/event-types`, `.../reasons`, `.../items`, `.../packages` | read / setup | the masters |
| `GET/PUT {P}/banquet/settings` | read / setup | tentative conflict mode (PR 1); posting mode, default price mode, discount limit (PR 2); warehouse (PR 4); audited |
| `GET {P}/banquet/calendar?from=&to=&view=day\|week` | read | rooms x hours, with the status colours, the rank of options and the flags |
| `GET {P}/banquet/today` | read | events of the business date |
| `GET/POST {P}/banquet/events`, `GET/PATCH .../events/{id}` | read / manage | list (filters by status, sales, type, period, "past option date"), create, edit (`version` check: 409 `VERSION_CONFLICT`) |
| `POST .../events/{id}/sessions`, `PATCH/DELETE .../sessions/{sid}` | manage | add, move, cancel (a posted session cannot) |
| `POST .../events/{id}/tentative`, `/confirm`, `/lost`, `/cancel`, `/complete` | manage / confirm | the transitions of 4.3; `lost` and `cancel` take a reason |
| `POST .../events/{id}/availability` (a dry run of the holds and the rank) | manage | the room check before saving, with the conflicts, the rank in the new slot and the rank given up (4.3) |
| **PR 2:** `POST .../events/{id}/folio` | manage | opens the EVENT folio of a Tentative or Definite event (the same folio if it exists, so a retry is safe); "Accept deposit" goes on to the payment routes of the folio, which ask `payment.post` |
| **PR 2:** `POST/PATCH/DELETE .../events/{id}/lines`, `.../discount`, `GET .../estimate` | manage | lines (item or package), prices and mode, the estimate by the engine (`DryRun`; the client adds nothing) |
| `PUT .../events/{id}/deposit-schedule`, `PUT .../events/{id}/policy`, `GET .../events/{id}/cancellation-quote` | manage / confirm | the schedule, the tiers, the fee of the day |
| `POST .../sessions/{sid}/post`, `GET {P}/banquet/reports/unposted` | manage / read | post a session; the list of the not posted |
| `GET .../events/{id}/folio` | read | the EVENT folio (the common folio routes do the rest) |
| **PR 3:** `GET/PUT .../sessions/{sid}/beo`, `GET .../events/{id}/beo/revisions`, `POST .../beo/ack`, `GET .../documents/{kind}.pdf`, `POST .../documents/{kind}/send` | read / manage / confirm | BEO, revisions, acknowledgement, PDF, e-mail |
| `GET {P}/banquet/notices`, `POST .../notices/seen` | any banquet permission | the bell of the user |
| **PR 4:** `GET {P}/banquet/reports/{forecast,rooms,guests,sales,lost,schedule}` with `?format=csv` | read | B15 |

New codes: `BANQUET_ROOM_NOT_AVAILABLE`, `BANQUET_OPTION_NOT_FIRST`, `BANQUET_ROOM_NOT_FOUND`, `EVENT_NOT_FOUND`, `SESSION_NOT_FOUND`, `SESSION_ALREADY_POSTED`, `SESSION_POSTED` (an edit of a posted session), `EVENT_STATUS_INVALID`, `REASON_REQUIRED`, `OPTION_DATE_REQUIRED`, `NO_GUARANTEED_GUESTS`, `SETUP_NOT_OFFERED`, `ROOM_COMBINATION_INVALID`, `EVENT_FOLIO_CLOSED`. Reused: `VERSION_CONFLICT`, `APPROVAL_REQUIRED`, `APPROVAL_SAME_USER`, `FEE_ALREADY_POSTED`, `BUSINESS_DAY_CLOSED`, `MODULE_NOT_IN_PLAN`. Another tenant's or unassigned property: 404 `PROPERTY_NOT_FOUND`; missing permission: 403 `PERMISSION_DENIED`.

## 9. Screens (en and id; the desk 1280 to 1440, the calendar and the daily schedule usable at 390)

All texts go through `src/i18n` keys; the status and kind names below are the labels. The menu "Banquet / Banquet" appears when the module is in the plan and the user holds a banquet permission.

| Screen | Content | en / id labels |
|---|---|---|
| **Room calendar** | A grid of **rooms x hours** for a day (and a week view with sessions as bars), a bar for each session with the colour of its status (Inquiry grey outline, Tentative amber with its rank "opsi 2", Definite green, Completed muted); the buffers are drawn as hatching; a combined room draws on its parts; a click on an empty cell opens "New event" with the room, the date and the hour; a click on a bar opens the event; the filters (room, status, sales) | "Room calendar" / "Kalender ruang"; "Today's events" / "Acara hari ini" |
| **Events** | List with filter chips (status, "past option date", "my events") | "Events" / "Acara"; Inquiry / **Inquiry**; Tentative / **Tentatif (opsi)**; Definite / **Definit**; Completed / **Selesai**; Cancelled / **Batal**; Lost / **Lost** |
| **Event page** | Header (booker, contact, sales, type, option date, group link), tabs **Sessions** (the table and the add form with the room check and the capacity warning), **Lines** (items and packages, mode NETT / ++, discount; the estimate), **Deposits and policy**, **BEO**, **Documents**, **Folio** (with **"Accept deposit" / "Terima DP"** on a Tentative, shown to a user who holds `banquet.manage` and `payment.post`); the actions by status; the dialog of **moving a Tentative session** warns of the rank (4.3) | "Confirm" / "Konfirmasi (Definit)"; "Post session" / "Posting sesi"; "Complete the event" / "Selesaikan acara" |
| **Masters** | Meeting rooms with combinations, set-ups and buffers; items; packages with components; event types; reasons; templates; settings (with the lines of B3 and B9 explained) | "Meeting rooms" / "Ruang pertemuan"; "Set-up" / "Susunan"; "Theater / Classroom / U-shape / Round table / Cocktail / Boardroom" / "Teater / Kelas / Bentuk U / Meja bulat / Koktail / Rapat" |
| **BEO** | One page per session: set-up, run of the show, menu and guests, equipment, notes by department; the revision list; **"changed since revision X"** on each section; Print | "BEO" / "BEO (Perintah Acara)" |
| **Reports** | The seven reports of 4.10 | "Revenue forecast" / "Proyeksi pendapatan"; "Not posted" / "Belum diposting" |
| **Bell** | The notices of the user | "Option moved up" / "Opsi naik" ; "Option date passed" / "Tanggal opsi lewat" |

The cancel dialog shows the tier, the fee, the deposits held and the result (4.7) and cannot post with a blank reason. The approval dialog is the shared one of 32 with its kind and amount. Screens that read masters follow document 33 (`openPagesReads.test.ts` scans the new pages).

## 10. Flows with numbers

The property: IDR (0 decimals), tax PB1 10% on service charge, service SVC 10%, so the factor of a price with both is `1 + 0.10 + 0.10 x 1.10 = 1.21`.

### 10.1 A package, NETT, 100 guests (5.2)

Package "Full day meeting" at **363,000 nett per guest**; components (list price x quantity): Lunch 150,000, Coffee break 2 x 40,000 = 80,000, Room rental per guest 50,000, Equipment 20,000; `sum(w) = 300,000`; total `363,000 x 100 = 36,300,000`.

| Component | Share | Gross `g_i` | Net (÷ 1.21) | Charge code, account |
|---|---|---:|---:|---|
| Lunch | 150/300 | 18,150,000 | 15,000,000 | `BQT_FB`, 4240 |
| Coffee break | 80/300 | 9,680,000 | 8,000,000 | `BQT_FB`, 4240 |
| Room rental | 50/300 | 6,050,000 | 5,000,000 | `BQT_ROOM`, 4410 |
| Equipment | 20/300 | 2,420,000 | 2,000,000 | `BQT_EQUIPMENT`, 4410 |
| **Total** | | **36,300,000** | **30,000,000** | |

Service 3,000,000 (10% of net), tax 3,300,000 (10% of 33,000,000). Journal at the close of the day of posting: **Dr 1210 36,300,000; Cr 4240 23,000,000; Cr 4410 7,000,000; Cr 2430 3,000,000; Cr 2410 3,300,000**; balanced. The same package at **300,000 ++** gives 30,000,000 net and the same totals on top.

**The rounding case.** A package of **100,000 nett for 1 guest** with three equal components: `g_i = 33,333` each, `D = 100,000 - 99,999 = 1`, added to the first component (tie): 33,334, 33,333, 33,333; the total is 100,000. The engine extracts the net of each from its own gross and records its own rounding; the folio total is 100,000.

### 10.2 The deposit and the folio of an event (4.7, F3)

The event of 10.1 is Definite; the schedule is 30% at signature, 50% at H-30, 20% at H-7: **10,890,000, 18,150,000, 7,260,000** (sum 36,300,000). The payments are deposits: Dr 1110 / Cr 2310. Before the posting 2310 holds 36,300,000. The session is posted: charges 36,300,000 on the folio; EVERY_DAY applies `T = min(H, U) = 36,300,000`: **Dr 2310 36,300,000 / Cr 1210 36,300,000**; the folio balance is 0 and it can be closed. With only the first 10,890,000 paid, `T = 10,890,000` and the folio owes 25,410,000.

### 10.3 Cancelling a Definite event (4.7)

Tiers: 60 days or more **0%**, 30 to 59 **25%**, 7 to 29 **50%**, less than 7 **100%** of the estimated net revenue (30,000,000). Cancelled **20 days** before: tier 50%, fee **15,000,000** (`CANCEL_FEE`, 4510, no tax by the default of the charge code). Deposits received so far: 10,890,000 + 18,150,000 = 29,040,000. The fee is posted, the deposit pays it (Dr 2310 15,000,000 / Cr 1210 15,000,000, and Dr 1210 / Cr 4510 15,000,000 at the close), **14,040,000** is left in 2310 for the refund (an approval of kind `REFUND`). With the action `FORFEIT` the same amount goes to 4520 (`BQT_FORFEIT`).

**With the base GROSS** (the template says so; the same tiers): the base is the contract total **36,300,000**, so the 50% tier gives **18,150,000**, posted INCLUSIVE as `CANCEL_FEE`; the customer pays exactly that. The deposits received are 29,040,000: the deposit pays the fee (Dr 2310 18,150,000 / Cr 1210 18,150,000), **10,890,000** is left in 2310 for the refund. With the base NET (above) the same cancellation costs 15,000,000; the choice is the property's, by template, and is copied to the event at Definite.

**A Tentative that took a deposit and is lost** (4.3, Q2): the event of 10.2 is Tentative and the customer pays **10,890,000** by "Accept deposit" (Dr 1110 / Cr 2310). The event is lost. Either the whole is **refunded** (Dr 2310 / Cr 1110, an approval of kind `REFUND`), or **5,000,000** is **forfeited** (`BQT_FORFEIT`, 4520: Dr 2310 5,000,000 / Cr 1210 5,000,000 by the release, and Dr 1210 / Cr 4520 5,000,000 at the close) and **5,890,000** is refunded.

### 10.4 The queue of options (4.3)

Mode QUEUE. The Ballroom (A + B), Saturday 12:00 to 16:00, buffers 60 and 30 minutes: the hold is 11:00 to 16:30 on **A** and on **B**. Event X is Tentative: option 1. Event Y asks for **A** the same day 15:00 to 19:00: its hold (14:00 to 19:30 on A) overlaps X's on A, so it is **option 2**; event Z asks for **B** 08:00 to 10:00 (hold 07:00 to 10:30): no overlap, option 1. X is confirmed: Definite on A and B (the constraint). Y cannot be confirmed (the exclusion and `BANQUET_OPTION_NOT_FIRST`); Y's sales get `OPTION_LOST`. If instead X is **lost**, Y becomes option 1 and its sales get `OPTION_PROMOTED`. In mode REJECT Y is refused at creation. **Moving** (4.3): Z, option 1 on B, is moved to 09:00 to 11:00 the same day: the new hold overlaps nothing, so Z is option 1 of the new slot; a Z moved onto A at 15:00 would go **behind** X and Y (option 3), and the dialog says so before it saves.

### 10.5 Posting by guests: guaranteed and actual (4.5)

100 guaranteed, 92 actual: billed **100**. 100 guaranteed, 104 actual: billed **104**. AUTO with 100 guaranteed and no actual: billed 100, `on_guarantee`, and when 92 is entered later the posting stays at 100 (a lower billed number is a credit adjustment only if the owner's contract says so; it is not automatic).

## 11. Phases (PRs), migrations, tests

| PR | Migration | Contents |
|---|---|---|
| **1. Masters and schedule (no money)** | 00089 | `banquet_settings` (B3), rooms, parts, set-ups, event types, reasons, items, packages, events, sessions, holds with the exclusion and the deferred guard, notices; the module classification and the fix of the comment (F16); the permissions; the services, the calendar, the options queue, the screens (calendar, events, event page without lines, masters) |
| **2. Money** | 00090 | the EVENT folio and its view, `source BANQUET`, the transfer scope, the charge codes, lines and components, discounts and `BANQUET_DISCOUNT`, price mode, the deposit schedule, the policy tiers, the posting of a session (manual and by the night audit), the cancel fee and forfeit, the estimate, the not-posted report; the guard of the sale paths |
| **3. Documents** | 00091 | the BEO with revisions and acknowledgements, the notices of a revision, the proposal and the contract PDFs, `email_outbox` freed from its reservation, the documents list |
| **4. Stock and reports** | 00092 | the queue and the cost tables widened (F12, F13), the banquet warehouse, the worker for the new source, the gap report, the changes to the F&B reports, and the reports of B15 |

Each PR is DB, backend, API (OpenAPI first, `npm run gen:api`), frontend, i18n and tests together, on its own branch from `main`; `scripts/sqlc.sh generate`, lint (0 issues), `go test`, `scripts/db-test.sh` (migrations changed), and the web checks as CLAUDE.md lists. A migration that cannot be run again gets the up/down/up of the script; **no applied migration is edited**.

### Tests (real PostgreSQL; races with two goroutines and a barrier: exactly one success and a defined error for the other, as `05` 4)

1. **The calendar and the exclusion (PR 1):** a second Definite on the same room and time is refused by the constraint (also by direct SQL); the Ballroom and each of its parts exclude each other, in both directions; a base room in two combos; the preparation and the tear-down time make two sessions 20 minutes apart conflict; two sessions back to back with enough buffer do not; a session that ends after midnight; a multi-day event; the deferred guard refuses a Definite session with no holds.
2. **The options (PR 1):** REJECT and QUEUE with the numbers of 10.4; the rank is derived; option 2 cannot become Definite while option 1 holds; cancelling option 1 promotes option 2 and writes the notice in the same transaction (a rollback leaves none); the setting changes only new bookings and is audited; an option date in the past is flagged, not cancelled, and the day close writes one notice a day. **Moving a Tentative session** makes new holds: it goes to the back of the new slot, the old slot's options are promoted with their notices, the availability check answers both ranks, and a move in mode REJECT onto a held slot is refused.
3. **Races (PR 1):** two confirmations for the same room (one succeeds), a confirmation and a creation of an option, a move and a confirmation; `dbtest.LockTrace` shows the order of every use case and no descent.
4. **Masters and rules (PR 1):** the validations (combo depth, capacities, buffers, set-up not offered), the reads by permission (document 33), the tenant and the property isolation (another tenant's room is 404), the module gate over the real router, `TestEveryRouteHasAModule`.
5. **Money (PR 2):** the package split (10.1) with exactness for random prices and guests (the sum of the parts always equals the total), the rounding case, NETT and ++ for the same price, mixed modes in one event, the frozen mode and the frozen price after a master changes, the discount below the limit without approval and above it with the approval and the rule of the property (`FREE`, `DISTINCT`, `DISTINCT_ABOVE`), the journal of 10.1 balanced and the reconciliation of the control accounts at the close, the comparison test of the view `folio_item_gl` old against new, the `is_deposit` of an EVENT payment before and after the first charge, the deposit in 2310 and its release in both modes of 31.
6. **Posting (PR 2):** AUTO at the close and MANUAL (the warning, not a block), a session posted the next day is dated that day and listed in the report, billed guests = max, no guaranteed guests blocks AUTO, two parallel postings (one `SESSION_ALREADY_POSTED`), the unique key and the idempotency key, a posting in a closed day refused (the application gate and the trigger), the rollback of a posting that fails halfway (no folio item, no posting row), the correction by an adjustment, the structural guard of the paths.
7. **Cancellation (PR 2):** the tiers of 10.3, the fee once (`FEE_ALREADY_POSTED`), the forfeit limited to the deposits, a waiver by the reversal with its approval, the refund, the folio still closable. **The base:** the same tiers with NET and with GROSS (10.3, 15,000,000 and 18,150,000), the base and the tiers copied at Definite, a later change of the template changes no event, GROSS posted INCLUSIVE with and without a rule on the charge code. **A Tentative with a deposit (Q2):** "Accept deposit" opens the folio once (a retry returns it), the payment is a deposit in 2310, Confirm uses that folio and opens none, a Lost Tentative is refunded (approval `REFUND`) or forfeited to 4520 (up to the deposits, and no more), and the order of locks of the two transactions has no descent (`dbtest.LockTrace`).
8. **The group link (PR 2):** the transfer scope `EVENT_GROUP` (item and balance, the return), and `ANY` still needs `folio.transfer_any`.
9. **Documents (PR 3):** a revision for each save, the snapshot reproduces the BEO of that moment, "changed since" and the acknowledgement, the notices to the head and the deputy, the PDF of the proposal, the contract and the BEO in en and id, the e-mail in the outbox and its status, the old e-mail kinds unchanged.
10. **Stock and reports (PR 4):** the queue row of a posted session with the quantities of a recipe x billed guests, the drain by the night audit and `FAILED` blocking the close, no row without Inventory or without a link, the gap report, the revenue of the banquet in the F&B % with and without it ("of which banquet"), the reports of B15 with a known scenario each (the totals, the share, the occupancy, the conversion, the guaranteed and actual), CSV.
11. **Web (each PR):** the calendar (the colours, the rank, the buffers, the create by click), the event page tabs, the cancel dialog, the discount dialog, the BEO with "changed since", i18n en and id for every key (the test that the two files have the same keys), a phone-width layout of the calendar and the schedule.

## 12. Not in this design (B16 and the owner)

| Out | Note |
|---|---|
| An online booking portal | The inquiry is entered by the hotel. |
| A seating plan (denah kursi) | The set-up and the capacity only. |
| Catering outside the hotel | The rooms and the warehouse are the property's. |
| Commission of an agent | No commission table. |
| Allotment of rooms to an event | B13 only links the event to a group; the rooms of the group are booked as today. |
| A scheduler that runs by the clock | The reminders are derived and written at the day close (F9). |
| Resource conflicts of equipment and staff | An item is a sold thing, not a counted resource. |
| A banquet price list by season or by customer | The item price and the line price are what the person writes. |
| Returning stock when the guests are fewer after the posting | Q3. |

## 13. The decisions B1 to B16 (the owner, 2026-10-24; as they were given, not re-opened)

**B1.** Ruang acara: master per properti (nama, luas, kapasitas per setup: theater, classroom, U-shape, round table, cocktail, boardroom). Ruang bisa digabung (Ballroom = A + B); memesan gabungan mengunci bagiannya dan sebaliknya. Waktu persiapan dan bongkar (menit) per ruang. *(3.2, 4.1, 4.2)*

**B2.** Acara berisi satu atau lebih sesi. Header: pemesan (tamu atau perusahaan), contact person, sales, jenis acara. Sesi: tanggal, jam mulai-selesai (civil.Date + civil.TimeOfDay), ruang, setup, pax perkiraan/garansi/aktual. Boleh lebih dari satu hari. *(3.2, 4.2, F10)*

**B3.** Status Inquiry → Tentative (tanggal opsi) → Definite → Selesai, atau Batal/Lost (alasan wajib). Inquiry tidak menahan ruang. Definite selalu eksklusif, dijaga exclusion constraint database (termasuk ruang gabungan dan waktu persiapan/bongkar). Pengaturan per properti untuk Tentative yang bentrok: TOLAK, atau IZINKAN sebagai antrean opsi ke-2, ke-3... (opsi 1 batal/lost → opsi berikutnya naik dan sales-nya diberi notifikasi; opsi ke-n tidak bisa Definite selama opsi lebih awal menahan ruang). Pengaturan boleh diubah kapan saja, berlaku untuk pemesanan baru, diaudit. Tentative lewat tanggal opsi ditandai dan diingatkan, tidak dibatalkan otomatis. *(4.2, 4.3, F8, F9)*

**B4.** Kalender ruang: grid ruang × jam per hari dan per minggu, warna per status, klik untuk membuat; "Acara hari ini". *(9)*

**B5.** Item banquet: master sendiri (menu per pax, paket minuman, sewa ruang per sesi/jam, sewa alat, dekorasi, lain-lain), masing-masing memakai charge code (pajak/servis hanya lewat ChargeCalculationEngine). Item F&B boleh ditautkan ke produk POS untuk memakai resepnya (opsional; banquet jalan tanpa modul POS/Inventory). *(3.2, 4.9, F11)*

**B6.** Paket per pax dipecah ke komponen dengan akun pendapatannya (sewa ruang 4410, F&B 4240, lainnya sesuai charge code). Mode harga per item dan per paket: NETT (INCLUSIVE) atau ++ (EXCLUSIVE), default dari pengaturan properti; mode dan harga dibekukan di baris acara saat ditambahkan. Satu acara boleh mencampur mode. Paket nett: komponen dihitung mundur oleh mesin; selisih pembulatan ke komponen terbesar sehingga total = harga paket × pax. Proposal/kontrak menulis "nett" atau "++". *(5.1, 5.2, 10.1)*

**B7.** Diskon di atas batas memakai aturan persetujuan yang ada (per jenis dan nominal, penyetuju berbeda). *(5.4, 7.2, F14)*

**B8.** Folio jenis baru EVENT, satu per acara. Semua uang lewat jalur yang ada: FolioPostingService, deposit 2310 dan pelepasannya (31), pembayaran, transfer ke piutang perusahaan/city ledger, invoice PDF. *(F2, F3, 5.3)*

**B9.** Posting tagihan: pengaturan per properti OTOMATIS atau MANUAL, bisa ditimpa per acara. OTOMATIS: tutup hari memposting sesi hari itu yang belum diposting (pax = aktual bila diisi, kalau belum garansi dan sesi ditandai). MANUAL: banquet menekan "Posting sesi" setelah isi pax aktual; tutup hari memberi peringatan, tidak menghalangi; sesi bisa diposting di hari bisnis berikutnya (bertanggal hari itu) dan tampil di laporan "belum diposting". Di kedua mode posting manual lebih awal boleh. Pax yang ditagih = maksimum garansi dan aktual. Satu sesi hanya bisa diposting sekali (unik di database); koreksi lewat penyesuaian. Tunduk pada RequireOpenBusinessDay dan trigger business_day_must_be_open. *(4.5, F17)*

**B10.** Jadwal deposit per acara (misalnya 30% tanda tangan, 50% H-30, sisa H-7) dengan pengingat jatuh tempo. Kebijakan pembatalan per acara: biaya menurut jumlah hari sebelum acara, memakai 4510/4520 dan jalur pembatalan yang ada (21). *(4.7, F5, F6)*

**B11.** BEO per sesi: setup, susunan acara, menu, pax, alat, catatan per departemen (dapur, F&B service, housekeeping, engineering, security, front office). Setelah Definite setiap perubahan membuat revisi; departemen melihat "berubah sejak revisi X" dan diberi notifikasi. Cetak PDF. *(3.4, 4.6)*

**B12.** Proposal dan kontrak PDF (internal/documents), dikirim lewat email_outbox. *(3.4, F8)*

**B13.** Acara bisa ditautkan ke satu grup kamar; tagihan tetap di folio masing-masing, dipindah dengan transfer folio yang ada. *(4.8, F15)*

**B14.** Bila modul POS dan Inventory aktif: item bertaut resep memotong stok gudang banquet saat posting (pax ditagih × resep) lewat antrean yang sama dengan POS; HPP masuk laporan F&B. Jelaskan dampaknya ke 35-inventory 12 (F&B reconciliation, daily cost) yang kini hanya membaca POS. *(4.9, F12, F13)*

**B15.** Laporan: forecast pendapatan Definite dan Tentative per bulan, pendapatan dan okupansi per ruang, pax garansi vs aktual, produksi per sales, alasan lost, jadwal acara harian, sesi belum diposting. *(4.10)*

**B16.** Izin banquet.read, banquet.manage, banquet.confirm, banquet.setup. Route digerbang modul langganan BANQUET (yang sudah dicadangkan; perbaiki komentar di internal/subscription/model.go yang keliru menyebut "the outlets"). Level kunci baru untuk acara ditaruh setelah groups di urutan global; jelaskan di dokumen. *(6, 7, F1, F16)*

## 14. The decisions on the questions Q1 to Q15 (the owner, 2026-10-24)

The questions raised by the first draft and the owner's answers. **Approved as recommended**: Q1, Q3, Q4, Q5, Q7, Q8, Q9, Q10, Q11, Q12, Q13, Q14, Q15. **Changed by the owner**: Q2 and Q6 (marked).

| # | Question | Decision of the owner |
|---|---|---|
| Q1 | The lock level of the events (F1): an inner order at level 45 (groups, rooms, events), or a renumbering of 46 to 50. | **As recommended:** an inner order at 45; no renumbering, no change in other documents. |
| Q2 | When is the EVENT folio opened, and may a deposit be taken from a Tentative? | **Changed.** The folio is opened **automatically at Definite**, **and earlier on a Tentative by the action "Accept deposit"** (`banquet.manage` and the payment permission that exists), so that a deposit can lock the date. Confirm uses the folio that exists. A Tentative with a folio that is Lost or Cancelled has its deposit **refunded** (approval `REFUND`) or **forfeited** (`BQT_FORFEIT`, 4520) by the flow of 4.7. (3.3, 4.3, 4.4, 4.7, 6, 8, 9, 10.3, 11) |
| Q3 | Stock (4.9): a correction of the guests after the posting has no effect on stock, and a banquet is always `POS_SALE` in the ledger with no outlet. | **As recommended.** |
| Q4 | Notices: a table of Banquet (`banquet_notices`), and the freeing of `email_outbox` in PR 3 (F8). | **As recommended.** |
| Q5 | The faktur pajak of an event, and the tax on the cancellation fee and on a forfeit (F18). | **As recommended:** out until the tax consultant answers; the fee has the rules of its charge code (none by default). |
| Q6 | The base of the cancellation percent. | **Changed.** The base is **chosen by policy template**: **NET** (the estimated net revenue, the default) or **GROSS** (the contract total with service and tax). It is copied to the event with its tiers at Definite. (3.3, 4.7, 10.3, 11) |
| Q7 | Sessions that end after midnight: `ends_next_day`. | **As recommended:** yes. |
| Q8 | The six sections of the BEO and who is told: the head and the deputy of the department that the property maps to each section. | **As recommended.** |
| Q9 | A session with neither guaranteed nor actual guests when AUTO closes. | **As recommended:** blocked and flagged, posted only by a person on the estimate. |
| Q10 | The granularity of a revision: one for each save. | **As recommended.** |
| Q11 | A tenant with Banquet and Inventory but no POS has no recipe, so no stock deduction (F11). | **As recommended:** accepted. |
| Q12 | The deposit of an event is in 2310 (B8), though the chart has 2330. | **As recommended:** 2310; 2330 stays unused. |
| Q13 | The formats of the numbers: `BQ-000123` for the event. | **As recommended.** |
| Q14 | When an AUTO posting used the guarantee and the actual is lower: a credit is an adjustment made by a person. | **As recommended.** |
| Q15 | A session of a future day is not posted early by hand. | **As recommended:** not later than the open business day. |

**An addition of the owner to 4.3 (with Q2):** moving a Tentative session makes new holds, so the session goes to the back of the queue of its new slot, and the screen warns the sales before it saves.

## 15. As built: PR 1, masters and schedule, no money (migration 00089)

What exists: the tables of 3.2 up to the holds and the notices; the sequence `BANQUET_EVENT` (`BQ-000123`); the permissions `banquet.read/manage/confirm/setup` (granted to no role); the module `BANQUET` on every route (`TestEveryRouteHasAModule`); the use cases of the masters, of the events (Inquiry, Tentative, Definite, Lost, Cancelled), of the sessions and of the calendar; the screens (calendar by day and week, events, the event with its Sessions tab, the Confirm dialog, moving a session, rooms, items and packages, settings); `pms-seed banquet`.

Where it differs from, or adds to, the text above:

- **Lock order.** Groups and stock share the level 45, and the inner order of the events is groups, then the base rooms ascending, then the events ascending. It is kept by a second rank in the transaction state (`eventRank`: `booking_groups` 1, `banquet_rooms` 2, `banquet_events` 3), so the rule of the stock tables is unchanged. A use case of the banquet never locks stock (a test reads the sources). The room masters lock the rooms they change (`banquet_rooms` FOR UPDATE); the room set of a session is recomputed after the lock and a change since the read is a `VERSION_CONFLICT`.
- **Idempotency-Key for creating an event** is not built: the schema of 3.2 has no column for it. A double click is stopped by the button; the number is the only identity.
- **New codes.** `CAPACITY_EXCEEDED` (409; the guests are above the capacity of the room in that set-up; the request is sent again with `accept_capacity`), `BANQUET_ROOM_IN_USE` (the parts of a combined room cannot change while events hold it), `SESSION_CANCELLED`, `BOOKER_REQUIRED`, `OPTION_DATE_REQUIRED`, `REASON_NOT_FOUND`, `ROOM_COMBINATION_INVALID`, `SETUP_NOT_OFFERED`. The last five are refusals of the request (422) with a code of their own.
- **Inquiry.** `start_time`, `end_time` and `room_id` of a session are nullable while the event is an Inquiry; the database requires them for Tentative and Definite (`BANQUET_HOLDS_MISSING` is the deferred guard of the holds).
- **Notices.** `banquet_notices` has a `notice_date`, the key of the unique index of `OPTION_DUE` (once per event, user and day). The day close writes them (`WriteDueNotices`) in the transaction of the outlet close hook.
- **Moving a Tentative session** deletes the old holds and inserts the new ones (back of the queue of the new slot); the two ranks of the dry run come from `POST .../availability`; the dialog asks for a yes when the place is worse, the server does not demand it.
- **Complete** (Definite to Completed) is not in this PR: it belongs to the night audit with the bill.
- **Routes.** The sessions are nested under `events/{id}/sessions/{sid}`.
- **Charge codes.** `banquet.setup` reads `charge-codes` (33, section 7): an item has one, and whoever sets up the banquet need not hold a billing permission.
- **Screens.** The calendar draws 06:00 to 24:00; the operating hours of the settings are stored but not yet used by the grid. On a phone the day is a list in time order.

## 16. As built: PR 2, the money (migration 00090)

What exists: the folio type `EVENT` (one open folio per event, no stay, reservation, group or outlet) and the folio source `BANQUET`; the transfer scope `EVENT_GROUP`; the charge codes `BQT_FB`, `BQT_ROOM`, `BQT_EQUIPMENT`, `BQT_DECOR`, `BQT_OTHER` and `BQT_FORFEIT` (seeded by `seed_charge_codes`, so a new property has 18 system codes); the lines (`banquet_event_lines`) with their components (`banquet_line_components`); the deposit schedule; the policy templates with their deposits and tiers, and the policy of the event (`banquet_policy_tiers`); `banquet_session_postings` with the trigger `business_day_must_be_open`; the money settings (`posting_mode`, `default_price_mode`, `max_discount_pct_no_approval`); the approval kind `BANQUET_DISCOUNT`; the money screens (event tabs Items & packages, Deposit & policy and Folio, Accept deposit, Post, Cancel and Transfer dialogs, Events today, Sessions not posted, money settings and templates).

Where it differs from, or adds to, the text above:

- **Cancelling does not refund.** The cancel and lost use cases post the fee or the forfeit, apply the deposit to it and return `CancelResult` (`refundable`, `folio_id`). The deposit that is left is refunded by the existing folio refund (approval `REFUND`) as a separate step; the cancel dialog says so. There is no "Complete" use case in this PR either (see section 15).
- **Posting mode of one event.** `PUT events/{id}/posting-mode` (`SetPostingMode`) sets the AUTO or MANUAL override of an event, which the text calls "the override of the property (B9)".
- **The base of the deposit schedule** is the contract total (with service and tax, `contract_value_estimate`), the percent of a row is recalculated when the estimate changes until the row is paid. A deposit is derived (the payments of the folio before its first charge, `EventDeposits`), not a column.
- **Tier ranges.** A tier applies when `days_from <= days before <= days_to`; a `NULL` upper end is open. Two tiers that cover the same days are refused (`TIER_OVERLAP`).
- **Cancel fee.** NET is posted in the mode of the charge code, GROSS and a forfeit INCLUSIVE; a forfeit is limited to the deposits received (`FORFEIT_EXCEEDS_DEPOSITS`); an amount below the tier needs its reason (`FEE_REASON_REQUIRED`). A Tentative event has no tier: only a forfeit can be asked of it (`ACTION_NOT_ALLOWED`).
- **Billed guests** are the larger of the guaranteed and the actual number; a session with neither is not posted (`NO_GUARANTEED_GUESTS` by hand, the warning `BANQUET_SESSION_NO_GUESTS` at the close). Posting keys: `banquet:session:<session>:<line>:<component>`, `banquet:cancel:<event>` and `banquet:cancel:<event>:forfeit`.
- **Day close.** The automatic posting runs after the room charges and before `ExpireOutletLocks`. A MANUAL event, a session without guests and a session whose lines use an inactive charge code only warn (`BANQUET_SESSION_NOT_POSTED`, `BANQUET_SESSION_NO_GUESTS`, `BANQUET_CHARGE_CODE_INACTIVE`): **none of them blocks the close**. The session is not posted by the close, stays on the report "Sessions not posted" with its cause ("Charge code inactive", `inactive_code` in the report) and is posted by hand once the code is active again or the line is changed. A late post of an old session is dated the open business day.
- **Transfer.** `EVENT_GROUP` moves the bill (items or the balance) to the open master folio of the group, linking the event first when it is not linked (`EVENT_GROUP_MISMATCH` when it is linked to another group, `GROUP_MASTER_NOT_FOUND`); it needs the approval `FOLIO_TRANSFER`, and a balance transfer replays by `Idempotency-Key`.
- **New codes.** `SESSION_NOT_DUE`, `SESSION_ALREADY_POSTED`, `SESSION_POSTED`, `FORFEIT_EXCEEDS_DEPOSITS`, `FEE_REASON_REQUIRED`, `TIER_OVERLAP`, `POLICY_TEMPLATE_NOT_FOUND`, `DEFAULT_EXISTS`, `EVENT_GROUP_MISMATCH`, `GROUP_MASTER_NOT_FOUND`, `PACKAGE_EMPTY`, `ACTION_NOT_ALLOWED`, `EVENT_FOLIO_CLOSED`.
- **Policy template.** The separate deposit-template list of the mockup is merged into the policy template (its deposits and its tiers are copied together at Definite).
- **Events today** reads the report "Sessions not posted" for the guaranteed and the actual number of guests.
- **Structural guard.** `TestTheBanquetPostsOnlyThroughTheEventEntryPoints` scans the non-test files of the module: no `PostCharge(` and no write to `folio_items`; the money goes only through the `PostEventCharge` paths of the folios module.
- **Seed.** `pms-seed banquet` gives every item the charge code of its kind **by code** (`BQT_FB` for food and drinks, `BQT_ROOM`, `BQT_EQUIPMENT`, `BQT_DECOR`; the first active code of the type only as a fallback, with a warning), gives `BQT_FB`, `BQT_ROOM`, `BQT_EQUIPMENT`, `BQT_DECOR` and `BQT_OTHER` the service charge SVC 10% and PB1 10% (on the service) when they have no rules (`BQT_FORFEIT` has none), and makes the package "Fullday Meeting" of 10.1 (363,000 nett per guest: lunch 150,000, coffee break 2 x 40,000, room 50,000, equipment 20,000; 100 guests = 36,300,000, net 30,000,000, service 3,000,000, tax 3,300,000) and the policy template `STANDARD` (30% at signature, 50% at H-30, 20% at H-7; tiers 0, 25, 50 and 100%). A property seeded by the first version is repaired through the services (audited): an item that still has the first code of its type (and the old price of the lunch and the coffee break) and a package that is still the first one follow the document; one a person changed is kept and a warning is printed.
- **Estimate and transfer (screens).** "Revenue by account" shows the number and the name of the ledger account of the charge codes (4240, 4410), one row for the codes that share an account. The button "Transfer to the folio of a group" is open for an event that is not linked as well (the dialog links it to the group chosen); it is off only without `banquet.manage` or when no group has an open master folio, and its tooltip says which.

## 17. As built: PR 3, the documents (migration 00091)

What exists: the BEO per session (`banquet_beo_sections`, `banquet_run_of_show`), its revisions with a snapshot (`banquet_beo_revisions`), the marks of reading (`banquet_beo_ack`), the department of each section (`banquet_beo_recipients`), the recorded documents (`banquet_documents`: the contract and the proposal that was sent), the sequence `BANQUET_CONTRACT` (`BQC-000123`), the notice kinds `BEO_REVISED` and `FEE_PROPOSED`, the e-mail outbox freed from the reservation (`reservation_id` nullable; `banquet_event_id`, `document_id`, `language`; the kinds `BANQUET_PROPOSAL` and `BANQUET_CONTRACT`; `email_outbox_shape_ck` keeps each kind to its own columns, and `RESERVATION_CONFIRMATION` is unchanged), the PDFs (proposal, contract, BEO of an event, of a session or of a revision, in English and Indonesian), "Complete event", the BEO and Documents tabs, the bell, the recipients in the settings, and `pms-seed banquet` (the recipients and a sample BEO for a Definite event that has none).

Where it differs from, or adds to, the text above:

- **What a revision is.** A revision is made at the end of every use case that changes what a BEO or a contract shows, and only when the snapshot differs from the one of the last revision (saving what is already there is no revision). The hooks are: confirming (revision 1, kind `baseline`/`confirmed`), adding, changing and cancelling a session, adding, changing and removing a line, the deposit schedule and the cancellation policy (so that a contract is never stale), and saving the BEO. A Definite event that has no revision (made before this PR) gets a baseline one at its first save. Before Definite nothing is kept: the BEO is a draft and `BEO_IS_DRAFT` answers a mark of reading.
- **Shared data.** A change of the date, the start, the end, the room, the set-up or the guests (estimated or guaranteed) of a session marks every department section of that session "changed"; a change of the text of a section marks only that section. The section `GENERAL` belongs to no department and tells nobody. The marks are per section and user; the revision a section "was read at" is the highest of its readers. "Changed since" reads the snapshot of that revision.
- **Who is told.** The head and the deputy of the department mapped to a section (a sub-department with none takes the ones of its parent, as the approvals of the inventory do), in the transaction of the revision, except the person who made it. A person who has not seen their last `BEO_REVISED` of the event gets that notice brought up to date, not a second one (a partial unique index). A section with no department tells nobody and the screen says so. **The first revision (the confirmation) tells the departments that the BEO exists**. The recipients are set with `banquet.setup`; the list of departments is readable with `banquet.setup` as well.
- **Saving needs an open business day** (the revision is dated by it); the text of a BEO can be saved up to the day the event is closed.
- **Contract.** `POST events/{id}/documents/contract` (`banquet.confirm`) numbers the contract of the revision in force once: the same revision gives the same number, a later revision needs a new contract. The contract is drawn from the snapshot of its revision, so it never changes. A proposal that is e-mailed is recorded with its own snapshot, so the e-mail carries what the person saw; a proposal that is only opened is drawn from the live event.
- **PDF.** `GET events/{id}/documents/proposal.pdf|contract.pdf|beo.pdf` (`?session_id=`, `?revision=`) and `GET documents/{id}/file.pdf`, with `?lang=`. The proposal shows "nett" or "++" beside each price, the estimate, the deposit schedule and the cancellation policy; the BEO shows no price and prints only the sessions that are active; the Latin-1 rules are those of the other documents. Headings are printed in capitals.
- **E-mail.** `POST events/{id}/emails/proposal|contract` records the document, queues the message in the same transaction and the existing worker sends it with the PDF in the language asked (English when left out). `GET events/{id}/emails` lists them with the state QUEUED, SENT, FAILED or SKIPPED; a message of an event that was cancelled or lost meanwhile is SKIPPED. The address is the one given or the contact e-mail of the event (`EVENT_HAS_NO_EMAIL`); `EMAIL_NOT_CONFIGURED` without a mail server; `EMAIL_ALREADY_QUEUED` for the same message to the same address while one waits. The worker's lookups live in `notifications` (which imports the banquet and the documents; the banquet imports neither).
- **Complete event.** `POST events/{id}/complete` (`banquet.manage`): a Definite event whose active sessions are all posted or cancelled becomes `COMPLETED` (`closed_at`); `SESSIONS_NOT_POSTED` says how many are not. It does not close the folio.
- **Bell.** The bell of the banquet shows the notices of the caller (`OPTION_PROMOTED`, `OPTION_DUE`, `OPTION_LOST`, `DEPOSIT_DUE`, `BEO_REVISED`), refreshed on focus and every 60 seconds; opening one marks it seen.
- **New codes.** `BEO_IS_DRAFT`, `REVISION_NOT_FOUND`, `CONTRACT_NOT_ISSUED`, `SESSIONS_NOT_POSTED`, `EVENT_HAS_NO_EMAIL`, `INVALID_EMAIL`, `INVALID_LANGUAGE`, `INVALID_REVISION`, `INVALID_SESSION`, `DOCUMENT_NOT_FOUND`. New audit actions: `banquet.beo_saved`, `banquet.beo_revised`, `banquet.beo_read`, `banquet.beo_recipients_changed`, `banquet.proposal_made`, `banquet.contract_issued`, `banquet.event_completed`.
- **Not built.** `FEE_PROPOSED` is in the notice kinds but nothing writes it yet; the BEO has no per-line "changed" marks; the proposal does not store a validity date.
- **Transfer dialog (PR 2 review).** With no posted bill the choice "by items" is off and says "Belum ada tagihan terposting" and the dialog starts on the amount.
