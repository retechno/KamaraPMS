# 35. Inventory and recipes: items, warehouses, purchasing, costing, stock counts and the deduction of POS sales

Status: **design, waiting for the owner's approval (2026-10-16); D1 to D22 of section 15 decided on 2026-10-16: as recommended; D23 (added after the review of PR #40) is open.** Asked by the owner on 2026-10-16 as stage 4 of `34-pos.md` section 14. The owner's decisions are in the request (numbered I1 to I12, referred to here as **I1 to I12**) and are **not re-opened**. Where one of them touches a rule of the platform or of an earlier document, section 1 says so, with the reason and what this design does about it. Nothing here is built; there is no code and no migration in this change. Words in `code font` are tables, columns, permissions or error codes; every new name is a proposal until approved. Decisions are in section 15 (D1 to D22 decided as recommended, D23 open).

Read with: `02-database-schema.md`, `03-financial-engines.md` (one posting path per ledger), `05-transactions-locking.md` (lock order), `11-cashier-budget-cashflow-card.md` (shifts: a market purchase paid in cash), `12-departments.md` (department of a line, the rule of an account), `14-supplier-credit-notes.md`, `26-subscriptions.md` (module gate), `31-deposit-release.md` (a setting that locks at the first transaction, the day-close journal), `32-distinct-approver.md` (kinds of approval), `33-setup-read-permissions.md`, `34-pos.md` (recipes: 3.2, 7, 14) and `internal/payables`.

## 0. The idea in one page

- **One stock ledger.** Every change of quantity or of value is a row of `inv_movements`: append-only, dated by the open business day, written by one package (`internal/inventory`, the only writer of the ledger and of the balances, the way `FolioPostingService` is the only writer of `folio_items`). A correction is a reversing row, never an edit (section 7.2).
- **Balances are a table, not a sum.** `inv_balances` holds quantity and value per item and warehouse, changed under a row lock in the same transaction as the movement; the ledger can rebuild it and a test proves they agree (section 7.5).
- **Two costing methods, one per property** (I3): moving average or FIFO, chosen before the first stock transaction and then locked, both per warehouse (D1). Section 4 states what each does for receipts, transfers, negative stock, returns and counts.
- **Purchasing is a chain** (I4): request (PR, approved by the rule of document 32) -> order (PO) -> goods receipt -> a **draft supplier bill made by the receipt** -> the supplier's invoice confirms it into a real bill in `payables`, matched against the PO and the receipt (three-way). A receipt without a PO is allowed for the daily market purchase.
- **Accounting is perpetual and written at the day close** (I8): the day-close journal gains lines read from the stock ledger, like it reads the folio ledger. Goods received are booked to inventory against a new liability **"Goods received not invoiced"** (GRNI); the supplier bill debits GRNI, so a purchase is **never booked twice**.
- **POS sales deduct stock through a queue** (I6, D4): posting a POS line inserts a row in a queue in the same transaction; the stock is deducted a moment later by a worker, and always before the day closes. The cashier never waits for a stock lock and a sale is never refused for lack of stock (I7).
- **Everything else is a document with a state**: requisition and transfer between warehouses, issue to a department, waste, stock count with a freeze. They never make the stock negative (I7); only a POS sale may.
- **Inventory is a module of its own** (I12): the code `INVENTORY` already reserved in `26-subscriptions.md`. Recipes work only when POS and Inventory are both in the plan.

## 1. Findings and conflicts (read this first)

| # | Finding | What the design does |
|---|---|---|
| F1 | **A supplier bill has no draft state.** `supplier_bills.status` is `POSTED` or `VOIDED`; `payables.PostBill` writes the journal at once, on the bill date (migration 00031, `bills.go`). I4 asks for a draft bill made by the receipt. | The draft is **inventory's own record** (`inv_bill_drafts`), made inside the receipt's transaction. It becomes a bill only when a person confirms it with the supplier's invoice: `inventory.ConfirmBill` calls the existing `PostBill`. `payables` gets no draft status. The bill, its journal, its VAT treatment, its payments and its credit notes are exactly what they are today (section 5.3). |
| F2 | **Double booking.** A bill line debits an expense or asset account chosen by hand. If goods are booked to inventory at the receipt (perpetual, I8) **and** the bill debits 1310 again, the purchase is counted twice. | The receipt is booked **Dr inventory / Cr GRNI** (account 2130, new). A bill that comes from a draft has its lines on **GRNI**, so it debits what the receipt credited. Accounts 13xx and GRNI become **control accounts** once the module has its first movement: a hand-entered bill line or manual journal on them is refused (`CONTROL_ACCOUNT`, the code that already exists for the payables account); the one way in is the inventory path (section 6.4). |
| F3 | **The day-close journal reads only the folio ledger** (`DayActivity` over `folio_item_gl`, `internal/accounting/journals.go`). | `accounting` gets a second, small source: an interface `StockDayLines(ctx, property, date)` implemented by `internal/inventory`, whose lines are added to the same map (account, source type, reference, department) before the "journal balances" check. Each stock movement carries the debit and credit account **snapshotted when it is posted** (as a folio item carries its revenue account, document 12), so a later change of a category never rewrites a day. The lines of the stock source balance by themselves. One journal per day, as now; the day is posted once (`gl_day_posts`). |
| F4 | **The trigger `business_day_must_be_open` (migration 00065) names the tables that hold "posting rows".** A stock movement is a row dated by the business day whose journal is written at the close of that day. A movement dated a closed day would never be journaled. | `inv_movements` **is** a posting table and gets the trigger. The documents around it (requests, orders, receipt and count headers, drafts, queue rows) are operational records and do not (section 7.1). |
| F5 | **Global lock order has no place for stock.** `05-transactions-locking.md` ends: ... groups (45) -> accounting settings (46) -> suppliers (47) -> bank (48) -> tax and budget (49) -> sequences (50). `payables.PostBill` takes the accounting settings, then the supplier, then the sequence. A use case that locks stock **after** `PostBill` would break the order; one that locks stock **before** it must not be below 45. | New level **`LevelStock = 45`**, shared with the booking groups (the order allows re-entering a level; a booking never touches stock). Inside the level the order is fixed: documents (request, order, receipt, bill draft, return, requisition, transfer, issue, count), then the warehouse row, then balance rows ascending. `ConfirmBill` locks the stock rows first and calls `PostBill` second (settings 46, supplier 47, sequence 50), which is ascending. No renumbering of the existing levels (section 5.1). |
| F6 | **Approvals in document 32 are corrections with the approver's credentials, given at the moment of the act.** A purchase request is approved later, by another person in their own session. | A second, small mode of `iam.Verify`: a **decision** by the signed-in approver (`Decision: true`), where the rule compares the approver with a named `Subject` (the requester) instead of with the caller. Kinds, amounts, rules (`FREE`, `DISTINCT`, `DISTINCT_ABOVE`), the screen of the rules and the structural test are unchanged. Used by the kinds `INV_PR` and `INV_COUNT` (section 9.2). The alternative (the approver types a password next to the requester) is D7. |
| F7 | **A POS sale must not wait for stock and must not be refused for lack of it** (I6, I7), but the POS posting runs under the folio lock (level 42), where every extra lock lengthens the cashier's wait. | The deduction is a **queue row inserted in the POS transaction** and applied by a worker, never inside the cashier's lock (section 5.6 compares the two ways and chooses). |
| F8 | **`pos_recipe_lines.inventory_item_id` has no foreign key, `unit` is free text** (`34-pos.md` 3.2, migration 00080). | PR 4 adds the foreign key and makes `unit` a unit **of that item** (composite FK to `inv_item_units`). Existing rows are empty (no endpoint writes them), so the migration needs no data step. |
| F9 | **A property may already have a balance on 1310 to 1340** (hand-entered bills and journals, the chart exists since 00028). Opening the module would add a second balance on top. | The first opening posting is refused with `INVENTORY_GL_NOT_EMPTY` (the balances in the message) until the accountant has cleared those accounts by a manual journal. Opening stock is booked against 3900 "Opening balance equity" (D11). |
| F10 | **The module gate classifies a route by one module** (document 26 3). The recipe routes belong to POS **and** Inventory. | Recipe routes are classified `POS` by the table and add a check of `INVENTORY` in the handler (403 `MODULE_NOT_IN_PLAN`, `context.module = INVENTORY`). The POS deduction hook asks the same entitlement (I12). |
| F11 | **Costing methods that change history.** A property that switches method after it has stock would have layers or averages that mean something else. | The method is a setting that **locks at the first stock movement**, by a database trigger, exactly as `deposit_release_mode` does (document 31 3): a conditional `UPDATE ... WHERE costing_locked_on IS NULL` in a trigger on `inv_movements`, a guard trigger on the setting, the audit entry written by the first day close. |
| F12 | **Night audit** has a list of checks (document 34 13.1). Stock adds a queue that must be empty and a count that may be open. | Section 5.7: the queue is drained as a step of the close (blocker if a row cannot be applied); an open count is a blocker or a warning by setting (D6). |
| F13 | **Payables void.** `payables.VoidBill` reverses the journal and knows nothing of stock. A matched bill voided there would leave the match, the variance and the draft in the wrong state. | `payables` gets one optional guard (`VoidGuard`, set by inventory): a bill with a stock match answers 409 `BILL_HAS_STOCK_MATCH`; the way is **Inventory > Bill drafts > Unmatch**, which reverses the variance, voids the bill through `payables.VoidBill` (with its approval) and reopens the draft. Credit notes of document 14 need nothing (section 5.3.6). |
| F14 | **`gl_account_map_key_ck` is a closed list of keys** (widened for `COMP` and `POS_ROUNDING`). | Migration 00084 widens it with `INV_GRNI`, `INV_PPV`, `INV_WASTE`, `INV_COUNT_VARIANCE`, and creates the four accounts for every property that exists (with the replacement rule of 00071: a taken code goes to the next free one) and in `usali_chart()` for new properties (3.1). |

## 2. Vocabulary

| Term | Meaning |
|---|---|
| Item | Something kept in stock and counted: a food, a drink, a housekeeping amenity, a spare part, stationery. Belongs to one property and one category. |
| Category | Groups items and decides their **accounts** (inventory, cost of sales, issue, waste, count variance). |
| Warehouse | A place that holds stock: the main store, the kitchen of an outlet, the bar, the housekeeping store. Stock exists only in a warehouse. |
| Stock unit | The unit the quantity is kept in (botol, kg, pcs). **Purchase unit** (karton = 12 botol) and **recipe unit** (ml) convert to it (I5). |
| Movement | One row of the stock ledger: a quantity and a value that enter or leave one warehouse for one item. |
| Balance | Quantity and value of an item in a warehouse, now. |
| Layer | Under FIFO: the part of a receipt that is still on hand, with its own unit cost. |
| Shortfall | Quantity sold from a warehouse that did not have it (I7), costed by estimate, waiting for the goods to come in. |
| PR, PO, GR | Purchase request, purchase order, goods receipt. |
| GRNI | "Goods received not invoiced": the liability account that holds a receipt until the supplier's bill is entered. |
| Draft bill | What a receipt makes for its supplier: lines at the PO price, not yet an invoice. |
| Three-way match | The bill is checked against the PO (price) and the receipt (quantity). |
| PPV | Purchase price variance: the part of the difference between invoice and receipt that is not stock any more. |
| Requisition | A warehouse (an outlet) asks the main store for goods; a transfer answers it. |
| Count | A stock count of one warehouse; it freezes the warehouse while it runs. |
| Consumption queue | The rows that carry a POS sale to the stock ledger. |

## 3. Data model

All tables carry `tenant_id` and `property_id` with **composite foreign keys** (no cross-tenant reference possible), the standard audit columns, a `version` where an edit screen needs optimistic concurrency, `numeric(18,3)` for money, `numeric(18,6)` for quantities and unit costs, `date` for dates and `timestamptz` (UTC) for instants. Quantities are always stored in the **stock unit**. Constraint names are mapped in `internal/platform/db/errors.go`. Migration numbers start at **00084** (section 14).

### 3.1 Changes to existing tables (spread over the migrations of section 14)

| Table | Change |
|---|---|
| `gl_account_map` | key check widened: `INV_GRNI`, `INV_PPV`, `INV_WASTE`, `INV_COUNT_VARIANCE` (00084). |
| accounts, `usali_chart()` | new accounts, for new properties from the function and for existing ones by a data step with the replacement rule of 00071: **2130 "Goods received not invoiced"** (liability, group PAYABLES, department NONE), **5290 "F&B stock waste and spoilage"**, **5295 "Stock count variance"** and **5299 "Purchase price variance"** (expenses under F&B department expenses, department OPTIONAL with the default department "Food and beverage"). The 13xx accounts and the cost accounts 5210/5220 (food and beverage sales), 5140/5150 (housekeeping), 6160 (office supplies), 6470 (maintenance supplies) already exist and are the defaults of section 6.1. |
| `business_day_must_be_open` | trigger on `inv_movements (business_date)` (00084). |
| `pos_outlets` | `warehouse_id bigint NULL`, composite FK to `inv_warehouses`: the warehouse an outlet sells from (00087). NULL = this outlet deducts nothing. |
| `pos_recipe_lines` | FK `(property_id, inventory_item_id)` to `inv_items`; `unit` becomes a unit of the item: composite FK `(property_id, inventory_item_id, unit)` to `inv_item_units`; `CHECK (inventory_item_id IS NOT NULL)` once the FK exists; unique `(product_id, COALESCE(option_id, 0), inventory_item_id)` (00087). |
| `payables` | none in the tables. One optional Go guard `VoidGuard` on `VoidBill` (F13). |
| `document_sequences` types | `INV_PR`, `INV_PO`, `INV_GR`, `INV_RET`, `INV_REQ`, `INV_TRF`, `INV_ISS`, `INV_CNT` (gapless per property, numbers like `PO-000007`). |
| permissions, approval kinds | section 9. Granted to **no existing role** (I12). |

Down of every migration: refused while a row of the new tables exists (as 00074, 00075).

### 3.2 PR 1 (migration 00084): masters, ledger, balances, opening stock

```
inventory_settings                                   -- one row per property, made by the first admin action
  property_id PK
  costing_method        CHECK IN (AVERAGE, FIFO) DEFAULT 'AVERAGE'   -- locked at the first movement (F11)
  costing_locked_on     date NULL                                     -- set by the trigger on inv_movements
  price_tolerance_pct   numeric(5,2) DEFAULT 1.00   -- bill price above/below the PO price without an approval (5.3.4)
  qty_tolerance_pct     numeric(5,2) DEFAULT 0.00   -- a bill quantity above the receipt
  over_receipt_pct      numeric(5,2) DEFAULT 5.00   -- a receipt quantity above the PO line
  po_requires_pr        boolean DEFAULT true                          -- D13
  direct_receipt_limit  numeric(18,3) NULL          -- receipt without a PO: NULL = not allowed, 0 = no limit, > 0 = most per receipt
  count_blocks_night_audit boolean DEFAULT true     -- D6
  count_blind           boolean DEFAULT true        -- the count sheet does not show the book quantity
  version

inv_units              (id, property_id, code varchar(10) UK per property, name)               -- ml, g, kg, l, botol, karton, pcs, pack ...
inv_categories         (id, code UK, name, kind CHECK IN (FOOD, BEVERAGE, SUPPLIES, OTHER),
                        inventory_account_id NOT NULL,   -- 1310 / 1320 / 1330 / 1340 by default; must be an ASSET in group INVENTORIES
                        cogs_account_id NULL,            -- cost of sales for a POS sale: 5210 food, 5220 beverage; NULL = POS may not sell it
                        issue_account_id NULL,           -- the default expense of an issue to a department (5150, 6160, 6470 ...)
                        waste_account_id NULL,           -- NULL = the key INV_WASTE
                        variance_account_id NULL,        -- NULL = the key INV_COUNT_VARIANCE
                        is_active, sort_order, version)
inv_items              (id, code UK per property, name, category_id, stock_unit_id,
                        reference_cost numeric(18,6) NULL,  -- used only when nothing better exists (4.4, recipe cost)
                        default_supplier_id NULL, barcode varchar(40) NULL, note,
                        is_active, version)
inv_item_units         (item_id, unit_id, stock_num int, stock_den int,        -- 1 of this unit = stock_num / stock_den stock units
                        is_purchase, is_recipe, is_default_purchase, is_default_recipe)
                        PK (item_id, unit_id); CHECK (stock_num > 0 AND stock_den > 0); the stock unit itself is 1/1
inv_warehouses         (id, code UK, name, kind CHECK IN (MAIN, OUTLET, DEPARTMENT), outlet_id NULL → pos_outlets, department_id NULL → departments,
                        count_id NULL → inv_counts,     -- set while a count freezes the warehouse (5.5)
                        is_active, version)
                        CHECK (kind = 'OUTLET') = (outlet_id IS NOT NULL); at most one active warehouse per outlet
inv_levels             (warehouse_id, item_id, min_qty, max_qty NULL, reorder_qty NULL)   PK (warehouse_id, item_id)       -- I10: low-stock alert
inv_supplier_items     (supplier_id, item_id, purchase_unit_id, last_price numeric(18,3), supplier_sku)                   -- default for a PO line

inv_movements          -- THE LEDGER. Append-only (trigger: no UPDATE, no DELETE, no TRUNCATE); trigger business_day_must_be_open(business_date)
  id, tenant_id, property_id
  warehouse_id, item_id
  kind          CHECK IN (OPENING, RECEIPT, RECEIPT_REVERSAL, SUPPLIER_RETURN, TRANSFER_OUT, TRANSFER_IN, ISSUE, WASTE,
                          COUNT_GAIN, COUNT_LOSS, POS_SALE, POS_SALE_RETURN, COGS_TO_WASTE, COST_ADJUST, NEG_SETTLE)
  qty_delta     numeric(18,6)     -- + in, - out, in stock units
  value_delta   numeric(18,3)     -- the effect on the value of the balance; same sign as qty_delta, or zero for COST_ADJUST / NEG_SETTLE / COGS_TO_WASTE
  unit_cost     numeric(18,6)     -- value / qty, information only
  cost_estimated boolean          -- true when no real cost existed (4.4)
  business_date date              -- the OPEN day when written
  source_date   date NULL         -- POS sale: the day of the sale (it may be applied later, 5.6)
  source_type   varchar(20), source_id bigint, source_line int           -- the document and its line
  reverses_id   bigint NULL UK    -- the movement this one undoes (once)
  gl_debit_account_id NULL, gl_credit_account_id NULL, gl_amount numeric(18,3) >= 0, gl_department_id NULL
                                  -- what the day close journals; both accounts or neither (a transfer has none); snapshotted
  created_at, created_by
  UK (property_id, source_type, source_id, source_line, kind)           -- a document line is posted once
  CHECK (qty_delta <> 0 OR kind IN (COST_ADJUST, NEG_SETTLE, COGS_TO_WASTE))
  CHECK ((qty_delta > 0 AND value_delta >= 0) OR (qty_delta < 0 AND value_delta <= 0) OR qty_delta = 0)
  IDX (warehouse_id, item_id, business_date, id)   (property_id, business_date)

inv_balances           -- derived, rebuilt from the ledger by a test and by an admin check; changed only by the ledger writer
  id, property_id, warehouse_id, item_id   UK (warehouse_id, item_id)
  qty numeric(18,6), value numeric(18,3), last_unit_cost numeric(18,6) NULL, version
  CHECK (qty <> 0 OR value = 0)                       -- no value stranded on an empty shelf
  CHECK ((qty >= 0 AND value >= 0) OR (qty <= 0 AND value <= 0))
inv_layers             -- FIFO only (empty under AVERAGE)
  id, warehouse_id, item_id, movement_id (the IN), received_on, qty_in, qty_remaining, value_remaining numeric(18,3)
  CHECK (qty_remaining >= 0 AND qty_remaining <= qty_in), CHECK (qty_remaining <> 0 OR value_remaining = 0)
  IDX (warehouse_id, item_id, received_on, id) WHERE qty_remaining > 0
inv_layer_uses         (movement_id, layer_id, qty, value)    -- append-only: what an OUT took from which layer, so a return can restore it
inv_shortfalls         -- I7: a POS sale that found less than it needed
  id, warehouse_id, item_id, movement_id, qty_open > 0, qty_original, est_unit_cost numeric(18,6),
  gl_debit_account_id (the cost-of-sales account of the sale), gl_department_id,
  status CHECK IN (OPEN, SETTLED, CLOSED_BY_COUNT), business_date
inv_openings + inv_opening_lines   (header and lines of the opening stock: warehouse, item, qty, unit cost; one opening per item and warehouse)
```

The triggers: `inv_movements_append_only`, `business_day_must_be_open('business_date')`, and `inv_movements_lock_costing` (F11: the conditional `UPDATE inventory_settings SET costing_locked_on = NEW.business_date WHERE property_id = NEW.property_id AND costing_locked_on IS NULL`; a locked property is neither locked nor written by a movement, which a test holds by the `xmin` of the row, as in document 31 6). `inv_balances` is not append-only; its writer is the ledger package.

### 3.3 PR 2 (migration 00085): purchasing

```
inv_requests           (id, request_number, client_uuid UK, requester_id, department_id NULL, warehouse_id NULL (deliver to), needed_on,
                        status CHECK IN (DRAFT, SUBMITTED, APPROVED, REJECTED, CONVERTED, CANCELLED),
                        est_total numeric(18,3), decided_by NULL, decided_at NULL, decision_note, version)
inv_request_lines      (request_id, line_no, item_id, qty_purchase, purchase_unit_id, qty_stock, est_unit_price numeric(18,3) NULL, note)
inv_purchase_orders    (id, po_number, client_uuid UK, supplier_id, warehouse_id, request_id NULL UK-per-line, order_date, expected_on,
                        status CHECK IN (DRAFT, ORDERED, PARTIAL, RECEIVED, CLOSED, CANCELLED), notes, total numeric(18,3), version)
inv_po_lines           (po_id, line_no, item_id, purchase_unit_id, qty_purchase, qty_stock, unit_price numeric(18,3) -- per purchase unit, net of VAT,
                        amount numeric(18,3), qty_received_stock numeric(18,6) DEFAULT 0, closed boolean)    -- qty_received is kept under the PO lock
inv_receipts           (id, receipt_number, client_uuid UK, idempotency_key UK, supplier_id, po_id NULL, warehouse_id,
                        received_on date,        -- the business day it was received; a delivery note date may differ (delivery_note_date)
                        delivery_note varchar(60), status CHECK IN (POSTED, REVERSED), reverses_receipt_id NULL, total_value, note, created_*)
inv_receipt_lines      (receipt_id, line_no, item_id, po_line_id NULL, qty_purchase, purchase_unit_id, qty_stock, unit_price (per purchase unit), value numeric(18,3),
                        movement_id)                -- value = qty_purchase * unit_price at the currency decimals: the purchase-unit price is the exact figure
inv_bill_drafts        (id, receipt_id UK, supplier_id, status CHECK IN (OPEN, PARTIAL, BILLED, DISCARDED), total numeric(18,3), version)
inv_bill_draft_lines   (draft_id, receipt_line_id UK, qty_stock, amount)
inv_bill_matches       (id, receipt_line_id, bill_id → supplier_bills, bill_line_no, qty_stock, bill_cost numeric(18,3),   -- cost basis: amount (+ VAT when the treatment is EXPENSE)
                        receipt_value numeric(18,3), variance numeric(18,3), clearing_movement_ids,
                        credit_note_id NULL → supplier_credit_notes, unmatched_at NULL, created_*)
inv_returns            (id, return_number, client_uuid UK, supplier_id, warehouse_id, receipt_id, reason, status POSTED|REVERSED, total_value)
inv_return_lines       (return_id, line_no, receipt_line_id, item_id, qty_stock, value, movement_id)
```

### 3.4 PR 3 (migration 00086): requisition, transfer, issue, waste, count

```
inv_requisitions       (id, req_number, client_uuid UK, to_warehouse_id (the one that asks), from_warehouse_id (the main store), requester_id, needed_on,
                        status CHECK IN (DRAFT, SUBMITTED, PARTIAL, FULFILLED, REJECTED, CANCELLED), version)
inv_requisition_lines  (requisition_id, line_no, item_id, qty_requested, qty_issued DEFAULT 0, unit_id)
inv_transfers          (id, trf_number, client_uuid UK, idempotency_key UK, requisition_id NULL, from_warehouse_id, to_warehouse_id, status POSTED|REVERSED, note)
inv_transfer_lines     (transfer_id, line_no, item_id, qty_stock, value, out_movement_id, in_movement_id)
inv_issues             (id, iss_number, client_uuid UK, idempotency_key UK, warehouse_id, purpose CHECK IN (USE, WASTE),
                        department_id NULL (required for USE), outlet_id NULL, reason_code CHECK IN (SPOILAGE, EXPIRED, BREAKAGE, STAFF_MEAL, TASTING, OTHER) NULL for USE,
                        status POSTED|REVERSED, note)
inv_issue_lines        (issue_id, line_no, item_id, qty_stock, value, expense_account_id NULL (default: category), movement_id)
inv_counts             (id, cnt_number, client_uuid UK, warehouse_id, scope CHECK IN (ALL, CATEGORY, LIST), category_id NULL,
                        status CHECK IN (OPEN, SUBMITTED, APPROVED, CANCELLED), started_at, started_by, business_date_started,
                        submitted_by, decided_by, decided_at, total_variance_value, version)
inv_count_lines        (count_id, item_id, book_qty (snapshot at the freeze), counted_qty NULL,
                        counted_at timestamptz NULL,      -- set by the SERVER (injected clock) whenever counted_qty is entered or changed; CHECK ((counted_qty IS NULL) = (counted_at IS NULL))
                        expected_qty, held_before_qty, variance_qty, variance_value,   -- all four computed at approval (4.7), NULL before
                        counted_by, note)
```

### 3.5 PR 4 (migration 00087): the POS side

```
inv_consumption_queue
  id, tenant_id, property_id, outlet_id, warehouse_id
  pos_bill_line_id bigint (composite FK to pos_bill_lines), kind CHECK IN (SALE, VOID_RETURN, VOID_WASTE)
  sale_date date                         -- the business day of the posting
  components jsonb                       -- [{item_id, qty_stock}] snapshot of the recipe, expanded at posting (5.6)
  status CHECK IN (PENDING, HELD, APPLIED, CANCELLED, FAILED), attempts, error_code, movement_ids bigint[], applied_on, created_at, applied_at
  UK (pos_bill_line_id, kind)            -- a line is queued once; the idempotency of the hook
  IDX (property_id, status, id) WHERE status IN (PENDING, HELD, FAILED)
```

Plus the `pos_outlets.warehouse_id` and `pos_recipe_lines` changes of 3.1. A queue row is an operational record (no day trigger); the movements it produces are.

## 4. Costing

### 4.1 What is common to both methods

- **Per item and per warehouse.** The cost lives where the goods are (D1). A transfer carries cost with the goods; there is no property-wide average to lock.
- **The value of a movement is rounded once**, at `properties.currency_decimals`, half away from zero; the balance changes by exactly that figure. An OUT that empties the balance takes **all the value that is left** (so the balance never keeps a residue, `CHECK (qty <> 0 OR value = 0)`).
- **A purchase is valued from the purchase unit**: `value = qty_purchase x unit_price` (rounded), so the conversion of units (karton 12 botol, 1 ml = 1/650 botol) never produces a rounding difference in money. The unit cost per stock unit is derived and informational. Conversions are rational (`stock_num / stock_den`); a quantity is rounded to six decimals when stored; a count corrects what tiny roundings leave (D18). Choosing the smallest practical unit as the stock unit (ml, g) makes recipes exact; the design allows either.
- **Estimated costs.** When no cost exists (4.4) the movement is flagged `cost_estimated` and appears in a report; it is never silent.
- **The method is read once per transaction** from `inventory_settings` and decides the code path of the ledger writer (section 5.1); nothing else in the module knows the method.

### 4.2 Moving average (per warehouse)

| Event | Quantity and value |
|---|---|
| Receipt, transfer in, count gain, return of a sale | `qty += q`, `value += v` (the average is `value / qty`, never stored). |
| Issue, waste, sale, transfer out, count loss | `v = round(q x value / qty)`; if `q = qty`, `v = value`. |
| Return to supplier | at the cost of the **receipt line** it returns (4.6), not the average. |
| Cost adjustment (a bill price different from the receipt, 5.3.4) | `value += part` for the quantity still on hand; the average moves. |

### 4.3 FIFO (per warehouse)

- Each IN creates a **layer** (`qty_in`, `qty_remaining`, `value_remaining`), dated by the day it was received.
- An OUT consumes layers in `(received_on, id)` order. Each layer gives `round(q_layer x value_remaining / qty_remaining)` (the last piece of a layer takes what is left); the pieces are recorded in `inv_layer_uses`, and `value_delta` is their sum. One movement may touch several layers.
- **Layers are protected by the balance row** of their item and warehouse: every change of a layer goes through the ledger writer, which holds that balance row `FOR UPDATE`, so layers need no lock of their own.
- A **return to stock** (a POS void before cooking, a reversed issue) puts the quantity back **into the layers it came from**, with the value that was taken (`inv_layer_uses` is the memory). A transfer in creates one layer for each layer the transfer out consumed, with the same unit cost **and the same date**, so age is not lost.
- A count gain creates a layer dated the count day at the last known unit cost (4.4); a count loss consumes the oldest.
- Invariant, tested: `balance.qty = sum(layer.qty_remaining) - sum(open shortfalls)`, and the same for value.

### 4.4 Negative stock and the correction when goods arrive (I7)

Only a **POS sale** may take a balance below zero. A transfer, an issue, a waste, a return to supplier and a count refuse (409 `NEGATIVE_STOCK_NOT_ALLOWED`, with the item, the warehouse and the quantity available).

1. The sale takes what the warehouse has at the real cost (average or layers). The part it does not have, `s`, is costed at an **estimated unit cost** `e`: the last unit cost of the balance (`last_unit_cost`, the cost of the last IN), else the item's `reference_cost`, else the cost of the newest receipt of the item in any warehouse of the property, else **zero** with `cost_estimated`.
2. The movement `POS_SALE` carries the whole quantity and the whole value; the balance becomes negative (`qty -s`, `value -s x e`). A row of `inv_shortfalls` records `s`, `e`, the cost-of-sales account and the department of the sale. Cost of sales is booked at the estimate on the day of the sale; the report "Stock sold below zero" lists the open shortfalls (item, warehouse, quantity, since when, menu items that caused it).
3. **When goods come in** (a receipt or a transfer in; a count gain is not a settlement, item 4) at unit cost `c`, the IN first **settles the open shortfalls oldest first**. For the covered quantity `k` of a shortfall with estimate `e` it writes a row `NEG_SETTLE` with `qty_delta = 0` and `value_delta = -k x (c - e)`, journaled **Dr cost of sales / Cr inventory** (reversed in sign when the real cost was lower than the estimate), with the department and account of the original sale. The IN movement itself is booked at `c` for its whole quantity. The result: the balance holds the right value for what is really on hand, the cost of sales carries the real cost of the goods sold, and nothing is lost between the two. The method makes no difference to the rule: under **FIFO** the covered quantity creates no layer (the layers hold only the part of the IN that is really on hand); under **average** the balance is the plain sum plus the settlement row.
4. A **count** that sets the quantity to what was found ends the shortfalls of that item and warehouse without a settlement (`CLOSED_BY_COUNT`): the physical result already says what exists, and the cost of the sales stands as estimated. This is how a negative balance that was never explained by a receipt is cleared.
5. Worked in 8.4.

### 4.5 Transfers between warehouses

The transfer out is valued like any OUT of the source (average, or the layers consumed). The transfer in receives **exactly that value** (average: the destination balance adds the value, its average moves; FIFO: layers copied, 4.3). There is no revaluation and **no journal**, because both warehouses of an item are under the same inventory account (the category decides the account, not the warehouse). The ledger holds the pair (`TRANSFER_OUT`, `TRANSFER_IN`) with `gl_amount = 0` and no accounts. The source must have the quantity (I7); the destination may be negative (a shortfall there is settled by this very transfer, 4.4).

### 4.6 Return to supplier

A return names the **receipt line** it returns. It is valued at the **cost that receipt line created**, not at today's average: under average the value out is the line's value for the quantity returned (`qty x unit cost of the line`), under FIFO the layer of that line is consumed first, then the oldest. Journal: Dr GRNI / Cr inventory. If the receipt was already billed, the supplier's credit note (document 14) is entered in `payables` against the bill line; linked to the return, it is matched like a bill and any difference between the credit note and the cost of the goods is cleared the same way (5.3.6). A return is allowed only for stock that is on hand (I7). An average that has moved since the receipt makes the balance value differ from `qty x avg` by the small difference; that difference stays in the balance (it is the effect of the line's real cost) and is not an error.

### 4.7 Count differences

A count line has `book_qty` (taken at the freeze), `counted_qty` and `counted_at` (the server's instant when the quantity was entered or last changed). Sales that happen while the warehouse is frozen are held (5.5), and a sale made **before** `counted_at` has already taken its goods off the shelf the counter looked at. So at approval:

- `held_before_qty` = the signed sum, in stock units, of the components for this item and warehouse of queue rows `HELD` with `created_at < counted_at` (a `SALE` row counts negative, a `VOID_RETURN` positive, a `VOID_WASTE` nothing);
- `expected = book_qty + held_before_qty`;
- **`variance = counted - expected`**.

The held rows before `counted_at` are applied **first**, as ordinary `POS_SALE` / `POS_SALE_RETURN` movements, so the balance is `expected` when the count adjusts it; the held rows from `counted_at` on are applied **after** the count. Nothing is deducted twice (worked in 8.6). The comparison is by item: a queue row whose components touch several items is applied component by component, each on its side of its own item's `counted_at` (the component carries an `applied` flag in the snapshot; the row is `APPLIED` when all are). An item that was not counted has no `counted_at`: its held rows are applied after the count, and it is not adjusted. Under **FIFO** a held sale applied first consumes the oldest layers, then a loss consumes the oldest remaining and a gain makes a layer at the last unit cost, exactly as in 4.3. With **negative stock**: if applying the held sales takes the balance below zero, the shortfall (4.4) is created at the estimate, and the count (which sets the quantity to what was found) closes it without a settlement (4.4 item 4); `expected` may then be negative and the gain is `counted - expected`, valued at the last unit cost, so the net effect on inventory is the counted quantity at cost and the sale stays in cost of sales at its estimate.

 A **loss** is an OUT of `|variance|` valued by the method (average, or oldest layers); a **gain** is an IN valued at the last unit cost (4.4); neither may leave the balance negative (a counted quantity is never negative; `expected` is read after the held sales were applied). The edge of `counted_at` is as exact as the clock of the server: a sale whose queue row was inserted at the same instant counts as after. The pair of accounts is the category's count-variance account against its inventory account. A shortfall of the item is closed (4.4 item 4).

### 4.8 The two methods side by side

| Question | Moving average | FIFO |
|---|---|---|
| Where cost lives | `inv_balances.value / qty` | `inv_layers` (value per layer) |
| Cost of an OUT | `q x average` (the last unit takes the rest) | the oldest layers; may touch several |
| Transfer | value moves with the goods; destination average moves | layers copied with unit cost and date |
| Negative sale | estimate; settled when goods come in, 4.4 | same; the covered part makes no layer |
| Return to supplier | the line's value | the line's layer first |
| Return to stock (POS void) | value restored at the cost taken | quantity back into the same layers |
| Count loss / gain | average / last cost | oldest layers / new layer at last cost |
| Bill price variance | `value += part` on hand | the layer of the receipt line is revalued |
| Storage | `inv_layers` stays empty | `inv_layers` and `inv_layer_uses` are used |
| Reports | stock card shows value and the running average | stock card also shows the layers |

## 5. Use cases and locks

### 5.1 The order of locks

The global order of `05-transactions-locking.md` gets one change: **`LevelStock = 45`** (shared with the booking groups, which never meet stock in a transaction; the structural test below holds it). Inside the level, in this order:

1. the document rows of the use case (request, order, receipt, bill draft, return, requisition, transfer, issue, count), ascending id;
2. the **warehouse** row (`FOR SHARE` for every mutation, so a mutation sees a freeze; `FOR UPDATE` to start or end a count);
3. the **balance** rows of every (warehouse, item) the use case touches, `FOR UPDATE`, **in one call, ascending id**. The missing rows are created first by `INSERT ... ON CONFLICT DO NOTHING`, which is safe and holds no lock that matters.

`db.LockTable` gets the tables `StockDocuments`, `Warehouses` and `StockBalances` at that level. The order of a use case that also touches money is:

`business day (10, share) -> STOCK (45) -> accounting settings (46) -> supplier (47) -> sequences (50)`

(POS never takes stock locks inside its own transaction: it only inserts a queue row, 5.6. The night audit takes the day `FOR UPDATE`, the stays and folios of the room charges, then stock to drain the queue, then the accounting settings for the journal: ascending.)

so `ConfirmBill` locks the draft, the receipt lines and the balances, then calls `payables.PostBill`, which takes the settings, the supplier and the number as it does today.

**The test that holds the shared level.** `internal/inventory/lockorder_test.go` (real PostgreSQL, run with the package's tests and by CI) has two parts. (a) *Static*: with `go/parser` it reads every package and fails if a use case that locks `db.Groups` can reach, in its call tree, a lock on one of the stock tables (`StockDocuments`, `Warehouses`, `StockBalances`), or the other way round; the use cases are the exported methods of the services of `groups`, `reservations`, `frontdesk` and `inventory`. (b) *Dynamic*: `dbtest` gets a recorder of the lock levels and tables a transaction takes (`dbtest.LockTrace`, active only in tests); every use case of section 5.2 and every group use case runs once under it, and the test fails if one trace holds both a group row and a stock row, and on any descent in level (the same check `db.EnterLockLevel` makes at run time). A new use case that mixes them fails CI and has to move to its own level (the renumbering of D12) before it merges.

**The ledger writer** (`internal/inventory/ledger.go`) is the only code that inserts `inv_movements` and changes `inv_balances`, `inv_layers`, `inv_layer_uses` and `inv_shortfalls`; the structural guard `stockpath_guard_test.go` reads the source with `go/parser` and fails any other writer, as `salepath_guard_test.go` does for the sale paths. It exposes `In`, `Out`, `Reverse`, `Adjust` and `Settle`, takes the method from the settings, and refuses to run outside a transaction that holds the balance rows (it checks `db` state).

### 5.2 The use cases

| Use case | Permission | Locks (after the business day in share mode) | Writes |
|---|---|---|---|
| Create, edit, submit a PR | `inventory.request` | PR row | `inv_requests` |
| Approve or reject a PR | `inventory.approve` | PR row | status, decision, audit; the approval is verified **before** the transaction |
| Create a PO (from a PR or direct) | `inventory.purchase` | PR row, PO row | `inv_purchase_orders`; the PR becomes CONVERTED |
| Send, cancel, close short a PO | `inventory.purchase` | PO row | status |
| **Post a goods receipt** | `inventory.receive` | PO row (if any) -> warehouse (share) -> balances -> sequence | receipt, lines, movements, **bill draft**, PO quantities, audit |
| Reverse a receipt | `inventory.receive` | receipt row -> PO row -> warehouse -> balances | reversing movements; only on the day of the receipt, unbilled (5.3.5) |
| Return to supplier | `inventory.receive` | receipt row -> warehouse -> balances -> sequence | return, `SUPPLIER_RETURN` movements |
| **Confirm a bill from drafts** | `inventory.bill` and `payables.post` (+ approval for a variance) | drafts, receipt lines, PO lines, balances, then (inside `PostBill`) settings, supplier, sequence | the bill in `payables`, `inv_bill_matches`, clearing movements |
| Unmatch a bill | `inventory.bill` and `payables.post` + approval `PAYABLES` | the same | reverses the clearing, voids the bill, reopens the draft |
| Requisition | `inventory.request` | requisition row | rows |
| Transfer (answers a requisition or free) | `inventory.move` | requisition -> transfer -> both warehouses (share) -> balances of both, one call -> sequence | `TRANSFER_OUT` and `TRANSFER_IN` |
| Issue to a department / waste | `inventory.move` | warehouse (share) -> balances -> sequence | `ISSUE` / `WASTE` |
| Start a count | `inventory.count` | count row -> **warehouse `FOR UPDATE`** -> balances (to snapshot) | the freeze, the snapshot |
| Enter, submit a count | `inventory.count` | count row | lines |
| **Approve a count** | `inventory.approve` | count row -> warehouse `FOR UPDATE` -> balances | `COUNT_GAIN` / `COUNT_LOSS`, ends the freeze |
| Opening stock | `inventory.opening` | warehouse -> balances -> sequence | `OPENING` movements |
| Apply the POS queue (worker, night audit) | system | warehouse (share) -> balances | `POS_SALE`, `POS_SALE_RETURN`, `COGS_TO_WASTE` |
| Edit a recipe | `pos.manage` | none (plain data, like the product master) | `pos_recipe_lines` |

### 5.3 Purchasing

**5.3.1 The request (PR) and its approval (I4).** A person with `inventory.request` writes a PR: items, quantities in the purchase unit, an estimated price (the supplier item's last price, editable), the department and the date needed. `est_total` is the sum. **Submit** moves it to SUBMITTED. The **approval** is a decision of the approver in their own session (`POST .../approve`, kind `INV_PR`, amount `est_total`, `Subject` = the requester, F6): under `FREE` anyone who holds `inventory.approve`, the requester included, may approve; under `DISTINCT` it must be someone else; under `DISTINCT_ABOVE 5,000,000` the requester may approve a PR up to 5,000,000 and a different person above it. Reject needs a reason. An APPROVED PR can be turned into one or more POs (one per supplier) and then is CONVERTED; the line quantities of the POs may not exceed it. With `po_requires_pr = false` a PO can be written without a PR (D13).

**5.3.2 The order (PO).** Lines in the purchase unit with a price per purchase unit, net of VAT. `ORDERED` means the supplier has the order; the PO changes only by cancelling it, closing it short, or a new PO. A PO has no journal (a commitment is not booked).

**5.3.3 The receipt (GR).** A receipt names a supplier and a warehouse, usually a PO. Per line: item, purchase unit, quantity, and the **price taken from the PO line** (the receipt does not offer a price to change; a different price is the supplier's invoice and is handled at the bill). In one transaction: PO row `FOR UPDATE` -> warehouse (share; a frozen warehouse refuses with `WAREHOUSE_FROZEN`) -> balances ascending; one `RECEIPT` movement per line, settling shortfalls first (4.4), `qty_received_stock` of each PO line increased, the PO set to PARTIAL or RECEIVED, **the bill draft made** (one per receipt, lines at the receipt value), the audit entry. Quantity: `received <= ordered x (1 + over_receipt_pct)` per PO line over all receipts, else 409 `RECEIPT_EXCEEDS_ORDER`. A receipt **without a PO** (the market purchase, D8) needs `inventory.receive` plus the property's `direct_receipt_limit` (NULL = not allowed, 409 `DIRECT_RECEIPT_NOT_ALLOWED`; above the limit, `DIRECT_RECEIPT_LIMIT`); the price is typed on the line. A convenience action **Receive and bill** posts the receipt and confirms its draft in one request when the person also holds `inventory.bill` and `payables.post`; the payment stays a payables payment (cash is `payment_method CASH`).

**5.3.4 The bill: three-way match.** The supplier's invoice arrives. In **Inventory > Bill drafts** the clerk picks the drafts of one supplier, enters the supplier's invoice number, the bill date and VAT per line, and for each line the invoice quantity and price (the draft proposes the receipt's). The server compares, for each line, three sources:

| Compared | Rule |
|---|---|
| invoice quantity against **the receipt** | may not exceed the received and not yet billed quantity by more than `qty_tolerance_pct`; else 409 `BILL_EXCEEDS_RECEIPT` (record the delivery that is missing, or ask the supplier to correct the invoice). A smaller quantity bills part of the line; the rest stays on the draft. |
| invoice price against **the PO price** | the difference per line is the **price variance**. The sum of `abs(variance)` over the request is the amount of the approval: up to `price_tolerance_pct` of the receipt value of the lines, none is asked; above it, an approval of kind **`INV_BILL_VARIANCE`** (credentials of a holder of `inventory.approve`, rule of document 32) is required. |
| the bill as a whole against **PostBill** | the rules of `payables` unchanged (supplier active, month open, VAT treatment of the date, department rule of the accounts). |

`ConfirmBill` then calls `payables.PostBill` with one line per matched receipt line, **on GRNI** (`INV_GRNI`), `amount` = invoice amount, `vat_amount` as typed. The bill's journal is made at once, as always (Dr GRNI, Dr input VAT / Cr accounts payable). Extra lines of the invoice that are not stock (delivery, a discount) may be added on any other account; **they are not capitalised into the stock** (D14).

**The effect on the cost of the stock.** For each matched line: `cost basis = invoice amount`, plus the VAT of the line **only when the VAT treatment of the date is EXPENSE** (then the VAT is part of what the purchase cost, document 14); `variance = cost basis - receipt value of the billed quantity`. The variance is cleared out of GRNI by `COST_ADJUST` movements dated the day of the confirmation (so the day close journals them):

- the part that belongs to **stock still on hand** goes to inventory: `inventory part = variance x min(qty billed, qty on hand in the receiving warehouse) / qty billed`; under **average** the balance value changes (the average moves), under **FIFO** the layer of that receipt line is revalued (and a part of the receipt that moved to another warehouse is not followed: D2);
- the rest, `variance - inventory part`, goes to **PPV (5299)**: the goods are already sold, issued or moved.

Journal of the clearing: variance positive: Dr inventory (the category account) and Dr PPV / Cr GRNI; negative: the reverse. After it, GRNI of the line is exactly zero. Worked in 8.1.

**Quantity and price differences between the three documents, in one table:**

| Case | Receipt | PO | Invoice | Result |
|---|---|---|---|---|
| exact | 60 botol @ 50,000 | same | same | no variance |
| price higher by 2% | 3,000,000 | 3,000,000 | 3,060,000 | variance +60,000, approval above 1%, inventory/PPV split |
| price lower | 3,000,000 | 3,000,000 | 2,940,000 | variance -60,000 (a credit to inventory/PPV), same split, no approval below tolerance |
| invoice for more than received | 4 karton | 5 karton | 5 karton | the 5th is refused (`BILL_EXCEEDS_RECEIPT`); bill 4, the PO stays PARTIAL |
| invoice for less than received | 5 karton | 5 karton | 4 karton | bill 4; one karton stays on the draft and in GRNI, shown in "received, not invoiced" |
| received without a PO | 3 karton @ 90,000 typed | none | 3 karton @ 90,000 | price variance is zero against its own price; compared only to the receipt |

**5.3.5 Reversing a receipt.** A mistake in a receipt is reversed by `RECEIPT_REVERSAL` movements (`reverses_id`), allowed only **on the business day of the receipt, while it is unbilled and the quantity is still on hand** (else `RECEIPT_NOT_REVERSIBLE`: use a return to supplier). The PO quantities and the draft are restored; the day-close lines cancel (Dr/Cr swapped).

**5.3.6 Returns and credit notes.** A return (4.6) is booked Dr GRNI / Cr inventory on the return day. If the receipt line has been billed, the clerk enters the supplier credit note in `payables` (document 14: it credits the bill line, whose account is GRNI, so Cr GRNI) and **links** it to the return in Inventory; the link is an `inv_bill_matches` row with a negative quantity, and a difference between the credit note and the cost of the returned goods is cleared exactly like a bill variance. If the receipt is still unbilled, the return just lowers the draft (the quantity billable is `received - returned`). Worked in 8.7.

**5.3.7 Unmatching.** `Unmatch` (F13) reverses the clearing movements (reversing rows, dated today), voids the bill through the payables void (with its approval; the bill must have no payment), and reopens the draft lines. Nothing is edited.

### 5.4 Movements between warehouses and out of stock

- **Requisition (I2).** An outlet's person writes a requisition (items, quantities in the recipe or stock unit, date needed) to the main store. The store answers with **one or more transfers** (`requisition_id`), each posting immediately; the requisition becomes PARTIAL or FULFILLED by the issued quantities; the store may reject with a reason. A transfer can also be made without a requisition (`inventory.move`).
- **Transfer.** One transaction, both warehouses (share), the balances of both in one call (ascending id, so two transfers in opposite directions cannot deadlock), the source must hold the quantity (I7), one pair of movements per line, no journal (4.5). Single step: the goods are in the destination when the transfer posts (D9).
- **Issue to a department** ("pemakaian departemen", I1). Housekeeping amenities, engineering parts, stationery leave a warehouse for the expense of a department. The department is required; each line's expense account defaults to `inv_categories.issue_account_id` and may be changed on the line; the department rule of the account is applied when the line is posted (`Poster.ResolveDepartment`, the way `PostBill` does), so a REQUIRED department that cannot be found is refused at the issue and never stops the day close.
- **Waste.** An issue with `purpose = WASTE`, a reason code and the account of the category's waste (or `INV_WASTE`), the department of the warehouse's outlet or department.
- **Correcting** a transfer, an issue or a waste: a reversing document on the same day; later, a new document the other way. Never an edit.

### 5.5 The count and the freeze (I9)

1. **Start** (`inventory.count`): the count row and the warehouse are locked `FOR UPDATE` (so in-flight mutations finish first); `inv_warehouses.count_id` is set; `book_qty` is copied for every item in scope (a warehouse, a category or a list). From now on every mutation of that warehouse except the POS queue refuses with `WAREHOUSE_FROZEN` (409). Only one count per warehouse at a time (a partial unique index on `count_id`).
2. **Enter**: counters type `counted_qty` in the stock or a counting unit, on a phone or the desk; the sheet is blind by default (`count_blind`). Each time a quantity is entered or changed the **server** stamps `counted_at` on that line (never the client's clock). Items not counted are not changed.
3. **Submit** locks the sheet; **approve** (`inventory.approve`, decision of the approver, kind `INV_COUNT`, amount `abs(total variance value)`, `Subject` = the person who submitted) in one transaction (count row -> warehouse `FOR UPDATE` -> balances) first applies, per item, the held sales created before that item's `counted_at`, then posts the variances (4.7) against `expected`, ends the freeze, applies the rest of the held rows and writes the audit. **Cancel** ends the freeze and posts nothing.
4. **What happens to POS sales during the count.** A sale is never blocked (I7). Its queue row is applied to a frozen warehouse only after the freeze ends: the worker marks it `HELD` and skips it. Held rows are applied in order, dated the business day when they are applied; `source_date` keeps the day of the sale. **Which side of the count a sale is on** is decided per item by the instant the item was counted (4.7): a sale before `counted_at` is already missing from the shelf, so it is applied before the adjustment and lowers what the count expects; a sale after it is applied after the adjustment. The sale is therefore deducted once, whatever the order in which things happen. On a **cancel** all held rows are simply applied. A sale made *while the counter is walking to the shelf* is the only residual error (the counter may have seen the goods before the plate left); it is as small as the time between looking and typing, and the screen suggests counting when the outlet is quiet. Worked in 8.6.
5. **A count that runs over the day close.** By default it is a **blocker** of the night audit (`count_blocks_night_audit`, D6): the close would otherwise have to leave the held rows for tomorrow and date their cost a day late. A property that chooses the warning accepts that: the held rows are applied on the first business day after the freeze ends (their `created_at` still decides their side of each `counted_at`), and the report shows the sale date.

### 5.6 The deduction of a POS sale (I6)

**What triggers it.** A POS line becomes a sale when it is posted to the ledger: on **send** (modes `PRINT`, `KDS`) or on **payment** (mode `DIRECT`), exactly the moments of `34-pos.md` 4.2. In the same transaction as `PostCharge`, and only if the plan has POS **and** Inventory (I12) and the outlet has a warehouse, the POS code inserts **one row in `inv_consumption_queue`** for the line.

**What the row contains.** The recipe is expanded then: the product's lines with `option_id IS NULL` plus the lines of the options the line carries; **a variant's line for an item replaces the product's line for that same item** (a "large" variant says 0.3 kg of rice instead of 0.2), **a modifier's lines add**; each quantity is converted from the recipe unit to the stock unit and multiplied by the line quantity. The result is the `components` snapshot, so a later change of the recipe never alters a sale already queued. A product with no recipe lines queues nothing.

**Same transaction or the outbox: the choice (D4).**

| | Deduct inside the POS transaction | **Queue row, worker applies (chosen)** |
|---|---|---|
| Cashier's wait | the send holds the folio `FOR UPDATE` (42) and also the balance rows (45) of every ingredient: all sends that use the same rice, oil or ice cube take turns | one `INSERT` of a few hundred bytes; no stock lock in the POS transaction |
| Lock order | stock (45) after folios (42): valid, but the bill locks are held longer and a stock lock timeout (5 s) would fail a send | none: the POS transaction takes no lock it did not take before |
| I7 (a sale is never blocked) | a lock wait or a failure of the stock code can fail the sale | the sale cannot fail because of stock |
| Consistency | stock exact at commit | stock exact within seconds; **always before the day closes** (the night audit drains the queue) |
| Dated | the day of the sale | the day it is applied; equal except when a count holds it over midnight |
| Cost | simple | one table, one worker, one failure state |

**The worker.** A process loop (and the night audit) takes up to 50 rows `PENDING` ordered by id with `FOR UPDATE SKIP LOCKED`, groups them by warehouse, and in one transaction per group: business day (share) -> warehouse (share) -> the balances of all the items, ascending, in one call -> `POS_SALE` movements (one per component, valued by the method, shortfall rules of 4.4, the cost-of-sales account of the item's category, the department of the **outlet**, so cost and revenue of "Restoran" meet in the departmental report) -> rows `APPLIED`. A warehouse that is frozen leaves its rows `HELD`. A row that cannot be applied (the warehouse was switched off, an account missing) becomes `FAILED` with a code; failed rows are shown in the setup check and **block the night audit** (`INVENTORY_QUEUE_FAILED`) until retried or cancelled by someone with `inventory.manage`. Two workers or two instances never take the same row (`SKIP LOCKED`); the unique key `(pos_bill_line_id, kind)` makes the hook idempotent, so a replayed request queues nothing twice.

**Voids (I6).** Voiding a SENT line in POS adds a queue row, by the state of the line's kitchen ticket at that moment (`34-pos.md` 19.2):

| State | Queue row | Effect on stock and books |
|---|---|---|
| the sale row is still `PENDING` or `HELD` | the sale row is set `CANCELLED` | nothing was ever deducted; no movement |
| ticket `NEW` (not started), or the outlet is `DIRECT` (nothing is cooked) | `VOID_RETURN` | `POS_SALE_RETURN`: the quantities go back at the cost they were taken (layers restored under FIFO); journal Dr inventory / Cr cost of sales |
| ticket `COOKING`, `READY` or `SERVED` | `VOID_WASTE` | the food exists and is thrown away: the stock is **not** returned; `COGS_TO_WASTE` moves the cost: Dr waste (5290) / Cr cost of sales; no change to the balance |

A void after the day of the sale works the same: the return or the waste is dated today (the sale's day is closed and stays). D5 states the definition of "cooked" and asks if a kitchen that does not use the display (`PRINT` mode, ticket always `NEW`) should treat every void as waste after N minutes.

**Discounts, comps and split/merge do not matter to stock**: a comped meal is still eaten; its cost of sales stays in 5210/5220 (and the comp expense of document 34 7.5 is separate). A bill split or merged inside an outlet keeps the lines' sale rows; a line moved to a bill of **another** outlet is not allowed by POS (document 34 5.3), so the warehouse never changes.

**Cost in the product master.** `GET /pos/products/{id}/recipe` returns the lines and the **recipe cost** at the outlet's warehouse costs (balance average or last unit cost, then `reference_cost`), the net price and the margin (I10). Read-only; it never writes.

**When the module is off.** If Inventory is not in the plan (or POS is not), the hook inserts nothing and the recipe tab is hidden; sales between the two states are listed by the report "POS lines with a recipe and no stock deduction" (derived: POS-source folio items of products with recipe lines and no queue row), so switching the module on later shows the gap and does not hide it.

### 5.7 Night audit and the close

The close gets these steps, in this order, inside the audit's transaction (day `FOR UPDATE` first): after the room charges, **drain the queue** (5.6; every `PENDING` row applied; `FAILED` rows block), then `accounting.PostDay` reads the stock ledger of the day (section 6). Checks added to document 34 13.1's list:

| Check | Kind |
|---|---|
| consumption queue rows `FAILED` or not drainable | blocker `INVENTORY_QUEUE_FAILED` |
| an open count (OPEN or SUBMITTED) | blocker `INVENTORY_COUNT_OPEN`, or a warning if `count_blocks_night_audit` is off |
| balances below zero (shortfalls open) | warning (count of items, link to the report) |
| the ledger and the balances disagree (the drift check, 7.5) | blocker `INVENTORY_BALANCE_DRIFT` |
| the stock value and the GL control accounts disagree (section 6.3) | blocker, like `TRANSFER_ACCOUNT_NOT_ZERO` |
| receipts older than 30 days with no bill | warning |

## 6. Accounting (I8)

### 6.1 Accounts

| Where | Account | Default |
|---|---|---|
| Inventory of a category | `inv_categories.inventory_account_id` | 1310 food, 1320 beverage, 1330 operating supplies, 1340 other |
| Cost of sales of a POS sale | `cogs_account_id` | 5210 cost of food sales, 5220 cost of beverage sales; a category with none cannot be sold by POS (the setup check says so) |
| Issue to a department | `issue_account_id`, or the account on the line | 5150 guest supplies and 5140 cleaning supplies (Rooms), 6470 maintenance supplies, 6160 office supplies |
| Waste | `waste_account_id` or key `INV_WASTE` | 5290 |
| Count variance | `variance_account_id` or key `INV_COUNT_VARIANCE` | 5295 |
| Goods received not invoiced | key `INV_GRNI` | 2130 |
| Purchase price variance | key `INV_PPV` | 5299 |
| Opening stock | the existing 3900 "Opening balance equity" | |

The accounts of a category are chosen in Setup with the rules of the chart (an ASSET for inventory, an EXPENSE for costs, postable, active); the department rule of document 12 applies to the cost accounts.

### 6.2 The journal at the day close

Every movement of the day with `gl_amount > 0` becomes part of the day-close journal. The movement carries its accounts, so the lines are a grouping of rows by (debit account, credit account, kind, department); the source types and their labels:

| Movement | Debit | Credit | Source type |
|---|---|---|---|
| `OPENING` | inventory | 3900 | `INV_OPENING` |
| `RECEIPT` | inventory | GRNI | `INV_RECEIPT` |
| `RECEIPT_REVERSAL` | GRNI | inventory | `INV_RECEIPT` |
| `SUPPLIER_RETURN` | GRNI | inventory | `INV_RETURN` |
| `COST_ADJUST` (variance up) | inventory (on hand part) and PPV (rest) | GRNI | `INV_COST_ADJUST` |
| `POS_SALE` | cost of sales of the category, department of the outlet | inventory | `INV_SALE_COST` |
| `POS_SALE_RETURN` | inventory | cost of sales | `INV_SALE_COST` |
| `COGS_TO_WASTE` | waste | cost of sales | `INV_WASTE` |
| `NEG_SETTLE` | cost of sales (original sale's) | inventory | `INV_SALE_COST` |
| `ISSUE` | expense of the line, department of the issue | inventory | `INV_ISSUE` |
| `WASTE` | waste | inventory | `INV_WASTE` |
| `COUNT_LOSS` / `COUNT_GAIN` | count variance / inventory | inventory / count variance | `INV_COUNT` |
| `TRANSFER_OUT` / `TRANSFER_IN` | none | none | none |

Each stock row is self-balancing, so the "journal does not balance" check of `postDay` still holds. The **department** of a cost line is the outlet's (sales), the issue's (use), the warehouse's (waste, count), snapshotted; the rule of the account (`lineDepartment`) was applied when the movement was posted, so the close never stops for a missing department. The lines are labelled like the others (`Cost of sales, Restoran`, `Goods received`, `Stock count CNT-000004`), and the description of a line carries the document number when one document makes the line.

### 6.3 Reconciliation of the GL with the stock value

`accounting.ControlSources` (document 31 5) learns two controls, computed in SQL from the **stock tables, not from the journals**, so the check is independent:

| Control | GL account | Stock source as of D |
|---|---|---|
| Inventory by category | each `inventory_account_id` | `sum(value_delta)` of `inv_movements` up to D, grouped by the category of the item (a transfer nets to zero over the property); for today it equals `sum(inv_balances.value)` |
| GRNI | 2130 | receipts - returns - bill lines on GRNI + credit-note lines on GRNI + the clearing movements (signed), each from its own table (receipt lines, return lines, `supplier_bill_lines` and credit-note lines on the GRNI account, `COST_ADJUST` rows), all dated up to D |

The readiness check lists any category whose GL balance differs from the stock value (`INVENTORY_GL_MISMATCH`), with both figures; the balance **report** by category shows both columns side by side (I10). Because a manual journal on these accounts is refused once the module has a movement (F2), the usual cause of a difference is a posting dated before the module started; the message says so.

### 6.4 Control accounts and the one way in

From the first movement, the accounts 13xx of the categories and GRNI are **control accounts**: `Poster.CheckAccount` refuses a payables bill line, a manual journal line and a credit-note line on them with `CONTROL_ACCOUNT` unless the call comes from the inventory path (`ConfirmBill`, the credit-note link). Before the first movement they are ordinary accounts (a hotel that books a purchase by hand today keeps working).

### 6.5 Timing, stated once

- Receipt, issue, sale and count lines are journaled **at the close of the business day they happened on** (I8, like `DAY_CLOSE`).
- The supplier bill is journaled **when it is confirmed**, on the bill date, as every bill is (`PostBill`). So on the day of a receipt that is billed the same day, GRNI carries the bill's debit during the day and the receipt's credit after the close; by the end of that day's close they meet.
- The clearing of a variance is a `COST_ADJUST` movement dated the day the bill is confirmed, journaled at that day's close.

## 7. Cross-cutting rules

### 7.1 Business date and the trigger (a required point)

Hotel dates come only from the open business day. Every use case that writes a movement first calls `RequireOpenBusinessDay(ctx, propertyID, db.ForShare, ...)`, the application gate. Behind it, `inv_movements` has the trigger `business_day_must_be_open` on `business_date`: **yes, a stock movement is a "posting row"** in the sense of CLAUDE.md, because it carries money dated by the business day and is journaled by the day close; a movement dated a closed day would be missed by the journal for ever (F4). The receipt, the count, the request and the other headers are **operational records** (like POS bills): the money they cause is in the movements. The date of a receipt is the open day; the date on the supplier's delivery note is a separate column. The queue stores `sale_date`, and a movement made later keeps it as `source_date`.

### 7.2 Append-only and correction by reversal

`inv_movements`, `inv_layer_uses`, `inv_bill_matches` (except `unmatched_at`) and the audit are append-only (triggers refuse UPDATE, DELETE and TRUNCATE). A correction is a reversing row: `reverses_id` (unique, a movement is reversed once) with the quantity and value negated and the accounts swapped. Working tables (`inv_balances`, `inv_layers`, `inv_shortfalls`, the queue) are changed only by the ledger writer and are rebuilt from the ledger by the drift check. A document is `POSTED` or `REVERSED`, never edited.

### 7.3 Idempotency

As in POS (document 34 12): every document has a **client-made UUID** (`client_uuid`, unique per property: creating again returns the first), and every posting request carries `Idempotency-Key`; the document stores its key (unique per property) and a replay returns the stored result and does not ask for an approval again. Movements are unique by `(source_type, source_id, source_line, kind)`, so a retried transaction cannot post a line twice. The queue is unique by `(pos_bill_line_id, kind)`.

### 7.4 Subscription (I12)

Routes `{P}/inventory/*` belong to module `INVENTORY` in the table of `26-subscriptions.md` 3 (`TestEveryRouteHasAModule` makes the classification compulsory). The recipe routes of POS add the check of F10. The services stay ungated underneath, like check-out: a tenant that switches the module off keeps its data. The new permissions are **granted to no existing role** by the migration (the lesson of 00076, D11 of document 34); the tenant administrator holds everything; `pms-seed inventory` may create demo roles in a development database only.

### 7.5 Performance: a balance table, not a sum

The balance of an item is read on every POS screen that shows a recipe cost, every transfer, every count and every report; the ledger of a busy bar grows by thousands of rows a day. So `inv_balances` is **maintained**: the writer updates it under the row lock in the same transaction as the movement, which is cheap (one row) and gives the lock the use cases need. The cost of this is the risk of a bug making it differ from the ledger, so: (a) the writer is the single code path and is guarded structurally; (b) `CHECK` constraints on the sign and on `qty = 0 -> value = 0`; (c) a **drift check** (`GET /inventory/setup-check`, run by the night audit for the warehouses touched that day, and in full by a test) recomputes `sum(qty_delta)`, `sum(value_delta)` and, under FIFO, the layers, and compares; (d) an admin action **Rebuild balances** (audited, `inventory.manage`) recomputes from the ledger. Reports "as of a date" read the ledger from the **day snapshot** `inv_balance_days (warehouse_id, item_id, business_date, qty, value)`, written at the close only for the pairs that moved that day (PR 5), so a stock card or a valuation of last month does not scan the whole history. Indexes: `(warehouse_id, item_id, business_date, id)` on the ledger, `(property_id, business_date)` for the close.

### 7.6 Audit

Every state change writes `audit.Writer.Write` in the same transaction: `inventory.item_created`, `inventory.costing_method_set` (old, new; the lock is audited by the first close, as `accounting.deposit_release_locked` is), `inventory.pr_submitted`, `_approved`, `_rejected`, `inventory.po_created`, `_sent`, `_cancelled`, `_closed`, `inventory.receipt_posted`, `_reversed`, `inventory.bill_confirmed` (the matches, the variance, the approver), `inventory.bill_unmatched`, `inventory.return_posted`, `inventory.transfer_posted`, `inventory.issue_posted`, `inventory.waste_posted`, `inventory.count_started`, `_submitted`, `_approved` (the total variance, the approver), `_cancelled`, `inventory.opening_posted`, `inventory.queue_retried`, `inventory.balances_rebuilt`, `pos.recipe_changed` (old and new lines). Approvals add `AuditFields()`. Audit labels (`auditlabel`) get the document numbers. A movement carries `created_by`; a POS worker's movements carry the actor `system` and the sale's bill number in `source_type/source_id`.

### 7.7 Language

Screens, errors and labels are keys of `src/i18n` in **English and Indonesian** (parity test). Seed data in a development database is in Indonesian (`24-seed-data-language.md`). Units and item names are the property's data.

### 7.8 Demo data: `pms-seed inventory`

`go run ./cmd/pms-seed inventory -tenant DEMO -property BALI [-dry-run]`, through the services, idempotent by code, for a **development** database: the setup of the module (average method, tolerances), the units (ml, g, kg, l, botol, karton, pcs, pack, karung), four categories with their accounts, four warehouses (Gudang Utama, Gudang Dapur Restoran, Gudang Bar, Gudang Housekeeping) tied to the outlets of `pms-seed pos`, about 40 items in Indonesian with conversions (sirup 650 ml, beras karung 25 kg, shampoo 30 ml ...), two suppliers (one "Pasar" for market purchases), opening stock, **recipes for the demo products of `pms-seed pos`** (nasi goreng, es teh, kopi ...), one approved PR, a PO, a receipt with its draft, a confirmed bill with a small variance, a requisition and a transfer, an issue to housekeeping, a count in progress. `-dry-run` shows it. It never touches a production database (the same guard as the other seeds).

## 8. Flows with numbers

Conventions: IDR (0 decimals); PPN 11% (creditable input VAT, treatment CREDITABLE) unless said; accounts 1310 food inventory, 1320 beverage inventory, 1330 operating supplies, 1425 input VAT, 2110 accounts payable, 2130 GRNI, 3900 opening balance equity, 5140/5150 housekeeping supplies, 5210/5220 cost of food/beverage sales, 5290 waste, 5295 count variance, 5299 PPV. Warehouses: MAIN (Gudang Utama), RESTO, BAR, HK. Each flow starts from the state it names; the flows are independent. "Journal" is what the day close writes for the stock ledger; the supplier bill is journaled when it is confirmed.

### 8.1 Purchase: PR, PO, receipt, bill with a price variance (average and FIFO)

Item SIRUP (1320): stock unit botol, purchase unit karton = 12 botol, recipe unit ml (1 ml = 1/650 botol). Start: MAIN holds 20 botol, value 960,000 (average 48,000). `price_tolerance_pct` 1%.

| Step | What happens |
|---|---|
| PR-000031 | 5 karton, estimated 600,000 each = 3,000,000. Kind `INV_PR`, amount 3,000,000; rule `FREE`: the manager approves it in their session |
| PO-000007 | 5 karton @ 600,000 (per karton, net) = 3,000,000, 60 botol at 50,000 each |
| 16 Oct GR-000012 | 60 botol, value 3,000,000. **Average:** MAIN 80 botol, 3,960,000 (49,500). **FIFO:** layers L1 20 @ 48,000, L2 60 @ 50,000. The draft bill is made: 3,000,000 |
| 16 Oct close | Dr 1320 3,000,000 / Cr 2130 3,000,000 (`INV_RECEIPT`) |
| 19 Oct transfer | 30 botol MAIN -> BAR. **Average:** 30 x 49,500 = 1,485,000 (MAIN 50 / 2,475,000; BAR 30 / 1,485,000). **FIFO:** 20 from L1 (960,000) + 10 from L2 (500,000) = 1,460,000 (MAIN: L2 50 / 2,500,000; BAR layers 20 @ 48,000 and 10 @ 50,000). No journal |
| 19 Oct invoice | 5 karton @ 612,000 = 3,060,000, PPN 336,600, total 3,396,600. Variance +60,000 = 2.0% of the receipt value, above 1%: approval `INV_BILL_VARIANCE`, amount 60,000 |
| 19 Oct bill (immediate) | Dr 2130 3,060,000, Dr 1425 336,600 / Cr 2110 3,396,600 |
| clearing | on hand in the receiving warehouse: `min(60 billed, 50 on hand)` = 50. Inventory part = 60,000 x 50 / 60 = **50,000**; PPV = **10,000**. **Average:** MAIN 50 / 2,525,000 (50,500). **FIFO:** L2 value 2,550,000 (51,000 per botol) |
| 19 Oct close | Dr 1320 50,000, Dr 5299 10,000 / Cr 2130 60,000 (`INV_COST_ADJUST`) |

GRNI: Cr 3,000,000 (receipt) - Dr 3,060,000 (bill) + Cr 60,000 (clearing) = **0**. Reconciliation: 1320 = 960,000 + 3,000,000 + 50,000 = **4,010,000**; stock: **average** 2,525,000 + 1,485,000 = 4,010,000; **FIFO** 2,550,000 + 1,460,000 = 4,010,000. The two methods end with the same GL and different values by warehouse. A price below the PO price gives the same entries with the sign changed. **Quantity:** had the invoice been for 5 karton while only 4 were received, the bill would be refused for the fifth (`BILL_EXCEEDS_RECEIPT`).

### 8.2 A POS sale with a recipe, a void before cooking and a void after

Product "Es Sirup Vanila", recipe SIRUP 65 ml = 0.1 botol per glass. Outlet BAR sells from warehouse BAR. Start: **average** BAR 30 botol / 1,485,000 (49,500); **FIFO** layers 20 @ 48,000 and 10 @ 50,000 (1,460,000).

20 glasses are sent: 2.0 botol. The queue row is inserted with the posting; the worker applies it a moment later.

| | Average | FIFO |
|---|---|---|
| `POS_SALE` | 2.0 x 49,500 = **99,000** | 2.0 from L1 = **96,000** |
| 2 glasses voided, ticket `NEW` (`VOID_RETURN`, 0.2 botol) | back at the cost taken: **9,900** | back into L1 at 48,000: **9,600** |
| 2 glasses voided, ticket `READY` (`VOID_WASTE`) | `COGS_TO_WASTE` **9,900**, stock not returned | **9,600** |

Journal (average): Dr 5220 / Bar 99,000 / Cr 1320 99,000; Dr 1320 9,900 / Cr 5220 9,900; Dr 5290 9,900 / Cr 5220 9,900. Net: 5220 **79,200** (16 glasses = 1.6 botol), 5290 **9,900** (2 glasses), 1320 **-89,100** (1.8 botol left the shelf). BAR: 28.2 botol / **1,395,900**. The revenue of 16 sold glasses is in the POS day journal (document 34), so the departmental report of "Bar" shows revenue and its cost together.

### 8.3 The market purchase: receipt without a PO, "Receive and bill", paid in cash

The property allows direct receipts up to 1,000,000 (`direct_receipt_limit`). Supplier PASAR (a supplier of the property for market purchases, not a PKP: no VAT). The storekeeper receives **2 karung of BERAS (25 kg each) at 375,000 per karung** into MAIN and, holding `inventory.bill` and `payables.post`, uses **Receive and bill**.

- Receipt GR-000021: 50 kg, value **750,000** (15,000 per kg), typed price; below the limit. MAIN gains 50 kg / 750,000. The draft is made and confirmed in the same request with the invoice number "NOTA 0412": one bill line on GRNI of 750,000; receipt and invoice agree, so there is no variance and no approval.
- Bill (immediate): Dr 2130 750,000 / Cr 2110 750,000. The payment is the existing payables payment, method `CASH`: Dr 2110 750,000 / Cr cash (or petty cash, document 11).
- Close: Dr 1310 750,000 / Cr 2130 750,000. GRNI: Cr 750,000 - Dr 750,000 = **0**. A receipt of 1,200,000 would have been refused (`DIRECT_RECEIPT_LIMIT`): it needs a PO.

### 8.4 Stock sold below zero, and the correction when goods come in (average and FIFO)

Item BERAS (1310): stock kg. RESTO holds 2.0 kg / 28,000 (14,000 per kg). Fifteen "nasi goreng" are sold, 0.2 kg each = **3.0 kg**.

- `POS_SALE`: 2.0 kg at 14,000 = 28,000 plus 1.0 kg uncovered at the estimate `e = 14,000` (last unit cost) = 14,000. Movement: qty -3.0, value **-42,000**. Balance **-1.0 kg / -14,000**. A shortfall row: 1.0 kg, 14,000. Journal: Dr 5210 / Restoran 42,000 / Cr 1310 42,000. The report "sold below zero" lists RESTO / BERAS / 1.0 kg.
- Next day the requisition is answered: a transfer of 10 kg MAIN -> RESTO at MAIN's cost 15,500 (value 155,000). Transfer out and in: no journal. The in settles the shortfall: covered 1.0 kg, `c = 15,500`, `e = 14,000`: `NEG_SETTLE` value **-1,500**, journaled Dr 5210 / Restoran 1,500 / Cr 1310 1,500. RESTO: plain sum 9.0 kg / 141,000, minus 1,500 = **9.0 kg / 139,500** (15,500 each). Under **FIFO** the in creates one layer of **9.0 kg @ 15,500 = 139,500** (the covered kilo makes none): the same figures.
- The cost of those fifteen plates is 43,500 (28,000 + 15,500 for the kilo that was not there), which is the real cost. A transfer or an issue that would have taken RESTO below zero is refused with `NEGATIVE_STOCK_NOT_ALLOWED`.

### 8.5 Requisition, transfer and an issue to a department

Requisition REQ-000008: RESTO asks MAIN for 10 kg of BERAS; TRF-000015 answers it (8.4): MAIN 40 kg / 620,000 -> 30 kg / 465,000; no journal. Issue ISS-000004: housekeeping takes 200 pcs of SHAMPOO 30 ml (1330) from HK, average 3,500 = **700,000**; the line's expense is 5150 guest supplies with the department Rooms: Dr 5150 700,000 / Cr 1330 700,000, `INV_ISSUE`. Had the account required a department and none been named, the issue would be refused (`DEPARTMENT_REQUIRED`) when it is posted.

### 8.6 A count with a freeze, while the bar sells

BAR, SIRUP, average, book at the end of 8.2: **28.2 botol / 1,395,900** (49,500 each). 21:00 the count starts: warehouse frozen, `book_qty` = 28.2.

**A sale before the item is counted.** 21:05 ten glasses are sent (1.0 botol): the queue row is `HELD`, created 21:05. 21:20 the counter looks at the shelf and enters **27.5 botol**; the server stamps `counted_at` 21:20. The shelf already lacks the 1.0 botol sold at 21:05, so `held_before_qty` = -1.0, `expected` = 28.2 - 1.0 = 27.2 and **variance = 27.5 - 27.2 = +0.3 botol**. 21:30 the manager approves (kind `INV_COUNT`, amount 0.3 x 49,500 = 14,850; the rule `DISTINCT_ABOVE 10,000` makes it need someone other than the counter). In one transaction: first the held sale is applied, `POS_SALE` 1.0 botol x 49,500 = 49,500 (balance 27.2 / 1,346,400); then `COUNT_GAIN` +0.3 at the last unit cost, **14,850** (balance **27.5 / 1,361,250**); the freeze ends. The book ends where the shelf is: **27.5**, not 26.5. Journal: Dr 5220 / Bar 49,500 / Cr 1320 49,500; Dr 1320 14,850 / Cr 5295 14,850 (`INV_COUNT`).

**The same sale after the item is counted.** Had the glasses been sent at 21:25, after `counted_at` 21:20, `held_before_qty` is 0, `expected` = 28.2, **variance = 27.5 - 28.2 = -0.7** x 49,500 = **-34,650** (`COUNT_LOSS`, Dr 5295 34,650 / Cr 1320 34,650), balance 27.5 / 1,361,250; then the held sale is applied after: 49,500, balance **26.5 / 1,311,750**, which is the shelf after the glasses were made. Both orders give the right book; a rule that applied every held sale after the count would be right only for the second.

**FIFO** (BAR layers 18.2 botol @ 48,000 and 10 @ 50,000 after 8.2, value 1,373,600): the sale before the count takes 1.0 from the oldest layer (48,000), then the gain makes a layer of 0.3 at the last unit cost (50,000, value 15,000). **Below zero:** had BAR held only 0.5 botol when the sale arrived, applying it first would take the balance to -0.5 (a shortfall at the estimate); the count (found 0.0) has `expected` -0.5, variance +0.5, closes the shortfall without a settlement and leaves 0.0.

### 8.7 Return to supplier

GR-000020: 5 karton of SIRUP (60 botol, 3,000,000 at 50,000), unbilled. Two karton (24 botol) arrive damaged. RET-000003 at the receipt line's cost: 24 x 50,000 = **1,200,000**: Dr 2130 1,200,000 / Cr 1320 1,200,000; the draft falls to 36 botol / 1,800,000. **If the receipt had been billed** (3,000,000 + PPN 330,000): the supplier's credit note SCN (document 14) of 1,200,000 + PPN 132,000 = 1,332,000: Dr 2110 1,332,000 / Cr 2130 1,200,000 / Cr 1425 132,000; GRNI = Cr 3,000,000 - Dr 3,000,000 - Dr 1,200,000 + Cr 1,200,000 = **0**. A credit note of 1,260,000 for the same goods would leave 60,000 in GRNI, cleared to inventory/PPV by the link, as in 8.1.

### 8.8 The month: reconciliation and food-and-beverage cost %

Food and beverage, September (all figures in IDR):

| | Amount |
|---|---:|
| Opening stock (count of 31 Aug) | 30,000,000 |
| + Received (GRNI side, net of returns) | 60,000,000 |
| - Closing stock (count of 30 Sep) | 28,000,000 |
| **Actual cost** | **62,000,000** |
| F&B net revenue (document 34 reports, outlet departments) | 190,000,000 |
| **Actual cost %** | **32.6%** |
| Theoretical cost (recipes x quantities sold, `INV_SALE_COST` net of returns) | 58,000,000 |
| **Theoretical cost %** | **30.5%** |
| Difference | 4,000,000 = waste 1,500,000 + count variances 1,800,000 + PPV 700,000 |

The three accounts 5290, 5295, 5299 in the departmental report explain the difference to the rupiah; the report states it. Reconciliation at 30 Sep: the sum of `inv_balances.value` of the food and beverage categories equals the GL balance of 1310 + 1320, and GRNI (2130) equals the "received and not invoiced" list.

## 9. Permissions and approvals

### 9.1 Permissions (new; granted to **no role** by the migration; the tenant administrator holds all)

| Permission | Who | Allows |
|---|---|---|
| `inventory.view` | any | read stock, documents, reports |
| `inventory.manage` | store manager | units, categories (with accounts), items, warehouses, levels, settings, retry or cancel failed queue rows, rebuild balances |
| `inventory.request` | any department | PR and requisition |
| `inventory.purchase` | purchasing | PO |
| `inventory.receive` | storekeeper | receipt, return to supplier, direct receipt |
| `inventory.bill` | accountant | confirm and unmatch bill drafts (also needs `payables.post`) |
| `inventory.move` | storekeeper, outlet | transfer, issue to a department, waste |
| `inventory.count` | storekeeper, outlet | start, enter, submit a count |
| `inventory.opening` | store manager | opening stock |
| `inventory.approve` | manager | the approver of `INV_PR`, `INV_BILL_VARIANCE`, `INV_COUNT` |
| `pos.manage` | (existing) | edit recipes; read items for the recipe editor |

Reads follow document 33: the list of items, units, warehouses is readable by `inventory.view`, `inventory.manage`, or the permission of a screen that uses it (`pos.manage` for the recipe editor, `inventory.request` for the request form).

### 9.2 Approval kinds (document 32 2)

| Kind (label) | Use cases | Amount | Mode | Approver's permission |
|---|---|---|---|---|
| `INV_PR` (purchase request) | `ApprovePR` | `est_total` | decision; `Subject` = requester | `inventory.approve` |
| `INV_BILL_VARIANCE` (price difference on a bill) | `ConfirmBill` | sum of `abs(variance)` of the request | credentials, as a correction | `inventory.approve` |
| `INV_COUNT` (stock count result) | `ApproveCount` | `abs(total variance value)` | decision; `Subject` = who submitted | `inventory.approve` |

All three take `FREE`, `DISTINCT` and `DISTINCT_ABOVE` per property. The structural test of document 32 4 is extended (each use case names its kind and amount and writes `AuditFields()`), and `ApprovalRequest` gains `Decision bool` and `Subject int64`: with `Decision`, the caller is the approver, needs the permission, and the rule compares the approver with `Subject`; a tenant administrator who is the subject is accepted and marked as for any correction. The approval of a decision is verified before the transaction, as in document 32. An existing kind is still used where it applies: unmatching a bill is `PAYABLES` (void of a bill).

## 10. API

`{P}` = `/api/v1/properties/{propertyId}`. Every POST that creates or posts takes a `client_uuid` or `Idempotency-Key` as 7.3 says. Mutations are in OpenAPI first; the TS types are generated. Errors are `*apperr.Error` with the stable codes below; new constraint names are mapped.

| Area | Routes |
|---|---|
| Start and settings | `GET/PUT {P}/inventory/settings` (the method is accepted only before the lock: 409 `COSTING_METHOD_LOCKED`); `GET {P}/inventory/setup-check` |
| Masters | `GET/POST/PATCH {P}/inventory/units`, `categories`, `items`, `warehouses`; `PUT items/{id}/units`; `PUT warehouses/{id}/levels`; `POST items/import` (CSV, all or nothing, rows listed); `GET items?q=&category=&warehouse=` |
| Opening | `POST {P}/inventory/openings` (`INVENTORY_GL_NOT_EMPTY`, `OPENING_EXISTS`) |
| Purchasing | `GET/POST requests`, `POST requests/{id}/submit|approve|reject|cancel`; `GET/POST purchase-orders` (from a request or direct), `POST purchase-orders/{id}/send|cancel|close`; `GET/POST receipts`, `POST receipts/{id}/reverse`; `GET bill-drafts`, `POST bill-drafts/confirm`, `POST bill-drafts/{id}/discard`, `POST bills/{id}/unmatch`; `GET/POST returns`, `POST returns/{id}/link-credit-note` |
| Movements | `GET/POST requisitions` (+ `submit|reject|cancel`), `GET/POST transfers`, `GET/POST issues` (`purpose USE|WASTE`), `POST transfers|issues/{id}/reverse` |
| Counts | `GET/POST counts` (start), `PUT counts/{id}/lines`, `POST counts/{id}/submit|approve|cancel` |
| Stock and reports | `GET stock?warehouse=&item=&category=`, `GET stock-card?warehouse&item&from&to`, `GET reports/valuation?warehouse&as_of`, `GET reports/low-stock`, `GET reports/negative-stock`, `GET reports/received-not-invoiced`, `GET reports/fb-cost?from&to&outlet`, `GET reports/waste`, `GET reports/count-variance`, `GET reports/pos-without-deduction` (CSV where tables) |
| POS | `GET/PUT {P}/pos/products/{id}/recipe` (`pos.manage`; PUT replaces the lines; 422 `RECIPE_UNIT_INVALID`), `GET {P}/pos/products/{id}/recipe-cost?outlet=`; `PATCH pos/outlets/{id}` gains `warehouse_id` |
| Queue | `GET {P}/inventory/consumption-queue?status=`, `POST .../{id}/retry|cancel` (`inventory.manage`) |
| Admin | `POST {P}/inventory/rebuild-balances` (`inventory.manage`) |

Error codes: 409 `NEGATIVE_STOCK_NOT_ALLOWED`, `WAREHOUSE_FROZEN`, `COUNT_IN_PROGRESS`, `COSTING_METHOD_LOCKED`, `RECEIPT_EXCEEDS_ORDER`, `RECEIPT_NOT_REVERSIBLE`, `BILL_EXCEEDS_RECEIPT`, `BILL_HAS_STOCK_MATCH`, `PO_NOT_ORDERABLE`, `PR_NOT_APPROVED`, `DIRECT_RECEIPT_NOT_ALLOWED`, `DIRECT_RECEIPT_LIMIT`, `INVENTORY_GL_NOT_EMPTY`, `WAREHOUSE_HAS_STOCK`, `ITEM_HAS_STOCK`, `INVENTORY_QUEUE_FAILED`, `INVENTORY_COUNT_OPEN`; 422 `RECIPE_UNIT_INVALID`, `UNIT_CONVERSION_MISSING`, `CATEGORY_ACCOUNT_INVALID`, `DEPARTMENT_REQUIRED` (existing); 403 `MODULE_NOT_IN_PLAN`, `PERMISSION_DENIED`, `APPROVAL_*` (existing); 404 for another tenant's or an unassigned property (existing).

## 11. Screens (en and id; desk 1280-1440, receiving and counting at 390 px)

| Screen | For | Content |
|---|---|---|
| Inventory dashboard | `inventory.view` | items below minimum, PRs waiting for approval, POs not received, received and not invoiced (aged), counts open, shortfalls, failed queue rows |
| Items and categories | `inventory.manage` | list with search and filters, item form with the units table (purchase, recipe, conversions shown as "1 karton = 12 botol"), category accounts with the chart check, CSV import |
| Warehouses and levels | `inventory.manage` | warehouses, their outlet or department, minimum levels |
| Requests and orders | `inventory.request`, `inventory.purchase` | PR form with the supplier's last price; the approval inbox for `inventory.approve` with the rule line ("Above 5,000,000 the approver must be someone other than the requester"); PO from PR; receive against a PO |
| Receiving | `inventory.receive` | receipt against a PO (quantity per line, the over-receipt warning), direct receipt, a phone layout for the dock |
| Bill drafts | `inventory.bill` | drafts by supplier, the supplier's invoice entry with quantity, price and VAT per line, the three columns **receipt / PO / invoice** with the variance coloured and its split (inventory / PPV) before confirming, the approval dialog |
| Returns | `inventory.receive` | return from a receipt line, the credit-note link |
| Requisition, transfer, issue, waste | `inventory.request`, `inventory.move` | the outlet's requisition and the store's picking list that becomes a transfer; issue to a department with the account and department shown; waste with reason |
| Stock count | `inventory.count`, `inventory.approve` | start with scope, the sheet (blind by default, big touch targets, a counting unit), submit, the approval screen with the variance and its value, the freeze banner on the warehouse everywhere |
| Stock and cards | `inventory.view` | stock by warehouse and item, the stock card (running quantity, value, average or layers), valuation, by category against the GL |
| POS product master | `pos.manage` | the **recipe** tab becomes editable (items, quantity in the recipe unit, variants and options), recipe cost, price, margin; hidden unless both modules are in the plan |
| Setup | `inventory.manage` | settings (method with the warning that it locks, tolerances, limits), the setup check, the failed queue |
| Reports | `inventory.view` | section 12 |
| Night audit | existing | the new findings (5.7) |

## 12. Reports (I10)

All have CSV; the tables with a period also take the outlet or department.

- **Stock card (kartu stok)**: per item and warehouse, each movement with date, document, quantity in and out, running quantity, value and unit cost (average) or the layers touched (FIFO); opening and closing balance from the day snapshot.
- **Valuation per warehouse** as of a date (qty, value, average), totals by category, **against the GL account** of the category (6.3).
- **Food and beverage cost %**: theoretical (recipes x sold, from `INV_SALE_COST`), actual (opening + received - closing, closing from an approved count; without a count in the period the report says "estimated from the book stock"), the difference split into waste, count variances, PPV and shortfalls, as % of F&B net revenue by outlet department (8.8).
- **Recipe cost** per product in the POS master (5.6) and a list of products with cost, price and margin, lowest margin first.
- **Low stock**: items with `qty < min_qty` per warehouse, with the reorder quantity; also the dashboard; a PR can be started from it.
- Others: waste by reason, count variances, stock sold below zero, received and not invoiced (the GRNI detail), POS lines with a recipe and no deduction, price variances by supplier.

## 13. Tests (real PostgreSQL; races with two goroutines and a barrier, as `05` 4)

**PR 1 (masters, ledger, balances)**
- Schema (`db/tests/schema_test.sql`): sign checks on `inv_balances`, `qty = 0 -> value = 0`, the ledger is append-only (UPDATE, DELETE, TRUNCATE refused), `business_day_must_be_open` on `inv_movements`, one opening per item and warehouse, the reversal unique, a category account must be an inventory asset, unit conversions positive, one active warehouse per outlet; Up/Down/Up and Down refused with data; the new accounts and keys, with a taken code (replacement rule).
- **Costing**: for each of AVERAGE and FIFO, a table of cases with exact figures: receipts then issues, an OUT that empties the balance takes the whole value (no residue), rounding at 0, 2 and 3 decimals, the unit conversions (karton, botol, ml) exact in money, transfers with value carried and layers copied with their dates, the layer invariant `qty = sum(layers) - shortfalls`.
- **Negative stock**: a sale below zero (qty, value, shortfall, flag), a second sale, the settlement at higher and lower cost (8.4) for both methods, a count that closes the shortfall, transfer / issue / waste / count refusing, the estimate chain (last cost, reference cost, newest receipt, zero flagged).
- The method lock (the trigger sets the date on the first movement; the property row is not written after it, by `xmin`; the setting is refused after; two first movements at once; one audit entry by the first close).
- **The ledger writer is the only writer** (structural test, as `salepath_guard_test.go`); the drift check and rebuild reproduce the balances and the layers.
- Day close: each movement kind produces the journal of 6.2 exactly; the journal balances; a day posts once; the department rule; `PostPending` unaffected.
- Reconciliation: GL 13xx and GRNI against the stock sources at every close in 8.1, 8.2, 8.4, 8.6, 8.7 (`ControlSources`).
- Web: items, units table, categories with accounts, opening stock, i18n parity.

**PR 2 (purchasing)**
- **Races**: two receipts of one PO at the same time (the quantity never passes the over-receipt tolerance; exactly the allowed one succeeds); a receipt against a reversal; two bill confirmations of the same draft (one succeeds, no line billed twice); a receipt and a transfer on the same balance (no deadlock, order held); a confirmation and a payables payment of the same supplier (lock order 45 -> 46 -> 47 -> 50).
- The PR approval under `FREE`, `DISTINCT`, `DISTINCT_ABOVE` at, below and above the limit, the administrator exception, the structural test of document 32 with the three new kinds; the decision mode with a `Subject`.
- Three-way: exact; price up and down; quantity over and under; no PO; the variance within and above tolerance (approval asked or not); the inventory/PPV split with the stock on hand, partly moved and fully sold; VAT treatments CREDITABLE, EXPENSE (cost basis includes the VAT), DEFERRED; GRNI is zero after each case, for both methods.
- Control accounts: a hand-entered bill line and a manual journal on 13xx / GRNI refused after the first movement and accepted before; `INVENTORY_GL_NOT_EMPTY`.
- Receipt reversal (same day, unbilled, on hand) and its refusals; return to supplier before and after the bill; the credit-note link; `Unmatch` and `BILL_HAS_STOCK_MATCH` on a plain void.
- Idempotent replay of every posting; no second approval on a replay; the draft made once.
- Web: the receipt, the three-column bill screen, the approval inbox.

**PR 3 (movements, requisition, count)**
- Transfer with value carried (both methods), in opposite directions at once (no deadlock), the source refusal; requisition filled by two transfers; issue to a department with the department rule (`DEPARTMENT_REQUIRED` at the issue, never at the close); waste with reasons; reversals the same day.
- **Count and freeze**: start freezes (`WAREHOUSE_FROZEN` for every manual mutation, the queue held), a mutation in flight finishes before the freeze, **a sale before and a sale after an item is counted (a race with a barrier: the sale and the entry of the count at the same time; the side of `counted_at` decides, the sale is deducted exactly once, and the book equals the counted quantity minus only the sales after `counted_at`)**, `counted_at` set by the server and moved when the quantity is changed, a queue row with components on two items split by their own `counted_at`, the held sale that takes the balance below zero before the count (shortfall closed by the count), both methods, two counts of one warehouse refused, blind sheet, gain and loss valuation for both methods, a count closes shortfalls, approval under the rules (`INV_COUNT`), cancel releases the freeze and applies the held rows, held rows applied in order, the open-count blocker and the warning setting of the night audit.
- Web: the count sheet at 390 px, the requisition and transfer screens.

**PR 4 (POS recipes)**
- The hook: a send and a DIRECT pay each queue one row per line with the recipe snapshot; variant replaces the same item, modifier adds, a line with no recipe queues nothing; the plan gate (POS only, Inventory only, both); a replayed request queues nothing twice; the structural guard that every POS posting path writes the hook.
- The worker: `SKIP LOCKED` with two workers (no row twice), a frozen warehouse leaves `HELD`, a failed row blocks the audit and retry works, **the sale is never refused for lack of stock** and **never waits for a stock lock** (a test holds a balance lock and sends: the send commits), the audit drains before the journal, a sale across midnight with a held row keeps `source_date`.
- Voids: before the worker (cancelled row), ticket `NEW` (return, layers restored), `READY` (waste, no balance change), DIRECT (return), on a later day.
- The numbers of 8.2 and 8.6 for both methods; cost of sales by the outlet's department in the departmental report.
- Web: the recipe tab, the recipe cost, hidden without both modules.

**PR 5 (reports)**
- The reports of section 12 against hand-computed data (8.8), the day snapshot against the ledger, CSV, the permission of each report.

**Always**: tenant isolation of every table and route, permissions (403) and the read-only subscription state, `TestEveryRouteHasAModule`, the demo seed is idempotent and passes the setup check, `scripts/db-test.sh` for each migration.

## 14. Phases (PRs), migrations

Migration numbers are the next free: **00084 to 00088**; if another PR takes one first they move (as documents 31 and 34 did). Each PR is DB, backend, API (OpenAPI first), frontend, i18n and tests together, on its own branch from `main`; `gen:api`, `sqlc`, lint and the web checks as CLAUDE.md lists. The module is usable from PR 1 for the owner who only wants stock.

| PR | Migration | Contents |
|---|---|---|
| **1. Masters, ledger and opening stock** | 00084 | settings and the method lock, units, categories with accounts, items and units, warehouses, levels, the ledger with its triggers, balances, layers, shortfalls, the ledger writer for both methods, opening stock, the new accounts and keys, `LevelStock` in `db/locks.go`, the control accounts, the stock source of the day close and of `ControlSources`, the readiness checks, the permissions, the module classification, `pms-seed inventory` (masters and opening) |
| **2. Purchasing** | 00085 | requests, orders, receipts, bill drafts, matches, returns, `ConfirmBill` with the three-way rules, the clearing of variances, `VoidGuard`, the approval kinds and the `Decision` mode of `iam.Verify`, direct receipt, the screens of purchasing |
| **3. Movements, requisition and count** | 00086 | requisition, transfer, issue, waste, count with the freeze, reversals, the open-count check, the screens |
| **4. Recipes and the POS deduction** | 00087 | `pos_outlets.warehouse_id`, the foreign keys of `pos_recipe_lines`, the recipe editor and the recipe cost, the queue and the worker, the hook in the POS paths, voids, the night-audit drain, the gap report, the POS master tab |
| **5. Reports and snapshots** | 00088 | `inv_balance_days` written at the close, the stock card, valuation, F&B cost %, low stock and the other reports, dashboard, CSV |

## 15. Decisions (D1 to D22 decided 2026-10-16: as recommended; D23 open)

| # | Question | Recommendation |
|---|---|---|
| D1 | Average (and FIFO layers) **per warehouse** or per property? | **Per warehouse.** The cost lives where the goods are, the row lock is local, the value by warehouse is exact, and FIFO layers are per warehouse by nature. Per property would need one lock per item for the whole hotel and would make a transfer a non-event whose cost then jumps. |
| D2 | A bill price variance: **revalue what is on hand and send the rest to PPV**, or send all of it to PPV? | **Revalue the on-hand part** (4.2, 5.3.4), the rest to PPV. It keeps stock at actual cost. A part of the receipt that moved to another warehouse is not followed (it goes to PPV); the simple alternative (all to PPV) is easy to switch to. |
| D3 | Tolerances. | Price **1%**, over-receipt **5%**, bill quantity above receipt **0%**; per property. |
| D4 | POS deduction in the POS transaction, or a **queue**? | **Queue** (5.6): no lock in the cashier's transaction, a sale never fails for stock, stock is right within seconds and always before the close. |
| D5 | Void "before cooking returns, after cooking is waste": by the ticket status (`NEW` = return; `COOKING`, `READY`, `SERVED` = waste), `DIRECT` always return? A kitchen on `PRINT` mode never moves the ticket. | **By the status of the ticket**, `DIRECT` = return. For `PRINT` outlets whose ticket stays `NEW`, add an outlet setting "void after N minutes counts as waste" (default off, 0). |
| D6 | A count still open at the close. | **Blocker by default**, a warning by setting (5.5). |
| D7 | PR and count approval: a **decision by the approver in their own session** (a small `Decision` and `Subject` on `iam.Verify`), or the approver typing a password next to the requester? | **The decision mode.** A manager approves from their phone; the rules of document 32 apply unchanged. The password mode would put the manager next to every requester. |
| D8 | Receipt without a PO (market purchase). | **Allowed** with `inventory.receive`, a property **limit** (`direct_receipt_limit`, default not allowed), the typed price compared only with the receipt, and the one-click **Receive and bill**. |
| D9 | Transfer: **one step** (in the destination when posted) or two (in transit)? | **One step.** Both warehouses are in one building under one control; a two-step transfer can be added later without changing the ledger (a transit warehouse). |
| D10 | The four new accounts and their codes: 2130, 5290, 5295, 5299. | As proposed, with the replacement rule of 00071; every one is a row of Account mapping the owner may repoint. |
| D11 | A property that already has a balance on 13xx before the module starts. | **Refuse** the first opening until the accountant clears it (`INVENTORY_GL_NOT_EMPTY`); opening stock against 3900. An automatic adjusting journal would hide an accounting decision. |
| D12 | Lock level: **45 shared with the groups**, or renumber the levels above? | **Share 45** (a booking never touches stock; a test lists the sequences). Renumbering 46 to 50 touches every document that quotes them for no gain. |
| D13 | Is a PO allowed without an approved PR? | **Setting `po_requires_pr`, default on.** A small hotel may switch it off. |
| D14 | Delivery and other charges on the supplier invoice. | **Not capitalised** into stock now (they are lines of the bill on an expense account). Landed cost allocation is a later step; the match table does not prevent it. |
| D15 | A recipe variant: replaces the **whole** recipe, or only the lines for the **same item**? | **Same item** (5.6): a large size changes the rice and leaves the oil; modifiers add. |
| D16 | The receipt price. | **From the PO**, not editable; a different price is the invoice. A receipt without a PO types it. |
| D17 | Balances maintained by the application under the lock, or by a database trigger? | **The application** (the single writer, guarded), with the drift check and the rebuild (7.5). A trigger would hide the writer and double the work on a hot row. |
| D18 | Units: rational conversions and quantities to six decimals? | **Yes**; and advise the smallest practical unit as the stock unit where recipes use it. |
| D19 | A sale applied after midnight because of a count: dated when applied, with `source_date` kept. | **Yes.** The cost lands on the day it is applied (one day late at most in the rare case); the report shows both dates. |
| D20 | Which users may touch which warehouse? | **Not now** (any holder of the permission, any warehouse); an assignment table can come later without changing the ledger. |
| D21 | The count-day cutoff for F&B cost %: use only approved counts, or also estimate? | **Approved counts**, with "estimated from the book stock" when a period has none. |
| D22 | Returns to supplier at the receipt line's cost. | **Yes** (4.6); the small difference to the moving average stays in the balance. |
| D23 | **Count without a freeze** at all: every item gets its book quantity *as of its `counted_at`* from the ledger (`sum(qty_delta)` up to that instant), manual movements and POS sales go on, and the variance is `counted - book at counted_at`. | **Keep the freeze (OPEN, the owner decides).** Without it a transfer or a receipt between two lines of the sheet moves goods the counter may see twice or not at all, every movement needs an exact instant, and the balance at an instant is a query over the ledger, not a row. It is possible (the ledger is append-only and every movement has `created_at`), it removes `WAREHOUSE_FROZEN` and the held rows, and it suits a small bar that never stops; the cost is that only POS sales are safe, not manual movements. A middle way is to freeze only manual movements, which is what this design does. |

## 16. Not in this design (I11: deferred; the design does not close the way)

| Deferred | Why it stays open |
|---|---|
| **Batch and expiry** | A FIFO layer is already a lot. Adding `lot_code`, `expires_on` to a layer, a `lot_id` to the ledger (nullable) and a FEFO order (first to expire) changes the OUT order, not the tables or the journal. Under AVERAGE a lot would be a tag on the receipt line only. |
| **Auto sold-out in POS when stock is zero** | `inv_balances` of the outlet's warehouse is the data; a rule "when qty of any recipe item reaches 0, insert `pos_outlet_sold_out` for the day" can be a trigger or a step of the worker, using the table that already clears itself with the day. |
| **Sub-recipes and production** (a sauce made from ingredients) | `kind` of the ledger is a list that a migration extends: `PRODUCTION_OUT` and `PRODUCTION_IN` with the same writer; `pos_recipe_lines.inventory_item_id` may point to a produced item. |
| **Fixed assets and depreciation** | A separate module. Non-stock purchases stay on the payables bill with an expense or asset account; items that are not stocked are not items here. |
| Landed cost, supplier price lists and quotes, forecasting, barcode scanning, multi-currency purchases, user-to-warehouse assignment, a transit warehouse | Each adds tables or screens around the ledger; none needs it to change. |
| An offline stock client | Documents carry `client_uuid` and idempotency keys as POS does; the ledger needs nothing more. |
