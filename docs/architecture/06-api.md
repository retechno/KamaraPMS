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

**GET / POST `{P}/bed-types`, PATCH `{P}/bed-types/{id}`** (write: `room.manage`; GET: any access to the property)
- **Request:** `{ code, name, sort_order?, is_active? }`; PATCH: `{ name?, sort_order?, is_active? }` (the code is fixed). GET takes `active` and is not paginated; it lists in `sort_order, code`.
- **Rules:** codes are unique per property (409 `CODE_TAKEN`). A bed type can be switched off while rooms use it: they keep it, but choosing it anew is 422 `BED_TYPE_INACTIVE`. A bed type of another property is 404 `BED_TYPE_NOT_FOUND` (path) or 422 `BED_TYPE_NOT_FOUND` (field). Every property starts with King, Queen, Double, Twin and Single.
- **TX:** master pattern (no row locks: nothing is counted per bed type)

**GET / POST `{P}/rooms`, GET / PATCH `{P}/rooms/{id}`** (write: `room.manage`)
- **Request:** `{ room_number, room_type_id, floor?, building?, bed_type_id?, is_active?, initial_housekeeping_status? }`; PATCH `bed_type_id: 0` takes the bed type off.
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
- **Request:** `{ code, name, description?, meal_plan (RO|BB|HB|FB|AI), cancellation_policy?, is_refundable?, room_charge_code_id, occupancy_kind? (PAID|COMPLIMENTARY|HOUSE_USE, default PAID), is_active? }`. The code is upper-cased and immutable, and so is `occupancy_kind` (the PATCH has no such field). A COMPLIMENTARY or HOUSE_USE plan is priced at zero: every night costs 0 without a grid rate, price overrides are refused (422 `OVERRIDE_NOT_ALLOWED`) and the room charge posts as a zero charge (no service, no tax). Booking one needs `reservation.complimentary` and an `occupancy_reason` on the line (422 `REQUIRED` on `occupancy_reason`); the availability search lists such plans only to who has the permission. The line shows `occupancy_kind` (its plan's) and `occupancy_reason`; the reason can be edited by PATCH of the line, and a paid plan keeps no reason. Responses add `room_charge_code` (its code) and `price_mode` (the code's, i.e. how grid amounts are read).
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
- **Request:** `{ guest_id?, source, market?, special_request?, remarks?, rooms: [{ room_type_id, rate_plan_id, arrival_date, departure_date, adult_count, child_count, guest_id?, room_id?, bed_type_id?, nightly_overrides?: [{date, amount, discount_amount?}] }], confirm?: false }`
- **Response 201:** the reservation (as in GET).
- **Validation:** Step 14 §14.2 "Create". Overrides need `reservation.override_rate`, a `rate_override_reason` and an approval: `rate_override_approval` (`{email, password}` of a user holding `reservation.override_rate_approve`), which is not needed when the caller holds that permission too (422 `APPROVAL_REQUIRED`, 401 `APPROVAL_INVALID_CREDENTIALS`, 403 `APPROVAL_NOT_PERMITTED`). The same rule applies when a room is added (`POST .../rooms`), a room is amended (`PATCH .../rooms/{lineId}`), to a walk-in and to the extension of a stay (`PATCH /stays/{id}/departure`, with `nightly_overrides`); the credentials are verified once per request, never stored (the idempotency hash leaves them out) and the audit entry records `override_approved_by` and `rate_override_reason`. `room_id` requires `confirm: true`.
- **Rules:** creates the DRAFT and snapshots the nightly rows. `confirm: true` runs **confirm** in the same transaction.
- **Idempotency (implemented):** the key and a body hash are stored on the reservation; a replay returns the reservation, a different body with the same key is 422 `IDEMPOTENCY_KEY_REUSED`.
- **TX:** `T[L1 share, L5]`. With `confirm: true`, it takes the confirm locks (L2, L3) before the sequence.

**GET `{P}/availability/calendar?from&to`** (`reservation.read`)
- **Purpose:** the availability of every active room type for each night of `[from, to)`, as a grid: `sellable`, `blocked`, `held`, `available` (`sellable - held`, negative when oversold) and `occupancy_percent` per type and night, the parts of `held` (`in_house` = rooms of open stays, `reservations` = rooms of CONFIRMED lines not yet checked in; `held = in_house + reservations`) and `arrivals` (rooms arriving that night: CONFIRMED lines arriving plus stays, walk-ins included, whose arrival date it is), and `totals` per night, and `complimentary` / `house_use` (the held rooms that belong to a plan of that occupancy kind; they stay part of `held`). At most 62 days.
- **Optional `by_bed=true`:** each room type also lists `beds`: per bed type of its active rooms the same numbers, counted over the rooms with that bed. Only bookings already assigned to a room (and open stays) count there; a booking without a room may still end up in any bed, so it is not counted, and the bed rows can show more free rooms than the type row. The type rows and `totals` are unchanged. Bed type stays a description, not inventory: this is a view of the free rooms, not a rule for booking.
- **Rules:** read-only and advisory (only booking decides); the numbers are the availability engine's own (`NightInventory`), so the calendar can never disagree with a booking attempt. Inactive rooms count nowhere; blocked rooms are `blocked`, not `sellable`.
- **TX:** R

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
- **Request:** `{ version, arrival_date?, departure_date?, room_type_id?, rate_plan_id?, adult_count?, child_count?, bed_type_id?, nightly_overrides? }`; `bed_type_id: 0` takes the request off.
- **Rules:** only DRAFT or CONFIRMED lines. `bed_type_id` is the bed the guest asks for (see §5: 422 `BED_TYPE_NOT_FOUND` / `BED_TYPE_INACTIVE`); a request that was valid stays valid when it is not changed, even if the bed type was switched off since. It does not touch availability. Availability is re-checked excluding the line itself. Surviving nights keep their snapshot.
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

**GET `{P}/arrivals?date`** (`reservation.read`)
- **Purpose:** CONFIRMED rooms arriving on a date (default: the business date), for the arrivals screen.
- **TX:** R

**POST `{P}/walk-ins`** ⓘ (`frontdesk.checkin` + `reservation.create`)
- **Purpose:** a walk-in.
- **Request:** `{ guest_id | new_guest{...}, room_id, rate_plan_id, departure_date, adult_count, child_count, nightly_overrides?, occupancy_reason?, accompanying_guest_ids? }` (`occupancy_reason` as on a reservation line: required on a complimentary or house use plan)
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
- **Rules:** processed through the engine, with a signed base. **An adjustment corrects what is already posted on the folio:** the charge code needs a posted net above zero on this folio (its charges, adjustments and reversals added up, so a reversed charge nets out), else 409 `ADJUSTMENT_NOTHING_POSTED` (post a charge instead); a credit cannot take that net below zero, else 409 `ADJUSTMENT_EXCEEDS_POSTED` with `context.posted`. An increase of what is posted is allowed. The check runs under the folio row lock, so two credits cannot both fit. A ROOM code is adjustable once room charges are posted on the folio.
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
- **Rules:** `amount ≤ refundable`. A refund leaves by a method the property allows (`properties.refund_methods`, CASH only by default, set in the property settings, migration 00035): another method is 422 `payment_method: NOT_ALLOWED`. Without `payment_method` the payment's own method is used when allowed, otherwise the first allowed method (cash). The cashier totals and the day close journal follow the refund's own method, so a bank transfer refunded in cash is booked to the cash account. The folio screen offers only the allowed methods.
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

## 16. Audit (implemented in M15: `internal/auditlog`; the tenant-level trail is `GET /api/v1/audit-logs`)

**GET `{P}/audit-logs?entity_type&entity_id&user_id&from&to`** (`audit.read`)
- **Response:** `[{ created_at, business_date, user, action, entity_type, entity_id, old_data, new_data }]`
- **Rules:** entries for tenant-level entities (guests, users) are available at `/audit-logs?…` to tenant admins.
- **TX:** R

### 14.1 Correction approval (decision: every correction, M9)

Applies to: `POST {P}/folios/{id}/adjustments`, `POST {P}/folio-items/{id}/reverse`, `POST {P}/payments/{id}/void` and `POST {P}/payments/{id}/refunds`.

- **Request block:** `approval: { email, password }`. It is required; a request without it is 422 `APPROVAL_REQUIRED`.
- **Who may approve:** any active user of the tenant who holds the new permission **`correction.approve`** at that property (tenant administrators pass every check). **The actor may approve their own correction**: they enter their own credentials again. A separate approver is optional.
- **Two separate checks:** the actor needs the operation's permission (`folio.adjust`, `folio.reverse`, `payment.void`, `payment.refund`), and the approver needs `correction.approve`. A user with both can do both.
- **Verification:** the approver's password is checked with the same argon2id verifier and the same rate limits as login, just before the request's transaction opens (no lock is held while the password is hashed). Wrong email or password gives 401 `APPROVAL_INVALID_CREDENTIALS` (one generic error, no hint which part was wrong). A valid approver without the permission gives 403 `APPROVAL_NOT_PERMITTED`. Both count toward the login rate limit. Verifying an approval never creates a session or token.
- **Never stored:** the password is not logged, not audited and not echoed back. Only the approver's user id is recorded.
- **Recorded:** `approved_by` (NOT NULL, added by the M9 migration) on the correcting row: REVERSAL and ADJUSTMENT `folio_items`, VOIDED payments and REFUND payments. The audit entry carries `actor` and `approved_by` (equal when self-approved).
- **Replays:** an `Idempotency-Key` replay returns the stored result and does not ask for approval again.
- **Correction routes by date:** same business date → void (payments) or reversal (charges); an earlier date → an ADJUSTMENT or a REFUND posted on the current business date. A void or reversal of an earlier date is 409 `CORRECTION_REQUIRES_ADJUSTMENT`. Nothing is ever back-posted.

## 17. Companies, groups and the city ledger (after M15)
Errors and conventions are as everywhere. Permissions: `company.manage`, `group.manage`, `cityledger.read`, `cityledger.transfer`, `cityledger.receive`.

| Method and path | Permission | Notes |
|---|---|---|
| `GET/POST {P}/companies`, `GET/PATCH {P}/companies/{id}` | read: `reservation.read` or `cityledger.read`; write: `company.manage` | `credit_limit` `null`/empty = no limit, `0` = no credit. Code unique (409 `CODE_TAKEN`). Deactivating a company that owes: 409 `COMPANY_HAS_BALANCE` |
| `GET/POST {P}/groups`, `GET/PATCH {P}/groups/{id}`, `GET {P}/groups/{id}/reservations` | read: `reservation.read`; write: `group.manage` | Dates and company are guarded by the group's reservations: 409 `GROUP_HAS_ROOMS_OUTSIDE_DATES`, `GROUP_HAS_RESERVATIONS` |
| `POST {P}/reservations` and `PATCH {P}/reservations/{id}` | as before | New fields `company_id` and `booking_group_id` (a value below 1 clears them on PATCH). 404 `COMPANY_NOT_FOUND`/`GROUP_NOT_FOUND`, 409 `COMPANY_INACTIVE`/`GROUP_INACTIVE`, 422 for a stay outside the group's dates or a company other than the group's. `GET {P}/reservations` filters by `company_id` and `booking_group_id` |
| `POST {P}/folios/{id}/city-ledger-transfers` | `cityledger.transfer` | Idempotency-Key. 409 `TRANSFER_EXCEEDS_BALANCE`, `CREDIT_LIMIT_EXCEEDED`, `COMPANY_INACTIVE`; returns a payment with method `CITY_LEDGER` |
| `GET {P}/city-ledger/accounts`, `.../accounts/{id}`, `/statement`, `/aging`, `/receipts` | `cityledger.read` | The balance is derived: transfers minus receipts |
| `POST {P}/city-ledger/accounts/{id}/receipts` | `cityledger.receive` | Idempotency-Key. 409 `RECEIPT_EXCEEDS_BALANCE`. Optional `allocations` `[{invoice_id, amount}]` pay invoices of the company: 404 `INVOICE_NOT_FOUND`, 409 `INVOICE_NOT_PAYABLE`, `ALLOCATION_EXCEEDS_INVOICE` |
| `POST {P}/city-ledger/receipts/{id}/void` | `cityledger.receive` + approval | Current business date only (409 `CORRECTION_REQUIRES_ADJUSTMENT`), 409 `RECEIPT_ALREADY_VOIDED` |
| `GET {P}/companies/{id}/statement.pdf` | `cityledger.read` | `from`, `to` optional |
| `GET {P}/city-ledger/accounts/{id}/invoice-candidates` | `cityledger.read` | Transfers not on a live invoice; `invoiceable` is true once the guest has checked out |
| `GET/POST {P}/city-ledger/accounts/{id}/invoices` | read: `cityledger.read`; issue: `cityledger.invoice` | POST `{payment_ids, notes}` with Idempotency-Key combines the transfers into one invoice. 409 `TRANSFER_NOT_AVAILABLE`, `STAY_NOT_CHECKED_OUT` (`context.payment_ids`) |
| `GET {P}/city-ledger/invoices/{id}` | `cityledger.read` | With its lines |
| `POST {P}/city-ledger/invoices/{id}/void` | `cityledger.invoice` + approval | Releases the transfers; 409 `INVOICE_ALREADY_VOIDED` |
| `GET {P}/city-ledger/invoices/{id}/invoice.pdf` | `cityledger.read` | A voided invoice is stamped VOID |

`CITY_LEDGER` appears as a payment method on payments (with `company_id`) but is refused by the payment, deposit and refund endpoints; a transfer is not refunded (409 `PAYMENT_NOT_REFUNDABLE`), and voiding one after receipts settled it is 409 `COMPANY_BALANCE_SETTLED`.

## 18. Housekeeping, continued (after M15)
Permissions: `housekeeping.update` (flags, start, finish, skip), `housekeeping.assign` (generate, assign, add a task, staff list), reads need access to the property.

| Method and path | Permission | Notes |
|---|---|---|
| `PUT {P}/rooms/{id}/housekeeping/flags` | `housekeeping.update` | `{priority, dnd, make_up_requested, note}` replaces the flags; does not change the status or its clock |
| `GET {P}/housekeeping?occupancy&flagged` | access | The board now carries `priority`, `dnd`, `make_up_requested`, `flag_note` |
| `GET {P}/housekeeping/staff` | `housekeeping.assign` | Who can be assigned |
| `GET {P}/housekeeping/tasks?date&status&assigned_to&unassigned&floor` | access | `{date, data, workload}`; high priority first |
| `POST {P}/housekeeping/tasks/generate` | `housekeeping.assign` | Adds the day's list; `{date, created}`; idempotent |
| `POST {P}/housekeeping/tasks` | `housekeeping.assign` | A manual task for the current business date |
| `POST {P}/housekeeping/tasks/assign` | `housekeeping.assign` | `{task_ids, user_id\|null}`; 409 `TASK_NOT_ASSIGNABLE` (`context.task_ids`), field error `ASSIGNEE_INVALID` |
| `POST {P}/housekeeping/tasks/{id}/start`, `/complete`, `/skip` | `housekeeping.update` | 409 `TASK_NOT_PENDING`, `TASK_ALREADY_CLOSED`; skip needs `{reason}` |
| `GET/POST {P}/maintenance-requests`, `GET/PATCH {P}/maintenance-requests/{id}` | read and report: `maintenance.report`; PATCH: `maintenance.manage` | Filters `status`, `open`, `room_id`, `assigned_to`, `category`, `priority`; cursor paging |
| `POST {P}/maintenance-requests/{id}/assign`, `/start`, `/resolve`, `/cancel`, `/reopen` | `maintenance.manage` | 409 `REQUEST_NOT_OPEN`, `REQUEST_NOT_RESOLVED`; cancel needs `note`; resolve and cancel take `release_block` |
| `POST {P}/maintenance-requests/{id}/block` | `maintenance.manage` + `room_block.manage` | `{block_type, start_date?, end_date}`; 409 `REQUEST_HAS_NO_ROOM`, `REQUEST_ALREADY_BLOCKED`, `ROOM_BLOCK_CONFLICT` |
| `GET {P}/maintenance-staff` | `maintenance.manage` | Who can take work |
| `GET/POST {P}/lost-found`, `GET/PATCH {P}/lost-found/{id}` | read and record: `lostfound.report`; PATCH: `lostfound.manage` | Filters `status`, `category`, `room_id`, `found_from`, `found_to`, `q` |
| `GET {P}/lost-found/{id}/possible-owners` | `lostfound.report` + `reservation.read` | Guests of the room around the day it was found |
| `POST {P}/lost-found/{id}/return`, `/dispose` | `lostfound.manage` | Final; 409 `ITEM_NOT_STORED`; return needs `claimant_name`, dispose needs `reason` |
| `GET {P}/dashboard` | `report.view` | The manager dashboard of the open business day: `today` (live summary), `movements`, `rooms` (housekeeping), `trend` (last 14 closed days), `month_to_date` and `previous_month`, `forecast` (next 14 nights). JSON only |
| `GET {P}/reports/free-rooms`, `/free-rooms-by-reason`, `/free-rooms-by-kind` | `report.view` | JSON or `?format=csv`, with `from` and `to`. The complimentary and house use rooms with nights in the range, valued at the grid price of the reference rate plan (`is_reference`, set by PATCH of a paid plan; one per property): one row per room, per reason (spellings that differ in case or spacing are one reason) or per kind. Without a reference plan the nights are counted and the value is 0. |
| `GET {P}/reports/housekeeping-productivity`, `/housekeeping-dirty-rooms`, `/maintenance` | `report.view` | JSON or `?format=csv`; the first and the last take `from` and `to`, the second `min_hours` |

## 19. Accounting (after M15)
Permissions: `accounting.view` (read), `accounting.manage` (chart and system accounts), `accounting.post` (manual journals), `accounting.close` (periods).

| Method and path | Permission | Notes |
|---|---|---|
| `GET {P}/accounting/accounts` | `accounting.view` | Filters `account_type`, `statement_group`, `active`, `postable`, `q`; `format=csv` exports the chart |
| `POST {P}/accounting/accounts` | `accounting.manage` | 409 `CODE_TAKEN`; field errors for the group, parent and code; `department_requirement` (NONE, OPTIONAL, REQUIRED) and `default_department_id` (422 `DEPARTMENT_NOT_ALLOWED` on a NONE account) |
| `GET/PATCH/DELETE {P}/accounting/accounts/{id}` | read: `accounting.view`; write: `accounting.manage` | 409 `ACCOUNT_IN_USE`, `ACCOUNT_HAS_CHILDREN` |
| `POST {P}/accounting/accounts/import` | `accounting.manage` | `{csv, dry_run}`; all or nothing; row errors as `rows[N].field` |
| `GET/PUT {P}/accounting/account-map` | read: `accounting.view`; write: `accounting.manage` | The ten system keys; PUT `{entries: [{map_key, account_id}]}` is all or nothing |
| `GET {P}/accounting/department-setup` | `accounting.view` | `ok`, `issues` (`severity` ERROR or WARNING, `code`, `source_type`, `source_ref`, `account_code`, `message`): what a REQUIRED account cannot find a department for; `PATCH accounts/{id}` (rule), `PUT account-map`, charge codes, taxes and service charges are refused with 409 `DEPARTMENT_SETUP_INCOMPLETE` or 422 `DEPARTMENT_REQUIRED` when they would create such a gap |
| `GET {P}/accounting/unmapped` | `accounting.view` | Charge codes, taxes and service charges whose account code the journals cannot use, and where they are posted instead |

### 19.1 Journals and periods
| Method and path | Permission | Notes |
|---|---|---|
| `GET {P}/accounting/journals` | `accounting.view` | Filters `from`, `to`, `type` (DAY_CLOSE, MANUAL, REVERSAL), `account_id`, `q`, `limit` (at most 200); newest first, without lines |
| `GET {P}/accounting/journals/{id}` | `accounting.view` | With its lines; day close lines carry `source_type` (CHARGE_CODE, TAX, SERVICE_CHARGE, PAYMENT, RECEIPT, DEPOSIT_RELEASE) and `source_ref` |
| `POST {P}/accounting/journals` | `accounting.post` | Manual journal; `Idempotency-Key` required; 422 field errors per line (`lines[N].account_id`: NOT_FOUND, NOT_POSTABLE, INACTIVE, CONTROL_ACCOUNT; `lines`: UNBALANCED, INVALID_COUNT); 409 `PERIOD_CLOSED` |
| `POST {P}/accounting/journals/{id}/reverse` | `accounting.post` + approval | `{journal_date?, reason, approval}`; 409 `JOURNAL_NOT_REVERSIBLE` (day close or reversal), `JOURNAL_ALREADY_REVERSED` |
| `POST {P}/accounting/journals/post-pending` | `accounting.close` | Journals closed days that have none; `{posted}` |
| `GET {P}/accounting/periods` | `accounting.view` | Months from the start date to today, newest first, with `posted_days`, `closable`, `reopenable` |
| `POST {P}/accounting/periods/{start}/close` | `accounting.close` | `{start}` is the first day of the month; 409 `PERIOD_NOT_READY` (context `days`, `posted_days`), `PERIOD_ALREADY_CLOSED` |
| `POST {P}/accounting/periods/{start}/reopen` | `accounting.close` | `{reason}`; only the latest closed month: 409 `PERIOD_NOT_LATEST`, `PERIOD_NOT_CLOSED` |

### 19.2 Reports
All need `accounting.view`; `from` and `to` default to the current business month so far and the current business date, `as_of` to the current business date (not later: 422). A range is at most 5 years.

| Method and path | Notes |
|---|---|
| `GET {P}/accounting/trial-balance` | `from`, `to`; rows per account with opening, movement and closing on their sides, and totals; `format=csv` |
| `GET {P}/accounting/accounts/{id}/ledger` | `from`, `to`; opening balance, lines with running balance on the normal side, totals, `truncated`; `format=csv` |
| `GET {P}/accounting/income-statement` | `from`, `to`; lines of kind HEADING, GROUP (with its accounts), SUBTOTAL and TOTAL in USALI order (keys include TOTAL_REVENUE, DEPT_PROFIT, GOP, EBITDA, EBIT, NET_INCOME), `net_income`; `format=csv` |
| `GET {P}/accounting/balance-sheet` | `as_of`; the same line structure, totals and `difference`; `format=csv` |
| `GET {P}/accounting/reconciliation` | `as_of`; `controls` (ledger, source, difference), `pending_days`, `includes_open_day`, `reconciled` |

PDF versions (the documents of §15, `accounting.view`, inline and never cached): `GET {P}/accounting/trial-balance.pdf`, `GET {P}/accounting/accounts/{id}/ledger.pdf` and `GET {P}/accounting/income-statement.pdf` take `from` and `to`; `GET {P}/accounting/balance-sheet.pdf` takes `as_of`.

### 19.3 Fiscal years
| Method and path | Permission | Notes |
|---|---|---|
| `GET {P}/accounting/fiscal-years` | `accounting.view` | Newest first; `net_income` (closing journals left out), `months`, `closed_months`, `closable`, `reopenable`, `closing_journal_number` |
| `POST {P}/accounting/fiscal-years/{start}/close` | `accounting.close` | `{start}` is the first day of the year; 409 `FISCAL_YEAR_NOT_READY` (context `months`, `closed_months`), `FISCAL_YEAR_ALREADY_CLOSED`, `ACCOUNT_MAP_INCOMPLETE`; 404 `FISCAL_YEAR_NOT_FOUND` |
| `POST {P}/accounting/fiscal-years/{start}/reopen` | `accounting.close` + approval | `{reason, approval}`; 409 `FISCAL_YEAR_NOT_LATEST`, `FISCAL_YEAR_NOT_CLOSED`. Reopening a month of a closed year: 409 `PERIOD_IN_CLOSED_YEAR` |

The journal type `CLOSING` appears in the journal list; the system account map has 11 keys (`RETAINED_EARNINGS` is the new one).

## 20. Payables (after M15)
Permissions: `payables.view` (read), `payables.manage` (suppliers), `payables.post` (bills and payments; a void also needs an approval).

| Method and path | Permission | Notes |
|---|---|---|
| `GET {P}/payables/suppliers` | `payables.view` | Filters `active`, `q`; each supplier carries `outstanding` |
| `POST {P}/payables/suppliers` | `payables.manage` | 409 `CODE_TAKEN`; field errors `code`, `name`, `email`, `payment_terms_days`, `default_account_id` |
| `GET/PATCH {P}/payables/suppliers/{id}` | read: `payables.view`; write: `payables.manage` | The code never changes |
| `GET {P}/payables/suppliers/{id}/open-bills` | `payables.view` | What is still owed on each bill, oldest due date first |
| `GET {P}/payables/bills` | `payables.view` | Filters `supplier_id`, `status`, `from`, `to`, `q`, `open_only`, `limit` |
| `POST {P}/payables/bills` | `payables.post` | `Idempotency-Key` required; 409 `DUPLICATE_INVOICE`, `SUPPLIER_INACTIVE`, `PERIOD_CLOSED`; field errors per line (`lines[N].account_id`: NOT_FOUND, NOT_POSTABLE, INACTIVE, CONTROL_ACCOUNT; `lines[N].amount`; `lines[N].vat_amount`: NEGATIVE, TOO_PRECISE). A line may carry `vat_amount` (the VAT paid on it, on top of `amount`): the PKP status of the property on the `bill_date` decides how it is booked (`CREDITABLE`/`DEFERRED` on the INPUT_VAT account, `EXPENSE` added to the cost), frozen on the line as `vat_treatment`; the `total` is the amounts plus their VAT |
| `GET {P}/payables/bills/{id}` | `payables.view` | With its lines |
| `POST {P}/payables/bills/{id}/void` | `payables.post` + approval | `{reason, approval}`; 409 `BILL_HAS_PAYMENTS`, `BILL_HAS_CREDIT_NOTES`, `BILL_ALREADY_VOIDED` |
| `GET {P}/payables/credit-notes` | `payables.view` | Filters `supplier_id`, `bill_id`, `status`, `from`, `to`, `q`, `unapplied_only`, `limit` |
| `POST {P}/payables/credit-notes` | `payables.post` | `Idempotency-Key`; `{bill_id, supplier_credit_number, credit_date, reason, lines: [{bill_line_no, amount, vat_amount, description}]}`; 409 `DUPLICATE_CREDIT_NOTE`, `BILL_ALREADY_VOIDED`; 422 `EXCEEDS_BILL_LINE`, `DEPARTMENT_REQUIRED` |
| `GET {P}/payables/credit-notes/{id}` | `payables.view` | With `lines` and `allocations` (the bills it was taken off); 404 `CREDIT_NOTE_NOT_FOUND` |
| `POST {P}/payables/credit-notes/{id}/apply` | `payables.post` | `{allocations: [{bill_id, amount}]}`; no journal; 409 `ALLOCATION_EXCEEDS_OUTSTANDING`, `CREDIT_EXCEEDS_UNAPPLIED`, `CREDIT_NOTE_VOIDED` |
| `POST {P}/payables/credit-notes/{id}/void` | `payables.post` + approval | `{reason, approval}`; 409 `CREDIT_NOTE_ALREADY_VOIDED` |
| `GET/POST {P}/payables/payments` | read: `payables.view`; post: `payables.post` | POST needs `Idempotency-Key`; body `{supplier_id, payment_date, payment_method (CASH, BANK_TRANSFER, OTHER), allocations: [{bill_id, amount}]}`; 409 `ALLOCATION_EXCEEDS_OUTSTANDING` with one field error per allocation |
| `GET {P}/payables/payments/{id}` | `payables.view` | With the bills it settles |
| `POST {P}/payables/payments/{id}/void` | `payables.post` + approval | `{reason, approval}`; 409 `PAYMENT_ALREADY_VOIDED` |
| `GET {P}/payables/aging` | `payables.view` | `as_of` (not after the business date); buckets CURRENT, DAYS_1_30, DAYS_31_60, DAYS_61_90, DAYS_OVER_90 per supplier and in total; `unapplied_credit`, `credits` and `net` for the credit notes no bill has taken off |

The journal type `PAYABLES` appears in the journal list; the system account map has 12 keys (`ACCOUNTS_PAYABLE` is the new one) and the reconciliation a fourth control.

## 21. Bank reconciliation (after M15)
Permissions: `bank.view` (read), `bank.manage` (register bank accounts), `bank.reconcile` (import, match, post, reconcile; reopening also needs an approval).

| Method and path | Permission | Notes |
|---|---|---|
| `GET/POST {P}/bank/accounts` | read: `bank.view`; write: `bank.manage` | POST `{account_id, name, account_number?, is_active?}`; 409 `BANK_ACCOUNT_EXISTS`; each carries `book_balance`, `reconciled_to`, `open_statements` |
| `GET/PATCH {P}/bank/accounts/{id}` | read: `bank.view`; write: `bank.manage` | The account of the books never changes |
| `GET {P}/bank/statements` | `bank.view` | Filters `bank_account_id`, `status` |
| `POST {P}/bank/statements` | `bank.reconcile` | `{bank_account_id, period_from, period_to, opening_balance, closing_balance, note?, csv}`; 422 with one field error per row (`rows[N].field`), `closing_balance: DOES_NOT_ADD_UP`, `opening_balance: NOT_CONTINUOUS`; 409 `STATEMENT_OVERLAPS`, `STATEMENT_OUT_OF_ORDER`, `BANK_ACCOUNT_INACTIVE` |
| `GET/DELETE {P}/bank/statements/{id}` | read: `bank.view`; delete: `bank.reconcile` | The detail has `lines` (with `cleared`, `matched`, `clearings`), `clearings` and the `summary` (book balance, uncleared in/out, adjusted bank, difference, `blockers`, `can_reconcile`); delete only while open |
| `GET {P}/bank/statements/{id}/uncleared` | `bank.view` | The journal lines of the account up to the end of the statement that are not cleared in full, with `cleared` and `remaining` |
| `POST {P}/bank/statements/{id}/clearings` | `bank.reconcile` | `{statement_line_id?, journal_line_ids}` (each cleared for what is left of it) or `{allocations: [{statement_line_id?, journal_line_id, amount?}]}` (parts); 409 `ALREADY_CLEARED`, `STATEMENT_RECONCILED`; 422 `EXCEEDS_LINE`, `EXCEEDS_STATEMENT_LINE`, `WRONG_SIDE`; without a statement line only offsetting or opening lines, in full |
| `DELETE {P}/bank/statements/{id}/clearings/{clearingId}` | `bank.reconcile` | Undo a matching |
| `POST {P}/bank/statements/{id}/auto-match` | `bank.reconcile` | `{matched, remaining}` |
| `POST {P}/bank/statements/{id}/lines/{lineId}/adjust` | `bank.reconcile` | `{account_id, description?}`; posts a BANK journal; 409 `LINE_ALREADY_MATCHED`, `PERIOD_CLOSED` |
| `GET {P}/bank/statements/{id}/settlement-lines` | `bank.view` | `account_key` CARD or OTHER_PAYMENT: the payment lines waiting for their settlement |
| `POST {P}/bank/statements/{id}/lines/{lineId}/settle` | `bank.reconcile` | `{account_key, journal_line_ids, fee_account_id?, description?}`; posts a BANK journal (bank net, commission, clearing gross); 409 `ALREADY_SETTLED`, `LINE_ALREADY_MATCHED`; 422 `NET_EXCEEDS_GROSS`, `NOT_MONEY_IN`; `vat_amount` (optional: the final VAT in what the bank kept, between 0 and the deduction, else the proposal is used; 422 `VAT_EXCEEDS_DEDUCTION`); the MDR booked is the deduction less the VAT, and the VAT treatment of the day is frozen |
| `GET {P}/bank/card-fee-rules` | `bank.view` | The fee (MDR) rules of card and e-wallet payments: method, percent, days to pay out, the date it starts; by method, newest first |
| `POST {P}/bank/card-fee-rules` | `bank.manage` | `{payment_method CARD|OTHER, mdr_rate (0-100, 4 decimals), vat_rate? (0-100, 4 decimals, default 0: the VAT the acquirer charges on the MDR), settlement_days (0-60), effective_from}`; never changed afterwards; 409 `FEE_RULE_EXISTS`; a payment keeps `mdr_rate`, `mdr_fee` and `expected_settlement_date` of the rule of its business date |
| `GET {P}/bank/card-settlements/expected` | `bank.view` | `?account_key CARD|OTHER_PAYMENT`: the unsettled payment lines with `expected_mdr`, `expected_vat` (`vat_rate`, `without_vat_rate`), `expected_deduction`, `expected_net` (the amount less the deduction), payout and due date, the late ones and the ones without a rate; the totals add `expected_vat`, `expected_deduction` and `without_vat_rate` (an MDR snapshot with no VAT rate: it expects no VAT) |
| `POST {P}/bank/statements/{id}/lines/{lineId}/settlement-preview` | `bank.view` | `{account_key, journal_line_ids}`; writes nothing: `gross`, `net`, `deduction`, `expected_mdr`, `expected_vat`, `proposed_vat`, `proposed_mdr`, `mdr_rate`, `vat_rate`, `without_vat_rate`, `without_rate`, `vat_treatment` (what would be frozen on the date of the line), `input_vat_account`; 422 `NET_EXCEEDS_GROSS`, `NOT_MONEY_IN` |
| `GET {P}/bank/card-settlements` | `bank.view` | The settlements made, newest first: `fee` (what the bank kept) = `mdr_amount` + `vat_amount`, `vat_treatment`, `expected_mdr`, `expected_vat`, `proposed_vat`, `mdr_rate`, `vat_rate`, `payments_without_vat_rate`, `mdr_variance` and `vat_variance` (null when a payment had no snapshot) |
| `GET {P}/bank/statements/{id}/lines/{lineId}/settlement-proposal` | `bank.view` | `?account_key`: the oldest-due payments whose expected net is closest to the line, `matched` within a currency unit or 0.5% of the line; only a choice of lines |
| `POST {P}/bank/statements/{id}/reconcile` | `bank.reconcile` | 409 `STATEMENT_NOT_READY` (context `blockers`) |
| `POST {P}/bank/statements/{id}/reopen` | `bank.reconcile` + approval | `{reason, approval}`; 409 `STATEMENT_NOT_LATEST`, `STATEMENT_NOT_RECONCILED` |

The journal type `BANK` appears in the journal list.

## 22. Tax filing (after M15)
Permissions: `tax.view` (read), `tax.manage` (filing profiles), `tax.file` (file returns and pay the authority; voiding one also needs an approval).

| Method and path | Permission | Notes |
|---|---|---|
| `GET {P}/city-ledger/accounts/{id}/adjustments` | `cityledger.read` | The credit notes and write-offs of a company, newest first, with the lines of the credit notes |
| `POST {P}/city-ledger/credit-notes` | `cityledger.credit_note` + approval | `Idempotency-Key` required; `{invoice_id | payment_id, reason, lines: [{description, account_id (revenue), net_amount, tax_id?}], approval}`: against an issued invoice (409 `ADJUSTMENT_EXCEEDS_INVOICE`) or a transfer not on an invoice (409 `ADJUSTMENT_EXCEEDS_TRANSFER`, `TRANSFER_ON_INVOICE`); journal Dr allowance and tax payable, Cr CITY_LEDGER on the business date; the invoice later made from a transfer asks the net |
| `POST {P}/city-ledger/write-offs` | `cityledger.write_off` + approval | `Idempotency-Key` required; `{invoice_id, amount, account_id (expense or 1240), reason, approval}`; 409 `ADJUSTMENT_EXCEEDS_INVOICE`; no tax effect |
| `GET {P}/city-ledger/overdue` | `cityledger.read` | The overdue invoices by company: days overdue, bucket, owed, interest, last reminder, `next_level` of the company, and the late fee in use |
| `GET {P}/city-ledger/settings/late-fee` | `cityledger.read` | `{monthly_rate, grace_days}`; a rate of 0 is off |
| `PUT {P}/city-ledger/settings/late-fee` | `cityledger.reminder` | `{monthly_rate (0-100, 4 decimals), grace_days (0-365)}`; only changes the interest shown |
| `GET {P}/city-ledger/accounts/{id}/reminders` | `cityledger.read` | The reminders sent to a company, newest first, with their items |
| `POST {P}/city-ledger/accounts/{id}/reminders` | `cityledger.reminder` | `Idempotency-Key` required; `{level 1-3, note?, invoice_ids?}` (every overdue invoice by default); freezes what each owes today; 409 `NO_OVERDUE_INVOICES`, `INVOICE_NOT_OVERDUE`; no journal |
| `GET {P}/city-ledger/reminders/{id}` | `cityledger.read` | One reminder; 404 `REMINDER_NOT_FOUND` |
| `GET {P}/city-ledger/reminders/{id}/reminder.pdf` | `cityledger.read` | The letter (First, Second or Final reminder), the interest column only when there is interest |
| `GET {P}/accounting/cash-flow` | `accounting.view` | `from`, `to`, `method=INDIRECT|DIRECT`, `format=csv`, `lang`; `method`, `lines` (statement lines), `net_income`, `operating`, `investing`, `financing`, `net_change`, `opening_cash`, `closing_cash`, `difference`, `reconciled`; `cash-flow.pdf` is the same as a PDF |
| `GET {P}/accounting/department-report` | `accounting.view` | `from`, `to`, `department_id`, `format=csv`, `lang`; `departments` (a tree: `revenue`, `expense`, `profit`, `own_revenue`, `own_expense`, `accounts`, `children`), `unassigned`, `totals`; `department-report.pdf` is the same as a PDF |
| `GET {P}/departments` | `accounting.view` | `active`; each department followed by its sub-departments, with `level`, `child_count`, `in_use` |
| `POST {P}/departments` | `accounting.manage` | `{code, name, parent_id?, sort_order?}`; 409 `CODE_TAKEN`; 422 `parent_id` `TOO_DEEP` or `INACTIVE` |
| `GET {P}/departments/{id}` | `accounting.view` | 404 `DEPARTMENT_NOT_FOUND` |
| `PATCH {P}/departments/{id}` | `accounting.manage` | `{name?, sort_order?, is_active?}`; the code and the parent never change; 409 `DEPARTMENT_HAS_ACTIVE_CHILDREN`, `DEPARTMENT_PARENT_INACTIVE` |
| `DELETE {P}/departments/{id}` | `accounting.manage` | 409 `DEPARTMENT_IN_USE` (sub-departments, or anything that points to it) |
| `GET {P}/budgets` | `budget.view` | `year_start`, `status`; the summaries (no grid), latest year and version first |
| `POST {P}/budgets` | `budget.manage` | `{year_start?, name, description?, copy_from_id?}`: a draft, empty or a copy (which keeps its year); the version is the next of the year; 201 with the grid; 409 `BUDGET_VERSION_TAKEN` (try again) |
| `GET {P}/budgets/{id}` | `budget.view` | With `months`, `rows` (twelve amounts and a total per account) and `available_accounts`; 404 `BUDGET_NOT_FOUND` |
| `PATCH {P}/budgets/{id}` | `budget.manage` | `{name, description?}` of a draft; 409 `BUDGET_NOT_DRAFT` |
| `DELETE {P}/budgets/{id}` | `budget.manage` | A draft with its figures; 409 `BUDGET_NOT_DRAFT`, `BUDGET_IN_USE` (a version was copied from it) |
| `PUT {P}/budgets/{id}/grid` | `budget.manage` | `{rows: [{account_id, amounts: [12]}]}` replaces the figures of a draft; an empty amount is zero; 422 `rows[N].account_id` `INVALID_ACCOUNT` |
| `POST {P}/budgets/{id}/spread` | `budget.manage` | `{account_id, total, method EQUAL|LAST_YEAR}`; 422 `method` `NO_PATTERN` |
| `POST {P}/budgets/{id}/fill-from-actuals` | `budget.manage` | `{source_year_start?, percent_change?, replace?}`; 409 `NO_ACTUALS` |
| `POST {P}/budgets/{id}/activate` | `budget.manage` + approval of `budget.approve` | `{approval}`; the version that was active is archived; 409 `BUDGET_EMPTY`, `BUDGET_NOT_DRAFT` |
| `GET {P}/budgets/{id}/export` | `budget.view` | CSV: `code,name,m1..m12` |
| `POST {P}/budgets/{id}/import` | `budget.manage` | `{csv, dry_run?}`; all or nothing, 422 with a field error per row (`rows[N].field`) |
| `PUT {P}/budgets/{id}/statistics` | `budget.manage` | `{rows: [{month, rooms_available, rooms_sold, adr}]}` replaces the statistics of a draft; 422 `rows[N].rooms_sold`; 409 `BUDGET_NOT_DRAFT` |
| `GET {P}/budgets/statistics-vs-actual` | `budget.view` | `year_start`, `from`, `to`, `budget_id`; `metrics` (rooms_available, rooms_sold, occupancy, adr, revpar, room_revenue with `period` and `ytd` cells), `closed_days`, `room_revenue_check` |
| `GET {P}/budgets/department-vs-actual` | `budget.view` | `year_start`, `from`, `to`, `budget_id`, `department_id`; `departments` (a tree: `revenue`, `expense`, `profit` each with `period` and `ytd` cells, `children`), `unassigned`, `totals`; 404 `DEPARTMENT_NOT_FOUND`, `NO_ACTIVE_BUDGET` |
| `GET {P}/budgets/vs-actual` | `budget.view` | `year_start`, `from`, `to` (snapped to whole months of the year), `budget_id` (the active version by default), `format=csv`, `lang`; the income statement layout with `period` and `ytd` cells (`actual`, `budget`, `variance`, `variance_percent`, `favourable`); 404 `NO_ACTIVE_BUDGET` |
| `GET {P}/budgets/vs-actual.pdf` | `budget.view` | The same as a PDF |
| `GET {P}/cashier/settings` | any cashier permission | `{require_shift_for_cash, max_variance, block_night_audit}` |
| `PUT {P}/cashier/settings` | `cashier.settings` | The same body; `max_variance` 0 means every difference needs an approval |
| `GET {P}/cashier/shifts` | `cashier.shift_manage` (all) or `cashier.shift` (own) | `status`, `user_id`, `from`, `to`, `limit`, `cursor`; newest first |
| `POST {P}/cashier/shifts` | `cashier.shift` | `{drawer?, opening_float?}`; 409 `SHIFT_ALREADY_OPEN`, `DRAWER_IN_USE`; the float defaults to what the last shift of the drawer left |
| `GET {P}/cashier/shifts/current` | `cashier.shift` | `{shift}`: the caller's open shift with its cash so far, or null |
| `GET {P}/cashier/shifts/suggested-float` | `cashier.shift` | `?drawer`; what the last shift of the drawer left |
| `GET {P}/cashier/shifts/{id}` | the owner, or `cashier.shift_manage` | The shift with `cash` (float, payments, refunds, receipts, voided_after_close, pay-ins, pay-outs, drops, expected), movements and counts |
| `POST {P}/cashier/shifts/{id}/movements` | the owner, or `cashier.shift_manage` | `Idempotency-Key` required; `{kind DROP|PAY_IN|PAY_OUT, amount, account_id (pay-in and pay-out), reason}`; pay-in and pay-out are journaled (type CASHIER), a drop is not; 409 `SHIFT_NOT_OPEN` |
| `POST {P}/cashier/shifts/{id}/close` | the owner, or `cashier.shift_manage` | `{counted_cash, counts?, reason?, approval?, hand_over_to?}`; a difference beyond `max_variance` needs `reason` and an `approval` of `cashier.shift_approve` (422 `APPROVAL_REQUIRED`) and is journaled against the cash over and short account; 409 `SHIFT_NOT_OPEN` |
Cash payments, cash refunds and cash city ledger receipts need the caller's open shift when `require_shift_for_cash` is on (409 `NO_OPEN_SHIFT`); the night audit preview and run list open shifts in `blockers.open_shifts` when `block_night_audit` is on.
| `GET {P}/city-ledger/adjustments/{id}` | `cityledger.read` | One credit note or write-off |
| `POST {P}/city-ledger/adjustments/{id}/void` | the permission of its kind + approval | `{reason, approval}`: reverses its journal on the business date; 409 `ADJUSTMENT_ALREADY_VOIDED`, `ADJUSTMENT_ON_INVOICE` |
| `GET {P}/city-ledger/adjustments/{id}/credit-note.pdf` | `cityledger.read` | The credit note as a document (a write-off has none: 409 `NOT_A_CREDIT_NOTE`) |
| `GET/POST {P}/tax/invoices` | read: `tax.view`; issue: `tax.invoice` | Tax invoices (faktur pajak) of a PKP property. GET filters `status`, `from`, `to`, `q`, `limit` (no lines). POST `{source_type: CITY_LEDGER_INVOICE or FOLIO, city_ledger_invoice_id | folio_id, buyer? (a folio), replaces_invoice_id?}` with `Idempotency-Key`: dated the business date; 409 `TAX_INVOICE_NOT_READY` (context `blockers`: NOT_PKP, SOURCE_NOT_ISSUED, SOURCE_NOT_CLOSED, BUYER_*, NO_VAT, NO_BASE), `TAX_INVOICE_EXISTS`, `TAX_INVOICE_REPLACE_INVALID`, `TAX_INVOICE_ALREADY_REPLACED` |
| `GET {P}/tax/invoices/preview?source_type=&id=` | `tax.invoice` | What the invoice would say and what stops it; nothing is written (for a folio `buyer_name`, `buyer_npwp`, `buyer_address`) |
| `GET {P}/tax/invoices/coverage?from=&to=` | `tax.view` | VAT collected against VAT on live invoices, and the folios with VAT and no invoice (information only) |
| `GET/POST {P}/tax/invoices/exports` | read: `tax.view`; write: `tax.invoice` | POST `{from, to}` answers the CSV (one row per invoice line, void invoices with their status), the batch in the headers `X-Export-Id`, `X-Export-Sha256`, `X-Export-Invoices`; 409 `TAX_INVOICE_EXPORT_EMPTY` |
| `GET {P}/tax/invoices/{id}` | `tax.view` | With its lines |
| `POST {P}/tax/invoices/{id}/void` | `tax.invoice` | `{reason, approval}`; 409 `TAX_INVOICE_ALREADY_VOIDED` |
| `PUT {P}/tax/invoices/{id}/djp-number` | `tax.invoice` | `{number}`: the official number of the tax authority, once; 409 `TAX_INVOICE_NUMBER_SET`, `TAX_INVOICE_NUMBER_TAKEN` |
| `GET {P}/tax/invoices/{id}/invoice.pdf` | `tax.view` | The invoice as issued |
| `GET/POST {P}/tax/settings` | read: `tax.view`; write: `tax.manage` | The PKP status of the property: GET gives `{current, history}` (the status in force on the business date and every change, newest first). POST adds a change `{effective_from, is_pkp, npwp?, pkp_number?, pkp_confirmed_on?, input_vat_treatment?, signer_name?, signer_title?, approval?}`; the history only moves forward (409 `TAX_SETTINGS_NOT_NEWER`), a change that begins before the business date needs an `approval` (422 `APPROVAL_REQUIRED`), a property that is not PKP cannot use `CREDITABLE` and a PKP property needs its `npwp` (422 `VALIDATION_FAILED`) |
| `GET/POST {P}/tax/profiles` | read: `tax.view`; write: `tax.manage` | POST `{tax_id, authority, registration_number?, due_day?, is_active?, claims_input_vat?}`; `claims_input_vat` marks the VAT tax whose returns claim the input VAT of the bills (one per property: 409 `TAX_CLAIMS_TAKEN`; only a tax of kind VAT: 422 `NOT_VAT`; taking it off while claims are live: 409 `TAX_CLAIMS_IN_USE`); 409 `TAX_PROFILE_EXISTS` |
| `POST {P}/tax/profiles/{id}/opening-credit` | `tax.manage` | `{as_of, amount, approval}`: the VAT credit the hotel starts with (Dr INPUT_VAT, Cr 3900). Only a VAT tax, before its first return, once: 409 `TAX_OPENING_CREDIT_LOCKED`, `TAX_OPENING_CREDIT_EXISTS`, `OPENING_EQUITY_ACCOUNT_MISSING` |
| `POST {P}/tax/profiles/{id}/opening-credit/void` | `tax.manage` | `{reason, approval}`: reverses its journal; only before the first return: 409 `TAX_OPENING_CREDIT_LOCKED`; 404 `TAX_OPENING_CREDIT_NOT_FOUND` |
| `GET/PATCH {P}/tax/profiles/{id}` | read: `tax.view`; write: `tax.manage` | The tax never changes |
| `GET {P}/tax/periods?tax_id=` | `tax.view` | The months from the start of the books: `tax_amount`, `status` (OPEN, READY, FILED), `paid`, `outstanding`, `overdue` |
| `GET {P}/tax/worksheet?tax_id=&period=` | `tax.view` | `period` is the first day of the month; lines, totals, `gl_collected`, `difference`, `ready`, `blockers`, and the `return` filed for it |
| `GET/POST {P}/tax/returns` | read: `tax.view`; file: `tax.file` | POST `{tax_id, period_start, filed_on?, filing_reference?, notes?}` with `Idempotency-Key`; 409 `TAX_MONTH_NOT_READY` (context `blockers`), `TAX_RETURN_EXISTS`, `TAX_PREVIOUS_NOT_FILED`, `TAX_PROFILE_INACTIVE` |
| `GET {P}/tax/returns/{id}` | `tax.view` | With worksheet lines and payments |
| `POST {P}/tax/returns/{id}/void` | `tax.file` + approval | `{reason, approval}`; 409 `TAX_RETURN_HAS_PAYMENTS`, `TAX_RETURN_ALREADY_VOIDED` |
| `POST {P}/tax/returns/{id}/payments` | `tax.file` | `{payment_date, amount, penalty?, penalty_account_id?, payment_method, reference_number?, remarks?}` with `Idempotency-Key`; 422 `amount: EXCEEDS_OUTSTANDING`, `penalty_account_id: REQUIRED`; 409 `TAX_RETURN_VOIDED`, `PERIOD_CLOSED` |
| `GET {P}/tax/payments`, `GET {P}/tax/payments/{id}` | `tax.view` | Filters `return_id`, `status` |
| `POST {P}/tax/payments/{id}/void` | `tax.file` + approval | `{reason, approval}`; 409 `TAX_PAYMENT_ALREADY_VOIDED` |
| `GET {P}/tax/liability` | `tax.view` | `as_of` (not after the business date); per tax and per tax payable account |
| `GET {P}/tax/returns/{id}/return.pdf`, `GET {P}/tax/worksheet.pdf?tax_id=&period=` | `tax.view` | PDF documents (§15 style); a filed month answers the return |

The journal type `TAX` appears in the journal list.

## 23. Yield management (after M15)
Permissions: `rate.manage` changes rules; reading rules and quotes needs only access to the property.

| Method and path | Permission | Notes |
|---|---|---|
| `GET {P}/yield-rules` | property access | Filter `active`; the rules in the order they apply |
| `POST {P}/yield-rules` | `rate.manage` | `{code, name, rate_plan_id?, room_type_id?, stay_from?, stay_to?, weekdays?, occupancy_from?, occupancy_to?, lead_days_min?, lead_days_max?, stay_nights_min?, stay_nights_max?, adjustment_type (PERCENT, AMOUNT), adjustment_value, floor_amount?, cap_amount?, priority?, is_active?}`; 409 `CODE_TAKEN`, 404 `RATE_PLAN_NOT_FOUND`, `ROOM_TYPE_NOT_FOUND`, 422 field errors |
| `GET/PUT/DELETE {P}/yield-rules/{id}` | read: property access; write: `rate.manage` | PUT replaces the whole rule, the code cannot change; 404 `YIELD_RULE_NOT_FOUND` |
| `GET {P}/rate-quotes?rate_plan_id&room_type_id&arrival_date&departure_date` | property access | Per night: `grid_rate`, `occupancy_percent`, `steps` (code, before, after), `amount`; `total`, `grid_total`, `missing_nights` |

Reservation nights (`nightly_rates[]`) carry `grid_rate` and `yield_rules`; `base_rate` is the price the night was sold at.
