# REST API Design

## 1. Conventions

| Topic | Rule |
|---|---|
| Base | `/api/v1`. Property-scoped routes live under `/api/v1/properties/{propertyId}`, written below as **`{P}`**. |
| Auth | `Authorization: Bearer <JWT>`. Access tokens last 15 minutes and carry `sub` (user id) and `tid` (tenant id). Refresh tokens are opaque and rotated on every refresh. |
| Scope check | For every `{P}` route: the property belongs to `tid`, **and** (the user is a tenant admin **or** has a `user_properties` row). Then the endpoint's permission is checked against that property's role. A failure returns **404** for a foreign property (to avoid leaking that it exists) and **403** for a missing permission. |
| Commands | State transitions use `POST {resource}/{id}/{action}` (for example `/confirm`, `/check-in`). `PATCH` is only used for plain attribute edits. |
| Concurrency | Requests that modify an aggregate with a `version` carry `"version": n`. A mismatch returns **409 `VERSION_CONFLICT`**. |
| Idempotency | An `Idempotency-Key` header is **required** on charge, adjustment, payment, refund, deposit, check-in, walk-in and check-out. A replay returns the original response. |
| Money | JSON **strings** (`"1221000.00"`), never floats. The currency is implied by the property and returned in `GET {P}`. |
| Dates | Business dates are `YYYY-MM-DD`. Instants are RFC 3339 in UTC. |
| Business date | It's never taken from the client for a posting. The server always uses the OPEN business day. |
| Lists | `?limit=50&cursor=…`. Responses look like `{ "data": [...], "next_cursor": "…" }`. |
| Errors | RFC 9457 `application/problem+json`: `{ "type", "title", "status", "code", "detail", "errors": [{field, code}], "context": {...} }`. The `code` is stable, for example `ROOM_NOT_AVAILABLE`, `ROOM_NOT_READY`, `NIGHT_AUDIT_BLOCKED` or `FOLIO_NOT_BALANCED`. |
| Status codes | 200/201 success · 400 malformed · 401 · 403 · 404 · 409 business-rule or state conflict · 422 validation · 429 |

**Common validation**, applied everywhere and not repeated below: IDs must belong to the same property or tenant (otherwise 404). Text lengths follow the column sizes. Enum values must match. Dates must be valid.

## 2. Permission catalogue (Go constants)

`property.manage` · `user.manage` · `role.manage` · `room.manage` · `room_block.manage` · `housekeeping.update` · `housekeeping.inspect` · `guest.read` · `guest.write` · `guest.search_all` · `guest.history_all_properties` · `billing_config.manage` · `rate.manage` · `reservation.read` · `reservation.create` · `reservation.update` · `reservation.cancel` · `reservation.reinstate` · `reservation.override_rate` · `reservation.upgrade` · `frontdesk.checkin` · `frontdesk.checkin_unready_room` · `frontdesk.checkout` · `frontdesk.room_move` · `frontdesk.rate_change` · `frontdesk.reverse_checkin` · `folio.read` · `folio.post_charge` · `folio.adjust` · `folio.reverse` · `folio.post_after_checkout` · `folio.reopen` · `payment.post` · `payment.void` · `payment.refund` · `nightaudit.run` · `nightaudit.no_show` · `audit.read`

---

## 3. Endpoint index

| # | Method | Path | Purpose | Permission |
|---|---|---|---|---|
| **Auth** |||||
| A1 | POST | `/auth/login` | Log in | public |
| A2 | POST | `/auth/refresh` | Rotate tokens | refresh token |
| A3 | POST | `/auth/logout` | Revoke the session | authenticated |
| A4 | GET | `/auth/me` | Current user, properties, permissions | authenticated |
| **Properties and business date** |||||
| B1 | GET | `/properties` | Properties the user can access | authenticated |
| B2 | POST | `/properties` | Create a property (+ opening business day, sequences, seed codes) | tenant admin |
| B3 | GET | `{P}` | Property details and settings | any grant |
| B4 | PATCH | `{P}` | Update settings | property.manage |
| B5 | GET | `{P}/business-date` | BD vs server and local time | any grant |
| B6 | GET | `{P}/business-days` | Closed-day history and summaries | audit.read |
| **Users and roles** |||||
| U1 | GET/POST | `/users` | List or create users | user.manage (tenant admin) |
| U2 | PATCH | `/users/{id}` | Update or deactivate | user.manage |
| U3 | PUT | `/users/{id}/properties` | Replace property grants | user.manage |
| U4 | GET/POST | `/roles`, PATCH `/roles/{id}` | Manage roles | role.manage |
| U5 | GET | `/permissions` | Catalogue | authenticated |
| **Room types and rooms** |||||
| R1 | GET/POST | `{P}/room-types` | List or create | read: any · write: room.manage |
| R2 | GET/PATCH | `{P}/room-types/{id}` | Get or update | same |
| R3 | GET/POST | `{P}/rooms` | List or create (creates the housekeeping row) | same |
| R4 | GET/PATCH | `{P}/rooms/{id}` | Get or update | same |
| R5 | GET | `{P}/room-status?date=` | **Derived** board: occupancy, housekeeping, block | any |
| **Room blocks** |||||
| K1 | GET | `{P}/room-blocks?from&to&room_id` | List | any |
| K2 | POST | `{P}/room-blocks` | Create OOO/OOS | room_block.manage |
| K3 | PATCH | `{P}/room-blocks/{id}` | Change dates or reason | room_block.manage |
| K4 | POST | `{P}/room-blocks/{id}/cancel` | Cancel | room_block.manage |
| **Housekeeping** |||||
| H1 | GET | `{P}/housekeeping?status&floor` | Housekeeping board | any |
| H2 | POST | `{P}/rooms/{id}/housekeeping` | Change status | housekeeping.update / .inspect |
| H3 | GET | `{P}/rooms/{id}/housekeeping/logs` | History | any |
| **Guests** (tenant-wide) |||||
| G1 | GET | `/guests?q=&property_id=` | Search | guest.read (+ search_all) |
| G2 | POST | `/guests` | Create (`origin_property_id` required) | guest.write at origin |
| G3 | GET/PATCH | `/guests/{id}` | Get or update | guest.read / guest.write |
| G4 | GET | `/guests/{id}/history` | Reservations and stays (filtered) | guest.read (+ history_all_properties) |
| **Billing config** |||||
| C1 | GET/POST | `{P}/taxes`, PATCH `{P}/taxes/{id}` | Taxes | read: any · write: billing_config.manage |
| C2 | GET/POST | `{P}/service-charges`, PATCH `/{id}` | Service charges | same |
| C3 | GET/POST | `{P}/charge-codes`, PATCH `/{id}` | Charge codes | same |
| C4 | PUT | `{P}/charge-codes/{id}/rules` | Replace tax and service mappings | billing_config.manage |
| C5 | POST | `{P}/charge-calculations` | **Preview** the engine result (no posting) | any |
| **Rates** |||||
| P1 | GET/POST | `{P}/rate-plans`, PATCH `/{id}` | Rate plans | read: any · write: rate.manage |
| P2 | GET | `{P}/rates?rate_plan_id&room_type_id&from&to` | Rate grid | any |
| P3 | PUT | `{P}/rates` | Bulk upsert (date ranges + weekdays) | rate.manage |
| **Availability** |||||
| V1 | GET | `{P}/availability?arrival&departure&adults&children` | Per room type, per night | reservation.read |
| V2 | GET | `{P}/availability/rooms?room_type_id&arrival&departure` | Free specific rooms | reservation.read |
| **Reservations** |||||
| S1 | POST | `{P}/reservations` | Create a draft with lines | reservation.create |
| S2 | GET | `{P}/reservations?arrival_from&arrival_to&status&q` | Search | reservation.read |
| S3 | GET | `{P}/reservations/{id}` | Detail with lines, rates, estimate, folios | reservation.read |
| S4 | PATCH | `{P}/reservations/{id}` | Header fields | reservation.update |
| S5 | POST | `{P}/reservations/{id}/confirm` | Confirm | reservation.create |
| S6 | POST | `{P}/reservations/{id}/cancel` | Cancel the whole booking | reservation.cancel |
| S7 | POST | `{P}/reservations/{id}/reinstate` | Reinstate | reservation.reinstate |
| S8 | POST | `{P}/reservations/{id}/rooms` | Add a line | reservation.update |
| S9 | PATCH | `{P}/reservations/{id}/rooms/{lineId}` | Amend dates, type, occupancy, plan or rates | reservation.update |
| S10 | POST | `…/rooms/{lineId}/cancel` | Cancel a line | reservation.cancel |
| S11 | POST | `…/rooms/{lineId}/no-show` | Mark a line as no-show | nightaudit.no_show |
| S12 | POST | `…/rooms/{lineId}/assign-room` | Assign or reassign a room | reservation.update |
| S13 | POST | `…/rooms/{lineId}/unassign-room` | Unassign | reservation.update |
| S14 | POST | `{P}/reservations/{id}/deposits` | Take a deposit | payment.post |
| **Front desk** |||||
| F1 | POST | `…/rooms/{lineId}/check-in` | Check in | frontdesk.checkin |
| F2 | POST | `{P}/walk-ins` | Walk-in (create, confirm and check in) | frontdesk.checkin + reservation.create |
| F3 | GET | `{P}/stays?status&departure_date` | In-house and due-out lists | reservation.read |
| F4 | GET | `{P}/stays/{id}` | Stay detail | reservation.read |
| F5 | POST | `{P}/stays/{id}/move` | Room move | frontdesk.room_move |
| F6 | POST | `{P}/stays/{id}/change-departure` | Extend or shorten | reservation.update |
| F7 | POST | `{P}/stays/{id}/guests` | Add an accompanying guest | frontdesk.checkin |
| F8 | POST | `{P}/stays/{id}/check-out` | Check out | frontdesk.checkout |
| F9 | POST | `{P}/stays/{id}/reverse-check-in` | Undo a check-in | frontdesk.reverse_checkin |
| **Folios, charges and payments** |||||
| L1 | GET | `{P}/folios?reservation_id&stay_id&status` | List | folio.read |
| L2 | GET | `{P}/folios/{id}` | Items, components and computed balance | folio.read |
| L3 | POST | `{P}/folios/{id}/charges` | Post a charge | folio.post_charge |
| L4 | POST | `{P}/folios/{id}/adjustments` | Post an adjustment | folio.adjust |
| L5 | POST | `{P}/folio-items/{id}/reverse` | Reverse the same day | folio.reverse |
| L6 | POST | `{P}/folios/{id}/payments` | Post a payment | payment.post |
| L7 | POST | `{P}/payments/{id}/void` | Void | payment.void |
| L8 | POST | `{P}/payments/{id}/refunds` | Refund | payment.refund |
| L9 | POST | `{P}/folios/{id}/close` | Close (balance 0, not linked to an in-house stay) | folio.post_charge |
| L10 | POST | `{P}/folios/{id}/reopen` | Reopen | folio.reopen |
| L11 | GET | `{P}/payments?business_date&method` | Cashier list | folio.read |
| **Night audit** |||||
| N1 | GET | `{P}/night-audit/preview` | Blockers, warnings, guard | nightaudit.run |
| N2 | POST | `{P}/night-audit/no-shows` | Bulk "Mark remaining as no-show" | nightaudit.no_show |
| N3 | POST | `{P}/night-audit/run` | Close the day | nightaudit.run |
| **Audit** |||||
| X1 | GET | `{P}/audit-logs?entity_type&entity_id&from&to` | Audit trail | audit.read |

---

## 4. Endpoint specifications

### Auth

**A1 `POST /auth/login`**
- **Request:** `{ "tenant_code": "abc", "email": "admin@hotel.com", "password": "…" }`
- **Response 200:** `{ "access_token", "refresh_token", "expires_in": 900, "user": { "id", "full_name", "is_tenant_admin" } }`
- **Validation:** all fields are required.
- **Rules:** look up the user by `(tenant.code, lower(email))`. The tenant must be ACTIVE and the user active. The failure response is identical for every cause (401 `INVALID_CREDENTIALS`). Rate-limited per IP and per email. Creates a `user_sessions` row and sets `last_login_at`.

**A2 `POST /auth/refresh`**: `{ refresh_token }` → new pair. The old session is revoked (rotation). If a revoked token is reused, all of the user's sessions are revoked.

**A4 `GET /auth/me`** → `{ user, tenant, properties: [{ id, code, name, role, permissions: [...] }] }`. The Vue app builds its menus from this.

### Properties and business date

**B2 `POST /properties`**
- **Request:** `{ code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time, opening_business_date, require_room_inspection_for_checkin?, night_audit_marks_occupied_dirty? }`
- **Response 201:** the property.
- **Validation:** timezone is a valid IANA name. currency_code is ISO 4217. Decimals are 0–3. The code is unique within the tenant.
- **Rules:** in one transaction, insert the property, the OPEN `business_days(opening_business_date)`, the 4 `document_sequences`, and the seeded `charge_codes` (with no tax mappings; the admin configures taxes).

**B4 `PATCH {P}`**: any setting. `currency_code` and `currency_decimals` are rejected (409 `CURRENCY_LOCKED`) once folio items exist.

**B5 `GET {P}/business-date`**
- **Response:** `{ "business_date": "2026-09-30", "server_time": "2026-09-30T19:30:00Z", "property_local_time": "2026-10-01T02:30:00+07:00", "timezone": "Asia/Jakarta", "night_audit_overdue": false }`

### Users and roles

**U1 `POST /users`**: `{ email, full_name, password, is_tenant_admin?, properties?: [{ property_id, role_id }] }`. The email is unique per tenant (409 `EMAIL_TAKEN`). The password policy is at least 12 characters. The roles and properties must belong to the tenant.

**U3 `PUT /users/{id}/properties`**: `{ grants: [{ property_id, role_id }] }` replaces the whole set, with one role per property. It's audited.

**U4 roles**: `{ name, description, permissions: ["folio.read", …] }`. Codes must be in the catalogue. `is_system` roles can't be renamed or deleted.

### Rooms, room types, status board

**R1 `POST {P}/room-types`**: `{ code, name, description?, max_adults, max_children, max_occupancy, sort_order? }`. Occupancy CHECKs apply. The code is unique.

**R3 `POST {P}/rooms`**: `{ room_number, room_type_id, floor?, building?, initial_housekeeping_status?: "DIRTY" }`. The room number is unique. A `room_housekeeping` row is created in the same transaction (default DIRTY, since a new room should be inspected). Deactivating a room (`PATCH is_active=false`) is rejected if it has an open stay segment, future CONFIRMED assigned lines, or would oversell its type.

**R5 `GET {P}/room-status?date=`** (defaults to BD)
- **Response item:** `{ room_id, room_number, room_type, occupancy: "OCCUPIED"|"RESERVED"|"VACANT", stay?: { id, guest, departure_date, is_due_out }, arrival?: { reservation_room_id, guest }, housekeeping: "CLEAN"|…, block?: { type: "OOO"|"OOS", end_date } }`
- **Rules:** occupancy is **derived**. OCCUPIED if there's an open segment (for `date = BD`; for a future date, if the in-house stay's `eff_dep > date`). RESERVED if a CONFIRMED assigned line covers the date. Otherwise VACANT. Nothing is stored.

### Room blocks

**K2 `POST {P}/room-blocks`**
- **Request:** `{ room_id, block_type: "OOO"|"OOS", start_date, end_date, reason }`
- **Response 201:** the block.
- **Validation:** `start_date ≥ BD`, `end_date > start_date`, and a reason is required.
- **Rules:** see Business Rules §17. It locks the room (and, for OOO, the room type). Conflicts return 409 `ROOM_BLOCK_CONFLICT` with `context.conflicts: [{ kind: "STAY"|"RESERVATION"|"BLOCK", id, dates }]`. An OOO that would oversell returns 409 `INVENTORY_OVERSOLD` with the affected nights.

**K3 `PATCH`**: `{ start_date?, end_date?, reason? }`. Added nights are re-validated. Early release means `end_date ≥ BD`.

### Housekeeping

**H2 `POST {P}/rooms/{id}/housekeeping`**
- **Request:** `{ "status": "CLEAN", "notes": "…" }`
- **Response:** `{ room_id, status, updated_at }`
- **Rules:** transitions follow §16. `INSPECTED` needs `housekeeping.inspect`. It writes `room_housekeeping` + a log (`source = MANUAL`, `business_date = BD`). The transition is rejected with 409 `INVALID_HK_TRANSITION`.

### Guests

**G1 `GET /guests?q=&property_id=`**
- **Rules:** without `guest.search_all`, results are limited to guests linked to the user's properties (by origin, reservation or stay). With it, the whole tenant is searched. `q` matches name, email, phone or document number (prefix match in the MVP; trigram later).

**G2 `POST /guests`**: `{ origin_property_id, last_name, first_name?, email?, phone?, nationality?, date_of_birth?, id_document_type?, id_document_number?, … }`. Needs `guest.write` at the origin property. The response includes a `possible_duplicates` array (same email, phone or document); this is a warning and doesn't block.

**G4 `GET /guests/{id}/history`**: reservations and stays across properties, filtered to the user's accessible properties unless they have `guest.history_all_properties`. Items from hidden properties are counted but not shown (`hidden_count`).

### Billing configuration

**C1 `POST {P}/taxes`**: `{ code, name, rate_percent: "11.0000", tax_on_service: true }`. `0 ≤ rate ≤ 100`. A `PATCH` of `rate_percent` is audited and returns a warning count of in-house stays affected. Deactivating a tax is rejected while it's mapped (409 `TAX_IN_USE`).

**C2 `POST {P}/service-charges`**: `{ code, name, rate_percent: "10.0000" }`.

**C3 `POST {P}/charge-codes`**: `{ code, name, category, default_price_mode: "EXCLUSIVE", default_unit_price? }`.

**C4 `PUT {P}/charge-codes/{id}/rules`**
- **Request:** `{ "tax_ids": [1], "service_charge_ids": [1] }`
- **Response:** the charge code with its rules.
- **Rules:** replaces both mapping sets atomically. Every id must be active and in the same property. Affects future postings only. Audited.

**C5 `POST {P}/charge-calculations`** (preview only; nothing is stored)
- **Request:** `{ charge_code_id, quantity: "1", unit_price: "1000000.00", price_mode?: "EXCLUSIVE", discount_amount?: "0" }`
- **Response:** `{ price_mode, base_amount, discount_amount, net_amount, service_charge_amount, tax_amount, amount, components: [{ type: "SERVICE_CHARGE", code: "SERVICE", rate_percent: "10.0000", taxable_base: "1000000.00", amount: "100000.00" }, { type: "TAX", code: "VAT", rate_percent: "11.0000", taxable_base: "1100000.00", amount: "121000.00" }] }`
- **Rules:** resolver + engine. The engine used here is identical to the one used for posting.

### Rates

**P1 `POST {P}/rate-plans`**: `{ code, name, description?, room_charge_code_id, price_mode: "EXCLUSIVE"|"INCLUSIVE" }`. The charge code must be active with `category = ROOM`.

**P3 `PUT {P}/rates`**
- **Request:** `{ rate_plan_id, room_type_ids: [..], from: "2026-10-01", to: "2026-12-31", weekdays?: [5,6], amount: "1200000.00" }`. `to` is exclusive.
- **Response:** `{ updated_nights: 184 }`
- **Validation:** `to > from`, the span is ≤ 730 days, and `amount ≥ 0`.
- **Rules:** upserts into `rates`. **Existing reservations don't change**, because their nightly prices are snapshots.

### Availability

**V1 `GET {P}/availability?arrival=2026-10-10&departure=2026-10-13&adults=2&children=0`**
- **Response:** `{ nights: ["2026-10-10", …], room_types: [{ room_type_id, code, fits_occupancy: true, available_min: 3, per_night: [{ date, sellable, demand, available }], rates: [{ rate_plan_id, code, price_mode, nightly: [{date, amount}], estimate: { net, service, tax, total } }] }] }`
- **Validation:** `departure > arrival`, `arrival ≥ BD`, and a span of ≤ 365 nights.
- **Rules:** Phase 7 formula. The estimate comes from the engine. It's read-only and takes no locks, so the result is advisory; the real check happens under locks at confirm, assign or check-in.

**V2 `GET {P}/availability/rooms?room_type_id&arrival&departure`** → free rooms for the whole range, with housekeeping status. Used by the assignment and move pickers.

### Reservations

**S1 `POST {P}/reservations`**
- **Request:**
```json
{
  "booker_guest_id": 42,
  "source_code": "PHONE",
  "market_code": "LEISURE",
  "special_request": "High floor",
  "rooms": [{
    "room_type_id": 3, "rate_plan_id": 1,
    "arrival_date": "2026-10-10", "departure_date": "2026-10-13",
    "adult_count": 2, "child_count": 0,
    "guest_id": null, "room_id": null,
    "nightly_overrides": [{ "date": "2026-10-11", "amount": "900000.00" }]
  }],
  "confirm": false
}
```
- **Response 201:** the reservation (as in S3).
- **Validation:** see §2 of the business rules. `nightly_overrides` needs `reservation.override_rate`. `room_id` is only allowed together with `confirm: true`.
- **Rules:** creates a DRAFT with DRAFT lines and snapshots the nightly rates with their `price_mode`. If `confirm: true`, it runs the S5 logic in the same transaction, which is all or nothing.

**S3 `GET {P}/reservations/{id}`**
- **Response:** header + `display_status` (derived, for example `CONFIRMED`, `PARTIALLY_IN_HOUSE`, `IN_HOUSE`, `DEPARTED`, `NO_SHOW` or `CANCELLED`) + `arrival_date`/`departure_date` (derived min and max) + `rooms[]` (each with `status`, `room`, `nightly_rates[]`, `estimate{net, service, tax, total}` and `stay_id?`) + `folios[]` (with computed balances) + `version`.

**S5 `POST …/confirm`** `{ version }`
- **Rules:** §3. Lock order is: business day → room types → assigned rooms → reservation. On failure it returns 409 `ROOM_TYPE_NOT_AVAILABLE` with `context: { room_type_id, nights: [{date, available}] }`, or `ROOM_NOT_AVAILABLE` with the conflicts.

**S6 `POST …/cancel`** `{ version, reason }` → §4. The response includes `folio_balance`, and when that's non-zero, `requires_folio_resolution: true`.

**S9 `PATCH …/rooms/{lineId}`** `{ version, arrival_date?, departure_date?, room_type_id?, rate_plan_id?, adult_count?, child_count?, nightly_overrides? }`
- **Rules:** for CONFIRMED lines, re-run availability for the new shape (excluding the line itself) under locks. Changing the type clears an assigned room that no longer matches, unless it's an upgrade. Surviving nights keep their agreed price, new nights are priced from the grid, and removed nights are deleted. Not allowed on CHECKED_IN lines (use F6).

**S11 `POST …/rooms/{lineId}/no-show`** `{ version, reason? }`: the line is CONFIRMED and `arrival_date ≤ BD` → NO_SHOW (§5).

**S12 `POST …/rooms/{lineId}/assign-room`** `{ version, room_id, upgrade?: false }`
- **Rules:** §6. It locks the booked type, the room's type (if different) and the room. The DB exclusion constraint is the backstop, and a violation is mapped to 409 `ROOM_NOT_AVAILABLE`.

**S14 `POST …/deposits`** `Idempotency-Key` · `{ amount, payment_method, reference_number?, remarks? }`
- **Rules:** §13. Finds or creates the OPEN unlinked folio of the reservation, then does the same as L6.

### Front desk

**F1 `POST …/rooms/{lineId}/check-in`** `Idempotency-Key`
- **Request:** `{ version, room_id?, primary_guest_id, accompanying_guest_ids?: [], adult_count, child_count, override_room_not_ready?: false, override_reason? }`
- **Response 201:** `{ stay: {...}, folio: { id, folio_number, balance } }`
- **Validation:** occupancy is within the room type's limits. `override_reason` is required when overriding.
- **Rules:** §7, including the cleanliness rule from `require_room_inspection_for_checkin`. Errors include `ARRIVAL_DATE_MISMATCH` (with the BD and arrival in the context), `ROOM_NOT_READY` (with the current housekeeping status and the required one), `ROOM_OCCUPIED` and `ROOM_BLOCKED`.

**F2 `POST {P}/walk-ins`** `Idempotency-Key`
- **Request:** `{ primary_guest_id | new_guest: {...}, room_id, rate_plan_id, departure_date, adult_count, child_count, nightly_overrides?, accompanying_guest_ids? }`
- **Response 201:** `{ reservation, stay, folio }`
- **Rules:** §8. The arrival is always BD. It's one transaction through the same services as S1, S5, S12 and F1.

**F5 `POST {P}/stays/{id}/move`** `{ version, room_id, reason, new_nightly_rates?: [{date, amount}], override_room_not_ready? }` → §9. New rates need `frontdesk.rate_change`.

**F6 `POST {P}/stays/{id}/change-departure`** `{ version, departure_date, nightly_overrides? }`
- **Rules:** extending or shortening is decided by comparing the new date with the current one (§10). If the current room isn't free for the extension, it returns 409 `ROOM_NOT_AVAILABLE_FOR_EXTENSION` with `suggest_room_move: true` and a list of alternative free rooms.

**F8 `POST {P}/stays/{id}/check-out`** `Idempotency-Key` · `{ version, early_departure_confirmed?: false }`
- **Response:** `{ stay, folios: [{ id, status: "CLOSED" }], room_housekeeping: "DIRTY" }`
- **Rules:** §14. If `departure_date > BD`, `early_departure_confirmed` must be true. A non-zero balance returns 409 `FOLIO_NOT_BALANCED` with the balance per folio.

**F9 `POST {P}/stays/{id}/reverse-check-in`** `{ version, reason }`: same BD only, and no charges posted (§7).

### Folios, charges and payments

**L2 `GET {P}/folios/{id}`**
- **Response:** `{ id, folio_number, status, reservation_id, stay_id, balance: "…", totals: { charges, payments }, items: [{ id, item_type, business_date, service_date, description, charge_code, quantity, unit_price, price_mode, base_amount, discount_amount, net_amount, service_charge_amount, tax_amount, amount, components: [...], reversed_by_item_id?, reverses_item_id?, posted_at, posted_by }] }`
- The balance is computed as `SUM(amount)`.

**L3 `POST {P}/folios/{id}/charges`** `Idempotency-Key`
- **Request:** `{ charge_code_id, quantity: "2", unit_price?: "50000.00", price_mode?: "EXCLUSIVE", discount_amount?: "0", service_date?: "2026-09-30", description? }`
- **Response 201:** the item with its components (as in L2) and `folio_balance`.
- **Validation:** `quantity > 0`. `unit_price` falls back to the charge code default, and if there's neither, 422. `0 ≤ discount ≤ base`. `service_date ≤ BD`.
- **Rules:** §11. Takes the business day `FOR SHARE`, then the folio `FOR UPDATE`. Uses the resolver and engine. `business_date = BD`.

**L4 `POST {P}/folios/{id}/adjustments`** `Idempotency-Key` · `{ charge_code_id, amount: "-100000.00", price_mode?, reason, related_item_id? }`: a negative amount processed through the engine. The reason is required.

**L5 `POST {P}/folio-items/{id}/reverse`** `{ reason }`: the item's `business_date` must equal BD, it must not already be reversed, and the folio must be OPEN. PAYMENT items **can't** be reversed here; use L7. Creates a REVERSAL item with negated components.

**L6 `POST {P}/folios/{id}/payments`** `Idempotency-Key`
- **Request:** `{ amount: "1221000.00", payment_method: "CARD", reference_number?: "AUTH123", remarks? }`
- **Response 201:** `{ payment, folio_item, folio_balance }`
- **Rules:** §12. The payment and the ledger item are written in one transaction.

**L7 `POST {P}/payments/{id}/void`** `{ reason }`: `payment.business_date = BD`, the status is POSTED, the folio is OPEN, and it has no refunds. Voiding writes a REVERSAL item.

**L8 `POST {P}/payments/{id}/refunds`** `Idempotency-Key` · `{ amount, payment_method?, reference_number?, reason }`: `amount ≤ refundable` (the original minus earlier refunds). The method defaults to the original's.

**L9 `POST {P}/folios/{id}/close`** `{ version }`: balance = 0. It can't be used on a folio linked to an IN_HOUSE stay; use check-out. This is for cancelled or no-show folios.

### Night audit

**N1 `GET {P}/night-audit/preview`**
- **Response:**
```json
{
  "business_date": "2026-09-30",
  "property_local_time": "2026-09-30T23:40:00+07:00",
  "local_time_guard_ok": true,
  "can_run": false,
  "blockers": {
    "unresolved_arrivals": [
      { "reservation_room_id": 103, "confirmation_number": "RES-003", "guest": "Tan", "room_type": "DLX", "arrival_date": "2026-09-30" },
      { "reservation_room_id": 104, "confirmation_number": "RES-004", "guest": "Lee", "room_type": "STD", "arrival_date": "2026-09-30" }
    ],
    "unresolved_departures": [],
    "missing_room_rates": []
  },
  "warnings": { "stale_drafts": 1, "unsettled_cancelled_folios": 0 },
  "expected": { "room_charges_to_post": 57 }
}
```

**N2 `POST {P}/night-audit/no-shows`** (bulk "Mark remaining as no-show")
- **Request:** `{ "business_date": "2026-09-30", "reservation_room_ids": [103, 104], "confirm": true, "reason": "No arrival by night audit" }`
- **Response 200:** `{ "marked": [103, 104], "remaining_blockers": { "unresolved_arrivals": 0, ... } }`
- **Validation:** `confirm` must be true. The list isn't empty. `business_date` equals BD.
- **Rules:** §19.3. Each id must still be an unresolved arrival. It's all or nothing, so any change returns 409 `NO_SHOW_SET_CHANGED` with the ids that changed. **The server never expands the set on its own.**

**N3 `POST {P}/night-audit/run`**
- **Request:** `{ "business_date": "2026-09-30" }`
- **Response 200:** `{ "closed_business_date": "2026-09-30", "new_business_date": "2026-10-01", "summary": { "occupied_rooms": 57, "arrivals": 12, "departures": 9, "no_shows": 2, "room_revenue": { "net", "service", "tax" }, "payments_by_method": {...} } }`
- **Errors:**
  - 409 `BUSINESS_DATE_MISMATCH` (stale screen)
  - 409 `NIGHT_AUDIT_BLOCKED` (same shape as the preview blockers)
  - 409 `NIGHT_AUDIT_TOO_EARLY` (BD > the local date)
  - 409 `ROOM_RATE_MISSING`
- **Rules:** §19.4. Everything happens in one transaction.

### Audit

**X1 `GET {P}/audit-logs`** → entries with `occurred_at` (server time), `business_date`, actor, action and changes. Tenant-level entities (guests, users) are queried with `/audit-logs?entity_type=guest&entity_id=`, and tenant admins only see those.
