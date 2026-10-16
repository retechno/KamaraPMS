# 35. Inventory and recipes: items, warehouses, purchasing, costing, stock counts and the deduction of POS sales

Status: **approved by the owner (2026-10-16), the whole document, including I13 to I18 (section 0.1) and D1 to D31 of section 15.** D1 to D22 and D25 to D30 are as recommended; D23 as recommended (the freeze stays for manual movements); **D24 and D31 were decided differently from the recommendation**: D24, the amenities of a close that falls in a count are held like POS rows and applied after the freeze (5.9, 4.7); D31, the daily cost covers all costs, not only F&B (12.2). The parts that come from the review of the screens are marked "(UI review)". **PR 1 is built (migration 00084): section 17 is the record of what was built and where it differs from this text; PR 2 to PR 5 are not built.** Asked by the owner on 2026-10-16 as stage 4 of `34-pos.md` section 14. The owner's decisions are in the request (numbered I1 to I12) and in the review (I13 to I18) and are **not re-opened**. Where one of them touches a rule of the platform or of an earlier document, section 1 says so, with the reason and what this design does about it. Sections 1 to 16 are the design; section 17 is the record of PR 1. Words in `code font` are tables, columns, permissions or error codes; every new name is a proposal until approved. The screens are drawn in `docs/design/17` to `34` (`docs/design/README.md`, part Inventory); section 11 gives the mockup of each screen.

Read with: `02-database-schema.md`, `03-financial-engines.md` (one posting path per ledger), `05-transactions-locking.md` (lock order), `11-cashier-budget-cashflow-card.md` (shifts: a market purchase paid in cash), `12-departments.md` (department of a line, the rule of an account), `14-supplier-credit-notes.md`, `26-subscriptions.md` (module gate), `31-deposit-release.md` (a setting that locks at the first transaction, the day-close journal), `32-distinct-approver.md` (kinds of approval), `33-setup-read-permissions.md`, `34-pos.md` (recipes: 3.2, 7, 14; the realtime channel: 10.1) and `internal/payables`, `internal/nightaudit`.

## 0. The idea in one page

- **One stock ledger.** Every change of quantity or of value is a row of `inv_movements`: append-only, dated by the open business day, written by one package (`internal/inventory`, the only writer of the ledger and of the balances, the way `FolioPostingService` is the only writer of `folio_items`). A correction is a reversing row, never an edit (section 7.2).
- **Balances are a table, not a sum.** `inv_balances` holds quantity and value per item and warehouse, changed under a row lock in the same transaction as the movement; the ledger can rebuild it and a test proves they agree (section 7.5).
- **Two costing methods, one per property** (I3): moving average or FIFO, chosen before the first stock transaction and then locked, both per warehouse (D1). Section 4 states what each does for receipts, transfers, negative stock, returns and counts.
- **Purchasing is a chain** (I4): request (PR, approved by a **chain of up to three levels**, I13) -> order (PO) -> goods receipt -> a **draft supplier bill made by the receipt** -> the supplier's invoice confirms it into a real bill in `payables`, matched against the PO and the receipt (three-way). A receipt without a PO is allowed for the daily market purchase.
- **Accounting is perpetual and written at the day close** (I8): the day-close journal gains lines read from the stock ledger, like it reads the folio ledger. Goods received are booked to inventory against a new liability **"Goods received not invoiced"** (GRNI); the supplier bill debits GRNI, so a purchase is **never booked twice**.
- **POS sales deduct stock through a queue** (I6, D4): posting a POS line inserts a row in a queue in the same transaction; the stock is deducted a moment later by a worker, and always before the day closes. The cashier never waits for a stock lock and a sale is never refused for lack of stock (I7).
- **Everything else is a document with a state**: requisition (SR, a chain of two levels) and transfer between warehouses, issue to a department, waste, staff meal, entertainment, stock count with a freeze. They never make the stock negative (I7); only a POS sale and the automatic amenities document of the close may (4.4).
- **Inventory is a module of its own** (I12): the code `INVENTORY` already reserved in `26-subscriptions.md`. Recipes work only when POS and Inventory are both in the plan.
- **An approval is a chain, with a memory** (I13, UI review): PR up to three levels, SR two, configured per property; its own small engine, not `iam.Verify` (5.8). Department heads and deputies are kept in Departments (3.3).
- **Cost has a class** (I14, UI review): the cost of a POS sale goes to food or beverage cost of sales by the class of what was **sold**, not by the category of the ingredient; the cost transfers between food and beverage that the reconciliation shows are **measured**, not posted (4.9).
- **Special uses of F&B stock** (I15, UI review): staff meal and entertainment / officer check are purposes of their own, with their accounts and department, and are left out of the net cost of sales (5.4).
- **The close posts the amenities** (I16, UI review): one automatic issue per business day for every room slept in, a shortage becomes negative stock and never stops the close (5.9).
- **Cost reports** (I17, UI review): F&B cost reconciliation, daily cost of all costs (F&B with targets, amenities, engineering, variances), cost per outlet, all from the ledger and a small table written at the close (section 12).
- **Screens** (I18, UI review): a list page that fills the screen, then a document page that fills the screen, no list beside the document; Items and stock uses a side panel (section 11).

### 0.1 The decisions of the review of the screens, 2026-10-16 (I13 to I18, not re-opened)

| # | Decision | Where |
|---|---|---|
| I13 | Approval chain: PR up to 3 sequential levels (head of the requesting department, cost control, final approval FC/GM), SR 2 levels (head, final approval); per property and per document type: name, who approves, condition ("always" or "total above X"), levels may be added or removed; one person one level per document; the requester never approves their own; optional skip of level 1 when the requester is the head; an approver may change quantity or estimated price (recorded per line, audited); a rejection at any level returns the document to the requester with a note and a new submission starts the chain again; the approver is notified; a change of the chain applies only to documents submitted afterwards (snapshot); an SR can be answered by a transfer only when its chain is done. It **replaces** the one-level approval of the PR (kind `INV_PR`, F6, D7). Head and deputy of a department are kept in Departments. | 5.8, 3.3, 9, F15 |
| I14 | Cost transfer between food and beverage (lime of the food category used by the bar, wine of the beverage category used by the kitchen). | 4.9, 6, F16, D28 |
| I15 | Two special F&B uses, **staff meal** and **entertainment / officer check**, each with its expense account and department; both leave the net cost of sales. Waste stays a kind of its own. | 5.4, 6, F18 |
| I16 | Automatic amenities per occupied room: one document per closed day, plan per room or per guest plus extras by room type, source warehouse, department and account, a shortage does not stop the close, correction by a reversing document or a manual issue, can be switched off, a change applies from the next close; a close that falls in a count holds the amenities like POS rows (D24). Built in PR 3. | 5.9, F17, D24 |
| I17 | Reports of PR 5: F&B cost reconciliation (mockup 30), daily cost of **all** costs with MTD, three views and an F&B target per outlet (mockup 31, D31). | section 12 |
| I18 | Screen pattern (list page, then document page, both full width; Items and stock with a side panel). | section 11 |

## 1. Findings and conflicts (read this first)

| # | Finding | What the design does |
|---|---|---|
| F1 | **A supplier bill has no draft state.** `supplier_bills.status` is `POSTED` or `VOIDED`; `payables.PostBill` writes the journal at once, on the bill date (migration 00031, `bills.go`). I4 asks for a draft bill made by the receipt. | The draft is **inventory's own record** (`inv_bill_drafts`), made inside the receipt's transaction. It becomes a bill only when a person confirms it with the supplier's invoice: `inventory.ConfirmBill` calls the existing `PostBill`. `payables` gets no draft status. The bill, its journal, its VAT treatment, its payments and its credit notes are exactly what they are today (section 5.3). |
| F2 | **Double booking.** A bill line debits an expense or asset account chosen by hand. If goods are booked to inventory at the receipt (perpetual, I8) **and** the bill debits 1310 again, the purchase is counted twice. | The receipt is booked **Dr inventory / Cr GRNI** (account 2130, new). A bill that comes from a draft has its lines on **GRNI**, so it debits what the receipt credited. Accounts 13xx and GRNI become **control accounts** once the module has its first movement: a hand-entered bill line or manual journal on them is refused (`CONTROL_ACCOUNT`, the code that already exists for the payables account); the one way in is the inventory path (section 6.4). |
| F3 | **The day-close journal reads only the folio ledger** (`DayActivity` over `folio_item_gl`, `internal/accounting/journals.go`). | `accounting` gets a second, small source: an interface `StockDayLines(ctx, property, date)` implemented by `internal/inventory`, whose lines are added to the same map (account, source type, reference, department) before the "journal balances" check. Each stock movement carries the debit and credit account **snapshotted when it is posted** (as a folio item carries its revenue account, document 12), so a later change of a category never rewrites a day. The lines of the stock source balance by themselves. One journal per day, as now; the day is posted once (`gl_day_posts`). |
| F4 | **The trigger `business_day_must_be_open` (migration 00065) names the tables that hold "posting rows".** A stock movement is a row dated by the business day whose journal is written at the close of that day. A movement dated a closed day would never be journaled. | `inv_movements` **is** a posting table and gets the trigger. The documents around it (requests, orders, receipt and count headers, drafts, queue rows) are operational records and do not (section 7.1). |
| F5 | **Global lock order has no place for stock.** `05-transactions-locking.md` ends: ... groups (45) -> accounting settings (46) -> suppliers (47) -> bank (48) -> tax and budget (49) -> sequences (50). `payables.PostBill` takes the accounting settings, then the supplier, then the sequence. A use case that locks stock **after** `PostBill` would break the order; one that locks stock **before** it must not be below 45. | New level **`LevelStock = 45`**, shared with the booking groups (the order allows re-entering a level; a booking never touches stock). Inside the level the order is fixed: documents (request, order, receipt, bill draft, return, requisition, transfer, issue, count), then the warehouse row, then balance rows ascending. `ConfirmBill` locks the stock rows first and calls `PostBill` second (settings 46, supplier 47, sequence 50), which is ascending. No renumbering of the existing levels (section 5.1). |
| F6 | **Approvals in document 32 are corrections with the approver's credentials, given at the moment of the act.** A purchase request is approved later, by another person in their own session. | A second, small mode of `iam.Verify`: a **decision** by the signed-in approver (`Decision: true`), where the rule compares the approver with a named `Subject` (the requester) instead of with the caller. Kinds, amounts, rules (`FREE`, `DISTINCT`, `DISTINCT_ABOVE`), the screen of the rules and the structural test are unchanged. Used by the kind `INV_COUNT` (section 9.2). It was meant for `INV_PR` too; **the review of the screens (I13) replaced the one-level approval of the PR by a chain of levels**, which has its own engine (F15, 5.8). The alternative (the approver types a password next to the requester) is D7. |
| F7 | **A POS sale must not wait for stock and must not be refused for lack of it** (I6, I7), but the POS posting runs under the folio lock (level 42), where every extra lock lengthens the cashier's wait. | The deduction is a **queue row inserted in the POS transaction** and applied by a worker, never inside the cashier's lock (section 5.6 compares the two ways and chooses). |
| F8 | **`pos_recipe_lines.inventory_item_id` has no foreign key, `unit` is free text** (`34-pos.md` 3.2, migration 00080). | PR 4 adds the foreign key and makes `unit` a unit **of that item** (composite FK to `inv_item_units`). Existing rows are empty (no endpoint writes them), so the migration needs no data step. |
| F9 | **A property may already have a balance on 1310 to 1340** (hand-entered bills and journals, the chart exists since 00028). Opening the module would add a second balance on top. | The first opening posting is refused with `INVENTORY_GL_NOT_EMPTY` (the balances in the message) until the accountant has cleared those accounts by a manual journal. Opening stock is booked against 3900 "Opening balance equity" (D11). |
| F10 | **The module gate classifies a route by one module** (document 26 3). The recipe routes belong to POS **and** Inventory. | Recipe routes are classified `POS` by the table and add a check of `INVENTORY` in the handler (403 `MODULE_NOT_IN_PLAN`, `context.module = INVENTORY`). The POS deduction hook asks the same entitlement (I12). |
| F11 | **Costing methods that change history.** A property that switches method after it has stock would have layers or averages that mean something else. | The method is a setting that **locks at the first stock movement**, by a database trigger, exactly as `deposit_release_mode` does (document 31 3): a conditional `UPDATE ... WHERE costing_locked_on IS NULL` in a trigger on `inv_movements`, a guard trigger on the setting, the audit entry written by the first day close. |
| F12 | **Night audit** has a list of checks (document 34 13.1). Stock adds a queue that must be empty and a count that may be open. | Section 5.7: the queue is drained as a step of the close (blocker if a row cannot be applied); an open count is a blocker or a warning by setting (D6). |
| F13 | **Payables void.** `payables.VoidBill` reverses the journal and knows nothing of stock. A matched bill voided there would leave the match, the variance and the draft in the wrong state. | `payables` gets one optional guard (`VoidGuard`, set by inventory): a bill with a stock match answers 409 `BILL_HAS_STOCK_MATCH`; the way is **Inventory > Bill drafts > Unmatch**, which reverses the variance, voids the bill through `payables.VoidBill` (with its approval) and reopens the draft. Credit notes of document 14 need nothing (section 5.3.6). |
| F14 | **`gl_account_map_key_ck` is a closed list of keys** (widened for `COMP` and `POS_ROUNDING`). | Migration 00084 widens it with `INV_GRNI`, `INV_PPV`, `INV_WASTE`, `INV_COUNT_VARIANCE` and (UI review) `INV_COGS_FOOD`, `INV_COGS_BEVERAGE`, `INV_STAFF_MEAL`, `INV_ENTERTAINMENT`, and creates the four accounts for every property that exists (with the replacement rule of 00071: a taken code goes to the next free one) and in `usali_chart()` for new properties (3.1). |
| F15 (UI review) | **Document 32 gives a decision of one person at one moment; I13 asks for a chain with a memory.** `iam.Verify` compares the approver with the actor of one act and holds nothing between acts; a chain needs the level that is next, who decided before, the snapshot of the chain and the edits. It also has no notion of "the head of the department of this document". | The chain is a **small engine of its own** in `internal/inventory` (tables `inv_approval_*`, section 3.3 and 5.8). It re-uses from document 32 the shape of the audit entry (`approved_by`), the check of a permission through the `Authorizer` and the idea of a structural test; it does **not** use the table of kinds or the rules `FREE` / `DISTINCT` / `DISTINCT_ABOVE` (the chain is stricter: never the requester, one person one level, held by a database constraint). `INV_PR` leaves the list of kinds (never built). `INV_COUNT` and `INV_BILL_VARIANCE` stay as they are: one act, one approver, at once. |
| F16 (UI review) | **The cost-of-sales account of a POS sale followed the category of the ingredient** (6.1: 5210 for a food category, 5220 for a beverage one), but the revenue of the sale follows what was sold. A lime in a bar drink would charge food cost against beverage revenue. | The account follows the **class of the product sold** (4.9, D28). Two keys, `INV_COGS_FOOD` and `INV_COGS_BEVERAGE`, say which account that is. Class columns are added to the ledger (`item_class`, `cost_class`, `outlet_id`), to warehouses (`cost_class`) and to POS categories (`fb_class`). |
| F17 (UI review) | **I7 says only a POS sale may make a balance negative**; the amenities of the close (I16) must not stop the night audit when the shelf is short. | The rule is widened by one sentence (4.4): a POS sale **and the automatic amenities issue of the close**, both written by system code that passes `AllowShortfall` to the ledger writer; the structural guard lists these two callers. A manual issue, waste, transfer and count still refuse. |
| F18 (UI review) | **`STAFF_MEAL` was a reason of a waste issue** (3.4), which books it to the waste account and leaves it inside the cost of sales. I15 asks for a separate use with its own account and department, outside the net cost. | `inv_issues.purpose` becomes `USE`, `WASTE`, `STAFF_MEAL`, `ENTERTAINMENT`; `STAFF_MEAL` leaves the list of waste reasons; the movement kinds `STAFF_MEAL` and `ENTERTAINMENT` are added (3.2). |
| F19 (UI review) | **The product has no notification of a user.** The only queue is `email_outbox`, whose rows name a reservation (migration 00021); the only live channel is the POS stream (document 34, 10.1), by outlet, station and front desk. | The approver is told by a **table of notices read by the app** (`inv_notices`: the bell, the badge, the inbox "Perlu saya setujui"), refreshed on focus and every minute, and by one event on the POS stream where it runs. No e-mail now (D27). |
| F20 (UI review) | **A department has no head.** Document 12 gives a department a code, a name, a parent and a sort order. | Two nullable columns on `departments`: `head_user_id`, `deputy_user_id` (3.3), their rules and the effect on document 12 (3.3). |

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
| SR | Store requisition (permintaan barang ke gudang): the document by which an outlet or department asks a warehouse for goods; a transfer answers it. Numbered `SR-000008`. Called "requisition" elsewhere in this text. |
| Approval chain (UI review) | The ordered levels a PR or SR must pass, configured per property; its copy in the document is the **snapshot** (5.8). |
| Level / step | A level is a line of the configured chain; a step is its copy in one round of one document, with its state and its decision. |
| Round (cycle) | One run of the chain over a document; a return to the requester and a new submission start round 2 from level 1. |
| Department head / deputy | The people kept in Departments who approve the first level for their department. |
| Item class, cost class (UI review) | `item_class`: FOOD, BEVERAGE or OTHER by the category of the item. `cost_class`: the class the cost of a movement is charged to (4.9). |
| Sale class (UI review) | The class of the product sold: food or beverage; it decides the cost-of-sales account and the side of the revenue in the cost reports. |
| Cost transfer (UI review) | The cost of items of one class consumed in the other (lime sold in a drink): **measured** for the reconciliation, not posted. |
| Staff meal, entertainment (UI review) | Purposes of an issue: food given to the staff; food or drink given to a guest or an event as a courtesy (officer check). |
| Amenities run (UI review) | The automatic issue the close makes for one business day (5.9). |


## 3. Data model

All tables carry `tenant_id` and `property_id` with **composite foreign keys** (no cross-tenant reference possible), the standard audit columns, a `version` where an edit screen needs optimistic concurrency, `numeric(18,3)` for money, `numeric(18,6)` for quantities and unit costs, `date` for dates and `timestamptz` (UTC) for instants. Quantities are always stored in the **stock unit**. Constraint names are mapped in `internal/platform/db/errors.go`. Migration numbers start at **00084** (section 14).

### 3.1 Changes to existing tables (spread over the migrations of section 14)

| Table | Change |
|---|---|
| `gl_account_map` | key check widened: `INV_GRNI`, `INV_PPV`, `INV_WASTE`, `INV_COUNT_VARIANCE`, and (UI review) `INV_COGS_FOOD` (default 5210), `INV_COGS_BEVERAGE` (5220), `INV_STAFF_MEAL` (5240 "Employee benefits", food and beverage), `INV_ENTERTAINMENT` (6170 "Travel and entertainment") (00084). The last four point at accounts that already exist (00028); the data step only fills the map, with the replacement rule of 00071 where an owner has repointed. |
| accounts, `usali_chart()` | new accounts, for new properties from the function and for existing ones by a data step with the replacement rule of 00071: **2130 "Goods received not invoiced"** (liability, group PAYABLES, department NONE), **5290 "F&B stock waste and spoilage"**, **5295 "Stock count variance"** and **5299 "Purchase price variance"** (expenses under F&B department expenses, department OPTIONAL with the default department "Food and beverage"). The 13xx accounts and the cost accounts 5210/5220 (food and beverage sales), 5140/5150 (housekeeping), 6160 (office supplies), 6470 (maintenance supplies) already exist and are the defaults of section 6.1. |
| `business_day_must_be_open` | trigger on `inv_movements (business_date)` (00084). |
| `pos_outlets` | `warehouse_id bigint NULL`, composite FK to `inv_warehouses`: the warehouse an outlet sells from (00087). NULL = this outlet deducts nothing. |
| `pos_categories` (UI review) | `fb_class varchar(10) NULL CHECK IN ('FOOD','BEVERAGE','OTHER')` (00087): the sale class of the products of the category; NULL = the sub-category takes its parent's, a top category takes the class of the outlet's warehouse (4.9). |
| `departments` (UI review) | `head_user_id`, `deputy_user_id` (00085): see 3.3, "Head and deputy". |
| `pos_recipe_lines` | FK `(property_id, inventory_item_id)` to `inv_items`; `unit` becomes a unit of the item: composite FK `(property_id, inventory_item_id, unit)` to `inv_item_units`; `CHECK (inventory_item_id IS NOT NULL)` once the FK exists; unique `(product_id, COALESCE(option_id, 0), inventory_item_id)` (00087). |
| `payables` | none in the tables. One optional Go guard `VoidGuard` on `VoidBill` (F13). |
| `document_sequences` types | `INV_PR`, `INV_PO`, `INV_GR`, `INV_RET`, `INV_SR`, `INV_TRF`, `INV_ISS`, `INV_CNT` (gapless per property, numbers like `PO-000007`, `SR-000008`). The amenities document needs no sequence: its number is `AMN-` and the business date (`AMN-20260903`; mockup 33 shows the short form `AMN-0903`, which would repeat every year). |
| permissions, approval kinds | section 9. Granted to **no existing role** (I12). The kind `INV_PR` is not created (I13). |

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
                        cogs_account_id NULL,            -- cost of sales of a POS sale **whose sale class is OTHER** (4.9); food and beverage sales use the keys INV_COGS_FOOD / INV_COGS_BEVERAGE
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
                        cost_class CHECK IN (FOOD, BEVERAGE, OTHER) DEFAULT 'OTHER',   -- (UI review) 4.9: the class an outlet's sales default to; the main store is OTHER
                        count_id NULL → inv_counts,     -- set while a count freezes the warehouse (5.5)
                        is_active, version)
                        CHECK (kind = 'OUTLET') = (outlet_id IS NOT NULL); at most one active warehouse per outlet
inv_levels             (warehouse_id, item_id, min_qty, max_qty NULL, reorder_qty NULL)   PK (warehouse_id, item_id)       -- I10: low-stock alert
inv_supplier_items     (supplier_id, item_id, purchase_unit_id, last_price numeric(18,3), supplier_sku)                   -- default for a PO line

inv_movements          -- THE LEDGER. Append-only (trigger: no UPDATE, no DELETE, no TRUNCATE); trigger business_day_must_be_open(business_date)
  id, tenant_id, property_id
  warehouse_id, item_id
  kind          CHECK IN (OPENING, RECEIPT, RECEIPT_REVERSAL, SUPPLIER_RETURN, TRANSFER_OUT, TRANSFER_IN, ISSUE, WASTE, STAFF_MEAL, ENTERTAINMENT,
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
  item_class    CHECK IN (FOOD, BEVERAGE, OTHER)   -- (UI review) the kind of the item's category when posted, snapshotted
  cost_class    CHECK IN (FOOD, BEVERAGE, OTHER)   -- the class the cost is charged to (4.9): the sale class for a POS movement, the item class for every other kind
  outlet_id     NULL → pos_outlets                 -- the outlet of the sale, else the outlet of the warehouse; NULL for the main store
  created_at, created_by
  UK (property_id, source_type, source_id, source_line, kind)           -- a document line is posted once
  CHECK (qty_delta <> 0 OR kind IN (COST_ADJUST, NEG_SETTLE, COGS_TO_WASTE))
  IDX (property_id, business_date, kind, cost_class, outlet_id)              -- the cost reports (section 12)
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
inv_shortfalls         -- I7: a POS sale (or the amenities of the close, I16) that found less than it needed
  id, warehouse_id, item_id, movement_id, qty_open > 0, qty_original, est_unit_cost numeric(18,6),
  gl_debit_account_id (the cost-of-sales account of the sale, or the expense account of the amenities), gl_department_id,
  source_type (POS_SALE, AMENITY), cost_class, outlet_id,       -- (UI review) copied from the movement, so a settlement keeps the class and the outlet
  status CHECK IN (OPEN, SETTLED, CLOSED_BY_COUNT), business_date
inv_openings + inv_opening_lines   (header and lines of the opening stock: warehouse, item, qty, unit cost; one opening per item and warehouse)
```

The triggers: `inv_movements_append_only`, `business_day_must_be_open('business_date')`, and `inv_movements_lock_costing` (F11: the conditional `UPDATE inventory_settings SET costing_locked_on = NEW.business_date WHERE property_id = NEW.property_id AND costing_locked_on IS NULL`; a locked property is neither locked nor written by a movement, which a test holds by the `xmin` of the row, as in document 31 6). `inv_balances` is not append-only; its writer is the ledger package.

### 3.3 PR 2 (migration 00085): purchasing

```
inv_requests           (id, request_number, client_uuid UK, requester_id,
                        department_id NULL,      -- required to submit (CHECK status = DRAFT OR department_id IS NOT NULL): the head of this department approves level 1
                        warehouse_id NULL (deliver to), needed_on,
                        status CHECK IN (DRAFT, SUBMITTED, APPROVED, RETURNED, CONVERTED, CANCELLED),   -- (UI review) RETURNED replaces REJECTED: a return to the requester, not an end
                        est_total numeric(18,3),
                        cycle smallint DEFAULT 1, submitted_at NULL, returned_note NULL,                -- the round of approval (5.8); the decisions are in inv_approval_steps
                        version)
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

**The approval chain (UI review, I13; the engine is built here, SR joins it in PR 3).**

```
inv_approval_levels    (property_id, doc_type CHECK IN (PR, SR), level_no smallint CHECK 1..5, name varchar(60),
                        approver_kind CHECK IN (DEPT_HEAD, PERMISSION),
                        permission varchar(30) NULL CHECK IN (inventory.cost_control, inventory.final_approve, inventory.sr_final_approve),
                        condition_kind CHECK IN (ALWAYS, TOTAL_ABOVE), threshold numeric(18,3) NULL,
                        updated_at, updated_by)                        PK (property_id, doc_type, level_no)
                        CHECK ((approver_kind = 'PERMISSION') = (permission IS NOT NULL)), CHECK ((condition_kind = 'TOTAL_ABOVE') = (threshold IS NOT NULL))
inv_approval_settings  (property_id, doc_type, skip_level1_if_head boolean DEFAULT true, approver_may_edit boolean DEFAULT true, version)   PK (property_id, doc_type)
inv_approval_steps     -- the SNAPSHOT of the chain for one round of one document, and the decisions
  id, property_id,
  request_id NULL → inv_requests,  requisition_id NULL → inv_requisitions (the column and its FK are added by 00086),  CHECK exactly one of the two
  cycle smallint, level_no, name, approver_kind, permission, condition_kind, threshold, edit_allowed boolean       -- copied from the levels and the settings at the submission
  status        CHECK IN (WAITING, ACTIVE, APPROVED, RETURNED, SKIPPED, NOT_REACHED)
  skip_reason   CHECK IN (CONDITION, REQUESTER_IS_HEAD) NULL,   activated_at NULL
  decided_by NULL, decided_at NULL, total_seen numeric(18,3) NULL (the total the approver decided on, after their edits), note
  UK (request_id, cycle, level_no), UK (requisition_id, cycle, level_no)
  UK (request_id, cycle, decided_by)     WHERE status = 'APPROVED'   -- one person, one level, per document and round: held by the DATABASE
  UK (requisition_id, cycle, decided_by) WHERE status = 'APPROVED'
inv_approval_edits     (id, step_id, line_no, field CHECK IN (QTY, EST_UNIT_PRICE), old_value numeric(18,6), new_value numeric(18,6), edited_by, edited_at)   -- append-only
inv_notices            (id, property_id, user_id, kind CHECK IN (APPROVAL_WAITING, APPROVED, RETURNED, CHAIN_STUCK), step_id NULL,
                        request_id NULL, requisition_id NULL, created_at, seen_at NULL)         -- the bell and the badge (5.8)
```

**Head and deputy of a department (UI review, I13).** Document 12 keeps a department as a master. The proposal adds to `departments` two nullable columns, `head_user_id` and `deputy_user_id`, each a composite foreign key `(tenant_id, user_id)` to `users` (`users_tenant_id_uk` exists), `CHECK (head_user_id <> deputy_user_id)` and `CHECK (deputy_user_id IS NULL OR head_user_id IS NOT NULL)`. Their effect on document 12, one by one:

- *Who sets it:* `accounting.manage`, the permission that changes the master; audit `department.head_changed` (old, new). The service refuses a user who is inactive or has no access to the property (422 `DEPARTMENT_HEAD_NOT_ASSIGNED`).
- *Sub-departments:* a sub-department with no head of its own is read through its parent (a lookup, never a copy, so a change of the parent's head reaches it). A department and its parent with no head at all make the chain unsatisfiable for a document of that department (5.8).
- *History:* decision 10 of document 12 holds: a change of head never touches a document already decided; the step stores `decided_by`. A head changed while a document waits changes **who may decide the active step**, not the snapshot.
- *Switching off:* a user who is a head cannot be deactivated without first being replaced (the user screen says so); the setup check `DEPARTMENT_HEAD_INACTIVE` lists a head or deputy who became inactive.
- *Posting and reports:* none. The head does not touch the journals, the department report or the budget; no table of document 12 other than the master changes.
- *Screen:* Setup > Departments gets two user pickers per row; the department check of document 12 shows the new findings. Nothing else in document 12 changes; its "Not in the MVP" list stands.

### 3.4 PR 3 (migration 00086): requisition, transfer, issue, waste, count

```
inv_requisitions       (id, sr_number, client_uuid UK, to_warehouse_id (the one that asks), from_warehouse_id (the main store), requester_id,
                        department_id,           -- (UI review) the department whose head approves level 1; defaults to the department of the asking warehouse or of its outlet
                        needed_on, est_total numeric(18,3),     -- quantity x the average (or last cost) of the main store, for a level with a condition on the total
                        status CHECK IN (DRAFT, SUBMITTED, RETURNED, APPROVED, PARTIAL, FULFILLED, REJECTED, CANCELLED),
                                                 -- RETURNED = sent back by the chain; REJECTED = the store cannot supply (its own decision, after APPROVED)
                        cycle smallint DEFAULT 1, submitted_at NULL, returned_note NULL, version)
inv_requisition_lines  (requisition_id, line_no, item_id, qty_requested, qty_issued DEFAULT 0, unit_id)
inv_transfers          (id, trf_number, client_uuid UK, idempotency_key UK, requisition_id NULL, from_warehouse_id, to_warehouse_id, status POSTED|REVERSED, note)
inv_transfer_lines     (transfer_id, line_no, item_id, qty_stock, value, out_movement_id, in_movement_id)
inv_issues             (id, iss_number, client_uuid UK, idempotency_key UK, warehouse_id,
                        purpose CHECK IN (USE, WASTE, STAFF_MEAL, ENTERTAINMENT),          -- (UI review) F18
                        origin CHECK IN (MANUAL, AMENITY) DEFAULT 'MANUAL',                -- AMENITY: made by the close (5.9)
                        department_id NULL (required for USE, STAFF_MEAL, ENTERTAINMENT), outlet_id NULL,
                        reason_code CHECK IN (SPOILAGE, EXPIRED, BREAKAGE, TASTING, OTHER) NULL unless purpose = WASTE,
                        covers int NULL,          -- STAFF_MEAL: how many meals (cost per meal); ENTERTAINMENT: how many guests
                        beneficiary varchar(100) NULL,   -- ENTERTAINMENT: the host (an officer) or the guest / event; required for ENTERTAINMENT
                        reverses_issue_id NULL UK, status POSTED|REVERSED, note)
inv_issue_lines        (issue_id, line_no, item_id, qty_stock, value, expense_account_id NULL (default: category), movement_id)
inv_counts             (id, cnt_number, client_uuid UK, warehouse_id, scope CHECK IN (ALL, CATEGORY, LIST), category_id NULL,
                        status CHECK IN (OPEN, SUBMITTED, APPROVED, CANCELLED), started_at, started_by, business_date_started,
                        submitted_by, decided_by, decided_at, total_variance_value, version)
inv_count_lines        (count_id, item_id, book_qty (snapshot at the freeze), counted_qty NULL,
                        counted_at timestamptz NULL,      -- set by the SERVER (injected clock) whenever counted_qty is entered or changed; CHECK ((counted_qty IS NULL) = (counted_at IS NULL))
                        expected_qty, held_before_qty, variance_qty, variance_value,   -- all four computed at approval (4.7), NULL before
                        counted_by, note)
```

**Automatic amenities (UI review, I16).** A plan is a set of **immutable versions**: saving inserts a new version, a run keeps the version it used, so a change never rewrites a day.

```
inv_amenity_plans      (id, property_id, version_no, effective_from date,      -- the open business day when it was saved: the close of that day already uses it (5.9)
                        is_enabled boolean,                                     -- switching the feature off is a version with false
                        warehouse_id, department_id, expense_account_id,        -- source warehouse, department (Rooms / Housekeeping), account (5150)
                        created_at, created_by)                                 UK (property_id, version_no)
inv_amenity_plan_lines (plan_id, line_no, room_type_id NULL (NULL = every type), item_id, unit_id, qty numeric(18,6) > 0,
                        basis CHECK IN (PER_ROOM, PER_GUEST))                   -- composite FK (item_id, unit_id) to inv_item_units; a guest = adult + child of the room that night
inv_amenity_runs       (id, property_id, business_date, plan_id, issue_id UK -> inv_issues, rooms int, guests int, comp_rooms int, house_use_rooms int,
                        total_value numeric(18,3), status CHECK IN (POSTED, REVERSED), reversal_issue_id NULL, created_at)
                        UK (property_id, business_date)                         -- one document per business day: the idempotency key
inv_amenity_run_rooms  (run_id, room_id, room_type_id, stay_id, guests smallint, occupancy_kind CHECK IN (PAID, COMPLIMENTARY, HOUSE_USE))   PK (run_id, room_id)
inv_amenity_run_lines  (run_id, line_no, plan_line_id, item_id, basis, qty_per, multiplier int,      -- "1 x 78 rooms", "2 x 78 rooms", "Suite: 1 x 12 guests"
                        qty_stock, stock_before, unit_cost NULL, value NULL, issue_line_no,
                        status CHECK IN (APPLIED, HELD) DEFAULT APPLIED,       -- (D24) HELD while the warehouse is frozen by a count
                        applied_on date NULL, applied_at timestamptz NULL)     -- the open day and the instant it was applied; unit_cost and value are known only then
```

`inv_issue_lines` gets the same `status` (its `movement_id` is NULL while HELD). The document and the run are created at the close whatever happens; only the **movements** of a frozen warehouse wait (5.9, 4.7). The document's `created_at` is the instant of the close and decides the side of each held line against `counted_at`.

The document is an `inv_issues` row (`origin = AMENITY`, `purpose = USE`) with its lines, so it appears in "Pemakaian & waste" (mockup 33) and the movements are ordinary `ISSUE` rows with `source_type = INV_ISSUE`. The run tables keep what the plan and the occupancy were.

### 3.5 PR 4 (migration 00087): the POS side

```
inv_consumption_queue
  id, tenant_id, property_id, outlet_id, warehouse_id
  pos_bill_line_id bigint (composite FK to pos_bill_lines), kind CHECK IN (SALE, VOID_RETURN, VOID_WASTE)
  sale_class CHECK IN (FOOD, BEVERAGE, OTHER)   -- (UI review) 4.9: the class of the product, resolved and snapshotted at posting
  sale_date date                         -- the business day of the posting
  components jsonb                       -- [{item_id, qty_stock}] snapshot of the recipe, expanded at posting (5.6)
  status CHECK IN (PENDING, HELD, APPLIED, CANCELLED, FAILED), attempts, error_code, movement_ids bigint[], applied_on, created_at, applied_at
  UK (pos_bill_line_id, kind)            -- a line is queued once; the idempotency of the hook
  IDX (property_id, status, id) WHERE status IN (PENDING, HELD, FAILED)
```

Plus the `pos_outlets.warehouse_id`, `pos_categories.fb_class` and `pos_recipe_lines` changes of 3.1. A queue row is an operational record (no day trigger); the movements it produces are.

### 3.6 PR 5 (migration 00088): the snapshots of the reports

```
inv_balance_days       (warehouse_id, item_id, business_date, qty, value)       -- as 7.5: written at the close only for the pairs that moved
inv_fb_cost_days       -- (UI review, I17) one row per business day, outlet and class, written ONCE by the close, after the queue and the amenities
  id, property_id, business_date, outlet_id NULL (NULL = warehouses without an outlet), class CHECK IN (FOOD, BEVERAGE, OTHER)
  revenue_net numeric(18,3)         -- POS net sales of the class (12.3)
  no_recipe_revenue numeric(18,3)   -- of which lines whose product has no recipe
  sale_cost, waste_cost, staff_meal_cost, entertainment_cost numeric(18,3)       -- from the ledger of the day (kinds and cost_class)
  UK (property_id, business_date, COALESCE(outlet_id, 0), class)
inv_cost_days          -- (D31) one row per business day, department and kind of cost: the daily cost of all costs (12.2)
  id, property_id, business_date, department_id NULL, outlet_id NULL,
  cost_kind CHECK IN (SALE_COST_FOOD, SALE_COST_BEVERAGE, WASTE, STAFF_MEAL, ENTERTAINMENT, AMENITY, ENGINEERING, OTHER_USE, COUNT_VARIANCE, PRICE_VARIANCE)
  amount numeric(18,3)              -- signed: a gain or a favourable variance is negative
  UK (property_id, business_date, COALESCE(department_id, 0), COALESCE(outlet_id, 0), cost_kind)
inv_cost_targets       (id, property_id, outlet_id NULL (NULL = the property's default), class CHECK IN (FOOD, BEVERAGE), target_pct numeric(5,2), updated_at, updated_by)
                       UK (property_id, COALESCE(outlet_id, 0), class)           -- a target per outlet and class (mockup 31); an outlet without a row takes the property's
```

`ENGINEERING` is the `USE` issue of the department named by the setting `engineering_department_id` (NULL = none); every other non-amenity `USE` issue is `OTHER_USE` (stationery and the rest). `AMENITY` is the amenities document by the day its movements are written (so a held document, D24, counts on the day it is applied). `COUNT_VARIANCE` is dated the business day the count is **approved**, `PRICE_VARIANCE` the day the bill is **confirmed**. The occupied rooms come from `inv_amenity_runs.rooms`. A closed day never changes (the day trigger, the append-only ledger), so a row of `inv_fb_cost_days` and of `inv_cost_days` is final the moment it is written; a test recomputes it from the ledger and the POS lines and compares. The tables are operational (no day trigger).

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

Only a **POS sale** and the **automatic amenities issue of the close** (5.9, I16) may take a balance below zero; both are written by system code that passes `AllowShortfall` to the ledger writer, and the structural guard of 5.1 lists these two callers and no other. A transfer, a manual issue, a staff meal, an entertainment, a waste, a return to supplier and a count refuse (409 `NEGATIVE_STOCK_NOT_ALLOWED`, with the item, the warehouse and the quantity available). In what follows "sale" reads "issue" for the amenities, and the cost-of-sales account reads the expense account of the amenities plan.

1. The sale takes what the warehouse has at the real cost (average or layers). The part it does not have, `s`, is costed at an **estimated unit cost** `e`: the last unit cost of the balance (`last_unit_cost`, the cost of the last IN), else the item's `reference_cost`, else the cost of the newest receipt of the item in any warehouse of the property, else **zero** with `cost_estimated`.
2. The movement `POS_SALE` carries the whole quantity and the whole value; the balance becomes negative (`qty -s`, `value -s x e`). A row of `inv_shortfalls` records `s`, `e`, the cost-of-sales account and the department of the sale. Cost of sales is booked at the estimate on the day of the sale; the report "Stock sold below zero" lists the open shortfalls (item, warehouse, quantity, since when, menu items that caused it).
3. **When goods come in** (a receipt or a transfer in; a count gain is not a settlement, item 4) at unit cost `c`, the IN first **settles the open shortfalls oldest first**. For the covered quantity `k` of a shortfall with estimate `e` it writes a row `NEG_SETTLE` with `qty_delta = 0` and `value_delta = -k x (c - e)`, journaled **Dr cost of sales / Cr inventory** (reversed in sign when the real cost was lower than the estimate), with the department and account of the original sale. The IN movement itself is booked at `c` for its whole quantity. The result: the balance holds the right value for what is really on hand, the cost of sales carries the real cost of the goods sold, and nothing is lost between the two. The method makes no difference to the rule: under **FIFO** the covered quantity creates no layer (the layers hold only the part of the IN that is really on hand); under **average** the balance is the plain sum plus the settlement row.
4. A **count** that sets the quantity to what was found ends the shortfalls of that item and warehouse without a settlement (`CLOSED_BY_COUNT`): the physical result already says what exists, and the cost of the sales stands as estimated. This is how a negative balance that was never explained by a receipt is cleared.
5. Worked in 8.4.

### 4.5 Transfers between warehouses

The transfer out is valued like any OUT of the source (average, or the layers consumed). The transfer in receives **exactly that value** (average: the destination balance adds the value, its average moves; FIFO: layers copied, 4.3). There is no revaluation and **no journal**, because both warehouses of an item are under the same inventory account (the category decides the account, not the warehouse). The ledger holds the pair (`TRANSFER_OUT`, `TRANSFER_IN`) with `gl_amount = 0` and no accounts. The source must have the quantity (I7); the destination may be negative (a shortfall there is settled by this very transfer, 4.4). **The same holds across cost classes (UI review, I14):** a lime sent from the main store to the bar, a bottle of wine sent to the kitchen, still need no journal, because the class works when the cost leaves the balance sheet (a sale, a staff meal ...), not when goods move between warehouses (4.9).

### 4.6 Return to supplier

A return names the **receipt line** it returns. It is valued at the **cost that receipt line created**, not at today's average: under average the value out is the line's value for the quantity returned (`qty x unit cost of the line`), under FIFO the layer of that line is consumed first, then the oldest. Journal: Dr GRNI / Cr inventory. If the receipt was already billed, the supplier's credit note (document 14) is entered in `payables` against the bill line; linked to the return, it is matched like a bill and any difference between the credit note and the cost of the goods is cleared the same way (5.3.6). A return is allowed only for stock that is on hand (I7). An average that has moved since the receipt makes the balance value differ from `qty x avg` by the small difference; that difference stays in the balance (it is the effect of the line's real cost) and is not an error.

### 4.7 Count differences

A count line has `book_qty` (taken at the freeze), `counted_qty` and `counted_at` (the server's instant when the quantity was entered or last changed). Sales that happen while the warehouse is frozen are held (5.5), and a sale made **before** `counted_at` has already taken its goods off the shelf the counter looked at. So at approval:

- `held_before_qty` = the signed sum, in stock units, of the components for this item and warehouse of queue rows `HELD` with `created_at < counted_at` (a `SALE` row counts negative, a `VOID_RETURN` positive, a `VOID_WASTE` nothing);
- `expected = book_qty + held_before_qty`;
- **`variance = counted - expected`**.

The held rows before `counted_at` are applied **first**, as ordinary `POS_SALE` / `POS_SALE_RETURN` movements, so the balance is `expected` when the count adjusts it; the held rows from `counted_at` on are applied **after** the count. Nothing is deducted twice (worked in 8.6). The comparison is by item: a queue row whose components touch several items is applied component by component, each on its side of its own item's `counted_at` (the component carries an `applied` flag in the snapshot; the row is `APPLIED` when all are). An item that was not counted has no `counted_at`: its held rows are applied after the count, and it is not adjusted. **Held amenities lines (D24).** The lines of an amenities document held by the freeze (5.9) are treated exactly like the components of a queue row: they count in `held_before_qty` as a **negative** quantity when the document's `created_at < counted_at` of that item, they are applied first (as `ISSUE` movements) and after the count otherwise. They are in stock units and the item's own `counted_at` decides, line by line. Under **FIFO** a held sale applied first consumes the oldest layers, then a loss consumes the oldest remaining and a gain makes a layer at the last unit cost, exactly as in 4.3. With **negative stock**: if applying the held sales takes the balance below zero, the shortfall (4.4) is created at the estimate, and the count (which sets the quantity to what was found) closes it without a settlement (4.4 item 4); `expected` may then be negative and the gain is `counted - expected`, valued at the last unit cost, so the net effect on inventory is the counted quantity at cost and the sale stays in cost of sales at its estimate.

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

### 4.9 Cost class and the cost transfer between food and beverage (UI review, I14)

**The problem.** Lime (category kind FOOD, inventory account 1310) goes into the drinks of the bar; wine (BEVERAGE, 1320) goes into a sauce of the kitchen. If the cost of sales followed the category of the **item** (as 6.1 said before this review), the lime would charge 5210 "Cost of food sales" against the **beverage** revenue of the cocktail, every day, and the food cost % would be too high and the beverage cost % too low. A journal at the month end could correct the month, never the daily cost of mockup 31.

**The design (recommended; D28).**

1. *Two classes on every movement, snapshotted when it is posted:* `item_class` = the kind of the item's category (FOOD, BEVERAGE; SUPPLIES and OTHER are class OTHER); `cost_class` = the class the cost is charged to.
2. *For a POS sale* `cost_class` is the **sale class**, resolved once, when the line is queued (5.6), and stored in the queue row: the `fb_class` of the product's POS category (a sub-category takes its parent's), else the `cost_class` of the outlet's warehouse, else OTHER. The cost-of-sales account follows the class: FOOD -> key `INV_COGS_FOOD` (5210), BEVERAGE -> `INV_COGS_BEVERAGE` (5220), OTHER -> the `cogs_account_id` of the item's category. **It follows the product sold, not the ingredient, and not the warehouse alone:** a restaurant that sells coffee from its kitchen warehouse (class FOOD) charges the coffee to 5220 because the category "Minuman" of the product says BEVERAGE; the warehouse class is only the default for a category that says nothing (the three considerations of the owner are all used: the class on the warehouse, the account decided by the class and not by the item's category, and, below, no journal between accounts).
3. *Warehouses* carry `cost_class` (FOOD, BEVERAGE, OTHER; the main store is OTHER). It is the default of item 2 and the label of the warehouse in the reports.
4. *Every other kind of movement* (staff meal, entertainment, waste, count variance, issue) takes `cost_class = item_class`: a drink given to a guest is beverage cost, a plate is food cost.
5. **No transfer journal between cost-of-sales accounts.** The account is chosen at the moment the cost leaves the balance sheet, from a class that is then fixed in the movement, so there is nothing to move later. A journal Dr 5220 / Cr 5210 would be needed only if the cost were first booked by the item's category and corrected afterwards (the alternative that was weighed): it would be right only after the month-end entry, wrong in every daily report, and it would be a derived entry the accountant has to trust. It is rejected for that reason, and the chart keeps one posting per sale.
6. **The "cost transfer in / out" of the reconciliation (mockup 30) is measured, not posted.** The reconciliation starts from the stock by **item class** (what the counts and the control accounts 1310 / 1320 measure) and must end at the cost by **sale class** (what 5210 / 5220 hold). The difference is exactly the cost of items consumed by a sale of the other class:
   - *cost transfer out of class A* = sum of `value_delta` of the sale movements (`POS_SALE`, `POS_SALE_RETURN`, `NEG_SETTLE`, signed as cost) with `item_class = A` and `cost_class = B`, `B <> A`, both in FOOD / BEVERAGE;
   - *cost transfer in to class B* = the same sum, seen from the other side.
   For September in mockup 30: food to beverage 1,480,000 (lime, sugar, fruit used by the bar), beverage to food 320,000 (wine and beer used by the kitchen). Worked, one day and the month, in 8.10.

**Impact on the rest of this document.** *4.5:* a transfer between warehouses of different class has no journal (stated there). *6.1:* the account of a POS sale reads the sale class (the row is rewritten). *6.2:* the debit of `POS_SALE`, `POS_SALE_RETURN` and `NEG_SETTLE` is the account of the movement's `cost_class`; rows for `STAFF_MEAL` and `ENTERTAINMENT` are added. *6.3:* a third control compares the cost-of-sales accounts with the ledger by class (a warning, because a hotel may still post by hand to 5210 / 5220). *3.x:* the columns `item_class`, `cost_class`, `outlet_id` on the ledger, `cost_class` on warehouses, `fb_class` on POS categories, `sale_class` on the queue. *Setup check:* a POS category whose `fb_class` disagrees with the revenue account of the charge code of its products (a BEVERAGE category on a charge code that posts to 4210 food revenue) is a warning `FB_CLASS_REVENUE_MISMATCH`; the cost reports follow the class, the general ledger revenue follows the charge code, and the warning says when they part.

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
| Create, edit, submit, recall a PR | `inventory.request` | PR row; at the submission the chain is copied into steps | `inv_requests`, `inv_approval_steps` (the snapshot), notices |
| **Approve or return** a PR (one level) | the permission of the active level, and for a head level the assignment in Departments (5.8) | PR row -> its steps | step, edits of lines, status, notices, audit; no password: the approver is the signed-in user |
| Create a PO (from a PR or direct) | `inventory.purchase` | PR row, PO row | `inv_purchase_orders`; the PR becomes CONVERTED |
| Send, cancel, close short a PO | `inventory.purchase` | PO row | status |
| **Post a goods receipt** | `inventory.receive` | PO row (if any) -> warehouse (share) -> balances -> sequence | receipt, lines, movements, **bill draft**, PO quantities, audit |
| Reverse a receipt | `inventory.receive` | receipt row -> PO row -> warehouse -> balances | reversing movements; only on the day of the receipt, unbilled (5.3.5) |
| Return to supplier | `inventory.receive` | receipt row -> warehouse -> balances -> sequence | return, `SUPPLIER_RETURN` movements |
| **Confirm a bill from drafts** | `inventory.bill` and `payables.post` (+ approval for a variance) | drafts, receipt lines, PO lines, balances, then (inside `PostBill`) settings, supplier, sequence | the bill in `payables`, `inv_bill_matches`, clearing movements |
| Unmatch a bill | `inventory.bill` and `payables.post` + approval `PAYABLES` | the same | reverses the clearing, voids the bill, reopens the draft |
| Requisition (SR): create, edit, submit, recall | `inventory.request` | requisition row; the chain is copied | rows, steps |
| **Approve or return** an SR (one level) | as for a PR | requisition row -> its steps | as above |
| Transfer (answers an APPROVED SR, or free) | `inventory.move` | requisition -> transfer -> both warehouses (share) -> balances of both, one call -> sequence | `TRANSFER_OUT` and `TRANSFER_IN` |
| Issue to a department / waste / staff meal / entertainment | `inventory.move` | warehouse (share) -> balances -> sequence | `ISSUE` / `WASTE` / `STAFF_MEAL` / `ENTERTAINMENT` |
| Start a count | `inventory.count` | count row -> **warehouse `FOR UPDATE`** -> balances (to snapshot) | the freeze, the snapshot |
| Enter, submit a count | `inventory.count` | count row | lines |
| **Approve a count** | `inventory.approve` | count row -> warehouse `FOR UPDATE` -> balances | `COUNT_GAIN` / `COUNT_LOSS`, ends the freeze |
| Opening stock | `inventory.opening` | warehouse -> balances -> sequence | `OPENING` movements |
| Apply the POS queue (worker, night audit) | system | warehouse (share) -> balances | `POS_SALE`, `POS_SALE_RETURN`, `COGS_TO_WASTE` |
| **Amenities run** (the close) | system (night audit) | business day (update) -> stays, folios (the room charges) -> warehouse (share) -> balances, in one call with those of the queue -> | the issue, the run tables, `ISSUE` movements, shortfalls (5.9) |
| Reverse an amenities run | `inventory.manage` | run row -> warehouse (share) -> balances | a reversing issue (5.9) |
| Edit the amenities plan | `inventory.manage` | none (plain data; a new version) | `inv_amenity_plans` |
| Edit an approval chain | `property.manage` | none (plain data; documents in flight keep their snapshot) | `inv_approval_levels`, `inv_approval_settings` |
| Set the head and the deputy of a department | `accounting.manage` | none | `departments` (document 12, 3.3) |
| Edit a recipe | `pos.manage` | none (plain data, like the product master) | `pos_recipe_lines` |

### 5.3 Purchasing

**5.3.1 The request (PR) and its approval (I4, I13).** A person with `inventory.request` writes a PR: items, quantities in the purchase unit, an estimated price (the supplier item's last price, editable), the **department** (required to submit: its head approves level 1) and the date needed. `est_total` is the sum. **Submit** copies the approval chain into the document and moves it to SUBMITTED. The **approval** is the chain of 5.8 (up to three levels, each a decision of a signed-in person, with no password and no rule of document 32). Return needs a note; the PR goes back to the requester (RETURNED) and a new submission starts the chain again. An APPROVED PR can be turned into one or more POs (one per supplier) and then is CONVERTED; the line quantities of the POs may not exceed the approved ones (after any edit of an approver). With `po_requires_pr = false` a PO can be written without a PR (D13).

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

- **Requisition, SR (I2, I13).** An outlet's person writes a requisition (items, quantities in the recipe or stock unit, date needed) to the main store; its department (the head of which approves level 1) defaults to the department of the asking warehouse or of its outlet. It goes through the **chain of 5.8 (two levels by default)**. **Only an APPROVED or PARTIAL SR can be answered**: a transfer that names an SR in any other state is refused with 409 `SR_NOT_APPROVED`. The store answers with **one or more transfers** (`requisition_id`), each posting immediately; the requisition becomes PARTIAL or FULFILLED by the issued quantities; the store may still reject an approved SR with a reason (REJECTED: it cannot supply, which is not the chain's decision). A transfer can also be made without a requisition (`inventory.move`): the "Transfer bebas" of mockup 22; it needs no approval, as before, and the report of transfers lists the ones without an SR.
- **Transfer.** One transaction, both warehouses (share), the balances of both in one call (ascending id, so two transfers in opposite directions cannot deadlock), the source must hold the quantity (I7), one pair of movements per line, no journal (4.5). Single step: the goods are in the destination when the transfer posts (D9).
- **Issue to a department** ("pemakaian departemen", I1). Housekeeping amenities, engineering parts, stationery leave a warehouse for the expense of a department. The department is required; each line's expense account defaults to `inv_categories.issue_account_id` and may be changed on the line; the department rule of the account is applied when the line is posted (`Poster.ResolveDepartment`, the way `PostBill` does), so a REQUIRED department that cannot be found is refused at the issue and never stops the day close.
- **Waste.** An issue with `purpose = WASTE`, a reason code (SPOILAGE, EXPIRED, BREAKAGE, TASTING, OTHER) and the account of the category's waste (or `INV_WASTE`), the department of the warehouse's outlet or department. Waste stays a kind of its own and is **not** a staff meal or an entertainment.
- **Staff meal (UI review, I15).** An issue with `purpose = STAFF_MEAL` from a kitchen warehouse: the department that eats is required, `covers` says how many meals (the report shows cost per meal). Account: key `INV_STAFF_MEAL` (default 5240, employee benefits of food and beverage; D29), department of the document, movement kind `STAFF_MEAL`, `cost_class = item_class`. It leaves the **net cost of sales** (12.1).
- **Entertainment / officer check (UI review, I15).** An issue with `purpose = ENTERTAINMENT`: food or drink given to a guest, an event or an officer's table as a courtesy. Required: the department that bears the cost (the host's: Sales and marketing, Administrative and general ...), `beneficiary` (the host or the guest / event) and `covers`. Account: key `INV_ENTERTAINMENT` (default 6170, travel and entertainment; D29), movement kind `ENTERTAINMENT`. It leaves the net cost of sales. No approval in this design (D30). Both purposes can be reversed on the day like any issue.
- **Correcting** a transfer, an issue or a waste: a reversing document on the same day; later, a new document the other way. Never an edit. (The amenities document of the close is the one exception: it is made on a day that is already closed, so its reversing document is dated a later day, 5.9.)

### 5.5 The count and the freeze (I9)

1. **Start** (`inventory.count`): the count row and the warehouse are locked `FOR UPDATE` (so in-flight mutations finish first); `inv_warehouses.count_id` is set; `book_qty` is copied for every item in scope (a warehouse, a category or a list). From now on every mutation of that warehouse except the POS queue refuses with `WAREHOUSE_FROZEN` (409). Only one count per warehouse at a time (a partial unique index on `count_id`).
2. **Enter**: counters type `counted_qty` in the stock or a counting unit, on a phone or the desk; the sheet is blind by default (`count_blind`). Each time a quantity is entered or changed the **server** stamps `counted_at` on that line (never the client's clock). Items not counted are not changed.
3. **Submit** locks the sheet; **approve** (`inventory.approve`, decision of the approver, kind `INV_COUNT`, amount `abs(total variance value)`, `Subject` = the person who submitted) in one transaction (count row -> warehouse `FOR UPDATE` -> balances) first applies, per item, the held sales created before that item's `counted_at`, then posts the variances (4.7) against `expected`, ends the freeze, applies the rest of the held rows and writes the audit. **Cancel** ends the freeze and posts nothing.
4. **What happens to POS sales during the count.** A sale is never blocked (I7). Its queue row is applied to a frozen warehouse only after the freeze ends: the worker marks it `HELD` and skips it. Held rows are applied in order, dated the business day when they are applied; `source_date` keeps the day of the sale. **Which side of the count a sale is on** is decided per item by the instant the item was counted (4.7): a sale before `counted_at` is already missing from the shelf, so it is applied before the adjustment and lowers what the count expects; a sale after it is applied after the adjustment. The sale is therefore deducted once, whatever the order in which things happen. On a **cancel** all held rows are simply applied. **The held lines of the amenities document of a close that fell inside the count (5.9, D24) follow the same rule** as the components of a queue row: their side of each `counted_at` is decided by the document's `created_at`, they are applied with the held rows, and nothing is applied twice. A sale made *while the counter is walking to the shelf* is the only residual error (the counter may have seen the goods before the plate left); it is as small as the time between looking and typing, and the screen suggests counting when the outlet is quiet. Worked in 8.6.
5. **A count that runs over the day close.** By default it is a **blocker** of the night audit (`count_blocks_night_audit`, D6): the close would otherwise have to leave the held rows for tomorrow and date their cost a day late. A property that chooses the warning accepts that: the held rows are applied on the first business day after the freeze ends (their `created_at` still decides their side of each `counted_at`), and the report shows the sale date. **The amenities of that close are among the held rows** (D24): the document is written at the close, its lines for the frozen warehouse are `HELD`, and they are applied when the count is approved or cancelled, dated the first business day after the freeze, with `source_date` kept as the night of the amenities (as D19).

### 5.6 The deduction of a POS sale (I6)

**What triggers it.** A POS line becomes a sale when it is posted to the ledger: on **send** (modes `PRINT`, `KDS`) or on **payment** (mode `DIRECT`), exactly the moments of `34-pos.md` 4.2. In the same transaction as `PostCharge`, and only if the plan has POS **and** Inventory (I12) and the outlet has a warehouse, the POS code inserts **one row in `inv_consumption_queue`** for the line.

**What the row contains.** The recipe is expanded then: the product's lines with `option_id IS NULL` plus the lines of the options the line carries; **a variant's line for an item replaces the product's line for that same item** (a "large" variant says 0.3 kg of rice instead of 0.2), **a modifier's lines add**; each quantity is converted from the recipe unit to the stock unit and multiplied by the line quantity. The result is the `components` snapshot, so a later change of the recipe never alters a sale already queued. The **sale class** of the line (4.9) is resolved at the same moment and stored in `sale_class`, so a later change of a POS category never alters it either. A product with no recipe lines queues nothing.

**Same transaction or the outbox: the choice (D4).**

| | Deduct inside the POS transaction | **Queue row, worker applies (chosen)** |
|---|---|---|
| Cashier's wait | the send holds the folio `FOR UPDATE` (42) and also the balance rows (45) of every ingredient: all sends that use the same rice, oil or ice cube take turns | one `INSERT` of a few hundred bytes; no stock lock in the POS transaction |
| Lock order | stock (45) after folios (42): valid, but the bill locks are held longer and a stock lock timeout (5 s) would fail a send | none: the POS transaction takes no lock it did not take before |
| I7 (a sale is never blocked) | a lock wait or a failure of the stock code can fail the sale | the sale cannot fail because of stock |
| Consistency | stock exact at commit | stock exact within seconds; **always before the day closes** (the night audit drains the queue) |
| Dated | the day of the sale | the day it is applied; equal except when a count holds it over midnight |
| Cost | simple | one table, one worker, one failure state |

**The worker.** A process loop (and the night audit) takes up to 50 rows `PENDING` ordered by id with `FOR UPDATE SKIP LOCKED`, groups them by warehouse, and in one transaction per group: business day (share) -> warehouse (share) -> the balances of all the items, ascending, in one call -> `POS_SALE` movements (one per component, valued by the method, shortfall rules of 4.4, the cost-of-sales account of the row's **sale class** (4.9), the department of the **outlet**, so cost and revenue of "Restoran" meet in the departmental report) -> rows `APPLIED`. A warehouse that is frozen leaves its rows `HELD`. A row that cannot be applied (the warehouse was switched off, an account missing) becomes `FAILED` with a code; failed rows are shown in the setup check and **block the night audit** (`INVENTORY_QUEUE_FAILED`) until retried or cancelled by someone with `inventory.manage`. Two workers or two instances never take the same row (`SKIP LOCKED`); the unique key `(pos_bill_line_id, kind)` makes the hook idempotent, so a replayed request queues nothing twice.

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

Today `nightaudit.Run` takes the advisory lock, the open day `FOR UPDATE`, analyses the blockers, marks the occupied rooms dirty, posts the room charges (`PostLocked`), ends the locks of outlet charges (`ExpireOutletLocks`), checks the journal's readiness (`JournalReadiness`), makes the journal (`PostDay`), writes the summary and opens the next day. The stock steps go **between `ExpireOutletLocks` and `JournalReadiness`**, inside the same transaction, in this order:

1. **Drain the POS queue** (5.6): every `PENDING` row applied; `FAILED` rows block.
2. **The amenities run** (5.9, I16): reads the nights of the day that the room-charge step has just settled, writes the issue.
3. **The snapshots** (PR 5): `inv_balance_days`, `inv_fb_cost_days` and `inv_cost_days` are written from the ledger of the day, which is now complete.
4. `JournalReadiness` and `accounting.PostDay` read the stock ledger of the day (section 6).

The order of the locks is ascending: day (10), rooms (30), stays (41) and folios (42) of the room charges, then **stock (45)** for steps 1 to 3, then the accounting settings (46). The balance rows of steps 1 and 2 are taken **in one call, ascending id**. No other writer of stock can run: it needs the day in share mode, and the close holds it `FOR UPDATE`. `nightaudit` gets one more small interface, `StockCloser` (the analysis lines and `CloseDay`), implemented by `internal/inventory` and wired in `internal/app`, like `OutletChecker`; inventory gets the occupied nights from `frontdesk` through an interface of its own (5.9).

| Check | Kind |
|---|---|
| consumption queue rows `FAILED` or not drainable | blocker `INVENTORY_QUEUE_FAILED` |
| an open count (OPEN or SUBMITTED) | blocker `INVENTORY_COUNT_OPEN`, or a warning if `count_blocks_night_audit` is off |
| the amenities plan is on and cannot run (warehouse inactive, an item, the account or the department rule invalid) | blocker `AMENITY_PLAN_INVALID`, with the cause; the way out is to repair the plan or switch it off. **A warehouse frozen by a count is not a cause** (D24) |
| the amenities warehouse is frozen by a count that the property lets the close pass | warning `AMENITY_HELD` (count number, lines held); the lines are applied after the freeze (5.9) |
| an amenity item will not cover tonight's need | warning `AMENITY_STOCK_SHORT` (item, quantity short); **never a blocker** (I16) |
| balances below zero (shortfalls open) | warning (count of items, link to the report) |
| the ledger and the balances disagree (the drift check, 7.5) | blocker `INVENTORY_BALANCE_DRIFT` |
| the stock value and the GL control accounts disagree (section 6.3) | blocker, like `TRANSFER_ACCOUNT_NOT_ZERO` |
| receipts older than 30 days with no bill | warning |

### 5.8 The approval chain (UI review, I13)

**What it covers.** The PR (up to three levels) and the SR (two). Made with the module and editable per property and per document type (mockup 34; `property.manage`; audited):

| Document | Level | Name | Who approves | When |
|---|---|---|---|---|
| PR | 1 | Kepala departemen | head (or deputy) of the requesting department | always |
| PR | 2 | Cost control | holders of `inventory.cost_control` | always |
| PR | 3 | Persetujuan akhir (FC/GM) | holders of `inventory.final_approve` | total above 5,000,000 |
| SR | 1 | Kepala departemen | head (or deputy) of the requesting department | always |
| SR | 2 | Persetujuan akhir SR | holders of `inventory.sr_final_approve` | always |

A level has a **name** (free text), an **approver** (the head of the requesting department, or the holders of one permission of the closed list `inventory.cost_control`, `inventory.final_approve`, `inventory.sr_final_approve`) and a **condition** (`ALWAYS`, or `TOTAL_ABOVE` a threshold; the total of an SR is its `est_total`, quantity times the cost in the main store). Levels can be added and removed, 0 to 5 per document type; a chain with no level approves at the submission (the setup check warns).

**The rules** (the four lines "Aturan umum" of mockup 34; the first two are firm and should be drawn checked and locked, the last two are the options O1 and O2):

| | Rule |
|---|---|
| R1 | One person decides **one level** of a document in a round. Held by a unique index on the steps (3.3) and answered `APPROVAL_SAME_PERSON`. |
| R2 | The requester never approves their own document (403 `APPROVAL_OWN_DOCUMENT`), the tenant administrator included (D25). |
| R3 | Levels are in order: a level decides only when it is ACTIVE (409 `APPROVAL_NOT_YOUR_TURN`). A return needs a note. |
| O1 | `skip_level1_if_head` (default on): if the requester is the head or the deputy of the department, level 1 is SKIPPED (`REQUESTER_IS_HEAD`). Off: level 1 stays, and the other of head and deputy has to decide (R2 keeps the requester out). |
| O2 | `approver_may_edit` (default on): an approver may change the quantity or the estimated price of lines before approving. |

**Not `iam.Verify` (F15).**

| | `iam.Verify`, mode Decision | The chain engine (chosen) |
|---|---|---|
| Memory between acts | none: one act, one approver, one rule | the snapshot, the level, who decided, the edits (`inv_approval_*`) |
| Who may approve | one permission per kind | per level: head or deputy of the department of the document, or the holders of one permission |
| Rules | `FREE`, `DISTINCT`, `DISTINCT_ABOVE` by kind and amount | fixed R1 to R3 and two options; the rules screen of document 32 would list `INV_PR` with a rule that means nothing next to the chain |
| Administrator | accepted and marked | no exception (D25) |
| Used for | `INV_COUNT` (one act, within hours), `INV_BILL_VARIANCE` (credentials, at once) | PR, SR |

What the engine takes from document 32: the permission check through the `Authorizer`, the shape of the audit entry (`approved_by`), and a structural test in its spirit (`chainpath_guard_test.go`, with `go/parser`: only the engine writes the status of `inv_requests` and `inv_requisitions` to SUBMITTED, APPROVED or RETURNED, and every such path writes audit and notices).

**The flow.**

1. **Submit** (requester, `inventory.request`). The lines and the department are checked, the total computed, the chain read and **copied into steps** (the snapshot; round = `cycle`). Each level is evaluated on the current total: `ALWAYS` applies, `TOTAL_ABOVE` applies if the total is above the threshold (shown as "wajib karena di atas 5.000.000" or "tidak perlu", provisional). If no level applies the document is APPROVED at once, with that note in the audit.
2. **Eligible** at a level: for a head level, the head or the deputy of the department of the document (a sub-department without a head takes its parent's, 3.3) who also holds `inventory.dept_approve`; for a permission level, a holder of the permission. In both, never the requester and never a person who already approved another level of this round (R1, R2). If a level that applies has **nobody** eligible at the submission, the submission is refused with 409 `APPROVAL_CHAIN_UNSATISFIABLE`, naming the level and the department: the requester learns it at once, not after waiting. If a level becomes stuck later (the head left, one person holds both permissions and has used one), a notice `CHAIN_STUCK` goes to the holders of `property.manage` and the inbox marks the document.
3. **Activation.** The first level that applies becomes ACTIVE; the others stay WAITING. Every eligible user gets a notice `APPROVAL_WAITING`.
4. **Approve** (the signed-in eligible user; body: a note, optional edits, a `client_uuid`/`Idempotency-Key`). One transaction: the document row `FOR UPDATE` (level 45, documents) -> its steps. If O2 is on, the edits are applied to the lines (quantity above zero, price not negative), one row of `inv_approval_edits` per changed field (old, new, by whom, at which level), the total recomputed, the document `version` raised. The step becomes APPROVED with `total_seen` (the total after the edits). The **later levels are evaluated again on the new total**: a conditional level is added when an edit pushed the total over its threshold and dropped when an edit pulled it under; levels already passed are never reopened (D26). The next level that applies becomes ACTIVE and is notified; if none is left the document is APPROVED and the requester is notified.
5. **Return** (any eligible user of the active level). A note is required. The step becomes RETURNED, the later steps NOT_REACHED, the document RETURNED, the requester is notified with the note. The requester changes the document and submits again: **round n+1, a fresh snapshot of the chain as it is then, from level 1.** R1 counts per round, so the same people can decide again.
6. **Recall.** The requester can pull a SUBMITTED document back to DRAFT before it is APPROVED; the steps become NOT_REACHED and stay as history.
7. **The snapshot.** Editing the chain (a level added, renamed, a threshold changed, another permission) changes `inv_approval_levels` and is audited; it never touches a step that exists. A document in flight keeps its chain; a document submitted afterwards, **and a resubmission**, gets the new one.
8. **After the chain.** An APPROVED PR can be converted into POs (5.3.1); an APPROVED SR can be answered by transfers (5.4). The approved lines, edits included, are the ceiling.
9. **Notices** are rows of `inv_notices` written in the same transaction as the step (a rollback leaves none): `APPROVAL_WAITING` to each eligible user of the active level (closed for the others when one decides), `APPROVED` and `RETURNED` to the requester, `CHAIN_STUCK` to the administrators. The header bell shows the count; the app refreshes it on focus and every 60 seconds; where the POS stream of document 34 (10.1) is running, one event `inv.approval_changed` on a channel `user:{id}` carries only ids. E-mail is not part of this design (D27).
10. **The inbox "Perlu saya setujui"** is a query, not the notices: the documents whose ACTIVE step the user is eligible for. Its list shows the stage ("2/3 Cost control") and the filter chip of mockups 18 and 22.

**The effect on the other documents.** Document 32: the list of kinds gets no `INV_PR`; `INV_BILL_VARIANCE` and `INV_COUNT` are the two new kinds (9.2). Document 12: the head and deputy columns (3.3). Document 33: the lists the screens need to read (departments, users who are heads) follow its rule of the permission of the screen.

### 5.9 Automatic amenities per occupied room (UI review, I16)

**What.** At each close the system makes **one issue** (`AMN-20260903`) of the amenities for every room that was slept in on that business day: complimentary and house-use rooms included, vacant and out-of-order rooms not (day-use is not a stay in this product: `stays` always has at least one night). The document is an `inv_issues` row of `origin = AMENITY` (3.4), shown in "Pemakaian & waste" (mockups 24, 33).

**The plan (mockup 32).** Settings: on or off, **source warehouse** (housekeeping), **department** (Rooms / Housekeeping), **expense account** (5150 guest supplies). Lines: a **base package for every room type** (item, quantity, per room or per guest) and, per room type, **extras** (the Suite also gets sandals per guest and body lotion per room). A guest is the **adults plus the children** of the stay that night. The cost per room shown on the screen (23,680 for two guests, 40,880 for a Suite) is read from the balances' cost. Saved as a **new version**; validated when saved (warehouse active, account an active postable expense account, the department rule of the account met, items active and the unit convertible, all else 422 `AMENITY_PLAN_INVALID`). A freeze by a count is not a reason to refuse a plan or a close (D24).

**Which rooms (the source of the data).** The nights of the stays, by the rule of document 00009: a stay-room segment covers the night when `start_business_date <= bd < COALESCE(end_business_date, infinity)`, for a stay that is not cancelled and whose `arrival_date <= bd < departure_date`. So the arrival night counts, the departure day does not, and a guest who moved room that day counts **once**, under the room and the room type of that night. The query lives in `frontdesk` (next to the room-night rule that `roomcharge` uses) and `inventory` reads it through an interface (`NightOccupancy(ctx, property, date)` returning room, room type, stay, guests and the occupancy kind of the rate plan: PAID, COMPLIMENTARY or HOUSE_USE), so amenities and room revenue read one rule. A test holds that the rooms of the run equal the rooms with a room night of the day (charged, complimentary or house use).

**Where in the close and the locks.** After the room charges and the queue (5.7), before the journal. The close holds the day `FOR UPDATE`, the stays and folios are already locked and settled (41, 42), so the occupancy cannot change while it is read; stock (45) is taken after them, the accounting row (46) after that. The balances of the queue and of the amenities are locked in one call, ascending.

**Idempotent.** `UNIQUE (property_id, business_date)` on `inv_amenity_runs`, and the movements are unique by `(source_type, source_id, source_line, kind)`. A second attempt on the same day finds the run and does nothing. The run is part of the close's transaction: if the close rolls back, the run does too.

**A shortage never stops the close (I16).** The issue passes `AllowShortfall`: what the shelf lacks is costed at the estimate of 4.4 (last unit cost, else the item's `reference_cost`, else the newest receipt, else zero and flagged), the balance goes below zero, a row of `inv_shortfalls` keeps the expense account and the department of the plan, and the report "Stock sold below zero" lists it with the source "Amenities". The next receipt or transfer in settles it (`NEG_SETTLE`, Dr expense / Cr inventory by the difference of cost); a count closes it without a settlement. What *can* block the close is a plan that cannot run at all (the blocker of 5.7), never the quantity and never a count.

**The journal.** Dr the plan's expense account (5150), department of the plan (Rooms) / Cr the inventory account of the item's category (1330), per category, at the close, like every issue (6.2). Worked in 8.11 (78 rooms, 142 guests, 1,895,640).

**Correction.** *Buat dokumen pembalik* reverses the **whole** document, once, on any later day: the reversing issue is dated the open day (the day of the original is closed and stays), each movement reverses its own (`reverses_id`). A line whose quantity went below zero when it was issued first lowers its open shortfall (no settlement value: the cost is the same estimate) and the rest goes back at the cost taken (layers restored under FIFO). A partial correction is a **manual issue** (`USE`) or, for a wrong count of rooms, a reversal and no new run; nothing is edited.

**Switch off, and from when a change counts.** Switching the feature off is a version with `is_enabled = false`. A version saved on business day D has `effective_from = D`: **the next close that runs after the save uses it**, normally the close of D; a run already made keeps the version it used. No occupied room, no document.

**Frozen warehouse (D24, decided 2026-10-16).** If a count freezes the amenities warehouse (possible only when `count_blocks_night_audit` is off), the close is **not** blocked and the run is still written. The component lines of the frozen warehouse are treated like the rows of the POS queue:

- the document, the run, the rooms and the lines are created at the close as always, with `created_at` = the instant of the close; the lines are `HELD` (3.4), with no movement, no cost and no journal yet;
- when the count is **approved or cancelled** the lines are applied, in the order of the document, as ordinary `ISSUE` movements with `AllowShortfall`; the side of each item against its own `counted_at` is decided by the document's `created_at` (4.7: before it, the line is applied first and lowers what the count expects; after it, applied after the adjustment), so the goods are deducted once;
- they are dated **the open business day when applied** (the first day after the freeze), `source_date` stays the night of the amenities (as D19, the report shows both); cost and journal (Dr 5150 Rooms / Cr 1330) appear on that day, valued at the cost of that moment;
- a count that runs over several closes holds one document per close; each is applied in the order of its date;
- the preview of the close warns `AMENITY_HELD` and the document screen shows "ditahan karena opname CNT-..." per line; a reversal of the document while lines are held is refused (`AMENITY_RUN_HELD`) until they are applied;
- a count that does not touch the warehouse holds nothing. The blocker `AMENITY_PLAN_INVALID` is no longer raised for this cause; it stays for the other causes of 5.7.


## 6. Accounting (I8)

### 6.1 Accounts

| Where | Account | Default |
|---|---|---|
| Inventory of a category | `inv_categories.inventory_account_id` | 1310 food, 1320 beverage, 1330 operating supplies, 1340 other |
| Cost of sales of a POS sale (UI review) | by the **sale class** (4.9): key `INV_COGS_FOOD` or `INV_COGS_BEVERAGE`; class OTHER: the category's `cogs_account_id` | 5210 cost of food sales, 5220 cost of beverage sales; a sale of class OTHER whose category has no account is not queued and the setup check says so |
| Staff meal (UI review) | key `INV_STAFF_MEAL` | 5240 employee benefits (food and beverage); department of the document |
| Entertainment / officer check (UI review) | key `INV_ENTERTAINMENT` | 6170 travel and entertainment; department of the document |
| Amenities (UI review) | `expense_account_id` of the plan | 5150 guest supplies, department Rooms |
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
| `POS_SALE` | cost of sales of the **sale class** (4.9), department of the outlet | inventory | `INV_SALE_COST` |
| `POS_SALE_RETURN` | inventory | cost of sales | `INV_SALE_COST` |
| `COGS_TO_WASTE` | waste | cost of sales | `INV_WASTE` |
| `NEG_SETTLE` | cost of sales (original sale's) | inventory | `INV_SALE_COST` |
| `ISSUE` | expense of the line, department of the issue (an amenities run: the plan's account and department) | inventory | `INV_ISSUE` |
| `WASTE` | waste | inventory | `INV_WASTE` |
| `STAFF_MEAL` | staff-meal account, department of the document | inventory | `INV_STAFF_MEAL` |
| `ENTERTAINMENT` | entertainment account, department of the document | inventory | `INV_ENTERTAINMENT` |
| `COUNT_LOSS` / `COUNT_GAIN` | count variance / inventory | inventory / count variance | `INV_COUNT` |
| `TRANSFER_OUT` / `TRANSFER_IN` | none | none | none |

Each stock row is self-balancing, so the "journal does not balance" check of `postDay` still holds. The **department** of a cost line is the outlet's (sales), the issue's (use), the warehouse's (waste, count), snapshotted; the rule of the account (`lineDepartment`) was applied when the movement was posted, so the close never stops for a missing department. The lines are labelled like the others (`Cost of sales, Restoran`, `Goods received`, `Stock count CNT-000004`), and the description of a line carries the document number when one document makes the line.

### 6.3 Reconciliation of the GL with the stock value

`accounting.ControlSources` (document 31 5) learns two controls, computed in SQL from the **stock tables, not from the journals**, so the check is independent:

| Control | GL account | Stock source as of D |
|---|---|---|
| Inventory by category | each `inventory_account_id` | `sum(value_delta)` of `inv_movements` up to D, grouped by the category of the item (a transfer nets to zero over the property); for today it equals `sum(inv_balances.value)` |
| Cost of sales by class (UI review) | `INV_COGS_FOOD`, `INV_COGS_BEVERAGE` | for a period: the sum of `gl_amount` of `POS_SALE`, `POS_SALE_RETURN` and `NEG_SETTLE` by `cost_class`; a **warning** `INVENTORY_COGS_MISMATCH` (not a blocker: a hotel may post by hand to these accounts) |
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

Every state change writes `audit.Writer.Write` in the same transaction: `inventory.item_created`, `inventory.costing_method_set` (old, new; the lock is audited by the first close, as `accounting.deposit_release_locked` is), `inventory.pr_submitted`, `_recalled`, `_approved` (the last level done), `_returned`, `inventory.sr_submitted`, `_recalled`, `_approved`, `_returned`, `_rejected` (by the store), `inventory.approval_step_decided` (level, decision, note, total seen), `inventory.approval_line_edited` (line, field, old, new, level), `inventory.approval_chain_changed` (old and new levels and options), `department.head_changed`, `inventory.po_created`, `_sent`, `_cancelled`, `_closed`, `inventory.receipt_posted`, `_reversed`, `inventory.bill_confirmed` (the matches, the variance, the approver), `inventory.bill_unmatched`, `inventory.return_posted`, `inventory.transfer_posted`, `inventory.issue_posted`, `inventory.waste_posted`, `inventory.count_started`, `_submitted`, `_approved` (the total variance, the approver), `_cancelled`, `inventory.opening_posted`, `inventory.queue_retried`, `inventory.balances_rebuilt`, `pos.recipe_changed` (old and new lines), `inventory.amenity_plan_saved`, `inventory.amenity_run_posted` (rooms, guests, value), `_reversed`, `inventory.staff_meal_posted`, `inventory.entertainment_posted`, `inventory.cost_target_changed`. Approvals add `AuditFields()`. Audit labels (`auditlabel`) get the document numbers. A movement carries `created_by`; a POS worker's movements carry the actor `system` and the sale's bill number in `source_type/source_id`.

### 7.7 Language

Screens, errors and labels are keys of `src/i18n` in **English and Indonesian** (parity test). Seed data in a development database is in Indonesian (`24-seed-data-language.md`). Units and item names are the property's data.

### 7.8 Demo data: `pms-seed inventory`

`go run ./cmd/pms-seed inventory -tenant DEMO -property BALI [-dry-run]`, through the services, idempotent by code, for a **development** database: the setup of the module (average method, tolerances), the units (ml, g, kg, l, botol, karton, pcs, pack, karung), four categories with their accounts, four warehouses (Gudang Utama, Gudang Dapur Restoran, Gudang Bar, Gudang Housekeeping) tied to the outlets of `pms-seed pos`, about 40 items in Indonesian with conversions (sirup 650 ml, beras karung 25 kg, shampoo 30 ml ...), two suppliers (one "Pasar" for market purchases), opening stock, **recipes for the demo products of `pms-seed pos`** (nasi goreng, es teh, kopi ...), one approved PR, a PO, a receipt with its draft, a confirmed bill with a small variance, a requisition and a transfer, an issue to housekeeping, a count in progress. `-dry-run` shows it. It never touches a production database (the same guard as the other seeds).

## 8. Flows with numbers

Conventions: IDR (0 decimals); PPN 11% (creditable input VAT, treatment CREDITABLE) unless said; accounts 1310 food inventory, 1320 beverage inventory, 1330 operating supplies, 1425 input VAT, 2110 accounts payable, 2130 GRNI, 3900 opening balance equity, 5140/5150 housekeeping supplies, 5210/5220 cost of food/beverage sales, 5240 employee benefits (staff meal), 5290 waste, 5295 count variance, 5299 PPV, 6170 travel and entertainment. Warehouses: MAIN (Gudang Utama), RESTO, BAR, HK. Each flow starts from the state it names; the flows are independent. "Journal" is what the day close writes for the stock ledger; the supplier bill is journaled when it is confirmed.

### 8.1 Purchase: PR, PO, receipt, bill with a price variance (average and FIFO)

Item SIRUP (1320): stock unit botol, purchase unit karton = 12 botol, recipe unit ml (1 ml = 1/650 botol). Start: MAIN holds 20 botol, value 960,000 (average 48,000). `price_tolerance_pct` 1%.

| Step | What happens |
|---|---|
| PR-000031 | 5 karton, estimated 600,000 each = 3,000,000. 3,000,000 is not above 5,000,000, so the chain of 5.8 asks level 1 (head of the department) and level 2 (cost control); level 3 does not apply (8.9) |
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

Requisition SR-000008 (approved by its two levels, 8.9): RESTO asks MAIN for 10 kg of BERAS; TRF-000015 answers it (8.4): MAIN 40 kg / 620,000 -> 30 kg / 465,000; no journal. Issue ISS-000004: housekeeping takes 200 pcs of SHAMPOO 30 ml (1330) from HK, average 3,500 = **700,000**; the line's expense is 5150 guest supplies with the department Rooms: Dr 5150 700,000 / Cr 1330 700,000, `INV_ISSUE`. Had the account required a department and none been named, the issue would be refused (`DEPARTMENT_REQUIRED`) when it is posted.

### 8.6 A count with a freeze, while the bar sells

BAR, SIRUP, average, book at the end of 8.2: **28.2 botol / 1,395,900** (49,500 each). 21:00 the count starts: warehouse frozen, `book_qty` = 28.2.

**A sale before the item is counted.** 21:05 ten glasses are sent (1.0 botol): the queue row is `HELD`, created 21:05. 21:20 the counter looks at the shelf and enters **27.5 botol**; the server stamps `counted_at` 21:20. The shelf already lacks the 1.0 botol sold at 21:05, so `held_before_qty` = -1.0, `expected` = 28.2 - 1.0 = 27.2 and **variance = 27.5 - 27.2 = +0.3 botol**. 21:30 the manager approves (kind `INV_COUNT`, amount 0.3 x 49,500 = 14,850; the rule `DISTINCT_ABOVE 10,000` makes it need someone other than the counter). In one transaction: first the held sale is applied, `POS_SALE` 1.0 botol x 49,500 = 49,500 (balance 27.2 / 1,346,400); then `COUNT_GAIN` +0.3 at the last unit cost, **14,850** (balance **27.5 / 1,361,250**); the freeze ends. The book ends where the shelf is: **27.5**, not 26.5. Journal: Dr 5220 / Bar 49,500 / Cr 1320 49,500; Dr 1320 14,850 / Cr 5295 14,850 (`INV_COUNT`).

**The same sale after the item is counted.** Had the glasses been sent at 21:25, after `counted_at` 21:20, `held_before_qty` is 0, `expected` = 28.2, **variance = 27.5 - 28.2 = -0.7** x 49,500 = **-34,650** (`COUNT_LOSS`, Dr 5295 34,650 / Cr 1320 34,650), balance 27.5 / 1,361,250; then the held sale is applied after: 49,500, balance **26.5 / 1,311,750**, which is the shelf after the glasses were made. Both orders give the right book; a rule that applied every held sale after the count would be right only for the second.

**FIFO** (BAR layers 18.2 botol @ 48,000 and 10 @ 50,000 after 8.2, value 1,373,600): the sale before the count takes 1.0 from the oldest layer (48,000), then the gain makes a layer of 0.3 at the last unit cost (50,000, value 15,000). **Below zero:** had BAR held only 0.5 botol when the sale arrived, applying it first would take the balance to -0.5 (a shortfall at the estimate); the count (found 0.0) has `expected` -0.5, variance +0.5, closes the shortfall without a settlement and leaves 0.0.

### 8.7 Return to supplier

GR-000020: 5 karton of SIRUP (60 botol, 3,000,000 at 50,000), unbilled. Two karton (24 botol) arrive damaged. RET-000003 at the receipt line's cost: 24 x 50,000 = **1,200,000**: Dr 2130 1,200,000 / Cr 1320 1,200,000; the draft falls to 36 botol / 1,800,000. **If the receipt had been billed** (3,000,000 + PPN 330,000): the supplier's credit note SCN (document 14) of 1,200,000 + PPN 132,000 = 1,332,000: Dr 2110 1,332,000 / Cr 2130 1,200,000 / Cr 1425 132,000; GRNI = Cr 3,000,000 - Dr 3,000,000 - Dr 1,200,000 + Cr 1,200,000 = **0**. A credit note of 1,260,000 for the same goods would leave 60,000 in GRNI, cleared to inventory/PPV by the link, as in 8.1.

### 8.8 The month: the F&B cost reconciliation (UI review: the figures of mockup 30)

September 2026, IDR, by **class of the item** (what the counts measure) down to the net cost, then against revenue and recipes:

| | Food | Beverage | Total |
|---|---:|---:|---:|
| Opening stock 1 Sep (book after the count of 31 Aug) | 48,600,000 | 22,300,000 | 70,900,000 |
| + Purchases (receipts net of returns and price variance) | 70,150,000 | 9,900,000 | 80,050,000 |
| + Cost transfer in (items of the other class sold in this class) | 320,000 | 1,480,000 | 1,800,000 |
| - Cost transfer out | (1,480,000) | (320,000) | (1,800,000) |
| - Closing stock 30 Sep (approved counts) | (47,974,000) | (16,750,000) | (64,724,000) |
| **= Cost of goods used** | **69,616,000** | **16,610,000** | **86,226,000** |
| - Staff meal (`STAFF_MEAL`) | (3,050,000) | 0 | (3,050,000) |
| - Entertainment and officer check (`ENTERTAINMENT`) | (870,000) | (410,000) | (1,280,000) |
| **= Net cost of sales** | **65,696,000** | **16,200,000** | **81,896,000** |
| Revenue (POS, 12.3) | 211,000,000 | 64,800,000 | 275,800,000 |
| **Actual cost %** | **31.1%** | **25.0%** | **29.7%** |
| Theoretical cost (recipes x sales, `INV_SALE_COST` net of returns) | 59,652,000 | 14,256,000 | 73,908,000 |
| **Theoretical cost %** | **28.3%** | **22.0%** | **26.8%** |
| Difference, actual - theoretical | 6,044,000 | 1,944,000 | 7,988,000 |

The cost transfers are the measured sums of 4.9 item 6 (food to beverage 1,480,000: lime, sugar and fruit used by the bar; beverage to food 320,000: wine and beer used by the kitchen). **What explains the difference of 7,988,000:**

| Cause | Amount | Where it is read |
|---|---:|---|
| Waste recorded | 684,000 | `WASTE` movements of F&B items |
| Count variances (not caused by sales without a recipe) | 4,120,000 | `COUNT_LOSS` less `COUNT_GAIN`: 6,430,000 in all, less the estimate on the next line |
| Sales without a recipe (an estimate) | 2,310,000 | revenue of POS lines of products with no recipe, 8,163,000, times the theoretical cost % of the same outlet and class (28.3%); capped at the count loss, because the goods that were not deducted show up in the count |
| The rest | 874,000 | the other outflows the ledger names but the table does not: F&B items issued to another department, settlements of estimates |

**The check against the general ledger (the footer of mockup 30):** net cost of sales 81,896,000 = 5210 + 5220 (73,908,000) + 5290 waste (684,000) + 5295 count variance (6,430,000) + the other outflows (874,000, on the expense accounts of those issues). The mockup's footer names only 5210 + 5220; with waste, count variance and the price variance on accounts of their own it has to list these too, and the screen does. A void after cooking moves its cost from 5210 / 5220 to 5290; the sum does not change. At 30 Sep the sum of `inv_balances.value` of the food and beverage categories equals the GL balance of 1310 + 1320, and GRNI (2130) equals the "received and not invoiced" list.

### 8.9 Approval: three levels for a PR, two for an SR (UI review, I13)

Chain of 5.8 as made by default. People: Made (sous chef, the requester), Ketut (executive chef, head of "Dapur Teratai", holds `inventory.dept_approve`), Dewi and Agus (hold `inventory.cost_control`), Hadi (financial controller: holds `inventory.cost_control` and `inventory.final_approve`).

**PR-0033, 3 Sep.** Lines: chicken 20 kg x 72,900 = 1,458,000; eggs 300 x 2,500 = 750,000; shallots 25 kg x 45,000 = 1,125,000; rice 5 karung x 435,000 = 2,175,000; oil 4 dus x 201,000 = 804,000; soy sauce 2 dus x 69,000 = 138,000. `est_total` **6,450,000**; department Dapur Teratai.

| Time | What happens | State |
|---|---|---|
| 10:15 | Made submits. Round 1: three steps copied. Level 1 always; level 2 always; level 3 applies (6,450,000 > 5,000,000). Eligible at level 1: Ketut (and the deputy, if any); Made is the requester and cannot. Ketut gets a notice | L1 ACTIVE |
| 10:40 | Ketut approves (`total_seen` 6,450,000) | L1 APPROVED; L2 ACTIVE; Dewi and Agus notified |
| 10:55 | Ketut tries again at level 2 (he does not hold the permission): 403 `APPROVAL_NOT_ELIGIBLE`. Made tries to approve level 2: 403 `APPROVAL_OWN_DOCUMENT` | unchanged |
| 11:20 | Dewi lowers eggs from 300 to 240 (-60 x 2,500 = -150,000; one `inv_approval_edits` row) and approves. `total_seen` **6,300,000**. Level 3 is re-evaluated: still above 5,000,000 | L2 APPROVED; L3 ACTIVE; Hadi notified; Agus's notice closed |
| 11:30 | Dewi tries level 3: not eligible (no `final_approve`). Hadi would be eligible at level 2 as well, but he has not decided any level yet | unchanged |
| 14:05 | Hadi approves level 3 | PR APPROVED, 6,300,000; Made notified |
| 14:20 | Purchasing converts it into 2 POs: UD Segar Jaya (chicken 1,458,000 + eggs 240 x 2,500 = 600,000 + shallots 1,125,000) **3,183,000**; CV Sumber Rasa (rice, oil, soy) **3,117,000**; the sum is 6,300,000, the approved ceiling | CONVERTED |

**A smaller PR stops early.** PR-0034 of 3,880,000: level 3 does not apply (not above 5,000,000), so the PR is APPROVED when Dewi approves level 2 (mockup 34: "PR 3.880.000 berhenti di tingkat 2").

**A return.** Had Hadi returned PR-0033 at 14:05 with "kurangi beras" (note required), level 3 becomes RETURNED, the PR RETURNED, Made is notified. Made lowers the rice to 4 karung (6,300,000 - 435,000 = 5,865,000) and submits again: **round 2, a fresh snapshot, from level 1**. Ketut and Dewi decide again (R1 counts per round); the rows of round 1 stay as history. If the owner had meanwhile changed the threshold of level 3 to 7,000,000, PR-0033 round 2 uses the **new** chain (a resubmission is a new submission) and stops at level 2; a PR submitted before the change and still in round 1 would have kept 5,000,000.

**The requester is the head.** Ketut writes a PR himself: with O1 on, level 1 is SKIPPED (`REQUESTER_IS_HEAD`) and Dewi is the first approver; with O1 off, the deputy of Dapur Teratai has to approve level 1, and a department with no deputy refuses the submission (`APPROVAL_CHAIN_UNSATISFIABLE`).

**SR-0017** (Dapur Teratai asks MAIN for 20 kg of rice, value 20 x 15,500 = 310,000): level 1 Ketut (always), level 2 "Persetujuan akhir SR" (always, holders of `inventory.sr_final_approve`). Until level 2 approves, a transfer that names SR-0017 answers 409 `SR_NOT_APPROVED` (mockup 22: "1/2 · Persetujuan akhir"). Once APPROVED, the store transfers 20 kg (8.5) and the SR becomes FULFILLED.

### 8.10 Cost across classes: lime in the bar, wine in the kitchen (UI review, I14)

Classes: warehouse BAR is BEVERAGE, RESTO (kitchen) is FOOD, MAIN is OTHER. POS categories: "Cocktail" BEVERAGE, "Makanan utama" FOOD, "Kopi" BEVERAGE. Items: LIME (category food, 1310), ANGGUR MASAK (cooking wine, category beverage, 1320), KOPI (beans, beverage, 1320). Average costs: LIME 25,000 per kg in BAR; ANGGUR 160,000 per bottle in RESTO; KOPI 150,000 per kg in RESTO. A requisition moved the lime from MAIN to BAR earlier: **no journal** (4.5).

12 Sep:

| Sale | Recipe | Cost | `item_class` | `cost_class` (sale class) | Journal at the close |
|---|---|---:|---|---|---|
| 20 "Mojito" at the bar (Cocktail) | LIME 0.05 kg per glass = 1.0 kg | 1.0 x 25,000 = **25,000** | FOOD | BEVERAGE | Dr 5220 / Bar 25,000, Cr 1310 25,000 |
| 12 "Ayam saus anggur" in the restaurant (Makanan utama) | ANGGUR 0.05 bottle per portion = 0.6 bottle | 0.6 x 160,000 = **96,000** | BEVERAGE | FOOD | Dr 5210 / Restoran 96,000, Cr 1320 96,000 |
| 10 "Kopi tubruk" in the restaurant (Kopi), sold **from the food warehouse** | KOPI 0.02 kg per cup = 0.2 kg | 0.2 x 150,000 = **30,000** | BEVERAGE | BEVERAGE (the category, not the warehouse) | Dr 5220 / Restoran 30,000, Cr 1320 30,000 |

Each posting is a single entry: the cost goes once to the account of the class that was sold, and the inventory account falls by the item's own category, so the control of 6.3 still holds (1310 down 25,000; 1320 down 126,000). The daily cost of mockup 31 reads these rows: beverage cost of the day carries the lime and the coffee, food cost the wine. **Measured, not posted:** cost transfer food to beverage 25,000 (the lime), beverage to food 96,000 (the wine); the coffee is not a transfer (item and sale are both beverage). Continued over September, such rows add up to the 1,480,000 and 320,000 of 8.8.

*What the alternative would have done* (cost by the item's category): the lime would have debited 5210 and the wine 5220: that day food cost +25,000 against beverage revenue, beverage cost +96,000 against food revenue, and the correction would exist only as a month-end journal Dr 5220 / Cr 5210 of 1,480,000 and the reverse 320,000, invisible to every daily report.

### 8.11 Amenities of one night: 78 rooms, 142 guests (UI review, I16; the figures of mockups 32 and 33)

Plan in force (version 4, effective 1 Aug): warehouse Gudang Housekeeping, department Rooms, account 5150. Every room type: soap 1 per room, shampoo 1 per room, toothbrush 1 per guest, toothpaste 1 per guest, mineral water 2 per room, tea 2 per room, coffee 2 per room. Suite extras: slippers 1 per guest, body lotion 1 per room. Cost for one room of two guests: 2,960 + 3,250 + 2 x 2,100 + 2 x 1,800 + 2 x 2,500 + 2 x 835 + 2 x 1,500 = **23,680**; a Suite adds 2 x 6,500 + 4,200 = 17,200, **40,880**.

**3 Sep, at the close.** Occupied rooms of the night (the nights of the stays, 5.9): **78**, of which 2 complimentary and 1 house use; vacant and out-of-order rooms are not counted. Standard 40 rooms / 68 guests, Deluxe 28 / 52, Suite 6 / 12, Family 4 / 10: 78 rooms, **142 guests** (adults plus children).

| Item | Rule | Quantity | Stock before | Unit cost | Value |
|---|---|---:|---:|---:|---:|
| Soap 25 g | 1 x 78 rooms | 78 | 42 | 2,960 | 230,880 |
| Shampoo 30 ml | 1 x 78 rooms | 78 | 310 | 3,250 | 253,500 |
| Toothbrush | 1 x 142 guests | 142 | 620 | 2,100 | 298,200 |
| Toothpaste 10 g | 1 x 142 guests | 142 | 580 | 1,800 | 255,600 |
| Mineral water 600 ml | 2 x 78 rooms | 156 | 960 | 2,500 | 390,000 |
| Black tea | 2 x 78 rooms | 156 | 1,200 | 835 | 130,260 |
| Coffee sachet | 2 x 78 rooms | 156 | 880 | 1,500 | 234,000 |
| Hotel slippers | Suite: 1 x 12 guests | 12 | 96 | 6,500 | 78,000 |
| Body lotion 30 ml | Suite: 1 x 6 rooms | 6 | 54 | 4,200 | 25,200 |
| **Total** | | | | | **1,895,640** |

24,303 per occupied room. **Soap is short:** 42 on the shelf for 78. The close is **not** refused. `AllowShortfall` costs 42 x 2,960 = 124,320 from the shelf and 36 x 2,960 = 106,560 at the estimate (the last unit cost); the balance becomes **-36 pcs / -106,560**; a shortfall row keeps account 5150 and the department Rooms; the night-audit preview had said `AMENITY_STOCK_SHORT` (a warning). Journal at the close: **Dr 5150 Perlengkapan tamu, Rooms 1,895,640 / Cr 1330 1,895,640**.

*Next day*, a receipt of 500 soaps at 3,000 (1,500,000): it settles the 36 first, `NEG_SETTLE` = -36 x (3,000 - 2,960) = **-1,440**, Dr 5150 Rooms 1,440 / Cr 1330 1,440; the soap balance is **464 pcs / 1,392,000** (3,000 each; the plain sum 1,393,440 less the settlement), as in 8.4. **Idempotent:** a second attempt of the same day finds `AMN-20260903` and does nothing. **Correction:** on 4 Sep a reversing issue returns all nine lines (Dr 1330 / Cr 5150 1,895,640, dated 4 Sep); a wrong count of 3 rooms is better corrected by a manual issue, since a reversal is whole.

**Variant: the close falls in a count (D24).** Gudang Housekeeping has been frozen since 3 Sep 21:00 by count CNT-000012 and the property lets the close pass (`count_blocks_night_audit` off). At 03:00 on 4 Sep the close of 3 Sep runs: the preview says `AMENITY_HELD`, **the close is not blocked**, `AMN-20260903` is written (78 rooms, 142 guests, `created_at` 03:00) with its nine lines `HELD`: no movement, no journal. Book of soap at the freeze: 42. Mineral water is outside the scope of the count.

- *Soap counted at 22:10 (before the close).* The count is approved on 4 Sep 08:30. `counted_at` 22:10 is **before** the document's `created_at` 03:00, so the line is on the *after* side of the count. Found 40, `expected` 42 (no held quantity before 22:10), variance **-2** x 2,960 = -5,920 (Dr 5295 / Cr 1320 5,920), balance 40. Then the held line is applied: 78 needed, 40 on the shelf: 40 x 2,960 = 118,400 from stock and **38** at the estimate, balance **-38 pcs / -112,480**, a shortfall row with 5150 / Rooms. (In the main case of the close without a count it was -36: here the count found two fewer.)
- *Soap counted at 04:00 (after the close).* Now `counted_at` 04:00 is after `created_at` 03:00: the line is on the *before* side and lowers what the count expects. `held_before_qty` = -78, `expected` = 42 - 78 = -36, found 40, variance **+76** (a gain valued at the last unit cost, 2,960, 224,960, which closes the shortfall without a settlement, 4.4 item 4). The line is applied first (balance -36 / -106,560) and the count then sets the balance to **40**. The 78 soaps stay in the cost of the night at the estimate; the net effect on inventory is the counted quantity at cost.
- *The other eight lines* are applied after the count (no `counted_at` for an item not counted). The journal of the whole document is dated **4 Sep** (the open day): Dr 5150 Rooms / Cr 1330 by the costs of that moment, `source_date` 2026-09-03 on every movement. The report "Pemakaian" shows both dates; the daily cost (12.2) shows the amenities on 4 Sep.
- *A count that is cancelled on 6 Sep:* the same, dated 6 Sep; the closes of 3, 4 and 5 Sep each hold their own document, applied in date order (`AMN-20260903`, `AMN-20260904`, `AMN-20260905`).

## 9. Permissions and approvals

### 9.1 Permissions (new; granted to **no role** by the migration; the tenant administrator holds all)

| Permission | Who | Allows |
|---|---|---|
| `inventory.view` | any | read stock, documents, reports |
| `inventory.manage` | store manager | units, categories (with accounts), items, warehouses (with their cost class), levels, settings, the amenities plan and the reversal of an amenities run, cost targets, retry or cancel failed queue rows, rebuild balances |
| `inventory.request` | any department | PR and SR (create, submit, recall) |
| `inventory.purchase` | purchasing | PO |
| `inventory.receive` | storekeeper | receipt, return to supplier, direct receipt |
| `inventory.bill` | accountant | confirm and unmatch bill drafts (also needs `payables.post`) |
| `inventory.move` | storekeeper, outlet | transfer, issue to a department, waste, staff meal, entertainment |
| `inventory.count` | storekeeper, outlet | start, enter, submit a count |
| `inventory.opening` | store manager | opening stock |
| `inventory.approve` | manager | **narrowed (UI review):** the approver of `INV_BILL_VARIANCE` and `INV_COUNT` only |
| `inventory.dept_approve` (UI review) | department head, deputy | the approval of a level whose approver is "the head of the requesting department" (also needs the assignment in Departments, 3.3) |
| `inventory.cost_control` (UI review) | cost control | a PR level that names it (level 2 by default) |
| `inventory.final_approve` (UI review) | financial controller, GM | a PR level that names it (level 3 by default) |
| `inventory.sr_final_approve` (UI review) | store manager, financial controller | an SR level that names it (level 2 by default) |
| `property.manage` | (existing) | edit the approval chains, as it edits the approval rules of document 32 |
| `accounting.manage` | (existing) | set the head and the deputy of a department (document 12) |
| `pos.manage` | (existing) | edit recipes and the `fb_class` of POS categories; read items for the recipe editor |

Reads follow document 33: the list of items, units, warehouses is readable by `inventory.view`, `inventory.manage`, or the permission of a screen that uses it (`pos.manage` for the recipe editor, `inventory.request` for the request form). The inbox and the bell are the user's own and need only one of the approval permissions or the assignment as a head.

### 9.2 Approval kinds (document 32 2)

| Kind (label) | Use cases | Amount | Mode | Approver's permission |
|---|---|---|---|---|
| `INV_BILL_VARIANCE` (price difference on a bill) | `ConfirmBill` | sum of `abs(variance)` of the request | credentials, as a correction | `inventory.approve` |
| `INV_COUNT` (stock count result) | `ApproveCount` | `abs(total variance value)` | decision; `Subject` = who submitted | `inventory.approve` |

**`INV_PR` is not a kind (UI review).** The approval of a PR, and of an SR, is the chain of 5.8, with its own engine; the screen of the rules of document 32 does not list it. The two kinds above take `FREE`, `DISTINCT` and `DISTINCT_ABOVE` per property. The structural test of document 32 4 is extended (each use case names its kind and amount and writes `AuditFields()`), and `ApprovalRequest` gains `Decision bool` and `Subject int64`: with `Decision`, the caller is the approver, needs the permission, and the rule compares the approver with `Subject`; a tenant administrator who is the subject is accepted and marked as for any correction. The approval of a decision is verified before the transaction, as in document 32. An existing kind is still used where it applies: unmatching a bill is `PAYABLES` (void of a bill).

## 10. API

`{P}` = `/api/v1/properties/{propertyId}`. Every POST that creates or posts takes a `client_uuid` or `Idempotency-Key` as 7.3 says. Mutations are in OpenAPI first; the TS types are generated. Errors are `*apperr.Error` with the stable codes below; new constraint names are mapped.

| Area | Routes |
|---|---|
| Start and settings | `GET/PUT {P}/inventory/settings` (the method is accepted only before the lock: 409 `COSTING_METHOD_LOCKED`); `GET {P}/inventory/setup-check` |
| Masters | `GET/POST/PATCH {P}/inventory/units`, `categories`, `items`, `warehouses`; `PUT items/{id}/units`; `PUT warehouses/{id}/levels`; `POST items/import` (CSV, all or nothing, rows listed); `GET items?q=&category=&warehouse=` |
| Opening | `POST {P}/inventory/openings` (`INVENTORY_GL_NOT_EMPTY`, `OPENING_EXISTS`) |
| Purchasing | `GET/POST requests`, `POST requests/{id}/submit|recall|approve|return|cancel` (`approve` takes `{note, edits: [{line_no, qty_purchase?, est_unit_price?}]}`, 5.8); `GET/POST purchase-orders` (from a request or direct), `POST purchase-orders/{id}/send|cancel|close`; `GET/POST receipts`, `POST receipts/{id}/reverse`; `GET bill-drafts`, `POST bill-drafts/confirm`, `POST bill-drafts/{id}/discard`, `POST bills/{id}/unmatch`; `GET/POST returns`, `POST returns/{id}/link-credit-note` |
| Movements | `GET/POST requisitions` (+ `submit|recall|approve|return|cancel`, and `reject` by the store), `GET/POST transfers` (a transfer that names an SR: 409 `SR_NOT_APPROVED` unless APPROVED or PARTIAL), `GET/POST issues` (`purpose USE|WASTE|STAFF_MEAL|ENTERTAINMENT`), `POST transfers|issues/{id}/reverse` |
| Counts | `GET/POST counts` (start), `PUT counts/{id}/lines`, `POST counts/{id}/submit|approve|cancel` |
| Stock and reports | `GET stock?warehouse=&item=&category=`, `GET stock-card?warehouse&item&from&to`, `GET reports/valuation?warehouse&as_of`, `GET reports/low-stock`, `GET reports/negative-stock`, `GET reports/received-not-invoiced`, `GET reports/fb-cost?from&to&outlet`, `GET reports/waste`, `GET reports/count-variance`, `GET reports/pos-without-deduction` (CSV where tables) |
| POS | `GET/PUT {P}/pos/products/{id}/recipe` (`pos.manage`; PUT replaces the lines; 422 `RECIPE_UNIT_INVALID`), `GET {P}/pos/products/{id}/recipe-cost?outlet=`; `PATCH pos/outlets/{id}` gains `warehouse_id` |
| Queue | `GET {P}/inventory/consumption-queue?status=`, `POST .../{id}/retry|cancel` (`inventory.manage`) |
| Admin | `POST {P}/inventory/rebuild-balances` (`inventory.manage`) |
| Approvals (UI review) | `GET/PUT {P}/inventory/approval-chains/{PR|SR}` (levels and options; `property.manage`; 422 `APPROVAL_CHAIN_INVALID`), `GET {P}/inventory/approvals/inbox` ("Perlu saya setujui", with the stage of each document), `GET {P}/inventory/notices`, `POST {P}/inventory/notices/{id}/seen`; the steps and edits are read with the document (`GET requests/{id}`, `GET requisitions/{id}`) |
| Department heads (UI review) | `PATCH {P}/departments/{id}` gains `head_user_id`, `deputy_user_id` (`accounting.manage`; 422 `DEPARTMENT_HEAD_NOT_ASSIGNED`) |
| Amenities (UI review) | `GET/PUT {P}/inventory/amenities/plan` (the PUT makes a new version; `inventory.manage`), `GET .../amenities/preview` (tonight's rooms, guests, value, short items), `GET .../amenities/runs?from&to`, `GET .../amenities/runs/{date}` (lines, rooms, per type), `POST .../amenities/runs/{date}/reverse` |
| Cost reports (UI review) | `GET {P}/inventory/reports/fb-reconciliation?from&to`, `GET .../reports/daily-cost?from&to&outlet`, `GET/PUT .../inventory/cost-targets` (`inventory.manage`) |

Error codes: 409 `NEGATIVE_STOCK_NOT_ALLOWED`, `WAREHOUSE_FROZEN`, `COUNT_IN_PROGRESS`, `COSTING_METHOD_LOCKED`, `RECEIPT_EXCEEDS_ORDER`, `RECEIPT_NOT_REVERSIBLE`, `BILL_EXCEEDS_RECEIPT`, `BILL_HAS_STOCK_MATCH`, `PO_NOT_ORDERABLE`, `PR_NOT_APPROVED`, `DIRECT_RECEIPT_NOT_ALLOWED`, `DIRECT_RECEIPT_LIMIT`, `INVENTORY_GL_NOT_EMPTY`, `WAREHOUSE_HAS_STOCK`, `ITEM_HAS_STOCK`, `INVENTORY_QUEUE_FAILED`, `INVENTORY_COUNT_OPEN`, `SR_NOT_APPROVED`, `APPROVAL_NOT_YOUR_TURN`, `APPROVAL_SAME_PERSON`, `APPROVAL_CHAIN_UNSATISFIABLE`, `APPROVAL_NOT_IN_PROGRESS`, `AMENITY_RUN_NOT_REVERSIBLE`; 403 `APPROVAL_OWN_DOCUMENT`, `APPROVAL_NOT_ELIGIBLE`; 422 `APPROVAL_EDIT_NOT_ALLOWED`, `APPROVAL_CHAIN_INVALID`, `AMENITY_PLAN_INVALID`, `DEPARTMENT_HEAD_NOT_ASSIGNED`, `RECIPE_UNIT_INVALID`, `UNIT_CONVERSION_MISSING`, `CATEGORY_ACCOUNT_INVALID`, `DEPARTMENT_REQUIRED` (existing); 403 `MODULE_NOT_IN_PLAN`, `PERMISSION_DENIED`, `APPROVAL_*` (existing); 404 for another tenant's or an unassigned property (existing).

## 11. Screens (en and id; desk 1280-1440, receiving and counting at 390 px)

**The pattern (I18, UI review).** A **list page** that fills the screen (table, filter chips, the button "Baru") and from it a **document page** that fills the screen (the form on the left, the summary on the right, a link back); **no list column beside the document**. The one exception is **Items and stock**, which uses a side panel that can be closed. The Inventory menu (mockups): Barang & stok, Pembelian, Draf tagihan, Permintaan & transfer, Pemakaian & waste, Opname, Laporan; and in Setup: Gudang, Kategori & akun, Satuan, Harga pokok, Amenities otomatis, Alur persetujuan. The mockups are in `docs/design/` (numbers 17 to 34); every text goes through `src/i18n` (en and id).

| Screen | For | Content | Mockup |
|---|---|---|---|
| Inventory dashboard | `inventory.view` | items below minimum, documents waiting for my approval, POs not received, received and not invoiced (aged), counts open, shortfalls, failed queue rows, chains that are stuck | none yet |
| Items and stock | `inventory.manage`, `inventory.view` | list with search, per-warehouse stock and value, markers for below minimum and below zero; the side panel of an item (units, cost per warehouse, short stock card); category accounts with the chart check, CSV import | 17 |
| Purchasing list | `inventory.request`, `inventory.purchase` | tabs PR, PO, receipts; the chip "Perlu saya setujui"; a column with the stage of the approval ("2/3 Cost control") | 18 |
| PR document | `inventory.request`, the approvers | lines with current stock, estimated price and proposed supplier; the preview of POs per supplier; the panel of the chain with the steps and who decided; for the approver of the active level: edit lines, note, **Setujui / Tolak** | 19 |
| Receiving | `inventory.receive` | receipt against a PO (price from the PO, not editable, partial receipt, "Receive and bill"), direct receipt, a phone layout for the dock | 20 |
| Bill drafts | `inventory.bill` | drafts by supplier, the invoice entry, the three columns **receipt / PO / invoice**, the split of the variance (stock value / purchase price variance), the approval above tolerance, non-stock lines | 21 |
| Returns | `inventory.receive` | return from a receipt line, the credit-note link | none yet |
| SR and transfers list | `inventory.request`, `inventory.move` | tabs SR and Transfer; chips "Siap dikirim", "Perlu saya setujui", "Sebagian", "Selesai", "Ditolak"; columns "Persetujuan" (2/2) and "Dijawab dengan" | 22 |
| Transfer | `inventory.move` | source stock, partial shipment, the source cannot go below zero; answers an APPROVED SR | 23 |
| Issues and waste list | `inventory.view` | issues, waste, staff meals, entertainment, the automatic amenities documents; cost per department this month | 24 |
| Issue document | `inventory.move` | department, account per line, journal preview; purposes use, waste, staff meal (covers), entertainment (beneficiary, covers) | 25 |
| Amenities of one night | `inventory.view`, `inventory.manage` | the document made by the close: rooms (with comp and house use), guests, cost per room, the item with a shortage, per room type, list of rooms, journal, "Buat dokumen pembalik" | 33 |
| Stock count | `inventory.count`, `inventory.approve` | on a phone: blind count, the time each item was counted, the freeze banner, POS goes on; on the desk: start with scope, submit, the approval screen | 26 |
| POS product master, recipe tab | `pos.manage` | ingredients per portion, a variant replaces, a modifier adds, food cost %, margin; hidden unless both modules are in the plan | 27 |
| Costing (Harga pokok) | `inventory.manage` | the method with the warning that it locks, average against FIFO on the same data, negative stock and its correction, the journal | 28 |
| F&B cost per outlet | `inventory.view` | theory (recipes) against actual (counts), tied to the general ledger | 29 |
| F&B cost reconciliation | `inventory.view` | 12.1: by class, with cost transfers, staff meal, entertainment, net cost, cost % against theory, the explanation of the difference | 30 |
| Daily cost | `inventory.view` | 12.2 (D31): all costs by day and MTD (F&B cost and %, waste, staff meal, entertainment, amenities with occupied rooms and cost per room, engineering, stationery / other, count and price variances, total); three views: All costs (default), F&B only (with the target line chart), By department | 31 |
| Amenities plan | `inventory.manage` | on or off, warehouse, department, account, the base package (per room or per guest), extras by room type, cost per room, "applies from the next close" | 32 |
| Approval chains | `property.manage` | PR and SR: levels (name, who, when), add or remove a level, the four rules, how it works | 34 |
| Departments (document 12) | `accounting.manage` | head and deputy per department | none yet |
| Warehouses and levels | `inventory.manage` | warehouses, their outlet or department, **cost class**, minimum levels | none yet |
| Setup check, failed queue | `inventory.manage` | settings, tolerances, the setup check (including the new findings of 3.3, 4.9, 5.9), the failed queue | none yet |
| Night audit | existing | the new findings (5.7), including the preview of tonight's amenities | none yet |

**Where the mockups and this text differ** (mockups 30, 31, 33 and 34 were aligned in #43; the rest to be aligned when the screens are built):

- *Syrup:* the figures of the mockups and of section 8 come from different examples (for instance the syrup at the bar: 49,400 average and 1,393,080 in the mockups, 49,500 and 1,395,900 in 8.2); each is consistent in its own place; the mockups are not changed.

## 12. Reports (I10, I17)

All have CSV; the tables with a period also take the outlet or department. The three cost reports of the review (12.1 to 12.4) read the ledger and the table `inv_fb_cost_days` (3.6).

### 12.1 F&B cost reconciliation (mockup 30, I17)

For a month (or any period) and for FOOD, BEVERAGE and the total. The figures of September are worked in 8.8.

| Line | Where it comes from |
|---|---|
| Opening stock | value at the end of the day before the period of the items whose `item_class` is the class: `inv_balance_days` |
| + Purchases | `RECEIPT`, `RECEIPT_REVERSAL`, `SUPPLIER_RETURN` and the on-hand part of `COST_ADJUST` of the period, by `item_class` (receipts net of returns and price variance) |
| + Cost transfer in, - out | measured: the sale movements whose `cost_class` is not the `item_class` (4.9 item 6) |
| - Closing stock | value at the end of the period by `item_class`; marked "from approved counts of <date>" when every F&B warehouse has an approved count dated the last day, otherwise "estimated from the book stock" (D21) |
| = Cost of goods used | the sum of the above |
| - Staff meal, - Entertainment | movements of kind `STAFF_MEAL`, `ENTERTAINMENT` by `item_class` |
| = Net cost of sales | |
| Revenue | `inv_fb_cost_days.revenue_net` of the class (12.3) |
| Actual cost % | net cost / revenue |
| Theoretical cost and % | `POS_SALE`, `POS_SALE_RETURN`, `NEG_SETTLE` by `cost_class` (recipes x quantities sold, at the cost of the day) / revenue |
| Difference, and what explains it | waste (`WASTE`); count variances (`COUNT_LOSS` less `COUNT_GAIN`, less the next line); sales without a recipe (**an estimate**: revenue of lines whose product has no recipe x the theoretical cost % of the same outlet and class, capped at the count loss, because the goods that were not deducted appear in the count); the rest (the other outflows named in the ledger) |
| The check | net cost = 5210 + 5220 + 5290 + 5295 + the other outflows, each from the general ledger; shown as "cocok" or with the difference |

The identity of the first lines is exact in ledger terms, because the counts post their variances: the book at the end of the period **is** the counted stock. The table is printable and every line drills down to the movements.

### 12.2 Daily cost of all costs (mockup 31, I17, D31)

**What (D31, decided 2026-10-16).** The daily cost covers **every cost of stock**, not only food and beverage, per business day and **month to date**. Columns, per day:

| Column | Source |
|---|---|
| F&B cost of sales and **F&B %** (cost of sales / F&B revenue), with the target of the outlet | `SALE_COST_FOOD` and `SALE_COST_BEVERAGE` over `inv_fb_cost_days.revenue_net` (12.3) |
| Waste | `WASTE` (void after cooking and waste issues) |
| Staff meal and entertainment | `STAFF_MEAL`, `ENTERTAINMENT` |
| Room amenities, the **occupied rooms** and the **cost per occupied room** | `AMENITY`; rooms from `inv_amenity_runs.rooms`; cost / rooms |
| Engineering and stationery / other | `ENGINEERING`, `OTHER_USE` |
| Count variance and purchase price variance | `COUNT_VARIANCE` on the day the count is approved, `PRICE_VARIANCE` on the day the bill is confirmed |
| **Total cost** | the sum of the kinds above (signed) |

**Three views:** **All costs** (the default), **F&B only** (food and beverage separately, cost %, MTD %, the points above or below the target, the days above target marked, the chart with the target line) and **By department** (one column per department of the movements; the cost of sales belongs to the department of the outlet). All three read the same rows and add up to the same total. A click on a day shows the documents and, for F&B, the outlets and menu items (quantity sold, recipe cost, price).

**Revenue for the F&B %: unchanged.** Net of discount, before service charge and tax, a comp without revenue, POS only (12.3). Staff meal and entertainment are *shown* in the daily cost but stay **outside the net cost of sales** of the reconciliation (12.1, I15): the F&B % uses cost of sales only, so it agrees with the target and with 12.1.

**Source.** `inv_fb_cost_days` and `inv_cost_days` for every closed day (written at the close, 3.6); the open day is computed from the ledger and the POS lines and marked "hari berjalan". A held amenities document (5.9, D24) is on the day it is applied. **Targets:** `inv_cost_targets`, a percentage per outlet and class (F&B only; the other columns have no target), the property's row as the default; "all outlets" is the revenue-weighted mean of the targets of the outlets. In mockup 31, MTD food cost 28.5% against a target of 30%: 1.5 points below.

### 12.3 The split of POS revenue into food and beverage (rule, I17)

- A POS line counts in the class of its **sale class**, the one resolved for the cost (4.9): the `fb_class` of the category of the product, else the class of the outlet's warehouse. So revenue and cost of one line meet in one class.
- Revenue = the net amount of the line after discount, **before service charge and tax**; voided lines are out; a **comp** line carries no revenue (its cost stays in the cost of sales, 5.6, and the report shows "of which comps" as a memo line).
- A line of class OTHER (shop, a service) is not in these reports. A set menu or a combo is one product, so one class: the class of its category.
- Only POS revenue is read. Banquet or room-service revenue posted straight to a folio is outside these reports until POS covers it.
- The class does not come from the revenue account of the charge code. When the two disagree the setup check shows `FB_CLASS_REVENUE_MISMATCH` (4.9), so the general-ledger revenue by account and this split can be reconciled by the accountant.

### 12.4 F&B cost per outlet (mockup 29)

Per outlet and class: theory (recipes x sales of the outlet) against actual consumption of the outlet's warehouses (opening + receipts and transfers in - transfers out - closing, from the counts of the warehouse), the difference and its causes, and the general-ledger accounts of the outlet's department beside it.

### 12.5 The other reports

- **Stock card (kartu stok)**: per item and warehouse, each movement with date, document, quantity in and out, running quantity, value and unit cost (average) or the layers touched (FIFO); opening and closing balance from the day snapshot.
- **Valuation per warehouse** as of a date (qty, value, average), totals by category, **against the GL account** of the category (6.3).
- **Recipe cost** per product in the POS master (5.6) and a list of products with cost, price and margin, lowest margin first.
- **Low stock**: items with `qty < min_qty` per warehouse, with the reorder quantity; also the dashboard; a PR can be started from it.
- Others: waste by reason, count variances, stock sold below zero (with the source: POS or amenities), received and not invoiced (the GRNI detail), POS lines with a recipe and no deduction, price variances by supplier, **transfers without an SR**, **staff meals and entertainment** by department and period (cost per meal), the **amenities by month** (rooms, guests, cost per occupied room), documents waiting in the approval chain and their age.

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
- `INV_BILL_VARIANCE` under `FREE`, `DISTINCT`, `DISTINCT_ABOVE` at, below and above the limit, the administrator exception, the structural test of document 32 with the two new kinds (`INV_BILL_VARIANCE`, `INV_COUNT`); the decision mode with a `Subject` (used by the count).
- Three-way: exact; price up and down; quantity over and under; no PO; the variance within and above tolerance (approval asked or not); the inventory/PPV split with the stock on hand, partly moved and fully sold; VAT treatments CREDITABLE, EXPENSE (cost basis includes the VAT), DEFERRED; GRNI is zero after each case, for both methods.
- Control accounts: a hand-entered bill line and a manual journal on 13xx / GRNI refused after the first movement and accepted before; `INVENTORY_GL_NOT_EMPTY`.
- Receipt reversal (same day, unbilled, on hand) and its refusals; return to supplier before and after the bill; the credit-note link; `Unmatch` and `BILL_HAS_STOCK_MATCH` on a plain void.
- Idempotent replay of every posting; no second approval on a replay; the draft made once.
- **The approval chain (UI review, I13)**, with the real database:
  - *Order and conditions:* level 2 cannot decide before level 1 (`APPROVAL_NOT_YOUR_TURN`); the conditions are evaluated when a level is reached: 6,450,000 goes to level 3, 3,880,000 stops at level 2; an edit that lowers the total under the threshold drops level 3, an edit that raises it over the threshold adds it; a level already passed is never reopened (8.9).
  - *One person, one level:* the same user cannot decide two levels of a round (`APPROVAL_SAME_PERSON`), **and the unique index refuses a second APPROVED step of the same user in a direct insert**; a user who holds two permissions decides only one; the rule counts per round (after a return the same people can decide again).
  - *The requester:* never approves their own document at any level, the tenant administrator included (`APPROVAL_OWN_DOCUMENT`); O1 on skips level 1 when the requester is the head or the deputy (`REQUESTER_IS_HEAD`), O1 off keeps it for the other of the two, a department with no other eligible person refuses the submission (`APPROVAL_CHAIN_UNSATISFIABLE`).
  - *Races (barrier):* two eligible approvers decide the same active step at once (exactly one succeeds, the other `APPROVAL_NOT_YOUR_TURN`); an approver and the requester's recall at once; an approver and a change of the chain at once (the snapshot decides).
  - *Edits:* recorded per line and field (old, new, level, who), the total recomputed, later levels see the new total; O2 off refuses (`APPROVAL_EDIT_NOT_ALLOWED`); quantity zero or a negative price refused; the audit entry names the edits.
  - *Return and resubmission:* a note is required; every level can return; round 2 starts at level 1 with a fresh snapshot; the steps of round 1 are kept; the PR cannot be converted to a PO before APPROVED and a PO line cannot exceed the approved (edited) quantity.
  - *Snapshot:* a change of the chain (a level added, a threshold, an approver kind) while a PR is in flight does not touch its steps; a PR submitted afterwards, and a resubmission, use the new chain (8.9).
  - *Who is the head:* head, deputy, a sub-department that takes its parent's, none (refused), a head who became inactive (`DEPARTMENT_HEAD_INACTIVE`), a head changed while a PR waits (the new head decides the active step), the permission `inventory.dept_approve` required besides the assignment.
  - *Notices and inbox:* notices are written in the transaction of the step (a rollback leaves none), to every eligible user except the requester, closed for the others when one decides; the inbox lists exactly the documents the user can decide now; the event on the stream carries only ids.
  - *Replay:* the same `Idempotency-Key` returns the first result and decides nothing twice. *Permissions and tenant:* 403/404 matrix for the approval routes, the chain routes (`property.manage`) and the head columns (`accounting.manage`); another tenant's user cannot be a head (composite FK).
  - *Structural:* `chainpath_guard_test.go` (only the engine writes the states SUBMITTED, APPROVED, RETURNED; audit and notices on every path).
- Web: the receipt, the three-column bill screen, the PR document with its chain panel and the approver's box (edit, note, Setujui / Tolak), the inbox and the bell, the chain settings (mockups 18, 19, 20, 21, 34), the head pickers in Departments.

**PR 3 (movements, requisition, count)**
- Transfer with value carried (both methods), in opposite directions at once (no deadlock), the source refusal; **SR (UI review): a transfer that names an SR is refused until its two levels are done (`SR_NOT_APPROVED`) and accepted after; an SR with a threshold on its value; the store rejecting an approved SR; a free transfer needs no SR and is listed by the report; two transfers fill an SR;** issue to a department with the department rule (`DEPARTMENT_REQUIRED` at the issue, never at the close); waste with reasons; reversals the same day.
- **Count and freeze**: start freezes (`WAREHOUSE_FROZEN` for every manual mutation, the queue held), a mutation in flight finishes before the freeze, **a sale before and a sale after an item is counted (a race with a barrier: the sale and the entry of the count at the same time; the side of `counted_at` decides, the sale is deducted exactly once, and the book equals the counted quantity minus only the sales after `counted_at`)**, `counted_at` set by the server and moved when the quantity is changed, a queue row with components on two items split by their own `counted_at`, the held sale that takes the balance below zero before the count (shortfall closed by the count), both methods, two counts of one warehouse refused, blind sheet, gain and loss valuation for both methods, a count closes shortfalls, approval under the rules (`INV_COUNT`), cancel releases the freeze and applies the held rows, held rows applied in order, the open-count blocker and the warning setting of the night audit.
- **Staff meal and entertainment (UI review, I15):** the department is required, `covers` and `beneficiary` as stated; the accounts of the keys and the department of the document; the movement kinds; the item class decides the class column; both leave the net cost of sales in the reconciliation and neither is a waste; reversal on the day.
- **Automatic amenities (UI review, I16):**
  - *Idempotent per day:* the step run twice in one close and by two concurrent transactions makes one document, one movement per line (unique key) and one run (`UNIQUE (property_id, business_date)`); a rolled-back close leaves no document.
  - *A shortage does not stop the close:* the soap of 8.11 (42 on the shelf, 78 needed) closes the day, the balance is -36 / -106,560, a shortfall row keeps account 5150 and the department Rooms; a later receipt settles it (`NEG_SETTLE` -1,440) and a count closes it; both costing methods; the preview warns (`AMENITY_STOCK_SHORT`) and never blocks. **A manual issue, a staff meal and a transfer still refuse negative stock.**
  - *Which rooms:* complimentary and house use counted, vacant and out-of-order not, a guest who moved room that day counted once under the room type of the night, the arrival night counted and the departure day not, a cancelled stay not; guests = adults + children; the rooms of the run equal the rooms with a room night of the day; per room, per guest, per room type extras (8.11: 78 rooms, 142 guests, 1,895,640 exactly); the journal Dr 5150 Rooms / Cr 1330.
  - *The plan:* versions are immutable; a change saved on the day D applies to the close of D, a run keeps its version; switching off makes no document; validation of warehouse, account, department rule and units (`AMENITY_PLAN_INVALID`); a plan that cannot run is a blocker, **a warehouse frozen by a count is not (D24)**.
  - *A close inside a count (D24):* the close runs and is not blocked; the document, the run and the lines are written with the lines `HELD`, no movement, no journal, and the warning `AMENITY_HELD`; at approval and at cancel they are applied as `ISSUE` with `AllowShortfall`, dated the first open day after the freeze with `source_date` the night; the side of each item by the document's `created_at` against that item's `counted_at` (both variants of 8.11, counted before and after the close, give the quantities of the arithmetic of 4.7; a race with a barrier between the entry of `counted_at` and the close); nothing is applied twice if the approval is retried; two closes inside one count make two documents applied in date order; a count of another warehouse holds nothing; a reversal while lines are held is refused (`AMENITY_RUN_HELD`); the daily cost shows the cost on the day of application; both costing methods.
  - *Correction:* the reversal is whole, once, dated the open day, lowers an open shortfall first and restores layers under FIFO; a second reversal is refused (`AMENITY_RUN_NOT_REVERSIBLE`); a manual issue as the partial correction.
  - *Order and locks:* the step runs after the room charges and the queue, before the journal; the lock trace shows 10 -> 30 -> 41/42 -> 45 -> 46 without a descent; no stock writer can run during the close (the day is held `FOR UPDATE`); `lockorder_test.go` covers the new use case.
- Web: the count sheet at 390 px, the SR and transfer screens, the issue document, the plan and the night document of the amenities, the chain on an SR (mockups 22, 23, 24, 25, 26, 32, 33).

**PR 4 (POS recipes)**
- The hook: a send and a DIRECT pay each queue one row per line with the recipe snapshot; variant replaces the same item, modifier adds, a line with no recipe queues nothing; the plan gate (POS only, Inventory only, both); a replayed request queues nothing twice; the structural guard that every POS posting path writes the hook.
- The worker: `SKIP LOCKED` with two workers (no row twice), a frozen warehouse leaves `HELD`, a failed row blocks the audit and retry works, **the sale is never refused for lack of stock** and **never waits for a stock lock** (a test holds a balance lock and sends: the send commits), the audit drains before the journal, a sale across midnight with a held row keeps `source_date`.
- Voids: before the worker (cancelled row), ticket `NEW` (return, layers restored), `READY` (waste, no balance change), DIRECT (return), on a later day.
- The numbers of 8.2 and 8.6 for both methods; cost of sales by the outlet's department in the departmental report.
- **Sale class and cost (UI review, I14):** the class of the category, its parent's, the warehouse's, OTHER with the category's account; the class is snapshotted in the queue row and in the movement (a later change of the category rewrites nothing); the lime, wine and coffee of 8.10 book to 5220, 5210 and 5220 in one posting each; the control of 6.3 for 1310 / 1320 holds; the warning `INVENTORY_COGS_MISMATCH` and `FB_CLASS_REVENUE_MISMATCH`; a restaurant selling coffee from its food warehouse.
- Web: the recipe tab, the recipe cost, hidden without both modules.

**PR 5 (reports)**
- The reports of section 12 against hand-computed data (8.8), the day snapshot against the ledger, CSV, the permission of each report.
- **F&B reconciliation (UI review, I17):** the table of 8.8 reproduced to the rupiah from seeded movements (opening, purchases, the measured cost transfers 1,480,000 / 320,000, closing, staff meal, entertainment, revenue by class); the identity with the general ledger (5210 + 5220 + 5290 + 5295 + the other outflows); a void after cooking moves cost between 5210 / 5220 and 5290 without changing the sum; the no-recipe estimate and its cap; "estimated from the book stock" when a period has no approved count.
- **Daily cost (all costs, D31):** `inv_fb_cost_days` and `inv_cost_days` written once at the close and equal to a recomputation from the ledger, the POS lines and the amenities runs; F&B revenue by the rule of 12.3 (net of discount, before service and tax, comps without revenue, voids out, POS only); MTD; the target per outlet and class, the property default and the revenue-weighted target of "all outlets"; the open day computed live; **each column**: F&B cost of sales and F&B %, waste, staff meal and entertainment, amenities with the occupied rooms and the cost per occupied room (the held amenities of D24 on the day applied), engineering, stationery and other, count variance on the day the count is approved, price variance on the day the bill is confirmed, total; the three views (all costs, F&B only, by department) add up to the same total; a reversal lowers its day; a day with no occupied rooms shows no cost per room (no division by zero); staff meal and entertainment are in the daily cost but not in the F&B %.

**Always**: tenant isolation of every table and route, permissions (403) and the read-only subscription state, `TestEveryRouteHasAModule`, the demo seed is idempotent and passes the setup check, `scripts/db-test.sh` for each migration.

## 14. Phases (PRs), migrations

Migration numbers are the next free: **00084 to 00088**; if another PR takes one first they move (as documents 31 and 34 did). Each PR is DB, backend, API (OpenAPI first), frontend, i18n and tests together, on its own branch from `main`; `gen:api`, `sqlc`, lint and the web checks as CLAUDE.md lists. The module is usable from PR 1 for the owner who only wants stock.

| PR | Migration | Contents |
|---|---|---|
| **1. Masters, ledger and opening stock** | 00084 | settings and the method lock, units, categories with accounts, items and units, warehouses (with `cost_class`), levels, the ledger with its triggers and the class columns (`item_class`, `cost_class`, `outlet_id`, the kinds `STAFF_MEAL` and `ENTERTAINMENT`), balances, layers, shortfalls, the ledger writer for both methods (with `AllowShortfall`), opening stock, the new accounts and the eight keys, `LevelStock` in `db/locks.go`, the control accounts, the stock source of the day close and of `ControlSources`, the readiness checks, the permissions, the module classification, `pms-seed inventory` (masters and opening) |
| **2. Purchasing** | 00085 | requests, orders, receipts, bill drafts, matches, returns, `ConfirmBill` with the three-way rules, the clearing of variances, `VoidGuard`, the approval kinds `INV_BILL_VARIANCE` and `INV_COUNT` and the `Decision` mode of `iam.Verify`, direct receipt; **(UI review) the approval-chain engine** (`inv_approval_levels`, `_settings`, `_steps`, `_edits`, `inv_notices`), the default PR chain, the inbox and the bell, **the head and deputy columns of `departments`**, the new permissions; the screens of purchasing (mockups 18 to 21) and of the chain settings (34) |
| **3. Movements, requisition, count, amenities** | 00086 | requisition (SR) with the chain (the column and FK of `inv_approval_steps`, the default SR chain), transfer answering an approved SR, issue, waste, **(UI review) staff meal and entertainment**, count with the freeze, reversals, the open-count check; **the automatic amenities** (plan versions, the run at the close, the interface `StockCloser` of the night audit and `NightOccupancy` of the front desk, the preview, the reversal); the screens (mockups 22 to 26, 32, 33). It is the largest PR; if the reviewers want it smaller, the amenities may go to a PR 3b with the next free migration number, and the PRs after it move |
| **4. Recipes and the POS deduction** | 00087 | `pos_outlets.warehouse_id`, **`pos_categories.fb_class`**, the foreign keys of `pos_recipe_lines`, the recipe editor and the recipe cost, the queue (with `sale_class`) and the worker, the hook in the POS paths, the sale class and the cost-of-sales account by class (4.9), voids, the night-audit drain, the gap report, the POS master tab (mockup 27) |
| **5. Reports and snapshots** | 00088 | `inv_balance_days`, `inv_fb_cost_days` and `inv_cost_days` written at the close, `inv_cost_targets`, the stock card, valuation, **F&B cost reconciliation, daily cost, cost per outlet** (mockups 29 to 31), low stock and the other reports, dashboard, CSV |

The migration numbers do not change with the review: the new tables and columns ride in the migration of the PR that needs them. If another PR takes a number first, the numbers move.

## 15. Decisions (all decided 2026-10-16: D1 to D23 as recommended; D24 to D31 after the review of the screens, D24 and D31 different from the recommendation)

| # | Question | Recommendation |
|---|---|---|
| D1 | Average (and FIFO layers) **per warehouse** or per property? | **Per warehouse.** The cost lives where the goods are, the row lock is local, the value by warehouse is exact, and FIFO layers are per warehouse by nature. Per property would need one lock per item for the whole hotel and would make a transfer a non-event whose cost then jumps. |
| D2 | A bill price variance: **revalue what is on hand and send the rest to PPV**, or send all of it to PPV? | **Revalue the on-hand part** (4.2, 5.3.4), the rest to PPV. It keeps stock at actual cost. A part of the receipt that moved to another warehouse is not followed (it goes to PPV); the simple alternative (all to PPV) is easy to switch to. |
| D3 | Tolerances. | Price **1%**, over-receipt **5%**, bill quantity above receipt **0%**; per property. |
| D4 | POS deduction in the POS transaction, or a **queue**? | **Queue** (5.6): no lock in the cashier's transaction, a sale never fails for stock, stock is right within seconds and always before the close. |
| D5 | Void "before cooking returns, after cooking is waste": by the ticket status (`NEW` = return; `COOKING`, `READY`, `SERVED` = waste), `DIRECT` always return? A kitchen on `PRINT` mode never moves the ticket. | **By the status of the ticket**, `DIRECT` = return. For `PRINT` outlets whose ticket stays `NEW`, add an outlet setting "void after N minutes counts as waste" (default off, 0). |
| D6 | A count still open at the close. | **Blocker by default**, a warning by setting (5.5). |
| D7 | PR and count approval: a **decision by the approver in their own session** (UI review: for the PR this is superseded by the chain of I13, 5.8; the decision mode stays for the count) (a small `Decision` and `Subject` on `iam.Verify`), or the approver typing a password next to the requester? | **The decision mode.** A manager approves from their phone; the rules of document 32 apply unchanged. The password mode would put the manager next to every requester. |
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
| D23 | **Count without a freeze** at all: every item gets its book quantity *as of its `counted_at`* from the ledger (`sum(qty_delta)` up to that instant), manual movements and POS sales go on, and the variance is `counted - book at counted_at`. | **Decided 2026-10-16: keep the freeze for manual movements (as recommended).** Without it a transfer or a receipt between two lines of the sheet moves goods the counter may see twice or not at all, every movement needs an exact instant, and the balance at an instant is a query over the ledger, not a row. It is possible (the ledger is append-only and every movement has `created_at`), it removes `WAREHOUSE_FROZEN` and the held rows, and it suits a small bar that never stops; the cost is that only POS sales are safe, not manual movements. A middle way is to freeze only manual movements, which is what this design does. |
| D24 (UI review) | The amenities run and a warehouse frozen by a count that the property lets the close pass (`count_blocks_night_audit` off). | **Decided 2026-10-16, different from the recommendation:** the run does **not** block the close. Its lines for the frozen warehouse are `HELD` like POS queue rows and applied when the count is approved or cancelled, the side of each item by the document's `created_at` against `counted_at` (4.7), on the first business day after the freeze with `source_date` the night of the amenities (as D19). `AMENITY_PLAN_INVALID` is no longer raised for this cause (5.5, 5.7, 5.9, 8.11). The earlier recommendation was a blocker with the cause. |
| D25 (UI review) | May the tenant administrator approve their own PR or SR (document 32 lets them for corrections)? | **Decided 2026-10-16, as recommended:** No.** R2 is firm: the requester never approves their own document. A property with one decider configures fewer levels, even none (the setup check warns). |
| D26 (UI review) | An approver's edit that **raises** the total after earlier levels approved: send it back to level 1, or keep going? | **Decided 2026-10-16, as recommended:** Keep going**, with the later conditional levels evaluated again on the new total, the edit visible to the later levels and in the audit. A reset on every rise would punish the cost controller for correcting a price; a rule "reset above N %" can be added later as a column of `inv_approval_settings`. |
| D27 (UI review) | How is an approver told? | **Decided 2026-10-16, as recommended:** The bell and the inbox from `inv_notices`, refreshed on focus and every 60 seconds, plus one event on the POS stream where it runs.** E-mail would first need `email_outbox` freed from its reservation column (migration 00021); the notices table is the hook for it. |
| D28 (UI review) | The cost-of-sales account of a POS sale and the cost transfers between food and beverage (4.9). | **Decided 2026-10-16, as recommended:** The class of the product sold** (the `fb_class` of its POS category, the warehouse class as the default), the account by key, **no journal between cost-of-sales accounts**, the cost transfers of the reconciliation measured. The warehouse or outlet class alone (the owner's first thought) is simpler but charges the coffee of a restaurant to food cost; the category of the item with a month-end journal is wrong in every daily report until the entry. |
| D29 (UI review) | The accounts of staff meal and entertainment. | **Decided 2026-10-16, as recommended:** One account each** (5240, 6170 as keys), the department on the document; the owner may repoint the keys. An account per department (5120 for Rooms staff, 5240 for F&B ...) can come later as a map from department to account. |
| D30 (UI review) | An approval for entertainment above an amount? | **Decided 2026-10-16, as recommended:** Not now**; the reports list them by department and host. A kind `INV_ENTERTAINMENT` of document 32 can be added without a change of tables. |
| D31 (UI review) | The rule of revenue in the cost %, and what the daily cost covers. | **Decided 2026-10-16, changed from the recommendation:** the daily cost covers **all costs** (F&B cost of sales and %, waste, staff meal and entertainment, amenities with occupied rooms and cost per room, engineering, stationery / other, count and price variances, total), in three views (all costs, F&B only, by department). The revenue rule for the F&B % stays as in 12.2 and 12.3: net of discount, before service and tax, comps without revenue, POS only. The earlier recommendation was an F&B-only daily cost without staff meal, entertainment and the variances. |

## 16. Not in this design (I11: deferred; the design does not close the way)

| Deferred | Why it stays open |
|---|---|
| **Batch and expiry** | A FIFO layer is already a lot. Adding `lot_code`, `expires_on` to a layer, a `lot_id` to the ledger (nullable) and a FEFO order (first to expire) changes the OUT order, not the tables or the journal. Under AVERAGE a lot would be a tag on the receipt line only. |
| **Auto sold-out in POS when stock is zero** | `inv_balances` of the outlet's warehouse is the data; a rule "when qty of any recipe item reaches 0, insert `pos_outlet_sold_out` for the day" can be a trigger or a step of the worker, using the table that already clears itself with the day. |
| **Sub-recipes and production** (a sauce made from ingredients) | `kind` of the ledger is a list that a migration extends: `PRODUCTION_OUT` and `PRODUCTION_IN` with the same writer; `pos_recipe_lines.inventory_item_id` may point to a produced item. |
| **Fixed assets and depreciation** | A separate module. Non-stock purchases stay on the payables bill with an expense or asset account; items that are not stocked are not items here. |
| Landed cost, supplier price lists and quotes, forecasting, barcode scanning, multi-currency purchases, user-to-warehouse assignment, a transit warehouse | Each adds tables or screens around the ledger; none needs it to change. |
| E-mail, SMS or push of an approval | `inv_notices` and the event are the hook (D27); the e-mail outbox needs its reservation column freed first. |
| An approval kind for entertainment, a reset of the chain when an edit raises the total, an account of staff meal per department | Columns or maps beside the engine (D26, D29, D30); no table changes shape. |
| An offline stock client | Documents carry `client_uuid` and idempotency keys as POS does; the ledger needs nothing more. |

## 17. As built: PR 1, masters, ledger, balances and opening stock (migration 00084)

What was built follows sections 3.1, 3.2, 4, 5.1, 6, 7, 9, 10 and 13 (PR 1) and section 14 row 1, with the owner's approval of 2026-10-16. Where it differs from the text above, this is the record. Nothing of purchasing, requisitions, counts, recipes, amenities, the approval chains or the reports is built.

**Built.**

- **Schema** (`migrations/00084_inventory_ledger.sql`): `inventory_settings` with the lock of the costing method (the trigger on the ledger stamps `costing_locked_on` at the first movement, a guard trigger refuses a change of the method after it), `inv_units`, `inv_categories` (a trigger checks that the inventory account is an asset of the group INVENTORIES and every cost account an expense), `inv_items`, `inv_item_units` (rational conversions), `inv_warehouses` (with `cost_class`), `inv_levels`, `inv_movements` (append-only, no truncate, `business_day_must_be_open`), `inv_balances`, `inv_layers`, `inv_layer_uses` (append-only), `inv_shortfalls`, `inv_openings` and `inv_opening_lines`; the eight keys of the account map and the four accounts 2130, 5290, 5295 and 5299 (the chart of a new property and, for the properties that exist, a data step with the replacement rule of 00071), the sequence `INV_OPENING`. Down is refused while the module holds anything (`cannot roll back 00084`).
- **The ledger writer** (`internal/inventory/ledger.go`): `Ledger.Lock` takes the warehouses `FOR SHARE` and the balance rows `FOR UPDATE` in one call, ascending, and returns a `*Stock`; only a `*Stock` can write (`In`, `Out`, `Reverse`, `Adjust`), so a write without the locks cannot be expressed. Both costing methods, the settlement of stock sold below zero inside `In`, the estimate chain of 4.4, `AllowShortfall` (an exported function whose callers the guard lists; none yet), the class columns (`item_class`, `cost_class`, `outlet_id`), the accounts of the journal snapshotted on the movement.
- **Services**: settings, units, categories with their accounts, items with their units, warehouses, minimum levels, the opening stock (all or nothing, idempotent by `client_uuid`, `INVENTORY_GL_NOT_EMPTY`, `OPENING_EXISTS`), the list of items with the stock of each warehouse, the stock card (running balance, FIFO layers, open shortfalls) and the setup check.
- **Locks**: `LevelStock = 45` shared with the groups, `StockOpenings`, `Warehouses` and `StockBalances` in `db/locks.go`; the order *documents, warehouse, balances* inside the level is enforced at run time (`ErrLockOrder`), and `db.WithLockTrace` records the locks of a transaction for the tests.
- **Accounting**: `accounting.StockSource` (the lines of the day close, the value by account, the control accounts) wired by an adapter in `internal/app` (inventory does not import accounting); from the first movement the inventory accounts and the account of `INV_GRNI` are control accounts (`CONTROL_ACCOUNT` for a manual journal and for a bill line); the reconciliation shows one control `INVENTORY_<code>` per inventory account, computed from the stock tables; the eight keys are in `mapKeys` and seeded for a new property.
- **Night audit**: `nightaudit.StockChecker` with the findings `INVENTORY_BALANCE_DRIFT` and `INVENTORY_GL_MISMATCH` (blockers, in `blockers.stock`), `INVENTORY_NEGATIVE_STOCK` (warning, in `warnings.stock`), and `CloseStock`, which audits the lock of the method once (`inventory.costing_locked`) before the accounting row is taken.
- **API** (OpenAPI first, section 10 of `06-api.md` here): `GET/PUT inventory/settings`, `GET inventory/setup-check`, `GET inventory/accounts`, units, categories, items (with `PUT items/{id}/units`), warehouses (with `GET/PUT warehouses/{id}/levels`), openings and `GET inventory/stock-card`. Module `INVENTORY`; permissions `inventory.view`, `inventory.manage`, `inventory.opening`, granted to no role.
- **Web**: `/inventory/items` (list and side panel), the pages of an item, a warehouse (with its levels), a category and a unit (list, then a page of its own), the opening stock as a document page, and the costing page (method and lock, setup check, stock card with the FIFO layers and the shortfalls).
- **Demo**: `go run ./cmd/pms-seed inventory -tenant DEMO -property BALI [-dry-run]` (units, four categories, four warehouses tied to the outlets of `pms-seed pos` when they exist, 41 items in Indonesian with their conversions, the opening stock of each warehouse; idempotent by code, the openings by a fixed client uuid).

**Differences from the design**

1. **`inv_warehouses.count_id` has no foreign key yet** (the table of counts is PR 3, which adds it). The writer already refuses a locked warehouse with `WAREHOUSE_FROZEN` unless the caller passes `AllowFrozen`.
2. **`inv_supplier_items` is PR 2's** (section 14 row 1 does not list it).
3. **`inv_shortfalls.est_value_open`** is added: the value of the open quantity at the estimate. With only `qty_open` and the unit estimate a settlement in parts drifts by a rounding; with the value kept, `balance = layers - open shortfalls` holds to the last unit of money (tested, both methods).
4. **The opening has a number**: the sequence `INV_OPENING` with the prefix `OPN-` (`OPN-000001`). The design lists no sequence for it but 5.2 takes the sequence lock for the opening, and a document of the books should be numbered.
5. **Only three permissions are created**: `inventory.view`, `inventory.manage`, `inventory.opening`. The other eleven of 9.1 come with the PR that uses them (a permission no screen asks for would only confuse the role editor).
6. **The four new accounts have no default department** (the cost accounts 5210 and 5220 of the chart have none either; the department of a movement comes from the warehouse or the document).
7. **A settlement row** (`NEG_SETTLE`) has `source_type = INV_SETTLE`, `source_id` = the movement of the goods that came in and `source_line` = the id of the shortfall, so that one receipt can settle several shortfalls and one shortfall can be settled in parts. No row is written when the real cost equals the estimate.
8. **`Reverse` takes the simple cases**: goods that came in and are all still on the shelf (and settled nothing), and goods that went out whose shortfall, if any, nobody has settled. The others answer 409 `MOVEMENT_NOT_REVERSIBLE`; the PR that needs a case adds it. `Adjust` refuses with `COST_ADJUST_NOT_APPLICABLE` when no goods of the receipt are on hand (the caller then takes the whole difference to the variance).
9. **The currency lock**: `inv_movements` is a root of `property_has_financial_data` (the ledger holds money in the currency), and the inventory tables are classified in the schema test of the lock.
10. **The reconciliation of GRNI is not shown** (nothing moves it before PR 2); the account is already a control account.
11. **Not in PR 1**: the CSV import of items (the button of mockup 17), the admin action *Rebuild balances* (7.5 d; the drift check is built, the repair is not), the permissions of the later PRs, the per-item `client_uuid` of items.
12. **`PATCH` takes the whole input with its `version`** (the same shape as the POS `PUT`), and `GET inventory/accounts` (the accounts a category may use) is an addition.
13. **The static lock test reads the call graph within a package** (functions are matched by name): it cannot follow a call through an interface. The dynamic test (a lock trace of the use cases) is the check that does.

**Compared with the mockups 17 and 28**

*As in the mockups.* 17: the list with a column for each warehouse, the chips of the categories and "Di bawah minimum" with their counts, the markers *Di bawah min* and *Minus*, the footer with the value of all the stock, the side panel with the units (stock, buy, recipe), the stock per warehouse with its average and its minimum and a short card; the method and its lock in the header. 28: the four steps, the method cards with the lock text, the stock card with the running balance and the average, the layers under FIFO.

*Different on purpose.* The items, the warehouses, the categories and the units are a list that fills the screen and a page of its own (I18), not a dialog. The panel shows the card of the first warehouse that has stock; the full card is on the costing page. The panel of an item (UI review of PR #44): its table keeps the name of an item on one line (a minimum width and an ellipsis with the whole name in `title`); with the panel open the warehouses that hold nothing of the items listed leave the table, and the table scrolls sideways inside its box when the rest is still too wide. Business dates are written as everywhere else (`3 Sep 2026`; the short card of the panel, a dense list, leaves the year: `3 Sep`); the cost of an opening line is typed in `MoneyInput` (the separators of the language, the decimals of the currency; with a currency that has no decimals the separator is not accepted). The rows of the lists of the masters open their page on a click and the code is a link for the keyboard (a table with `clickable` ignores `rowTo`: the lists no longer set it).

The page of costing shows the layers only when the property uses FIFO (the mockup draws both methods side by side on the same data, which the application cannot compute for the method it does not use). The example of stock sold below zero and the journal of the day close of mockup 28 are not on the page: the journal is in Accounting (Control accounts and the day close) and the shortfalls are listed under the card. Mockup 17 has the button *Impor CSV*: not built (section 11 above).

**Tests (real PostgreSQL).** `internal/inventory`: the costing of both methods with exact figures (receipts and issues, an out that empties the balance, rounding at 0, 2 and 3 decimals, the units in money, FIFO layers and partial layers), stock sold below zero (the figures of 8.4 for both methods, a settlement upwards and downwards, several shortfalls in parts, the estimate chain, only a sale may go below zero), reversals and cost adjustments, the writer refusing a pair that was not locked, three races with two goroutines and a barrier (two issues for one shelf, two issues that fit under FIFO, a sale and a receipt), the method lock (the trigger, the settings row written once, two first movements at once), the masters (accounts of a category, units, conversions, outlets, levels), the opening stock (booking, idempotency, one per item and warehouse, all or nothing, decimals of the currency, `INVENTORY_GL_NOT_EMPTY`), permissions and tenant isolation, `stockpath_guard_test.go` (the source of the repository is read: no other writer of the ledger, `AllowShortfall` has no caller) with its meta-test, and `lockorder_test.go` (the static call graph and the dynamic trace, with its meta-test). `internal/app/inventory_api_test.go`: the masters and the opening through the router, the module gate and the permissions, the day close (the journal of the opening, the control accounts, the reconciliation) and the two blockers of the night audit. `db/tests`: the schema tests of every table and rule above, and the migration test of the data step (`migration_00084_*.sql`: a chart without the accounts, one with 2130 and 5290 taken, none, the second run, the Down refused with data, an account its user changed). `cmd/pms-seed`: the demo data refers only to what it creates. Web: `Inventory.test.ts` and the i18n, audit, error-code and navigation tests.
