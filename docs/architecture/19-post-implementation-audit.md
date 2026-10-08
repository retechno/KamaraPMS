# 19. Post-implementation audit (after Architecture 18)

Status: **audit only, written on 2026-10-07 from `main` at `fbecf6f`** (156 commits). **Update, same day: F-01 and F-02 are resolved by `c38ca68` (CI run 17 is green, including the race detector over the whole suite); sections 1, 17, 18 and 20 carry a line saying so, and the rest of the text is the audit as it was written. Later the same day: P0 #3 (deployment, F-03 and F-05) was verified and CI run 18 is green; see "P0 #3 final verification" after F-05 in section 17. Then P0 #4: backup and restore were built and a real restore was verified (F-04, section 14, `docs/backup-restore.md`); CI run 19 for `3a43178` is green. Then P0 #6: the night audit refuses a day it cannot journal (F-15, the update under F-15).** It changes no migration, Go code, API, OpenAPI, frontend or test; this file is the only change (as written; the later steps changed code, see the updates). Language of the audit: English, like the other documents; the owner's chat is in Indonesian.

How to read it. Every claim is tagged **VERIFIED** (a file, a line, a command or a test run that was seen) or **UNVERIFIED** (it could not be proved from the repository or from what could be run). A feature or finding is classified **DONE**, **PARTIAL**, **MISSING**, **DOCUMENTATION DRIFT** or **UNVERIFIED**. Nothing is called done because a document says so.

What was run for this audit (all read-only for the code; the scratch database was created and dropped):

| What | Result |
|---|---|
| `go build ./... && go vet ./...`, `scripts/lint.sh` (golangci-lint v2.14.0), `scripts/sqlc.sh diff` | clean, 0 lint issues, no stale generated code (on the audit machine) |
| `go test -p 1 -cover ./...` | 43 packages with tests; 42 passed on the first run, `internal/rates` failed only because Docker could not start its PostgreSQL container (reaper timeout) and passed when run alone (54.1%) |
| `scripts/db-test.sh` with `postgres:16-alpine` (run after the last migration) and with `postgres:18-alpine` | 514 schema tests and up/down/up pass on both |
| `go test -race` | cannot run on the audit machine (`CGO_ENABLED=0`, no C compiler). Run inside the `golang:1.26-trixie` container for the nine packages with the most concurrency: **`folios`, `roomcharge`, `nightaudit`, `frontdesk`, `reservations` and `tenancy` passed with no race report**; the system then stopped the run for low memory, before `shifts`, `cityledger` and `payables`. It was not restarted (F-02) |
| Web: `npm run type-check`, `npm test`, `npm run build` | 736 tests in 99 files pass; type-check and build clean (run at the last commit) |
| Catalog queries on a scratch database migrated to version 62 (dropped afterwards) | 101 tables, 434 foreign keys, 314 CHECK constraints, 123 unique constraints, 387 indexes, 167 triggers |
| GitHub Actions status of `main` through the public API | **every one of the 16 runs failed**: see F-01 |
| Static scans written for the audit (routes, SQL scoping, service authorization, UI patterns) | results quoted where used; the scripts are heuristics and are said to be so |

## 1. Executive Summary

**Verdict: CONDITIONAL PILOT** (section 20). The product is much further along than a pilot needs, and Architecture 18 is implemented as decided. What stands between it and a pilot is not features: it is that **the safety net described in `CLAUDE.md` is not actually running**, and that **there is nothing to deploy or restore with**.

The findings that decide the verdict:

1. **F-01 (Critical): CI has been red on every run since the first commit.** *(Resolved, see the update on F-01.)* The cause is an exit code 126 on the steps that call `scripts/sqlc.sh` and `scripts/db-test.sh`: the scripts are stored with mode `100644` in git, so a Linux runner cannot execute them. The lint, the race-detector test run, the sqlc freshness check and the schema tests on PostgreSQL 16 and 18 have therefore **never run in CI**. Locally everything is green, including a new run of the schema tests on PostgreSQL 18, so the fix is probably small, but until it is made none of those guarantees is evidence. The "all checks pass" reported after each step of this project was true of the local machine; the CI status was not consulted, which is how this stayed unseen for 16 runs.
2. **F-02 (Medium): the race detector has run on only 6 of the 43 test packages (all clean) and never in CI** *(resolved: the whole suite passes under `-race` in CI run 17)*, although the code base has 57 concurrency tests.
3. **F-03 and F-04 (High): there is no deployable artefact and no backup, restore or disaster-recovery procedure.** No Dockerfile, no production compose, no reverse-proxy or TLS guidance, no place where the SPA is served, and not one line about backup, RPO or RTO. *Update, later the same day: a deployment recipe exists (P0 #3) and backup scripts with one real restore exist (P0 #4); an agreed RPO and RTO, a backup schedule, an off-machine copy and point-in-time recovery do not. See F-03, F-04 and the verdict.*
4. **F-05 (High): the rate limits trust only the connection address.** Behind the reverse proxy that production needs, every client is the proxy: one shared bucket for the whole hotel, and the login and approval throttles are in memory per process.
5. **F-06 (Medium): reverse check-in leaves company folios open on a cancelled stay**, and a payment on such a folio is stranded there. Small, but it is money.

Architecture 18 itself: **DONE with caveats** (section 3). Every decision of the document is in the code and in tests. The caveats are the reverse check-in gap above, test requirements of the document that were not all written (the net-zero of a transfer through the day-close journal, INCLUSIVE items, the end-to-end case), and a documentation that still describes the work as "not built".

Financial integrity is strong where it is enforced by the database (25 append-only triggers, 45 no-truncate triggers and state-guard triggers on the documents that may change state, a deferred balance check on every journal, `numeric` money, composite foreign keys, a single insert site for folio items, payments and journal lines) and weaker where it is enforced by convention (the rule that a ledger row is only written on the OPEN business day: F-07).

Tenant isolation is strong by construction: 617 of the 669 SQL queries filter by tenant **and** property, and the other 52 are queries on tenant-level tables or internal workers. What is missing is a structural test that proves it for every route (F-10).

**Transaction Group / Split Bill** is recorded as PLANNED in section 16 and in the roadmap as **P1**, after the P0 items and after a short design that settles one question the evidence raises: folio items and payments are append-only, so a group cannot be a column that is updated later.

Counts that were measured, not copied: 62 migrations, 101 tables, about 56 thousand lines of Go outside tests and generated code and 34 thousand in tests, 771 Go tests (+33 sub-tests) in 142 files, 514 schema tests, 736 web tests, 295 routes (the OpenAPI test proves the contract lists exactly these), 87 views.

## 2. Current System Inventory

| Area | Measured state |
|---|---|
| Repository | 156 commits on `main`, working tree clean, no secret tracked (`.env` and `bin/` are ignored; `git ls-files` shows only `.env.example`) |
| Backend | Go modular monolith, 35 packages under `internal/` (`platform` has 10 sub-packages), commands `api`, `migrate`, `pms-admin`, `pms-seed` |
| Database | PostgreSQL, 62 migrations (all with a Down), 101 tables including `goose_db_version`: every table has a primary key, every table with `property_id` also has `tenant_id` |
| Money | 88 columns `numeric(18,3)`, 1 `numeric(18,2)` (`reservation_room_rates.grid_rate`), 1 `numeric(18,4)`; no float or real column; `float64` appears in code only for PDF layout and the rate limiter |
| API | 295 routes found in the handlers; `TestOpenAPIDescribesEveryRoute` fails if a route is missing from `api/openapi.yaml` or the other way round; 22 creating POST routes carry an `Idempotency-Key` (charges, adjustments, payments, deposits, refunds, city ledger transfers, journals, bills, supplier payments and credit notes, receipts, invoices, tax returns, reservations, check-in, walk-in, check-out); the other state-changing routes are guarded by a version or a status |
| Frontend | Vue 3 + TypeScript, 134 `.vue` files of which 87 are views, English and Indonesian, types generated from the OpenAPI file; access token in memory, refresh token in an httpOnly cookie |
| Tests | Go: 771 tests + 33 sub-tests; web: 736; schema: 514. Statement coverage by package (Go): median 63.8% over the 32 domain packages, lowest `auditlog` 43.6%, `groups` 48.8%, `iam` 52.5%, `rooms` 53.1%, `rates` 54.1%; highest `chargecalc` 98.3%; platform packages 63% to 100% |
| CI | one workflow with three jobs (Go: vet, sqlc diff, lint, `go test -race`; schema on PostgreSQL 16 and 18; web). **All red** (F-01) |
| Operations | `/healthz` and `/readyz`, structured logs with request id and access log, graceful shutdown, configuration validated at start (JWT secret of at least 32 characters, production refuses insecure cookies), `PMS_MIGRATE_ON_START` default off. No Dockerfile, no metrics, no backup procedure |

## 3. Architecture 18 Verification

Status of each decision of `18-architecture-decisions.md`, checked against the code, the database and the tests.

### 3.1 Folio model

| Item | Status | Evidence |
|---|---|---|
| Several folios per stay | **DONE** · VERIFIED | `migrations/00060_folio_payer.sql:14` `folios_stay_payer_uk` on `(stay_id, folio_type, COALESCE(bill_to_company_id, 0)) WHERE stay_id IS NOT NULL` (seen in the catalog of a database at version 62); schema tests (8) and `TestStayHasOneFolioPerPayer` |
| GUEST and COMPANY, payer rules | **DONE** · VERIFIED | `folios_type_ck`, `folios_payer_ck` (`COMPANY` exactly when `bill_to_company_id` is set), `folios_company_fk` composite to `companies (property_id, id)`; `MASTER` is refused by the CHECK (schema test "a master folio is not built yet") |
| Company billing instructions | **DONE** · VERIFIED | `00061`, `internal/folios/instructions.go`, `GET/PUT .../billing-instructions`, screen `BillingInstructions.vue`; tests: 8 in `instructions_test.go`, 4 in `roomcharge/routing_test.go`, `nightaudit` blocker test, API flow test, 5 component tests |
| Resolver is the single source of the target folio | **DONE** · VERIFIED, with a note | `internal/folios/routing.go:71` `For` and `:85` `resolve`; the expected-charges loader asks `ResolveRoomTargets` (`expected/loader.go:80`); a grep of the repository finds no other decision of a posting target. Note: `ResolveTarget` (one charge) is called only by tests, because manual charges name their folio, as the design says |
| Default routing to the guest folio | **DONE** · VERIFIED | `TestResolveTargetIsTheGuestFolio`, `TestInstructionOpensTheCompanyFolioOfAStayInHouse` (removing a rule returns to the guest folio) |
| Closed routing target is a blocker, never a fallback | **DONE** · VERIFIED | `expected/model.go:30` reason `ROUTING_TARGET_CLOSED`, evaluated at `:211` before `NO_OPEN_FOLIO`; `TestRoutedNightsFollowTheirTarget`, `TestClosedCompanyFolioBlocksTheNight` (nothing posted, preview says the same), `TestAClosedCompanyFolioBlocksTheNightAudit` (blocked and rolled back) |
| Company folio created eagerly | **PARTIAL** · VERIFIED | created by `AttachStayFolio` at check-in and by `SetBillingInstructions` for a guest in house, before any posting run takes its locks. **Gap: reverse check-in does not deal with company folios** (F-06) |
| Invoice buyer and tax invoice buyer | **DONE** · VERIFIED | `documents` invoice of a company folio is addressed "Bill to" the company (`TestInvoiceOfACompanyFolioIsAddressedToTheCompany`); the tax invoice of a company folio takes the company as buyer whatever the request says (`TestACompanyFolioHasTheCompanyAsBuyer`) |
| City ledger transfer company validation | **DONE** · VERIFIED | `internal/folios/payments.go:607` `TRANSFER_COMPANY_MISMATCH`; service and API tests |
| Transfer of a charge between folios | **DONE** · VERIFIED | `POST /folio-items/{id}/transfer`, `internal/folios/transfer.go`; see 3.1.1 |
| Check-out of every folio | **DONE** · VERIFIED | `CloseStayFolios` locks and checks every open folio of the stay; `TestCheckOutNeedsEveryFolioOfTheStayBalanced`; the check-out wizard lists every unbalanced folio |
| Stay API and frontend | **PARTIAL** · VERIFIED | `folios[]` carries `folio_type` and `bill_to_company_id`; the reservation sidebar and the folio screen name the company; the stay detail only marks a folio "(Company)" without the name (cosmetic) |

#### 3.1.1 The transfer, checked point by point (`internal/folios/transfer.go`)

| Question | Answer | Evidence |
|---|---|---|
| Duplicate posting possible? | No for the item (`folio_items_reverses_uk` unique index and the `GetReversalOf` check at `:109`; two concurrent transfers: one wins, `TestConcurrentTransfersMoveTheChargeOnce`). No for a room night (register row flipped, new POSTED row inserted at `:148`, `stay_charge_postings_once_uk` stays satisfied; `TestMovedRoomNightIsNotChargedAgain`: a second run posts nothing) | VERIFIED |
| Tax recomputed? | No. The copy takes amounts and components of the original; the test changes the tax rate to 20% after posting and the copy still carries 12,100 (11%) | VERIFIED (`TestTransferMovesAChargeAsACopy`) |
| Rounding drift? | No by construction (no calculation). Tested for EXCLUSIVE items only; **INCLUSIVE is not tested** | VERIFIED / PARTIAL (F-09) |
| Journal imbalance? | The day journal reads `folio_item_gl` by account; the copy carries the account of the original through `copies_item_id`, the reversal through `reverses_item_id` (`folios/queries.sql`, `InsertFolioItem`); the test proves net zero per account with SQL. Every journal is also checked by the deferred balance trigger (`00029`). **Not tested: the day-close journal after a transfer** | VERIFIED by SQL, UNVERIFIED through `PostDay` (F-09) |
| Department mismatch? | The department is copied the same way. The test compares only whether it is set, not its value | PARTIAL (F-09) |
| To a folio of another reservation? | Refused: `transfer.go:96` `FOLIO_TRANSFER_INVALID`; same property is guaranteed by the property-scoped read of the target | VERIFIED |
| A transaction that must not move? | Only `CHARGE` moves: payments, adjustments and reversals are refused; the target must have a stay (the deposit folio is refused) | VERIFIED |
| Already reversed or already transferred? | `ALREADY_REVERSED`; a transferred copy can move again (tested for a plain charge, not for a room night) | VERIFIED / PARTIAL |
| To a closed folio? | `FOLIO_CLOSED` on either folio (`requireOpen` at `:90` and `:93`) | VERIFIED |
| Permission and approval | `folio.reverse` plus a verified approval before the transaction; tests for missing reason, missing approval and missing permission | VERIFIED |
| Audit and references | `folio.item_transferred`; both items carry `reference_type FOLIO_TRANSFER` and the id of the original; the new item has `source TRANSFER`; migration 00062 widens the source CHECK and refuses a Down while one exists | VERIFIED |
| Lock order | Business day (shared), then the two folios in ascending id; the runtime lock-order guard accepts it | VERIFIED |
| Idempotency | No `Idempotency-Key`; a double click gets `ALREADY_REVERSED` instead of a replay (safe, noisy) | VERIFIED, Low |

One consequence of the transfer was decided and built, and is not in the original design: `SummaryRoomNights` of the day summary now counts the posting register instead of the items, so a moved night is counted once for the stay that earned it.

### 3.2 Restrictions

| Item | Status | Evidence |
|---|---|---|
| Grid table, tri-state attributes, unique scope per date | **DONE** · VERIFIED | `00059`, `rr_scope_uk`; schema tests |
| Resolver precedence (type and plan, type, plan, property; type wins a tie) | **DONE** · VERIFIED | `availability.Resolve`, `restrictions_test.go`, the effective view agrees with the evaluator (`TestTheEffectiveViewAgreesWithTheEvaluator`) |
| STOP_SELL, CTA, CTD, MIN_STAY, MAX_STAY at their anchor dates | **DONE** · VERIFIED | `Check` and its tests, including the boundaries |
| Every sale path asks the evaluator | **DONE** · VERIFIED, with a note | `TestEverySalePathAsksTheSalesRestrictions` has a sub-test per path (create draft and confirmed, add room, amended dates, room type or plan, confirm, reinstate, every kind, search agreement), plus walk-in and extension in `frontdesk`. **It is an explicit list**: a new sale path added later is not caught by a structural test (F-09) |
| Override: permission, approval, reason, audit | **DONE** · VERIFIED | `TestAnOverrideNeedsAPermissionAReasonAndAnApproval`, `TestOneOverrideCoversTheRoomsOfARequestAndTheOtherSales`, `TestAnOverrideReplaysWithTheSameKey`; the override is written into the audit entry of the operation |
| WEBSITE and OTA cannot override | **DONE** · VERIFIED | `restriction.go:60` `overridable`; `TestABookingFromTheWebOrAnOTACannotOverrideARestriction`; the screen offers nothing for such a refusal |
| Search verdict equals the booking attempt | **DONE** · VERIFIED | sub-test "the offers of a search say so, and agree with the booking" |
| Availability calendar marks | **DONE** · VERIFIED | `availability.NightMarks`, `TestCalendarMarksTheRestrictions` (partial plans, inactive plan, stock unchanged), component test |
| Override screens (confirm, reinstate, extension, walk-in) | **DONE** · VERIFIED | `RestrictionOverride.vue`, four component tests |
| A fill racing a booking | **MISSING** | the document asks for it (4.12); only fill-versus-fill is tested (F-09) |

### 3.3 Currency

| Item | Status | Evidence |
|---|---|---|
| One definition, `property_has_financial_data` | **DONE** · VERIFIED | `00058:8`, 15 root tables |
| Trigger and service agree, no race | **DONE** · VERIFIED | `properties_currency_lock` takes the property row FOR UPDATE; `TestEveryPathRefusesTheCurrencyOnceFinancialDataExists` (three cases: folio item, journal, bank statement, each through the service and a plain UPDATE), five more cases in the schema tests (journal, statement, city ledger receipt, budget, cashier shift), the two race tests (update first, write first). **Behaviourally 6 of the 15 roots are proved to lock**; for the other 9 (payments, supplier bills, supplier payments, supplier credit notes, city ledger invoices, city ledger adjustments, tax returns, tax payments, tax opening credits) the proof is a text check that the function mentions each root (F-09) |
| Roots and configuration | **DONE** · VERIFIED | a schema test classifies every table with `property_id` and a `numeric(18,x)` column as root, child, configuration or plan, so a new money table cannot be forgotten. `bank_accounts` alone and `reservation_room_rates` do not lock: the owner decided both |
| Bank statement guard | **DONE** · VERIFIED | `bankrec/csvimport.go:169`, `service.go:436`: request `currency` and CSV `currency` column, 422 `STATEMENT_CURRENCY_MISMATCH`; decimals of the property enforced on lines and balances; 2 backend tests, 1 frontend test |
| UI locked state | **DONE** · VERIFIED | `currency_locked` on the property, read-only fields with the reason; backend test before and after data for each root, 2 component tests |
| Property decimals | **PARTIAL** | amounts are validated against the decimals in folios, city ledger, payables, bank import, manual journals. **System journals (`accounting.Poster.Post`) do not check decimals** and the column accepts three (F-19) |

## 4. Feature Completeness

Compared with `13-feature-map.md`, `08-backlog.md` and the architecture documents, then checked against the code (a package, a route, a screen and a test must exist for DONE). Nothing was added to this table from assumption: a row that is not in the documents or in the code is not here.

| Feature | Status | Evidence | Gap | Priority |
|---|---|---|---|---|
| Tenancy, properties, business day, document sequences | DONE | `tenancy`; 19 tests; currency lock | none | – |
| Users, roles, permissions, rotating sessions, approvals | DONE | `iam` (argon2id, refresh replay detection, session re-check per request) | coverage of `iam` is 52.5% (F-16); no MFA, no self-service password reset, an approver may be the actor (F-17) | P1 / P2 |
| Rooms, room types, OOO/OOS blocks, bed types, bed variants | DONE | `rooms`, `availability/bed.go`, brute-force proof test | none found | – |
| Housekeeping, tasks, maintenance, lost and found | DONE | `housekeeping`, `maintenance`, `lostfound` | none found | – |
| Guests (tenant-wide) | DONE | `guests`; `guests.tenant_id` + `origin_property_id` | none found | – |
| Rates, grid, yield rules, free-night quotas, occupancy kinds | DONE | `rates` | none found | – |
| Availability engine, calendar, tape chart | DONE | `availability`; calendar now marks restrictions | none found | – |
| Sales restrictions with override | DONE | section 3.2 | a structural guard for new sale paths, a fill-versus-booking race test (F-09) | P1 |
| Reservations: create, amend, confirm, cancel, no-show, reinstate | DONE | `reservations`, 56 tests | **no cancellation fee or no-show fee** (F-08) | P1 |
| Front desk: check-in, walk-in, move, extend, check-out, reverse check-in | PARTIAL | `frontdesk`, 39 tests | reverse check-in and company folios (F-06) | P1 |
| Taxes, service charges, charge codes, charge calculation | DONE | `billingconfig`, `chargecalc` (98.3% coverage) | credit notes of the city ledger compute their own tax (F-20) | P2 |
| Folios, payments, refunds, voids, adjustments, reversals | DONE | `folios`, 40 tests, concurrency tests | idempotency does not compare the payload (F-21) | P2 |
| Several folios per stay, billing instructions, charge transfer | DONE | section 3.1 | test gaps (F-09) | P1 |
| Room charge posting and night audit | DONE | `roomcharge`, `nightaudit`, `expected` | accounting-not-set-up warning (F-15, resolved by P0 #6) | P1 |
| Cashier shifts, handover, X and Z reports | DONE | `shifts`, 19 tests | none found | – |
| City ledger: invoices, receipts, allocations, credit notes, write-offs, overdue, reminders, late fee | DONE | `cityledger`, 42 tests | credit-note tax (F-20) | P2 |
| Companies and booking groups | DONE | `companies`, `groups` | a master folio for a group is not built (documented, "later") | later |
| Chart of accounts, journals, periods, fiscal years, statements, cash flow, control accounts | DONE | `accounting`, 51 tests, deferred balance triggers | system journals do not check decimals (F-19) | P2 |
| Departments as an accounting dimension, department rule per account | DONE | `departments`, `deptrule.go`, tests | none found | – |
| Accounts payable, input VAT, supplier credit notes | DONE | `payables`, 18 tests | none found | – |
| Bank reconciliation, card settlements, MDR and its VAT | DONE | `bankrec`, 42 tests; statement currency guard | MT940/OFX import not built (backlog "later") | later |
| Tax filing, PKP settings, tax invoices and export | DONE | `taxfiling`, `taxinvoice` | the tax invoice CSV does not neutralise formulas (F-14) | P1 |
| Budget, statistics, budget against actual, by department | DONE | `budget`, 73.5% coverage | none found | – |
| Reports, PDF documents, CSV, Indonesian | DONE | `reports`, `documents` | none found | – |
| Confirmation e-mail (outbox, SMTP optional) | DONE | `notifications` | e-mail of invoices and receipts is out of scope by decision | later |
| Currency lock, bank statement guard | DONE | section 3.3 | none beyond the owner's accepted exclusions | – |
| **Transaction Group / Split Bill** | **BUILT** (was PLANNED) | `20-transaction-group.md`, migration 00063; a presentation grouping inside one folio, not a financial folio | demand UNVERIFIED | P1 |
| Channel manager, OTA, web booking engine | MISSING | not in any document as scheduled; restrictions are designed to feed it | out of the pilot | later |
| Point of sale integration | MISSING | none | out of the pilot | later |
| Fixed assets, purchase orders, withholding tax on payables | MISSING | backlog "later" | out of the pilot | later |
| Multi-currency | MISSING (non-goal) | `18` section 5.9 describes the future shape | by decision | later |
| Deployment, backup and restore, monitoring | **PARTIAL** (deployment and backup scripts exist; schedule, RPO and RTO decision, off-machine copy and monitoring do not) | F-03, F-04, F-13 | **blocks the pilot** | P0 / P1 |
| Browser-level end-to-end tests | MISSING | only API-level `e2e_test.go` and mocked component tests (F-27) | the UI has never been driven against the real API by a test | P1 |

## 5. Financial Integrity

Scope: folio posting, payments, refunds, adjustments, transfers, city ledger, supplier and payables, VAT, tax return, MDR and card settlement, cashier shift, general ledger, journal balancing, department posting, budget, bank reconciliation, night audit.

### 5.1 Controls that are enforced by the database or by one code path (VERIFIED)

| Control | Evidence |
|---|---|
| Ledger rows are never updated or deleted | 25 append-only triggers, 45 no-truncate triggers, state-guard triggers on documents that may change state (migrations 00010, 00020, 00031 to 00034, 00044 to 00047, ...) |
| One place writes each ledger | `InsertFolioItem` is called once (`folios/posting.go:91`), `InsertPayment` once (`folios/payments.go:122`), journal lines only inside `accounting` |
| Every journal balances | `00029`: deferred constraint triggers `gl_journals_balance_check` and `gl_journal_lines_balance_check` raise `gl_journals_balanced`; `Poster.Post` also checks equality before inserting |
| Money is exact | no float, real or double column; 88 `numeric(18,3)`; `decimal.NewFromFloat` and `time.Now` are lint-forbidden (`.golangci.yml`), the three `time.Now` exceptions are annotated and are not business dates |
| One room night is posted once | `stay_charge_postings_once_uk` (partial unique on POSTED), kept true by a transfer (section 3.1.1) |
| One document per request | unique idempotency keys on items, payments, journals, bills, receipts, invoices; a key reused on another folio is refused (`IDEMPOTENCY_KEY_REUSED`) |
| Wrong tenant or property is not representable | 434 foreign keys, among them composite `(tenant_id, property_id)` and `(property_id, id)` keys; no table has `property_id` without `tenant_id` |
| A department is never missing where the account requires one | `lineDepartment` is the final gate of the day close and of `Poster.Post`; `deptrule_test.go` |
| A day cannot close without its journal | `nightaudit.Run` calls `PostDay` inside the transaction that closes the day; `gl_day_posts` makes the day journal idempotent |
| Corrections go through reversal and approval | void, refund, adjustment, reversal, transfer all need an approval verified by `iam` and write an audit entry |

### 5.2 The questions of the audit, answered

| Can a financial transaction ... | Answer |
|---|---|
| be posted twice? | Not found. Charges, payments, receipts, bills, invoices, journals and tax invoices carry keys or unique indexes; room nights have the register; transfers have the unique reversal; concurrency tests exist for refunds, adjustments, receipts, credit limit, invoices claiming a transfer, supplier payments, tax invoices (`TestOnlyOneTaxInvoiceIsIssuedForASourceUnderARace`). `bankrec` adjust, settle and clear have no key but are guarded by the matched-line check under the statement lock (`matching.go:158`) |
| be missing from the GL? | Only by configuration. If accounting is **not set up**, `PostDay` returns without a journal (`accounting/journals.go:299`) and the night audit still closes the day; a backfill of up to 400 days exists, and the control accounts report would show the difference. Nothing warns at night audit (F-15) |
| make the GL unbalanced? | No path found: balance is a database constraint |
| have the wrong business date? | The convention is that every business-dated write first takes the OPEN day (`RequireOpenBusinessDay`, found in every module that inserts a ledger row: a file-level scan found none without it). **The database does not enforce it** and no test scans for it (F-07) |
| have the wrong property or tenant? | Not by query (617 of 669 queries filter both; section 8) and not by key |
| have the wrong department? | Not found (department gate) |
| bypass a permission? | Cannot be proved structurally (F-10); every service I sampled checks through its own helper (`authorize`, `updater`, `reporter`, `change`, `actor`, `need`) |
| bypass the correction workflow? | No ledger row can be updated or deleted; the only mutations are the approved endpoints. `bankrec` adjustments, clearings and card settlements are permission-only by design, not approval-gated |

### 5.3 Findings of this chapter

F-06 (reverse check-in strands company folios), F-07 (open-day rule is a convention), F-15 (day closes without a journal when accounting is not set up), F-19 (system journals do not check decimals), F-20 (credit-note tax outside the engine), F-21 (idempotency does not fingerprint the payload), F-22 (routing ignores the credit exposure of a company).

## 6. Reservation & Front Office

| Step | Implemented as | Status | Gap |
|---|---|---|---|
| Reservation (draft, confirmed) | `POST /reservations`, lines, nightly price snapshot, restrictions | DONE | – |
| Confirm | `POST /reservations/{id}/confirm`; asks restrictions and inventory again | DONE | – |
| Check-in | `POST .../check-in`; opens the guest folio and the company folios | DONE | – |
| In-house | stay detail, guests, charges, payments, several folios | DONE | company name not shown in the stay detail (cosmetic) |
| Room move | `POST /stays/{id}/move` (segments, housekeeping, charge by segment) | DONE | – |
| Extend or shorten | `POST /stays/{id}/change-departure`; restrictions asked for the extra nights; override on the screen | DONE | – |
| Amend (dates, room type, plan, rate) | `PATCH` of reservation and line; price override with approval | DONE | – |
| Check-out | `POST /stays/{id}/check-out`; posts due nights, every folio must be zero | DONE | – |
| Cancel | reservation and line; `requires_folio_resolution` when a deposit is held; the night audit only **warns** about an open folio with a balance of a dead reservation (`warnings.open_folios`), it does not block | DONE | no fee |
| No-show | per line and bulk at night audit | DONE | no fee |
| Reinstate | `POST .../reinstate`; asks restrictions and inventory | DONE | – |
| Reverse check-in | same day only, refused once a CHARGE exists on any folio of the stay | PARTIAL | company folios and payments on them (F-06) |
| Cancellation fee, no-show fee | the charge codes `CANCEL_FEE` and `NO_SHOW_FEE` and the account 4510 are seeded (`00014:21`, `00028:179`) but **nothing charges them**; a reservation without a deposit has no folio to charge on | MISSING | F-08 |
| Deposit | `POST /reservations/{id}/deposits` on the unlinked folio; linked to the stay at check-in | DONE | – |
| Split of a bill between payers | several folios, billing instructions, charge transfer | DONE | the guest-level split (Transaction Group) is planned |
| Master folio for a group | not built, documented | MISSING | later |

## 7. Night Audit

`nightaudit.Run` (`internal/nightaudit/service.go:234`) is one transaction: advisory lock (a second run is `NIGHT_AUDIT_IN_PROGRESS`), the OPEN day `FOR UPDATE`, the time guard of the property, the analysis, housekeeping, `roomcharge.PostLocked`, a revalidation, the day journal, the summary, the move of the business date and one audit entry.

| Check | Result |
|---|---|
| Orchestrates only | **VERIFIED**: it calls `roomcharge.PostLocked` and `Journaler.PostDay`; it computes no tax, service or price. The summary reads rows (`Summarize`) and is not a posting |
| Blockers | unresolved arrivals and departures, charge errors (including `ROUTING_TARGET_CLOSED`), invalid posted nights and open cashier shifts (when the property says so) roll back the run (stale drafts, open folios of dead reservations and blocks ending are warnings and never stop it), including the charges already posted (`TestRunIsBlockedAndRollsBackEverything`) |
| Idempotency, retry, restart | a crash rolls back the whole transaction; a retry posts only what the register says is missing; the day journal is unique per day; `TestConcurrentRunsOneWins` |
| Routed nights | a night routed to a closed company folio is an error, never posted to the guest folio (`TestAClosedCompanyFolioBlocksTheNightAudit`) |
| Isolation | every query is scoped by tenant and property; the advisory lock is per property |
| Gap | the day closes without a journal when accounting is not set up and nothing says so (F-15) |
| Note | the night audit is run by a person; there is no scheduler. This is a decision, not a defect, but a pilot needs a procedure for the person who forgets (the time guard only says "too early") |

## 8. Security & Tenant Isolation

### 8.1 Isolation, measured

| Check | Result |
|---|---|
| SQL scoping | a scan of the 669 sqlc queries: **617 filter by `tenant_id` and `property_id`**; the 52 others are queries on tenant-level tables (users, roles, guests, properties, audit by tenant), sessions (keyed by user or token) and the e-mail outbox worker (a background queue keyed by id), and `IsSettledOrSettling` (called with a line already read under scope). One is dead code: `ListAuditLogsForEntity` (tenant-only, no caller). VERIFIED (heuristic scan, then read) |
| Routes are authenticated | every `/api/` route is wrapped by `RequireAuthenticated` (`app.go:172`); only login, refresh and logout are public |
| Property access | `GetProperty` calls `CanAccess` first (the business-days handler does), and a property of another tenant is `404 PROPERTY_NOT_FOUND`; 21 of the 27 test files of `internal/app` assert it for a second tenant |
| Cross-property (same tenant) | by query scoping; **no test for the financial modules** (folios, payments, city ledger, payables, bank, shifts, documents) opens an object of property A through property B (F-10) |
| Users, roles and grants | tenant administrator only (`auth.RequireTenantAdmin`): a user manager cannot escalate |

### 8.2 Authentication and input

| Check | Result |
|---|---|
| Passwords | argon2id (m=19 MiB, t=2, p=1) |
| Tokens | access JWT HS256 only (`WithValidMethods`), 15 minutes, kept in memory; refresh cookie httpOnly, SameSite=Strict, scoped to `/api/v1/auth`, rotated, replay revokes the user's sessions; the session is re-checked in the database on every request |
| Input | body limit and `DisallowUnknownFields` (`httpx/json.go:38`), field validation with stable codes |
| CSV | three exporters neutralise formulas (`accounting`, `budget`, `reports`); **the tax invoice export does not** (F-14) |
| Headers | the application sets `Cache-Control: no-store` and `nosniff` on documents only; no CSP, HSTS or frame protection is set by the API, and the API does not serve the SPA, so they depend on a proxy that does not exist yet (F-03) |
| Throttling | per-process, in memory, keyed by `RemoteAddr` (F-05) |
| Secrets | none tracked; `PMS_JWT_SECRET` must be at least 32 characters; production refuses an insecure cookie |
| Dependencies | no vulnerability scan in CI (`govulncheck`, `npm audit`) (F-27) |
| MFA, self-service reset | none (F-17) |

### 8.3 What the static authorization scan can and cannot say

The scan lists service methods that take a property id: 396. In 109 of them the body shows no obvious authorization call. They are, by sampling, helpers that are only called after authorization (`availability.*`, `reservations.Lock`, `tenancy.RequireOpenBusinessDay`, `roomcharge.Post`), services that authorize through another helper (`maintenance` through `reporter`, `housekeeping` through `updater`, `lostfound` through `change`, `roomcharge.Preview` through `authorize`), and documents that authorize through the folio or reservation they read. That is a reading of a sample, not a proof, and the project has no test that fails when a new route forgets its permission. **UNVERIFIED** structurally: F-10 recommends a test that calls every route with a user who has no permission and expects 403 or 404.

## 9. API Contract

| Check | Status | Evidence |
|---|---|---|
| Every route is in OpenAPI and the other way round | DONE · VERIFIED | `TestOpenAPIDescribesEveryRoute` (295 routes); in CI the web job also checks that `schema.d.ts` equals the generation of the YAML |
| Frontend uses only fields the contract has | DONE · VERIFIED | `vue-tsc` against the generated types is green; a field that is not in the schema does not compile |
| Every backend error code has a text in both languages | DONE · VERIFIED | `errorCodes.test.ts` scans the Go sources for codes |
| Responses have the shape, enums and nullability that the YAML says | **UNVERIFIED** | no test validates a real response against the schema (no validator in `go.mod`); the YAML and the Go structs are kept in step by hand. Example from this stretch: `ReservationFolio` had to be edited by hand when `folio_type` was added, and the compiler would not have said so |
| OpenAPI is the only description of the API | **DOCUMENTATION DRIFT** risk | `06-api.md` is a second prose description of the same routes; only the YAML is tested |

Finding: F-11.

## 10. Database & Migration

| Check | Status | Evidence |
|---|---|---|
| Keys and checks | DONE · VERIFIED | 434 foreign keys (composite on tenant, property and parent), 314 CHECK, 123 unique constraints, 387 indexes (partial and expression indexes among them: `folios_stay_payer_uk`, `fbi_scope_uk`, `stay_charge_postings_once_uk`), 167 triggers; no table without a primary key |
| Every migration has a Down | DONE · VERIFIED | all 62 files have a `+goose Down` section with statements |
| Up, down, up on an empty schema, with 514 schema tests | DONE · VERIFIED locally on PostgreSQL **16 and 18** (this audit ran both). In CI: **never ran** (F-01) |
| Down that would lose data | DONE · VERIFIED for the two most recent | `00060` and `00062` refuse a Down while a COMPANY folio or a transferred item exists, and tests prove it. The older Downs drop columns or tables; the only rollback of a populated database is a restore, and there is no restore procedure (F-04) |
| Migration over a database that has data | **PARTIAL** | 21 of the 62 migrations contain data statements at the start of a line (`INSERT INTO`, `UPDATE ... SET`, `DELETE FROM`: 00012, 00014, 00022, 00023, 00026-00031, 00034, 00037, 00041, 00042, 00044-00047, 00051, 00055, 00057), which are backfills, seeds for the properties that already exist and reclassifications: for example `00037` and `00057` give every room a bed type, `00028` seeds a chart for existing properties, `00041` classifies VAT by a name pattern. Only `00060` and `00062` are tested over seeded rows, and `platform/migrate_test.go` goes down to 14 and up on an empty schema. The schema tests insert their data after the last migration. A pilot database that is upgraded later is the first populated upgrade (F-12) |
| Concurrent migrations | INFO | `PMS_MIGRATE_ON_START` defaults to off and `goose` is used without a session locker, so two instances starting with it on could race; the documented way is `cmd/migrate` |
| Foreign keys without an index on their first column | INFO · VERIFIED | 263 of 434: almost all are the audit columns (`created_by`, `updated_by`, `approved_by`, ...) and the `(tenant_id, property_id)` keys to `properties`; rows of users and properties are never deleted, so this costs nothing at pilot size |
| One money column with scale 2 | LOW · VERIFIED | `reservation_room_rates.grid_rate numeric(18,2)` (migration `00036`); every other money column is scale 3. It is a reference price for yield rules, but a 3-decimal currency would be cut (F-23) |
| Dead query | INFO | `ListAuditLogsForEntity` has no caller (F-25) |

## 11. Concurrency

The project has 57 tests that start goroutines against a real PostgreSQL. By area:

| Area | Race test | Status |
|---|---|---|
| Room availability, reservation creation, confirmation | `TestLastRoomBookedConcurrentlyGoesToOne`, `TestConcurrentConfirmOfDraftsRespectsInventory` | DONE |
| Check-in, room assignment, room move | `TestConcurrentCheckInsIntoOneRoom`, `TestSameLineCheckedInTwiceAtOnce`, `TestConcurrentAssignmentOfOneRoom`, `TestConcurrentMovesIntoOneRoom` | DONE |
| Room charge posting, night audit | `TestConcurrentPostingChargesEachNightOnce`, `TestConcurrentRunsOneWins` | DONE; **not with a routed night** (F-09) |
| Cashier shift open and close, cash payments | `TestTwoOpensOfOneCashierMakeOneShift`, `TestPaymentsAndTheCloseOfAShiftDoNotLoseCash` | DONE |
| Payments, refunds, adjustments | replay and refund races, adjustment race | DONE |
| Folio transfer | `TestConcurrentTransfersMoveTheChargeOnce` | DONE; transfer against a posting run: **not tested** |
| Company folio creation | none: `SetBillingInstructions` against check-in, check-out or reverse check-in, and two sets of one line | **MISSING** (F-09) |
| Currency lock | both orders, through the service and through a plain UPDATE | DONE |
| Restriction override | the override is verified inside the request; the race that matters is a **fill racing a booking** | **MISSING** (F-09) |
| City ledger, payables, bank, tax, budget, accounting, rates, housekeeping, rooms, guests, groups | one or more race tests each | DONE |

The race detector result is in F-02.

## 12. Frontend/UI

A scan of the 87 views (a heuristic over the source, then read) and the tests:

| Check | Result |
|---|---|
| Error state | every one of the 78 views that call the API handles `ApiError` and shows the code and the message |
| Duplicate submit | all 56 views that write have a busy or saving guard on the button; the approval dialog keeps the password out of storage |
| Permission state | 71 of 78 have one; the 7 without (`AccountView`, `GuestDetailView`, `PropertyFormView`, `RoleFormView`, `RolesView`, `UserFormView`, `UsersView`) are tenant or personal screens the server guards |
| Empty state | present where a list is shown (`EmptyState`, `DataTable` empty slot) |
| Loading state | **weak**: only 11 views pass `:loading` to the table; the others show an empty screen until the answer comes (F-24) |
| Validation | field errors from the server are shown next to the field, with texts in both languages |
| Money | strings end to end; `formatMoney` groups digits as text. Floats appear in a few hints and totals shown on screen (`ReservationDetailView` estimate total, `FolioView` hint of what can be adjusted); the server never trusts them (F-24) |
| Currency | amounts carry no currency symbol in most views; with one currency per property that is acceptable, the property and the PDFs name it |
| Dates | business dates as text, instants through the wall clock of the offset they carry |
| Languages | `i18n.test.ts` fails on a key missing in either language or empty; hard-coded English text found: one `aria-label` |
| Tests | 736 tests; views without a test of their own: `RolesView`, `RoleFormView`, `UsersView`, `UserFormView` (user and role administration) and `CheckInPanel` (covered through its parent). Browser-level end-to-end: none (F-27) |
| Architecture 18 screens | billing instructions (5 tests), restriction override on four screens (8), charge move and company folio on the folio screen (3), company on the reservation sidebar, calendar marks (1), read-only currency (2), statement currency (1): all present |

## 13. Test Coverage

| Test kind | Count | Notes |
|---|---|---|
| Go tests with a real PostgreSQL | 771 + 33 sub-tests in 142 files | median statement coverage of the domain packages 63.8%; lowest `auditlog` 43.6, `groups` 48.8, `iam` 52.5, `rooms` 53.1, `rates` 54.1, `lostfound` 54.6; highest `chargecalc` 98.3 and `app` 98.9 |
| Schema tests | 514 (435 violations that must be rejected, 79 valid cases) | pass on PostgreSQL 16 and 18 locally |
| Web tests | 736 in 99 files | mocked API client; no real API behind them |
| Concurrency tests | 57 | the audit machine cannot run `-race`; in a container six packages passed under it (F-02) |
| API-level end-to-end | `internal/app` (27 files, 98.9% of the composition root) | includes the cross-tenant 404 checks and the OpenAPI route test |
| Migration tests | `00060`, `00062`, `platform/migrate` | see F-12 |
| Browser end-to-end | none | F-27 |
| CI | red | F-01 |

Coverage by the topics the owner asked about:

| Topic | Assessment |
|---|---|
| Folio transfer | strong (7 backend tests, API, component) with the gaps of F-09: net zero through the day journal, department value, INCLUSIVE, a chain of room nights |
| Restriction override | strong; a structural guard for new sale paths and a fill-versus-booking race are missing |
| Company routing | strong for rules, precedence, blocker, check-in, in-house, night audit, invoice, tax invoice; reverse check-in missing (F-06) |
| Currency lock | strong: schema classification of every money table, a text check that the function names every root and no configuration table, both race orders, 6 of 15 roots proved by inserting a row (the other 9 by the text check) |
| Night audit | 15 tests; blockers, rollback, concurrency, routed blocker; no test for "accounting not set up" (F-15) |
| Tenant isolation | cross-tenant at API level for 21 of 27 files; cross-property for financial modules missing (F-10) |
| Concurrent posting | base case yes, with routing no |
| Multiple folios | yes |

Test infrastructure (F-18): two of the six complete Go runs of this stretch had one package fail only because Docker could not start the test PostgreSQL ("wait for reaper ... context deadline exceeded": `bankrec` once, `rates` once); both passed when run alone. Two of the six complete web runs had tests time out at the 5 second default under load; they pass alone.

## 14. Operational Readiness

| Item | Status | Evidence |
|---|---|---|
| Dockerfile | **MISSING** | none in the repository (`find`); `compose.yaml` has the development database only |
| Production deployment | **MISSING** | no document, no manifest, no reverse proxy or TLS configuration; the API does not serve the SPA (`web/dist` is not referenced by any Go file) |
| Environment configuration | DONE · VERIFIED | `internal/platform/config` (the `PMS_` variables, validated at start), `.env.example`; production refuses an insecure cookie, a short JWT secret fails to start |
| Migration procedure | PARTIAL | `cmd/migrate up|down|status|version`; no production procedure, no locker; the rollback of a populated database is not defined |
| Backup, restore, disaster recovery, RPO, RTO | **PARTIAL (was MISSING)** | the audit found nothing; P0 #4 added the scripts and a verified restore (F-04 update). Not done: an agreed RPO and RTO, a schedule, an off-machine copy, point-in-time recovery |
| Logging | DONE · VERIFIED | `log/slog` text or JSON, request id on every line, access log, errors with codes |
| Monitoring, metrics, alerting | **MISSING** | no metrics endpoint, no tracing, no alert definition (F-13) |
| Health and readiness | DONE · VERIFIED | `GET /healthz`, `GET /readyz` (checks the database); both excluded from the rate limit |
| Graceful shutdown, timeouts | DONE · VERIFIED | `cmd/api/main.go`: signal context, server timeouts, `PMS_SHUTDOWN_TIMEOUT` |
| Rollback of the application | MISSING | not described |
| Several instances | **PARTIAL** | sessions are in the database (good), the night audit uses a database advisory lock (good), the e-mail worker claims rows with a lease (good); **the rate limit and the login and approval throttles are in memory** (F-05) |
| Security headers, TLS | MISSING | not set by the application, no proxy defined (F-03) |
| CI/CD | PARTIAL | CI exists and is red (F-01); no deployment job |

## 15. Architecture Consistency

The 25 principles, each against the code:

| # | Principle | Status | Evidence |
|---|---|---|---|
| 1 | Reservation is not a Stay | DONE | `reservations`/`reservation_rooms` and `stays`/`stay_rooms` are separate tables (`00008`, `00009`) |
| 2 | Occupancy is derived | DONE | no `rooms.status`; the availability engine and the calendar derive it |
| 3 | Housekeeping separate | DONE | `room_housekeeping` and its append-only log |
| 4 | OOO/OOS separate | DONE | `room_blocks` with an exclusion constraint on overlap |
| 5 | Tax and service configurable | DONE | `taxes`, `service_charges`, rules per charge code; no rate constant found in the code |
| 6 | Price mode belongs to the rate or charge | DONE | no `taxes.is_inclusive`; `price_mode` on charge codes, rate plans, items |
| 7 | Historical values are snapshots | DONE | components, revenue account, department, nightly prices, MDR and its VAT are stored on the row |
| 8 | Guest is tenant-wide | DONE | `guests.tenant_id`, `origin_property_id` |
| 9 | User e-mail unique per tenant | DONE | `users_tenant_email_uk (tenant_id, lower(email))` |
| 10 | One currency per property | DONE | column, lock, bank guard |
| 11 | Business date from `business_days` | **DONE** (was PARTIAL) | reading: `time.Now` is lint-forbidden; writing: the service gate `RequireOpenBusinessDay`, and since 2026-10-08 a database trigger for the ledger and cash tables (F-07, migration 00065) |
| 12 | Night audit blocks unresolved conditions | DONE | section 7 |
| 13 | Room charge posting centralized and idempotent | DONE | `RoomPoster` capability, the register |
| 14 | Charge calculation centralized | **PARTIAL** | `chargecalc` computes every folio charge; city ledger credit notes compute their own tax (`cityledger/adjustments.go:381`, F-20); the VAT on a bank commission in `bankrec` is a different calculation by design |
| 15 | Folio posting centralized | DONE | one call site of `InsertFolioItem` |
| 16 | Payment posting centralized | DONE | one call site of `InsertPayment` |
| 17 | Financial operations transactional | DONE | `WithinTx` in every use case; replay loops for keyed requests |
| 18 | Money is numeric | DONE | one column with scale 2 (F-23) |
| 19 | No hard-coded tax or service rule | DONE | none in code; `00041` classified existing taxes as VAT by a name pattern once (F-25) |
| 20 | Night audit orchestrates only | DONE | section 7 |
| 21 | Database constraints and application validation | DONE | both layers throughout |
| 22 | No unnecessary microservices | DONE | one binary, one database |
| 23 | Append-only ledgers | DONE | 25 append-only triggers, 45 no-truncate triggers |
| 24 | Corrections by reversal or new documents | DONE | reversal, adjustment, void, refund, transfer |
| 25 | Tenant and property isolation | DONE by construction, **tests incomplete** | section 8, F-10 |

No principle is violated. Two are partly enforced by convention (11) or have one exception (14).

## 16. Planned Transaction Group / Split Bill

**Status: PLANNED when this audit was written; BUILT later (2026-10-08): see `20-transaction-group.md`.** The text below is the plan as it was. What the build decided on each point: (1) the change of group after posting is a **side table** (`folio_item_groups`, migration 00063) that leaves the append-only ledger untouched, the history being the audit trail; a line with no row is in group A; (2) the limit of four is in the application, the table accepts one capital letter; (3) a reversal and a void are shown in the group of the line they reverse, but a refund and the copy of a transfer start in group A (follow-up); (4) the print takes `?group=`, the tax invoice is untouched and stays per folio; (5) the print of a group is the lines of the group and their totals only (the owner's decision after review: nothing of the other groups, no figure of the whole folio); (6) the general ledger does not know groups; (7) demand is still UNVERIFIED.

The concept, as decided: a folio has at most four transaction groups (A to D). `group_code` lives on the transaction (`folio_items`, `payments`), default `A`. A group is not a financial folio: the balance stays per folio. The screen and the print can choose group A, B, C, D or all groups. The group only organises transactions for operations and for printing.

What the evidence in the repository says about it (observations for the design, not decisions):

1. **The ledger is append-only.** `folio_items` and `payments` cannot be updated (triggers). A group fixed at posting time is easy. A change of group afterwards cannot be an `UPDATE`: it needs either a correction that writes a reversal and a copy (the machinery of the charge transfer already exists) or a small append-only side table of group assignments with its history. The owner should choose before anything is written; the first keeps the ledger honest but doubles the lines, the second keeps the lines but adds a join to every folio read.
2. **The four limit is a CHECK** (`group_code IN ('A','B','C','D')`), no table needed.
3. **What inherits a group**: a reversal, a void and a refund should take the group of the original; a transfer copy should keep it unless a target group is given; the room night takes `A` unless a billing instruction says otherwise (a possible optional field of the instruction); a payment takes the group the cashier chooses.
4. **Prints and documents**: `InvoiceData` and the folio screen take their lines from one query, so a group filter is natural. The **tax invoice** is the open question: `FolioVATLines` works per folio; a tax invoice per group needs a group parameter and a decision on whether a group can have its own buyer (a legal question, not a technical one).
5. **Balances**: group A may owe 500,000 while group B holds a credit of 500,000. The folio balance is zero and check-out passes. A per-group print must say whether it shows a balance due, or it will mislead the guest. Decide the wording before the print is built.
6. **General ledger**: not a dimension. The day journal reads `folio_item_gl`; adding a group there would be a different feature.
7. **Overlap with what exists**: several folios per stay already split a bill **by payer** when the payer is a company. The group splits **within one payer** (room and breakfast on one bill, extras in cash on another). No pilot feedback in the repository says how often a guest needs that: **demand is UNVERIFIED**.
8. **Position**: P1, after the P0 items and after the folio correctness items (F-06, F-09), because the feature adds a column to the ledger and a filter to every folio view and document. It is **not P0**: no evidence of a pilot blocker, and the payer split is covered. It needs a short design document (`20-...`) before the migration.

## 17. Critical Findings

Severity: **Critical** defeats a safeguard the project relies on; **High** would hurt a pilot in operation; **Medium** a gap with a workaround or a low frequency; **Low** quality or hygiene; **Info** a note. Status is the classification of section 1; the evidence level is in the same line.

---

#### F-01 · Critical · CI and quality gate

| | |
|---|---|
| Status | **MISSING**: the gate has never passed · VERIFIED |
| Evidence | The GitHub Actions API for `retechno/KamaraPMS` lists 16 runs of the workflow `CI`, from `f5e03e7` ("first commit") to `fbecf6f`; all 16 have the conclusion `failure`. For the latest run the Go job fails at the step "sqlc generated code is up to date" with "Process completed with exit code 126" and its steps *Lint* and *Test (race detector)* are `skipped`; both schema jobs (PostgreSQL 16 and 18) fail at "Migrations and schema tests" with the same exit code 126; only the web job is green. `git ls-files -s scripts/` shows `100644` for `sqlc.sh`, `db-test.sh`, `lint.sh` and `cloud-setup.sh`, and `ci.yml` runs `scripts/sqlc.sh diff` and `scripts/db-test.sh` directly: a file without the execute bit gives exit code 126. The repository was developed on Windows, where the bit does not exist (`core.fileMode=false`). On the audit machine all of it is green, including the schema tests on PostgreSQL 18, so the cause is the file mode, not the code |
| Risk | Everything `CLAUDE.md` says is enforced is, in CI, not run: lint, the race detector, the stale-generated-code check, the schema tests on two PostgreSQL versions. A red CI that has always been red is ignored, so a real regression would be ignored too. The Go test suite itself has never run on a Linux runner in CI (`CLAUDE.md` describes a Linux cloud sandbox where it runs; whether it passed there recently is not recorded) |
| Recommendation | Give the scripts the execute bit in git (`git update-index --chmod=+x scripts/*.sh`) or call them with `bash`; run the workflow; fix what then turns red (unknown until it runs: UNVERIFIED); make the checks required for `main` (branch protection) |
| Suggested next step | One commit, "CI: make the scripts executable", then read the first complete run and list what it finds |
| **Update (2026-10-07): RESOLVED** · VERIFIED | `c38ca68` gave the four scripts the mode `100755`. CI run 17 (`https://github.com/retechno/KamaraPMS/actions/runs/37575543159`) is green in all four jobs: Go (vet 36 s, sqlc up to date 6 s, lint 15 s, `go test -race` of every package 264 s), schema on PostgreSQL 16 (23 s) and 18 (27 s), web (tests 90 s, build 31 s). Nothing else had to be fixed: the first complete run found no failure. The second recommendation, **make the checks required for `main`** (branch protection), is a repository setting; **the owner set it on 2026-10-08 and verified it from GitHub** (the repository cannot check it itself), with the four checks listed under "Branch protection for `main`" in the P0 close-out status |

#### F-02 · Medium · Race detector

| | |
|---|---|
| Status | **PARTIAL**: clean where it was run, never in CI · VERIFIED |
| Evidence | The audit machine has `CGO_ENABLED=0` and no C compiler, so `go test -race` cannot run on it, and in CI it never ran (F-01). It was run in the `golang:1.26-trixie` container (Go 1.26.8) against the compose database with `go test -race -p 1 -count=1`: `folios` 104 s, `roomcharge` 106 s, `nightaudit` 45 s, `frontdesk` 102 s, `reservations` 188 s and `tenancy` 30 s **passed with no race report**. The system then stopped the run for low memory, before `shifts`, `cityledger` and `payables` finished; the run was not restarted and the container was stopped. The other 37 test packages were not run under the detector |
| Risk | The code base relies on row locks and a lock order for correctness, and has 57 concurrency tests. Without `-race` a data race in Go memory (a shared map, a counter) is not seen by any of them. The six packages that were checked are the ones with most shared state around posting and stays; the rest is UNVERIFIED |
| Recommendation | Let CI run `go test -race` (F-01) and keep the container command below for a local run on Windows: `docker run --rm -v <repo>:/src -w /src -e CGO_ENABLED=1 -e PMS_TEST_DATABASE_URL=postgres://pms:pms@host.docker.internal:55432/pms?sslmode=disable golang:1.26-trixie go test -race -p 1 ./...` |
| Suggested next step | After F-01, run the whole suite under `-race` once and record the result here |
| **Update (2026-10-07): RESOLVED** · VERIFIED | The step "Test (race detector)" of CI run 17 ran `go test -race -count=1 ./...` over all packages on the runner and passed in 264 s: no race report anywhere, including the 37 packages that were not checked in the container |

#### F-03 · High · Deployment artefacts and headers

| | |
|---|---|
| Status | **MISSING** · VERIFIED |
| Evidence | No `Dockerfile` anywhere (`find . -iname Dockerfile*` outside `node_modules`); `compose.yaml` runs the development database only; `README.md` explains a local run; no Go file references `web/dist`, so the API does not serve the SPA; the API sets no CSP, HSTS or frame header (`Cache-Control: no-store` and `nosniff` only on documents) |
| Risk | There is no defined way to put the system in front of users: where the SPA is hosted, who terminates TLS, which headers are set, which environment variables a server needs, how it restarts |
| Recommendation | A production recipe: a multi-stage Dockerfile for the API, a `compose` (or systemd) file with the API, PostgreSQL and a reverse proxy that serves `web/dist`, terminates TLS and sets CSP, HSTS, `X-Frame-Options` and `Referrer-Policy`; a page of required variables; a smoke test after start |
| Suggested next step | Write `docs/architecture/` or `docs/operations/` "Deploy" with the recipe and run it once on a clean machine |
| **Update (2026-10-07): RESOLVED for a single machine** · VERIFIED | P0 #3: `Dockerfile` (targets `api` and `web`), `deploy/compose.yaml`, the nginx proxy with the security headers, `deploy/compose.tls.yaml`, `deploy/.env.example`, `scripts/prod-smoke.sh` and `docs/deployment.md`. The stack was built and run, the smoke test passed and the builds were reproducible (the checks are listed in section 12 of the deployment document; the final verification, with CI run 18 green, is recorded after F-05). Still open: a deployment to a real server and a certificate from an authority, which need a server and a domain |

#### F-04 · High · Backup, restore and disaster recovery

| | |
|---|---|
| Status | **MISSING** · VERIFIED |
| Evidence | A search of `docs`, `README.md`, `CLAUDE.md`, `scripts` and `compose.yaml` for backup, restore, `pg_dump`, point-in-time recovery, RPO and RTO finds nothing about them; the database of the development compose is one volume |
| Risk | The whole ledger of a hotel is one PostgreSQL database. Without a tested backup and a stated RPO and RTO, one disk failure or one bad migration is the end of the pilot. It also means the rollback of a migration on a populated database (F-12) has no defined mechanism |
| Recommendation | Decide RPO and RTO with the owner (a daily dump is an RPO of a day; WAL archiving or a managed database with point-in-time recovery is minutes); write the backup job, the restore procedure and the rollback procedure for an application release; **restore once into an empty database and run `scripts/db-test.sh` queries and a night audit preview against it** |
| Suggested next step | A one-page runbook plus one rehearsed restore, before the first real night audit |
| **Update (2026-10-07): RESOLVED for the mechanism, OPEN for the decisions** · VERIFIED | P0 #4: `scripts/db-backup.sh` (custom-format compressed `pg_dump`, verified before it is kept, checksum, retention), `scripts/db-restore.sh` (only into an empty database, checks after), `scripts/db-verify.sql`, `scripts/restore-drill.sh` and `docs/backup-restore.md` (which replaced the loose commands in `deployment.md`). **A real restore was done and compared:** the development database (version 58, 99 tables, 1 994 rows) was backed up, restored into a new empty PostgreSQL container, and the row count and row hash of all 99 tables were identical; `migrate up` then took the populated copy from 58 to 62, the API was ready on it, a sign-in and reads of reservations, folios, payments and business days worked. The drill also passed on the `kamarapms-deploy` stack. **CI: run 19 for `3a43178` succeeded** (https://github.com/retechno/KamaraPMS/actions/runs/37587630089): Go (build, vet, sqlc diff, lint, `go test -race ./...`), schema on PostgreSQL 16 and 18, web. **CI does not exercise the backup scripts** (they were added without Go or web changes and no workflow step runs them); the restore evidence above is from a manual run on the development machine, not from CI. Fourteen failure cases give the documented exit codes (wrong password, no such database, server down, garbage, truncated and tampered files, non-empty target, failing restore). **Update (pilot mechanism, later the same day):** the owner approved the Backup/DR decisions, and the pilot minimum was built and drilled once: a scheduled `backup` service with a failure alert, an encrypted secondary copy (directory or SSH, no provider named), pilot retention, the backup-before-upgrade procedure and a restore drill from the secondary copy (section 14 to 16 of `docs/backup-restore.md`). Production DR (15 min RPO, 1 h RTO, WAL/PITR, off-site, monthly and year-end retention, encryption at rest) is a TARGET and is not built. **Still open:** the backup and restore scripts are not run by CI (so a regression in them would not be caught); RPO and RTO are PROPOSED (24 h and 4 h), not agreed by the owner; the restore time was measured on 0.7 MB only, so no RTO is derived; no schedule is installed; no off-machine copy; no WAL archiving (RPO below a day); no encryption of the files at rest; domains whose tables were empty in the source (city ledger invoices and receipts, supplier bills and payments, tax invoices and returns, bank statements) are UNVERIFIED with data |

#### F-05 · High · Rate limits trust only the connection address and live in memory

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED (by reading the code) |
| Evidence | `internal/platform/httpx/middleware.go:64` takes the client key from `r.RemoteAddr` only; no `X-Forwarded-For` or trusted-proxy setting exists (grep). The limiter (`httpx/ratelimit.go`, default 600 a minute, burst 600) and the login and approval throttles (`iam/ratelimit.go`, `iam/approval.go`) are per-process maps |
| Risk | Behind the reverse proxy that production needs (F-03) every client has the proxy's address: **one bucket for the whole hotel**. A busy front desk (several screens, several calls per screen) can reach 600 requests a minute and get 429 for everyone. With two instances the limits double, and a restart clears the login throttle, which weakens brute-force protection |
| Recommendation | A trusted-proxy setting (`PMS_TRUSTED_PROXIES`) that reads `X-Forwarded-For` only from those addresses; keep the login throttle keyed by tenant and e-mail (it already is) and store failed attempts in the database or document that one instance is the supported shape for the pilot |
| Suggested next step | Decide "one instance behind a proxy" for the pilot, implement the trusted-proxy setting, add a test |
| **Update (2026-10-07): RESOLVED** · VERIFIED | `PMS_TRUSTED_PROXIES` and `httpx.ResolveClientIP`: `X-Forwarded-For` is believed only from a trusted proxy and is read from the right; the proxy overwrites the header. Unit tests for a direct client, a trusted proxy, spoofing from an untrusted peer and several clients behind one proxy; the smoke test repeats them with clients that have addresses of their own. The throttles are still per process, so the supported shape stays one API instance (documented) |

#### P0 #3 final verification (2026-10-07) · VERIFIED

Commit under verification: `f77e016`; the smoke-test extension below is `2f25be7`, which is the commit CI ran. Findings above are not rewritten.

| Check | Result |
|---|---|
| `taxfiling`, `taxinvoice`, `tenancy` (one at a time, after the earlier runs were stopped for low memory) | all three pass |
| Web `npm test` | 736 tests pass |
| Images rebuilt with `--no-cache` from `f77e016` (api 44 MB, web 50 MB, migrate is the api image) | built; the Go binaries have the same SHA-256 as two earlier independent no-cache builds; the web `dist` (113 files) has the same hash as the earlier build (reproducible) |
| `scripts/prod-smoke.sh` on the rebuilt images, project `kamarapms-deploy` | **PASSED, 57 checks**: migration job, API start, `/healthz`, `/readyz`, SPA and deep link, API proxy, security headers (once, no version), 413 over the body limit, containers (healthy, nonroot, read-only, no published API or database port), client address (forged header replaced, ignored from an untrusted peer, claiming to be the proxy fails), rate limit per real client, **PostgreSQL stopped: `/healthz` 200, `/readyz` 503 `NOT_READY`, 200 again without restarting the API**, **TLS variant: https page, fallback, `/api`, HSTS only on https, 308 from http, probes on http, TLS 1.2 and 1.3 accepted, 1.1 refused** |
| CI for `2f25be7` | **run 18, success**: https://github.com/retechno/KamaraPMS/actions/runs/37584132744. Go (build, vet, sqlc diff, lint, `go test -race ./...`), schema on PostgreSQL 16 and 18, web (types, tests, build): all green |

The DB-down and TLS checks were manual in the first verification and are now part of `scripts/prod-smoke.sh` (`SMOKE_DB_DOWN=0`, `SMOKE_TLS=0` skip them).

**Status after verification.** F-03: resolved for a single machine. F-05: resolved (throttles still per process; one API instance is the supported shape). **F-04 was still open when this block was written and was then worked as P0 #4**: nothing had been done on backup or restore at that moment (the later state is in the F-04 update and in the verdict).

**Remaining limitations.** No deployment to a real server and no certificate from an authority (needs a server and a domain). The throttles are per process. The smoke test does not read the headers of an error produced by nginx itself beyond the cases listed. Branch protection for `main` is not set.

**Incident during the work: the compose project name.** The first run of `docker compose -f deploy/compose.yaml up` had no project name of its own, so Compose used the default `kamarapms`, the project of the development database. It **recreated the development container `kamarapms-db-1`** (it lost its published port 55432 for a while, and the migration job failed on the password because the new container used the deployment's variables). The development volume `kamarapms_pms-db` was **not deleted or damaged**: the stack was brought down without `-v`, the development database was started again from the root `compose.yaml`, and port 55432, goose version 58 and the 100 tables were checked. The deployment file now says `name: kamarapms-deploy` (own containers, network and volume `kamarapms-deploy_pms-db`). In the final verification the development container had the same id and start time as after the repair, the same volume (created 2026-09-30), version 58, 100 tables and port 55432. **Warning, kept in `CLAUDE.md` and `docs/deployment.md`: never run the deployment stack under the project name `kamarapms`, and never use `down -v` on it without looking at the project.**

#### F-06 · Medium · Reverse check-in leaves company folios on a cancelled stay

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED by reading, no test |
| Evidence | `frontdesk/service.go:621` calls `DetachStayFolio` and then cancels the stay. `folios/stayfolio.go` `DetachStayFolio` unlinks only the GUEST folio and refuses only when a `CHARGE` exists on a folio of the stay (`CountStayChargeItems`). A COMPANY folio opened at check-in stays OPEN, linked to the cancelled stay. A **payment** on it is not a CHARGE, so the reversal proceeds and the money stays on a folio of a cancelled stay |
| Risk | Orphan open folios in lists; money stranded on a company folio that no check-out will ever close; a re-check-in opens a second company folio for the new stay |
| Recommendation | Refuse the reverse when any folio of the stay has a payment or a refund, as it refuses a charge; close an empty company folio when the stay is cancelled (it is empty, so it closes at zero) |
| Suggested next step | A test with a company folio and a payment, then the fix; part of the first folio correctness commit |
| **Update (2026-10-08): DONE** · VERIFIED by tests and CI run 31 for `bb5eaa3` (https://github.com/retechno/KamaraPMS/actions/runs/37722724117: Go with `go test -race ./...`, lint, sqlc diff, schema on PostgreSQL 16 and 18, web) | **Root cause, run before the fix:** `DetachStayFolio` read and locked only the GUEST folio and counted only `CHARGE` items, so a payment on a COMPANY folio let the reversal go through (folio OPEN, balance -50,000, on a CANCELLED stay; a later payment was still accepted; 6 of 6 concurrent runs ended that way). **Fix (migration 00064, `folios.DetachStayFolio`, `frontdesk.ReverseCheckIn`):** every folio of the stay is locked FOR UPDATE in ascending id before anything is read; a CHARGE on any folio still gives `CHECK_IN_HAS_CHARGES`; an OPEN company folio with a balance gives **409 `CHECK_IN_HAS_PAYMENTS`** (`context.folios`: folio_id, folio_number, folio_type, balance) and nothing is voided, refunded or moved for the caller; an OPEN company folio with a zero balance is **CLOSED in the same transaction** (audit `folio.closed` with the reason, and `closed_folios` in the reversal entry and in the response) and stays linked to the cancelled stay as history; a company folio already CLOSED is left alone; the guest folio is unlinked as before and keeps its payments (the deposit). A deferred constraint trigger (00064) makes `CANCELLED stay + OPEN folio` impossible at COMMIT, as a safety net and not as the logic. After a reversal the existing `FOLIO_CLOSED` guards later postings; no check was added to the posting paths. **A second finding of the race test:** with the stay locked FOR UPDATE a payment being posted (it holds the folio and key-shares the stay through `folio_items.stay_id`) and the reversal (it holds the stay and waits for the folio) deadlocked (`40P01`, `RESOURCE_BUSY`); the reversal now takes the stay row `FOR NO KEY UPDATE` (new `db.ForNoKeyUpdate`; the status and version are not key columns), and the outcome is deterministic: the payment wins and the reversal refuses, or the reversal wins and the payment gets `FOLIO_CLOSED`. **Tests:** `internal/frontdesk/reverse_folios_test.go` (empty company folio closed and re-check-in, payment refused with no trace, void then reverse, guest-folio payment kept, refund then reverse, charge on the guest or company folio even when reversed, closed company folio left alone, reverse against payment and against charge 8 runs each, four reversals at once, tenant and property isolation, check-out still closes the company folio and a late payment gets `FOLIO_CLOSED`), the schema tests of 00064, `StayDetailView` tests. **Historical data:** no backfill is written; this was the only path that cancels a stay, the development database has no cancelled stay, and an orphan found later is closed with the folio close or settled with a void or refund first. **CI-only failures: none.** On the development machine the auditlog package once failed with a Docker reaper timeout and passed when run again. **Follow-up, not changed:** check-out and the room-night posting also take the stay `FOR UPDATE` and the folios next, so the same wait cycle with a payment on the same stay can exist there; it ends in a deadlock error (no corruption) and was not part of F-06 |

#### F-07 · Medium · "Ledger rows are written only on the OPEN business day" is a convention

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `CLAUDE.md` says every business-dated write first calls `RequireOpenBusinessDay`. The database only has a foreign key from `business_date` to `business_days` (the trigger of `00003` forbids changing a CLOSED day row, not inserting into it). A scan found a reference to the day in every file that inserts a folio item, payment, journal, bill, receipt, shift, clearing, settlement, tax payment or adjustment, but no test or trigger proves it per function |
| Risk | A new use case that forgets the call can write a ledger row dated a closed day, after that day's journal was posted: the day journal and the folios disagree, and the control accounts show it only later |
| Recommendation | A trigger that rejects an insert into the ledger tables whose `business_date` is not the OPEN day of the property (with an explicit exception for the backfill and for opening balances if needed), or at least a test that lists the writers and fails for an unknown one |
| Suggested next step | Prototype the trigger on `folio_items` and `payments`, run the whole suite, decide whether to extend |
| **Update (2026-10-08): DONE** · VERIFIED by tests (CI result in the last line of this row) | **Migration 00065**, function `business_day_must_be_open()` and nine triggers. **Protected (the date column is, by the services, always the business date a posting is made on):** `folio_items.business_date`, `payments.business_date`, `stay_charge_postings.business_date`, `city_ledger_receipts.business_date`, `city_ledger_adjustments.business_date`, `city_ledger_invoices.invoice_date`, `cashier_shift_movements.business_date`, `cashier_shifts.business_date_opened` (insert) and `cashier_shifts.business_date_closed` (set on close). BEFORE INSERT (and BEFORE UPDATE OF the close date). **Inventory, table by table, of what is NOT protected and why:** `gl_journals.journal_date` (a manual journal may be dated earlier in an open accounting period, and the backfill makes journals for CLOSED days; a guard here would break both), `gl_journal_lines` (they follow the journal), `gl_day_posts` (the backfill writes them for closed days; the night audit writes them while the day is open), supplier bills, payments, credit notes and allocations, tax payments and returns, bank statements, card settlements (their dates are document dates given by the user, `po.CheckDate` checks them against the accounting period, and the service takes the open day only for the audit entry), and the records that carry a business date for reference only (`housekeeping_*`, `maintenance_requests`, `lost_found_items`, `city_ledger_reminders`, `audit_logs`, `folios.closed_on`). So the DB guard covers the business-dated **ledger and cash** rows, not every business-dated record. **Design:** the trigger reads the day row `FOR SHARE`. The night audit holds it `FOR UPDATE` until it commits, so a write that arrives during the close waits and is then refused; a write that got the share lock first makes the close wait and lands before it. A service call has taken the same share lock already, so nothing new is taken and the lock order is unchanged. A date that is not a business day is left to the foreign key. **Error:** `BUSINESS_DAY_CLOSED` (409), constraint `business_day_must_be_open`, mapped in `internal/platform/db/errors.go`. **Service gate kept:** no `RequireOpenBusinessDay` call was removed or weakened. **Tests:** the schema tests copy a row of each of four tables onto a closed day with a direct insert (no service); Go tests do the same for the other five (`cityledger`, `shifts`), prove the lock with two connections (a write in flight makes the close wait and lands before it; a write that arrives during the close waits and is refused), and run a charge, a payment and the night audit at the same moment six times (no row committed after the close, and the closed day reconciles with the folios). Mutation check: with `FOR SHARE` removed the second lock test fails (the write commits on the closed day), so the lock is what the test measures. **Historical rows:** not examined, not changed. **Not claimed:** operational records with a business date are not guarded; a new ledger table needs its own trigger. **Follow-up:** a test that lists every table with a `business_date`-like column and fails for one that is in neither list was considered and not built; the lists are in migration 00065 |

#### F-08 · Medium · Cancellation fee and no-show fee

| | |
|---|---|
| Status | **MISSING** · VERIFIED |
| Evidence | Charge codes `NO_SHOW_FEE` and `CANCEL_FEE` are seeded (`00014:21`) and the account `4510` exists (`00028:179`); no use case posts either. A reservation without a deposit has no folio at all, so a fee cannot even be charged by hand; the night audit only warns about an open folio of a dead reservation |
| Risk | The hotel's policy cannot be applied in the system; the fee is collected outside it or not at all, and revenue is not recorded |
| Recommendation | A use case "charge a fee to a reservation" (creates the unlinked folio when there is none, posts the fee through `FolioPostingService` with approval) with a suggested amount from a policy per rate plan, offered at cancel and at no-show; or, for the pilot, a documented manual procedure |
| Suggested next step | Ask the owner whether the pilot needs automatic fees or a manual procedure; only then design |

#### F-09 · Medium · Tests that Architecture 18 itself asked for and that are not there

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | Compared with `18` sections 3.12, 4.12 and 5.9: **missing** are the net zero of a transfer through the day-close journal (`PostDay`) per account and department (the test checks per account in SQL, and the department only for "set or not"); a transfer of an INCLUSIVE item; a chain of transfers of a room night (a transferred copy that moves again: the register lookup is by the copy's POSTED row, untested); the extended `TestConcurrentPostingChargesEachNightOnce` with routing; the end-to-end case "company pays the room, guest pays the minibar, two invoices, one city ledger invoice"; a fill racing a booking; a race of `SetBillingInstructions` against check-in, check-out or reverse check-in; a transfer racing a posting run; cross-tenant and permission tests of the transfer and instruction routes at API level; a structural guard that a new sale path must call the evaluator (the guard test is an explicit list); a row in each of the 9 root tables of the currency lock that no test inserts (payments, supplier bills, supplier payments, supplier credit notes, city ledger invoices, city ledger adjustments, tax returns, tax payments, tax opening credits: the definition is checked by text only) |
| Risk | The properties the design relies on are argued, not proved, in exactly the places where money moves between folios |
| Recommendation | Write them; the first three are small and use fixtures that exist |
| Suggested next step | One "Architecture 18, remaining tests" commit |

#### F-10 · Medium · Authorization is not proved structurally; cross-property tests are missing for the financial modules

| | |
|---|---|
| Status | **PARTIAL** (isolation by query) / **UNVERIFIED** (permission per route) |
| Evidence | 617 of 669 queries filter by tenant and property (section 8). A scan of the service methods that take a property id found 109 of 396 without an obvious authorization call in their body; by sampling these are internal helpers or authorize through another helper (section 8.3), which is a reading, not a proof. No test calls every route as a user without permission. No test of `folios`, `cityledger`, `payables`, `bankrec`, `shifts` or `documents` opens an object of property A through property B of the same tenant |
| Risk | A route added later that forgets its permission, or a query that forgets the property, is found by a person, not by a test |
| Recommendation | A table-driven test over the routes registered in `app` (the OpenAPI test already knows them): call each as a user with no permission and expect 403 or 404 (an allow-list for the few routes that are open to any signed-in user); add cross-property tests for the financial modules |
| Suggested next step | The route matrix first; it is mechanical and covers all modules at once |

#### F-11 · Medium · The API contract is checked at route level only

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `TestOpenAPIDescribesEveryRoute` compares method and path. Nothing validates a response against its schema (no OpenAPI validator in `go.mod`); `06-api.md` is a second prose description |
| Risk | A field, an enum value or a nullable that the server sends and the YAML does not say reaches the frontend as an untyped surprise; the generated types give a false sense of safety |
| Recommendation | In the `app` end-to-end tests, validate each response against the schema of its operation (`kin-openapi` or equivalent); retire or generate `06-api.md` |
| Suggested next step | Start with the folio, reservation and night audit responses |

#### F-12 · Medium · Migrations over a database with data

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | Section 10: 21 of 62 migrations have data statements (backfills and seeds for existing rows); only `00060`, `00062` (and the down to 14 of `platform/migrate`) are tested over rows. The CI schema job never ran (F-01) |
| Risk | The first upgrade of the pilot database is the first populated upgrade of most migrations; a failure there is in production, and F-04 has no restore to fall back on |
| Recommendation | A CI job that restores a seeded snapshot of version N (the demo seed: `pms-seed` plus a script that makes a reservation, a stay, charges, a payment, a journal, a bill, a bank statement, a tax return) and migrates to N+1; rehearse it before each release |
| Suggested next step | Build the seeded snapshot once; keep it as a fixture |

#### F-13 · Medium · No metrics, tracing or alerting

| | |
|---|---|
| Status | **MISSING** · VERIFIED |
| Evidence | No metrics endpoint, no tracing; logs are structured with a request id and an access log; `/healthz` and `/readyz` exist |
| Risk | A slow query, a full pool, a failing e-mail queue, a night audit that did not run, a rate of 5xx: nobody knows until a user says |
| Recommendation | A small set: request count and latency by route and status, database pool usage, outbox depth, time since the last night audit per property; alerts on 5xx rate, `readyz` failing and "night audit overdue" (the status already has `night_audit_overdue`) |
| Suggested next step | Log-based alerts are enough for a pilot; a metrics endpoint can follow |

#### F-14 · Medium · The tax invoice CSV export does not neutralise spreadsheet formulas

| | |
|---|---|
| Status | **MISSING** the guard in one of four exporters · VERIFIED |
| Evidence | `accounting`, `budget` and `reports` neutralise cells that start with `=`, `+`, `-`, `@` (`reports/model.go:306` `SafeCell`); `taxinvoice/export.go:30-45` writes `buyer_name`, `buyer_address` and `description` straight into the CSV |
| Risk | A buyer name such as `=HYPERLINK(...)` typed by a user opens as a formula when the file is opened in a spreadsheet. The names are typed by staff or taken from companies, so the risk is low, but the other three exporters already treat it as a rule |
| Recommendation | Use the same cell function for every text cell of the export |
| Suggested next step | A small commit with a test, with the other P1 fixes |

#### F-15 · Medium · The day closes without a journal when accounting is not set up

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `accounting/journals.go:297-310`: `PostDay` returns nil when the property has no accounting settings or the date is before the start date; `nightaudit.Run` does not warn. A backfill of up to 400 pending days exists and is tested |
| Risk | A pilot property that goes live before accounting is set up closes days with no GL; the backfill repairs it only if somebody remembers, and the 400 day cap is a hidden limit |
| Recommendation | A warning in the night audit preview ("accounting is not set up: nothing will be journalled") and an item in the go-live checklist; consider making the first night audit refuse until the settings exist |
| Suggested next step | The warning (one line in `analyze`), plus the checklist |
| **Update (2026-10-07): RESOLVED, with enforcement in the backend** · VERIFIED by tests | P0 #6. **Root cause, traced:** `accounting.PostDay` returned nil without a word when the property had no accounting settings or the business date was before the start date, so `nightaudit.Run` closed the day with no journal; a gap in the system account map or in the department setup was only met in the middle of the close (`ACCOUNT_MAP_INCOMPLETE`, a department error) and a room charge code without a revenue account sent the room revenue to the suspense account without a word. In production a new property gets its chart, settings and map from the creation hook, so the gap needs a property made outside the hook, an edited map or code, or a changed department rule; the check closes all of them. **Fix:** `accounting.Service.Readiness` and `JournalReadiness` (`internal/accounting/readiness.go`), `GET /properties/{id}/accounting/readiness`, and `nightaudit` asks for the check twice: in `analyze` (the preview shows the blockers and `can_run` is false) and again in `Run` after the room charges and **with the accounting settings row locked FOR SHARE**, just before the journal and before the day moves; a blocker rolls the whole transaction back (`409 NIGHT_AUDIT_BLOCKED`, `blockers.accounting_readiness`). No migration. **Blocker codes:** `ACCOUNTING_NOT_SET_UP`, `ACCOUNTING_NOT_STARTED`, `ACCOUNT_MAP_MISSING` and `ACCOUNT_MAP_UNUSABLE` (the ten keys the day close resolves: the three ledgers, cash, card, bank transfer, other payment, suspense, tax payable, service payable), `ROOM_CHARGE_CODE_UNMAPPED`, `DEPARTMENT_SETUP_INCOMPLETE`, `JOURNAL_SEQUENCE_MISSING`. **Warnings, never blocking:** `CODE_UNMAPPED` (another charge code, tax or service charge with no usable account: the engine places it in the suspense, tax payable or service payable account by design) and `ACCOUNT_MAP_OPTIONAL` (retained earnings, accounts payable, input VAT and cash over and short, used by other modules, not by the night audit). **Not rules, because the code path does not need them:** cashier settings (an open shift is its own blocker) and PKP or tax filing settings (the day close does not read them). **Tests:** `internal/accounting/readiness_test.go` (a configured property is ready and its night audit runs and journals; ten broken setups, each reported with its code and ref in the readiness, in the preview and in the `NIGHT_AUDIT_BLOCKED` error, and after each the business day is still OPEN and no day was closed, no journal, no day post and no ledger row was written; several blockers at once; warnings do not block; permission and property scoping; the lock: a change of the account map waits for the night audit's check), `internal/app/readiness_api_test.go` (HTTP), the web test of the night audit screen, and two existing tests changed on purpose (see below). **Behaviour change:** a property whose accounting is incomplete can no longer close a day; before, the day closed with no journal or with the revenue in suspense. Two tests that closed days without accounting now simulate history by taking the journaler off (`SetJournaler(nil)`), and a test fixture that pointed the room charge code at an account that is not in the chart now uses a real account. **CI:** run 21 for `deab8f8` succeeded (https://github.com/retechno/KamaraPMS/actions/runs/37602445902): Go with `go test -race ./...`, lint, sqlc diff, schema on PostgreSQL 16 and 18, web tests and build. Locally the whole web suite timed out on a loaded machine in two runs (33 and 1 tests, all timeouts; the files pass alone), so the local full web run is UNVERIFIED and the CI run is the evidence. **Remaining:** the go-live check has an API and the blocker list on the night audit screen, no page of its own; other charge codes, taxes and service charges with no account stay warnings; `PostPending` (the backfill) is unchanged; the days that closed without a journal before this change are still found only by the reconciliation and the backfill |

#### F-16 · Medium · Coverage of `iam` is 52.5%

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `go test -cover`: `iam` 52.5%, the lowest of the security-relevant packages (`platform/auth` 76.9%, `platform/httpx` 92.4%); 17 tests. `auditlog` 43.6% and `groups` 48.8% are lower still but less critical |
| Risk | Sign-in, refresh rotation and replay detection, session revocation, the approval check and its throttle are the doors of the system |
| Recommendation | Read the uncovered lines of `iam` and add tests for each branch of refresh, replay, deactivation, password change, approval failure and throttle |
| Suggested next step | A coverage report of `iam` first, to see which branches |

#### F-17 · Low · No MFA, no self-service password reset, an approver may be the actor

| | |
|---|---|
| Status | **MISSING** · VERIFIED |
| Evidence | The auth routes are login, refresh, logout, change password (own), and a reset by a tenant administrator; no second factor. A correction needs an approval, and the approver may be the person who is signed in (the design says so) |
| Risk | A stolen password of a tenant administrator is the whole tenant; one person can post and approve their own correction |
| Recommendation | MFA at least for tenant administrators; an option "the approver must be another user" for corrections above a limit |
| Suggested next step | After the pilot, unless the owner wants separation of duties from the first day |

#### F-18 · Low · Test infrastructure is flaky on the development machine

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | Two of six complete Go runs had one package fail with "wait for reaper ... context deadline exceeded" (`bankrec`, then `rates`); both passed alone. Two of six complete web runs had tests time out at 5 seconds; they passed alone. Background runs were stopped by the system for low memory more than once (the Docker VM has 4 GB); the development server holds 9 `pms_test_*` databases left by killed or interrupted runs |
| Risk | A red run that is only infrastructure teaches people to rerun, which hides a real failure |
| Recommendation | For local runs use `PMS_TEST_DATABASE_URL` against the compose database or set `TESTCONTAINERS_RYUK_DISABLED=true`; raise the timeout of the heaviest web test files |
| Suggested next step | Write the local-run advice into `CLAUDE.md` |

#### F-19 · Low · System journals do not check the decimals of the currency

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `accounting/journals.go:524` refuses more decimals than the property's for manual journals; `Poster.Post` (`accounting/posting.go:191`) checks only that debit equals credit; the column accepts three decimals |
| Risk | A module that computes an unrounded amount for a property of zero decimals would post it |
| Recommendation | Assert the decimals in `Poster.Post` and in the day close |
| Suggested next step | Two lines and a test |

#### F-20 · Low · Credit notes of the city ledger compute tax outside the charge engine

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `cityledger/adjustments.go:381`: `tax = net * rate / 100`, rounded; `CLAUDE.md` says only `ChargeCalculationEngine` computes tax and service. The formula handles an exclusive tax on a net amount only (no tax on service, no inclusive mode) |
| Risk | A credit note of an invoice built from inclusive or service-taxed charges could differ from the tax of the invoice it reduces |
| Recommendation | Take the tax from the lines of the invoice being credited (a share of what was charged) or call the engine |
| Suggested next step | A test with a service-taxed charge first, to see whether the numbers differ |

#### F-21 · Low · Idempotency does not compare the request

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `folios/charges.go:49-62`: a key already used on the same folio returns the stored result whatever the new request says; on another folio it is `IDEMPOTENCY_KEY_REUSED` |
| Risk | A client bug that reuses a key with another amount is answered with success and the first amount |
| Recommendation | Store a hash of the request with the key and refuse a mismatch |
| Suggested next step | With the next change of the keyed use cases |

#### F-22 · Low · A billing instruction ignores the state and the credit of the company

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED |
| Evidence | `SetBillingInstructions` checks that the company exists in the property, not that it is active; the credit limit is checked only at the city ledger transfer (`LockForTransfer`), so charges accumulate on a company folio without a check |
| Risk | Exposure on a company that is over its limit or inactive builds up for the length of the stay and surfaces at check-out |
| Recommendation | Refuse an inactive company; show the exposure of the open company folios next to the limit on the company account |
| Suggested next step | The owner decides whether a limit should stop the routing |

#### F-23 · Low · Currency: accepted exclusions and one column

| | |
|---|---|
| Status | **ACCEPTED** by the owner · VERIFIED |
| Evidence | `reservation_room_rates` does not lock the currency and `bank_accounts` alone does not (the owner's decisions of 2026-10-06); `reservation_room_rates.grid_rate` is `numeric(18,2)` |
| Risk | A property with only bookings can change its currency and keep prices in the old unit; a 3-decimal currency would cut the reference grid rate |
| Recommendation | Keep the decisions; change `grid_rate` to `numeric(18,3)` in the next migration |
| Suggested next step | None needed for the pilot |

#### F-24 · Low · Frontend polish

| | |
|---|---|
| Status | **PARTIAL** · VERIFIED (heuristic scan) |
| Evidence | Loading states in 11 of 78 data views; no test of the user and role administration views; floats in a few displayed totals and hints; enumerations such as the reservation source are shown as the raw code |
| Risk | A screen that looks empty while loading invites a second click; admin screens are untested |
| Recommendation | Pass `loading` to every table; test `UsersView` and `RolesView`; show money sums from the server or with decimal strings |
| Suggested next step | With the first UI pass after the pilot feedback |

#### F-25 · Info · Housekeeping items

| | |
|---|---|
| Status | VERIFIED |
| Evidence | `ListAuditLogsForEntity` has no caller; `LevelTax` and `LevelBudget` share the lock level 49; migration `00041` classified existing taxes as VAT by a name pattern; 263 audit-column foreign keys have no index (harmless) |
| Risk | none now |
| Recommendation | Remove the dead query; note the shared level where the lock order is documented |
| Suggested next step | With a cleanup commit |

#### F-26 · Medium · Documentation drift

| | |
|---|---|
| Status | **DOCUMENTATION DRIFT** · VERIFIED |
| Evidence | Section 19 lists 13 places where a document says something the repository no longer does; the most serious is `02-database-schema.md`, which describes 34 of the 101 tables and is the schema source `CLAUDE.md` tells a reader to consult |
| Risk | A person or a tool that "reads the architecture before changing behaviour" starts from a false picture |
| Recommendation | One documentation commit that fixes the list; a rule that a step is not done until the status lines are updated |
| Suggested next step | After F-01, before the pilot |

#### F-27 · Medium · Test and CI scope: no browser end-to-end, no dependency scan, no coverage gate

| | |
|---|---|
| Status | **MISSING** · VERIFIED |
| Evidence | `ci.yml` has no browser test, no `govulncheck`, no `npm audit`, no coverage threshold, no image build; the web tests mock the API client; the owner checks the screens by hand (the repository records that the assistant never saw one) |
| Risk | The path reservation, check-in, charge, payment, check-out, night audit is tested at the API and at the component level, never in one browser against one server; a known vulnerability in a dependency is not noticed |
| Recommendation | One Playwright smoke test of that path against the real API in CI; `govulncheck` and `npm audit --omit=dev` as non-blocking steps first |
| Suggested next step | The smoke test, after the deployment recipe exists (it can reuse it) |

## 18. Prioritized Roadmap

The order follows the evidence of this audit, not the order in which things were discussed.

### P0: before the pilot

| # | What | Findings | Why it is P0 |
|---|---|---|---|
| 1 | **Make CI run and green, and required for `main`.** Execute bit on the scripts, first full run, fix what it finds | F-01 | Until this is done no guarantee of the project is evidence. **Done on 2026-10-07 (`c38ca68`, run 17 green); branch protection set by the owner and verified from GitHub on 2026-10-08** |
| 2 | **(Done: CI run 17, 264 s, clean)** **Run the race detector over the whole suite** (CI after item 1; the container command for a local run, package by package because of memory) and fix reports | F-02 | The locking design is the heart of the system; six packages are clean, the rest is unproved |
| 3 | **(Done for one machine and verified, 2026-10-07: `docs/deployment.md`, CI run 18 green)** **A deployment recipe**: Dockerfile, production compose or service files, reverse proxy with TLS and headers serving `web/dist`, the variables, a smoke test, and the decision "one instance" for the pilot | F-03 | Nothing exists to put in front of users |
| 4 | **(Pilot mechanism built and drilled once, 2026-10-07: `docs/backup-restore.md` sections 13 to 16; owner decisions and the real drill open; production DR not built)** **Backup, restore and rollback runbook with one rehearsed restore** and agreed RPO and RTO | F-04 | The whole ledger is one database |
| 5 | **(Done, 2026-10-07)** **Client address and rate limits behind the proxy** (trusted proxy setting) | F-05 | Part of the recipe: without it one bucket serves the whole hotel |
| 6 | **(Done, 2026-10-07: the night audit refuses a day it cannot journal; see F-15)** **Go-live checklist**: accounting settings and start date, charge-code accounts, department setup check, cashier settings, PKP settings, a first night audit preview | F-15 | A property can go live without a GL and nothing says so |

### P0 close-out status (2026-10-07, after `7ee10c4`)

| # | Status | Evidence | What is left |
|---|---|---|---|
| 1 | **DONE** (CI, and branch protection set by the owner and verified from GitHub, 2026-10-08) | CI run 17 onward green; run 24 green for `4f0dece` | none |
| 2 | **DONE** | `go test -race ./...` in CI, runs 17 to 21 | none |
| 3 | **DONE for one machine** | CI run 18; `scripts/prod-smoke.sh` 57 checks | a real server and a certificate (need a server and a domain) |
| 4 | **PILOT mechanism BUILT and drilled once; owner decisions open; production DR NOT built** | Decided by the owner on 2026-10-07 (`docs/backup-restore.md`, section 13): pilot RPO 24 h, RTO 4 h (to be measured), daily backup after the night audit and before every upgrade, mandatory secondary copy on another machine or disk, the 14 most recent backup files locally (file-based, not calendar days) and 14 daily recovery points + 4 weekly (in addition) on the secondary, no WAL/PITR, encryption mandatory for the secondary copy. **Built (section 14):** the `backup` service (scheduler with catch-up, health status), the failure alert (webhook, plus an optional dead-man ping), the secondary copy (a directory on another file system, or SSH with the host key checked, never a named provider), age encryption with the PUBLIC key on the server and the private key off it, pilot retention, the procedure for the backup before an upgrade (section 15), and a drill that restores from the encrypted secondary copy. **Drilled (section 16):** a scheduled run, catch-up, directory and SSH copies, retention, alert delivery to a local receiver, restore from the encrypted copy with an identical fingerprint and the API ready in 25 s on 1.3 MB; wrong key, changed file and no key each refuse. **Production values (RPO 15 min, RTO 1 h, WAL/PITR, off-site with separate credentials, 14/8/12/1 retention, encryption at rest) are TARGETS: not built, not drilled, not to be claimed** | **OWNER CONFIGURATION, not complete (the pilot configuration is therefore not complete):** the hour of the backup, the alert webhook, the secondary location, the owner's private key and its two storage places, the accountant's retention answer (production tiers). **Secondary retention is decided: 14 daily + 4 weekly, the weekly ones in addition to the daily ones** (a weekly-only secondary would lose up to 7 days against a 24 h RPO; implemented and tested: 15 files kept out of 46). **Not proved:** the 25 s is for 1.3 MB, so the 4 h RTO is not demonstrated; the alert reached a local receiver, not the owner's channel; the secondary copy was on the same machine; CI does not run these scripts. **CI:** run 24 for `4f0dece` is green (https://github.com/retechno/KamaraPMS/actions/runs/37640345578); it does not run the backup scripts. **Then:** the final drill with the real secondary copy, the real private key and the real deployment, time written in section 16 |
| 5 | **DONE** | `PMS_TRUSTED_PROXIES`, smoke test | none |
| 6 | **DONE** | `deab8f8`, CI run 21 green: the night audit asks the readiness check twice (in the preview, and in `Run` after the charges and with the accounting settings row locked FOR SHARE, before the journal and the day move); a blocker gives `409 NIGHT_AUDIT_BLOCKED` with `blockers.accounting_readiness`, rolls the whole transaction back and leaves the business day OPEN (tests assert no closed day, journal, day post or ledger row) | none for new days |

**Branch protection for `main` (set by the owner in GitHub, Settings, Branches, and verified from GitHub on 2026-10-08).** Required status checks, as the green runs show them (the names are the job names of `.github/workflows/ci.yml`): `Go (lint, vet, test)`; `Schema (migrations up/down/up + integrity tests) (postgres:16-alpine)`; `Schema (migrations up/down/up + integrity tests) (postgres:18-alpine)`; `Web (types, tests, build)`. The workflow runs on pushes to `main` and on pull requests, so the checks exist for both. Suggested with it: require a pull request, require the branches to be up to date, no force pushes. The repository neither sets nor checks this setting; its state is what the owner verified in GitHub.

**Historical closed days.** A business day that closed without a journal before P0 #6 is **not** repaired automatically, on purpose: a journal made later for a past date changes reports that may already have been used. Such days are found by the accounting reconciliation (`PendingDays`) and repaired by the separate, explicit backfill (`PostPending`, permission `accounting.close`, at most 400 days a call). It is a decision for the owner whether and when to run it on a real property.

**Engineering work left in P0: none.** What remains is the OWNER CONFIGURATION of P0 #4 (`docs/backup-restore.md`, section 13) and the final drill with the real values. Three states are kept apart: **pilot mechanism implemented** (built, drilled once on one machine); **pilot configuration not complete** (the hour, the alert webhook, the secondary location, the owner's private key and its two storage places, set by the owner at deployment; the accountant's retention requirement for the production tiers); **production DR not ready** (RPO 15 min, RTO 1 h, WAL/PITR, off-site with separate credentials, 14/8/12/1 retention, encryption at rest are targets, not capabilities). The verdict stays **CONDITIONAL PILOT**: item 4 is closed only when the configuration is set and the final drill has measured the recovery with the real secondary copy and the real key.

### P1: important, does not block the pilot

| # | What | Findings |
|---|---|---|
| 7 | **(Done, 2026-10-08)** Reverse check-in with company folios and payments; close the empty company folio | F-06 |
| 8 | **(Done, 2026-10-08, migration 00065)** A database or test guard for "ledger rows only on the OPEN day" | F-07 |
| 9 | The Architecture 18 tests that were asked for and are not there; fill-versus-booking race; structural guard for sale paths | F-09 |
| 10 | Route-by-permission matrix test; cross-property tests for the financial modules | F-10 |
| 11 | Response validation against OpenAPI in the end-to-end tests | F-11 |
| 12 | Migrations over a seeded snapshot in CI | F-12 |
| 13 | Basic monitoring and alerts (log-based first) | F-13 |
| 14 | Tax invoice CSV formula neutralisation | F-14 |
| 15 | **(Done by P0 #6, as a blocker and not only a warning)** Night audit warning when accounting is not set up | F-15 |
| 16 | Tests for the branches of `iam` | F-16 |
| 17 | One browser end-to-end smoke test; `govulncheck` and `npm audit` in CI | F-27 |
| 18 | Documentation sync (section 19) | F-26 |
| 19 | Cancellation and no-show fee: decide automatic or manual for the pilot | F-08 |
| 20 | **(Built, 2026-10-08: `20-transaction-group.md`)** **Transaction Group / Split Bill**: a design document first (section 16), then the feature | section 16 |

**Where Transaction Group sits and why.** It is P1 and the first new feature after the P0 list and the folio correctness items (7 and 9), for three reasons from the evidence: it adds a column to the two append-only ledger tables and a filter to every folio view and document, so it must land on a green CI and on tests that already cover transfers; the payer split that pilots with corporate guests need is already built (several folios, instructions, transfer), so no evidence makes it a blocker; and the design has one open question the ledger forces (how a group changes after posting). It is not a P0.

### P2: after the pilot

MFA and separation of duties (F-17); local test infrastructure advice (F-18); decimals check in system journals (F-19); credit-note tax from the invoice lines (F-20); request fingerprint in idempotency keys (F-21); company state and exposure at routing (F-22); `grid_rate` scale (F-23); frontend polish (F-24); housekeeping items (F-25); master folio, automatic company transfer at check-out, channel restrictions, MT940 import (all in `08-backlog.md`).

## 19. Documentation Drift

| # | Where | What it says | What the repository shows |
|---|---|---|---|
| D-01 | `18-architecture-decisions.md`, status line | steps 1 to 3 are built "the rest is not"; written from `b361280`; "changes no code" | all seven steps are built and pushed (`fbecf6f`) |
| D-02 | `18` section 14, rows 1 and 2 | row 1: "the read-only fields on the property form are not done"; row 2: "nothing sells by them yet"; the restriction steps are "part 1" and "part 2" | both are done; the commits and the README call the three restriction commits parts 1, 2 and 3 |
| D-03 | `18` section 3.8 | lists `POST /stays/{id}/folios` as a new endpoint | not built, on purpose: setting an instruction opens the folio (the table of section 14 says so; section 3.8 does not) |
| D-04 | `README.md` (repository root), top | "Current state: **M2 (identity & access) complete**"; a stray line `sssssssssssdssssssss` in the quick start | 62 migrations, all milestones and the later features are built |
| D-05 | `docs/architecture/README.md` | the DDL is "11 files, 34 tables ... 80 schema tests" | 62 files, 101 tables (100 plus the goose table), 514 schema tests |
| D-06 | `docs/architecture/README.md`, VAT on the card commission | "steps 1 and 2 ... steps 3 to 5 are not built" | the feature map (row 24) and `15-card-fee-vat.md` list the settlement with the final VAT as built, with `settlement-preview` and `settle` routes |
| D-07 | `docs/architecture/README.md`, city ledger | "Not done, on purpose: ... dunning, interest and write-offs, and a general ledger posting" | credit notes, write-offs, overdue list, reminders and the late fee are built, and the city ledger posts journals |
| D-08 | `CLAUDE.md`, status | "Since then: PDF documents and confirmation e-mail, corporate accounts, groups and the city ledger" | finance, budget, departments, tax invoices, shifts, bed variants, restrictions and the folio model have followed |
| D-09 | `02-database-schema.md` | "Inventory (34 tables)", the schema source named by `CLAUDE.md` | 100 tables; the other 66 are described only in the README, the feature map, the design documents and the migrations |
| D-10 | `13-feature-map.md` | module table without `rate_restrictions`, `folio_billing_instructions`; row 27 has "the commit after it" for step C | `1b590b7`; no row for the bank statement currency guard (`7e19756`) or the leftovers (`fbecf6f`) |
| D-11 | `05-transactions-locking.md` | the use-case matrix | no row for the charge transfer (business day, then two folios ascending), for setting billing instructions (business day, reservation, stay, sequence) or for the restriction fill |
| D-12 | `06-api.md` | a prose description of every route | a second source of truth beside `openapi.yaml`; only the YAML is tested; sampled sections agree |
| D-13 | the documents as a set | the brief for this audit names `docs/13-feature-map.md` and `docs/08-backlog.md` | they are in `docs/architecture/`; the numbering skips 17 |

## 20. Final Verdict

**CONDITIONAL PILOT.**

Why not NOT READY: the system does what a hotel needs for the whole cycle, the money paths are protected by the database as much as by code (append-only ledger, balanced journals, exact numbers, one writer per ledger, scoped keys), Architecture 18 is implemented as decided, and everything that could be run on the audit machine passed: the Go suite, the schema tests on PostgreSQL 16 **and** 18, lint, the generated code, the web tests and build.

Why not PILOT READY: a pilot needs evidence that the checks hold and a way to run and recover the system, and both are missing.

- **Blocker 1, F-01 (resolved 2026-10-07)**: CI had failed on all 16 runs since the first commit because the scripts were not executable. After `c38ca68` run 17 is green in every job; what is left is to make the checks required for `main`.
- **Blocker 2, F-03 and F-04** (as found; later state in brackets): there is no deployable artefact, no definition of where the SPA is served or TLS terminated, and no backup, restore, RPO or RTO. [F-03: a one-machine recipe exists and is verified (CI run 18). F-04: scripts exist and one real restore was compared table by table on the development machine (CI run 19 is green, but CI does not run the scripts). **Not closed:** RPO 24 h and RTO 4 h are PROPOSED, not agreed, and the RTO was not measured on production-sized data; no backup schedule, no off-machine copy, no point-in-time recovery, no encryption of the files; tables that were empty in the source (city ledger, supplier payables, tax filing, bank statements) are UNVERIFIED with data. The system is **not** declared recovery-ready for production.]
- **Blocker 3, F-05**: the rate limit would be one shared bucket behind the proxy.
- **Condition, F-02 (resolved 2026-10-07)**: the race detector passed on the six packages checked in the container, and then on the whole suite in CI run 17.
- **Condition, F-15** (as found; resolved by P0 #6: the night audit now refuses to close a day without a journal and names what is missing): the pilot property must have accounting set up before its first night audit; the system does not say so.

**Situation after P0 #1 to #6 (2026-10-08), still CONDITIONAL PILOT:** P0 #1 is done (branch protection set and verified by the owner). P0 #6 is done: a property cannot close a day without a journal. P0 #4: the pilot mechanism is implemented; open is the OWNER CONFIGURATION (backup hour, alert webhook, secondary location, private key and its storage, accountant's retention) and the final drill with the real values.

The verdict becomes **PILOT READY** when P0 items 3 to 6 are closed (items 1, 2, 3, 5 and 6 are done; item 4 needs its configuration and the final drill) and the CI is required for `main` (done). It would become **PRODUCTION READY** after the P1 list, with the rehearsed restore, the monitoring and the route-by-permission test as the items that matter most. Nothing in this audit is a feeling: every line above has the evidence next to it.

