# REST API Contracts (revision 3): Step 16

## 1. Conventions

| Topic | Rule |
|---|---|
| Base | `/api/v1`. Property-scoped resources live under **`{P}` = `/api/v1/properties/{propertyId}`**. The specification's `/api/v1/night-audit/...` becomes `{P}/night-audit/...`, because night audit is property-specific. |
| Auth | `Authorization: Bearer <JWT>` (15 minutes; claims `sub` = user id, `tid` = tenant id). The refresh token is opaque and rotated. |
| Scope | For `{P}`: the property belongs to `tid`, and (the user is a tenant admin or has a `user_properties` row), and the endpoint's permission is in that role. A foreign property returns **404**. A missing permission returns **403**. |
| Commands | State changes use `POST …/{action}`. `PATCH` is for attribute edits only. |
| Concurrency | Aggregates with `version` require `"version"` in mutating requests. A mismatch returns 409 `VERSION_CONFLICT`. |
| Idempotency | `Idempotency-Key` header, **required** on endpoints marked ⓘ. A replay returns the stored response. |
| Money | Decimal **strings**. Currency and decimals come from `GET {P}`. |
| Dates | `YYYY-MM-DD` = business or calendar date. RFC 3339 UTC = instant. **Postings never accept a business date from the client**, except as a stale-screen guard where noted. |
| Approval | **Every correction needs approval:** adjustments, reversals, payment voids and refunds carry `approval: { email, password }` (the approver's own credentials, entered in a dialog). See §14.1. |
| Lists | `?limit=&cursor=` → `{ data: [...], next_cursor }` |
| Errors | RFC 9457 problem+json: `{ type, title, status, code, detail, errors: [{field, code}], context }` |
| Status codes | 200, 201, 400, 401, 403, 404, **409** (business rule or state), **422** (validation), 429 |
| TX column | `R` = read-only, with no locks. `T[...]` = single transaction with the listed locks (see [05-transactions-locking.md](05-transactions-locking.md)). |

**Common validation**, which applies to every endpoint and isn't repeated: IDs belong to the same tenant or property (otherwise 404); string lengths follow the columns; enums must match; dates must be valid; money must be ≥ 0 unless stated otherwise.

**Master-data pattern** (room types, rooms, taxes, service charges, charge codes and rate plans):
- `GET` list is R.
- `POST` create is `T[—]` plus an audit entry. A duplicate code returns 409 `CODE_TAKEN`.
- `PATCH` is `T[row FOR UPDATE]` plus an audit entry with old and new data.
- There's no DELETE. Set `is_active = false`, subject to that resource's deactivation rule.

---

## 2. Authentication

**POST `/auth/login`**
- **Purpose:** log in.
- **Request:** `{ tenant_code, email, password }`
- **Response 200:** `{ access_token, refresh_token, expires_in, user: { id, full_name, is_tenant_admin } }`
- **Validation:** all fields are required.
- **Rules:** the tenant is ACTIVE and matched by upper-cased code. The user is looked up by `(tenant_id, lower(email))` and must be active. The argon2id hash is verified. Every failure gives the same 401 `INVALID_CREDENTIALS`. Rate-limited per IP and per tenant + email.
- **TX:** `T[—]`: insert the session and update `last_login_at`.

**POST `/auth/refresh`**
- **Purpose:** rotate the tokens.
- **Request:** `{ refresh_token }`
- **Response:** a new token pair.
- **Rules:** the session is valid and not revoked. The old session is revoked and a new one issued. Reuse of a revoked token revokes all of the user's sessions and returns 401.
- **TX:** `T[session FOR UPDATE]`

**POST `/auth/logout`**
- **Purpose:** revoke the current session.
- **Response:** 204.
- **TX:** `T[—]`

**GET `/auth/me`**
- **Purpose:** return the identity and the permissions per property.
- **Response:** `{ user, tenant, properties: [{ id, code, name, role, permissions[] }] }`
- **TX:** R

## 3. Properties and business days

**GET `/properties`**
- **Purpose:** list the properties the user can access.
- **Response:** `[{ id, code, name, status }]`
- **TX:** R

**POST `/properties`** (tenant admin)
- **Purpose:** create a property.
- **Request:** `{ code, name, address?, city?, country_code?, timezone, currency_code, currency_decimals, check_in_time, check_out_time, require_room_inspection_for_checkin?, night_audit_marks_occupied_dirty?, night_audit_earliest_time?, opening_business_date }`
- **Response 201:** the property, including `business_date`.
- **Validation:** the timezone is a valid IANA name. The currency is ISO 4217. Decimals are 0–3. The code is unique in the tenant.
- **Rules:** creates the first OPEN business day, the 4 document sequences and the ten seeded system charge codes (`seed_charge_codes`, migration 00014; all EXCLUSIVE, with no tax or service mappings).
- **TX:** `T[—]`: all inserts.

**GET `{P}`**
- **Purpose:** property details and settings.
- **TX:** R

**PATCH `{P}`** (`property.manage`)
- **Purpose:** update settings.
- **Request:** any setting.
- **Validation / rules:** `currency_code` and `currency_decimals` return 409 `CURRENCY_LOCKED` once financial data exists (🛡 trigger).
- **TX:** `T[property FOR UPDATE]` + audit

**GET `{P}/business-date`**
- **Purpose:** show the BD against the server time.
- **Response:** `{ business_date, server_time, property_local_time, timezone, night_audit_overdue, night_audit_allowed_from }`
- **TX:** R

**GET `{P}/business-days?from&to`** (`audit.read`)
- **Purpose:** day history.
- **Response:** `[{ business_date, status, opened_at, closed_at, closed_by, summary }]`
- **TX:** R

There are no endpoints to open or close days directly. That only happens through night audit.

## 4. Users and roles (tenant scope; `user.manage` / `role.manage`)

**GET `/users`**
- **Purpose:** list users.
- **TX:** R

**POST `/users`**
- **Purpose:** create a user.
- **Request:** `{ email, full_name, password, is_tenant_admin?, properties?: [{ property_id, role_id }] }`
- **Response 201:** the user.
- **Validation:** 409 `EMAIL_TAKEN` if `(tenant, lower(email))` already exists. The password has at least 12 characters. Properties and roles belong to the tenant (🛡 composite FK).
- **TX:** `T[—]` + audit

**PATCH `/users/{id}`**
- **Purpose:** update or deactivate a user.
- **Rules:** deactivation revokes all sessions.
- **TX:** `T[user FOR UPDATE]` + audit

**PUT `/users/{id}/properties`**
- **Purpose:** replace the user's property grants.
- **Request:** `{ grants: [{ property_id, role_id }] }` (one role per property)
- **TX:** `T[user FOR UPDATE]`: delete and insert + audit

**GET / POST `/roles`, PATCH `/roles/{id}`**
- **Purpose:** manage roles.
- **Request:** `{ name, description?, permissions: [codes] }`
- **Validation:** the codes are in the catalogue. System roles are protected.
- **TX:** `T[role FOR UPDATE]` + audit

**GET `/permissions`**
- **Purpose:** the permission catalogue.
- **TX:** R

## 5. Room types, rooms, status board

**GET / POST `{P}/room-types`, GET / PATCH `{P}/room-types/{id}`** (write: `room.manage`)
- **Request:** `{ code, name, description?, max_adult, max_child, max_occupancy, base_occupancy, sort_order?, is_active? }`
- **Validation:** 🛡 `max_occupancy ≤ max_adult + max_child`, and `base_occupancy ≤ max_occupancy`.
- **Rules:** deactivation is rejected while there are active rooms of the type or future CONFIRMED lines.
- **TX:** master pattern

**GET / POST `{P}/rooms`, GET / PATCH `{P}/rooms/{id}`** (write: `room.manage`)
- **Request:** `{ room_number, room_type_id, floor?, building?, is_active?, initial_housekeeping_status? }`
- **Rules:**
  - Creating a room also creates its `room_housekeeping` row (default DIRTY).
  - Changing `room_type_id` or setting `is_active = false` is rejected if there's an open segment or a future CONFIRMED assigned line, or if either type would be oversold.
- **TX:**
  - create: `T[L2 type]`
  - type change or deactivate: `T[L1, L2 (old and new type), L3 room]`

**GET `{P}/room-status?date=`**
- **Purpose:** the **derived** room board.
- **Response:** `[{ room_id, room_number, room_type, occupancy: OCCUPIED|RESERVED|VACANT, stay?: {id, guest, departure_date, due_out}, arrival?: {reservation_room_id, guest}, housekeeping, block?: {type, end_date} }]`
- **Rules:** Step 14 §14.1. Nothing is stored.
- **TX:** R

## 6. Room blocks (`room_block.manage`)

**GET `{P}/room-blocks?from&to&room_id&status`**
- **Purpose:** list blocks.
- **TX:** R

**POST `{P}/room-blocks`**
- **Purpose:** create an OOO or OOS block.
- **Request:** `{ room_id, block_type, start_date, end_date, reason }`
- **Response 201:** the block.
- **Validation:** `start ≥ BD` and `end > start`. A reason is required.
- **Rules:** no conflicting open segment or CONFIRMED assigned line (409 `ROOM_BLOCK_CONFLICT` with the conflicts). The type must stay at `available ≥ 0` (409 `INVENTORY_OVERSOLD` with the nights).
- **TX:** `T[L1 share, L2 type, L3 room]` + 🛡 EXCLUDE + audit

**PATCH `{P}/room-blocks/{id}`**
- **Purpose:** change the dates or reason, or release early.
- **Request:** `{ start_date?, end_date?, reason? }`
- **Validation:** the new `end ≥ BD`.
- **Rules:** added nights are re-checked.
- **TX:** as for create

**POST `{P}/room-blocks/{id}/cancel`**
- **Request:** `{ reason }`
- **TX:** `T[block FOR UPDATE]` + audit

## 7. Housekeeping

**GET `{P}/housekeeping?status&floor`**
- **Purpose:** the board, with the derived occupancy alongside.
- **TX:** R

**POST `{P}/rooms/{id}/housekeeping`** (`housekeeping.update`; `housekeeping.inspect` for INSPECTED)
- **Purpose:** change the status.
- **Request:** `{ status, notes? }`
- **Response:** `{ room_id, status, updated_at }`
- **Rules:** the legal transitions (Step 14 §14.4). An invalid one returns 409 `INVALID_HK_TRANSITION`.
- **TX:** `T[L1 share, room_housekeeping FOR UPDATE]`: update and insert the log (`business_date = BD`)

**GET `{P}/rooms/{id}/housekeeping/logs`**
- **TX:** R

## 8. Guests (tenant-wide)

**Visibility (one rule for search, profile, duplicates and history).** A guest is *linked* to a property when it is the guest's origin property, or the guest is a booker or occupant of a reservation there, or a primary or accompanying guest of a stay there (`guest_linked_to`, migration 00013). The caller's *readable properties* are those where they hold `guest.read` (tenant administrators: all). A guest is visible when the caller holds `guest.search_all` at one of their readable properties, or the guest is linked to one. An invisible guest is indistinguishable from a missing one: 404 `GUEST_NOT_FOUND`. With `property_id`, only permissions at that property count (404 `PROPERTY_NOT_FOUND` without a grant there). Without `guest.read` anywhere: 403.

**GET `/guests?q&property_id`** (`guest.read`)
- **Purpose:** search, alphabetical by last and first name, with keyset paging.
- **Rules:** `q` is split into tokens; every token must match the start of the first name, last name, email, ID number or guest code, or appear inside the phone number's digits (tokens with at least 4 digits). `%` and `_` are literals. An empty `q` lists everything visible.
- **TX:** R

**POST `/guests`** (`guest.write` at `origin_property_id`, which is required)
- **Purpose:** create a profile.
- **Request:** `{ origin_property_id, first_name?, last_name, email?, phone?, nationality?, country_code?, date_of_birth?, gender?, id_type?, id_number?, address?, city?, notes? }`
- **Validation:** `last_name` required; email format; phone digits with optional `+`, spaces, dashes and brackets; ISO alpha-2 codes (upper-cased); birth date between 1900 and today; `id_type` and `id_number` go together.
- **Response 201:** the guest plus `possible_duplicates: [{ guest, reasons[] }]` and `hidden_duplicate_count`.
- **Rules:** the code (`GST000001`, ...) comes from a gapless tenant-wide series (`tenant_sequences`, allocated last, rolled back with the transaction). Duplicates never block. Candidates match on email (case-insensitive), phone digits, ID type and number, or name and birth date. Look-alikes the caller may not see are only counted, so a profile at another property is not disclosed.
- **TX:** `T[L5 sequence]` + audit

**GET `/guests/{id}`** (`guest.read`)
- **Response:** the guest plus `can_edit` (`guest.write` at a property where the guest is linked).
- **TX:** R

**PATCH `/guests/{id}`**
- **Request:** any profile field; omitted fields stay, an empty string clears (including `date_of_birth`). The code and the origin property never change.
- **Rules:** visible, and `guest.write` at a property where the guest is linked (otherwise 403). Validation applies to the merged profile.
- **TX:** `T[guest FOR UPDATE]` + audit. The audit entry masks the ID number (last four characters) and records the notes' length only, so the trail is not a second copy of identity data.

**GET `/guests/{id}/history`** (`guest.read`)
- **Purpose:** reservations (as booker or occupant) and stays (as primary or accompanying guest) across properties, newest first.
- **Response:** `{ data: [{ type, id, number, role, status, property_id, property_code, property_name, arrival_date, departure_date }], next_cursor?, hidden_count }`.
- **Rules:** limited to the caller's readable properties unless they hold `guest.history_all_properties` at one of them (administrators: all). `hidden_count` counts what was left out. Reservation dates are the span of its lines.
- **TX:** R

## 9. Billing configuration (write: `billing_config.manage`; read: any access to the property)

Codes are upper-cased and immutable (PATCH rejects `code`). Rates are percentage strings with four decimals (`"11.0000"`); input may omit trailing zeros. Amounts (`default_unit_price`) are plain decimal strings, formatted with the property's currency decimals, and may not have more decimals than the currency (0 to 3).

**GET / POST `{P}/taxes`, PATCH `{P}/taxes/{id}`**
- **Request:** `{ code, name, rate: "11.0000", tax_on_service, is_active? }`
- **Validation:** `0 ≤ rate ≤ 100`, at most four decimals. There is **no inclusive flag** (unknown fields are 422 `UNKNOWN_FIELD`).
- **Rules:**
  - A rate change affects future postings only. The PATCH response then includes `affected_open_stays`: the open stays whose remaining nights are charged through a charge code that maps this tax.
  - Deactivation is rejected while the tax is actively mapped (409 `TAX_IN_USE`, `context.charge_code_rules`).
- **TX:** master pattern (`T[tax FOR UPDATE]` + audit)

**GET / POST `{P}/service-charges`, PATCH `/{id}`**
- **Request:** `{ code, name, rate, is_active? }`
- **Rules:** same as taxes (409 `SERVICE_CHARGE_IN_USE`).
- **TX:** master pattern

**GET / POST `{P}/charge-codes`, GET / PATCH `/{id}`**
- **Request:** `{ code, name, charge_type, price_mode, default_unit_price?, is_active? }`. Responses carry the active rules: `taxes: [{ tax_id, code, name, rate, tax_on_service, sequence }]` and `service_charges: [{ service_charge_id, code, name, rate, sequence }]`.
- **Filters:** `active`, `charge_type`.
- **Rules:**
  - `price_mode` is immutable once the code is used by a rate plan, a nightly rate or a folio item (409 `PRICE_MODE_LOCKED`, enforced by a trigger). Re-sending the current value is not a change.
  - The charge type of a **system** code is fixed (409 `SYSTEM_CHARGE_CODE_LOCKED`); a code used for room revenue stays `ROOM` (trigger, 409 `CHARGE_TYPE_LOCKED`).
  - A code cannot be deactivated while an active rate plan sells through it (409 `CHARGE_CODE_IN_USE`).
  - Every property starts with ten system codes: `ROOM`, `ROOM_EXEMPT` (type ROOM), `BREAKFAST`, `RESTAURANT`, `MINIBAR` (FOOD_BEVERAGE), `LAUNDRY`, `EXTRA_BED` (SERVICE), `NO_SHOW_FEE`, `CANCEL_FEE` (FEE), `OTHER`. They carry no rules; the owner maps taxes and service charges per property.
- **TX:** master pattern (`T[charge_code FOR UPDATE]` + audit)

**PUT `{P}/charge-codes/{id}/rules`**
- **Purpose:** replace the ordered tax and service mappings.
- **Request:** `{ taxes: [{ tax_id, sequence }], service_charges: [{ service_charge_id, sequence }] }` (at most 20 each; an empty list removes all rules).
- **Response:** the charge code with its rules.
- **Validation:** sequences are unique per list, from 1 to 32767 (gaps allowed); ids are not repeated; the taxes and service charges exist in the property (404 `TAX_NOT_FOUND` / `SERVICE_CHARGE_NOT_FOUND`) and are active (422 `TAX_INACTIVE` / `SERVICE_CHARGE_INACTIVE`, field `taxes[i].tax_id`).
- **Rules:** mappings that are no longer listed are set to `is_active = false` (history stays); re-adding one reuses its row. Affects future postings only.
- **TX:** `T[L1 share, charge_code FOR UPDATE, taxes and service charges FOR SHARE]`: deactivate all, upsert the listed rules, audit with the old and new rule sets. The share locks make "map a tax" and "deactivate the tax" mutually exclusive, so an inactive tax is never mapped.

**ChargeRuleResolver** (internal, used by `ChargeCalculationService` in M6): `billingconfig.Service.ResolveRules(tenant, property, chargeCode)` returns the code with its active, ordered rules and decimal rates. A mapping applies only while the tax or service charge itself is active. It is a lock-free read of committed configuration.

**POST `{P}/charge-calculations`** (any access to the property)
- **Purpose:** preview the engine result. Nothing is posted or stored.
- **Request:** `{ charge_code_id, quantity, unit_price, price_mode?, discount_amount? }`. The numbers are plain decimal strings (no separators or exponents). `quantity` and `unit_price` are signed: a negative price (or quantity) previews a credit, as an adjustment does. `discount_amount` is a magnitude.
- **Response 200:** `{ price_mode, quantity, unit_price, base_amount, discount_amount, net_amount, rounding_adjustment, service_charges: [{ rule_id, code, name, rate, base_amount, amount, sequence }], taxes: [{ ..., tax_on_service }], service_charge_total, tax_total, taxable_amount, total_amount }`. Money uses the property's currency decimals (`"7.00"`), rates four (`"11.0000"`). `discount_amount` has the sign of `base_amount`, so `net = base − discount` holds for exclusive prices.
- **Errors:** 422 with the field (`quantity`, `unit_price`, `discount_amount`, `price_mode`, `charge_code_id`); 404 `CHARGE_CODE_NOT_FOUND` (also another property's code); 409 `CHARGE_CODE_INACTIVE`.
- **Rules:** `billingconfig.Service.Calculate` (ChargeCalculationService): resolve the active ordered rules and the property's decimals, call `chargecalc.Calculate`. A price-mode override replaces the code's mode for this calculation only.
- **TX:** R

## 10. Rate plans and rates

**GET / POST `{P}/rate-plans`, PATCH `/{id}`** (write: `rate.manage`; read: any access to the property)
- **Request:** `{ code, name, description?, meal_plan (RO|BB|HB|FB|AI), cancellation_policy?, is_refundable?, room_charge_code_id, is_active? }`. The code is upper-cased and immutable. Responses add `room_charge_code` (its code) and `price_mode` (the code's, i.e. how grid amounts are read).
- **Validation:** the room charge code is an active charge code of this property with `charge_type = ROOM` (404 `CHARGE_CODE_NOT_FOUND` for another property's, 422 `CHARGE_CODE_NOT_ROOM` on `room_charge_code_id` otherwise; a trigger backs it up).
- **Rules:**
  - Changing `room_charge_code_id` affects **new** nightly snapshots only.
  - A plan that already has rates cannot move to a room charge code with a different price mode (409 `RATE_PLAN_PRICE_MODE_MISMATCH`, with the counts in `context`): its amounts would silently be read as another kind of price. Create a new plan instead.
  - Deactivation is allowed; it stops new bookings (M8) and leaves existing reservations alone.
- **TX:** `T[L1 share, room charge code FOR SHARE, plan FOR UPDATE]` + audit

**GET `{P}/rates?rate_plan_id&from&to&room_type_id?`**
- **Purpose:** the rate grid, with the plan's price mode.
- **Response:** `{ rate_plan_id, price_mode, room_charge_code, from, to, rates: [{ room_type_id, stay_date, amount }] }`, ordered by night then room type. Amounts carry the currency's decimals. Nights without a rate are absent. Not paginated: `from` and `to` (exclusive) are required and span at most 730 days.
- **TX:** R

**PUT `{P}/rates`** (`rate.manage`)
- **Purpose:** bulk upsert.
- **Request:** `{ rate_plan_id, room_type_ids[] (1 to 50), from, to (exclusive), weekdays? (MON..SUN; all days when omitted), amount }`
- **Response:** `{ updated_nights, created_nights }`: nights written (new or overwritten) and how many of those were new.
- **Validation:** `to > from`, a span of ≤ 730 days, `amount ≥ 0` with at most the currency's decimals (0 to 3) and fewer than 10^15; the weekdays must select at least one night (422 `NO_NIGHTS`). Room types and the plan must exist in the property (404). Rates may be prepared for an inactive plan and for past dates.
- **Rules:** all or nothing. Existing reservations are not affected (snapshots).
- **TX:** `T[L1 share, L2 room types FOR SHARE, plan FOR SHARE]`: one batched upsert + an audit summary. The plan's share lock and the plan update's exclusive lock make a fill and a price mode switch mutually exclusive.

**Price lookup** (internal, for reservations in M8): `rates.Service.NightlyPrices(tenant, property, plan, room type, arrival, departure)` returns the price of every night of `[arrival, departure)` (at most 365 nights) with the room charge code and price mode they are read in. The plan must be active (409 `RATE_PLAN_INACTIVE`) and every night must have a rate (409 `RATE_NOT_SET`, `context.nights` lists up to 31 missing nights and `context.missing_nights` the total). A missing rate is never guessed. Taxes and service are the engine's job: the caller passes each amount and the code's rules to `ChargeCalculationService`.

## 11. Availability

**GET `{P}/availability?arrival&departure&adults&children`** (`reservation.read`)
- **Purpose:** availability per room type and night, with rate estimates.
- **Response:** `{ nights[], room_types: [{ room_type_id, fits_occupancy, available_min, per_night: [{date, sellable, demand, available}], rate_plans: [{ id, code, nightly: [{date, amount}], estimate: {net, service, tax, total} }] }] }`
- **Validation:** `departure > arrival`, `arrival ≥ BD`, and ≤ 365 nights.
- **Rules:** Step 14 §14.1. The estimate comes from `ChargeCalculationService`. The result is advisory. A rate plan also carries `name`, `price_mode` and `missing_nights`; with missing nights it has no `estimate`.
- **TX:** R

**GET `{P}/availability/rooms?room_type_id&arrival&departure`**
- **Purpose:** free specific rooms, with their housekeeping status.
- **TX:** R

## 12. Reservations

**POST `{P}/reservations`** ⓘ (`reservation.create`)
- **Purpose:** create a draft, optionally confirming it at once.
- **Request:** `{ guest_id?, source, market?, special_request?, remarks?, rooms: [{ room_type_id, rate_plan_id, arrival_date, departure_date, adult_count, child_count, guest_id?, room_id?, nightly_overrides?: [{date, amount, discount_amount?}] }], confirm?: false }`
- **Response 201:** the reservation (as in GET).
- **Validation:** Step 14 §14.2 "Create". Overrides need `reservation.override_rate`. `room_id` requires `confirm: true`.
- **Rules:** creates the DRAFT and snapshots the nightly rows. `confirm: true` runs **confirm** in the same transaction.
- **Idempotency (implemented):** the key and a body hash are stored on the reservation; a replay returns the reservation, a different body with the same key is 422 `IDEMPOTENCY_KEY_REUSED`.
- **TX:** `T[L1 share, L5]`. With `confirm: true`, it takes the confirm locks (L2, L3) before the sequence.

**GET `{P}/tape-chart?from&to`** (`reservation.read`)
- **Purpose:** the read-only tape chart: active rooms with the CONFIRMED and CHECKED_IN lines and active blocks touching `[from, to)`, plus lines without a room per room type. At most 62 days.
- **TX:** R

**GET `{P}/reservations?arrival_from&arrival_to&status&q`**
- **Purpose:** search (lines are joined for dates and status).
- **TX:** R

**GET `{P}/reservations/{id}`**
- **Purpose:** the reservation detail.
- **Response:** the header + derived `arrival_date`, `departure_date` and `display_status` + `rooms[]` (with `status`, `room`, `nightly_rates[]`, `estimate`, `stay_id?`) + `folios[]` (with computed balances) + `version`.
- **TX:** R

**PATCH `{P}/reservations/{id}`** (`reservation.update`)
- **Purpose:** edit header fields (source, market, requests, booker).
- **TX:** `T[L4 reservation]` + audit

**POST `{P}/reservations/{id}/confirm`** (`reservation.create`)
- **Request:** `{ version }`
- **Rules:** §14.2 "Confirm". Failures return 409 `ROOM_TYPE_NOT_AVAILABLE` (with `context.nights`) or `ROOM_NOT_AVAILABLE`.
- **TX:** `T[L1, L2, L3, L4]` (see Step 15 #1)

**POST `{P}/reservations/{id}/cancel`** (`reservation.cancel`)
- **Request:** `{ version, reason }`
- **Response:** the reservation + `folio_balance` + `requires_folio_resolution`.
- **Rules:** rejected if any line is CHECKED_IN or COMPLETED.
- **TX:** `T[L1, L4]` + audit

**POST `{P}/reservations/{id}/reinstate`** (`reservation.reinstate`)
- **Request:** `{ version }`
- **Rules:** arrival ≥ BD, plus availability.
- **TX:** as for confirm

**POST `{P}/reservations/{id}/rooms`** (`reservation.update`)
- **Purpose:** add a line.
- **Request:** one `rooms[]` element.
- **Rules:** on a CONFIRMED header the line is created as CONFIRMED, with the availability check.
- **TX:** as for confirm

**PATCH `{P}/reservations/{id}/rooms/{lineId}`** (`reservation.update`)
- **Purpose:** amend a line.
- **Request:** `{ version, arrival_date?, departure_date?, room_type_id?, rate_plan_id?, adult_count?, child_count?, nightly_overrides? }`
- **Rules:** only DRAFT or CONFIRMED lines. Availability is re-checked excluding the line itself. Surviving nights keep their snapshot.
- **TX:** `T[L1, L2 (old and new type), L3 (assigned room), L4]`

**POST `…/rooms/{lineId}/cancel`**
- **Request:** `{ version, reason }`
- **TX:** `T[L1, L4]`

**POST `…/rooms/{lineId}/no-show`** (`nightaudit.no_show`)
- **Request:** `{ version, reason? }`
- **Rules:** CONFIRMED with arrival ≤ BD.
- **TX:** `T[L1 share, L4]`

**POST `…/rooms/{lineId}/assign-room`** (`reservation.update`)
- **Request:** `{ version, room_id, upgrade?: false }`
- **Rules:** §14.2 "Assign". `upgrade` needs `reservation.upgrade`.
- **TX:** see Step 15 #2. `23P01` → 409 `ROOM_NOT_AVAILABLE`.

**POST `…/rooms/{lineId}/unassign-room`**
- **Request:** `{ version }`
- **Rules:** an upgraded line returns to its booked type, which must have availability.
- **TX:** `T[L1, L2, L3, L4]` (the type locks are needed for the availability re-check)

**POST `{P}/reservations/{id}/deposits`** ⓘ (`payment.post`)
- **Purpose:** take a deposit.
- **Request:** `{ amount, payment_method, reference_number?, remarks? }`
- **Response 201:** `{ payment, folio_item, folio: { id, balance } }`
- **Rules:** the reservation is DRAFT or CONFIRMED. `PaymentService.Deposit`.
- **TX:** `T[L1 share, L4 reservation, folio, L5]`

## 13. Stays, check-in, room move, check-out

**POST `…/reservations/{id}/rooms/{lineId}/check-in`** ⓘ (`frontdesk.checkin`)
- **Purpose:** check in.
- **Request:** `{ version, room_id?, guest_id, accompanying_guest_ids?: [], adult_count, child_count, override_room_not_ready?: false, override_reason? }`
- **Response 201:** `{ stay, stay_room, folio: { id, folio_number, balance } }`
- **Validation:** occupancy is within the room type's limits. An override needs `frontdesk.checkin_unready_room` and a reason.
- **Rules:** Step 14 §14.3 "Check-in", including `require_room_inspection_for_checkin`. Errors: `ARRIVAL_DATE_MISMATCH`, `ROOM_NOT_READY` (with `context.current` and `context.required`), `ROOM_OCCUPIED`, `ROOM_BLOCKED` and `ROOM_NOT_AVAILABLE`.
- **TX:** see Step 15 #3

**POST `{P}/walk-ins`** ⓘ (`frontdesk.checkin` + `reservation.create`)
- **Purpose:** a walk-in.
- **Request:** `{ guest_id | new_guest{...}, room_id, rate_plan_id, departure_date, adult_count, child_count, nightly_overrides?, accompanying_guest_ids? }`
- **Response 201:** `{ reservation, stay, folio }`
- **Rules:** arrival = BD, `source = WALK_IN`. Runs create, confirm, assign and check-in.
- **TX:** a single `T[L1, L2, L3, L4, L5]`

**GET `{P}/stays?status&departure_date&room_id`**
- **Purpose:** in-house and due-out lists.
- **TX:** R

**GET `{P}/stays/{id}`**
- **Purpose:** the stay detail.
- **Response:** the stay + `segments[]` + `guests[]` + `line` + `nightly_rates[]` (with the posted flag for each night) + `folios[]` + `expected_charges[]`.
- **TX:** R

**POST `{P}/stays/{id}/move`** (`frontdesk.room_move`)
- **Purpose:** move rooms.
- **Request:** `{ version, room_id, reason, new_nightly_rates?: [{date, amount}], override_room_not_ready?, override_reason? }`
- **Response:** `{ stay, closed_segment, new_segment }`
- **Rules:** §14.3 "Room move". New rates need `frontdesk.rate_change`, and only for unposted nights.
- **TX:** see Step 15 #4

**POST `{P}/stays/{id}/change-departure`** (`reservation.update`)
- **Purpose:** extend, shorten, or correct the departure date.
- **Request:** `{ version, departure_date, nightly_overrides? }`
- **Rules:**
  - Extend: room + inventory. Otherwise 409 `ROOM_NOT_AVAILABLE_FOR_EXTENSION` with `suggest_room_move` and alternative rooms.
  - Shorten: the new date is ≥ BD + 1 and later than the last posted night.
- **TX:** `T[L1, L2, L3 current room, L4 stay]`

**POST `{P}/stays/{id}/guests`** (`frontdesk.checkin`)
- **Purpose:** add an accompanying guest.
- **Request:** `{ guest_id }`
- **TX:** `T[L4 stay]`

**POST `{P}/stays/{id}/check-out`** ⓘ (`frontdesk.checkout`)
- **Purpose:** check out.
- **Request:** `{ version, confirm_early_departure?: false }`
- **Response:** `{ stay, posted_room_charges: [...], folios: [{ id, status: CLOSED }], housekeeping: DIRTY }`
- **Rules:** §14.3 "Check-out" (departure handling for local date = BD vs local date > BD, then RoomChargePostingService, then balance = 0). Errors: `EARLY_DEPARTURE_NOT_CONFIRMED`, `REQUIRED_CHARGES_NOT_POSTED` (with the ERROR items) and `FOLIO_NOT_BALANCED` (with the balances).
- **TX:** see Step 15 #7

**POST `{P}/stays/{id}/reverse-check-in`** (`frontdesk.reverse_checkin`)
- **Purpose:** undo a check-in.
- **Request:** `{ version, reason }`
- **Rules:** same BD. No CHARGE items.
- **TX:** `T[L1, L3 room, L4 reservation → stay → folio]`

## 14. Folios, charges, payments

**GET `{P}/folios?reservation_id&stay_id&status`** (`folio.read`)
- **TX:** R

**GET `{P}/folios/{id}`**
- **Response:** `{ id, folio_number, folio_type, status, reservation_id, stay_id, balance, totals: {debit, credit}, items: [{ id, transaction_type, business_date, service_date, transaction_at, description, charge_code, quantity, unit_price, price_mode, base_amount, discount_amount, net_amount, rounding_adjustment, service_charge_total, tax_total, debit, credit, components: [{ component_type, code, name, rate, base_amount, amount, sequence }], reverses_item_id, reversed_by_item_id, stay_room: {room_number}, created_by }] }`
- **Rules:** `balance = Σdebit − Σcredit`.
- **TX:** R

**POST `{P}/folios/{id}/charges`** ⓘ (`folio.post_charge`)
- **Purpose:** a manual charge.
- **Request:** `{ charge_code_id, quantity, unit_price?, price_mode?, discount_amount?, service_date?, description? }`
- **Response 201:** the item (as in GET) + `folio_balance`.
- **Validation:** `quantity > 0`. `unit_price` falls back to the charge code default, otherwise 422. `0 ≤ discount ≤ base`. `service_date ≤ BD`.
- **Rules:** `charge_type = ROOM` is **rejected** (409 `ROOM_CHARGE_REQUIRES_ROOM_POSTING`). The folio is OPEN. After check-out, `folio.post_after_checkout` is needed. `FolioPostingService.PostCharge`.
- **TX:** see Step 15 #5

**POST `{P}/folios/{id}/adjustments`** ⓘ (`folio.adjust`)
- **Purpose:** an adjustment.
- **Request:** `{ charge_code_id, amount (signed), price_mode?, reason, related_item_id?, approval }`
- **Rules:** processed through the engine, with a signed base.
- **TX:** as for charges

**POST `{P}/folio-items/{id}/reverse`** (`folio.reverse`)
- **Purpose:** a same-day reversal.
- **Request:** `{ reason, approval }`
- **Response 201:** the reversal item.
- **Rules:** the item's `business_date = BD` and it isn't already reversed. Not allowed on PAYMENT or REFUND items (use void). A room-charge item flips its register row to REVERSED.
- **TX:** `T[L1 share, L4 folio]` + 🛡 UK `reverses_item_id`

**POST `{P}/folios/{id}/payments`** ⓘ (`payment.post`)
- **Purpose:** a payment.
- **Request:** `{ amount, payment_method, reference_number?, remarks? }`
- **Response 201:** `{ payment, folio_item, folio_balance }`
- **Rules:** `PaymentService.Post`.
- **TX:** see Step 15 #6

**POST `{P}/payments/{id}/void`** (`payment.void`)
- **Request:** `{ reason, approval }`
- **Rules:** same BD, POSTED, the folio is OPEN, and there are no refunds.
- **TX:** `T[L1 share, L4 folio → payment]`

**POST `{P}/payments/{id}/refunds`** ⓘ (`payment.refund`)
- **Request:** `{ amount, payment_method?, reference_number?, reason, approval }`
- **Rules:** `amount ≤ refundable`.
- **TX:** `T[L1 share, L4 folio → original payment, L5]`

**GET `{P}/payments?business_date&method`** (`folio.read`)
- **Purpose:** cashier list and totals.
- **TX:** R

**POST `{P}/folios/{id}/close`** (`folio.post_charge`)
- **Request:** `{ version }`
- **Rules:** balance = 0. It must not be linked to an OPEN stay (use check-out). Used for cancelled or no-show folios.
- **TX:** `T[L4 folio]`

**POST `{P}/folios/{id}/reopen`** (`folio.reopen`)
- **Request:** `{ version, reason }`
- **TX:** `T[L4 folio]` + audit

## 15. Night audit, room charge preview and posting

**GET `{P}/night-audit/preview`** (`nightaudit.run`)
- **Purpose:** the full pre-check.
- **Response:** `{ business_date, property_local_time, time_guard_ok, can_run, blockers: { unresolved_arrivals: [{reservation_room_id, confirmation_number, guest, room_type, arrival_date}], unresolved_departures: [{stay_id, stay_number, room, guest, departure_date}], charge_errors: [...], invalid_charges: [...] }, missing_charges: { count, items }, tonight_charges: { count, total }, warnings: {...} }`
- **Rules:** runs all checks (Step 12 §12.1). Writes nothing.
- **TX:** R

**POST `{P}/night-audit/no-shows`** (`nightaudit.no_show`)
- **Purpose:** the bulk "Mark remaining as no-show" action.
- **Request:** `{ business_date, reservation_room_ids: [103, 104], confirm: true, reason }`
- **Response:** `{ marked: [...], remaining_blockers }`
- **Validation:** `confirm = true`, the list isn't empty, and `business_date = BD`.
- **Rules:** exactly those IDs, and each is still unresolved. Otherwise 409 `NO_SHOW_SET_CHANGED`. The server never expands the set.
- **TX:** `T[L1 share, L4 lines sorted]` + audit for each line

**POST `{P}/night-audit/room-charges/preview`** (`nightaudit.run` or `folio.post_charge`)
- **Purpose:** the room charge preview.
- **Request:** `{ business_date, stay_ids?: [] }`
- **Response:** `{ business_date, items: [{ stay_id, stay_number, guest, room_number, folio_id, service_date, charge_code, room_rate, price_mode, service_charge, tax, total, rounding_adjustment, status: READY|ALREADY_POSTED|NOT_APPLICABLE|ERROR, reason }], totals: { ready_count, ready_total } }`
- **Rules:** `RoomChargePostingService.Post(DryRun = true)`. **It modifies nothing.**
- **TX:** R

**POST `{P}/night-audit/room-charges`** ⓘ (`nightaudit.run` or `folio.post_charge`)
- **Purpose:** post room charges ("Post missing charges").
- **Request:** `{ business_date, stay_ids?: [] }`
- **Response:** `{ results: [{ stay_id, service_date, status: POSTED|ALREADY_POSTED|NOT_APPLICABLE|ERROR, folio_item_id?, total?, reason? }], revalidation: { ready: 0, errors: [...], invalid: [...] } }`
- **Validation:** `business_date = BD` (409 `BUSINESS_DATE_MISMATCH`).
- **Rules:** `RoomChargePostingService.Post(trigger = MANUAL)`. It's idempotent: running it twice gives ALREADY_POSTED. ERROR items aren't posted. A validation re-run happens after the commit.
- **TX:** `T[L1 share, L4 stays sorted → folios]` + 🛡 register UK, then a separate read for the revalidation

**POST `{P}/night-audit/run`** (`nightaudit.run`)
- **Purpose:** close the business day.
- **Request:** `{ business_date }`
- **Response 200:** `{ closed_business_date, new_business_date, room_charges_posted, summary }`
- **Errors:** 409 `NIGHT_AUDIT_IN_PROGRESS`, `BUSINESS_DATE_MISMATCH`, `NIGHT_AUDIT_TOO_EARLY` and `NIGHT_AUDIT_BLOCKED` (with `context` in the same shape as the preview blockers).
- **Rules:** Step 12 §12.5. It never changes guest, reservation or stay status.
- **TX:** a single `T[L0 advisory, L1 UPDATE, L4 stays → folios]`. Any blocker rolls back everything.

## 16. Audit

**GET `{P}/audit-logs?entity_type&entity_id&user_id&from&to`** (`audit.read`)
- **Response:** `[{ created_at, business_date, user, action, entity_type, entity_id, old_data, new_data }]`
- **Rules:** entries for tenant-level entities (guests, users) are available at `/audit-logs?…` to tenant admins.
- **TX:** R

### 14.1 Correction approval (decision: every correction, M9)

Applies to: `POST {P}/folios/{id}/adjustments`, `POST {P}/folio-items/{id}/reverse`, `POST {P}/payments/{id}/void` and `POST {P}/payments/{id}/refunds`.

- **Request block:** `approval: { email, password }`. It is required; a request without it is 422 `APPROVAL_REQUIRED`.
- **Who may approve:** any active user of the tenant who holds the new permission **`correction.approve`** at that property (tenant administrators pass every check). **The actor may approve their own correction**: they enter their own credentials again. A separate approver is optional.
- **Two separate checks:** the actor needs the operation's permission (`folio.adjust`, `folio.reverse`, `payment.void`, `payment.refund`), and the approver needs `correction.approve`. A user with both can do both.
- **Verification:** the approver's password is checked with the same argon2id verifier and the same rate limits as login, inside the request's transaction. Wrong email or password gives 401 `APPROVAL_INVALID_CREDENTIALS` (one generic error, no hint which part was wrong). A valid approver without the permission gives 403 `APPROVAL_NOT_PERMITTED`. Both count toward the login rate limit. Verifying an approval never creates a session or token.
- **Never stored:** the password is not logged, not audited and not echoed back. Only the approver's user id is recorded.
- **Recorded:** `approved_by` (NOT NULL, added by the M9 migration) on the correcting row: REVERSAL and ADJUSTMENT `folio_items`, VOIDED payments and REFUND payments. The audit entry carries `actor` and `approved_by` (equal when self-approved).
- **Replays:** an `Idempotency-Key` replay returns the stored result and does not ask for approval again.
- **Correction routes by date:** same business date → void (payments) or reversal (charges); an earlier date → an ADJUSTMENT or a REFUND posted on the current business date. A void or reversal of an earlier date is 409 `CORRECTION_REQUIRES_ADJUSTMENT`. Nothing is ever back-posted.
