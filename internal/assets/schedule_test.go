package assets_test

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets"
)

// ratOf is the exact value of a decimal.
func ratOf(d decimal.Decimal) *big.Rat {
	r, ok := new(big.Rat).SetString(d.String())
	if !ok {
		panic("not a number: " + d.String())
	}
	return r
}

// roundRat rounds half away from zero to `dec` decimals, with integers only.
func roundRat(x *big.Rat, dec int) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)
	n := new(big.Int).Mul(x.Num(), scale)
	den := x.Denom()
	neg := n.Sign() < 0
	n.Abs(n)
	// floor((2n + den) / (2 den)) = round half up of n/den
	q := new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(n, big.NewInt(2)), den), new(big.Int).Mul(den, big.NewInt(2)))
	if neg {
		q.Neg(q)
	}
	return new(big.Rat).SetFrac(q, scale)
}

// bruteNext is the amount of 5.1 written once more with exact fractions, apart from the code under test.
func bruteNext(cost, resid, accum *big.Rat, life, done int, method string, factor *big.Rat, dec int) *big.Rat {
	v := new(big.Rat).Sub(cost, accum)
	base := new(big.Rat).Sub(v, resid)
	r := life - done
	if r <= 0 || base.Sign() <= 0 {
		return new(big.Rat)
	}
	if r == 1 {
		return base
	}
	m := roundRat(new(big.Rat).Quo(base, big.NewRat(int64(r), 1)), dec)
	if method == assets.MethodDB {
		db := roundRat(new(big.Rat).Quo(new(big.Rat).Mul(v, factor), big.NewRat(int64(life), 1)), dec)
		if db.Cmp(m) > 0 {
			m = db
		}
	}
	if m.Cmp(base) > 0 {
		m = base
	}
	return m
}

func state(cost, resid string, life int, method, factor string, dec int32) assets.DeprState {
	return assets.DeprState{Cost: dec_(cost), Residual: dec_(resid), Accum: decimal.Zero, LifeMonths: life, Method: method, Factor: dec_(factor), Decimals: dec}
}

func dec_(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// 10.3: 10,000,000 over 36 months. Month 1 is 277,778 and month 2 is (10,000,000 - 277,778) / 35 = 277,777.77, so 277,778 as the document says. The rule of 5.1 recomputes the state every month, so
// the amount drifts down by one rupiah now and then (the text of 10.3, "277,778 for 35 months then 277,770", is an approximation: As built, PR 2, records the real series). What holds for
// certain: every month is 277,777 or 277,778, the last month takes what is left, and the total is exactly the cost.
func TestExample103TheLastMonthAbsorbsTheRounding(t *testing.T) {
	s := state("10000000", "0", 36, assets.MethodSL, "2", 0)
	sum := decimal.Zero
	counts := map[string]int{}
	for k := 1; k <= 36; k++ {
		var m decimal.Decimal
		m, s = assets.Step(s)
		sum = sum.Add(m)
		counts[m.String()]++
		if k <= 21 && m.String() != "277778" {
			t.Fatalf("month %d: %s, want 277778", k, m)
		}
		if k == 36 && !m.Equal(dec_("10000000").Sub(sum.Sub(m))) {
			t.Fatalf("the last month is %s: it must take what is left", m)
		}
	}
	if len(counts) != 2 || counts["277778"] != 28 || counts["277777"] != 8 {
		t.Fatalf("the months: %v", counts)
	}
	if !sum.Equal(dec_("10000000")) || !s.Carrying().IsZero() || s.Due() {
		t.Fatalf("total %s carrying %s", sum, s.Carrying())
	}
}

// 10.4: 24,000,000 over 48 months, factor 2: 1,000,000 then 958,333, and the straight line over what is left from month 26.
func TestExample104DecliningBalanceSwitchesToStraightLine(t *testing.T) {
	s := state("24000000", "0", 48, assets.MethodDB, "2", 0)
	var amounts []decimal.Decimal
	for s.Due() {
		var m decimal.Decimal
		m, s = assets.Step(s)
		amounts = append(amounts, m)
	}
	if len(amounts) != 48 || amounts[0].String() != "1000000" || amounts[1].String() != "958333" {
		t.Fatalf("%d months, first %v", len(amounts), amounts[:2])
	}
	// the switch: while the declining amount is the larger it is V/24; from month 26 (r = 23) the straight line V/r is
	prev := state("24000000", "0", 48, assets.MethodDB, "2", 0)
	for k := 1; k <= 48; k++ {
		sl := prev.Carrying().Div(decimal.NewFromInt(int64(prev.Left()))).Round(0)
		db := prev.Carrying().Mul(dec_("2")).Div(decimal.NewFromInt(48)).Round(0)
		m, next := assets.Step(prev)
		if k <= 25 && prev.Left() > 1 && !m.Equal(db) {
			t.Fatalf("month %d: %s, the declining amount is %s", k, m, db)
		}
		if k >= 26 && prev.Left() > 1 && !m.Equal(sl) {
			t.Fatalf("month %d: %s, the straight line is %s", k, m, sl)
		}
		prev = next
	}
	if !prev.Carrying().IsZero() {
		t.Fatalf("carrying value at the end: %s", prev.Carrying())
	}
}

// 10.9: four months of 300,000 caught up in one go.
func TestExample109CatchUpIsTheSameAsMonthByMonth(t *testing.T) {
	s := state("10800000", "0", 36, assets.MethodSL, "2", 0)
	total, after, n := assets.Advance(s, 4)
	if n != 4 || !total.Equal(dec_("1200000")) || !after.Accum.Equal(dec_("1200000")) || after.MonthsDone != 4 {
		t.Fatalf("%s %d %+v", total, n, after)
	}
}

func TestResidualNeverCrossed(t *testing.T) {
	s := state("240000000", "24000000", 96, assets.MethodSL, "2", 0)
	m, _ := assets.Step(s)
	if m.String() != "2250000" { // (240,000,000 - 24,000,000) / 96, 10.2
		t.Fatalf("%s", m)
	}
	total, after, _ := assets.Advance(s, 1000)
	if !total.Equal(dec_("216000000")) || !after.Carrying().Equal(dec_("24000000")) || after.MonthsDone != 96 {
		t.Fatalf("%s %+v", total, after)
	}
	// nothing is left to depreciate at the residual value, whatever the months left
	full := s
	full.Accum = dec_("216000000")
	if full.Due() || !assets.NextAmount(full).IsZero() {
		t.Fatal("an asset at its residual value is not due")
	}
}

// The simulation: for thousands of random assets, the code and an exact-fraction implementation agree month by month, and the invariants of the depreciation hold.
func TestScheduleAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(37))
	factors := []string{"1", "1.5", "2", "2.5", "4"}
	for i := 0; i < 4000; i++ {
		dec := int32(rng.Intn(4)) // 0 to 3 decimals
		unit := decimal.New(1, -dec)
		cost := unit.Mul(decimal.NewFromInt(1 + rng.Int63n(2_000_000_000)))
		resid := decimal.Zero
		if rng.Intn(3) == 0 {
			resid = cost.Mul(decimal.NewFromInt(rng.Int63n(40))).Div(decimal.NewFromInt(100)).Round(dec)
		}
		life := 1 + rng.Intn(240)
		method := assets.MethodSL
		if rng.Intn(2) == 0 {
			method = assets.MethodDB
		}
		f := factors[rng.Intn(len(factors))]
		s := assets.DeprState{Cost: cost, Residual: resid, Accum: decimal.Zero, LifeMonths: life, Method: method, Factor: dec_(f), Decimals: dec}
		// start somewhere in the life (an opening balance) now and then
		if rng.Intn(3) == 0 && life > 2 {
			_, s, _ = assets.Advance(s, rng.Intn(life))
		}
		startBase := s.Carrying().Sub(s.Residual)
		sum := decimal.Zero
		cr, rr, ar, fr := ratOf(s.Cost), ratOf(s.Residual), ratOf(s.Accum), ratOf(s.Factor)
		proj := assets.Project(s, d("2026-10-01"))
		k := 0
		for s.Due() {
			want := bruteNext(cr, rr, ar, s.LifeMonths, s.MonthsDone, s.Method, fr, int(dec))
			var m decimal.Decimal
			m, s = assets.Step(s)
			if ratOf(m).Cmp(want) != 0 {
				t.Fatalf("case %d month %d: got %s, want %s (cost %s resid %s life %d %s f=%s dec=%d)", i, k+1, m, want.FloatString(int(dec)), cost, resid, life, method, f, dec)
			}
			if m.IsNegative() || s.Carrying().LessThan(resid) {
				t.Fatalf("case %d month %d: amount %s, carrying %s below the residual %s", i, k+1, m, s.Carrying(), resid)
			}
			ar = ratOf(s.Accum)
			sum = sum.Add(m)
			if k >= len(proj) || !proj[k].Amount.Equal(m) || !proj[k].Accum.Equal(s.Accum) {
				t.Fatalf("case %d: the projection differs at month %d", i, k+1)
			}
			k++
		}
		if k != len(proj) {
			t.Fatalf("case %d: projection of %d months, simulation of %d", i, len(proj), k)
		}
		if !sum.Equal(startBase) || !s.Carrying().Equal(resid) {
			t.Fatalf("case %d: total %s of %s, carrying %s, residual %s", i, sum, startBase, s.Carrying(), resid)
		}
		if s.MonthsDone > life {
			t.Fatalf("case %d: %d months of %d", i, s.MonthsDone, life)
		}
	}
}

// A catch-up of n months is n single months.
func TestAdvanceEqualsSteps(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 500; i++ {
		s := assets.DeprState{Cost: decimal.NewFromInt(1 + rng.Int63n(900_000_000)), Residual: decimal.Zero, LifeMonths: 1 + rng.Intn(120), Method: assets.MethodDB, Factor: dec_("2")}
		if i%2 == 0 {
			s.Method = assets.MethodSL
		}
		n := rng.Intn(150)
		total, after, months := assets.Advance(s, n)
		sum, cur := decimal.Zero, s
		applied := 0
		for ; applied < n && cur.Due(); applied++ {
			var m decimal.Decimal
			m, cur = assets.Step(cur)
			sum = sum.Add(m)
		}
		if !total.Equal(sum) || months != applied || !after.Accum.Equal(cur.Accum) || after.MonthsDone != cur.MonthsDone {
			t.Fatalf("case %d: %s/%d against %s/%d", i, total, months, sum, applied)
		}
	}
}
