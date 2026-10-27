package assets

import (
	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// The monthly amount of the depreciation (docs/architecture/37-fixed-assets.md 5.1). The functions are pure: the preview, the run and the schedule of an asset use them, and they are tested against a
// brute-force simulation. The state is recomputed every month from the cost, the residual value, the accumulated depreciation, the life and the months done, so the last month absorbs every rounding
// difference, and a change of the inputs is simply a change of the next amount.

// DeprState is what the next amount is computed from.
type DeprState struct {
	Cost       decimal.Decimal
	Residual   decimal.Decimal
	Accum      decimal.Decimal
	LifeMonths int
	MonthsDone int
	Method     string          // MethodSL or MethodDB
	Factor     decimal.Decimal // the factor of a declining balance (2 = double declining), ignored for the straight line
	Decimals   int32           // the decimals of the currency: every amount is rounded half away from zero to them
}

// Carrying is the carrying value: the cost less the accumulated depreciation.
func (s DeprState) Carrying() decimal.Decimal { return s.Cost.Sub(s.Accum) }

// Left is the number of months of life that are left.
func (s DeprState) Left() int { return s.LifeMonths - s.MonthsDone }

// base is what can still be depreciated: the carrying value less the residual value.
func (s DeprState) base() decimal.Decimal { return s.Carrying().Sub(s.Residual) }

// Due says whether the asset has anything left to depreciate: months of life and a carrying value above the residual value.
func (s DeprState) Due() bool { return s.Left() > 0 && s.base().IsPositive() }

// NextAmount is the depreciation of the next month (5.1), 0 when the asset is not due.
//
//	straight line:     m = round((V - R) / r), and V - R in the last month
//	declining balance: m = max(round(V x f / N), round((V - R) / r)): the declining amount while it is larger, then the straight line over what is left
//	both:              m = min(m, V - R)
func NextAmount(s DeprState) decimal.Decimal {
	if !s.Due() {
		return decimal.Zero
	}
	base := s.base()
	r := s.Left()
	if r == 1 {
		return base
	}
	m := base.Div(decimal.NewFromInt(int64(r))).Round(s.Decimals)
	if s.Method == MethodDB && s.LifeMonths > 0 {
		db := s.Carrying().Mul(s.Factor).Div(decimal.NewFromInt(int64(s.LifeMonths))).Round(s.Decimals)
		if db.GreaterThan(m) {
			m = db
		}
	}
	if m.GreaterThan(base) {
		m = base
	}
	return m
}

// Step depreciates one month: the amount and the state after it.
func Step(s DeprState) (decimal.Decimal, DeprState) {
	m := NextAmount(s)
	s.Accum = s.Accum.Add(m)
	s.MonthsDone++
	return m, s
}

// Advance depreciates up to n months in turn (the catch-up of an asset that is due for several months: 10.9) and answers the total, the state after it and how many months it applied: fewer than n
// when the life or the carrying value runs out first.
func Advance(s DeprState, n int) (total decimal.Decimal, after DeprState, months int) {
	total = decimal.Zero
	for ; months < n && s.Due(); months++ {
		var m decimal.Decimal
		m, s = Step(s)
		total = total.Add(m)
	}
	return total, s, months
}

// ScheduleMonth is one month of a schedule.
type ScheduleMonth struct {
	Month      civil.Date
	Amount     decimal.Decimal
	Accum      decimal.Decimal // the accumulated depreciation after the month
	Carrying   decimal.Decimal // the carrying value after the month
	MonthsDone int
}

// Project lists the months from the state to the end of the life, the first of them being `from` (a book month): the projected part of the schedule of an asset.
func Project(s DeprState, from civil.Date) []ScheduleMonth {
	out := []ScheduleMonth{}
	for k := 0; s.Due() && k < maxLifeMonths; k++ {
		var m decimal.Decimal
		m, s = Step(s)
		out = append(out, ScheduleMonth{Month: AddMonths(from, k), Amount: m, Accum: s.Accum, Carrying: s.Carrying(), MonthsDone: s.MonthsDone})
	}
	return out
}

// firstDue is the first month an asset is depreciated in a run: the month after the last one it was depreciated in, else its first month, and never before the register starts (the register does not
// depreciate what the books of the old system closed).
func firstDue(monthsDone int, lastDep, firstDep *civil.Date, registerFrom civil.Date) civil.Date {
	var from civil.Date
	switch {
	case monthsDone > 0 && lastDep != nil:
		from = AddMonths(*lastDep, 1)
	case firstDep != nil:
		from = *firstDep
	default:
		from = registerFrom
	}
	if from.Before(registerFrom) {
		from = registerFrom
	}
	return from
}
