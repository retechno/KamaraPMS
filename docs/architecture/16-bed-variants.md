# 16. Bed variants: a bed type as a sellable variant of a room type

Status: **design approved; building in six steps (step 1 done: the bed type of a room is required, `bed_locked` on a line without effect on stock, the supplement table with `GET/POST /rate-plans/{id}/bed-adjustments`; step 2 done: the engine with bed lines, `internal/availability/bed.go`; step 3 done: the reservation rules; step 4 done: the price; step 5 done: search and calendar; step 6 done: the screens).** The feature is complete. It answers the backlog item "bed type as a sellable variant with its own stock or rate" (`08-backlog.md`). Bed counts per room type (1 King or 2 Twin) stay out.

Decisions of the owner (2026-10-06):

1. **A variant is the bed type of the room** (`rooms.bed_type_id`, the catalogue of `bed_types`): a room type is sold as "Deluxe King" or "Deluxe Twin" according to the rooms of the type that have that bed. No bed counts.
2. **Price**: the room type keeps its price in the rate grid; a variant may add a **fixed amount or a percentage per night, per rate plan** (nothing = the same price). The rate grid is not multiplied.
3. **Stock**: availability is counted **per room type and per variant**. A reservation line asks for a variant, **locked** (it takes a room with that bed and uses the stock of the variant), or for **no preference** (it takes any room of the type and uses the stock of the type only).
4. **In the configuration of a room, the room type and the bed type are both required.** A room cannot be saved without a bed type, so every room belongs to exactly one variant. (The request of a reservation line stays optional: "no preference" is a choice.)

## Today (read from the code)

- Availability is `available(T, n) = sellable(T, n) - demand(T, n)` per room type and night; demand is the CONFIRMED lines (by the type of the assigned room, else the booked type) and the open stays in rooms of the type. Writers take the locks first and then call `availability.Shortfalls` / `RequireAvailable` (through `checkHolds` in the reservations module).
- A bed type is a description: `rooms.bed_type_id`, `reservation_rooms.requested_bed_type_id` (soft: a room of another bed can be assigned). The calendar already shows per type and bed `BedInventory`, counting only the rooms that are assigned.

## The rule of availability

Take one room type T and one night. Let `R` be the sellable rooms of T (no active block), `R_B` those with bed B, and let the demand be:

- **fixed**: a room is already chosen for it: an open stay, or a CONFIRMED line with a room assigned. It uses that room, so it uses a bed of that room's bed type (or none, when the room has no bed type);
- **locked**: a CONFIRMED line without a room that asked for bed B (`bed_locked`); it needs a room of B;
- **any**: a CONFIRMED line without a room and without a locked bed; it needs any room of T.

Whether all the demand fits is a bipartite matching (demand to rooms). The demands that need a bed need a room of exactly one bed, and the "any" demands need any room, so Hall's condition reduces to a set of plain counts, and **it is exact**:

```
for every bed B of the type:   fixed_B + locked_B  <=  R_B          (the rooms of B)
for the type as a whole:       fixed + locked + any  <=  R           (the existing rule)
```

(`fixed_B` are the fixed demands whose room has bed B; a fixed demand in a room with no bed counts only in the whole.) So **the stock of a variant is `available(T, B, n) = min(R_B - fixed_B - locked_B, available(T, n))`**: a new locked booking adds one to both sides, and a new "no preference" booking adds one only to the type. The engine checks the same two lines for every extra demand, so no operation can leave a night that cannot be matched. A brute-force matching on small random cases is part of the tests, to prove the counts.

## Schema (migration 00057, with Down)

- `reservation_rooms.bed_locked boolean NOT NULL DEFAULT false`; CHECK `NOT bed_locked OR requested_bed_type_id IS NOT NULL`. Every existing request stays soft (`false`): nothing already booked changes meaning.
- `rate_plan_bed_adjustments` (per rate plan, room type and bed type): `adjust_kind` AMOUNT or PERCENT, `amount numeric(18,3)` (a percentage may be negative; both may lower the price down to 0), `effective_from`. A row is never changed: a new figure is a new row from a later date (like the fee rules). The room type and the bed type are composite foreign keys with the property; unique `(rate_plan_id, room_type_id, bed_type_id, effective_from)`. No row, or an amount of 0, means the same price.
- `reservation_room_rates` keeps what each night was priced at (the snapshot exists): a night of a locked line also keeps `bed_adjustment` (the amount added), so the price of a night never moves with a later change of an adjustment.
- **`rooms.bed_type_id` becomes `NOT NULL`** (decision 4). The migration first gives every room that has none the first active bed type of its property by the sort order of the catalogue (the catalogue of 00037 starts with King), and then sets the constraint; the number of rooms it filled is reported in the migration log and in the audit trail (`room.bed_type_defaulted`), and Setup shows no list of them because there is nothing left to find: the owner reviews the rooms after the migration. The Down drops the constraint only.
- Guard: a room's bed type is changed through the service (see below), never to a bed that leaves demand unmatched.

## Engine (`internal/availability`)

- New `BedNights`/`BedInventory` replace the current one: per room type, bed type and night: `sellable` (R_B), `fixed`, `locked`, `available_bed = min(R_B - fixed - locked, available_type)`. Queries count fixed from stays and assigned CONFIRMED lines by the bed of the room, locked from CONFIRMED lines without a room (`bed_locked`).
- `Extra` gets a second dimension: demand per (type, bed, night) next to per (type, night); `FindShortfalls` tests both lines above and a `Shortfall` carries the bed (`bed_type_id`, absent for the type line). `RequireAvailable` answers 409 `ROOM_TYPE_NOT_AVAILABLE` (the type) or the new 409 `BED_NOT_AVAILABLE` (the variant), with the nights.
- `RemovalShortfalls`, `BlockShortfalls` and the change of a room's bed type test the bed lines too: a room leaving the stock of its bed (deactivated, moved to another type, blocked, given another bed) must not make a variant oversold.

## Rooms (`internal/rooms`)

- **Create and update a room**: `room_type_id` and `bed_type_id` are required (422 `REQUIRED` on the field; 422 `BED_TYPE_INACTIVE` for a bed switched off, as today). The update that gave `0` to clear the bed (`PATCH bed_type_id: 0`) is no longer accepted (422 `REQUIRED`): a room's bed can be changed, not removed. A bed type that rooms have can still be switched off: that only stops new use, as today, and the rooms keep it.
- **Changing the bed type of a room** is an operation of the inventory: it moves the room between variants, so it is refused when it would leave a variant oversold on a night from the business date on (409 `BED_NOT_AVAILABLE`, with the nights), and it takes the same locks as moving a room to another type.
- The room form and the room list show both as required fields; the import or the seed of rooms (`pms-seed rooms`) always gives a bed. Tests and fixtures that create rooms give them a bed type (the helper of the test setup takes one, the first of the catalogue by default).
- Because every room has a bed, the engine has no "room without a bed": a fixed demand always counts in a bed line, and a type whose rooms all have one bed offers one variant.

## Reservations (`internal/reservations`)

- `LineInput`/`LinePatch` get `bed_locked` (with the existing `bed_type_id`): `bed_type_id` alone is the soft request as today; with `bed_locked: true` it is a variant. Locking needs a bed type that exists and is active (422 `BED_LOCK_NEEDS_BED_TYPE`).
- `checkHolds` (create, add a line, amend, confirm) adds the bed demand of a locked line to the extra demand, so the variant's stock is checked with the type's. Amending the dates, the type, the bed or the lock re-checks without the line's own demand, as today.
- **Assigning a room** to a locked line needs a room with that bed (422 `ROOM_BED_MISMATCH`, field `room_id`); a line that is not locked may take any room, as today. A room is assigned only if the bed lines still hold after the change (the fixed demand moves from "locked" to "fixed" in the same bed, which never worsens a bed line; assigning another bed to an unlocked line moves its demand between beds, which is tested).
- **Stays**: check-in, walk-in and reverse check-in of a locked line need a room with the bed; a walk-in with no line may choose any room. A **room move** of a stay checks the bed lines for the nights that remain (the same shortfall check); a stay moved out of the bed of its locked line is allowed when the check passes and the move is audited with the original request.
- A line whose room is unassigned keeps its lock; unlocking is an amend (`bed_locked: false`) that frees the variant stock and is re-checked.
- Existing lines: all soft. The free-room picker keeps listing the rooms with the requested bed first; for a locked line it lists only the rooms with the bed.

## Price (`internal/rates` and the pricing of a line)

- `GET/POST {P}/rate-plans/{id}/bed-adjustments` (`rate.manage` to write, any access to the property to read): the figures per room type and bed type, as rows from a date (append-only, so POST and not PUT). As built: the percentage is of the price the night is sold at (the grid price after the yield rules), and the amount is in the price mode of the rate plan, like the grid. `base_rate` of the night includes the supplement and `bed_adjustment` records it; locking, unlocking or changing the bed of a line prices all its nights again (as a change of plan does, so overrides on its nights are dropped); an amendment that does not touch the bed keeps the snapshot.
- A night of a **locked** line is priced `grid price (after the yield rules) + adjustment` where the adjustment is the amount, or the percentage of the grid price, rounded to the currency decimals half away from zero, in force on the night; the result is not below 0. A line with no lock pays the grid price. The adjustment is taken when the night is priced (create, amend, new nights of an extension) and kept with the night; the override with reason and approval works on the price as it works today, after the adjustment.
- Complimentary and house use plans stay at zero whatever the adjustment (the occupancy kind rule comes first).

## Search and screens

- **Search** (`SearchAvailability`): each room type offers its variants beside the type: per variant the rooms left (`min(...)`), and the price per plan with the adjustment; the type line stays "any bed". Types whose rooms have no bed type offer only the type.
- **Availability calendar**: the bed rows (already there) show `sellable`, `fixed + locked` and `available_bed`; the detailed view marks the nights on which a variant is the limit.
- **New reservation and the lines of a reservation**: "Bed requested" becomes the choice **No preference / King / Twin / ...** with the count left for the dates, and a "keep this bed" toggle (`bed_locked`) that shows the supplement; an unavailable variant is greyed.
- **Setup**: on a rate plan, a grid "Bed supplements" (room type x bed type, amount or percent, from a date).
- Arrivals and the board show a locked request apart from a soft one.

## API (OpenAPI first)

`bed_locked` on the reservation line (create, add, amend; and in the answers); `bed_adjustment` on a priced night; `variants` in the search answer; `GET/PUT /rate-plans/{id}/bed-adjustments`; calendar bed rows carry `fixed`, `locked`, `available_bed`; 409 `BED_NOT_AVAILABLE`, 422 `ROOM_BED_MISMATCH`, `BED_LOCK_NEEDS_BED_TYPE`. Error codes get their texts in both languages.

## Locking and transactions

No new lock level: the writers already hold the business day, the room types and the rooms before they check; the bed lines are read inside the same transaction. A change of the adjustments takes the rate plan like any rate edit.

## Tests

- **The rule**: a brute-force matching of demand to rooms against `FindShortfalls` on small random cases (rooms with beds and without, blocks, fixed, locked and any demand): both always agree.
- **Stock**: a locked booking uses the variant and the type; a no-preference booking only the type; the last King cannot be locked twice; a block, a deactivation, a move to another type and a change of a room's bed that would oversell a variant are refused (409 `BED_NOT_AVAILABLE`), and one that does not oversell is allowed.
- **Assignment**: a locked line takes only a room with its bed; an unlocked line any; check-in of a locked line; a room move that keeps or breaks the bed lines; unlocking frees the stock.
- **Price**: AMOUNT and PERCENT, rounding at 0 and 2 decimals, the floor at 0, a later adjustment moves no night already priced, an unlocked line pays the grid, a complimentary plan stays zero, the override after the adjustment.
- **Search and calendar** carry the variants and their stock; **API**: validation, permissions, tenant isolation, the OpenAPI check; **front end**: the choice and the lock on the new reservation, the supplement grid, the search variants.

## Order of building (approved, one commit per step)

1. Migration 00057 (the bed type of a room required, `bed_locked`, the adjustment table), constraint mappings, schema tests; the rooms service and screens requiring the bed type; the fixtures and the seed giving rooms a bed; `bed_locked` through the line (create, add, amend, views) without effect on stock yet; the adjustment table with its API.
2. (done) The engine: bed lines in `Inventory`/`Extra`/`FindShortfalls`, the guards for blocks, removals and a change of a room's bed, and the random matching test.
3. (done) Reservations: `checkHolds` with the bed demand, assignment and check-in rules, room moves, unlocking.
4. (done) Price: the adjustments in the pricing of a line, the snapshot per night, the override order.
5. (done) Search and the calendar.
6. (done) Front end and the full checks. Documents (PDF, e-mail) do not show the bed: they never showed the bed request.

## Not in this scope

Bed counts per room type (1 King or 2 Twin); a variant with a rate grid of its own; a variant that crosses room types (a King in any type); a bed variant on the tape chart; channel manager mapping.

## Points to confirm

- **Every room has a bed type** (decision 4), so the migration fills the rooms that have none with the first active bed type of the property and says how many; check them afterwards. A type whose rooms all share one bed offers one variant, which is the same as the type.
- A **walk-in** and a check-in of a line with no lock may take any room, as today.
- The adjustment is **per rate plan and room type and bed type** (rows from a date); nothing is inherited between plans.
