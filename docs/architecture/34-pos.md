# 34. POS: outlets, bills on the folio, the lock of a folio for outlet charges, kitchen and print agent

Status: **approved by the owner (2026-10-15)**. **PR 1 (stage 1, migration 00080) is built: section 17. PR 2 (stage 2, migration 00081) is built: section 18. PR 3a (realtime, the waiter on a phone, the kitchen display and the print queue, migration 00082) is built: section 19.** The print agent (3b) and the rest is design only. D1 to D21 of section 15 are decided (D2 and D21 differently from the recommendation: marked there and in 7.5, 7.6, 6.3 and 9.10, 9.11). Asked by the owner on 2026-10-15; the owner's decisions are in the request (numbered 1 to 13 there, referred to here as **R1 to R13**) and are **not re-opened**. Where one of them touches a rule of the platform or of an earlier document, section 1 says so, with the reason and what this design does about it. Nothing here is built. Words in `code font` are tables, columns, permissions or error codes; every new name is a proposal until approved.

Read with: `03-financial-engines.md` (one engine, one posting path), `05-transactions-locking.md` (lock order), `18-architecture-decisions.md` 3 (folio model), `26-subscriptions.md` (module gate), `28-check-in-panel.md`, `30-group-folio.md` (master folio, `TRF`, the trail and the return, routing), `31-deposit-release.md`, `32-distinct-approver.md`, `33-setup-read-permissions.md`, `11-cashier-budget-cashflow-card.md` (shifts), `27-qris.md`.

## 0. The idea in one page

- A **bill** of an outlet is a **folio of type `OUTLET`**: no stay, no reservation, owned by one outlet, numbered per outlet (`R-0123`). Everything money-related is what the platform already does: charges through `FolioPostingService`, tax and service through `ChargeCalculationEngine`, payments through `PaymentService` on the cashier shift, reversals and adjustments with an approval, transfers through the transfer log of document 30.
- What is new is **everything before the ledger**: the catalogue, the floor plan, the **draft order** (rows of its own tables, free to change, no ledger row), the kitchen ticket, the print job.
- **Posting happens at one moment**: when the order is sent to the kitchen or bar (modes `PRINT`, `KDS`) or when it is paid (mode `DIRECT`). From then on a line is a folio item and a correction is a void (a reversal) with an approval.
- **Charge to room** is the balance transfer of document 30 (`TRF`, account 1215): the revenue, service and tax stay on the outlet folio and are recorded once; the guest folio gets one line "{outlet name} · R-0123" that opens the bill. A new **lock of the receiving folio** decides whether that is allowed at all, up to which amount, until when and for which outlets (R5).
- The server is in the cloud: the printers and the cash drawer are reached by a small **print agent** in the hotel that only makes outgoing calls (R10).
- The design does not build offline, but nothing in it makes it impossible (section 12).

## 1. Findings and conflicts (read this first)

| # | Finding | What the design does |
|---|---|---|
| F1 | **The UI reference** (`docs/design/10-pos-*` to `16-pos-*`, README section POS, merged to `main` in PR #34) is now in the repository and was read for this revision. | Section 11 follows the seven mockups. Where a mockup is richer than the first draft of this document it was added (floor-plan decoration, CSV import of products, ticket history, limit and used amount in the room lookup); where it differs from a decision, the decision wins and the difference is listed in 11.1. |
| F2 | **There is no realtime channel in the product.** No SSE, no WebSocket in `internal/` or `web/`. | Section 10 designs one (Server-Sent Events over `fetch`, an outbox table, resume by id). It is new infrastructure, built in PR 3 (the cashier screen of PR 2 polls on focus and after its own actions). |
| F3 | **Tax and service of an outlet (R2) are not what the engine resolves today.** `ChargeCalculationService` takes the rules mapped to the **charge code** (`billingconfig.ResolveRules`). "Rules follow the outlet, with an override per product" cannot be expressed with one code per product without a code per outlet and treatment. | Section 4.3 adds **rule profiles** and one optional field on the request to the same service (`RuleProfileID`). Still **only `chargecalc` multiplies** and only `ChargeCalculationService` resolves rules; a profile replaces the mapped rules of the code for that request, nothing else. Alternative A (a charge code per treatment, no engine change) is listed in section 15, D1. |
| F4 | **`folios` cannot hold an outlet bill.** `folio_type` is `GUEST`, `COMPANY`, `MASTER`; `reservation_id` is NOT NULL except for a MASTER (`folios_reservation_ck`). | Migration 00080 adds `OUTLET` with an `outlet_id`, as 00075 did for MASTER (section 3.1). |
| F5 | **`is_deposit` would misclassify payments of an outlet folio** (a folio with no stay: every payment is a deposit until it closes), the same problem document 30 5.2 solved for the MASTER. | The view `folio_item_gl` gets the MASTER branch for OUTLET too (a payment is a deposit only before the first CHARGE row). A row-by-row comparison test against the old view is part of PR 2, as in 00075. |
| F6 | **Document 30 4.2 asks `folio.reverse` plus an approval for every transfer between folios.** Splitting and merging bills of one outlet by that rule would need a supervisor's password for a waiter's action that moves no money between payers. | A new transfer scope **`OUTLET_BILL`** (both folios OUTLET, same outlet, both OPEN) needs `pos.cashier`, **no approval**, and is audited; net zero per account and department as every transfer. Moving between an outlet bill and any other folio is **not** this scope (D5). |
| F7 | **The scopes of the transfer (4.2) do not know "charge to room".** | A new scope **`OUTLET_CHARGE`** (outlet folio → a folio that is open for outlets): permission `pos.charge_room`, no approval, the lock check of section 6 replaces the approval. The scope rules of 4.2 are not asked again for a return of it. |
| F8 | **Routing (document 30 5.3) resolves by charge code.** A balance has no charge code. | Outlet charges look at the instructions of scope `ALL` only (the line's, then the group's); `ROOM` and `CHARGE_CODE` instructions never capture them (D6). The lock and limit checked are those of the **folio the charge lands on**, i.e. the master for a routed member (R5). |
| F9 | **The shift of a cashier is "the caller's open shift"** (document 11). A POS cashier can hold an open shift for a front-office drawer and one for the outlet's drawer. | `payments.shift_id` is chosen by the **drawer of the outlet** (`pos_outlets.drawer`): the existing rule "one open shift per cashier and per drawer" already allows it; `PaymentService` gets the drawer as an optional input. The night audit still refuses to run while a shift is open. |
| F10 | **A balance transfer from a folio with no stay lands on a guest folio with a stay**, and the guest is the room's; but on a **master** (no stay) the line must say whose it is. | The incoming line of an outlet charge to a master carries the guest's `stay_id` (document 30 13.1: "a charge posted to the master for a guest"), so the master's lines show guest and room. |
| F11 | **The `INTEGRATION` source says "an external system calculated this" and goes through `Verify`.** POS inside the platform calculates nothing itself. | A new `folio_items.source = 'POS'` (CHECK widened; `folio_items_source_ck` was widened for `TRANSFER` in 00062). `Verify` stays for a future external POS and is not used here. The structural guard of the sale paths (`salepath_guard_test.go`) learns the POS path. |
| F12 | **Approval kinds are a closed table** (document 32 4) and a structural test fails a use case that asks for an approval without a kind and an amount. | Four new kinds with their amounts (section 8.2). Existing kinds `FOLIO_REVERSAL`, `FOLIO_ADJUSTMENT` and `FOLIO_TRANSFER` are **not** reused for POS, so the owner can set a different rule for a void at the restaurant than for a void of a room charge. |
| F13 | **The module gate classifies routes** (document 26 3) and `POS` is already a reserved module code. The lock of a folio is used by the front desk, which is `CORE`. | The routes of the lock (open, change, close, list) are classified **POS** (R12: a tenant without the module sees none of it). The services stay ungated: check-out locks the folios of a stay whether or not the module is on, and the data stays when the module is switched off. |
| F14 | **Tax invoice (faktur pajak).** A charge to a room leaves the revenue on the outlet folio, so the faktur of that sale is the outlet bill's, not the guest's (the same limit as document 30 4.3). An OUTLET folio has no buyer. | POS prints a **receipt** (struk), not a tax invoice. A faktur from an outlet bill is not built; it needs the answer of the tax consultant (the open question of document 30 5.4), D9. |

## 2. Vocabulary

| Term | Meaning |
|---|---|
| Outlet | A place that sells: restaurant, bar, café, room service, spa, laundry, business center, minibar, other. Belongs to one property. |
| Bill | One `pos_bills` row and its `OUTLET` folio. The number (`R-0123`) is the folio number. |
| Order, draft | The `DRAFT` lines of a bill. Not in the ledger. |
| Round | One "send": all the draft lines posted together (and their tickets). A bill has rounds 1, 2, 3. |
| Ticket | What a station gets from a round: new lines, a change, or a void. |
| Station | A place of production (kitchen, bar, pastry): a printer and/or a KDS column. |
| Order mode | `PRINT` (tickets on a printer), `KDS` (tickets on a screen), `DIRECT` (no kitchen; posted when paid). |
| Folio lock for outlets | The permission of a folio (guest, company, master) to be charged by outlets: closed by default, opened by the front desk with a limit (R5). |
| Charge to room | Transfer of the balance of an outlet bill to a folio that is open for outlets. |
| Print agent | The program in the hotel that fetches print jobs and prints them. |

## 3. Data model

All tables carry `tenant_id` and `property_id` with **composite foreign keys** (no cross-tenant reference possible), the standard audit columns (`created_at`, `created_by`, `updated_at`, `updated_by`), and a `version` where an edit screen needs optimistic concurrency. Money is `numeric(18,3)`; dates are `date`; instants are `timestamptz` (UTC). Constraint names are mapped in `internal/platform/db/errors.go`.

### 3.1 Changes to existing tables (migration 00080)

| Table | Change |
|---|---|
| `folios` | `folio_type` accepts `OUTLET`; new `outlet_id bigint NULL` with FK `(property_id, outlet_id) → pos_outlets`; `CHECK ((folio_type = 'OUTLET') = (outlet_id IS NOT NULL))`; `folios_master_shape_ck`'s sibling `folios_outlet_shape_ck`: an OUTLET has no `stay_id`, no `reservation_id`, no `booking_group_id`, no `bill_to_company_id`; `folios_reservation_ck` becomes `folio_type IN ('MASTER','OUTLET') OR reservation_id IS NOT NULL`; `folios_payer_ck` is relaxed as for MASTER. Index `(property_id, outlet_id) WHERE status = 'OPEN'`. |
| `charge_codes` | `charge_type` accepts `ROUNDING` (system code `POS_ROUNDING`, section 7.4; created for every property by the migration and by `seed_charge_codes`, like `TRF` in 00074). Like `TRF`, the configuration refuses to map a tax or a service charge to it and it is not postable by hand. |
| `folio_items` | `source` accepts `POS`. No other column changes; the POS line is described by `reference_type = 'POS_BILL_LINE'` and `reference_id` = the line's uuid. |
| `payments` | `payment_method` accepts `COMP` (section 7.5, if D2 is approved as recommended), together with its clearing key in `gl_account_map` and the other CHECKs listed in `27-qris.md` for QRIS (`payments_method_ck`, `payments_mdr_ck`, `gl_account_map_key_ck`). It is **not** accepted by refunds, city ledger receipts, fee rules or settlements. |
| `folio_transfer_batches` | `scope` accepts `OUTLET_BILL` and `OUTLET_CHARGE`. |
| `companies` | `pos_billing_allowed boolean NOT NULL DEFAULT false`: a company may be charged by an outlet only when its account says so (D8). |
| `properties` | none. (Settings of POS are per outlet.) |
| `gl_account_map`, accounts | key `POS_ROUNDING` to account **4591 "Rounding"** (new properties from `usali_chart()`, existing ones by the data step with the replacement rule of 00071) and, if D2, key `COMP` to **6591 "Complimentary and house use"** (an expense). The code of an existing property may be taken: next free code, as 00071. |
| `folio_item_gl` (view) | the MASTER branch of the role-based `is_deposit` also covers `OUTLET` (F5). |
| `permissions` catalogue | the new permissions of section 8.1 (granted to **no role**, D11). |

Down: refused while an OUTLET folio, a `POS` item, a `COMP` payment or a `ROUNDING` row exists (as 00074 and 00075).

### 3.2 PR 1: masters

```
pos_outlets
  id, tenant_id, property_id
  code                 varchar(6)  UK (property_id, code)   -- the bill prefix: R, B, RS, LD; upper case, no digits; not FOL, PAY, RSV, STY, SHF
  name                 varchar(100)
  outlet_type          CHECK IN (RESTAURANT, BAR, CAFE, ROOM_SERVICE, SPA, LAUNDRY, BUSINESS_CENTER, MINIBAR, OTHER)
  order_mode           CHECK IN (PRINT, KDS, DIRECT)
  department_id        FK (property_id, department_id) → departments   -- a sub-department (12-departments.md); NOT NULL
  rule_profile_id      FK → pos_rule_profiles NOT NULL                 -- default tax/service of the outlet (4.3)
  price_list_id        FK → pos_price_lists   NOT NULL
  drawer               varchar(20) NULL                                 -- the cashier drawer label of document 11; NULL = no cash in this outlet
  receipt_printer_id   FK → pos_printers NULL                           -- also opens the drawer when pos_printers.opens_drawer
  uses_tables          boolean     -- false: takeaway and counter only
  guest_count_required boolean
  rounding_unit        smallint    CHECK IN (0, 100, 500, 1000)         -- 0 = none (7.4)
  allow_room_charge    boolean, allow_company_charge boolean, allow_comp boolean
  comp_treatment       CHECK IN (PAYMENT_COMP, DISCOUNT_100) DEFAULT 'PAYMENT_COMP'   -- 7.5 (D2); changing it is audited (old, new) and applies to bills opened afterwards
  max_discount_pct_no_approval numeric(5,2) DEFAULT 0                   -- D12
  block_night_audit_with_open_bills boolean DEFAULT false               -- D4
  next_bill_no         bigint DEFAULT 1                                 -- the sequence of the outlet (level 50)
  is_active, sort_order, version

pos_stations           (id, code UK per property, name, printer_id NULL, kds_enabled, late_after_minutes, is_active)
pos_printers           (id, name, kind RECEIPT|KITCHEN|LABEL, connection_info varchar(200) -- "192.168.1.50:9100" or "USB:EPSON TM-T82", used by the agent only,
                        paper_width_mm CHECK IN (58, 80), codepage varchar(20), opens_drawer boolean, device_id NULL → pos_devices (PR 3), is_active)
pos_rule_profiles      (id, code, name, price_mode CHECK IN (EXCLUSIVE, INCLUSIVE), is_active)
pos_rule_profile_rules (profile_id, rule_type TAX|SERVICE, tax_id NULL, service_charge_id NULL, sequence, CHECK one of the two)
pos_categories         (id, code, name, parent_id (one level), sort_order, is_active)
pos_products           (id, code UK per property, name, short_name varchar(24) -- on tickets, category_id, charge_code_id NOT NULL,
                        rule_profile_id NULL -- the override of 4.3, station_id NULL, is_active, version)
pos_option_groups      (id, product_id, name, kind VARIANT|MODIFIER, min_select, max_select CHECK (kind <> 'VARIANT' OR (max_select = 1)), sort_order)
pos_options            (id, group_id, name, price_delta numeric(18,3) -- may be negative for a variant, >= 0 for a modifier, is_default, max_qty smallint, is_active)
pos_price_lists        (id, code, name, is_active)
pos_price_list_items   (price_list_id, product_id, price numeric(18,3) >= 0, is_sold boolean, station_id NULL)  PK (price_list_id, product_id)
pos_outlet_sold_out    (outlet_id, product_id, business_date)  PK (outlet_id, product_id)  -- "sold out today": counts only while business_date = the open day
pos_recipe_lines       (product_id, option_id NULL, inventory_item_id bigint NULL -- no FK until PR 4, quantity numeric(12,4), unit varchar(10), note)
pos_areas              (id, outlet_id, name, sort_order, grid_w smallint, grid_h smallint, layout_version int)
pos_area_marks         (id, area_id, kind CHECK IN (WINDOW, DOOR, CASHIER, OTHER), label, x, y, w, h smallint)
pos_tables             (id, area_id, code UK per outlet, capacity, shape CHECK IN (SQUARE, RECT, ROUND), x, y, w, h smallint (grid units), is_active)
pos_table_holds        (id, table_id, business_date, from_time, party_size, name, phone, note, status HELD|SEATED|CANCELLED)   -- "dipesan"; not linked to PMS reservations
folio_outlet_locks, folio_outlet_lock_outlets, folio_outlet_lock_events, pos_room_charge_attempts   -- section 6
```

Rules that are decided here and not left to the code:

- **A product is mapped to a charge code** (revenue account and department come from it, as for every charge). The outlet's own sub-department wins over the charge code's department when the outlet names one (the department snapshotted on the item is the outlet's, so "Restoran" and "Bar" report apart even when both sell the code `RESTAURANT`); this is the only change to the department resolution of document 12 and it is made in the POS path, not in `FolioPostingService`'s general rule.
- **Price per outlet** is the price list of the outlet; a product with no item, or `is_sold = false`, is not offered there (R2). The price of a line is the list price plus the deltas of the options picked, **snapshotted on the line**; a later price change never touches a bill.
- **Sold out today** is a row dated by the open business day: it clears itself when the day changes (no job), and it is checked when a line is added and when a round is sent (a read, not a lock).
- **Recipes**: `pos_recipe_lines` is created now so that the inventory module (PR 4, its own document) adds only the foreign key and the stock movements. No endpoint and no screen in PR 1 to 3.
- **Deleting**: a product, outlet, table or station that has ever been used is switched off, never deleted (foreign keys), as departments are.
- **Bill number**: `pos_outlets.next_bill_no` is incremented by `UPDATE … RETURNING` at level 50 (short, last), and the number is `{code}-{n padded to 4}`; it is also the `folios.folio_number` of the bill (`UK (property_id, folio_number)` already holds it unique across outlets because the codes are unique and cannot be a prefix used by another sequence).

### 3.3 PR 2: bills, lines, rounds, tickets, room charges

```
pos_bills
  id, tenant_id, property_id, outlet_id
  client_uuid        uuid NOT NULL   UK (property_id, client_uuid)      -- from the client (R11)
  folio_id           bigint NULL     FK → folios (UK)                    -- NULL only for a bill made on a device that has not synced yet (section 12)
  bill_number        varchar(20) NULL                                    -- = folios.folio_number
  status             CHECK IN (OPEN, CLOSED, VOID, MERGED)
  order_type         CHECK IN (DINE_IN, TAKEAWAY, ROOM_SERVICE, DIRECT)
  table_id           NULL FK → pos_tables;  CHECK (order_type <> 'DINE_IN' OR table_id IS NOT NULL OR outlet.uses_tables = false)
  guest_count        smallint NULL
  target_stay_id     bigint NULL FK → stays   -- room service: the room it is for; a hint for the screen, not a link of money
  customer_name      varchar(100) NULL
  waiter_id          bigint FK → users
  comp_treatment     CHECK IN (PAYMENT_COMP, DISCOUNT_100) NOT NULL   -- copied from the outlet when the bill is opened; an open bill keeps it (7.5)
  business_date_opened date, opened_at, bill_requested_at NULL, closed_at, closed_business_date NULL
  merged_into_bill_id NULL, note, version

pos_bill_lines
  id, bill_id, client_uuid uuid UK (bill_id, client_uuid)
  product_id, product_name, charge_code_id, rule_profile_id        -- snapshots
  options jsonb              -- [{option_id, name, price_delta, qty}] snapshot
  quantity numeric(10,3) > 0, unit_price numeric(18,3), discount_pct NULL, discount_amount numeric(18,3) DEFAULT 0
  note varchar(200), station_id NULL
  status CHECK IN (DRAFT, SENT, VOID)
  round_id NULL, folio_item_id NULL (UK)      -- set when posted; (status = 'DRAFT') = (folio_item_id IS NULL)
  voided_item_id NULL                          -- the REVERSAL item (or adjustment) that voided it
  created_by, created_at, sent_by, sent_at, voided_by, voided_at, void_reason
  -- trigger: a SENT or VOID line is immutable except SENT → VOID; no DELETE of a non-DRAFT line

pos_rounds        (id, bill_id, round_no, client_uuid UK, sent_by, sent_at)  UK (bill_id, round_no)
pos_tickets       (id, round_id, bill_id, station_id, kind NEW|CHANGE|VOID, status NEW|COOKING|READY|SERVED|CANCELLED,
                   created_at, started_at, ready_at, served_at, print_job_id NULL)
pos_ticket_lines  (ticket_id, bill_line_id, quantity, name, options_text, note, is_void)
pos_room_charges  (id, bill_id, from_folio_id, to_folio_id, stay_id NULL, outlet_id, amount > 0, lock_period_no,
                   transfer_batch_id UK → folio_transfer_batches, returned_by_transfer_id NULL,
                   business_date, idempotency_key UK per property, created_by, created_at)
```

- **The line is a row, not an element of an array on the bill**, so two waiters adding to one bill never overwrite each other. Line order is `(created_at, client_uuid)`; there is no server-assigned line number (R11).
- `pos_room_charges` is the one new table that holds rows dated by the business day and counted for a limit: it gets the **`business_day_must_be_open` trigger** (as city ledger receipts did in 00065), and its row is written in the same transaction as the `TRF` pair. `pos_bills`, lines, rounds, tickets, attempts and lock events are operational records (the rule of CLAUDE.md does not cover them; the money they cause is in `folio_items` and `payments`, which are covered).
- The **ledger rows of a POS bill carry `folios.outlet_id`** (through the folio), so every report by outlet is a query over `folio_items`, never a second set of totals.

### 3.4 PR 3: devices, print jobs, events

```
pos_devices       (id, name, kind AGENT, token_hash char(64) UK, token_prefix char(8), status ACTIVE|REVOKED, last_seen_at, agent_version,
                   enrolled_by, created_at, revoked_at, revoked_by)
pos_device_enrollments (id, code_hash, expires_at, used_at NULL, created_by)         -- one-time, 15 minutes
pos_print_jobs    (id uuid, printer_id, kind RECEIPT|TICKET|DRAWER|REPORT, source_type, source_id, dedupe_key UK,
                   payload bytea NULL (ESC/POS, <= 64 KB; purged after 7 days), copy_no smallint, reprint_of NULL,
                   status QUEUED|CLAIMED|PRINTED|FAILED|CANCELLED, attempts, max_attempts DEFAULT 3, lease_until, claimed_by NULL,
                   error_code, created_at, created_by, printed_at)   IDX (printer_id, status, created_at)
pos_events        (id bigserial, property_id, channel varchar(40), type varchar(40), entity_id bigint, payload jsonb, at)    -- outbox, 48 h
```

## 4. Posting: how an order becomes ledger rows

### 4.1 The paths (the single-path rules stay)

| Rule | Still true |
|---|---|
| Only `chargecalc` multiplies by a rate | yes: POS passes `quantity`, `unit_price`, `discount`, the charge code and the rule profile to `ChargeCalculationService`; it computes no tax or service |
| Only `FolioPostingService` inserts `folio_items` | yes: `PostCharge` (source `POS`), `PostAdjustment`, `Reverse` |
| Only `PaymentService` writes `payments` | yes |
| Transfers go through the transfer log (document 30) | yes: charge to room is `transfer-balance`, split and merge are `transfer-items` |
| `ROOM` and `TRANSFER` codes are not postable by hand | the same guard refuses `ROUNDING` (`ROUNDING_CODE_NOT_MANUAL`); only the POS payment path posts it |

POS adds one package, `internal/pos`, with the usual layout (`model.go` pure rules, `service.go` use cases, `store.go` + `queries.sql`, `http.go`), and two small ones: `internal/pos/printing` (renders ESC/POS, queues jobs) and `internal/pos/realtime` (outbox and stream). `internal/folios` gets the new transfer scopes, the folio type, the lock gate (`OutletChargeGate`) and the check-out hook; the calculation request gets `RuleProfileID`.

### 4.2 When

| Event | What is written | Ledger |
|---|---|---|
| Add, change, delete a draft line; move table; merge drafts | `pos_bill_lines` (DRAFT) | **none** |
| **Send** (`PRINT`, `KDS`) | the draft lines of the bill become SENT: each is posted (`PostCharge`), a round, one ticket per station, print jobs or KDS rows, events | one CHARGE item per line (with components) |
| **Pay** (`DIRECT`) | the same posting, then the payment(s), then the close | CHARGE items, PAYMENT items |
| Void a sent line | the line becomes VOID, a ticket of kind VOID | a REVERSAL of its item (same business day) or a negative ADJUSTMENT (earlier day), with approval |
| Discount after the send | `PostAdjustment` per line | ADJUSTMENT items |
| Pay, charge to room, charge to a company | payments, a `TRF` pair, a `CITY_LEDGER` payment | PAYMENT items, `TRF` pair |

A line posted to the ledger carries `service_date = the open business day`, never an earlier date; the `business_day_must_be_open` trigger is behind the application gate as for every posting.

### 4.3 Rule profiles (F3)

A **rule profile** is a named list of tax and service rules in an order, plus a price mode (EXCLUSIVE or INCLUSIVE). The outlet has a default profile; a product may name another (for example "no service charge" for bottled water, or "tax only" for laundry). The profile in force for a line is `product.rule_profile_id ?? outlet.rule_profile_id` and is **snapshotted on the line**.

- `ChargeRequest` gains `RuleProfileID *int64`. When set, `ChargeCalculationService` loads the rules of that profile **instead of** the rules mapped to the charge code, and takes the price mode from it; the charge code still decides the revenue account and the department. The components are snapshotted on the item exactly as today (code, name, rate, `tax_on_service`, base, amount, GL account), so editing a profile later rewrites nothing.
- A profile may not contain a rule that is inactive or of another property. A code of type `TRANSFER`, `ROUNDING` or an exempt code ignores any profile (no rules) as it ignores the mapped ones.
- The estimate on the order screen is `POST /pos/price-quote` (the same service, `DryRun`), **never computed in the client** (the rule of document 28 for the estimate).

## 5. Bills, tables and the order

### 5.1 Status

```
bill:   OPEN ──pay to zero──▶ CLOSED ──folio.reopen (+approval)──▶ OPEN
          │ no posted line, no payment
          ├──────────────▶ VOID
          └──merge──────────▶ MERGED (into another bill; its folio closes at zero)

line:   DRAFT ──send / pay(DIRECT)──▶ SENT ──void (+approval)──▶ VOID
        DRAFT ──delete──▶ (row removed)

ticket: NEW ──▶ COOKING ──▶ READY ──▶ SERVED           (any ──▶ CANCELLED when its line is voided before SERVED)
table (derived, never stored): FREE | OCCUPIED (an OPEN bill) | AWAITING_PAYMENT (a bill with bill_requested_at) | HELD (a hold within the next 60 minutes)
```

A bill is **CLOSED when the folio balance is exactly zero after a payment or a transfer** (R4); the same transaction closes the folio (`closed_on`, as every folio). A bill with a credit balance (an overpayment) is refunded with the existing refund flow before it closes.

### 5.2 Locks and concurrency (no new lock level)

**The lock of a bill is its folio row (level 42).** A bill without a folio (section 12) has no ledger and needs no lock. So POS adds no level to the global order of `05-transactions-locking.md`; the order for every POS use case is

`business day (10, share) → [room types, rooms, reservations: never] → stays (41, only to key-share the target of a charge to room) → folios ascending (42) → payments / cashier shift (43) → companies (44) → … → sequences (50)`.

| Use case | Locks |
|---|---|
| Open bill | day (10, share) → outlet row read → sequence (50, `next_bill_no`) → insert folio and bill |
| Add / change / delete a draft line, move table | day (10, share) → the bill's folio **FOR SHARE** (42): the bill must be OPEN, so a close cannot slip in |
| Send round | day (10, share) → folio FOR UPDATE (42) → post lines (the engine, no further locks) → rounds, tickets, jobs |
| Void a line | day → folio FOR UPDATE (42); the approval is verified **before** the transaction (as document 32 does) |
| Pay (several methods, with a charge to room or a company) | day → **stay of the target** (41, `FOR SHARE`, so a check-out waits instead of deadlocking: the cycle of F-06 in 05 1.1) → **all folios of the request, ascending, in one call**: the outlet bill and the target (42) → payments (43, each cash payment then the shift in share mode) → the company (44) → sequences (50) |
| Split / merge | day → the folios involved ascending (42); the new bill is **made first in its own transaction** (it takes a number, level 50), then the transfer in a second one |
| Change a lock of a folio | day → folio FOR UPDATE (42) |

Why the folio of the **target** is taken in the same call as the bill's folio: a charge to room reads the lock and the amount already used and writes the `TRF` pair and `pos_room_charges`; two outlets charging the same room both need the target FOR UPDATE, so they take turns, and the second reads what the first committed (the race of R5, section 6.4).

**Two waiters adding to one bill**: both inserts succeed (different rows, `client_uuid` each, folio FOR SHARE is shared); a send that runs at the same time takes FOR UPDATE, waits for the adds in flight, and posts what it sees; a line committed after it stays DRAFT for the next round. **A pay and an add**: the add waits for the pay or, if the pay closed the folio, answers `BILL_NOT_OPEN`; the line is not lost silently, the screen says so.

### 5.3 Table operations

- **Open**: a table (or none: takeaway, counter), the number of guests (required when the outlet says so), the waiter is the user.
- **Several open bills on a table are allowed** (separate guests); the plan shows the table occupied with the count.
- **Move table**: sets `table_id`; no ledger. A ticket already printed is not reprinted; the KDS shows the new table (the ticket reads the bill).
- **Merge** (B into A): same outlet, both OPEN. DRAFT lines are re-pointed to A (no ledger). SENT lines are **transferred by item** from B's folio to A's with the existing `transferOne` (a reversal on B and a copy on A with the snapshot of the original: amounts, components, revenue account, department, service date; document 30 4.1), scope `OUTLET_BILL`, one batch, one transaction, at most 200 items (`TOO_MANY_ITEMS`). **No charge is split and nothing is recomputed.** B's balance is zero, B becomes `MERGED` and its folio closes. Payments already on B block the merge (`BILL_HAS_PAYMENTS`: void or refund first).
- **Split** (A into A and A'):
  - *per item*: the new bill A' is made empty, then the chosen SENT lines are transferred by item (as above) and the chosen DRAFT lines re-pointed. Each bill now has its own receipt and its own payer. Example 9.6.
  - *equal share* (rata) and *by amount* (nominal): **no folio is split**. They are payments of one bill: N payments of `total / N` (the last takes the remainder at the currency decimals), or any amounts, each labelled "Guest 1/3" in `payments.remarks`. The bill closes when the balance is zero. A separate receipt per payment is printed.
- **Request the bill** sets `bill_requested_at` (the table turns AWAITING_PAYMENT) and prints the pre-bill (a RECEIPT job marked "NOT A RECEIPT").

## 6. The lock of a folio for outlet charges (R5)

### 6.1 Model

```
folio_outlet_locks          -- one row per folio that was ever opened; NO row = LOCKED (the default, R5)
  folio_id PK, tenant_id, property_id
  status          CHECK IN (OPEN, LOCKED)
  limit_amount    numeric(18,3) NULL; CHECK ((status = 'OPEN') = (limit_amount IS NOT NULL AND limit_amount > 0))
  valid_mode      CHECK IN (UNTIL_CLOSE, TODAY)     -- UNTIL_CLOSE = until check-out (guest, company folio) or until the master closes
  valid_on        date NULL (the business date it was opened, for TODAY)
  period_no       int NOT NULL                       -- +1 at every opening: usage is counted per period
  locked_reason   NULL | MANUAL | LIMIT_REACHED | CHECKOUT | EXPIRED | FOLIO_CLOSED
  opened_by, opened_at, updated_by, updated_at, version

folio_outlet_lock_outlets   (folio_id, outlet_id)   -- the outlets that may charge; at least one while OPEN
folio_outlet_lock_events    -- append-only: OPENED, CHANGED, LOCKED (with the reason), each with actor, old and new values, business date
pos_room_charge_attempts    (id, source CHECK IN (CHARGE, MOVE), folio_id (the folio the charge would land on), requested_stay_id NULL, outlet_id, bill_id,
                             amount, result ACCEPTED|REJECTED, code NULL, context jsonb, pos_room_charge_id NULL,
                             business_date, created_at, created_by)
```

- **The used amount is derived, not stored** (the sum of the outlet charges to the folio less those returned or moved away through POS, never the folio's balance): `sum(amount) FROM pos_room_charges WHERE to_folio_id = F AND lock_period_no = period_no AND returned_by_transfer_id IS NULL`. It is read under the folio's FOR UPDATE lock, which is the one that serialises every charge on that folio.
- A change of the lock (open, change the limit, the outlets, the validity, lock) is a use case that **locks the folio FOR UPDATE first** (level 42), so it cannot interleave with a charge. Opening increments `period_no`: an old period never counts against a new limit (the front desk is deliberately extending credit again). Changing the limit of an open lock keeps the period (the used amount counts; a limit below the used amount is refused, `LIMIT_BELOW_USED`).
- **Audit**: every open, change and lock writes `audit.Writer.Write` (`folio.outlet_lock_opened`, `_changed`, `_locked`) in the same transaction and a row in `folio_outlet_lock_events`; the automatic locks (limit reached, check-out, expiry) are audited with the `system` actor and the reason.

### 6.2 What may be opened

A folio is **open-able** when it is OPEN and is one of: the GUEST folio of an in-house stay, a COMPANY folio of an in-house stay, a MASTER folio of an active group. Not a closed folio, not the unlinked deposit folio, not an OUTLET folio. The master of a group follows the same rules and is opened by the holder of the same permission (R5).

### 6.3 The decision of a charge to room

Input: the bill, the room chosen by the cashier (a stay), the amount X (≤ the bill's balance). The server decides in this order and stops at the first failure; **every failure writes a `pos_room_charge_attempts` row** (REJECTED) and nothing else:

1. **Target**: resolve the folio the charge lands on, with the `ALL` instructions of the line and then the group (F8), else the stay's GUEST folio. A routed target whose master is closed answers `ROUTING_TARGET_CLOSED` (the existing code; no fallback to the guest, as in document 18 3.7).
2. The outlet allows room charges (`allow_room_charge`), and the property's subscription includes POS (the route gate).
3. The target is OPEN: else `FOLIO_CLOSED`.
4. There is a lock row with `status = OPEN`: else **`OUTLET_FOLIO_LOCKED`**, `context`: `target_folio_id`, `reason` (`NEVER_OPENED`, `MANUAL`, `LIMIT_REACHED`, `CHECKOUT`, `EXPIRED`), `target` (`GUEST`, `COMPANY`, `MASTER`).
5. Not expired: `valid_mode = TODAY` and `valid_on < the open business date` → the lock is **written LOCKED (EXPIRED) in this transaction** and the answer is `OUTLET_FOLIO_LOCKED`, reason `EXPIRED`. (The night audit also writes it at the close, so the screens are right at 06:00; the check is lazy so that nothing depends on the audit having run.)
6. The outlet is in `folio_outlet_lock_outlets`: else **`OUTLET_NOT_ALLOWED`**.
7. `used + X ≤ limit`: else **`OUTLET_LIMIT_EXCEEDED`**, `context`: `limit`, `used`, `remaining`, `requested`. The folio **stays open**: a smaller part may still be charged (the screen offers "charge `remaining` to the room, the rest another way").
8. Accepted: the `TRF` pair is posted (document 30 4.3: ADJUSTMENT −X on the outlet folio, CHARGE +X on the target, description "{outlet name} · R-0123"), `pos_room_charges` gets its row, the attempt row is ACCEPTED. **If `used + X = limit` the lock becomes LOCKED (LIMIT_REACHED) in the same transaction**: the next charge is refused for that reason.

The same decision is taken by **both outlet-originated actions**: the charge to room and the move of 7.6; the attempts table says which (`source CHARGE|MOVE`). A front-office balance transfer does not take it (7.6).

Because a refused attempt must be written although the use case refuses, the use case returns a **result** of kind `REFUSED` (commit) that the handler turns into the 409 with the code: the attempt rows are the only writes of that transaction.

### 6.4 The race, stated

Two outlets (A at the restaurant, B at the bar) charge 60,000 each to a folio with limit 100,000 and used 0, at the same moment. Both transactions take the target folio FOR UPDATE; one waits. The first reads used 0, accepts, commits (used 60,000). The second then reads used 60,000, finds 120,000 > 100,000 and answers `OUTLET_LIMIT_EXCEEDED` (remaining 40,000). There is no read of `used` outside the folio lock, so the limit cannot be passed by concurrency. The test of section 14 asserts exactly one accepted and the sum ≤ limit, 20 times in a loop.

### 6.5 Opening, the check-in panel, the list

- **Open for outlets** is a dialog on the folio (and on the room panel and the master folio screen): the limit (**required**, no default, no "unlimited"), valid until (check-out / today only), the outlets (a checklist of the outlets of the property, at least one). Permission **`folio.outlet_charge_open`**, which also closes and changes it (R5 asks "izin baru").
- **Check-in panel (document 28)**: after the deposit step, an option "Open for outlets" with the same three fields, shown only when the module is on and the desk holds the permission. It is the **fifth request of the sheet**: deposit, check-in, then `PUT /folios/{id}/outlet-lock` (the stay's folio exists only after the check-in), with its own `Idempotency-Key`. If it fails after the check-in, the Done step shows a banner with "Try again"; **it never blocks or undoes the check-in**, as a failed deposit never undid one.
- **Auto lock at check-out**: `CloseStayFolios` (the folios of the stay) writes the lock LOCKED (CHECKOUT) for every folio of the stay that had an OPEN lock, in the same transaction. Closing the master does the same. This is a service-level effect, not a route, so it runs whether or not POS is in the plan. If bills are still open for the room (`pos_bills.target_stay_id`), the check-out result carries a **warning** with them (D8).
- **What the cashier sees** (`GET /pos/room-lookup?q=`): in-house rooms matching the number or the guest's name, each with the folio it would land on (own folio or "master of group X"), the lock (`OPEN` with `remaining`, or `LOCKED` with the reason and "ask reception to open it"), and nothing else: **no balance, no folio lines**. Permission `pos.charge_room`. The server still decides (6.3); the lookup is advice.
- **"Outlet charges today"** (`GET /outlet-charges?date=`, front desk): every attempt of the business date, accepted and rejected, per room: time, outlet, bill, amount, result and code, remaining limit, with live updates (event `outlet_charge.attempted` on channel `frontdesk`), and a row action "Open / change lock" for the rooms with a rejected attempt. Permission `folio.outlet_charge_open` or `folio.read`.

## 7. Payments, discounts, rounding, comp

### 7.1 Methods on a bill

One request pays a bill with **one or more** payments: `POST /pos/bills/{id}/pay` with `payments[]` of `{method, amount, reference?, label?}` and at most one `charge_room` and one `charge_company`. All are in **one transaction** (all or nothing), under one `Idempotency-Key` (each payment's own key is `K#i`). The sum must equal the balance after rounding (7.4); an overpayment in cash is change, **not** a credit balance (the cashier enters tendered cash; the posted CASH payment is the amount due, the change is shown and printed only).

| Method | Ledger |
|---|---|
| CASH | PAYMENT on the bill's folio, `payments.shift_id` = the open shift of the cashier **for the outlet's drawer** (F9), `NO_OPEN_SHIFT` otherwise; opens the drawer (a DRAWER print job) |
| CARD, QRIS, BANK_TRANSFER, OTHER | PAYMENT; QRIS and CARD take the fee snapshot rules of documents 15 and 27 unchanged |
| Charge to room | the `TRF` pair of 6.3 |
| Company | a `CITY_LEDGER` payment (the transfer of `folios.Transfer`: company credit-limit gate under the company lock, `TRANSFER_COMPANY_MISMATCH` does not apply because an OUTLET folio has no company), requires `cityledger.transfer`, `outlets.allow_company_charge` and `companies.pos_billing_allowed` (D8); invoiceable at once (it has no stay to wait for, as a master with all rooms over) |
| Walk-in guest | CASH/CARD/QRIS as above; nothing else exists to "register" (R4) |
| Complimentary | method `COMP`, 7.5 |

### 7.2 Journal of a payment on an outlet folio

Identical to a folio payment: Dr the method's account (1110 cash, 1150 card, 1155 QRIS, 1130 bank, 1160 other, 1220 city ledger), Cr 1210 guest ledger, at the day close, grouped by method. A POS cash payment is drawer cash of the outlet's drawer on the Z report of the shift (document 11 6a); card and QRIS are other tenders on the same report.

### 7.3 Discounts (R4)

- **Item discount before the send**: `discount_pct` or `discount_amount` on the DRAFT line; at posting it is the `Discount` of the request (0 ≤ discount ≤ |base|, document 03 7.6). **The engine computes the tax on the discounted net**, so there is no second tax path.
- **Bill discount before the send**: a percent or an amount of the draft total, **allocated to the lines by base** with the largest-remainder method at the currency decimals so that the parts sum to the amount exactly, then applied as item discounts. Example in 9.9.
- **Discount after the send** (some lines already posted): one **negative ADJUSTMENT per posted line** through `PostAdjustment` (the engine on a signed base: service and tax reduce in proportion), same business day or later, reason required, approval of kind `POS_DISCOUNT`. The result equals the same discount applied before the send (9.9).
- A discount up to `max_discount_pct_no_approval` of the bill's base, by the holder of `pos.discount`, needs no approver's credentials (default 0: always an approval, D12); above it the `POS_DISCOUNT` approval and the rule of document 32 apply.
- Discounts are reported by who gave and who approved (the line, the audit entry).

### 7.4 Rounding

- `pos_outlets.rounding_unit` (0, 100, 500, 1000): the **payable** is the balance rounded **to the nearest unit** (half up) when the bill is settled and the **cash** part is the whole payable, or when only cash is left to pay. It is shown on the screen before the payment, never applied to a card or QRIS amount.
- It is recorded as **one ADJUSTMENT (or CHARGE when rounding up) of the system code `POS_ROUNDING`** of ±r on the same folio, source `POS`: **no tax and no service** (the code has no rules, as `TRF` and `ROOM_EXEMPT` have none, so the same engine returns zero components; the configuration refuses to map one). Its revenue account is 4591 "Rounding", a revenue account of the department of the outlet. So there is **no second tax path**: the tax and service of the bill are exactly those of its lines, and the rounding is a separate line that carries none.
- Whether the rounding changes the tax base in the eyes of the tax office is a question for the consultant (D3); the design records it so that it can be turned off per outlet (`rounding_unit = 0`).

### 7.5 Complimentary (R4, D2): a setting of the outlet

`pos_outlets.comp_treatment`, "Perlakuan komplimen" in the outlet's setup, is one of two; the bill **copies it when it is opened and keeps it** (`pos_bills.comp_treatment`), so a change of the setting applies to the bills opened afterwards and never to an open one. Every change is audited with the old and the new value (`pos.outlet_comp_treatment_changed`). In **both** treatments a complimentary needs `pos.comp` on the person, the approval of kind **`POS_COMP`** (8.2), a reason, and the name of the guest or of the authorising manager; the outlet must allow it (`allow_comp`).

| | `PAYMENT_COMP` (default) | `DISCOUNT_100` |
|---|---|---|
| Mechanism | a **payment of method `COMP`** for the comped part of the balance | a **100% discount** on the item(s) or the bill |
| Before the send | the lines are posted at full value at the send; the COMP payment follows | the line's `discount_amount` is its whole base: the engine returns net 0, service 0, tax 0, so the item is posted with a zero total |
| After the send | the same | one negative ADJUSTMENT per posted line through `PostAdjustment` (7.3), reference `POS_COMP`; it reverses the net, the service and the tax of the line in full |
| Revenue, service, tax | **recorded as if sold** | **none** |
| Expense | the value goes to the **complimentary expense account** (system key `COMP`, a row of Account mapping, default 6591 "Complimentary and house use") | **none** |
| Tax line of the books | the VAT/PB1 of the sale is declared as for any sale | nothing is declared, because the engine computed it on a discounted net of zero |
| Report | counted as comp (value with tax, base, by whom, approver, reason) | counted as comp from the discount lines marked `POS_COMP` (value of what was given away at list price, with tax) |

**There is no second tax path in either mode.** In `PAYMENT_COMP` the tax is the one of the lines and the COMP payment is a payment like any other (it settles the guest ledger against an expense account). In `DISCOUNT_100` the tax is **zero because the engine computed it on the discounted net**, exactly as for any discount (7.3); nothing is reduced by hand and no line is exempted from a rule. The **report of the outlet shows comps in both modes** (a "Complimentary" block with count, value at list price including tax, and by approver), so the two treatments are comparable even though only one of them reaches the expense account.

**To be confirmed with the tax consultant (konsultan pajak) before either mode is used by a PKP property:** whether the VAT/PB1 on a complimentary item is due (what `PAYMENT_COMP` assumes) or not (what `DISCOUNT_100` assumes). The design makes both available because the answer may differ by outlet and by the nature of the comp; it does not decide it.

### 7.6 Voids and corrections (R6)

| Situation | What the server does | Approval |
|---|---|---|
| Void of a sent line, the **same business day**, bill OPEN | `folios.Reverse` of its item (a REVERSAL: debit and credit swapped, components negated, department copied), line VOID, a VOID ticket | `POS_VOID` |
| Same, **earlier business day** (the day is closed) | a negative ADJUSTMENT through the engine with the line's code and profile, today's date; revenue reduces **today** | `POS_VOID` |
| The bill is CLOSED, same business day | **reopen** (`folio.reopen` + `POS_REOPEN`), then void or refund, then pay again; a payment of the same day is voided (`PaymentService.Void`), an earlier one is refunded | `POS_REOPEN`, then the existing kinds for the payment |
| The bill is CLOSED, the day is closed | reopen, then adjustments in the current day (never back-posting, document 04 rule), then pay again | `POS_REOPEN`, `POS_VOID` |
| Already **charged to a room** | **Return** the `TRF` pair (document 30 4.5; the folio must be OPEN, so reopen the bill first), correct, charge again; or **move the charge to the right room** (`POST /pos/room-charges/{id}/move`): a return and a new charge in **one transaction**, where the new charge goes through **every check of 6.3 at the target** (lock, validity, allowed outlets, limit); if any fails nothing moves and the first room keeps the charge | `POS_REOPEN` and `FOLIO_TRANSFER` (the return), then the checks of 6.3 |

A void of a line whose ticket was SERVED is allowed (with the approval) and the ticket shows it struck through; `TRANSFER_COPY_USE_RETURN` of document 30 12.1 still refuses reversing the copy of a transfer on the guest folio: the way back is the return.

**The check applies to what comes from an outlet, with no exception (D21).** The lock, the validity, the allowed outlets and the limit are checked on the target folio (or the master when routed) by the two actions that **originate in an outlet**: a new charge to room, and the **move** of a charge (`POST /pos/room-charges/{id}/move`, a return and a new charge in one transaction: if the check at the target fails nothing moves). They are checked **for every person, including the holder of `folio.transfer_any`**; that permission widens where a front-office transfer may go (document 30 4.2) and never opens a folio for outlets.

**An ordinary balance transfer between folios by the front office (`TRF`, document 30) is not checked against the lock and is not counted against the limit at the target.** The balance of a folio is fungible (document 30 4.3: after it was paid down, which part came from where has no answer), so no attribution is attempted. The reason is that such a transfer is always **a deliberate act of the receptionist, with an approval** (`folio.reverse` and the approval kind `FOLIO_TRANSFER`, `folio.transfer_any` for an unrelated folio), and is audited with both folios and the approver; it is not an outlet acting on its own, which is what the lock exists to restrain. If a receptionist moves a balance to a room whose lock is closed, they have decided to.

**What "used" means.** The amount used against a limit is **the sum of the outlet charges charged to that folio (`pos_room_charges`, current period) less those returned or moved away through POS** (a return of document 30 4.5 of the pair, or the move above). It is **not the balance of the folio**: payments, other charges and front-office transfers never change it.

## 8. Permissions and approvals

### 8.1 Permissions (new; granted to **no role** by the migration, D11)

| Permission | Who | Allows |
|---|---|---|
| `pos.manage` | setup | outlets, stations, products, options, price lists, rule profiles, floor plans, printers, devices; read all of POS |
| `pos.order` | waiter | open bills, add, change and delete draft lines, send, move table, request the bill, mark a product sold out |
| `pos.cashier` | cashier | everything a waiter does, plus pay, close, split, merge, print receipt, no-sale drawer open, the outlet's bill list and X report |
| `pos.charge_room` | cashier | the room lookup and charge to room (a waiter on a tablet may hold it without `pos.cashier`) |
| `pos.discount` | cashier, supervisor | item and bill discounts (subject to 7.3) |
| `pos.comp` | supervisor | complimentary |
| `pos.void` | cashier, supervisor | void a sent line (subject to the approval) |
| `pos.approve` | supervisor | the **approver's** permission for `POS_VOID`, `POS_DISCOUNT`, `POS_COMP`, `POS_REOPEN` (the "permission its approver must hold" column of document 32 2) |
| `pos.kds` | kitchen | the kitchen display and the status of tickets |
| `pos.report` | manager | the reports of 13 |
| `folio.outlet_charge_open` | front desk | open, change and lock a folio for outlets; the list of incoming charges |

A reopen needs the existing `folio.reopen` **and** the approval kind; the existing permissions `payment.post`, `payment.refund`-style rights, `cityledger.transfer` are asked by the use cases that call those services, unchanged.

The reads of the reference data follow document 33: the endpoint asks for the permission that manages the data **or** one of the permissions of the screens that use it (the catalogue and floor plan are readable by `pos.manage`, `pos.order`, `pos.cashier`, `pos.kds`; the outlets list also by `folio.outlet_charge_open`, for the checklist of the lock dialog). Menu and router carry the manage permission in `meta.permission`, and the scan test of `openPagesReads.test.ts` covers the new pages.

### 8.2 New approval kinds (document 32 2)

| Kind (label) | Use cases | Amount | Approver's permission |
|---|---|---|---|
| `POS_VOID` (void of a sale line) | `pos.VoidLine`, `pos.VoidBill` | the **total with tax and service** of the lines voided in the request | `pos.approve` |
| `POS_DISCOUNT` (discount) | `pos.ApplyDiscount` (before or after the send) | the **base amount discounted**, `abs`, added over the lines of the request | `pos.approve` |
| `POS_COMP` (complimentary) | `pos.PayComp` | the **total with tax and service** that is comped | `pos.approve` |
| `POS_REOPEN` (reopening of a bill) | `pos.ReopenBill` | the **sum of the charges** of the bill (its total sold) | `pos.approve` |

All four take `FREE`, `DISTINCT` and (they have an amount) `DISTINCT_ABOVE` like any other kind, per property. The structural test of document 32 4 is extended by the new package; each use case adds `AuditFields()` to its entry. An existing kind is still asked by the existing path: voiding a payment is `FOLIO_REVERSAL`, a refund `REFUND`, a transfer to an unrelated folio `FOLIO_TRANSFER`.

## 9. Flows with numbers

Conventions: IDR (0 decimals); service charge 10% on net; tax 11% on net **plus** service (`on_service`), as in document 03 7.5; prices EXCLUSIVE; no rounding unless said. Accounts: 1110 cash, 1150 card, 1155 QRIS, 1210 guest ledger, 1215 transfer between folios, 1220 city ledger, 2410 tax payable, 2430 service payable, 4210 food and beverage revenue (department "Restoran" or "Bar"), 4350 laundry, 4590 other revenue, 4591 rounding. "Journal" is what `DayActivity` writes at the close of the business day, grouped by account and department; the **reconciliation** is the comparison of the control accounts with the folios (document 31 5).

### 9.1 Restaurant bill, 2 guests, kitchen, one void, cash + room (R-0123)

Outlet RESTO (mode PRINT, rule profile "F&B": service 10%, tax 11% on service), table T5, 2 guests, room 305 in house with an open lock (limit 500,000, until check-out, outlets RESTO and BAR).

| Line | Qty × price | Net | Service | Tax | Total |
|---|---:|---:|---:|---:|---:|
| L1 Nasi goreng | 2 × 50,000 | 100,000 | 10,000 | 12,100 | 122,100 |
| L2 Steak | 1 × 150,000 | 150,000 | 15,000 | 18,150 | 183,150 |
| L3 Es teh | 2 × 15,000 | 30,000 | 3,000 | 3,630 | 36,630 |

**Folio R-0123 (the outlet folio):**

| # | Item | Debit | Credit | Note |
|---|---|---:|---:|---|
| 1 | CHARGE L1 (RESTAURANT, source POS) | 122,100 | | round 1 (kitchen: L1, L2; bar: L3) |
| 2 | CHARGE L2 | 183,150 | | |
| 3 | CHARGE L3 | 36,630 | | |
| 4 | REVERSAL of 2 (void, approved by the supervisor, `POS_VOID`, 183,150) | | 183,150 | VOID ticket printed to the kitchen |
| 5 | PAYMENT cash (shift SHF…, drawer RESTO) | | 100,000 | drawer opens |
| 6 | ADJUSTMENT `TRF` "Restoran Teratai · R-0123 (room 305)" | | 58,730 | `OUTLET_CHARGE`, scope without approval |
| | **Balance** | **0** | | 341,880 − 183,150 − 100,000 − 58,730 = 0 → bill CLOSED |

**Folio of room 305:** CHARGE `TRF` +58,730, "Restoran Teratai · R-0123" (opens the bill, 8.). The lock now shows used 58,730, remaining 441,270. `pos_room_charges`: one row.

**Journal of the day** (all lines of the day, before netting): Dr 1210 341,880; Cr 4210/Restoran 280,000, Cr 2430 28,000, Cr 2410 33,880 (the charges). The reversal: Dr 4210 150,000, Dr 2430 15,000, Dr 2410 18,150, Cr 1210 183,150. The payment: Dr 1110 100,000, Cr 1210 100,000. The `TRF` pair: Dr 1215 58,730 / Cr 1210 58,730 (out) and Dr 1210 58,730 / Cr 1215 58,730 (in), which `DayActivity` nets to nothing. **Net:**

| Account | Debit | Credit |
|---|---:|---:|
| 1110 cash | 100,000 | |
| 1210 guest ledger | 58,730 | |
| 4210 revenue (Restoran) | | 130,000 |
| 2430 service payable | | 13,000 |
| 2410 tax payable | | 15,730 |
| 1215 | 0 | 0 |
| | **158,730** | **158,730** |

**Reconciliation:** 1210 = sum of folio balances = R-0123 (0) + room 305 (+58,730) = **58,730** ✓; 1215 = 0 ✓ (the check `TRANSFER_ACCOUNT_NOT_ZERO` of the readiness passes); 2310 untouched (the payment came after the charges: not a deposit, F5). Revenue is recorded **once**.

### 9.2 Charge to a locked room, rejected, opened, accepted

1. The lock of room 305 has never been opened. The cashier of the bar asks for the room: the lookup says `LOCKED · NEVER_OPENED · ask reception to open it`. The cashier tries anyway (or a stale screen): **409 `OUTLET_FOLIO_LOCKED`** (`reason NEVER_OPENED`). Writes: one `pos_room_charge_attempts` row REJECTED. **No folio item, no payment, no journal line, no reconciliation effect.** Reception's "Outlet charges today" shows the attempt with "Open lock".
2. Reception opens it (`folio.outlet_charge_open`): limit 500,000, until check-out, outlets RESTO and BAR. Events: `folio_outlet_lock_events` OPENED (old: none; new: limit, mode, outlets), audit `folio.outlet_lock_opened`, `period_no = 1`.
3. The cashier retries bill R-0123, 58,730: accepted as in 9.1: the `TRF` pair, `pos_room_charges`, attempt ACCEPTED (linked). Used 58,730 of 500,000.

### 9.3 Charge beyond the limit

Room 305: limit 100,000, used 58,730 (R-0123). Bar bill B-0045: 2 × 40,000 = net 80,000, service 8,000, tax 9,680, **total 97,680**.

| Step | Result |
|---|---|
| Charge 97,680 to room 305 | 58,730 + 97,680 = 156,410 > 100,000: **409 `OUTLET_LIMIT_EXCEEDED`** (`limit 100,000`, `used 58,730`, `remaining 41,270`, `requested 97,680`); REJECTED attempt; the lock **stays open**; no ledger row |
| Charge 41,270 to the room + cash 56,410 | `TRF` pair 41,270, PAYMENT cash 56,410; bill balance 0 → CLOSED; used = 100,000 = limit → **lock LOCKED (LIMIT_REACHED)** in the same transaction, event and audit written |
| A further charge of 10,000 | 409 `OUTLET_FOLIO_LOCKED` (`reason LIMIT_REACHED`) |

Journal of the accepted payment: Dr 1110 56,410, Dr 1210 41,270 (the room's folio); Cr 4210/Bar 80,000, Cr 2430 8,000, Cr 2410 9,680 (= 97,680 both sides). Reconciliation: 1210 = room 305 (58,730 + 41,270 = 100,000) ✓, 1215 = 0 ✓.

### 9.4 A guest of a group, routed to the master

Group G has master folio M (opened, instruction `ALL` → M, document 30 5.3), lock on M: limit 5,000,000, until the master closes, outlets BAR. Guest in room 412 (a member) orders at the bar, bill B-0046 total **97,680** (as 9.3).

- Resolver (F8): no instruction on the line; the group's `ALL` → **M**. The checks are those of **M's lock**, not of room 412's (room 412 may be locked and the charge still goes through; M locked and the room open is refused with `OUTLET_FOLIO_LOCKED`, `target MASTER`).
- Ledger: B-0046: CHARGE lines 97,680, then ADJUSTMENT `TRF` −97,680 "Room 412 · Guest · B-0046". M: CHARGE `TRF` +97,680 with `stay_id` of the guest (F10); `pos_room_charges.to_folio_id = M`, `stay_id`. Guest folio of room 412: untouched.
- Journal: charges Dr 1210 97,680 / Cr 4210/Bar 80,000, 2430 8,000, 2410 9,680; the pair nets to zero. **Net: Dr 1210 97,680 (on M), Cr the three revenue accounts.** 1215 = 0. Reconciliation: 1210 = M's balance (97,680) ✓.
- Later the master is settled by a transfer to the company's city ledger (document 30 5.4): Dr 1220 / Cr 1210 97,680 + the rest.

### 9.5 A bill charged to a room and returned (R6)

Day 1 bill R-0130: A 60,000 net (svc 6,000, tax 7,260, total 73,260) and B 40,000 net (svc 4,000, tax 4,840, total 48,840); **total 122,100**, charged to room 305 (accepted).

Day 2 the guest says B was not theirs and the rest was for room 307. The supervisor:

| Step | Folio R-0130 | Room 305 | Room 307 | Approval |
|---|---|---|---|---|
| 1. Reopen the bill (day 1 is closed) | status OPEN | | | `POS_REOPEN`, `folio.reopen` |
| 2. Return the transfer | CHARGE `TRF` **+122,100** | ADJUSTMENT `TRF` −122,100 | | return of document 30 4.5 (`folio.reverse` + `FOLIO_TRANSFER`); `pos_room_charges.returned_by_transfer_id` set: the limit used on 305 goes back down |
| 3. Void B (day 1 is closed: an adjustment) | ADJUSTMENT B −48,840 (net −40,000, svc −4,000, tax −4,840) | | | `POS_VOID` |
| 4. Charge the rest to room 307 | ADJUSTMENT `TRF` −73,260 | | CHARGE `TRF` +73,260 | none (lock of 307 is checked) |
| **Balances** | 73,260 + 48,840 − 122,100 = 0 → +122,100 − 48,840 − 73,260 = **0** | 0 | 73,260 | |

**Journal day 1**: Dr 1210 122,100 / Cr 4210 100,000, 2430 10,000, 2410 12,100; the pair nets to zero (1215 = 0). **Journal day 2**: Dr 4210 40,000, Dr 2430 4,000, Dr 2410 4,840 / Cr 1210 48,840 (the adjustment); the return pair and the new pair net to zero (1215 = 0 on both days). **Revenue**: day 1 +100,000, day 2 −40,000, total **60,000 = A**, never counted twice. **Reconciliation after day 2**: 1210 = room 305 (0) + room 307 (73,260) + R-0130 (0) = 73,260; the day's movement −48,840 equals the journal's Cr 1210 ✓.

### 9.6 Split by item (R-0150)

Four lines, two payers: A nasi 50,000 (svc 5,000, tax 6,050 → **61,050**), B mie 40,000 (4,000, 4,840 → **48,840**), C teh 15,000 (1,500, 1,815 → **18,315**), D kopi 20,000 (2,000, 2,420 → **24,420**). Bill total **152,625**. Payer 1 takes A and C, payer 2 takes B and D.

- A' (R-0151) is made empty (own transaction: number from the sequence). Then **transfer by item**, scope `OUTLET_BILL`, one batch, no approval: on R-0150 REVERSAL of B (credit 48,840) and of D (credit 24,420); on R-0151 a copy CHARGE of B (48,840) and of D (24,420), snapshots copied (not recomputed).
- R-0150 balance: 61,050 + 48,840 + 18,315 + 24,420 − 48,840 − 24,420 = **79,365**; R-0151: **73,260**; sum 152,625 ✓. Payer 1 pays cash 79,365 (R-0150 closes); payer 2 pays QRIS 73,260 (R-0151 closes).
- **Journal** (net): Dr 1110 79,365, Dr 1155 73,260 / Cr 4210 125,000, Cr 2430 12,500, Cr 2410 15,125 (152,625 both sides). The transfers add reversal and copy lines that cancel **per account and per department** (Dr 4210 B+D net 60,000 on the reversals, Cr the same on the copies) so nothing moves in revenue; 1210 nets to zero. Reconciliation: both folios zero, 1210 untouched by the split.
- **Equal share** of the same 152,625 between two: payment 1 = 76,313, payment 2 = 76,312 on the **one** bill (remainder to the last), each labelled "Guest 1/2"; no transfer, no new folio.

### 9.7 Room service (RS-0007)

Outlet ROOMSVC (mode PRINT, kitchen), order phoned for room 305 (`order_type ROOM_SERVICE`, `target_stay_id`). The lookup at order time says OPEN (the screen's advice). Lines: club sandwich 85,000 (svc 8,500, tax 10,285 → 103,785) and delivery fee 10,000 (svc 1,000, tax 1,210 → 12,210); **total 115,995**.

- The kitchen ticket is printed when sent: the lines are posted then (CHARGE ×2 on RS-0007, source POS, department "Room service"): Dr 1210 115,995 / Cr 4210/Room service 95,000, 2430 9,500, 2410 11,495.
- The waiter delivers; the guest signs; the cashier or waiter holding `pos.charge_room` charges it: the `TRF` pair 115,995 is **decided again at that moment** (6.3): if the lock was closed meanwhile (checkout at the desk, limit reached) the answer is `OUTLET_FOLIO_LOCKED` or `OUTLET_LIMIT_EXCEEDED` and the bill stays open to be paid another way. Net journal after the charge: Dr 1210 115,995 (on the room's folio) / Cr the three revenue accounts; 1215 = 0.
- A guest who checks out before paying: the check-out result warns "open outlet bills for this room: RS-0007" (D8), and the lock is LOCKED (CHECKOUT) so the bill can only be paid directly.

### 9.8 Laundry, DIRECT (LD-0012)

Outlet LAUNDRY (mode DIRECT, profile "tax only": no service), walk-in guest, 3 shirts × 20,000 = net 60,000, tax 11% = 6,600, **total 66,600**. Draft lines live in the order (no ledger). On **Pay**, in one transaction: CHARGE ×1 (qty 3) 66,600 (source POS, code LAUNDRY → 4350), PAYMENT QRIS 66,600, balance 0, bill CLOSED, receipt and drawer-less (no cash). **Journal**: Dr 1155 66,600 / Cr 4350 60,000, Cr 2410 6,600; 1210 nets to zero. Reconciliation: 1210 unchanged; the QRIS clearing account 1155 +66,600 settles later by the existing settlement (document 27). If the guest is in house and asks to charge to the room, the same payment step is `OUTLET_CHARGE`.

### 9.9 Discount and rounding

Bill of 9.1 without the void: L1 (100,000 net) and L3 (30,000 net), total 158,730. **Bill discount 10%** of the base 130,000 = 13,000, allocated 10,000 to L1 and 3,000 to L3:

| Line | Net | Service | Tax | Total |
|---|---:|---:|---:|---:|
| L1 − 10,000 | 90,000 | 9,000 | 10,890 | 109,890 |
| L3 − 3,000 | 27,000 | 2,700 | 3,267 | 32,967 |
| | | | | **142,857** |

158,730 − 142,857 = 15,873 = 10% of 158,730: the discount reduces service and tax in proportion because the engine computes them on the discounted net. Applied after the send, it is two ADJUSTMENTs of −12,210 and −3,663 (the same total). Cash settlement with `rounding_unit = 100`: 142,857 → **142,900**: one CHARGE `POS_ROUNDING` **+43** (no tax, no service; Cr 4591 43), cash 142,900. Journal: Dr 1110 142,900 / Cr 4210 117,000, 2430 11,700, 2410 14,157, 4591 43 (142,900 ✓). The VAT base stays 117,000 + 11,700; the 43 is not taxed (D3).

### 9.10 Complimentary in both treatments (D2)

Bill R-0160: 2 × nasi goreng 50,000, total **122,100** (net 100,000, service 10,000, tax 12,100), approved as complimentary by the supervisor (`POS_COMP`, amount 122,100, reason "Guest of the owner").

| | `PAYMENT_COMP` | `DISCOUNT_100` |
|---|---|---|
| Folio R-0160 | CHARGE 122,100 (source POS); PAYMENT method COMP 122,100 (credit; `remarks` the name and the approver) | CHARGE with discount 100,000: net 0, service 0, tax 0, **debit 0** (or, after the send, CHARGE 122,100 and ADJUSTMENT −122,100 reference `POS_COMP`) |
| Balance | 0, bill CLOSED | 0, bill CLOSED (a bill with a zero balance and no payment closes) |
| Journal | Dr 1210 122,100 / Cr 4210 100,000, 2430 10,000, 2410 12,100; Dr **6591** 122,100 / Cr 1210 122,100. **Net: Dr 6591 122,100; Cr 4210 100,000, Cr 2430 10,000, Cr 2410 12,100** | **none** (net zero in every account; before the send nothing was ever posted with value) |
| Reconciliation | 1210 unchanged; 6591 +122,100 | 1210 unchanged |
| Outlet report, "Complimentary" | 1 bill, 122,100 incl. tax (base 100,000), approver | the same figures, read from the lines marked `POS_COMP` |

An outlet whose treatment is changed from `PAYMENT_COMP` to `DISCOUNT_100` at 14:00: bills opened before 14:00 keep `PAYMENT_COMP`, bills opened after use `DISCOUNT_100`; the audit has one entry with the old and new value.

### 9.11 A charge moved to another room checks the lock again (D21)

The charge of 9.1 (58,730, R-0123, outlet RESTO) sits on room 305; it should have been room 307. 307 has **no open lock**.

| Step | Result |
|---|---|
| Move to 307 (`POST /pos/room-charges/{id}/move`), by the supervisor who holds `folio.transfer_any` | **409 `OUTLET_FOLIO_LOCKED`** (`reason NEVER_OPENED`, `source MOVE`); the transaction is the refusal only; **nothing moves**: 305 keeps +58,730, 307 has nothing; attempt row REJECTED |
| Reception opens 307 (limit 100,000, outlets RESTO) and the move is repeated | accepted: R-0123 gets CHARGE `TRF` +58,730 (return) then ADJUSTMENT `TRF` −58,730; 305 gets ADJUSTMENT −58,730; 307 gets CHARGE +58,730; `pos_room_charges`: the old row returned, a new one on 307 (used 58,730 of 100,000) |
| The same move to 307 with the limit 50,000 | `OUTLET_LIMIT_EXCEEDED` (remaining 50,000); 305 unchanged |
| A balance transfer of 58,730 from 305 to 307 by the receptionist (`folio.reverse` + approval `FOLIO_TRANSFER`, `folio.transfer_any` as the rooms are unrelated) | **accepted without a lock check**: it is a front-office transfer (7.6); 305 −58,730, 307 +58,730; the usage of 305 is unchanged (the outlet charge is still there and not returned) and 307's is 0; audited with both folios and the approver |

Journal of an accepted move or transfer: the return pair and the new pair each net to zero on 1215; 1210 moves 58,730 from the folio of 305 to the folio of 307; revenue untouched.

## 10. Realtime, KDS and printing

### 10.1 Realtime (F2)

- **Transport: Server-Sent Events** over `fetch` (`GET {P}/pos/events?channels=…`, `Authorization` header, `Accept: text/event-stream`), because the traffic is server to client only, it passes proxies and is simple to authorise; the browser `EventSource` cannot send the header, so the client reads the stream with `fetch` and a reader. WebSocket is not needed for what is asked (D15).
- **Outbox**: every use case that changes something a screen shows inserts a row in `pos_events` in its transaction (`channel`, `type`, `entity_id`, ids only, no amounts or names) and issues `NOTIFY pos_events` after commit; a stream handler holds a connection that `LISTEN`s and reads rows after the `Last-Event-ID`. With several API instances the outbox is what keeps them in step. Rows are kept 48 hours; a client that is too far behind gets `410` and reloads.
- **Channels** and who may subscribe: `outlet:{id}` (`pos.order`, `pos.cashier`: tables, bills, sold-out), `station:{id}` (`pos.kds`: tickets), `frontdesk` (`folio.outlet_charge_open` or `folio.read`: `folio_lock.changed`, `outlet_charge.attempted`), `printers` (`pos.manage`, `pos.cashier`: job status). Authorisation is checked at subscription and the payload is only ids, so a screen reads the data with its own permission.
- **Events**: `table.updated`, `bill.updated`, `bill.closed`, `ticket.created`, `ticket.updated`, `product.sold_out`, `print_job.updated`, `folio_lock.changed`, `outlet_charge.attempted`. Heartbeat comment every 20 s; client reconnects with backoff and resumes by id; it also reloads on focus.
- The module gate and the read-only state apply to the stream (it is a GET).

### 10.2 Kitchen

- **PRINT**: on send, one **ticket per station** (lines grouped by `station_id`), kind NEW; a change of a DRAFT line before the send needs nothing; after the send a *change* is a void plus a new line, so the kitchen gets a VOID ticket ("CANCEL 1 × Steak, table T5") and a NEW ticket. Each ticket is a print job to the station's printer.
- **KDS**: the same tickets, shown in columns **New / Cooking / Ready** per station; a tap moves them (`pos.kds`), `SERVED` is set by the waiter or the cashier; waiting time since `created_at` with the station's `late_after_minutes` marking late tickets; a voided line stays on the ticket, struck through, until the ticket is served. Updates by the stream; the screen is read-only about money.
- **DIRECT**: no ticket, no station.
- A station with neither a printer nor KDS fails the **configuration check** (`GET /pos/setup-check`: an outlet in `PRINT` whose products have a station without a printer, a product with no price in the list of an outlet that sells it, a charge code whose revenue account requires a department the outlet lacks), shown in Setup like the department check of document 12.

### 10.3 Print agent protocol (R10)

The server is in the cloud and **never connects to the hotel**. The agent calls out over HTTPS.

1. **Enrolment.** A `pos.manage` user creates an enrolment code (one-time, 15 minutes, shown once). The agent sends `POST /api/v1/device/enroll {code, name, agent_version}` and receives `{device_id, token, poll_seconds}`; the **token** is 32 random bytes (`kpd_…`), shown once, stored only as SHA-256; it is bound to one device and one property. A printer is assigned to a device (`pos_printers.device_id`). A token can be **revoked** or **rotated** at any time; a revoked token answers 401 and the agent stops.
2. **Claim.** `GET /api/v1/device/jobs/next?wait=25` (long poll): the server returns the oldest QUEUED job of the device's printers (`FOR UPDATE SKIP LOCKED`), sets it CLAIMED with `lease_until = now + 60 s` and increments `attempts`; or `204` after the wait. Jobs of one printer are handed out **in order**; a job whose lease expired returns to QUEUED (attempts count).
3. **Document.** The server renders the job to **ESC/POS bytes** using the printer's profile (width 58/80 mm, codepage, drawer pulse, cut) and sends them (`payload` base64, `printer_id`, `copy_no`). The agent is a **dumb pipe**: it writes the bytes to the TCP 9100 or USB printer. Rendering in the cloud keeps one place to change a layout and makes a reprint byte-identical.
4. **Result.** `POST /device/jobs/{id}/result {status PRINTED|FAILED, error_code, printer_status}`, idempotent by job id and attempt. FAILED with attempts < `max_attempts` goes back to QUEUED after a backoff (10 s, 60 s); then it is FAILED for good and an event `print_job.updated` tells the cashier screen, with **Reprint**.
5. **Heartbeat.** `POST /device/heartbeat {printers: [{id, status ONLINE|OFFLINE|PAPER_OUT|COVER_OPEN}], queue_depth}` every 30 s; the setup screen and the cashier screen show a printer that has not reported in 90 s as offline.
6. **Reprint.** A new job with `reprint_of`, printed with "COPY n" on a receipt and audited (who, why); the original is never changed.
7. **Drawer.** A job of kind DRAWER (ESC `p` pulse) is created by a cash payment and by a no-sale open (reason required, `pos.cashier`, audited).
8. **Security.** TLS only; the token is only in the `Authorization` header; rate limit per token; the device routes accept **no user token** and the user routes accept **no device token**; a device can read only jobs of its printers; payloads (names, amounts of a receipt) are purged after 7 days and the metadata kept; nothing in the protocol lets the server run anything on the agent. The device routes are classified POS for the module gate, and in `READ_ONLY` state they fail like any write (the agent reports it and retries).
9. **Dedupe.** `dedupe_key = type:source_id:copy_no` is unique: a double send or a retried request creates one job.

The **agent program** is a separate stage (PR 3b): this document fixes the protocol; the server side and a simulator used by the tests are built with PR 3.

## 11. Screens (en and id, touch targets ≥ 44 px; cashier 1280–1440, waiter 390)

The screens follow the mockups of `docs/design/10-pos-denah-meja` to `16-pos-pelayan-ponsel` (the file of each is named in the table); 11.1 lists what the mockups showed beyond the first draft and where a decision overrides them.

| Screen | For | Content |
|---|---|---|
| Floor plan (`10`) | cashier, waiter | areas as tabs, tables drawn at their position, shape and size, colour and count by derived status; tap opens or continues a bill; on 390 px a zoomable canvas with a list mode |
| Order (`11`, `16`) | waiter (390), cashier | category chips, product grid with sold-out marking, options sheet (variant: required, pick one; modifiers: with limits), draft lines with note, "Send" (PRINT/KDS) or "Pay" (DIRECT); sold-out and server errors inline |
| Bill / payment (`11`, `12`) | cashier | lines by round, void, discount, request bill, tender with several methods, room lookup with the lock chip, company, comp, split, merge, receipt; the change due |
| Kitchen display (`14`) | kitchen | the columns, timers, late flag, voids struck through, station switch |
| Floor plan editor | `pos.manage` | drag, resize, shape, capacity, code, areas; saved as a whole with `layout_version` (409 `VERSION_CONFLICT`) |
| Setup, product master (`15`) | `pos.manage` | outlets, stations, rule profiles, categories, products and options, price lists, printers, devices and enrolment, the configuration check |
| Front desk (`13`) | reception | folio dialog "Open for outlets", the option in the check-in panel, the room panel chip, "Outlet charges today", a folio list that **separates room, company and master folios from outlet bills** (R13) |
| Reports | `pos.report` | section 13 |

### 11.1 What the mockups add, and where a decision overrides them

- **Floor plan decoration** (`10`: "Jendela", "Pintu masuk", "Kasir"): a table `pos_area_marks (area_id, kind WINDOW|DOOR|CASHIER|OTHER, label, x, y, w, h)` in the plan, drawn by the editor, no behaviour. Added to 3.2.
- **Floor plan content** (`10`): a table shows the guests and minutes open, the bill number and the total, "Bill dicetak" and a hold ("Dipesan 19.00 Kel. Santoso"); beside it the list of open bills with a search by bill, table or room, "Takeaway / bill without table", "Shift report" and "Open drawer". The shift state and the business date sit in the top bar. The total of an open bill is its folio balance, read with `pos.order` or `pos.cashier`.
- **Bill lines** (`11`): each line shows the status of its ticket ("Dimasak", "Siap", "Diantar" = NEW/COOKING, READY, SERVED) and a voided line is struck through with the reason and the approver ("Void 12.40 · salah pesan · disetujui Wayan"). The status is the ticket's (D19), repeated on its lines.
- **Product options** (`11`, `15`): a product may have **several** required "pick one" groups (portion, spice level) and optional groups with a maximum ("max 3"); it is what `pos_option_groups` already allows.
- **Charge code and prices** (`15`): the product page edits the price of the product in the price list of each outlet (a table "Outlet / sold / price"); outlets that share a price list are shown together with a notice. "Tax and service": *follow the outlet* (shows the rules of each outlet) or *set for this product* (a profile, including a price that already includes tax). The recipe block is read-only and says it needs the inventory module (4). **CSV import of products** ("Impor CSV") is added to PR 1: all or nothing with the rows that are wrong listed, the shape of the chart import.
- **Payment** (`12`): six methods (cash, card, QRIS, charge to room, company, complimentary), a keypad, "exact amount", several lines whose rest must reach 0, the drawer opens by itself. The room lookup shows, beside the lock, **the limit and the amount used** ("Batas 2.000.000 · terpakai 0") and where it would be billed ("ditagihkan ke tamu" or the master); so `GET /pos/room-lookup` returns `limit` and `used` (the limit is not a balance; the folio lines and balance remain unreadable to the cashier). The guest signs on the receipt. The front desk gets a notification for each incoming outlet charge (the `frontdesk` event, with a toast and the list of 6.5).
- **The line on the guest folio** (`12`) reads "**{outlet name} · R-0123**" ("Restoran Teratai · R-0123"), not the generic "Outlet": the description of the incoming `TRF` line is built from the outlet name and the bill number, with the structured link that opens the bill. The earlier text of sections 6 and 9 is to be read that way.
- **Kitchen display** (`14`): the station switch lists the outlets a station serves (stations are property-level, so one kitchen serves the restaurant and room service); the header shows the average wait and the number of late tickets; a ticket shows its origin ("Meja 5 R-0123 · Ketut", "Kamar 214 Room service · RS-0045"), "tambahan" for a later round, its age, and "Mulai", "Siap", "Sudah diambil" buttons; **History** lists the tickets served in the last hour.
- **Waiter** (`16`): per-category chips, a `Tambah` button that becomes a stepper, "sudah dipesan 2" for what the bill already holds, sold-out marked, "1 item draf · bill 453.750 · Lihat bill" and "Kirim ke dapur & bar" at the foot.
- **Rates in the examples.** The mockups compute service 10% and PB1 10% **on net plus service** (375,000 → 37,500 → 41,250 → 453,750). Section 9 uses 11% as in `03-financial-engines.md`; the rate is a rule of the profile, so the same flows hold with 10% (only the figures change). The `pms-seed pos` makes the rule profile "F&B" with service 10% and PB1 10% so that the mockup's figures are the demo's.

**Folio list (R13).** `GET /folios` takes `folio_type`; its default (no filter) **excludes `OUTLET`** so every existing screen, count and search of the front desk keeps its meaning; the list gets two tabs, "Room / company / master" and "Outlet bills" (`folio_type=OUTLET`, with the outlet filter). The queries that join folios to reservations or stays are checked again, one by one, as document 30 13.1 did (a test reads everything that touches folios with an outlet bill that holds a payment and a transfer).

## 12. Offline readiness (R11)

**Built now, because it costs nothing and cannot be added later without breaking data:**

- **Every id of an order is made by the client** (UUID v7, time-ordered): `pos_bills.client_uuid`, `pos_bill_lines.client_uuid`, `pos_rounds.client_uuid`; creating with an existing uuid returns the existing row (idempotent).
- **Every mutating request carries `Idempotency-Key`** and every use case replays the stored result (the entity holds its key, as `payments` and `folio_items` do): add line = the line's uuid; send = the round's uuid; pay = the payments' keys; void = the reversal's key; move table = idempotent by nature.
- **The bill number may come later**: `pos_bills.folio_id` and `bill_number` are nullable until the bill is first synced; the screen shows "Draft · A1B2" meanwhile.
- **Drafts do not depend on server order**: a line is a row with its own uuid and a client timestamp, never a position; a bill's draft set is the union of its rows.
- **The catalogue is versioned**: `GET /pos/catalog?outlet=` returns the whole catalogue of an outlet (products, options, prices, rule profile, tables) with `catalog_version`, and supports `If-None-Match`; a draft stores the `catalog_version` it was priced with.
- The server is the **only source of tax and service** (`price-quote`): offline totals are estimates and are shown as such.

**What offline will need (not built):**

| Need | Design note |
|---|---|
| Local store | IndexedDB: catalogue, floor plans, open bills and drafts, an **outbox** of operations (uuid, type, payload, idempotency key, created_at) |
| Sync | `POST /pos/sync` with an ordered batch; the server applies each operation in its own transaction and answers per operation (`APPLIED`, `REPLAYED`, `REJECTED` + code); the client keeps the rejected ones for a person |
| Conflicts | lines: union (no conflict); an edit of a line that was SENT elsewhere is rejected, the line shown; a table taken by another bill is allowed (several bills per table); a product sold out meanwhile is warned, not blocked; a **void, a discount, a comp, a reopen and every payment by card, QRIS, room or company are online only** (approval, authority, lock and limit are decided by the server); cash may be accepted offline and recorded on sync **only if** the shift is open on that device (a later decision) |
| Business date | an operation made offline posts on the business date open **when it is applied** (never a closed day); the client's time is kept as `client_created_at` for the receipt and the audit |
| Printing | the agent also listens on the hotel LAN so a device can print locally while the cloud is unreachable (the same job format) |
| Numbers | bill numbers are assigned at the first sync, in the order of `client_created_at`, per outlet |

## 13. Night audit, close, reports

### 13.1 Night audit (R7)

| Check | Kind | Reason |
|---|---|---|
| Open bills (OPEN, with posted lines) at the end of the day | **warning** by default; **blocker** when `pos_outlets.block_night_audit_with_open_bills` | The sale is already revenue of the day and the books are right (1210 carries it and reconciles); but an unpaid bill means cash that nobody counted and a guest who may have left. A hotel with a bar that never closes needs the warning only; one that wants every bill closed turns it into a blocker, as it does with shifts. (D4) |
| Draft lines never sent on an OPEN bill | warning | They are not in the books; they would post on tomorrow's date |
| POS shifts still open | blocker (existing `SHIFTS_OPEN`, no change) | the drawer is counted before the day closes |
| `TRANSFER_ACCOUNT_NOT_ZERO` | blocker (existing) | covers every `TRF`, outlet charges included |
| Print jobs FAILED today | warning | |
| Locks `TODAY` with `valid_on ≤ BD` still OPEN | not a check: the close writes them LOCKED (EXPIRED) with events | |

**Sales enter the day-close journal through the existing path**: `DayActivity` reads `folio_item_gl` for every folio, OUTLET included; the revenue lines are grouped by account **and department** (the outlet's sub-department), the payments by method. No POS-specific journal exists. The close summary gains the sales and payments by outlet.

### 13.2 Reports (per outlet, per day or range, CSV and PDF like the other books)

Sales by product, category and hour; **service, tax and net** (the totals the tax return needs, from the item components); payments by method; voids (who, who approved, reason, amount); discounts and comps (same); per cashier and per waiter (the line's `created_by` and `sent_by`, the payment's `created_by`); outlet charges to rooms and to companies; open bills; and the **outlet revenue reconciliation**: revenue by outlet from `folio_items` against the journal lines of the outlet's department, which must agree. Permission `pos.report` (any-of `accounting.view` for the revenue reconciliation).

## 14. Phases (PRs), migrations, tests

Migration numbers are the next free: **00080, 00081, 00082**; if another PR takes one first, these move (as documents 31 and 32 did). Each PR is DB, backend, API (OpenAPI first), frontend and tests together; each on its own branch from `main`; `gen:api`, `sqlc`, lint and the web checks as CLAUDE.md lists.

| PR | Migration | Contents |
|---|---|---|
| **1. Masters and the lock** | 00080 | `folios` OUTLET shape (no OUTLET folio is made yet), `pos_outlets`, stations, printers (no agent), rule profiles and `RuleProfileID` in the calculation request, categories, products, options, price lists, sold-out, recipe table, areas, tables, holds, floor plan editor; `folio_outlet_locks` and its events, `OutletChargeGate`, the lock use cases and endpoints, the check-in panel option, the check-out auto-lock, "Outlet charges today" (empty until PR 2), the permissions, the module gate classification, `seed_charge_codes` and the `POS_ROUNDING` code and account, `companies.pos_billing_allowed`; `pms-seed pos` (demo outlet, products, tables) |
| **2. Orders and payment** | 00081 | `pos_bills`, lines, rounds, tickets (rows only), `pos_room_charges` and its attempts, folio source `POS`, `COMP`, transfer scopes, the view change, the approval kinds, open / add / send / void / discount / pay / split / merge / reopen, charge to room and to company, the cashier screen with the floor plan, shifts by drawer, receipts as an HTML/PDF document (no agent yet), reports, night audit checks, the folio list tabs |
| **3. Waiter, KDS, printing** | 00082 | the waiter layout (390 px), KDS and ticket statuses, `pos_events` and the stream, `pos_devices`, enrolments, print jobs, ESC/POS rendering, the device routes, a **simulated agent** for tests, drawer jobs, reprint |
| 3b. Print agent program | none | the program for Windows/Linux in the hotel (own repository or `cmd/pos-agent`: D16) |
| **4. Inventory and recipes** | own document | stock, recipe use, movement on a sale, costing; adds the foreign key of `pos_recipe_lines` |

### Tests (real PostgreSQL; races with two goroutines and a barrier: exactly one success and a defined error for the other, as `05` 4)

**PR 1**
- Schema (`db/tests/schema_test.sql`): the OUTLET shape (no stay, reservation, group, company; `outlet_id` required), a bill-number prefix cannot clash, option group `VARIANT` max 1, `limit_amount` required exactly while OPEN, `folio_outlet_lock_outlets` not empty while OPEN (by the use case and a deferred trigger), the migration Up/Down/Up and Down refused with data (`scripts/db-test.sh`), `POS_ROUNDING` refused a tax mapping.
- Calculation: a profile replaces the mapped rules, same totals as a charge code with the same rules (a table of 12 cases, EXCLUSIVE and INCLUSIVE), exempt and `TRF` ignore it, `chargecalc` untouched.
- Lock: no row means locked; open requires a limit and outlets; limit below used refused; TODAY expires at the next business date lazily and at the close; check-out writes LOCKED (CHECKOUT); closing the master locks it; every change audited with old and new; permission and tenant isolation; **the gate with a test harness** (`OutletChargeGate.Check` under the folio lock): locked, expired, not allowed, exceeded, accepted, reaches the limit.
- Setup: price per outlet, product not sold in an outlet, sold-out clears with the day, the floor plan saved whole with a version conflict, the configuration check, the reads by role (as `setup_read_roles_test.go`), the module gate (`MODULE_NOT_IN_PLAN` on every POS route, none on the check-out).
- Web: the lock dialog, the check-in panel step (failure after the check-in does not undo it), the editor, the menu by role and module, i18n parity.

**PR 2**
- **Races**: two waiters add to one bill at once (both lines present, order by `(created_at, uuid)`); an add against a pay (the pay wins or the add answers `BILL_NOT_OPEN`); two sends of one bill (one round per line, no line posted twice, `folio_items.reference_id` unique); **two payments at once** on one bill (balance never negative, one `CLOSED`); **two outlets charge one room at once and pass the limit** (6.4, repeated); a charge to room against the check-out of that stay (no deadlock: the stay share lock, or `CHECKOUT` wins and the charge answers `OUTLET_FOLIO_LOCKED`); a charge against the lock being closed by reception; two merges of the same bills; a void against a pay.
- **Comp (D2)**: both treatments end to end (9.10): journal, reconciliation, report; the treatment kept on the bill when the setting changes; the audit of the change; `POS_COMP` asked in both modes under `FREE`, `DISTINCT` and `DISTINCT_ABOVE`; `DISCOUNT_100` leaves no revenue, service, tax or expense and no zero-value line in the journal.
- **Move and transfer (D21)**: a new charge and a move are refused for a locked, expired, not-allowed or over-limit target **also for the holder of `folio.transfer_any`**; a move is one transaction (nothing moves on a refusal; the first room keeps the charge); the usage follows the charge (the old row returned, a new row at the target); **a front-office balance transfer to a locked room is accepted, writes no `pos_room_charges` row and changes no usage** (even when the source holds outlet charges), is audited with the approver, and still needs its approval; "used" is unchanged by payments, other charges and transfers; two moves, or a move and a charge, to one room at once cannot pass the limit.
- **Ledger**: for each flow of section 9 the exact folio lines, the day journal, and the reconciliation (`ControlSources`) with and without deposits, before and after the close and the release of deposits (document 31, both modes); **net zero per account and department** of a split, a merge and a return; **revenue not doubled** when charged to a room (the revenue summary, the department report and the journal by outlet); `TRF` zero on 1215 at every close; the faktur of the guest folio unchanged by an outlet charge.
- **View**: the new `is_deposit` branch compared row by row with the old view for every existing case (as 00075).
- **Approvals**: each new kind under `FREE`, `DISTINCT`, `DISTINCT_ABOVE` at, below and above the limit; the structural test; the idempotency replay does not ask again.
- **Shift**: cash by drawer with two open shifts; `NO_OPEN_SHIFT`; the Z report with POS cash and QRIS; the night audit refused while open.
- **Rounding, discounts**: allocation sums exactly (largest remainder, 1, 3 and 7 lines, decimals 0, 2, 3); the adjustment after the send equals the discount before it.
- **Closed days**: void on an earlier day is an adjustment dated today; reopen on the same and another day; return after the close.
- **Routing**: line `ALL` to a company folio, group `ALL` to the master (lock and limit of the master), `ROOM` and `CHARGE_CODE` instructions ignored, a closed master `ROUTING_TARGET_CLOSED`.
- **Company**: credit limit under the company lock, `pos_billing_allowed`, invoiceable at once.
- **Folio reads**: the list default excludes OUTLET; every query that joins folios to reservations or stays with an outlet bill present.
- Web: the cashier flow with the mock client (a fresh `vi.fn()` per test), the lookup chips, every error code with its en and id text, the split and merge dialogs.

**PR 3**
- Stream: resume by `Last-Event-ID`, `410` when too far behind, permission per channel, no payload with amounts, two instances through the outbox.
- Tickets: one per station per round, VOID ticket, KDS transitions and the late flag, a voided line struck through.
- Print: claim order and lease, `SKIP LOCKED` with two agents, the retry and the failed state, idempotent result, dedupe, reprint copy mark, drawer job, token revocation and rotation, device token on a user route and the reverse (401/403), payload purge, the module gate and `READ_ONLY`.
- Web: the waiter layout at 390 px, KDS, the printer status.

## 15. Decisions of the owner (decided 2026-10-15, review of PR #35)

**D1 and D3 to D20 are approved as recommended.** D2 and D21 were decided differently from the recommendation; the column "Recommendation" below is kept as the question was asked, and the **decision** of the owner is given for those two in bold at the start of the row.

| # | Question | Recommendation |
|---|---|---|
| D1 | Tax and service per outlet: **rule profiles** (a field on the calculation request) or **a charge code per treatment** (no engine change, more codes and a revenue report per code)? | **Profiles.** It is the literal R2 ("follows the outlet, override per product"); the engine change is one optional field and `chargecalc` is untouched. |
| D2 | Complimentary: payment method `COMP` or a 100% discount? | **DECIDED: a setting of each outlet, "Perlakuan komplimen": `PAYMENT_COMP` (default) or `DISCOUNT_100`**, stored on the bill when it is opened, audited, always with the `POS_COMP` approval, reported in both modes; the tax treatment is to be confirmed with the consultant. Section 7.5, flow 9.10. (Recommended was the payment method only.) |
| D3 | Rounding as an untaxed `POS_ROUNDING` line: acceptable to the tax office? | **Yes, per outlet and switchable** (`rounding_unit`); confirm with the consultant before it is switched on for a PKP property. |
| D4 | Open bills at the night audit | **Warning by default, per outlet a blocker** (13.1). |
| D5 | Splitting and merging inside one outlet without a supervisor's approval (`OUTLET_BILL`) | **Yes**: no money changes payer; audited. Moving a line to another folio keeps the approval. |
| D6 | Which routing instructions capture an outlet charge | **`ALL` only** (a balance has no charge code). A hotel that wants the bar on the company and the restaurant on the guest uses two outlets with their own rules later; not now. |
| D7 | A charge that **exceeds** the limit: reject only (recommended) or also lock? | **Reject only**; the lock closes when the limit is **reached exactly** or by hand, so a smaller charge may still pass. |
| D8 | (a) Check-out with open bills for the room; (b) charging a company account from an outlet | (a) **Warning with the list, not a blocker** (the bill can be paid another way) and the lock closes. (b) **`companies.pos_billing_allowed` (default off)** plus the outlet's setting plus `cityledger.transfer`, so no company is charged by a bar that was not told to. |
| D9 | Tax invoice (faktur) for an outlet bill | **Not built**; receipt only; wait for the consultant's answer on document 30 5.4. |
| D10 | Bill number = folio number (`R-0123`), gaps allowed for voided empty bills | **Yes**; a bill is a receipt, not a fiscal document. |
| D11 | Grant the new permissions to existing roles by migration? | **No**: the module is new and the 00076 lesson was a grant that was too wide. The tenant administrator holds everything; `pms-seed pos` can create demo roles in a development database only. |
| D12 | Discount without an approver's password up to a percent (`max_discount_pct_no_approval`) | **Default 0** (always approve), settable per outlet; a staff discount of 5% at a café is a decision of the hotel. |
| D13 | `COMP` and the `POS` source add CHECK values and a system account each | **Approved**; the account of `COMP` is a row of Account mapping (D2); the Down refuses while used. |
| D14 | Cash for the outlet: the shift **per drawer**, several drawers per cashier | **Yes** (F9); no change to the shift rules, a drawer per outlet. |
| D15 | SSE over fetch instead of WebSocket | **SSE.** Nothing asked needs the client to push, and an outbox gives resume. |
| D16 | The agent in this repository (`cmd/pos-agent`, Go, one binary) or its own? | **Here**, same protocol tests and release; it needs no database. |
| D17 | Tips (gratuity) are **not** modelled: the service charge is the only add-on. | Confirm; a tip would be a liability account and a payment allocation, a later step. |
| D18 | A prepaid amount on an outlet bill before any charge (a spa deposit) | Allowed by the role-based `is_deposit`; the release of document 31 applies. Confirm it is wanted. |
| D19 | Kitchen status per ticket (recommended) or per line | **Per ticket**; per line only if a kitchen asks. |
| D20 | A table "hold" (dipesan) is a small list with a time, **not** a link to PMS reservations | **Yes** for now. |
| D21 | A stay's outlet charges moved to another room after a wrong room (7.6) | **DECIDED (second decision of the owner, replaces the first): the lock, validity, allowed outlets and limit are checked on what originates in an outlet** (a new charge to room and `POST /pos/room-charges/{id}/move`, a return and a new charge in one transaction, nothing moves on a refusal), **for everyone including `folio.transfer_any`**. A front-office balance transfer (`TRF`, with approval, document 30) **is not checked and not counted**: the balance is fungible and the act is the receptionist's own, approved and audited. "Used" = outlet charges to the folio less those returned or moved through POS, not the folio balance. No attribution rule. Section 7.6, flow 9.11. (Recommended was to skip the check for `folio.transfer_any`.) |

## 16. Not in this design

Inventory, stock and costing (PR 4); an offline client and `POST /pos/sync` (section 12 lists the need); loyalty and vouchers; happy-hour or time-based prices; tips; table reservations linked to PMS; delivery and online ordering; a fiscal printer; multi-currency tendering (the property's currency only); the agent program itself (PR 3b); the faktur pajak of an outlet bill.

## 17. As built: PR 1, the masters and the lock of a folio (migration 00080)

What was built follows sections 3.2, 4.3, 6, 10.2 (the check of the setup) and 11, with the owner's decisions of PR #35. Where it differs from the text above, this is the record.

**Built.** The masters of outlets, stations and printers (no agent), tax and service profiles, price lists, categories, products with variants and add-ons, "sold out today", the structure of a recipe (a table, no endpoint), the floor plan and its editor; the lock of a folio for outlet charges, the gate (`CheckOutletCharge`, `RecordOutletCharge`) with the list of incoming charges, the option in the check-in sheet, the lock at check-out and when a folio closes, the lock "of this business day only" that ends with the day; `RuleProfileID` in the calculation request; the CSV import of products; the POS module gate and the menu that follows the plan.

**Differences from the design**

1. **What moved to migration 00081 (PR 2).** Nothing in stage 1 touches the ledger, so these wait for the stage that uses them: the folio type `OUTLET` and its shape, the charge type `ROUNDING` with the code `POS_ROUNDING` and its account 4591, the payment method `COMP` with its account, `folio_items.source = 'POS'`, the scopes `OUTLET_BILL` and `OUTLET_CHARGE` of the transfer log, `companies.pos_billing_allowed`, and the change of the view `folio_item_gl`. The next free migration for the bills is therefore 00081 and for the kitchen 00082, as planned.
2. **`pos_room_charges` is created now**, with its business-day trigger, so that the gate and its limit can be tested before the bills exist; `bill_id`, `from_folio_id` and `transfer_batch_id` are nullable until stage 2 fills them. `pos_room_charge_attempts` has a `source` column (`CHARGE` or `MOVE`), as decided for D21.
3. **A lock that is LOCKED keeps its limit** (`limit_amount` is not null while the folio is locked): the screen can say "limit reached, 100,000" and a reopening starts from it. Whether the lock is open is `status`, never the amount.
4. **The reading of the lock is one endpoint**: `GET folios/{id}/outlet-lock` answers the state, who can open it and why not, the limit, what was used and what remains, the outlets and the last 50 events. `PUT` opens it (a new period) or changes it (needs the `version`); `POST …/lock` locks by hand. A lock of an earlier business day is shown LOCKED (EXPIRED) at once and is written down by the first charge that finds it, or by `ExpireOutletLocks` at the close.
5. **A refused charge is a result, not an error**: `CheckOutletCharge` writes the attempt and returns the refusal, and the use case commits and answers 409 (the attempt rows are the only writes of that transaction).
6. **The reads of the reference data an outlet needs** ask also for `pos.manage` (`document 33`): taxes, service charges, charge codes and departments, so that who sets up the outlets does not need `billing_config.manage` and `accounting.view` for it. The list of outlets is also read by `folio.outlet_charge_open` (the checklist of the lock).
7. **The holds of tables ("dipesan")** are not built: they belong to the cashier floor plan of stage 2. The floor plan has areas, tables and marks.
8. **The profile and the codes**: `RuleProfileID` is ignored for a `TRANSFER` or a `ROOM` code (the `ROUNDING` code comes with stage 2).
9. **"Outlet charges today" polls** every 20 seconds and when the tab comes back; the live stream is stage 3. The room panel chip of 11 is not built (the lock is on the folio screen and in the check-in).
10. **The check-in** opens the lock as a fifth request after the deposit and the check-in (`PUT folios/{id}/outlet-lock`), with its own failure that never undoes the check-in.
11. **Permissions** (section 8.1) are in the catalogue and are given to no role; the tenant administrator holds them. The audit entries are `pos.*` and `folio.outlet_lock_*`.

**Tests (real PostgreSQL).** `internal/folios/outletlock_test.go`: never opened, the validation, who may open and read, a folio that cannot be opened, the limit and the outlets and the refusals with their context, an excess that does not lock and a limit that is reached exactly that does, change and version, a new period, "today only", the check-out locking the folios of the stay, two outlets at once that cannot pass the limit (six rounds), the holder of `folio.transfer_any` checked like everyone, the list of incoming charges. `internal/pos/pos_test.go`: the outlets, the profile in the calculation (exclusive, inclusive, tax only, `TRF`, a switched-off rule), products with variants and add-ons, prices per outlet and sold out, the floor plan (version, a swap of codes, a table switched off, a code unique in the outlet), the CSV import, the check of the setup, permissions. `internal/app/pos_api_test.go`: the masters through the router and the module gate. `db/tests/schema_test.sql`: the constraints of the new tables. Web: `OutletLockCard`, `OutletChargesView`, the masters, the product master and the floor plan editor, the check-in option, the menu by module.

### 17.1 Compared with the mockups 13 and 15 (screens of PR 1)

The screens were checked against the running application (desktop 1366 px and phone 390 px) and the mockups `docs/design/13-pos-buka-folio-outlet` and `15-pos-master-produk`; the images are in the description of the PR, not in the repository.

**As in the mockups.** 13: the card "Beban dari outlet: terkunci" with the limit and "berlaku sampai Check-out (date)" side by side, the checklist of outlets, the note about opening at check-in, the button "Buka untuk outlet"; the list of incoming charges with coloured rows (charged, refused) and the reason of a refusal. 15: the list of products by category with the price at an outlet on the right, the card of the product, "Harga per outlet" (sold, price), "Varian" and "Tambahan" side by side, "Pajak & servis" (follow the outlet / set for this product), "Resep & biaya bahan" waiting for inventory, the bar Nonaktifkan / Simpan.

**Different on purpose.** An open folio shows the limit, what is used and what remains (the mockup draws only the locked state). The incoming charges are a page of their own (`/outlet-charges`, every room), not part of the page of one folio; the blue dot of "new" waits for the live stream of stage 3. In the product master the price at an outlet is the price in the price list of that outlet: outlets that share a list show once and the others cannot be edited. The recipe is a note until the inventory module. The cashier floor plan (mockup 10) is stage 2; stage 1 has the editor.

**Empty state.** "Beban outlet masuk hari ini" with nothing to show keeps its header and its filters and says "Belum ada beban dari outlet hari ini" / "No outlet charges today"; a filter that matches nothing offers "Show all".

### 17.2 The demo data: `pms-seed pos`

`go run ./cmd/pms-seed pos -tenant DEMO -property BALI [-dry-run]` fills a development database through the services (idempotent by code): the tax PB1 and the service charge SVC when missing, the profiles FB and TAXONLY, three price lists, four outlets (RESTO, BAR, RS, LD), two stations and two printers, food, drinks and laundry services in Indonesian, and the floor plans. **The laundry has a price list of its own that holds laundry services only; no food is sold at the laundry**, and no laundry service is in a kitchen list (a test of the seed data holds it).

## 18. As built: PR 2, bills, orders and payment (migration 00081)

What was built follows sections 3.3, 4, 5, 6.3, 7, 9 and 13 with the owner's decisions of PR #35. Where it differs from the text above, this is the record.

**Built.** The ledger changes of section 3.1: the folio type `OUTLET` (with `outlet_id`, no reservation, stay, group or company), the charge type `ROUNDING` with the code `POS_ROUNDING`, the source `POS` of a folio item (unique per line of a bill), the payment method `COMP`, the scopes `OUTLET_BILL` and `OUTLET_CHARGE` of the transfer log, `companies.pos_billing_allowed`, the role-based `is_deposit` for `OUTLET` in the view `folio_item_gl`. The tables `pos_bills`, `pos_bill_lines`, `pos_rounds`, `pos_tickets`, `pos_ticket_lines`, `pos_table_holds`, and the foreign keys that waited for them. The cashier floor plan with the derived state of each table, the open bills with their minutes and totals, the holds ("dipesan"), the order screen (menu, variants and add-ons, kitchen note, free edits of a draft), send, void with `POS_VOID`, discount (item or bill, by percent or amount, allocated by the largest remainder; `POS_DISCOUNT` above the limit of the outlet), complimentary in both treatments with `POS_COMP`, request of the bill, payment in one transaction (cash, card, QRIS, transfer, charge to room through the gate, company, rounding of cash by outlet), split by item, merge, void of an empty bill, reopening with `POS_REOPEN`, the return and the move of a charge to a room, the receipt printed by the browser, the reports of an outlet, the night audit findings, the warning at check-out, the folio list with its tab of outlet bills, the line of the guest folio that opens the bill.

**Differences from the design**

1. **The accounts.** The rounding is booked to a revenue account 4591 "Rounding" as designed. The complimentary expense account is **6196** "Complimentary and house use" (under administrative and general, beside the cash over and short 6195), not 6591, which stands under utilities in the standard chart. Both are made for every property that exists (with the replacement rule of 00071) and for every new one (`usali_chart()`, `SeedRetainedEarningsMap`, `SeedPosRoundingAccount`).
2. **Approvers on the cashier's own rows.** The constraint of 00017 asks an approver for every adjustment and reversal. The reversal of a split or a merge, the pair `TRF` of a charge to a room and a discount within the limit of the outlet are the cashier's own acts (sections 5.3, 6.3 and 7.3), so `folio_items_approval_ck` accepts a row of the source `POS` without one. The same migration lets a `COMP` payment carry the person who approved it (`payments_approval_ck`).
3. **A void of an earlier day** is the exact opposite of the original row, as an ADJUSTMENT of today (every signed column and component negated, the account and the department copied), not a new run of the engine: the revenue of the day it was sold cannot drift, which is the rule of a transfer (document 30 4.1).
4. **Complimentary is not a line of the payment.** The request to pay takes cash, card, QRIS, transfer, a room and a company; a complimentary bill is `POST …/comp`, in the treatment the bill kept. It takes the name, the reason and the approval, and posts after the lines that it covers.
5. **Cash is one payment.** Several cash lines of a request are one payment on the shift (a second lock of the shift after the first document number would break the order of the locks); the labels ("Guest 1/3") go to its remarks. The shift is the open shift of the cashier for the drawer of the outlet (`CashShiftOfDrawer`); an outlet with no drawer takes no cash (`OUTLET_NO_CASH`).
6. **The rounding** is applied when every payment is cash and their sum is the balance rounded to the unit of the outlet; any other mix is paid to the exact amount. A part payment is allowed: the bill stays open.
7. **Send before pay.** A payment sends the draft lines first (in all modes), so a counter cashier pays what they just took; the gate of a room charge is asked before any draft is sent, so a refusal leaves the attempt and nothing else.
8. **Holds** are shown as HELD for the whole day, not for the 60 minutes before them: the screen has no clock of the floor.
9. **Print** is the browser's window (`utils/posReceipt.ts`); there is no PDF and no agent (stage 3).
10. **Reports** are one JSON answer (days, products, methods, voids, discounts, comps, cashiers, waiters, charges to rooms, open bills) with a CSV of the days in the browser; the closing summary of the night audit does not list the outlets yet.
11. **The tickets** (rows, a VOID ticket, the status shown on the line) exist; the kitchen display and the printing of them are stage 3.
12. **A bill without a folio** (offline, section 12) is not made: `folio_id` is null in the schema and the screens refuse a bill that has none (`BILL_NOT_SYNCED`).
13. **Generic routes.** The charge, adjustment, reversal, payment and transfer routes of a folio answer `OUTLET_FOLIO_USE_POS` for the folio of a bill; a payment void and a refund stay (a payment of the same day is voided with `FOLIO_REVERSAL`).
14. **What a line shows.** A line of a bill (draft and sent, and the receipt) shows its menu price after the discount of the line and before service and tax (`net_amount`), so the lines add up to the net of the summary; service, tax and the total are only in the summary. The line of the guest folio of a charge from an outlet reads "{outlet} · {bill}" with one link to the bill. The report lists a charge to a room, a company and a complimentary as rows of the table of payments, and the open bills with items sold as a box of their own.

**Tests (real PostgreSQL).** `internal/pos/bills_test.go`: the quote of a draft, send once, cash rounded and the replay of a payment, void with approval and its ticket, the waiter that cannot pay, void or discount, a discount before and after the send is the same total, the allocation that adds up, the charge to a room through the lock and the limit, two bills at once on one room (one fits), two waiters on one bill, split and merge without recomputation, reopening, both treatments of a complimentary, the report, the night audit findings, the reads of the front office with bills present with the view of 00075 compared row by row, the day journal of the restaurant bill (cash and room; the transfer account nets to zero; the revenue by department), a split bill and a returned charge (revenue once). `db/tests/schema_test.sql`: the shape of an OUTLET folio, the source POS and its unique line, the COMP payment, the transfer scopes, the bills and their lines. Web: the floor plan, the order and the bill, the payment dialog, the report, the receipt.

### 18.1 Compared with the mockups 10, 11 and 12 (screens of PR 2)

The screens were checked in the running application (desktop 1366 px, Indonesian) against `docs/design/10-pos-denah-meja`, `11-pos-pesanan` and `12-pos-pembayaran`; the images are not in the repository.

**As in the mockups.** 10: the areas as tabs, the tables drawn where they stand and in their shape, the state in words and in colour, the guests, the minutes and the total on the table, the list of open bills with a search, "takeaway / bill without a table", "open drawer", the report link. 11: the menu by category with the price, the lines by round with the state of the ticket, the draft lines with a stepper, a voided line struck through with its reason and approver, the totals (net, service, tax, to pay), send, pay, request, discount, complimentary, move, merge, split. 12: several payments with the rest that must reach zero, "exact", the cash received and the change, the room lookup with the lock, the limit and what is used, the company, the rounding of cash.

**Different on purpose.** The floor plan is the cashier's and the waiter's, one screen; the 390 px layout of the waiter (mockup 16) is stage 3. The menu has a search box beside the categories. The payment is a dialog over the bill, not a screen of its own. The complimentary is a button of the bill with a reason and a name, not a payment method of the dialog. There is no keypad: the amounts are typed with the keyboard of the device.


## 19. As built: PR 3a, realtime, the waiter on a phone, the kitchen display and the print queue (migration 00082)

PR 3 of section 14 is built in two parts: **3a** (this section: the stream of events, the waiter layout, the kitchen display and the queue of print jobs) and **3b** (the print agent: devices, enrolment, claim, result, ESC/POS, drawer, reprint). Nothing prints yet: a ticket of a station that has a printer waits QUEUED.

### 19.1 Realtime

- **The outbox** `pos_events (id, tenant_id, property_id, channel, type, entity_id, txid, at)`: ids only, no amounts or names. It is written by **triggers** (the function `pos_emit`) on `pos_bills`, `pos_bill_lines`, `pos_tickets`, `pos_ticket_lines`, `pos_outlet_sold_out`, `pos_table_holds`, `folio_outlet_locks`, `pos_room_charge_attempts` and `pos_print_jobs`, in the transaction of the change: no use case can forget one, and a change that rolled back sends nothing. The same channel, type and entity again in one transaction is one event. A payment on the folio of a bill touches the bill (`TouchBill`), so the table total follows. `pg_notify('pos_events')` wakes the streams after the commit.
- **Order and resume.** An identity value is taken when a row is inserted and the row is visible when its transaction commits, so "everything after id 7" can skip the row 6 of a slower transaction. Each row therefore carries the id of its transaction (`txid`); a reader sees only the rows whose transaction is older than every transaction still running (`pg_snapshot_xmin`) and reads them in the order `(txid, id)`: such a row is final, nothing can appear behind it. The `id` of the event is the one the client sends back as `Last-Event-ID`; the server finds its place by that id. An id that is not held (older than 48 hours, purged, or of another property) answers **410 `EVENTS_GONE`** and the client reads everything again. A stream with no `Last-Event-ID` begins at the present. The cost: a transaction that stays open for a long time anywhere in the database (a backup that holds a snapshot, a forgotten session) delays the events until it ends; they are not lost.
- **The stream** `GET {P}/pos/events?channels=outlet:{id},station:{id},frontdesk,printers` (Server-Sent Events, read with `fetch`). Authorisation is checked at the start for each channel (`outlet:` pos.order, pos.cashier or pos.manage; `station:` pos.kds or pos.manage; `frontdesk` folio.outlet_charge_open or folio.read; `printers` pos.manage or pos.cashier) and the outlet or station must be of the property (404 for another tenant's id and for one that does not exist alike). A caller who may listen to nothing is told 403 before the channels are read. At most 8 channels; **6 streams for a user and 400 for the process** (429 `TOO_MANY_STREAMS`); a stream ends by itself after 10 minutes (`event: reconnect`) because its token is short-lived; a comment `: heartbeat` every 20 seconds; the write deadline of the server is lifted for it (`http.ResponseController`).
- **One LISTEN connection** per process (the `Hub`), held while any stream is open; each stream keeps its own cursor and reads on a wake-up and on a 2 second timer, so a missed notification delays and never loses. With several API instances the outbox is what keeps them in step.
- **The client** (`composables/usePosEvents.ts`): `fetch` with the token of the session (renewed on 401), parses the frames, keeps the last id, reconnects after 1, 2, 4 ... 15 seconds with that id, reads the screen again after a 410 and when a tab comes back after more than 30 seconds. A page coalesces a burst of events into one read (`debounce`).
- **Used by** the cashier floor plan (`outlet:{id}`), the order and the bill (`outlet:{id}`: another waiter's line, the status of a ticket, a payment), the kitchen display (`station:{id}`), the waiter screens, the list "Outlet charges today" (`frontdesk`; it no longer asks every 20 seconds) and the queue of print jobs in the setup of stations (`printers`).

### 19.2 The kitchen

- **Tickets** are the ones of stage 2 (one for each station of a round; a VOID ticket for a cancellation). 3a adds who moved a ticket (`started_by`, `ready_by`, `served_by`) and a constraint that the status and its times agree. A VOID ticket is **not a card of the display**: the line it cancels stays on its ticket, struck through and marked DIBATALKAN, until the ticket is served; the cancellation is a print job.
- **The board** `GET {P}/pos/stations/{id}/board` (pos.kds): the open tickets in New, Cooking and Ready, oldest first, each with its table, room or customer, bill number, waiter, round (the later rounds are marked `is_addition`, "tambahan"), the lines and how long it has waited; the tickets served in the last hour as the history; the outlets the station serves (one station may serve several outlets); the average time to ready of the last hour and how many tickets are late (more than the `late_after_minutes` of the station).
- **Status** `POST {P}/pos/tickets/{id}/status {action}`: `START` NEW to COOKING, `READY` NEW or COOKING to READY, `SERVE` READY to SERVED (pos.kds, pos.order or pos.cashier). One conditional `UPDATE ... WHERE status = :seen`: two screens that press the same button at once make **one** change (audited once) and both get the same answer; a ticket that has passed the status is 409 `TICKET_STATUS_INVALID`. No lock level is needed: the ticket is not on the ledger path. The status shows on the lines of the bill of the cashier (the stage 2 view derives it from the ticket) and arrives there by the stream.
- **Print queue.** When an order is sent (and when a line is voided) an outlet in `PRINT` mode puts the ticket of a station that has a printer in `pos_print_jobs` (kind TICKET, status QUEUED, `dedupe_key = TICKET:{id}:1`, so a retry makes one job). `GET {P}/pos/print-jobs` (pos.manage or pos.cashier) lists the queue with how many wait for each printer; the setup of stations and printers shows it and follows `printers`. The columns of devices, claim, lease and payload are added by 3b.

### 19.3 The waiter on a phone

`/pos/waiter` (the tables) and `/pos/waiter/bills/:id` (the order) for `pos.order` only, built for 390 px: the areas as chips, the tables as large cards that say their state and total in words, the number of guests before a table is opened; the menu as rows with the price, **Tambah** that becomes **− n +**, "sudah dipesan N" for what the bill already holds, a product that is sold out today that cannot be chosen, the options sheet (variant, extras, a note for the kitchen), "N item draf · bill X", **Lihat bill** (a read-only sheet: the lines with the status of their ticket and the totals) and **Kirim ke dapur dan bar**. There is no payment, void or discount. Two waiters on one bill: each add is its own request under the folio lock of stage 2 (both lines are kept, in the order of `(created_at, uuid)`), and the stream shows each the other's line.

### 19.4 Differences from the design

1. **The events are written by triggers**, not by each use case (10.1 said "every use case inserts a row"): one place, and a new use case cannot forget. Only a payment needs a call (`TouchBill`), because it changes the folio and not a table of the POS.
2. **The cursor is `(txid, id)`**, not `id` (19.1).
3. **Devices, enrolment, claim, result, the ESC/POS document, the drawer job and the reprint are 3b**; `pos_print_jobs` has no `claimed_by` and no `payload` yet.
4. **The configuration check** of 10.2 (an outlet in PRINT mode whose station has no printer) is the one of stage 1; it is not extended.
5. **The kitchen display is read-only about money** and has no sound; the "Selesai 1 jam terakhir" history is a list under the columns, not a page.
6. **Services are not split by outlet on the board**: a ticket names its outlet, the board is one list per station.

**Tests (real PostgreSQL).** `internal/pos/kitchen_test.go`: one ticket for each station and the job for a station with a printer, the flow NEW, COOKING, READY, SERVED with the status on the bill and one audit row for each change, **two kitchen screens pressing READY at once** (three rounds, one change each), a voided line struck through with its cancellation in the queue, the events of a send, **the resume without losing an event** (what came while it was away, once, in order), the channel of another outlet and of another property stay quiet, the permission of each channel, **two waiters adding to one bill at once**. `internal/app/pos_events_api_test.go`: the stream through the router and a real socket, begin at the present, resume by `Last-Event-ID`, ids only in the payload, another tenant (404), no token (401), bad channels (422), a resume point that is not held (410). `db/tests/schema_test.sql`: the events and their triggers, the ticket times, the print jobs. Web: `usePosEvents.test.ts` (frames, resume by id after a drop, backoff, 410), `PosKds.test.ts`, `PosWaiter.test.ts`; the pages that follow the stream are tested with a stub of it that can send an event.

### 19.5 Compared with the mockups 14 and 16 (screens of PR 3a)

The screens were checked in the running application in Indonesian (the kitchen at 1366 px, the waiter at 390 px) against `docs/design/14-pos-layar-dapur` and `16-pos-pelayan-ponsel`; the images are not in the repository.

**As in the mockups.** 14: the dark theme; the station switch (Dapur, Bar) and the outlets the station serves in the header; "Rata-rata 9 mnt · 1 terlambat" and the clock; three columns Baru, Dimasak, Siap diambil with their counts; a ticket with the table or the room, the bill number and the waiter, "tambahan" for a later round, the age (and "siap 2 mnt lalu" when ready), TERLAMBAT in red with a red border; lines with the quantity, the options and the note in orange, a voided line struck through with DIBATALKAN; the buttons Mulai, Siap and Sudah diambil; "Selesai 1 jam terakhir: N tiket · Riwayat". 16: the header with the table, the guests, the bill and the outlet; "Cari menu"; the category chips; a row with the name, the price and "· sudah dipesan N"; **Tambah** and the stepper **− n +**; "Habis hari ini" for a sold-out product (not selectable); "N item draf · bill X", "Lihat bill" and "Kirim ke dapur dan bar" at the foot.

**Different on purpose.** The display lives in the shell of the application (the menu is on its left) and is not a full-screen kiosk; its station chips show only the stations with a display. A new ticket has "Siap" beside "Mulai" (a bar drink is made at once). The waiter's header is the page header of the application with a back arrow; the clock and the avatar of the mockup are the top bar of the shell. The send button reads "Kirim ke dapur dan bar" as the cashier's does. The mockup's keypad-less guest entry is a stepper in a small sheet. "Lihat bill" opens a sheet instead of a page.
