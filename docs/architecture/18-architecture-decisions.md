# 18. Architecture decisions before the pilot: folio model, rate restrictions, currency policy

Status: **approved by the owner on 2026-10-06; steps 1 (the currency lock, migration 00058), 2 (the restriction table, the evaluator and the grid screen, migration 00059) and 3 (the evaluator on every sale path, the override, the search verdicts) of section 14 are built, the rest is not.** Written on 2026-10-06 from the code of `main` (head `b361280`). It changes no code, migration, API or screen. It does not repeat what `13-feature-map.md` already lists as built, nor what `08-backlog.md` keeps as left out on purpose; where a decision touches a built feature, the feature is named and only the change is described.

The three decisions were chosen because each one reaches many modules later: a folio is read by the room charge posting, the night audit, check-out, invoices, tax invoices, the city ledger and the general ledger; a sales restriction has to be seen by every path that sells a night; and the currency of a property is assumed in every money column. Settling them now costs a document; settling them after the pilot costs a migration of live data.

## 1. Executive Summary

| # | Decision | In one sentence |
|---|---|---|
| 1 | **Folio model** | A stay may have **several folios**, one per payer: the guest folio that exists today, plus at most one **company folio** per company. A small **billing instruction** on the reservation line says which charges go to the company; one function resolves the target folio. Charges move between folios of one reservation by reversal and re-posting. Master (group) folios are reserved in the design but **not** built: a city ledger invoice over several company folios already gives a group one bill. |
| 2 | **Rate restrictions** | One table, `rate_restrictions`, a daily grid per room type and rate plan (both optional) holding stop sell, closed to arrival, closed to departure, minimum and maximum stay. One function in the availability package, `EvaluateStay`, is the only place that reads it; reservations, front desk, search, calendar and the future channel manager all call it. A restriction is a **hard block** that staff with a permission can override with a reason and an approval, and the override is audited. |
| 3 | **Currency policy** | One currency per property, as today. The currency (and its decimals) is changeable **only while the property has no financial record of any kind**; today the lock looks at folio items only, which leaves a gap. One database function becomes the single definition of "financial data". Multi-currency later is **functional currency plus tender currency on the payment**, never a currency on the folio item or the journal. |

Recommended order: currency lock (days), restrictions (a short feature), folio generalisation (the largest, in four small steps). Section 14 has the steps.

The three points that are the owner's call, because the code cannot decide them: the tie-break of two restriction rows of the same specificity (section 4.5), what to do when the company folio of a night is closed (section 3.5), and who may override a restriction (section 4.8).

## 2. Current State (read from the code)

### 2.1 Folios

- `folios` (`00010_ledger.sql`): `reservation_id NOT NULL`, `stay_id` nullable, `folio_type` with `CHECK (folio_type IN ('GUEST'))`, status OPEN or CLOSED, a `version`. The balance is never stored (debit minus credit of `folio_items`).
- Two partial unique indexes: `folios_stay_guest_uk` (one GUEST folio per stay) and `folios_unlinked_open_uk` (at most one open folio per reservation that has no stay yet, the deposit folio).
- A deposit (`POST /reservations/{id}/deposits`) goes to the unlinked folio, created on the first deposit. Check-in links it to the stay (`LockUnlinkedFolio`, `AttachStayFolio` in `folios/stayfolio.go`), or creates the stay folio when there is none. A reversed check-in unlinks it again if no CHARGE item is on it. With a multi-room reservation the deposit folio goes to the first stay that is checked in.
- The code is already written in the plural in three places: `ListStayOpenFolios`, `CloseStayFolios` ("closes every OPEN folio of a stay"), `StayFolios`; the stay detail API returns `folios[]` and `StayDetailView.vue` loops over `detail.folios`. Only the database (`folios_stay_guest_uk`, the `GUEST` check) and three single-folio lookups stand in the way.
- The single-folio assumption lives in: `GetFolioOfStay` (`folio_type = 'GUEST'`), the loader of the expected charge engine (`expected/loader.go`, `Snapshot.Folios`: "stay id to its OPEN guest folio", query in `expected/queries.sql`), and `AttachStayFolio`.
- Room nights: `roomcharge` takes the folio of each due night from the expected charge, locks the folios in ascending id (level 4, before any sequence) and calls `folios.RoomPoster.Post`. The idempotency key of a night is **`stay_charge_postings (stay_id, service_date, charge_source)`**, not the folio, so the target folio can change without any risk of a double charge.
- `folio_items.stay_id` is copied from `folio.stay_id` at posting (`posting.insert`); a manual charge, adjustment, payment, refund or transfer names its folio in the URL (`/folios/{id}/...`).
- Payments are folio-scoped rows (`payments.folio_id`). A payment is never moved: it is voided or refunded. A **city ledger transfer** is a payment with method `CITY_LEDGER` and a `company_id` (`folios.Transfer`): any amount up to the folio balance, gated by the credit limit of the company; city ledger invoices later claim transfers, and a tax invoice of such an invoice lists "the folios behind" it (`taxinvoice/queries.sql`).
- Check-out: `CloseStayFolios` needs **every** open folio of the stay at exactly zero (`FOLIO_NOT_BALANCED`); `folios/{id}/close` refuses a folio of an open stay (`FOLIO_LINKED_TO_OPEN_STAY`).
- Documents: `documents.Invoice(folioID)` is already per folio (number = folio number, `Final` when closed); a tax invoice (faktur pajak) has source `FOLIO` or `CITY_LEDGER_INVOICE`.
- General ledger: the day close reads the view `folio_item_gl` (last form in `00052_department_posting.sql`). Charges go to revenue, tax and service accounts by charge code; payments go to the **guest ledger** control account by method, or to **advance deposits** when `is_deposit`. `is_deposit` is true for a payment that was taken before the check-in of the stay or on a folio without a stay (and is not a city ledger transfer). The deposit is released when its folio closes (`folios.closed_on`). Folios are not a GL dimension.
- The reservation has `company_id` and `booking_group_id` (`00022`): they are links for reporting and the credit gate, they do not route money.

### 2.2 Sales restrictions

- None exist. `04-operations.md` §14.1 says "a restriction hook (min stay, closed to arrival, stop sell) is reserved for later"; `00-review.md` B12 and the README ("Not included") say the same. There is no table.
- Every path that sells a night passes one of: `reservations.checkHolds` (create, confirm, reinstate, add a line, amend dates, type or bed, assign a room, unassign; the walk-in reaches it through `CreateHeld`), `frontdesk.checkInCore` (an upgrade), `frontdesk.Move` (another type), `frontdesk.extend` (a longer stay). Since the bed variants, `checkHolds` is the single place that builds the demand and calls `availability.RequireAvailableFor`; the extension and the move build their own demand beside it.
- The read side: `SearchAvailability` (the offers), `AvailabilityCalendar`, and the yield rule quote (`GET /rate-quotes`).
- Yield rules (`00036`) already have a scope of plan and room type (both optional, NULL = all), stay dates, weekdays, priority; they change a **price**. A restriction changes **whether** a night can be sold.

### 2.3 Currency

- `properties.currency_code` (ISO 4217) and `currency_decimals` (0 to 3), validated in `tenancy`. All money columns are `numeric(18,3)` and the application rounds half away from zero at `currency_decimals` (`platform/money`).
- **No other table has a currency column.** `payments.currency_code` is a rejected decision (`02-database-schema.md`, "There is no `currency_code` column (rejected)"; `CLAUDE.md`). Folio items, payments, journals, bank accounts and statements, supplier bills, city ledger, cash drawers, budgets and tax returns are all in the currency of their property by construction.
- The lock: the trigger `properties_currency_lock` (`00010`) and the service check `PropertyHasFinancialData` (`tenancy/queries.sql`) both ask only `EXISTS (SELECT 1 FROM folio_items WHERE property_id = ...)`. A property can therefore post manual journals, supplier bills, bank statements, cashier shifts or budgets in one currency, and still change its currency or its decimals as long as no guest has been charged. That is a gap, not a design.
- Reports and documents print the currency of the property (`dc.prop.CurrencyCode`); nothing sums across properties.

## 3. Decision 1: Folio model

### 3.1 Problem

A stay has exactly one place to put money. The cases the audit listed all need a second one: a company pays the room and the guest pays the minibar; two payers on one stay; a charge posted to the wrong room of a booking; one invoice for the company and another for the guest; a group whose company wants one bill. Today the only tool is a city ledger transfer of an **amount** of the whole folio at check-out, which cannot say "these charges".

### 3.2 Current behavior

See 2.1. In short: one GUEST folio per stay, a deposit folio before it, routing nowhere (a night goes to the one folio), payer split only by transferring an amount to the city ledger, one invoice per folio, checkout needs every open folio of the stay at zero.

### 3.3 Options considered

| Option | What it is | Verdict |
|---|---|---|
| A | Keep one folio per stay; split the bill only by city ledger transfers of an amount | Cannot split by charge; the guest's invoice always shows the company's room too. Leaves the audit finding open. |
| **B** | **Several folios per stay, one per payer** (guest, company); a **billing instruction** per reservation line routes charges; a **transfer of a charge** between folios of a reservation | **Recommended.** Uses what the code already does in the plural; every other module keeps working with a default that equals today's behavior. |
| C | A full "folio window" model: up to N windows per stay, routing by charge code and date range, per-night routing, master folios, split and merge of folios | Too large for a pilot; most of it is not asked for by the cases above. B does not prevent it. |
| D | A separate "billing account" entity (a payer) that owns folios | A second ledger concept next to the city ledger company account. The company folio plus the city ledger transfer already is that. |

### 3.4 Recommended option

**Option B, in four small steps, each of which leaves the system working with no routing configured.**

### 3.5 Why

- The unit of idempotency is the stay, not the folio, so room charge posting can change target without a double charge risk.
- Check-out, the stay API, the stay screen, the invoice and the tax invoice are already folio-plural or per folio.
- Money still ends in the same two general ledger places as today: the guest ledger (while it sits on a folio) and the city ledger (after a transfer). **No general ledger account, journal or report changes**, except the one view rule in 3.9.
- A group "master bill" is, in this model, a **city ledger invoice over the transfers of the company folios of its members**: `city_ledger_invoices` already claim transfers from many folios and a tax invoice already lists them. That removes the main reason for a master folio.
- The target of a routing rule is a **folio id**, resolved at check-in. When a master folio is built later it is just another folio id; no rule has to be rewritten.

### 3.6 Data model (design only)

```
folios                                   -- existing table, three changes
  folio_type          CHECK IN ('GUEST', 'COMPANY')      -- was ('GUEST'); 'MASTER' is reserved in this document, not added yet
  bill_to_company_id  bigint NULL, FK (property_id, company_id) -> companies
  CHECK ((folio_type = 'COMPANY') = (bill_to_company_id IS NOT NULL))
  -- replaces folios_stay_guest_uk:
  UNIQUE (stay_id, folio_type, COALESCE(bill_to_company_id, 0)) WHERE stay_id IS NOT NULL
  -- folios_unlinked_open_uk (the deposit folio) is unchanged.

folio_billing_instructions               -- new: the intent, set on a reservation line (before and during the stay)
  id, tenant_id, property_id
  reservation_room_id  bigint NOT NULL, FK (property_id, reservation_room_id)
  scope                varchar  CHECK IN ('ALL', 'ROOM', 'CHARGE_CODE')
  charge_code_id       bigint NULL, FK; CHECK ((scope = 'CHARGE_CODE') = (charge_code_id IS NOT NULL))
  company_id           bigint NOT NULL, FK (property_id, company_id)
  created_at, created_by, updated_at, updated_by
  UNIQUE (reservation_room_id, scope, COALESCE(charge_code_id, 0))
  -- reserved for a later step, not created now: target_folio_id (a folio id, for a master folio) with CHECK exactly one of company_id and target_folio_id
```

- **Scope** `ROOM` means a charge whose charge code is of type ROOM (the night, with its tax and service, which are components of the same item). `CHARGE_CODE` means one code (for example the minibar). `ALL` means everything not named by a more specific instruction.
- A folio is never created without a reason: the company folio of a stay is created **eagerly**, at check-in (inside `AttachStayFolio`) or when an instruction is added to an in-house line, **before** any posting run takes its locks. This matters: creating a folio takes a document number, and the lock order puts sequences last, so it must not happen in the middle of the night audit.
- Folio numbers come from the same `FOLIO` sequence.

### 3.7 Business rules

1. **Default**: with no instruction a charge goes to the GUEST folio. Existing data and every existing flow behave as today.
2. **Resolver**: one function, `folios.ResolveTarget(stay, chargeCode)`, is the only place that decides a target folio. Order of choice: an instruction with `CHARGE_CODE` for that code; else `ROOM` if the code is of type ROOM; else `ALL`; else the GUEST folio. Its answer is a folio id. The expected charge engine asks it instead of reading `Snapshot.Folios`; the manual charge endpoint keeps naming a folio explicitly.
3. **A closed target is a blocker, not a fallback.** If the resolved folio is closed (or missing) the posting fails with `ROUTING_TARGET_CLOSED`; in the night audit that is an existing kind of blocker (`ChargeErrors`). Falling back to the guest folio would bill a guest for what the company agreed to pay. *(Owner's call: this is the safe choice; the alternative is a warning and a fallback.)*
4. **When a rule applies**: from the next night not yet posted. A night already posted stays where it is; moving it is a transfer (rule 6). The same rule as a price change (`NIGHT_ALREADY_POSTED`).
5. **Payment allocation**: a payment belongs to exactly one folio, the one the cashier posts it to (the screen proposes the folio with a positive balance). There is no allocation across folios and no new table. Moving a payment is void and repost, as today. Company deposits before check-in stay out of scope: the single unlinked deposit folio keeps taking deposits (see 3.12).
6. **Transfer of a charge** (not in the first step): `POST /folio-items/{id}/transfer {folio_id, reason}`. Both folios must be OPEN, of the **same reservation** (so a charge can go to another room of the booking, or to the company folio), same property; permission `folio.reverse` plus the approval that corrections already need. It writes a REVERSAL on the source folio and a new CHARGE on the target folio, **copying the snapshot of the original** (amounts, components, revenue account, department, service date) instead of recomputing it; the new item has `source = 'TRANSFER'` and the two items point at each other through `reference_type = 'FOLIO_TRANSFER'`. Recomputing would let tax rounding drift; copying cannot.
7. **Split billing** is the combination of rule 2 (what is routed) and rule 6 (what is moved). A percentage split of one charge is a non-goal.
8. **Company transfer to the city ledger** stays as it is. One new check: on a COMPANY folio the transfer's `company_id` must equal `bill_to_company_id` (else `TRANSFER_COMPANY_MISMATCH`), so a company folio cannot be sent to another company's account.
9. **Check-out**: unchanged. Every open folio of the stay must be at zero. In practice the company folio is brought to zero by its city ledger transfer; an optional "transfer the company folio to the city ledger at check-out" setting on the instruction is a later convenience (section 12).
10. **Invoices**: one per folio (already so). For a COMPANY folio the party is the company (`bill_to_company_id`), for the guest folio the guest or booker as today. A tax invoice of source `FOLIO` takes the buyer from `bill_to_company_id` (name, NPWP, address) when the folio is a COMPANY folio.
11. **Reservation line changes**: removing an instruction applies to future nights; a COMPANY folio is not deleted (append-only ledger); an empty company folio simply closes at check-out with a zero balance.
12. **Reverse check-in** detaches the guest folio only if no CHARGE item exists on any folio of the stay (today: on the one folio); the same rule, over the plural.

### 3.8 API impact

- `StayFolio` and `Folio` gain `folio_type`, `bill_to_company_id`, `bill_to_company_name`. `GET /folios?stay_id` already returns a list.
- New: `PUT /reservations/{id}/rooms/{lineId}/billing-instructions` (replaces the set; permission `reservation.update`, and the company must belong to the property); `GET` of the same; `POST /stays/{id}/folios` to open a company folio of an in-house stay by hand (permission `folio.post_charge`); `POST /folio-items/{id}/transfer`.
- New error codes: `ROUTING_TARGET_CLOSED`, `TRANSFER_COMPANY_MISMATCH`, `FOLIO_TRANSFER_INVALID` (different reservation, closed folio, already reversed, a payment item).
- `GET /payments` and the cashier screens already take a folio; no change.
- OpenAPI first, as always; the stay detail already declares `folios[]`.

### 3.9 Accounting impact

- **Charges**: unchanged; the day close reads items by charge code and business date, whatever folio they are on.
- **Guest ledger control**: `ControlSources.folio_balance` sums every folio item of the property, so it keeps agreeing with the control account whichever folios hold the money.
- **Transfer of a charge**: the reversal copies the account and department of the original and so does the new charge; on the day close both land in the same accounts with opposite signs, **net zero per account and per department**. The department rule (`deptrule.go`) is not asked again for a reversal and the copied department on the new item satisfies it.
- **Deposits (`is_deposit`)**: the rule "paid before check-in, or the folio has no stay" works for COMPANY folios, which always have a stay. It would **misclassify a master folio** (no stay: every payment would be an advance deposit until the folio closes). That is the one prerequisite of a future MASTER folio: the view must become role-based first (a payment is a deposit when it precedes the first charge of its folio, or its folio is the unlinked deposit folio). It is listed in section 12, not done now.
- **City ledger**: unchanged (transfer from any folio, invoices claim transfers).

### 3.10 Night audit impact

- Room charges: the target folio of each due night comes from the resolver; the posting run locks the folios it will post to, ascending (unchanged); the posting register key is unchanged, so a night is posted once whatever the routing.
- New blocker kind `ROUTING_TARGET_CLOSED` in the existing `ChargeErrors`; preview and "post missing charges" show it.
- No other step changes: no-shows, the day journal, the close.

### 3.11 Migration impact

- One migration, three parts (Up and Down): the `folios` change above; `folio_billing_instructions`; widen `folio_items_source_ck` with `'TRANSFER'` (only needed by the transfer step, so it ships with that step).
- **Backward compatible**: every existing folio is `GUEST` with `bill_to_company_id NULL`; the new unique index is satisfied by the existing rows (one GUEST folio per stay already).
- The Down: refuse with a clear error when a COMPANY folio exists, because the old schema cannot represent it (the ledger is append-only, so the rows cannot simply be deleted).
- Code that changes: `GetFolioOfStay` stays (it means "the guest folio"); the expected charge loader changes from a stay→folio map to the resolver; `AttachStayFolio`; `CloseStayFolios` and `ListStayOpenFolios` need nothing.

### 3.12 Testing requirements

- Schema: a stay with a GUEST and a COMPANY folio; a second COMPANY folio of the same company refused; the company check; the Down refuses with data.
- Resolver: precedence (code over ROOM over ALL over default); a night posted to the company folio; a rerouted rule applies only to unposted nights; a closed target blocks the night audit and nothing is posted (rollback of the whole run, as today).
- The idempotency key still holds: two concurrent posting runs never post a night twice, with and without routing (extend `TestConcurrentPostingChargesEachNightOnce`).
- Transfer of a charge: net zero on the day journal per account and department; copied tax and components equal to the original to the cent for INCLUSIVE and EXCLUSIVE items; refused across reservations; concurrent transfers of one item (the unique `folio_items_reverses_uk` decides); the department rule.
- Check-out with a company folio: refused until zero; the city ledger transfer must match the company; the invoice of each folio; the tax invoice buyer.
- API contract (`openapi_test.go`), tenant isolation of the new routes, permissions.
- An end-to-end case of the audit: company pays the room, guest pays the minibar, two invoices, one city ledger invoice.

### 3.13 Future compatibility

- **Master (group) folio**: `folio_type = 'MASTER'`, `reservation_id` nullable with `booking_group_id` and a check that exactly one is set, instructions with `target_folio_id`. Prerequisites: the role-based `is_deposit`, and a lock level for the group. Nothing in this design has to be undone for it.
- **POS and other integrations** post through the resolver (`source = 'INTEGRATION'`).
- **Routing by date range or by night** adds columns to the instruction, not a new concept.
- **Folio as a payer of another folio** (a hierarchy) is not prepared and not needed.

## 4. Decision 2: Rate restrictions

### 4.1 Problem

Nothing stops a night from being sold. A hotel needs to close a night for sale (stop sell), refuse an arrival or a departure on a day, and require or cap the length of stay, per room type and rate plan, and it needs the rule to hold the same way for the front desk, the web, the search screen, an extension of a stay and, later, an OTA.

### 4.2 Current behavior

See 2.2: no restriction anywhere; availability is only counts (`sellable - demand`, plus the bed line); every sale path ends in `checkHolds` or a sibling that builds its own demand; search and calendar read the same engine.

### 4.3 Options considered

| Option | What it is | Verdict |
|---|---|---|
| A | Range rules with priority, like yield rules (`stay_from`, `stay_to`, weekdays, priority) | Flexible, but the answer for one night depends on sorting rules; a channel manager needs the answer **per date** anyway, so every consumer would expand rules to dates. |
| **B** | **A daily grid** per (room type, rate plan, date) with the five attributes, filled in bulk by date range and weekdays like the rate grid | **Recommended.** The same shape as the rate grid the hotel already uses, the shape every OTA uses, and the answer for a night is a lookup. |
| C | Flags on `rate_plans` and `room_types` (a plan "closed", a minimum stay on the plan) | Cannot say "closed on 24 December". |
| D | Put the checks inside each caller | Duplicated business logic, which is exactly what must not happen. |

### 4.4 Recommended option

**Option B, one table, one resolver, one evaluator, in the availability package** (which already reads rooms, blocks, lines and stays by its own queries, so reading a restriction table fits its role). The storage, the CRUD and the API belong to the `rates` module beside the rate grid and the yield rules.

### 4.5 Why

- The unit of a restriction is a **date**: stop sell is about a night, closed to arrival about an arrival date, closed to departure about a departure date, min and max stay are keyed by the arrival date (the usual "MinLOS on arrival"). A grid per date answers all five with one lookup.
- It is the exact shape a channel manager exports, so the future OTA step needs no new model.
- One evaluator means one place for the rule and for its tests.

### 4.6 Data model (design only)

```
rate_restrictions
  id, tenant_id, property_id
  room_type_id         bigint NULL, FK (property_id, room_type_id)   -- NULL = every room type
  rate_plan_id         bigint NULL, FK (property_id, rate_plan_id)   -- NULL = every rate plan
  stay_date            date NOT NULL
  stop_sell            boolean  NULL    -- NULL = no opinion at this level (inherit), TRUE = closed, FALSE = explicitly open
  closed_to_arrival    boolean  NULL
  closed_to_departure  boolean  NULL
  min_stay             smallint NULL    CHECK (min_stay >= 1)
  max_stay             smallint NULL    CHECK (max_stay >= 1)   CHECK (min_stay IS NULL OR max_stay IS NULL OR min_stay <= max_stay)
  note                 varchar(200) NULL
  created_at, created_by, updated_at, updated_by
  CHECK (at least one of the five attributes is not NULL)
  UNIQUE (property_id, COALESCE(room_type_id, 0), COALESCE(rate_plan_id, 0), stay_date)
  INDEX (property_id, stay_date)
```

- The table is a **mutable grid**, like `rates`; its history is the audit trail (`rate_restrictions.filled`), not append-only rows. A restriction is configuration, not a financial record.
- **Level of application (scope)**: property-wide (both NULL), a room type, a plan, or a room type and a plan.
- **Precedence**: for each attribute separately, the value from the **most specific row that has a value for it** wins. Specificity, most to least: (type, plan) > (type, all plans) > (all types, plan) > (all, all). This lets "stop sell DLX for every plan" and "except the CORP plan" coexist with an explicit `FALSE` on the (DLX, CORP) row, which one closed flag alone could not express. The two middle levels cannot collide on one row; if both have a value for one attribute, **the room type level wins over the plan level** (this tie-break is arbitrary and is the owner's call; it is documented so it is never a surprise). Two rows of the same scope and date cannot exist (unique key), so no further tie remains.
- Contradictory numbers (an effective minimum above the effective maximum) are not blocked at write time across levels; the stay then fails both bounds and is simply unsellable, which is the honest outcome.
- **Effective date**: a row is for a stay date; it needs no separate effective-from. A change applies at once to every **new** sale and **never** changes a reservation that exists (see 4.7, modification).
- Restrictions apply to **all rate plans unless a plan is named**; a complimentary or house use plan is restricted like any other (a closed night is closed for the owner too; staff override as in 4.8).

### 4.7 Business rules

**The evaluator.** `availability.EvaluateStay(ctx, tenantID, propertyID, StayRequest) (Verdict, error)`, with a pure core `Resolve(rows, roomTypeID, ratePlanID, date) Effective` and `Check(request, effectiveByDate) []Violation`. `StayRequest` carries room type, rate plan, arrival, departure, the business date, what is **new** in the request (as built: `Previous`, the stay as it was, nil for a whole new sale, and `InHouse` for an extension of a guest who has arrived; see below) and, in the step that wires it, the source of the booking. A `Violation` is `{type, date, room_type_id, rate_plan_id, scope, value, row_id}`; a `Verdict` is the violations and `overridable`.

| Type | Evaluated on | Violation when |
|---|---|---|
| `STOP_SELL` | every night `n` of the stay that is new, `n >= business date` | effective `stop_sell` of `n` is TRUE |
| `CLOSED_TO_ARRIVAL` | the arrival date, if the arrival is new | effective `closed_to_arrival` of the arrival date is TRUE |
| `CLOSED_TO_DEPARTURE` | the departure date, if the departure is new | effective `closed_to_departure` of the departure date is TRUE |
| `MIN_STAY` | the arrival date, if the length is new | nights < effective `min_stay` of the arrival date |
| `MAX_STAY` | the arrival date, if the length is new | nights > effective `max_stay` of the arrival date |

**Where it is called, and what is "new"** (one evaluator, every caller passes what changed):

| Path | Evaluated | New parts |
|---|---|---|
| Create (draft or confirmed), confirm of a draft, reinstate, add a line, walk-in (through `CreateHeld`) | yes: it is a sale | everything |
| Amend dates | yes | only what changed: the arrival if it moved, the departure if it moved, the nights that were not in the old stay, the length if it changed |
| Amend room type or rate plan | yes: another product is sold | everything |
| Amend adults, children, bed, lock | no | none |
| Assign a room, unassign, upgrade at check-in, room move | no: operational, the product sold does not change | none |
| Extend an in-house stay (`ChangeDeparture`, longer) | yes | the extra nights (`STOP_SELL`), the new departure (`CLOSED_TO_DEPARTURE`), the total length against `MAX_STAY` (`MIN_STAY` and `CLOSED_TO_ARRIVAL` are not asked: the guest is already in) |
| Shorten a stay before arrival | yes, as an amendment of dates | the new departure and the new length |
| Early check-out of an in-house stay, cancel, no-show | no | none |
| Search, calendar, quote | yes, as a **read**: each offer gets its verdict | everything |

**Order inside a sale**: restrictions first (cheap, a few rows), then the inventory (`RequireAvailableFor`). The reason a person sees is the restriction when both fail, because it is the rule the hotel set on purpose.

**Hard block, with a controlled override.** A violation refuses the operation with `409 STAY_RESTRICTED`. It is **not** a warning (a warning that nobody has to answer is ignored). Staff with `reservation.override_restriction` can override with a reason, and the override needs the approval of a person who holds `reservation.restriction_approve` (their own, or their credentials typed in), the same pattern as the rate override and the free room approval (`VerifyApprovalFor`). The override is per request and audited. Bookings with source `WEBSITE` or `OTA` can never override (nobody is there to approve); this is what makes the rule safe when an online channel exists.

**No overbooking** is unchanged and not a restriction: the inventory still refuses `available < 0` and cannot be overridden.

**Locks and transactions**: no new lock level. Restrictions are read inside the booking transaction under the locks it already holds, like the rate grid; an edit of the grid takes the same locks as `FillRates` (room types shared, then the plan). A sale and a stop sell that race in the same millisecond may resolve either way; both are correct outcomes (the sale happened before the closing, or was refused after it), and the existing reservation is never touched.

### 4.8 API impact

- `GET {P}/rate-restrictions?from&to&room_type_id&rate_plan_id`: the raw rows of the window (`rate.manage` or `reservation.read` to read).
- `PUT {P}/rate-restrictions`: fill, like `PUT /rates`: `{ room_type_ids?, rate_plan_ids?, from, to, weekdays?, set: { stop_sell?, closed_to_arrival?, closed_to_departure?, min_stay?, max_stay? }, clear: [names] }`, one transaction, `rate.manage`, audit `rate_restrictions.filled` with the scope, the dates and the attributes. `clear` sets an attribute back to NULL (inherit); a row left with no attribute is deleted. At most 366 dates per call.
- `GET {P}/rate-restrictions/effective?room_type_id&rate_plan_id&from&to`: the **effective** restrictions per date, computed by the evaluator's resolver (what the UI grid shows and what a channel manager will export). The screens never resolve precedence themselves.
- Reservation create, add line, amend, confirm, reinstate, walk-in and extend: an optional `restriction_override { reason, approval }`.
- Refusal: `409 STAY_RESTRICTED`, `context.violations[]` as above and `context.overridable`. When the override was given but the approval is wrong: the usual approval errors.
- `GET /availability` (search): each `PlanOffer` (and each bed variant plan) gets `bookable` and `restrictions[]` (the violations), so a screen can grey an offer and say why before anyone tries to book. The calendar can add an optional marker per night.
- New permissions: `reservation.override_restriction`, `reservation.restriction_approve`. Editing the grid reuses `rate.manage`.

### 4.9 UI impact

- Setup → **Restrictions** (next to the rate grid): a grid of dates by room type for a chosen rate plan (or "all plans"), each cell showing small marks for stop sell, CTA, CTD, minimum and maximum stay; a fill dialog with the date range, weekdays and the attributes; an "effective" toggle that shows the resolved values and which level they come from.
- New reservation: an offer that is not bookable is greyed with the reason chips (the same reason texts the server sends); a staff member who may override sees an "override" action that opens the reason and approval dialog already used for rate overrides.
- Reservation detail and the extension dialog: the error is shown with its nights and the same override path.
- Availability calendar: an optional row or mark for a closed night, from the same effective view. Texts in English and Indonesian.

### 4.10 Accounting impact

None. A restriction never posts anything.

### 4.11 Migration impact

One new table, no change to an existing one, no data to migrate (no restriction exists today, so the first state is "nothing restricted", which equals today). Down: drop the table.

### 4.12 Testing requirements

- Resolver: precedence across the four levels, an explicit `FALSE` opening a plan inside a closed type, the type-over-plan tie-break, NULL inheritance, a row with all attributes cleared.
- Evaluator: each of the five types at its anchor date (arrival date for CTA, MinLOS and MaxLOS; departure date for CTD; every night for stop sell), the boundaries (a stay of exactly the minimum, of the maximum), past nights ignored.
- Each caller in the table of 4.7: a sale refused, an amendment that changes only the guests not re-evaluated, an amendment of dates evaluating only what is new, an extension, a walk-in, the draft that is closed before it is confirmed.
- **A guard test that every sale path calls the evaluator**: a table of the use cases and the verdict each must return, so a new path cannot be added without it (this is the answer to "no duplicate business logic").
- Override: needs the permission, needs the approval, is audited, is refused for source `OTA` and `WEBSITE`.
- Search verdicts equal the verdict of the real booking attempt for the same request (one table of cases run both ways).
- Concurrency: a fill racing a booking.
- API contract, tenant isolation, permissions, the Indonesian and English texts of every new code (`errorCodes.test.ts`).

### 4.13 Future compatibility

- **Channel manager / OTA**: the effective view is the export; inbound bookings are evaluated with their source and cannot override. A `channel` column (nullable, lowest specificity) can be added to the grid when a channel needs its own closing, without touching the evaluator's interface.
- **Release period (cut-off) and automatic restrictions from occupancy**: lead time and occupancy are conditions of the yield rules; a yield action "close" can write rows into this table later, keeping a single place where closing is stored.
- **Allotments and group blocks** are a different concept (they hold inventory); they are not restrictions and are not prepared here.

## 5. Decision 3: Currency policy

### 5.1 Problem

The MVP keeps one currency per property, and the owner wants to keep it. The policy has to be explicit about when the currency may change, what each module assumes, and how foreign money would be added without rewriting the ledger. Today the lock is narrower than the promise (2.3).

### 5.2 Current behavior

See 2.3: a currency and a precision per property; a lock that looks at folio items only; no currency column anywhere else; reports in the property currency.

### 5.3 Options considered

| Option | What it is | Verdict |
|---|---|---|
| A | Keep it as it is and document it | Leaves the gap: journals, bills or bank lines in one currency, then a change of currency. |
| **B** | **Keep one currency per property, lock it at the first financial record of any kind, with one definition of "financial data" shared by the trigger and the service** | **Recommended.** Small, and closes the gap. |
| C | A currency column on payments, folio items and journals now | Contradicts the rejected decision, touches every ledger table, and no pilot needs it. |
| D | Let a property change its currency with a conversion of history | Rewrites append-only ledgers. Never. |

### 5.4 Recommended option

**B, plus a written design for the later multi-currency step (section 5.9) so that nothing built until then has to change.**

### 5.5 Why

- The pilot is one hotel in one country; the cost of a wrong currency after real money exists is a rebuilt ledger, the cost of the stricter lock is one function.
- The ledger tables are append-only and money columns are unitless numbers; the only safe way to a second currency is to keep them in the **functional currency** (the property's) and add the foreign amount beside the payment, not inside the ledger.

### 5.6 Data model (design only)

```
-- one definition, used by the trigger and by the service:
property_has_financial_data(property_id) RETURNS boolean
  TRUE when the property has any row in: folio_items, payments, gl_journals, supplier_bills,
  supplier_payments, supplier_credit_notes, city_ledger_receipts, city_ledger_invoices,
  city_ledger_adjustments, cashier_shifts, bank_statements, tax_returns, tax_payments, tax_opening_credits, budgets
properties_currency_lock trigger: raises when currency_code or currency_decimals changes and the function is TRUE
tenancy.UpdateProperty: asks the same function (the CURRENCY_LOCKED code stays)
```

As built (migration 00058) the list also has `tax_opening_credits` (an amount entered by hand, like a journal), and the trigger takes the property row `FOR UPDATE` before it looks, so that a currency update and the first financial write of a property cannot race (every financial table has a foreign key to the property, which holds a key-share lock until the write commits). A schema test classifies every table that has `property_id` and a `numeric(18,x)` column as a root (looked at), a child of a root, configuration or a plan (never looked at), so a new money table cannot be added without a decision. A bank account on its own is configuration; bank statements are bank data and lock. The nightly price of a booking (`reservation_room_rates`) is classified as a plan, not a transaction: it does not lock, which means a property with bookings but no ledger entry can still change its currency (the owner may choose to include it).

The list is of tables that hold **amounts of money recorded in the currency**. Configuration that holds amounts (rates, yield floors, credit limits, late fees, card fee rules) is **not** on the list: it does not make a currency unchangeable, and it is **not converted** when the currency changes (the screen says so).

### 5.7 Business rules

1. **When the currency (and its decimals) may change**: only while `property_has_financial_data` is false, that is before the first folio item, payment, journal, bill, receipt, shift, bank statement, tax filing or budget of the property. Both change together or neither does (as the trigger does today). Opening a business day is not a financial record.
2. **When it is immutable**: from the first such record, for ever. There is no unlock and no conversion. A hotel that chose wrongly after that starts a new property.
3. **Payment**: the amount is in the property currency; there is no currency column. A guest who pays in foreign cash is recorded as the counter value in the property currency (`CASH`), at the rate the front desk applied; the difference with the drawer is the shift's over and short. The rate and the foreign amount can go in `reference_number` or `remarks`; they carry no accounting meaning until section 5.9.
4. **Folio**: items are in the property currency; a folio has no currency of its own. Rounding follows `currency_decimals` at every boundary (`platform/money`), which is why the decimals are locked with the code.
5. **Journal**: a journal line is in the property currency; the application refuses more decimals than the property's (`journals.go`). The chart and the system account map have no currency.
6. **City ledger**: a company account, its credit limit, invoices, receipts, credit notes, write-offs and late fees are in the currency of the property that holds them. A company that trades with two properties of different currencies has two accounts, one per property; nothing is converted between them.
7. **Bank reconciliation**: a bank account of the property is a **property-currency** account; a statement with other currencies is refused or not imported. Registering a foreign-currency bank account is a non-goal of the MVP (it would be reconciled against a ledger it does not belong to). *(Today `bank_accounts` has no currency column and the import does not check; the rule is documented here and enforced in the step that adds the guard, section 14.)*
8. **Cashier shift**: opening float, movements, counts by denomination and the closing journal are in the property currency; a shift is one currency.
9. **Reporting**: every report and PDF prints the currency of the property and amounts at its decimals. There is no report that adds amounts of different properties; if a tenant-level report is ever built it must group by currency and never sum across them (a rule for that feature, noted now).
10. **Tenant**: a tenant may own properties in different currencies; the currency is a property fact, not a tenant fact.

### 5.8 API, UI, accounting, migration impact

- API: no new endpoint. `CURRENCY_LOCKED` stays; its message names the first financial record found when that is cheap (an optional `context.reason`).
- UI: the property form shows the currency fields as read-only with the reason once locked, and says before the lock that rates, limits and fees are **not** converted.
- Accounting: none (the ledgers do not change).
- Migration: one function and a replacement of the trigger body (Up and Down); no data change. Existing properties that already have folio items stay locked; properties with only journals or bills become locked, which is the intended fix.

### 5.9 Testing requirements and the future step

Tests: the function for each table (a row in any one makes it true, an empty property is false); the trigger and the service agree for every table; a property with only a manual journal cannot change its decimals; one with only configuration can; the schema test of the trigger; `CURRENCY_LOCKED` over the API.

**How multi-currency would be added later without breaking this design** (not built, not scheduled):

- Keep **folio items, folios, journals and every report in the functional currency** (the property's). Nothing there changes.
- Add the foreign money **beside the payment**: nullable `tender_currency`, `tender_amount`, `fx_rate`, `fx_rate_source` on `payments` (amount stays functional, so every existing row is valid by default), and an effective-dated `exchange_rates` table per property.
- Cash drawer: counts and movements gain a currency key (`cashier_shift_counts` is already per denomination); the over and short is computed in the functional currency.
- Bank accounts gain `currency_code` (default: the property's); statement lines in the account's currency; a conversion line at reconciliation.
- General ledger: a system account for exchange differences in the account map; no currency on journal lines.
- Rates: one price list in the property currency, with display conversions only.
- The rejected decision `payments.currency_code` stays rejected in the sense that matters: the **amount** of a payment is never in a foreign currency; only the tender is recorded beside it.

## 6. Cross-module impact

| Module | Folio model | Restrictions | Currency |
|---|---|---|---|
| `folios` | Folio type and payer, resolver, transfer of a charge, check of the transfer company, reverse check-in over the plural | none | none |
| `expected`, `roomcharge` | Target from the resolver instead of the stay→folio map; new blocker kind | none | none |
| `frontdesk` | Eager company folio at check-in; stay folio list | Evaluator in the extension; walk-in through `CreateHeld` | none |
| `reservations` | Billing instructions on the line | Evaluator in the sale paths; override request; search verdicts | none |
| `rates` | none | Owns the table, the fill and the effective endpoints | none |
| `availability` | none | Owns the evaluator (`EvaluateStay`, `Resolve`) and reads the table | none |
| `tenancy` | none | none | The one function and the trigger |
| `cityledger`, `companies` | Company of a folio, matching check on transfer | none | Rule 6 documented |
| `accounting` | The `is_deposit` rule only for a future master folio | none | none |
| `taxinvoice`, `documents` | Buyer and party from the company folio | none | none |
| `nightaudit` | New blocker kind | none | none |
| `bankrec` | none | none | Guard for the account currency |
| `iam` | none | Two permissions | none |
| `web` | Folio switcher, bill-to selector, transfer dialog | Restrictions grid, offers with reasons, override dialog | Read-only fields with reason |

## 7. Migration considerations

- Three independent migrations, in the order of section 14. None edits an applied migration; each has a Down.
- Folio: backward compatible by construction (every existing row satisfies the new constraints); the Down refuses when a COMPANY folio exists (see 3.11).
- Restrictions: a new empty table.
- Currency: a function and a trigger body; existing data cannot violate it.
- Data migration tests are needed for the first time with data (see the audit note that migration 00057 was only run on an empty database): run each migration up and down over a database seeded with a month of folios, payments and journals.

## 8. API impact (summary)

New: billing instructions (GET, PUT), `POST /stays/{id}/folios`, `POST /folio-items/{id}/transfer`, `GET` and `PUT /rate-restrictions`, `GET /rate-restrictions/effective`. Changed: folio objects (type, bill-to), reservation sale operations (`restriction_override`), availability search (`bookable`, `restrictions`). New codes: `ROUTING_TARGET_CLOSED`, `TRANSFER_COMPANY_MISMATCH`, `FOLIO_TRANSFER_INVALID`, `STAY_RESTRICTED`. New permissions: `reservation.override_restriction`, `reservation.restriction_approve`. Every change goes into `api/openapi.yaml` first; the generated types and the contract test follow.

## 9. Frontend impact (summary)

Stay and folio screens: a folio switcher and the bill-to company; reservation line: "bill room to company" with the company picker (a second line for the charge codes the company pays); an item action "move to folio". Setup: the restrictions grid and its fill dialog. New reservation and search: reasons for a closed offer, the override dialog. Property settings: read-only currency with the reason. All texts in English and Indonesian; every new view gets a component test (the audit found 24 views without one).

## 10. Accounting / GL impact (summary)

None of the three decisions adds an account, a journal type or a report. The folio decision keeps the guest ledger and the city ledger as the only two places a guest's money can sit, makes a transfer between folios net to zero per account and department, and names one prerequisite for a future master folio (the role-based `is_deposit`). Restrictions post nothing. The currency decision only tightens a lock.

## 11. Night audit impact (summary)

One new blocker kind (`ROUTING_TARGET_CLOSED`) in the existing charge errors; room nights are posted through the resolver with the same register key. Restrictions and currency do not touch the night audit.

## 12. Future extension

- Master (group) folio and the role-based `is_deposit`; transfer of the company folio to the city ledger automatically at check-out; routing by date range; company deposits before check-in.
- Restrictions per channel, release periods, automatic closing from yield rules, an export for a channel manager.
- Multi-currency as in 5.9.

## 13. Explicit decisions and non-goals

**Decisions**
1. A stay may have several folios; the types are `GUEST` and `COMPANY` now, `MASTER` reserved.
2. One company folio per company per stay; the guest folio always exists; the deposit folio stays at reservation level.
3. One resolver decides the target folio of a charge; a closed target blocks, it does not fall back.
4. A transfer between folios is a reversal plus a copied re-posting, only inside one reservation, with the correction approval.
5. A payment belongs to one folio and is never allocated across folios.
6. A group's single bill is a city ledger invoice over company folios; no master folio now.
7. Restrictions are one daily grid, one resolver, one evaluator in `availability`; precedence is the most specific row per attribute; they are hard blocks with an audited, approved override for staff and no override for online sources.
8. An existing reservation is never changed by a restriction; a modification evaluates only what is new.
9. The currency of a property is changeable only before its first financial record of any kind, defined once in the database; config amounts are not converted.
10. Multi-currency, when built, keeps ledgers functional and records the tender on the payment.

**Non-goals** (of this design, not forbidden forever): a master folio; a percentage split of one charge; moving a payment between folios; company deposits before check-in; per-night routing; restrictions per channel, per source or per guest; release periods; through-stay minimums; allotments; any foreign currency amount in a ledger table; a foreign-currency bank account; conversion of history; a tenant-level report across currencies; a pure warning level for restrictions.

**Points the owner must confirm**: (a) a closed routing target blocks the night audit rather than falling back to the guest folio; (b) when a room type row and a plan row both give a value, the room type wins; (c) the override of a restriction needs the approval of a second permission; (d) the bank account currency guard in the currency step.

## 14. Implementation order

Each step is its own commit, with database, backend, API, frontend and tests together, as `CLAUDE.md` requires. Order by dependency and by risk, not by ease.

| Step | What | Why here |
|---|---|---|
| 1 | **Currency lock** (**built**, migration 00058): `property_has_financial_data`, the trigger, the service, the tests; the read-only fields on the property form are not done | Smallest; it protects the data of everything that follows, and a pilot must not start with the gap open. No dependency. |
| 2 | **Restrictions, part 1** (**built**: migration 00059, `availability/restrictions.go` the resolver and the evaluator, `rates/restrictions.go` the fill and the reads, `GET`/`PUT`/`effective`, the audit entry, Setup → Restrictions; nothing sells by them yet): table, resolver, evaluator, `GET`/`PUT`/`effective`, audit, the grid screen | Self-contained: no other module's schema changes. The evaluator is built and tested alone first. |
| 3 | **Restrictions, part 2** (**built**: `reservations/restriction.go`; create and walk-in, add a room, amend dates or room type or plan, confirm, reinstate and the extension of a stay ask the evaluator; `restriction_override` with `reservation.override_restriction` and `reservation.restriction_approve`; `bookable` and `restrictions` on every offer of the search; the guard test `TestEverySalePathAsksTheSalesRestrictions`; New reservation shows the reasons and the override. Not done: the override on the screens of an existing reservation and of the extension, and a marker on the availability calendar): wire the evaluator into every sale path, the override, the search verdicts, the guard test | Needs step 2. Done as its own step because it touches `reservations` and `frontdesk`, where the review effort is. |
| 4 | **Folio, step A**: schema (types, payer, unique index), the resolver with the **default routing only**, `AttachStayFolio` and the expected loader moved onto it, the stay and folio API fields | No behavior change by design, which makes it the safe place to move the assumption out of three lookups. Run the migration over seeded data. **Done** (migration 00060, `internal/folios/routing.go`). |
| 5 | **Folio, step B**: billing instructions (room, charge code, all), the eager company folio, the blocker, the company check on the city ledger transfer, the invoice party and tax invoice buyer, the screens | The payer split the pilot's corporate guests need. Needs step 4. **Done** (migration 00061; the manual `POST /stays/{id}/folios` is not built: setting an instruction opens the folio). |
| 6 | **Folio, step C**: transfer of a charge between folios of a reservation | Independent of step 5 in code but used with it in practice; carries the copy-the-snapshot rule and the net-zero tests. **Done** (migration 00062, `internal/folios/transfer.go`). Two things the design did not say: a room night keeps its place in the posting register (the reversal flips the original row and the copy gets a new POSTED row for the stay that earned the night, so no run charges it again), and the room-night count of the day summary now reads the register instead of the items. |
| 7 | **Bank account currency guard** (rule 5.7.7) | Small; after the lock because it reuses its vocabulary. May move earlier if bank import is part of the first week. |
| later | Master folio (with the role-based `is_deposit`), automatic company transfer at check-out, channel restrictions, multi-currency | See section 12. |

Steps 2 to 3 and 4 to 6 are two independent lines; they can be built in either order. Step 1 first, because it is the cheapest and closes a gap that exists today.
