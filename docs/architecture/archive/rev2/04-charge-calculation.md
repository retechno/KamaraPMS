# Charge Calculation Engine

> Package `internal/chargecalc`. A **pure** function: no database, no clock, no knowledge of countries or charge types.
> It's the only place in the codebase where service charge and tax are calculated.

## 1. Where the rules come from

```
            ┌──────────────┐     charge_code_id     ┌───────────────────────────┐
 caller ───▶│  billing.    │ ─────────────────────▶ │ charge_codes              │
 (reserv.,  │  ChargeRule  │                        │  ├─ charge_code_service_… │──▶ service_charges
 folio,     │  Resolver    │ ◀───── Rules ───────── │  └─ charge_code_taxes     │──▶ taxes
 night      └──────┬───────┘                        └───────────────────────────┘
 audit,            │ Rules{ServiceCharges[], Taxes[]}
 POS…)             ▼
            ┌──────────────┐
            │ chargecalc.  │  pure math → Breakdown
            │ Calculate()  │
            └──────────────┘
```

- **Charge type** (ROOM, BREAKFAST, LAUNDRY, and so on) is resolved into rules *before* the engine is called, so the engine never sees it.
- **Room charges** use `rate_plans.room_charge_code_id` and the price mode snapshotted on the nightly rate.
- **Manual charges** use the chosen charge code and its `default_price_mode`, unless the request overrides the mode.
- **Different treatment per rate plan** (for example a tax-exempt government rate) is done by pointing the plan at a different charge code. That keeps a single resolution path.
- **Future effective-dated rates** mean the resolver takes a `serviceDate` and picks the right rate period. The engine doesn't change.

## 2. Contract

```go
package chargecalc

type PriceMode string // "EXCLUSIVE" | "INCLUSIVE" (includes ALL mapped service charges and taxes)

type ServiceChargeRule struct {
    ID          int64
    Code        string
    RatePercent decimal.Decimal // 10 means 10%
}

type TaxRule struct {
    ID           int64
    Code         string
    RatePercent  decimal.Decimal
    OnService    bool // the taxable base includes the service charges
}

type Rounding struct {
    Decimals int32 // from properties.currency_decimals
    Mode     RoundingMode // default HalfAwayFromZero
}

type Input struct {
    Quantity       decimal.Decimal // > 0
    UnitPrice      decimal.Decimal // expressed in PriceMode terms (net if EXCLUSIVE, gross if INCLUSIVE)
    PriceMode      PriceMode
    Discount       decimal.Decimal // an amount in the same terms as UnitPrice; 0 ≤ Discount ≤ Base
    ServiceCharges []ServiceChargeRule
    Taxes          []TaxRule
    ExemptTaxIDs   []int64 // reserved for future exemptions; unused in the MVP
    Rounding       Rounding
}

type Component struct {
    Kind        string // "SERVICE_CHARGE" | "TAX"
    RuleID      int64
    Code        string
    RatePercent decimal.Decimal
    TaxableBase decimal.Decimal
    Amount      decimal.Decimal
}

type Breakdown struct {
    PriceMode     PriceMode
    BaseAmount    decimal.Decimal // Quantity × UnitPrice, rounded
    Discount      decimal.Decimal
    NetAmount     decimal.Decimal // revenue without service or tax
    ServiceTotal  decimal.Decimal
    TaxTotal      decimal.Decimal
    TotalAmount   decimal.Decimal // Net + ServiceTotal + TaxTotal
    Components    []Component
}

func Calculate(in Input) (Breakdown, error)
func Verify(in Input, claimed Breakdown, tolerance decimal.Decimal) error // for future POS integrations
```

The same function is used for the reservation estimate, manual folio charges, night-audit room charges, adjustments, and later for invoices and POS. **No other package multiplies by a tax rate.**

## 3. Algorithm

Let `s_i` be the service charge rates and `t_j` the tax rates, both as fractions (`RatePercent / 100`), and let `S = Σ s_i`. Let `round()` round to `Rounding.Decimals`.

### 3.1 EXCLUSIVE (the default)
```
base      = round(quantity × unit_price)
net       = base − discount
svc_i     = round(net × s_i)                          for each service charge
SVC       = Σ svc_i
taxable_j = net + (tax_j.on_service ? SVC : 0)
tax_j     = round(taxable_j × t_j)                    for each tax (no compounding)
total     = net + SVC + Σ tax_j
```

### 3.2 INCLUSIVE ("nett": the price includes every mapped component)
```
base   = round(quantity × unit_price)
gross  = base − discount                              # the discount comes off the gross amount
F      = 1 + S + Σ_j t_j × (1 + (on_service_j ? S : 0))
net₀   = round(gross / F)
compute svc_i, tax_j from net₀ exactly as in 3.1
residual = gross − (net₀ + SVC + Σ tax_j)
net    = net₀ + residual                               # so total == gross exactly
total  = gross
```
Components are computed from `net₀` and the rounding residual is absorbed into net revenue. The guest always pays exactly the quoted price.

### 3.3 Signs
- A negative `base` (used by ADJUSTMENT) is calculated the same way. Rounding is half *away from zero*, so the same amount negated gives exactly the negated components.
- A **reversal** never calls the engine. It copies and negates the original item and its component rows.

### 3.4 Validation (errors)
The engine rejects: a quantity ≤ 0; a discount < 0 or greater than |base|; a rate outside 0–100; two taxes or two service charges with the same ID; an unknown price mode; and decimals outside 0–3.

## 4. Worked examples (Decimals = 0)

Rules: `SERVICE` 10%, `VAT` 11%.

**A. Exclusive, `tax_on_service = true`**
| | |
|---|---:|
| Base (room rate) | 1,000,000 |
| Service 10% | 100,000 |
| Taxable base | 1,100,000 |
| VAT 11% | 121,000 |
| **Total** | **1,221,000** |

**B. Exclusive, `tax_on_service = false`**
| | |
|---|---:|
| Base | 1,000,000 |
| Service 10% | 100,000 |
| Taxable base | 1,000,000 |
| VAT 11% | 110,000 |
| **Total** | **1,210,000** |

**C. Inclusive, VAT only (no service mapped)**, price 1,110,000
`F = 1.11` → net 1,000,000 · VAT 110,000 · **Total 1,110,000**

**D. Inclusive, service + VAT on service**, price 1,221,000
`F = 1 + 0.10 + 0.11 × 1.10 = 1.221` → net 1,000,000 · service 100,000 · VAT 121,000 · **Total 1,221,000**

**E. Inclusive with a rounding residual**, price 1,000,000, VAT 11% only
`net₀ = round(1,000,000 / 1.11) = 900,901` · VAT `round(900,901 × 0.11) = 99,099` · sum 1,000,000 · residual 0 → the total is exactly 1,000,000.
(Whenever the rounded parts don't add up to the price, the difference goes into net.)

**F. Discount, exclusive:** base 1,000,000, discount 100,000 → net 900,000 · service 90,000 · VAT (on service) 108,900 · total 1,098,900.

**G. Charge code with no mapped rules** (for example a deposit-related fee that's exempt): net = total, and there are no components.

## 5. Persisting the result

| Breakdown field | Column |
|---|---|
| PriceMode | `folio_items.price_mode` |
| Quantity, UnitPrice | `folio_items.quantity`, `unit_price` |
| BaseAmount, Discount, NetAmount | `base_amount`, `discount_amount`, `net_amount` |
| ServiceTotal, TaxTotal, TotalAmount | `service_charge_amount`, `tax_amount`, `amount` |
| Components[] | One `folio_item_components` row each (kind, rule id, code snapshot, rate snapshot, taxable base, amount) |

🛡 CHECK `amount = net_amount + service_charge_amount + tax_amount`.
🛡 CHECK for price mode: `EXCLUSIVE → net_amount = base_amount − discount_amount`, and `INCLUSIVE → amount = base_amount − discount_amount`.
🛡 A deferred constraint trigger checks that `service_charge_amount` and `tax_amount` equal the sums of the item's components by kind, at commit time.

## 6. Test strategy
- Table-driven unit tests covering examples A–G, negative amounts, zero rates, multiple taxes and multiple service charges, decimals 0 and 2, and discount edge cases.
- **Property-based tests**, which must hold for all inputs: in INCLUSIVE mode `total == gross`; in EXCLUSIVE mode `net == base − discount`; `Calculate(−x)` equals the negation of `Calculate(x)`; and every component is ≥ 0 when the base is ≥ 0.
- No test needs a database.
