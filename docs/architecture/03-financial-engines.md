# Financial Services (revision 3): Steps 7–11

```
                  NightAuditService (orchestrates; no money logic)
                    │                 │
     CheckOutService│                 │  POST /night-audit/room-charges
          │         ▼                 ▼
          └──▶ RoomChargePostingService ──▶ ExpectedChargeEngine (what is due?)
                    │
                    ▼
              FolioPostingService ──▶ ChargeCalculationService ──▶ chargecalc (pure)
                    ▲                        │
 PaymentService ────┘                        └──▶ ChargeRuleResolver (charge code → rules)
                    │
                    ▼
              PostgreSQL (folio_items, folio_item_components, payments, stay_charge_postings)
```

**Single-path rules:**
- Only `chargecalc` multiplies by a rate.
- Only `FolioPostingService` inserts `folio_items` and `folio_item_components`.
- Only `RoomChargePostingService` posts `charge_type = ROOM` codes or writes `stay_charge_postings`.
- Only `PaymentService` writes `payments`.

All services require an **ambient transaction** (`TxManager.WithinTx`) supplied by the caller's use case. They never open or commit transactions themselves, so an orchestrator (check-out, night audit) composes them atomically.

---

## Step 7: Charge Calculation Engine

### 7.1 Two layers, one engine
| Layer | Package | Knows about |
|---|---|---|
| `ChargeCalculationService.Calculate(ctx, ChargeRequest)` | `billing` | Charge codes, rule resolution, the property's precision |
| `chargecalc.Calculate(Input) → Breakdown` | `chargecalc` | Only numbers. It has no DB, clock, country or charge-type knowledge. |

```go
// billing
type ChargeRequest struct {
    PropertyID   int64
    ChargeCodeID int64
    Quantity     decimal.Decimal
    UnitPrice    decimal.Decimal
    PriceMode    *chargecalc.PriceMode // nil = the charge code's price_mode
    Discount     decimal.Decimal
}

// chargecalc
type PriceMode string // EXCLUSIVE | INCLUSIVE   (MIXED later = a new case, no schema change)

type ServiceChargeRule struct{ ID int64; Code, Name string; Rate decimal.Decimal; Sequence int }
type TaxRule           struct{ ID int64; Code, Name string; Rate decimal.Decimal; OnService bool; Sequence int }

type Input struct {
    Quantity, UnitPrice, Discount decimal.Decimal
    PriceMode      PriceMode
    ServiceCharges []ServiceChargeRule // ordered by Sequence
    Taxes          []TaxRule           // ordered by Sequence
    Decimals       int32               // properties.currency_decimals
    ExemptTaxIDs   []int64             // reserved; unused in the MVP
}

type Component struct {
    Type string // SERVICE_CHARGE | TAX
    RuleID int64; Code, Name string; Rate decimal.Decimal; OnService *bool
    BaseAmount, Amount decimal.Decimal; Sequence int
}

type Breakdown struct {
    PriceMode                          PriceMode
    Quantity, UnitPrice                decimal.Decimal
    BaseAmount, Discount               decimal.Decimal
    NetAmount                          decimal.Decimal // net revenue (includes RoundingAdjustment)
    RoundingAdjustment                 decimal.Decimal
    ServiceComponents, TaxComponents   []Component
    ServiceChargeTotal, TaxTotal       decimal.Decimal
    TaxableAmount                      decimal.Decimal // the largest tax base (for display)
    TotalAmount                        decimal.Decimal // = Net + Service + Tax
}
```

### 7.2 Rules
- **Rounding:** at the precision `properties.currency_decimals`, using **half away from zero**, on every *line* amount (base, each component, and net₀). `shopspring/decimal`, never float.
- **Ordering:** discount → service charges (on net) → taxes (on net, plus service if `on_service`). **Taxes are not compounded** in the MVP. `sequence` fixes the order of calculation and display, which is where compounding can be added later.

**Signs and credits (implementation notes).** Amounts are signed: a negative quantity or unit price calculates a credit, and every rounding is half away from zero, so `Calculate(−x) = −Calculate(x)` for every amount. The discount is always a non-negative *magnitude* that reduces the amount towards zero; the breakdown reports it with the sign of the base (`Discount = magnitude × sign(base)`), which keeps the ledger check `net = base − discount` true for credits too. A discount may not have more decimals than the currency. Validation errors are `*chargecalc.InputError` (naming the field) and satisfy `errors.Is(err, chargecalc.ErrInvalid)`. `ExemptTaxIDs` is reserved: a non-empty list is rejected, not ignored.

### 7.3 EXCLUSIVE
```
base    = round(qty × unit_price)
net     = base − discount                       (0 ≤ discount ≤ |base|)
svc_i   = round(net × rate_i/100)               S = Σ svc_i
tb_j    = net + (on_service_j ? S : 0)
tax_j   = round(tb_j × rate_j/100)              T = Σ tax_j
total   = net + S + T,   rounding_adjustment = 0
```

### 7.4 INCLUSIVE (the price already contains every mapped component)
```
gross   = round(qty × unit_price) − discount
s       = Σ rate_i/100,   F = 1 + s + Σ_j (rate_j/100) × (1 + (on_service_j ? s : 0))
net₀    = round(gross / F)
svc_i, tax_j computed from net₀ exactly as in 7.3
rounding_adjustment = gross − (net₀ + S + T)
net     = net₀ + rounding_adjustment
total   = gross                                  ← always equals the quoted amount
```
**Deterministic rounding policy (documented):** components are computed from the extracted net₀. Any residual left by rounding goes into **net revenue** and is recorded in `rounding_adjustment`. Service and tax amounts are therefore always exactly `round(base × rate)` of their own base, which is what tax authorities audit. The guest always pays the quoted price.

### 7.5 Worked examples (IDR, decimals = 0; SERVICE 10%; VAT 11%)
| Case | Input | Net | Service | Taxable | VAT | Rounding adj. | Total |
|---|---|---:|---:|---:|---:|---:|---:|
| Exclusive, tax on service | 1,000,000 | 1,000,000 | 100,000 | 1,100,000 | 121,000 | 0 | **1,221,000** |
| Exclusive, no tax on service | 1,000,000 | 1,000,000 | 100,000 | 1,000,000 | 110,000 | 0 | **1,210,000** |
| Inclusive, tax on service (§15) | 1,110,000 | 909,091 | 90,909 | 1,000,000 | 110,000 | 0 | **1,110,000** |
| Inclusive, VAT only | 1,110,000 | 1,000,000 | none | 1,000,000 | 110,000 | 0 | **1,110,000** |
| Exclusive, discount 100,000 | 1,000,000 | 900,000 | 90,000 | 990,000 | 108,900 | 0 | **1,098,900** |
| `ROOM_EXEMPT` (no rules) | 1,000,000 | 1,000,000 | none | none | none | 0 | **1,000,000** |

**Residual example (USD, decimals = 2)**, inclusive 7.00 with service 10% and VAT 11% on service: `F = 1.221`, `net₀ = round(5.7330) = 5.73`, service `round(0.573) = 0.57`, taxable 6.30, VAT `round(0.693) = 0.69`, sum 6.99 → **rounding adjustment +0.01** → net 5.74, total **7.00**.

### 7.6 Validation errors
- Quantity is 0.
- Discount is outside `[0, |base|]`.
- A rate is outside `[0, 100]`.
- A rule ID is duplicated.
- The price mode is unknown.
- Decimals are outside `[0, 3]`.
- The charge code is inactive or belongs to another property (service layer).

### 7.7 Verify (for future integrations)
`Verify(in, claimed, tolerance)` recalculates the breakdown and compares it component by component, for systems (POS) that calculate their own tax. It's defined now and used later.

### 7.8 Consumers
- Reservation estimate (a `ChargeRequest` per night, summed)
- `POST /charge-calculations` preview
- FolioPostingService (every charge and adjustment)
- RoomChargePostingService (through FolioPostingService)
- Future invoice and POS

---

## Step 8: Expected Charge Engine

### 8.1 Purpose
It gives a deterministic answer to *"which charges should exist for stay S on night N, and do they?"*. It's split into:
- a **loader**, which runs SQL and returns a `Snapshot`, and
- a **pure evaluator**: `Evaluate(Snapshot, Scope) → []ExpectedCharge`. For the same snapshot it always returns the same output, and it's unit-testable without a database.

```go
type Scope struct {
    PropertyID   int64
    BusinessDate time.Time   // the property's BD
    StayIDs      []int64     // empty = all OPEN stays
    UpToDate     time.Time   // the last night considered; normally = BD
}

type ExpectedCharge struct {
    StayID, StayRoomID, ReservationRoomID int64
    ServiceDate    time.Time           // the night
    Source         string              // ROOM_NIGHT (MVP)
    SourceRefID    *int64
    ChargeCodeID   int64
    RatePlanID     int64
    PriceMode      chargecalc.PriceMode
    UnitPrice      decimal.Decimal     // the nightly agreed amount
    Status         string              // READY | ALREADY_POSTED | NOT_APPLICABLE | ERROR
    ReasonCode     string              // e.g. MISSING_NIGHTLY_RATE
    PostedItemID   *int64
    FolioID        *int64
}
```

### 8.2 Sources
A `ChargeSource` interface. The MVP has one source, **`ROOM_NIGHT`**. `PACKAGE` and `RECURRING` are added later without changing the engine's contract or the register.

### 8.3 ROOM_NIGHT eligibility (for each stay S in scope and each night N)
Candidate nights: `N ∈ [S.arrival_date, S.departure_date)` with `N ≤ UpToDate`.

The rules are evaluated **in order**, and the first match wins:

| # | Condition | Status | Reason |
|---|---|---|---|
| 1 | `S.status = CANCELLED`, or its line is `CANCELLED`/`NO_SHOW` | NOT_APPLICABLE | `STAY_NOT_ACTIVE` |
| 2 | A POSTED register row exists for (S, N, ROOM_NIGHT) | ALREADY_POSTED | none |
| 3 | `S.status = CHECKED_OUT` | NOT_APPLICABLE | `STAY_CLOSED` (check-out already guaranteed completeness) |
| 4 | No stay segment covers N (`start_bd ≤ N < COALESCE(end_bd, ∞)`) | ERROR | `NO_ROOM_FOR_NIGHT` |
| 5 | No `reservation_room_rates` row for N | ERROR | `MISSING_NIGHTLY_RATE` |
| 6 | The nightly row's charge code is inactive, or its `charge_type ≠ ROOM` | ERROR | `INVALID_CHARGE_CODE` |
| 7 | The target folio is missing: no OPEN GUEST folio (default routing), or a billing instruction sends the night to a company folio that is closed (`folios.ResolveTarget`) | ERROR | `NO_OPEN_FOLIO`, or `ROUTING_TARGET_CLOSED` for a routed night (never a fallback to the guest folio) |
| 8 | otherwise | **READY** | none |

The applicable **room** is the segment from rule 4 (the room-move rule, §38). The **rate, rate plan, charge code and price mode** all come from the **nightly snapshot row**, not from the current rate plan (§27).

Nights with `N ≥ S.departure_date` or `N < S.arrival_date` are never candidates. That's how "do not charge outside the eligible stay period" holds.

### 8.4 Invalid posted charges (checked separately)
`FindInvalid(scope)` lists POSTED register rows where:
- N is outside `[S.arrival_date, S.departure_date)` (the stay was shortened after posting), or
- the stay is CANCELLED.

Night audit treats these as blockers, and staff reverse them.

### 8.5 Missing vs. tonight
- `READY` with `N < BD` means **missing** (an earlier night that was never posted). This should be rare and signals a gap.
- `READY` with `N = BD` means **due tonight**. It's posted by night audit or in advance.

Both are posted by the same service. The labels are only for display.

---

## Step 9: RoomChargePostingService

### 9.1 Contract
```go
type PostRoomChargesCmd struct {
    PropertyID   int64
    BusinessDate time.Time        // must equal the OPEN BD (guards against stale screens)
    StayIDs      []int64          // empty = all eligible stays
    Trigger      string           // NIGHT_AUDIT | MANUAL | CHECK_OUT | RECOVERY
    DryRun       bool             // true = preview: evaluate + calculate, write nothing
    ActorUserID  *int64
}

type RoomChargeResult struct {
    StayID, StayRoomID int64; StayNumber, RoomNumber, GuestName string
    FolioID *int64; ServiceDate time.Time
    ChargeCode string; RoomRate decimal.Decimal; PriceMode string
    Breakdown *chargecalc.Breakdown          // service, tax, total
    Status  string                            // READY | POSTED | ALREADY_POSTED | NOT_APPLICABLE | ERROR
    Reason  string; FolioItemID *int64
}

func (s *RoomChargePostingService) Post(ctx, PostRoomChargesCmd) ([]RoomChargeResult, error)
```

### 9.2 Algorithm (inside the caller's transaction)
1. `BusinessDayService.RequireOpen(ctx, property, cmd.BusinessDate)`, which locks the OPEN day `FOR SHARE` (`FOR UPDATE` if the caller is night audit, which already holds it).
2. Resolve the scope's stay IDs and **lock the `stays` rows `FOR UPDATE` ordered by id**. This serialises concurrent posting for the same stay (for example, night audit and a manual "post missing" click).
3. `ExpectedChargeEngine.Evaluate` with `UpToDate = BD`, run **after** the locks, so it re-checks the current state.
4. For each `READY` item, in order of `(stay_id, service_date)`:
   1. `FolioPostingService.PostCharge(ctx, {folio, charge_code (from the nightly row), qty 1, unit_price = nightly amount, price_mode = nightly price_mode, discount 0, service_date = N, stay_id, stay_room_id, source = ROOM_POSTING, description "Room 305 – 30 Sep 2026"})`. This returns the item. The breakdown comes from the engine.
   2. Insert into `stay_charge_postings` (stay, stay_room, N, ROOM_NIGHT, charge_code, folio_item, BD, trigger, POSTED). 🛡 The partial unique index is the final guard. A violation (which can't happen under the stay lock) aborts the transaction, so there are never duplicates.
   3. Record the result as `POSTED`.
5. `ERROR` items are returned as results and not posted. The caller decides whether an error is fatal (night audit: yes; check-out: yes for that stay).
6. One audit log entry summarises the run (the counts and the trigger).

With `DryRun`, steps 1–3 run with plain reads (no row locks), and step 4 calls `ChargeCalculationService` only. This is the **preview**, and it writes nothing.

### 9.3 Idempotency
- A retry after a commit finds the register rows, so the items come back as `ALREADY_POSTED`.
- A concurrent run blocks on the stay locks, then re-evaluates and finds the items `ALREADY_POSTED`.
- The DB guarantee is the partial unique index on `(stay_id, service_date, charge_source, COALESCE(source_ref_id, 0)) WHERE status = 'POSTED'`.
- After a legitimate reversal, the register row becomes `REVERSED`, so the night is READY again (by design).

### 9.4 Reuse
- Night audit (`NIGHT_AUDIT`)
- "Post room charges / post missing charges" (`MANUAL`)
- Check-out for the consumed night after midnight (`CHECK_OUT`)
- Admin recovery (`RECOVERY`)

All four call the same method.

---

## Step 10: FolioPostingService

### 10.1 Operations
| Method | Creates | Debit/credit |
|---|---|---|
| `PostCharge(cmd)` | CHARGE item + components | debit = total (≥ 0), credit = 0 |
| `PostAdjustment(cmd)` | ADJUSTMENT item + components (engine on a signed base) | total < 0 → credit = −total; total > 0 → debit |
| `PostPaymentEntry(payment)` | PAYMENT item, no components | credit = amount |
| `PostRefundEntry(payment)` | REFUND item | debit = amount |
| `Reverse(itemID, reason)` | REVERSAL item + negated components | debit and credit swapped; signed columns negated |

### 10.2 Validations (every operation)
1. The OPEN business day is locked `FOR SHARE`. `business_date = BD`. `service_date ≤ BD`.
2. The folio is locked `FOR UPDATE`, belongs to the property, and has `status = OPEN`.
3. For charges and adjustments: the charge code is active and in the same property. **`PostCharge` rejects `charge_type = ROOM` unless the caller is `RoomChargePostingService`**, enforced by an unexported capability token, so there is one room-posting path.
4. `PostCharge` and `PostAdjustment` get their breakdown **only** from `ChargeCalculationService`. The caller never supplies amounts, except for future `INTEGRATION` sources, which go through `Verify`.
5. **Reverse:** the original's `business_date = BD` (same-day correction). It isn't already reversed (🛡 UK). It isn't a PAYMENT or REFUND item (those go through `PaymentService.Void`). If the original has a register row, that row is set to `REVERSED` in the same transaction.
6. Idempotency: if `idempotency_key` already exists for the property, return the original item and write nothing.
7. **Approval (adjustment, reversal and payment void/refund entries):** the command carries a verified `ApprovedBy` user id (see [06-api.md §14.1](06-api.md)). Without it the operation fails with `APPROVAL_REQUIRED`. `FolioPostingService` and `PaymentService` never verify passwords themselves: an `approval.Verifier` (in `iam`) does that before the use case calls them, and its result is an unexported-constructor value so callers cannot fabricate one.
8. Write the item, its components (with snapshots of code, name, rate, `tax_on_service`, base, amount and sequence) and an audit log entry. The deferred trigger checks the totals at commit.

### 10.3 Balance
`balance = SUM(debit) − SUM(credit)` over the folio's items. It's never stored. `GetBalance(folioID)` is the only accessor.

---

## Step 11: PaymentService

| Operation | Rules | Writes (one transaction) |
|---|---|---|
| **Post** | The folio is OPEN. `amount > 0`. The method is valid. The currency is implicitly the property's. The `Idempotency-Key` is new (otherwise the original is returned). | Lock the BD `FOR SHARE` and the folio `FOR UPDATE`. Allocate the `payment_number` from the sequence. Insert the `payments` row (POSTED, `paid_at = now()`, `business_date = BD`). Call `FolioPostingService.PostPaymentEntry`. Write the audit log. |
| **Deposit** | The reservation is DRAFT or CONFIRMED. | Find or create the reservation's OPEN unlinked folio (🛡 UK), then **Post**. |
| **Void** | POSTED, `business_date = BD`, the folio is OPEN, no refunds exist, and a reason is given. | Status → VOIDED (`voided_*`). `FolioPostingService.Reverse(payment item)`, which is internally allowed for this path. Write the audit log. |
| **Refund** | The original is POSTED. `amount ≤ original − Σ refunds` (checked under the original payment's `FOR UPDATE` lock). A reason is given. The folio is OPEN. | Insert a REFUND payment (`refund_of_payment_id`), call `FolioPostingService.PostRefundEntry`, and write the audit log. |

Duplicate protection:
- 🛡 `UNIQUE (property_id, idempotency_key)` on `payments`
- 🛡 `UNIQUE (payment_id)` on `folio_items`
- The client sends an `Idempotency-Key` on every call
