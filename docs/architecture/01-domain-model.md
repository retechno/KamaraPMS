# Final Domain Model (revision 3): Step 4

> Conventions: ranges are half-open `[start, end)`. A night is identified by the business date it starts on. `*_date` means a `date`, and `*_at` means a `timestamptz`. BD = the property's OPEN business date.

## 1. Modules and entities

**Bold** entities are aggregate roots.

| Module | Entity | Responsibility |
|---|---|---|
| **tenancy** | **Tenant** | A SaaS customer. Holds the default timezone. It's the top-level isolation boundary. |
| | **Property** | A hotel. Holds its timezone, currency and decimals (locked once financial data exists), check-in and check-out policy times, `require_room_inspection_for_checkin`, `night_audit_marks_occupied_dirty` and status. |
| | **BusinessDay** | A property's business dates. Exactly one is OPEN, and that row is the current BD. CLOSED rows hold the daily closing summary. |
| | DocumentSequence | Gapless numbering: RESERVATION, STAY, FOLIO, PAYMENT. |
| **iam** | **User** | Belongs to one tenant, and the email is unique per tenant. `is_tenant_admin` grants full access. |
| | UserSession | A rotating refresh-token session. |
| | **Role** / RolePermission | A tenant-defined role holding permission codes from the catalogue in Go. |
| | UserProperty | Access to one property with one role. |
| **rooms** | **RoomType** | The inventory unit. Holds occupancy limits (max adult, max child, max occupancy, base occupancy). |
| | **Room** | A physical room with no status. `is_active` means decommissioned. |
| | **RoomBlock** | OOO or OOS for a date range. **Both make the room unsellable and unassignable.** OOO is also excluded from the available-room statistics. |
| **housekeeping** | **RoomHousekeeping** | Current `CLEAN`, `DIRTY`, `CLEANING` or `INSPECTED` state, with HousekeepingLog as its history. |
| **guests** | **Guest** | A tenant-wide profile (`code`, identity, contact). `origin_property_id` supports the visibility rules. |
| **billing (config)** | **Tax** | Code, name, `rate` (a percent) and `tax_on_service`. It has **no** inclusive flag. |
| | **ServiceCharge** | Code, name and `rate` (a percent). |
| | **ChargeCode** | What is charged: `charge_type` (ROOM, FOOD_BEVERAGE, SERVICE, FEE, OTHER) and `price_mode` (EXCLUSIVE or INCLUSIVE, and extensible). **It owns its ordered tax and service mappings.** |
| | ChargeCodeTax / ChargeCodeServiceCharge | The ordered (`sequence`) and activatable mapping to taxes and service charges. |
| **rates** | **RatePlan** | Pricing strategy: meal plan (informational in the MVP), cancellation policy (text), refundable flag and **room charge code**. |
| | Rate | The grid price for (plan, room type, night), in the room charge code's price mode. |
| **reservations** | **Reservation** | The booking container: booker, `reservation_date` (the BD when booked), source, market and requests. Status is `DRAFT`, `CONFIRMED` or `CANCELLED`. |
| | ReservationRoom | One booked room unit with the full lifecycle: `DRAFT`, `CONFIRMED`, `CHECKED_IN`, `COMPLETED`, `CANCELLED` or `NO_SHOW`. It holds the dates, occupancy, booked type, optional room and rate plan, and it's the unit that consumes inventory before arrival. |
| | ReservationRoomRate | The pricing snapshot for one night: rate plan, charge code, price mode, base rate, discount and agreed amount. |
| **frontdesk** | **Stay** | The physical occupancy of one reservation room. Holds its BD arrival, current expected departure, actual check-in and check-out instants, and status (`OPEN`, `CHECKED_OUT` or `CANCELLED`). |
| | StayRoom | Sequential segments. Each has instants and business dates, and exactly one is open while the stay is OPEN. |
| | StayGuest | Accompanying guests. |
| **billing (ledger)** | **Folio** | The financial account. Belongs to a reservation and is linked to a stay at check-in (1 GUEST folio per stay). The balance is always computed. |
| | FolioItem | An immutable ledger line: debit or credit plus the calculation snapshot. |
| | FolioItemComponent | One applied tax or service charge: code, name, rate, taxable base, amount and sequence. |
| | Payment | Payment instrument record (POSTED or VOIDED; a REFUND is a new row). Always paired with a FolioItem. |
| | StayChargePosting | The **posting register**: the business identity of each posted expected charge (stay, night, source). Makes posting idempotent. |
| **audit** | AuditLog | Append-only record of old and new data, the user, server time and BD. |

**Services** (stateless, with no entities of their own):

| Service | Module | Role |
|---|---|---|
| ChargeCalculationEngine | `chargecalc` (pure) + `billing.ChargeCalculationService` | The only place amounts are calculated |
| ExpectedChargeEngine | `billing/expected` | Deterministically classifies expected charges |
| RoomChargePostingService | `billing/roomcharge` | The only room-charge posting path |
| FolioPostingService | `billing/folio` | The only writer of `folio_items` and `folio_item_components` |
| PaymentService | `billing/payment` | Payments, voids, refunds and deposits |
| AvailabilityEngine | `availability` | Room-type and room availability (read-only) |
| CheckInService, CheckOutService, RoomMoveService, StayChangeService, WalkInService | `frontdesk` | Front-desk orchestration |
| NightAuditService | `nightaudit` | Orchestration only: no calculation, no posting logic |
| BusinessDayService | `tenancy` | Reads the BD, and closes and opens days (called only by night audit) |

## 2. Relationships

```
Tenant 1─* Property 1─* BusinessDay (1 OPEN)      Tenant 1─* User, Role, Guest
User 1─* UserProperty *─1 Property;  UserProperty *─1 Role
Property 1─* RoomType 1─* Room;  Room 1─1 RoomHousekeeping, 1─* HousekeepingLog, 1─* RoomBlock
Property 1─* Tax, ServiceCharge, ChargeCode;  ChargeCode *─* Tax, ServiceCharge (ordered mappings)
RatePlan *─1 ChargeCode (room);  Rate = (RatePlan, RoomType, night)
Reservation *─0..1 Guest (booker);  Reservation 1─* ReservationRoom
ReservationRoom *─1 RoomType, *─0..1 Room, *─1 RatePlan, *─0..1 Guest (occupant), 1─* ReservationRoomRate
ReservationRoomRate *─1 RatePlan, *─1 ChargeCode
ReservationRoom 1─0..1 Stay (live);  Stay *─1 Guest, 1─* StayRoom *─1 Room, 1─* StayGuest
Reservation 1─* Folio *─0..1 Stay  (≤ 1 GUEST folio per stay)
Folio 1─* FolioItem 1─* FolioItemComponent;  FolioItem *─0..1 ChargeCode, Payment, StayRoom, FolioItem(reverses)
Stay 1─* StayChargePosting 1─1 FolioItem;  StayChargePosting *─1 StayRoom, ChargeCode
FolioItem, Payment, StayChargePosting *─1 BusinessDay
```

## 3. Aggregate boundaries

| Aggregate | Contains | Invariants |
|---|---|---|
| **BusinessDay** | none | One OPEN per property. Closing BD opens BD+1 in the same transaction. There are no gaps, no reopening, and a CLOSED day is immutable. |
| **Property** | settings | Currency and decimals are locked once `folio_items` exist. |
| **User** | UserSession, UserProperty | Email is unique per tenant. Grants stay within the tenant (composite FK). |
| **Role** | RolePermission | Codes come from the catalogue. System roles are protected. |
| **ChargeCode** | ChargeCodeTax, ChargeCodeServiceCharge | Mapped rules are active and belong to the same property. The sequence is unique among active mappings. `price_mode` is immutable once used. |
| **Tax**, **ServiceCharge** | none | `0 ≤ rate ≤ 100`. Deactivate only. Can't be deactivated while actively mapped. |
| **RatePlan** | Rate | The room charge code is active and has `charge_type = ROOM`. Rates are ≥ 0. |
| **Reservation** | ReservationRoom, ReservationRoomRate | `departure > arrival`. One rate row for every night of every CONFIRMED or DRAFT line. Header and line statuses are consistent. Occupancy is within the room type's limits. Posted nights are immutable. |
| **Stay** | StayRoom, StayGuest | Exactly one open segment while OPEN and none otherwise. Segments are contiguous in business dates. `departure > arrival`. |
| **Folio** | FolioItem, FolioItemComponent, Payment, StayChargePosting | Items are immutable. Totals equal the component sums. Debit and credit follow the rules. Nothing is posted to a CLOSED folio. The balance must be 0 to close (the MVP policy). Each item is reversed at most once. Refunds are ≤ the refundable amount. There is one POSTED register row per (stay, night, source). |
| **RoomBlock** | none | `end > start`. No overlapping ACTIVE block on a room. |
| **RoomHousekeeping** | HousekeepingLog | Transitions are legal. Every change is logged. |

**Cross-aggregate invariants.** These are enforced by services in one transaction using the lock protocol ([05-transactions-locking.md](05-transactions-locking.md)), with constraints as a backstop:
1. No double occupancy or double allocation of a physical room (Reservation × Stay × RoomBlock).
2. Room-type inventory is never oversold.
3. Check-in, room move, check-out and night audit are atomic across aggregates.
4. Every business-dated write uses the OPEN day and never interleaves with closing it.

## 4. Source of truth vs derived

| Fact | Source of truth | Derived |
|---|---|---|
| Current business date | `business_days` (OPEN) | none |
| Server time | DB `now()` (UTC) | Local time = converted to the property timezone |
| Occupancy (OCCUPIED, RESERVED, VACANT) | Open `stay_rooms` and CONFIRMED assigned `reservation_rooms` | **Derived** |
| Housekeeping | `room_housekeeping` | Source |
| Blocked today | `room_blocks` (ACTIVE and dates) | **Derived** |
| Reservation arrival, departure, room count and display status | `reservation_rooms` (+ `stays`) | **Derived** |
| Agreed nightly price and its pricing context | `reservation_room_rates` | **Snapshot** |
| Reservation estimate (service, tax, total) | Engine over the nightly rows and current rules | **Derived**, not stored |
| Posted amounts, rates and taxable bases | `folio_items` + `folio_item_components` | **Snapshot** |
| Whether a night is posted | `stay_charge_postings` (POSTED) | none |
| Expected charges (READY, ALREADY_POSTED, NOT_APPLICABLE, ERROR) | Computed by the ExpectedChargeEngine | **Derived** |
| Folio balance | `SUM(debit − credit)` | **Derived** |
| Availability | Rooms, blocks, lines, stays | **Derived** |
| Daily statistics | `business_days.summary` | Immutable closing snapshot |

## 5. State machines

```
BusinessDay:     OPEN ──night audit──▶ CLOSED  (+ BD+1 inserted OPEN, same tx)
Reservation:     DRAFT ─confirm─▶ CONFIRMED ─cancel─▶ CANCELLED ;  DRAFT ─cancel─▶ CANCELLED
ReservationRoom: DRAFT ─(confirm)─▶ CONFIRMED ─check-in─▶ CHECKED_IN ─check-out─▶ COMPLETED
                   └─cancel─▶ CANCELLED      ├─cancel─▶ CANCELLED
                                             └─no-show (staff)─▶ NO_SHOW
                 CHECKED_IN ─reverse check-in (same BD)─▶ CONFIRMED
Stay:            OPEN ─check-out─▶ CHECKED_OUT ;  OPEN ─reverse check-in─▶ CANCELLED
Folio:           OPEN ⇄ CLOSED   (close: balance 0 ; reopen: permission)
Payment:         POSTED ─void (same BD)─▶ VOIDED
StayChargePosting: POSTED ─item reversed─▶ REVERSED
RoomBlock:       ACTIVE ─cancel─▶ CANCELLED
Housekeeping:    DIRTY→CLEANING→CLEAN→INSPECTED ;  DIRTY→CLEAN ;  any→DIRTY
```

## 6. Project structure (module-first modular monolith)

```
kamara-pms/
├── cmd/api/main.go                        # wiring only
├── internal/
│   ├── platform/                          # technical kernel (no business rules)
│   │   ├── config/  db/ (pgxpool, TxManager, locks)  httpx/ (router, errors, pagination)
│   │   ├── auth/ (JWT, argon2id, scope middleware)  clock/  money/ (decimal, rounding)
│   ├── chargecalc/                        # ChargeCalculationEngine core (pure)
│   ├── tenancy/                           # tenant, property, BusinessDayService, sequences
│   ├── iam/                               # users, roles, permissions, sessions
│   ├── rooms/                             # room types, rooms, room blocks
│   ├── housekeeping/
│   ├── guests/
│   ├── rates/                             # rate plans, rate grid
│   ├── billing/
│   │   ├── config/                        # taxes, service charges, charge codes, ChargeRuleResolver
│   │   ├── calc.go                        # ChargeCalculationService (resolver + chargecalc)
│   │   ├── folio/                         # folios + FolioPostingService
│   │   ├── payment/                       # PaymentService
│   │   ├── expected/                      # ExpectedChargeEngine (loader + pure evaluator)
│   │   └── roomcharge/                    # RoomChargePostingService
│   ├── availability/                      # AvailabilityEngine (read-only)
│   ├── reservations/
│   ├── frontdesk/                         # CheckIn, WalkIn, RoomMove, StayChange, CheckOut services
│   ├── nightaudit/                        # NightAuditService (orchestrator) + check registry
│   └── audit/
├── migrations/                            # goose SQL
├── api/openapi.yaml                       # used to generate the TS client
├── web/                                   # Vue 3 + TS
├── config/
├── docs/
└── go.mod
```
Each package follows this pattern: `model.go` (pure domain types and rules) → `service.go` (use cases, transaction boundary) → `store.go` + `queries.sql` (sqlc + pgx) → `http.go` (thin handler).

**Mapping to the requested layers:** Handler = `http.go`, Application Service = `service.go`, Domain = `model.go` and `chargecalc`, Repository = `store.go`, PostgreSQL = `migrations/`.

**Dependency rules:**
- No cycles. `platform` ← everything. `chargecalc` has no dependencies.
- `billing` imports `tenancy` (BD, sequences) and `chargecalc`. It **defines** the ports it needs (`StayReader`, `NightlyRateReader`). `frontdesk` and `reservations` implement those ports, and they are wired in `main.go`. Billing never imports frontdesk, so frontdesk → billing (check-in creates a folio, check-out posts room charges) stays acyclic.
- `nightaudit` depends on `billing/expected`, `billing/roomcharge`, `tenancy` and the `reservations` and `frontdesk` read models.
- **Only `billing/folio` writes `folio_items`.** A test enforces this by scanning SQL files for `INSERT INTO folio_items`.
