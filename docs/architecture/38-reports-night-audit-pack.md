# 38. Reports and the night audit pack: the pack by e-mail after the close, the archive of snapshots, and five new reports

Status: **Approved 29 Oct 2026.** The decisions K1 to K7 (section 1; K7 is the amendment of 29 Oct) were given by the owner on 2026-10-29 and are not reopened here. The findings of section 2 are facts read from the code; the questions Q1 to Q19 of section 12 were decided by the owner on the same day (all as recommended; Q6 with the hash of the PDF kept, Q13 accepted with its controls), and two sections (`OUTLETS`, `OUT_OF_ORDER`) were added to the catalog. **Amended 29 Oct 2026 (K7):** report (a) is the **Daily flash report** (DFR) of the hotel, section 7.1, built after the DFR the owner gave as the reference. Nothing is built: this document adds no code and no migration. The numbers of the migrations (00097 onwards) are the next free ones at the time of writing (`00096_fixed_assets_reports_stocktake.sql` is the last on `main`).

Read with: `04-operations.md` (the business day and the night audit run, 12.5 and 13), `05-transactions-locking.md` (lock order), `06-api.md` (conventions of the routes), `11-cashier-budget-cashflow-card.md` (shifts, the Z and X report, the budget), `21-cancellation-and-no-show-fee.md` (fees are posted by hand), `31-deposit-release.md` (the guest ledger and the deposits as of a date), `34-pos.md` (voids, discounts, comps), `36-banquet.md` (the outbox freed from the reservation, 00091), `35-inventory.md` (the snapshot of a closed day is a function of the ledger), `37-fixed-assets.md` (the style of this document).

## 0. The idea in one page

- **One pack for each closed business day.** When the night audit closes a day, the same transaction builds a **snapshot** of the pack (one JSON document for each **section**, sections 4 and 5) and queues **one e-mail for each recipient**. The e-mail has a short summary in its body and the PDF of the sections that recipient chose. Nothing is sent inside the transaction: the worker that already sends the confirmations sends the pack after the commit.
- **The run cannot fail or slow down because of SMTP.** The run writes rows (a snapshot, outbox rows) and nothing else. Rendering the PDF, talking to the mail server, retrying and giving up all happen in the worker (`notifications.Service.ProcessDue`, every 15 seconds), outside any transaction and lock.
- **The snapshot is a function of the ledger.** Every section reads append-only data by business date (the folio items, the payments, the journals, the closed shifts, the audit log), so the snapshot of a closed day can be built again and must equal the stored one. That makes the snapshot a frozen copy of what was sent (an audit trail), and a failed section can be rebuilt (section 6).
- **JSON is stored, the PDF is drawn when it is needed** (download, send, resend), by the deterministic renderer of `internal/documents`. A pack is a few tens of kilobytes; a stored PDF would be five to ten times that, for every day of every property, for ever. What is kept of a PDF that was **sent** is its SHA-256 and the version of the renderer, in the `email_outbox` row (Q6).
- **Recipients are addresses of a property**, not users and not roles (K2). Each has the **sections** he or she receives (K3). The language of the e-mail and the PDF is English (K4).
- **Five new reports** (K5) are also ordinary reports on the Reports page, with a date or a period, CSV and PDF: the daily flash report, cancellations and no-shows, the guest ledger and the deposit ledger, voids and corrections, and the Z report of all drawers. The pack carries their figures for the closed day.
- **An archive screen** (K6): the days with a pack, the sections, the PDF again, a resend, and the history of every delivery.
- **No new lock level and no change of the order of locks** (section 9). The migrations are small and additive: three tables for the pack and the recipients, the pack columns (including the hash of the PDF and the renderer version) and a wider CHECK on `email_outbox`, one index on `audit_logs`.
- **Three PRs** (section 10): the five reports; the snapshot, the archive and the PDF; the recipients, the e-mail and the history. Stage 2 (forecast, pickup, production, nationality, foreign guests, high balance, rate variance) is a plan only (section 11).

## 1. The decisions of the owner (2026-10-29)

| # | Decision |
|---|---|
| K1 | The pack of the night audit is sent **automatically by e-mail after the night audit has finished**. |
| K2 | The recipients are **a list of e-mail addresses for each property** (not users, not roles). An address can be added, deactivated and deleted. |
| K3 | The content of the pack **can be chosen for each recipient**: which sections he or she receives. |
| K4 | The language of the e-mail and of the PDF of the pack is **English**. |
| K5 | **Stage 1** is the pack plus five new reports: (a) the manager flash (today, MTD, YTD, the same day last year, the budget of the budget module), **which K7 below turns into the Daily flash report**; (b) cancellations and no-shows for a period, with the reason and the fee; (c) the guest ledger (the balance of all open folios) and the deposit ledger, as of a business date; (d) voids, corrections, discounts and allowances (folio and POS); (e) the Z report of the day for all cash drawers. **Stage 2** is written as a plan only (section 11). |
| K6 | A **snapshot** of the reports is stored by the night audit (an archive for each business date), with an archive screen, download of the PDF again, resend, and the history of the deliveries. |
| K7 | (29 Oct 2026, an amendment of K5 (a)) Report (a) is the **Daily flash report** (DFR), after the DFR of the owner. Columns: today, MTD, MTD of last month (days 1 to n of the month before), MTD of last year, budget MTD, MTD variance, YTD. A budget only for the figures that have one (money by account group; rooms available, rooms sold and ADR from `budget_statistics`); a count of rooms never gets a money budget. Room statistics (total, out of order, available, sold by room type, complimentary, house use, vacant, walk-in, guests in house, occupancy, double occupancy, ARR with and without complimentary, RevPAR, ALOS); activity (reservations made, cancelled, no-shows, early and late check-outs, arrivals and departures today and tomorrow); covers and the average per cover for each POS outlet and for banquet; net revenue in groups A (rooms), B (food and beverage, by outlet) and C (other) by charge code, the group being the revenue account group of the charge code, with service charge, tax and the gross total revenue (GTR), and a summary line for each source; payments (GTP) by cashier and by payment method; a **control** (GTR - GTP + refunds - transfers to the city ledger = the change of the guest ledger) that is shown and never blocks; signatures *prepared by* (night audit) and *approved by* (accounting) on the PDF. The code of the section stays `FLASH`; its title is "Daily flash report". Breakfast covers included in the room rate are stage 2. |

**Decisions on the questions (owner, 2026-10-29).** Q1 to Q19 of section 12 are decided as recommended there. Q6: the PDF is not stored, **but its SHA-256 and the version of the renderer are kept** in the `email_outbox` row (new columns in the migration of PR 3). Q13: accepted, with the controls written in section 12. Two sections were added to the catalog of section 4, **not selected by default**: `OUTLETS` (only when the POS module is on, otherwise `OFF`) and `OUT_OF_ORDER`. The catalog has **fifteen** sections.

## 2. Findings (read this first)

Each is a fact of the code or of a document in force that shapes the design. The consequence is in the last column.

| # | Finding | Consequence |
|---|---|---|
| F1 | **The run is one transaction** (`nightaudit.Service.Run`, 04 12.5): advisory lock (L0), the open day `FOR UPDATE` (L1), the checks, the room charges, the stock close, the day's journal (`PostDay`), `Summarize` (written to `business_days.summary`), `CloseAndOpenNextLocked`, the audit entry, commit. A blocker anywhere rolls back everything. | The pack is built **after `CloseAndOpenNextLocked` and before the audit entry**, in the same transaction (section 5.1). If the run rolls back, there is no pack and no e-mail. If it commits, both exist. |
| F2 | **A closed business day never reopens** (04 13.2: a trigger; a correction is an adjustment on the current day). "Reopen" exists only for the accounting **month** (`accounting.ReopenPeriod`, with `DEPRECIATION_NOT_REVERSIBLE` for assets) and does not touch a business day. | A snapshot of a day cannot become stale because the day was reopened. It can differ from a live report only through rows that carry a **later** business date, which a report for the closed date does not read (section 5.4). |
| F3 | **The outbox is reservation-bound and was freed once already.** `email_outbox` (00021) has `kind` (`varchar(30)`, a CHECK), `to_address`, `status` (`QUEUED`, `SENT`, `FAILED`, `SKIPPED`), `attempts`, `next_attempt_at`, `last_error`, `sent_at`, `created_by`. Migration 00091 made `reservation_id` nullable, added `banquet_event_id`, `document_id`, `language`, and `email_outbox_shape_ck` keeps each kind to its own columns. The message is **composed when it is sent** (`Service.compose`, `composeBanquet`): the PDF is rendered then, from the data, and nothing is stored in the row. `Service.sender == nil` means "e-mail is off": nothing is queued (`ReservationConfirmed` returns at once). | The pack uses the same path: a new kind, new columns, a wider shape CHECK, a `compose` branch (section 6). The PDF is rendered by the worker from the snapshot, so the row stays small. |
| F4 | **Delivery bounds exist**: 5 attempts with the waits 1 min, 5 min, 30 min, 2 h, 6 h (about 8.6 hours in all), a lease of 10 minutes, a batch of 20, a timeout of 90 seconds for one send; `ErrPermanent` fails at once. The worker runs every 15 seconds (`app.go`). | Retry, failure and the manual resend need no new machinery (section 6.4). A property with many recipients takes more than one tick; that is acceptable. |
| F5 | **A cancellation and a no-show have no business date.** `reservations.cancelled_at`, `reservation_rooms.cancelled_at` and `no_show_at` are `timestamptz`; the reason is `cancellation_reason`. The business date is in `audit_logs.business_date` (`reservation.cancelled`, `reservation.room_cancelled`, `reservation.no_show`), written in the same transaction, append-only. The index of `audit_logs` is by `(property_id, created_at)`, not by business date. | The report (b) takes the date of the event from the audit row and needs one index (section 7.2, Q10). |
| F6 | **Fees are charges posted by hand** (21): `CANCEL_FEE` and `NO_SHOW_FEE` folio items, once for each event, on the folio of the reservation that has no stay. | The report (b) joins the event to its fee by the reservation (and the room line for a no-show), and shows "no fee" when there is none. It does not compute a fee. |
| F7 | **Service charge and tax are liabilities** (`SERVICE_PAYABLE`, `TAX_PAYABLE`, 00028); the revenue accounts get the **net** amount. `folio_items` carries `net_amount`, `service_charge_total`, `tax_total`. The summary of the night audit already shows `{net, service, tax}`. | "Total revenue" in this document is the **net** revenue, the same figure as the income statement; service charge and tax are memo lines (Q19). |
| F8 | **The budget is money per account per month, and rooms available, rooms sold and ADR per month** (`budgets`, `budget_lines`, `budget_statistics`; `budget.VsActual`, `budget.StatisticsVsActual`). There is no daily budget. The statistics of the actual side are read from the summaries of the closed days (`ListClosedDaySummaries`). | The budget column of the flash for MTD is a **pro rata** of the month (Q4); there is no daily and no YTD budget (K7). The year starts where the budget calendar says (the fiscal year), not necessarily in January (Q18). |
| F9 | **The guest ledger and the deposits can be computed for any date.** `folio_item_gl` (a view, with `business_date`, `signed_amount`, `kind`, `is_deposit`), `folios.closed_on`, and the SQL function `folio_deposit_state(property, date)` (00078: `held`, `payable`, `closed`, `applied` for each folio). `accounting.ControlSources` uses them to reconcile 1210 and 2310 as of a date: `folio_balance` is the sum of `debit - credit` of **all** folio items to the date, and the deposits held are `sum(held - applied)` (EVERY_DAY) or `sum(held)` of the folios not closed (ON_CLOSE). | The report (c) for a past date is a query, not a stored table, and must equal `ControlSources` and the two accounts by construction (section 7.3). |
| F10 | **Voids and corrections are folio items and payments with an approver.** `folio_items.transaction_type` is `CHARGE`, `ADJUSTMENT`, `PAYMENT`, `REFUND` or `REVERSAL`; `reverses_item_id`, `reason`, `created_by`, `approved_by` (00017: an ADJUSTMENT or a REVERSAL always has an approver); `discount_amount`. A payment is voided in place (`payments.status = VOIDED`, `voided_at`, `voided_by`, `void_reason`, `approved_by`), and a void is a same-day reversal (04 14.5). The POS lines (`pos_bill_lines`) carry `discount_pct`, `discount_amount`, `discount_reason`, `discount_by`, `is_comp`, `status` (`DRAFT`, `SENT`, `VOID`), `void_reason`, `voided_by`, `void_approved_by`, `voided_item_id`; a complimentary payment has the method `COMP`. The **allowance** accounts are the revenue accounts with a DEBIT normal side (4160, 4290, 4390). | The report (d) reads these directly. A POS line voided while still `DRAFT` never reached the ledger and has no business date; it is out of the report (Q11). |
| F11 | **The Z and X report exists for one shift** (11 6a; `documents.ShiftReportPDF`; `GET cashier/shifts/{id}/report`). The night audit blocks while a shift is open only when `block_night_audit_with_open_shift` is on (default on); with it off a shift can stay open across midnight. Cash taken without a shift (`require_shift_for_cash` off) is shown in the shift report of the day as "not on a shift". A void of a payment of a closed shift is counted in the shift that is open when it happens. | The report (e) is the consolidation of the day over all shifts, with the same figures as the single report, an interim (X) block for a shift still open, a line for "not on a shift", and a check against the cashier report (section 7.5). |
| F12 | **There is no public base URL** in the configuration (37, F16), so an e-mail cannot carry a link to the archive. The PDFs are drawn by `go-pdf/fpdf` from plain data, with no clock and no randomness. | The e-mail carries the PDF as an attachment, never a link. A pure renderer makes "draw it again" safe (section 5.3). |
| F13 | **There is a savepoint precedent** (`inventory/consumption.go`: `db.Tx(ctx)`, `SAVEPOINT`, `ROLLBACK TO`, `RELEASE`) and the run already has optional collaborators set by wiring (`SetStockChecker`, `SetJournaler`, `SetShiftChecker`). | The pack builder is another collaborator set by `app` wiring; one section failing rolls back to its own savepoint (Q1). |
| F14 | **The permission catalog is code** (`platform/auth/permissions.go`, with a tag such as `FA1`); roles are data of the tenant. `report.view` guards the reports; `cashier.shift_manage` sees every shift; `nightaudit.run` runs the audit. | Three new permissions (section 6.6); no migration for them. |

## 3. Vocabulary

| Term | Meaning |
|---|---|
| Pack | The set of sections built for one business date of one property when its night audit commits. Table `nap_packs`, one row for each `(property, business_date)`. |
| Section | One part of the pack: a code from the catalog of section 4, with its own JSON (`nap_pack_sections`). A recipient receives a chosen subset. |
| Snapshot | The stored JSON of a section as built at the close. "Frozen copy" in section 5. |
| Recipient | An e-mail address of a property with the sections it receives (`nap_recipients`). Active or not. |
| Delivery | One `email_outbox` row of the kind `NIGHT_AUDIT_PACK`: one message to one address for one pack, with the sections that were sent. |
| Resend | A staff action that queues a new delivery of a stored pack to active recipients. |
| As of D | The state at the end of business date D: every row with `business_date <= D`. |

## 4. The content of the pack

Fifteen sections. The code is what the database stores (`varchar(16)`). **Default** is what a new recipient receives (Q2). "Live report" says which page of Reports shows the same figures for any date; "new" marks the five reports of K5.

| Code | Title | Content (columns) | Source | Live report | Default |
|---|---|---|---|---|---|
| `SUMMARY` | Night audit summary | Rooms: total, out of order, out of service, sellable, occupied, complimentary, house use, sold. Arrivals, departures, no-shows. Room revenue (net, service, tax) and revenue by charge type (net, service, tax). Payments by method (payments, refunds, net). City ledger: transferred, received, adjusted, outstanding. Occupancy %, ADR, RevPAR. Room charges posted. Who closed the day and when. | A copy of `business_days.summary` of the closed day plus `closed_by` and `closed_at` | Daily summary (exists) | yes |
| `FLASH` | Daily flash report | Section 7.1: today, MTD, MTD of last month, MTD of last year, budget MTD and variance, YTD; room statistics; activity; covers; net revenue A/B/C by charge code with service, tax and GTR; payments (GTP) by cashier and by method; the control balance; signatures | New. The room statistics and the money come from the books by business date (`stay_charge_postings`, `stay_rooms`, `folio_items`, `payments`, the journals), the budget side from `budget_statistics` and `budget_lines` | New | yes |
| `REVENUE` | Revenue of the day | Net, service, tax and gross for each charge code and each USALI department (statement group), with the total | `reports.Revenue` for `[D, D]` | Revenue (exists) | no |
| `TAX` | Tax of the day | Each tax: base, tax, for the day | `reports.Tax` for `[D, D]` | Tax (exists) | no |
| `PAYMENTS` | Payments of the day | Payments, refunds and net for each method and each cashier; the city ledger movement | `reports.Cashier` for `[D, D]` | Cashier (exists) | no |
| `Z_REPORTS` | Z report, all drawers | Section 7.5 | New | New | no |
| `GUEST_LEDGER` | Guest ledger | Section 7.3: each folio with a balance as of D, with the total and the check against the accounts | New | New | no |
| `DEPOSIT_LEDGER` | Deposit ledger | Section 7.3: each folio holding a deposit as of D, with the movement of the day | New | New | no |
| `CORRECTIONS` | Voids, corrections, discounts and allowances | Section 7.4, for the day | New | New | no |
| `CANCELLATIONS` | Cancellations and no-shows | Section 7.2, for the day | New | New | no |
| `OUTLETS` | Outlets (POS) | For each outlet: net sales, service charge, tax, covers (the guests of the bills closed on the day), number of bills, voids (count and net amount), complimentary (count and value at list price) and discounts (count and amount); with a total row. Only when the POS module is on; otherwise the section is `OFF`. | The ledger by business date: the `folio_items` of source `POS` grouped by `folios.outlet_id` (the sales, service and tax components, as the outlet report of 34 section 13.2 reads them), `pos_bills` closed on D (`closed_business_date`, `guest_count`, `status`), `pos_bill_lines` (`status = VOID`, `is_comp`, `discount_amount`) with the business date of the item they caused. The same queries as `pos.Service.Report` for `[D, D]`, summed per outlet. | POS outlet report (exists, one outlet at a time) | no |
| `OUT_OF_ORDER` | Rooms out of order at the close | The rooms blocked for the night that has just closed: room, room type, status (`OOO` or `OOS`), reason, from, until. | `room_blocks` with `status = ACTIVE` and `start_date <= D < end_date` (the interval is half-open, `[start_date, end_date)`), with `rooms` and `room_types`; the count agrees with the out of order and out of service rooms of `SUMMARY`. Taken in the run. | Rooms and blocks page (exists, live only) | no |
| `IN_HOUSE` | In house at the close | The stays open after the close (room, guest, arrival, departure, nights, balance) | `reports.InHouse` taken in the run, after the close | In house (exists, live only) | no |
| `NEXT_DAY` | Tomorrow | The arrivals and the departures expected on the new business date (room type, guest, nights, booker, company) | `reports.Arrivals`, `reports.Departures` for `D + 1` | Arrivals, Departures (exist) | no |
| `AUDIT_NOTES` | Notes of the audit | The warnings the run carried (stale drafts, open folios of cancelled reservations, open group masters, blocks ending, open outlet bills, stock) and the number of room charges posted by the run | The analysis of the run (`analysis.warnings`) | none | no |

**Rules of the content**

1. Money is a string at the property's decimals in the JSON and in the PDF; the sums are computed in `decimal.Decimal` and rounded half away from zero at `properties.currency_decimals` (CLAUDE.md). A percentage has two decimals. Nothing is a float.
2. A section that does not apply is `OFF` and is left out of the PDF without a gap: `Z_REPORTS` when the property has no shifts, `OUTLETS` when the plan has no `POS` module, the POS part of `CORRECTIONS` when the plan has no `POS` module, `DEPOSIT_LEDGER` is never off (an empty ledger prints "no deposits held").
3. The order of the sections in the PDF is the order of the table above, whatever order the recipient was given.
4. The first page of the PDF is a cover: the property, the business date, the date and time it was built, the sections, and for each section a one-line headline, so that a recipient who opens only the PDF sees what is in it.
5. Every section states its date or period and "as built at the close of the business day" in its footer, so a PDF printed later cannot be mistaken for a live report.

## 5. The snapshot

### 5.1 When it is built

**In the transaction of the run, after the day is closed, before the audit entry**: after `CloseAndOpenNextLocked` the day D is CLOSED and D + 1 is open, the day's journal is posted, the shifts are counted (when the setting blocks the audit), the stock is closed. The numbers are therefore **the books**: the flash agrees with the income statement, the guest ledger with 1210, the deposit ledger with 2310, the Z reports with the cashier report.

```
 ... existing steps up to CloseAndOpenNextLocked (summary already in business_days.summary)
 N. pack.Build(ctx, p, propertyID, closedDay)            -- section by section, each in a savepoint
      insert nap_packs (property, D, format_version, status)
      for each section: insert nap_pack_sections (OK + data, or FAILED + error)
      read the active recipients; for each: insert email_outbox (kind NIGHT_AUDIT_PACK, sections, QUEUED)
      audit nap_pack.built (sections built, failed, recipients queued, build_ms)
 N+1. audit night_audit.completed (as today)
 COMMIT
```

The builder is a collaborator of `nightaudit.Service` set by wiring (`SetPackBuilder`, an interface in `nightaudit`, as `SetJournaler`), so `nightaudit` imports neither `notifications` nor `documents`. Its code lives in a new package `internal/auditpack` (sections, the archive, the recipients), which uses the services that own the data (reports, budget, shifts, folios, accounting) through their read functions. It reads; the only rows it writes are its own and the outbox.

### 5.2 Failure of a section (Q1)

A section is built inside `SAVEPOINT nap_section`. If it fails (a bug, a missing report permission is not relevant because the builder acts as the tenant, an unexpected row), the savepoint is rolled back, the section is stored `FAILED` with a short error, the other sections are built, and **the day still closes**. The pack is `PARTIAL`. Reasons: the close of a business day is the most expensive thing the hotel does at night, and a report must never be the reason it fails. The consequences are visible, not silent: the archive marks the pack, the e-mail of a recipient who selected only failed sections is **not queued** (an e-mail without its content is worse than none) and the audit entry lists them, the next night audit preview shows a warning `PACK_INCOMPLETE` with the date, and an authorized user can **rebuild** the failed sections from the ledger (section 5.5).

If the build fails outside a section (the `nap_packs` insert, the outbox insert: a database error), the error is returned and **the run rolls back like for any other error**. That is rare and means the database is unhealthy; the run would likely have failed on its next write anyway.

### 5.3 What is stored: JSON, not PDF

| Option | For | Against |
|---|---|---|
| **JSON for each section (recommended)** | Small (the largest sections, guest ledger and in house, are some kilobytes to a few tens for a 150-room property; `jsonb` is compressed by TOAST). The PDF and the CSV are drawn from it. The same renderer serves the live reports and the archive. A section can be queried (the history of the occupancy for a month). The renderer can be improved; old packs get the new look. | A renderer that changes changes old PDFs: the file downloaded today may not be byte-identical to the one sent a year ago. The data is the same; the data is the record. |
| PDF stored (`bytea`) | The exact file that was sent, for ever. | Ten times the size, for every property and day. A layout fix cannot be applied to the past. A second source of truth next to the data. A recipient with a different selection needs a different PDF, so a PDF for each recipient (or for each section, and merging, which `fpdf` cannot do). |

The shape of every section carries `format_version` (an integer, in `nap_packs` and in each section row). The renderer reads every version it ever wrote; a change of the shape of a section is a new version, never an edit of an old one. The first rendered PDF is **not** stored (Q6, decided); what is kept is the SHA-256 of each PDF that is sent and the version of the renderer, in the outbox row (section 6.2), so that a difference with a file a recipient holds can be proven without keeping the file.

### 5.4 The snapshot and the live report

For a closed day D the live report and the snapshot **must agree**, because every source is append-only and filtered by `business_date <= D` or `= D`, and a later correction is posted on a later day (F2). The exceptions, and how each is handled:

| Section | Can the live report differ later? | Handling |
|---|---|---|
| `SUMMARY` | No: it is the stored `business_days.summary` | Copy |
| `FLASH`, `REVENUE`, `TAX`, `PAYMENTS`, `Z_REPORTS`, `GUEST_LEDGER`, `DEPOSIT_LEDGER`, `CORRECTIONS`, `OUTLETS` | No, if the report reads by business date. **One exception in `FLASH`:** the arrivals and departures of tomorrow are the state at the close (A8 of 7.1); a rebuild leaves them blank and the equality test skips them | A test rebuilds each from the ledger after later days have been posted and requires equality with the stored JSON (section 9.3). `OUTLETS` reads the covers and the bill counts by `closed_business_date`, so a bill reopened and closed on a later day counts on that later day, in the live report and in a rebuild alike |
| `CANCELLATIONS` | **Yes in its state columns**: a reservation cancelled on D can be reinstated later (`reservation.reinstated`) | The snapshot is what happened on D; the live report adds "reinstated since" |
| `IN_HOUSE`, `NEXT_DAY` | **Yes**: they are the state at the close and change at once | Not rebuildable for a past day; the snapshot is the only record. A rebuild of these two refuses (`SECTION_NOT_REBUILDABLE`) |
| `OUT_OF_ORDER` | **Yes**: a block (`room_blocks`) is edited in place (its end date changes, it is cancelled), so the blocks of a past night cannot be read back | Same: not rebuildable, the snapshot is the only record |
| `AUDIT_NOTES` | **Yes**: the warnings are the analysis of the run | Same: not rebuildable |

The sections that cannot be rebuilt are therefore `IN_HOUSE`, `NEXT_DAY`, `AUDIT_NOTES` and `OUT_OF_ORDER`.

### 5.5 Rebuild and days before the feature

`POST {P}/night-audit/packs/{date}/rebuild` (permission `nightaudit.pack_manage`, Q12) rebuilds the sections that are `FAILED` and are rebuildable (table above), from the ledger, in its own transaction; it sets `built_late` on the section. It also builds a pack for **a closed day that has none** (every day before this feature), with the four sections that are not rebuildable (`IN_HOUSE`, `NEXT_DAY`, `AUDIT_NOTES`, `OUT_OF_ORDER`) marked `NOT_AVAILABLE`. It queues **no** e-mail. A day older than 400 days is refused (the range limit used by the other reports). No automatic backfill (Q12).

### 5.6 Reopening, a failed audit, a second run

- **The day is reopened:** it cannot be (F2). The accounting month can be reopened; it changes no business-dated row, so no pack.
- **The audit fails or is blocked:** the transaction rolls back: no pack, no outbox row, no audit entry. The preview and the retry are as today.
- **The run is repeated:** the second request finds the day closed (`BUSINESS_DATE_MISMATCH`) or waits for the advisory lock (`NIGHT_AUDIT_IN_PROGRESS`). The database says the same: `nap_packs` has a unique key on `(property_id, business_date)`.
- **A pack and no recipients:** the pack is stored; nothing is queued. The archive shows "no recipients". Adding a recipient later does not send the old packs (it can be resent by hand).

## 6. The e-mail

### 6.1 One message for each recipient

The builder queues, for each **active** recipient of the property, one `email_outbox` row: `kind = 'NIGHT_AUDIT_PACK'`, `to_address`, `nap_pack_id`, `nap_sections` (the recipient's sections that exist in the pack and are `OK`, in catalog order), `language = 'en'`, `created_by` (the user who ran the audit), `status = QUEUED`, `next_attempt_at = now`. A recipient whose sections are all failed or off gets none (5.2). With `Service.sender == nil` (e-mail off) nothing is queued and the archive says "E-mail is off" (Q9).

### 6.2 What the worker sends

`compose` gains a branch for the kind. In the worker, outside a transaction:

1. Load the pack and the chosen sections from the snapshot (a read, as the tenant, as for the other kinds).
2. If the recipient is no longer active (deactivated or deleted since the row was queued), the row is `SKIPPED` ("the recipient was deactivated"). K2 says that a deactivated address stops receiving; a message already on its way must obey that.
3. Draw the PDF of the chosen sections (one file, cover page first). The file name is `night-audit-{property code}-{date}.pdf`. When the message is sent, the worker writes the **SHA-256 of the file and the version of the renderer** (`documents.RendererVersion`, a constant raised with every change of layout) into the row (`nap_pdf_sha256`, `nap_renderer`). The PDF itself is not kept (Q6). A part of a split message (6.3) has its own hash.
4. Build the message: subject `Night audit {date} - {property name}`; a plain-text and an HTML body in the style of the confirmation: the **headline figures** (occupancy %, ADR, RevPAR, net total revenue, payments received and the arrivals, departures and no-shows), the list of the sections attached, who closed the day and when, and the property's address block. The headline is shown only to a recipient who has `SUMMARY` or `FLASH` (Q3): a recipient who may receive only the in-house list is not given the revenue in the body.

### 6.3 Size

An attachment limit applies to the message (a default of 7 MB, a setting `PMS_SMTP_MAX_BYTES`, so that a mail server with a 10 MB limit accepts it after base64). If the drawn PDF is larger, the worker **splits the sections in order into several PDFs and sends them as numbered messages** (`Night audit 2026-10-28 - Hotel (2 of 3)`), each queued as its own outbox row at that time (so each has its own retry). If **one section alone** exceeds the limit the row is `FAILED` with `PACK_TOO_LARGE`, and the archive still offers that section as a download. A normal pack is far below the limit; the split exists so that the unusual one (a big group, a long guest ledger) is delivered. Q7.

### 6.4 Retry, failure, resend, history

| Case | Behaviour |
|---|---|
| SMTP is slow or down | The worker marks the row for retry with the existing waits (F4); the run finished long before. A property whose SMTP is down for the whole night still has its pack in the archive. |
| 5 attempts fail, or a permanent error (a refused address) | `FAILED` with `last_error` (480 characters at most). The history shows it in red. |
| A failed or lost message | **Resend**: `POST {P}/night-audit/packs/{date}/resend` with `recipient_ids` (one or more active recipients; none means all active) queues new rows. It needs `nightaudit.pack_resend`. A recipient who already has a QUEUED row for that pack is refused for that address (409 `PACK_ALREADY_QUEUED`; a partial unique index is the guarantee, so a double click is harmless). |
| The pack has failed sections | The resend sends the sections that are OK and says so in the answer. |
| Delivery history | `GET {P}/night-audit/packs/{date}/deliveries`: one line for each outbox row of the pack (address, sections, status, attempts, last error, sent time, who queued it, the part number, the SHA-256 of the PDF and the renderer version once sent). The row keeps the address and the sections **as sent**, so deleting a recipient does not change history. |
| Failures noticed | The next night audit **preview** carries the warning `PACK_DELIVERY_FAILED` with the dates and addresses (a warning, never a blocker), and the archive marks the day (Q8). |

### 6.5 What the e-mail does not do

It carries no link (F12), no password and no secret. The PDF contains guest names and balances in the sections that have them (`GUEST_LEDGER`, `IN_HOUSE`, `NEXT_DAY`, `CANCELLATIONS`, `CORRECTIONS`): these are opt-in sections, not in the default selection, and managing the recipients is a separate permission (Q13).

### 6.6 Permissions (new)

| Permission | Group, text | Used by |
|---|---|---|
| `nightaudit.pack_manage` | End of day: "Manage the recipients of the night audit pack and rebuild a pack" | recipients routes, rebuild |
| `nightaudit.pack_view` | End of day: "See the archive of the night audit packs and download them again" | archive, section data, PDF, history |
| `nightaudit.pack_resend` | End of day: "Send a stored night audit pack again" | resend |

Tag `NP1` in the catalog. The five new reports use `report.view` as the other reports do; the Z report of all drawers also needs `cashier.shift_manage` (it shows every cashier's cash). The checks come **before** the path and body are parsed (the authorization matrix tests, CLAUDE.md).

## 7. The five new reports

All five take their money from the append-only ledger by business date, are scoped by `tenant_id` and `property_id`, answer JSON, `?format=csv` and `.pdf` (`?lang=` is accepted for the web but the pack uses English), and use `checkRange` (366 days at most, `OUT_OF_RANGE`). The CSV has plain numbers and ISO dates, with `SafeCell` against formula injection like the other reports. Routes are in section 8.3.

### 7.1 (a) Daily flash report (DFR)

**Question it answers:** how did the hotel do on the day, in the month and in the year, against last month, last year and the budget, and does the money agree with the guest ledger. It is the daily flash report of the owner (K7), one page of numbers that the night audit prepares and accounting approves.

**Parameters:** `date` (a closed business date; default the last closed day). The open day gives an **interim** flash (marked "INTERIM", no control balance, no signatures).

**Columns.** Seven columns, the same rows in each:

| Column | Range |
|---|---|
| Today | the day D |
| MTD | the first of the month of D to D |
| MTD last month | days 1 to n of the month before, where n is the day of D, cut at the length of that month (D = 31 Oct compares with 1 to 30 Sep) |
| MTD last year | the same range a year before (Q5: the same calendar date; 29 February compares with 28 February) |
| Budget MTD | the budget of the month times n over the days of the month (pro rata, Q4). **The total budget is the sum of the groups that have a budget** (a group without a budget row is blank and adds nothing), so the total variance compares only what was budgeted |
| MTD variance | MTD less budget MTD, in amount and in percent; favourable or not by the type of the line, as the budget report does |
| YTD | the first day of the fiscal year of the property's budget calendar (`YearStart`, Q18) to D |

**A budget only for figures that have one.** The money rows have a budget by **account group** (the `statement_group` of the account, summed over `budget_lines` of the month). The rows rooms available, rooms sold, ADR and the occupancy, RevPAR derived from them have a budget from `budget_statistics`. **A count of rooms or guests is never given a money budget**: total rooms, out of order, complimentary, house use, vacant, guests, covers and the activity rows have a blank budget and a blank variance. There is no daily budget and no YTD budget: Today and YTD show actuals only. A line without a budget is blank, not zero.

**Room statistics** (sources and definitions; each count is of the night D, summed over the nights of the range for MTD and YTD):

| Row | Definition and source |
|---|---|
| Total rooms | active rooms: `rooms.total` of the stored summary of the day |
| Out of order | rooms out of order and out of service on the night (`rooms.out_of_order`, `rooms.out_of_service` of the summary; the blocks themselves are in `OUT_OF_ORDER`) |
| Available | total less out of order, out of service and house use: `rooms.sellable` of the summary |
| Sold, by room type | paid room nights of the night: `stay_charge_postings` (`ROOM_NIGHT`) with the business date D, the room of the night from `stay_rooms` (`start_business_date` to `end_business_date`) and its `rooms.room_type_id`; the total equals `rooms.sold` of the summary (a test requires it) |
| Complimentary | `rooms.complimentary`: the nights of a rate plan of occupancy kind `COMPLIMENTARY` (`rate_plans.occupancy_kind`, 00038) |
| House use | `rooms.house_use`: rate plan kind `HOUSE_USE` |
| Vacant | available less sold less complimentary |
| Walk-in | stays checked in on D whose reservation has `reservations.source = 'WALK_IN'`; the business date of the check-in is that of the `stay.checked_in` audit entry |
| Guests in house | persons in the sold and the complimentary rooms of the night: `stays.adult_count + stays.child_count` of the stays in house on D; house use rooms are not counted |
| Occupancy % | (sold + complimentary) over available, as `Summary.OccupancyPercent` |
| Paid occupancy % | sold over available |
| Double occupancy % | (guests in house - rooms occupied) over rooms occupied, as a percentage with two decimals (A2); rooms occupied = sold + complimentary |
| Guests per room | guests in house over rooms occupied, two decimals (A2) |
| ARR | net room revenue over rooms sold; equals `Summary.ADR` |
| ARR including complimentary | net room revenue over (sold + complimentary) |
| RevPAR | net room revenue over available |
| ALOS | nights of the stays checked out in the range over the number of those stays (the check-out is the `stay.checked_out` audit entry; the nights are from `stays.arrival_date` to that business date) |

**Activity** (counts, no budget):

| Row | Source |
|---|---|
| Reservations made | `reservation.created` audit entries with the business date in the range (a reservation, and its rooms and nights) |
| Cancelled, no-show | the events of 7.2 (`reservation.cancelled`, `reservation.room_cancelled`, `reservation.no_show`) |
| Early check-outs, late check-outs | stays checked out on a business date before, or after, the `stays.departure_date` in force when they checked out (A7) |
| Arrivals, departures today | `Summary.Arrivals`, `Summary.Departures` |
| Arrivals, departures tomorrow | the state **at the close** for D + 1, as `NEXT_DAY` (A8); not rebuildable |

**Covers.** For each POS outlet: the sum of `pos_bills.guest_count` of the bills closed on D (`closed_business_date`), the number of bills **without** a guest count (the column is nullable; `guest_count_required` is an outlet setting), and the **average per cover** (the outlet's net sales over the covers). For banquet: the pax of the events held on D (`banquet_sessions.pax_actual`, else `pax_guaranteed`, with a footnote counting the sessions that used the fallback, A9) and the average per pax (the net revenue of the `EVENT` folios over the pax). Breakfast covers included in the room rate are **stage 2**.

**Net revenue.** The rows are the **charge codes**; the name of a row is the name of the charge code. The groups follow from the **revenue account** the charge code posts to, with no setup (A5): the item carries `folio_items.revenue_account_code` (the snapshot of `charge_codes.gl_account_code` at the time of posting, 00019), which names a row of `gl_accounts` of the property, and the group is the `statement_group` of that account:

| Group | Rule (`statement_group` of the revenue account) | Rows |
|---|---|---|
| **A. Rooms** | `REV_ROOMS` | one per charge code |
| **B. Food and beverage** | `REV_FB` | grouped by outlet: the title of each block is the POS outlet (`pos_outlets` name, from `folios.outlet_id`), "Banquet" for an `EVENT` folio (`folios.banquet_event_id`), and "Hotel folios" for food and beverage posted on a guest, company or master folio; one row per charge code under each |
| **C. Other** | every other revenue group: `REV_OOD`, `REV_RENTAL_OTHER`, `REV_MISC` | still grouped by outlet or "Banquet" when the folio is an `OUTLET` or an `EVENT` folio (a spa, a laundry, a room rental of the banquet), and "Hotel folios" otherwise; one row per charge code under each |

The department of the account, not the charge type and not the folio, decides the group: a spa outlet is C, the rental of a banquet room is C, the food and beverage of a banquet event is B. An item with no `revenue_account_code` (a charge code with no account) is printed in C under "Unmapped" with a warning line; the setup check of the module already reports such a code. Because the groups are those of the income statement, **A + B + C equals the revenue groups of the income statement** for the range (net of allowances), which the tie below checks.

`TRANSFER` items are not revenue and are left out. Allowances and rebates carry their sign (they reduce the group). **Total revenue = A + B + C** is the **net** revenue of the income statement (Q19), group for group: A is `REV_ROOMS`, B is `REV_FB`, C is the rest. Below it: **service charge**, **tax**, and **GTR** (gross total revenue = net + service + tax). The figures come from `folio_items` by business date (`net_amount`, `service_charge_total`, `tax_total`).

**Tie to the books.** A row "Revenue posted by journal, not from a folio" (manual journals to revenue accounts, in the same `statement_group`s) is added so that **A + B + C + that row equals the income statement for the range**; it is zero for a hotel that posts revenue only through folios, and a test requires it to be zero in the normal fixtures. A **summary by source** follows: **Hotel** (guest, company and master folios), **POS outlets** (the `OUTLET` folios), **Banquet** (the `EVENT` folios), **Other** (the journal row). The four add up to the total.

**Payments (GTP).** The payments of the range (`payment_type = 'PAYMENT'`, not voided), in two cuts that each add up to the same GTP:

- **By cashier:** Front office (the payments taken on a shift, by the cashier of the shift `cashier_shifts.user_id` and its `drawer`), Outlets (payments of an `OUTLET` folio, by outlet), Back office (payments with no `shift_id`, no drawer: bank transfers and the like). A6.
- **By method:** every method of `payments.payment_method` that has a payment in the range: `CASH`, `CARD`, `BANK_TRANSFER`, `QRIS`, `OTHER`, and `COMP`. `CITY_LEDGER` is **not** in GTP: it is a transfer to the city ledger and is a line of the control. The methods are a fixed set in the schema (00081), not a configured list (A6).

Refunds (`payment_type = 'REFUND'`) are shown below GTP, by method.

**The control balance.** For Today and for MTD:

```
 GTR - GTP + refunds - transferred to the city ledger = change of the guest ledger
```

The change of the guest ledger is the guest ledger of 7.3 at the end of D less that at the end of the day before the range (`folio_balance` of `ControlSources`, deposits included as credits). `GTR` is the gross of the charge, adjustment and reversal items; the `TRANSFER` items between folios net to zero for the property and are not in either side. The report prints both sides and the **difference**. A difference is printed in red and in words, never hidden, and **never blocks** anything (the report is a finding, as the check lines of 7.3). For a closed day built after the day's journal it is zero by construction; a non-zero value points at a row that did not follow the usual path.

**Signatures.** The last block of the PDF has two lines: **Prepared by** (the night audit: the user who closed the day, `business_days.closed_by`, with the time) and **Approved by** (accounting: a line to sign and date by hand, A10). Neither is a workflow.

**Rounding.** Each line is computed at full precision and rounded once, at the end, to the property's decimals; ADR, ARR and RevPAR are the rounded quotient of the rounded-at-the-end totals, never an average of daily values. Percentages and double occupancy have two decimals.

**When the property has no journals yet** (no accounting setup: `s.journal` is nil in the run) the tie-to-books row and the budget columns are left out, with a note.

**Worked example (hotel of 100 rooms, a month of 30 days, day 10 in October).** Rooms available 100 a day, sold 70 on D, MTD sold 620 of 1,000 available, net room revenue on D 105,000,000, MTD 930,000,000. Today: occupancy 70.00 %, ADR 1,500,000, RevPAR 1,050,000. MTD: occupancy 62.00 %, ADR 1,500,000, RevPAR 930,000. The budget of the month for rooms is 3,000,000,000: budget MTD 1,000,000,000 (10/30), variance MTD -70,000,000, -7.00 %. Last month (September has 30 days) days 1 to 10 are compared, and last year 1 to 10 October. Control, day D: GTR 140,000,000 (net 120,000,000, service 10,000,000, tax 10,000,000), GTP 90,000,000, refunds 5,000,000, transferred to the city ledger 20,000,000: 140,000,000 - 90,000,000 + 5,000,000 - 20,000,000 = 35,000,000 = the change of the guest ledger, difference 0.

**Assumptions to confirm at the build** (read from the schema; each is a choice the owner can correct, none changes the rest):

| # | Assumption |
|---|---|
| A1 | **Day use does not exist**: `stays_dates_ck` requires `departure_date > arrival_date`, so a stay on one day cannot be recorded. **The row is not printed** until a day-use feature exists (stage 2, section 11). |
| A2 | **Double occupancy** = (guests in house - rooms occupied) / rooms occupied, a percentage with two decimals (the schema has no flag for a "double" room); a **guests per room** row (guests / rooms occupied, two decimals) is printed beside it. |
| A3 | **Occupancy** is printed twice: with complimentary rooms (the system's definition) and for paid rooms only. |
| A4 | Complimentary and house use rooms come from the **occupancy kind of the rate plan** (`rate_plans.occupancy_kind`), as the closing summary already does. |
| A5 | The A/B/C groups follow the **`statement_group` of the revenue account** of the charge code (`REV_ROOMS` to A, `REV_FB` to B, the other revenue groups to C), titled by POS outlet or "Banquet" for the folio. No charge type or folio type decides the group. |
| A6 | Payment methods are a **fixed set** in the schema, not "configured"; the three cashier groups follow `shift_id`, the folio type and the drawer. |
| A7 | Early and late check-out compare the business date of the check-out with the departure date in force at that moment (an extension changes `stays.departure_date`). |
| A8 | The arrivals and departures of tomorrow are the state at the close and cannot be rebuilt for a past day; they are blank in a rebuild and left out of the equality test. |
| A9 | Banquet covers use `pax_actual`, then `pax_guaranteed`, with the fallback shown. |
| A10 | "Approved by" is a signature line on the paper, with no record in the system. |

### 7.2 (b) Cancellations and no-shows

**Question it answers:** who cancelled or did not arrive in a period, why, how much it cost the hotel, and what was charged.

**Parameters:** `from`, `to` (business dates of the **event**), `kind` (`ALL`, `CANCELLED`, `NO_SHOW`), optional `source`, `company_id`.

**One row for each event.** A cancelled reservation is one event (`reservation.cancelled`), a cancelled room line of a reservation that stays is one (`reservation.room_cancelled`), a no-show is one for each room line (`reservation.no_show`; the bulk action writes one per line).

| Column | Source |
|---|---|
| Event business date, time | `audit_logs.business_date` and `created_at` of the event (F5); the user from `user_id` |
| Kind | `CANCELLED`, `ROOM_CANCELLED`, `NO_SHOW` |
| Confirmation number, line number, guest, booker, company, source, market | `reservations`, `reservation_rooms`, the guest, `reservations.source`, `market` |
| Room type, arrival, departure, nights, lead time (days from the event date to arrival; negative for a no-show) | the line |
| Rate plan, value of the nights lost | the nightly rows of the line (`amount`, the rate snapshot): the sum of the net amounts of its nights, which is what the booking was worth |
| Reason | `cancellation_reason` (the line's, else the reservation's); a no-show has the reason of the bulk action when one was given, else blank |
| Fee | the `CANCEL_FEE` / `NO_SHOW_FEE` item of the event (F6): amount (net and gross), the business date it was posted, whether it was reversed. "No fee posted" is shown as blank, not zero, so a missing fee is visible. |
| Deposit | the deposit held on the reservation's folio without a stay as of the event date and now (`folio_deposit_state`): held, applied to the fee, refunded, still held |
| Reinstated since | the reservation is `CONFIRMED` again (live report only) |

**Totals and breakdowns:** events, rooms, nights and value lost by kind; fees posted; deposits held; by reason (the free text is grouped as the free-rooms report groups reasons: case and spacing folded); by source; by company. The pack carries `[D, D]`.

**Rules.** A reservation cancelled and reinstated and cancelled again is two events (21). A cancelled **group** (`group.cancelled`) shows its lines. Cancelled lines of a draft are not events (a draft holds no inventory).

### 7.3 (c) Guest ledger and deposit ledger

**Question it answers:** what the guests owe and what we hold for them, on a given close.

**Parameter:** `as_of` (a business date; default the last closed day). A date in the open day is refused (`DATE_NOT_CLOSED`, the books are not complete); future dates are refused.

**Guest ledger.** One row for each folio with a non-zero balance as of D (open folios, and closed folios that still show a balance, which the reconciliation also counts):

| Column | Meaning |
|---|---|
| Folio number, type (GUEST, COMPANY, MASTER, OUTLET, EVENT where the banquet module is on), status as of D | `folios` (`closed_on` against D) |
| Reservation, room, guest or company or group or outlet, arrival, departure | the owner of the folio |
| Charges, settlements, deposits | `sum(signed_amount)` of `folio_item_gl` with `business_date <= D`, by `kind` and `is_deposit` |
| **Balance** | `sum(debit - credit)` of the folio's items to D: what `ControlSources.folio_balance` adds up |
| Deposit held as a liability | `held - applied` of `folio_deposit_state(D)` in EVERY_DAY (the amount of the deposit not yet used to pay a charge); the held amount of an open folio in ON_CLOSE |
| Age | days from the oldest unpaid charge's business date to D (buckets 0-1, 2-7, 8-30, 31+) |

**Totals and the check.** The total of the Balance column equals `ControlSources.folio_balance`; the deposit column equals `deposits_held`; the **account 1210 as of D** equals the balance plus the deposits held (31 sections 4 and 5). The report prints the three comparisons with the difference, which is zero for a closed day because the day's journal is posted before the close. A non-zero difference is printed in red and does not stop the report (it is a finding of the reconciliation, not of this report).

**Deposit ledger.** One row for each folio holding a deposit as of D or with deposit movement on D:

| Column | Meaning |
|---|---|
| Folio, reservation, guest or company, arrival | as above |
| Opening | the liability as of D - 1 (`folio_deposit_state(D - 1)`) |
| Received | deposit payments dated D (`is_deposit`, `kind = PAYMENT`, positive) |
| Applied | the increase of `applied` between D - 1 and D (the `DEPOSIT_APPLIED` lines of the day journal; the `DEPOSIT_RELEASE` of a closing folio in ON_CLOSE) |
| Refunded | refunds of a deposit dated D |
| Closing | the liability as of D; the total equals account 2310 as of D |
| Expected use | the arrival date; "overdue" when the arrival is past and there is no stay (a deposit held for a no-show or a cancellation with no fee yet) |

The movement identity `Opening + Received - Applied - Refunded = Closing` holds for each row and is asserted by a test for both release modes.

**Rounding.** All sums are of stored amounts; no rounding is introduced.

### 7.4 (d) Voids, corrections, discounts and allowances

**Question it answers:** what was taken back, reduced or given away, by whom, with whose approval and why.

**Parameters:** `from`, `to`, `kind` (below), `user_id`, `outlet_id`.

**Kinds, all folio and POS, one row for each event:**

| Kind | Rows | Source | Business date |
|---|---|---|---|
| `VOID` | a folio item reversed (`REVERSAL`) and a payment voided | `folio_items` where `transaction_type = REVERSAL`; `payments` where `status = VOIDED` (the void is a same-day reversal, so the business date is the payment's) | `folio_items.business_date` |
| `ADJUSTMENT` | a charge corrected after its day (`ADJUSTMENT`) | `folio_items` where `transaction_type = ADJUSTMENT`, not an allowance | idem |
| `ALLOWANCE` | an adjustment or a charge that posts to an allowance or rebate account | the same items whose `revenue_account_code` is a revenue account with a DEBIT normal side (4160, 4290, 4390) | idem |
| `REFUND` | a refund of a payment | `payments` where `payment_type = REFUND` | `payments.business_date` |
| `DISCOUNT` | a discount on a posted charge or a POS line | `folio_items.discount_amount > 0`; `pos_bill_lines.discount_amount > 0` (a bill discount is already allocated to the lines) | the folio item's date |
| `COMP` | a complimentary POS line or payment, **one row per bill under either treatment of the outlet** | `payments.payment_method = 'COMP'` under `PAYMENT_COMP`; under `DISCOUNT_100` the adjustment that cancels the line (`pos_bill_lines.is_comp`, reason "Complimentary: ...", net negative), which is not also listed as `ADJUSTMENT` or `DISCOUNT` | idem |
| `POS_VOID` | a POS line voided after it was sent | `pos_bill_lines.status = VOID` with `voided_item_id` (the reversal or adjustment it caused) | the business date of that item |

| Column | Source |
|---|---|
| Business date, time, kind | above |
| Folio, reservation or bill, outlet, guest or table, charge code, department | the item and its folio or bill |
| Original (the reversed item and its date) | `reverses_item_id`, or `voided_item_id` |
| Amount net, service, tax, gross (signed as the ledger reduces revenue) | the item's `net_amount`, `service_charge_total`, `tax_total`; the discount amount for a discount |
| Reason | `reason`, `void_reason`, `discount_reason`, `remarks` |
| Done by, approved by | `created_by`, `approved_by` / `void_approved_by` / `discount_by` |

**Totals:** by kind, by reason, by user (done by, with count and amount; the user with most voids stands out), by outlet, and the **share of the net revenue** of the period, so a manager can see 0.4 % of revenue taken back against 4 %. A POS line voided while `DRAFT` never reached the ledger (Q11) and is not shown.

### 7.5 (e) The Z report of the day for all drawers

**Question it answers:** what every drawer took and counted on the day, in one place.

**Parameter:** `date` (a closed business date, or the open one for an X-report of the day so far).

**One block for each cashier shift** that was closed on D, or was open at any time on D (a shift open across midnight when the setting allows it), with the figures of the existing Z report (11, rule 6a): shift number, drawer, cashier, opened and closed times, business date opened and closed, opening float, cash payments, cash refunds, cash city ledger receipts, pay-ins, pay-outs, drops, expected cash, counted cash, over/short, the reason and approver of a variance, the hand-over, the card, transfer, QRIS and other tenders taken, and the total takings. A shift still open at the time of the report is marked **X (interim)** and its counted cash is blank.

**Consolidation** (one table above the blocks): for each drawer and for the property, the same columns summed; **cash not on a shift** (cash taken while `require_shift_for_cash` was off); **voids from closed shifts landing in an open shift**; the total over/short.

**The check** (printed): the cash of all shifts plus the cash not on a shift equals the cash line of the cashier report for D (`reports.Cashier`), and the card/transfer/other lines agree with the summary's `payments_by_method`. A difference is printed, not hidden.

**Permissions and the pack.** The report needs `report.view` and `cashier.shift_manage`. In the pack the section is `Z_REPORTS`.

## 8. Data

### 8.1 Tables (migrations in section 10)

All amounts are `numeric(18,3)`; composite FKs to `properties (tenant_id, id)` and `(property_id, ...)` make a cross-tenant reference impossible; every query is scoped by `tenant_id` and `property_id`. Prefix `nap_` (night audit pack), as `fa_` and `banquet_` mark theirs.

```
nap_recipients
  id bigint identity PK
  tenant_id, property_id            FK properties (tenant_id, id)
  email       varchar(254) NOT NULL        CHECK shape: no spaces, one '@', no CR/LF
  name        varchar(100)                 who it is (a label, optional)
  sections    varchar(16)[] NOT NULL       CHECK: not empty, every element in the catalog (4), no duplicate
  is_active   boolean NOT NULL DEFAULT true
  created_at/by, updated_at/by
  UK  (property_id, lower(email))          nap_recipients_email_uk
  UK  (property_id, id)

nap_packs
  id bigint identity PK
  tenant_id, property_id
  business_date date NOT NULL              FK business_days (property_id, business_date)
  format_version smallint NOT NULL         CHECK >= 1
  status varchar(10) NOT NULL              CHECK IN ('COMPLETE', 'PARTIAL')
  built_at timestamptz NOT NULL, built_by bigint REFERENCES users (id)
  build_ms int NOT NULL                    CHECK >= 0    (the time the build took, for Q1 and the report of section 9.2)
  built_late boolean NOT NULL DEFAULT false              (a pack made by a rebuild, 5.5)
  UK  (property_id, business_date)         nap_packs_date_uk
  UK  (property_id, id)

nap_pack_sections
  pack_id bigint NOT NULL                  FK nap_packs (property_id, id) via (property_id, pack_id)
  property_id, tenant_id
  section varchar(16) NOT NULL             CHECK IN the catalog
  status  varchar(14) NOT NULL             CHECK IN ('OK', 'FAILED', 'OFF', 'NOT_AVAILABLE')
  format_version smallint NOT NULL
  data jsonb                               NOT NULL when OK, NULL otherwise (CHECK shape)
  error varchar(300)                       NOT NULL when FAILED (CHECK shape)
  built_late boolean NOT NULL DEFAULT false
  PK  (pack_id, section)

email_outbox  (changes of 00091's table)
  kind     CHECK adds 'NIGHT_AUDIT_PACK'
  nap_pack_id bigint                       FK nap_packs (property_id, id)
  nap_sections varchar(16)[]               the sections sent (a copy; the recipient may change later)
  nap_part smallint, nap_parts smallint    1 of 1, 1 of 3 (6.3)
  nap_pdf_sha256 char(64)                  the SHA-256 of the PDF as sent, lower-case hex; NULL until the message is SENT (Q6)
  nap_renderer   varchar(20)               the version of the renderer that drew it; NULL until SENT
  email_outbox_nap_sent_ck                 CHECK (nap_pdf_sha256 IS NULL) = (nap_renderer IS NULL), the hash has the shape '^[0-9a-f]{64}$', and a SENT pack row has both
  email_outbox_shape_ck                    replaced: NIGHT_AUDIT_PACK needs nap_pack_id, nap_sections, nap_part, nap_parts and language = 'en', and no reservation, event or document; the other kinds need these columns (and the two above) NULL
  INDEX email_outbox_pack_idx   (property_id, nap_pack_id, id DESC) WHERE nap_pack_id IS NOT NULL
  UNIQUE email_outbox_pack_queued_uk (nap_pack_id, lower(to_address), nap_part) WHERE status = 'QUEUED'

audit_logs  (one index, F5)
  INDEX audit_logs_bd_action_idx (property_id, business_date, action) WHERE property_id IS NOT NULL
```

No FK from `email_outbox` to `nap_recipients`: deleting a recipient (K2) must not be refused or erase history, and the outbox row already holds the address and the sections as sent. `nap_pack_sections` is append-only except `status/data/error/built_late` of a rebuilt `FAILED` row (a guard trigger refuses any other update, as the other snapshot tables do). A pack row is never deleted by the application; a trigger refuses `DELETE` on `nap_packs` and `nap_pack_sections`. `nap_packs` is added to `property_has_financial_data` (the currency lock, as in 37 F19) because its JSON holds money. No sequence is needed: the identity of a pack is `(property, business_date)`.

**Indexes for the reports** (to confirm with `EXPLAIN` on the demo property at the build, and add only what is missing): `folio_items (property_id, business_date, transaction_type)` for the corrections; `payments (property_id, business_date, status)`; `cashier_shifts (property_id, business_date_closed)`; `reservation_rooms (property_id, no_show_at)` is not needed (the audit row is the key).

**Down.** Each migration refuses when its rows exist (a pack, a recipient, an outbox row of the new kind), like 00091, and changes nothing then.

### 8.2 Errors, audit, tenancy

**Codes** (in `errors.go`, with the web `errorCodes` and the `en`/`id` texts, which the tests require): `PACK_NOT_FOUND` (404), `PACK_ALREADY_QUEUED` (409), `PACK_RECIPIENT_NOT_FOUND` (404), `PACK_SECTION_INVALID` (422), `PACK_TOO_LARGE` (the delivery state, not an HTTP error), `SECTION_NOT_REBUILDABLE` (409), `EMAIL_OFF` (409: a resend with e-mail not configured), `DATE_NOT_CLOSED` (422, the ledger reports). **Constraint names** mapped (a test verifies each exists): `nap_recipients_email_uk` → 409 `DUPLICATE_REQUEST` ("this address is already a recipient of the property"), `nap_recipients_sections_ck` and `nap_recipients_email_ck` → 422 `INVALID_VALUE`, `nap_packs_date_uk` → 409 `DUPLICATE_REQUEST`, `email_outbox_pack_queued_uk` → 409 `PACK_ALREADY_QUEUED`, `email_outbox_nap_sent_ck` → 422 `INVALID_VALUE` (a guard, never reached by a user), `email_outbox_shape_ck` (its text is widened: "an e-mail belongs to a reservation, to an event and a document, or to a pack").

**Audit** (same transaction, `audit.Writer.Write`, with the label helpers; the audit-coverage test lists them): `nap_recipient.created`, `nap_recipient.updated` (old and new sections, active), `nap_recipient.deleted` (address), `nap_pack.built` (date, sections OK and failed, recipients queued, `build_ms`), `nap_pack.rebuilt`, `nap_pack.resent` (date, addresses, sections), `nap_pack.downloaded` is **not** audited (a read). The address is in the audit entry because the recipient row can be deleted.

**Tenancy.** Another tenant's or an unassigned property: 404 `PROPERTY_NOT_FOUND`; a missing permission: 403 `PERMISSION_DENIED`, checked before the path and body are parsed. A recipient or a pack of another property of the same tenant is 404 as well. The worker reads as the tenant of the row (as it does for the confirmations).

### 8.3 API (`{P}` is `/api/v1/properties/{propertyId}`)

| Route | Permission | Notes |
|---|---|---|
| `GET {P}/reports/daily-flash?date=` | `report.view` | JSON, `?format=csv`, `.pdf` (with the signature block); the open day gives an interim flash |
| `GET {P}/reports/cancellations?from=&to=&kind=&source=&company_id=` | `report.view` | idem |
| `GET {P}/reports/guest-ledger?as_of=` and `GET {P}/reports/deposit-ledger?as_of=` | `report.view` | idem; `DATE_NOT_CLOSED` for the open day |
| `GET {P}/reports/corrections?from=&to=&kind=&user_id=&outlet_id=` | `report.view` | idem |
| `GET {P}/reports/z-reports?date=` | `report.view` + `cashier.shift_manage` | idem |
| `GET {P}/night-audit/packs?from=&to=&cursor=` | `nightaudit.pack_view` | the archive list: date, status, sections, recipients queued, deliveries by state |
| `GET {P}/night-audit/packs/{date}` | `nightaudit.pack_view` | the manifest: sections with status and size |
| `GET {P}/night-audit/packs/{date}/sections/{section}` | `nightaudit.pack_view` | the JSON; `?format=csv` where the section is tabular |
| `GET {P}/night-audit/packs/{date}.pdf?sections=` | `nightaudit.pack_view` | the PDF drawn from the snapshot (all OK sections by default) |
| `GET {P}/night-audit/packs/{date}/deliveries` | `nightaudit.pack_view` | the history |
| `POST {P}/night-audit/packs/{date}/resend` `{recipient_ids?}` | `nightaudit.pack_resend` | 409 `EMAIL_OFF`, `PACK_ALREADY_QUEUED` |
| `POST {P}/night-audit/packs/{date}/rebuild` | `nightaudit.pack_manage` | 5.5 |
| `GET/POST {P}/night-audit/pack-recipients`, `PATCH/DELETE {P}/night-audit/pack-recipients/{id}` | `nightaudit.pack_manage` (GET also `nightaudit.pack_view`) | the list is not paged (a property has tens of addresses at most) |

OpenAPI first (`api/openapi.yaml`), then `npm run gen:api`; a mutation takes an `Idempotency-Key` where the project already does.

### 8.4 Screens

- **Reports page:** five new entries with a date or period control, the table, CSV and PDF buttons, and the check lines of 7.3 and 7.5 in text (not by colour alone).
- **Night audit archive** (Reports, "Night audit archive"; `nightaudit.pack_view`): a table of days (date, status, sections, e-mail status such as "4 sent, 1 failed"), a row opens a drawer with the sections (download the PDF of all or of chosen sections, the CSV of a tabular section), the history of the deliveries, and **Send again** (choose recipients) and **Rebuild** where permitted.
- **Recipients** (Settings, End of day; `nightaudit.pack_manage`): the list with address, name, sections as a checklist with the catalog titles, active switch, delete with confirmation; "E-mail is off" when the sender is not configured.
- The texts are in `src/i18n` in English and Indonesian (the screens follow the user's language; the pack PDF and e-mail are English, K4). `data-testid` for the tests.

## 9. Locks, transactions and tests

### 9.1 Position in the lock order

**No new lock level, no change of the order (05 1.1).** The build runs inside the transaction of the run, after the steps that take locks, and only **reads** (plain `SELECT`, no `FOR UPDATE`) and **inserts** its own rows:

- the inserts into `nap_packs` reference `business_days (property_id, business_date)`; the key-share lock of that foreign key is on the row the run already holds `FOR UPDATE` (L1), so it waits for nothing;
- `email_outbox` and `nap_*` have no lock level (no one locks them as aggregates); a resend and a rebuild lock nothing but insert or update their own rows;
- no sequence is used (no L5).

The reads are of rows the run already locked or that no writer can change in this transaction (the day is held `FOR UPDATE`: every business-dated write is waiting for the commit).

### 9.2 Effect on the duration of the run

The pack adds about thirty aggregate queries, each by an indexed `(property_id, business_date)` range or by the open folios. The heaviest are the guest ledger (the items of folios with a balance: the open folios, tens or hundreds) and the daily flash report (three ranges of the month and the year to date: aggregates over `stay_charge_postings`, `folio_items`, `payments` and `gl_journal_lines` by business date or period, each for the day, MTD, MTD last month, MTD last year and YTD). The target is **at most 3 seconds more on the demo property and no more than 10 % of the run for a 300-room property**, measured at the build and stored in `nap_packs.build_ms`; a test records it and fails above a generous ceiling (to catch an accidental scan, not to benchmark). Rendering and sending cost the run nothing.

### 9.3 Tests

Real PostgreSQL (`dbtest`), the project's way (CLAUDE.md); constraints and races included.

| Area | Test |
|---|---|
| Reports | Each report's numbers **worked by hand** from a small ledger: the **daily flash report** with the example of 7.1 (occupancy, ADR, RevPAR, the pro rata budget MTD, MTD of last month with a 31-day month against a 30-day one, MTD of last year across 29 February, YTD in a fiscal year that starts in July), sold by room type adding up to `rooms.sold` of the summary, complimentary and house use by rate plan, walk-in, guests in house, ALOS, early and late check-out, covers per outlet and for banquet (with a bill without a guest count and a session that uses the `pax_guaranteed` fallback), the A/B/C groups by revenue account group (guest, company, master, outlet and event folios, a transfer that is left out): a **spa outlet** posting to `REV_OOD` lands in C, the **rental of a banquet room** in C, the **food and beverage of a banquet event** in B, an item with no revenue account in C "Unmapped" with its warning, and **A + B + C equals the revenue groups of the income statement** for the range; double occupancy (%) and guests per room with two decimals, the sum of the sources equal to the total, the tie-to-books row zero, GTP by cashier and by method adding up to the same figure, a blank budget for a count of rooms and for a day without a budget line, and the **control balance**: it is **0** for a day with posting, void, refund, deposit, transfer to the city ledger, banquet and POS, for MTD of the same month, and a deliberately broken fixture (a manual ledger row inserted by the test) prints a non-zero difference and still returns the report; cancellations with a fee, without a fee, a reinstated one, a bulk no-show of three lines; the guest ledger and the deposit ledger **for a past date after later days were posted**, in both release modes, with `Opening + Received - Applied - Refunded = Closing`, and the totals equal to `ControlSources` and the accounts 1210 and 2310; corrections of every kind (a reversal, an adjustment, an allowance account, a discount on a folio item and on a POS line, a comp, a refund, a POS void of a sent line and of a draft line that must not appear); Z report with two drawers, a shift open across midnight (setting off), cash not on a shift, and the check against the cashier report |
| Snapshot | The snapshot equals a live rebuild of the same date after later days posted (for each rebuildable section); `IN_HOUSE`, `NEXT_DAY`, `AUDIT_NOTES` and `OUT_OF_ORDER` refuse a rebuild; **`OUTLETS`** by hand for two outlets (net, service, tax, covers, bills, a void, a comp of each treatment, a discount; a bill reopened and closed on a later day counts on that day) and `OFF` when the plan has no POS module; **`OUT_OF_ORDER`** with an OOO and an OOS block, a block ending on D (not listed: the interval is half-open), a block cancelled before the close (not listed) and a block edited after (the snapshot keeps the old end date); the count equals the out of order and out of service rooms of `SUMMARY`; the catalog CHECK accepts the fifteen codes and refuses a sixteenth; a default recipient does not get `OUTLETS` or `OUT_OF_ORDER`; format version is stored; a failing section (injected) leaves the day closed, the pack `PARTIAL`, the section `FAILED`, no e-mail for a recipient who chose only failed sections, and the rest intact (savepoint) |
| Run | **A blocker rolls back the pack and the outbox rows** (no `nap_packs`, no `email_outbox` of the kind); a run with recipients queues one row for each active one, with the right sections, and none for an inactive one; a run with e-mail off queues nothing and builds the pack; **a sender that blocks or fails does not change the run** (the run test uses a sender that never returns: the run finishes, the worker is the only thing that waits) |
| Races | **Two runs at the same time:** exactly one pack and one set of rows (the advisory lock and `nap_packs_date_uk`). **Run against a resend:** a resend of a date before the commit gets `PACK_NOT_FOUND`; after the commit it succeeds; neither blocks the other. **Two resends to the same address:** one queued row, the other `PACK_ALREADY_QUEUED` (the partial unique index). **A recipient edited or deleted during the run:** the run sees one consistent list (READ COMMITTED reads the list once), the outbox row keeps the address and sections as sent. **A recipient deactivated between queue and send:** `SKIPPED` |
| E-mail | Compose: subject, body with and without the headline (`SUMMARY`/`FLASH` selected or not), the attachment name and that it opens as a PDF with the chosen sections only, in the catalog order; the split into numbered parts above the limit, with each part its own row, hash and retry; **the SHA-256 and the renderer version are written when the message is sent** (and equal the hash of the bytes the sender received), stay NULL on a failed row, and the history shows them; one section above the limit fails with `PACK_TOO_LARGE`; retry and give-up use the existing bounds; the history shows the address as sent after the recipient was deleted |
| Renderer | Deterministic: two renders of one snapshot give the same bytes; every section renders with no rows; long names wrap; money alignment at 0 and 3 decimals |
| Schema | The CHECKs (sections catalog, non-empty, no duplicate; shape of `nap_pack_sections`; the outbox shape for every kind), the unique keys, the append-only guard of a section, the refusal of `DELETE`, the Down refusing with data, up/down/up (`scripts/db-test.sh` through CI) |
| Security | Authorization matrix for every new route (403 before the path or body is parsed), tenant isolation (another tenant's pack, recipient and property are 404), the formula-injection guard on CSV |
| Web | The archive list and drawer, the recipients form with the sections checklist, the five report pages, "E-mail is off", resend errors; `errorCodes`, `noRawCodes`, `auditCoverage`, i18n key parity (en = id) |

## 10. The PRs (proposal)

Each PR is DB, backend, API, frontend, i18n and tests together (07-milestones). The first can be reviewed and used without the other two.

| PR | Migration | Content |
|---|---|---|
| **1. The five reports** | 00097: `audit_logs_bd_action_idx` and the indexes `EXPLAIN` shows missing (section 8.1; the daily flash report adds `stay_charge_postings (property_id, business_date)` and `stay_rooms (property_id, start_business_date)` to the ones to check) | Reports (a) to (e) as live reports with date or period, CSV and PDF, on the Reports page (the daily flash report is the largest: the statistics, the A/B/C revenue, GTP and the control, about half of this PR, and the PDF with the signature block); the new reports package files; permission use; tests of 9.3 for the reports; OpenAPI; doc update. No pack, no e-mail. |
| **2. The snapshot and the archive** | 00098: `nap_packs`, `nap_pack_sections`, their guards, the currency lock; `SetPackBuilder` in `nightaudit`; `internal/auditpack` | The 15 sections (including `OUTLETS`, `OFF` without the POS module, and `OUT_OF_ORDER`) with their JSON and format version, and the catalog CHECK of fifteen codes; the build in the run with savepoints; the pack PDF (cover page, sections) in `internal/documents`; the archive routes and screen (list, manifest, section JSON, PDF, rebuild); permissions `nightaudit.pack_view` and `nightaudit.pack_manage`; the warning `PACK_INCOMPLETE` in the preview; tests of the snapshot, the run and the renderer. No e-mail. |
| **3. Recipients, e-mail and history** | 00099: `nap_recipients`, the `email_outbox` columns (pack, sections, parts, **`nap_pdf_sha256`, `nap_renderer`**), the wider CHECKs, the partial unique index; `PMS_SMTP_MAX_BYTES`; `documents.RendererVersion` | Recipients routes and screen; queueing in the build; `compose` for the kind; size split; resend and the history routes and drawer; permission `nightaudit.pack_resend`; the warning `PACK_DELIVERY_FAILED`; `pms-seed` recipients for the demo property; tests of e-mail, races and the web; docs (README, 06-api, 13-feature-map, CLAUDE.md status). |

Screenshots in Indonesian for the review of each PR, in `docs/screenshots/nap-prN/` (gitignored). The numbers 00097 to 00099 are the next free ones when this is written; a PR takes the next free number at its time.

## 11. Stage 2 (a plan only; nothing here is decided)

Each is a report on the Reports page and, if the owner wants it, a section of the pack (the catalog gets a new code and a `format_version`, not a new mechanism).

| Report | What it needs |
|---|---|
| Room forecast 14 / 30 / 90 days | Rooms available, expected occupied (confirmed lines and open stays), occupancy %, expected ADR, arrivals and departures for each night. The availability engine (04 14.1) already computes the demand per night and type; the dashboard has a 14-night forecast. A stored forecast history is useful only for the pace report. |
| Pickup / pace | The change in rooms on the books for a future night between two dates ("pickup since last week", "this year against last year at the same lead time"). Needs **a daily on-the-books snapshot** of future nights, taken at the close (a new table by night of stay and date taken). This is the reason to keep the mechanism of this document general. |
| Production by segment, source, company, rate plan | Room nights, revenue, ADR and share by `reservations.source` (WALK_IN, PHONE, EMAIL, WEBSITE, OTA, AGENT, OTHER), `reservations.market`, the company, and the rate plan; a stay or a booking date basis. `market` is a free `varchar(30)` today: a controlled list (a master) may be needed first. |
| Nationality statistics | Guest nights by nationality, from the guest profile (`guests`); depends on how complete the nationality field is. |
| Foreign guests report | The foreign guests in house with passport, nationality, arrival, departure and visa data, for the immigration authority. Needs fields the guest profile may not hold (passport number, expiry). |
| High balance | Open folios above a threshold or a share of the credit limit, with age; close to the guest ledger of 7.3 (a threshold and a column). |
| Breakfast covers | Breakfast covers included in the room rate, for the covers rows of the daily flash report (K7). Needs the package or inclusion on the rate plan to say how many breakfasts a night carries; the rows of 7.1 count only the covers of outlets and banquet until then. |
| Day use | A stay that begins and ends on one business date, with its own rate, room-hours and report row. Needs a product decision and a change of `stays_dates_ck`; the daily flash report prints no day-use row until then (A1). |
| Payment detail by bank | The payments of the daily flash report broken down by bank or acquirer (card batches, bank transfer accounts), beyond the fixed methods of A6. Needs the bank or acquirer on the payment row. |
| Rate variance | Posted room rate against the rate plan's grid for the night (the `nightly` snapshot against the price in force), with the user and the reason of an override. Needs the override reasons to be recorded (the override permission exists). |

## 12. Questions decided (owner, 2026-10-29)

Q1 to Q19 were put to the owner with a recommendation each. **All were decided as recommended**, with two points settled in the text of the decision: **Q6** (the PDF is not stored, but its SHA-256 and the renderer version are kept in the outbox row, a new pair of columns in the migration of PR 3) and **Q13** (accepted, with the controls listed). K1 to K6 were never among them. No question is open.

| # | Question | Decision |
|---|---|---|
| Q1 | A section fails while the pack is built at the close: block the close, or close and mark the section failed? | **Close and mark it** (savepoint for each section, the pack `PARTIAL`, a warning in the next preview, rebuildable). A report must not be able to stop the close of the day (5.2). A database error outside a section still rolls the run back. |
| Q2 | Which sections does a new recipient get? | **`SUMMARY` and `FLASH`.** The sections with guest names or the cash (`GUEST_LEDGER`, `Z_REPORTS`, `CORRECTIONS`, `IN_HOUSE`, `NEXT_DAY`, `CANCELLATIONS`) are opt-in, and so are `OUTLETS` and `OUT_OF_ORDER`. |
| Q3 | Is the headline (occupancy, ADR, RevPAR, revenue, payments) always in the body? | **Only when the recipient has `SUMMARY` or `FLASH`.** A recipient who is meant to see the in-house list only should not receive revenue in the body. |
| Q4 | The budget of the flash for MTD: the budget module has months only. | **Pro rata of the month** (elapsed days n over the days of the month), labelled "pro rata" on the page and in the PDF. **Amended by K7:** only the MTD column has a budget; there is no daily and no YTD budget, and a count of rooms or guests never has a money budget (7.1). A budget by day of week is a later extension. |
| Q5 | "The same day last year": the same calendar date, or the same weekday (364 days before)? | **The same calendar date** as the owner wrote (the day and MTD of the year before; MTD last month compares days 1 to n of the month before, K7); a second column with the same weekday can be added if the owner wants it. A 29 February compares with 28 February of the year before. |
| Q6 | Store the first PDF that was sent? | **The PDF is not stored; its SHA-256 and the renderer version are.** The JSON is the record and the PDF is drawn (5.3). The hash of each PDF that is sent and the version of the renderer are written in the `email_outbox` row (`nap_pdf_sha256`, `nap_renderer`, migration 00099 of PR 3; 6.2 and 8.1), so a difference with a file a recipient holds can be proven without keeping the file. |
| Q7 | Attachment limit and what happens above it. | **7 MB for a message (`PMS_SMTP_MAX_BYTES`), split into numbered messages by section; a section above the limit fails with `PACK_TOO_LARGE` and stays downloadable** (6.3). |
| Q8 | How is a failed delivery noticed? | **A warning in the next night audit preview and a mark in the archive**; no push or second e-mail (a failed e-mail would be told by e-mail). |
| Q9 | E-mail is off (no SMTP host) when the pack is built. | **Build the pack, queue nothing, show "E-mail is off"**; once configured, the user resends by hand. No automatic catch-up of old days. |
| Q10 | The date of a cancellation or a no-show: from the audit log (an index) or new business-date columns on the reservation tables? | **The audit log with the index of 00097** (F5): the audit row is written in the same transaction with the business date, is append-only, and needs no change to the tables of the reservations. New columns can come with a backfill if the report proves slow. |
| Q11 | A POS line voided while still a draft. | **Not in the report**: it never reached the ledger and has no business date. The voids of sent lines (which reach the kitchen and the folio) are. |
| Q12 | Packs for the days before the feature, and who may rebuild. | **No automatic backfill.** A user with `nightaudit.pack_manage` may build a pack for a closed day from the ledger (four sections marked not available), no e-mail, within 400 days. |
| Q13 | The pack carries guest names to addresses that are not users. | **Accepted, with the controls above:** the sections with names are opt-in; `nightaudit.pack_manage` is its own permission; every change of a recipient is audited with the address; the SMTP setting `PMS_SMTP_TLS` defaults to `starttls`. The owner accepted that these PDFs leave the system to addresses that are not users, under these controls. |
| Q14 | A resend to an address that is not a recipient (a one-off). | **Not allowed.** Add the address, resend, deactivate it. This keeps every address in a list that is audited. |
| Q15 | Does the corrections report include refunds, and the credit notes of the city ledger? | **Refunds yes** (they are money returned with an approval); **city ledger credit notes and write-offs no** (their own documents and reports, 10). |
| Q16 | A shift open across midnight (the setting is off) in the Z report of the day. | **Shown as X (interim)** with the cash counted left blank; the next day's Z report shows it closed. |
| Q17 | Retention of the packs. | **Keep all.** A pack is tens of kilobytes; one year is a few megabytes for a property. Revisit after five years. |
| Q18 | The year of the YTD column: the fiscal year of the budget calendar, or the calendar year? | **The fiscal year of the budget calendar** (`YearStart`), so that the actual and the budget cover the same months (K7: the YTD column shows actuals only); for a hotel whose year starts in January the two are the same. |
| Q19 | What is "total revenue" in the flash and the e-mail? | **Net revenue**, the figure of the income statement (service charge and tax are liabilities, F7), with service charge and tax as memo lines so a reader used to a gross figure can find it. **K7 adds the GTR** (net + service + tax) below them, so the gross figure is a line of the daily flash report too. |
