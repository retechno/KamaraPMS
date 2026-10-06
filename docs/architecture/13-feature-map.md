# 13. Feature map: what exists, and where to find it

A trace table for the whole system. For a feature it gives the migration, the backend package, the API area, the screens, the design document and the commit. It does not repeat the designs: follow the pointers.
Status of every row: built, tested and on `main`. How to read the columns: **Package** is `internal/<name>`; **Screens** are folders and views of `web/src/views`; **Design** is a file of `docs/architecture` (the README holds the long
description of each feature, under the bold name used here).

## 1. How a request travels (the same in every module)

`api/openapi.yaml` (contract) → `internal/<module>/http.go` (thin handler) → `service.go` (use case; opens `TxManager.WithinTx`, takes locks in the global order, writes the audit entry) → `store.go` + `queries.sql` (sqlc, generated in
`<module>db/`) → PostgreSQL (`migrations/`, constraints named and mapped to error codes in `internal/platform/db/errors.go`) → web (`web/src/api/schema.d.ts` is generated from the contract, `views/`, `i18n` en and id).
Wiring is in `internal/app/app.go` (and `internal/rooms/roomstest/roomstest.go` for tests). Rules that never bend are in `CLAUDE.md`.

## 2. Modules of the backend

| Package | What it does | Main tables (migration) | Screens | Design |
|---|---|---|---|---|
| `platform/*` | Config, DB pool and `TxManager`, locks (`db.LockRows`, global order), errors (`apperr`), clock, `civil` dates, auth and permissions, idempotency | 00001 | none | 05-transactions-locking |
| `iam` | Login, rotating sessions, roles and permissions, approvals (`VerifyApproval`) | tenants, users, roles (00002, 00012, 00017) | `setup/` Users, Roles | 01-domain-model |
| `tenancy` | Tenants, properties, the business day and document sequences | properties, business_days (00003) | `setup/` Properties | 01, 04 |
| `rooms` | Room types, rooms, OOO/OOS blocks, bed types | rooms, room_types (00004, 00037) | `rooms/` Rooms, RoomTypes, RoomBlocks, BedTypes | 01, 02 |
| `housekeeping` | Cleaning state per room, cleaning tasks, assignment by floor | (00004, 00025) | `rooms/` Housekeeping, HousekeepingTasks, RoomStatus | 04 |
| `maintenance`, `lostfound` | Maintenance requests; lost and found items | (00026), (00027) | `rooms/` Maintenance, LostFound | 04 |
| `guests` | Tenant-wide guest profiles and search | guests (00005, 00013) | `guests/` | 01 |
| `rates` | Rate plans, grid, price lookup, yield rules, free night quotas, occupancy kinds, reference plan | (00007, 00036, 00038, 00039, 00040) | `rates/` RatePlans, RateGrid, YieldRules, FreeNightQuotas | 01, 04 |
| `availability` | Inventory engine: rooms sellable per type and night; the availability calendar | (derived) | `reservations/` AvailabilityCalendar, TapeChart | 04 |
| `reservations` | Bookings, room lines, nightly price snapshot, rate override approval, free room approval | (00008, 00016) | `reservations/` | 04 |
| `frontdesk` | Check-in, walk-in, reverse check-in, stay changes, extended stay prices | stays (00009, 00018) | `frontdesk/` | 04 |
| `billingconfig` | Taxes (VAT, local, other), service charges, charge codes (revenue account, default department) | (00006, 00014, 00041) | `billing/` ChargeCodes, Taxes | 03 |
| `chargecalc`, `expected`, `roomcharge` | Charge Calculation Engine (the only place that computes tax and service), expected charges, room charge posting | none own | none | 03 |
| `folios` | Guest ledger: folios, append-only items, payments, corrections by reversal, card fee snapshot, department snapshot | folios, folio_items (00010, 00015, 00017, 00020, 00035) | `billing/` Folios, Folio, Cashier | 03 |
| `nightaudit` | Orchestrates the day close: room charges, journal of the day, next business day | (00003) | `nightaudit/` | 04 |
| `companies`, `groups` | Corporate accounts, booking groups | (00022) | `accounts/` Companies, Groups, GroupDetail | 01 |
| `cityledger` | Receivables of companies: invoices, receipts and allocations, credit notes, write-offs, overdue, reminders, late fee | (00023, 00024, 00045, 00046) | `accounts/` CityLedger, CityLedgerAccount, CityLedgerOverdue | 10 |
| `accounting` | Chart of accounts (USALI), system account map, manual journals, day close journal, periods, fiscal years, reports (trial balance, income statement, balance sheet, cash flow, department report), department rule | gl_accounts, gl_journals, gl_journal_lines (00019, 00028 to 00030, 00051 to 00054) | `accounting/` | 03, 12 |
| `payables` | Suppliers, bills (with input VAT), payments, credit notes of suppliers, aging | (00031, 00042, 00055) | `payables/` | 03, 14 |
| `bankrec` | Bank accounts, statement import, matching, adjustments, card settlements (MDR and the VAT on it), reconciliation | (00032, 00033, 00048, 00056) | `bank/` | 11, 15 |
| `taxfiling` | Monthly returns of the taxes collected, payments, input VAT and credit carried forward, liability report | (00034, 00041 to 00043) | `tax/` TaxReturns, TaxLiability, TaxProfiles, TaxStatus | 09 |
| `taxinvoice` | Tax invoices (faktur pajak) of a PKP property and their export | (00044) | `tax/` TaxInvoices | 09 |
| `shifts` | Cashier shifts, the cash drawer, drops, pay-in and pay-out, cash over and short | (00047) | `billing/` CashierShift | 11 |
| `budget` | Budgets per fiscal year (versions, grid, spread, CSV, approval), statistics, budget against actual, by department | (00049, 00050, 00053) | `budget/` | 11, 12 |
| `departments` | Departments and sub-departments, the accounting dimension | departments (00051) | `accounting/` Departments | 12 |
| `reports`, `documents` | Operational and money reports (JSON, CSV, PDF); printed documents, confirmation e-mail | none own (00021) | `reports/` | 04, 07 |
| `audit`, `auditlog`, `notifications` | Append-only audit trail (writer, reader), e-mail outbox | audit_logs (00011, 00021) | `audit/` | 01 |
| `app` | Composition root, HTTP routing, end-to-end API tests | none | none | 06 |

Commands: `cmd/api` (server), `cmd/migrate`, `cmd/pms-admin` (tenant and admin), `cmd/pms-seed` (demo rooms and rates).

## 3. Features added in the recent stretch, in order

Each row: what it is, migration, backend, API, screens, design, commit (`git show <hash>`).

| # | Feature | Migration | Backend | API area | Screens | Design / README | Commit |
|---|---|---|---|---|---|---|---|
| 1 | Complimentary and house use rooms, free rooms report, reference plan, quota and approval | 00038 to 00040 | `rates`, `reservations`, `reports` | rate plans, reservations, reports | `rates/` FreeNightQuotas, `reports/` | README: Free rooms | `229070d`, `6b1a192`, `28dc9e1`, `a11b3b9` |
| 2 | Rate overrides with reason and approval (`reservation.override_rate_approve`), price editor | none | `reservations`, `frontdesk` | reservations | `reservations/`, `frontdesk/` | README: Rate overrides | `caf115a`, `0825def` |
| 3 | Bed types and the availability calendar | 00037 | `rooms`, `availability` | rooms, availability | `rooms/` BedTypes, `reservations/` AvailabilityCalendar | README: Bed types | `0a93964` .. `b0ea72b` |
| 4 | Indonesian PDFs, CSV and server errors | none | `documents`, `reports`, `platform` | `lang` parameter | all | README: Indonesian | `3d01229`, `2792f36` |
| 5 | PKP status and input VAT on supplier bills | 00041, 00042 | `billingconfig`, `payables`, `taxfiling` | taxes, bills, tax profiles | `tax/` TaxStatus, `payables/` Bills | 09 | `4b83fae`, `e200486` |
| 6 | Input side of the VAT return, VAT credit carried forward | 00043 | `taxfiling` | tax returns | `tax/` TaxReturns | 09 | `b4b8af4` |
| 7 | Tax invoices (faktur pajak) and export | 00044 | `taxinvoice` | tax invoices | `tax/` TaxInvoices | 09 | `1c60c59` |
| 8 | Credit notes and write-offs of the city ledger, tax of credit notes in the return | 00045 | `cityledger`, `taxfiling` | city ledger | `accounts/` CityLedgerAccount | 10 | `3142424`, `b7baf20` |
| 9 | Overdue invoices, reminders, late fee | 00046 | `cityledger` | city ledger | `accounts/` CityLedgerOverdue | 10 | `033777f` |
| 10 | Cashier shifts and the cash drawer | 00047 | `shifts` | cashier shifts | `billing/` CashierShift | 11 | `2d9cf39` |
| 11 | Card fee rules, fee snapshot, expected card settlements | 00048 | `bankrec`, `folios` | bank, folios | `bank/` BankCards | 11 | `fc15d4a` |
| 12 | Budget: versions, grid, spread, fill from actuals, CSV, approval, vs actual (JSON, CSV, PDF) | 00049 | `budget` | budgets | `budget/` | README: Budget; 11 | `79c9a42` |
| 13 | Cash flow statement, indirect and direct | none | `accounting` | accounting reports (`method`) | `accounting/` Statement | README: Cash flow; 11 | `49f1a30`, `f975d4d` |
| 14 | Budget statistics (rooms available, sold, ADR) | 00050 | `budget` | budgets/statistics | `budget/` | README: Budget statistics | `57f19f7` |
| 15 | Departments, step 1: master and columns | 00051 | `departments`, `billingconfig`, `payables` | departments | `accounting/` Departments | 12 | `7297ee9` |
| 16 | Departments, step 2: posting carries the department (folio snapshot, day close by department) | 00052 | `folios`, `accounting` | none new | none | 12 | `85632e3` |
| 17 | Departments, step 3: the department report | none | `accounting` | accounting/department-report | `accounting/` DepartmentReport | 12 | `76b783d` |
| 18 | Departments, step 4: budget by department, drill-down | 00053 | `budget` | budgets (department rows, department-vs-actual) | `budget/` | 12 | `f60ddd4` |
| 19 | Departments on system journals: bank adjustment, card fee, credit note line, write-off, pay-in and pay-out, tax penalty | none | `bankrec`, `cityledger`, `shifts`, `taxfiling` | those requests take `department_id` | their forms | README: Departments on system journals | `6917afc` |
| 20 | Department on the cash over and short line of a shift close | none | `shifts` | cashier shifts close | `billing/` CashierShift | same | `b644829` |
| 21 | Department rule of an account: NONE, OPTIONAL, REQUIRED, default department, one gate for every journal, setup check | 00054 | `accounting` (`deptrule.go`, `deptsetup.go`), `folios`, `billingconfig`, `departments`, and the posting modules | accounts, accounting/department-setup | `accounting/` ChartOfAccounts, Departments | 12 (step 5) | `098022b` |
| 22 | Shift handover (cashiers, handovers) and the Z and X report of a shift (JSON and PDF), other tenders on the report | none | `shifts` (`handover.go`), `documents` (`shiftreport.go`) | cashier/cashiers, cashier/handovers, cashier/shifts/{id}/report(.pdf) | `billing/` CashierShift | README: Shift handover; 11 | `ac36014` |
| 23 | Supplier credit notes: against a bill, journal mirror, apply the rest to other bills, input VAT on the return (claims of credit notes), credit in aging and supplier list | 00055 | `payables` (`credits.go`), `taxfiling` (claims) | payables/credit-notes, bills (`credited`), aging, tax returns (`source`) | `payables/` CreditNotes, Bills, Aging, `tax/` TaxReturns | README: Supplier credit notes; 14 | `dde98f8` |
| 24 | VAT on the card commission (MDR): VAT rate on the rule, VAT snapshot on payments, expected VAT and net, settle with the final VAT and the frozen treatment, claims on the VAT return, screens | 00056 | `bankrec` (`cardfee.go`, `settlementvat.go`), `folios`, `cityledger`, `taxfiling` (claims) | bank/card-fee-rules, card-settlements (+expected), settlement-preview, settle, tax returns (`source` SETTLEMENT) | `bank/` BankCards, BankReconcile, `tax/` TaxReturns | README: VAT on the card commission; 15 | `4d6a9da`, `f0faf77`, `cded281`, `988dd43`, `d3ed9ea` |
| 25 | Bed variants: bed type of a room required, a bed kept on a reservation line with its own stock (two-line availability rule), supplement per rate plan, room type and bed type, variants in search and calendar | 00057 | `rooms`, `availability` (`bed.go`), `reservations`, `frontdesk`, `rates` (`bedadj.go`, `bedprice.go`) | rooms (bed type required), reservation lines (`bed_locked`), rate-plans/{id}/bed-adjustments, availability (`beds`), calendar (`by_bed`) | `rooms/` Rooms, `reservations/` NewReservation, ReservationDetail, AvailabilityCalendar, `frontdesk/` Arrivals, `rates/` BedSupplements | README: Bed variants; 16 | `42f955a`, `0a2d81c`, `af1072e`, `18e9340`, `c22ff60` and the commit of step 6 |
| 26 | Sales restrictions, grid only (stop sell, closed to arrival and departure, minimum and maximum stay per room type and rate plan, one precedence, bulk fill, effective view); **not yet enforced on any sale** | 00059 | `availability` (`restrictions.go`), `rates` (`restrictions.go`) | rate-restrictions, rate-restrictions/effective | `rates/` Restrictions | README: Rate restrictions; 18 | (commits are added when the restrictions are enforced) |

## 4. Cross-cutting rules, and the place each one lives

| Rule | Where |
|---|---|
| Business date is the open `business_days` row, never the server date | `tenancy.Service.CurrentBusinessDay`, `RequireOpenBusinessDay`; `time.Now` is lint-forbidden outside `platform/clock` |
| Money is `decimal.Decimal`, `numeric(18,3)`, rounded at the property's decimals | `platform`, `chargecalc` |
| Locks only through `db.LockRows` / `db.EnterLockLevel`, in the global order | `platform/db/locks.go`; order list in `CLAUDE.md` and 05 |
| Ledgers are append-only; corrections are reversals | `folios` (items), `accounting` (journal lines), DB triggers |
| Journals are written only by `accounting` (the Poster for other modules, `PostDay` for the day close, `PostManual`) | `accounting/posting.go`, `journals.go` |
| Department rule is applied at one gate, history is never rewritten | `accounting/deptrule.go` (`lineDepartment`), `Poster.ResolveDepartment`, `folios/posting.go` (`requireDepartment`) |
| Every constraint has a code | `platform/db/errors.go`; a test checks each name exists |
| Every state change has an audit entry in the same transaction | `audit.Writer.Write` |
| Every text is an i18n key (en, id); backend error codes need a text | `web/src/i18n/locales`, `errorCodes.test.ts` |
| Generated code is never edited | `schema.d.ts` (`npm run gen:api`), `*db/` (`scripts/sqlc.sh generate`) |

## 5. How to trace a feature

1. Find its row in section 3; read the **Design** file first.
2. `git show <commit>` lists every file of that step (the steps of a feature are separate commits).
3. Migration: `migrations/000NN_*.sql` (Up and Down). Constraint names there are in `platform/db/errors.go`.
4. Backend: `internal/<package>/` (`model.go` rules, `service.go` use cases, `queries.sql`, `http.go`); tests beside them (`*_test.go`), API end-to-end tests in `internal/app/*_api_test.go`.
5. API: search the path in `api/openapi.yaml`; the table of rows and permissions is in `06-api.md`.
6. Screens: the view named in the table, its test beside it (`*.test.ts`), texts under the key group of the same name in the i18n files.
7. Checks that prove it: `scripts/db-test.sh` (schema), `go test ./...`, `cd web && npm test`.

## 6. Known gaps, by feature (details in 08-backlog.md and the README)

- Departments: closing entries by department (a year closes each account in one line); allocation of a shared expense over departments; a department budget as its own CSV or PDF.
- Budget: a button that fills room revenue from sold rooms x ADR; statistics in CSV and PDF.
- Cashier: a Z report of a whole business day over all drawers; a drawer lock that refuses other users.
- Card: a void or correction of a settlement (a posted settlement is final); the VAT of the other bank fees (the `Adjust` of a statement line).
- Supplier credit notes: a refund received from the supplier; a credit note without a bill; a PDF.
- Language: numbers in Indonesian field errors; confirmation e-mail language; bed counts per room type.
- No screen was looked at in a browser by the assistant; the owner checks them.
