# Domain Design (revision 2)

> See [00-decisions.md](00-decisions.md) for the final decisions and the list of challenges.
> Conventions: every night or date range is **half-open** `[start, end)`. A "night" is identified by the business date it starts on. **Business date ≠ server date.**

---

## 1. Modules (bounded contexts) and entities

**Bold** entities are aggregate roots.

### 1.1 Tenancy (`tenancy`)
| Entity | Responsibility |
|---|---|
| **Tenant** | The SaaS customer (a hotel company). Top-level isolation boundary. Owns users, roles, guests and properties. |
| **Property** | One hotel. Holds its configuration: timezone, currency, currency decimals, check-in and check-out policy times, `require_room_inspection_for_checkin`, and `night_audit_marks_occupied_dirty`. It does **not** hold the business date. |
| **BusinessDay** | One row per business date of a property. Exactly one is `OPEN`, and that row *is* the current business date. `CLOSED` rows are the history of night audits and store a summary. |
| DocumentSequence | Gapless per-property counters for confirmation, stay, folio and payment numbers. |

### 1.2 Identity and access (`iam`)
| Entity | Responsibility |
|---|---|
| **User** | A login identity. Belongs to one tenant. `email` is unique within the tenant. `is_tenant_admin` gives full access across the tenant. |
| UserSession | A refresh-token session, which can be revoked. |
| **Role** | A named set of permission codes, defined per tenant. |
| RolePermission | A permission code. The catalogue of valid codes lives in Go code. |
| UserProperty | Gives a user access to a property **with one role**. Having no row means no access. |

### 1.3 Rooms (`rooms`, `housekeeping`)
| Entity | Responsibility |
|---|---|
| **RoomType** | A sellable category and the unit of inventory. Defines occupancy limits. |
| **Room** | A physical room. Master data only, with **no occupancy status**. `is_active` only means the room has been decommissioned. |
| **RoomBlock** | `OOO` removes the room from inventory. `OOS` keeps it in inventory but makes it unassignable. Covers a date range. |
| **RoomHousekeeping** | The current cleanliness state (`CLEAN`, `DIRTY`, `CLEANING` or `INSPECTED`). This is a human observation, so it's source data. |
| HousekeepingLog | An append-only history of state transitions. |

### 1.4 Guests (`guests`)
| Entity | Responsibility |
|---|---|
| **Guest** | A **tenant-wide** person profile. `origin_property_id` records where the profile was created, which is used for visibility rules. |

### 1.5 Billing configuration (`billing`)
| Entity | Responsibility |
|---|---|
| **Tax** | A configurable tax: code, name, `rate_percent`, `tax_on_service`. **No inclusivity flag** (see R1). |
| **ServiceCharge** | A configurable service charge: code, name, `rate_percent`. |
| **ChargeCode** | What is being charged (ROOM, BREAKFAST, RESTAURANT, LAUNDRY, MINIBAR, EXTRA_BED, OTHER, …) plus its revenue category and default price mode. **It owns its tax and service mappings.** |
| ChargeCodeTax | Maps a charge code to a tax it attracts (0..n). |
| ChargeCodeServiceCharge | Maps a charge code to a service charge it attracts (0..n). |

### 1.6 Pricing (`rates`)
| Entity | Responsibility |
|---|---|
| **RatePlan** | A commercial offer. Chooses `room_charge_code_id` (which determines the tax and service rules) and `price_mode` (`EXCLUSIVE` or `INCLUSIVE`). |
| Rate | The price for (rate plan, room type, night), interpreted according to the plan's `price_mode`. |

### 1.7 Front office (`reservations`, `frontdesk`)
| Entity | Responsibility |
|---|---|
| **Reservation** | The booking container: booker, source, market, requests. Header status is `DRAFT`, `CONFIRMED` or `CANCELLED`. |
| ReservationRoom | One booked room unit with its own lifecycle (`DRAFT`, `CONFIRMED`, `CANCELLED`, `NO_SHOW` or `CHECKED_IN`). Holds its dates, occupancy, booked type, optional assigned room and rate plan. |
| ReservationRoomRate | The agreed price per night, with a **snapshot of `price_mode`**. |
| **Stay** | The physical occupancy of one room unit (1 stay per line). Holds actual timestamps and the *current expected* departure. |
| StayRoom | Sequential room segments. A room move closes one segment and opens the next. |
| StayGuest | Accompanying guests. |

### 1.8 Billing ledger (`billing`)
| Entity | Responsibility |
|---|---|
| **Folio** | A financial account. Belongs to a reservation and is linked to a stay at check-in. Its balance is computed. |
| FolioItem | An immutable ledger line with a signed total and a breakdown (base, discount, net, service, tax). |
| FolioItemComponent | One applied tax or service charge on an item, with a snapshot of its code, rate, taxable base and amount. |
| Payment | Details of the payment instrument. Always paired with one FolioItem. |

### 1.9 Cross-cutting
| Component | Responsibility |
|---|---|
| **Charge Calculation Engine** (`chargecalc`) | A pure function that turns an amount, price mode, discount and rules into a breakdown. It has no database access and no country rules. See [04-charge-calculation.md](04-charge-calculation.md). |
| Availability engine (`availability`) | Read-only queries that compute room type and room availability. |
| Night audit (`nightaudit`) | The process that validates blockers, posts room charges, closes the day and opens the next. |
| AuditLog (`audit`) | An append-only record of who changed what, stamped with both the server time and the business date. |

---

## 2. Relationships

```
Tenant 1─* Property 1─* BusinessDay        (exactly one OPEN per property)
Tenant 1─* User, Role, Guest
User *─* Property via UserProperty (1 role per pair)

Property 1─* RoomType 1─* Room
Room 1─1 RoomHousekeeping, 1─* HousekeepingLog, 1─* RoomBlock

Property 1─* Tax, ServiceCharge, ChargeCode
ChargeCode *─* Tax            (ChargeCodeTax)
ChargeCode *─* ServiceCharge  (ChargeCodeServiceCharge)
RatePlan *─1 ChargeCode (room revenue);  (RatePlan × RoomType × night) → Rate

Reservation *─0..1 Guest (booker), 1─* ReservationRoom
ReservationRoom *─1 RoomType, *─0..1 Room, *─1 RatePlan, *─0..1 Guest (occupant)
ReservationRoom 1─* ReservationRoomRate, 1─0..1 Stay (non-cancelled)

Stay *─1 Guest (primary), 1─* StayGuest, 1─* StayRoom *─1 Room

Reservation 1─* Folio *─0..1 Stay
Folio 1─* FolioItem 1─* FolioItemComponent *─0..1 Tax | ServiceCharge
FolioItem *─0..1 ChargeCode, 0..1─1 Payment, 0..1─0..1 FolioItem (reverses)
FolioItem, Payment *─1 BusinessDay (the posting date)
```

---

## 3. Aggregate boundaries

| Aggregate (root) | Contains | Invariants |
|---|---|---|
| **BusinessDay** | none | Exactly one OPEN per property. Dates run on without gaps. Only night audit closes a day, and it opens exactly `closed + 1`. A CLOSED day never reopens. |
| **Property** | settings | Currency and decimals are immutable once any financial transaction exists. |
| **User** | UserSession, UserProperty | Email is unique per tenant. Property grants only reference the user's own tenant (composite FK). |
| **Role** | RolePermission | Codes come from the catalogue. System roles can't be deleted. |
| **ChargeCode** | ChargeCodeTax, ChargeCodeServiceCharge | The mapped taxes and service charges belong to the same property and are active when mapped. |
| **Tax**, **ServiceCharge** | none | `0 ≤ rate_percent ≤ 100`. Deactivate only, never delete, once used. |
| **RatePlan** | Rate | The room charge code is active. Amounts are ≥ 0. |
| **Reservation** | ReservationRoom, ReservationRoomRate | `departure > arrival`. A rate row exists for each night of every active line. Header and line statuses are consistent. Occupancy is within the room type's limits. |
| **Stay** | StayRoom, StayGuest | Exactly one open segment while IN_HOUSE. Segments are sequential. |
| **Folio** | FolioItem, FolioItemComponent, Payment | Items are immutable. The component sums equal the item totals. Nothing is posted to a CLOSED folio. The balance is 0 to close. An item is reversed at most once. Refunds are ≤ the refundable amount. |
| **RoomBlock** | none | `end > start`. No overlapping ACTIVE block on a room. |
| **RoomHousekeeping** | HousekeepingLog | Transitions are legal and every change is logged. |
| Room, RoomType, Guest, Tenant | none | Uniqueness. |

**Cross-aggregate invariants.** These are enforced by application services in one transaction using the lock protocol, with DB constraints as a backstop:
1. There is no double occupancy of a physical room (spans Reservation, Stay and RoomBlock).
2. Room type inventory is never oversold.
3. Check-in, check-out and room move are atomic across Reservation, Stay, Folio and Housekeeping.
4. Every business-dated write uses the **OPEN** business day, and night audit never interleaves with them. All writers lock the OPEN row `FOR SHARE`, and night audit locks it `FOR UPDATE`.

---

## 4. Source of truth vs derived

| Fact | Source of truth | Derived / snapshot |
|---|---|---|
| Current business date | `business_days` row with `status = OPEN` | Cached per request |
| Server date and time | Database `now()` (UTC) | Property local time = `now() AT TIME ZONE properties.timezone` |
| Room occupancy | Open `stay_rooms`, and CONFIRMED assigned `reservation_rooms` | **Derived** |
| Housekeeping | `room_housekeeping` | Source |
| Current OOO/OOS | `room_blocks` (ACTIVE) and its dates compared with the business date | **Derived** |
| Reservation arrival, departure and room count | `reservation_rooms` | **Derived** |
| Agreed nightly price and its price mode | `reservation_room_rates` | **Snapshot** (a historical fact) |
| Estimated reservation total including tax | The engine applied to the nightly rates and *current* rules | **Derived**, never stored (it's a quote) |
| Posted tax and service amounts and rates | `folio_items` + `folio_item_components` | **Snapshot**, frozen at posting |
| Folio balance | `SUM(folio_items.amount)` | **Derived** |
| Room type availability | Rooms, blocks, lines, stays | **Derived** |
| Daily statistics (future) | Written by night audit into `business_days.summary` | Immutable snapshot |

---

## 5. State machines

```
BusinessDay:        OPEN ──night audit──▶ CLOSED        (+ the next day is inserted OPEN, in the same tx)

Reservation:        DRAFT ──confirm──▶ CONFIRMED ──cancel──▶ CANCELLED
                      └──────────cancel─────────────────▶ CANCELLED

ReservationRoom:    DRAFT ──(header confirm)──▶ CONFIRMED ──check-in──▶ CHECKED_IN
                      └─cancel─▶ CANCELLED       ├─cancel──▶ CANCELLED
                                                 └─no-show─▶ NO_SHOW
                    CHECKED_IN ──reverse check-in (same business day)──▶ CONFIRMED

Stay:               IN_HOUSE ──check-out──▶ CHECKED_OUT
                       └──reverse check-in──▶ CANCELLED

Folio:              OPEN ⇄ CLOSED        (close needs a balance of 0; reopen needs permission)
Payment:            POSTED ──void (same business day)──▶ VOIDED
RoomBlock:          ACTIVE ──cancel──▶ CANCELLED
Housekeeping:       DIRTY → CLEANING → CLEAN → INSPECTED;  DIRTY → CLEAN;  any → DIRTY
```

---

## 6. Go project structure (module-first modular monolith)

```
kamara-pms/
├── cmd/api/main.go                 # wiring only
├── internal/
│   ├── platform/                   # config, db (pgxpool, TxManager, lock helpers), httpx,
│   │                               # auth (JWT, argon2id), clock (injectable), money (decimal)
│   ├── chargecalc/                 # PURE engine: no DB, no country rules
│   ├── tenancy/                    # tenants, properties, business days, document sequences
│   ├── iam/                        # users, roles, permissions catalogue, sessions
│   ├── rooms/                      # room types, rooms, room blocks
│   ├── housekeeping/
│   ├── guests/
│   ├── billing/                    # taxes, service charges, charge codes (+ ChargeRuleResolver),
│   │                               # folios, items, components, payments
│   ├── rates/                      # rate plans, rate grid
│   ├── availability/               # read-only engine
│   ├── reservations/
│   ├── frontdesk/                  # stays; check-in, walk-in, move, extend, check-out (orchestrator)
│   ├── nightaudit/                 # blockers, bulk no-show, run (orchestrator)
│   └── audit/
├── migrations/                     # goose SQL
├── api/openapi.yaml                # used to generate the TS client for Vue
├── web/                            # Vue 3 + TS
└── go.mod
```

The layout within each module is `model.go` (pure rules), `service.go` (use cases and transaction boundary), `store.go` + `queries.sql` (sqlc/pgx) and `http.go` (thin handlers).

**Dependency direction** (with no cycles):
```
chargecalc  ◀── billing ◀── reservations ◀── frontdesk ◀── nightaudit
                   ▲            ▲    ▲           │
tenancy ◀──────────┴── rates ◀──┘    └── availability ◀── rooms
```
`tenancy` (business date, sequences) and `audit` are leaf services that every module uses. `frontdesk` and `nightaudit` are the only modules that orchestrate several aggregates inside one `TxManager.WithinTx`.
