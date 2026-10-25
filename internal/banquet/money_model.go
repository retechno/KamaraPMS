package banquet

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// The pure rules of the money of an event (docs/architecture/36-banquet.md sections 4.5, 4.7, 5.2, 5.4). Nothing here reads the database or the clock.

// The posting modes, the price modes, the bases and the actions of a tier.
const (
	PostingAuto   = "AUTO"
	PostingManual = "MANUAL"

	PriceExclusive = "EXCLUSIVE"
	PriceInclusive = "INCLUSIVE"

	BaseNet   = "NET"
	BaseGross = "GROSS"

	ActionFee     = "FEE"
	ActionForfeit = "FORFEIT"

	BasisActual     = "ACTUAL"
	BasisGuaranteed = "GUARANTEED"
	BasisEstimated  = "ESTIMATED"

	NoticeDepositDue = "DEPOSIT_DUE"
)

// The charge codes of the banquet (the migration 00090 seeds them for every property).
const (
	CodeFB        = "BQT_FB"
	CodeRoom      = "BQT_ROOM"
	CodeEquipment = "BQT_EQUIPMENT"
	CodeDecor     = "BQT_DECOR"
	CodeOther     = "BQT_OTHER"
	CodeForfeit   = "BQT_FORFEIT"
	CodeCancelFee = "CANCEL_FEE"
)

// SplitPackage shares total over the components in proportion to their weights (5.2): each share is rounded half away from zero at the decimals of the currency, and the difference goes to the
// largest share (the first one on a tie), so the shares always add up to the total. When every weight is zero the shares are equal.
func SplitPackage(total decimal.Decimal, weights []decimal.Decimal, decimals int32) []decimal.Decimal {
	out := make([]decimal.Decimal, len(weights))
	if len(weights) == 0 {
		return out
	}
	sum := decimal.Zero
	for _, w := range weights {
		sum = sum.Add(w)
	}
	w := weights
	if !sum.IsPositive() {
		w = make([]decimal.Decimal, len(weights))
		for i := range w {
			w[i] = decimal.NewFromInt(1)
		}
		sum = decimal.NewFromInt(int64(len(w)))
	}
	paid := decimal.Zero
	largest := 0
	for i := range w {
		out[i] = total.Mul(w[i]).Div(sum).Round(decimals)
		paid = paid.Add(out[i])
		if out[i].GreaterThan(out[largest]) {
			largest = i
		}
	}
	out[largest] = out[largest].Add(total.Sub(paid))
	return out
}

// BilledPax is the number of guests a session is billed on (4.5): the larger of the guaranteed and the actual number. ok is false when neither was given. onGuarantee is true when the actual
// number was not entered (the posting is flagged); basis says which number decided.
func BilledPax(guaranteed, actual *int) (pax int, basis string, onGuarantee, ok bool) {
	switch {
	case guaranteed == nil && actual == nil:
		return 0, "", false, false
	case actual == nil:
		return *guaranteed, BasisGuaranteed, true, true
	case guaranteed == nil || *actual >= *guaranteed:
		return *actual, BasisActual, false, true
	default:
		return *guaranteed, BasisGuaranteed, false, true
	}
}

// PlanComponent is a component of a package line as it was frozen when the line was added.
type PlanComponent struct {
	ItemID       int64
	Name         string
	ChargeCodeID int64
	QtyPerPax    decimal.Decimal
	ListPrice    decimal.Decimal
}

// PlanLine is a line of a session as the plan reads it.
type PlanLine struct {
	ID             int64
	Name           string // the item or the package
	IsPackage      bool
	Basis          string
	Quantity       decimal.Decimal
	UnitPrice      decimal.Decimal
	Mode           string
	DiscountPct    *decimal.Decimal
	DiscountAmount *decimal.Decimal
	ChargeCodeID   int64 // an item line
	Components     []PlanComponent
}

// PlannedCharge is one charge a session makes: the single source of what is posted and of what is estimated, so the two can never differ.
type PlannedCharge struct {
	LineID       int64
	ComponentID  int64 // the item of the component; 0 for an item line
	ChargeCodeID int64
	Quantity     decimal.Decimal
	UnitPrice    decimal.Decimal
	Discount     decimal.Decimal
	Mode         string
	Description  string // what follows "event · session · "
}

// Key is the idempotency key of the charge in a session.
func (c PlannedCharge) Key(sessionID int64) string {
	return fmt.Sprintf("banquet:session:%d:%d:%d", sessionID, c.LineID, c.ComponentID)
}

// DiscountOf is the amount a line discounts of a base: the percent of it, or the amount, never more than the base.
func DiscountOf(base decimal.Decimal, pct, amount *decimal.Decimal, decimals int32) decimal.Decimal {
	switch {
	case pct != nil:
		return base.Mul(*pct).Div(decimal.NewFromInt(100)).Round(decimals)
	case amount != nil:
		if amount.GreaterThan(base) {
			return base
		}
		return amount.Round(decimals)
	}
	return decimal.Zero
}

// PlanSession turns the lines of a session into the charges to post for pax billed guests (4.5, 5.2). A line per guest has the quantity pax x its multiplier; the other bases have their own count.
// A package line is a total of price x guests split over its components, one charge for each (quantity 1, the share as the price, the discount split by the shares). A charge that comes to nothing
// (no guests, a free item) is not posted.
func PlanSession(lines []PlanLine, pax int, decimals int32) []PlannedCharge {
	var out []PlannedCharge
	for _, l := range lines {
		if l.IsPackage {
			out = append(out, planPackage(l, pax, decimals)...)
			continue
		}
		qty := l.Quantity
		if l.Basis == "PER_PAX" {
			qty = qty.Mul(decimal.NewFromInt(int64(pax)))
		}
		base := qty.Mul(l.UnitPrice).Round(decimals)
		if !qty.IsPositive() || !base.IsPositive() {
			continue
		}
		out = append(out, PlannedCharge{LineID: l.ID, ChargeCodeID: l.ChargeCodeID, Quantity: qty, UnitPrice: l.UnitPrice, Mode: l.Mode,
			Discount: DiscountOf(base, l.DiscountPct, l.DiscountAmount, decimals), Description: l.Name})
	}
	return out
}

func planPackage(l PlanLine, pax int, decimals int32) []PlannedCharge {
	if len(l.Components) == 0 || pax < 1 {
		return nil
	}
	total := l.UnitPrice.Mul(decimal.NewFromInt(int64(pax))).Round(decimals)
	if !total.IsPositive() {
		return nil
	}
	weights := make([]decimal.Decimal, len(l.Components))
	for i, c := range l.Components {
		weights[i] = c.ListPrice.Mul(c.QtyPerPax)
	}
	shares := SplitPackage(total, weights, decimals)
	discounts := SplitPackage(DiscountOf(total, l.DiscountPct, l.DiscountAmount, decimals), shares, decimals)
	var out []PlannedCharge
	for i, c := range l.Components {
		if !shares[i].IsPositive() {
			continue
		}
		d := discounts[i]
		if d.GreaterThan(shares[i]) {
			d = shares[i]
		}
		out = append(out, PlannedCharge{LineID: l.ID, ComponentID: c.ItemID, ChargeCodeID: c.ChargeCodeID, Quantity: decimal.NewFromInt(1), UnitPrice: shares[i], Mode: l.Mode, Discount: d,
			Description: fmt.Sprintf("%s (%d pax) · %s", l.Name, pax, c.Name)})
	}
	return out
}

// Tier is a step of a cancellation policy: from days_from days before the first session up to days_to (no end: and more) the percent of the base is due, as a fee or as a forfeit.
type Tier struct {
	ID       int64           `json:"id"`
	DaysFrom int             `json:"days_from"`
	DaysTo   *int            `json:"days_to"`
	Percent  decimal.Decimal `json:"percent"`
	Action   string          `json:"action"`
}

// DaysBefore is the days from the business date to the first session, never below zero (a cancellation on the day or after it falls in the last tier).
func DaysBefore(businessDate, first civil.Date) int {
	if first.IsZero() || !businessDate.Before(first) {
		return 0
	}
	return businessDate.DaysUntil(first)
}

// TierFor finds the tier in force for the days before the first session, or nil when none covers them (nothing is due).
func TierFor(tiers []Tier, daysBefore int) *Tier {
	for i := range tiers {
		t := tiers[i]
		if daysBefore >= t.DaysFrom && (t.DaysTo == nil || daysBefore <= *t.DaysTo) {
			return &t
		}
	}
	return nil
}

// CheckTiers refuses a policy whose ranges overlap. The answer is the index of the second of two tiers that share a day, or -1.
func CheckTiers(tiers []Tier) int {
	s := append([]Tier(nil), tiers...)
	sort.Slice(s, func(i, j int) bool { return s[i].DaysFrom < s[j].DaysFrom })
	for i := 1; i < len(s); i++ {
		if s[i-1].DaysTo == nil || *s[i-1].DaysTo >= s[i].DaysFrom {
			for k := range tiers {
				if tiers[k].DaysFrom == s[i].DaysFrom {
					return k
				}
			}
		}
	}
	return -1
}

// FeeOf is the amount a tier asks of a base.
func FeeOf(base, percent decimal.Decimal, decimals int32) decimal.Decimal {
	return base.Mul(percent).Div(decimal.NewFromInt(100)).Round(decimals)
}

// DepositRow is a row of a deposit schedule: a percent of the contract total, or a fixed amount.
type DepositRow struct {
	Seq     int              `json:"seq"`
	Label   string           `json:"label"`
	DueDate civil.Date       `json:"due_date"`
	Percent *decimal.Decimal `json:"percent"`
	Amount  decimal.Decimal  `json:"amount"`
}

// ScheduleAmounts works the amounts of the rows that have a percent out of the base. When the percents add up to 100 the last of them takes the rounding difference, so the schedule adds up to the
// base exactly; a row with no percent keeps its amount.
func ScheduleAmounts(rows []DepositRow, base decimal.Decimal, decimals int32) []DepositRow {
	out := append([]DepositRow(nil), rows...)
	sum, last, paid := decimal.Zero, -1, decimal.Zero
	for i := range out {
		if out[i].Percent == nil {
			continue
		}
		sum = sum.Add(*out[i].Percent)
		last = i
		out[i].Amount = FeeOf(base, *out[i].Percent, decimals)
		paid = paid.Add(out[i].Amount)
	}
	if last >= 0 && sum.Equal(decimal.NewFromInt(100)) {
		out[last].Amount = out[last].Amount.Add(base.Sub(paid))
	}
	return out
}

// RowStatus is where a row of the schedule stands against the deposits received: PAID when the cumulative amount due up to it has been received, else OVERDUE when its date has passed, DUE_SOON
// within seven days, else PENDING. A row is never linked to a payment (4.7).
func RowStatus(rows []DepositRow, i int, received decimal.Decimal, today civil.Date) string {
	cum := decimal.Zero
	for k := 0; k <= i; k++ {
		cum = cum.Add(rows[k].Amount)
	}
	switch {
	case received.GreaterThanOrEqual(cum):
		return "PAID"
	case !today.IsZero() && rows[i].DueDate.Before(today):
		return "OVERDUE"
	case !today.IsZero() && today.DaysUntil(rows[i].DueDate) <= 7:
		return "DUE_SOON"
	}
	return "PENDING"
}
